package keeper

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/address"
	"cosmossdk.io/math"
	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// ---- stubs -------------------------------------------------------------

type anchorHubStub struct {
	*verificationHubStub
	bond uint64
}

func (s anchorHubStub) GetServiceBondFacts(sdk.Context, string) hubtypes.ServiceBondFacts {
	return hubtypes.ServiceBondFacts{ActiveBond: s.bond, EffectiveBond: s.bond, Found: true}
}

type anchorStakingStub struct {
	codec  address.Codec
	powers map[string]int64
}

func (s anchorStakingStub) GetValidator(_ context.Context, addr sdk.ValAddress) (stakingtypes.Validator, error) {
	if _, ok := s.powers[string(addr)]; !ok {
		return stakingtypes.Validator{}, stakingtypes.ErrNoValidatorFound
	}
	return stakingtypes.Validator{Status: stakingtypes.Bonded}, nil
}

func (s anchorStakingStub) GetLastValidatorPower(_ context.Context, addr sdk.ValAddress) (int64, error) {
	return s.powers[string(addr)], nil
}

func (s anchorStakingStub) GetLastTotalPower(context.Context) (math.Int, error) {
	total := int64(0)
	for _, p := range s.powers {
		total += p
	}
	return math.NewInt(total), nil
}

func (s anchorStakingStub) ValidatorAddressCodec() address.Codec { return s.codec }

// ---- fixture -----------------------------------------------------------

type anchorFixture struct {
	*verificationFixture
	staking  anchorStakingStub
	valoper  []string // bech32 valoper of three validators (power 1 each)
	valAcct  []string // the matching account addresses
	worker   string
	beacon   []byte
	sampleAt uint64
	params   types.TaskParamsV1
}

func newAnchorFixture(t *testing.T, bond uint64, tokenSize uint64) *anchorFixture {
	t.Helper()
	vf := newVerificationFixture(t)
	vf.keeper.hubKeeper = anchorHubStub{verificationHubStub: vf.hub, bond: bond}
	valCodec := addresscodec.NewBech32Codec(sdk.GetConfig().GetBech32ValidatorAddrPrefix())
	staking := anchorStakingStub{codec: valCodec, powers: map[string]int64{}}
	f := &anchorFixture{verificationFixture: vf, staking: staking, worker: vf.operators[0],
		beacon: bytes.Repeat([]byte{0x77}, types.Hash32Len), sampleAt: 100}
	for i := byte(1); i <= 3; i++ {
		raw := bytes.Repeat([]byte{0xA0 + i}, 20)
		staking.powers[string(raw)] = 1
		valoper, err := valCodec.BytesToString(raw)
		require.NoError(t, err)
		f.valoper = append(f.valoper, valoper)
		f.valAcct = append(f.valAcct, sdk.AccAddress(raw).String())
	}
	vf.keeper.stakingKeeper = staking
	vf.server = msgServer{k: vf.keeper}
	params, err := vf.keeper.Params.Get(vf.ctx)
	require.NoError(t, err)
	f.params = params
	require.NoError(t, vf.keeper.TaskCore.Set(vf.ctx, vf.taskKey, types.TaskCoreState{
		TaskId: vf.taskID, SessionId: bytes.Repeat([]byte{0x61}, types.Hash32Len), ModelId: bytes.Repeat([]byte{0x6d}, types.Hash32Len),
		ProfileVersion: 1, OrderValue: shared.NewAmount(1_000),
		TaskPhase: types.TaskPhase_TASK_PHASE_VERIFIER_ASSIGNED, VerificationStatus: types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_ASSIGNED,
	}))
	require.NoError(t, vf.keeper.WriteInferReceipt(vf.ctx, vf.taskKey, types.InferReceiptState{
		TaskId: vf.taskID, WinnerWorker: f.worker, GeneratedTokenCount: 10, InferReceiptHash: bytes.Repeat([]byte{0x99}, 32),
		RequiredEvidenceCommitments: []types.EvidenceCommitmentV1{
			{EvidenceKind: shared.EvidenceKind_EVIDENCE_KIND_WORKER_VALUE_OPENING, EvidenceHashOrRoot: bytes.Repeat([]byte{1}, 32), EncodedSizeBytes: 100},
			{EvidenceKind: shared.EvidenceKind_EVIDENCE_KIND_WORKER_TOKEN_OPENING, EvidenceHashOrRoot: bytes.Repeat([]byte{2}, 32), EncodedSizeBytes: tokenSize},
			{EvidenceKind: shared.EvidenceKind_EVIDENCE_KIND_WORKER_ANCHOR_OPENING, EvidenceHashOrRoot: bytes.Repeat([]byte{3}, 32), EncodedSizeBytes: 64},
		},
	}))
	return f
}

