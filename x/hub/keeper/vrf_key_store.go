package keeper

import (
	"bytes"
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
)

func (k Keeper) vrfKeyStateToStore(state types.VrfKeyState) (internaltypes.VrfKeyStoreState, error) {
	address, _, err := k.requireCanonicalAddress("VRF operator address", state.OperatorAddress)
	if err != nil {
		return internaltypes.VrfKeyStoreState{}, err
	}
	stored := internaltypes.VrfKeyStoreState{
		OperatorAddress:       append([]byte(nil), address...),
		ActiveVrfPubkey:       append([]byte(nil), state.ActiveVrfPubkey...),
		ActiveFromEpoch:       state.ActiveFromEpoch,
		VrfAuthorizationNonce: state.VrfAuthorizationNonce,
		RegisteredHeight:      state.RegisteredHeight,
	}
	if pending, ok := state.XPendingVrfPubkey.(*types.VrfKeyState_PendingVrfPubkey); ok {
		if pending == nil {
			return internaltypes.VrfKeyStoreState{}, fmt.Errorf("VRF key has nil pending public key")
		}
		stored.PendingVrfPubkey = append([]byte(nil), pending.PendingVrfPubkey...)
		stored.HasPendingVrfPubkey = true
	} else if state.XPendingVrfPubkey != nil {
		return internaltypes.VrfKeyStoreState{}, fmt.Errorf("VRF key has unknown pending public key")
	}
	if pending, ok := state.XPendingFromEpoch.(*types.VrfKeyState_PendingFromEpoch); ok {
		if pending == nil {
			return internaltypes.VrfKeyStoreState{}, fmt.Errorf("VRF key has nil pending epoch")
		}
		stored.PendingFromEpoch = pending.PendingFromEpoch
		stored.HasPendingFromEpoch = true
	} else if state.XPendingFromEpoch != nil {
		return internaltypes.VrfKeyStoreState{}, fmt.Errorf("VRF key has unknown pending epoch")
	}
	return stored, nil
}

func (k Keeper) vrfKeyStorePublicProjection(stored internaltypes.VrfKeyStoreState) (types.VrfKeyState, error) {
	if len(stored.OperatorAddress) != accountAddressBytes {
		return types.VrfKeyState{}, fmt.Errorf("stored VRF operator address must be %d bytes", accountAddressBytes)
	}
	address, err := k.addressCodec.BytesToString(stored.OperatorAddress)
	if err != nil {
		return types.VrfKeyState{}, fmt.Errorf("encode stored VRF operator address: %w", err)
	}
	state := types.VrfKeyState{
		OperatorAddress:       address,
		ActiveVrfPubkey:       append([]byte(nil), stored.ActiveVrfPubkey...),
		ActiveFromEpoch:       stored.ActiveFromEpoch,
		VrfAuthorizationNonce: stored.VrfAuthorizationNonce,
		RegisteredHeight:      stored.RegisteredHeight,
	}
	if stored.HasPendingVrfPubkey {
		state.XPendingVrfPubkey = &types.VrfKeyState_PendingVrfPubkey{PendingVrfPubkey: append([]byte(nil), stored.PendingVrfPubkey...)}
	} else if len(stored.PendingVrfPubkey) != 0 {
		return types.VrfKeyState{}, fmt.Errorf("stored VRF key has unmarked pending public key")
	}
	if stored.HasPendingFromEpoch {
		state.XPendingFromEpoch = &types.VrfKeyState_PendingFromEpoch{PendingFromEpoch: stored.PendingFromEpoch}
	} else if stored.PendingFromEpoch != 0 {
		return types.VrfKeyState{}, fmt.Errorf("stored VRF key has unmarked pending epoch")
	}
	return state, nil
}

func (k Keeper) GetVrfKey(ctx context.Context, operator string) (types.VrfKeyState, error) {
	raw, canonical, err := k.requireCanonicalAddress("VRF operator address", operator)
	if err != nil {
		return types.VrfKeyState{}, err
	}
	stored, err := k.VrfKey.Get(ctx, canonical)
	if err != nil {
		return types.VrfKeyState{}, err
	}
	if !bytes.Equal(stored.OperatorAddress, raw) {
		return types.VrfKeyState{}, fmt.Errorf("stored VRF key/address mismatch")
	}
	return k.vrfKeyStorePublicProjection(stored)
}

