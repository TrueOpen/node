package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// workerBondRuleRefusal applies the Worker bond rules that need no state of
// their own to a dispatch winner: a paused Worker takes no new order, and a
// Worker whose effective bond is below the floor of the Profile's resource
// tier is not a dispatch candidate. Both refuse the new record only; neither
// is a fault of the Worker, whose accept may predate the pause or a raised
// floor.
func workerBondRuleRefusal(bond hubtypes.ServiceBondSnapshot, terms hubtypes.WorkerQuotaTerms, resourceTier uint32) error {
	if bond.DispatchPaused {
		return fmt.Errorf("winner dispatch is paused (reasons %d)", bond.PauseReasons)
	}
	if floor := terms.BondFloor(resourceTier); bond.EffectiveActiveBond < floor {
		return fmt.Errorf("winner effective bond %d is below the tier %d floor %d", bond.EffectiveActiveBond, resourceTier, floor)
	}
	return nil
}

// reserveWorkerQuota books the order value of a task being recorded against
// the winner's quota for the settlement period of the record height. Without a
// settlement period there is no quota. An exhausted quota refuses this record
// and nothing else.
func (k Keeper) reserveWorkerQuota(ctx context.Context, operator string, orderValue, height uint64, params types.TaskParamsV1) error {
	period, ok := hubtypes.WorkerSettlementPeriod(height, params.Verification.BatchSettlementPeriodBlocks)
	if !ok {
		return nil
	}
	err := k.hubKeeper.ReserveWorkerQuota(ctx, operator, period, orderValue, height)
	if errors.Is(err, hubtypes.ErrWorkerQuotaExceeded) {
		return rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_WINNER_NOT_ELIGIBLE, err)
	}
	return err
}

// settleWorkerQuota releases a final task's reservation down to its actual
// order value. The period is the one the record height fell in under the
// task's own frozen parameters, so a later parameter change cannot move it.
func (k Keeper) settleWorkerQuota(ctx context.Context, core types.TaskCoreState, worker string, actual, height uint64) error {
	params, err := k.ParamsForTask(ctx, types.NewTaskKey(core.TaskId))
	if err != nil {
		return err
	}
	period, ok := hubtypes.WorkerSettlementPeriod(core.CreatedHeight, params.Verification.BatchSettlementPeriodBlocks)
	if !ok || worker == "" {
		return nil
	}
	reserved, err := shared.ParseAmount(core.OrderValue)
	if err != nil {
		return err
	}
	return k.hubKeeper.SettleWorkerQuota(ctx, worker, period, reserved, actual, height)
}

// settledOrderValue is the order value formula applied to the generated token
// count the settlement billed: worker gross plus the verify share.
func settledOrderValue(plan types.SettlementPlanV1, budget types.TaskBudgetState) (uint64, error) {
	priceBid, err := shared.ParseAmount(budget.PriceBid)
	if err != nil {
		return 0, err
	}
	workerGross, ok := mulDivFloorRound(plan.GeneratedTokenCount, priceBid, 1_000_000)
	if !ok {
		return 0, fmt.Errorf("settled worker gross overflows")
	}
	verify, ok := mulDivFloorRound(workerGross, uint64(budget.VerifyRatioBpsSnapshot), uint64(types.BasisPointsMaximum))
	if !ok {
		return 0, fmt.Errorf("settled verify share overflows")
	}
	total, ok := checkedAddU64(workerGross, verify)
	if !ok {
		return 0, fmt.Errorf("settled order value overflows")
	}
	return total, nil
}

// WorkerQuotaPeriodBlocks is the settlement period length the Worker quota is
// counted in; zero when batch settlement is off.
func (k Keeper) WorkerQuotaPeriodBlocks(ctx context.Context) (uint64, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, err
	}
	return params.Verification.BatchSettlementPeriodBlocks, nil
}

// WorkerHasOpenObligations reports whether the operator still holds a task
// that has not reached finality as its Worker.
func (k Keeper) WorkerHasOpenObligations(ctx context.Context, operatorAddress string) (bool, error) {
	worker, err := k.sessionAddressToStore("worker obligations", operatorAddress)
	if err != nil {
		return false, err
	}
	iter, err := k.WorkerActiveTaskIndex.Iterate(ctx, collections.NewPrefixedPairRange[types.AddrKey, types.Hash32Key](worker))
	if err != nil {
		return false, err
	}
	defer iter.Close()
	return iter.Valid(), nil
}
