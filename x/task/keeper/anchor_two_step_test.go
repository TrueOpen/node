package keeper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// ---- fixture -------------------------------------------------------------

// twoStepHubStub serves one verified proposer-VRF beacon per height, like a
// chain on which the VRF beacon is required. A height can be made to have no
// beacon, or a block-hash placeholder, to stage the windows the draw refuses.
type twoStepHubStub struct {
	anchorOnlyHubStub
	missing     map[int64]bool
	placeholder map[int64]bool
	// missingFrom, when non-zero, removes every beacon from that height on.
	missingFrom *int64
	endpoint    *string
}

func testBlockBeacon(height int64) []byte {
	var h [8]byte
	binary.BigEndian.PutUint64(h[:], uint64(height))
	sum := sha256.Sum256(append([]byte("two-step-test-block-beacon"), h[:]...))
	return sum[:]
}

func (s twoStepHubStub) GetBeaconForDomain(_ sdk.Context, _ string, height int64) (hubtypes.BeaconSnapshot, bool) {
	if s.missing[height] || (*s.missingFrom != 0 && height >= *s.missingFrom) {
		return hubtypes.BeaconSnapshot{}, false
	}
	if s.placeholder[height] {
		return hubtypes.BeaconSnapshot{Height: height, Randomness: testBlockBeacon(height), SourceTag: hubtypes.BeaconSourcePlaceholderBlockHashV1}, true
	}
	return hubtypes.BeaconSnapshot{
		Height: height, Randomness: testBlockBeacon(height),
		SourceTag: hubtypes.BeaconSourceProposerVRFV1, ProofDigest: bytes32(0x5d),
	}, true
}

func (s twoStepHubStub) GetAnchorDAEndpoint(context.Context, string) string { return *s.endpoint }

type twoStepFixture struct {
	*anchorOnlyFixture
	hub        twoStepHubStub
	listWindow uint64
	m          uint64
	rows       uint64
}

const (
	twoStepListWindow = uint64(50)
	twoStepBeacons    = uint32(4)
)

// newTwoStepFixture stages an anchor-only task on a Profile version whose
// anchor requirement carries commitment schema version 2, on a chain that has
// the two-step parameters set and serves a VRF beacon at every height.
func newTwoStepFixture(t *testing.T, bond uint64) *twoStepFixture {
	t.Helper()
	af := newAnchorOnlyFixtureWithSchema(t, bond, true, shared.WorkerAnchorCommitmentSchemaVersionV2)
	af.params.Verification.AnchorListWindowBlocks = twoStepListWindow
	af.params.Verification.AnchorPositionBeaconBlocks = twoStepBeacons
	require.NoError(t, af.params.Validate())
	require.NoError(t, af.keeper.Params.Set(af.ctx, af.params))
	var missingFrom int64
	endpoint := ""
	hub := twoStepHubStub{
		anchorOnlyHubStub: af.keeper.hubKeeper.(anchorOnlyHubStub),
		missing:           map[int64]bool{}, placeholder: map[int64]bool{}, missingFrom: &missingFrom, endpoint: &endpoint,
	}
	af.keeper.hubKeeper = hub
	af.server = msgServer{k: af.keeper}
	return &twoStepFixture{
		anchorOnlyFixture: af, hub: hub, listWindow: twoStepListWindow, m: uint64(twoStepBeacons),
		rows: anchorOnlyPrompt + anchorOnlyGenerated - 1,
	}
}

// selected accepts the receipt and runs the first beacon; the task must be
// selected (the fixture bond makes the audit rate 100 % unless stated).
func (f *twoStepFixture) selected(t *testing.T) types.AnchorAuditState {
	t.Helper()
	f.submitReceipt(t)
	state := f.sampleRound(t)
	require.True(t, state.Selected)
	require.Equal(t, anchorSamplingModeTwoStep, state.SamplingMode)
	return state
}

func (f *twoStepFixture) listMsg(hashByte byte) *types.MsgSubmitAnchorCommitmentList {
	return &types.MsgSubmitAnchorCommitmentList{
		TaskId: f.taskID, ListHash: bytes32(hashByte), ListSizeBytes: 32 * f.rows,
		ListUri: "https://da.example/list", SubmitterAddress: f.worker,
	}
}

func (f *twoStepFixture) audit(t *testing.T) types.AnchorAuditState {
	t.Helper()
	state, err := f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	return state
}

// sweep runs the sampling queue at height and returns the rows it visited.
func (f *twoStepFixture) sweep(t *testing.T, height uint64) uint64 {
	t.Helper()
	visited, err := f.keeper.processAnchorSamplingDue(f.at(int64(height)), height, 10)
	require.NoError(t, err, "the sampling queue must never return an error")
	return visited
}

func (f *twoStepFixture) dueHeight(t *testing.T) (uint64, bool) {
	t.Helper()
	height, err := f.keeper.AnchorSamplingDueHeight.Get(f.ctx, f.taskKey)
	if err != nil {
		require.True(t, errIsNotFound(err))
		return 0, false
	}
	indexed, err := f.keeper.AnchorSamplingDueIndex.Has(f.ctx, types.NewDeadlineIndexKey(height, f.taskKey))
	require.NoError(t, err)
	require.True(t, indexed, "the queue row and its by-task pointer must agree")
	return height, true
}

// postList records the list at height and returns the audit.
func (f *twoStepFixture) postList(t *testing.T, height uint64) types.AnchorAuditState {
	t.Helper()
	resp, err := f.server.SubmitAnchorCommitmentList(f.at(int64(height)), f.listMsg(0x1a))
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, resp.Status)
	require.Equal(t, height+1, resp.PositionBeaconFirstHeight)
	require.Equal(t, height+f.m, resp.PositionBeaconLastHeight)
	return f.audit(t)
}

// drawn records the list at listAt and runs the draw at its last contributing height.
func (f *twoStepFixture) drawn(t *testing.T, listAt uint64) types.AnchorAuditState {
	t.Helper()
	state := f.postList(t, listAt)
	require.Equal(t, uint64(1), f.sweep(t, state.PositionBeaconLastHeight))
	state = f.audit(t)
	require.NotEmpty(t, state.Positions)
	return state
}

func (f *twoStepFixture) openAndProveDrawn(t *testing.T, state types.AnchorAuditState) {
	t.Helper()
	h := int64(state.PositionsHeight)
	_, err := f.server.SubmitAnchorOpening(f.at(h+1), f.openingMsg(state, f.worker))
	require.NoError(t, err)
	_, err = f.server.SubmitAnchorProof(f.at(h+2), &types.MsgSubmitAnchorProof{TaskId: f.taskID, OpeningHash: bytes.Repeat([]byte{0x0c}, 32),
		ProofHash: bytes.Repeat([]byte{0x0d}, 32), ProofSizeBytes: 5, ProofUri: "https://da.example/p",
		EpochManifestHash: bytes.Repeat([]byte{0x0e}, 32), EpochManifestUri: "https://da.example/m", SubmitterAddress: f.worker})
	require.NoError(t, err)
}

func (f *twoStepFixture) windows() (open, proof, verdict uint64) {
	v := f.params.Verification
	return v.AnchorOpenWindowBlocks, v.AnchorProofWindowBlocks, v.AnchorVerdictWindowBlocks
}

// ---- selection -----------------------------------------------------------

