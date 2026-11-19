package keeper

import (
	"errors"
	"testing"

	"cosmossdk.io/collections"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/task/types"
)

// requireNoContentVerdict asserts that the content track of the stored audit
// neither holds cleanup nor imposes a verdict or a fault at its deadline.
func (f *anchorFixture) requireNoContentVerdict(t *testing.T) {
	t.Helper()
	state, err := f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.False(t, anchorContentOpen(state))
	hold, err := f.keeper.anchorContentCleanupHold(f.ctx, f.taskKey, f.sampleAt+1)
	require.NoError(t, err)
	require.Zero(t, hold)
	late := f.at(int64(state.VerdictDeadlineHeight + f.params.Verification.AnchorContentVerdictWindowBlocks + 10))
	changed, err := f.keeper.closeAnchorContentAtCleanup(late, f.taskKey, &state)
	require.NoError(t, err)
	require.False(t, changed, "the content deadline imposes nothing")
	require.Zero(t, state.ContentResultHeight)
	require.Empty(t, state.ContentWorkerFaultId)
	pending, err := queryServer{k: f.keeper}.AnchorAuditsPending(f.ctx, &types.QueryAnchorAuditsPendingRequest{Content: true})
	require.NoError(t, err)
	require.Empty(t, pending.Audits)
	for i := 0; i < 4; i++ {
		_, err := f.keeper.cleanupAnchorAudit(late, f.taskKey)
		require.NoError(t, err)
	}
	_, err = f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.True(t, errors.Is(err, collections.ErrNotFound), "cleanup removes the audit: no verdict and no fault were recorded on it")
}

// With the content track on, a task the coin did not select at rate 0 has no
// content track either: nothing to open, seal or prove, so the content deadline
// must not reject it.
func TestAnchorContentZeroRateIsUnselected(t *testing.T) {
	f := newContentFixture(t, 1_000_000_000, anchorGoodTokenSize)
	f.params.Verification.AnchorAuditMinRateBps = 0
	f.params.Verification.AnchorAuditRiskMarginBps = 0
	f.params.Verification.AnchorMinBondMultiple = 0
	require.NoError(t, f.keeper.Params.Set(f.ctx, f.params))
	state := f.sample(t)
	require.Zero(t, state.AuditRateBps)
	require.False(t, state.Selected)
	require.Empty(t, state.Positions)
	require.False(t, anchorSelected(state))
	_, err := f.seal(f.sampleAt+2, f.worker)
	require.ErrorContains(t, err, "not selected")
	f.requireSettlesOnRoundVerdict(t, state)
	f.requireNoContentVerdict(t)
}

// A selected audit without sampled rows must not be condemned by the content
// deadline for a proof it could never have bound.
func TestAnchorContentSelectedAuditWithoutPositionsNeverSlashes(t *testing.T) {
	f := newContentFixture(t, 0, anchorGoodTokenSize)
	state := f.sample(t)
	require.True(t, state.Selected)
	require.NotZero(t, state.ContentVerdictDeadlineHeight)
	state.Positions = nil
	require.NoError(t, f.keeper.AnchorAudit.Set(f.ctx, f.taskKey, state))
	require.False(t, anchorComputeRejected(state))
	f.requireSettlesOnRoundVerdict(t, state)
	f.requireNoContentVerdict(t)
}
