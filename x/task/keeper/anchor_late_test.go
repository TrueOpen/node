package keeper

import (
	"bytes"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	"github.com/TrueOpen/node/x/task/types"
)

const (
	lateWindow    = uint64(200)
	latePeriod    = uint64(1_000)
	lateRolling   = uint64(10_000)
	lateFreeCount = uint32(2)
)

var (
	reasonOpening      = types.AnchorTimeoutReasonV1_ANCHOR_TIMEOUT_REASON_V1_OPENING_DEADLINE
	reasonProof        = types.AnchorTimeoutReasonV1_ANCHOR_TIMEOUT_REASON_V1_PROOF_DEADLINE
	reasonUnavailable  = types.AnchorTimeoutReasonV1_ANCHOR_TIMEOUT_REASON_V1_PANEL_UNAVAILABLE
	reasonInconclusive = types.AnchorTimeoutReasonV1_ANCHOR_TIMEOUT_REASON_V1_PANEL_INCONCLUSIVE
	sourcePanel        = types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_VERIFIER_PANEL
	sourceLateExpired  = types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_LATE_EXPIRED
)

func lateParams(freeCount uint32) func(*types.TaskParamsV1) {
	return func(params *types.TaskParamsV1) {
		params.Verification.AnchorLateWindowBlocks = lateWindow
		params.Verification.BatchSettlementPeriodBlocks = latePeriod
		params.Verification.AnchorTimeoutRollingWindowBlocks = lateRolling
		params.Verification.AnchorTimeoutFreeCount = freeCount
	}
}

// newAnchorLateFixture stages a sampled panel audit with late submission on,
// before the Worker has opened anything.
func newAnchorLateFixture(t *testing.T, bond uint64, candidates int) *anchorPanelFixture {
	t.Helper()
	return newTunedAnchorPanelFixture(t, bond, candidates, lateParams(lateFreeCount))
}

// newProvedAnchorLateFixture is the same audit with the opening and the proof
// recorded in time: its first panel round is open.
func newProvedAnchorLateFixture(t *testing.T, bond uint64, candidates int) *anchorPanelFixture {
	t.Helper()
	f := newAnchorLateFixture(t, bond, candidates)
	f.openAndProveSampled(t, f.audit)
	f.reload(t)
	return f
}

func (f *anchorPanelFixture) openAt(height uint64) error {
	_, err := f.server.SubmitAnchorOpening(f.at(int64(height)), f.openingMsg(f.audit, f.worker))
	return err
}

// proveAt records a proof at height; location distinguishes a re-submission.
func (f *anchorPanelFixture) proveAt(height uint64, location string) (*types.MsgSubmitAnchorProofResponse, error) {
	return f.server.SubmitAnchorProof(f.at(int64(height)), &types.MsgSubmitAnchorProof{
		TaskId: f.taskID, OpeningHash: bytes.Repeat([]byte{0x0c}, 32),
		ProofHash: bytes.Repeat([]byte{0x0d}, 32), ProofSizeBytes: 5, ProofUri: "https://da.example/" + location,
		EpochManifestHash: bytes.Repeat([]byte{0x0e}, 32), EpochManifestUri: "https://da.example/m", SubmitterAddress: f.worker,
	})
}

func (f *anchorPanelFixture) workerPaused() bool {
	return f.hubState.paused[f.worker]&hubtypes.WorkerPauseReasonLateSubmission != 0
}

func (f *anchorPanelFixture) lateList(t *testing.T) []types.WorkerLateSubmissionV1 {
	t.Helper()
	response, err := (queryServer{k: f.keeper}).WorkerLateSubmissions(f.ctx, &types.QueryWorkerLateSubmissionsRequest{WorkerOperatorAddress: f.worker})
	require.NoError(t, err)
	return response.Submissions
}

func (f *anchorPanelFixture) ledger(t *testing.T) (types.WorkerTimeoutLedgerState, bool) {
	t.Helper()
	raw, err := f.keeper.sessionAddressToStore("worker", f.worker)
	require.NoError(t, err)
	ledger, err := f.keeper.WorkerTimeoutLedger.Get(f.ctx, raw)
	if errIsNotFound(err) {
		return ledger, false
	}
	require.NoError(t, err)
	return ledger, true
}