// The first beacon selects the task and decides nothing else: no position
// exists, the list deadline and the upper-bound verdict deadline are written,
// and settlement is held from this moment.
func TestTwoStepSelectionDrawsNoPositionAndHoldsSettlement(t *testing.T) {
	f := newTwoStepFixture(t, 0)
	state := f.selected(t)
	open, proof, verdict := f.windows()
	sel := f.window.SelectionRandomnessHeight

	require.Empty(t, state.Positions, "no position may be derived from the first beacon")
	require.Equal(t, sel, state.SampleHeight)
	require.Equal(t, testBlockBeacon(int64(sel)), state.SampleBeacon)
	require.Equal(t, sel+f.listWindow, state.ListDeadlineHeight)
	require.Zero(t, state.OpeningDeadlineHeight, "the opening deadline is written at the draw")
	require.Zero(t, state.ProofDeadlineHeight)
	require.Equal(t, sel+f.listWindow+f.m+open+proof+verdict, state.VerdictDeadlineHeight)
	require.Empty(t, state.CommitmentListHash)
	require.Zero(t, state.ResultHeight)
	require.True(t, anchorAuditBinds(state), "a selected two-step audit binds before it has positions")

	due, queued := f.dueHeight(t)
	require.True(t, queued)
	require.Equal(t, state.ListDeadlineHeight+1, due)
	require.Equal(t, state.VerdictDeadlineHeight+1, f.settlementDeadline(t), "the settlement hold sits on the verdict deadline from selection on")

	_, err := f.settleNow(t, sel+2)
	require.ErrorContains(t, err, "pending until", "an anchor-only task awaiting its list must not settle unaudited")

	pending, err := (&queryServer{k: f.keeper}).AnchorAuditsPending(f.ctx, &types.QueryAnchorAuditsPendingRequest{})
	require.NoError(t, err)
	require.Len(t, pending.Audits, 1, "a Worker finds its selected task by polling the pending audits")
	require.Equal(t, anchorSamplingModeTwoStep, pending.Audits[0].SamplingMode)
	got, err := (&queryServer{k: f.keeper}).AnchorAudit(f.ctx, &types.QueryAnchorAuditRequest{TaskId: f.taskID})
	require.NoError(t, err)
	require.Equal(t, state.ListDeadlineHeight, got.Audit.ListDeadlineHeight)
}

func TestTwoStepSelectionEmitsItsEvent(t *testing.T) {
	f := newTwoStepFixture(t, 0)
	f.submitReceipt(t)
	h := f.window.SelectionRandomnessHeight
	ctx := sdk.UnwrapSDKContext(f.at(int64(h))).WithEventManager(sdk.NewEventManager())
	_, err := f.keeper.processVerifierSelectionRandomnessIndexWithBudget(ctx, h, 10, 1<<30)
	require.NoError(t, err)
	found := false
	for _, event := range ctx.EventManager().Events() {
		if event.Type == "task.v1.EventAnchorAuditSelected" {
			found = true
		}
	}
	require.True(t, found)
}

// The list window counts from the block that writes the selection, which is
// the first block in which the Worker can see it. EndBlock running late never
// shortens the Worker's time.
func TestTwoStepListWindowCountsFromTheBlockThatRecordsTheSelection(t *testing.T) {
	f := newTwoStepFixture(t, 0)
	f.submitReceipt(t)
	sel := f.window.SelectionRandomnessHeight
	late := sel + 7
	require.LessOrEqual(t, late, f.window.AssignmentDeadlineHeight)
	usage, err := f.keeper.processVerifierSelectionRandomnessIndexWithBudget(f.at(int64(late)), late, 10, 1<<30)
	require.NoError(t, err)
	require.Equal(t, uint64(1), usage.visited)
	state := f.audit(t)
	require.Equal(t, sel, state.SampleHeight, "the coin still comes from the frozen sampling height")
	require.Equal(t, late+f.listWindow, state.ListDeadlineHeight)
}

// An unselected task of a two-step Profile gets the row of an unselected
// one-step task at the same height, enters no queue and settles at once.
func TestTwoStepUnselectedTaskIsNotDelayed(t *testing.T) {
	f := newTwoStepFixture(t, 1_000_000)
	f.zeroAuditRate(t)
	f.submitReceipt(t)
	state := f.sampleRound(t)
	require.False(t, state.Selected)
	require.Equal(t, anchorSamplingModeTwoStep, state.SamplingMode, "the mode of the Profile is recorded")
	require.Zero(t, state.ListDeadlineHeight)
	require.Empty(t, state.Positions)
	require.False(t, anchorAuditBinds(state))
	require.False(t, isAnchorTwoStep(state), "no two-step code path may run for an unselected task")
	_, queued := f.dueHeight(t)
	require.False(t, queued)

	// The same task on a one-step Profile: identical row but for the mode.
	g := newAnchorOnlyFixture(t, 1_000_000, true)
	g.zeroAuditRate(t)
	g.submitReceipt(t)
	oneStep := g.sampleRound(t)
	twoStep := state
	twoStep.SamplingMode = 0
	twoStep.SampleBeacon, twoStep.AuditBasis = oneStep.SampleBeacon, oneStep.AuditBasis // the stubs serve different beacons
	require.Equal(t, oneStep, twoStep)

	_, err := f.server.SubmitAnchorCommitmentList(f.at(int64(state.SampleHeight+1)), f.listMsg(0x1a))
	require.ErrorContains(t, err, "not selected")
	settlement, err := f.settleNow(t, state.SampleHeight+1)
	require.NoError(t, err, "an unselected task settles right after sampling")
	f.requirePaidInFull(t, settlement)
}

// ---- list message --------------------------------------------------------

// The whole honest path: list, draw after it, opening, proof, PASS vote, pay.
func TestTwoStepHonestWorkerIsPaid(t *testing.T) {
	f := newTwoStepFixture(t, 4_000)
	state := f.selected(t)
	open, proof, _ := f.windows()
	listAt := state.SampleHeight + 3

	state = f.postList(t, listAt)
	require.Equal(t, listAt, state.CommitmentListHeight)
	require.Equal(t, bytes32(0x1a), state.CommitmentListHash)
	require.Equal(t, 32*f.rows, state.CommitmentListSizeBytes)
	require.Equal(t, "https://da.example/list", state.CommitmentListUri)
	require.Empty(t, state.Positions)
	due, queued := f.dueHeight(t)
	require.True(t, queued)
	require.Equal(t, listAt+f.m, due)

	// Nothing can be opened before the draw.
	_, err := f.server.SubmitAnchorOpening(f.at(int64(listAt+1)), f.openingMsg(types.AnchorAuditState{Positions: []uint64{0, 1}}, f.worker))
	require.ErrorContains(t, err, "not been drawn")
	require.Zero(t, f.sweep(t, listAt+f.m-1), "no draw before the last contributing height")
	require.Empty(t, f.audit(t).Positions)

	require.Equal(t, uint64(1), f.sweep(t, listAt+f.m))
	state = f.audit(t)
	require.Len(t, state.Positions, anchorSamplePositionsV0)
	require.Equal(t, listAt+f.m, state.PositionsHeight)
	require.Equal(t, state.PositionsHeight+open, state.OpeningDeadlineHeight, "the opening window starts at the draw")
	require.Equal(t, state.OpeningDeadlineHeight+proof, state.ProofDeadlineHeight)
	require.Less(t, state.CommitmentListHeight, state.PositionBeaconFirstHeight, "every contributing beacon is produced after the list")
	require.LessOrEqual(t, state.PositionBeaconFirstHeight, state.PositionBeaconLastHeight)
	require.LessOrEqual(t, state.PositionBeaconLastHeight, state.PositionsHeight)
	_, queued = f.dueHeight(t)
	require.False(t, queued)

	// A verifier recomputes the draw from chain data alone.
	var beacons [][]byte
	for h := state.PositionBeaconFirstHeight; h <= state.PositionBeaconLastHeight; h++ {
		beacons = append(beacons, testBlockBeacon(int64(h)))
	}
	chainID := sdkChainID(f.ctx)
	wantBeacon := anchorPositionBeacon(chainID, f.taskID, state.PositionBeaconFirstHeight, beacons)
	require.Equal(t, wantBeacon, state.PositionBeacon)
	require.Equal(t, anchorSamplePositionsV1(chainID, f.taskID, state.SampleBeacon, state.CommitmentListHash, wantBeacon, f.rows, anchorSamplePositionsV0), state.Positions)
	for _, p := range state.Positions {
		require.Less(t, p, f.rows)
	}

	f.openAndProveDrawn(t, state)
	voteAt := state.PositionsHeight + 3
	_, err = f.vote(0, f.valAcct[0], types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS, voteAt)
	require.NoError(t, err)
	resp, err := f.vote(1, f.valAcct[1], types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS, voteAt)
	require.NoError(t, err)
	require.True(t, resp.Finalized)
	settlement, err := f.settleNow(t, voteAt+1)
	require.NoError(t, err)
	f.requirePaidInFull(t, settlement)
}