func (f *anchorFixture) sample(t *testing.T) types.AnchorAuditState {
	t.Helper()
	assignment := f.assignment
	assignment.SelectionRandomnessBeacon = f.beacon
	assignment.SelectionRandomnessHeight = f.sampleAt
	require.NoError(t, f.keeper.sampleAnchorAudit(f.ctx, assignment))
	state, err := f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	return state
}

func (f *anchorFixture) at(height int64) context.Context {
	return sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(height))
}

// 8 + 4*(L+G): L = 5 prompt ids, G = 10 generated ids.
const anchorGoodTokenSize = 8 + 4*15

// ---- sampling ----------------------------------------------------------

func TestAnchorSampleRecordsDeadlinesRateAndBond(t *testing.T) {
	f := newAnchorFixture(t, 0, anchorGoodTokenSize)
	state := f.sample(t)
	v := f.params.Verification
	require.True(t, state.Sampled)
	require.True(t, state.Selected, "zero bond is always audited")
	require.Equal(t, uint32(types.BasisPointsMaximum), state.AuditRateBps)
	require.Len(t, state.Positions, anchorSamplePositionsV0)
	require.Equal(t, f.sampleAt+v.AnchorOpenWindowBlocks, state.OpeningDeadlineHeight)
	require.Equal(t, state.OpeningDeadlineHeight+v.AnchorProofWindowBlocks, state.ProofDeadlineHeight)
	require.Equal(t, state.ProofDeadlineHeight+v.AnchorVerdictWindowBlocks, state.VerdictDeadlineHeight)
	require.Zero(t, state.ResultHeight)
}

func TestAnchorSampleLargeBondLowersRateToFloor(t *testing.T) {
	f := newAnchorFixture(t, 1_000_000_000, anchorGoodTokenSize)
	state := f.sample(t)
	require.Equal(t, f.params.Verification.AnchorAuditMinRateBps, state.AuditRateBps)
	require.Equal(t, uint64(1_000_000_000), state.SlashableBond)
	if !state.Selected {
		require.Empty(t, state.Positions, "an unselected task gets no positions")
	}
}

func TestAnchorSampleMalformedCommitmentRejectsInsteadOfHalting(t *testing.T) {
	f := newAnchorFixture(t, 0, 9) // not 8 + 4k
	state := f.sample(t)           // must not return an error inside EndBlock
	require.True(t, state.Selected)
	require.NotZero(t, state.ResultHeight)
	require.Equal(t, types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT, state.TaskVerdict)
	require.Equal(t, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_INVALID_COMMITMENT, state.ResultSource)
}

