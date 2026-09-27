package keeper

import (
	"bytes"
	"context"
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) gasReimbursementToStore(state types.TaskGasReimbursementV1) (internaltypes.TaskGasReimbursementStoreState, error) {
	address, _, err := k.canonicalAddress("gas reimbursement fee payer", state.FeePayer)
	if err != nil {
		return internaltypes.TaskGasReimbursementStoreState{}, err
	}
	return internaltypes.TaskGasReimbursementStoreState{
		TaskId:                      append([]byte(nil), state.TaskId...),
		TxHash:                      append([]byte(nil), state.TxHash...),
		ItemIndex:                   state.ItemIndex,
		ReimbursementKind:           int32(state.ReimbursementKind),
		FeePayer:                    append([]byte(nil), address...),
		GasBasis:                    state.GasBasis,
		ActualFeePaidAtomicUnits:    state.ActualFeePaid.AtomicUnits,
		NecessaryFeeAtomicUnits:     state.NecessaryFee.AtomicUnits,
		ReimbursedAmountAtomicUnits: state.ReimbursedAmount.AtomicUnits,
		FeePolicyVersion:            state.FeePolicyVersion,
		AcceptedHeight:              state.AcceptedHeight,
	}, nil
}

func (k Keeper) ProjectGasReimbursementStore(stored internaltypes.TaskGasReimbursementStoreState) (types.TaskGasReimbursementV1, error) {
	if len(stored.FeePayer) != taskAccountAddressBytes {
		return types.TaskGasReimbursementV1{}, fmt.Errorf("stored gas reimbursement fee payer must be %d bytes", taskAccountAddressBytes)
	}
	if stored.ReimbursementKind == 0 {
		return types.TaskGasReimbursementV1{}, fmt.Errorf("stored gas reimbursement kind is unspecified")
	}
	if _, ok := types.TaskReimbursementKindV1_name[stored.ReimbursementKind]; !ok {
		return types.TaskGasReimbursementV1{}, fmt.Errorf("stored gas reimbursement kind is invalid")
	}
	for _, amount := range []struct{ name, atomicUnits string }{
		{"actual fee paid", stored.ActualFeePaidAtomicUnits},
		{"necessary fee", stored.NecessaryFeeAtomicUnits},
		{"reimbursed amount", stored.ReimbursedAmountAtomicUnits},
	} {
		if _, err := shared.ParseAmount(shared.Amount{AtomicUnits: amount.atomicUnits}); err != nil {
			return types.TaskGasReimbursementV1{}, fmt.Errorf("stored gas reimbursement %s: %w", amount.name, err)
		}
	}
	address, err := k.addressCodec.BytesToString(stored.FeePayer)
	if err != nil {
		return types.TaskGasReimbursementV1{}, fmt.Errorf("encode stored gas reimbursement fee payer: %w", err)
	}
	return types.TaskGasReimbursementV1{
		TaskId:            append([]byte(nil), stored.TaskId...),
		TxHash:            append([]byte(nil), stored.TxHash...),
		ItemIndex:         stored.ItemIndex,
		ReimbursementKind: types.TaskReimbursementKindV1(stored.ReimbursementKind),
		FeePayer:          address,
		GasBasis:          stored.GasBasis,
		ActualFeePaid:     shared.Amount{AtomicUnits: stored.ActualFeePaidAtomicUnits},
		NecessaryFee:      shared.Amount{AtomicUnits: stored.NecessaryFeeAtomicUnits},
		ReimbursedAmount:  shared.Amount{AtomicUnits: stored.ReimbursedAmountAtomicUnits},
		FeePolicyVersion:  stored.FeePolicyVersion,
		AcceptedHeight:    stored.AcceptedHeight,
	}, nil
}

func gasReimbursementKeyMatches(key types.TaskGasReimbursementKey, taskID, txHash []byte, itemIndex uint32) bool {
	return bytes.Equal(key.K1(), taskID) && bytes.Equal(key.K2(), txHash) && key.K3() == itemIndex
}

func (k Keeper) ReadGasReimbursement(ctx context.Context, key types.TaskGasReimbursementKey) (types.TaskGasReimbursementV1, error) {
	stored, err := k.TaskGasReimbursement.Get(ctx, key)
	if err != nil {
		return types.TaskGasReimbursementV1{}, err
	}
	if !gasReimbursementKeyMatches(key, stored.TaskId, stored.TxHash, stored.ItemIndex) {
		return types.TaskGasReimbursementV1{}, fmt.Errorf("gas reimbursement key/value mismatch")
	}
	return k.ProjectGasReimbursementStore(stored)
}

func (k Keeper) WriteGasReimbursement(ctx context.Context, key types.TaskGasReimbursementKey, state types.TaskGasReimbursementV1) error {
	if !gasReimbursementKeyMatches(key, state.TaskId, state.TxHash, state.ItemIndex) {
		return fmt.Errorf("gas reimbursement key/value mismatch")
	}
	stored, err := k.gasReimbursementToStore(state)
	if err != nil {
		return err
	}
	return k.TaskGasReimbursement.Set(ctx, key, stored)
}

func (k Keeper) exportGasReimbursements(ctx context.Context) ([]types.TaskGasReimbursementV1, error) {
	iter, err := k.TaskGasReimbursement.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.TaskGasReimbursementV1{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		if !gasReimbursementKeyMatches(entry.Key, entry.Value.TaskId, entry.Value.TxHash, entry.Value.ItemIndex) {
			return nil, fmt.Errorf("gas reimbursement key/value mismatch")
		}
		state, err := k.ProjectGasReimbursementStore(entry.Value)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}