// The positions depend on the recorded list: another list hash, same beacons,
// gives another draw. Nothing about the positions is fixed before the list is.
func TestTwoStepPositionsDependOnTheRecordedList(t *testing.T) {
	chain, task, sample, agg := "c", bytes32(1), bytes32(2), bytes32(3)
	a := anchorSamplePositionsV1(chain, task, sample, bytes32(4), agg, 1_000_000, 2)
	b := anchorSamplePositionsV1(chain, task, sample, bytes32(5), agg, 1_000_000, 2)
	c := anchorSamplePositionsV1(chain, task, sample, bytes32(4), bytes32(6), 1_000_000, 2)
	d := anchorSamplePositionsV1(chain, task, bytes32(7), bytes32(4), agg, 1_000_000, 2)
	require.NotEqual(t, a, b)
	require.NotEqual(t, a, c)
	require.NotEqual(t, a, d)
	require.NotEqual(t, a, anchorSamplePositions(task, sample, 1_000_000, 2), "the one-step draw is a different function")
	require.Equal(t, []uint64{0}, anchorSamplePositionsV1(chain, task, sample, bytes32(4), agg, 1, 2), "a single row yields one position, never a zero modulus")
}

// The same list again is a NOOP at every later stage; any other content is
// refused at every later stage. The first accepted list is final.
func TestTwoStepFirstListIsFinal(t *testing.T) {
	f := newTwoStepFixture(t, 0)
	state := f.selected(t)
	listAt := state.SampleHeight + 2
	recorded := f.postList(t, listAt)

	replay := func(height uint64) {
		t.Helper()
		msg := f.listMsg(0x1a)
		msg.ListUri = "https://da.example/another-location"
		resp, err := f.server.SubmitAnchorCommitmentList(f.at(int64(height)), msg)
		require.NoError(t, err)
		require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, resp.Status)
		require.Equal(t, recorded.PositionBeaconFirstHeight, resp.PositionBeaconFirstHeight)
		require.Equal(t, recorded.PositionBeaconLastHeight, resp.PositionBeaconLastHeight)
		now := f.audit(t)
		require.Equal(t, recorded.CommitmentListUri, now.CommitmentListUri, "the location of a repetition is not stored")
		require.Equal(t, recorded.CommitmentListHeight, now.CommitmentListHeight)
		require.Equal(t, recorded.PositionBeaconFirstHeight, now.PositionBeaconFirstHeight, "a repetition never moves the beacon window")
	}
	other := func(height uint64) {
		t.Helper()
		_, err := f.server.SubmitAnchorCommitmentList(f.at(int64(height)), f.listMsg(0x2b))
		require.ErrorContains(t, err, "first recorded list is final")
		require.Equal(t, bytes32(0x1a), f.audit(t).CommitmentListHash)
	}

	// Before the draw.
	replay(listAt)
	replay(listAt + 1)
	other(listAt + 1)
	// After the draw.
	require.Equal(t, uint64(1), f.sweep(t, recorded.PositionBeaconLastHeight))
	drawn := f.audit(t)
	replay(drawn.PositionsHeight + 1)
	other(drawn.PositionsHeight + 1)
	require.Equal(t, drawn.Positions, f.audit(t).Positions, "positions are drawn once per task")
	// After the list deadline.
	replay(drawn.ListDeadlineHeight + 5)
	other(drawn.ListDeadlineHeight + 5)
	// After the verdict is final.
	f.openAndProveDrawn(t, drawn)
	_, err := f.vote(0, f.valAcct[0], anchorReject, drawn.PositionsHeight+3)
	require.NoError(t, err)
	_, err = f.vote(1, f.valAcct[1], anchorReject, drawn.PositionsHeight+3)
	require.NoError(t, err)
	require.NotZero(t, f.audit(t).ResultHeight)
	replay(drawn.PositionsHeight + 4)
	other(drawn.PositionsHeight + 4)
}

func TestTwoStepListMessageIsRefused(t *testing.T) {
	f := newTwoStepFixture(t, 0)
	// No audit yet.
	_, err := f.server.SubmitAnchorCommitmentList(f.at(5), f.listMsg(0x1a))
	require.ErrorContains(t, err, "no anchor audit sample")
	state := f.selected(t)
	*f.hub.endpoint = "https://da.example/"
	state.DaUriPrefix = "https://da.example/"
	require.NoError(t, f.keeper.AnchorAudit.Set(f.ctx, f.taskKey, state))
	at := f.at(int64(state.SampleHeight + 1))

	for name, tc := range map[string]struct {
		mutate func(*types.MsgSubmitAnchorCommitmentList)
		want   string
	}{
		"a Builder or any other account": {func(m *types.MsgSubmitAnchorCommitmentList) { m.SubmitterAddress = f.operators[1] }, "not the winner Worker"},
		"a zero hash":                    {func(m *types.MsgSubmitAnchorCommitmentList) { m.ListHash = make([]byte, 32) }, "envelope is invalid"},
		"a short hash":                   {func(m *types.MsgSubmitAnchorCommitmentList) { m.ListHash = []byte{1, 2, 3} }, "envelope is invalid"},
		"a zero size":                    {func(m *types.MsgSubmitAnchorCommitmentList) { m.ListSizeBytes = 0 }, "envelope is invalid"},
		"a size that is not whole rows":  {func(m *types.MsgSubmitAnchorCommitmentList) { m.ListSizeBytes = 32*f.rows + 1 }, "multiple of 32"},
		"one row too few":                {func(m *types.MsgSubmitAnchorCommitmentList) { m.ListSizeBytes = 32 * (f.rows - 1) }, "rows"},
		"one row too many (one per token instead of one per row)": {func(m *types.MsgSubmitAnchorCommitmentList) { m.ListSizeBytes = 32 * (f.rows + 1) }, "rows"},
		"a location that is not a URL":                            {func(m *types.MsgSubmitAnchorCommitmentList) { m.ListUri = "ftp://da.example/list" }, "http or https"},
		"a location outside the published endpoint":               {func(m *types.MsgSubmitAnchorCommitmentList) { m.ListUri = "https://elsewhere.example/list" }, "data-availability endpoint"},
		"another task": {func(m *types.MsgSubmitAnchorCommitmentList) { m.TaskId = bytes32(0xee) }, "no anchor audit sample"},
	} {
		msg := f.listMsg(0x1a)
		tc.mutate(msg)
		_, err := f.server.SubmitAnchorCommitmentList(at, msg)
		require.ErrorContains(t, err, tc.want, name)
		require.Empty(t, f.audit(t).CommitmentListHash, name)
	}
	_, err = f.server.SubmitAnchorCommitmentList(at, nil)
	require.Error(t, err)

	// The honest message is accepted in the same state.
	_, err = f.server.SubmitAnchorCommitmentList(at, f.listMsg(0x1a))
	require.NoError(t, err)
}

// Inclusion at the boundary: the block of the list deadline is in time, the
// next block is not.
func TestTwoStepListDeadlineBoundary(t *testing.T) {
	f := newTwoStepFixture(t, 0)
	state := f.selected(t)
	_, err := f.server.SubmitAnchorCommitmentList(f.at(int64(state.ListDeadlineHeight+1)), f.listMsg(0x1a))
	require.ErrorContains(t, err, "deadline")
	require.Empty(t, f.audit(t).CommitmentListHash)

	recorded := f.postList(t, state.ListDeadlineHeight)
	// A list included in the very last block still gets a draw that leaves the
	// full opening, proof and verdict windows.
	open, proof, verdict := f.windows()
	require.Equal(t, uint64(1), f.sweep(t, recorded.PositionBeaconLastHeight))
	drawn := f.audit(t)
	require.NotEmpty(t, drawn.Positions)
	require.Equal(t, drawn.PositionsHeight+open+proof+verdict, drawn.VerdictDeadlineHeight, "the upper bound is exactly reached by the latest in-time list")
}

// ---- missing list --------------------------------------------------------

// No list by the deadline: the audit ends one block later as the Worker's
// omission, with the fault of a missing opening, applied once.
func TestTwoStepMissingListTakesTheMissingOpeningFault(t *testing.T) {
	const bond = uint64(4_000)
	f := newTwoStepFixture(t, bond)
	state := f.selected(t)

	require.Zero(t, f.sweep(t, state.ListDeadlineHeight), "nothing is due while the list can still be included")
	require.Zero(t, f.audit(t).ResultHeight)
	require.Equal(t, uint64(1), f.sweep(t, state.ListDeadlineHeight+1))
	final := f.audit(t)
	require.Equal(t, state.ListDeadlineHeight+1, final.ResultHeight)
	require.Equal(t, anchorReject, final.TaskVerdict)
	require.Equal(t, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_TIMEOUT, final.ResultSource)
	require.Equal(t, anchorListMissingReason, final.ResultReason)
	_, queued := f.dueHeight(t)
	require.False(t, queued)

	// Nothing is accepted after the outcome.
	_, err := f.server.SubmitAnchorCommitmentList(f.at(int64(final.ResultHeight)), f.listMsg(0x1a))
	require.ErrorContains(t, err, "already final")
	_, err = f.server.SubmitAnchorOpening(f.at(int64(final.ResultHeight)), f.openingMsg(types.AnchorAuditState{Positions: []uint64{0, 1}}, f.worker))
	require.ErrorContains(t, err, "already final")

	settlement, err := f.settleNow(t, final.ResultHeight)
	require.NoError(t, err, "the outcome is final, so the task settles without waiting for the verdict deadline")
	f.requireWorkerFaulted(t, settlement, hubtypes.FaultTypeAnchorTimeout, f.params.Verification.AnchorTimeoutSlashBps, bond)
	require.Len(t, f.audit(t).WorkerFaultId, types.Hash32Len)

	// One task is penalised at most once.
	_, err = f.settleNow(t, final.ResultHeight+1)
	require.Error(t, err)
	require.Len(t, *f.faults, 1)
}

