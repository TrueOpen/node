package keeper_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	proto "github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// serviceRegistrationProofBytes rebuilds the TRUEOPEN_SERVICE_REGISTRATION_V1
// proof-of-possession preimage step 1. The initial
// authorization nonce is Keeper-derived and fixed to 1, and service_pubkey is the
// raw 33-byte compressed key rather than its hex text. operator_address is framed
// as address-codec bytes, never Bech32 presentation text (Ruling 17).
//
// participant_type sits right after chain_id, matching the two sibling domains
// TRUEOPEN_SERVICE_KEY_ROTATION_V1 and TRUEOPEN_UNBONDING_ID_V1. It is what makes the
// Cortex and the Builder registration proof two distinct digests, so the role
// replay negative test below is expressible at all (§1.3 rule 7).
func serviceRegistrationProofBytes(
	chainID string, participantType shared.ParticipantType, operatorAddress string, servicePubkey []byte,
) []byte {
	operatorBytes := sdk.MustAccAddressFromBech32(operatorAddress)
	return shared.CanonicalHashBytes(
		shared.MustDomain(shared.DomainServiceRegistrationV1),
		[]byte(chainID), shared.EnumBE(uint32(participantType)), operatorBytes, servicePubkey, shared.Uint64BE(1),
	)
}

// serviceKeyRotationProofBytes rebuilds the TRUEOPEN_SERVICE_KEY_ROTATION_V1
// preimage: only the new key signs, and it binds
// both the expected current nonce and the Keeper-derived next nonce.
func serviceKeyRotationProofBytes(
	chainID string, participantType shared.ParticipantType, operatorBytes, newServicePubkey []byte,
	currentNonce, nextNonce uint64,
) []byte {
	return shared.CanonicalHashBytes(
		shared.MustDomain(shared.DomainServiceKeyRotationV1),
		[]byte(chainID), shared.EnumBE(uint32(participantType)), operatorBytes, newServicePubkey,
		shared.Uint64BE(currentNonce), shared.Uint64BE(nextNonce),
	)
}

func registerServiceAction(servicePubkey, serviceKeyProof []byte, amount uint64) types.ServiceStakeActionV1 {
	return types.ServiceStakeActionV1{Action: &types.ServiceStakeActionV1_Register{
		Register: &types.RegisterServiceV1{
			ServicePubkey: servicePubkey, ServiceKeyProof: serviceKeyProof,
			Amount: shared.NewAmount(amount),
		},
	}}
}

// topUpServiceAction carries an amount and nothing else: the API contract
// step 1 removed every service key, proof and nonce field from TopUpServiceV1.
func topUpServiceAction(amount uint64) types.ServiceStakeActionV1 {
	return types.ServiceStakeActionV1{Action: &types.ServiceStakeActionV1_TopUp{
		TopUp: &types.TopUpServiceV1{Amount: shared.NewAmount(amount)},
	}}
}

