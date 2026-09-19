package taskevents

import (
	"fmt"
	"strconv"
	"strings"

	abci "github.com/cometbft/cometbft/abci/types"
	tmhash "github.com/cometbft/cometbft/crypto/tmhash"
	cmttypes "github.com/cometbft/cometbft/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	proto "github.com/cosmos/gogoproto/proto"

	shared "github.com/TrueOpen/node/x/shared/types"
)

type eventKind uint8

const (
	// eventKindTask carries a §5.11 code whose typed payload lives in the
	// Task-owned task.v1.TaskProtocolEventPayloadV1 oneof.
	eventKindTask eventKind = iota + 1
	// eventKindProtocol carries a §5.11 code whose typed payload lives in the
	// Hub-owned hub.v1.ProtocolEventPayloadV1 oneof.
	eventKindProtocol
	eventKindCheckpoint
)

// chainEvent is the in-process form of one committed typed event.
//
// The two local TaskEventCode / ProtocolEventCode enums were deleted with the
// event wire: shared.v1.ProtocolEventCodeV1 is now the single event code
// registry for the whole protocol, and the envelope split (TaskEvent vs
// ProtocolEvent) follows the owning module of the typed payload, not the
// scope of the event. session_id / task_id / block_hash / tx_hash are raw
// bytes on the wire now, so they are carried as bytes end to end.
type chainEvent struct {
	Height     uint64
	BlockHash  []byte
	Source     string
	TxHash     []byte
	TxIndex    uint32
	EventIndex uint32
	Kind       eventKind
	Code       shared.ProtocolEventCodeV1
	SessionID  []byte
	TaskID     []byte
	Targets    []*shared.EventTarget
	Payload    proto.Message
}

func blockCheckpoint(height uint64) chainEvent {
	return chainEvent{
		Height:     height,
		Source:     "BLOCK",
		EventIndex: ^uint32(0),
		Kind:       eventKindCheckpoint,
	}
}

type blockData struct {
	Height              int64
	BlockHash           []byte
	Txs                 cmttypes.Txs
	TxResults           []*abci.ExecTxResult
	FinalizeBlockEvents []abci.Event
}

func eventsFromBlock(block blockData) []chainEvent {
	return eventsFromBlockObserved(block, nil)
}

type skippedEventObserver func(eventType string, source string, txIndex uint32, eventIndex uint32)

func eventsFromBlockObserved(block blockData, observeSkipped skippedEventObserver) []chainEvent {
	if block.Height <= 0 {
		return nil
	}
	out := make([]chainEvent, 0)
	for txIndex, result := range block.TxResults {
		if result == nil || result.Code != 0 {
			continue
		}
		var txHash []byte
		if txIndex < len(block.Txs) {
			txHash = tmhash.Sum(block.Txs[txIndex])
		}
		for eventIndex, event := range result.Events {
			if normalized, ok := normalizeEvent(event); ok {
				normalized.Height = uint64(block.Height)
				normalized.BlockHash = block.BlockHash
				normalized.Source = "TX"
				normalized.TxHash = txHash
				normalized.TxIndex = uint32(txIndex)
				normalized.EventIndex = uint32(eventIndex)
				out = append(out, normalized)
			} else if observeSkipped != nil && isTypedEvent(event.Type) {
				observeSkipped(event.Type, "TX", uint32(txIndex), uint32(eventIndex))
			}
		}
	}
	for eventIndex, event := range block.FinalizeBlockEvents {
		if normalized, ok := normalizeEvent(event); ok {
			normalized.Height = uint64(block.Height)
			normalized.BlockHash = block.BlockHash
			normalized.Source = "BLOCK"
			normalized.EventIndex = uint32(eventIndex)
			out = append(out, normalized)
		} else if observeSkipped != nil && isTypedEvent(event.Type) {
			observeSkipped(event.Type, "BLOCK", 0, uint32(eventIndex))
		}
	}
	return out
}

func isTypedEvent(eventType string) bool {
	return strings.HasPrefix(eventType, "task.v1.Event") || strings.HasPrefix(eventType, "hub.v1.Event")
}

func normalizeEvent(event abci.Event) (chainEvent, bool) {
	// Cosmos SDK appends this non-protobuf marker to every Begin/EndBlock event after
	// EmitTypedEvent has encoded the payload. ParseTypedEvent correctly rejects
	// unknown attributes, so remove only the exact framework-owned marker;
	// every other unknown or malformed attribute remains a hard skip.
	for index, attribute := range event.Attributes {
		if attribute.Key == "mode" && (attribute.Value == "BeginBlock" || attribute.Value == "EndBlock") {
			attributes := make([]abci.EventAttribute, 0, len(event.Attributes)-1)
			attributes = append(attributes, event.Attributes[:index]...)
			attributes = append(attributes, event.Attributes[index+1:]...)
			event.Attributes = attributes
			break
		}
	}
	payload, err := sdk.ParseTypedEvent(event)
	if err != nil {
		return chainEvent{}, false
	}
	return classifyTypedEvent(payload)
}

type eventPosition struct {
	Height      uint64
	SourceOrder uint32
	TxIndex     uint32
	EventIndex  uint32
}

func (e chainEvent) position() eventPosition {
	sourceOrder := uint32(0)
	if e.Source == "BLOCK" {
		sourceOrder = 1
	}
	return eventPosition{Height: e.Height, SourceOrder: sourceOrder, TxIndex: e.TxIndex, EventIndex: e.EventIndex}
}

func (p eventPosition) cursor() string {
	return fmt.Sprintf("v1:%d:%d:%d:%d", p.Height, p.SourceOrder, p.TxIndex, p.EventIndex)
}

func parseCursor(value string) (eventPosition, error) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 5 || parts[0] != "v1" {
		return eventPosition{}, fmt.Errorf("invalid task event cursor")
	}
	values := make([]uint64, 4)
	for i := range values {
		parsed, err := strconv.ParseUint(parts[i+1], 10, 64)
		if err != nil {
			return eventPosition{}, fmt.Errorf("invalid task event cursor component %d: %w", i+1, err)
		}
		values[i] = parsed
	}
	if values[1] > 1 || values[2] > uint64(^uint32(0)) || values[3] > uint64(^uint32(0)) {
		return eventPosition{}, fmt.Errorf("invalid task event cursor range")
	}
	if values[0] == 0 {
		return eventPosition{}, fmt.Errorf("invalid task event cursor height")
	}
	return eventPosition{Height: values[0], SourceOrder: uint32(values[1]), TxIndex: uint32(values[2]), EventIndex: uint32(values[3])}, nil
}

func comparePosition(left, right eventPosition) int {
	leftValues := [...]uint64{left.Height, uint64(left.SourceOrder), uint64(left.TxIndex), uint64(left.EventIndex)}
	rightValues := [...]uint64{right.Height, uint64(right.SourceOrder), uint64(right.TxIndex), uint64(right.EventIndex)}
	for i := range leftValues {
		if leftValues[i] < rightValues[i] {
			return -1
		}
		if leftValues[i] > rightValues[i] {
			return 1
		}
	}
	return 0
}
