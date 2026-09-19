package keeper

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	"github.com/TrueOpen/node/x/task/types"
)

type taskEndBlockBudgetStub struct {
	internalStubHubKeeper
	height uint64
	items  uint64
	bytes  uint64
}

func (s *taskEndBlockBudgetStub) GetEndBlockBudget(_ context.Context, height uint64) (hubtypes.EndBlockBudgetSnapshot, error) {
	if height != s.height {
		return hubtypes.EndBlockBudgetSnapshot{}, fmt.Errorf("unexpected budget height %d", height)
	}
	return hubtypes.EndBlockBudgetSnapshot{Height: height, RemainingItems: s.items, RemainingBytes: s.bytes}, nil
}

func (s *taskEndBlockBudgetStub) ConsumeEndBlockBudget(_ context.Context, height, items, serializedBytes uint64) error {
	if height != s.height || items > s.items {
		return fmt.Errorf("invalid budget consumption")
	}
	s.items -= items
	if serializedBytes >= s.bytes {
		s.bytes = 0
	} else {
		s.bytes -= serializedBytes
	}
	return nil
}

// taskKeyFromByte builds a distinct 32-byte Hash32 store key from a single byte.
// bytes.Repeat allocates a fresh slice per call, so two rows built here never
// alias the same backing array.
func taskKeyFromByte(b byte) types.TaskKey {
	return bytes.Repeat([]byte{b}, types.Hash32Len)
}

// P1-07 regression. Before the fix, sweepExpiredTaskDeadlines' predecessor only
// counted rows whose primary actually advanced, and the EndBlock fair-share limit
// is one item per queue per turn. A single row that always came back
// "recoverable" therefore kept the counter at zero, the per-block bound never
// tripped, and the loop walked the entire expired prefix — every block, on every
// validator, at zero cost to whoever planted the row.
//
// The three assertions below are the exact properties that close it:
//
//  1. with maxItems = 1 the driver visits exactly one row even though many are
//     due, no matter what the handler reports;
//  2. a handler that never advances (deadlineSweepPending) still charges one
//     visited item per call, so the caller's budget strictly decreases;
//  3. the iteration is bounded by maxItems, not by the number of due rows, so a
//     poison row cannot amplify the scan.
func TestDeadlineSweepChargesVisitedItemsForPoisonRows(t *testing.T) {
	f := initInternalFixture(t)

	dueRows := 32
	for i := 0; i < dueRows; i++ {
		require.NoError(t, addDeadlineIndex(f.ctx, f.keeper.InferDeadlineIndex, taskKeyFromByte(byte(i+1)), 10))
	}

	handlerCalls := 0
	poison := func(context.Context, types.TaskKey, uint64) (deadlineSweepOutcome, uint64, error) {
		handlerCalls++
		// Never advances and never lets the row be deleted.
		return deadlineSweepPending, 0, nil
	}

	for block := 0; block < 5; block++ {
		visited, err := f.keeper.sweepExpiredTaskDeadlines(f.ctx, f.keeper.InferDeadlineIndex, 100, 1, poison)
		require.NoError(t, err)
		// (1) + (2): exactly one visited item is charged per block.
		require.Equal(t, uint64(1), visited, "poison row must charge exactly one visited item")
	}
	// (3): five blocks at a fair share of one item may only touch five rows.
	require.Equal(t, 5, handlerCalls)

	// All rows are still due: the sweep never silently drained them.
	remaining := 0
	iter, err := f.keeper.InferDeadlineIndex.Iterate(f.ctx, nil)
	require.NoError(t, err)
	for ; iter.Valid(); iter.Next() {
		remaining++
	}
	require.NoError(t, iter.Close())
	require.Equal(t, dueRows, remaining)
}

// A provably stale row is counted *and* deleted (§4.6 line 713), so the index
// self-heals instead of accumulating orphans.
func TestDeadlineSweepDeletesStaleRows(t *testing.T) {
	f := initInternalFixture(t)
	taskKey := taskKeyFromByte(0x7f)
	require.NoError(t, addDeadlineIndex(f.ctx, f.keeper.InferDeadlineIndex, taskKey, 10))

	// There is no TaskCore row, so the production handler proves the row stale.
	visited, err := f.keeper.processExpiredInferDeadlines(f.ctx, 100, 4)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)

	has, err := f.keeper.InferDeadlineIndex.Has(f.ctx, types.NewDeadlineIndexKey(10, taskKey))
	require.NoError(t, err)
	require.False(t, has, "a stale deadline index row must be deleted")
}