// TestRotationDoesNotDeleteAnotherOperatorsServiceAddressIndexRow walks the
// lifecycle that a revoked-but-not-cleared service address makes reachable.
// A revoke deletes the index row but leaves current_service_address on the primary,
// so the address becomes claimable again while the old owner still remembers it as
// current. Any later descriptor update or rotation by that old owner must not delete
// the new owner's live row. Cleanup must be binding-owned, not address-keyed.
func TestRotationDoesNotDeleteAnotherOperatorsServiceAddressIndexRow(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	victimOfCleanup := hubIdentity(t, 141)
	firstOwner := hubIdentity(t, 142)
	contestedKey := hubIdentity(t, 143)
	replacementKey := hubIdentity(t, 144)
	const chainID = "trueopen-index-ownership-test"
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithChainID(chainID).WithBlockHeight(100))
	server := keeper.NewMsgServerImpl(f.keeper)
	contestedIndexKey := types.NewCurrentServiceAddressIndexKey(
		shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, contestedKey.Address,
	)

	// (1) firstOwner registers the contested service key.
	f.bank.seedAccount(firstOwner.Address, testServiceBondMinInitial)
	registerBytes := serviceRegistrationProofBytes(chainID, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, firstOwner.Address, contestedKey.PubKey)
	_, err := server.StakeService(f.ctx, &types.MsgStakeService{
		OperatorAddress: firstOwner.Address,
		Action: registerServiceAction(
			contestedKey.PubKey, hubSign(t, contestedKey, registerBytes), testServiceBondMinInitial,
		),
	})
	require.NoError(t, err)

	// (2) firstOwner revokes. The index row disappears, but its primary still names
	// the contested address as its current one.
	revokedEventsBefore := len(hubEventsOfType(t, sdk.UnwrapSDKContext(f.ctx), &types.EventServiceKeyRevoked{}))
	_, err = server.RevokeServiceKey(f.ctx, &types.MsgRevokeServiceKey{
		OperatorAddress: firstOwner.Address, ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_CORTEX,
		ExpectedCurrentServiceAuthorizationNonce: 1,
		ReasonCode:                               types.ServiceKeyRevocationReason_SERVICE_KEY_REVOCATION_REASON_COMPROMISED,
	})
	require.NoError(t, err)
	revokedEvents := hubEventsOfType(t, sdk.UnwrapSDKContext(f.ctx), &types.EventServiceKeyRevoked{})
	require.Len(t, revokedEvents, revokedEventsBefore+1)
	revokedEvent := revokedEvents[len(revokedEvents)-1].(*types.EventServiceKeyRevoked)
	require.Equal(t, firstOwner.Address, revokedEvent.Operator)
	require.Equal(t, contestedKey.Address, revokedEvent.RevokedServiceAddress)
	require.Equal(t, uint64(1), revokedEvent.AuthorizationNonce)
	hasIndex, err := f.keeper.CurrentServiceAddressIndex.Has(f.ctx, contestedIndexKey)
	require.NoError(t, err)
	require.False(t, hasIndex, "revoke must clear the index row; that is what frees the address")
	revoked, err := f.keeper.GetCortexNodeState(f.ctx, firstOwner.Address)
	require.NoError(t, err)
	require.Equal(t, contestedKey.Address, revoked.CurrentServiceAddress, "revoke deliberately keeps the address on the primary (A-20)")

	// (3) A second operator legitimately takes the now-free address.
	f.bank.seedAccount(victimOfCleanup.Address, testServiceBondMinInitial)
	secondRegisterBytes := serviceRegistrationProofBytes(chainID, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, victimOfCleanup.Address, contestedKey.PubKey)
	_, err = server.StakeService(f.ctx, &types.MsgStakeService{
		OperatorAddress: victimOfCleanup.Address,
		Action: registerServiceAction(
			contestedKey.PubKey, hubSign(t, contestedKey, secondRegisterBytes), testServiceBondMinInitial,
		),
	})
	require.NoError(t, err)
	index, err := f.keeper.GetCurrentServiceAddressIndex(f.ctx, contestedIndexKey)
	require.NoError(t, err)
	require.Equal(t, victimOfCleanup.Address, index.OperatorAddress)

	// (4) firstOwner updates its descriptor while still revoked. The primary still
	// names the contested address, but its derived index is now owned by
	// victimOfCleanup and must survive both a rejected and an accepted update.
	descriptor := types.ServiceDescriptorV1{Endpoints: []types.ServiceEndpointV1{{
		EndpointKind:    types.ServiceEndpointKind_SERVICE_ENDPOINT_KIND_HEALTH_HTTPS,
		Uri:             "https://revoked-owner.invalid/health",
		ProtocolVersion: "v1",
	}}}
	descriptorEventsBefore := len(hubEventsOfType(t, sdk.UnwrapSDKContext(f.ctx), &types.EventServiceDescriptorUpdated{}))
	_, err = server.UpdateServiceDescriptor(f.ctx, &types.MsgUpdateServiceDescriptor{
		ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_CORTEX,
		OperatorAddress: firstOwner.Address, ExpectedDescriptorVersion: 1, Descriptor_: descriptor,
	})
	require.ErrorContains(t, err, "expected descriptor version")
	require.Len(t, hubEventsOfType(t, sdk.UnwrapSDKContext(f.ctx), &types.EventServiceDescriptorUpdated{}), descriptorEventsBefore)

	_, err = server.UpdateServiceDescriptor(f.ctx, &types.MsgUpdateServiceDescriptor{
		ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_CORTEX,
		OperatorAddress: firstOwner.Address, ExpectedDescriptorVersion: 0, Descriptor_: descriptor,
	})
	require.NoError(t, err)
	descriptorEvents := hubEventsOfType(t, sdk.UnwrapSDKContext(f.ctx), &types.EventServiceDescriptorUpdated{})
	require.Len(t, descriptorEvents, descriptorEventsBefore+1)
	descriptorEvent := descriptorEvents[len(descriptorEvents)-1].(*types.EventServiceDescriptorUpdated)
	require.Equal(t, firstOwner.Address, descriptorEvent.Operator)
	require.Equal(t, uint64(1), descriptorEvent.DescriptorVersion)

	survivor, err := f.keeper.GetCurrentServiceAddressIndex(f.ctx, contestedIndexKey)
	require.NoError(t, err)
	require.Equal(t, victimOfCleanup.Address, survivor.OperatorAddress, "descriptor update deleted a row it did not own")
	require.Equal(t, uint64(1), survivor.ServiceAuthorizationNonce)
	require.NoError(t, f.keeper.EnsureCurrentServiceAddressIndexInvariant(f.ctx))

	// (5) firstOwner rotates away. Its "old" address is the contested one, which now
	// belongs to somebody else, so the rotation must leave that row alone.
	firstOwnerBytes := sdk.MustAccAddressFromBech32(firstOwner.Address).Bytes()
	rotateBytes := serviceKeyRotationProofBytes(
		chainID, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, firstOwnerBytes, replacementKey.PubKey, 1, 2,
	)
	rotatedEventsBefore := len(hubEventsOfType(t, sdk.UnwrapSDKContext(f.ctx), &types.EventServiceKeyRotated{}))
	_, err = server.RotateServiceKey(f.ctx, &types.MsgRotateServiceKey{
		OperatorAddress: firstOwner.Address, ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_CORTEX,
		NewServicePubkey: replacementKey.PubKey, ExpectedCurrentServiceAuthorizationNonce: 1,
		NewServiceKeyProof: hubSign(t, replacementKey, rotateBytes),
	})
	require.NoError(t, err)
	rotatedEvents := hubEventsOfType(t, sdk.UnwrapSDKContext(f.ctx), &types.EventServiceKeyRotated{})
	require.Len(t, rotatedEvents, rotatedEventsBefore+1)
	rotatedEvent := rotatedEvents[len(rotatedEvents)-1].(*types.EventServiceKeyRotated)
	require.Equal(t, firstOwner.Address, rotatedEvent.Operator)
	require.Equal(t, contestedKey.Address, rotatedEvent.OldServiceAddress)
	require.Equal(t, replacementKey.Address, rotatedEvent.NewServiceAddress)
	require.Equal(t, uint64(2), rotatedEvent.AuthorizationNonce)

	survivor, err = f.keeper.GetCurrentServiceAddressIndex(f.ctx, contestedIndexKey)
	require.NoError(t, err)
	require.Equal(t, victimOfCleanup.Address, survivor.OperatorAddress, "the rotation deleted a row it did not own")
	require.Equal(t, uint64(1), survivor.ServiceAuthorizationNonce)
	rotatedIndex, err := f.keeper.GetCurrentServiceAddressIndex(f.ctx, types.NewCurrentServiceAddressIndexKey(
		shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, replacementKey.Address,
	))
	require.NoError(t, err)
	require.Equal(t, firstOwner.Address, rotatedIndex.OperatorAddress)
	require.Equal(t, uint64(2), rotatedIndex.ServiceAuthorizationNonce)

	// The invariant is the actual halt condition, so assert it directly.
	require.NoError(t, f.keeper.EnsureCurrentServiceAddressIndexInvariant(f.ctx))
}