// timeOutTheProof lets the proof deadline pass on an audit whose opening was
// recorded in time, and returns the height the timeout was recorded at.
func (f *anchorPanelFixture) timeOutTheProof(t *testing.T) uint64 {
	t.Helper()
	require.NoError(t, f.openAt(f.audit.SampleHeight+1))
	require.Equal(t, uint64(1), f.sweep(t, f.audit.OpeningDeadlineHeight+1), "the sweep moves on to the proof deadline")
	require.Zero(t, f.reload(t).LateState)
	require.Zero(t, f.sweep(t, f.audit.ProofDeadlineHeight), "the deadline height itself is still in time")
	recorded := f.audit.ProofDeadlineHeight + 1
	require.Equal(t, uint64(1), f.sweep(t, recorded))
	audit := f.reload(t)
	require.Equal(t, lateStateAwaiting, audit.LateState)
	require.Equal(t, reasonProof, audit.TimeoutReason)
	require.Equal(t, recorded, audit.TimeoutRecordedHeight)
	require.Equal(t, recorded+lateWindow, audit.LateDeadlineHeight)
	require.Zero(t, audit.ResultHeight)
	return recorded
}

// A Worker that misses its proof deadline is paused, not slashed. Its late
// proof lifts the pause and opens a panel round; that panel's PASS pays it in
// full. The timeout stays counted in its ledger.
func TestMissedProofDeadlineIsCuredByALateSubmission(t *testing.T) {
	f := newAnchorLateFixture(t, 4_000, 6)
	recorded := f.timeOutTheProof(t)
	require.True(t, f.workerPaused())
	list := f.lateList(t)
	require.Len(t, list, 1)
	require.Equal(t, f.taskID, list[0].TaskId)
	require.Equal(t, reasonProof, list[0].TimeoutReason)
	require.Equal(t, recorded+lateWindow, list[0].LateDeadlineHeight)
	ledger, found := f.ledger(t)
	require.True(t, found)
	require.Equal(t, []uint64{recorded / latePeriod}, ledger.Periods)
	require.Empty(t, ledger.SlashedPeriods)
	_, err := f.settleNow(t, recorded+1)
	require.ErrorContains(t, err, "pending until", "a task waiting for a late submission does not settle")
	require.Empty(t, *f.faults)

	response, err := f.proveAt(recorded+5, "p")
	require.NoError(t, err)
	require.NotNil(t, response)
	audit := f.reload(t)
	require.Equal(t, lateStateSubmitted, audit.LateState)
	require.False(t, f.workerPaused(), "the late submission lifts the pause")
	require.Len(t, audit.PanelRounds, 1)
	require.Equal(t, uint32(1), audit.PanelRounds[0].Attempt)
	require.Equal(t, types.AnchorPanelRoundKindV1_ANCHOR_PANEL_ROUND_KIND_V1_INITIAL, audit.PanelRounds[0].Kind)
	require.Equal(t, lateStateSubmitted, f.lateList(t)[0].LateState)

	round := f.assign(t)
	f.castVotes(t, round, round.AssignmentHeight+1, votePass, votePass)
	audit = f.reload(t)
	require.Equal(t, votePass, audit.ProvisionalVerdict)
	f.sweep(t, audit.PublicationEndHeight)
	audit = f.reload(t)
	require.Equal(t, votePass, audit.TaskVerdict)
	require.Empty(t, f.lateList(t))
	settlement, err := f.settleNow(t, audit.FinalHeight+1)
	require.NoError(t, err)
	f.requireAuditedPaid(t, settlement)
	f.requireNoPanelWork(t)
	_, found = f.ledger(t)
	require.True(t, found, "the timeout stays counted")
}