// The missing-list fault is the missing-opening fault: same type, same share,
// same base. The two cannot drift apart because they are one code path.
func TestTwoStepMissingListEqualsTheMissingOpeningFault(t *testing.T) {
	const bond = uint64(4_000)
	missingList := newTwoStepFixture(t, bond)
	state := missingList.selected(t)
	require.Equal(t, uint64(1), missingList.sweep(t, state.ListDeadlineHeight+1))
	_, err := missingList.settleNow(t, state.ListDeadlineHeight+1)
	require.NoError(t, err)

	missingOpening := newAnchorOnlyFixture(t, bond, true)
	missingOpening.submitReceipt(t)
	missingOpening.sampleRound(t)
	missingOpening.settleAtDeadline(t)

	require.Len(t, *missingList.faults, 1)
	require.Len(t, *missingOpening.faults, 1)
	a, b := (*missingList.faults)[0], (*missingOpening.faults)[0]
	require.Equal(t, b.FaultType, a.FaultType)
	require.Equal(t, b.SlashBps, a.SlashBps)
	require.Equal(t, b.RequestedAmount, a.RequestedAmount)
	require.Equal(t, b.Duty, a.Duty)
	require.Equal(t, hubtypes.FaultTypeAnchorTimeout, a.FaultType, "a timeout slashes and does not jail")
}

// Settlement does not depend on the queue: when the visit after the list
// deadline has not happened, settlement reads the same outcome itself.
func TestTwoStepMissingListSettlesWithoutTheSweep(t *testing.T) {
	const bond = uint64(4_000)
	f := newTwoStepFixture(t, bond)
	state := f.selected(t)
	_, err := f.settleNow(t, state.ListDeadlineHeight)
	require.ErrorContains(t, err, "pending until", "at the deadline height the list can still be included")

	settlement, err := f.settleNow(t, state.ListDeadlineHeight+1)
	require.NoError(t, err)
	f.requireWorkerFaulted(t, settlement, hubtypes.FaultTypeAnchorTimeout, f.params.Verification.AnchorTimeoutSlashBps, bond)
	final := f.audit(t)
	require.Equal(t, anchorReject, final.TaskVerdict)
	require.Equal(t, anchorListMissingReason, final.ResultReason)
	require.Equal(t, state.ListDeadlineHeight+1, final.ResultHeight)

	// The queue row that is still there is retired without a second outcome.
	require.Equal(t, uint64(1), f.sweep(t, state.ListDeadlineHeight+2))
	require.Equal(t, final, f.audit(t))
	_, queued := f.dueHeight(t)
	require.False(t, queued)
	require.Len(t, *f.faults, 1)
}

// The verdict deadline imposes the same outcome on an audit nobody visited.
func TestTwoStepMissingListAtTheVerdictDeadline(t *testing.T) {
	const bond = uint64(4_000)
	f := newTwoStepFixture(t, bond)
	f.selected(t)
	settlement := f.settleAtDeadline(t)
	f.requireWorkerFaulted(t, settlement, hubtypes.FaultTypeAnchorTimeout, f.params.Verification.AnchorTimeoutSlashBps, bond)
}

// ---- outcomes after the draw are the existing ones -------------------------

func TestTwoStepRejectByVoteSlashesAndJails(t *testing.T) {
	const bond = uint64(4_000)
	f := newTwoStepFixture(t, bond)
	state := f.selected(t)
	drawn := f.drawn(t, state.SampleHeight+1)
	f.openAndProveDrawn(t, drawn)
	voteAt := drawn.PositionsHeight + 3
	_, err := f.vote(0, f.valAcct[0], anchorReject, voteAt)
	require.NoError(t, err)
	_, err = f.vote(1, f.valAcct[1], anchorReject, voteAt)
	require.NoError(t, err)
	settlement, err := f.settleNow(t, voteAt+1)
	require.NoError(t, err)
	f.requireWorkerFaulted(t, settlement, hubtypes.FaultTypeAnchorReject, f.params.Verification.AnchorRejectSlashBps, bond)
}

func TestTwoStepListPostedButNoOpeningTakesTheTimeoutFault(t *testing.T) {
	const bond = uint64(4_000)
	f := newTwoStepFixture(t, bond)
	state := f.selected(t)
	drawn := f.drawn(t, state.SampleHeight+1)
	_, err := f.server.SubmitAnchorOpening(f.at(int64(drawn.OpeningDeadlineHeight+1)), f.openingMsg(drawn, f.worker))
	require.ErrorContains(t, err, "deadline")
	_, err = f.settleNow(t, drawn.VerdictDeadlineHeight-1)
	require.ErrorContains(t, err, "pending until")
	settlement := f.settleAtDeadline(t)
	f.requireWorkerFaulted(t, settlement, hubtypes.FaultTypeAnchorTimeout, f.params.Verification.AnchorTimeoutSlashBps, bond)
	require.Contains(t, f.audit(t).ResultReason, "did not open")
}

func TestTwoStepOpenedButNoProofTakesTheTimeoutFault(t *testing.T) {
	const bond = uint64(4_000)
	f := newTwoStepFixture(t, bond)
	state := f.selected(t)
	drawn := f.drawn(t, state.SampleHeight+1)
	_, err := f.server.SubmitAnchorOpening(f.at(int64(drawn.OpeningDeadlineHeight)), f.openingMsg(drawn, f.worker))
	require.NoError(t, err, "the last block of the opening window is in time")
	settlement := f.settleAtDeadline(t)
	f.requireWorkerFaulted(t, settlement, hubtypes.FaultTypeAnchorTimeout, f.params.Verification.AnchorTimeoutSlashBps, bond)
	require.Contains(t, f.audit(t).ResultReason, "did not prove")
}

func TestTwoStepSilentValidatorsPayTheWorker(t *testing.T) {
	f := newTwoStepFixture(t, 4_000)
	state := f.selected(t)
	drawn := f.drawn(t, state.SampleHeight+1)
	f.openAndProveDrawn(t, drawn)
	f.requireNoVerdictPaid(t)
}

func TestTwoStepUnavailableMaterialKeepsItsRule(t *testing.T) {
	const bond = uint64(4_000)
	f := newTwoStepFixture(t, bond)
	state := f.selected(t)
	drawn := f.drawn(t, state.SampleHeight+1)
	f.openAndProveDrawn(t, drawn)
	late := anchorUnavailableVoteOpenHeight(f.audit(t))
	require.NotZero(t, late)
	for i := 0; i < 2; i++ {
		_, err := f.vote(i, f.valAcct[i], types.AnchorVerdictV1_ANCHOR_VERDICT_V1_UNAVAILABLE, late)
		require.NoError(t, err)
	}
	f.requireMaterialUnavailable(t, bond)
}

// The opening must name exactly the drawn positions.
func TestTwoStepOpeningMustNameTheDrawnPositions(t *testing.T) {
	f := newTwoStepFixture(t, 0)
	state := f.selected(t)
	drawn := f.drawn(t, state.SampleHeight+1)
	wrong := drawn
	wrong.Positions = []uint64{drawn.Positions[0], drawn.Positions[1] + 1}
	_, err := f.server.SubmitAnchorOpening(f.at(int64(drawn.PositionsHeight+1)), f.openingMsg(wrong, f.worker))
	require.ErrorContains(t, err, "do not match")
}

// ---- honest-Worker safety: the chain's own omissions -----------------------