func TestBuilderRotationRequiresAllIdentityCountersToBeZero(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*types.BuilderState)
	}{
		{"active task liability", func(state *types.BuilderState) { state.ActiveTaskLiabilityCount = 1 }},
		{"pending stage duty", func(state *types.BuilderState) { state.PendingStageDutyCount = 1 }},
		{"pending evidence submission", func(state *types.BuilderState) { state.PendingEvidenceSubmissionCount = 1 }},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			f := initFixture(t)
			require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
			chainID := "trueopen-builder-rotation-counter-test"
			f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithChainID(chainID).WithBlockHeight(100))
			operator := hubIdentity(t, byte(180+index*2))
			newKey := hubIdentity(t, byte(181+index*2))
			builder := registerBuilderIdentityForTest(t, f, operator, 10)
			test.mutate(&builder)
			require.NoError(t, f.keeper.StoreBuilder(f.ctx, operator.Address, builder))

			operatorBytes := sdk.MustAccAddressFromBech32(operator.Address).Bytes()
			proofBytes := serviceKeyRotationProofBytes(
				chainID, shared.ParticipantType_PARTICIPANT_TYPE_BUILDER,
				operatorBytes, newKey.PubKey, 1, 2,
			)
			request := &types.MsgRotateServiceKey{
				ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_BUILDER,
				OperatorAddress: operator.Address, NewServicePubkey: newKey.PubKey,
				ExpectedCurrentServiceAuthorizationNonce: 1,
				NewServiceKeyProof:                       hubSign(t, newKey, proofBytes),
			}
			eventsBefore := len(hubEventsOfType(t, sdk.UnwrapSDKContext(f.ctx), &types.EventServiceKeyRotated{}))
			_, err := keeper.NewMsgServerImpl(f.keeper).RotateServiceKey(f.ctx, request)
			require.ErrorIs(t, err, types.ErrPendingResponsibility)
			require.Len(t, hubEventsOfType(t, sdk.UnwrapSDKContext(f.ctx), &types.EventServiceKeyRotated{}), eventsBefore)

			stored, err := f.keeper.GetBuilderState(f.ctx, operator.Address)
			require.NoError(t, err)
			require.Equal(t, operator.Address, stored.CurrentServiceAddress)
			require.Equal(t, uint64(1), stored.ServiceAuthorizationNonce)
			hasNewIndex, err := f.keeper.CurrentServiceAddressIndex.Has(
				f.ctx, types.NewCurrentServiceAddressIndexKey(shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, newKey.Address),
			)
			require.NoError(t, err)
			require.False(t, hasNewIndex)
		})
	}

	t.Run("all counters zero", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
		const chainID = "trueopen-builder-rotation-zero-counters"
		f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithChainID(chainID).WithBlockHeight(100))
		operator := hubIdentity(t, 190)
		newKey := hubIdentity(t, 191)
		registerBuilderIdentityForTest(t, f, operator, 10)

		proofBytes := serviceKeyRotationProofBytes(
			chainID, shared.ParticipantType_PARTICIPANT_TYPE_BUILDER,
			sdk.MustAccAddressFromBech32(operator.Address).Bytes(), newKey.PubKey, 1, 2,
		)
		eventsBefore := len(hubEventsOfType(t, sdk.UnwrapSDKContext(f.ctx), &types.EventServiceKeyRotated{}))
		response, err := keeper.NewMsgServerImpl(f.keeper).RotateServiceKey(f.ctx, &types.MsgRotateServiceKey{
			ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_BUILDER,
			OperatorAddress: operator.Address, NewServicePubkey: newKey.PubKey,
			ExpectedCurrentServiceAuthorizationNonce: 1,
			NewServiceKeyProof:                       hubSign(t, newKey, proofBytes),
		})
		require.NoError(t, err)
		require.Equal(t, newKey.Address, response.NewServiceAddress)
		require.Equal(t, uint64(2), response.NewNonce)
		events := hubEventsOfType(t, sdk.UnwrapSDKContext(f.ctx), &types.EventServiceKeyRotated{})
		require.Len(t, events, eventsBefore+1)
		event := events[len(events)-1].(*types.EventServiceKeyRotated)
		require.Equal(t, operator.Address, event.Operator)
		require.Equal(t, newKey.Address, event.NewServiceAddress)
		require.Equal(t, uint64(2), event.AuthorizationNonce)
	})
}