// A missed opening deadline: the late opening and the late proof are both
// accepted past their own deadlines, and nothing is accepted past the late
// deadline.
func TestLateOpeningAndProofAreAcceptedUntilTheLateDeadline(t *testing.T) {
	f := newAnchorLateFixture(t, 4_000, 6)
	recorded := f.audit.OpeningDeadlineHeight + 1
	require.ErrorContains(t, f.openAt(recorded), "deadline", "before the timeout is recorded the deadline stands")
	require.Equal(t, uint64(1), f.sweep(t, recorded))
	audit := f.reload(t)
	require.Equal(t, reasonOpening, audit.TimeoutReason)
	require.True(t, f.workerPaused())
	require.NoError(t, f.openAt(audit.LateDeadlineHeight-1))
	require.True(t, f.workerPaused(), "the opening alone is not the late submission")
	_, err := f.proveAt(audit.LateDeadlineHeight, "p")
	require.NoError(t, err, "the late deadline height itself is still in time")
	require.False(t, f.workerPaused())
	require.Equal(t, lateStateSubmitted, f.reload(t).LateState)

	g := newAnchorLateFixture(t, 4_000, 6)
	require.Equal(t, uint64(1), g.sweep(t, g.audit.OpeningDeadlineHeight+1))
	deadline := g.reload(t).LateDeadlineHeight
	require.ErrorContains(t, g.openAt(deadline+1), "deadline")
}

// Without a late submission the audit ends at the late deadline as LATE
// EXPIRED: not paid, the timeout slash, no jail fault, pause lifted. The sweep
// and a settlement that runs without it reach the same outcome.
func TestNoLateSubmissionEndsAsLateExpired(t *testing.T) {
	const bond = uint64(4_000)
	for _, bySweep := range []bool{true, false} {
		f := newAnchorLateFixture(t, bond, 6)
		require.Equal(t, uint64(1), f.sweep(t, f.audit.OpeningDeadlineHeight+1))
		audit := f.reload(t)
		require.True(t, f.workerPaused())
		require.Zero(t, f.sweep(t, audit.LateDeadlineHeight-1))
		_, err := f.settleNow(t, audit.LateDeadlineHeight-1)
		require.ErrorContains(t, err, "pending until")
		if bySweep {
			require.Equal(t, uint64(1), f.sweep(t, audit.LateDeadlineHeight))
			audit = f.reload(t)
			require.Equal(t, voteReject, audit.TaskVerdict)
			require.Equal(t, sourceLateExpired, audit.ResultSource)
			require.Equal(t, lateStateExpired, audit.LateState)
		}
		settlement, err := f.settleNow(t, audit.LateDeadlineHeight)
		require.NoError(t, err)
		f.requireWorkerFaulted(t, settlement, hubtypes.FaultTypeAnchorTimeout, f.params.Verification.AnchorTimeoutSlashBps, bond)
		audit = f.reload(t)
		require.Equal(t, sourceLateExpired, audit.ResultSource)
		require.Equal(t, lateStateExpired, audit.LateState)
		require.False(t, f.workerPaused(), "the pause ends with the audit")
		require.Empty(t, f.lateList(t))
		f.requireNoPanelWork(t)
	}
}

// Two members cast UNAVAILABLE: the Worker is paused and asked for its
// material again. It records its proof again; a panel of other members passes
// it and it is paid in full.
func TestUnavailablePanelIsCuredByRecordingTheProofAgain(t *testing.T) {
	f := newProvedAnchorLateFixture(t, 4_000, 9)
	first := f.assign(t)
	voteAt := first.AssignmentHeight + 1
	f.castVotes(t, first, voteAt, voteUnavailable, voteUnavailable)
	audit := f.reload(t)
	require.Zero(t, audit.ResultHeight, "with a late window an unavailable round ends nothing")
	require.Equal(t, lateStateAwaiting, audit.LateState)
	require.Equal(t, reasonUnavailable, audit.TimeoutReason)
	require.Equal(t, voteAt+lateWindow, audit.LateDeadlineHeight)
	require.True(t, f.workerPaused())
	require.Empty(t, *f.faults)

	response, err := f.proveAt(voteAt+1, "p-again")
	require.NoError(t, err)
	require.NotNil(t, response)
	audit = f.reload(t)
	require.Equal(t, "https://da.example/p-again", audit.WorkerProofUri)
	require.False(t, f.workerPaused())
	require.Len(t, audit.PanelRounds, 2)
	require.Equal(t, uint32(1), audit.PanelRounds[1].Attempt)
	second := f.assign(t)
	for _, member := range second.Members {
		require.NotContains(t, first.Members, member)
	}
	f.castVotes(t, second, second.AssignmentHeight+1, votePass, votePass)
	audit = f.reload(t)
	f.sweep(t, audit.PublicationEndHeight)
	settlement, err := f.settleNow(t, audit.PublicationEndHeight+1)
	require.NoError(t, err)
	f.requireAuditedPaid(t, settlement)
}

