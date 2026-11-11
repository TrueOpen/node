package keeper

import (
	"bytes"
	"errors"
	"testing"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// ADR-0040 v7 keeper tests: positions and bond-guard params, the B1 epoch seal and
// x beacon ordering, the separate content-verdict track, its slash, its deadline
// at cleanup, the cleanup hold, the pending query, genesis and the upgrade step.

var (
	anchorTestManifest = bytes.Repeat([]byte{0x0e}, 32)
	anchorTestOpening  = bytes.Repeat([]byte{0x0c}, 32)
	anchorTestProof    = bytes.Repeat([]byte{0x0d}, 32)
)

// newContentFixture is the anchor fixture with the content track enabled (the
// default params keep it off until governance turns it on).
func newContentFixture(t *testing.T, bond uint64, tokenSize uint64) *anchorFixture {
	t.Helper()
	f := newAnchorFixture(t, bond, tokenSize)
	f.setParams(t, func(v *types.VerificationParamsV1) {
		v.AnchorContentVerdictWindowBlocks = types.RecommendedAnchorContentVerdictWindowBlocks
		v.AnchorOpenWindowBlocks = types.RecommendedAnchorContentOpenWindowBlocks
	})
	return f
}

func (f *anchorFixture) setParams(t *testing.T, mutate func(v *types.VerificationParamsV1)) {
	t.Helper()
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	mutate(&params.Verification)
	require.NoError(t, params.Validate())
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))
	f.params = params
}

func (f *anchorFixture) open(t *testing.T, state types.AnchorAuditState, height uint64) {
	t.Helper()
	_, err := f.server.SubmitAnchorOpening(f.at(int64(height)), f.openingMsg(state, f.worker))
	require.NoError(t, err)
}

func (f *anchorFixture) seal(height uint64, submitter string, ids ...[]byte) (*types.MsgSealAnchorEpochResponse, error) {
	if len(ids) == 0 {
		ids = [][]byte{f.taskID}
	}
	return f.server.SealAnchorEpoch(f.at(int64(height)), &types.MsgSealAnchorEpoch{
		EpochManifestHash: anchorTestManifest, TaskIds: ids, SubmitterAddress: submitter,
	})
}

func (f *anchorFixture) proofMsg(xHeight uint64) *types.MsgSubmitAnchorProof {
	return &types.MsgSubmitAnchorProof{TaskId: f.taskID, OpeningHash: anchorTestOpening, ProofHash: anchorTestProof,
		ProofSizeBytes: 5, ProofUri: "https://da.example/p", EpochManifestHash: anchorTestManifest,
		EpochManifestUri: "https://da.example/m", SubmitterAddress: f.worker, XBeaconHeight: xHeight}
}

// openSealProve runs the B1 path: open, seal, beacon, x-bound proof.
func (f *anchorFixture) openSealProve(t *testing.T) types.AnchorAuditState {
	t.Helper()
	state := f.sample(t)
	f.open(t, state, f.sampleAt+1)
	resp, err := f.seal(f.sampleAt+2, f.worker)
	require.NoError(t, err)
	f.beacons[int64(resp.XBeaconHeight)] = bytes.Repeat([]byte{0x3c}, 32)
	_, err = f.server.SubmitAnchorProof(f.at(int64(resp.XBeaconHeight)), f.proofMsg(resp.XBeaconHeight))
	require.NoError(t, err)
	got, err := f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	return got
}

// settle marks the task final with a settlement row, as settlement does.
func (f *anchorFixture) settle(t *testing.T, height uint64) {
	t.Helper()
	core, err := f.keeper.TaskCore.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	core.FinalityStatus = shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL
	core.XTaskFinalityHeight = &types.TaskCoreState_TaskFinalityHeight{TaskFinalityHeight: height}
	require.NoError(t, f.keeper.TaskCore.Set(f.ctx, f.taskKey, core))
	zero := shared.NewAmount(0)
	require.NoError(t, f.keeper.WriteTaskSettlement(f.ctx, f.taskKey, types.TaskSettlementState{
		TaskId: f.taskID, TaskFinalityHeight: height, SettlementHeight: height,
		WorkerGross: zero, WorkerMaintenance: zero, WorkerNet: zero, VerifierSlotGross: zero,
		MaintenanceFee: zero, RefundAmount: zero, OriginalReservedAmount: zero, GasReimbursedTotal: zero,
	}))
}

func (f *anchorFixture) contentVote(i int, verdict types.AnchorContentVerdictV1, x []byte, height uint64) (*types.MsgSubmitAnchorContentVoteResponse, error) {
	return f.server.SubmitAnchorContentVote(f.at(int64(height)), &types.MsgSubmitAnchorContentVote{
		TaskId: f.taskID, ProofHash: anchorTestProof, XBeacon: x, Verdict: verdict,
		Verdicts:                 []types.AnchorContentPositionVerdictV1{{Position: 1, Verdict: verdict, ZMicro: 9_200_000, Reason: "z"}},
		ValidatorOperatorAddress: f.valoper[i], SubmitterAddress: f.valAcct[i],
	})
}

func (f *anchorFixture) audit(t *testing.T) types.AnchorAuditState {
	t.Helper()
	state, err := f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	return state
}

// ---- params: positions per task, bond guard, content deadline ------------

