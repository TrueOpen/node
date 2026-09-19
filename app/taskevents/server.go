package taskevents

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"cosmossdk.io/log"
	cmtrpc "github.com/cometbft/cometbft/rpc/client"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

type Server struct {
	tasktypes.UnimplementedTaskEventServiceServer
	hubtypes.UnimplementedHubEventServiceServer

	config      Config
	broker      *broker
	source      eventSource
	replaySlots chan struct{}

	mu     sync.Mutex
	cancel context.CancelFunc
}

type eventSource interface {
	Run(context.Context, cmtrpc.Client) error
	Replay(context.Context, uint64, uint64, *eventPosition, eventFilter, func(chainEvent) error) error
	LatestHeight(context.Context) (uint64, error)
}

func NewServer(logger log.Logger, config Config) *Server {
	broker := newBroker(config.MaxSubscribers, config.SubscriberBufferSize)
	return &Server{
		config:      config,
		broker:      broker,
		source:      newCometSource(logger.With("module", "task-event-grpc"), config.MaxReplayBlocks, broker),
		replaySlots: make(chan struct{}, config.MaxConcurrentReplays),
	}
}

// Run binds the event source and all server streams to the node start context.
func (s *Server) Run(ctx context.Context, client cmtrpc.Client) error {
	runCtx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		cancel()
		return fmt.Errorf("task event service is already running")
	}
	s.cancel = cancel
	s.mu.Unlock()
	defer s.broker.Close()
	return s.source.Run(runCtx, client)
}

func (s *Server) Close() {
	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	s.broker.Close()
}

func (s *Server) SubscribeTaskEvents(req *tasktypes.SubscribeTaskEventsRequest, stream tasktypes.TaskEventService_SubscribeTaskEventsServer) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "request is required")
	}
	sessionID := req.SessionId
	taskID := req.TaskId
	if len(sessionID) != 32 {
		return status.Error(codes.InvalidArgument, "session_id must be a raw 32-byte Hash32")
	}
	if len(taskID) != 0 && len(taskID) != 32 {
		return status.Error(codes.InvalidArgument, "task_id must be absent or a raw 32-byte Hash32")
	}
	codesFilter, err := normalizedCodeFilter(req.Codes, eventKindTask)
	if err != nil {
		return err
	}
	filter := func(event chainEvent) bool {
		return event.Kind == eventKindTask &&
			bytes.Equal(event.SessionID, sessionID) &&
			(len(taskID) == 0 || bytes.Equal(event.TaskID, taskID)) &&
			codeAllowed(codesFilter, event.Code)
	}
	return s.stream(stream.Context(), req.AfterCursor, req.FromHeight, filter, func(event chainEvent) error {
		if event.Kind == eventKindCheckpoint {
			return stream.Send(&tasktypes.SubscribeTaskEventsResponse{Item: &tasktypes.SubscribeTaskEventsResponse_Checkpoint{Checkpoint: streamCheckpointProto(event)}})
		}
		value, err := taskEventProto(event)
		if err != nil {
			return status.Error(codes.Internal, err.Error())
		}
		return stream.Send(&tasktypes.SubscribeTaskEventsResponse{Item: &tasktypes.SubscribeTaskEventsResponse_Event{Event: value}})
	})
}

func (s *Server) SubscribeRoleEvents(req *tasktypes.SubscribeRoleEventsRequest, stream tasktypes.TaskEventService_SubscribeRoleEventsServer) error {
	if req == nil {
		return status.Error(codes.InvalidArgument, "request is required")
	}
	operator := strings.TrimSpace(req.OperatorAddress)
	operatorAddress, err := sdk.AccAddressFromBech32(operator)
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "invalid operator_address: %v", err)
	}
	operator = operatorAddress.String()
	role := req.TargetRole
	if role != shared.EventRole_EVENT_ROLE_WORKER && role != shared.EventRole_EVENT_ROLE_VERIFIER && role != shared.EventRole_EVENT_ROLE_BUILDER {
		return status.Error(codes.InvalidArgument, "target_role must be EVENT_ROLE_WORKER, EVENT_ROLE_VERIFIER, or EVENT_ROLE_BUILDER")
	}
	codesFilter, err := normalizedCodeFilter(req.Codes, eventKindTask)
	if err != nil {
		return err
	}
	filter := func(event chainEvent) bool {
		return event.Kind == eventKindTask && containsTarget(event.Targets, operator, role) && codeAllowed(codesFilter, event.Code)
	}
	return s.stream(stream.Context(), req.AfterCursor, req.FromHeight, filter, func(event chainEvent) error {
		if event.Kind == eventKindCheckpoint {
			return stream.Send(&tasktypes.SubscribeRoleEventsResponse{Item: &tasktypes.SubscribeRoleEventsResponse_Checkpoint{Checkpoint: streamCheckpointProto(event)}})
		}
		value, err := taskEventProto(event)
		if err != nil {
			return status.Error(codes.Internal, err.Error())
		}
		return stream.Send(&tasktypes.SubscribeRoleEventsResponse{Item: &tasktypes.SubscribeRoleEventsResponse_Event{Event: value}})
	})
}

