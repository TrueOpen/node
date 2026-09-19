package keeper

import (
	"bytes"
	"context"
	"errors"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// refundTaskBudget releases the entire remaining balance on a failed task.
// Exact replay after FINALIZED is a no-op and never moves funds twice.
func (k Keeper) refundTaskBudget(ctx context.Context, taskID []byte) (uint64, error) {
	taskKey, err := taskStoreKey(taskID)
	if err != nil {
		return 0, err
	}
	budget, err := k.TaskBudget.Get(ctx, taskKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return 0, errorsmod.Wrap(types.ErrInvalidSettlement, "task budget not found")
		}
		return 0, err
	}
	if !bytes.Equal(budget.TaskId, taskID) {
		return 0, errorsmod.Wrap(types.ErrInvariantBroken, "task budget primary key does not match task_id")
	}
	if budget.BudgetStatus == types.TaskBudgetStatus_TASK_BUDGET_STATUS_FINALIZED {
		reserved, err := shared.ParseAmount(budget.ReservedAmount)
		if err != nil || reserved != 0 {
			return 0, errorsmod.Wrap(types.ErrInvariantBroken, "finalized task budget must have zero reserved_amount")
		}
		// Repair the only safe partial replay state (budget persisted after the
		// bank refund but before the order audit row), while rejecting a terminal
		// row owned by another task or finalized as SETTLED.
		if err := k.finalizeOrderSequenceState(ctx, taskID, types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_REFUNDED); err != nil {
			return 0, err
		}
		return 0, nil
	}
	if budget.BudgetStatus != types.TaskBudgetStatus_TASK_BUDGET_STATUS_RESERVED {
		return 0, errorsmod.Wrapf(types.ErrInvariantBroken, "unsupported task budget status %s", budget.BudgetStatus)
	}
	refundAmount, err := shared.ParseAmount(budget.ReservedAmount)
	if err != nil {
		return 0, errorsmod.Wrap(types.ErrInvariantBroken, "task budget reserved_amount is not canonical")
	}
	core, err := k.TaskCore.Get(ctx, taskKey)
	if err != nil {
		return 0, errorsmod.Wrap(types.ErrTaskNotFound, "task core not found for budget refund")
	}
	sessionKey, err := sessionStoreKey(core.SessionId)
	if err != nil {
		return 0, err
	}
	stream, err := k.Stream.Get(ctx, sessionKey)
	if err != nil {
		return 0, errorsmod.Wrap(types.ErrInvalidSessionID, "session not found for budget refund")
	}
	if err := k.releaseTaskBudgetRefund(ctx, stream.OwnerUserAddress, refundAmount); err != nil {
		return 0, err
	}
	zero := shared.NewAmount(0)
	budget.ReservedAmount = zero
	budget.TxFeeReserveRemaining = zero
	budget.BudgetStatus = types.TaskBudgetStatus_TASK_BUDGET_STATUS_FINALIZED
	if err := k.TaskBudget.Set(ctx, taskKey, budget); err != nil {
		return 0, err
	}
	if err := k.finalizeOrderSequenceState(ctx, taskID, types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_REFUNDED); err != nil {
		return 0, err
	}
	return refundAmount, nil
}

func (k Keeper) releaseTaskBudgetRefund(ctx context.Context, owner string, refundAmount uint64) error {
	if refundAmount == 0 {
		return nil
	}
	ownerBytes, err := k.addressCodec.StringToBytes(owner)
	if err != nil {
		return errorsmod.Wrap(types.ErrInvalidUserAddress, err.Error())
	}
	denom := k.hubKeeper.GetHubParams(sdk.UnwrapSDKContext(ctx)).BusinessDenom
	if denom == "" {
		return errorsmod.Wrap(types.ErrInvariantBroken, "business denom is unavailable")
	}
	coins := sdk.NewCoins(sdk.NewCoin(denom, sdkmath.NewIntFromUint64(refundAmount)))
	if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, shared.TaskEscrowModuleName, sdk.AccAddress(ownerBytes), coins); err != nil {
		return errorsmod.Wrap(types.ErrInsufficientEscrow, err.Error())
	}
	return nil
}
