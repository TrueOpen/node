package taskevents

import (
	"context"
	"fmt"
	"math"

	cmtrpc "github.com/cometbft/cometbft/rpc/client"
	"github.com/cosmos/gogoproto/proto"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func queryEventState(ctx context.Context, client cmtrpc.Client, height uint64, path string, request, response proto.Message) error {
	if height == 0 || height > math.MaxInt64 {
		return fmt.Errorf("event query height is invalid")
	}
	raw, err := proto.Marshal(request)
	if err != nil {
		return err
	}
	result, err := client.ABCIQueryWithOptions(ctx, path, raw, cmtrpc.ABCIQueryOptions{Height: int64(height)})
	if err != nil {
		return fmt.Errorf("read event authority at height %d: %w", height, err)
	}
	if result == nil || result.Response.Code != 0 || result.Response.Height != int64(height) {
		return fmt.Errorf("event authority %s is unavailable at height %d", path, height)
	}
	return proto.Unmarshal(result.Response.Value, response)
}

// enrichEventTargets resolves only facts absent from the payload, at the event's
// exact height. A missing historical authority is an error, never a guessed target.
func enrichEventTargets(ctx context.Context, client cmtrpc.Client, height uint64, events []chainEvent) ([]chainEvent, error) {
	challenges := map[string]*tasktypes.DispatchChallengeState{}
	var builders *hubtypes.BuilderSetViewV1
	for i := range events {
		event := &events[i]
		var challengeID []byte
		switch payload := event.Payload.(type) {
		case *tasktypes.EventDispatchChallengeOpened:
			challengeID = payload.ChallengeId
		case *tasktypes.EventDispatchChallengeResolved:
			challengeID = payload.ChallengeId
		case *hubtypes.EventServiceKeyRotationRegistered:
			pending := payload.PendingRotation
			if pending.ParticipantType != shared.ParticipantType_PARTICIPANT_TYPE_BUILDER && pending.ParticipantType != shared.ParticipantType_PARTICIPANT_TYPE_CORTEX {
				return nil, fmt.Errorf("service rotation event has invalid participant role")
			}
			if builders == nil {
				var response hubtypes.QueryBuilderSetResponse
				request := &hubtypes.QueryBuilderSetRequest{Selector: &hubtypes.QueryBuilderSetRequest_Height{Height: height}}
				if err := queryEventState(ctx, client, height, "/hub.v1.Query/BuilderSet", request, &response); err != nil {
					return nil, err
				}
				if response.Set.BodyStatus != shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE || uint64(len(response.Set.ActiveBuilders)) != uint64(response.Set.ActiveBuilderCount) {
					return nil, fmt.Errorf("event BuilderSet body is unavailable")
				}
				builders = &response.Set
			}
			event.Targets = append(event.Targets, eventTargets(shared.EventRole_EVENT_ROLE_BUILDER, builders.ActiveBuilders...)...)
			if pending.ParticipantType == shared.ParticipantType_PARTICIPANT_TYPE_BUILDER {
				event.Targets = append(event.Targets, eventTargets(shared.EventRole_EVENT_ROLE_BUILDER, pending.OperatorAddress)...)
			}
		}
		if challengeID != nil {
			if len(challengeID) != 32 {
				return nil, fmt.Errorf("dispatch challenge event ID is not raw32")
			}
			primary := challenges[string(challengeID)]
			if primary == nil {
				var response tasktypes.QueryDispatchChallengeResponse
				if err := queryEventState(ctx, client, height, "/task.v1.Query/DispatchChallenge", &tasktypes.QueryDispatchChallengeRequest{ChallengeId: challengeID}, &response); err != nil {
					return nil, err
				}
				if response.Row == nil || response.Row.BuilderAddress == "" || response.Row.ChallengerAddress == "" {
					return nil, fmt.Errorf("dispatch challenge event authority is incomplete")
				}
				primary = response.Row
				challenges[string(challengeID)] = primary
			}
			event.Targets = append(event.Targets, eventTargets(shared.EventRole_EVENT_ROLE_BUILDER, primary.BuilderAddress)...)
			event.Targets = append(event.Targets, eventTargets(shared.EventRole_EVENT_ROLE_OPERATOR, primary.ChallengerAddress)...)
		}
		canonical, err := shared.CanonicalEventTargets(event.Targets)
		if err != nil {
			return nil, err
		}
		event.Targets = canonical
	}
	return events, nil
}
