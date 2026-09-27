package keeper

import (
	"bytes"
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (k Keeper) serviceBondToStore(state types.ServiceBondState) (internaltypes.ServiceBondStoreState, error) {
	address, err := k.accountAddressToStore("service bond operator", state.OperatorAddress, false)
	if err != nil {
		return internaltypes.ServiceBondStoreState{}, err
	}
	return internaltypes.ServiceBondStoreState{
		OperatorAddress:            address,
		ActiveBond:                 state.ActiveBond,
		EffectiveActiveBond:        state.EffectiveActiveBond,
		ReservedLiability:          state.ReservedLiability,
		PendingUnbondingTotal:      state.PendingUnbondingTotal,
		BondVersion:                state.BondVersion,
		EffectiveBondEpoch:         state.EffectiveBondEpoch,
		Status:                     int32(state.Status),
		JailCount:                  state.JailCount,
		NormalActionCountSinceJail: state.NormalActionCountSinceJail,
		LastStakeHeight:            state.LastStakeHeight,
		LastUnstakeHeight:          state.LastUnstakeHeight,
	}, nil
}

func (k Keeper) ProjectServiceBondStore(stored internaltypes.ServiceBondStoreState) (types.ServiceBondState, error) {
	address, err := k.accountAddressFromStore("service bond operator", stored.OperatorAddress, false)
	if err != nil {
		return types.ServiceBondState{}, err
	}
	return types.ServiceBondState{
		OperatorAddress:            address,
		ActiveBond:                 stored.ActiveBond,
		EffectiveActiveBond:        stored.EffectiveActiveBond,
		ReservedLiability:          stored.ReservedLiability,
		PendingUnbondingTotal:      stored.PendingUnbondingTotal,
		BondVersion:                stored.BondVersion,
		EffectiveBondEpoch:         stored.EffectiveBondEpoch,
		Status:                     types.ServiceBondStatus(stored.Status),
		JailCount:                  stored.JailCount,
		NormalActionCountSinceJail: stored.NormalActionCountSinceJail,
		LastStakeHeight:            stored.LastStakeHeight,
		LastUnstakeHeight:          stored.LastUnstakeHeight,
	}, nil
}

func (k Keeper) ReadServiceBondValue(ctx context.Context, key string) (types.ServiceBondState, error) {
	stored, err := k.ServiceBond.Get(ctx, key)
	if err != nil {
		return types.ServiceBondState{}, err
	}
	state, err := k.ProjectServiceBondStore(stored)
	if err != nil {
		return types.ServiceBondState{}, err
	}
	if key != state.OperatorAddress {
		return types.ServiceBondState{}, fmt.Errorf("service bond key/address mismatch")
	}
	return state, nil
}

func (k Keeper) WriteServiceBondValue(ctx context.Context, key string, state types.ServiceBondState) error {
	if key != state.OperatorAddress {
		return fmt.Errorf("service bond key/address mismatch")
	}
	stored, err := k.serviceBondToStore(state)
	if err != nil {
		return err
	}
	return k.ServiceBond.Set(ctx, key, stored)
}

func (k Keeper) exportServiceBonds(ctx context.Context) ([]types.ServiceBondState, error) {
	iter, err := k.ServiceBond.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.ServiceBondState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		state, err := k.ProjectServiceBondStore(entry.Value)
		if err != nil {
			return nil, err
		}
		if entry.Key != state.OperatorAddress {
			return nil, fmt.Errorf("service bond key/address mismatch")
		}
		states = append(states, state)
	}
	return states, nil
}

func (k Keeper) unbondingToStore(state types.UnbondingState) (internaltypes.UnbondingStoreState, error) {
	address, err := k.accountAddressToStore("unbonding operator", state.OperatorAddress, false)
	if err != nil {
		return internaltypes.UnbondingStoreState{}, err
	}
	return internaltypes.UnbondingStoreState{
		UnbondingId:        append([]byte(nil), state.UnbondingId...),
		OperatorAddress:    address,
		Amount:             state.Amount,
		RequestHeight:      state.RequestHeight,
		MatureHeight:       state.MatureHeight,
		Status:             int32(state.Status),
		SlashAppliedAmount: state.SlashAppliedAmount,
	}, nil
}

func (k Keeper) ProjectUnbondingStore(stored internaltypes.UnbondingStoreState) (types.UnbondingState, error) {
	address, err := k.accountAddressFromStore("unbonding operator", stored.OperatorAddress, false)
	if err != nil {
		return types.UnbondingState{}, err
	}
	return types.UnbondingState{
		UnbondingId:        append([]byte(nil), stored.UnbondingId...),
		OperatorAddress:    address,
		Amount:             stored.Amount,
		RequestHeight:      stored.RequestHeight,
		MatureHeight:       stored.MatureHeight,
		Status:             types.UnbondingStatus(stored.Status),
		SlashAppliedAmount: stored.SlashAppliedAmount,
	}, nil
}

