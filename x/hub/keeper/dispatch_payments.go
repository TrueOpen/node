package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// PayDispatchCompensation debits the sole compensation ledger and credits the
// existing earnings ledger atomically. The caller owns claim identity/deduplication.
func (k Keeper) PayDispatchCompensation(ctx context.Context, builder, worker string, sessionID, taskID []byte, requested, height uint64) (uint64, error) {
	cacheCtx, commit := sdk.UnwrapSDKContext(ctx).CacheContext()
	paid, _, err := k.debitDispatchAccount(sdk.WrapSDKContext(cacheCtx), builder, worker, sessionID, taskID, requested, 0, height)
	if err != nil {
		return 0, err
	}
	commit()
	return paid, nil
}

// debitDispatchAccount is shared by claims and faults so payment ordering and
// cumulative accounting cannot diverge between the two entry points.
func (k Keeper) debitDispatchAccount(ctx context.Context, builder, worker string, sessionID, taskID []byte, compensation, penalty, height uint64) (uint64, uint64, error) {
	state, err := k.GetBuilderCompensationAccount(ctx, builder)
	if err != nil {
		return 0, 0, err
	}
	balance, err := shared.ParseAmount(state.Balance)
	if err != nil {
		return 0, 0, err
	}
	paid := min(compensation, balance)
	penalized := min(penalty, balance-paid)
	if compensation != 0 {
		if len(sessionID) != 32 || len(taskID) != 32 || height == 0 {
			return 0, 0, fmt.Errorf("dispatch compensation requires session/task identity and height")
		}
		if _, _, err := k.requireCanonicalAddress("compensation Worker", worker); err != nil {
			return 0, 0, err
		}
	}
	if paid != 0 {
		if err := k.sendModuleToModule(ctx, types.BuilderCompensationModuleName, types.RewardsModuleName, paid); err != nil {
			return 0, 0, err
		}
		if err := k.CreditTaskSettlementEarnings(ctx, sessionID, taskID, []types.TaskEarningsCredit{{Beneficiary: worker, Amount: shared.NewAmount(paid)}}, height); err != nil {
			return 0, 0, err
		}
	}
	if penalized != 0 {
		epochLength, err := k.epochLengthBlocks(ctx)
		if err != nil {
			return 0, 0, err
		}
		if err := k.addTreasuryInflow(ctx, epochForHeight(height, epochLength), penalized, height); err != nil {
			return 0, 0, err
		}
		if err := k.sendModuleToModule(ctx, types.BuilderCompensationModuleName, types.TreasuryModuleName, penalized); err != nil {
			return 0, 0, err
		}
	}
	oldPaid, err := shared.ParseAmount(state.TotalPaid)
	if err != nil {
		return 0, 0, err
	}
	oldPenalty, err := shared.ParseAmount(state.TotalPenalized)
	if err != nil {
		return 0, 0, err
	}
	newPaid, err := checkedAdd(oldPaid, paid)
	if err != nil {
		return 0, 0, err
	}
	newPenalty, err := checkedAdd(oldPenalty, penalized)
	if err != nil {
		return 0, 0, err
	}
	state.Balance, state.TotalPaid, state.TotalPenalized = shared.NewAmount(balance-paid-penalized), shared.NewAmount(newPaid), shared.NewAmount(newPenalty)
	if err := k.WriteBuilderCompensationAccount(ctx, state); err != nil {
		return 0, 0, err
	}
	return paid, penalized, nil
}