func TestAnchorReceiptAdmissionRejectsMalformedTokenCommitment(t *testing.T) {
	bad := types.InferReceiptState{GeneratedTokenCount: 10, RequiredEvidenceCommitments: []types.EvidenceCommitmentV1{
		{EvidenceKind: shared.EvidenceKind_EVIDENCE_KIND_WORKER_TOKEN_OPENING, EncodedSizeBytes: 9},
		{EvidenceKind: shared.EvidenceKind_EVIDENCE_KIND_WORKER_ANCHOR_OPENING, EncodedSizeBytes: 64},
	}}
	require.Error(t, validateAnchorTokenCommitment(bad))
	bad.RequiredEvidenceCommitments[0].EncodedSizeBytes = 8 + 4*10 // only the generated ids
	require.Error(t, validateAnchorTokenCommitment(bad))
	bad.RequiredEvidenceCommitments[0].EncodedSizeBytes = anchorGoodTokenSize
	require.NoError(t, validateAnchorTokenCommitment(bad))
	// Without an anchor commitment the token size is not the anchor's concern.
	require.NoError(t, validateAnchorTokenCommitment(types.InferReceiptState{GeneratedTokenCount: 10, RequiredEvidenceCommitments: []types.EvidenceCommitmentV1{
		{EvidenceKind: shared.EvidenceKind_EVIDENCE_KIND_WORKER_TOKEN_OPENING, EncodedSizeBytes: 9},
	}}))
}

// ---- Worker messages -----------------------------------------------------

func (f *anchorFixture) openingMsg(state types.AnchorAuditState, submitter string) *types.MsgSubmitAnchorOpening {
	return &types.MsgSubmitAnchorOpening{
		TaskId: f.taskID, Positions: state.Positions, OpeningHash: bytes.Repeat([]byte{0x0c}, 32), OpeningSizeBytes: 10,
		OpeningUri: "https://da.example/o", AnchorCommitUri: "https://da.example/c", SubmitterAddress: submitter,
	}
}

func TestAnchorOpeningOnlyByWorkerAndBeforeDeadline(t *testing.T) {
	f := newAnchorFixture(t, 0, anchorGoodTokenSize)
	state := f.sample(t)
	builder := sdk.AccAddress(bytes.Repeat([]byte{0x5b}, 20)).String()
	_, err := f.server.SubmitAnchorOpening(f.at(int64(f.sampleAt+1)), f.openingMsg(state, builder))
	require.ErrorContains(t, err, "winner Worker's current service", "a Builder may not write the Worker's opening")
	_, err = f.server.SubmitAnchorOpening(f.at(int64(state.OpeningDeadlineHeight+1)), f.openingMsg(state, f.worker))
	require.ErrorContains(t, err, "opening deadline")
	_, err = f.server.SubmitAnchorOpening(f.at(int64(state.OpeningDeadlineHeight)), f.openingMsg(state, f.worker))
	require.NoError(t, err)

	proof := &types.MsgSubmitAnchorProof{TaskId: f.taskID, OpeningHash: bytes.Repeat([]byte{0x0c}, 32), ProofHash: bytes.Repeat([]byte{0x0d}, 32),
		ProofSizeBytes: 5, ProofUri: "https://da.example/p", EpochManifestHash: bytes.Repeat([]byte{0x0e}, 32), EpochManifestUri: "https://da.example/m",
		SubmitterAddress: f.worker}
	_, err = f.server.SubmitAnchorProof(f.at(int64(state.ProofDeadlineHeight+1)), proof)
	require.ErrorContains(t, err, "proof deadline")
	_, err = f.server.SubmitAnchorProof(f.at(int64(state.ProofDeadlineHeight)), proof)
	require.NoError(t, err)
	got, err := f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.Equal(t, "https://da.example/c", got.AnchorCommitUri)
	require.Equal(t, proof.EpochManifestHash, got.EpochManifestHash)
}

// ---- validator votes ------------------------------------------------------