func unbondingKeyMatches(key types.UnbondingKeyPair, state types.UnbondingState) bool {
	return key.K1() == state.OperatorAddress && bytes.Equal(key.K2(), state.UnbondingId)
}

func (k Keeper) ReadUnbondingValue(ctx context.Context, key types.UnbondingKeyPair) (types.UnbondingState, error) {
	stored, err := k.Unbonding.Get(ctx, key)
	if err != nil {
		return types.UnbondingState{}, err
	}
	state, err := k.ProjectUnbondingStore(stored)
	if err != nil {
		return types.UnbondingState{}, err
	}
	if !unbondingKeyMatches(key, state) {
		return types.UnbondingState{}, fmt.Errorf("unbonding key/value mismatch")
	}
	return state, nil
}

func (k Keeper) WriteUnbondingValue(ctx context.Context, key types.UnbondingKeyPair, state types.UnbondingState) error {
	if !unbondingKeyMatches(key, state) {
		return fmt.Errorf("unbonding key/value mismatch")
	}
	stored, err := k.unbondingToStore(state)
	if err != nil {
		return err
	}
	return k.Unbonding.Set(ctx, key, stored)
}

func (k Keeper) exportUnbondings(ctx context.Context) ([]types.UnbondingState, error) {
	iter, err := k.Unbonding.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.UnbondingState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		state, err := k.ProjectUnbondingStore(entry.Value)
		if err != nil {
			return nil, err
		}
		if !unbondingKeyMatches(entry.Key, state) {
			return nil, fmt.Errorf("unbonding key/value mismatch")
		}
		states = append(states, state)
	}
	return states, nil
}

func (k Keeper) unbondingReceiptToStore(state types.UnbondingReceiptState) (internaltypes.UnbondingReceiptStoreState, error) {
	address, err := k.accountAddressToStore("unbonding receipt operator", state.OperatorAddress, false)
	if err != nil {
		return internaltypes.UnbondingReceiptStoreState{}, err
	}
	return internaltypes.UnbondingReceiptStoreState{
		UnbondingId:     append([]byte(nil), state.UnbondingId...),
		OperatorAddress: address,
		OriginalAmount:  state.OriginalAmount,
		WithdrawnAmount: state.WithdrawnAmount,
		SlashedAmount:   state.SlashedAmount,
		TerminalStatus:  int32(state.TerminalStatus),
		TerminalHeight:  state.TerminalHeight,
		ReceiptHash:     append([]byte(nil), state.ReceiptHash...),
	}, nil
}

func (k Keeper) ProjectUnbondingReceiptStore(stored internaltypes.UnbondingReceiptStoreState) (types.UnbondingReceiptState, error) {
	address, err := k.accountAddressFromStore("unbonding receipt operator", stored.OperatorAddress, false)
	if err != nil {
		return types.UnbondingReceiptState{}, err
	}
	return types.UnbondingReceiptState{
		UnbondingId:     append([]byte(nil), stored.UnbondingId...),
		OperatorAddress: address,
		OriginalAmount:  stored.OriginalAmount,
		WithdrawnAmount: stored.WithdrawnAmount,
		SlashedAmount:   stored.SlashedAmount,
		TerminalStatus:  types.UnbondingReceiptTerminalStatus(stored.TerminalStatus),
		TerminalHeight:  stored.TerminalHeight,
		ReceiptHash:     append([]byte(nil), stored.ReceiptHash...),
	}, nil
}

func (k Keeper) ReadUnbondingReceiptValue(ctx context.Context, key shared.Hash32Key) (types.UnbondingReceiptState, error) {
	stored, err := k.UnbondingReceipt.Get(ctx, key)
	if err != nil {
		return types.UnbondingReceiptState{}, err
	}
	if !bytes.Equal(key, stored.UnbondingId) {
		return types.UnbondingReceiptState{}, fmt.Errorf("unbonding receipt key/value mismatch")
	}
	return k.ProjectUnbondingReceiptStore(stored)
}

func (k Keeper) WriteUnbondingReceiptValue(ctx context.Context, key shared.Hash32Key, state types.UnbondingReceiptState) error {
	if !bytes.Equal(key, state.UnbondingId) {
		return fmt.Errorf("unbonding receipt key/value mismatch")
	}
	stored, err := k.unbondingReceiptToStore(state)
	if err != nil {
		return err
	}
	return k.UnbondingReceipt.Set(ctx, key, stored)
}

func (k Keeper) exportUnbondingReceipts(ctx context.Context) ([]types.UnbondingReceiptState, error) {
	iter, err := k.UnbondingReceipt.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.UnbondingReceiptState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(entry.Key, entry.Value.UnbondingId) {
			return nil, fmt.Errorf("unbonding receipt key/value mismatch")
		}
		state, err := k.ProjectUnbondingReceiptStore(entry.Value)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}