// The late panel also casts UNAVAILABLE twice. The task has one late attempt:
// the Worker is paused again, a further proof changes nothing, and the audit
// ends as LATE EXPIRED at the deadline fixed at the first timeout.
func TestSecondUnavailablePanelLeadsToLateExpired(t *testing.T) {
	const bond = uint64(4_000)
	f := newProvedAnchorLateFixture(t, bond, 9)
	first := f.assign(t)
	voteAt := first.AssignmentHeight + 1
	f.castVotes(t, first, voteAt, voteUnavailable, voteUnavailable)
	deadline := f.reload(t).LateDeadlineHeight
	_, err := f.proveAt(voteAt+1, "p-again")
	require.NoError(t, err)
	second := f.assign(t)
	f.castVotes(t, second, second.AssignmentHeight+1, voteUnavailable, voteUnavailable)
	audit := f.reload(t)
	require.Equal(t, lateStateAwaiting, audit.LateState)
	require.Equal(t, deadline, audit.LateDeadlineHeight, "the late deadline does not move")
	require.True(t, f.workerPaused())
	require.Len(t, audit.PanelRounds, 2)
	response, err := f.proveAt(second.AssignmentHeight+2, "p-again")
	require.NoError(t, err)
	require.NotNil(t, response)
	require.Len(t, f.reload(t).PanelRounds, 2, "no second late round")
	require.Equal(t, uint64(1), f.sweep(t, deadline))
	settlement, err := f.settleNow(t, deadline+1)
	require.NoError(t, err)
	f.requireWorkerFaulted(t, settlement, hubtypes.FaultTypeAnchorTimeout, f.params.Verification.AnchorTimeoutSlashBps, bond)
	require.False(t, f.workerPaused())
}

// silence takes a proved audit through two panel rounds in which nobody votes.
func (f *anchorPanelFixture) silence(t *testing.T) types.AnchorAuditState {
	t.Helper()
	first := f.assign(t)
	require.Equal(t, uint64(2), f.sweep(t, first.VoteDeadlineHeight+1))
	second := f.assign(t)
	require.Equal(t, uint64(1), f.sweep(t, second.VoteDeadlineHeight+1))
	audit := f.reload(t)
	require.Equal(t, reasonInconclusive, audit.TimeoutReason)
	return audit
}

// Verifiers that stay silent cost the Worker nothing, with late submission on
// as without it: no pause, no ledger entry, no fault. If the Worker does
// nothing the audit ends without a verdict at the late deadline and it is
// paid.
func TestSilentPanelsWithALateWindowNeverCostTheWorker(t *testing.T) {
	f := newProvedAnchorLateFixture(t, 4_000, 12)
	audit := f.silence(t)
	require.Zero(t, audit.ResultHeight)
	require.Equal(t, lateStateAwaiting, audit.LateState)
	require.False(t, f.workerPaused(), "silence of Verifiers never pauses the Worker")
	_, counted := f.ledger(t)
	require.False(t, counted, "and is never counted against it")
	require.Len(t, f.lateList(t), 1)

	require.Equal(t, uint64(1), f.sweep(t, audit.LateDeadlineHeight))
	audit = f.reload(t)
	require.Equal(t, noVerdict, audit.TaskVerdict)
	require.Equal(t, sourcePanel, audit.ResultSource)
	settlement, err := f.settleNow(t, audit.ResultHeight+1)
	require.NoError(t, err)
	f.requireAuditedPaid(t, settlement)
	require.Empty(t, *f.faults)
	f.requireNoPanelWork(t)
}