func TestMsgStakeServiceRejectsNonCanonicalServicePubkeyWithoutSideEffects(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	operator := hubIdentity(t, 111)
	serviceKey := hubIdentity(t, 112)
	const chainID = "trueopen-stake-test"
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithChainID(chainID).WithBlockHeight(10))
	f.bank.seedAccount(operator.Address, testServiceBondMinInitial)

	// service_pubkey is bytes now, so "non-canonical" means "not the canonical
	// 33-byte compressed secp256k1 encoding" instead of the old hex-text
	// whitespace case. All three shapes must be refused before any write.
	uncompressed := append([]byte{0x04}, serviceKey.PubKey[1:]...)
	for _, testCase := range []struct {
		name              string
		servicePubkey     []byte
		expectedErrorPart string
	}{
		{
			name:              "uncompressed prefix",
			servicePubkey:     uncompressed,
			expectedErrorPart: "must be a compressed secp256k1 pubkey",
		},
		{
			name:              "truncated",
			servicePubkey:     serviceKey.PubKey[:len(serviceKey.PubKey)-1],
			expectedErrorPart: "must be exactly 33 bytes",
		},
		{
			name:              "trailing byte",
			servicePubkey:     append(append([]byte(nil), serviceKey.PubKey...), 0x00),
			expectedErrorPart: "must be exactly 33 bytes",
		},
	} {
		signingBytes := serviceRegistrationProofBytes(chainID, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, operator.Address, testCase.servicePubkey)
		_, err := keeper.NewMsgServerImpl(f.keeper).StakeService(f.ctx, &types.MsgStakeService{
			OperatorAddress: operator.Address,
			Action: registerServiceAction(
				testCase.servicePubkey, hubSign(t, serviceKey, signingBytes), testServiceBondMinInitial,
			),
		})
		require.ErrorContains(t, err, testCase.expectedErrorPart, testCase.name)
	}

	require.Equal(t, testServiceBondMinInitial, f.bank.accountBalance(operator.Address))
	require.Zero(t, f.bank.moduleBalance(types.ServiceBondModuleName))

	hasNode, err := f.keeper.CortexNode.Has(f.ctx, operator.Address)
	require.NoError(t, err)
	require.False(t, hasNode)
	hasBond, err := f.keeper.ServiceBond.Has(f.ctx, types.NewServiceBondKey(operator.Address))
	require.NoError(t, err)
	require.False(t, hasBond)
	hasIndex, err := f.keeper.CurrentServiceAddressIndex.Has(
		f.ctx, types.NewCurrentServiceAddressIndexKey(shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, serviceKey.Address),
	)
	require.NoError(t, err)
	require.False(t, hasIndex)
}