// Rows whose deadline height is still in the future are never visited (§7 line
// 2021: EndBlock only processes height >= deadline_height).
func TestDeadlineSweepSkipsFutureRows(t *testing.T) {
	f := initInternalFixture(t)
	require.NoError(t, addDeadlineIndex(f.ctx, f.keeper.InferDeadlineIndex, taskKeyFromByte(0x11), 500))

	visited, err := f.keeper.sweepExpiredTaskDeadlines(f.ctx, f.keeper.InferDeadlineIndex, 100, 8,
		func(context.Context, types.TaskKey, uint64) (deadlineSweepOutcome, uint64, error) {
			t.Fatal("a future deadline row must not be visited")
			return deadlineSweepStale, 0, nil
		})
	require.NoError(t, err)
	require.Zero(t, visited)
}

func TestDeadlineSweepByteBudgetChargesStaleAndPendingRows(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outcome deadlineSweepOutcome
	}{
		{name: "stale", outcome: deadlineSweepStale},
		{name: "pending", outcome: deadlineSweepPending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := initInternalFixture(t)
			first := taskKeyFromByte(0x21)
			second := taskKeyFromByte(0x22)
			require.NoError(t, addDeadlineIndex(f.ctx, f.keeper.InferDeadlineIndex, first, 10))
			require.NoError(t, addDeadlineIndex(f.ctx, f.keeper.InferDeadlineIndex, second, 10))

			rowBytes := deadlineIndexRowBytes(first, false)
			usage, err := f.keeper.sweepExpiredTaskDeadlinesWithBudget(
				f.ctx, f.keeper.InferDeadlineIndex, 10, 2, rowBytes,
				func(context.Context, types.TaskKey, uint64) (deadlineSweepOutcome, uint64, error) {
					return tc.outcome, 0, nil
				},
			)
			require.NoError(t, err)
			require.Equal(t, uint64(1), usage.visited)
			require.Equal(t, rowBytes, usage.serializedBytes)

			secondExists, err := f.keeper.InferDeadlineIndex.Has(f.ctx, types.NewDeadlineIndexKey(10, second))
			require.NoError(t, err)
			require.True(t, secondExists, "the shared byte budget must stop before the second row")
		})
	}
}

func TestEndBlockDeadlineByteBudgetIsSharedAcrossQueues(t *testing.T) {
	f := initInternalFixture(t)
	revealTask := taskKeyFromByte(0x31)
	openTask := taskKeyFromByte(0x32)
	inferTask := taskKeyFromByte(0x33)
	require.NoError(t, addDeadlineIndex(f.ctx, f.keeper.RevealDeadlineIndex, revealTask, 10))
	require.NoError(t, addDeadlineIndex(f.ctx, f.keeper.VerifyOpenDeadlineIndex, openTask, 10))
	require.NoError(t, addDeadlineIndex(f.ctx, f.keeper.InferDeadlineIndex, inferTask, 10))

	result, serializedBytes, err := f.keeper.runEndBlockDeadlineSweeps(
		f.ctx, 10, 4, 2*taskDeadlineConservativeBytesV1,
	)
	require.NoError(t, err)
	require.Equal(t, uint64(2), result.Total)
	require.Equal(t, uint64(1), result.RevealDeadline)
	require.Equal(t, uint64(1), result.VerifyOpenDeadline)
	require.Zero(t, result.InferDeadline)
	require.Equal(t, 2*taskDeadlineConservativeBytesV1, serializedBytes)

	inferExists, err := f.keeper.InferDeadlineIndex.Has(f.ctx, types.NewDeadlineIndexKey(10, inferTask))
	require.NoError(t, err)
	require.True(t, inferExists, "changing queues must not reset the serialized-byte budget")
}

