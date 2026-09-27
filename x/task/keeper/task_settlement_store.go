package keeper

import (
	"bytes"
	"context"
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) taskSettlementToStore(state types.TaskSettlementState) (internaltypes.TaskSettlementStoreState, error) {
	stored := internaltypes.TaskSettlementStoreState{
		TaskId:                            append([]byte(nil), state.TaskId...),
		SettlementId:                      append([]byte(nil), state.SettlementId...),
		FeeRuleVersion:                    state.FeeRuleVersion,
		EffectiveVerifyRound:              state.EffectiveVerifyRound,
		Verdict:                           int32(state.Verdict),
		FailureClass:                      int32(state.FailureClass),
		SettlementFactsCutoffHeight:       state.SettlementFactsCutoffHeight,
		SettlementHeight:                  state.SettlementHeight,
		TaskFinalityHeight:                state.TaskFinalityHeight,
		GeneratedTokenCount:               state.GeneratedTokenCount,
		WorkUnit:                          state.WorkUnit,
		InferReceiptRefOrZero32:           append([]byte(nil), state.InferReceiptRefOrZero32...),
		WorkerGrossAtomicUnits:            state.WorkerGross.AtomicUnits,
		WorkerMaintenanceAtomicUnits:      state.WorkerMaintenance.AtomicUnits,
		WorkerNetAtomicUnits:              state.WorkerNet.AtomicUnits,
		VerifierSlotGrossAtomicUnits:      state.VerifierSlotGross.AtomicUnits,
		VerifierPayoutCount:               state.VerifierPayoutCount,
		GasReimbursementCount:             state.GasReimbursementCount,
		GasReimbursementsHash:             append([]byte(nil), state.GasReimbursementsHash...),
		MaintenanceFeeAtomicUnits:         state.MaintenanceFee.AtomicUnits,
		RefundAmountAtomicUnits:           state.RefundAmount.AtomicUnits,
		OriginalReservedAmountAtomicUnits: state.OriginalReservedAmount.AtomicUnits,
		SettlementFactsHash:               append([]byte(nil), state.SettlementFactsHash...),
		TaskRoundSummaryHash:              append([]byte(nil), state.TaskRoundSummaryHash...),
		SettlementBillHashOrZero32:        append([]byte(nil), state.SettlementBillHashOrZero32...),
		SettlementPlanHash:                append([]byte(nil), state.SettlementPlanHash...),
		GasReimbursedTotalAtomicUnits:     state.GasReimbursedTotal.AtomicUnits,
	}
	if state.XWorkerOperatorAddress != nil {
		address, _, err := k.canonicalAddress("settlement Worker", state.GetWorkerOperatorAddress())
		if err != nil {
			return internaltypes.TaskSettlementStoreState{}, err
		}
		stored.HasWorkerOperatorAddress = true
		stored.WorkerOperatorAddress = append([]byte(nil), address...)
	}
	return stored, nil
}

