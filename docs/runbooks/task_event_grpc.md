# Node typed event gRPC

The Node gRPC server can stream committed custom chain events on its existing
listener. This is a local read service. It does not participate in consensus or
replace authoritative module queries.

## Configuration

The service is disabled by default. Configure it in `app.toml`:

```toml
[task-event-grpc]
enabled = true
protocol-events-enabled = false
subscriber-buffer-size = 128
max-subscribers = 500
max-replay-blocks = 10000
max-concurrent-replays = 8
```

The corresponding environment variables use the `NODED_` prefix, for example
`NODED_TASK_EVENT_GRPC_ENABLED=true`.

`enabled` registers `task.v1.TaskEventService` and
`hub.v1.HubEventService`, then starts their shared committed block reader.
`protocol-events-enabled` independently controls the Hub-owned
`SubscribeProtocolEvents`. The remaining settings bound subscriber memory,
replay work, and local RPC pressure.

## Public contract

The public definitions are:

- `task/v1/event.proto`: task-module owned event payloads.
- `hub/v1/event.proto`: hub-module owned event payloads.
- `shared/v1/event.proto`: common event codes, sources, checkpoints, roles,
  and targets.
- `task/v1/task_event.proto`: Task-owned streaming envelopes and filters.
- `hub/v1/hub_event.proto`: Hub-owned protocol-event stream and filters.

Consumers must generate clients from these protobuf files. They must not import
Node Keeper or store types. Event codes and roles are enums; raw ABCI attributes,
string event names, CSV address sets, and subscriber-specific payload rewrites
are not part of the API.

The same committed event cursor always has the same code, payload, and repeated
targets in task and role streams. A target is routing metadata, not authority.
Each response is a typed oneof containing either an event or a
`StreamCheckpoint`.

## Methods

### SubscribeTaskEvents

`session_id` is required. An empty `task_id` selects every task in that session.
An empty `codes` filter selects every supported Task-owned `ProtocolEventCodeV1`.

### SubscribeRoleEvents

This stream routes events to one service operator. `target_role` must be
`EVENT_ROLE_WORKER`, `EVENT_ROLE_VERIFIER`, or `EVENT_ROLE_BUILDER`. An empty
`codes` filter selects every matching task event.

Assignment and verification-open events carry structured repeated targets. A
consumer should use this stream for discovery, then query the task and follow it
through `SubscribeTaskEvents`.

### SubscribeProtocolEvents

This `hub.v1.HubEventService` stream carries non-task operational events such as model profile, service
bond, reward cursor, and freeze notifications. An optional
`target_address` limits delivery to events whose structured targets contain the
account, operator, or validator address. The method is available only when
`protocol-events-enabled = true`.

## Resume and ordering

- `after_cursor` resumes strictly after one committed event or checkpoint.
- `from_height` starts at an inclusive block height.
- The two fields are mutually exclusive.
- If neither is set, delivery begins with the next committed block observed
  after subscription setup.
- The cursor is an opaque, global chain-position token. It is unrelated to a
  Nexus per-task cursor.
- Live streams publish monotonic, coalescible checkpoints as committed blocks
  advance; a backpressured stream may skip an intermediate checkpoint. Replay
  sends a checkpoint at its final block. Persist the latest checkpoint cursor
  even when no business event matched the filter.

Live and replay delivery use the same path: a new-block signal triggers reads of
CometBFT `Block` and `BlockResults`. Successful transaction events and
FinalizeBlock events therefore share deterministic ordering and filtering.

## Consumer rules

Events are notifications. Builder, Cortex, Nexus, wallets, and indexers must
query authoritative state before taking an irreversible action.

Consumers must:

- persist the cursor after handling or deliberately skipping an event;
- ignore an unknown future oneof case, advance the cursor, and reconcile state
  with the corresponding Query RPC;
- reconnect with the last persisted cursor after `UNAVAILABLE`;
- after `OUT_OF_RANGE`, query authoritative state, discard the expired cursor,
  and establish a new live baseline; this is required only after being offline
  longer than the configured replay window;
- use either this service or a separately normalized indexer as the business
  event source, rather than treating raw Comet WebSocket events as a second
  source of truth.

Failed transactions are never streamed. Unsupported Cosmos SDK events are
ignored. Malformed or unsupported TrueOpen typed events are skipped so a permanent
historical block cannot repeatedly terminate every consumer, and Node records a
structured error containing the height, source, event type, and event position.
Direct CometBFT indexers should quarantine malformed typed attributes before
advancing their own position.

## Development reset

This contract is introduced as a development-stage hard cut. Node, Builder,
Cortex, and Nexus must regenerate clients and reset devnet event cursors
together. There is no raw-attribute fallback or typed/untyped double emission.

The language-neutral descriptor image and the TypeScript compatibility client
are produced by TrueOpen/wire, not here. Each wire release publishes
`wire.binpb` plus `release-manifest.json`, and wire CI generates and
type-checks the TypeScript from the same image before publishing it. Regenerate
your client from the release this repository pins in `wire/pin.json`.

`make proto-wire-check` verifies that pin; `make proto-event-check` regenerates
the Go from it and requires the committed sources to be identical. Node used to
carry its own `proto-sdk` target that re-generated and re-type-checked the same
TypeScript from the same descriptor; it was removed as a duplicate of wire CI
when generation moved to the release.