// A contributing beacon is missing, or is a block-hash placeholder: no
// position is drawn from that window, a fresh window of the same length
// follows, and the Worker's windows count from the draw that then succeeds.
func TestTwoStepBadBeaconRestartsTheWindow(t *testing.T) {
	for name, spoil := range map[string]func(f *twoStepFixture, height int64){
		"missing beacon":     func(f *twoStepFixture, height int64) { f.hub.missing[height] = true },
		"placeholder beacon": func(f *twoStepFixture, height int64) { f.hub.placeholder[height] = true },
	} {
		t.Run(name, func(t *testing.T) {
			f := newTwoStepFixture(t, 4_000)
			state := f.selected(t)
			listAt := state.SampleHeight + 2
			recorded := f.postList(t, listAt)
			spoil(f, int64(recorded.PositionBeaconFirstHeight+1))

			require.Equal(t, uint64(1), f.sweep(t, recorded.PositionBeaconLastHeight))
			restarted := f.audit(t)
			require.Empty(t, restarted.Positions, "no position is drawn from a window with a bad beacon")
			require.Zero(t, restarted.ResultHeight, "a bad beacon never ends the audit")
			require.Equal(t, recorded.PositionBeaconLastHeight+1, restarted.PositionBeaconFirstHeight, "the fresh window starts after the current block")
			require.Equal(t, recorded.PositionBeaconLastHeight+f.m, restarted.PositionBeaconLastHeight)
			require.Equal(t, recorded.CommitmentListHash, restarted.CommitmentListHash, "the list stays as recorded")
			require.Equal(t, recorded.CommitmentListHeight, restarted.CommitmentListHeight)
			require.Equal(t, recorded.VerdictDeadlineHeight, restarted.VerdictDeadlineHeight, "the verdict deadline never moves")
			due, queued := f.dueHeight(t)
			require.True(t, queued)
			require.Equal(t, restarted.PositionBeaconLastHeight, due)
			require.Empty(t, *f.faults)

			require.Equal(t, uint64(1), f.sweep(t, restarted.PositionBeaconLastHeight))
			drawn := f.audit(t)
			require.NotEmpty(t, drawn.Positions)
			open, _, _ := f.windows()
			require.Equal(t, restarted.PositionBeaconLastHeight, drawn.PositionsHeight)
			require.Equal(t, drawn.PositionsHeight+open, drawn.OpeningDeadlineHeight, "a late draw does not shorten the opening window")
			var beacons [][]byte
			for h := drawn.PositionBeaconFirstHeight; h <= drawn.PositionBeaconLastHeight; h++ {
				beacons = append(beacons, testBlockBeacon(int64(h)))
			}
			require.Equal(t, anchorPositionBeacon(sdkChainID(f.ctx), f.taskID, drawn.PositionBeaconFirstHeight, beacons), drawn.PositionBeacon,
				"only beacons of the window that succeeded enter the draw")

			f.openAndProveDrawn(t, drawn)
			_, err := f.vote(0, f.valAcct[0], types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS, drawn.PositionsHeight+3)
			require.NoError(t, err)
			_, err = f.vote(1, f.valAcct[1], types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS, drawn.PositionsHeight+3)
			require.NoError(t, err)
			settlement, err := f.settleNow(t, drawn.PositionsHeight+4)
			require.NoError(t, err)
			f.requirePaidInFull(t, settlement)
		})
	}
}

// The chain never produces a usable beacon again. The window restarts while a
// later draw would still leave the full opening, proof and verdict windows;
// after that the audit ends NO_VERDICT. The Worker is paid and never faulted.
func TestTwoStepBeaconsNeverArrivePaysTheWorker(t *testing.T) {
	f := newTwoStepFixture(t, 4_000)
	state := f.selected(t)
	listAt := state.SampleHeight + 2
	recorded := f.postList(t, listAt)
	*f.hub.missingFrom = int64(recorded.PositionBeaconFirstHeight)
	open, proof, verdict := f.windows()

	restarts := 0
	for height := recorded.PositionBeaconLastHeight; ; {
		require.Equal(t, uint64(1), f.sweep(t, height))
		now := f.audit(t)
		require.Empty(t, now.Positions)
		if now.ResultHeight != 0 {
			break
		}
		restarts++
		require.LessOrEqual(t, now.PositionBeaconLastHeight+open+proof+verdict, now.VerdictDeadlineHeight,
			"a restarted window is only opened if its draw would still leave every window its full length")
		height = now.PositionBeaconLastHeight
		require.Less(t, restarts, 1000)
	}
	// The slack is the part of the list window the Worker did not use.
	require.Equal(t, int((state.ListDeadlineHeight-listAt)/f.m), restarts)
	final := f.audit(t)
	require.Equal(t, anchorNoVerdict, final.TaskVerdict)
	require.Equal(t, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_TIMEOUT, final.ResultSource)
	require.Contains(t, final.ResultReason, anchorPositionsMissingReson)
	_, queued := f.dueHeight(t)
	require.False(t, queued)
	settlement, err := f.settleNow(t, final.ResultHeight)
	require.NoError(t, err)
	f.requirePaidInFull(t, settlement)
	require.Empty(t, *f.faults)
}

// EndBlock visits the audit late (blocks were full, or the queue was behind).
// A late draw that still fits is made, and the Worker's windows count from it.
func TestTwoStepLateDrawKeepsTheWorkersWindows(t *testing.T) {
	f := newTwoStepFixture(t, 0)
	state := f.selected(t)
	listAt := state.SampleHeight + 2
	recorded := f.postList(t, listAt)
	late := recorded.PositionBeaconLastHeight + 9
	require.Equal(t, uint64(1), f.sweep(t, late))
	drawn := f.audit(t)
	open, proof, verdict := f.windows()
	require.NotEmpty(t, drawn.Positions)
	require.Equal(t, late, drawn.PositionsHeight)
	require.Equal(t, late+open, drawn.OpeningDeadlineHeight)
	require.Equal(t, late+open+proof, drawn.ProofDeadlineHeight)
	require.LessOrEqual(t, drawn.ProofDeadlineHeight+verdict, drawn.VerdictDeadlineHeight, "the validators keep their full voting window")
	require.Equal(t, recorded.PositionBeaconFirstHeight, drawn.PositionBeaconFirstHeight, "a late visit draws from the heights fixed by the list, not from later ones")

	// An opening in the last block of the window counted from the late draw is in time.
	_, err := f.server.SubmitAnchorOpening(f.at(int64(drawn.OpeningDeadlineHeight)), f.openingMsg(drawn, f.worker))
	require.NoError(t, err)
}

// A draw that could no longer leave the full windows is not made: the audit
// ends NO_VERDICT instead of handing the Worker shortened deadlines.
func TestTwoStepDrawThatWouldShortenTheWindowsIsNotMade(t *testing.T) {
	f := newTwoStepFixture(t, 4_000)
	state := f.selected(t)
	recorded := f.postList(t, state.ListDeadlineHeight) // no slack at all
	require.Equal(t, uint64(1), f.sweep(t, recorded.PositionBeaconLastHeight+1))
	final := f.audit(t)
	require.Empty(t, final.Positions)
	require.Equal(t, anchorNoVerdict, final.TaskVerdict)
	settlement, err := f.settleNow(t, final.ResultHeight)
	require.NoError(t, err)
	f.requirePaidInFull(t, settlement)
	require.Empty(t, *f.faults)
}

// The queue never visits the audit after the list (a stalled sweep). At the
// verdict deadline the audit ends NO_VERDICT: the chain's omission.
func TestTwoStepListRecordedAndNeverDrawnPaysTheWorker(t *testing.T) {
	f := newTwoStepFixture(t, 4_000)
	state := f.selected(t)
	f.postList(t, state.SampleHeight+1)
	_, err := f.settleNow(t, state.VerdictDeadlineHeight-1)
	require.ErrorContains(t, err, "pending until")
	settlement := f.settleAtDeadline(t)
	f.requirePaidInFull(t, settlement)
	require.Empty(t, *f.faults)
	final := f.audit(t)
	require.Equal(t, anchorNoVerdict, final.TaskVerdict)
	require.Equal(t, anchorPositionsMissingReson, final.ResultReason)
}