// TestServiceRegistrationProofIsRoleBound is the §1.3 rule 7 role-replay negative
// test that binding participant_type made writable. While Cortex registration and
// Builder registration built a byte-identical TRUEOPEN_SERVICE_REGISTRATION_V1
// preimage, one proof-of-possession satisfied both handlers and this assertion
// could not be expressed at all: that is the §1.4 rule 1 breach of one domain
// carrying two semantics.
func TestServiceRegistrationProofIsRoleBound(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	operator := hubIdentity(t, 141)
	serviceKey := hubIdentity(t, 142)
	const chainID = "trueopen-role-replay"
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithChainID(chainID).WithBlockHeight(10))
	f.bank.seedAccount(operator.Address, testServiceBondMinInitial)

	cortexProof := serviceRegistrationProofBytes(
		chainID, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, operator.Address, serviceKey.PubKey,
	)
	builderProof := serviceRegistrationProofBytes(
		chainID, shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, operator.Address, serviceKey.PubKey,
	)
	require.NotEqual(t, cortexProof, builderProof,
		"one registered domain must not serve two participant roles with one preimage")

	server := keeper.NewMsgServerImpl(f.keeper)
	_, err := server.StakeService(f.ctx, &types.MsgStakeService{
		OperatorAddress: operator.Address,
		Action: registerServiceAction(
			serviceKey.PubKey, hubSign(t, serviceKey, builderProof), testServiceBondMinInitial,
		),
	})
	require.ErrorContains(t, err, "invalid service key proof-of-possession",
		"a Builder-role registration proof must not register a Cortex node")

	builderRequest := &types.MsgRegisterBuilder{
		BuilderOperatorAddress: operator.Address,
		ServicePubkey:          serviceKey.PubKey,
		ServiceKeyProof:        hubSign(t, serviceKey, cortexProof),
		Descriptor_: types.ServiceDescriptorV1{Endpoints: []types.ServiceEndpointV1{{
			EndpointKind: types.ServiceEndpointKind_SERVICE_ENDPOINT_KIND_NEXUS_GRPC,
			Uri:          "https://builder.invalid", ProtocolVersion: "v1",
		}}},
	}
	_, err = server.RegisterBuilder(f.ctx, builderRequest)
	require.ErrorContains(t, err, "invalid service key proof-of-possession",
		"a Cortex-role registration proof must not register a Builder")

	// The role-correct proof still registers, so the new field narrows the digest
	// without breaking either handler.
	_, err = server.StakeService(f.ctx, &types.MsgStakeService{
		OperatorAddress: operator.Address,
		Action: registerServiceAction(
			serviceKey.PubKey, hubSign(t, serviceKey, cortexProof), testServiceBondMinInitial,
		),
	})
	require.NoError(t, err)
}