func (f *anchorFixture) openAndProve(t *testing.T) types.AnchorAuditState {
	t.Helper()
	state := f.sample(t)
	_, err := f.server.SubmitAnchorOpening(f.at(int64(f.sampleAt+1)), f.openingMsg(state, f.worker))
	require.NoError(t, err)
	_, err = f.server.SubmitAnchorProof(f.at(int64(f.sampleAt+2)), &types.MsgSubmitAnchorProof{TaskId: f.taskID, OpeningHash: bytes.Repeat([]byte{0x0c}, 32),
		ProofHash: bytes.Repeat([]byte{0x0d}, 32), ProofSizeBytes: 5, ProofUri: "https://da.example/p",
		EpochManifestHash: bytes.Repeat([]byte{0x0e}, 32), EpochManifestUri: "https://da.example/m", SubmitterAddress: f.worker})
	require.NoError(t, err)
	got, err := f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	return got
}

func (f *anchorFixture) vote(i int, signer string, verdict types.AnchorVerdictV1, height uint64) (*types.MsgSubmitAnchorValidatorVoteResponse, error) {
	return f.server.SubmitAnchorValidatorVote(f.at(int64(height)), &types.MsgSubmitAnchorValidatorVote{
		TaskId: f.taskID, OpeningHash: bytes.Repeat([]byte{0x0c}, 32), ProofHash: bytes.Repeat([]byte{0x0d}, 32),
		Verdict: verdict, ValidatorOperatorAddress: f.valoper[i], SubmitterAddress: signer,
	})
}

func TestAnchorValidatorVotesReachTwoThirdsWithDelegatedKey(t *testing.T) {
	f := newAnchorFixture(t, 0, anchorGoodTokenSize)
	state := f.openAndProve(t)
	h := f.sampleAt + 3
	outsider := sdk.AccAddress(bytes.Repeat([]byte{0x5c}, 20)).String()
	_, err := f.vote(0, outsider, types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS, h)
	require.ErrorContains(t, err, "registered vote address")

	resp, err := f.vote(0, f.valAcct[0], types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS, h)
	require.NoError(t, err)
	require.False(t, resp.Finalized, "1/3 of the power is not final")
	replay, err := f.vote(0, f.valAcct[0], types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS, h)
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, replay.Status)
	_, err = f.vote(0, f.valAcct[0], types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT, h)
	require.ErrorContains(t, err, "already voted")

	// Validator 2 delegates to a hot vote key and votes with it.
	hot := sdk.AccAddress(bytes.Repeat([]byte{0x5d}, 20)).String()
	_, err = f.server.RegisterAnchorVoteKey(f.at(int64(h)), &types.MsgRegisterAnchorVoteKey{ValidatorOperatorAddress: f.valoper[1], VoteAddress: hot, SubmitterAddress: hot})
	require.ErrorContains(t, err, "only the validator operator account")
	_, err = f.server.RegisterAnchorVoteKey(f.at(int64(h)), &types.MsgRegisterAnchorVoteKey{ValidatorOperatorAddress: f.valoper[1], VoteAddress: hot, SubmitterAddress: f.valAcct[1]})
	require.NoError(t, err)
	resp, err = f.vote(1, hot, types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS, h)
	require.NoError(t, err)
	require.True(t, resp.Finalized)
	require.Equal(t, types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS, resp.TaskVerdict)
	final, err := f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.Equal(t, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_VALIDATOR_VOTE, final.ResultSource)
	require.NotZero(t, state.VerdictDeadlineHeight)

	pending, err := queryServer{k: f.keeper}.AnchorAuditsPending(f.ctx, &types.QueryAnchorAuditsPendingRequest{})
	require.NoError(t, err)
	require.Empty(t, pending.Audits, "a final audit is no longer pending")
}

func TestAnchorVoteRefusedAfterVerdictDeadline(t *testing.T) {
	f := newAnchorFixture(t, 0, anchorGoodTokenSize)
	state := f.openAndProve(t)
	pending, err := queryServer{k: f.keeper}.AnchorAuditsPending(f.ctx, &types.QueryAnchorAuditsPendingRequest{})
	require.NoError(t, err)
	require.Len(t, pending.Audits, 1)
	_, err = f.vote(0, f.valAcct[0], types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT, state.VerdictDeadlineHeight)
	require.ErrorContains(t, err, "deadline")
}