// §5.9: all twelve Wire v0.3 kind_priority values, and only those, resolve.
func TestDeadlineKindPriorityTableIsComplete(t *testing.T) {
	expected := map[types.DeadlineKindV1]uint32{
		types.DeadlineKindV1_DEADLINE_KIND_V1_TASK_FINALITY:          30,
		types.DeadlineKindV1_DEADLINE_KIND_V1_TASK_SETTLEMENT:        40,
		types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_ROUND_CLOSE:     20,
		types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_REVEAL:          50,
		types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_COMMIT:          60,
		types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_OPEN:            70,
		types.DeadlineKindV1_DEADLINE_KIND_V1_WORKER_INFER:           80,
		types.DeadlineKindV1_DEADLINE_KIND_V1_WORKER_ASSIGNMENT:      90,
		types.DeadlineKindV1_DEADLINE_KIND_V1_SESSION_LIFECYCLE:      100,
		types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_FINAL:           45,
		types.DeadlineKindV1_DEADLINE_KIND_V1_CHALLENGE_WINDOW_CLOSE: 25,
		types.DeadlineKindV1_DEADLINE_KIND_V1_EVIDENCE_CLEANUP:       110,
	}
	require.Len(t, deadlineKindPriority, len(expected))
	for kind, priority := range expected {
		got, err := DeadlineKindPriority(kind)
		require.NoError(t, err)
		require.Equal(t, priority, got, "kind %d", int32(kind))
		require.Equal(t, endBlockQueuePriority(priority), endBlockDeadlinePriority(kind), "EndBlock must consume §5.9 kind_priority")
	}
	_, err := DeadlineKindPriority(types.DeadlineKindV1_DEADLINE_KIND_V1_UNSPECIFIED)
	require.Error(t, err, "UNSPECIFIED must be rejected before any state read")
	_, err = DeadlineKindPriority(types.DeadlineKindV1(99))
	require.Error(t, err)
}

// §5.9 line 964: the single same-height ordering rule is
// (deadline_height, kind_priority, primary_id).
func TestSortDeadlineWorkItemsUsesTheSingleOrderingRule(t *testing.T) {
	items := []deadlineWorkItem{
		{DeadlineHeight: 11, Kind: types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_ROUND_CLOSE, PrimaryID: "a"},
		{DeadlineHeight: 10, Kind: types.DeadlineKindV1_DEADLINE_KIND_V1_SESSION_LIFECYCLE, PrimaryID: "a"},
		{DeadlineHeight: 10, Kind: types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_ROUND_CLOSE, PrimaryID: "b"},
		{DeadlineHeight: 10, Kind: types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_ROUND_CLOSE, PrimaryID: "a"},
	}
	require.NoError(t, SortDeadlineWorkItems(items))
	require.Equal(t, []deadlineWorkItem{
		{DeadlineHeight: 10, Kind: types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_ROUND_CLOSE, PrimaryID: "a"},
		{DeadlineHeight: 10, Kind: types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_ROUND_CLOSE, PrimaryID: "b"},
		{DeadlineHeight: 10, Kind: types.DeadlineKindV1_DEADLINE_KIND_V1_SESSION_LIFECYCLE, PrimaryID: "a"},
		{DeadlineHeight: 11, Kind: types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_ROUND_CLOSE, PrimaryID: "a"},
	}, items)
}

func TestSpecificTaskDeadlineDoesNotAdvanceQueueHeadAndStaleIsNotAdvanced(t *testing.T) {
	f := initInternalFixture(t)
	headTask := taskKeyFromByte(0x01)
	targetTask := taskKeyFromByte(0x02)
	require.NoError(t, addDeadlineIndex(f.ctx, f.keeper.InferDeadlineIndex, headTask, 5))
	require.NoError(t, addDeadlineIndex(f.ctx, f.keeper.InferDeadlineIndex, targetTask, 10))

	var handled types.TaskKey
	visited, advanced, err := f.keeper.sweepSpecificTaskDeadline(
		f.ctx, f.keeper.InferDeadlineIndex, targetTask, 10, 10,
		func(_ context.Context, taskID types.TaskKey, _ uint64) (deadlineSweepOutcome, uint64, error) {
			handled = taskID
			return deadlineSweepStale, 0, nil
		},
	)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	require.Zero(t, advanced, "deleting a stale row is not a primary-state transition")
	require.True(t, bytes.Equal(targetTask, handled),
		"handler must receive the requested task: want %s, got %s",
		hex.EncodeToString(targetTask), hex.EncodeToString(handled))
	headExists, err := f.keeper.InferDeadlineIndex.Has(f.ctx, types.NewDeadlineIndexKey(5, headTask))
	require.NoError(t, err)
	require.True(t, headExists, "the unrelated queue head must remain untouched")
	targetExists, err := f.keeper.InferDeadlineIndex.Has(f.ctx, types.NewDeadlineIndexKey(10, targetTask))
	require.NoError(t, err)
	require.False(t, targetExists)
}