func (s *Server) SubscribeProtocolEvents(req *hubtypes.SubscribeProtocolEventsRequest, stream hubtypes.HubEventService_SubscribeProtocolEventsServer) error {
	if !s.config.ProtocolEventsEnabled {
		return status.Error(codes.Unimplemented, "protocol event subscriptions are disabled")
	}
	if req == nil {
		return status.Error(codes.InvalidArgument, "request is required")
	}
	targetAddress, err := normalizeBech32Address(req.TargetAddress)
	if err != nil {
		return status.Error(codes.InvalidArgument, "invalid target_address")
	}
	codesFilter, err := normalizedCodeFilter(req.Codes, eventKindProtocol)
	if err != nil {
		return err
	}
	filter := func(event chainEvent) bool {
		return event.Kind == eventKindProtocol && (targetAddress == "" || containsTargetAddress(event.Targets, targetAddress)) && codeAllowed(codesFilter, event.Code)
	}
	return s.stream(stream.Context(), req.AfterCursor, req.FromHeight, filter, func(event chainEvent) error {
		if event.Kind == eventKindCheckpoint {
			return stream.Send(&hubtypes.SubscribeProtocolEventsResponse{Item: &hubtypes.SubscribeProtocolEventsResponse_Checkpoint{Checkpoint: streamCheckpointProto(event)}})
		}
		value, err := protocolEventProto(event)
		if err != nil {
			return status.Error(codes.Internal, err.Error())
		}
		return stream.Send(&hubtypes.SubscribeProtocolEventsResponse{Item: &hubtypes.SubscribeProtocolEventsResponse_Event{Event: value}})
	})
}

func normalizeBech32Address(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	hrP, addressBytes, err := bech32.DecodeAndConvert(value)
	if err != nil || len(addressBytes) == 0 {
		return "", fmt.Errorf("invalid bech32 address")
	}
	normalized, err := bech32.ConvertAndEncode(hrP, addressBytes)
	if err != nil {
		return "", fmt.Errorf("normalize bech32 address: %w", err)
	}
	return normalized, nil
}

func (s *Server) stream(ctx context.Context, cursor string, fromHeight uint64, filter eventFilter, send func(chainEvent) error) error {
	after, err := validateReplayStart(cursor, fromHeight)
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	subscriber, _, err := s.broker.Subscribe(filter)
	if err != nil {
		if errors.Is(err, ErrMaxSubscribers) {
			return status.Error(codes.ResourceExhausted, err.Error())
		}
		if errors.Is(err, ErrBrokerClosed) {
			return status.Error(codes.Unavailable, err.Error())
		}
		return status.Error(codes.Internal, err.Error())
	}
	defer s.broker.Unsubscribe(subscriber)

	latestHeight, err := s.source.LatestHeight(ctx)
	if err != nil {
		return status.Errorf(codes.Unavailable, "task event source unavailable: %v", err)
	}
	if after != nil && after.Height > latestHeight {
		return status.Error(codes.OutOfRange, "after_cursor is ahead of the latest committed height")
	}
	if fromHeight > 0 && fromHeight <= latestHeight {
		if err := s.sendReplay(ctx, fromHeight, latestHeight, after, filter, send); err != nil {
			return err
		}
	} else if after != nil {
		if err := s.sendReplay(ctx, after.Height, latestHeight, after, filter, send); err != nil {
			return err
		}
	}

	// The subscriber was installed before latestHeight was read. Discard queued
	// events through that boundary because replay already covered them (or this
	// is a live-only subscription starting after the observed head).
	boundary := eventPosition{Height: latestHeight, SourceOrder: 1, TxIndex: ^uint32(0), EventIndex: ^uint32(0)}
	minimumLiveHeight := fromHeight
	for {
		select {
		case <-ctx.Done():
			return nil
		case terminalErr := <-subscriber.terminal:
			if errors.Is(terminalErr, ErrSlowSubscriber) {
				return status.Error(codes.ResourceExhausted, terminalErr.Error())
			}
			if terminalErr != nil && !errors.Is(terminalErr, context.Canceled) {
				return status.Error(codes.Unavailable, terminalErr.Error())
			}
			return nil
		case event := <-subscriber.events:
			if comparePosition(event.position(), boundary) <= 0 || (minimumLiveHeight > 0 && event.Height < minimumLiveHeight) {
				continue
			}
			if err := send(event); err != nil {
				return err
			}
		}
	}
}

