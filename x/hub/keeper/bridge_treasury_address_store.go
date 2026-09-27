package keeper

import (
	"bytes"
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (k Keeper) validatorBridgeSignerStateToStore(state types.ValidatorBridgeSignerState) (internaltypes.ValidatorBridgeSignerStoreState, error) {
	address, _, err := k.requireCanonicalAddress("bridge signer operator_address", state.OperatorAddress)
	if err != nil {
		return internaltypes.ValidatorBridgeSignerStoreState{}, err
	}
	return internaltypes.ValidatorBridgeSignerStoreState{
		OperatorAddress:          append([]byte(nil), address...),
		BridgeSignerAddressRaw20: append([]byte(nil), state.BridgeSignerAddressRaw20...),
		KeyVersion:               state.KeyVersion,
		PopSignature:             append([]byte(nil), state.PopSignature...),
		RegisteredHeight:         state.RegisteredHeight,
	}, nil
}

func (k Keeper) validatorBridgeSignerStorePublicProjection(stored internaltypes.ValidatorBridgeSignerStoreState) (types.ValidatorBridgeSignerState, error) {
	if len(stored.OperatorAddress) != accountAddressBytes {
		return types.ValidatorBridgeSignerState{}, fmt.Errorf("stored bridge signer operator address must be %d bytes", accountAddressBytes)
	}
	address, err := k.addressCodec.BytesToString(stored.OperatorAddress)
	if err != nil {
		return types.ValidatorBridgeSignerState{}, fmt.Errorf("encode stored bridge signer operator address: %w", err)
	}
	return types.ValidatorBridgeSignerState{
		OperatorAddress:          address,
		BridgeSignerAddressRaw20: append([]byte(nil), stored.BridgeSignerAddressRaw20...),
		KeyVersion:               stored.KeyVersion,
		PopSignature:             append([]byte(nil), stored.PopSignature...),
		RegisteredHeight:         stored.RegisteredHeight,
	}, nil
}

func (k Keeper) getValidatorBridgeSigner(ctx context.Context, operator string) (types.ValidatorBridgeSignerState, error) {
	address, canonical, err := k.requireCanonicalAddress("bridge signer operator_address", operator)
	if err != nil {
		return types.ValidatorBridgeSignerState{}, err
	}
	stored, err := k.ValidatorBridgeSigner.Get(ctx, canonical)
	if err != nil {
		return types.ValidatorBridgeSignerState{}, err
	}
	if !bytes.Equal(stored.OperatorAddress, address) {
		return types.ValidatorBridgeSignerState{}, fmt.Errorf("stored bridge signer key/address mismatch")
	}
	return k.validatorBridgeSignerStorePublicProjection(stored)
}

// GetValidatorBridgeSigner projects the private row for governance integration.
func (k Keeper) GetValidatorBridgeSigner(ctx context.Context, operator string) (types.ValidatorBridgeSignerState, error) {
	return k.getValidatorBridgeSigner(ctx, operator)
}

func (k Keeper) storeValidatorBridgeSigner(ctx context.Context, state types.ValidatorBridgeSignerState) error {
	stored, err := k.validatorBridgeSignerStateToStore(state)
	if err != nil {
		return err
	}
	return k.ValidatorBridgeSigner.Set(ctx, state.OperatorAddress, stored)
}

// StoreValidatorBridgeSigner preserves the public governance boundary.
func (k Keeper) StoreValidatorBridgeSigner(ctx context.Context, state types.ValidatorBridgeSignerState) error {
	return k.storeValidatorBridgeSigner(ctx, state)
}

func (k Keeper) exportValidatorBridgeSigners(ctx context.Context) ([]types.ValidatorBridgeSignerState, error) {
	iter, err := k.ValidatorBridgeSigner.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.ValidatorBridgeSignerState{}
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		stored, err := iter.Value()
		if err != nil {
			return nil, err
		}
		state, err := k.validatorBridgeSignerStorePublicProjection(stored)
		if err != nil {
			return nil, err
		}
		if key != state.OperatorAddress {
			return nil, fmt.Errorf("stored bridge signer key/address mismatch")
		}
		states = append(states, state)
	}
	return states, nil
}

