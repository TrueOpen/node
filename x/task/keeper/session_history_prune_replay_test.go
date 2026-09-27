package keeper

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/task/types"
)

// closedSessionWithTerminalOrder seeds the one shape the assertions below turn
// on: a closed stream whose single order sequence is terminal and names a task.
// The existing session_runtime tests all use task-less CANCELLED sequences,
// which return before ensureOrderBudgetTerminal ever looks at a task record.
func closedSessionWithTerminalOrder(t *testing.T, f *internalFixture, tag byte) (types.SessionKey, types.TaskKey, []byte) {
	t.Helper()
	sessionID := bytes.Repeat([]byte{tag}, types.Hash32Len)
	taskID := bytes.Repeat([]byte{tag ^ 0xff}, types.Hash32Len)
	sessionKey, err := sessionStoreKey(sessionID)
	require.NoError(t, err)
	taskKey, err := taskStoreKey(taskID)
	require.NoError(t, err)
	require.NoError(t, f.keeper.setStreamState(f.ctx, types.StreamState{
		SessionId: sessionID, OwnerUserAddress: sessionTestOwner(t, f), NextExpectedSequence: 1,
		LastActiveHeight: 9, Status: types.SessionStatus_SESSION_STATUS_CLOSED,
	}))
	require.NoError(t, f.keeper.OrderSequence.Set(
		f.ctx, types.NewOrderSequenceStateKey(sessionKey, 0), types.OrderSequenceState{
			SessionId: sessionID, OrderSequence: 0, TaskId: taskID,
			Status: types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_CONSUMED,
		},
	))
	require.NoError(t, f.keeper.SessionHistoryPruneIndex.Set(
		f.ctx, types.NewSessionHistoryPruneIndexKey(10, sessionKey),
	))
	return sessionKey, taskKey, taskID
}

// A terminal order with neither TaskBudget nor TaskTerminalSummary must compact.
//
// Both records are optional by construction. TaskBudget appears only once an
// assignment reserved escrow, so a task that failed earlier — verifier selection
// coming up short is exactly that — never had one. TaskTerminalSummary is written
// at the tail of the incremental cleanup pipeline and then deleted again by its
// own retention sweep, which runs off an independent prune index. Once task
// terminal summary retention elapses before session order retention, "neither
// exists" is the steady state of every old task.
//
// Treating that as ErrInvariantBroken halted FinalizeBlock and then refused the
// replay on restart, so the chain could not be brought back up.
func TestSessionHistoryPruneToleratesPrunedTaskRecords(t *testing.T) {
	f := initInternalFixture(t)
	sessionKey, taskKey, _ := closedSessionWithTerminalOrder(t, f, 0x51)
	requireNoTaskRecords(t, f, taskKey)

	result, err := f.keeper.SweepSessionHistoryPrune(f.ctx, 10, uint64(types.DefaultMaxSessionHistoryPruneItemsPerBlock))
	require.NoError(t, err, "a pruned task history must not stop the sweep")
	require.Equal(t, SessionHistoryPruneResult{VisitedCount: 1, CompactedCount: 1}, result)

	// Folding without the assertion is state-identical: the check fed neither the
	// rolling root nor the status counts, so the compacted summary is exactly what
	// a session with intact task records would have produced.
	summary, err := f.keeper.ReadSessionTerminalSummary(f.ctx, sessionKey)
	require.NoError(t, err)
	require.Equal(t, uint32(1), summary.SequenceCount)
	require.Equal(t, uint32(1), summary.ConsumedCount)
	require.Len(t, summary.SequenceRoot, types.Hash32Len)
}

// The other half of the boundary: tolerating absence must not tolerate a record
// that contradicts the order. A summary naming a different task is a real
// storage contradiction and still has to stop the sweep.
func TestSessionHistoryPruneStillRejectsForeignTerminalSummary(t *testing.T) {
	f := initInternalFixture(t)
	_, taskKey, _ := closedSessionWithTerminalOrder(t, f, 0x52)
	stored, err := f.keeper.taskTerminalSummaryToStore(types.TaskTerminalSummaryState{
		TaskId:        bytes.Repeat([]byte{0x99}, types.Hash32Len),
		TerminalPhase: types.TaskPhase_TASK_PHASE_SETTLED,
	})
	require.NoError(t, err)
	require.NoError(t, f.keeper.TaskTerminalSummary.Set(f.ctx, taskKey, stored))

	_, err = f.keeper.SweepSessionHistoryPrune(f.ctx, 10, uint64(types.DefaultMaxSessionHistoryPruneItemsPerBlock))
	require.ErrorIs(t, err, types.ErrInvariantBroken)
}

// And escrow still reserved under a terminal order stays an invariant too: the
// order says the task ended, the ledger says its funds are still held.
func TestSessionHistoryPruneStillRejectsReservedBudget(t *testing.T) {
	f := initInternalFixture(t)
	_, taskKey, taskID := closedSessionWithTerminalOrder(t, f, 0x53)
	require.NoError(t, f.keeper.TaskBudget.Set(f.ctx, taskKey, types.TaskBudgetState{
		TaskId:       taskID,
		BudgetStatus: types.TaskBudgetStatus_TASK_BUDGET_STATUS_RESERVED,
	}))

	_, err := f.keeper.SweepSessionHistoryPrune(f.ctx, 10, uint64(types.DefaultMaxSessionHistoryPruneItemsPerBlock))
	require.ErrorIs(t, err, types.ErrInvariantBroken)
}

func requireNoTaskRecords(t *testing.T, f *internalFixture, taskKey types.TaskKey) {
	t.Helper()
	hasBudget, err := f.keeper.TaskBudget.Has(f.ctx, taskKey)
	require.NoError(t, err)
	require.False(t, hasBudget)
	hasSummary, err := f.keeper.TaskTerminalSummary.Has(f.ctx, taskKey)
	require.NoError(t, err)
	require.False(t, hasSummary)
}
