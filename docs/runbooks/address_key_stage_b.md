# Address store-key Stage B audit

This audit is separate from the private value migration. Public wire addresses
remain canonical Bech32 strings. Account-address components in Node store keys
must use address codec bytes, while model IDs use the separate fixed-width
Hash32 codec. The rows below are the audited migration scope. Address-typed
components now use `AddrKeyCodec` or `AddressStringKeyCodec`; ordinary string
components retain `collections.StringKey`.

| Module | Address-keyed collections or key families | Order-sensitive readers to verify |
| --- | --- | --- |
| Hub identity | `CortexNode`, `ServiceBond`, `Builder`, `BuilderAdmission`, `ServiceDescriptor`, `CurrentServiceAddressIndex`, `OperatorCandidateSlot`, `ValidatorBridgeSigner` | Participant and service-address reverse scans; Genesis index reconstruction. |
| Hub support | `ModelCapability`, `ModelSupport`, expiry/prune indexes, by-model/by-operator indexes, `DailySupport` and its expiry index | Model/operator prefix walks, bounded expiry/prune/recheck cursors, and daily replay order. |
| Hub liability and fault | `Unbonding`, maturity/status indexes, `TaskLiabilityReservation`, task/operator indexes, `ServiceKeyResponsibility` and its by-task index, `BuilderFault` and prune index | Bounded maturity/slash and responsibility-release walks; cursor identity and exact key/value agreement. |
| Hub accounting and VRF | `Earnings`, `VrfKey`, history/activation/prune indexes, `ServiceBondEffectiveIndex` | Query pagination, epoch activation/prune budgets, and Genesis round trips. |
| Task session and duty | `SessionByOwnerIndex`, `WorkerActiveTaskIndex`, `VerifierActiveJobIndex` | Owner/role-scoped pagination and active-task cleanup. |
| Task evidence | `DataUnavailableReport`, `BuilderDataUnavailableAggregate`, `WorkerEvidenceReceipt` | Verify-round actor scans and bounded evidence receipt walks. |

The Task session and evidence rows now use raw address key bytes. The complete
Task keeper suite passes, including Genesis rebuild, owner/role pagination,
page-token validation, and the `SessionNonce` key/value mismatch test. Hub
address components also use the shared strict codec, while non-address strings
remain unchanged. The full Hub keeper and Genesis suite now passes, including
the model-freeze/deactivation cursor contract; Hub Stage B is accepted.

`BuilderSetByIDIndex`, parameter-bucket keys, beacon consumer IDs and Task
bucket keys remain strings because those components are not account addresses.
Existing Task `AddrKeyCodec` paths already store raw address bytes and are not
counted as pending `StringKey` migrations.

Before switching any family, record its exact `collections.StringKey` occurrence
and every point, prefix, range, pagination or EndBlock reader. Bech32 text order
differs from raw-byte order. For each order-sensitive reader, either prove the
outcome is order-independent or explicitly adopt raw-byte order and test cursor
progress, item/byte budgets, fairness and page-token continuation. Fresh Genesis
has no cross-layout page token; no dual-key compatibility is planned.

Acceptance for each collection requires a raw key codec with defensive decoding,
canonical Bech32-to-bytes conversion at the public boundary, the existing
private-value projection, key/value identity checks, Genesis export/import
round-trip, and a focused ordering or pagination test. Do not describe Stage B as
complete until every row above has a checked implementation and test.