// Every deadline is a block height. A halted or slow chain (any wall-clock gap
// between two heights) changes no outcome.
func TestTwoStepDeadlinesAreCountedInBlocks(t *testing.T) {
	f := newTwoStepFixture(t, 0)
	state := f.selected(t)
	afterHalt := func(height uint64) context.Context {
		return sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.at(int64(height))).WithBlockTime(time.Unix(4_000_000_000, 0)))
	}
	resp, err := f.server.SubmitAnchorCommitmentList(afterHalt(state.ListDeadlineHeight), f.listMsg(0x1a))
	require.NoError(t, err, "three days of halt between two blocks do not consume the list window")
	visited, err := f.keeper.processAnchorSamplingDue(afterHalt(resp.PositionBeaconLastHeight), resp.PositionBeaconLastHeight, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	drawn := f.audit(t)
	require.NotEmpty(t, drawn.Positions)
	_, err = f.server.SubmitAnchorOpening(afterHalt(drawn.OpeningDeadlineHeight), f.openingMsg(drawn, f.worker))
	require.NoError(t, err)
}

// The locked Profile cannot be read at sampling: the chain cannot tell which
// flow the Worker runs, so it writes no selected audit. The anchor-only task
// fails closed and is refunded; the Worker is not faulted.
func TestTwoStepUnreadableProfileWritesNoAuditAndNoFault(t *testing.T) {
	f := newTwoStepFixture(t, 4_000)
	f.submitReceipt(t)
	// The Profile loses its anchor requirement between the receipt and sampling.
	f.anchorOnlyFixture.anchorFixture.hub.profile.ExecutionSnapshot.VerificationProfile.EvidenceSchema = shared.NewPhase0WorkerEvidenceSchemaV1(1<<30, 1<<26)
	h := f.window.SelectionRandomnessHeight
	_, err := f.keeper.processVerifierSelectionRandomnessIndexWithBudget(f.at(int64(h)), h, 10, 1<<30)
	require.NoError(t, err)
	_, err = f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.Error(t, err, "no audit row is written on a guess")
	require.Empty(t, *f.faults)
}

// ---- EndBlock ------------------------------------------------------------

// One bad row never returns an error from the queue and never blocks the rows
// behind it.
func TestTwoStepQueueSurvivesBadRows(t *testing.T) {
	f := newTwoStepFixture(t, 0)
	state := f.selected(t)
	recorded := f.postList(t, state.SampleHeight+1)

	// A row for a task that has no audit, due earlier than the real one.
	ghost := types.NewTaskKey(bytes32(0x01))
	require.NoError(t, f.keeper.setAnchorSamplingDue(f.ctx, ghost, 5))
	// A row whose by-task pointer is gone.
	orphan := types.NewTaskKey(bytes32(0x02))
	require.NoError(t, f.keeper.AnchorSamplingDueIndex.Set(f.ctx, types.NewDeadlineIndexKey(6, orphan)))
	// A row of a one-step audit that should never have been queued.
	legacy := types.NewTaskKey(bytes32(0x03))
	require.NoError(t, f.keeper.AnchorAudit.Set(f.ctx, legacy, types.AnchorAuditState{TaskId: bytes32(0x03), Sampled: true, Selected: true, Positions: []uint64{1}, VerdictDeadlineHeight: 9_999}))
	require.NoError(t, f.keeper.setAnchorSamplingDue(f.ctx, legacy, 7))

	require.Equal(t, uint64(4), f.sweep(t, recorded.PositionBeaconLastHeight), "the three bad rows and the real one are each visited once")
	require.NotEmpty(t, f.audit(t).Positions, "the audit behind the bad rows is drawn in the same block")
	iter, err := f.keeper.AnchorSamplingDueIndex.Iterate(f.ctx, nil)
	require.NoError(t, err)
	defer iter.Close()
	require.False(t, iter.Valid(), "every visited row left the queue")
	untouched, err := f.keeper.AnchorAudit.Get(f.ctx, legacy)
	require.NoError(t, err)
	require.Equal(t, []uint64{1}, untouched.Positions)
	require.Zero(t, untouched.ResultHeight)
}

// The per-block limit bounds the visited rows; the rest wait for later blocks.
func TestTwoStepQueueIsBounded(t *testing.T) {
	f := newTwoStepFixture(t, 0)
	for i := byte(1); i <= 5; i++ {
		key := types.NewTaskKey(bytes32(i))
		require.NoError(t, f.keeper.AnchorAudit.Set(f.ctx, key, types.AnchorAuditState{
			TaskId: bytes32(i), Sampled: true, Selected: true, AuditBasis: "x", SamplingMode: anchorSamplingModeTwoStep,
			ListDeadlineHeight: 10, VerdictDeadlineHeight: 10_000,
		}))
		require.NoError(t, f.keeper.setAnchorSamplingDue(f.ctx, key, 11))
	}
	visited, err := f.keeper.processAnchorSamplingDue(f.at(20), 20, 2)
	require.NoError(t, err)
	require.Equal(t, uint64(2), visited)
	left := 0
	iter, err := f.keeper.AnchorSamplingDueIndex.Iterate(f.ctx, nil)
	require.NoError(t, err)
	for ; iter.Valid(); iter.Next() {
		left++
	}
	iter.Close()
	require.Equal(t, 3, left)
}

// The whole EndBlock schedule runs the queue and stays clean around it.
func TestTwoStepRunsInsideTheEndBlockSchedule(t *testing.T) {
	f := newTwoStepFixture(t, 0)
	state := f.selected(t)
	recorded := f.postList(t, state.SampleHeight+1)
	for height := recorded.CommitmentListHeight; height < recorded.PositionBeaconLastHeight; height++ {
		result, err := f.keeper.RunEndBlockDeadlineSweeps(f.at(int64(height)), height, 10)
		require.NoError(t, err)
		require.Zero(t, result.AnchorSampling)
	}
	result, err := f.keeper.RunEndBlockDeadlineSweeps(f.at(int64(recorded.PositionBeaconLastHeight)), recorded.PositionBeaconLastHeight, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(1), result.AnchorSampling)
	require.NotEmpty(t, f.audit(t).Positions)
	result, err = f.keeper.RunEndBlockDeadlineSweeps(f.at(int64(recorded.PositionBeaconLastHeight+1)), recorded.PositionBeaconLastHeight+1, 10)
	require.NoError(t, err)
	require.Zero(t, result.AnchorSampling)
}

func TestTwoStepEventsAreEmitted(t *testing.T) {
	f := newTwoStepFixture(t, 0)
	state := f.selected(t)
	count := func(ctx sdk.Context, name string) int {
		n := 0
		for _, event := range ctx.EventManager().Events() {
			if event.Type == name {
				n++
			}
		}
		return n
	}
	listAt := state.SampleHeight + 1
	ctx := sdk.UnwrapSDKContext(f.at(int64(listAt))).WithEventManager(sdk.NewEventManager())
	_, err := f.server.SubmitAnchorCommitmentList(ctx, f.listMsg(0x1a))
	require.NoError(t, err)
	require.Equal(t, 1, count(ctx, "task.v1.EventAnchorCommitmentListRecorded"))
	_, err = f.server.SubmitAnchorCommitmentList(ctx, f.listMsg(0x1a))
	require.NoError(t, err)
	require.Equal(t, 1, count(ctx, "task.v1.EventAnchorCommitmentListRecorded"), "a repetition emits nothing")

	ctx = sdk.UnwrapSDKContext(f.at(int64(listAt + f.m))).WithEventManager(sdk.NewEventManager())
	_, err = f.keeper.processAnchorSamplingDue(ctx, listAt+f.m, 10)
	require.NoError(t, err)
	require.Equal(t, 1, count(ctx, "task.v1.EventAnchorPositionsDrawn"))
}

// ---- parameters ------------------------------------------------------------

