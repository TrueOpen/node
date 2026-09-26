package keeper_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cosmos/gogoproto/jsonpb"
	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestCurrentServiceKeyQueryContractForCortexAndBuilder(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))

	operator := hubAddress(t, 171)
	cortexService := hubIdentity(t, 172)
	registerCortexNodeIdentityForTest(t, f, operator, cortexService, testServiceBondMinInitial, 10, 1)
	builderService := hubIdentity(t, 173)
	registerBuilderIdentityForTest(t, f, builderService, 11)

	queries := keeper.NewQueryServerImpl(f.keeper)
	cortexKey, err := queries.CurrentServiceKey(f.ctx, &types.QueryCurrentServiceKeyRequest{
		ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_CORTEX,
		OperatorAddress: operator,
	})
	require.NoError(t, err)
	require.Equal(t, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, cortexKey.Binding.ParticipantType)
	require.Equal(t, operator, cortexKey.Binding.OperatorAddress)
	require.Equal(t, cortexService.Address, cortexKey.Binding.ServiceAddress)
	require.Equal(t, cortexService.PubKey, cortexKey.Binding.ServicePubkey)
	require.Equal(t, uint64(1), cortexKey.Binding.ServiceAuthorizationNonce)
	// §16.3 CurrentServiceKeyViewV1 carries the lifecycle status in the
	// participant_status oneof; a CORTEX request must take the cortex branch and
	// must not also expose the builder branch.
	require.IsType(t, &types.CurrentServiceKeyViewV1_CortexServiceKeyStatus{}, cortexKey.Binding.ParticipantStatus)
	require.Equal(t, types.ServiceKeyStatusActive, cortexKey.Binding.GetCortexServiceKeyStatus())

	builderKey, err := queries.CurrentServiceKey(f.ctx, &types.QueryCurrentServiceKeyRequest{
		ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_BUILDER,
		OperatorAddress: builderService.Address,
	})
	require.NoError(t, err)
	require.IsType(t, &types.CurrentServiceKeyViewV1_BuilderServiceKeyStatus{}, builderKey.Binding.ParticipantStatus)
	require.Equal(t, types.ServiceKeyStatusActive, builderKey.Binding.GetBuilderServiceKeyStatus())

	node, err := queries.CortexNode(f.ctx, &types.QueryCortexNodeRequest{OperatorAddress: operator})
	require.NoError(t, err)
	require.Equal(t, operator, node.Node.OperatorAddress)
	require.Equal(t, cortexService.Address, node.Node.CurrentServiceAddress)
	require.Equal(t, cortexService.PubKey, node.Node.CurrentServicePubkey)
	require.Equal(t, uint64(1), node.Node.ServiceAuthorizationNonce)
	require.Equal(t, types.ServiceKeyStatusActive, node.Node.ServiceKeyStatus)
	require.Equal(t, uint64(10), node.Node.UpdatedHeight)

	assertCurrentServiceKeyJSONContract(t, cortexKey, node)
	assertCurrentServiceKeyGatewayContract(t, f.ctx, queries, operator)
}