func TestAnchorSampleUsesPositionsParamAndContentDeadline(t *testing.T) {
	f := newContentFixture(t, 0, 8+4*40) // 40 ids: 39 rows
	f.setParams(t, func(v *types.VerificationParamsV1) { v.AnchorPositionsPerTask = 7 })
	state := f.sample(t)
	require.Len(t, state.Positions, 7)
	for i := 1; i < len(state.Positions); i++ {
		require.Less(t, state.Positions[i-1], state.Positions[i])
	}
	require.Equal(t, state.VerdictDeadlineHeight+f.params.Verification.AnchorContentVerdictWindowBlocks, state.ContentVerdictDeadlineHeight)

	g := newAnchorFixture(t, 0, anchorGoodTokenSize)
	require.Zero(t, types.DefaultTaskParams().Verification.AnchorContentVerdictWindowBlocks, "the content track ships disabled")
	require.Zero(t, g.sample(t).ContentVerdictDeadlineHeight, "window zero: no content track")

	// Bodies recorded before v7 carry zero and sample the former two.
	require.Equal(t, 2, anchorPositionsPerTask(types.VerificationParamsV1{}))
	// The draw bound grows with the sample so a large sample is not truncated.
	require.Len(t, anchorSamplePositions(f.taskID, f.beacon, 1_000, 64), 64)
	require.Equal(t, anchorSamplePositions(f.taskID, f.beacon, 1_000, 2),
		anchorSamplePositions(f.taskID, f.beacon, 1_000, 2), "deterministic")
}

func TestAnchorMinBondMultipleForcesFullAudit(t *testing.T) {
	// R = 1000 (fixture order value). D = 30_000 < 40 R: always audited.
	f := newContentFixture(t, 30_000, anchorGoodTokenSize)
	f.setParams(t, func(v *types.VerificationParamsV1) { v.AnchorAuditMinRateBps = 0 })
	state := f.sample(t)
	require.Equal(t, uint32(types.BasisPointsMaximum), state.AuditRateBps)
	require.True(t, state.Selected)
	require.Contains(t, state.AuditBasis, "min bond multiple 40 (below true)")

	// The same bond with the guard off follows s = kR/(bD+kR) < 100%.
	g := newContentFixture(t, 30_000, anchorGoodTokenSize)
	g.setParams(t, func(v *types.VerificationParamsV1) {
		v.AnchorAuditMinRateBps = 0
		v.AnchorMinBondMultiple = 0
	})
	require.Less(t, g.sample(t).AuditRateBps, uint32(types.BasisPointsMaximum))

	require.True(t, anchorBondBelowMultiple(39_999, 1_000, 40))
	require.False(t, anchorBondBelowMultiple(40_000, 1_000, 40))
	require.False(t, anchorBondBelowMultiple(0, 1_000, 0), "zero disables the guard")
	require.False(t, anchorBondBelowMultiple(0, 0, 40), "nothing to gain")
}

// ---- B1 epoch seal and x ordering ----------------------------------------

func TestAnchorSealEpochFixesXHeightAfterOpenings(t *testing.T) {
	f := newContentFixture(t, 0, anchorGoodTokenSize)
	state := f.sample(t)
	_, err := f.seal(f.sampleAt+1, f.worker)
	require.ErrorContains(t, err, "has not been opened", "g and v_int must be on chain before x exists")

	f.open(t, state, f.sampleAt+1)
	builder := sdk.AccAddress(bytes.Repeat([]byte{0x5b}, 20)).String()
	_, err = f.seal(f.sampleAt+5, builder)
	require.ErrorContains(t, err, "winner Worker's current service")
	_, err = f.server.SealAnchorEpoch(f.at(int64(f.sampleAt+5)), &types.MsgSealAnchorEpoch{
		EpochManifestHash: anchorTestManifest, TaskIds: [][]byte{f.taskID, f.taskID}, SubmitterAddress: f.worker})
	require.ErrorContains(t, err, "strictly ascending")

	resp, err := f.seal(f.sampleAt+5, f.worker)
	require.NoError(t, err)
	// x = opening-set freeze (seal) height + 1, above the opening at sampleAt+1.
	require.Equal(t, f.sampleAt+6, resp.XBeaconHeight)
	require.Greater(t, resp.XBeaconHeight, f.audit(t).OpeningHeight)
	got := f.audit(t)
	require.Equal(t, f.sampleAt+5, got.EpochSealHeight)
	require.Equal(t, resp.XBeaconHeight, got.XBeaconHeight)
	require.Equal(t, anchorTestManifest, got.EpochManifestHash)
	epoch, err := f.keeper.AnchorEpoch.Get(f.ctx, types.Hash32Key(anchorTestManifest))
	require.NoError(t, err)
	require.Equal(t, f.sampleAt+1, epoch.MaxOpeningHeight)
	require.Equal(t, f.worker, epoch.Worker)

	replay, err := f.seal(f.sampleAt+7, f.worker)
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, replay.Status)
	require.Equal(t, resp.XBeaconHeight, replay.XBeaconHeight, "a replay never moves x")
	_, err = f.server.SealAnchorEpoch(f.at(int64(f.sampleAt+7)), &types.MsgSealAnchorEpoch{
		EpochManifestHash: bytes.Repeat([]byte{0x0f}, 32), TaskIds: [][]byte{f.taskID}, SubmitterAddress: f.worker})
	require.ErrorContains(t, err, "already sealed into another epoch")

	// Openings at or after x are refused (here: a re-submission of the same opening).
	_, err = f.server.SubmitAnchorOpening(f.at(int64(resp.XBeaconHeight)), f.openingMsg(state, f.worker))
	require.ErrorContains(t, err, "x beacon height")
	_, err = f.server.SubmitAnchorOpening(f.at(int64(resp.XBeaconHeight-1)), f.openingMsg(state, f.worker))
	require.NoError(t, err, "before x the identical opening is still a NOOP")
}