func (k Keeper) StoreVrfKey(ctx context.Context, state types.VrfKeyState) error {
	stored, err := k.vrfKeyStateToStore(state)
	if err != nil {
		return err
	}
	return k.VrfKey.Set(ctx, state.OperatorAddress, stored)
}

func (k Keeper) exportVrfKeys(ctx context.Context) ([]types.VrfKeyState, error) {
	iter, err := k.VrfKey.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.VrfKeyState{}
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		stored, err := iter.Value()
		if err != nil {
			return nil, err
		}
		state, err := k.vrfKeyStorePublicProjection(stored)
		if err != nil {
			return nil, err
		}
		if key != state.OperatorAddress {
			return nil, fmt.Errorf("stored VRF key/address mismatch")
		}
		states = append(states, state)
	}
	return states, nil
}

func (k Keeper) vrfKeyHistoryToStore(state types.VrfKeyHistoryState) (internaltypes.VrfKeyHistoryStoreState, error) {
	address, _, err := k.requireCanonicalAddress("VRF history operator address", state.OperatorAddress)
	if err != nil {
		return internaltypes.VrfKeyHistoryStoreState{}, err
	}
	return internaltypes.VrfKeyHistoryStoreState{
		OperatorAddress:    append([]byte(nil), address...),
		EffectiveFromEpoch: state.EffectiveFromEpoch,
		VrfPubkey:          append([]byte(nil), state.VrfPubkey...),
		RetiredAtEpoch:     state.RetiredAtEpoch,
	}, nil
}

func (k Keeper) vrfKeyHistoryStorePublicProjection(stored internaltypes.VrfKeyHistoryStoreState) (types.VrfKeyHistoryState, error) {
	if len(stored.OperatorAddress) != accountAddressBytes {
		return types.VrfKeyHistoryState{}, fmt.Errorf("stored VRF history operator address must be %d bytes", accountAddressBytes)
	}
	address, err := k.addressCodec.BytesToString(stored.OperatorAddress)
	if err != nil {
		return types.VrfKeyHistoryState{}, fmt.Errorf("encode stored VRF history operator address: %w", err)
	}
	return types.VrfKeyHistoryState{
		OperatorAddress:    address,
		EffectiveFromEpoch: stored.EffectiveFromEpoch,
		VrfPubkey:          append([]byte(nil), stored.VrfPubkey...),
		RetiredAtEpoch:     stored.RetiredAtEpoch,
	}, nil
}

func (k Keeper) GetVrfKeyHistory(ctx context.Context, key types.VrfKeyHistoryKeyPair) (types.VrfKeyHistoryState, error) {
	stored, err := k.VrfKeyHistory.Get(ctx, key)
	if err != nil {
		return types.VrfKeyHistoryState{}, err
	}
	state, err := k.vrfKeyHistoryStorePublicProjection(stored)
	if err != nil {
		return types.VrfKeyHistoryState{}, err
	}
	if state.OperatorAddress != key.K1() || state.EffectiveFromEpoch != key.K2() {
		return types.VrfKeyHistoryState{}, fmt.Errorf("stored VRF history key/address mismatch")
	}
	return state, nil
}

func (k Keeper) StoreVrfKeyHistory(ctx context.Context, state types.VrfKeyHistoryState) error {
	stored, err := k.vrfKeyHistoryToStore(state)
	if err != nil {
		return err
	}
	return k.VrfKeyHistory.Set(ctx, types.NewVrfKeyHistoryKey(state.OperatorAddress, state.EffectiveFromEpoch), stored)
}

func (k Keeper) exportVrfKeyHistory(ctx context.Context) ([]types.VrfKeyHistoryState, error) {
	iter, err := k.VrfKeyHistory.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.VrfKeyHistoryState{}
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		stored, err := iter.Value()
		if err != nil {
			return nil, err
		}
		state, err := k.vrfKeyHistoryStorePublicProjection(stored)
		if err != nil {
			return nil, err
		}
		if state.OperatorAddress != key.K1() || state.EffectiveFromEpoch != key.K2() {
			return nil, fmt.Errorf("stored VRF history key/address mismatch")
		}
		states = append(states, state)
	}
	return states, nil
}
