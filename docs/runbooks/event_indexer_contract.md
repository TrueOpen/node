# Typed event indexer contract

Node publishes custom chain events as module-owned protobuf messages. This file
defines the contract for indexers that read CometBFT block results directly.
Builder, Cortex, Nexus, wallets, and most third-party clients should prefer the
owner service: `task.v1.TaskEventService` for Task events and
`hub.v1.HubEventService` for Hub/protocol events. Both provide the same
supported payloads in typed envelopes with replay cursors and structured targets.

Both services also send `shared.v1.StreamCheckpoint` messages after committed block
boundaries, so low-frequency filtered consumers can advance their cursors even
when they receive no business event.

## Canonical schemas

- Wire `task/v1/event.proto` owns TrueOpen Task event payloads.
- Wire `hub/v1/event.proto` owns Hub and protocol event payloads.
- Wire `shared/v1/event.proto` owns the single event-code registry, sources,
  checkpoints, roles, and targets.
- Wire `task/v1/task_event.proto` and `hub/v1/hub_event.proto` own their
  respective stream envelopes and filters. Their payload oneofs partition the
  shared code registry exactly.

Custom events are emitted only with the Cosmos SDK typed-event API. There is no
legacy snake-case mirror and no public raw-attribute fallback.

## Direct CometBFT indexing

The ABCI event type is the protobuf fully qualified message name, for example:

```text
task.v1.EventWorkerAssignmentFinalized
task.v1.EventInferReceiptAccepted
task.v1.EventTaskParamsUpdated
hub.v1.EventModelSupportUpdated
hub.v1.EventServiceStakeChanged
hub.v1.EventCandidatePoolPublished
```

Attribute keys use protobuf `snake_case` field names. Attribute values are
ProtoJSON fragments, not an independent string schema. In particular, string
and uint64 values retain their JSON quoting. Direct consumers must reconstruct
the typed message with its protobuf descriptor instead of treating attributes
as an untyped map.

A low-level subscription can select an event type without depending on encoded
attribute values:

```json
{
  "jsonrpc": "2.0",
  "method": "subscribe",
  "id": 1,
  "params": {
    "query": "tm.event='Tx' AND task.v1.EventWorkerAssignmentFinalized.task_id EXISTS"
  }
}
```

Use `Block` plus `BlockResults` when replaying by height. Read only events from
successful transaction results, then read FinalizeBlock events after all
transaction events. Preserve `(height, source, tx_index, event_index)` ordering.

Do not combine raw WebSocket delivery and either gRPC event service as two business
sources. An indexer may normalize raw block results into its own durable stream,
but every downstream consumer must choose one normalized source and deduplicate
by committed chain position.

## Stable routing facts

Task-scoped payloads contain non-empty `session_id` and `task_id`. Discovery
targets are carried by the owning stream envelope:

- `EventWorkerAssignmentFinalized` targets the selected Worker.
- Task discovery events use `TaskEventService`; Hub account/operator/validator
  events use `HubEventService`.
- account, operator, and validator protocol events use repeated `EventTarget`
  values with typed `EventRole` values.

Repeated addresses are protobuf repeated fields. CSV address lists are not part
of the public event contract.

Reward and support processing is deliberately represented by separate facts:

- `EventModelSupportActivated` reports the first successful support activation.
- `EventEarningsAccrued` reports a newly committed earning.
- `EventMarkHit` reports a deterministic mark-gate hit.

There is no aggregate `valid_task` event because those decisions occur at
different protocol stages and cannot be truthfully fixed at settlement time.

## Authoritative reconciliation

Events are notifications, not authoritative state. After receiving an event:

1. Persist or deliberately skip its cursor/chain position.
2. Query the owning module for committed state.
3. Make irreversible decisions only from the query result.

Typical task consumers should call `Query/Task` after any task event. Settlement
and finality consumers may additionally call the settlement and finality
queries. Protocol consumers should use the corresponding Hub query for model,
service bond, reward, freeze, or treasury state.

Malformed known events and unknown future event types must be quarantined or
skipped without permanently stopping historical replay. Consumers must advance
past unknown oneof cases and then reconcile relevant state.

## Data exposure

Public events must remain bounded. They must not contain prompt text, output
text, payload keys, private keys, complete results, raw evidence blobs, detached
signatures, full state objects, or debug-only policy fields. Large data is
represented by a hash/reference and recovered through an authoritative query or
the authenticated data plane.

## Development reset

This typed contract is a development-stage hard cut. Node, Builder, Cortex,
Nexus, wallets, and indexers must regenerate clients and reset stored event
cursors together. Runtime compatibility aliases and double emission are not
supported. Browser wallets should consume a trusted indexer/gateway rather than
the Node-only `grpc-js` stream transport. Protobuf field numbers and enum values become stable once this
contract is published; removed published identifiers must remain reserved.

Run these checks before publishing a Node revision:

```bash
make proto-wire-check
make proto-event-check
go test ./...
go vet ./...
```
