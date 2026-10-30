package keeper_test

import (
	"encoding/hex"
	"testing"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func rotationFixture(t *testing.T) (*fixture, hubTestIdentity, hubTestIdentity) {
	t.Helper()
	f := initFixture(t)
	genesis := types.DefaultGenesis()
	genesis.Params.Epoch.EpochLengthBlocks = 120
	genesis.Params.Epoch.DeltaMBlocks = 10
	genesis.Params.Epoch.DeltaWBlocks = 10
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(100))
	operator, next := hubIdentity(t, 207), hubIdentity(t, 208)
	registerBuilderIdentityForTest(t, f, operator, 10)
	return f, operator, next
}

func requestBuilderRotation(t *testing.T, f *fixture, operator, next hubTestIdentity) *types.MsgRotateServiceKeyResponse {
	t.Helper()
	proof := serviceKeyRotationProofBytes(sdk.UnwrapSDKContext(f.ctx).ChainID(), shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, sdk.MustAccAddressFromBech32(operator.Address).Bytes(), next.PubKey, 1, 2)
	response, err := keeper.NewMsgServerImpl(f.keeper).RotateServiceKey(f.ctx, &types.MsgRotateServiceKey{ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, OperatorAddress: operator.Address, NewServicePubkey: next.PubKey, ExpectedCurrentServiceAuthorizationNonce: 1, NewServiceKeyProof: hubSign(t, next, proof)})
	require.NoError(t, err)
	return response
}

func activateServiceRotationForTest(t *testing.T, f *fixture, role shared.ParticipantType, operator string) {
	t.Helper()
	pending, err := f.keeper.ReadPendingServiceKeyRotation(f.ctx, role, operator)
	require.NoError(t, err)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(pending.EffectiveHeight - 1)))
	err = f.keeper.ProcessServiceKeyRotations(f.ctx, pending.EffectiveHeight)
	require.NoError(t, err)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(pending.EffectiveHeight)))
}

func TestServiceRotationActivatesAtFixedEpochWithCurrentResponsibilities(t *testing.T) {
	f, operator, next := rotationFixture(t)
	locator := busObjectiveEvidenceLocator(operator, "rotation-session", "old-task")
	_, err := f.keeper.AcquireBusObjectiveEvidenceResponsibility(f.ctx, locator)
	require.NoError(t, err)
	response := requestBuilderRotation(t, f, operator, next)
	require.Equal(t, uint64(240), response.PendingRotation.EffectiveHeight)
	future := busObjectiveEvidenceLocator(operator, "rotation-session", "future-task")
	future.ServiceAuthorizationNonce = 2
	_, err = f.keeper.AcquireBusObjectiveEvidenceResponsibility(f.ctx, future)
	require.Error(t, err, "pending keys must not acquire future Task responsibilities")
	pendingTask := busObjectiveEvidenceLocator(operator, "rotation-session", "pending-task")
	_, err = f.keeper.AcquireBusObjectiveEvidenceResponsibility(f.ctx, pendingTask)
	require.NoError(t, err, "pending rotation does not stop current duties")
	require.NoError(t, f.keeper.ProcessServiceKeyRotations(f.ctx, 239))
	before, err := f.keeper.GetBuilderState(f.ctx, operator.Address)
	require.NoError(t, err)
	require.Equal(t, operator.PubKey, before.CurrentServicePubkey)
	activateServiceRotationForTest(t, f, shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, operator.Address)
	current, err := f.keeper.GetBuilderState(f.ctx, operator.Address)
	require.NoError(t, err)
	require.Equal(t, next.PubKey, current.CurrentServicePubkey)
	require.Equal(t, uint64(2), current.ServiceAuthorizationNonce)
	require.Equal(t, operator.PubKey, current.GetPreviousServicePubkey())
	require.Equal(t, uint64(1), current.GetPreviousServiceAuthorizationNonce())
	require.Equal(t, uint64(240), current.GetPreviousRetiredHeight())
	require.Equal(t, uint32(2), current.PendingEvidenceSubmissionCount)
	oldProof, err := f.keeper.GetBuilderEvidenceProofKey(f.ctx, operator.Address, 1, locator.SessionId, locator.TaskId)
	require.NoError(t, err)
	require.Equal(t, operator.PubKey, mustDecodeHexForRotation(t, oldProof.ServicePubkey))
	_, err = f.keeper.GetBuilderEvidenceProofKey(f.ctx, operator.Address, 2, locator.SessionId, locator.TaskId)
	require.Error(t, err, "a current key does not rewrite a frozen proof generation")
	oldIndexed, err := f.keeper.CurrentServiceAddressIndex.Has(f.ctx, types.NewCurrentServiceAddressIndexKey(shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, operator.Address))
	require.NoError(t, err)
	require.False(t, oldIndexed)
	require.NoError(t, f.keeper.EnsureServiceKeyRotationInvariant(f.ctx))
}

func mustDecodeHexForRotation(t *testing.T, value string) []byte {
	t.Helper()
	raw, err := hex.DecodeString(value)
	require.NoError(t, err)
	return raw
}

