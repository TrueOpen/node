# Registered session key activation

Pin a published wire release and review the generated diff before enabling the
feature. Confirm that the release includes the registration signing vectors as
well as the protobuf messages.

## Retained database activation

Coordinate all validators to halt after an agreed height H and restart with the
same binary. Set `[session-key-rules] activation-height = H+1` in app.toml on every
validator, full node and future replay node. This is a consensus deployment
setting: retain it for historical replay, never derive it from process startup.
Zero disables bootstrap of an old chain. Fresh positive genesis is already active.
Keep the database and a pre-activation backup. No governance upgrade proposal or
store-prefix migration is required by this activation path.

At and after the configured first block, before transactions, the pre-block hook
examines the current
`SessionParamsV1.max_session_key_blocks`. If it is zero, initialize it to
`DefaultMaxSessionKeyBlocks` (currently 604800 blocks). Archive the replaced
parameter version, advance params metadata and its commitment, and emit the
parameter-update event atomically. A positive value is never overwritten.
Governance requires a positive limit, so subsequent blocks and restarts do not
repeat initialization or overwrite an operator-selected value. The example
604800-block limit is about 9.8 days at a 1.4-second block interval, not seven
days; confirm the deployment limit against the network's measured block interval.

Before activation, preserve legacy parameter validation and the old parameter
commitment encoding: an omitted limit is not an additional zero hash field.
Historical zero-limit parameter bodies always keep that old hash encoding, even
after activation. Never replace archived parameter bodies or their pointers.
Wallet-only stream rows omit zero authorization amounts in private storage;
projection supplies canonical zero values without changing the old store bytes.
Fresh genesis requires an explicit positive limit. Importing an old zero-limit
genesis requires the replay plan and a state height before activation. For a
state exported after block E, set initial_height to E+1; a key's set_height must
not exceed E. Accepted tasks are never reauthorized during import.

Historical parameter versions and Task/Session pointers keep their original
values. Existing sessions have absent authorization fields, remain usable with
wallet signatures, and may register keys using the **current** limit. Grants
retain their own expiry when governance later changes that limit.

Owners may register a key while creating a session or through UpdateSessionKey.
The same key address may be registered on multiple sessions, each with its own
expiry, registration height and allowance accounting. Zero spend_cap explicitly
means unlimited; it does not waive checked arithmetic or the owner's balance.

## Validation before enabling SDK delegation

- Compare old and new binary replay through H, including parameter updates,
  wallet session writes and AppHash. Verify no bootstrap writes before H+1.
- Rehearse activation on a database copy with an existing session pinned to an
  older parameter version. Verify both wallet orders and new key registration.
- Verify current params, metadata hash/version, parameter-update event, and that
  a second pre-block call preserves the initialized or governance-updated value.
- Accept an order, rotate or clear its key, and verify continuation/refund plus
  export/import. Authorization changes cannot reauthorize an accepted task or
  charge its max_fee twice; refunds never replenish cumulative allowance.
- Check same-block transaction ordering and CLEAR/SET of the same key. Orders
  anchored before the new registration must fail the height boundary.
- Check account, sequence and escrow invariants after rejection, expiry,
  overflow, a downstream proposal error and a bank transfer failure.
- Review historical parameter retention separately before using Genesis export
  as a substitute for the retained database; private parameter history and row
  version pointers are not fields of the current public Genesis wire message.

Roll out wire, Node, Nexus and then SDK support. Enable delegated signing only
after the required services support the contract. A local Builder rejection is
not authoritative proof that another Builder did not obtain chain acceptance;
query the committed sequence before retrying an unused sequence with a new
signature.

## Wallet signing interoperability

Use the wire registration Amino vectors. UpdateSessionKey selects exactly one
flat action: `set` contains the grant; `clear` is the boolean `true`. Never use an
empty object for CLEAR in Amino/Web3, and never serialize the generated Go oneof
wrapper name. Native protobuf still uses the empty ClearSessionKeyV1 message.
The wire vectors pin the complete canonical sign document, typed graph, digest
and signature for creation, SET and CLEAR.