func TestAnchorSealEpochRejectsLateOrMixedMembers(t *testing.T) {
	f := newContentFixture(t, 0, anchorGoodTokenSize)
	state := f.sample(t)
	f.open(t, state, f.sampleAt+1)
	_, err := f.seal(state.ProofDeadlineHeight, f.worker)
	require.ErrorContains(t, err, "past its proof deadline", "x must leave the Worker a block to prove in")

	other := bytes.Repeat([]byte{0xf1}, 32)
	foreign := f.audit(t) // opened
	foreign.TaskId = other
	foreign.WinnerWorker = f.operators[1]
	require.NoError(t, f.keeper.AnchorAudit.Set(f.ctx, types.NewTaskKey(other), foreign))
	ids := [][]byte{f.taskID, other}
	if bytes.Compare(other, f.taskID) < 0 {
		ids = [][]byte{other, f.taskID}
	}
	_, err = f.seal(f.sampleAt+2, f.worker, ids...)
	require.ErrorContains(t, err, "different Workers")

	g := newContentFixture(t, 0, anchorGoodTokenSize)
	g.openAndProve(t)
	_, err = g.seal(g.sampleAt+3, g.worker)
	require.ErrorContains(t, err, "proved before its epoch was sealed", "x after the proof would be useless")
}

func TestAnchorProofMustReferenceTheRecordedXHeight(t *testing.T) {
	f := newContentFixture(t, 0, anchorGoodTokenSize)
	state := f.sample(t)
	f.open(t, state, f.sampleAt+1)
	_, err := f.server.SubmitAnchorProof(f.at(int64(f.sampleAt+2)), f.proofMsg(f.sampleAt+3))
	require.ErrorContains(t, err, "never sealed", "an unsealed task has no x to bind")

	resp, err := f.seal(f.sampleAt+2, f.worker)
	require.NoError(t, err)
	x := resp.XBeaconHeight
	_, err = f.server.SubmitAnchorProof(f.at(int64(x)), f.proofMsg(0))
	require.ErrorContains(t, err, "must carry the Y_x rows", "a sealed task cannot fall back to a compute-only proof")
	_, err = f.server.SubmitAnchorProof(f.at(int64(x)), f.proofMsg(x+1))
	require.ErrorContains(t, err, "must carry the Y_x rows")
	wrongEpoch := f.proofMsg(x)
	wrongEpoch.EpochManifestHash = bytes.Repeat([]byte{0x0f}, 32)
	_, err = f.server.SubmitAnchorProof(f.at(int64(x)), wrongEpoch)
	require.ErrorContains(t, err, "different epoch manifest")
	_, err = f.server.SubmitAnchorProof(f.at(int64(x-1)), f.proofMsg(x))
	require.ErrorContains(t, err, "has not been reached")
	_, err = f.server.SubmitAnchorProof(f.at(int64(x)), f.proofMsg(x))
	require.ErrorContains(t, err, "unavailable", "no beacon, no x")

	randomness := bytes.Repeat([]byte{0x3c}, 32)
	f.beacons[int64(x)] = randomness
	q := queryServer{k: f.keeper}
	epochResp, err := q.AnchorEpoch(f.ctx, &types.QueryAnchorEpochRequest{EpochManifestHash: anchorTestManifest})
	require.NoError(t, err)
	want := randomness
	require.Equal(t, want, epochResp.XBeacon, "the query returns the beacon before any proof records it")
	require.Empty(t, epochResp.Epoch.XBeacon)

	_, err = f.server.SubmitAnchorProof(f.at(int64(x+3)), f.proofMsg(x))
	require.NoError(t, err)
	got := f.audit(t)
	require.True(t, got.WorkerProofXBound)
	require.Equal(t, want, got.XBeacon)
	epoch, err := f.keeper.AnchorEpoch.Get(f.ctx, types.Hash32Key(anchorTestManifest))
	require.NoError(t, err)
	require.Equal(t, want, epoch.XBeacon)
	replay, err := f.server.SubmitAnchorProof(f.at(int64(x+4)), f.proofMsg(x))
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, replay.Status)
}

// ---- content verdict track ------------------------------------------------

func TestAnchorContentVoteOpensOnlyAfterSettlement(t *testing.T) {
	f := newContentFixture(t, 0, anchorGoodTokenSize)
	state := f.openSealProve(t)
	h := state.VerdictDeadlineHeight + 1
	_, err := f.contentVote(0, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_PASS, state.XBeacon, h)
	require.ErrorContains(t, err, "once the task has settled", "settlement is never held for the content verdict")
	f.settle(t, h)
	_, err = f.contentVote(0, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_PASS, nil, h)
	require.ErrorContains(t, err, "x beacon other than", "a vote names the x it checked Y_x against")
	_, err = f.contentVote(0, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_UNSPECIFIED, state.XBeacon, h)
	require.ErrorContains(t, err, "verdict is invalid")
	_, err = f.contentVote(0, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_PASS, state.XBeacon, state.ContentVerdictDeadlineHeight)
	require.ErrorContains(t, err, "content verdict deadline")
	resp, err := f.contentVote(0, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_PASS, state.XBeacon, h)
	require.NoError(t, err)
	require.False(t, resp.Finalized)
}

