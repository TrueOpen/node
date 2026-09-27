package keeper

import (
	"bytes"
	"crypto/sha256"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func TestTaskGasReimbursementBatchSplitsPoolsAndUpdatesEachBudget(t *testing.T) {
	f := initInternalFixture(t)
	sdkCtx := sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(10).WithTxBytes([]byte("gas-batch-tx"))
	ctx := sdk.WrapSDKContext(sdkCtx)
	tasks := [][]byte{bytes.Repeat([]byte{0x31}, 32), bytes.Repeat([]byte{0x32}, 32)}
	for _, taskID := range tasks {
		require.NoError(t, f.keeper.TaskBudget.Set(ctx, types.NewTaskKey(taskID), types.TaskBudgetState{
			TaskId: taskID, TxFeeReserveRemaining: shared.NewAmount(10),
			MaxReimbursableFeePerGasSnapshot: types.FeePerGasRateV1{Numerator: 2, Denominator: 3},
			MaxReimbursementPerTxSnapshot:    shared.NewAmount(10),
			MaxReimbursementPerTaskSnapshot:  shared.NewAmount(10),
			GasReimbursedTotal:               shared.NewAmount(0),
			BudgetStatus:                     types.TaskBudgetStatus_TASK_BUDGET_STATUS_RESERVED,
		}))
	}
	require.NoError(t, f.keeper.recordTaskGasReimbursementIntent(ctx, tasks[0], 0,
		types.TaskReimbursementKindV1_TASK_REIMBURSEMENT_KIND_V1_VERIFY_COMMIT))
	require.NoError(t, f.keeper.recordTaskGasReimbursementIntent(ctx, tasks[1], 1,
		types.TaskReimbursementKindV1_TASK_REIMBURSEMENT_KIND_V1_VERIFY_COMMIT))
	txHash := sha256.Sum256(sdkCtx.TxBytes())
	feePayer := sdk.AccAddress(bytes.Repeat([]byte{0x41}, 20)).String()
	require.NoError(t, f.keeper.ApplyTaskGasReimbursementIntents(ctx, txHash[:], feePayer, 5, 5))

	for index, taskID := range tasks {
		receipt, err := f.keeper.ReadGasReimbursement(ctx,
			types.NewTaskGasReimbursementKey(types.NewTaskKey(taskID), txHash[:], uint32(index)))
		require.NoError(t, err)
		require.Equal(t, shared.NewAmount(2), receipt.ReimbursedAmount)
		budget, err := f.keeper.TaskBudget.Get(ctx, types.NewTaskKey(taskID))
		require.NoError(t, err)
		require.Equal(t, shared.NewAmount(8), budget.TxFeeReserveRemaining)
		require.Equal(t, shared.NewAmount(2), budget.GasReimbursedTotal)
		require.Equal(t, uint32(1), budget.GasReimbursementCount)
	}
}

// Two items of one batch may target the same Task: loadTaskGasReimbursementIntents
// only requires item_index to be unique. Both items must debit the same budget in
// sequence — the second reading what the first already spent — or the second write
// silently reverses the first and the escrow reserve is under-debited.
func TestTaskGasReimbursementBatchItemsOnOneTaskDebitSequentially(t *testing.T) {
	f := initInternalFixture(t)
	sdkCtx := sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(10).WithTxBytes([]byte("gas-same-task-tx"))
	ctx := sdk.WrapSDKContext(sdkCtx)
	taskID := bytes.Repeat([]byte{0x33}, 32)
	require.NoError(t, f.keeper.TaskBudget.Set(ctx, types.NewTaskKey(taskID), types.TaskBudgetState{
		TaskId: taskID, TxFeeReserveRemaining: shared.NewAmount(10),
		MaxReimbursableFeePerGasSnapshot: types.FeePerGasRateV1{Numerator: 2, Denominator: 3},
		MaxReimbursementPerTxSnapshot:    shared.NewAmount(10),
		MaxReimbursementPerTaskSnapshot:  shared.NewAmount(10),
		GasReimbursedTotal:               shared.NewAmount(0),
		BudgetStatus:                     types.TaskBudgetStatus_TASK_BUDGET_STATUS_RESERVED,
	}))
	require.NoError(t, f.keeper.recordTaskGasReimbursementIntent(ctx, taskID, 0,
		types.TaskReimbursementKindV1_TASK_REIMBURSEMENT_KIND_V1_VERIFY_COMMIT))
	require.NoError(t, f.keeper.recordTaskGasReimbursementIntent(ctx, taskID, 1,
		types.TaskReimbursementKindV1_TASK_REIMBURSEMENT_KIND_V1_VERIFY_COMMIT))
	txHash := sha256.Sum256(sdkCtx.TxBytes())
	feePayer := sdk.AccAddress(bytes.Repeat([]byte{0x42}, 20)).String()

	// necessary = ceil(5*2/3) = 4, pool = min(5, 4, 10) = 4, shares 2 and 2.
	require.NoError(t, f.keeper.ApplyTaskGasReimbursementIntents(ctx, txHash[:], feePayer, 5, 5))

	for index := uint32(0); index < 2; index++ {
		receipt, err := f.keeper.ReadGasReimbursement(ctx,
			types.NewTaskGasReimbursementKey(types.NewTaskKey(taskID), txHash[:], index))
		require.NoError(t, err)
		require.Equal(t, shared.NewAmount(2), receipt.ReimbursedAmount)
		// Both receipts commit to the Tx-level totals, not to their own share.
		require.Equal(t, uint64(5), receipt.GasBasis)
		require.Equal(t, shared.NewAmount(5), receipt.ActualFeePaid)
		require.Equal(t, shared.NewAmount(4), receipt.NecessaryFee)
	}
	budget, err := f.keeper.TaskBudget.Get(ctx, types.NewTaskKey(taskID))
	require.NoError(t, err)
	require.Equal(t, shared.NewAmount(6), budget.TxFeeReserveRemaining, "both items must debit the one reserve")
	require.Equal(t, shared.NewAmount(4), budget.GasReimbursedTotal)
	require.Equal(t, uint32(2), budget.GasReimbursementCount)
}

// per_tx_cap is a Tx-level term. Applying it inside the per-item min let an
// n-item batch draw n x cap; the whole batch must fit under one cap.
func TestTaskGasReimbursementPerTxCapBoundsTheWholeBatch(t *testing.T) {
	f := initInternalFixture(t)
	sdkCtx := sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(10).WithTxBytes([]byte("gas-cap-tx"))
	ctx := sdk.WrapSDKContext(sdkCtx)
	tasks := [][]byte{bytes.Repeat([]byte{0x34}, 32), bytes.Repeat([]byte{0x35}, 32)}
	for _, taskID := range tasks {
		require.NoError(t, f.keeper.TaskBudget.Set(ctx, types.NewTaskKey(taskID), types.TaskBudgetState{
			TaskId: taskID, TxFeeReserveRemaining: shared.NewAmount(100),
			MaxReimbursableFeePerGasSnapshot: types.FeePerGasRateV1{Numerator: 1, Denominator: 1},
			MaxReimbursementPerTxSnapshot:    shared.NewAmount(3),
			MaxReimbursementPerTaskSnapshot:  shared.NewAmount(100),
			GasReimbursedTotal:               shared.NewAmount(0),
			BudgetStatus:                     types.TaskBudgetStatus_TASK_BUDGET_STATUS_RESERVED,
		}))
	}
	require.NoError(t, f.keeper.recordTaskGasReimbursementIntent(ctx, tasks[0], 0,
		types.TaskReimbursementKindV1_TASK_REIMBURSEMENT_KIND_V1_VERIFY_COMMIT))
	require.NoError(t, f.keeper.recordTaskGasReimbursementIntent(ctx, tasks[1], 1,
		types.TaskReimbursementKindV1_TASK_REIMBURSEMENT_KIND_V1_VERIFY_COMMIT))
	txHash := sha256.Sum256(sdkCtx.TxBytes())
	feePayer := sdk.AccAddress(bytes.Repeat([]byte{0x43}, 20)).String()

	// gas 100 / fee 100 would each cover far more than the cap. pool = 3, so the
	// two items split 2 and 1 — never 3 and 3.
	require.NoError(t, f.keeper.ApplyTaskGasReimbursementIntents(ctx, txHash[:], feePayer, 100, 100))

	total := uint64(0)
	for index, taskID := range tasks {
		receipt, err := f.keeper.ReadGasReimbursement(ctx,
			types.NewTaskGasReimbursementKey(types.NewTaskKey(taskID), txHash[:], uint32(index)))
		require.NoError(t, err)
		amount, err := shared.ParseAmount(receipt.ReimbursedAmount)
		require.NoError(t, err)
		total += amount
	}
	require.Equal(t, uint64(3), total, "the whole Tx must fit under one per_tx_cap")
}