// After two silent rounds the Worker may record its proof again to have one
// more panel drawn. If that panel is silent too the audit ends at once without
// a verdict; and so it does when settlement runs before any sweep.
func TestSilentLatePanelEndsWithoutAVerdict(t *testing.T) {
	f := newProvedAnchorLateFixture(t, 4_000, 12)
	audit := f.silence(t)
	_, err := f.proveAt(audit.TimeoutRecordedHeight+1, "p-again")
	require.NoError(t, err)
	third := f.assign(t)
	require.Equal(t, uint32(1), third.Attempt)
	require.Equal(t, uint64(1), f.sweep(t, third.VoteDeadlineHeight+1))
	audit = f.reload(t)
	require.Equal(t, noVerdict, audit.TaskVerdict)
	require.Equal(t, sourcePanel, audit.ResultSource)
	require.Len(t, audit.PanelRounds, 3, "a round on a late submission is not re-assigned")
	settlement, err := f.settleNow(t, audit.ResultHeight+1)
	require.NoError(t, err)
	f.requireAuditedPaid(t, settlement)
	require.Empty(t, *f.faults)

	g := newProvedAnchorLateFixture(t, 4_000, 12)
	audit = g.silence(t)
	settlement, err = g.settleNow(t, audit.LateDeadlineHeight)
	require.NoError(t, err)
	g.requireAuditedPaid(t, settlement)
	require.Empty(t, *g.faults)
}

// A late submission that no panel can be formed for is not the Worker's loss:
// the audit ends without a verdict and the Worker is paid.
func TestLateSubmissionWithoutAPanelPaysTheWorker(t *testing.T) {
	f := newAnchorLateFixture(t, 4_000, 2)
	recorded := f.timeOutTheProof(t)
	_, err := f.proveAt(recorded+1, "p")
	require.NoError(t, err)
	audit := f.reload(t)
	require.Equal(t, noVerdict, audit.TaskVerdict, "two eligible operators cannot form a panel of three")
	require.False(t, f.workerPaused())
	settlement, err := f.settleNow(t, audit.ResultHeight+1)
	require.NoError(t, err)
	f.requirePaidInFull(t, settlement)
	require.Empty(t, *f.faults)
}

// A late panel that rejects the proof is a rejection like any other.
func TestLatePanelRejectIsARejection(t *testing.T) {
	const bond = uint64(4_000)
	f := newAnchorLateFixture(t, bond, 6)
	recorded := f.timeOutTheProof(t)
	_, err := f.proveAt(recorded+1, "p")
	require.NoError(t, err)
	round := f.assign(t)
	f.castVotes(t, round, round.AssignmentHeight+1, voteReject, voteReject)
	audit := f.reload(t)
	require.Equal(t, voteReject, audit.ProvisionalVerdict)
	f.sweep(t, audit.PublicationEndHeight)
	settlement, err := f.settleNow(t, audit.PublicationEndHeight+1)
	require.NoError(t, err)
	f.requireWorkerFaulted(t, settlement, hubtypes.FaultTypeAnchorReject, f.params.Verification.AnchorRejectSlashBps, bond)
	require.False(t, f.workerPaused())
}

