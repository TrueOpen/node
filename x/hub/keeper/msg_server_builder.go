package keeper

import (
	"bytes"
	"context"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (m msgServer) RegisterBuilder(ctx context.Context, req *types.MsgRegisterBuilder) (*types.MsgRegisterBuilderResponse, error) {
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrInvalidBuilder, "nil request")
	}
	builderBytes, builder, err := m.k.requireCanonicalAddress("builder_operator_address", req.BuilderOperatorAddress)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidBuilder, err.Error())
	}
	stored, builderExists, err := m.k.loadBuilder(ctx, builder)
	if err != nil {
		return nil, err
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	height := sdkContextHeight(sdkCtx)
	participantType := shared.ParticipantType_PARTICIPANT_TYPE_BUILDER
	params, err := m.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	descriptorHash, err := types.CanonicalServiceDescriptorHash(participantType, builderBytes, 1, req.Descriptor_.Endpoints, params.Service)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidBuilder, err.Error())
	}
	registrationDigest, err := serviceRegistrationDigest(
		sdkCtx.ChainID(), participantType, builderBytes, req.ServicePubkey, initialServiceKeyNonce,
	)
	if err != nil {
		return nil, err
	}

	if builderExists {
		return m.registerBuilderReplay(ctx, stored, req, descriptorHash, registrationDigest)
	}
	descriptor := types.ServiceDescriptorState{
		ParticipantType: participantType, OperatorAddress: builder, DescriptorVersion: 1,
		EndpointCount: uint32(len(req.Descriptor_.Endpoints)), Endpoints: cloneServiceEndpoints(req.Descriptor_.Endpoints),
		DescriptorHash: append([]byte(nil), descriptorHash...), UpdatedHeight: height,
	}
	if err := descriptor.Validate(); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidBuilder, err.Error())
	}

	cacheCtx, commit := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	state, err := m.k.RegisterBuilder(cache, builder, height)
	if err != nil {
		return nil, err
	}
	// registerServiceKey resolves the participant primary before it writes the
	// current binding. Keep this provisional row inside the same cache so any
	// later proof, descriptor or invariant failure still rolls the whole
	// registration back atomically.
	if err := m.k.Builder.Set(cache, builder, state); err != nil {
		return nil, err
	}
	identity, err := m.k.registerServiceKey(cache, shared.ParticipantTypeBuilder, builder, req.ServicePubkey, req.ServiceKeyProof, initialServiceKeyNonce, height, registrationDigest)
	if err != nil {
		return nil, err
	}
	if err := m.k.ServiceDescriptor.Set(cache, types.NewParticipantKey(participantType, builder), descriptor); err != nil {
		return nil, err
	}
	state.CurrentServiceAddress = identity.ServiceAddress
	state.CurrentServicePubkey = append([]byte(nil), identity.ServicePubkey...)
	state.ServiceAuthorizationNonce = identity.AuthorizationNonce
	state.CurrentServiceKeyStatus = identity.ServiceKeyStatus
	state.CurrentDescriptorVersion = descriptor.DescriptorVersion
	if err := state.Validate(); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	if err := m.k.Builder.Set(cache, builder, state); err != nil {
		return nil, err
	}
	mustEmitHubEvent(cache, &types.EventBuilderRegistered{
		BuilderOperator: state.BuilderAddress, ServiceAddress: identity.ServiceAddress,
		ServiceAuthorizationNonce: identity.AuthorizationNonce, DescriptorVersion: descriptor.DescriptorVersion,
		DescriptorHash: append([]byte(nil), descriptor.DescriptorHash...),
	})
	commit()
	return &types.MsgRegisterBuilderResponse{
		BuilderAddress: state.BuilderAddress, BuilderStatus: types.BuilderStatus_BUILDER_STATUS_REVOKED,
		DescriptorVersion: descriptor.DescriptorVersion, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED,
	}, nil
}

func (m msgServer) registerBuilderReplay(
	ctx context.Context,
	state types.BuilderState,
	req *types.MsgRegisterBuilder,
	descriptorHash, registrationDigest []byte,
) (*types.MsgRegisterBuilderResponse, error) {
	if err := state.Validate(); err != nil || state.CurrentServiceKeyStatus != types.ServiceKeyStatusActive ||
		state.ServiceAuthorizationNonce != initialServiceKeyNonce || state.CurrentDescriptorVersion != 1 ||
		!bytes.Equal(state.CurrentServicePubkey, req.ServicePubkey) {
		return nil, errorsmod.Wrap(types.ErrInvalidBuilder, "builder already exists with different identity facts")
	}
	descriptor, err := m.k.ServiceDescriptor.Get(ctx, types.NewParticipantKey(shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, state.BuilderAddress))
	if err != nil || descriptor.DescriptorVersion != 1 || !bytes.Equal(descriptor.DescriptorHash, descriptorHash) {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "builder descriptor replay mismatch")
	}
	pubkey, err := parseServicePubKey("service_pubkey", req.ServicePubkey)
	if err != nil {
		return nil, err
	}
	if err := verifyServiceKeyProof(pubkey, registrationDigest, req.ServiceKeyProof); err != nil {
		return nil, err
	}
	return &types.MsgRegisterBuilderResponse{
		BuilderAddress: state.BuilderAddress, BuilderStatus: types.BuilderStatus_BUILDER_STATUS_REVOKED,
		DescriptorVersion: descriptor.DescriptorVersion, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP,
	}, nil
}

// RunBuilderTerm is intentionally predeclared but unavailable in Phase 0.
func (m msgServer) RunBuilderTerm(context.Context, *types.MsgRunBuilderTerm) (*types.MsgRunBuilderTermResponse, error) {
	return nil, status.Error(codes.FailedPrecondition, "FEATURE_DISABLED")
}
