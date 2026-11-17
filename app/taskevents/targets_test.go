package taskevents

import (
	"bytes"
	"context"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtrpc "github.com/cometbft/cometbft/rpc/client"
	clientmocks "github.com/cometbft/cometbft/rpc/client/mocks"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func targetTestAddress(t *testing.T, marker byte) string {
	t.Helper()
	address, err := bech32.ConvertAndEncode("trueopen", bytes.Repeat([]byte{marker}, 20))
	require.NoError(t, err)
	return address
}

func TestValueEvidenceTargetsFollowTheAttributedDuty(t *testing.T) {
	operator := targetTestAddress(t, 7)
	for _, duty := range []shared.Duty{shared.DutyWorker, shared.DutyVerifier} {
		event, known := classifyTypedEvent(&tasktypes.EventVerifierValueEvidenceAccepted{TargetOperator: operator, FaultedDuty: duty})
		require.True(t, known)
		require.Len(t, event.Targets, 1)
		require.True(t, containsTarget(event.Targets, operator, dutyEventRole(duty)))
	}
}

func TestChallengeEventTargetsUseThePrimaryAtTheEventHeight(t *testing.T) {
	builder, challenger := targetTestAddress(t, 1), targetTestAddress(t, 2)
	id := bytes.Repeat([]byte{3}, 32)
	response := &tasktypes.QueryDispatchChallengeResponse{Row: &tasktypes.DispatchChallengeState{ChallengeId: id, BuilderAddress: builder, ChallengerAddress: challenger}}
	raw, err := proto.Marshal(response)
	require.NoError(t, err)
	client := &clientmocks.Client{}
	client.On("ABCIQueryWithOptions", mock.Anything, "/task.v1.Query/DispatchChallenge", mock.Anything, cmtrpc.ABCIQueryOptions{Height: 77}).Return(&coretypes.ResultABCIQuery{Response: abci.ResponseQuery{Height: 77, Value: raw}}, nil).Once()
	opened, known := classifyTypedEvent(&tasktypes.EventDispatchChallengeOpened{ChallengeId: id, Challenger: challenger})
	require.True(t, known)
	resolved, known := classifyTypedEvent(&tasktypes.EventDispatchChallengeResolved{ChallengeId: id})
	require.True(t, known)
	events, err := enrichEventTargets(context.Background(), client, 77, []chainEvent{opened, resolved})
	require.NoError(t, err)
	for _, event := range events {
		require.Len(t, event.Targets, 2)
		require.True(t, containsTarget(event.Targets, builder, shared.EventRole_EVENT_ROLE_BUILDER))
		require.True(t, containsTarget(event.Targets, challenger, shared.EventRole_EVENT_ROLE_OPERATOR))
		require.False(t, containsTarget(event.Targets, challenger, shared.EventRole_EVENT_ROLE_BUILDER))
	}
	client.AssertExpectations(t)
}

func TestRotationTargetsNotifyCurrentBuildersAndTheAffectedOperator(t *testing.T) {
	first, second, rotating, cortex := targetTestAddress(t, 1), targetTestAddress(t, 2), targetTestAddress(t, 3), targetTestAddress(t, 4)
	response := &hubtypes.QueryBuilderSetResponse{Set: hubtypes.BuilderSetViewV1{ActiveBuilders: []string{first, second}, ActiveBuilderCount: 2, BodyStatus: shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE}}
	raw, err := proto.Marshal(response)
	require.NoError(t, err)
	client := &clientmocks.Client{}
	client.On("ABCIQueryWithOptions", mock.Anything, "/hub.v1.Query/BuilderSet", mock.Anything, cmtrpc.ABCIQueryOptions{Height: 90}).Return(&coretypes.ResultABCIQuery{Response: abci.ResponseQuery{Height: 90, Value: raw}}, nil).Once()
	cortexEvent, known := classifyTypedEvent(&hubtypes.EventServiceKeyRotationRegistered{PendingRotation: hubtypes.PendingServiceKeyRotationState{ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, OperatorAddress: cortex}})
	require.True(t, known)
	builderEvent, known := classifyTypedEvent(&hubtypes.EventServiceKeyRotationRegistered{PendingRotation: hubtypes.PendingServiceKeyRotationState{ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, OperatorAddress: rotating}})
	require.True(t, known)
	events, err := enrichEventTargets(context.Background(), client, 90, []chainEvent{cortexEvent, builderEvent})
	require.NoError(t, err)
	for _, event := range events {
		require.True(t, containsTarget(event.Targets, first, shared.EventRole_EVENT_ROLE_BUILDER))
		require.True(t, containsTarget(event.Targets, second, shared.EventRole_EVENT_ROLE_BUILDER))
	}
	require.Len(t, events[0].Targets, 3)
	require.True(t, containsTarget(events[0].Targets, cortex, shared.EventRole_EVENT_ROLE_OPERATOR))
	require.False(t, containsTarget(events[0].Targets, cortex, shared.EventRole_EVENT_ROLE_BUILDER))
	require.Len(t, events[1].Targets, 4)
	require.True(t, containsTarget(events[1].Targets, rotating, shared.EventRole_EVENT_ROLE_BUILDER))
	client.AssertExpectations(t)
}

func TestEventTargetsRejectSubstitutedQueryHeight(t *testing.T) {
	client := &clientmocks.Client{}
	client.On("ABCIQueryWithOptions", mock.Anything, "/task.v1.Query/DispatchChallenge", mock.Anything, cmtrpc.ABCIQueryOptions{Height: 77}).Return(&coretypes.ResultABCIQuery{Response: abci.ResponseQuery{Height: 78}}, nil).Once()
	event, _ := classifyTypedEvent(&tasktypes.EventDispatchChallengeResolved{ChallengeId: bytes.Repeat([]byte{3}, 32)})
	_, err := enrichEventTargets(context.Background(), client, 77, []chainEvent{event})
	require.ErrorContains(t, err, "unavailable at height 77")
	client.AssertExpectations(t)
}
