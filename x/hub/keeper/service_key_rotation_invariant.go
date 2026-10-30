package keeper

import (
	"bytes"
	"context"
	"fmt"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// EnsureServiceKeyRotationInvariant checks both directions of the pending indexes.
// Previous-generation protection is checked when another rotation is registered.
func (k Keeper) EnsureServiceKeyRotationInvariant(ctx context.Context) error {
	pending, err := k.exportPendingServiceKeyRotations(ctx)
	if err != nil {
		return err
	}
	length, err := k.epochLengthBlocks(ctx)
	if err != nil {
		return err
	}
	type activationIdentity struct {
		height   uint64
		role     int32
		operator string
	}
	expectedActivations := map[activationIdentity]struct{}{}
	expectedAddresses := map[string]types.ParticipantKeyPair{}
	for _, row := range pending {
		identity, err := k.loadParticipantIdentity(ctx, row.ParticipantType, row.OperatorAddress)
		if err != nil {
			return err
		}
		if err := row.Validate(length, identity.AuthorizationNonce, identity.ServiceAddress); err != nil {
			return err
		}
		expectedActivations[activationIdentity{row.EffectiveHeight, int32(row.ParticipantType), row.OperatorAddress}] = struct{}{}
		address, err := k.pendingServiceAddress(row)
		if err != nil {
			return err
		}
		if _, exists := expectedAddresses[address]; exists {
			return fmt.Errorf("pending rotations share one service address")
		}
		expectedAddresses[address] = types.NewParticipantKey(row.ParticipantType, row.OperatorAddress)
		for _, role := range []shared.ParticipantType{shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX} {
			current, err := k.CurrentServiceAddressIndex.Has(ctx, types.NewCurrentServiceAddressIndexKey(role, address))
			if err != nil {
				return err
			}
			if current {
				return fmt.Errorf("pending service address conflicts with a current binding")
			}
		}
	}
	activationIter, err := k.ServiceKeyRotationActivationIndex.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; activationIter.Valid(); activationIter.Next() {
		key, err := activationIter.Key()
		if err != nil {
			activationIter.Close()
			return err
		}
		identity := activationIdentity{key.K1(), key.K2(), key.K3()}
		if _, exists := expectedActivations[identity]; !exists {
			activationIter.Close()
			return fmt.Errorf("service rotation activation has no matching primary")
		}
		delete(expectedActivations, identity)
	}
	if err := activationIter.Close(); err != nil {
		return err
	}
	if len(expectedActivations) != 0 {
		return fmt.Errorf("pending rotation activation index is missing")
	}
	addressIter, err := k.PendingServiceAddressIndex.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; addressIter.Valid(); addressIter.Next() {
		entry, err := addressIter.KeyValue()
		if err != nil {
			addressIter.Close()
			return err
		}
		owner, exists := expectedAddresses[entry.Key]
		if !exists {
			addressIter.Close()
			return fmt.Errorf("pending service address has no matching rotation")
		}
		raw, err := k.accountAddressToStore("pending service operator", owner.K2(), false)
		if err != nil {
			addressIter.Close()
			return err
		}
		if entry.Value.ParticipantType != owner.K1() || !bytes.Equal(raw, entry.Value.OperatorAddress) {
			addressIter.Close()
			return fmt.Errorf("pending service address reservation owner mismatch")
		}
		delete(expectedAddresses, entry.Key)
	}
	if err := addressIter.Close(); err != nil {
		return err
	}
	if len(expectedAddresses) != 0 {
		return fmt.Errorf("pending service address reservation is missing")
	}
	return nil
}
