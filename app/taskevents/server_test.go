package taskevents

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"cosmossdk.io/log"
	cmtrpc "github.com/cometbft/cometbft/rpc/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	proto "github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

type staticSource struct {
	latest uint64
	events []chainEvent
}

func eventTestSessionID() []byte { return bytes.Repeat([]byte{0x11}, 32) }
func eventTestTaskID() []byte    { return bytes.Repeat([]byte{0x22}, 32) }

func (s *staticSource) Run(context.Context, cmtrpc.Client) error { return nil }
func (s *staticSource) LatestHeight(context.Context) (uint64, error) {
	return s.latest, nil
}
func (s *staticSource) Replay(_ context.Context, _, _ uint64, after *eventPosition, filter eventFilter, send func(chainEvent) error) error {
	for _, event := range s.events {
		if after != nil && comparePosition(event.position(), *after) <= 0 {
			continue
		}
		if filter(event) {
			if err := send(event); err != nil {
				return err
			}
		}
	}
	return nil
}

type blockingReplaySource struct {
	staticSource
	entered chan struct{}
	release chan struct{}
}

type errorReplaySource struct {
	staticSource
	err error
}

func (s *errorReplaySource) Replay(context.Context, uint64, uint64, *eventPosition, eventFilter, func(chainEvent) error) error {
	return s.err
}

func (s *blockingReplaySource) Replay(context.Context, uint64, uint64, *eventPosition, eventFilter, func(chainEvent) error) error {
	close(s.entered)
	<-s.release
	return nil
}

func TestTaskEventServerStreamingRoundTrip(t *testing.T) {
	config := DefaultConfig()
	config.Enabled = true
	server := NewServer(log.NewNopLogger(), config)
	server.source = &staticSource{}
	client, cleanup := startBufconnServer(t, server)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.SubscribeTaskEvents(ctx, &tasktypes.SubscribeTaskEventsRequest{SessionId: eventTestSessionID(), TaskId: eventTestTaskID()})
	require.NoError(t, err)
	waitForSubscribers(t, server.broker, 1)
	server.broker.PublishBlock(1, []chainEvent{{
		Height: 1, Source: "BLOCK", Kind: eventKindTask, SessionID: eventTestSessionID(), TaskID: eventTestTaskID(),
		Code:    shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_WORKER_ASSIGNMENT_FINALIZED,
		Payload: &tasktypes.EventWorkerAssignmentFinalized{SessionId: eventTestSessionID(), TaskId: eventTestTaskID()},
	}})

	response, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_WORKER_ASSIGNMENT_FINALIZED, response.GetEvent().Code)
	require.Equal(t, uint64(1), response.GetEvent().ChainHeight)
}

func TestProtocolSubscriptionHasIndependentSwitch(t *testing.T) {
	config := DefaultConfig()
	config.Enabled = true
	server := NewServer(log.NewNopLogger(), config)
	server.source = &staticSource{}
	client, cleanup := startBufconnServer(t, server)
	defer cleanup()

	stream, err := client.SubscribeProtocolEvents(context.Background(), &hubtypes.SubscribeProtocolEventsRequest{})
	require.NoError(t, err)
	_, err = stream.Recv()
	require.Equal(t, codes.Unimplemented, status.Code(err))
}

func TestTaskSubscriptionRejectsNonHash32Selectors(t *testing.T) {
	config := DefaultConfig()
	config.Enabled = true
	server := NewServer(log.NewNopLogger(), config)
	server.source = &staticSource{}
	client, cleanup := startBufconnServer(t, server)
	defer cleanup()

	for _, request := range []*tasktypes.SubscribeTaskEventsRequest{
		{SessionId: []byte("short")},
		{SessionId: eventTestSessionID(), TaskId: []byte("short")},
	} {
		stream, err := client.SubscribeTaskEvents(context.Background(), request)
		require.NoError(t, err)
		_, err = stream.Recv()
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	}
}

