# Settlement builder query contract

This runbook defines the Node-owned inputs for Cortex and other builders that
construct `task.v1.MsgSettle`. Comet event attributes are notifications,
not accounting authority.

## Public v1 queries

`TaskBudget` returns the committed per-task accounting row:

```text
GET /TrueOpen/task/v1/task_budget/{session_id}/{task_id}
```

`SettlementBuildFacts` returns the aggregate pre-settlement snapshot. Optional
query parameters `settlement_id`, `task_verdict`, `settlement_status`,
and `outlier_verifier` request a Keeper-derived settlement package:

```text
GET /TrueOpen/task/v1/settlement_build_facts/{session_id}/{task_id}
```

The aggregate response is versioned by
`contract_version = TRUEOPEN_SETTLEMENT_BUILD_FACTS_V1`. It contains the frozen
assignment, infer receipt, verifier assignment, task budget, worker reveal
receipt, every formal verifier's commit/result/full-reveal state, and the
canonical references and hashes consumed by Keeper settlement validation.
When a complete proposal is supplied and the task is ready,
`has_derived_fields = true` and `derived_fields` contains the exact fee,
refund, payout/fault hashes, evidence root, challenge boundary, and signing
bytes that transaction validation recomputes.

The REST JSON fixture for Cortex is:

```text
x/task/keeper/testdata/settlement_build_facts_v1.json
```

## Snapshot and height rules

A latest query reads committed height `H` and returns `snapshot_height = H`.
Every nested state value in one response comes from that same ABCI snapshot.
For historical reads, set the standard Cosmos `x-cosmos-block-height` metadata
and verify that the response reports the requested height.
Historical responses are audit/reproduction artifacts only:
`settlement_ready` describes readiness at that historical height, not
permission to submit against the current chain tip.

`expected_settlement_height` is `H + 1`.
`expected_challenge_close_height` is:

```text
expected_settlement_height + challenge_window_blocks
```

Those expected values are valid only if `MsgSettle` is included in the next
block. `must_requery_if_not_included` is therefore always true. If the
transaction misses that height, Cortex must query again, rebuild the
height-bound hashes/signing bytes, and re-sign. It must not reuse a response
from a different height.
An earlier transaction in block `H + 1` can also change accepted result or
reveal state before settlement executes. Keeper will reject the stale hashes
without writing settlement state; Cortex must wait for the block to commit and
restart from a fresh query.

`settlement_ready = true` means the committed snapshot has a reserved budget,
valid worker receipt, at least two accepted verifier results, no existing
settlement/root, and a next-block height within the verify deadline. A false
value is accompanied by stable machine-readable `readiness_failures`.

## Canonical ordering and hashes

`verifier_facts` follows the exact frozen `formal_verifier_set` order.
`formal_verifier_index` is part of each `result_receipt_leaf_hash`; clients must
not sort these leaves lexically. `commit_height_refs` and
`full_result_reveal_refs` are already in the order accepted by Keeper.
`missing_or_timeout_verifiers` is lexically sorted because that is the
canonical `MsgSettle` representation.

The following response fields are authoritative and must be copied without
substitution:

```text
worker_reveal_receipt_ref
result_receipt_refs_hash
registered_full_result_refs_hash
leaf_ordering_version
evidence_schema_hash
judgment_function_version
canonical_encoding_version
settlement_signing_domain
```

`settlement_signing_bytes` is the exact 32-byte payload to sign. The REST
gateway encodes it as base64. `settlement_signing_hash` is the lowercase hex
encoding of those same bytes and is provided for logging/comparison; it is not
a second SHA-256 operation.

`evidence_schema_hash` and `judgment_function_version` come from the immutable
`TaskAssignment.profile_execution_snapshot.verification_profile`. Keeper
rejects a settlement that supplies different values.

## MsgSettle field ownership

`field_policy` is machine-readable and uses the protobuf snake_case field
names:

- `caller_supplied_fields` are identities, builder proof, settlement identity,
  verdict/status, judgment output, and signature material.
- `copy_from_snapshot_fields` are accepted chain facts or next-height values.
- `keeper_recomputed_fields` must be supplied with the exact deterministic
  value Keeper recomputes and validates.
- `handler_written_empty_fields` must be empty; Keeper writes them.
- `deprecated_empty_fields` are rejected compatibility payloads.

Current integration rules keep business actual fees, payouts, maintenance fee,
and gas reimbursement disabled. Keeper recomputes them, requires the full
unused task budget refund, and rejects SDK or Cortex accounting estimates.
The compatibility fields `max_fee`, `assignment_height`,
`open_verify_height`, `trace_commit_root`,
`checkpoint_commit_root`, and `batch_log_root` may be left at their
zero value; if populated, they must be copied exactly from the snapshot.

## Builder sequence

1. Query `SettlementBuildFacts` once at committed height `H`.
2. Stop if `settlement_ready` is false.
3. Judge the accepted reveal/result facts using the returned frozen profile.
4. Re-query at the same current committed height with `settlement_id`,
   `task_verdict`, `settlement_status`, and optional
   `outlier_verifier`.
5. Require `has_derived_fields = true`, copy the returned derived fields into
   `MsgSettle`, and sign the returned `settlement_signing_bytes`.
6. Submit for height `H + 1`.
7. If it is not included at `H + 1`, discard the build package and restart at
   step 1.

Never reconstruct budget, accepted-result, payout, or receipt facts from
Comet events or local Nexus/Cortex messages.