func (s *Server) sendReplay(ctx context.Context, fromHeight, toHeight uint64, after *eventPosition, filter eventFilter, send func(chainEvent) error) error {
	select {
	case s.replaySlots <- struct{}{}:
		defer func() { <-s.replaySlots }()
	default:
		return status.Error(codes.ResourceExhausted, "task event replay concurrency limit reached")
	}

	err := s.source.Replay(ctx, fromHeight, toHeight, after, filter, send)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return status.Error(codes.Canceled, err.Error())
		case errors.Is(err, context.DeadlineExceeded):
			return status.Error(codes.DeadlineExceeded, err.Error())
		case errors.Is(err, ErrReplayRangeExceeded):
			return status.Error(codes.OutOfRange, err.Error())
		default:
			if status.Code(err) != codes.Unknown {
				return err
			}
			return status.Errorf(codes.Unavailable, "replay task events: %v", err)
		}
	}
	return nil
}

func validateReplayStart(cursor string, fromHeight uint64) (*eventPosition, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor != "" && fromHeight != 0 {
		return nil, fmt.Errorf("after_cursor and from_height are mutually exclusive")
	}
	if cursor == "" {
		return nil, nil
	}
	position, err := parseCursor(cursor)
	if err != nil {
		return nil, err
	}
	return &position, nil
}

// normalizedCodeFilter validates a subscription filter against the single
// §5.11 code registry. The two local enums that used to split task and
// protocol codes are gone, so one validator serves all three streams.
func normalizedCodeFilter(values []shared.ProtocolEventCodeV1, owner eventKind) (map[shared.ProtocolEventCodeV1]struct{}, error) {
	if len(values) > 64 {
		return nil, status.Error(codes.InvalidArgument, "codes contains more than 64 values")
	}
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[shared.ProtocolEventCodeV1]struct{}, len(values))
	for _, value := range values {
		if value == shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_UNSPECIFIED {
			return nil, status.Error(codes.InvalidArgument, "codes must not contain PROTOCOL_EVENT_CODE_V1_UNSPECIFIED")
		}
		if _, ok := shared.ProtocolEventCodeV1_name[int32(value)]; !ok {
			return nil, status.Errorf(codes.InvalidArgument, "unknown protocol event code %d", value)
		}
		if eventCodeOwner(value) != owner {
			return nil, status.Errorf(codes.InvalidArgument, "protocol event code %s does not belong to this stream", value)
		}
		out[value] = struct{}{}
	}
	return out, nil
}

