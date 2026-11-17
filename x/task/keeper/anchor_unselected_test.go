package keeper

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/task/types"
)

// passRound writes a closed PASS round 1 and returns the summary settlement
// reads, so the settlement deadline and hold of the fixture task can be computed.
func (f *anchorFixture) passRound(t *testing.T, closed uint64) types.TaskRoundSummaryState {
	t.Helper()
	require.NoError(t, f.keeper.WriteVerificationRound(f.ctx, types.NewVerifyRoundKey(f.taskKey, types.VerifyRoundV1), types.VerificationRoundState{
		TaskId: f.taskID, VerifyRound: types.VerifyRoundV1,
		XVerdict:      &types.VerificationRoundState_Verdict{Verdict: types.TaskVerdict_TASK_VERDICT_PASS},
		XClosedHeight: &types.VerificationRoundState_ClosedHeight{ClosedHeight: closed},
	}))
	return types.TaskRoundSummaryState{TaskId: f.taskID, EffectiveVerifyRound: types.VerifyRoundV1,
		XRoundsClosedHeight: &types.TaskRoundSummaryState_RoundsClosedHeight{RoundsClosedHeight: closed}}
}

// requireSettlesOnRoundVerdict asserts that the audit neither holds a PASS round
// nor imposes a verdict on it, before and after the verdict deadline.
func (f *anchorFixture) requireSettlesOnRoundVerdict(t *testing.T, state types.AnchorAuditState) {
	t.Helper()
	pass := types.TaskVerdict_TASK_VERDICT_PASS
	summary := f.passRound(t, 150)
	roundDeadline, err := settlementDeadlineHeight(150, f.params)
	require.NoError(t, err)
	for _, height := range []uint64{state.VerdictDeadlineHeight - 1, state.VerdictDeadlineHeight, state.VerdictDeadlineHeight + 1_000} {
		view, err := f.keeper.anchorSettlementView(f.ctx, f.taskKey, pass, height)
		require.NoError(t, err, "a PASS round must not be held at height %d", height)
		require.Nil(t, view, "no anchor verdict (and so no anchor fault) may be imposed at height %d", height)
	}
	hold, err := f.keeper.anchorSettlementHoldHeight(f.ctx, f.taskKey, types.VerifyRoundV1)
	require.NoError(t, err)
	require.Zero(t, hold, "the settlement hold must be off")
	deadline, err := f.keeper.effectiveSettlementDeadline(f.ctx, f.taskKey, summary, f.params)
	require.NoError(t, err)
	require.Equal(t, roundDeadline, deadline, "the settlement deadline must stay the round deadline")
	pending, err := queryServer{k: f.keeper}.AnchorAuditsPending(f.ctx, &types.QueryAnchorAuditsPendingRequest{})
	require.NoError(t, err)
	require.Empty(t, pending.Audits, "validators must not be asked for a verdict")
}

// A zero audit rate (floor 0 with margin 0, or an order value of 0) is a rate
// like any other: the coin never selects the task, so it has no positions, the
// Worker has nothing to open, and settlement must follow the round verdict with
// no hold and no anchor_timeout fault.
func TestAnchorZeroRateIsUnselectedAndSettlesOnRoundVerdict(t *testing.T) {
	require.Equal(t, uint32(0), anchorAuditRateBps(1_000_000, 1_000_000_000, 500, 0, 0))
	require.False(t, anchorCoinSelects(0, 0), "rate 0 never selects, whatever the coin")

	f := newAnchorFixture(t, 1_000_000_000, anchorGoodTokenSize)
	f.params.Verification.AnchorAuditMinRateBps = 0
	f.params.Verification.AnchorAuditRiskMarginBps = 0
	require.NoError(t, f.keeper.Params.Set(f.ctx, f.params))
	state := f.sample(t)
	require.Zero(t, state.AuditRateBps)
	require.False(t, state.Selected)
	require.Empty(t, state.Positions)
	require.NotEmpty(t, state.AuditBasis)
	require.False(t, anchorSelected(state), "the recorded coin outcome decides, not the rate")

	_, err := f.server.SubmitAnchorOpening(f.at(int64(f.sampleAt+1)), f.openingMsg(state, f.worker))
	require.Error(t, err, "there is nothing the Worker could open")
	opening := f.openingMsg(state, f.worker)
	opening.Positions = []uint64{0}
	_, err = f.server.SubmitAnchorOpening(f.at(int64(f.sampleAt+1)), opening)
	require.ErrorContains(t, err, "not selected")
	f.requireSettlesOnRoundVerdict(t, state)
}

// A record written before the audit rate existed carries no coin outcome, no
// rate and no basis; it was always audited and keeps binding settlement.
func TestAnchorPreRateRecordStaysSelected(t *testing.T) {
	f := newAnchorFixture(t, 0, anchorGoodTokenSize)
	legacy := types.AnchorAuditState{
		TaskId: f.taskID, Sampled: true, Positions: []uint64{1, 2, 3}, SampleBeacon: f.beacon, SampleHeight: f.sampleAt,
		WinnerWorker: f.worker, VerdictDeadlineHeight: f.sampleAt + 900,
	}
	require.True(t, anchorSelected(legacy))
	require.True(t, anchorAuditBinds(legacy))
	require.NoError(t, f.keeper.AnchorAudit.Set(f.ctx, f.taskKey, legacy))
	pass := types.TaskVerdict_TASK_VERDICT_PASS
	_, err := f.keeper.anchorSettlementView(f.ctx, f.taskKey, pass, legacy.VerdictDeadlineHeight-1)
	require.ErrorContains(t, err, "pending until")
	view, err := f.keeper.anchorSettlementView(f.ctx, f.taskKey, pass, legacy.VerdictDeadlineHeight)
	require.NoError(t, err)
	require.Equal(t, types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT, view.TaskVerdict)

	// The same record with a basis is a current one: the coin did not select it.
	current := legacy
	current.AuditBasis = "order 0; rate 0 bps; selected false"
	current.Positions = nil
	require.False(t, anchorSelected(current))
	require.False(t, anchorAuditBinds(current))

	// A slimmed fault reference (cleanup drops positions and basis) of a selected
	// audit is final and keeps naming the same settlement deadline.
	slim := types.AnchorAuditState{TaskId: f.taskID, Sampled: true, Selected: true, AuditRateBps: 10_000,
		VerdictDeadlineHeight: f.sampleAt + 900, ResultHeight: f.sampleAt + 10,
		TaskVerdict: types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT, WorkerFaultId: bytes.Repeat([]byte{0xfa}, 32)}
	require.True(t, anchorAuditBinds(slim))
}

// A selected audit without sampled rows cannot be produced by sampling, but if
// one is ever stored it must not hold a round or time out against the Worker.
func TestAnchorSelectedAuditWithoutPositionsNeverSlashes(t *testing.T) {
	f := newAnchorFixture(t, 0, anchorGoodTokenSize)
	state := f.sample(t)
	require.True(t, state.Selected)
	state.Positions = nil
	require.NoError(t, f.keeper.AnchorAudit.Set(f.ctx, f.taskKey, state))
	require.False(t, anchorAuditBinds(state))
	f.requireSettlesOnRoundVerdict(t, state)
	verdict, _ := anchorTimeoutVerdict(state)
	require.Equal(t, types.AnchorVerdictV1_ANCHOR_VERDICT_V1_NO_VERDICT, verdict, "the deadline never blames the Worker for rows that were not sampled")
}
