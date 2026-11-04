package keeper

import (
	"testing"

	"cosmossdk.io/collections"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func TestBlockedRecordPruneMovesBehindAlreadyDueCleanup(t *testing.T) {
	f := newDispatchFixture(t)
	order := f.signedOrder(t, 0, f.delegateKey)
	f.record(t, f.entry(t, order, f.hub.builders[0]))
	id, err := types.DeriveTaskIDFromRawSession(f.sessionID, 0)
	require.NoError(t, err)
	q := f.keeper.newDispatchDeadlineQueue(dispatchRecordPruning)
	params, err := f.keeper.ParamsForTask(f.ctx, id[:])
	require.NoError(t, err)
	due := uint64(sdk.UnwrapSDKContext(f.ctx).BlockHeight()) + params.Dispatch.DispatchRecordRetentionBlocks
	head, found, err := q.head(f.ctx, due)
	require.NoError(t, err)
	require.True(t, found)
	_, err = q.run(f.ctx, head.deadline, 1, 1<<20)
	require.NoError(t, err)
	old, err := f.keeper.DispatchRecordPrune.Has(f.ctx, collections.Join(head.deadline, id[:]))
	require.NoError(t, err)
	require.False(t, old)
	requeued, err := f.keeper.DispatchRecordPrune.Has(f.ctx, collections.Join(head.deadline+blockedDispatchPruneRetryBlocks, id[:]))
	require.NoError(t, err)
	require.True(t, requeued)
	_, found, err = f.keeper.newDispatchDeadlineQueue(dispatchRecordPruning).head(f.ctx, head.deadline)
	require.NoError(t, err)
	require.False(t, found, "due evidence cleanup can use the next block's budget before this retry")
	_, found, err = f.keeper.newDispatchDeadlineQueue(dispatchRecordPruning).head(f.ctx, head.deadline+1)
	require.NoError(t, err)
	require.False(t, found, "a blocked primary is not charged again in every next block")
}

func TestDispatchChallengeAuthorityBlocksEarlyTaskCleanup(t *testing.T) {
	f := newDispatchFixture(t)
	f.record(t, f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0]))
	id, err := types.DeriveTaskIDFromRawSession(f.sessionID, 0)
	require.NoError(t, err)
	core, err := f.keeper.TaskCore.Get(f.ctx, id[:])
	require.NoError(t, err)
	core.FinalityStatus = shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL
	core.XTaskFinalityHeight = &types.TaskCoreState_TaskFinalityHeight{TaskFinalityHeight: 123}
	require.NoError(t, f.keeper.TaskCore.Set(f.ctx, id[:], core))
	zero := shared.NewAmount(0)
	require.NoError(t, f.keeper.WriteTaskSettlement(f.ctx, id[:], types.TaskSettlementState{TaskId: id[:], TaskFinalityHeight: 123, WorkerGross: zero, WorkerMaintenance: zero, WorkerNet: zero, VerifierSlotGross: zero, MaintenanceFee: zero, RefundAmount: zero, OriginalReservedAmount: zero, GasReimbursedTotal: zero}))
	require.NoError(t, f.keeper.TaskRoundSummary.Set(f.ctx, id[:], types.TaskRoundSummaryState{TaskId: id[:], XRoundsClosedHeight: &types.TaskRoundSummaryState_RoundsClosedHeight{RoundsClosedHeight: 123}, XSettlementFactsCutoffHeight: &types.TaskRoundSummaryState_SettlementFactsCutoffHeight{SettlementFactsCutoffHeight: 123}}))
	blocked, err := f.keeper.taskCleanupBlocked(f.ctx, id[:])
	require.NoError(t, err)
	require.True(t, blocked)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(184))
	blocked, err = f.keeper.taskCleanupBlocked(f.ctx, id[:])
	require.NoError(t, err)
	require.True(t, blocked, "the Session-frozen double-dispatch window is still open")
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(204))
	blocked, err = f.keeper.taskCleanupBlocked(f.ctx, id[:])
	require.NoError(t, err)
	require.False(t, blocked, "cleanup resumes after both frozen challenge clocks close")
}
