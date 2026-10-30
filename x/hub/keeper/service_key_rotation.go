package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"github.com/TrueOpen/wire/dispatch"
	sdk "github.com/cosmos/cosmos-sdk/types"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (k Keeper) serviceRotationHeight(ctx context.Context, registeredHeight uint64) (uint64, error) {
	length, err := k.epochLengthBlocks(ctx)
	if err != nil {
		return 0, err
	}
	start, _, err := epochHeightRange(epochForHeight(registeredHeight, length), length)
	if err != nil {
		return 0, err
	}
	return dispatch.ServiceKeyRotationHeight(registeredHeight, start, length)
}

func (k Keeper) exportPendingServiceKeyRotations(ctx context.Context) ([]types.PendingServiceKeyRotationState, error) {
	iter, err := k.PendingServiceKeyRotation.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	rows := []types.PendingServiceKeyRotationState{}
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		state, err := k.ReadPendingServiceKeyRotation(ctx, shared.ParticipantType(key.K1()), key.K2())
		if err != nil {
			return nil, err
		}
		rows = append(rows, state)
	}
	return rows, nil
}

// ReadBusObjectiveEvidenceResponsibility returns the frozen nonce for cleanup;
// it grants no permission to sign and never derives it from the current key.
func (k Keeper) ReadBusObjectiveEvidenceResponsibility(ctx context.Context, operator string, sessionID, taskID []byte) (shared.BusObjectiveEvidenceResponsibilityReceiptV1, error) {
	// Nonce is excluded from the registered ID. A positive placeholder permits
	// the same strict identity constructor to derive the key without duplication.
	prepared, err := k.prepareBusObjectiveEvidenceResponsibility(shared.BusObjectiveEvidenceResponsibilityV1{SchemaVersion: busObjectiveEvidenceResponsibilitySchemaVersion, BuilderOperator: operator, SessionId: sessionID, TaskId: taskID, ServiceAuthorizationNonce: 1})
	if err != nil {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, err
	}
	state, err := k.ReadServiceKeyResponsibilityValue(ctx, types.NewServiceKeyResponsibilityKey(shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, operator, prepared.id))
	if err != nil {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, err
	}
	prepared.nonce = state.ServiceAuthorizationNonce
	if prepared.nonce == 0 || !busObjectiveEvidenceResponsibilityMatches(state, prepared) {
		return shared.BusObjectiveEvidenceResponsibilityReceiptV1{}, fmt.Errorf("Builder evidence responsibility does not match its identity")
	}
	return prepared.receipt(true, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP), nil
}

// ReadPendingServiceKeyRotation projects raw account storage at the boundary.
func (k Keeper) ReadPendingServiceKeyRotation(ctx context.Context, role shared.ParticipantType, operator string) (types.PendingServiceKeyRotationState, error) {
	stored, err := k.PendingServiceKeyRotation.Get(ctx, types.NewParticipantKey(role, operator))
	if err != nil {
		return types.PendingServiceKeyRotationState{}, err
	}
	address, err := k.accountAddressFromStore("pending rotation operator", stored.OperatorAddress, false)
	if err != nil {
		return types.PendingServiceKeyRotationState{}, err
	}
	if stored.ParticipantType != int32(role) || address != operator {
		return types.PendingServiceKeyRotationState{}, fmt.Errorf("pending rotation key does not match its identity")
	}
	return types.PendingServiceKeyRotationState{
		ParticipantType: role, OperatorAddress: address,
		CurrentServiceAuthorizationNonce: stored.CurrentServiceAuthorizationNonce,
		NewServicePubkey:                 bytes.Clone(stored.NewServicePubkey), NextServiceAuthorizationNonce: stored.NextServiceAuthorizationNonce,
		RegisteredHeight: stored.RegisteredHeight, EffectiveHeight: stored.EffectiveHeight,
	}, nil
}

func (k Keeper) writePendingServiceKeyRotation(ctx context.Context, pending types.PendingServiceKeyRotationState) error {
	operator, err := k.accountAddressToStore("pending rotation operator", pending.OperatorAddress, false)
	if err != nil {
		return err
	}
	return k.PendingServiceKeyRotation.Set(ctx, types.NewParticipantKey(pending.ParticipantType, pending.OperatorAddress), internaltypes.PendingServiceKeyRotationStoreState{
		ParticipantType: int32(pending.ParticipantType), OperatorAddress: operator,
		CurrentServiceAuthorizationNonce: pending.CurrentServiceAuthorizationNonce, NewServicePubkey: bytes.Clone(pending.NewServicePubkey),
		NextServiceAuthorizationNonce: pending.NextServiceAuthorizationNonce, RegisteredHeight: pending.RegisteredHeight,
		EffectiveHeight: pending.EffectiveHeight,
	})
}