func TestMsgRegisterBuilderPersistsIdentityKeyAndDescriptorAtomically(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	operator := hubIdentity(t, 145)
	serviceKey := hubIdentity(t, 146)
	const chainID = "trueopen-builder-registration"
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithChainID(chainID).WithBlockHeight(10))
	proofBytes := serviceRegistrationProofBytes(
		chainID, shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, operator.Address, serviceKey.PubKey,
	)
	request := &types.MsgRegisterBuilder{
		BuilderOperatorAddress: operator.Address,
		ServicePubkey:          serviceKey.PubKey,
		ServiceKeyProof:        hubSign(t, serviceKey, proofBytes),
		Descriptor_: types.ServiceDescriptorV1{Endpoints: []types.ServiceEndpointV1{{
			EndpointKind: types.ServiceEndpointKind_SERVICE_ENDPOINT_KIND_NEXUS_GRPC,
			Uri:          "grpc://builder.invalid:8080", ProtocolVersion: "trueopen-nexus-ingress-v1",
		}}},
	}

	response, err := keeper.NewMsgServerImpl(f.keeper).RegisterBuilder(f.ctx, request)
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Status)

	builder, err := f.keeper.GetBuilderState(f.ctx, operator.Address)
	require.NoError(t, err)
	require.Equal(t, serviceKey.Address, builder.CurrentServiceAddress)
	require.Equal(t, serviceKey.PubKey, builder.CurrentServicePubkey)
	require.Equal(t, uint64(1), builder.ServiceAuthorizationNonce)
	require.Equal(t, types.ServiceKeyStatus_SERVICE_KEY_STATUS_ACTIVE, builder.CurrentServiceKeyStatus)
	descriptor, err := f.keeper.GetServiceDescriptor(
		f.ctx, types.NewParticipantKey(shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, operator.Address),
	)
	require.NoError(t, err)
	require.Equal(t, uint64(1), descriptor.DescriptorVersion)

	replay, err := keeper.NewMsgServerImpl(f.keeper).RegisterBuilder(f.ctx, request)
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, replay.Status)
}

