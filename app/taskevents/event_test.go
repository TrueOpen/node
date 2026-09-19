package taskevents

import (
	"fmt"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	cmttypes "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	proto "github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func TestEventPositionCursorRoundTripAndOrder(t *testing.T) {
	position := eventPosition{Height: 42, SourceOrder: 1, TxIndex: 7, EventIndex: 9}
	parsed, err := parseCursor(position.cursor())
	require.NoError(t, err)
	require.Equal(t, position, parsed)
	require.Equal(t, -1, comparePosition(eventPosition{Height: 42, EventIndex: 1}, eventPosition{Height: 42, EventIndex: 2}))
	require.Equal(t, 1, comparePosition(eventPosition{Height: 43}, position))

	_, err = parseCursor("v2:42:1:7:9")
	require.Error(t, err)
	_, err = parseCursor("v1:0:0:0:0")
	require.ErrorContains(t, err, "height")
}

func TestEventsFromBlockParsesCommittedTypedEvents(t *testing.T) {
	tx := cmttypes.Tx("signed transaction")
	assignment := &tasktypes.EventWorkerAssignmentFinalized{
		SessionId: []byte("session"), TaskId: []byte("task"), WinnerWorker: "worker",
	}
	timeout := &tasktypes.EventWorkerTimeout{SessionId: []byte("session"), TaskId: []byte("task")}
	failed := &tasktypes.EventAssignmentFailed{SessionId: []byte("session"), TaskId: []byte("failed-task")}
	events := eventsFromBlock(blockData{
		Height:    12,
		BlockHash: []byte("ABC"),
		Txs:       cmttypes.Txs{tx, cmttypes.Tx("failed")},
		TxResults: []*abci.ExecTxResult{
			{Code: 0, Events: []abci.Event{typedABCIEvent(t, assignment)}},
			{Code: 9, Events: []abci.Event{typedABCIEvent(t, failed)}},
		},
		FinalizeBlockEvents: []abci.Event{typedABCIEvent(t, timeout)},
	})

	require.Len(t, events, 2)
	require.Equal(t, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_WORKER_ASSIGNMENT_FINALIZED, events[0].Code)
	require.Equal(t, "TX", events[0].Source)
	require.NotEmpty(t, events[0].TxHash)
	require.Equal(t, []*shared.EventTarget{{Role: shared.EventRole_EVENT_ROLE_WORKER, Address: "worker"}}, events[0].Targets)
	require.True(t, proto.Equal(assignment, events[0].Payload))
	require.Equal(t, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_WORKER_TIMEOUT, events[1].Code)
	require.Equal(t, "BLOCK", events[1].Source)
}

func TestMalformedOrUnsupportedEventIsSkipped(t *testing.T) {
	malformed := abci.Event{
		Type: proto.MessageName(&tasktypes.EventCommitAccepted{}),
		Attributes: []abci.EventAttribute{
			{Key: "session_id", Value: "not-json"},
		},
	}
	_, ok := normalizeEvent(malformed)
	require.False(t, ok)

	_, ok = normalizeEvent(abci.Event{Type: "cosmos.bank.v1.EventUnknownToTrueOpen"})
	require.False(t, ok)
}

func TestMalformedTrueOpenEventIsObservableButUnrelatedEventIsIgnored(t *testing.T) {
	malformedType := proto.MessageName(&tasktypes.EventCommitAccepted{})
	skipped := make([]string, 0)
	events := eventsFromBlockObserved(blockData{
		Height: 1,
		TxResults: []*abci.ExecTxResult{{
			Events: []abci.Event{
				{Type: malformedType, Attributes: []abci.EventAttribute{{Key: "session_id", Value: "not-json"}}},
				{Type: "cosmos.bank.v1.EventUnknownToTrueOpen"},
			},
		}},
	}, func(eventType string, source string, txIndex uint32, eventIndex uint32) {
		skipped = append(skipped, fmt.Sprintf("%s/%s/%d/%d", eventType, source, txIndex, eventIndex))
	})
	require.Empty(t, events)
	require.Equal(t, []string{malformedType + "/TX/0/0"}, skipped)
}

func TestNormalizeProtocolEventUsesTypedTarget(t *testing.T) {
	want := &hubtypes.EventModelSupportUpdated{Operator: "operator", ModelId: "model", ProfileVersion: 1, SupportVersion: 2}
	event, ok := normalizeEvent(typedABCIEvent(t, want))
	require.True(t, ok)
	require.Equal(t, eventKindProtocol, event.Kind)
	require.Equal(t, shared.ProtocolEventCodeV1_PROTOCOL_EVENT_CODE_V1_MODEL_SUPPORT_UPDATED, event.Code)
	require.Equal(t, []*shared.EventTarget{{Role: shared.EventRole_EVENT_ROLE_OPERATOR, Address: "operator"}}, event.Targets)
	require.True(t, proto.Equal(want, event.Payload))
}

func TestNormalizeEventRemovesOnlyCosmosBlockModeMarker(t *testing.T) {
	events := []proto.Message{
		&hubtypes.EventCandidatePoolPublished{
			Epoch: 1, SnapshotId: make([]byte, 32), PoolHash: make([]byte, 32), ActiveCount: 4,
			EffectiveHeight: 5, ExpiresHeight: 10,
		},
		&tasktypes.EventWorkerAssignmentFinalized{
			SessionId: make([]byte, 32), TaskId: make([]byte, 32), WinnerWorker: "worker",
			AssignmentCandidateSetHash: make([]byte, 32), RandomnessHeight: 5,
			ReservedAmount: shared.NewAmount(10), InferDeadline: 20,
		},
		&tasktypes.EventVerifierAssignmentFinalized{
			SessionId: make([]byte, 32), TaskId: make([]byte, 32), VerifyRound: 1,
			SelectedVerifiersHash: make([]byte, 32), VerifierLegalSetHash: make([]byte, 32),
			SelectionRandomnessHeight: 5, CommitDeadline: 10, VerifyDeadline: 20,
		},
		&tasktypes.EventDeadlineSwept{
			XSessionId:     &tasktypes.EventDeadlineSwept_SessionId{SessionId: make([]byte, 32)},
			XTaskId:        &tasktypes.EventDeadlineSwept_TaskId{TaskId: make([]byte, 32)},
			DeadlineKind:   tasktypes.DeadlineKindV1_DEADLINE_KIND_V1_CHALLENGE_WINDOW_CLOSE,
			TransitionCode: tasktypes.DeadlineTransitionCode_DEADLINE_TRANSITION_CODE_CHALLENGE_WINDOW_CLOSED,
		},
	}
	for _, payload := range events {
		for _, mode := range []string{"BeginBlock", "EndBlock"} {
			event := typedABCIEvent(t, payload)
			event.Attributes = append(event.Attributes, abci.EventAttribute{Key: "mode", Value: mode})
			normalized, ok := normalizeEvent(event)
			require.Truef(t, ok, "%T was dropped after Cosmos %s decoration", payload, mode)
			require.True(t, proto.Equal(payload, normalized.Payload))
		}
	}

	for _, attribute := range []abci.EventAttribute{{Key: "mode", Value: "Other"}, {Key: "other", Value: "EndBlock"}} {
		malformed := typedABCIEvent(t, &tasktypes.EventWorkerTimeout{})
		malformed.Attributes = append(malformed.Attributes, attribute)
		_, ok := normalizeEvent(malformed)
		require.False(t, ok, "an unknown non-framework attribute must remain rejected")
	}
}

func typedABCIEvent(t *testing.T, payload proto.Message) abci.Event {
	t.Helper()
	event, err := sdk.TypedEventToEvent(payload)
	require.NoError(t, err)
	return abci.Event{Type: event.Type, Attributes: event.Attributes}
}