func eventCodeOwner(code shared.ProtocolEventCodeV1) eventKind {
	switch code {
	case shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_SESSION_CREATED,
		shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_ORDER_CANCELLED,
		shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_REVEAL_PHASE_STARTED,
		shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_TASK_FAILURE_CLASS_UPDATED,
		shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_WORKER_HANDRAISES_ACCEPTED,
		shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_WORKER_ASSIGNMENT_FINALIZED,
		shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_INFER_RECEIPT_ACCEPTED,
		shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_VERIFIER_ASSIGNMENT_FINALIZED,
		shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_DATA_UNAVAILABLE_REPORTED,
		shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_BUILDER_DATA_UNAVAILABLE,
		shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_COMMIT_ACCEPTED,
		shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_RESULT_ACCEPTED,
		shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_TASK_SETTLED,
		shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_DEADLINE_SWEPT,
		shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_ASSIGNMENT_FAILED,
		shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_WORKER_TIMEOUT,
		shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_VERIFY_OPEN_TIMEOUT,
		shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_COMMIT_DEADLINE_CLOSED,
		shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_VERIFIER_HANDRAISES_ACCEPTED,
		shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_TASK_PARAMS_UPDATED,
		// Wire v0.3 round payloads; all three live in the Task oneof.
		shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_CHALLENGE_ROUND_OPENED,
		shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_VERIFICATION_ROUND_CLOSED,
		shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_TASK_GAS_REIMBURSEMENT_RECORDED,
		shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_WORKER_EVIDENCE_ACCEPTED:
		return eventKindTask
	default:
		if _, ok := shared.ProtocolEventCodeV1_name[int32(code)]; ok &&
			code != shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_UNSPECIFIED {
			return eventKindProtocol
		}
		return 0
	}
}

func codeAllowed(filter map[shared.ProtocolEventCodeV1]struct{}, code shared.ProtocolEventCodeV1) bool {
	if len(filter) == 0 {
		return true
	}
	_, ok := filter[code]
	return ok
}

func containsTarget(targets []*shared.EventTarget, address string, role shared.EventRole) bool {
	for _, target := range targets {
		if target != nil && target.Address == address && target.Role == role {
			return true
		}
	}
	return false
}

func containsTargetAddress(targets []*shared.EventTarget, address string) bool {
	for _, target := range targets {
		if target != nil && target.Address == address {
			return true
		}
	}
	return false
}

func taskEventProto(event chainEvent) (*tasktypes.TaskEvent, error) {
	value := &tasktypes.TaskEvent{
		Cursor:      event.position().cursor(),
		ChainHeight: event.Height,
		BlockHash:   event.BlockHash,
		TxIndex:     event.TxIndex,
		EventIndex:  event.EventIndex,
		Source:      eventSourceProto(event.Source),
		SessionId:   event.SessionID,
		TaskId:      event.TaskID,
		Code:        event.Code,
		Targets:     cloneTargets(event.Targets),
	}
	if len(event.TxHash) != 0 {
		value.XTxHash = &tasktypes.TaskEvent_TxHash{TxHash: append([]byte(nil), event.TxHash...)}
	}
	if !setTaskEventPayload(value, event.Payload) {
		return nil, fmt.Errorf("unsupported task event payload %T", event.Payload)
	}
	return value, nil
}

func protocolEventProto(event chainEvent) (*hubtypes.ProtocolEvent, error) {
	value := &hubtypes.ProtocolEvent{
		Cursor:      event.position().cursor(),
		ChainHeight: event.Height,
		BlockHash:   event.BlockHash,
		TxIndex:     event.TxIndex,
		EventIndex:  event.EventIndex,
		Source:      eventSourceProto(event.Source),
		Code:        event.Code,
		Targets:     cloneTargets(event.Targets),
	}
	if len(event.TxHash) != 0 {
		value.XTxHash = &hubtypes.ProtocolEvent_TxHash{TxHash: append([]byte(nil), event.TxHash...)}
	}
	if !setProtocolEventPayload(value, event.Payload) {
		return nil, fmt.Errorf("unsupported protocol event payload %T", event.Payload)
	}
	return value, nil
}

func cloneTargets(targets []*shared.EventTarget) []*shared.EventTarget {
	out := make([]*shared.EventTarget, 0, len(targets))
	for _, target := range targets {
		if target != nil {
			out = append(out, &shared.EventTarget{Role: target.Role, Address: target.Address})
		}
	}
	return out
}

func eventSourceProto(source string) shared.TaskEventSource {
	if source == "TX" {
		return shared.TaskEventSource_TASK_EVENT_SOURCE_TX
	}
	return shared.TaskEventSource_TASK_EVENT_SOURCE_FINALIZE_BLOCK
}

func streamCheckpointProto(event chainEvent) *shared.StreamCheckpoint {
	return &shared.StreamCheckpoint{
		Cursor:      event.position().cursor(),
		ChainHeight: event.Height,
	}
}