func TestServiceRotationRegistrationProtectsOnlyPreviousEvidence(t *testing.T) {
	f, operator, next := rotationFixture(t)
	locator := busObjectiveEvidenceLocator(operator, "rotation-session", "old-task")
	_, err := f.keeper.AcquireBusObjectiveEvidenceResponsibility(f.ctx, locator)
	require.NoError(t, err)
	requestBuilderRotation(t, f, operator, next)
	activateServiceRotationForTest(t, f, shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, operator.Address)
	third := hubIdentity(t, 209)
	proof := serviceKeyRotationProofBytes(sdk.UnwrapSDKContext(f.ctx).ChainID(), shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, sdk.MustAccAddressFromBech32(operator.Address).Bytes(), third.PubKey, 2, 3)
	req := &types.MsgRotateServiceKey{ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, OperatorAddress: operator.Address, NewServicePubkey: third.PubKey, ExpectedCurrentServiceAuthorizationNonce: 2, NewServiceKeyProof: hubSign(t, third, proof)}
	server := keeper.NewMsgServerImpl(f.keeper)
	_, err = server.RotateServiceKey(f.ctx, req)
	require.ErrorContains(t, err, "still protected")
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(481))
	_, err = server.RotateServiceKey(f.ctx, req)
	require.ErrorContains(t, err, "still protected", "the elapsed interval does not discard pending previous evidence")
	_, err = f.keeper.ReleaseBusObjectiveEvidenceResponsibility(f.ctx, locator)
	require.NoError(t, err)
	currentTask := busObjectiveEvidenceLocator(operator, "rotation-session", "current-task")
	currentTask.ServiceAuthorizationNonce = 2
	_, err = f.keeper.AcquireBusObjectiveEvidenceResponsibility(f.ctx, currentTask)
	require.NoError(t, err)
	response, err := server.RotateServiceKey(f.ctx, req)
	require.NoError(t, err, "current evidence responsibilities do not block registration")
	require.Equal(t, uint64(720), response.PendingRotation.EffectiveHeight)
	genesis, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, genesis.Validate())
	genesis.PendingServiceKeyRotations[0].RegisteredHeight = 480
	require.ErrorContains(t, genesis.Validate(), "protected", "import cannot bypass the registration interval")
	genesis.PendingServiceKeyRotations[0].RegisteredHeight = 481
	genesis.ServiceKeyResponsibilities[0].ServiceAuthorizationNonce = 1
	require.ErrorContains(t, genesis.Validate(), "protected", "import cannot overwrite previous evidence authority")
}

func TestServiceRotationGenesisRestoresPendingAndPrevious(t *testing.T) {
	f, operator, next := rotationFixture(t)
	locator := busObjectiveEvidenceLocator(operator, "rotation-session", "old-task")
	_, err := f.keeper.AcquireBusObjectiveEvidenceResponsibility(f.ctx, locator)
	require.NoError(t, err)
	requestBuilderRotation(t, f, operator, next)
	genesis, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Len(t, genesis.PendingServiceKeyRotations, 1)
	require.NoError(t, genesis.Validate())
	fresh := initFixture(t)
	fresh.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(fresh.ctx).WithBlockHeight(101))
	require.NoError(t, fresh.keeper.InitGenesis(fresh.ctx, *genesis))
	activateServiceRotationForTest(t, fresh, shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, operator.Address)
	genesis, err = fresh.keeper.ExportGenesis(fresh.ctx)
	require.NoError(t, err)
	require.NoError(t, genesis.Validate())
	restored := initFixture(t)
	restored.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(restored.ctx).WithBlockHeight(241))
	require.NoError(t, restored.keeper.InitGenesis(restored.ctx, *genesis))
	key, err := restored.keeper.GetBuilderEvidenceProofKey(restored.ctx, operator.Address, 1, locator.SessionId, locator.TaskId)
	require.NoError(t, err)
	require.Equal(t, operator.PubKey, mustDecodeHexForRotation(t, key.ServicePubkey))
}

func TestRepeatedRevokeCancelsNewPendingRotation(t *testing.T) {
	f, operator, next := rotationFixture(t)
	server := keeper.NewMsgServerImpl(f.keeper)
	req := &types.MsgRevokeServiceKey{ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, OperatorAddress: operator.Address, ExpectedCurrentServiceAuthorizationNonce: 1, ReasonCode: types.ServiceKeyRevocationReason_SERVICE_KEY_REVOCATION_REASON_COMPROMISED}
	_, err := server.RevokeServiceKey(f.ctx, req)
	require.NoError(t, err)
	requestBuilderRotation(t, f, operator, next)
	_, err = server.RevokeServiceKey(f.ctx, req)
	require.NoError(t, err)
	_, err = f.keeper.ReadPendingServiceKeyRotation(f.ctx, shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, operator.Address)
	require.ErrorIs(t, err, collections.ErrNotFound)
	reserved, err := f.keeper.PendingServiceAddressIndex.Has(f.ctx, next.Address)
	require.NoError(t, err)
	require.False(t, reserved)
}