// ApplyDispatchFault retains one immutable receipt and applies its compensation
// before the independent Treasury penalty. Task authority supplies a deduplicated
// objective scope; the fault key additionally protects exact replays here.
func (k Keeper) ApplyDispatchFault(ctx context.Context, request types.DispatchFaultRequest) (types.DispatchFaultState, bool, error) {
	fault := request.Fault
	if err := fault.Validate(); err != nil {
		return fault, false, err
	}
	key := collections.Join(fault.BuilderAddress, bytes.Clone(fault.FaultId))
	old, err := k.DispatchFault.Get(ctx, key)
	if err == nil {
		row, err := k.projectDispatchFault(old)
		if err != nil {
			return fault, false, err
		}
		if row.Kind != fault.Kind || !bytes.Equal(row.TaskId, fault.TaskId) || !bytes.Equal(row.DispatchRecordHash, fault.DispatchRecordHash) {
			return fault, false, fmt.Errorf("dispatch fault identity conflict")
		}
		return row, false, nil
	}
	if !errors.Is(err, collections.ErrNotFound) {
		return fault, false, err
	}
	prune, overflow := shared.CheckedHeightAddV1(fault.FaultHeight, request.RetentionBlocks)
	if overflow || request.RetentionBlocks == 0 {
		return fault, false, fmt.Errorf("invalid dispatch fault retention")
	}
	cacheCtx, commit := sdk.UnwrapSDKContext(ctx).CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	paid, penalized, err := k.debitDispatchAccount(cache, fault.BuilderAddress, fault.GetCompensatedWorker(), request.SessionID, fault.TaskId, request.Compensation, request.Penalty, fault.FaultHeight)
	if err != nil {
		return fault, false, err
	}
	fault.CompensatedAmount, fault.PenaltyAmount, fault.UnfilledAmount = shared.NewAmount(paid), shared.NewAmount(penalized), shared.NewAmount(request.Penalty-penalized)
	if err := k.writeDispatchFault(cache, fault); err != nil {
		return fault, false, err
	}
	if err := k.DispatchFaultPruneIndex.Set(cache, collections.Join3(prune, fault.BuilderAddress, bytes.Clone(fault.FaultId))); err != nil {
		return fault, false, err
	}
	commit()
	return fault, true, nil
}

func (k Keeper) writeDispatchFault(ctx context.Context, row types.DispatchFaultState) error {
	if err := row.Validate(); err != nil {
		return err
	}
	builder, err := k.accountAddressToStore("fault Builder", row.BuilderAddress, false)
	if err != nil {
		return err
	}
	worker, err := k.accountAddressToStore("compensated Worker", row.GetCompensatedWorker(), true)
	if err != nil {
		return err
	}
	penalty, err := shared.ParseAmount(row.PenaltyAmount)
	if err != nil {
		return err
	}
	unfilled, err := shared.ParseAmount(row.UnfilledAmount)
	if err != nil {
		return err
	}
	compensated, err := shared.ParseAmount(row.CompensatedAmount)
	if err != nil {
		return err
	}
	return k.DispatchFault.Set(ctx, collections.Join(row.BuilderAddress, bytes.Clone(row.FaultId)), internaltypes.DispatchFaultStoreState{BuilderAddress: builder, FaultId: bytes.Clone(row.FaultId), Kind: int32(row.Kind), TaskId: bytes.Clone(row.TaskId), DispatchRecordHash: bytes.Clone(row.DispatchRecordHash), ChallengedTicketHashOrZero32: bytes.Clone(row.ChallengedTicketHashOrZero32), PenaltyAmount: penalty, UnfilledAmount: unfilled, CompensatedWorker: worker, CompensatedAmount: compensated, FaultHeight: row.FaultHeight})
}

func (k Keeper) projectDispatchFault(row internaltypes.DispatchFaultStoreState) (types.DispatchFaultState, error) {
	builder, err := k.accountAddressFromStore("fault Builder", row.BuilderAddress, false)
	if err != nil {
		return types.DispatchFaultState{}, err
	}
	worker, err := k.accountAddressFromStore("compensated Worker", row.CompensatedWorker, true)
	if err != nil {
		return types.DispatchFaultState{}, err
	}
	state := types.DispatchFaultState{BuilderAddress: builder, FaultId: bytes.Clone(row.FaultId), Kind: shared.DispatchChallengeKindV1(row.Kind), TaskId: bytes.Clone(row.TaskId), DispatchRecordHash: bytes.Clone(row.DispatchRecordHash), ChallengedTicketHashOrZero32: bytes.Clone(row.ChallengedTicketHashOrZero32), PenaltyAmount: shared.NewAmount(row.PenaltyAmount), UnfilledAmount: shared.NewAmount(row.UnfilledAmount), CompensatedAmount: shared.NewAmount(row.CompensatedAmount), FaultHeight: row.FaultHeight}
	if worker != "" {
		state.XCompensatedWorker = &types.DispatchFaultState_CompensatedWorker{CompensatedWorker: worker}
	}
	return state, state.Validate()
}