func (k Keeper) treasuryRecipientStateToStore(state types.TreasurySpendRecipientEpochState) (internaltypes.TreasurySpendRecipientEpochStoreState, error) {
	address, _, err := k.requireCanonicalAddress("treasury recipient accumulator", state.RecipientAddress)
	if err != nil {
		return internaltypes.TreasurySpendRecipientEpochStoreState{}, err
	}
	if _, err := shared.ParseAmount(state.SpentAmount); err != nil {
		return internaltypes.TreasurySpendRecipientEpochStoreState{}, err
	}
	return internaltypes.TreasurySpendRecipientEpochStoreState{
		RewardEpoch:            state.RewardEpoch,
		RecipientAddress:       append([]byte(nil), address...),
		SpentAmountAtomicUnits: state.SpentAmount.AtomicUnits,
	}, nil
}

func (k Keeper) treasuryRecipientStorePublicProjection(stored internaltypes.TreasurySpendRecipientEpochStoreState) (types.TreasurySpendRecipientEpochState, error) {
	if len(stored.RecipientAddress) != accountAddressBytes {
		return types.TreasurySpendRecipientEpochState{}, fmt.Errorf("stored treasury recipient address must be %d bytes", accountAddressBytes)
	}
	address, err := k.addressCodec.BytesToString(stored.RecipientAddress)
	if err != nil {
		return types.TreasurySpendRecipientEpochState{}, fmt.Errorf("encode stored treasury recipient address: %w", err)
	}
	amount := shared.Amount{AtomicUnits: stored.SpentAmountAtomicUnits}
	if _, err := shared.ParseAmount(amount); err != nil {
		return types.TreasurySpendRecipientEpochState{}, fmt.Errorf("stored treasury recipient amount: %w", err)
	}
	return types.TreasurySpendRecipientEpochState{RewardEpoch: stored.RewardEpoch, RecipientAddress: address, SpentAmount: amount}, nil
}

func (k Keeper) getTreasuryRecipient(ctx context.Context, key types.TreasurySpendRecipientEpochKeyPair) (types.TreasurySpendRecipientEpochState, error) {
	stored, err := k.TreasurySpendRecipientEpoch.Get(ctx, key)
	if err != nil {
		return types.TreasurySpendRecipientEpochState{}, err
	}
	if stored.RewardEpoch != key.K1() || !bytes.Equal(stored.RecipientAddress, key.K2()) {
		return types.TreasurySpendRecipientEpochState{}, fmt.Errorf("stored treasury recipient key/address mismatch")
	}
	return k.treasuryRecipientStorePublicProjection(stored)
}

func (k Keeper) storeTreasuryRecipient(ctx context.Context, key types.TreasurySpendRecipientEpochKeyPair, state types.TreasurySpendRecipientEpochState) error {
	stored, err := k.treasuryRecipientStateToStore(state)
	if err != nil {
		return err
	}
	if stored.RewardEpoch != key.K1() || !bytes.Equal(stored.RecipientAddress, key.K2()) {
		return fmt.Errorf("stored treasury recipient key/address mismatch")
	}
	return k.TreasurySpendRecipientEpoch.Set(ctx, key, stored)
}

func (k Keeper) exportTreasuryRecipientEpochs(ctx context.Context) ([]types.TreasurySpendRecipientEpochState, error) {
	iter, err := k.TreasurySpendRecipientEpoch.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.TreasurySpendRecipientEpochState{}
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		stored, err := iter.Value()
		if err != nil {
			return nil, err
		}
		if stored.RewardEpoch != key.K1() || !bytes.Equal(stored.RecipientAddress, key.K2()) {
			return nil, fmt.Errorf("stored treasury recipient key/address mismatch")
		}
		state, err := k.treasuryRecipientStorePublicProjection(stored)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}
