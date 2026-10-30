# Settlement Builder contract

Settlement uses `task.v1.MsgSettleTask`, carrying only `task_id` and the
Cosmos transaction's `submitter_address`. Keeper derives settlement facts,
allocations, refunds and fault consequences from accepted state.

## Read authoritative state

Use one committed height for related queries. `Task`, `TaskBudget`,
`TaskAssignment`, `InferReceipt`, `TaskBuilders`, `VerificationRound` and
`ResultReceipt` expose accepted inputs. After settlement, `SettlementFacts`
and `Settlement` return the retained immutable result. Their current REST
paths and field definitions are in [the generated API reference](../static/node-api.md).

Events notify a state change; they do not authorize a payout. Pin a historical
read with standard Cosmos `x-cosmos-block-height` metadata when reproducing a
committed decision, and requery current state before submitting a transaction.

## Eligibility and closure

The current service account of the scheduled frozen Task Builder may settle
during its grace period. After all Builder grace periods, settlement becomes
permissionless until the deadline. The bounded deadline runner uses the same
settlement implementation when no transaction completes it.

Settlement waits for the challenge boundary, any second round, actual closure
of the bounded value-evidence window, and completion of round economic effects.
A stale readiness observation can fail without writing state. Retrying an
already completed settlement returns its terminal receipt.

## Funds and faults

Dispatch admission freezes funds already held in the Session. Final settlement
deducts the accepted bill and gas reimbursements, releases unused reservation
back to the Session, and never asks the owner for another Task funding transfer.
A pending CLOSE waits for in-flight Tasks before paying the remaining balance.

A committed value contradiction changes attribution and earning eligibility,
not the verdict or verifier denominator. A fault also disproved by round 2
uses one merged consequence with the greater frozen slash rate. An evidence-only
fine goes to Treasury. Keeper retains compact receipts and commitments, not raw
leaves, signatures or model output.