func (k Keeper) pendingServiceAddress(pending types.PendingServiceKeyRotationState) (string, error) {
	pub, err := parseServicePubKey("pending service pubkey", pending.NewServicePubkey)
	if err != nil {
		return "", err
	}
	return k.addressCodec.BytesToString(types.ServicePubKeyAddress(pub))
}

func rotationActivationKey(pending types.PendingServiceKeyRotationState) types.ServiceKeyRotationActivationKey {
	return collections.Join3(pending.EffectiveHeight, int32(pending.ParticipantType), pending.OperatorAddress)
}

func (k Keeper) registerPendingServiceKeyRotation(ctx context.Context, pending types.PendingServiceKeyRotationState) error {
	if err := requireParticipantType(pending.ParticipantType); err != nil {
		return err
	}
	identity, err := k.loadParticipantIdentity(ctx, pending.ParticipantType, pending.OperatorAddress)
	if err != nil {
		return err
	}
	length, err := k.epochLengthBlocks(ctx)
	if err != nil {
		return err
	}
	if err := pending.Validate(length, identity.AuthorizationNonce, identity.ServiceAddress); err != nil {
		return err
	}
	if pending.ParticipantType == shared.ParticipantType_PARTICIPANT_TYPE_CORTEX {
		bond, err := k.GetServiceBondState(ctx, pending.OperatorAddress)
		if err != nil {
			return err
		}
		if bond.Status == types.ServiceBondStatusExited || bond.Status == types.ServiceBondStatusTombstoned {
			return fmt.Errorf("terminal participant cannot rotate its service key")
		}
	}
	if err := k.requirePreviousServiceKeyReleased(ctx, identity, pending.RegisteredHeight); err != nil {
		return err
	}
	has, err := k.PendingServiceKeyRotation.Has(ctx, types.NewParticipantKey(pending.ParticipantType, pending.OperatorAddress))
	if err != nil {
		return err
	}
	if has {
		return fmt.Errorf("service key rotation is already pending")
	}
	service, err := k.pendingServiceAddress(pending)
	if err != nil {
		return err
	}
	if service == identity.ServiceAddress {
		return fmt.Errorf("pending service key must differ from current")
	}
	if err := k.requireServiceAddressAvailable(ctx, service); err != nil {
		return err
	}
	operator, err := k.accountAddressToStore("pending service operator", pending.OperatorAddress, false)
	if err != nil {
		return err
	}
	if err := k.writePendingServiceKeyRotation(ctx, pending); err != nil {
		return err
	}
	if err := k.PendingServiceAddressIndex.Set(ctx, service, internaltypes.PendingServiceAddressStoreState{ParticipantType: int32(pending.ParticipantType), OperatorAddress: operator}); err != nil {
		return err
	}
	return k.ServiceKeyRotationActivationIndex.Set(ctx, rotationActivationKey(pending))
}