func TestAnchorContentRejectSlashesBondAfterComputePass(t *testing.T) {
	f := newContentFixture(t, 1_000_000, anchorGoodTokenSize)
	f.setParams(t, func(v *types.VerificationParamsV1) { v.AnchorMinBondMultiple = 0 })
	f.sample(t) // pre-sample is replaced below: force selection for the test
	state := f.audit(t)
	state.Selected = true
	state.ContentVerdictDeadlineHeight = state.VerdictDeadlineHeight + f.params.Verification.AnchorContentVerdictWindowBlocks
	if len(state.Positions) == 0 {
		state.Positions = []uint64{1, 3}
	}
	require.NoError(t, f.keeper.AnchorAudit.Set(f.ctx, f.taskKey, state))
	f.open(t, state, f.sampleAt+1)
	seal, err := f.seal(f.sampleAt+2, f.worker)
	require.NoError(t, err)
	f.beacons[int64(seal.XBeaconHeight)] = bytes.Repeat([]byte{0x3c}, 32)
	_, err = f.server.SubmitAnchorProof(f.at(int64(seal.XBeaconHeight)), f.proofMsg(seal.XBeaconHeight))
	require.NoError(t, err)
	// Compute track: PASS at 2/3, the task settles and the Worker is paid.
	h := seal.XBeaconHeight + 1
	_, err = f.vote(0, f.valAcct[0], types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS, h)
	require.NoError(t, err)
	computeFinal, err := f.vote(1, f.valAcct[1], types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS, h)
	require.NoError(t, err)
	require.True(t, computeFinal.Finalized)
	f.settle(t, h+1)

	// Content track: its own tally; REJECT_CONTENT at 2/3.
	x := f.audit(t).XBeacon
	resp, err := f.contentVote(0, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_REJECT_CONTENT, x, h+2)
	require.NoError(t, err)
	require.False(t, resp.Finalized)
	_, err = f.contentVote(0, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_PASS, x, h+2)
	require.ErrorContains(t, err, "already cast a content vote")
	resp, err = f.contentVote(2, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_REJECT_CONTENT, x, h+3)
	require.NoError(t, err)
	require.True(t, resp.Finalized)
	require.Equal(t, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_REJECT_CONTENT, resp.ContentVerdict)

	got := f.audit(t)
	require.Equal(t, types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS, got.TaskVerdict, "the compute verdict is untouched")
	require.Equal(t, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_VALIDATOR_VOTE, got.ContentResultSource)
	require.Equal(t, h+3, got.ContentResultHeight)
	require.Len(t, got.ContentVerdicts, 1)
	require.Equal(t, int64(9_200_000), got.ContentVerdicts[0].ZMicro)
	require.Len(t, got.ContentWorkerFaultId, 32)
	require.Empty(t, got.WorkerFaultId)

	require.Len(t, *f.faults, 1)
	fault := (*f.faults)[0]
	require.Equal(t, hubtypes.FaultTypeAnchorContentReject, fault.FaultType)
	require.Equal(t, shared.DutyWorker, fault.Duty)
	require.Equal(t, f.worker, fault.OperatorAddress)
	require.Equal(t, f.params.Verification.AnchorContentRejectSlashBps, fault.SlashBps)
	require.Equal(t, uint64(1_000_000)*uint64(f.params.Verification.AnchorContentRejectSlashBps)/10_000, fault.RequestedAmount,
		"bps of the bond D fixed at sampling")
	require.Equal(t, anchorContentRejectEvidenceDigest(sdk.UnwrapSDKContext(f.ctx).ChainID(), got), fault.EvidenceDigest)

	replay, err := f.contentVote(1, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_PASS, x, h+4)
	require.NoError(t, err)
	require.True(t, replay.Finalized)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, replay.Status)
	require.Len(t, *f.faults, 1, "a final content verdict slashes once")

	votes, err := queryServer{k: f.keeper}.AnchorContentVotes(f.ctx, &types.QueryAnchorContentVotesRequest{TaskId: f.taskID})
	require.NoError(t, err)
	require.Len(t, votes.Votes, 2)
	require.Equal(t, uint32(2), votes.Tally.VoteCount)
	computeVotes, err := queryServer{k: f.keeper}.AnchorVotes(f.ctx, &types.QueryAnchorVotesRequest{TaskId: f.taskID})
	require.NoError(t, err)
	require.Len(t, computeVotes.Votes, 2, "the two tracks keep separate votes")
}

func TestAnchorContentPassDoesNotSlash(t *testing.T) {
	f := newContentFixture(t, 0, anchorGoodTokenSize)
	state := f.openSealProve(t)
	h := state.VerdictDeadlineHeight + 1
	f.settle(t, h)
	_, err := f.contentVote(0, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_PASS, state.XBeacon, h)
	require.NoError(t, err)
	resp, err := f.contentVote(1, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_PASS, state.XBeacon, h)
	require.NoError(t, err)
	require.True(t, resp.Finalized)
	require.Equal(t, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_PASS, resp.ContentVerdict)
	require.Empty(t, *f.faults)
	require.Empty(t, f.audit(t).ContentWorkerFaultId)
}

func TestAnchorContentVoteRefusedWithoutTrackOrAfterComputeReject(t *testing.T) {
	f := newContentFixture(t, 0, anchorGoodTokenSize)
	f.setParams(t, func(v *types.VerificationParamsV1) { v.AnchorContentVerdictWindowBlocks = 0 })
	f.openAndProve(t)
	f.settle(t, f.sampleAt+3)
	_, err := f.contentVote(0, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_PASS, nil, f.sampleAt+4)
	require.ErrorContains(t, err, "no content track")

	g := newContentFixture(t, 0, anchorGoodTokenSize)
	g.openAndProve(t)
	_, err = g.vote(0, g.valAcct[0], types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT, g.sampleAt+3)
	require.NoError(t, err)
	_, err = g.vote(1, g.valAcct[1], types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT, g.sampleAt+3)
	require.NoError(t, err)
	g.settle(t, g.sampleAt+4)
	_, err = g.contentVote(0, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_REJECT_CONTENT, nil, g.sampleAt+5)
	require.ErrorContains(t, err, "content track is closed", "one anchor slash per task")
}

