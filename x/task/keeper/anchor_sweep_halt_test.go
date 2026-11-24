package keeper

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/task/types"
)

// The sampling beacon stays unavailable through the assignment deadline D. In a
// real block order the verify-open deadline fires at D (it fails and refunds the
// task and removes its own row) while the selection row is still indexed; at
// D+1 the selection sweep must treat that row as stale. Before the fix it
// reported ErrInvariantBroken ("no later verify-open deadline") out of EndBlock
// on every validator.
func TestSelectionSweepAfterVerifyOpenDeadlineDoesNotHaltTheChain(t *testing.T) {
	f := newAnchorOnlyFixture(t, 0, true)
	window := f.submitReceipt(t)
	stub := f.keeper.hubKeeper.(anchorOnlyHubStub)
	stub.beacon = nil
	f.keeper.hubKeeper = stub

	h, d := window.SelectionRandomnessHeight, window.AssignmentDeadlineHeight
	_, err := f.keeper.processVerifierSelectionRandomnessIndexWithBudget(f.at(int64(h)), h, 10, 1<<30)
	require.NoError(t, err)
	_, err = f.keeper.processVerifierSelectionRandomnessIndexWithBudget(f.at(int64(d)), d, 10, 1<<30)
	require.NoError(t, err, "at the deadline the row is still pending")
	visited, err := f.keeper.processExpiredVerifyOpenDeadlines(f.at(int64(d)), d, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	core := mustCore(t, f)
	require.Equal(t, types.TaskPhase_TASK_PHASE_FAILED, core.TaskPhase)
	require.Equal(t, types.SettlementStatus_SETTLEMENT_STATUS_REFUNDED, core.SettlementStatus)

	rowKey := types.NewVerifyRoundIndexKey(h, f.taskKey, types.VerifyRoundV1)
	indexed, err := f.keeper.VerifierSelectionRandomnessIndex.Has(f.ctx, rowKey)
	require.NoError(t, err)
	require.True(t, indexed, "the verify-open failure leaves the selection row behind")

	usage, err := f.keeper.processVerifierSelectionRandomnessIndexWithBudget(f.at(int64(d+1)), d+1, 10, 1<<30)
	require.NoError(t, err, "a row of an already refunded task must not stop the chain")
	require.Equal(t, uint64(1), usage.visited)
	indexed, err = f.keeper.VerifierSelectionRandomnessIndex.Has(f.ctx, rowKey)
	require.NoError(t, err)
	require.False(t, indexed, "the stale row is removed")
	require.Empty(t, *f.faults, "the Worker is not faulted for a missing beacon")

	// The whole EndBlock schedule runs clean on the following heights too.
	for height := d + 1; height <= d+3; height++ {
		_, err := f.keeper.RunEndBlockDeadlineSweeps(f.at(int64(height)), height, 10)
		require.NoError(t, err)
	}
}