func TestCortexPreviousEvidenceCanReplayAndReleaseAfterRotation(t *testing.T) {
	f, _, _ := rotationFixture(t)
	operator, next := hubIdentity(t, 211), hubIdentity(t, 212)
	registerCortexNodeIdentityForTest(t, f, operator.Address, operator, 2*testServiceBondMinInitial, 10, 1)
	sessionID, taskID := hubHashBytes("rotating-worker-session"), hubHashBytes("rotating-worker-task")
	id, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainServiceKeyResponsibilityIDV1)).Raw(
		shared.EnumBE(uint32(shared.ParticipantType_PARTICIPANT_TYPE_CORTEX)),
		shared.EnumBE(uint32(types.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_WORKER_OUTPUT_EVIDENCE)),
		sessionID, taskID, taskID, sdk.MustAccAddressFromBech32(operator.Address),
	).Sum()
	require.NoError(t, err)
	responsibility := types.ServiceKeyResponsibilityState{ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, OperatorAddress: operator.Address, ResponsibilityId: id, ResponsibilityKind: types.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_WORKER_OUTPUT_EVIDENCE, SessionId: sessionID, TaskId: taskID, CreatedHeight: 100}
	require.NoError(t, f.keeper.ReserveServiceKeyResponsibility(f.ctx, responsibility))
	proof := serviceKeyRotationProofBytes(sdk.UnwrapSDKContext(f.ctx).ChainID(), shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, sdk.MustAccAddressFromBech32(operator.Address), next.PubKey, 1, 2)
	_, err = keeper.NewMsgServerImpl(f.keeper).RotateServiceKey(f.ctx, &types.MsgRotateServiceKey{ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, OperatorAddress: operator.Address, ExpectedCurrentServiceAuthorizationNonce: 1, NewServicePubkey: next.PubKey, NewServiceKeyProof: hubSign(t, next, proof)})
	require.NoError(t, err)
	activateServiceRotationForTest(t, f, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, operator.Address)
	require.NoError(t, f.keeper.ReserveServiceKeyResponsibility(f.ctx, responsibility), "replay must retain the original generation")
	key, err := f.keeper.GetWorkerEvidenceProofKey(f.ctx, hex.EncodeToString(sessionID), hex.EncodeToString(taskID), operator.Address)
	require.NoError(t, err)
	require.Equal(t, uint64(1), key.AuthorizationNonce)
	require.Equal(t, operator.PubKey, mustDecodeHexForRotation(t, key.ServicePubkey))
	require.NoError(t, f.keeper.ReleaseServiceKeyResponsibility(f.ctx, shared.ParticipantTypeCortexNode, operator.Address, hex.EncodeToString(id)))
	node, err := f.keeper.ReadCortexNodeStore(f.ctx, operator.Address)
	require.NoError(t, err)
	require.Zero(t, node.PendingEvidenceSubmissionCount)
	require.Equal(t, uint64(2), node.ServiceAuthorizationNonce)
}

func TestServiceKeyActivationIsAtomicAcrossAllDueParticipants(t *testing.T) {
	f, operator, next := rotationFixture(t)
	requestBuilderRotation(t, f, operator, next)
	other, otherNext := hubIdentity(t, 213), hubIdentity(t, 214)
	registerBuilderIdentityForTest(t, f, other, 10)
	requestBuilderRotation(t, f, other, otherNext)
	iter, err := f.keeper.ServiceKeyRotationActivationIndex.Iterate(f.ctx, nil)
	require.NoError(t, err)
	var last types.ServiceKeyRotationActivationKey
	for ; iter.Valid(); iter.Next() {
		last, err = iter.Key()
		require.NoError(t, err)
	}
	require.NoError(t, iter.Close())
	key := types.NewParticipantKey(shared.ParticipantType(last.K2()), last.K3())
	original, err := f.keeper.PendingServiceKeyRotation.Get(f.ctx, key)
	require.NoError(t, err)
	invalid := original
	invalid.NewServicePubkey = []byte{1}
	require.NoError(t, f.keeper.PendingServiceKeyRotation.Set(f.ctx, key, invalid))
	require.Error(t, f.keeper.ProcessServiceKeyRotations(f.ctx, 240))
	for _, address := range []string{operator.Address, other.Address} {
		state, err := f.keeper.GetBuilderState(f.ctx, address)
		require.NoError(t, err)
		require.Equal(t, uint64(1), state.ServiceAuthorizationNonce, "a later failure must roll back earlier activations")
	}
	require.NoError(t, f.keeper.PendingServiceKeyRotation.Set(f.ctx, key, original))
	require.NoError(t, f.keeper.ProcessServiceKeyRotations(f.ctx, 240))
	for _, address := range []string{operator.Address, other.Address} {
		state, err := f.keeper.GetBuilderState(f.ctx, address)
		require.NoError(t, err)
		require.Equal(t, uint64(2), state.ServiceAuthorizationNonce)
	}
}