// ---- settlement view and deadlines ---------------------------------------

func TestAnchorSettlementViewHoldsAndTimesOut(t *testing.T) {
	pass := types.TaskVerdict_TASK_VERDICT_PASS
	t.Run("no opening is REJECT", func(t *testing.T) {
		f := newAnchorFixture(t, 0, anchorGoodTokenSize)
		state := f.sample(t)
		_, err := f.keeper.anchorSettlementView(f.ctx, f.taskKey, pass, state.VerdictDeadlineHeight-1)
		require.ErrorContains(t, err, "pending until")
		view, err := f.keeper.anchorSettlementView(f.ctx, f.taskKey, types.TaskVerdict_TASK_VERDICT_FAIL, state.VerdictDeadlineHeight-1)
		require.NoError(t, err)
		require.Nil(t, view, "a failed round is not held")
		view, err = f.keeper.anchorSettlementView(f.ctx, f.taskKey, pass, state.VerdictDeadlineHeight)
		require.NoError(t, err)
		require.Equal(t, types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT, view.TaskVerdict)
		require.Equal(t, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_TIMEOUT, view.ResultSource)
	})
	t.Run("proof without quorum is NO_VERDICT", func(t *testing.T) {
		f := newAnchorFixture(t, 0, anchorGoodTokenSize)
		state := f.openAndProve(t)
		view, err := f.keeper.anchorSettlementView(f.ctx, f.taskKey, pass, state.VerdictDeadlineHeight)
		require.NoError(t, err)
		require.Equal(t, types.AnchorVerdictV1_ANCHOR_VERDICT_V1_NO_VERDICT, view.TaskVerdict)
	})
	t.Run("pre-v4 result is advisory", func(t *testing.T) {
		f := newAnchorFixture(t, 0, anchorGoodTokenSize)
		require.NoError(t, f.keeper.AnchorAudit.Set(f.ctx, f.taskKey, types.AnchorAuditState{
			TaskId: f.taskID, Sampled: true, ResultHeight: 5, TaskVerdict: types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT,
		}))
		view, err := f.keeper.anchorSettlementView(f.ctx, f.taskKey, pass, 1_000_000)
		require.NoError(t, err)
		require.Nil(t, view)
	})
}

func TestEffectiveSettlementDeadlineIsStableAcrossTheVerdict(t *testing.T) {
	f := newAnchorFixture(t, 0, anchorGoodTokenSize)
	state := f.openAndProve(t)
	require.NoError(t, f.keeper.WriteVerificationRound(f.ctx, types.NewVerifyRoundKey(f.taskKey, types.VerifyRoundV1), types.VerificationRoundState{
		TaskId: f.taskID, VerifyRound: types.VerifyRoundV1,
		XVerdict:      &types.VerificationRoundState_Verdict{Verdict: types.TaskVerdict_TASK_VERDICT_PASS},
		XClosedHeight: &types.VerificationRoundState_ClosedHeight{ClosedHeight: 150},
	}))
	summary := types.TaskRoundSummaryState{TaskId: f.taskID, EffectiveVerifyRound: types.VerifyRoundV1,
		XRoundsClosedHeight: &types.TaskRoundSummaryState_RoundsClosedHeight{RoundsClosedHeight: 150}}
	before, err := f.keeper.effectiveSettlementDeadline(f.ctx, f.taskKey, summary, f.params)
	require.NoError(t, err)
	require.Equal(t, state.VerdictDeadlineHeight+1, before)
	_, err = f.vote(0, f.valAcct[0], types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS, f.sampleAt+3)
	require.NoError(t, err)
	_, err = f.vote(1, f.valAcct[1], types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS, f.sampleAt+3)
	require.NoError(t, err)
	after, err := f.keeper.effectiveSettlementDeadline(f.ctx, f.taskKey, summary, f.params)
	require.NoError(t, err)
	require.Equal(t, before, after, "recomputations after the verdict must name the same index row")
	hold, err := f.keeper.anchorSettlementHoldHeight(f.ctx, f.taskKey, types.VerifyRoundV1)
	require.NoError(t, err)
	require.Zero(t, hold, "no hold once the verdict is final")
}