func (k Keeper) ProjectTaskSettlementStore(stored internaltypes.TaskSettlementStoreState) (types.TaskSettlementState, error) {
	if !stored.HasWorkerOperatorAddress && len(stored.WorkerOperatorAddress) != 0 {
		return types.TaskSettlementState{}, fmt.Errorf("stored absent settlement Worker has an address body")
	}
	for _, amount := range []struct{ name, atomicUnits string }{
		{"worker gross", stored.WorkerGrossAtomicUnits},
		{"worker maintenance", stored.WorkerMaintenanceAtomicUnits},
		{"worker net", stored.WorkerNetAtomicUnits},
		{"verifier slot gross", stored.VerifierSlotGrossAtomicUnits},
		{"maintenance fee", stored.MaintenanceFeeAtomicUnits},
		{"refund", stored.RefundAmountAtomicUnits},
		{"original reserve", stored.OriginalReservedAmountAtomicUnits},
		{"gas reimbursed total", stored.GasReimbursedTotalAtomicUnits},
	} {
		if _, err := shared.ParseAmount(shared.Amount{AtomicUnits: amount.atomicUnits}); err != nil {
			return types.TaskSettlementState{}, fmt.Errorf("stored settlement %s: %w", amount.name, err)
		}
	}
	state := types.TaskSettlementState{
		TaskId:                      append([]byte(nil), stored.TaskId...),
		SettlementId:                append([]byte(nil), stored.SettlementId...),
		FeeRuleVersion:              stored.FeeRuleVersion,
		EffectiveVerifyRound:        stored.EffectiveVerifyRound,
		Verdict:                     types.TaskVerdict(stored.Verdict),
		FailureClass:                types.TaskFailureClass(stored.FailureClass),
		SettlementFactsCutoffHeight: stored.SettlementFactsCutoffHeight,
		SettlementHeight:            stored.SettlementHeight,
		TaskFinalityHeight:          stored.TaskFinalityHeight,
		GeneratedTokenCount:         stored.GeneratedTokenCount,
		WorkUnit:                    stored.WorkUnit,
		InferReceiptRefOrZero32:     append([]byte(nil), stored.InferReceiptRefOrZero32...),
		WorkerGross:                 shared.Amount{AtomicUnits: stored.WorkerGrossAtomicUnits},
		WorkerMaintenance:           shared.Amount{AtomicUnits: stored.WorkerMaintenanceAtomicUnits},
		WorkerNet:                   shared.Amount{AtomicUnits: stored.WorkerNetAtomicUnits},
		VerifierSlotGross:           shared.Amount{AtomicUnits: stored.VerifierSlotGrossAtomicUnits},
		VerifierPayoutCount:         stored.VerifierPayoutCount,
		GasReimbursementCount:       stored.GasReimbursementCount,
		GasReimbursementsHash:       append([]byte(nil), stored.GasReimbursementsHash...),
		MaintenanceFee:              shared.Amount{AtomicUnits: stored.MaintenanceFeeAtomicUnits},
		RefundAmount:                shared.Amount{AtomicUnits: stored.RefundAmountAtomicUnits},
		OriginalReservedAmount:      shared.Amount{AtomicUnits: stored.OriginalReservedAmountAtomicUnits},
		SettlementFactsHash:         append([]byte(nil), stored.SettlementFactsHash...),
		TaskRoundSummaryHash:        append([]byte(nil), stored.TaskRoundSummaryHash...),
		SettlementBillHashOrZero32:  append([]byte(nil), stored.SettlementBillHashOrZero32...),
		SettlementPlanHash:          append([]byte(nil), stored.SettlementPlanHash...),
		GasReimbursedTotal:          shared.Amount{AtomicUnits: stored.GasReimbursedTotalAtomicUnits},
	}
	if stored.HasWorkerOperatorAddress {
		address, err := k.sessionAddressFromStore("settlement Worker", stored.WorkerOperatorAddress)
		if err != nil {
			return types.TaskSettlementState{}, err
		}
		state.XWorkerOperatorAddress = &types.TaskSettlementState_WorkerOperatorAddress{WorkerOperatorAddress: address}
	}
	return state, nil
}

func (k Keeper) ReadTaskSettlement(ctx context.Context, key types.TaskKey) (types.TaskSettlementState, error) {
	stored, err := k.TaskSettlement.Get(ctx, key)
	if err != nil {
		return types.TaskSettlementState{}, err
	}
	if !bytes.Equal(key, stored.TaskId) {
		return types.TaskSettlementState{}, fmt.Errorf("task settlement key/value mismatch")
	}
	return k.ProjectTaskSettlementStore(stored)
}

func (k Keeper) WriteTaskSettlement(ctx context.Context, key types.TaskKey, state types.TaskSettlementState) error {
	if !bytes.Equal(key, state.TaskId) {
		return fmt.Errorf("task settlement key/value mismatch")
	}
	stored, err := k.taskSettlementToStore(state)
	if err != nil {
		return err
	}
	return k.TaskSettlement.Set(ctx, key, stored)
}

func (k Keeper) exportTaskSettlements(ctx context.Context) ([]types.TaskSettlementState, error) {
	iter, err := k.TaskSettlement.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.TaskSettlementState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(entry.Key, entry.Value.TaskId) {
			return nil, fmt.Errorf("task settlement key/value mismatch")
		}
		state, err := k.ProjectTaskSettlementStore(entry.Value)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}
