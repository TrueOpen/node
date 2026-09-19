package taskevents

import (
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	proto "github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func TestProtocolEventCodeRegistryMatchesFrozenCatalog(t *testing.T) {
	// Wire v0.4.1 registry: 18, 70 and 106-108 are unallocated; 21 is
	// REWARD_EPOCH_CLOSED, and 26-29 / 113 / 120-127 are allocated.
	want := make([]int, 0, 67)
	for code := 1; code <= 29; code++ {
		if code == 18 {
			continue
		}
		want = append(want, code)
	}
	want = append(want, 34, 40, 41, 50, 51, 52, 53, 60, 80, 81)
	for code := 90; code <= 105; code++ {
		want = append(want, code)
	}
	for code := 109; code <= 113; code++ {
		want = append(want, code)
	}
	for code := 120; code <= 127; code++ {
		want = append(want, code)
	}

	got := make([]int, 0, len(shared.ProtocolEventCodeV1_name)-1)
	for code := range shared.ProtocolEventCodeV1_name {
		if code != 0 {
			got = append(got, int(code))
		}
	}
	sort.Ints(got)
	require.Equal(t, want, got)
}

// TestProtocolEventCodeRegistryIsFullyCovered pins the §5.11 invariant that
// shared.v1.ProtocolEventCodeV1 is the single event code registry and that
// the two typed payload oneofs partition it exactly: every registered code has
// exactly one payload, in exactly one of the two module-owned oneofs, and the
// oneof field number equals the code value.
func TestProtocolEventCodeRegistryIsFullyCovered(t *testing.T) {
	taskWrappers := (&tasktypes.TaskProtocolEventPayloadV1{}).XXX_OneofWrappers()
	hubWrappers := (&hubtypes.ProtocolEventPayloadV1{}).XXX_OneofWrappers()
	// -1 drops PROTOCOL_EVENT_CODE_V1_UNSPECIFIED, which has no payload.
	require.Len(t, append(append([]interface{}{}, taskWrappers...), hubWrappers...), len(shared.ProtocolEventCodeV1_name)-1)

	seen := make(map[shared.ProtocolEventCodeV1]struct{}, len(taskWrappers)+len(hubWrappers))

	for _, wrapper := range taskWrappers {
		payload, fieldName, fieldNumber := payloadForOneofWrapper(t, wrapper)
		event, ok := classifyTypedEvent(payload)
		require.Truef(t, ok, "catalog is missing %T", payload)
		require.Equal(t, eventKindTask, event.Kind)
		require.Equal(t, eventKindTask, eventCodeOwner(event.Code))
		assertCodeMatchesOneofField(t, event.Code, fieldName, fieldNumber)
		_, duplicate := seen[event.Code]
		require.Falsef(t, duplicate, "duplicate event code %s", event.Code)
		seen[event.Code] = struct{}{}

		envelope := &tasktypes.TaskEvent{}
		require.Truef(t, setTaskEventPayload(envelope, payload), "oneof mapping is missing %T", payload)
		require.Equal(t, reflect.TypeOf(wrapper), reflect.TypeOf(envelope.Payload.TypedEvent))
		assertTypedEventRoundTrip(t, payload)
		assertNarrowPublicEvent(t, payload)
	}

	for _, wrapper := range hubWrappers {
		payload, fieldName, fieldNumber := payloadForOneofWrapper(t, wrapper)
		event, ok := classifyTypedEvent(payload)
		require.Truef(t, ok, "catalog is missing %T", payload)
		require.Equal(t, eventKindProtocol, event.Kind)
		require.Equal(t, eventKindProtocol, eventCodeOwner(event.Code))
		assertCodeMatchesOneofField(t, event.Code, fieldName, fieldNumber)
		_, duplicate := seen[event.Code]
		require.Falsef(t, duplicate, "duplicate event code %s", event.Code)
		seen[event.Code] = struct{}{}

		envelope := &hubtypes.ProtocolEvent{}
		require.Truef(t, setProtocolEventPayload(envelope, payload), "oneof mapping is missing %T", payload)
		require.Equal(t, reflect.TypeOf(wrapper), reflect.TypeOf(envelope.Payload.TypedEvent))
		assertTypedEventRoundTrip(t, payload)
		assertNarrowPublicEvent(t, payload)
	}
}

func TestSessionCreatedTargetsOwnerAccount(t *testing.T) {
	event, ok := classifyTypedEvent(&tasktypes.EventSessionCreated{
		SessionId: []byte("session-owner"),
		Owner:     "trueopen1owner",
		Nonce:     1,
	})
	require.True(t, ok)
	require.Equal(t, eventKindTask, event.Kind)
	require.Equal(t, []byte("session-owner"), event.SessionID)
	require.Equal(t, []*shared.EventTarget{{
		Role:    shared.EventRole_EVENT_ROLE_USER,
		Address: "trueopen1owner",
	}}, event.Targets)
}