func TestMsgStakeServicePersistsOperatorBondAndCurrentServiceKeyAtomically(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	operator := hubIdentity(t, 113)
	serviceKey := hubIdentity(t, 114)
	const chainID = "trueopen-stake-test"
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithChainID(chainID).WithBlockHeight(10))
	f.bank.seedAccount(operator.Address, testServiceBondMinInitial)

	signingBytes := serviceRegistrationProofBytes(chainID, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, operator.Address, serviceKey.PubKey)
	response, err := keeper.NewMsgServerImpl(f.keeper).StakeService(f.ctx, &types.MsgStakeService{
		OperatorAddress: operator.Address,
		Action: registerServiceAction(
			serviceKey.PubKey, hubSign(t, serviceKey, signingBytes), testServiceBondMinInitial,
		),
	})
	require.NoError(t, err)
	require.Equal(t, operator.Address, response.OperatorAddress)
	require.Equal(t, shared.NewAmount(testServiceBondMinInitial), response.ActiveBond)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Status)
	require.Zero(t, f.bank.accountBalance(operator.Address))
	require.Equal(t, testServiceBondMinInitial, f.bank.moduleBalance(types.ServiceBondModuleName))

	storedNode, err := f.keeper.GetCortexNodeState(f.ctx, operator.Address)
	require.NoError(t, err)
	require.Equal(t, operator.Address, storedNode.OperatorAddress)
	bond, err := f.keeper.GetServiceBondState(f.ctx, operator.Address)
	require.NoError(t, err)
	require.Equal(t, testServiceBondMinInitial, bond.ActiveBond)

	// The standalone ServiceKeyBinding collection is gone: the accepted current key
	// is inlined on CortexNodeState and mirrored by the derived
	// CurrentServiceAddressIndex, so both sides of that pair are asserted here.
	require.Equal(t, serviceKey.Address, storedNode.CurrentServiceAddress)
	require.Equal(t, serviceKey.PubKey, storedNode.CurrentServicePubkey)
	require.Equal(t, uint64(1), storedNode.ServiceAuthorizationNonce)
	require.Equal(t, types.ServiceKeyStatusActive, storedNode.ServiceKeyStatus)
	index, err := f.keeper.GetCurrentServiceAddressIndex(
		f.ctx, types.NewCurrentServiceAddressIndexKey(shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, serviceKey.Address),
	)
	require.NoError(t, err)
	require.Equal(t, operator.Address, index.OperatorAddress)
	require.Equal(t, uint64(1), index.ServiceAuthorizationNonce)

	// P1-6 / §5.11 event order: 112 service_registered must precede
	// 6 service_stake_changed.
	registeredIndex, stakeChangedIndex := -1, -1
	for i, event := range sdk.UnwrapSDKContext(f.ctx).EventManager().Events() {
		switch event.Type {
		case proto.MessageName(&types.EventServiceRegistered{}):
			if registeredIndex < 0 {
				registeredIndex = i
			}
		case proto.MessageName(&types.EventServiceStakeChanged{}):
			if stakeChangedIndex < 0 {
				stakeChangedIndex = i
			}
		}
	}
	require.GreaterOrEqual(t, registeredIndex, 0)
	require.GreaterOrEqual(t, stakeChangedIndex, 0)
	require.Less(t, registeredIndex, stakeChangedIndex)

	const topUp = uint64(25_000)
	f.bank.seedAccount(operator.Address, topUp)
	topUpResponse, err := keeper.NewMsgServerImpl(f.keeper).StakeService(f.ctx, &types.MsgStakeService{
		OperatorAddress: operator.Address, Action: topUpServiceAction(topUp),
	})
	require.NoError(t, err)
	require.Equal(t, shared.NewAmount(testServiceBondMinInitial+topUp), topUpResponse.ActiveBond)
	// A top-up must not touch the identity row it is not allowed to carry.
	toppedUpNode, err := f.keeper.GetCortexNodeState(f.ctx, operator.Address)
	require.NoError(t, err)
	require.Equal(t, storedNode.CurrentServiceAddress, toppedUpNode.CurrentServiceAddress)
	require.Equal(t, storedNode.CurrentServicePubkey, toppedUpNode.CurrentServicePubkey)
	require.Equal(t, storedNode.ServiceAuthorizationNonce, toppedUpNode.ServiceAuthorizationNonce)
}

