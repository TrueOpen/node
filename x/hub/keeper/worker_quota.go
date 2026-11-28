package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
)

// WithWorkerSettlementView supplies the Task-owned settlement period and the
// Worker's open obligations. Without it the quota query reports no period and
// the open-obligation condition on unbonding withdrawals is not evaluated.
func (k Keeper) WithWorkerSettlementView(view types.WorkerSettlementView) Keeper {
	k.workerSettlement = view
	return k
}

func (k Keeper) readWorkerQuota(ctx context.Context, operatorAddress string, period uint64) (types.WorkerQuotaState, bool, error) {
	stored, err := k.WorkerQuota.Get(ctx, types.NewWorkerQuotaKey(operatorAddress, period))
	if errors.Is(err, collections.ErrNotFound) {
		return types.WorkerQuotaState{OperatorAddress: operatorAddress, Period: period}, false, nil
	}
	if err != nil {
		return types.WorkerQuotaState{}, false, err
	}
	return types.WorkerQuotaState{
		OperatorAddress: operatorAddress, Period: period, ReservedValue: stored.ReservedValue,
		SettledValue: stored.SettledValue, UpdatedHeight: stored.UpdatedHeight,
	}, true, nil
}

func (k Keeper) writeWorkerQuota(ctx context.Context, row types.WorkerQuotaState) error {
	return k.WorkerQuota.Set(ctx, types.NewWorkerQuotaKey(row.OperatorAddress, row.Period), internaltypes.WorkerQuotaUsage{
		ReservedValue: row.ReservedValue, SettledValue: row.SettledValue, UpdatedHeight: row.UpdatedHeight,
	})
}

// workerPeriodQuota is the order value the operator may carry in one period
// at the current epoch; zero bps means no quota is configured.
func (k Keeper) workerPeriodQuota(ctx context.Context, operatorAddress string, height uint64) (quota uint64, bps uint32, err error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, 0, err
	}
	bps = params.Service.WorkerQuotaPeriodValueBpsOfBond
	if bps == 0 {
		return 0, 0, nil
	}
	bond, err := k.ReadServiceBondValue(ctx, types.NewServiceBondKey(operatorAddress))
	if err != nil {
		return 0, 0, err
	}
	epochLength, err := k.epochLengthBlocks(ctx)
	if err != nil {
		return 0, 0, err
	}
	return types.WorkerPeriodQuota(types.EffectiveActiveBond(bond, epochForHeight(height, epochLength)), bps), bps, nil
}

// ReserveWorkerQuota books the maximum order value of a task being recorded
// against the Worker's quota for the task's settlement period. It refuses with
// ErrWorkerQuotaExceeded when the value does not fit, and does nothing when no
// quota is configured. A refusal concerns the new record only: nothing
// already reserved or settled is touched and no fault is recorded.
func (k Keeper) ReserveWorkerQuota(ctx context.Context, operatorAddress string, period, value, height uint64) error {
	quota, bps, err := k.workerPeriodQuota(ctx, operatorAddress, height)
	if err != nil || bps == 0 {
		return err
	}
	row, _, err := k.readWorkerQuota(ctx, operatorAddress, period)
	if err != nil {
		return err
	}
	if value > types.WorkerQuotaRemaining(quota, row.ReservedValue, row.SettledValue) {
		return errorsmod.Wrapf(types.ErrWorkerQuotaExceeded, "operator %s period %d", operatorAddress, period)
	}
	row.ReservedValue += value
	row.UpdatedHeight = height
	if err := k.writeWorkerQuota(ctx, row); err != nil {
		return err
	}
	return k.pruneWorkerQuota(ctx, operatorAddress, period)
}

// SettleWorkerQuota replaces a final task's reservation by its actual order
// value. It never fails for a missing or smaller reservation: a task recorded
// while no quota was configured has none, and its release must not disturb the
// task's finality.
func (k Keeper) SettleWorkerQuota(ctx context.Context, operatorAddress string, period, reserved, actual, height uint64) error {
	row, found, err := k.readWorkerQuota(ctx, operatorAddress, period)
	if err != nil || !found {
		return err
	}
	row.ReservedValue -= min(row.ReservedValue, reserved)
	row.SettledValue = saturatingAdd(row.SettledValue, min(actual, reserved))
	row.UpdatedHeight = height
	return k.writeWorkerQuota(ctx, row)
}