func TestTwoStepParamsValidation(t *testing.T) {
	base := types.DefaultTaskParams()
	require.Zero(t, base.Verification.AnchorListWindowBlocks, "two-step sampling is off by default")
	require.Zero(t, base.Verification.AnchorPositionBeaconBlocks)
	require.NoError(t, base.Validate())
	require.False(t, base.Verification.AnchorTwoStepConfigured())
	with := func(window uint64, beacons uint32) error {
		p := base
		p.Verification.AnchorListWindowBlocks, p.Verification.AnchorPositionBeaconBlocks = window, beacons
		return p.Validate()
	}
	require.NoError(t, with(types.RecommendedAnchorListWindowBlocks, types.RecommendedAnchorPositionBeaconBlocks))
	require.NoError(t, with(1, 2))
	require.NoError(t, with(1, 256))
	require.Error(t, with(1, 0), "a list window without a beacon count could not draw")
	require.Error(t, with(1, 1), "one beacon is known to the proposer of the next block")
	require.Error(t, with(1, 257))
	require.Error(t, with(0, 8), "a beacon count without a list window does nothing")
	require.Error(t, with(types.AnchorWindowBlocksMaximum+1, 8))

	// The retention check covers the longer audit.
	p := base
	before := p.Verification.AnchorTotalWindowBlocks()
	p.Verification.AnchorListWindowBlocks, p.Verification.AnchorPositionBeaconBlocks = 1_800, 8
	require.Equal(t, before+1_808, p.Verification.AnchorTotalWindowBlocks())
	p.Cleanup.TaskTerminalSummaryRetentionBlocks = p.Verification.AnchorTotalWindowBlocks()
	require.Error(t, p.Validate(), "the audit must end before the task's summary is pruned")

	// Both fields are part of the parameter commitment.
	h0, err := types.TaskParamsHashV1("c", 1, base)
	require.NoError(t, err)
	q := base
	q.Verification.AnchorListWindowBlocks, q.Verification.AnchorPositionBeaconBlocks = 1_800, 8
	h1, err := types.TaskParamsHashV1("c", 1, q)
	require.NoError(t, err)
	q.Verification.AnchorPositionBeaconBlocks = 9
	h2, err := types.TaskParamsHashV1("c", 1, q)
	require.NoError(t, err)
	require.NotEqual(t, h0, h1)
	require.NotEqual(t, h1, h2)
}

// The historical Default* anchor parameters are untouched by this change.
func TestTwoStepLeavesTheDefaultAnchorParametersAlone(t *testing.T) {
	v := types.DefaultTaskParams().Verification
	require.Equal(t, uint64(900), v.AnchorOpenWindowBlocks)
	require.Equal(t, uint64(6_600), v.AnchorProofWindowBlocks)
	require.Equal(t, uint64(900), v.AnchorVerdictWindowBlocks)
	require.Equal(t, uint32(2_500), v.AnchorRejectSlashBps)
	require.Equal(t, uint32(500), v.AnchorTimeoutSlashBps)
	require.Equal(t, uint32(500), v.AnchorAuditMinRateBps)
	require.Equal(t, uint32(10_000), v.AnchorAuditRiskMarginBps)
	require.Equal(t, uint64(1_800), types.RecommendedAnchorListWindowBlocks)
	require.Equal(t, uint32(8), types.RecommendedAnchorPositionBeaconBlocks)
}

// A task of a two-step Profile whose frozen parameters carry no list window is
// still sampled in two steps, with the recommended values: it never falls back
// to drawing positions before a list exists.
func TestTwoStepFailsClosedWithoutParameters(t *testing.T) {
	f := newTwoStepFixture(t, 0)
	f.params.Verification.AnchorListWindowBlocks, f.params.Verification.AnchorPositionBeaconBlocks = 0, 0
	require.NoError(t, f.keeper.Params.Set(f.ctx, f.params))
	state := f.selected(t)
	require.Empty(t, state.Positions)
	require.Equal(t, state.SampleHeight+types.RecommendedAnchorListWindowBlocks, state.ListDeadlineHeight)
	resp, err := f.server.SubmitAnchorCommitmentList(f.at(int64(state.SampleHeight+1)), f.listMsg(0x1a))
	require.NoError(t, err)
	require.Equal(t, state.SampleHeight+1+uint64(types.RecommendedAnchorPositionBeaconBlocks), resp.PositionBeaconLastHeight)
}

// ---- legacy unchanged, upgrade, genesis, cleanup ---------------------------

// A Profile of anchor commitment schema version 1 is sampled exactly as before
// this change: the row equals, field for field and byte for byte, the row the
// one-step code writes, nothing is queued, and the list message is refused.
func TestOneStepSamplingIsUnchanged(t *testing.T) {
	f := newAnchorOnlyFixture(t, 0, true)
	f.params.Verification.AnchorListWindowBlocks = twoStepListWindow // set on the chain, not used by this Profile
	f.params.Verification.AnchorPositionBeaconBlocks = twoStepBeacons
	require.NoError(t, f.keeper.Params.Set(f.ctx, f.params))
	f.submitReceipt(t)
	h := f.window.SelectionRandomnessHeight
	ctx := sdk.UnwrapSDKContext(f.at(int64(h))).WithEventManager(sdk.NewEventManager())
	_, err := f.keeper.processVerifierSelectionRandomnessIndexWithBudget(ctx, h, 10, 1<<30)
	require.NoError(t, err)
	state, err := f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.NoError(t, err)

	v := f.params.Verification
	rows := anchorOnlyPrompt + anchorOnlyGenerated - 1
	draw := f.keeper.anchorAuditDraw(f.ctx, f.taskKey, f.worker, f.beacon, f.params)
	want := types.AnchorAuditState{
		TaskId: f.taskID, Sampled: true, SampleBeacon: f.beacon, SampleHeight: h, WinnerWorker: f.worker,
		SelectedVerifiers:     []string{},
		OpeningDeadlineHeight: h + v.AnchorOpenWindowBlocks,
		ProofDeadlineHeight:   h + v.AnchorOpenWindowBlocks + v.AnchorProofWindowBlocks,
		VerdictDeadlineHeight: h + v.AnchorOpenWindowBlocks + v.AnchorProofWindowBlocks + v.AnchorVerdictWindowBlocks,
		AuditRateBps:          draw.rate, Selected: true, AuditBasis: draw.basis, SlashableBond: draw.bond,
		Positions: anchorSamplePositions(f.taskID, f.beacon, rows, anchorSamplePositionsV0),
	}
	wantBytes, err := want.Marshal()
	require.NoError(t, err)
	gotBytes, err := state.Marshal()
	require.NoError(t, err)
	require.Equal(t, wantBytes, gotBytes, "the stored row of a one-step audit is byte for byte the previous one")
	require.Zero(t, state.SamplingMode)

	for _, event := range ctx.EventManager().Events() {
		require.NotContains(t, event.Type, "EventAnchorAuditSelected", "one-step sampling emits no new event")
	}
	iter, err := f.keeper.AnchorSamplingDueIndex.Iterate(f.ctx, nil)
	require.NoError(t, err)
	require.False(t, iter.Valid(), "a one-step audit never enters the sampling queue")
	iter.Close()
	_, err = f.server.SubmitAnchorCommitmentList(f.at(int64(h+1)), &types.MsgSubmitAnchorCommitmentList{
		TaskId: f.taskID, ListHash: bytes32(0x1a), ListSizeBytes: 32 * rows, ListUri: "https://da.example/list", SubmitterAddress: f.worker})
	require.ErrorContains(t, err, "one-step sampling")

	// The rest of the flow is the existing one.
	f.openAndProveSampled(t, state)
	_, err = f.vote(0, f.valAcct[0], types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS, h+3)
	require.NoError(t, err)
	_, err = f.vote(1, f.valAcct[1], types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS, h+3)
	require.NoError(t, err)
	settlement, err := f.settleNow(t, h+4)
	require.NoError(t, err)
	f.requirePaidInFull(t, settlement)
}

// Audits in flight across the upgrade were written by the previous binary and
// carry no sampling mode. They finish on the one-step path: the opening is
// accepted against their positions, the list message is refused, the predicates
// read them as before, and importing them queues nothing.
func TestAuditsInFlightAcrossTheUpgradeStayOneStep(t *testing.T) {
	f := newAnchorFixture(t, 0, anchorGoodTokenSize)
	old := types.AnchorAuditState{
		TaskId: f.taskID, Sampled: true, Selected: true, AuditRateBps: 10_000, AuditBasis: "order 1000; ...",
		Positions: []uint64{3, 9}, SampleBeacon: f.beacon, SampleHeight: 100, WinnerWorker: f.worker,
		OpeningDeadlineHeight: 1_000, ProofDeadlineHeight: 7_600, VerdictDeadlineHeight: 8_500,
	}
	require.NoError(t, f.keeper.AnchorAudit.Set(f.ctx, f.taskKey, old))
	require.True(t, anchorAuditBinds(old))
	require.False(t, isAnchorTwoStep(old))
	require.Zero(t, anchorSamplingDueHeightOf(old))

	_, err := f.server.SubmitAnchorCommitmentList(f.at(101), &types.MsgSubmitAnchorCommitmentList{
		TaskId: f.taskID, ListHash: bytes32(0x1a), ListSizeBytes: 32 * 14, ListUri: "https://da.example/list", SubmitterAddress: f.worker})
	require.ErrorContains(t, err, "one-step sampling")
	_, err = f.server.SubmitAnchorOpening(f.at(101), f.openingMsg(old, f.worker))
	require.NoError(t, err)

	// A selected one-step audit without positions still never binds (the rule
	// that existed before two-step sampling extended the predicate).
	noRows := old
	noRows.Positions = nil
	require.False(t, anchorAuditBinds(noRows))
	verdict, _ := anchorTimeoutVerdict(noRows)
	require.Equal(t, anchorNoVerdict, verdict)

	// Import of pre-upgrade rows.
	g := newAnchorFixture(t, 0, anchorGoodTokenSize)
	require.NoError(t, g.keeper.initGenesisAnchor(g.ctx, types.GenesisState{AnchorAudits: []types.AnchorAuditState{old}}))
	iter, err := g.keeper.AnchorSamplingDueIndex.Iterate(g.ctx, nil)
	require.NoError(t, err)
	require.False(t, iter.Valid())
	iter.Close()
}