// ---- content deadline at cleanup ------------------------------------------

func TestAnchorContentDeadlineVerdictAtCleanup(t *testing.T) {
	closeAt := func(t *testing.T, f *anchorFixture, height uint64) types.AnchorAuditState {
		t.Helper()
		ctx := f.at(int64(height))
		visited, err := f.keeper.cleanupAnchorAudit(ctx, f.taskKey)
		require.NoError(t, err)
		require.Equal(t, uint64(1), visited, "closing the content track is one cleanup step")
		state, err := f.keeper.AnchorAudit.Get(ctx, f.taskKey)
		require.NoError(t, err)
		require.NotZero(t, state.ContentResultHeight)
		return state
	}
	t.Run("proof not bound to x is REJECT_CONTENT and slashed", func(t *testing.T) {
		f := newContentFixture(t, 0, anchorGoodTokenSize)
		state := f.openAndProve(t) // compute-only proof, never sealed
		f.settle(t, state.VerdictDeadlineHeight+1)
		got := closeAt(t, f, state.ContentVerdictDeadlineHeight)
		require.Equal(t, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_REJECT_CONTENT, got.ContentVerdict)
		require.Equal(t, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_TIMEOUT, got.ContentResultSource)
		require.Len(t, *f.faults, 1)
		require.Equal(t, hubtypes.FaultTypeAnchorContentReject, (*f.faults)[0].FaultType)
		require.Len(t, got.ContentWorkerFaultId, 32)
	})
	t.Run("bound proof without quorum is NO_VERDICT", func(t *testing.T) {
		f := newContentFixture(t, 0, anchorGoodTokenSize)
		state := f.openSealProve(t)
		got := closeAt(t, f, state.ContentVerdictDeadlineHeight)
		require.Equal(t, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_NO_VERDICT, got.ContentVerdict)
		require.Empty(t, *f.faults)
	})
	t.Run("opened and never proved is REJECT_CONTENT and slashed as a content fault", func(t *testing.T) {
		f := newContentFixture(t, 0, anchorGoodTokenSize)
		state := f.sample(t)
		f.open(t, state, f.sampleAt+1) // on the content track, then the proof never comes
		state = f.audit(t)
		got := closeAt(t, f, state.ContentVerdictDeadlineHeight)
		require.Equal(t, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_REJECT_CONTENT, got.ContentVerdict)
		require.Equal(t, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_TIMEOUT, got.ContentResultSource)
		require.Len(t, *f.faults, 1)
		require.Equal(t, hubtypes.FaultTypeAnchorContentReject, (*f.faults)[0].FaultType, "not the smaller anchor_timeout")
		require.Equal(t, f.params.Verification.AnchorContentRejectSlashBps, (*f.faults)[0].SlashBps)
		require.Len(t, got.ContentWorkerFaultId, 32)
	})
	t.Run("opened and never proved: settlement already applied the content fault, no second slash", func(t *testing.T) {
		f := newContentFixture(t, 0, anchorGoodTokenSize)
		state := f.sample(t)
		f.open(t, state, f.sampleAt+1)
		state = f.audit(t)
		state.WorkerFaultId = bytes.Repeat([]byte{0xfa}, 32) // written by settlement (anchorWorkerFaultForVerdict → anchor_content_reject)
		state.ResultHeight, state.TaskVerdict = state.VerdictDeadlineHeight, types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT
		state.ResultSource = types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_TIMEOUT
		require.NoError(t, f.keeper.AnchorAudit.Set(f.ctx, f.taskKey, state))
		got := closeAt(t, f, state.ContentVerdictDeadlineHeight)
		require.Equal(t, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_REJECT_CONTENT, got.ContentVerdict)
		require.Equal(t, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_TIMEOUT, got.ContentResultSource)
		require.Empty(t, *f.faults, "a task is slashed at most once")
		require.Empty(t, got.ContentWorkerFaultId)
	})
	t.Run("no opening closes as COMPUTE_REJECTED without a second slash", func(t *testing.T) {
		f := newContentFixture(t, 0, anchorGoodTokenSize)
		state := f.sample(t)
		got := closeAt(t, f, state.ContentVerdictDeadlineHeight)
		require.Equal(t, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_NO_VERDICT, got.ContentVerdict)
		require.Equal(t, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_COMPUTE_REJECTED, got.ContentResultSource)
		require.Empty(t, *f.faults)
	})
	t.Run("compute fault already applied: no content slash", func(t *testing.T) {
		f := newContentFixture(t, 0, anchorGoodTokenSize)
		state := f.openAndProve(t)
		state.WorkerFaultId = bytes.Repeat([]byte{0xfa}, 32)
		state.ResultHeight, state.TaskVerdict = state.VerdictDeadlineHeight, types.AnchorVerdictV1_ANCHOR_VERDICT_V1_NO_VERDICT
		require.NoError(t, f.keeper.AnchorAudit.Set(f.ctx, f.taskKey, state))
		got := closeAt(t, f, state.ContentVerdictDeadlineHeight)
		require.Equal(t, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_REJECT_CONTENT, got.ContentVerdict)
		require.Empty(t, *f.faults)
		require.Empty(t, got.ContentWorkerFaultId)
	})
	t.Run("before the deadline never condemns", func(t *testing.T) {
		f := newContentFixture(t, 0, anchorGoodTokenSize)
		state := f.openAndProve(t)
		got := closeAt(t, f, state.ContentVerdictDeadlineHeight-1)
		require.Equal(t, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_NO_VERDICT, got.ContentVerdict)
		require.Empty(t, *f.faults)
	})
}