func TestTaskDeadlineLocatorTargetsRequestedTask(t *testing.T) {
	f := initInternalFixture(t)
	headTask := taskKeyFromByte(0x11)
	targetID := bytes.Repeat([]byte{0x22}, types.Hash32Len)
	targetTask := types.NewTaskKey(targetID)
	require.NoError(t, addDeadlineIndex(f.ctx, f.keeper.InferDeadlineIndex, headTask, 5))
	require.NoError(t, addDeadlineIndex(f.ctx, f.keeper.InferDeadlineIndex, targetTask, 10))
	require.NoError(t, f.keeper.TaskCore.Set(f.ctx, targetTask, types.TaskCoreState{TaskId: targetID}))
	require.NoError(t, f.keeper.TaskAssignment.Set(f.ctx, targetTask, types.TaskAssignmentState{
		TaskId: targetID, InferDeadlineHeight: 10,
	}))

	response, err := f.keeper.sweepTaskDeadline(f.ctx, &types.TaskDeadlineLocator{
		TaskId: targetID, DeadlineKind: types.DeadlineKindV1_DEADLINE_KIND_V1_WORKER_INFER,
	}, 10)
	require.NoError(t, err)
	require.Equal(t, uint32(1), response.Visited)
	require.Zero(t, response.Advanced, "stale cleanup must report NOOP, not a task transition")
	headExists, err := f.keeper.InferDeadlineIndex.Has(f.ctx, types.NewDeadlineIndexKey(5, headTask))
	require.NoError(t, err)
	require.True(t, headExists)
	targetExists, err := f.keeper.InferDeadlineIndex.Has(f.ctx, types.NewDeadlineIndexKey(10, targetTask))
	require.NoError(t, err)
	require.False(t, targetExists)
}

func TestEndBlockDeadlineQueueCumulativeCap(t *testing.T) {
	f := initInternalFixture(t)
	for i := byte(1); i <= 5; i++ {
		require.NoError(t, addDeadlineIndex(f.ctx, f.keeper.InferDeadlineIndex, taskKeyFromByte(i), 10))
	}

	result, err := f.keeper.RunEndBlockDeadlineSweeps(f.ctx, 10, 2)
	require.NoError(t, err)
	require.Equal(t, uint64(2), result.InferDeadline)
	require.Equal(t, uint64(2), result.Total)
}

func TestTaskEndBlockConsumesOnlyTheBudgetLeftByHub(t *testing.T) {
	f := initInternalFixture(t)
	first := taskKeyFromByte(0x61)
	second := taskKeyFromByte(0x62)
	require.NoError(t, addDeadlineIndex(f.ctx, f.keeper.InferDeadlineIndex, first, 10))
	require.NoError(t, addDeadlineIndex(f.ctx, f.keeper.InferDeadlineIndex, second, 10))

	budget := &taskEndBlockBudgetStub{height: 10, items: 1, bytes: 1 << 20}
	f.keeper.hubKeeper = budget
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(10))
	require.NoError(t, f.keeper.EndBlocker(f.ctx))
	require.Zero(t, budget.items)

	countRows := func() int {
		iter, err := f.keeper.InferDeadlineIndex.Iterate(f.ctx, nil)
		require.NoError(t, err)
		defer iter.Close()
		count := 0
		for ; iter.Valid(); iter.Next() {
			count++
		}
		return count
	}
	require.Equal(t, 1, countRows())
	require.NoError(t, f.keeper.EndBlocker(f.ctx))
	require.Equal(t, 1, countRows(), "the same block cannot restart the shared budget")
}

func TestEndBlockSessionPrunesAdvanceWithinGlobalBudget(t *testing.T) {
	f := initInternalFixture(t)
	sessionKey := taskKeyFromByte(0x55)
	require.NoError(t, f.keeper.SessionHistoryPruneIndex.Set(f.ctx, types.NewSessionHistoryPruneIndexKey(1, sessionKey)))
	require.NoError(t, f.keeper.SessionTerminalSummaryPruneIndex.Set(f.ctx, types.NewSessionTerminalSummaryPruneIndexKey(1, sessionKey)))

	result, err := f.keeper.RunEndBlockDeadlineSweeps(f.ctx, 10, 4)
	require.NoError(t, err)
	require.Equal(t, uint64(1), result.SessionHistoryPrune)
	require.Equal(t, uint64(1), result.SessionSummaryPrune)
	require.Equal(t, uint64(2), result.Total)
	historyExists, err := f.keeper.SessionHistoryPruneIndex.Has(f.ctx, types.NewSessionHistoryPruneIndexKey(1, sessionKey))
	require.NoError(t, err)
	require.False(t, historyExists)
	summaryExists, err := f.keeper.SessionTerminalSummaryPruneIndex.Has(f.ctx, types.NewSessionTerminalSummaryPruneIndexKey(1, sessionKey))
	require.NoError(t, err)
	require.False(t, summaryExists)
}