func (k Keeper) cancelPendingServiceKeyRotation(ctx context.Context, role shared.ParticipantType, operator string) error {
	pending, err := k.ReadPendingServiceKeyRotation(ctx, role, operator)
	if errors.Is(err, collections.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	service, err := k.pendingServiceAddress(pending)
	if err != nil {
		return err
	}
	reservation, err := k.PendingServiceAddressIndex.Get(ctx, service)
	if err != nil {
		return err
	}
	raw, err := k.accountAddressToStore("pending service operator", operator, false)
	if err != nil {
		return err
	}
	if reservation.ParticipantType != int32(role) || !bytes.Equal(reservation.OperatorAddress, raw) {
		return fmt.Errorf("pending service address reservation mismatch")
	}
	if err := k.PendingServiceAddressIndex.Remove(ctx, service); err != nil {
		return err
	}
	if err := k.ServiceKeyRotationActivationIndex.Remove(ctx, rotationActivationKey(pending)); err != nil {
		return err
	}
	return k.PendingServiceKeyRotation.Remove(ctx, types.NewParticipantKey(role, operator))
}

// requirePreviousServiceKeyReleased checks the retained generation using the
// participant's existing responsibility primary prefix, never a second counter.
func (k Keeper) requirePreviousServiceKeyReleased(ctx context.Context, identity participantIdentityState, height uint64) error {
	var evidenceCount uint64
	if identity.PreviousAuthorizationNonce != 0 {
		iter, err := k.ServiceKeyResponsibility.Iterate(ctx, collections.NewSuperPrefixedTripleRange[int32, string, shared.Hash32Key](int32(identity.ParticipantType), identity.OperatorAddress))
		if err != nil {
			return err
		}
		for ; iter.Valid(); iter.Next() {
			row, err := iter.Value()
			if err != nil {
				iter.Close()
				return err
			}
			if row.ServiceAuthorizationNonce == identity.PreviousAuthorizationNonce && types.IsServiceKeyEvidenceResponsibility(types.ServiceKeyResponsibilityKind(row.ResponsibilityKind)) {
				evidenceCount = 1
				break
			}
		}
		if err := iter.Close(); err != nil {
			return err
		}
	}
	allowed, err := dispatch.CanRegisterServiceKeyRotation(height, identity.PreviousRetiredHeight, evidenceCount, shared.DispatchRotationWindowHardLimits())
	if err != nil {
		return err
	}
	if !allowed {
		return fmt.Errorf("previous service key generation is still protected")
	}
	return nil
}

// ProcessServiceKeyRotations installs every due key before the activation
// height's business transactions. This correctness transition is not prunable
// work and cannot be postponed by maintenance budgets or current duties.
func (k Keeper) ProcessServiceKeyRotations(ctx context.Context, nextHeight uint64) error {
	length, err := k.epochLengthBlocks(ctx)
	if err != nil {
		return err
	}
	start, _, err := epochHeightRange(epochForHeight(nextHeight, length), length)
	if err != nil || start != nextHeight {
		return err
	}
	iter, err := k.ServiceKeyRotationActivationIndex.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	keys := make([]types.ServiceKeyRotationActivationKey, 0)
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			iter.Close()
			return err
		}
		if key.K1() > nextHeight {
			break
		}
		keys = append(keys, key)
	}
	if err := iter.Close(); err != nil {
		return err
	}
	cacheCtx, commit := sdk.UnwrapSDKContext(ctx).CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	for _, key := range keys {
		pending, err := k.ReadPendingServiceKeyRotation(cache, shared.ParticipantType(key.K2()), key.K3())
		if err != nil {
			return err
		}
		if pending.EffectiveHeight != key.K1() || key.K1() != nextHeight {
			return fmt.Errorf("service key activation index does not match its fixed height")
		}
		identity, err := k.loadParticipantIdentity(cache, pending.ParticipantType, pending.OperatorAddress)
		if err != nil {
			return err
		}
		if err := pending.Validate(length, identity.AuthorizationNonce, identity.ServiceAddress); err != nil {
			return err
		}
		oldAddress, oldNonce := identity.ServiceAddress, identity.AuthorizationNonce
		identity.PreviousServicePubkey = bytes.Clone(identity.ServicePubkey)
		identity.PreviousAuthorizationNonce = oldNonce
		identity.PreviousRetiredHeight = nextHeight
		identity.ServiceAddress, err = k.pendingServiceAddress(pending)
		if err != nil {
			return err
		}
		identity.ServicePubkey = bytes.Clone(pending.NewServicePubkey)
		identity.AuthorizationNonce = pending.NextServiceAuthorizationNonce
		identity.ServiceKeyStatus = types.ServiceKeyStatusActive
		identity.UpdatedHeight = nextHeight
		if err := k.cancelPendingServiceKeyRotation(cache, pending.ParticipantType, pending.OperatorAddress); err != nil {
			return err
		}
		if err := k.persistParticipantIdentity(cache, identity, oldAddress, oldNonce); err != nil {
			return err
		}
		mustEmitHubEvent(cache, &types.EventServiceKeyRotated{ParticipantType: identity.ParticipantType, Operator: identity.OperatorAddress, OldServiceAddress: oldAddress, NewServiceAddress: identity.ServiceAddress, AuthorizationNonce: identity.AuthorizationNonce})
	}
	commit()
	return nil
}
