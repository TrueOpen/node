package keeper

import (
	"context"
	"fmt"
	"math"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// These are var, not const, because a domain may only be resolved through the
// single registry in x/shared/types/domain_registry.go (§1.4 rule 1) and a
// const initializer cannot call a function.
var (
	serviceKeyRotationDomain = shared.MustDomain(shared.DomainServiceKeyRotationV1)
)

func serviceKeyRotationDigest(
	chainID string,
	participantType shared.ParticipantType,
	operatorBytes, newServicePubkey []byte,
	currentNonce, nextNonce uint64,
) ([]byte, error) {
	return shared.NewCanonicalHashBuilderV1(serviceKeyRotationDomain).Raw(
		[]byte(chainID), shared.EnumBE(uint32(participantType)), operatorBytes,
		newServicePubkey, shared.Uint64BE(currentNonce), shared.Uint64BE(nextNonce),
	).Sum()
}

func (m msgServer) RotateServiceKey(ctx context.Context, msg *types.MsgRotateServiceKey) (response *types.MsgRotateServiceKeyResponse, err error) {
	defer func() { err = classifyPublicServiceError(err, types.ErrInvalidServiceProvider) }()
	if msg == nil {
		return nil, fmt.Errorf("request is required")
	}
	if err := requireParticipantType(msg.ParticipantType); err != nil {
		return nil, err
	}
	operatorBytes, operatorAddress, err := m.k.requireCanonicalAddress("operator_address", msg.OperatorAddress)
	if err != nil {
		return nil, err
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	current, err := m.k.loadParticipantIdentity(cache, msg.ParticipantType, operatorAddress)
	if err != nil {
		return nil, fmt.Errorf("current participant identity not found: %w", err)
	}
	if msg.ExpectedCurrentServiceAuthorizationNonce != current.AuthorizationNonce {
		return nil, errorsmod.Wrap(types.ErrExpectedVersionMismatch, "expected current service authorization nonce does not match")
	}
	if current.AuthorizationNonce == math.MaxUint64 {
		return nil, fmt.Errorf("service authorization nonce overflow")
	}
	if err := m.k.ensureServiceKeyCanChange(cache, msg.ParticipantType, operatorAddress); err != nil {
		return nil, err
	}
	pub, err := parseServicePubKey("new_service_pubkey", msg.NewServicePubkey)
	if err != nil {
		return nil, err
	}
	serviceAddress, err := m.k.addressCodec.BytesToString(types.ServicePubKeyAddress(pub))
	if err != nil {
		return nil, err
	}
	if serviceAddress == current.ServiceAddress {
		return nil, fmt.Errorf("new service key must differ from the current key")
	}
	if err := m.k.requireServiceAddressAvailable(cache, serviceAddress); err != nil {
		return nil, err
	}
	nextNonce := current.AuthorizationNonce + 1
	// Ruling 17: operator_address enters a consensus preimage as address codec
	// bytes, never as its bech32 text (the node context document).
	digest, err := serviceKeyRotationDigest(
		sdkCtx.ChainID(), msg.ParticipantType, operatorBytes, msg.NewServicePubkey,
		current.AuthorizationNonce, nextNonce,
	)
	if err != nil {
		return nil, err
	}
	if err := verifyServiceKeyProof(pub, digest, msg.NewServiceKeyProof); err != nil {
		return nil, err
	}
	oldServiceAddress := current.ServiceAddress
	oldAuthorizationNonce := current.AuthorizationNonce
	current.ServiceAddress = serviceAddress
	current.ServicePubkey = append([]byte(nil), msg.NewServicePubkey...)
	current.AuthorizationNonce = nextNonce
	current.ServiceKeyStatus = types.ServiceKeyStatus_SERVICE_KEY_STATUS_ACTIVE
	current.UpdatedHeight = sdkContextHeight(cacheCtx)
	if err := m.k.persistParticipantIdentity(cache, current, oldServiceAddress, oldAuthorizationNonce); err != nil {
		return nil, err
	}
	mustEmitHubEvent(cache, &types.EventServiceKeyRotated{
		ParticipantType: msg.ParticipantType, Operator: operatorAddress,
		OldServiceAddress: oldServiceAddress, NewServiceAddress: serviceAddress, AuthorizationNonce: nextNonce,
	})
	commit()
	return &types.MsgRotateServiceKeyResponse{
		OperatorAddress: operatorAddress, NewServiceAddress: serviceAddress, NewNonce: nextNonce,
		Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED,
	}, nil
}

func (m msgServer) RevokeServiceKey(ctx context.Context, msg *types.MsgRevokeServiceKey) (response *types.MsgRevokeServiceKeyResponse, err error) {
	defer func() { err = classifyPublicServiceError(err, types.ErrInvalidServiceProvider) }()
	if msg == nil {
		return nil, fmt.Errorf("request is required")
	}
	if err := requireParticipantType(msg.ParticipantType); err != nil {
		return nil, err
	}
	switch msg.ReasonCode {
	case types.ServiceKeyRevocationReason_SERVICE_KEY_REVOCATION_REASON_COMPROMISED,
		types.ServiceKeyRevocationReason_SERVICE_KEY_REVOCATION_REASON_OPERATOR_SHUTDOWN,
		types.ServiceKeyRevocationReason_SERVICE_KEY_REVOCATION_REASON_GOVERNANCE_EMERGENCY:
	default:
		return nil, fmt.Errorf("reason_code is required")
	}
	_, operatorAddress, err := m.k.requireCanonicalAddress("operator_address", msg.OperatorAddress)
	if err != nil {
		return nil, err
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	current, err := m.k.loadParticipantIdentity(cache, msg.ParticipantType, operatorAddress)
	if err != nil {
		return nil, fmt.Errorf("current participant identity not found: %w", err)
	}
	if msg.ExpectedCurrentServiceAuthorizationNonce != current.AuthorizationNonce {
		return nil, errorsmod.Wrap(types.ErrExpectedVersionMismatch, "expected current service authorization nonce does not match")
	}
	revokedServiceAddress := current.ServiceAddress
	if current.ServiceKeyStatus == types.ServiceKeyStatus_SERVICE_KEY_STATUS_REVOKED {
		return &types.MsgRevokeServiceKeyResponse{
			OperatorAddress: operatorAddress, RevokedServiceAddress: revokedServiceAddress,
			AuthorizationNonce: current.AuthorizationNonce, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP,
		}, nil
	}
	if current.ServiceKeyStatus != types.ServiceKeyStatus_SERVICE_KEY_STATUS_ACTIVE {
		return nil, fmt.Errorf("current service key is not ACTIVE")
	}
	current.ServiceKeyStatus = types.ServiceKeyStatus_SERVICE_KEY_STATUS_REVOKED
	current.UpdatedHeight = sdkContextHeight(cacheCtx)
	if err := m.k.persistParticipantIdentity(cache, current, revokedServiceAddress, current.AuthorizationNonce); err != nil {
		return nil, err
	}
	if msg.ParticipantType == shared.ParticipantType_PARTICIPANT_TYPE_CORTEX {
		// Revoking the current service key makes the operator ineligible for
		// support: SupportVoteWeight treats a non-ACTIVE service key as
		// ineligible, and validateSupportGenesis requires an ineligible row to
		// carry zero active/eligible stake. Without deactivating here the two
		// sides disagree and any chain that ever revoked a key exports a genesis
		// it can no longer import -- the same defect that was fixed for jail.
		// The operator -> profiles fan-out is bounded by
		// max_supported_profiles_per_operator (the data-structure contract),
		// so doing it in
		// this transaction is safe.
		//
		// CONTRACT-GAP: §6.1 lists support_window expiry, bond below min_stake,
		// unbonding, jail/tombstone and profile freeze as the invalidation
		// paths, but omits service-key revocation even though the eligibility
		// predicate keys on ServiceKeyStatus. The reason argument is inert
		// (DeactivateModelSupport never reads it, and no state or event field
		// carries it), so the value below is documentary only.
		if err := m.k.deactivateDeclaredSupports(cache, operatorAddress, types.ModelSupportDeactivateTombstoned, current.UpdatedHeight); err != nil {
			return nil, err
		}
		if err := m.k.invalidateCandidatePoolsForOperator(cache, operatorAddress, current.UpdatedHeight); err != nil {
			return nil, err
		}
	}
	mustEmitHubEvent(cache, &types.EventServiceKeyRevoked{
		ParticipantType: msg.ParticipantType, Operator: operatorAddress,
		RevokedServiceAddress: revokedServiceAddress, AuthorizationNonce: current.AuthorizationNonce, Reason: msg.ReasonCode,
	})
	commit()
	return &types.MsgRevokeServiceKeyResponse{
		OperatorAddress: operatorAddress, RevokedServiceAddress: revokedServiceAddress,
		AuthorizationNonce: current.AuthorizationNonce, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED,
	}, nil
}

func (m msgServer) UpdateServiceDescriptor(ctx context.Context, msg *types.MsgUpdateServiceDescriptor) (*types.MsgUpdateServiceDescriptorResponse, error) {
	if msg == nil {
		return nil, fmt.Errorf("request is required")
	}
	if err := requireParticipantType(msg.ParticipantType); err != nil {
		return nil, err
	}
	operatorBytes, operatorAddress, err := m.k.requireCanonicalAddress("operator_address", msg.OperatorAddress)
	if err != nil {
		return nil, err
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	current, err := m.k.loadParticipantIdentity(cache, msg.ParticipantType, operatorAddress)
	if err != nil {
		return nil, fmt.Errorf("participant identity not found: %w", err)
	}
	if msg.ExpectedDescriptorVersion != current.CurrentDescriptorVersion {
		return nil, fmt.Errorf("expected descriptor version does not match")
	}
	if current.CurrentDescriptorVersion == math.MaxUint64 {
		return nil, fmt.Errorf("descriptor version overflow")
	}
	params, err := m.k.Params.Get(cache)
	if err != nil {
		return nil, err
	}
	nextVersion := current.CurrentDescriptorVersion + 1
	descriptorHash, err := types.CanonicalServiceDescriptorHash(msg.ParticipantType, operatorBytes, nextVersion, msg.Descriptor_.Endpoints, params.Service)
	if err != nil {
		return nil, err
	}
	endpoints := cloneServiceEndpoints(msg.Descriptor_.Endpoints)
	descriptor := types.ServiceDescriptorState{
		ParticipantType: msg.ParticipantType, OperatorAddress: operatorAddress,
		DescriptorVersion: nextVersion, EndpointCount: uint32(len(endpoints)), Endpoints: endpoints,
		DescriptorHash: append([]byte(nil), descriptorHash...), UpdatedHeight: sdkContextHeight(cacheCtx),
	}
	if err := m.k.ServiceDescriptor.Set(cache, types.NewParticipantKey(msg.ParticipantType, operatorAddress), descriptor); err != nil {
		return nil, err
	}
	current.CurrentDescriptorVersion = nextVersion
	if msg.ParticipantType == shared.ParticipantType_PARTICIPANT_TYPE_CORTEX {
		current.UpdatedHeight = descriptor.UpdatedHeight
	}
	if err := m.k.persistParticipantIdentity(cache, current, current.ServiceAddress, current.AuthorizationNonce); err != nil {
		return nil, err
	}
	mustEmitHubEvent(cache, &types.EventServiceDescriptorUpdated{
		ParticipantType: msg.ParticipantType, Operator: operatorAddress,
		DescriptorVersion: nextVersion, DescriptorHash: append([]byte(nil), descriptorHash...),
	})
	commit()
	return &types.MsgUpdateServiceDescriptorResponse{
		NewDescriptorVersion: nextVersion, DescriptorHash: descriptorHash,
		Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED,
	}, nil
}

func cloneServiceEndpoints(source []types.ServiceEndpointV1) []types.ServiceEndpointV1 {
	result := make([]types.ServiceEndpointV1, len(source))
	for i := range source {
		result[i] = source[i]
		if source[i].GetXTlsPubkeyHash() != nil {
			result[i].XTlsPubkeyHash = &types.ServiceEndpointV1_TlsPubkeyHash{
				TlsPubkeyHash: append([]byte(nil), source[i].GetTlsPubkeyHash()...),
			}
		}
	}
	return result
}