func TestAnchorCleanupKeepsContentFaultAndRemovesContentVotes(t *testing.T) {
	f := newContentFixture(t, 0, anchorGoodTokenSize)
	state := f.openSealProve(t)
	h := state.VerdictDeadlineHeight + 1
	f.settle(t, h)
	_, err := f.contentVote(0, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_REJECT_CONTENT, state.XBeacon, h)
	require.NoError(t, err)
	_, err = f.contentVote(1, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_REJECT_CONTENT, state.XBeacon, h)
	require.NoError(t, err)
	for i := 0; i < 10; i++ {
		visited, err := f.keeper.cleanupAnchorAudit(f.ctx, f.taskKey)
		require.NoError(t, err)
		require.LessOrEqual(t, visited, uint64(1))
		if visited == 0 {
			break
		}
	}
	slim := f.audit(t)
	require.Len(t, slim.ContentWorkerFaultId, 32, "the audit stays while it names the content fault")
	require.Equal(t, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_REJECT_CONTENT, slim.ContentVerdict)
	require.Empty(t, slim.OpeningHash)
	iter, err := f.keeper.AnchorContentVote.Iterate(f.ctx, collections.NewPrefixedPairRange[types.TaskKey, []byte](f.taskKey))
	require.NoError(t, err)
	keys, err := iter.Keys()
	iter.Close()
	require.NoError(t, err)
	require.Empty(t, keys)
}

func TestAnchorContentHoldsEvidenceCleanupUntilItsDeadline(t *testing.T) {
	f := newContentFixture(t, 0, anchorGoodTokenSize)
	state := f.openSealProve(t)
	hold, err := f.keeper.anchorContentCleanupHold(f.ctx, f.taskKey, state.VerdictDeadlineHeight)
	require.NoError(t, err)
	require.Equal(t, state.ContentVerdictDeadlineHeight, hold)
	hold, err = f.keeper.anchorContentCleanupHold(f.ctx, f.taskKey, state.ContentVerdictDeadlineHeight)
	require.NoError(t, err)
	require.Zero(t, hold, "at the deadline cleanup closes the track itself")

	// runTaskCleanup moves the cleanup index row to the deadline in one jump.
	target := state.VerdictDeadlineHeight + 10
	ctx := f.at(int64(target))
	require.NoError(t, addDeadlineIndex(ctx, f.keeper.EvidenceCleanupIndex, f.taskKey, target))
	progress, err := f.keeper.runTaskCleanup(ctx, nil, f.taskKey, target, 8)
	require.NoError(t, err)
	require.Equal(t, uint64(1), progress.processed)
	moved, err := f.keeper.EvidenceCleanupIndex.Has(ctx, types.NewDeadlineIndexKey(state.ContentVerdictDeadlineHeight, f.taskKey))
	require.NoError(t, err)
	require.True(t, moved)
	stale, err := f.keeper.EvidenceCleanupIndex.Has(ctx, types.NewDeadlineIndexKey(target, f.taskKey))
	require.NoError(t, err)
	require.False(t, stale)

	g := newContentFixture(t, 0, anchorGoodTokenSize)
	sampled := g.sample(t) // never opened: the compute track condemns, nothing to wait for
	hold, err = g.keeper.anchorContentCleanupHold(g.ctx, g.taskKey, sampled.VerdictDeadlineHeight)
	require.NoError(t, err)
	require.Zero(t, hold)
}

func TestAnchorPendingQueryContentTrack(t *testing.T) {
	f := newContentFixture(t, 0, anchorGoodTokenSize)
	state := f.openSealProve(t)
	q := queryServer{k: f.keeper}
	pending, err := q.AnchorAuditsPending(f.ctx, &types.QueryAnchorAuditsPendingRequest{Content: true})
	require.NoError(t, err)
	require.Empty(t, pending.Audits, "not settled yet")
	h := state.VerdictDeadlineHeight + 1
	f.settle(t, h)
	pending, err = q.AnchorAuditsPending(f.ctx, &types.QueryAnchorAuditsPendingRequest{Content: true})
	require.NoError(t, err)
	require.Len(t, pending.Audits, 1)
	_, err = f.contentVote(0, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_PASS, state.XBeacon, h)
	require.NoError(t, err)
	_, err = f.contentVote(1, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_PASS, state.XBeacon, h)
	require.NoError(t, err)
	pending, err = q.AnchorAuditsPending(f.ctx, &types.QueryAnchorAuditsPendingRequest{Content: true})
	require.NoError(t, err)
	require.Empty(t, pending.Audits, "final content verdicts are no longer pending")
}

// ---- genesis and upgrade --------------------------------------------------