func TestStreamTxHashUsesOptionalPresence(t *testing.T) {
	taskBlock, err := taskEventProto(chainEvent{
		Height: 1, Source: "BLOCK", Kind: eventKindTask,
		Code:    shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_SESSION_CREATED,
		Payload: &tasktypes.EventSessionCreated{},
	})
	require.NoError(t, err)
	require.Nil(t, taskBlock.GetXTxHash())

	txHash := bytes.Repeat([]byte{0x44}, 32)
	hubTx, err := protocolEventProto(chainEvent{
		Height: 1, Source: "TX", Kind: eventKindProtocol, TxHash: txHash,
		Code:    shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_SERVICE_REGISTERED,
		Payload: &hubtypes.EventServiceRegistered{},
	})
	require.NoError(t, err)
	require.Equal(t, txHash, hubTx.GetTxHash())
	require.NotNil(t, hubTx.GetXTxHash())
}

func TestTypedCodeFiltersRejectUnspecifiedAndUnknownValues(t *testing.T) {
	_, err := normalizedCodeFilter([]shared.ProtocolEventCodeV1{shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_UNSPECIFIED}, eventKindTask)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = normalizedCodeFilter([]shared.ProtocolEventCodeV1{shared.ProtocolEventCodeV1(999)}, eventKindTask)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	filter, err := normalizedCodeFilter(nil, eventKindTask)
	require.NoError(t, err)
	require.Nil(t, filter)

	filter, err = normalizedCodeFilter([]shared.ProtocolEventCodeV1{shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_COMMIT_ACCEPTED}, eventKindTask)
	require.NoError(t, err)
	require.True(t, codeAllowed(filter, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_COMMIT_ACCEPTED))
	require.False(t, codeAllowed(filter, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_RESULT_ACCEPTED))

	_, err = normalizedCodeFilter([]shared.ProtocolEventCodeV1{
		shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_SERVICE_REGISTERED,
	}, eventKindTask)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = normalizedCodeFilter([]shared.ProtocolEventCodeV1{
		shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_COMMIT_ACCEPTED,
	}, eventKindProtocol)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestNormalizeBech32AddressCanonicalizesCase(t *testing.T) {
	address := sdk.AccAddress(bytes.Repeat([]byte{9}, 20)).String()
	normalized, err := normalizeBech32Address(strings.ToUpper(address))
	require.NoError(t, err)
	require.Equal(t, address, normalized)

	normalized, err = normalizeBech32Address("  ")
	require.NoError(t, err)
	require.Empty(t, normalized)
	_, err = normalizeBech32Address("not-an-address")
	require.Error(t, err)
}

func TestReplayCursorIsExclusive(t *testing.T) {
	config := DefaultConfig()
	config.Enabled = true
	first := chainEvent{
		Height: 5, Source: "TX", Kind: eventKindTask, SessionID: eventTestSessionID(), TaskID: eventTestTaskID(),
		Code:    shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_WORKER_HANDRAISES_ACCEPTED,
		Payload: &tasktypes.EventWorkerHandraisesAccepted{SessionId: eventTestSessionID(), TaskId: eventTestTaskID()},
	}
	second := chainEvent{
		Height: 5, Source: "TX", EventIndex: 1, Kind: eventKindTask, SessionID: eventTestSessionID(), TaskID: eventTestTaskID(),
		Code:    shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_WORKER_ASSIGNMENT_FINALIZED,
		Payload: &tasktypes.EventWorkerAssignmentFinalized{SessionId: eventTestSessionID(), TaskId: eventTestTaskID()},
	}
	server := NewServer(log.NewNopLogger(), config)
	server.source = &staticSource{latest: 5, events: []chainEvent{first, second}}
	client, cleanup := startBufconnServer(t, server)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.SubscribeTaskEvents(ctx, &tasktypes.SubscribeTaskEventsRequest{SessionId: eventTestSessionID(), TaskId: eventTestTaskID(), AfterCursor: first.position().cursor()})
	require.NoError(t, err)
	response, err := stream.Recv()
	require.NoError(t, err)
	require.Equal(t, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_WORKER_ASSIGNMENT_FINALIZED, response.GetEvent().Code)
}

func TestTaskAndRoleStreamsReturnIdenticalEvent(t *testing.T) {
	config := DefaultConfig()
	config.Enabled = true
	server := NewServer(log.NewNopLogger(), config)
	server.source = &staticSource{}
	client, cleanup := startBufconnServer(t, server)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	operator := sdk.AccAddress(bytes.Repeat([]byte{1}, 20)).String()
	taskStream, err := client.SubscribeTaskEvents(ctx, &tasktypes.SubscribeTaskEventsRequest{SessionId: eventTestSessionID(), TaskId: eventTestTaskID()})
	require.NoError(t, err)
	roleStream, err := client.SubscribeRoleEvents(ctx, &tasktypes.SubscribeRoleEventsRequest{
		OperatorAddress: operator,
		TargetRole:      shared.EventRole_EVENT_ROLE_WORKER,
	})
	require.NoError(t, err)
	waitForSubscribers(t, server.broker, 2)

	payload := &tasktypes.EventWorkerAssignmentFinalized{
		SessionId: eventTestSessionID(), TaskId: eventTestTaskID(), WinnerWorker: operator,
	}
	server.broker.PublishBlock(1, []chainEvent{{
		Height: 1, Source: "BLOCK", Kind: eventKindTask, SessionID: eventTestSessionID(), TaskID: eventTestTaskID(),
		Code: shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_WORKER_ASSIGNMENT_FINALIZED,
		Targets: []*shared.EventTarget{{
			Role: shared.EventRole_EVENT_ROLE_WORKER, Address: payload.WinnerWorker,
		}},
		Payload: payload,
	}})

	taskResponse, err := taskStream.Recv()
	require.NoError(t, err)
	roleResponse, err := roleStream.Recv()
	require.NoError(t, err)
	require.True(t, proto.Equal(taskResponse.GetEvent(), roleResponse.GetEvent()))
}

func TestBuilderRoleStreamReceivesHandraiseAcceptance(t *testing.T) {
	config := DefaultConfig()
	config.Enabled = true
	server := NewServer(log.NewNopLogger(), config)
	server.source = &staticSource{}
	client, cleanup := startBufconnServer(t, server)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	builder := sdk.AccAddress(bytes.Repeat([]byte{2}, 20)).String()
	stream, err := client.SubscribeRoleEvents(ctx, &tasktypes.SubscribeRoleEventsRequest{
		OperatorAddress: builder,
		TargetRole:      shared.EventRole_EVENT_ROLE_BUILDER,
	})
	require.NoError(t, err)
	waitForSubscribers(t, server.broker, 1)

	payload := &tasktypes.EventWorkerHandraisesAccepted{
		SessionId:       eventTestSessionID(),
		TaskId:          eventTestTaskID(),
		XBuilderOrEmpty: &tasktypes.EventWorkerHandraisesAccepted_BuilderOrEmpty{BuilderOrEmpty: builder},
	}
	server.broker.PublishBlock(1, []chainEvent{{
		Height: 1, Source: "BLOCK", Kind: eventKindTask, SessionID: eventTestSessionID(), TaskID: eventTestTaskID(),
		Code: shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_WORKER_HANDRAISES_ACCEPTED,
		Targets: []*shared.EventTarget{{
			Role: shared.EventRole_EVENT_ROLE_BUILDER, Address: builder,
		}},
		Payload: payload,
	}})

	response, err := stream.Recv()
	require.NoError(t, err)
	require.True(t, proto.Equal(payload, response.GetEvent().GetPayload().GetWorkerHandraisesAccepted()))
}

func TestFilteredStreamReceivesBlockCheckpoint(t *testing.T) {
	config := DefaultConfig()
	config.Enabled = true
	server := NewServer(log.NewNopLogger(), config)
	server.source = &staticSource{}
	client, cleanup := startBufconnServer(t, server)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stream, err := client.SubscribeTaskEvents(ctx, &tasktypes.SubscribeTaskEventsRequest{SessionId: eventTestSessionID()})
	require.NoError(t, err)
	waitForSubscribers(t, server.broker, 1)
	server.broker.PublishBlock(7, nil)

	response, err := stream.Recv()
	require.NoError(t, err)
	require.Nil(t, response.GetEvent())
	require.Equal(t, uint64(7), response.GetCheckpoint().ChainHeight)
	require.Equal(t, blockCheckpoint(7).position().cursor(), response.GetCheckpoint().Cursor)
}

func TestReplayConcurrencyLimitFailsFast(t *testing.T) {
	config := DefaultConfig()
	config.Enabled = true
	config.MaxConcurrentReplays = 1
	server := NewServer(log.NewNopLogger(), config)
	source := &blockingReplaySource{entered: make(chan struct{}), release: make(chan struct{})}
	server.source = source
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- server.sendReplay(context.Background(), 1, 1, nil, func(chainEvent) bool { return true }, func(chainEvent) error { return nil })
	}()
	<-source.entered

	err := server.sendReplay(context.Background(), 1, 1, nil, func(chainEvent) bool { return true }, func(chainEvent) error { return nil })
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	close(source.release)
	require.NoError(t, <-firstDone)
}

func TestReplayErrorsUseActionableGRPCCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code codes.Code
	}{
		{name: "cancelled", err: context.Canceled, code: codes.Canceled},
		{name: "deadline", err: context.DeadlineExceeded, code: codes.DeadlineExceeded},
		{name: "range", err: ErrReplayRangeExceeded, code: codes.OutOfRange},
		{name: "upstream", err: errors.New("rpc unavailable"), code: codes.Unavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := DefaultConfig()
			config.Enabled = true
			server := NewServer(log.NewNopLogger(), config)
			server.source = &errorReplaySource{err: test.err}
			err := server.sendReplay(context.Background(), 1, 1, nil, func(chainEvent) bool { return true }, func(chainEvent) error { return nil })
			require.Equal(t, test.code, status.Code(err))
		})
	}
}

type eventServiceClients struct {
	tasktypes.TaskEventServiceClient
	hubtypes.HubEventServiceClient
}

func startBufconnServer(t *testing.T, taskServer *Server) (eventServiceClients, func()) {
	t.Helper()
	// The stream messages carry repeated shared.v1.ProtocolEventCodeV1, a
	// cross-file gogoproto enum. The stock google.golang.org/protobuf codec
	// resolves it to a value-less placeholder and panics, so the harness must
	// force the same gogoproto codec the SDK gRPC server installs in
	// production (server/grpc uses grpc.ForceServerCodec with this codec).
	gogoCodec := codec.NewProtoCodec(codectypes.NewInterfaceRegistry()).GRPCCodec()
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer(grpc.ForceServerCodec(gogoCodec))
	tasktypes.RegisterTaskEventServiceServer(grpcServer, taskServer)
	hubtypes.RegisterHubEventServiceServer(grpcServer, taskServer)
	go func() { _ = grpcServer.Serve(listener) }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	connection, err := grpc.DialContext(ctx, "bufnet", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}), grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(gogoCodec)))
	cancel()
	require.NoError(t, err)
	return eventServiceClients{
			TaskEventServiceClient: tasktypes.NewTaskEventServiceClient(connection),
			HubEventServiceClient:  hubtypes.NewHubEventServiceClient(connection),
		}, func() {
			connection.Close()
			grpcServer.Stop()
			listener.Close()
		}
}

func waitForSubscribers(t *testing.T, broker *broker, expected int) {
	t.Helper()
	require.Eventually(t, func() bool {
		broker.mu.Lock()
		defer broker.mu.Unlock()
		return len(broker.subscribers) == expected
	}, time.Second, 10*time.Millisecond)
}