func TestWorkerAssignmentFinalizedTargetsWinnerWorker(t *testing.T) {
	payload := &tasktypes.EventWorkerAssignmentFinalized{
		SessionId:    []byte("session-assign"),
		TaskId:       []byte("task-assign"),
		WinnerWorker: "trueopen1worker",
	}
	event, ok := classifyTypedEvent(payload)
	require.True(t, ok)
	require.Equal(t, []byte("task-assign"), event.TaskID)
	require.Equal(t, []*shared.EventTarget{
		{Role: shared.EventRole_EVENT_ROLE_WORKER, Address: "trueopen1worker"},
	}, event.Targets)
}

func TestUnregisteredDutyDoesNotCreateMisroutedTarget(t *testing.T) {
	require.Equal(t, shared.EventRole_EVENT_ROLE_UNSPECIFIED, dutyEventRole(shared.Duty_DUTY_UNSPECIFIED))
	require.Empty(t, eventTargets(dutyEventRole(shared.Duty_DUTY_UNSPECIFIED), "operator-address"))
	require.Equal(t, shared.EventRole_EVENT_ROLE_WORKER, dutyEventRole(shared.Duty_DUTY_WORKER))
	require.Equal(t, shared.EventRole_EVENT_ROLE_VERIFIER, dutyEventRole(shared.Duty_DUTY_VERIFIER))
}

// assertCodeMatchesOneofField checks both halves of the §5.11 payload
// contract: the oneof field name spells the code, and the field number equals
// the code value.
func assertCodeMatchesOneofField(t *testing.T, code shared.ProtocolEventCodeV1, fieldName string, fieldNumber int32) {
	t.Helper()
	require.Equal(t, "PROTOCOL_EVENT_CODE_V1_"+strings.ToUpper(fieldName), shared.ProtocolEventCodeV1_name[int32(code)])
	require.Equalf(t, fieldNumber, int32(code), "oneof field %s number must equal its event code", fieldName)
}

func payloadForOneofWrapper(t *testing.T, wrapper interface{}) (proto.Message, string, int32) {
	t.Helper()
	wrapperType := reflect.TypeOf(wrapper)
	require.Equal(t, reflect.Ptr, wrapperType.Kind())
	wrapperStruct := wrapperType.Elem()
	require.Equal(t, 1, wrapperStruct.NumField())
	payloadField := wrapperStruct.Field(0)
	require.Equal(t, reflect.Ptr, payloadField.Type.Kind())

	payloadValue := reflect.New(payloadField.Type.Elem())
	payload, ok := payloadValue.Interface().(proto.Message)
	require.True(t, ok)
	name, number := protobufFieldNameAndNumber(t, payloadField.Tag.Get("protobuf"))
	return payload, name, number
}

func protobufFieldNameAndNumber(t *testing.T, tag string) (string, int32) {
	t.Helper()
	name := ""
	number := int32(0)
	for i, part := range strings.Split(tag, ",") {
		if value, ok := strings.CutPrefix(part, "name="); ok {
			name = value
		}
		if i == 1 {
			parsed, err := strconv.ParseInt(part, 10, 32)
			require.NoErrorf(t, err, "protobuf tag %q has no field number", tag)
			number = int32(parsed)
		}
	}
	require.NotEmptyf(t, name, "protobuf tag %q has no field name", tag)
	require.NotZerof(t, number, "protobuf tag %q has no field number", tag)
	return name, number
}

func assertTypedEventRoundTrip(t *testing.T, want proto.Message) {
	t.Helper()
	event, err := sdk.TypedEventToEvent(want)
	require.NoError(t, err)
	got, err := sdk.ParseTypedEvent(abci.Event{Type: event.Type, Attributes: event.Attributes})
	require.NoError(t, err)
	require.Truef(t, proto.Equal(want, got), "typed roundtrip mismatch for %T", want)
}

func assertNarrowPublicEvent(t *testing.T, event proto.Message) {
	t.Helper()
	typeOf := reflect.TypeOf(event).Elem()
	protobufFieldCount := 0
	forbidden := []string{"OrderEnvelope", "Signature", "Payments", "Histogram", "SupportCandidates", "RawPayload", "Attributes"}
	for i := 0; i < typeOf.NumField(); i++ {
		if typeOf.Field(i).Tag.Get("protobuf") == "" && typeOf.Field(i).Tag.Get("protobuf_oneof") == "" {
			continue
		}
		protobufFieldCount++
		for _, name := range forbidden {
			require.NotEqualf(t, name, typeOf.Field(i).Name, "%s exposes forbidden field %s", proto.MessageName(event), name)
		}
	}
	require.LessOrEqualf(t, protobufFieldCount, 16, "%s exceeds the public event field budget", proto.MessageName(event))
}