func TestAnchorContentGenesisRoundTrip(t *testing.T) {
	f := newContentFixture(t, 0, anchorGoodTokenSize)
	state := f.openSealProve(t)
	h := state.VerdictDeadlineHeight + 1
	f.settle(t, h)
	_, err := f.contentVote(0, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_PASS, state.XBeacon, h)
	require.NoError(t, err)
	audits, err := collectMapValues(f.ctx, f.keeper.AnchorAudit)
	require.NoError(t, err)
	votes, err := collectMapValues(f.ctx, f.keeper.AnchorContentVote)
	require.NoError(t, err)
	epochs, err := collectMapValues(f.ctx, f.keeper.AnchorEpoch)
	require.NoError(t, err)
	require.Len(t, votes, 1)
	require.Len(t, epochs, 1)

	g := newContentFixture(t, 0, anchorGoodTokenSize)
	require.ErrorContains(t, g.keeper.initGenesisAnchor(g.ctx, types.GenesisState{AnchorContentVotes: votes}), "without an anchor audit")
	genesis := types.GenesisState{AnchorAudits: audits, AnchorContentVotes: votes, AnchorEpochs: epochs}
	require.NoError(t, g.keeper.initGenesisAnchor(g.ctx, genesis))
	back, err := g.keeper.AnchorEpoch.Get(g.ctx, types.Hash32Key(anchorTestManifest))
	require.NoError(t, err)
	require.Equal(t, epochs[0], back)
	gotVotes, err := collectMapValues(g.ctx, g.keeper.AnchorContentVote)
	require.NoError(t, err)
	require.Equal(t, votes, gotVotes)
	require.ErrorContains(t, g.keeper.initGenesisAnchor(g.ctx, types.GenesisState{AnchorEpochs: epochs}), "duplicate anchor epoch")
	bad := epochs[0]
	bad.EpochManifestHash = bytes.Repeat([]byte{0x01}, 32)
	bad.TaskIds = nil
	require.ErrorContains(t, g.keeper.initGenesisAnchor(g.ctx, types.GenesisState{AnchorEpochs: []types.AnchorEpochState{bad}}), "malformed")
}

func TestInitializeAnchorContentParamsUpgrade(t *testing.T) {
	f := newContentFixture(t, 0, anchorGoodTokenSize)
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	v := &params.Verification
	v.AnchorPositionsPerTask, v.AnchorMinBondMultiple, v.AnchorContentVerdictWindowBlocks, v.AnchorContentRejectSlashBps = 0, 0, 0, 0
	require.NoError(t, f.keeper.Params.Set(f.ctx, params)) // a v6 body
	before, err := f.keeper.GetTaskParamsMeta(f.ctx)
	require.NoError(t, err)

	ctx := f.at(500)
	require.NoError(t, f.keeper.InitializeAnchorContentParams(ctx))
	after, err := f.keeper.GetTaskParamsMeta(ctx)
	require.NoError(t, err)
	require.Equal(t, before.ParamsVersion+1, after.ParamsVersion)
	got, err := f.keeper.Params.Get(ctx)
	require.NoError(t, err)
	d := types.DefaultTaskParams().Verification
	require.Equal(t, d.AnchorPositionsPerTask, got.Verification.AnchorPositionsPerTask)
	require.Equal(t, d.AnchorMinBondMultiple, got.Verification.AnchorMinBondMultiple)
	require.Equal(t, d.AnchorContentVerdictWindowBlocks, got.Verification.AnchorContentVerdictWindowBlocks)
	require.Equal(t, d.AnchorContentRejectSlashBps, got.Verification.AnchorContentRejectSlashBps)
	archived, err := f.keeper.ParamsHistory.Get(ctx, before.ParamsVersion)
	require.NoError(t, err)
	require.Zero(t, archived.Verification.AnchorPositionsPerTask, "tasks pinned to the old version keep the v6 shape")

	require.NoError(t, f.keeper.InitializeAnchorContentParams(ctx))
	again, err := f.keeper.GetTaskParamsMeta(ctx)
	require.NoError(t, err)
	require.Equal(t, after.ParamsVersion, again.ParamsVersion, "idempotent")
}

// TestAnchorWorkerFaultForVerdict pins the fault settlement applies for a REJECT
// compute verdict (decision of 2026-11-09: opened on the content track and never
// proved is a content fault, never opening stays anchor_timeout).
func TestAnchorWorkerFaultForVerdict(t *testing.T) {
	v := types.VerificationParamsV1{AnchorRejectSlashBps: 2500, AnchorTimeoutSlashBps: 500, AnchorContentRejectSlashBps: 2500}
	h := bytes.Repeat([]byte{0x11}, types.Hash32Len)
	cases := []struct {
		name  string
		state types.AnchorAuditState
		fault string
		bps   uint32
	}{
		{"validators rejected the proof", types.AnchorAuditState{ResultSource: types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_VALIDATOR_VOTE,
			OpeningHash: h, WorkerProofHash: h, ContentVerdictDeadlineHeight: 10}, hubtypes.FaultTypeAnchorReject, 2500},
		{"never opened, content track", types.AnchorAuditState{ResultSource: types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_TIMEOUT,
			ContentVerdictDeadlineHeight: 10}, hubtypes.FaultTypeAnchorTimeout, 500},
		{"opened and never proved, content track", types.AnchorAuditState{ResultSource: types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_TIMEOUT,
			OpeningHash: h, ContentVerdictDeadlineHeight: 10}, hubtypes.FaultTypeAnchorContentReject, 2500},
		{"opened and never proved, no content track (v6 task)", types.AnchorAuditState{ResultSource: types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_TIMEOUT,
			OpeningHash: h}, hubtypes.FaultTypeAnchorTimeout, 500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fault, bps := anchorWorkerFaultForVerdict(tc.state, v)
			require.Equal(t, tc.fault, fault)
			require.Equal(t, tc.bps, bps)
		})
	}
}