func TestCurrentServiceKeyQueryRejectsInvalidOrMissingIdentity(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	queries := keeper.NewQueryServerImpl(f.keeper)
	operator := hubAddress(t, 174)

	_, err := queries.CurrentServiceKey(f.ctx, nil)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	// participant_type is a closed enum now, so the old "wrong spelling" and
	// "role name instead of participant type" cases become UNSPECIFIED and an
	// out-of-range value. VERIFIER is a Duty, never a ParticipantType, and the
	// enum only registers CORTEX=1/BUILDER=2 (§9.6b).
	_, err = queries.CurrentServiceKey(f.ctx, &types.QueryCurrentServiceKeyRequest{
		ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_UNSPECIFIED, OperatorAddress: operator,
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = queries.CurrentServiceKey(f.ctx, &types.QueryCurrentServiceKeyRequest{
		ParticipantType: shared.ParticipantType(3), OperatorAddress: operator,
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = queries.CurrentServiceKey(f.ctx, &types.QueryCurrentServiceKeyRequest{
		ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, OperatorAddress: "not-an-address",
	})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = queries.CurrentServiceKey(f.ctx, &types.QueryCurrentServiceKeyRequest{
		ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, OperatorAddress: operator,
	})
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestCortexNodeQueryFailsClosedOnMissingOrMismatchedBinding(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	operator := hubAddress(t, 175)
	service := hubIdentity(t, 176)
	registerCortexNodeIdentityForTest(t, f, operator, service, testServiceBondMinInitial, 10, 1)
	queries := keeper.NewQueryServerImpl(f.keeper)

	// The standalone ServiceKeyBinding primary is gone: the current key is
	// inlined on CortexNodeState (§6.4), so "identity row present, binding row
	// missing" no longer exists as a state. Removing the single row is a plain
	// NotFound, already covered by
	// TestCurrentServiceKeyQueryRejectsInvalidOrMissingIdentity. Only the
	// mismatch half of this test stays representable.
	node, err := f.keeper.ReadCortexNodeStore(f.ctx, operator)
	require.NoError(t, err)

	mismatched := node
	mismatched.OperatorAddress = hubAddress(t, 177)
	require.NoError(t, mismatched.Validate())
	storedMismatch, err := f.keeper.CortexNode.Get(f.ctx, operator)
	require.NoError(t, err)
	storedMismatch.OperatorAddress = hubAddressBytes(t, mismatched.OperatorAddress)
	require.NoError(t, f.keeper.CortexNode.Set(f.ctx, operator, storedMismatch))
	_, err = queries.CortexNode(f.ctx, &types.QueryCortexNodeRequest{OperatorAddress: operator})
	require.Equal(t, codes.Internal, status.Code(err))

	corrupt := node
	corrupt.ServiceAuthorizationNonce = 0
	require.Error(t, corrupt.Validate())
	storedCorrupt, err := f.keeper.CortexNode.Get(f.ctx, operator)
	require.NoError(t, err)
	storedCorrupt.OperatorAddress = hubAddressBytes(t, operator)
	storedCorrupt.ServiceAuthorizationNonce = 0
	require.NoError(t, f.keeper.CortexNode.Set(f.ctx, operator, storedCorrupt))
	_, err = queries.CortexNode(f.ctx, &types.QueryCortexNodeRequest{OperatorAddress: operator})
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestCurrentServiceKeyQueryReturnsRevokedBindingAndRejectsOrphanBinding(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	operator := hubAddress(t, 178)
	service := hubIdentity(t, 179)
	registerCortexNodeIdentityForTest(t, f, operator, service, testServiceBondMinInitial, 10, 1)
	revoked, err := f.keeper.ReadCortexNodeStore(f.ctx, operator)
	require.NoError(t, err)
	revoked.ServiceAuthorizationNonce = 2
	revoked.UpdatedHeight = 12
	revoked.ServiceKeyStatus = types.ServiceKeyStatusRevoked
	require.NoError(t, revoked.Validate())
	require.NoError(t, f.keeper.StoreCortexNode(f.ctx, operator, revoked))

	queries := keeper.NewQueryServerImpl(f.keeper)
	current, err := queries.CurrentServiceKey(f.ctx, &types.QueryCurrentServiceKeyRequest{
		ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_CORTEX,
		OperatorAddress: operator,
	})
	require.NoError(t, err)
	require.Equal(t, types.ServiceKeyStatusRevoked, current.Binding.GetCortexServiceKeyStatus())
	require.Equal(t, uint64(2), current.Binding.ServiceAuthorizationNonce)
	node, err := queries.CortexNode(f.ctx, &types.QueryCortexNodeRequest{OperatorAddress: operator})
	require.NoError(t, err)
	require.Equal(t, types.ServiceKeyStatusRevoked, node.Node.ServiceKeyStatus)
	require.Equal(t, uint64(2), node.Node.ServiceAuthorizationNonce)
	// revoked_height is no longer a field on either the view or CortexNodeState;
	// the revocation height is the row's updated_height.
	require.Equal(t, uint64(12), node.Node.UpdatedHeight)
	_, err = f.keeper.GetCurrentServiceKey(f.ctx, shared.ParticipantTypeCortexNode, operator)
	require.ErrorContains(t, err, "not active")

	// Orphan case, restated for the inlined schema: a row stored under one
	// operator's key while claiming to belong to another operator must fail
	// closed with Internal instead of answering with the foreign identity.
	orphanOperator := hubAddress(t, 180)
	orphanNode := revoked
	require.NoError(t, orphanNode.Validate())
	storedOrphan, err := f.keeper.CortexNode.Get(f.ctx, operator)
	require.NoError(t, err)
	require.NoError(t, f.keeper.CortexNode.Set(f.ctx, orphanOperator, storedOrphan))
	_, err = queries.CurrentServiceKey(f.ctx, &types.QueryCurrentServiceKeyRequest{
		ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_CORTEX,
		OperatorAddress: orphanOperator,
	})
	require.Equal(t, codes.Internal, status.Code(err))
}

func assertCurrentServiceKeyJSONContract(
	t *testing.T,
	key *types.QueryCurrentServiceKeyResponse,
	node *types.QueryCortexNodeResponse,
) {
	t.Helper()
	marshaller := jsonpb.Marshaler{OrigName: true, EmitDefaults: true}

	var keyBuffer bytes.Buffer
	require.NoError(t, marshaller.Marshal(&keyBuffer, key))
	var keyJSON map[string]map[string]any
	require.NoError(t, json.Unmarshal(keyBuffer.Bytes(), &keyJSON))
	require.Equal(t, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX.String(), keyJSON["binding"]["participant_type"])
	require.Equal(t, "1", keyJSON["binding"]["service_authorization_nonce"])
	// The participant_status oneof must emit exactly the cortex branch.
	require.NotContains(t, keyJSON["binding"], "builder_status")

	var nodeBuffer bytes.Buffer
	require.NoError(t, marshaller.Marshal(&nodeBuffer, node))
	var nodeJSON map[string]map[string]any
	require.NoError(t, json.Unmarshal(nodeBuffer.Bytes(), &nodeJSON))
	require.Equal(t, keyJSON["binding"]["operator_address"], nodeJSON["node"]["operator_address"])
	require.Equal(t, keyJSON["binding"]["service_address"], nodeJSON["node"]["current_service_address"])
	require.Equal(t, keyJSON["binding"]["service_pubkey"], nodeJSON["node"]["current_service_pubkey"])
	require.Equal(t, keyJSON["binding"]["service_authorization_nonce"], nodeJSON["node"]["service_authorization_nonce"])
	require.Equal(t, keyJSON["binding"]["cortex_service_key_status"], nodeJSON["node"]["service_key_status"])
	require.Equal(t, "10", nodeJSON["node"]["updated_height"])
	require.NotContains(t, nodeJSON, "current_service_address")
	require.NotContains(t, nodeJSON, "current_service_pubkey")
	require.NotContains(t, nodeJSON, "service_authorization_nonce")
	require.NotContains(t, nodeJSON["node"], "cortex_node_id")
	require.NotContains(t, nodeJSON["node"], "current_service_key_version")
}

func assertCurrentServiceKeyGatewayContract(t *testing.T, ctx context.Context, queries types.QueryServer, operator string) {
	t.Helper()
	mux := runtime.NewServeMux(runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
		OrigName:     true,
		EmitDefaults: true,
	}))
	require.NoError(t, types.RegisterQueryHandlerServer(context.Background(), mux, queries))

	participantSegment := shared.ParticipantType_PARTICIPANT_TYPE_CORTEX.String()
	currentPath := "/TrueOpen/hub/v1/current_service_key/" + participantSegment + "/" + operator
	request := httptest.NewRequest(http.MethodGet, currentPath, nil).WithContext(ctx)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	var payload map[string]map[string]any
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &payload))
	require.Equal(t, operator, payload["binding"]["operator_address"])

	nodeRequest := httptest.NewRequest(http.MethodGet, "/TrueOpen/hub/v1/cortex_node/"+operator, nil).WithContext(ctx)
	nodeResponse := httptest.NewRecorder()
	mux.ServeHTTP(nodeResponse, nodeRequest)
	require.Equal(t, http.StatusOK, nodeResponse.Code)
	var nodePayload map[string]map[string]any
	require.NoError(t, json.Unmarshal(nodeResponse.Body.Bytes(), &nodePayload))
	require.Equal(t, operator, nodePayload["node"]["operator_address"])
	require.Equal(t, "1", nodePayload["node"]["service_authorization_nonce"])
	require.NotContains(t, nodePayload, "current_service_address")
	require.NotContains(t, nodePayload["node"], "cortex_node_id")

	oldRequest := httptest.NewRequest(http.MethodGet, "/TrueOpen/hub/v1/service_key/"+participantSegment+"/"+operator, nil).WithContext(ctx)
	oldResponse := httptest.NewRecorder()
	mux.ServeHTTP(oldResponse, oldRequest)
	require.Equal(t, http.StatusNotFound, oldResponse.Code)
}

func TestTaskLiabilityJSONUsesOnlyOperatorAddress(t *testing.T) {
	liability := types.TaskLiabilityReservationState{
		SchemaVersion:     1,
		TaskId:            bytes.Repeat([]byte{0x7a}, 32),
		Duty:              shared.Duty_DUTY_WORKER,
		OperatorAddress:   hubAddress(t, 181),
		BondVersion:       1,
		CapabilityVersion: 1,
		ReservedAmount:    1,
		Status:            types.TaskLiabilityStatusReserved,
	}
	marshaller := jsonpb.Marshaler{OrigName: true, EmitDefaults: true}
	var output bytes.Buffer
	require.NoError(t, marshaller.Marshal(&output, &liability))
	require.Contains(t, output.String(), `"operator_address"`)
	require.NotContains(t, output.String(), `"cortex_node_id"`)
	require.NotContains(t, output.String(), `"operator_address_snapshot"`)
	require.NotContains(t, output.String(), `"service_key_version"`)
	// §B.1.3 dropped session_id from the reservation row; the task_id Hash32 is
	// the only scope key left.
	require.NotContains(t, output.String(), `"session_id"`)
}