// pruneWorkerQuota deletes at most two of the operator's oldest rows that lie
// more than the retention behind the given period and hold no reservation.
func (k Keeper) pruneWorkerQuota(ctx context.Context, operatorAddress string, period uint64) error {
	if period <= types.WorkerQuotaRetentionPeriods {
		return nil
	}
	iter, err := k.WorkerQuota.Iterate(ctx, collections.NewPrefixedPairRange[string, uint64](operatorAddress).EndExclusive(period-types.WorkerQuotaRetentionPeriods))
	if err != nil {
		return err
	}
	var stale []types.WorkerQuotaKeyPair
	for visited := 0; iter.Valid() && visited < 2; iter.Next() {
		visited++
		entry, err := iter.KeyValue()
		if err != nil {
			iter.Close()
			return err
		}
		if entry.Value.ReservedValue == 0 {
			stale = append(stale, entry.Key)
		}
	}
	iter.Close()
	for _, key := range stale {
		if err := k.WorkerQuota.Remove(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

// workerQuotaAnswer evaluates the quota of one operator for one period.
func (k Keeper) workerQuotaAnswer(ctx context.Context, operatorAddress string, requestedPeriod uint64) (*types.QueryWorkerQuotaResponse, error) {
	bond, err := k.ReadServiceBondValue(ctx, types.NewServiceBondKey(operatorAddress))
	if err != nil {
		return nil, err
	}
	answer := &types.QueryWorkerQuotaResponse{Period: requestedPeriod, DispatchPaused: bond.DispatchPaused, PauseReasons: bond.PauseReasons}
	height := sdkContextHeight(sdk.UnwrapSDKContext(ctx))
	quota, bps, err := k.workerPeriodQuota(ctx, operatorAddress, height)
	if err != nil {
		return nil, err
	}
	periodBlocks := uint64(0)
	if k.workerSettlement != nil {
		if periodBlocks, err = k.workerSettlement.WorkerQuotaPeriodBlocks(ctx); err != nil {
			return nil, err
		}
	}
	current, hasPeriod := types.WorkerSettlementPeriod(height, periodBlocks)
	if requestedPeriod == 0 {
		answer.Period = current
	}
	row, _, err := k.readWorkerQuota(ctx, operatorAddress, answer.Period)
	if err != nil {
		return nil, err
	}
	answer.ReservedValue, answer.SettledValue = row.ReservedValue, row.SettledValue
	if bps == 0 || !hasPeriod {
		return answer, nil
	}
	answer.Enforced = true
	answer.Quota = quota
	answer.Remaining = types.WorkerQuotaRemaining(quota, row.ReservedValue, row.SettledValue)
	return answer, nil
}

// requireNoOpenWorkerObligations is the unbond_requires_no_open_tasks
// condition of a withdrawal: the operator has no task without a final verdict,
// no unsettled batch and no audit waiting for a late submission.
func (k Keeper) requireNoOpenWorkerObligations(ctx context.Context, operatorAddress string) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	if !params.Service.UnbondRequiresNoOpenTasks || k.workerSettlement == nil {
		return nil
	}
	open, err := k.workerSettlement.WorkerHasOpenObligations(ctx, operatorAddress)
	if err != nil {
		return err
	}
	if open {
		return errorsmod.Wrap(types.ErrPendingResponsibility, "service unbonding cannot be withdrawn while the operator has tasks that are not final")
	}
	return nil
}

func (k Keeper) exportWorkerQuotas(ctx context.Context) ([]types.WorkerQuotaState, error) {
	iter, err := k.WorkerQuota.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	var rows []types.WorkerQuotaState
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		rows = append(rows, types.WorkerQuotaState{
			OperatorAddress: entry.Key.K1(), Period: entry.Key.K2(), ReservedValue: entry.Value.ReservedValue,
			SettledValue: entry.Value.SettledValue, UpdatedHeight: entry.Value.UpdatedHeight,
		})
	}
	return rows, nil
}

func (k Keeper) importWorkerQuotas(ctx context.Context, rows []types.WorkerQuotaState) error {
	for _, row := range rows {
		if _, canonical, err := k.requireCanonicalAddress("worker quota operator_address", row.OperatorAddress); err != nil || canonical != row.OperatorAddress {
			return fmt.Errorf("worker quota operator_address %q is not canonical", row.OperatorAddress)
		}
		if err := k.writeWorkerQuota(ctx, row); err != nil {
			return err
		}
	}
	return nil
}