// TestAnchorParamsUpgradeToleratesInvalidLiveBody pins audit F-P1-4 (2026-11-09):
// the anchor upgrades never halt the chain over a body the new rules reject.
func TestAnchorParamsUpgradeToleratesInvalidLiveBody(t *testing.T) {
	f := newContentFixture(t, 0, anchorGoodTokenSize)
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	v := &params.Verification
	v.AnchorOpenWindowBlocks, v.AnchorProofWindowBlocks, v.AnchorVerdictWindowBlocks = 0, 0, 0 // pre-v6 body
	v.AnchorPositionsPerTask, v.AnchorMinBondMultiple, v.AnchorContentVerdictWindowBlocks, v.AnchorContentRejectSlashBps = 0, 0, 0, 0
	params.Cleanup.TaskTerminalSummaryRetentionBlocks = 100 // shorter than the anchor windows the defaults add: Validate rejects it
	require.Error(t, params.WithAnchorDefaults().Validate(), "the fixture must exercise the invalid path")
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))
	before, err := f.keeper.GetTaskParamsMeta(f.ctx)
	require.NoError(t, err)

	ctx := f.at(500)
	require.NoError(t, f.keeper.ensureAnchorParamsInitialized(ctx), "the upgrade repairs the body and is written")
	after, err := f.keeper.GetTaskParamsMeta(ctx)
	require.NoError(t, err)
	require.Equal(t, before.ParamsVersion+1, after.ParamsVersion, "one migration: the v6 upgrade fills the v7 fields too, the v7 step is a no-op")
	got, err := f.keeper.Params.Get(ctx)
	require.NoError(t, err)
	d := types.DefaultTaskParams().Verification
	require.Equal(t, d.AnchorOpenWindowBlocks, got.Verification.AnchorOpenWindowBlocks)
	require.Equal(t, d.AnchorPositionsPerTask, got.Verification.AnchorPositionsPerTask)
	require.Equal(t, types.DefaultTaskTerminalSummaryRetentionBlocks, got.Cleanup.TaskTerminalSummaryRetentionBlocks, "the known conflict is repaired, not left to halt the chain")
	require.NoError(t, got.Validate())
}

// TestAnchorContentVoteNeedsBoundProof pins audit C-5 (2026-11-09): a content
// vote on a proof without Y_x rows is refused; the deadline closes that case.
func TestAnchorContentVoteNeedsBoundProof(t *testing.T) {
	f := newContentFixture(t, 0, anchorGoodTokenSize)
	state := f.openAndProve(t) // compute-only proof, never sealed
	f.settle(t, state.VerdictDeadlineHeight+1)
	_, err := f.contentVote(0, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_PASS, nil, state.VerdictDeadlineHeight+2)
	require.ErrorContains(t, err, "bound to a sealed B1 epoch")
}

// TestAnchorEpochRemovedWithLastMember pins audit C-3 (2026-11-09): the epoch
// row goes away with the last audit that names it.
func TestAnchorEpochRemovedWithLastMember(t *testing.T) {
	f := newContentFixture(t, 0, anchorGoodTokenSize)
	state := f.openSealProve(t)
	key := types.Hash32Key(anchorTestManifest)
	_, err := f.keeper.AnchorEpoch.Get(f.ctx, key)
	require.NoError(t, err)
	f.settle(t, state.VerdictDeadlineHeight+1)
	ctx := f.at(int64(state.ContentVerdictDeadlineHeight))
	for i := 0; i < 20; i++ {
		n, err := f.keeper.cleanupAnchorAudit(ctx, f.taskKey)
		require.NoError(t, err)
		if n == 0 {
			break
		}
	}
	_, err = f.keeper.AnchorEpoch.Get(ctx, key)
	require.ErrorIs(t, err, collections.ErrNotFound, "no member left: the epoch is removed")
}

// TestAnchorContentRejectSurvivesRefusedSlash pins audit C-2 (2026-11-09): on
// the cleanup path a REJECT_CONTENT whose slash the Hub refused keeps its row;
// on the vote path the transaction is refused instead of swallowing the failure.
func TestAnchorContentRejectSurvivesRefusedSlash(t *testing.T) {
	refused := func(f *anchorFixture) {
		f.keeper.hubKeeper = anchorHubStub{verificationHubStub: f.hub, bond: 0, beacons: f.beacons, faults: f.faults, faultErr: errors.New("hub refused")}
		f.server = msgServer{k: f.keeper}
	}
	t.Run("cleanup keeps the verdict", func(t *testing.T) {
		f := newContentFixture(t, 0, anchorGoodTokenSize)
		refused(f)
		state := f.openAndProve(t) // unbound proof: REJECT_CONTENT at the deadline
		f.settle(t, state.VerdictDeadlineHeight+1)
		ctx := f.at(int64(state.ContentVerdictDeadlineHeight))
		for i := 0; i < 20; i++ {
			n, err := f.keeper.cleanupAnchorAudit(ctx, f.taskKey)
			require.NoError(t, err)
			if n == 0 {
				break
			}
		}
		got, err := f.keeper.AnchorAudit.Get(ctx, f.taskKey)
		require.NoError(t, err, "the REJECT_CONTENT verdict survives cleanup although no fault id names it")
		require.Equal(t, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_REJECT_CONTENT, got.ContentVerdict)
		require.Empty(t, got.ContentWorkerFaultId)
		require.Equal(t, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_TIMEOUT, got.ContentResultSource, "slimmed, verdict and source kept")
		require.Empty(t, *f.faults)
	})
	t.Run("the vote transaction is refused", func(t *testing.T) {
		f := newContentFixture(t, 0, anchorGoodTokenSize)
		refused(f)
		state := f.openSealProve(t)
		h := state.XBeaconHeight + 1
		_, err := f.vote(0, f.valAcct[0], types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS, h)
		require.NoError(t, err)
		_, err = f.vote(1, f.valAcct[1], types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS, h)
		require.NoError(t, err)
		f.settle(t, h+1)
		x := f.audit(t).XBeacon
		_, err = f.contentVote(0, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_REJECT_CONTENT, x, h+2)
		require.NoError(t, err)
		_, err = f.contentVote(1, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_REJECT_CONTENT, x, h+3)
		require.ErrorContains(t, err, "could not be applied")
		require.Zero(t, f.audit(t).ContentResultHeight, "not finalized: the validator retries")
	})
}
