package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func sdkContextFrom(ctx context.Context) sdk.Context { return sdk.UnwrapSDKContext(ctx) }

func validateTaskBudgetAmounts(budget types.TaskBudgetState) error {
	original, err := shared.ParseAmount(budget.OriginalReservedAmount)
	if err != nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, "original_reserved_amount is not canonical")
	}
	reserved, err := shared.ParseAmount(budget.ReservedAmount)
	if err != nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, "reserved_amount is not canonical")
	}
	if reserved > original {
		return errorsmod.Wrap(types.ErrInvariantBroken, "reserved_amount exceeds original_reserved_amount")
	}
	txReserve, err := shared.ParseAmount(budget.TxFeeReserveRemaining)
	if err != nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, "tx_fee_reserve_remaining is not canonical")
	}
	for name, amount := range map[string]shared.Amount{
		"price_bid": budget.PriceBid, "worker_max": budget.WorkerMax, "verify_max": budget.VerifyMax,
		"max_reimbursement_per_tx_snapshot":   budget.MaxReimbursementPerTxSnapshot,
		"max_reimbursement_per_task_snapshot": budget.MaxReimbursementPerTaskSnapshot,
	} {
		if _, err := shared.ParseAmount(amount); err != nil {
			return errorsmod.Wrapf(types.ErrInvariantBroken, "%s is not canonical", name)
		}
	}
	if _, err := shared.ParseAmount(budget.GasReimbursedTotal); err != nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, "gas_reimbursed_total is not canonical")
	}
	if txReserve > original {
		return errorsmod.Wrap(types.ErrInvariantBroken, "tx_fee_reserve_remaining exceeds the original reservation")
	}
	switch budget.BudgetStatus {
	case types.TaskBudgetStatus_TASK_BUDGET_STATUS_RESERVED:
	case types.TaskBudgetStatus_TASK_BUDGET_STATUS_FINALIZED:
		if reserved != 0 {
			return errorsmod.Wrap(types.ErrInvariantBroken, "finalized task budget must have zero reserved_amount")
		}
	default:
		return errorsmod.Wrap(types.ErrInvariantBroken, "task budget has no budget_status")
	}
	if budget.FeeRuleVersion == 0 {
		return errorsmod.Wrap(types.ErrInvariantBroken, "task budget has no registered fee_rule_version")
	}
	return nil
}