// Export and import carry the new fields inside the audit rows and rebuild the
// queue from them, in each of the two waiting stages.
func TestTwoStepGenesisRoundTripRebuildsTheQueue(t *testing.T) {
	f := newTwoStepFixture(t, 0)
	awaitingList := f.selected(t)

	awaitingDraw := awaitingList
	awaitingDraw.TaskId = bytes32(0x99)
	awaitingDraw.CommitmentListHash, awaitingDraw.CommitmentListSizeBytes = bytes32(0x1a), 32*f.rows
	awaitingDraw.CommitmentListUri, awaitingDraw.CommitmentListHeight = "https://da.example/list", awaitingList.SampleHeight+1
	awaitingDraw.PositionBeaconFirstHeight, awaitingDraw.PositionBeaconLastHeight = awaitingList.SampleHeight+2, awaitingList.SampleHeight+1+f.m
	final := awaitingList
	final.TaskId = bytes32(0x98)
	final.ResultHeight, final.TaskVerdict = 77, anchorReject

	// ExportGenesis writes anchor_audits from exactly this read.
	exported, err := collectMapValues(f.ctx, f.keeper.AnchorAudit)
	require.NoError(t, err)
	require.Len(t, exported, 1)
	require.Equal(t, awaitingList, exported[0], "the new fields travel inside the audit rows")

	fresh := newAnchorFixture(t, 0, anchorGoodTokenSize)
	audits := []types.AnchorAuditState{awaitingList, awaitingDraw, final}
	require.NoError(t, fresh.keeper.initGenesisAnchor(fresh.ctx, types.GenesisState{AnchorAudits: audits}))
	due := map[string]uint64{}
	iter, err := fresh.keeper.AnchorSamplingDueIndex.Iterate(fresh.ctx, nil)
	require.NoError(t, err)
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		require.NoError(t, err)
		due[hex.EncodeToString(key.K2())] = key.K1()
	}
	iter.Close()
	require.Equal(t, map[string]uint64{
		hex.EncodeToString(awaitingList.TaskId): awaitingList.ListDeadlineHeight + 1,
		hex.EncodeToString(awaitingDraw.TaskId): awaitingDraw.PositionBeaconLastHeight,
	}, due, "an audit awaiting its list is due after the list deadline, one awaiting its draw at its last contributing height, a final one not at all")

	again, err := collectMapValues(fresh.ctx, fresh.keeper.AnchorAudit)
	require.NoError(t, err)
	require.ElementsMatch(t, audits, again)
}

// A genesis written before the fields existed (all zero) imports cleanly.
func TestTwoStepGenesisWithoutTheFieldsImports(t *testing.T) {
	f := newAnchorFixture(t, 0, anchorGoodTokenSize)
	require.NoError(t, f.keeper.initGenesisAnchor(f.ctx, types.GenesisState{AnchorAudits: []types.AnchorAuditState{
		{TaskId: bytes32(0x31), Sampled: true, Selected: false, AuditRateBps: 500, AuditBasis: "b", VerdictDeadlineHeight: 9},
		{TaskId: bytes32(0x32), Sampled: true}, // a pre-rate record
	}}))
	iter, err := f.keeper.AnchorSamplingDueIndex.Iterate(f.ctx, nil)
	require.NoError(t, err)
	require.False(t, iter.Valid())
	iter.Close()
}

// Cleanup retires the queue row together with the audit.
func TestTwoStepCleanupRemovesTheQueueRow(t *testing.T) {
	f := newTwoStepFixture(t, 0)
	f.selected(t)
	_, queued := f.dueHeight(t)
	require.True(t, queued)
	for i := 0; i < 5; i++ {
		changed, err := f.keeper.cleanupAnchorAudit(f.ctx, f.taskKey)
		require.NoError(t, err)
		if changed == 0 {
			break
		}
	}
	_, queued = f.dueHeight(t)
	require.False(t, queued)
	_, err := f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.Error(t, err)
}

// On the Verifier path (a Profile with Verifiers and the anchor requirement)
// the same sampling call applies the same two-step selection.
func TestTwoStepOnTheVerifierPath(t *testing.T) {
	f := newAnchorFixture(t, 0, anchorGoodTokenSize)
	hub := f.keeper.hubKeeper.(anchorHubStub)
	hub.anchorSchema = shared.WorkerAnchorCommitmentSchemaVersionV2
	f.keeper.hubKeeper = hub
	f.params.Verification.AnchorListWindowBlocks, f.params.Verification.AnchorPositionBeaconBlocks = twoStepListWindow, twoStepBeacons
	require.NoError(t, f.keeper.Params.Set(f.ctx, f.params))
	state := f.sample(t)
	require.True(t, state.Selected)
	require.Equal(t, anchorSamplingModeTwoStep, state.SamplingMode)
	require.Empty(t, state.Positions)
	require.NotEmpty(t, state.SelectedVerifiers)
	require.NotZero(t, state.ListDeadlineHeight)
}

// The malformed-commitment outcome is decided at sampling in both modes.
func TestTwoStepMalformedCommitmentIsRejectedAtSampling(t *testing.T) {
	f := newAnchorFixture(t, 0, 9)
	hub := f.keeper.hubKeeper.(anchorHubStub)
	hub.anchorSchema = shared.WorkerAnchorCommitmentSchemaVersionV2
	f.keeper.hubKeeper = hub
	state := f.sample(t)
	require.Equal(t, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_INVALID_COMMITMENT, state.ResultSource)
	require.Equal(t, anchorReject, state.TaskVerdict)
	_, err := f.keeper.AnchorSamplingDueHeight.Get(f.ctx, f.taskKey)
	require.Error(t, err)
}

// ---- wire vectors ----------------------------------------------------------

// The aggregated beacon and the position draw reproduce the published vectors.
func TestTwoStepSamplingMatchesTheWireVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/anchor_two_step_sampling_v1.json")
	require.NoError(t, err)
	var doc struct {
		Vectors []struct {
			Name           string   `json:"name"`
			ChainID        string   `json:"chain_id"`
			TaskID         string   `json:"task_id_hex"`
			Rows           uint64   `json:"rows"`
			ListHash       string   `json:"list_hash_hex"`
			SampleBeacon   string   `json:"sample_beacon_hex"`
			First          uint64   `json:"position_beacon_first_height"`
			Beacons        []string `json:"block_beacons_hex"`
			PositionBeacon string   `json:"position_beacon_hex"`
			Count          int      `json:"position_count"`
			Positions      []uint64 `json:"positions"`
		} `json:"vectors"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))
	require.GreaterOrEqual(t, len(doc.Vectors), 5)
	unhex := func(s string) []byte {
		b, err := hex.DecodeString(s)
		require.NoError(t, err)
		return b
	}
	for _, v := range doc.Vectors {
		var beacons [][]byte
		for _, b := range v.Beacons {
			beacons = append(beacons, unhex(b))
		}
		agg := anchorPositionBeacon(v.ChainID, unhex(v.TaskID), v.First, beacons)
		require.Equal(t, v.PositionBeacon, hex.EncodeToString(agg), v.Name)
		require.Equal(t, anchorSamplePositionsV0, v.Count, v.Name)
		got := anchorSamplePositionsV1(v.ChainID, unhex(v.TaskID), unhex(v.SampleBeacon), unhex(v.ListHash), agg, v.Rows, v.Count)
		require.Equal(t, v.Positions, got, v.Name)
	}
}
