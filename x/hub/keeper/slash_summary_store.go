package keeper

import (
	"bytes"
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (k Keeper) slashSummaryToStore(state types.SlashSummaryState) (internaltypes.SlashSummaryStoreState, error) {
	address, err := k.accountAddressToStore("slash summary operator", state.OperatorAddress, false)
	if err != nil {
		return internaltypes.SlashSummaryStoreState{}, err
	}
	stored := internaltypes.SlashSummaryStoreState{
		SlashSummaryId:                      append([]byte(nil), state.SlashSummaryId...),
		SourceKind:                          int32(state.SourceKind),
		SourceId:                            append([]byte(nil), state.SourceId...),
		EffectIndex:                         state.EffectIndex,
		OperatorAddress:                     address,
		Duty:                                int32(state.Duty),
		RequestedAmountAtomicUnits:          state.RequestedAmount.AtomicUnits,
		PendingTaskEarningsDebitAtomicUnits: state.PendingTaskEarningsDebit.AtomicUnits,
		ActiveBondDebitAtomicUnits:          state.ActiveBondDebit.AtomicUnits,
		UnbondingDebitAtomicUnits:           state.UnbondingDebit.AtomicUnits,
		ClaimableEarningsDebitAtomicUnits:   state.ClaimableEarningsDebit.AtomicUnits,
		AppliedAmountAtomicUnits:            state.AppliedAmount.AtomicUnits,
		UnfilledAmountAtomicUnits:           state.UnfilledAmount.AtomicUnits,
		Destination:                         int32(state.Destination),
		UnbondingRowsVisited:                state.UnbondingRowsVisited,
		AppliedHeight:                       state.AppliedHeight,
		BondVersion:                         state.BondVersion,
	}
	if state.XTaskId != nil {
		stored.XTaskId = &internaltypes.SlashSummaryStoreState_TaskId{TaskId: append([]byte(nil), state.GetTaskId()...)}
	}
	return stored, nil
}

func (k Keeper) ProjectSlashSummaryStore(stored internaltypes.SlashSummaryStoreState) (types.SlashSummaryState, error) {
	address, err := k.accountAddressFromStore("slash summary operator", stored.OperatorAddress, false)
	if err != nil {
		return types.SlashSummaryState{}, err
	}
	for _, amount := range []struct{ name, atomicUnits string }{
		{"requested", stored.RequestedAmountAtomicUnits},
		{"pending task earnings debit", stored.PendingTaskEarningsDebitAtomicUnits},
		{"active bond debit", stored.ActiveBondDebitAtomicUnits},
		{"unbonding debit", stored.UnbondingDebitAtomicUnits},
		{"claimable earnings debit", stored.ClaimableEarningsDebitAtomicUnits},
		{"applied", stored.AppliedAmountAtomicUnits},
		{"unfilled", stored.UnfilledAmountAtomicUnits},
	} {
		if _, err := shared.ParseAmount(shared.Amount{AtomicUnits: amount.atomicUnits}); err != nil {
			return types.SlashSummaryState{}, fmt.Errorf("stored slash summary %s: %w", amount.name, err)
		}
	}
	state := types.SlashSummaryState{
		SlashSummaryId:           append([]byte(nil), stored.SlashSummaryId...),
		SourceKind:               types.SlashSourceKind(stored.SourceKind),
		SourceId:                 append([]byte(nil), stored.SourceId...),
		EffectIndex:              stored.EffectIndex,
		OperatorAddress:          address,
		Duty:                     shared.Duty(stored.Duty),
		RequestedAmount:          shared.Amount{AtomicUnits: stored.RequestedAmountAtomicUnits},
		PendingTaskEarningsDebit: shared.Amount{AtomicUnits: stored.PendingTaskEarningsDebitAtomicUnits},
		ActiveBondDebit:          shared.Amount{AtomicUnits: stored.ActiveBondDebitAtomicUnits},
		UnbondingDebit:           shared.Amount{AtomicUnits: stored.UnbondingDebitAtomicUnits},
		ClaimableEarningsDebit:   shared.Amount{AtomicUnits: stored.ClaimableEarningsDebitAtomicUnits},
		AppliedAmount:            shared.Amount{AtomicUnits: stored.AppliedAmountAtomicUnits},
		UnfilledAmount:           shared.Amount{AtomicUnits: stored.UnfilledAmountAtomicUnits},
		Destination:              types.SlashDestination(stored.Destination),
		UnbondingRowsVisited:     stored.UnbondingRowsVisited,
		AppliedHeight:            stored.AppliedHeight,
		BondVersion:              stored.BondVersion,
	}
	if stored.XTaskId != nil {
		state.XTaskId = &types.SlashSummaryState_TaskId{TaskId: append([]byte(nil), stored.GetTaskId()...)}
	}
	return state, nil
}

func slashSummaryKeyMatches(key types.SlashSummaryKeyTriple, state types.SlashSummaryState) bool {
	source, err := types.SlashSummarySourceKey(state.SourceKind, state.SourceId)
	return err == nil && key.K1() == int32(state.SourceKind) && bytes.Equal(key.K2(), source) && key.K3() == state.EffectIndex
}

func (k Keeper) ReadSlashSummaryValue(ctx context.Context, key types.SlashSummaryKeyTriple) (types.SlashSummaryState, error) {
	stored, err := k.SlashSummary.Get(ctx, key)
	if err != nil {
		return types.SlashSummaryState{}, err
	}
	state, err := k.ProjectSlashSummaryStore(stored)
	if err != nil {
		return types.SlashSummaryState{}, err
	}
	if !slashSummaryKeyMatches(key, state) {
		return types.SlashSummaryState{}, fmt.Errorf("slash summary key/value mismatch")
	}
	return state, nil
}

func (k Keeper) WriteSlashSummaryValue(ctx context.Context, key types.SlashSummaryKeyTriple, state types.SlashSummaryState) error {
	if !slashSummaryKeyMatches(key, state) {
		return fmt.Errorf("slash summary key/value mismatch")
	}
	stored, err := k.slashSummaryToStore(state)
	if err != nil {
		return err
	}
	return k.SlashSummary.Set(ctx, key, stored)
}

func (k Keeper) exportSlashSummaries(ctx context.Context) ([]types.SlashSummaryState, error) {
	iter, err := k.SlashSummary.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.SlashSummaryState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		state, err := k.ProjectSlashSummaryStore(entry.Value)
		if err != nil {
			return nil, err
		}
		if !slashSummaryKeyMatches(entry.Key, state) {
			return nil, fmt.Errorf("slash summary key/value mismatch")
		}
		states = append(states, state)
	}
	return states, nil
}