// The rolling ledger counts settlement periods, not tasks; drops what is older
// than the window; and names the timeout that passes the free count once per
// period.
func TestWorkerTimeoutLedgerCountsPeriodsInTheWindow(t *testing.T) {
	f := newAnchorLateFixture(t, 4_000, 6)
	params := f.params
	params.Verification.BatchSettlementPeriodBlocks = 100
	params.Verification.AnchorTimeoutRollingWindowBlocks = 1_000
	steps := []struct {
		height uint64
		count  uint32
		repeat bool
	}{
		{10, 1, false},    // first period
		{20, 1, false},    // same period again
		{110, 2, false},   // second period: still free
		{250, 3, true},    // third period: past the free count
		{260, 3, false},   // charged once per period
		{1_300, 1, false}, // everything before height 300 has left the window
	}
	for _, step := range steps {
		count, repeat, err := f.keeper.countAnchorWorkerTimeout(f.ctx, f.worker, step.height, params)
		require.NoError(t, err)
		require.Equalf(t, step.count, count, "height %d", step.height)
		require.Equalf(t, step.repeat, repeat, "height %d", step.height)
	}
	ledger, found := f.ledger(t)
	require.True(t, found)
	require.Equal(t, []uint64{13}, ledger.Periods)
	require.Empty(t, ledger.SlashedPeriods)

	other := sdk.AccAddress(bytes.Repeat([]byte{0x7a}, 20)).String()
	for _, off := range []func(*types.TaskParamsV1){
		func(p *types.TaskParamsV1) { p.Verification.BatchSettlementPeriodBlocks = 0 },
		func(p *types.TaskParamsV1) { p.Verification.AnchorTimeoutRollingWindowBlocks = 0 },
	} {
		uncounted := params
		off(&uncounted)
		count, repeat, err := f.keeper.countAnchorWorkerTimeout(f.ctx, other, 10, uncounted)
		require.NoError(t, err)
		require.Zero(t, count)
		require.False(t, repeat, "without a period or a window nothing is counted")
	}
}

// The timeout that takes a Worker past its free count is not open to late
// submission: it ends as a missed deadline with the timeout slash, once.
func TestTimeoutPastTheFreeCountEndsAtOnce(t *testing.T) {
	const bond = uint64(4_000)
	f := newTunedAnchorPanelFixture(t, bond, 6, lateParams(0))
	recorded := f.audit.OpeningDeadlineHeight + 1
	require.Equal(t, uint64(1), f.sweep(t, recorded))
	audit := f.reload(t)
	require.Equal(t, voteReject, audit.TaskVerdict)
	require.Equal(t, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_TIMEOUT, audit.ResultSource)
	require.Zero(t, audit.LateState)
	require.False(t, f.workerPaused())
	require.Empty(t, f.lateList(t))
	ledger, _ := f.ledger(t)
	require.Equal(t, []uint64{recorded / latePeriod}, ledger.SlashedPeriods)
	settlement, err := f.settleNow(t, recorded+1)
	require.NoError(t, err)
	f.requireWorkerFaulted(t, settlement, hubtypes.FaultTypeAnchorTimeout, f.params.Verification.AnchorTimeoutSlashBps, bond)
}

// The pause is held by every audit of the Worker that waits for a late
// submission after an omission of its own, and ends with the last of them.
func TestPauseEndsWithTheLastOutstandingLateSubmission(t *testing.T) {
	f := newAnchorLateFixture(t, 4_000, 6)
	recorded := f.timeOutTheProof(t)
	otherID := bytes.Repeat([]byte{0x9e}, types.Hash32Len)
	otherKey := types.NewTaskKey(otherID)
	other := types.AnchorAuditState{
		TaskId: otherID, Sampled: true, Selected: true, AuditMode: types.AnchorAuditModePanel, WinnerWorker: f.worker,
		LateState: lateStateAwaiting, TimeoutReason: reasonOpening, LateDeadlineHeight: recorded + lateWindow,
	}
	require.NoError(t, f.keeper.AnchorAudit.Set(f.ctx, otherKey, other))
	require.NoError(t, f.keeper.setWorkerLateIndex(f.ctx, f.worker, otherKey, true))
	require.Len(t, f.lateList(t), 2)

	_, err := f.proveAt(recorded+1, "p")
	require.NoError(t, err)
	require.True(t, f.workerPaused(), "another audit of the Worker still waits")

	// The other audit waits after silent panels only: that never holds a pause.
	other.TimeoutReason = reasonInconclusive
	require.NoError(t, f.keeper.AnchorAudit.Set(f.ctx, otherKey, other))
	require.NoError(t, f.keeper.refreshAnchorLatePause(f.ctx, f.worker, f.taskKey, f.taskID, false))
	require.False(t, f.workerPaused())
}

