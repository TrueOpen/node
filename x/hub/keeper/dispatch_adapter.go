package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// GetBuilderDispatchAuthority checks live admission without granting proof-only identities new work.
func (k Keeper) GetBuilderDispatchAuthority(ctx context.Context, operator string) (types.DispatchBuilderAuthority, error) {
	var authority types.DispatchBuilderAuthority
	admission, err := k.BuilderAdmission.Get(ctx, operator)
	if errors.Is(err, collections.ErrNotFound) {
		return authority, fmt.Errorf("%w: admission is missing", types.ErrDispatchBuilderUnavailable)
	}
	if err != nil {
		return authority, err
	}
	if types.BuilderStatus(admission.Status) != types.BuilderStatus_BUILDER_STATUS_ADMITTED {
		return authority, fmt.Errorf("%w: Builder is not admitted", types.ErrDispatchBuilderUnavailable)
	}
	binding, err := k.GetDispatchServiceProofKey(ctx, shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, operator)
	if err != nil {
		return authority, err
	}
	if binding.Status != types.ServiceKeyStatusActive {
		return authority, fmt.Errorf("%w: service key is revoked", types.ErrDispatchBuilderUnavailable)
	}
	account, err := k.GetBuilderCompensationAccount(ctx, operator)
	if err != nil {
		return authority, err
	}
	balance, err := shared.ParseAmount(account.Balance)
	if err != nil {
		return authority, err
	}
	authority = types.DispatchBuilderAuthority{Binding: binding, CompensationBalance: balance}
	return authority, nil
}

// GetDispatchServiceProofKey exposes current and one previous proof generation.
// Every caller separately checks signed nonce, scope and original deadlines.
func (k Keeper) GetDispatchServiceProofKey(ctx context.Context, role shared.ParticipantType, operator string) (types.CurrentServiceKeySnapshot, error) {
	if role != shared.ParticipantType_PARTICIPANT_TYPE_BUILDER && role != shared.ParticipantType_PARTICIPANT_TYPE_CORTEX {
		return types.CurrentServiceKeySnapshot{}, fmt.Errorf("dispatch proof role is invalid")
	}
	identity, err := k.loadParticipantIdentity(ctx, role, operator)
	if err != nil {
		return types.CurrentServiceKeySnapshot{}, err
	}
	if identity.AuthorizationNonce == 0 || (identity.ServiceKeyStatus != types.ServiceKeyStatusActive && identity.ServiceKeyStatus != types.ServiceKeyStatusRevoked) {
		return types.CurrentServiceKeySnapshot{}, fmt.Errorf("dispatch proof binding is unavailable")
	}
	name, err := participantTypeName(role)
	if err != nil {
		return types.CurrentServiceKeySnapshot{}, err
	}
	return serviceKeySnapshot(identity, name), nil
}

// GetBuilderVrfKeyForEpoch resolves the key that actually served the signed epoch.
// A delayed activation must never backdate its new key over the previous epoch.
func (k Keeper) GetBuilderVrfKeyForEpoch(ctx context.Context, operator string, epoch uint64) ([]byte, error) {
	role := shared.ParticipantType_PARTICIPANT_TYPE_BUILDER
	current, err := k.GetVrfKey(ctx, role, operator)
	if err != nil {
		return nil, err
	}
	if len(current.ActiveVrfPubkey) != 0 && current.ActiveFromEpoch <= epoch {
		return bytes.Clone(current.ActiveVrfPubkey), nil
	}
	iter, err := k.VrfKeyHistory.Iterate(ctx, collections.NewSuperPrefixedTripleRange[int32, string, uint64](int32(role), operator))
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		row, err := iter.Value()
		if err != nil {
			return nil, err
		}
		if row.EffectiveFromEpoch <= epoch && epoch < row.RetiredAtEpoch {
			return bytes.Clone(row.VrfPubkey), nil
		}
	}
	return nil, fmt.Errorf("Builder VRF key for the referenced epoch is unavailable")
}