// TestMsgStakeServiceTopUpRejectsOldKeyImmediatelyAfterRotation is the
// rotation-scoped form of the old-key rejection property.
//
// The original premise -- "a top-up signed by the pre-rotation service key is
// refused" -- became unexpressible when the API contract step 1 removed
// service_pubkey / service_key_proof / authorization_nonce from TopUpServiceV1: a
// top-up now carries an amount only, so there is no old key material left to
// refuse. The property under test is unchanged ("the previous service key loses
// all authority the instant the rotation commits"), so it is asserted where the
// key material still exists, on MsgRotateServiceKey itself:
//
//  1. the pre-rotation nonce is no longer accepted (replay),
//  2. a proof produced by the old key no longer authorizes a rotation,
//  3. the old service address is no longer resolvable and the new one is, and
//  4. the keyless top-up succeeds, which is the §10.0c step 1 fact that replaced
//     the deleted premise.
func TestMsgStakeServiceTopUpRejectsOldKeyImmediatelyAfterRotation(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	operator := hubIdentity(t, 117)
	oldKey := hubIdentity(t, 118)
	newKey := hubIdentity(t, 119)
	thirdKey := hubIdentity(t, 120)
	const chainID = "trueopen-top-up-test"
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithChainID(chainID).WithBlockHeight(100))
	f.bank.seedAccount(operator.Address, testServiceBondMinInitial)
	operatorBytes := sdk.MustAccAddressFromBech32(operator.Address).Bytes()

	registerBytes := serviceRegistrationProofBytes(chainID, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, operator.Address, oldKey.PubKey)
	_, err := keeper.NewMsgServerImpl(f.keeper).StakeService(f.ctx, &types.MsgStakeService{
		OperatorAddress: operator.Address,
		Action: registerServiceAction(
			oldKey.PubKey, hubSign(t, oldKey, registerBytes), testServiceBondMinInitial,
		),
	})
	require.NoError(t, err)

	rotateBytes := serviceKeyRotationProofBytes(
		chainID, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, operatorBytes, newKey.PubKey, 1, 2,
	)
	rotateResponse, err := keeper.NewMsgServerImpl(f.keeper).RotateServiceKey(f.ctx, &types.MsgRotateServiceKey{
		OperatorAddress: operator.Address, ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_CORTEX,
		NewServicePubkey: newKey.PubKey, ExpectedCurrentServiceAuthorizationNonce: 1,
		NewServiceKeyProof: hubSign(t, newKey, rotateBytes),
	})
	require.NoError(t, err)
	require.Equal(t, newKey.Address, rotateResponse.NewServiceAddress)
	require.Equal(t, uint64(2), rotateResponse.NewNonce)

	// (1) The pre-rotation nonce cannot be replayed.
	replayBytes := serviceKeyRotationProofBytes(
		chainID, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, operatorBytes, thirdKey.PubKey, 1, 2,
	)
	_, err = keeper.NewMsgServerImpl(f.keeper).RotateServiceKey(f.ctx, &types.MsgRotateServiceKey{
		OperatorAddress: operator.Address, ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_CORTEX,
		NewServicePubkey: thirdKey.PubKey, ExpectedCurrentServiceAuthorizationNonce: 1,
		NewServiceKeyProof: hubSign(t, thirdKey, replayBytes),
	})
	require.ErrorContains(t, err, "expected current service authorization nonce does not match")
	require.ErrorIs(t, err, types.ErrExpectedVersionMismatch)

	// (2) The old key can no longer produce an accepted proof, not even over the
	// otherwise correct next preimage.
	nextRotateBytes := serviceKeyRotationProofBytes(
		chainID, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, operatorBytes, thirdKey.PubKey, 2, 3,
	)
	_, err = keeper.NewMsgServerImpl(f.keeper).RotateServiceKey(f.ctx, &types.MsgRotateServiceKey{
		OperatorAddress: operator.Address, ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_CORTEX,
		NewServicePubkey: thirdKey.PubKey, ExpectedCurrentServiceAuthorizationNonce: 2,
		NewServiceKeyProof: hubSign(t, oldKey, nextRotateBytes),
	})
	require.ErrorContains(t, err, "invalid service key proof-of-possession")
	require.ErrorIs(t, err, types.ErrInvalidSignature)

	// (3) Exactly one current service address resolves, and it is the new one.
	node, err := f.keeper.GetCortexNodeState(f.ctx, operator.Address)
	require.NoError(t, err)
	require.Equal(t, newKey.Address, node.CurrentServiceAddress)
	require.Equal(t, newKey.PubKey, node.CurrentServicePubkey)
	require.Equal(t, uint64(2), node.ServiceAuthorizationNonce)
	hasOldIndex, err := f.keeper.CurrentServiceAddressIndex.Has(
		f.ctx, types.NewCurrentServiceAddressIndexKey(shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, oldKey.Address),
	)
	require.NoError(t, err)
	require.False(t, hasOldIndex)
	newIndex, err := f.keeper.GetCurrentServiceAddressIndex(
		f.ctx, types.NewCurrentServiceAddressIndexKey(shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, newKey.Address),
	)
	require.NoError(t, err)
	require.Equal(t, operator.Address, newIndex.OperatorAddress)
	require.Equal(t, uint64(2), newIndex.ServiceAuthorizationNonce)

	// (4) The keyless top-up is accepted and leaves the rotated binding alone.
	const topUp = uint64(20_000)
	f.bank.seedAccount(operator.Address, topUp)
	response, err := keeper.NewMsgServerImpl(f.keeper).StakeService(f.ctx, &types.MsgStakeService{
		OperatorAddress: operator.Address, Action: topUpServiceAction(topUp),
	})
	require.NoError(t, err)
	require.Equal(t, shared.NewAmount(testServiceBondMinInitial+topUp), response.ActiveBond)
	toppedUp, err := f.keeper.GetCortexNodeState(f.ctx, operator.Address)
	require.NoError(t, err)
	require.Equal(t, newKey.Address, toppedUp.CurrentServiceAddress)
	require.Equal(t, uint64(2), toppedUp.ServiceAuthorizationNonce)
}