// An import restores the late list and the sweep's visit; the audit then runs
// to the same end. The ledger is part of the exported state.
func TestLateStateSurvivesExportAndImport(t *testing.T) {
	const bond = uint64(4_000)
	f := newAnchorLateFixture(t, bond, 6)
	require.Equal(t, uint64(1), f.sweep(t, f.audit.OpeningDeadlineHeight+1))
	audit := f.reload(t)
	exported, err := collectMapValues(f.ctx, f.keeper.WorkerTimeoutLedger)
	require.NoError(t, err)
	require.Len(t, exported, 1)
	require.Equal(t, f.worker, exported[0].OperatorAddress)

	// What an import does not carry: the derived rows.
	require.NoError(t, f.keeper.setWorkerLateIndex(f.ctx, f.worker, f.taskKey, false))
	require.NoError(t, f.keeper.clearAnchorPanelDue(f.ctx, f.taskKey))
	raw, err := f.keeper.sessionAddressToStore("worker", f.worker)
	require.NoError(t, err)
	require.NoError(t, f.keeper.WorkerTimeoutLedger.Remove(f.ctx, raw))
	require.Empty(t, f.lateList(t))

	require.NoError(t, f.keeper.initGenesisAnchor(f.ctx, types.GenesisState{WorkerTimeoutLedgers: exported}))
	require.ErrorContains(t, f.keeper.initGenesisAnchor(f.ctx, types.GenesisState{WorkerTimeoutLedgers: exported}), "duplicate")
	require.NoError(t, f.keeper.rebuildAnchorPanelIndexes(f.ctx, f.taskKey, audit))
	require.Len(t, f.lateList(t), 1)
	ledger, found := f.ledger(t)
	require.True(t, found)
	require.Equal(t, exported[0], ledger)

	// The first sweep after the import finds the audit still waiting; the
	// Worker keeps its whole late window.
	require.Equal(t, uint64(1), f.sweep(t, f.dueHeight(t)))
	require.Equal(t, lateStateAwaiting, f.reload(t).LateState)
	require.NoError(t, f.openAt(audit.LateDeadlineHeight-2))
	_, err = f.proveAt(audit.LateDeadlineHeight-1, "p")
	require.NoError(t, err)
	require.Equal(t, lateStateSubmitted, f.reload(t).LateState)
	require.Empty(t, *f.faults)

	malformed := types.WorkerTimeoutLedgerState{OperatorAddress: f.worker, Periods: []uint64{1}}
	require.ErrorContains(t, f.keeper.initGenesisAnchor(f.ctx, types.GenesisState{WorkerTimeoutLedgers: []types.WorkerTimeoutLedgerState{malformed}}), "malformed")
}

// The verdict deadline of an audit with late submission also covers the late
// window and the round a late submission opens.
func TestVerdictDeadlineCoversTheLateWindow(t *testing.T) {
	f := newAnchorLateFixture(t, 4_000, 6)
	span, ok := anchorPanelSelectionSpanBlocks(f.params)
	require.True(t, ok)
	want := f.audit.ProofDeadlineHeight + f.params.Verification.AnchorVerdictWindowBlocks + 4*span + panelPublication + lateWindow
	require.Equal(t, want, f.audit.VerdictDeadlineHeight)
}

// An audit sampled without a late window keeps the earlier outcomes after the
// network turns late submission on: its deadlines are not watched by the
// sweep and a missed one is settled at the verdict deadline as before.
func TestAuditSampledWithoutALateWindowIsUnchanged(t *testing.T) {
	const bond = uint64(4_000)
	f := newAnchorPanelFixtureBeforeProof(t, bond, 6)
	has, err := f.keeper.AnchorPanelDueHeight.Has(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.False(t, has)
	// An import schedules a visit for every open panel audit; this one is left alone.
	require.NoError(t, f.keeper.rebuildAnchorPanelIndexes(f.ctx, f.taskKey, f.audit))
	require.Equal(t, uint64(1), f.sweep(t, f.audit.OpeningDeadlineHeight+1))
	require.Zero(t, f.reload(t).LateState)
	require.Empty(t, f.lateList(t))
	settlement, err := f.settleNow(t, f.audit.VerdictDeadlineHeight)
	require.NoError(t, err)
	f.requireWorkerFaulted(t, settlement, hubtypes.FaultTypeAnchorTimeout, f.params.Verification.AnchorTimeoutSlashBps, bond)
}