// ---- slash amount, cleanup, genesis ---------------------------------------

func TestAnchorSlashAmountUsesTheSampledBond(t *testing.T) {
	require.Equal(t, uint64(250), anchorSlashAmount(1_000, 2_500))
	require.Equal(t, uint64(0), anchorSlashAmount(1_000, 0))
	require.Equal(t, uint64(1_000), anchorSlashAmount(1_000, 10_000))
}

func TestCleanupAnchorAuditKeepsOnlyAFaultReference(t *testing.T) {
	f := newAnchorFixture(t, 0, anchorGoodTokenSize)
	f.openAndProve(t)
	_, err := f.vote(0, f.valAcct[0], types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT, f.sampleAt+3)
	require.NoError(t, err)
	state, err := f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	state.WorkerFaultId = bytes.Repeat([]byte{0xfa}, 32)
	require.NoError(t, f.keeper.AnchorAudit.Set(f.ctx, f.taskKey, state))

	steps := 0
	for {
		visited, err := f.keeper.cleanupAnchorAudit(f.ctx, f.taskKey)
		require.NoError(t, err)
		require.LessOrEqual(t, visited, uint64(1), "one row per cleanup step: the EndBlock fair-share budget allows a single visited row")
		if visited == 0 {
			break
		}
		steps++
		require.Less(t, steps, 10)
	}
	require.Equal(t, 2, steps, "one vote, then the slim audit")
	slim, err := f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.Equal(t, state.WorkerFaultId, slim.WorkerFaultId)
	require.Empty(t, slim.OpeningUri)
	iter, err := f.keeper.AnchorValidatorVote.Iterate(f.ctx, collections.NewPrefixedPairRange[types.TaskKey, []byte](f.taskKey))
	require.NoError(t, err)
	keys, err := iter.Keys()
	iter.Close()
	require.NoError(t, err)
	require.Empty(t, keys, "votes are removed")
	visited, err := f.keeper.cleanupAnchorAudit(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.Zero(t, visited, "cleanup is idempotent")

	g := newAnchorFixture(t, 0, anchorGoodTokenSize)
	g.sample(t)
	_, err = g.keeper.cleanupAnchorAudit(g.ctx, g.taskKey)
	require.NoError(t, err)
	_, err = g.keeper.AnchorAudit.Get(g.ctx, g.taskKey)
	require.True(t, errors.Is(err, collections.ErrNotFound), "an audit without a fault is removed")
}

func TestAnchorGenesisImportRejectsDuplicatesAndOrphanVotes(t *testing.T) {
	f := newAnchorFixture(t, 0, anchorGoodTokenSize)
	state := types.AnchorAuditState{TaskId: bytes.Repeat([]byte{0x42}, 32), Sampled: true}
	require.NoError(t, f.keeper.initGenesisAnchor(f.ctx, types.GenesisState{AnchorAudits: []types.AnchorAuditState{state}}))
	require.ErrorContains(t, f.keeper.initGenesisAnchor(f.ctx, types.GenesisState{AnchorAudits: []types.AnchorAuditState{state}}), "duplicate anchor audit")
	orphan := types.AnchorValidatorVoteState{TaskId: bytes.Repeat([]byte{0x43}, 32), ValidatorOperatorAddress: f.valoper[0]}
	require.ErrorContains(t, f.keeper.initGenesisAnchor(f.ctx, types.GenesisState{AnchorValidatorVotes: []types.AnchorValidatorVoteState{orphan}}), "without an anchor audit")
	exported, err := collectMapValues(f.ctx, f.keeper.AnchorAudit)
	require.NoError(t, err)
	require.Len(t, exported, 1)
}
