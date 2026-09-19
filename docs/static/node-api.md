# TrueOpen Node V1 public RPC reference

> Generated from the current branch protobuf FileDescriptorSet by `scripts/generate_node_api_doc.go`. The descriptor is the authority for registered Msg and Query methods; blocked or internal-only methods are intentionally absent.

> Scope: 98 registered `hub.v1` and `task.v1` Msg/Query RPCs. The full protocol rationale remains in the monorepo contract and is not duplicated here.

## 1. Client contract

- Query methods are available through their full gRPC method names and the listed REST GET paths. Msg methods must be signed and broadcast as Cosmos SDK transactions.
- Client-facing Hash32 values use canonical lowercase 64-hex. TrueOpen Query REST selectors and JSON responses use that form directly; Msg/SDK adapters decode it to raw32 before protobuf transaction or gRPC transport. The outer Cosmos `tx_bytes` and explicitly classified non-Hash32 bytes remain base64; Store values remain raw bytes.
- TrueOpen account and operator addresses must be canonical Bech32. Cosmos account signatures authorize and pay for transactions; protocol service signatures use the operator's sole current on-chain service binding.
- Queries read one committed height. Opaque `PageTokenV1` values are bound to the RPC, chain, selectors, canonical last primary key, and query height; do not decode, alter, or replay them across requests.
- `InvalidArgument` means the selector, enum, address, limit, or page token is invalid. `NotFound` means the exact authoritative row is absent. `FailedPrecondition` means the row exists but its lifecycle does not permit that view. `Internal` indicates inconsistent committed state.
- Protocol events are hints. Consumers deduplicate by the committed event locator and repair gaps through the Query named by the event contract. Store and Query state remain authoritative.

## 2. Frozen boundaries

- Node consensus does not call Builder, Cortex, or Nexus services and does not use wall-clock time or local observations as consensus inputs.
- Full AI input/output and unbounded evidence bodies are not stored on chain; only frozen commitments, narrow receipts, roots, sizes, and lifecycle facts are public.
- APIs gated by unresolved contract blockers are not registered. A frozen event code may remain non-emitting until the state transition that supplies all required facts is implemented.

## 3. RPC index

The table below lists every custom module RPC in this version. Msg methods are broadcast as Cosmos transactions; Query methods are served over both gRPC and HTTP GET.

| Module | Kind | RPC | HTTP GET |
|---|---|---|---|
| `hub` | `Msg` | [`BatchConfirmModelSupport`](#rpc-hub-batchconfirmmodelsupport) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `hub` | `Msg` | [`BeginServiceUnstake`](#rpc-hub-beginserviceunstake) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `hub` | `Msg` | [`ClaimEarnings`](#rpc-hub-claimearnings) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `hub` | `Msg` | [`DeclareModelSupport`](#rpc-hub-declaremodelsupport) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `hub` | `Msg` | [`EmergencyFreezeVote`](#rpc-hub-emergencyfreezevote) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `hub` | `Msg` | [`RegisterBuilder`](#rpc-hub-registerbuilder) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `hub` | `Msg` | [`RegisterModelProfile`](#rpc-hub-registermodelprofile) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `hub` | `Msg` | [`RegisterVrfKey`](#rpc-hub-registervrfkey) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `hub` | `Msg` | [`RevokeServiceKey`](#rpc-hub-revokeservicekey) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `hub` | `Msg` | [`RotateServiceKey`](#rpc-hub-rotateservicekey) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `hub` | `Msg` | [`RunBuilderTerm`](#rpc-hub-runbuilderterm) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `hub` | `Msg` | [`RunRewardEpoch`](#rpc-hub-runrewardepoch) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `hub` | `Msg` | [`SetModelStatus`](#rpc-hub-setmodelstatus) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `hub` | `Msg` | [`SetProfileStatus`](#rpc-hub-setprofilestatus) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `hub` | `Msg` | [`StakeService`](#rpc-hub-stakeservice) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `hub` | `Msg` | [`SubmitFreezeSignal`](#rpc-hub-submitfreezesignal) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `hub` | `Msg` | [`UpdateHubParams`](#rpc-hub-updatehubparams) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `hub` | `Msg` | [`UpdateServiceDescriptor`](#rpc-hub-updateservicedescriptor) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `hub` | `Msg` | [`UpdateTimeoutBucket`](#rpc-hub-updatetimeoutbucket) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `hub` | `Msg` | [`WithdrawServiceUnbonded`](#rpc-hub-withdrawserviceunbonded) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `hub` | `Query` | [`Beacon`](#rpc-hub-beacon) | `/TrueOpen/hub/v1/beacon/{height}` |
| `hub` | `Query` | [`BridgeStatus`](#rpc-hub-bridgestatus) | `/TrueOpen/hub/v1/bridge/status` |
| `hub` | `Query` | [`Builder`](#rpc-hub-builder) | `/TrueOpen/hub/v1/builder/{builder_address}` |
| `hub` | `Query` | [`BuilderSet`](#rpc-hub-builderset) | `/TrueOpen/hub/v1/builder_set/by_height/{height}`<br>`/TrueOpen/hub/v1/builder_set/by_id/{builder_set_id}` |
| `hub` | `Query` | [`Builders`](#rpc-hub-builders) | `/TrueOpen/hub/v1/builders` |
| `hub` | `Query` | [`CandidatePoolMember`](#rpc-hub-candidatepoolmember) | `/TrueOpen/hub/v1/candidate_pool/snapshot/{snapshot_id}/member/{candidate_slot}` |
| `hub` | `Query` | [`CandidatePoolMembers`](#rpc-hub-candidatepoolmembers) | `/TrueOpen/hub/v1/candidate_pool/snapshot/{snapshot_id}/members` |
| `hub` | `Query` | [`CandidatePoolSnapshot`](#rpc-hub-candidatepoolsnapshot) | `/TrueOpen/hub/v1/candidate_pool/snapshot/{snapshot_id}` |
| `hub` | `Query` | [`CompetitionEpoch`](#rpc-hub-competitionepoch) | `/TrueOpen/hub/v1/competition_epoch/{reward_bucket}/{epoch}` |
| `hub` | `Query` | [`CortexNode`](#rpc-hub-cortexnode) | `/TrueOpen/hub/v1/cortex_node/{operator_address}` |
| `hub` | `Query` | [`CurrentCandidatePool`](#rpc-hub-currentcandidatepool) | `/TrueOpen/hub/v1/candidate_pool/current` |
| `hub` | `Query` | [`CurrentServiceKey`](#rpc-hub-currentservicekey) | `/TrueOpen/hub/v1/current_service_key/{participant_type}/{operator_address}` |
| `hub` | `Query` | [`DailySupport`](#rpc-hub-dailysupport) | `/TrueOpen/hub/v1/daily_support/{operator_address}/{epoch}` |
| `hub` | `Query` | [`Earnings`](#rpc-hub-earnings) | `/TrueOpen/hub/v1/earnings/{address}` |
| `hub` | `Query` | [`EmergencyFreezeVotes`](#rpc-hub-emergencyfreezevotes) | `/TrueOpen/hub/v1/emergency_freeze_votes/{freeze_signal_id}` |
| `hub` | `Query` | [`Fault`](#rpc-hub-fault) | `/TrueOpen/hub/v1/fault/{fault_id}` |
| `hub` | `Query` | [`FreezeSignal`](#rpc-hub-freezesignal) | `/TrueOpen/hub/v1/freeze_signal/{freeze_signal_id}` |
| `hub` | `Query` | [`FreezeSignals`](#rpc-hub-freezesignals) | `/TrueOpen/hub/v1/freeze_signals/{model_id}/{profile_version}/{status}` |
| `hub` | `Query` | [`Model`](#rpc-hub-model) | `/TrueOpen/hub/v1/model/{model_id}` |
| `hub` | `Query` | [`ModelSupport`](#rpc-hub-modelsupport) | `/TrueOpen/hub/v1/model_support/{operator_address}/{model_id}/{profile_version}` |
| `hub` | `Query` | [`Models`](#rpc-hub-models) | `/TrueOpen/hub/v1/models` |
| `hub` | `Query` | [`Params`](#rpc-hub-params) | `/TrueOpen/hub/v1/params` |
| `hub` | `Query` | [`Profile`](#rpc-hub-profile) | `/TrueOpen/hub/v1/profile/{model_id}/{profile_version}` |
| `hub` | `Query` | [`ProfileCapability`](#rpc-hub-profilecapability) | `/TrueOpen/hub/v1/profile_capability/{operator_address}/{model_id}/{profile_version}` |
| `hub` | `Query` | [`RewardEpochAudit`](#rpc-hub-rewardepochaudit) | `/TrueOpen/hub/v1/reward_epoch_audit/{source_epoch}/{reward_bucket}` |
| `hub` | `Query` | [`RewardEpochCursor`](#rpc-hub-rewardepochcursor) | `/TrueOpen/hub/v1/reward_epoch_cursor/{epoch}/{reward_bucket}` |
| `hub` | `Query` | [`ServiceBond`](#rpc-hub-servicebond) | `/TrueOpen/hub/v1/service_bond/{operator_address}` |
| `hub` | `Query` | [`ServiceDescriptor`](#rpc-hub-servicedescriptor) | `/TrueOpen/hub/v1/service_descriptor/{participant_type}/{operator_address}` |
| `hub` | `Query` | [`ServiceLifecycle`](#rpc-hub-servicelifecycle) | `/TrueOpen/hub/v1/service_lifecycle/{operator_address}` |
| `hub` | `Query` | [`ServiceUnbondings`](#rpc-hub-serviceunbondings) | `/TrueOpen/hub/v1/service_unbondings/{operator_address}` |
| `hub` | `Query` | [`TimeoutBucket`](#rpc-hub-timeoutbucket) | `/TrueOpen/hub/v1/timeout_bucket/{bucket_key}` |
| `hub` | `Query` | [`Treasury`](#rpc-hub-treasury) | `/TrueOpen/hub/v1/treasury` |
| `hub` | `Query` | [`ValidatorBridgeSigner`](#rpc-hub-validatorbridgesigner) | `/TrueOpen/hub/v1/bridge/validator_signer/{operator_address}` |
| `hub` | `Query` | [`VrfKey`](#rpc-hub-vrfkey) | `/TrueOpen/hub/v1/vrf_key/{operator_address}` |
| `task` | `Msg` | [`BatchSubmitVerifyCommit`](#rpc-task-batchsubmitverifycommit) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `task` | `Msg` | [`BatchSubmitVerifyResult`](#rpc-task-batchsubmitverifyresult) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `task` | `Msg` | [`CancelOrder`](#rpc-task-cancelorder) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `task` | `Msg` | [`CreateSession`](#rpc-task-createsession) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `task` | `Msg` | [`OpenChallengeRound`](#rpc-task-openchallengeround) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `task` | `Msg` | [`ReportDataUnavailable`](#rpc-task-reportdataunavailable) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `task` | `Msg` | [`SettleTask`](#rpc-task-settletask) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `task` | `Msg` | [`SubmitBuilderEvidence`](#rpc-task-submitbuilderevidence) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `task` | `Msg` | [`SubmitInferReceipt`](#rpc-task-submitinferreceipt) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `task` | `Msg` | [`SubmitVerifierHandraises`](#rpc-task-submitverifierhandraises) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `task` | `Msg` | [`SubmitVerifyCommit`](#rpc-task-submitverifycommit) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `task` | `Msg` | [`SubmitVerifyResult`](#rpc-task-submitverifyresult) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `task` | `Msg` | [`SubmitWorkerEvidence`](#rpc-task-submitworkerevidence) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `task` | `Msg` | [`SubmitWorkerHandraises`](#rpc-task-submitworkerhandraises) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `task` | `Msg` | [`SweepDeadline`](#rpc-task-sweepdeadline) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `task` | `Msg` | [`UpdateTaskParams`](#rpc-task-updatetaskparams) | Broadcast a signed transaction through `/cosmos/tx/v1beta1/txs` |
| `task` | `Query` | [`AssignmentRandomness`](#rpc-task-assignmentrandomness) | `/TrueOpen/task/v1/task/{task_id}/assignment_randomness` |
| `task` | `Query` | [`BuilderDataUnavailable`](#rpc-task-builderdataunavailable) | `/TrueOpen/task/v1/task/{task_id}/builder_data_unavailable/{verify_round}/{builder_operator_address}` |
| `task` | `Query` | [`DataUnavailableReports`](#rpc-task-dataunavailablereports) | `/TrueOpen/task/v1/task/{task_id}/data_unavailable_reports/{verify_round}` |
| `task` | `Query` | [`EpochTaskSummary`](#rpc-task-epochtasksummary) | `/TrueOpen/task/v1/epoch/{epoch}/task_summary` |
| `task` | `Query` | [`EvidenceCleanup`](#rpc-task-evidencecleanup) | `/TrueOpen/task/v1/task/{task_id}/cleanup` |
| `task` | `Query` | [`InferReceipt`](#rpc-task-inferreceipt) | `/TrueOpen/task/v1/task/{task_id}/infer_receipt` |
| `task` | `Query` | [`OrderSequence`](#rpc-task-ordersequence) | `/TrueOpen/task/v1/session/{session_id}/order/{order_sequence}` |
| `task` | `Query` | [`Params`](#rpc-task-params) | `/TrueOpen/task/v1/params` |
| `task` | `Query` | [`ResultReceipt`](#rpc-task-resultreceipt) | `/TrueOpen/task/v1/task/{task_id}/result_receipt/{verify_round}/{verifier_operator_address}` |
| `task` | `Query` | [`RoleActiveTasks`](#rpc-task-roleactivetasks) | `/TrueOpen/task/v1/role/{operator_address}/{duty}/active_tasks` |
| `task` | `Query` | [`Session`](#rpc-task-session) | `/TrueOpen/task/v1/session/{session_id}` |
| `task` | `Query` | [`SessionNonce`](#rpc-task-sessionnonce) | `/TrueOpen/task/v1/session_nonce/{user_address}` |
| `task` | `Query` | [`SessionTerminalSummary`](#rpc-task-sessionterminalsummary) | `/TrueOpen/task/v1/session/{session_id}/terminal_summary` |
| `task` | `Query` | [`SessionsByOwner`](#rpc-task-sessionsbyowner) | `/TrueOpen/task/v1/sessions/{user_address}` |
| `task` | `Query` | [`Settlement`](#rpc-task-settlement) | `/TrueOpen/task/v1/task/{task_id}/settlement` |
| `task` | `Query` | [`SettlementFacts`](#rpc-task-settlementfacts) | `/TrueOpen/task/v1/task/{task_id}/settlement_facts` |
| `task` | `Query` | [`Task`](#rpc-task-task) | `/TrueOpen/task/v1/task/{task_id}` |
| `task` | `Query` | [`TaskAssignment`](#rpc-task-taskassignment) | `/TrueOpen/task/v1/task/{task_id}/assignment` |
| `task` | `Query` | [`TaskBudget`](#rpc-task-taskbudget) | `/TrueOpen/task/v1/task/{task_id}/budget` |
| `task` | `Query` | [`TaskBuilders`](#rpc-task-taskbuilders) | `/TrueOpen/task/v1/task/{task_id}/builders` |
| `task` | `Query` | [`TaskFailureClass`](#rpc-task-taskfailureclass) | `/TrueOpen/task/v1/task/{task_id}/failure_class` |
| `task` | `Query` | [`TaskGasReimbursements`](#rpc-task-taskgasreimbursements) | `/TrueOpen/task/v1/task/{task_id}/gas_reimbursements` |
| `task` | `Query` | [`TaskStage`](#rpc-task-taskstage) | `/TrueOpen/task/v1/task/{task_id}/stage` |
| `task` | `Query` | [`VerificationRound`](#rpc-task-verificationround) | `/TrueOpen/task/v1/task/{task_id}/verification_round/{verify_round}` |
| `task` | `Query` | [`VerifierAssignment`](#rpc-task-verifierassignment) | `/TrueOpen/task/v1/task/{task_id}/verifier_assignment/{verify_round}` |
| `task` | `Query` | [`VerifierCandidateWindow`](#rpc-task-verifiercandidatewindow) | `/TrueOpen/task/v1/task/{task_id}/verifier_window/{verify_round}` |
| `task` | `Query` | [`VerifyCommit`](#rpc-task-verifycommit) | `/TrueOpen/task/v1/task/{task_id}/commit/{verify_round}/{verifier_operator_address}` |
| `task` | `Query` | [`WorkerEvidence`](#rpc-task-workerevidence) | `/TrueOpen/task/v1/task/{task_id}/worker_evidence/{worker_operator_address}/{seq}` |

### <a id="rpc-hub-batchconfirmmodelsupport"></a> `hub.BatchConfirmModelSupport`

- gRPC: `/hub.v1.Msg/BatchConfirmModelSupport`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `hub.v1.MsgBatchConfirmModelSupport` -> `hub.v1.MsgBatchConfirmModelSupportResponse`
- Purpose: A relayer submits, in batch, model support refreshes signed by Cortex Node service keys.
- Stage: The Cortex/service identity, bonding and model capability preparation stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `epoch_index` | `uint64` / JSON decimal string | epoch index; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `confirmations` | array<`hub.v1.ModelSupportConfirmationV1` object> | confirmations list; the semantics of an element are given by its message/enum definition. | Built from a preceding Query / frozen set or from the local batch queue; deduplicate and fix the ordering before submitting. | Bounded by the on-chain maximum count and the transaction size; validated item by item, with batch endpoints failing atomically or in isolation as their implementation defines. |
| `submitter_address` | `string` | submitter address; this field is part of the protocol state or of the request. | Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state. | Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `accepted_confirmations` | `uint32` | accepted confirmations; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `refreshed_profile_count` | `uint32` | refreshed profile count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `batch_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | batch digest; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-beginserviceunstake"></a> `hub.BeginServiceUnstake`

- gRPC: `/hub.v1.Msg/BeginServiceUnstake`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `hub.v1.MsgBeginServiceUnstake` -> `hub.v1.MsgBeginServiceUnstakeResponse`
- Purpose: Creates a Cortex service unbonding record and freezes the corresponding amount.
- Stage: The Cortex/service identity, bonding and model capability preparation stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `amount` | `shared.v1.Amount` object | amount; this field is part of the protocol state or of the request. | Computed from on-chain parameters, from the quote/order, or from the local balance budget; integers travel as decimal strings in JSON. | Must be a legal non-negative Coin/uint64 with a consistent denom and no overflow, and must satisfy the minimum bond, the available balance, the outstanding liabilities and the module budget limits. |
| `amount.atomic_units` | `string` | atomic units; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Taken from the Cortex Node identity or from the on-chain ModelCapability/ModelSupport state. | Must be a canonical Bech32 address whose node has a valid model and profile capability/support status. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `unbonding_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | unbonding id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `mature_height` | `uint64` / JSON decimal string | mature height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `remaining_active_bond` | `shared.v1.Amount` object | remaining active bond; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `remaining_active_bond.atomic_units` | `string` | atomic units; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-claimearnings"></a> `hub.ClaimEarnings`

- gRPC: `/hub.v1.Msg/ClaimEarnings`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `hub.v1.MsgClaimEarnings` -> `hub.v1.MsgClaimEarningsResponse`
- Purpose: Transfers generic claimable earnings past the finality window into the account.
- Stage: The earnings finalization, claim or epoch fund allocation stage.
- Keeper behaviour: Allows only claimable earnings to be claimed; pending settlement earnings cannot be claimed before the finality/challenge window closes.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `claim_class` | `hub.v1.ClaimClassV1` enum | claim class; this field is part of the protocol state or of the request. | Use the numeric value / name defined by the proto enum; pick it from the preceding state machine stage and never use an unrecognized custom value. | Must be a known enum value permitted by this RPC and must satisfy the current state transition; UNSPECIFIED is normally rejected. |
| `signer_address` | `string` | signer address; this field is part of the protocol state or of the request. | Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state. | Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `beneficiary` | `string` | beneficiary; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `claimed_amount` | `shared.v1.Amount` object | claimed amount; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `claimed_amount.atomic_units` | `string` | atomic units; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `remaining_claimable` | `shared.v1.Amount` object | remaining claimable; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `remaining_claimable.atomic_units` | `string` | atomic units; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `matured_items` | `uint32` | matured items; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-declaremodelsupport"></a> `hub.DeclareModelSupport`

- gRPC: `/hub.v1.Msg/DeclareModelSupport`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `hub.v1.MsgDeclareModelSupport` -> `hub.v1.MsgDeclareModelSupportResponse`
- Purpose: A Cortex Node declares node-level model/profile capability.
- Stage: The Cortex/service identity, bonding and model capability preparation stage.
- Keeper behaviour: Requires operator_address to be the sole Cosmos signer; a first declaration or a logically stale re-declaration establishes a support window, a live capability-only change preserves freshness, and a live declaration of the same value is a NOOP; daily freshness is advanced independently by MsgBatchConfirmModelSupport.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `operator_address` | `string` | The stable Cortex operator declaring or changing the profile capability; it is also the sole Cosmos signer of this Msg. | Filled in and signed as a standard Cosmos Tx by the operator's offline/external account tooling; cortexd does not load the operator private key. | Must be the sole Cosmos signer and must correspond to a registered ServiceBond principal; a FeeGrant may only pay the gas and cannot change the authorizing identity. |
| `model_id` | `string` | The canonical identifier of a model registered with the Hub. | Taken from the Hub Model registry or from a Models/Profiles list query, preserving the exact canonical ID used at registration. | Must match `^[a-z0-9][a-z0-9_-]{0,127}$`, remain a single URL path segment and a single NATS subject token, and reference a registered model. |
| `profile_version` | `uint32` | The frozen version of a model's Integration Profile. | Read the frozen version returned by a preceding Query; when creating a new version, increment monotonically from the existing maximum. | The version must be non-zero (except on compatibility paths where the protocol permits zero), and a referenced version must exist and agree with the task snapshot. |
| `inference_capability` | `bool` | inference capability; this field is part of the protocol state or of the request. | Set by the caller as an explicit business choice; do not infer state from the field's default value. | Checked together with the current lifecycle and the Integration Profile; it cannot bypass permissions or frozen parameters. |
| `verification_capability` | `bool` | verification capability; this field is part of the protocol state or of the request. | Set by the caller as an explicit business choice; do not infer state from the field's default value. | Checked together with the current lifecycle and the Integration Profile; it cannot bypass permissions or frozen parameters. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `support_version` | `uint64` / JSON decimal string | support version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `support_active` | `bool` | support active; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `fresh_until_epoch` | `uint64` / JSON decimal string | fresh until epoch; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-emergencyfreezevote"></a> `hub.EmergencyFreezeVote`

- gRPC: `/hub.v1.Msg/EmergencyFreezeVote`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `hub.v1.MsgEmergencyFreezeVote` -> `hub.v1.MsgEmergencyFreezeVoteResponse`
- Purpose: Votes on a freeze signal and performs the status switch once the threshold is met.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `freeze_signal_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | freeze signal id; this field is part of the protocol state or of the request. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `vote` | `hub.v1.EmergencyFreezeVote` enum | vote; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `validator_address` | `string` | validator address; this field is part of the protocol state or of the request. | Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state. | Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `accepted_voting_power` | `uint64` / JSON decimal string | accepted voting power; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `rejected_voting_power` | `uint64` / JSON decimal string | rejected voting power; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `signal_status` | `hub.v1.FreezeSignalStatus` enum | signal status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-registerbuilder"></a> `hub.RegisterBuilder`

- gRPC: `/hub.v1.Msg/RegisterBuilder`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `hub.v1.MsgRegisterBuilder` -> `hub.v1.MsgRegisterBuilderResponse`
- Purpose: Registers the Builder identity and its basic metadata.
- Stage: The Builder registration, election, stage contribution or reward stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `service_pubkey` | `bytes` / base64 string | service pubkey; this field is part of the protocol state or of the request. | Encoded deterministically according to the corresponding protocol structure; REST/JSON uses base64. | Checked for non-emptiness/length/ceiling, plus hash, signature, Merkle proof or canonical encoding verification where applicable. |
| `service_key_proof` | `bytes` / base64 string | service key proof; this field is part of the protocol state or of the request. | Built by the component that produced the proof, following the protocol schema; a Builder selection proof comes from the frozen selection event / local selection record, and a Merkle proof comes from the evidence store. | The schema/version/domain/size are checked, and the proof is verified against the frozen on-chain root, beacon, selection or commitment; a proof inconsistent with the primary key is rejected. |
| `descriptor` | `hub.v1.ServiceDescriptorV1` object | descriptor; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `descriptor.endpoints` | array<`hub.v1.ServiceEndpointV1` object> | endpoints list; the semantics of an element are given by its message/enum definition. | Built from a preceding Query / frozen set or from the local batch queue; deduplicate and fix the ordering before submitting. | Bounded by the on-chain maximum count and the transaction size; validated item by item, with batch endpoints failing atomically or in isolation as their implementation defines. |
| `builder_operator_address` | `string` | builder operator address; this field is part of the protocol state or of the request. | Taken from the Builder's local account, from BuilderSet, or from a TaskBuilderSelection query result. | Must be a canonical Bech32 address; where required it is checked to be registered, validly bonded, not tombstoned, and a member of the frozen stage Builder set. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `builder_address` | `string` | builder address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `builder_status` | `hub.v1.BuilderStatus` enum | builder status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `descriptor_version` | `uint64` / JSON decimal string | The version of the service network descriptor. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-registermodelprofile"></a> `hub.RegisterModelProfile`

- gRPC: `/hub.v1.Msg/RegisterModelProfile`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `hub.v1.MsgRegisterModelProfile` -> `hub.v1.MsgRegisterModelProfileResponse`
- Purpose: Atomically registers a Model/Profile projection; the CLI uses noded tx hub register-model-profile --profile-file <projection.json> --from <account>, which computes and signs the registration digest automatically.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Computes the registration digest over the frozen projection; an identical digest returns the original receipt without charging again, a conflicting projection fails closed; a new registration writes the Model/Profile/receipt and transfers into the Treasury in the same transaction.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `proposer_address` | `string` | proposer address; this field is part of the protocol state or of the request. | Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state. | Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state. |
| `profile` | `shared.v1.ModelProfileProjection` object | profile; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `profile.model_id` | `string` | The canonical identifier of a model registered with the Hub. | Taken from the Hub Model registry or from a Models/Profiles list query, preserving the exact canonical ID used at registration. | Must match `^[a-z0-9][a-z0-9_-]{0,127}$`, remain a single URL path segment and a single NATS subject token, and reference a registered model. |
| `profile.profile_version` | `uint32` | The frozen version of a model's Integration Profile. | Read the frozen version returned by a preceding Query; when creating a new version, increment monotonically from the existing maximum. | The version must be non-zero (except on compatibility paths where the protocol permits zero), and a referenced version must exist and agree with the task snapshot. |
| `profile.manifest_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | manifest hash; this field is part of the protocol state or of the request. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `profile.tokenizer_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | tokenizer hash; this field is part of the protocol state or of the request. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `profile.runtime_class` | `string` | runtime class; this field is part of the protocol state or of the request. | Use the numeric value / name defined by the proto enum; pick it from the preceding state machine stage and never use an unrecognized custom value. | Must be a known enum value permitted by this RPC and must satisfy the current state transition; UNSPECIFIED is normally rejected. |
| `profile.required_top_k` | `uint32` | required top k; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `profile.task_types` | array<`shared.v1.TaskType` enum> | task types list; the semantics of an element are given by its message/enum definition. | Built from a preceding Query / frozen set or from the local batch queue; deduplicate and fix the ordering before submitting. | Bounded by the on-chain maximum count and the transaction size; validated item by item, with batch endpoints failing atomically or in isolation as their implementation defines. |
| `profile.generation_type` | `shared.v1.GenerationType` enum | generation type; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `profile.resource_tier` | `uint32` | resource tier; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `profile.min_stake` | `cosmos.base.v1beta1.Coin` object | min stake; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `profile.challenge_open_window_blocks` | `uint64` / JSON decimal string | challenge open window blocks; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `profile.verification_profile` | `shared.v1.VerificationProfile` object | verification profile; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `profile.verification_thresholds` | `shared.v1.VerificationThresholds` object | verification thresholds; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `profile.batch_verification` | `shared.v1.BatchVerification` object | batch verification; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `profile.pricing_profile` | `shared.v1.PricingProfile` object | pricing profile; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `profile.timeout_bootstrap_profile` | `shared.v1.TimeoutBootstrapProfile` object | timeout bootstrap profile; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `profile.schema_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | schema hash; this field is part of the protocol state or of the request. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `profile.previous_profile_version` | `uint32` | previous profile version; this field is part of the protocol state or of the request. | Read the frozen version returned by a preceding Query; when creating a new version, increment monotonically from the existing maximum. | The version must be non-zero (except on compatibility paths where the protocol permits zero), and a referenced version must exist and agree with the task snapshot. |
| `profile.registration_fee` | `cosmos.base.v1beta1.Coin` object | registration fee; this field is part of the protocol state or of the request. | Computed from on-chain parameters, from the quote/order, or from the local balance budget; integers travel as decimal strings in JSON. | Must be a legal non-negative Coin/uint64 with a consistent denom and no overflow, and must satisfy the minimum bond, the available balance, the outstanding liabilities and the module budget limits. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `model_id` | `string` | The canonical identifier of a model registered with the Hub. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `profile_version` | `uint32` | The frozen version of a model's Integration Profile. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `registration_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | registration digest; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `idempotent_replay` | `bool` | idempotent replay; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `registered_height` | `uint64` / JSON decimal string | registered height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `registration_fee_paid` | `shared.v1.Amount` object | registration fee paid; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `registration_fee_paid.atomic_units` | `string` | atomic units; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-registervrfkey"></a> `hub.RegisterVrfKey`

- gRPC: `/hub.v1.Msg/RegisterVrfKey`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `hub.v1.MsgRegisterVrfKey` -> `hub.v1.MsgRegisterVrfKeyResponse`
- Purpose: Performs the `RegisterVrfKey` state transition. The current state must be read before calling; a retry must not be treated as naturally idempotent.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `vrf_pubkey` | `bytes` / base64 string | vrf pubkey; this field is part of the protocol state or of the request. | Encoded deterministically according to the corresponding protocol structure; REST/JSON uses base64. | Checked for non-emptiness/length/ceiling, plus hash, signature, Merkle proof or canonical encoding verification where applicable. |
| `vrf_key_pop` | `bytes` / base64 string | vrf key pop; this field is part of the protocol state or of the request. | Encoded deterministically according to the corresponding protocol structure; REST/JSON uses base64. | Checked for non-emptiness/length/ceiling, plus hash, signature, Merkle proof or canonical encoding verification where applicable. |
| `vrf_authorization_nonce` | `uint64` / JSON decimal string | vrf authorization nonce; this field is part of the protocol state or of the request. | Query SessionNonce / the account sequence before calling, or derive it from the deterministic nonce generation rule of the protocol message to be signed. | Must be exactly the nonce expected on chain and may be consumed only once, which prevents replay. |
| `operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Taken from the Cortex Node identity or from the on-chain ModelCapability/ModelSupport state. | Must be a canonical Bech32 address whose node has a valid model and profile capability/support status. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `pending_from_epoch` | `uint64` / JSON decimal string | pending from epoch; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `vrf_authorization_nonce` | `uint64` / JSON decimal string | vrf authorization nonce; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-revokeservicekey"></a> `hub.RevokeServiceKey`

- gRPC: `/hub.v1.Msg/RevokeServiceKey`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `hub.v1.MsgRevokeServiceKey` -> `hub.v1.MsgRevokeServiceKeyResponse`
- Purpose: Immediately revokes the operator's sole current service key even while liabilities are in flight; the liabilities are retained and a replacement rotate stays blocked.
- Stage: The Cortex/service identity, bonding and model capability preparation stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `participant_type` | `shared.v1.ParticipantType` enum | participant type; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `expected_current_service_authorization_nonce` | `uint64` / JSON decimal string | expected current service authorization nonce; this field is part of the protocol state or of the request. | Query SessionNonce / the account sequence before calling, or derive it from the deterministic nonce generation rule of the protocol message to be signed. | Must be exactly the nonce expected on chain and may be consumed only once, which prevents replay. |
| `reason_code` | `hub.v1.ServiceKeyRevocationReason` enum | reason code; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Taken from the Cortex Node identity or from the on-chain ModelCapability/ModelSupport state. | Must be a canonical Bech32 address whose node has a valid model and profile capability/support status. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `revoked_service_address` | `string` | revoked service address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `authorization_nonce` | `uint64` / JSON decimal string | The monotonically increasing nonce authorizing the current service key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-rotateservicekey"></a> `hub.RotateServiceKey`

- gRPC: `/hub.v1.Msg/RotateServiceKey`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `hub.v1.MsgRotateServiceKey` -> `hub.v1.MsgRotateServiceKeyResponse`
- Purpose: Atomically replaces the sole current service signing public key of a Cortex/Builder once liabilities have drained.
- Stage: The Cortex/service identity, bonding and model capability preparation stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `participant_type` | `shared.v1.ParticipantType` enum | participant type; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `new_service_pubkey` | `bytes` / base64 string | new service pubkey; this field is part of the protocol state or of the request. | Encoded deterministically according to the corresponding protocol structure; REST/JSON uses base64. | Checked for non-emptiness/length/ceiling, plus hash, signature, Merkle proof or canonical encoding verification where applicable. |
| `expected_current_service_authorization_nonce` | `uint64` / JSON decimal string | expected current service authorization nonce; this field is part of the protocol state or of the request. | Query SessionNonce / the account sequence before calling, or derive it from the deterministic nonce generation rule of the protocol message to be signed. | Must be exactly the nonce expected on chain and may be consumed only once, which prevents replay. |
| `new_service_key_proof` | `bytes` / base64 string | new service key proof; this field is part of the protocol state or of the request. | Built by the component that produced the proof, following the protocol schema; a Builder selection proof comes from the frozen selection event / local selection record, and a Merkle proof comes from the evidence store. | The schema/version/domain/size are checked, and the proof is verified against the frozen on-chain root, beacon, selection or commitment; a proof inconsistent with the primary key is rejected. |
| `operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Taken from the Cortex Node identity or from the on-chain ModelCapability/ModelSupport state. | Must be a canonical Bech32 address whose node has a valid model and profile capability/support status. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `new_service_address` | `string` | new service address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `new_nonce` | `uint64` / JSON decimal string | new nonce; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-runbuilderterm"></a> `hub.RunBuilderTerm`

- gRPC: `/hub.v1.Msg/RunBuilderTerm`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `hub.v1.MsgRunBuilderTerm` -> `hub.v1.MsgRunBuilderTermResponse`
- Purpose: Performs the `RunBuilderTerm` state transition. The current state must be read before calling; a retry must not be treated as naturally idempotent.
- Stage: The Builder registration, election, stage contribution or reward stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `target_term` | `uint64` / JSON decimal string | target term; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `max_items` | `uint32` | max items; this field is part of the protocol state or of the request. | Taken from the Query Params / operational batch configuration; choose a positive integer no larger than the on-chain hard ceiling. | Must be within the permitted range; Node additionally takes the smaller of the parameter ceiling and the requested value, and neither zero nor an oversized value may be used to trigger a full scan. |
| `submitter_address` | `string` | submitter address; this field is part of the protocol state or of the request. | Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state. | Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `visited` | `uint32` | visited; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `advanced` | `uint32` | advanced; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `builder_set_id` | `string` | builder set id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-runrewardepoch"></a> `hub.RunRewardEpoch`

- gRPC: `/hub.v1.Msg/RunRewardEpoch`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `hub.v1.MsgRunRewardEpoch` -> `hub.v1.MsgRunRewardEpochResponse`
- Purpose: Performs the `RunRewardEpoch` state transition. The current state must be read before calling; a retry must not be treated as naturally idempotent.
- Stage: The earnings finalization, claim or epoch fund allocation stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `epoch` | `uint64` / JSON decimal string | epoch; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `reward_bucket` | `hub.v1.RewardBucket` enum | reward bucket; this field is part of the protocol state or of the request. | Computed from on-chain parameters, from the quote/order, or from the local balance budget; integers travel as decimal strings in JSON. | Must be a legal non-negative Coin/uint64 with a consistent denom and no overflow, and must satisfy the minimum bond, the available balance, the outstanding liabilities and the module budget limits. |
| `max_items` | `uint32` | max items; this field is part of the protocol state or of the request. | Taken from the Query Params / operational batch configuration; choose a positive integer no larger than the on-chain hard ceiling. | Must be within the permitted range; Node additionally takes the smaller of the parameter ceiling and the requested value, and neither zero nor an oversized value may be used to trigger a full scan. |
| `submitter_address` | `string` | submitter address; this field is part of the protocol state or of the request. | Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state. | Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `phase` | `hub.v1.RewardEpochPhase` enum | phase; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `visited` | `uint32` | visited; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `advanced` | `uint32` | advanced; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-setmodelstatus"></a> `hub.SetModelStatus`

- gRPC: `/hub.v1.Msg/SetModelStatus`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `hub.v1.MsgSetModelStatus` -> `hub.v1.MsgSetModelStatusResponse`
- Purpose: Governance enable/disable of a model registration status.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `model_id` | `string` | The canonical identifier of a model registered with the Hub. | Taken from the Hub Model registry or from a Models/Profiles list query, preserving the exact canonical ID used at registration. | Must match `^[a-z0-9][a-z0-9_-]{0,127}$`, remain a single URL path segment and a single NATS subject token, and reference a registered model. |
| `new_status` | `hub.v1.ModelProfileStatus` enum | new status; this field is part of the protocol state or of the request. | Use the numeric value / name defined by the proto enum; pick it from the preceding state machine stage and never use an unrecognized custom value. | Must be a known enum value permitted by this RPC and must satisfy the current state transition; UNSPECIFIED is normally rejected. |
| `reason_code` | `hub.v1.GovernanceReason` enum | reason code; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `authority` | `string` | authority; this field is part of the protocol state or of the request. | Read the authority address from the on-chain governance module configuration; an ordinary account cannot choose its own. | Must be exactly the module authority, otherwise the governance parameter or state update is rejected. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `old_status` | `hub.v1.ModelProfileStatus` enum | old status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `new_status` | `hub.v1.ModelProfileStatus` enum | new status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `last_status_change_height` | `uint64` / JSON decimal string | last status change height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-setprofilestatus"></a> `hub.SetProfileStatus`

- gRPC: `/hub.v1.Msg/SetProfileStatus`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `hub.v1.MsgSetProfileStatus` -> `hub.v1.MsgSetProfileStatusResponse`
- Purpose: Governance enable/disable of a specific Integration Profile version.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `model_id` | `string` | The canonical identifier of a model registered with the Hub. | Taken from the Hub Model registry or from a Models/Profiles list query, preserving the exact canonical ID used at registration. | Must match `^[a-z0-9][a-z0-9_-]{0,127}$`, remain a single URL path segment and a single NATS subject token, and reference a registered model. |
| `profile_version` | `uint32` | The frozen version of a model's Integration Profile. | Read the frozen version returned by a preceding Query; when creating a new version, increment monotonically from the existing maximum. | The version must be non-zero (except on compatibility paths where the protocol permits zero), and a referenced version must exist and agree with the task snapshot. |
| `new_status` | `hub.v1.ModelProfileStatus` enum | new status; this field is part of the protocol state or of the request. | Use the numeric value / name defined by the proto enum; pick it from the preceding state machine stage and never use an unrecognized custom value. | Must be a known enum value permitted by this RPC and must satisfy the current state transition; UNSPECIFIED is normally rejected. |
| `reason_code` | `hub.v1.GovernanceReason` enum | reason code; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `authority` | `string` | authority; this field is part of the protocol state or of the request. | Read the authority address from the on-chain governance module configuration; an ordinary account cannot choose its own. | Must be exactly the module authority, otherwise the governance parameter or state update is rejected. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `old_status` | `hub.v1.ModelProfileStatus` enum | old status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `new_status` | `hub.v1.ModelProfileStatus` enum | new status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `last_status_change_height` | `uint64` / JSON decimal string | last status change height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-stakeservice"></a> `hub.StakeService`

- gRPC: `/hub.v1.Msg/StakeService`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `hub.v1.MsgStakeService` -> `hub.v1.MsgStakeServiceResponse`
- Purpose: Adds service bond for a Cortex node.
- Stage: The Cortex/service identity, bonding and model capability preparation stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `action` | `hub.v1.ServiceStakeActionV1` object | action; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `action.register` | `hub.v1.RegisterServiceV1` object | register; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `action.top_up` | `hub.v1.TopUpServiceV1` object | top up; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Taken from the Cortex Node identity or from the on-chain ModelCapability/ModelSupport state. | Must be a canonical Bech32 address whose node has a valid model and profile capability/support status. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `active_bond` | `shared.v1.Amount` object | active bond; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `active_bond.atomic_units` | `string` | atomic units; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `bond_version` | `uint64` / JSON decimal string | bond version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `effective_epoch` | `uint64` / JSON decimal string | effective epoch; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-submitfreezesignal"></a> `hub.SubmitFreezeSignal`

- gRPC: `/hub.v1.Msg/SubmitFreezeSignal`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `hub.v1.MsgSubmitFreezeSignal` -> `hub.v1.MsgSubmitFreezeSignalResponse`
- Purpose: Submits an emergency freeze signal for a model/profile.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `model_id` | `string` | The canonical identifier of a model registered with the Hub. | Taken from the Hub Model registry or from a Models/Profiles list query, preserving the exact canonical ID used at registration. | Must match `^[a-z0-9][a-z0-9_-]{0,127}$`, remain a single URL path segment and a single NATS subject token, and reference a registered model. |
| `profile_version` | `uint32` | The frozen version of a model's Integration Profile. | Read the frozen version returned by a preceding Query; when creating a new version, increment monotonically from the existing maximum. | The version must be non-zero (except on compatibility paths where the protocol permits zero), and a referenced version must exist and agree with the task snapshot. |
| `submitter_address` | `string` | submitter address; this field is part of the protocol state or of the request. | Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state. | Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `build_status` | `hub.v1.FreezeSignalBuildStatus` enum | build status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `risk_window_id` | `uint64` / JSON decimal string | risk window id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `freeze_signal_id` | optional `Hash32` / omitted when absent; lowercase 64-hex client value when present (raw 32-byte protobuf) | freeze signal id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | An absent value must be omitted; a present value must satisfy the bytes encoding and width the descriptor declares, and null or empty bytes must not be used to stand in for absence. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-updatehubparams"></a> `hub.UpdateHubParams`

- gRPC: `/hub.v1.Msg/UpdateHubParams`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `hub.v1.MsgUpdateHubParams` -> `hub.v1.MsgUpdateHubParamsResponse`
- Purpose: Governance update of the Hub parameters; affects identity, bonding, Builder, rewards and the safety windows.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `expected_version` | `uint64` / JSON decimal string | expected version; this field is part of the protocol state or of the request. | Read the frozen version returned by a preceding Query; when creating a new version, increment monotonically from the existing maximum. | The version must be non-zero (except on compatibility paths where the protocol permits zero), and a referenced version must exist and agree with the task snapshot. |
| `params` | `hub.v1.HubParamsV2` object | params; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `params.schema_version` | `uint32` | schema version; this field is part of the protocol state or of the request. | Read the frozen version returned by a preceding Query; when creating a new version, increment monotonically from the existing maximum. | The version must be non-zero (except on compatibility paths where the protocol permits zero), and a referenced version must exist and agree with the task snapshot. |
| `params.epoch` | `hub.v1.EpochParamsV1` object | epoch; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `params.support` | `hub.v1.SupportParamsV1` object | support; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `params.candidate_pool` | `hub.v1.CandidatePoolParamsV1` object | candidate pool; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `params.service` | `hub.v1.ServiceParamsV1` object | service; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `params.builder` | `hub.v1.BuilderParamsV1` object | builder; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `params.reward` | `hub.v1.RewardParamsV1` object | reward; this field is part of the protocol state or of the request. | Computed from on-chain parameters, from the quote/order, or from the local balance budget; integers travel as decimal strings in JSON. | Must be a legal non-negative Coin/uint64 with a consistent denom and no overflow, and must satisfy the minimum bond, the available balance, the outstanding liabilities and the module budget limits. |
| `params.freeze` | `hub.v1.FreezeParamsV1` object | freeze; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `params.treasury` | `hub.v1.TreasuryParamsV1` object | treasury; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `params.bucket` | `hub.v1.ParameterBucketParamsV1` object | bucket; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `params.query_event` | `hub.v1.QueryEventParamsV1` object | query event; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `params.price` | `hub.v1.PriceParamsV1` object | price; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `params.beacon` | `hub.v1.BeaconParamsV1` object | beacon; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `params.phase0` | `hub.v1.Phase0ParamsV1` object | phase0; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `params.bridge` | `hub.v1.BridgeParamsV1` object | bridge; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `authority` | `string` | authority; this field is part of the protocol state or of the request. | Read the authority address from the on-chain governance module configuration; an ordinary account cannot choose its own. | Must be exactly the module authority, otherwise the governance parameter or state update is rejected. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `new_version` | `uint64` / JSON decimal string | new version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `params_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | params hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-updateservicedescriptor"></a> `hub.UpdateServiceDescriptor`

- gRPC: `/hub.v1.Msg/UpdateServiceDescriptor`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `hub.v1.MsgUpdateServiceDescriptor` -> `hub.v1.MsgUpdateServiceDescriptorResponse`
- Purpose: Publishes a versioned descriptor of service connection endpoints, capability digests and the like.
- Stage: The Cortex/service identity, bonding and model capability preparation stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `participant_type` | `shared.v1.ParticipantType` enum | participant type; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `expected_descriptor_version` | `uint64` / JSON decimal string | expected descriptor version; this field is part of the protocol state or of the request. | Read the frozen version returned by a preceding Query; when creating a new version, increment monotonically from the existing maximum. | The version must be non-zero (except on compatibility paths where the protocol permits zero), and a referenced version must exist and agree with the task snapshot. |
| `descriptor` | `hub.v1.ServiceDescriptorV1` object | descriptor; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `descriptor.endpoints` | array<`hub.v1.ServiceEndpointV1` object> | endpoints list; the semantics of an element are given by its message/enum definition. | Built from a preceding Query / frozen set or from the local batch queue; deduplicate and fix the ordering before submitting. | Bounded by the on-chain maximum count and the transaction size; validated item by item, with batch endpoints failing atomically or in isolation as their implementation defines. |
| `operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Taken from the Cortex Node identity or from the on-chain ModelCapability/ModelSupport state. | Must be a canonical Bech32 address whose node has a valid model and profile capability/support status. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `new_descriptor_version` | `uint64` / JSON decimal string | new descriptor version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `descriptor_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | descriptor hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-updatetimeoutbucket"></a> `hub.UpdateTimeoutBucket`

- gRPC: `/hub.v1.Msg/UpdateTimeoutBucket`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `hub.v1.MsgUpdateTimeoutBucket` -> `hub.v1.MsgUpdateTimeoutBucketResponse`
- Purpose: Performs the `UpdateTimeoutBucket` state transition. The current state must be read before calling; a retry must not be treated as naturally idempotent.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `update` | `hub.v1.TimeoutBucketUpdateV1` object | update; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `update.bucket_key` | `string` | bucket key; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `update.expected_current_version` | `uint64` / JSON decimal string | expected current version; this field is part of the protocol state or of the request. | Read the frozen version returned by a preceding Query; when creating a new version, increment monotonically from the existing maximum. | The version must be non-zero (except on compatibility paths where the protocol permits zero), and a referenced version must exist and agree with the task snapshot. |
| `update.effective_height` | `uint64` / JSON decimal string | effective height; this field is part of the protocol state or of the request. | Read the frozen height from the corresponding Query/event; relative windows are computed from on-chain params and must never use local wall-clock time. | Must fall inside the window the current stage permits; too early, past the deadline, a height that moves backwards, or an overflowing computation are all rejected. |
| `update.entries` | array<`hub.v1.TimeoutBucketEntryV1` object> | entries list; the semantics of an element are given by its message/enum definition. | Built from a preceding Query / frozen set or from the local batch queue; deduplicate and fix the ordering before submitting. | Bounded by the on-chain maximum count and the transaction size; validated item by item, with batch endpoints failing atomically or in isolation as their implementation defines. |
| `authority` | `string` | authority; this field is part of the protocol state or of the request. | Read the authority address from the on-chain governance module configuration; an ordinary account cannot choose its own. | Must be exactly the module authority, otherwise the governance parameter or state update is rejected. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `bucket_key` | `string` | bucket key; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `new_version` | `uint64` / JSON decimal string | new version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `effective_height` | `uint64` / JSON decimal string | effective height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `bucket_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | bucket hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-withdrawserviceunbonded"></a> `hub.WithdrawServiceUnbonded`

- gRPC: `/hub.v1.Msg/WithdrawServiceUnbonded`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `hub.v1.MsgWithdrawServiceUnbonded` -> `hub.v1.MsgWithdrawServiceUnbondedResponse`
- Purpose: Withdraws the service bond once it is mature and no task liability remains.
- Stage: The Cortex/service identity, bonding and model capability preparation stage.
- Keeper behaviour: Validates the mature height, pending TaskLiability and the not-yet-withdrawn status; after the transfer it records withdrawn_amount and debug_rule_version to prevent a repeated withdrawal.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `locator` | `hub.v1.UnbondingLocatorV1` object | locator; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `locator.by_id` | `shared.v1.ByIDV1` object | by id; this field is part of the protocol state or of the request. | Obtained from the successful response, the event or the corresponding Query that created the object, then persisted. | Must be non-empty, length-bounded and reference an existing object; the creation path also checks uniqueness. |
| `locator.batch` | `shared.v1.BatchV1` object | batch; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Taken from the Cortex Node identity or from the on-chain ModelCapability/ModelSupport state. | Must be a canonical Bech32 address whose node has a valid model and profile capability/support status. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `withdrawn_amount` | `shared.v1.Amount` object | withdrawn amount; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `withdrawn_amount.atomic_units` | `string` | atomic units; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `withdrawn_items` | `uint32` | withdrawn items; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-beacon"></a> `hub.Beacon`

- gRPC: `/hub.v1.Query/Beacon`
- REST: `GET /TrueOpen/hub/v1/beacon/{height}`
- Request/response: `hub.v1.QueryBeaconRequest` -> `hub.v1.QueryBeaconResponse`
- Purpose: Reads the authoritative on-chain state behind `Beacon`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `height` | `uint64` / JSON decimal string | height; this field is part of the protocol state or of the request. | Read the frozen height from the corresponding Query/event; relative windows are computed from on-chain params and must never use local wall-clock time. | Must fall inside the window the current stage permits; too early, past the deadline, a height that moves backwards, or an overflowing computation are all rejected. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `beacon` | `hub.v1.BeaconState` object | beacon; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `beacon.height` | `uint64` / JSON decimal string | height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `beacon.randomness_hex` | `string` | randomness_hex is the canonical lower-case hex encoding of the 32-byte beacon value R_h. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `beacon.source_tag` | `string` | source_tag identifies how the value was derived: placeholder_blockhash_v1 or proposer_vrf_v1. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `beacon.proof_digest` | optional `Hash32` / omitted when absent; lowercase 64-hex client value when present (raw 32-byte protobuf) | The SHA256(raw proof bytes) of proposer_vrf_v1; for a dev-only placeholder this optional field must be absent. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | An absent value must be omitted; a present value must satisfy the bytes encoding and width the descriptor declares, and null or empty bytes must not be used to stand in for absence. |
| `beacon.proposer_consensus_address` | `string` | proposer_consensus_address is the consensus proposer identity checked by ProcessProposal / PreBlocker before this state is persisted. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `beacon.verified` | `bool` | verified is true only after the proposal validation path has verified proof_h against the operator's active VrfKeyState public key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `beacon.block_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | block_hash is the committed header hash at this exact height. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-bridgestatus"></a> `hub.BridgeStatus`

- gRPC: `/hub.v1.Query/BridgeStatus`
- REST: `GET /TrueOpen/hub/v1/bridge/status`
- Request/response: `hub.v1.QueryBridgeStatusRequest` -> `hub.v1.QueryBridgeStatusResponse`
- Purpose: Reads the authoritative on-chain state behind `BridgeStatus`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| - | empty message | no parameters | send an empty object `{}` | no business field validation |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `status` | `hub.v1.BridgeStatusViewV1` object | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `status.usdc_route_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | usdc route id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.lifecycle` | `hub.v1.BridgeLifecycleV1` enum | lifecycle; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.frozen` | `bool` | frozen; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.hyperlane_local_domain` | `uint32` | hyperlane local domain; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.origin_domain` | `uint32` | origin domain; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.origin_token_address` | `bytes` / base64 string | origin token address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.origin_warp_router_address` | `bytes` / base64 string | origin warp router address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.origin_mailbox_address` | `bytes` / base64 string | origin mailbox address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.origin_decimals` | `uint32` | origin decimals; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.business_denom` | `string` | business denom; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.local_mailbox_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | local mailbox id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.local_warp_token_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | local warp token id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.local_origin_denom` | `string` | local origin denom; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.local_ism_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | local ism id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.local_signer_set_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | local signer set hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.confirmed_evm_ism_address` | `bytes` / base64 string | confirmed evm ism address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.confirmed_evm_signer_set_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | confirmed evm signer set hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.threshold` | `uint32` | threshold; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.signer_count` | `uint32` | signer count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.deployment_manifest_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | deployment manifest hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.active_inbound_limit_per_epoch` | `shared.v1.Amount` object | active inbound limit per epoch; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `status.active_outbound_limit_per_epoch` | `shared.v1.Amount` object | active outbound limit per epoch; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `status.active_limit_effective_epoch` | `uint64` / JSON decimal string | active limit effective epoch; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.pending_inbound_limit_per_epoch` | `shared.v1.Amount` object | pending inbound limit per epoch; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `status.pending_outbound_limit_per_epoch` | `shared.v1.Amount` object | pending outbound limit per epoch; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `status.pending_limit_effective_epoch` | `uint64` / JSON decimal string | pending limit effective epoch; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.current_reward_epoch` | `uint64` / JSON decimal string | current reward epoch; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.current_inbound_used` | `shared.v1.Amount` object | current inbound used; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `status.current_outbound_used` | `shared.v1.Amount` object | current outbound used; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `status.genesis_allocated` | `shared.v1.Amount` object | genesis allocated; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `status.cumulative_bridge_minted` | `shared.v1.Amount` object | cumulative bridge minted; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `status.cumulative_bridge_burned` | `shared.v1.Amount` object | cumulative bridge burned; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `status.current_bank_supply` | `shared.v1.Amount` object | current bank supply; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `status.bootstrap_mode` | `hub.v1.BridgeBootstrapModeV1` enum | bootstrap mode; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.bootstrap_message_id` | optional `Hash32` / omitted when absent; lowercase 64-hex client value when present (raw 32-byte protobuf) | bootstrap message id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | An absent value must be omitted; a present value must satisfy the bytes encoding and width the descriptor declares, and null or empty bytes must not be used to stand in for absence. |
| `status.bootstrap_route_id` | optional `Hash32` / omitted when absent; lowercase 64-hex client value when present (raw 32-byte protobuf) | bootstrap route id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | An absent value must be omitted; a present value must satisfy the bytes encoding and width the descriptor declares, and null or empty bytes must not be used to stand in for absence. |
| `status.bootstrap_recipient` | `string` | bootstrap recipient; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.bootstrap_amount` | `shared.v1.Amount` object | bootstrap amount; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `status.bootstrap_fee_payer` | `string` | bootstrap fee payer; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.bootstrap_max_gas` | `uint64` / JSON decimal string | bootstrap max gas; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.bootstrap_consumed_height` | `uint64` / JSON decimal string | bootstrap consumed height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.cutover_proposal_id` | `uint64` / JSON decimal string | cutover proposal id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.inflight_manifest_hash` | optional `Hash32` / omitted when absent; lowercase 64-hex client value when present (raw 32-byte protobuf) | inflight manifest hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | An absent value must be omitted; a present value must satisfy the bytes encoding and width the descriptor declares, and null or empty bytes must not be used to stand in for absence. |
| `status.inflight_message_count` | `uint32` | inflight message count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.evm_confirmation_block_number` | `uint64` / JSON decimal string | evm confirmation block number; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.evm_confirmation_block_hash` | optional `Hash32` / omitted when absent; lowercase 64-hex client value when present (raw 32-byte protobuf) | evm confirmation block hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | An absent value must be omitted; a present value must satisfy the bytes encoding and width the descriptor declares, and null or empty bytes must not be used to stand in for absence. |
| `status.invariant_bridge_1_ok` | `bool` | invariant bridge 1 ok; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.invariant_bridge_2_ok` | `bool` | invariant bridge 2 ok; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status.invariant_bridge_3_ok` | `bool` | invariant bridge 3 ok; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-builder"></a> `hub.Builder`

- gRPC: `/hub.v1.Query/Builder`
- REST: `GET /TrueOpen/hub/v1/builder/{builder_address}`
- Request/response: `hub.v1.QueryBuilderRequest` -> `hub.v1.QueryBuilderResponse`
- Purpose: Reads the authoritative on-chain state behind `Builder`, for building the next stage's request, for auditing, or for recovery.
- Stage: The Builder registration, election, stage contribution or reward stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `builder_address` | `string` | builder address; this field is part of the protocol state or of the request. | Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state. | Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `builder` | `hub.v1.BuilderState` object | builder; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `builder.schema_version` | `uint32` | schema version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `builder.builder_address` | `string` | builder address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `builder.current_service_address` | `string` | current service address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `builder.current_service_pubkey` | `bytes` / base64 string | current service pubkey; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `builder.current_service_key_status` | `hub.v1.ServiceKeyStatus` enum | current service key status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `builder.service_authorization_nonce` | `uint64` / JSON decimal string | service authorization nonce; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `builder.current_descriptor_version` | `uint64` / JSON decimal string | current descriptor version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `builder.registered_height` | `uint64` / JSON decimal string | registered height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `builder.active_task_liability_count` | `uint32` | active task liability count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `builder.pending_stage_duty_count` | `uint32` | pending stage duty count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `builder.pending_evidence_submission_count` | `uint32` | pending evidence submission count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-builderset"></a> `hub.BuilderSet`

- gRPC: `/hub.v1.Query/BuilderSet`
- REST: `GET /TrueOpen/hub/v1/builder_set/by_height/{height}`
- REST: `GET /TrueOpen/hub/v1/builder_set/by_id/{builder_set_id}`
- Request/response: `hub.v1.QueryBuilderSetRequest` -> `hub.v1.QueryBuilderSetResponse`
- Purpose: Reads the authoritative on-chain state behind `BuilderSet`, for building the next stage's request, for auditing, or for recovery.
- Stage: The Builder registration, election, stage contribution or reward stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `height` | `uint64` / JSON decimal string | height; this field is part of the protocol state or of the request. | Read the frozen height from the corresponding Query/event; relative windows are computed from on-chain params and must never use local wall-clock time. | Must fall inside the window the current stage permits; too early, past the deadline, a height that moves backwards, or an overflowing computation are all rejected. |
| `builder_set_id` | `string` | builder set id; this field is part of the protocol state or of the request. | Obtained from the successful response, the event or the corresponding Query that created the object, then persisted. | Must be non-empty, length-bounded and reference an existing object; the creation path also checks uniqueness. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `set` | `hub.v1.BuilderSetViewV1` object | set; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `set.builder_set_version` | `uint64` / JSON decimal string | builder set version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `set.builder_set_id` | `string` | builder set id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `set.builder_set_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | builder set hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `set.effective_height` | `uint64` / JSON decimal string | effective height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `set.active_builders` | array<`string`> | active builders list; the semantics of an element are given by its message/enum definition. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once. |
| `set.active_builder_count` | `uint32` | active builder count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `set.body_status` | `shared.v1.StoredBodyStatus` enum | body status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `set.pruned_height` | `uint64` / JSON decimal string | pruned height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-builders"></a> `hub.Builders`

- gRPC: `/hub.v1.Query/Builders`
- REST: `GET /TrueOpen/hub/v1/builders`
- Request/response: `hub.v1.QueryBuildersRequest` -> `hub.v1.QueryBuildersResponse`
- Purpose: Reads the authoritative on-chain state behind `Builders`, for building the next stage's request, for auditing, or for recovery.
- Stage: The Builder registration, election, stage contribution or reward stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `page` | `shared.v1.QueryPageRequestV1` object | The batch page or the pagination cursor. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `page.page_token` | `bytes` / base64 string | page_token is empty on the first page and otherwise a canonical PageTokenV1. It is an opaque variable-length cursor blob, not a Hash32, so REST projects it as ProtoJSON Base64. | Encoded deterministically according to the corresponding protocol structure; REST/JSON uses base64. | Checked for non-emptiness/length/ceiling, plus hash, signature, Merkle proof or canonical encoding verification where applicable. |
| `page.limit` | `uint32` | The ceiling on a single query/processing pass, which prevents unbounded gas and responses. | Taken from the Query Params / operational batch configuration; choose a positive integer no larger than the on-chain hard ceiling. | Must be within the permitted range; Node additionally takes the smaller of the parameter ceiling and the requested value, and neither zero nor an oversized value may be used to trigger a full scan. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `builders` | array<`hub.v1.BuilderState` object> | builders list; the semantics of an element are given by its message/enum definition. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once. |
| `page` | `shared.v1.QueryPageResponseV1` object | The batch page or the pagination cursor. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `page.next_page_token` | `bytes` / base64 string | next_page_token is empty when the walk is complete. Same opaque PageTokenV1 blob as page_token, so REST projects it as ProtoJSON Base64. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-candidatepoolmember"></a> `hub.CandidatePoolMember`

- gRPC: `/hub.v1.Query/CandidatePoolMember`
- REST: `GET /TrueOpen/hub/v1/candidate_pool/snapshot/{snapshot_id}/member/{candidate_slot}`
- Request/response: `hub.v1.QueryCandidatePoolMemberRequest` -> `hub.v1.QueryCandidatePoolMemberResponse`
- Purpose: Reads the authoritative on-chain state behind `CandidatePoolMember`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `snapshot_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | snapshot id; this field is part of the protocol state or of the request. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `candidate_slot` | `uint32` | candidate slot; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `member` | `hub.v1.CandidatePoolMemberViewV1` object | member; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `member.candidate_slot` | `uint32` | candidate slot; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `member.slot_version` | `uint64` / JSON decimal string | slot version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `member.operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `member.binding_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | binding hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-candidatepoolmembers"></a> `hub.CandidatePoolMembers`

- gRPC: `/hub.v1.Query/CandidatePoolMembers`
- REST: `GET /TrueOpen/hub/v1/candidate_pool/snapshot/{snapshot_id}/members`
- Request/response: `hub.v1.QueryCandidatePoolMembersRequest` -> `hub.v1.QueryCandidatePoolMembersResponse`
- Purpose: Reads the authoritative on-chain state behind `CandidatePoolMembers`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `snapshot_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | snapshot id; this field is part of the protocol state or of the request. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `page` | `shared.v1.QueryPageRequestV1` object | The batch page or the pagination cursor. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `page.page_token` | `bytes` / base64 string | page_token is empty on the first page and otherwise a canonical PageTokenV1. It is an opaque variable-length cursor blob, not a Hash32, so REST projects it as ProtoJSON Base64. | Encoded deterministically according to the corresponding protocol structure; REST/JSON uses base64. | Checked for non-emptiness/length/ceiling, plus hash, signature, Merkle proof or canonical encoding verification where applicable. |
| `page.limit` | `uint32` | The ceiling on a single query/processing pass, which prevents unbounded gas and responses. | Taken from the Query Params / operational batch configuration; choose a positive integer no larger than the on-chain hard ceiling. | Must be within the permitted range; Node additionally takes the smaller of the parameter ceiling and the requested value, and neither zero nor an oversized value may be used to trigger a full scan. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `members` | array<`hub.v1.CandidatePoolMemberViewV1` object> | members list; the semantics of an element are given by its message/enum definition. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once. |
| `page` | `shared.v1.QueryPageResponseV1` object | The batch page or the pagination cursor. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `page.next_page_token` | `bytes` / base64 string | next_page_token is empty when the walk is complete. Same opaque PageTokenV1 blob as page_token, so REST projects it as ProtoJSON Base64. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-candidatepoolsnapshot"></a> `hub.CandidatePoolSnapshot`

- gRPC: `/hub.v1.Query/CandidatePoolSnapshot`
- REST: `GET /TrueOpen/hub/v1/candidate_pool/snapshot/{snapshot_id}`
- Request/response: `hub.v1.QueryCandidatePoolSnapshotRequest` -> `hub.v1.QueryCandidatePoolSnapshotResponse`
- Purpose: Reads the authoritative on-chain state behind `CandidatePoolSnapshot`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `snapshot_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | snapshot id; this field is part of the protocol state or of the request. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `snapshot` | `hub.v1.CandidatePoolSnapshotViewV1` object | snapshot; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `snapshot.epoch` | `uint64` / JSON decimal string | epoch; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `snapshot.snapshot_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | snapshot id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `snapshot.pool_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | pool hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `snapshot.status` | `hub.v1.CandidatePoolSnapshotStatus` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `snapshot.slot_capacity` | `uint32` | slot capacity; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `snapshot.active_count` | `uint32` | active count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `snapshot.active_bitmap_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | active bitmap hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `snapshot.member_set_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | member set hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `snapshot.published_height` | `uint64` / JSON decimal string | published height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `snapshot.effective_height` | `uint64` / JSON decimal string | effective height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `snapshot.expires_height` | `uint64` / JSON decimal string | expires height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `snapshot.pruned_height` | `uint64` / JSON decimal string | pruned height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-competitionepoch"></a> `hub.CompetitionEpoch`

- gRPC: `/hub.v1.Query/CompetitionEpoch`
- REST: `GET /TrueOpen/hub/v1/competition_epoch/{reward_bucket}/{epoch}`
- Request/response: `hub.v1.QueryCompetitionEpochRequest` -> `hub.v1.QueryCompetitionEpochResponse`
- Purpose: Reads the authoritative on-chain state behind `CompetitionEpoch`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `reward_bucket` | `hub.v1.RewardBucket` enum | reward bucket; this field is part of the protocol state or of the request. | Computed from on-chain parameters, from the quote/order, or from the local balance budget; integers travel as decimal strings in JSON. | Must be a legal non-negative Coin/uint64 with a consistent denom and no overflow, and must satisfy the minimum bond, the available balance, the outstanding liabilities and the module budget limits. |
| `epoch` | `uint64` / JSON decimal string | epoch; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `epoch_state` | `hub.v1.RewardCompetitionEpochState` object | epoch state; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `epoch_state.epoch` | `uint64` / JSON decimal string | epoch; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `epoch_state.reward_bucket` | `hub.v1.RewardBucket` enum | reward bucket; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `epoch_state.order_value_hist` | array<`uint64` / JSON decimal string> | order value hist list; the semantics of an element are given by its message/enum definition. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once. |
| `epoch_state.order_value_bucket_boundaries_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | order value bucket boundaries hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `epoch_state.total_tasks` | `uint64` / JSON decimal string | total tasks; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `epoch_state.unique_payer_count` | `uint64` / JSON decimal string | unique payer count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `epoch_state.unique_worker_count` | `uint64` / JSON decimal string | unique worker count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `epoch_state.cumulative_fee_amount` | `shared.v1.Amount` object | cumulative fee amount; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `epoch_state.bootstrap_status` | `hub.v1.RewardCompetitionBootstrapStatus` enum | bootstrap status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `epoch_state.p30_cutoff` | `shared.v1.Amount` object | p30 cutoff; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `epoch_state.reward_epoch_closed` | `bool` | reward epoch closed; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-cortexnode"></a> `hub.CortexNode`

- gRPC: `/hub.v1.Query/CortexNode`
- REST: `GET /TrueOpen/hub/v1/cortex_node/{operator_address}`
- Request/response: `hub.v1.QueryCortexNodeRequest` -> `hub.v1.QueryCortexNodeResponse`
- Purpose: Reads the authoritative on-chain state behind `CortexNode`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Taken from the Cortex Node identity or from the on-chain ModelCapability/ModelSupport state. | Must be a canonical Bech32 address whose node has a valid model and profile capability/support status. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `node` | `hub.v1.CortexNodeState` object | node; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `node.schema_version` | `uint32` | schema version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `node.operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `node.current_service_address` | `string` | current service address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `node.current_service_pubkey` | `bytes` / base64 string | 33-byte compressed secp256k1 service key, not a digest; REST uses Base64. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `node.service_authorization_nonce` | `uint64` / JSON decimal string | service authorization nonce; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `node.service_key_status` | `hub.v1.ServiceKeyStatus` enum | service key status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `node.current_descriptor_version` | `uint64` / JSON decimal string | current descriptor version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `node.registered_height` | `uint64` / JSON decimal string | registered height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `node.updated_height` | `uint64` / JSON decimal string | updated height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `node.active_task_liability_count` | `uint32` | active task liability count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `node.pending_stage_duty_count` | `uint32` | pending stage duty count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `node.pending_evidence_submission_count` | `uint32` | pending evidence submission count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-currentcandidatepool"></a> `hub.CurrentCandidatePool`

- gRPC: `/hub.v1.Query/CurrentCandidatePool`
- REST: `GET /TrueOpen/hub/v1/candidate_pool/current`
- Request/response: `hub.v1.QueryCurrentCandidatePoolRequest` -> `hub.v1.QueryCurrentCandidatePoolResponse`
- Purpose: Reads the authoritative on-chain state behind `CurrentCandidatePool`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| - | empty message | no parameters | send an empty object `{}` | no business field validation |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `snapshot` | `hub.v1.CandidatePoolSnapshotViewV1` object | snapshot; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `snapshot.epoch` | `uint64` / JSON decimal string | epoch; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `snapshot.snapshot_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | snapshot id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `snapshot.pool_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | pool hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `snapshot.status` | `hub.v1.CandidatePoolSnapshotStatus` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `snapshot.slot_capacity` | `uint32` | slot capacity; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `snapshot.active_count` | `uint32` | active count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `snapshot.active_bitmap_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | active bitmap hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `snapshot.member_set_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | member set hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `snapshot.published_height` | `uint64` / JSON decimal string | published height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `snapshot.effective_height` | `uint64` / JSON decimal string | effective height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `snapshot.expires_height` | `uint64` / JSON decimal string | expires height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `snapshot.pruned_height` | `uint64` / JSON decimal string | pruned height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-currentservicekey"></a> `hub.CurrentServiceKey`

- gRPC: `/hub.v1.Query/CurrentServiceKey`
- REST: `GET /TrueOpen/hub/v1/current_service_key/{participant_type}/{operator_address}`
- Request/response: `hub.v1.QueryCurrentServiceKeyRequest` -> `hub.v1.QueryCurrentServiceKeyResponse`
- Purpose: Reads the authoritative on-chain state behind `CurrentServiceKey`, for building the next stage's request, for auditing, or for recovery.
- Stage: The Cortex/service identity, bonding and model capability preparation stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `participant_type` | `shared.v1.ParticipantType` enum | participant type; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Taken from the Cortex Node identity or from the on-chain ModelCapability/ModelSupport state. | Must be a canonical Bech32 address whose node has a valid model and profile capability/support status. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `binding` | `hub.v1.CurrentServiceKeyViewV1` object | binding; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `binding.participant_type` | `shared.v1.ParticipantType` enum | participant type; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `binding.operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `binding.service_address` | `string` | service address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `binding.service_pubkey` | `bytes` / base64 string | 33-byte compressed secp256k1 service key, not a digest; REST uses Base64. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `binding.service_authorization_nonce` | `uint64` / JSON decimal string | service authorization nonce; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `binding.cortex_service_key_status` | `hub.v1.ServiceKeyStatus` enum | cortex service key status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `binding.builder_service_key_status` | `hub.v1.ServiceKeyStatus` enum | builder service key status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `binding.current_descriptor_version` | `uint64` / JSON decimal string | current descriptor version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-dailysupport"></a> `hub.DailySupport`

- gRPC: `/hub.v1.Query/DailySupport`
- REST: `GET /TrueOpen/hub/v1/daily_support/{operator_address}/{epoch}`
- Request/response: `hub.v1.QueryDailySupportRequest` -> `hub.v1.QueryDailySupportResponse`
- Purpose: Reads the authoritative on-chain state behind `DailySupport`, for building the next stage's request, for auditing, or for recovery.
- Stage: The Cortex/service identity, bonding and model capability preparation stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Taken from the Cortex Node identity or from the on-chain ModelCapability/ModelSupport state. | Must be a canonical Bech32 address whose node has a valid model and profile capability/support status. |
| `epoch` | `uint64` / JSON decimal string | epoch; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `support` | `hub.v1.DailySupportState` object | support; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `support.epoch` | `uint64` / JSON decimal string | epoch; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `support.operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `support.supported_profiles_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | supported profiles hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `support.signature_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | SHA256(raw_signature_64), retained as a 32-byte audit/exact-replay fingerprint. The 64-byte signature was verified before initial live acceptance and is not retained. This digest is not authorization state and never enters a signing/business digest; replay compares it only to identify the same previously accepted signature. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `support.accepted_height` | `uint64` / JSON decimal string | accepted height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-earnings"></a> `hub.Earnings`

- gRPC: `/hub.v1.Query/Earnings`
- REST: `GET /TrueOpen/hub/v1/earnings/{address}`
- Request/response: `hub.v1.QueryEarningsRequest` -> `hub.v1.QueryEarningsResponse`
- Purpose: Reads the authoritative on-chain state behind `Earnings`, for building the next stage's request, for auditing, or for recovery.
- Stage: The earnings finalization, claim or epoch fund allocation stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `address` | `string` | address; this field is part of the protocol state or of the request. | Use the TrueOpen Bech32 address of the transaction signing account; in a Query it comes from the target account or from preceding state. | Must be a parseable, canonical account address; the Msg signer must match both the field and ownership of the resource. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `earnings` | `hub.v1.EarningsState` object | earnings; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `earnings.address` | `string` | address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `earnings.claimable_task_fee` | `shared.v1.Amount` object | claimable task fee; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `earnings.claimable_service_reward` | `shared.v1.Amount` object | claimable service reward; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `earnings.claimable_builder_reward` | `shared.v1.Amount` object | claimable builder reward; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `earnings.claimable_amount` | `shared.v1.Amount` object | claimable amount; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `earnings.earnings_version` | `uint64` / JSON decimal string | earnings version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `earnings.last_updated_height` | `uint64` / JSON decimal string | last updated height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-emergencyfreezevotes"></a> `hub.EmergencyFreezeVotes`

- gRPC: `/hub.v1.Query/EmergencyFreezeVotes`
- REST: `GET /TrueOpen/hub/v1/emergency_freeze_votes/{freeze_signal_id}`
- Request/response: `hub.v1.QueryEmergencyFreezeVotesRequest` -> `hub.v1.QueryEmergencyFreezeVotesResponse`
- Purpose: Reads the authoritative on-chain state behind `EmergencyFreezeVotes`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `freeze_signal_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | freeze signal id; this field is part of the protocol state or of the request. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `page` | `shared.v1.QueryPageRequestV1` object | The batch page or the pagination cursor. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `page.page_token` | `bytes` / base64 string | page_token is empty on the first page and otherwise a canonical PageTokenV1. It is an opaque variable-length cursor blob, not a Hash32, so REST projects it as ProtoJSON Base64. | Encoded deterministically according to the corresponding protocol structure; REST/JSON uses base64. | Checked for non-emptiness/length/ceiling, plus hash, signature, Merkle proof or canonical encoding verification where applicable. |
| `page.limit` | `uint32` | The ceiling on a single query/processing pass, which prevents unbounded gas and responses. | Taken from the Query Params / operational batch configuration; choose a positive integer no larger than the on-chain hard ceiling. | Must be within the permitted range; Node additionally takes the smaller of the parameter ceiling and the requested value, and neither zero nor an oversized value may be used to trigger a full scan. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `votes` | array<`hub.v1.EmergencyFreezeVoteState` object> | votes list; the semantics of an element are given by its message/enum definition. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once. |
| `page` | `shared.v1.QueryPageResponseV1` object | The batch page or the pagination cursor. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `page.next_page_token` | `bytes` / base64 string | next_page_token is empty when the walk is complete. Same opaque PageTokenV1 blob as page_token, so REST projects it as ProtoJSON Base64. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-fault"></a> `hub.Fault`

- gRPC: `/hub.v1.Query/Fault`
- REST: `GET /TrueOpen/hub/v1/fault/{fault_id}`
- Request/response: `hub.v1.QueryFaultRequest` -> `hub.v1.QueryFaultResponse`
- Purpose: Reads the authoritative on-chain state behind `Fault`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `fault_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | fault id; this field is part of the protocol state or of the request. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `fault` | `hub.v1.RoleFaultState` object | fault; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `fault.fault_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | fault id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `fault.task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `fault.operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `fault.duty` | `shared.v1.Duty` enum | The Cortex service duty domain (Worker/Verifier), which is also part of the service key verification domain. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `fault.fault_class` | `hub.v1.FaultKind` enum | fault class; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `fault.classification_source` | `shared.v1.FailureClassificationSource` enum | classification source; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `fault.evidence_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | evidence digest; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `fault.jail_delta` | `uint32` | jail delta; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `fault.slash_summary_id` | optional `Hash32` / omitted when absent; lowercase 64-hex client value when present (raw 32-byte protobuf) | slash summary id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | An absent value must be omitted; a present value must satisfy the bytes encoding and width the descriptor declares, and null or empty bytes must not be used to stand in for absence. |
| `fault.recorded_height` | `uint64` / JSON decimal string | recorded height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `fault.status` | `hub.v1.RoleFaultStatus` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `slash_summary` | `hub.v1.SlashSummaryState` object | slash summary; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `slash_summary.slash_summary_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | slash summary id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `slash_summary.source_kind` | `hub.v1.SlashSourceKind` enum | source kind; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `slash_summary.source_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | Raw Hash32 in both namespaces: the fault_id for ROLE_FAULT, the challenge_id for CHALLENGE_EFFECT. Never a hex projection. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `slash_summary.effect_index` | `uint64` / JSON decimal string | effect index; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `slash_summary.operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `slash_summary.duty` | `shared.v1.Duty` enum | The Cortex service duty domain (Worker/Verifier), which is also part of the service key verification domain. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `slash_summary.task_id` | optional `Hash32` / omitted when absent; lowercase 64-hex client value when present (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | An absent value must be omitted; a present value must satisfy the bytes encoding and width the descriptor declares, and null or empty bytes must not be used to stand in for absence. |
| `slash_summary.requested_amount` | `shared.v1.Amount` object | requested amount; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `slash_summary.pending_task_earnings_debit` | `shared.v1.Amount` object | pending task earnings debit; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `slash_summary.active_bond_debit` | `shared.v1.Amount` object | active bond debit; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `slash_summary.unbonding_debit` | `shared.v1.Amount` object | unbonding debit; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `slash_summary.claimable_earnings_debit` | `shared.v1.Amount` object | claimable earnings debit; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `slash_summary.applied_amount` | `shared.v1.Amount` object | applied amount; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `slash_summary.unfilled_amount` | `shared.v1.Amount` object | unfilled amount; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `slash_summary.destination` | `hub.v1.SlashDestination` enum | destination; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `slash_summary.unbonding_rows_visited` | `uint32` | unbonding rows visited; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `slash_summary.applied_height` | `uint64` / JSON decimal string | applied height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `slash_summary.bond_version` | `uint64` / JSON decimal string | bond version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-freezesignal"></a> `hub.FreezeSignal`

- gRPC: `/hub.v1.Query/FreezeSignal`
- REST: `GET /TrueOpen/hub/v1/freeze_signal/{freeze_signal_id}`
- Request/response: `hub.v1.QueryFreezeSignalRequest` -> `hub.v1.QueryFreezeSignalResponse`
- Purpose: Reads the authoritative on-chain state behind `FreezeSignal`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `freeze_signal_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | freeze signal id; this field is part of the protocol state or of the request. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `signal` | `hub.v1.FreezeSignalState` object | signal; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `signal.freeze_signal_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | freeze signal id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `signal.model_id` | `string` | The canonical identifier of a model registered with the Hub. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `signal.profile_version` | `uint32` | The frozen version of a model's Integration Profile. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `signal.risk_window_id` | `uint64` / JSON decimal string | risk window id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `signal.risk_window_start_height` | `uint64` / JSON decimal string | risk window start height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `signal.risk_window_end_height` | `uint64` / JSON decimal string | risk window end height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `signal.included_failure_task_ref_count` | `uint32` | included failure task ref count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `signal.included_failure_task_refs_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | included failure task refs hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `signal.included_failure_class_counts` | `hub.v1.FreezeFailureClassCountsV1` object | included failure class counts; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `signal.excluded_insufficient_verifier_count` | `uint32` | excluded insufficient verifier count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `signal.accepted_voting_power` | `uint64` / JSON decimal string | accepted voting power; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `signal.rejected_voting_power` | `uint64` / JSON decimal string | rejected voting power; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `signal.total_voting_power_snapshot` | `uint64` / JSON decimal string | total voting power snapshot; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `signal.signal_status` | `hub.v1.FreezeSignalStatus` enum | signal status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `signal.validator_snapshot_height` | `uint64` / JSON decimal string | validator snapshot height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `signal.validator_set_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | validator set hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `signal.created_height` | `uint64` / JSON decimal string | created height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `signal.vote_deadline_height` | `uint64` / JSON decimal string | vote deadline height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `signal.closed_height` | `uint64` / JSON decimal string | closed height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-freezesignals"></a> `hub.FreezeSignals`

- gRPC: `/hub.v1.Query/FreezeSignals`
- REST: `GET /TrueOpen/hub/v1/freeze_signals/{model_id}/{profile_version}/{status}`
- Request/response: `hub.v1.QueryFreezeSignalsRequest` -> `hub.v1.QueryFreezeSignalsResponse`
- Purpose: Reads the authoritative on-chain state behind `FreezeSignals`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `model_id` | `string` | The canonical identifier of a model registered with the Hub. | Taken from the Hub Model registry or from a Models/Profiles list query, preserving the exact canonical ID used at registration. | Must match `^[a-z0-9][a-z0-9_-]{0,127}$`, remain a single URL path segment and a single NATS subject token, and reference a registered model. |
| `profile_version` | `uint32` | The frozen version of a model's Integration Profile. | Read the frozen version returned by a preceding Query; when creating a new version, increment monotonically from the existing maximum. | The version must be non-zero (except on compatibility paths where the protocol permits zero), and a referenced version must exist and agree with the task snapshot. |
| `status` | `hub.v1.FreezeSignalStatus` enum | status; this field is part of the protocol state or of the request. | Use the numeric value / name defined by the proto enum; pick it from the preceding state machine stage and never use an unrecognized custom value. | Must be a known enum value permitted by this RPC and must satisfy the current state transition; UNSPECIFIED is normally rejected. |
| `page` | `shared.v1.QueryPageRequestV1` object | The batch page or the pagination cursor. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `page.page_token` | `bytes` / base64 string | page_token is empty on the first page and otherwise a canonical PageTokenV1. It is an opaque variable-length cursor blob, not a Hash32, so REST projects it as ProtoJSON Base64. | Encoded deterministically according to the corresponding protocol structure; REST/JSON uses base64. | Checked for non-emptiness/length/ceiling, plus hash, signature, Merkle proof or canonical encoding verification where applicable. |
| `page.limit` | `uint32` | The ceiling on a single query/processing pass, which prevents unbounded gas and responses. | Taken from the Query Params / operational batch configuration; choose a positive integer no larger than the on-chain hard ceiling. | Must be within the permitted range; Node additionally takes the smaller of the parameter ceiling and the requested value, and neither zero nor an oversized value may be used to trigger a full scan. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `signals` | array<`hub.v1.FreezeSignalState` object> | signals list; the semantics of an element are given by its message/enum definition. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once. |
| `page` | `shared.v1.QueryPageResponseV1` object | The batch page or the pagination cursor. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `page.next_page_token` | `bytes` / base64 string | next_page_token is empty when the walk is complete. Same opaque PageTokenV1 blob as page_token, so REST projects it as ProtoJSON Base64. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-model"></a> `hub.Model`

- gRPC: `/hub.v1.Query/Model`
- REST: `GET /TrueOpen/hub/v1/model/{model_id}`
- Request/response: `hub.v1.QueryModelRequest` -> `hub.v1.QueryModelResponse`
- Purpose: Reads the authoritative on-chain state behind `Model`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `model_id` | `string` | The canonical identifier of a model registered with the Hub. | Taken from the Hub Model registry or from a Models/Profiles list query, preserving the exact canonical ID used at registration. | Must match `^[a-z0-9][a-z0-9_-]{0,127}$`, remain a single URL path segment and a single NATS subject token, and reference a registered model. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `model` | `hub.v1.ModelState` object | model; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `model.model_id` | `string` | The canonical identifier of a model registered with the Hub. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `model.proposer_address` | `string` | proposer address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `model.status` | `hub.v1.ModelProfileStatus` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `model.active_profile_count` | `uint32` | active profile count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `model.latest_profile_version` | `uint32` | latest profile version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `model.status_source` | `hub.v1.ModelStatusSource` enum | status source; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `model.registration_fee_paid` | `uint64` / JSON decimal string | registration fee paid; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `model.created_height` | `uint64` / JSON decimal string | created height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `model.updated_height` | `uint64` / JSON decimal string | updated height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-modelsupport"></a> `hub.ModelSupport`

- gRPC: `/hub.v1.Query/ModelSupport`
- REST: `GET /TrueOpen/hub/v1/model_support/{operator_address}/{model_id}/{profile_version}`
- Request/response: `hub.v1.QueryModelSupportRequest` -> `hub.v1.QueryModelSupportResponse`
- Purpose: Reads the authoritative on-chain state behind `ModelSupport`, for building the next stage's request, for auditing, or for recovery.
- Stage: The Cortex/service identity, bonding and model capability preparation stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Taken from the Cortex Node identity or from the on-chain ModelCapability/ModelSupport state. | Must be a canonical Bech32 address whose node has a valid model and profile capability/support status. |
| `model_id` | `string` | The canonical identifier of a model registered with the Hub. | Taken from the Hub Model registry or from a Models/Profiles list query, preserving the exact canonical ID used at registration. | Must match `^[a-z0-9][a-z0-9_-]{0,127}$`, remain a single URL path segment and a single NATS subject token, and reference a registered model. |
| `profile_version` | `uint32` | The frozen version of a model's Integration Profile. | Read the frozen version returned by a preceding Query; when creating a new version, increment monotonically from the existing maximum. | The version must be non-zero (except on compatibility paths where the protocol permits zero), and a referenced version must exist and agree with the task snapshot. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `support` | `hub.v1.ModelSupportState` object | support; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `support.operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `support.model_id` | `string` | The canonical identifier of a model registered with the Hub. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `support.profile_version` | `uint32` | The frozen version of a model's Integration Profile. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `support.declared_support` | `bool` | declared support; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `support.support_active` | `bool` | support active; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `support.activation_kind` | `hub.v1.ModelSupportActivationKind` enum | activation kind; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `support.first_activation_duty` | `shared.v1.Duty` enum | first activation duty; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `support.first_support_task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | first support task id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `support.first_support_order_value` | `uint64` / JSON decimal string | first support order value; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `support.p30_cutoff_epoch` | `uint64` / JSON decimal string | p30 cutoff epoch; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `support.p30_bootstrap` | `bool` | p30 bootstrap; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `support.support_fresh_until_epoch` | `uint64` / JSON decimal string | support fresh until epoch; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `support.last_refresh_task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | last refresh task id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `support.last_refresh_height` | `uint64` / JSON decimal string | last refresh height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `support.active_support_stake_snapshot` | `uint64` / JSON decimal string | active support stake snapshot; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `support.eligible_support_stake_snapshot` | `uint64` / JSON decimal string | eligible support stake snapshot; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `support.support_version` | `uint64` / JSON decimal string | support version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-models"></a> `hub.Models`

- gRPC: `/hub.v1.Query/Models`
- REST: `GET /TrueOpen/hub/v1/models`
- Request/response: `hub.v1.QueryModelsRequest` -> `hub.v1.QueryModelsResponse`
- Purpose: Reads the authoritative on-chain state behind `Models`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `page` | `shared.v1.QueryPageRequestV1` object | The batch page or the pagination cursor. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `page.page_token` | `bytes` / base64 string | page_token is empty on the first page and otherwise a canonical PageTokenV1. It is an opaque variable-length cursor blob, not a Hash32, so REST projects it as ProtoJSON Base64. | Encoded deterministically according to the corresponding protocol structure; REST/JSON uses base64. | Checked for non-emptiness/length/ceiling, plus hash, signature, Merkle proof or canonical encoding verification where applicable. |
| `page.limit` | `uint32` | The ceiling on a single query/processing pass, which prevents unbounded gas and responses. | Taken from the Query Params / operational batch configuration; choose a positive integer no larger than the on-chain hard ceiling. | Must be within the permitted range; Node additionally takes the smaller of the parameter ceiling and the requested value, and neither zero nor an oversized value may be used to trigger a full scan. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `models` | array<`hub.v1.ModelState` object> | models list; the semantics of an element are given by its message/enum definition. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once. |
| `page` | `shared.v1.QueryPageResponseV1` object | The batch page or the pagination cursor. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `page.next_page_token` | `bytes` / base64 string | next_page_token is empty when the walk is complete. Same opaque PageTokenV1 blob as page_token, so REST projects it as ProtoJSON Base64. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-params"></a> `hub.Params`

- gRPC: `/hub.v1.Query/Params`
- REST: `GET /TrueOpen/hub/v1/params`
- Request/response: `hub.v1.QueryHubParamsRequest` -> `hub.v1.QueryHubParamsResponse`
- Purpose: Reads the authoritative on-chain state behind `Params`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| - | empty message | no parameters | send an empty object `{}` | no business field validation |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `params` | `hub.v1.HubParamsV2` object | params holds all the parameters of this module. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `params.schema_version` | `uint32` | schema version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `params.epoch` | `hub.v1.EpochParamsV1` object | epoch; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `params.support` | `hub.v1.SupportParamsV1` object | support; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `params.candidate_pool` | `hub.v1.CandidatePoolParamsV1` object | candidate pool; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `params.service` | `hub.v1.ServiceParamsV1` object | service; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `params.builder` | `hub.v1.BuilderParamsV1` object | builder; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `params.reward` | `hub.v1.RewardParamsV1` object | reward; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `params.freeze` | `hub.v1.FreezeParamsV1` object | freeze; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `params.treasury` | `hub.v1.TreasuryParamsV1` object | treasury; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `params.bucket` | `hub.v1.ParameterBucketParamsV1` object | bucket; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `params.query_event` | `hub.v1.QueryEventParamsV1` object | query event; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `params.price` | `hub.v1.PriceParamsV1` object | price; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `params.beacon` | `hub.v1.BeaconParamsV1` object | beacon; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `params.phase0` | `hub.v1.Phase0ParamsV1` object | phase0; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `params.bridge` | `hub.v1.BridgeParamsV1` object | bridge; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `meta` | `hub.v1.HubParamsMetaState` object | meta carries this module's own params version/hash/updated height. Params ownership stays per module: a client that needs a joint view calls hub.v1.Query/Params and task.v1.Query/Params at the same x-cosmos-block-height. Wire adds no app-level package importing both. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `meta.params_version` | `uint64` / JSON decimal string | params version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `meta.params_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | params hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `meta.updated_height` | `uint64` / JSON decimal string | updated height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-profile"></a> `hub.Profile`

- gRPC: `/hub.v1.Query/Profile`
- REST: `GET /TrueOpen/hub/v1/profile/{model_id}/{profile_version}`
- Request/response: `hub.v1.QueryProfileRequest` -> `hub.v1.QueryProfileResponse`
- Purpose: Reads the authoritative on-chain state behind `Profile`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `model_id` | `string` | The canonical identifier of a model registered with the Hub. | Taken from the Hub Model registry or from a Models/Profiles list query, preserving the exact canonical ID used at registration. | Must match `^[a-z0-9][a-z0-9_-]{0,127}$`, remain a single URL path segment and a single NATS subject token, and reference a registered model. |
| `profile_version` | `uint32` | The frozen version of a model's Integration Profile. | Read the frozen version returned by a preceding Query; when creating a new version, increment monotonically from the existing maximum. | The version must be non-zero (except on compatibility paths where the protocol permits zero), and a referenced version must exist and agree with the task snapshot. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `profile` | `hub.v1.ProfileState` object | profile; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `profile.model_id` | `string` | The canonical identifier of a model registered with the Hub. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `profile.profile_version` | `uint32` | The frozen version of a model's Integration Profile. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `profile.manifest_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | manifest hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `profile.tokenizer_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | tokenizer hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `profile.runtime_class` | `string` | runtime class; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `profile.required_top_k` | `uint32` | required top k; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `profile.task_types` | array<`shared.v1.TaskType` enum> | task types list; the semantics of an element are given by its message/enum definition. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once. |
| `profile.generation_type` | `shared.v1.GenerationType` enum | generation type; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `profile.resource_tier` | `uint32` | resource tier; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `profile.min_stake` | `uint64` / JSON decimal string | min stake; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `profile.challenge_open_window_blocks` | `uint64` / JSON decimal string | challenge open window blocks; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `profile.verification_profile` | `shared.v1.VerificationProfile` object | verification profile; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `profile.verification_thresholds` | `shared.v1.VerificationThresholds` object | verification thresholds; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `profile.batch_verification` | `shared.v1.BatchVerification` object | batch verification; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `profile.pricing_profile` | `shared.v1.PricingProfile` object | pricing profile; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `profile.timeout_bootstrap_profile` | `shared.v1.TimeoutBootstrapProfile` object | timeout bootstrap profile; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `profile.schema_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | schema hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `profile.status` | `hub.v1.ModelProfileStatus` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `profile.active_support_stake` | `uint64` / JSON decimal string | active support stake; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `profile.eligible_support_stake` | `uint64` / JSON decimal string | eligible support stake; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `profile.active_supporter_count` | `uint32` | active supporter count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `profile.status_source` | `hub.v1.ProfileStatusSource` enum | status source; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `profile.registration_fee_paid` | `uint64` / JSON decimal string | registration fee paid; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `profile.previous_profile_version` | `uint32` | previous profile version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `profile.proposer_address` | `string` | proposer address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `profile.registration_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | registration digest; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `profile.created_height` | `uint64` / JSON decimal string | created height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `profile.updated_height` | `uint64` / JSON decimal string | updated height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `profile.last_freeze_risk_window_evaluated` | `uint64` / JSON decimal string | last freeze risk window evaluated; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `profile.ref_price` | `uint64` / JSON decimal string | ref price; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-profilecapability"></a> `hub.ProfileCapability`

- gRPC: `/hub.v1.Query/ProfileCapability`
- REST: `GET /TrueOpen/hub/v1/profile_capability/{operator_address}/{model_id}/{profile_version}`
- Request/response: `hub.v1.QueryProfileCapabilityRequest` -> `hub.v1.QueryProfileCapabilityResponse`
- Purpose: Reads the authoritative on-chain state behind `ProfileCapability`, for building the next stage's request, for auditing, or for recovery.
- Stage: The Cortex/service identity, bonding and model capability preparation stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Taken from the Cortex Node identity or from the on-chain ModelCapability/ModelSupport state. | Must be a canonical Bech32 address whose node has a valid model and profile capability/support status. |
| `model_id` | `string` | The canonical identifier of a model registered with the Hub. | Taken from the Hub Model registry or from a Models/Profiles list query, preserving the exact canonical ID used at registration. | Must match `^[a-z0-9][a-z0-9_-]{0,127}$`, remain a single URL path segment and a single NATS subject token, and reference a registered model. |
| `profile_version` | `uint32` | The frozen version of a model's Integration Profile. | Read the frozen version returned by a preceding Query; when creating a new version, increment monotonically from the existing maximum. | The version must be non-zero (except on compatibility paths where the protocol permits zero), and a referenced version must exist and agree with the task snapshot. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `capability` | `hub.v1.ProfileCapabilityState` object | capability; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `capability.operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `capability.model_id` | `string` | The canonical identifier of a model registered with the Hub. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `capability.profile_version` | `uint32` | The frozen version of a model's Integration Profile. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `capability.inference_capability` | `bool` | inference capability; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `capability.verification_capability` | `bool` | verification capability; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `capability.capability_version` | `uint64` / JSON decimal string | capability version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-rewardepochaudit"></a> `hub.RewardEpochAudit`

- gRPC: `/hub.v1.Query/RewardEpochAudit`
- REST: `GET /TrueOpen/hub/v1/reward_epoch_audit/{source_epoch}/{reward_bucket}`
- Request/response: `hub.v1.QueryRewardEpochAuditRequest` -> `hub.v1.QueryRewardEpochAuditResponse`
- Purpose: Reads the authoritative on-chain state behind `RewardEpochAudit`, for building the next stage's request, for auditing, or for recovery.
- Stage: The earnings finalization, claim or epoch fund allocation stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `source_epoch` | `uint64` / JSON decimal string | source epoch; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `reward_bucket` | `hub.v1.RewardBucket` enum | reward bucket; this field is part of the protocol state or of the request. | Computed from on-chain parameters, from the quote/order, or from the local balance budget; integers travel as decimal strings in JSON. | Must be a legal non-negative Coin/uint64 with a consistent denom and no overflow, and must satisfy the minimum bond, the available balance, the outstanding liabilities and the module budget limits. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `audit` | `hub.v1.RewardEpochAuditState` object | audit; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `audit.source_epoch` | `uint64` / JSON decimal string | source epoch; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `audit.reward_bucket` | `hub.v1.RewardBucket` enum | reward bucket; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `audit.audit_root` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | audit root; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `audit.folded_root` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | folded root; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `audit.eligible_task_count` | `uint64` / JSON decimal string | eligible task count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `audit.mark_count` | `uint64` / JSON decimal string | mark count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `audit.accrual_count` | `uint64` / JSON decimal string | accrual count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `audit.contribution_count` | `uint64` / JSON decimal string | contribution count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `audit.total_count` | `uint64` / JSON decimal string | total count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `audit.completed_height` | `uint64` / JSON decimal string | completed height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-rewardepochcursor"></a> `hub.RewardEpochCursor`

- gRPC: `/hub.v1.Query/RewardEpochCursor`
- REST: `GET /TrueOpen/hub/v1/reward_epoch_cursor/{epoch}/{reward_bucket}`
- Request/response: `hub.v1.QueryRewardEpochCursorRequest` -> `hub.v1.QueryRewardEpochCursorResponse`
- Purpose: Reads the authoritative on-chain state behind `RewardEpochCursor`, for building the next stage's request, for auditing, or for recovery.
- Stage: The earnings finalization, claim or epoch fund allocation stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `epoch` | `uint64` / JSON decimal string | epoch; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `reward_bucket` | `hub.v1.RewardBucket` enum | reward bucket; this field is part of the protocol state or of the request. | Computed from on-chain parameters, from the quote/order, or from the local balance budget; integers travel as decimal strings in JSON. | Must be a legal non-negative Coin/uint64 with a consistent denom and no overflow, and must satisfy the minimum bond, the available balance, the outstanding liabilities and the module budget limits. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `cursor` | `hub.v1.RewardEpochProgressViewV1` object | cursor; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `cursor.epoch` | `uint64` / JSON decimal string | epoch; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `cursor.reward_bucket` | `hub.v1.RewardBucket` enum | reward bucket; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `cursor.phase` | `hub.v1.RewardEpochPhase` enum | phase; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `cursor.visited_count` | `uint64` / JSON decimal string | visited count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `cursor.applied_count` | `uint64` / JSON decimal string | applied count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-servicebond"></a> `hub.ServiceBond`

- gRPC: `/hub.v1.Query/ServiceBond`
- REST: `GET /TrueOpen/hub/v1/service_bond/{operator_address}`
- Request/response: `hub.v1.QueryServiceBondRequest` -> `hub.v1.QueryServiceBondResponse`
- Purpose: Reads the authoritative on-chain state behind `ServiceBond`, for building the next stage's request, for auditing, or for recovery.
- Stage: The Cortex/service identity, bonding and model capability preparation stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Taken from the Cortex Node identity or from the on-chain ModelCapability/ModelSupport state. | Must be a canonical Bech32 address whose node has a valid model and profile capability/support status. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `bond` | `hub.v1.ServiceBondState` object | bond; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `bond.operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `bond.active_bond` | `uint64` / JSON decimal string | active bond; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `bond.effective_active_bond` | `uint64` / JSON decimal string | effective active bond; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `bond.reserved_liability` | `uint64` / JSON decimal string | reserved liability; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `bond.pending_unbonding_total` | `uint64` / JSON decimal string | pending unbonding total; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `bond.bond_version` | `uint64` / JSON decimal string | bond version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `bond.effective_bond_epoch` | `uint64` / JSON decimal string | effective bond epoch; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `bond.status` | `hub.v1.ServiceBondStatus` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `bond.jail_count` | `uint32` | jail count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `bond.normal_action_count_since_jail` | `uint32` | normal action count since jail; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `bond.last_stake_height` | `uint64` / JSON decimal string | last stake height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `bond.last_unstake_height` | `uint64` / JSON decimal string | last unstake height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-servicedescriptor"></a> `hub.ServiceDescriptor`

- gRPC: `/hub.v1.Query/ServiceDescriptor`
- REST: `GET /TrueOpen/hub/v1/service_descriptor/{participant_type}/{operator_address}`
- Request/response: `hub.v1.QueryServiceDescriptorRequest` -> `hub.v1.QueryServiceDescriptorResponse`
- Purpose: Reads the authoritative on-chain state behind `ServiceDescriptor`, for building the next stage's request, for auditing, or for recovery.
- Stage: The Cortex/service identity, bonding and model capability preparation stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `participant_type` | `shared.v1.ParticipantType` enum | participant type; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Taken from the Cortex Node identity or from the on-chain ModelCapability/ModelSupport state. | Must be a canonical Bech32 address whose node has a valid model and profile capability/support status. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `descriptor` | `hub.v1.ServiceDescriptorState` object | descriptor; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `descriptor.participant_type` | `shared.v1.ParticipantType` enum | participant type; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `descriptor.operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `descriptor.descriptor_version` | `uint64` / JSON decimal string | The version of the service network descriptor. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `descriptor.endpoint_count` | `uint32` | endpoint count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `descriptor.endpoints` | array<`hub.v1.ServiceEndpointV1` object> | endpoints list; the semantics of an element are given by its message/enum definition. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once. |
| `descriptor.descriptor_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | descriptor hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `descriptor.updated_height` | `uint64` / JSON decimal string | updated height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-servicelifecycle"></a> `hub.ServiceLifecycle`

- gRPC: `/hub.v1.Query/ServiceLifecycle`
- REST: `GET /TrueOpen/hub/v1/service_lifecycle/{operator_address}`
- Request/response: `hub.v1.QueryServiceLifecycleRequest` -> `hub.v1.QueryServiceLifecycleResponse`
- Purpose: Reads the authoritative on-chain state behind `ServiceLifecycle`, for building the next stage's request, for auditing, or for recovery.
- Stage: The Cortex/service identity, bonding and model capability preparation stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Taken from the Cortex Node identity or from the on-chain ModelCapability/ModelSupport state. | Must be a canonical Bech32 address whose node has a valid model and profile capability/support status. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `lifecycle` | `hub.v1.ServiceLifecycleViewV1` object | lifecycle; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `lifecycle.node` | `hub.v1.CortexNodeState` object | node; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `lifecycle.bond` | `hub.v1.ServiceBondState` object | bond; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |

### <a id="rpc-hub-serviceunbondings"></a> `hub.ServiceUnbondings`

- gRPC: `/hub.v1.Query/ServiceUnbondings`
- REST: `GET /TrueOpen/hub/v1/service_unbondings/{operator_address}`
- Request/response: `hub.v1.QueryServiceUnbondingsRequest` -> `hub.v1.QueryServiceUnbondingsResponse`
- Purpose: Reads the authoritative on-chain state behind `ServiceUnbondings`, for building the next stage's request, for auditing, or for recovery.
- Stage: The Cortex/service identity, bonding and model capability preparation stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Taken from the Cortex Node identity or from the on-chain ModelCapability/ModelSupport state. | Must be a canonical Bech32 address whose node has a valid model and profile capability/support status. |
| `status` | `hub.v1.UnbondingStatus` enum | status; this field is part of the protocol state or of the request. | Use the numeric value / name defined by the proto enum; pick it from the preceding state machine stage and never use an unrecognized custom value. | Must be a known enum value permitted by this RPC and must satisfy the current state transition; UNSPECIFIED is normally rejected. |
| `page` | `shared.v1.QueryPageRequestV1` object | The batch page or the pagination cursor. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `page.page_token` | `bytes` / base64 string | page_token is empty on the first page and otherwise a canonical PageTokenV1. It is an opaque variable-length cursor blob, not a Hash32, so REST projects it as ProtoJSON Base64. | Encoded deterministically according to the corresponding protocol structure; REST/JSON uses base64. | Checked for non-emptiness/length/ceiling, plus hash, signature, Merkle proof or canonical encoding verification where applicable. |
| `page.limit` | `uint32` | The ceiling on a single query/processing pass, which prevents unbounded gas and responses. | Taken from the Query Params / operational batch configuration; choose a positive integer no larger than the on-chain hard ceiling. | Must be within the permitted range; Node additionally takes the smaller of the parameter ceiling and the requested value, and neither zero nor an oversized value may be used to trigger a full scan. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `entries` | array<`hub.v1.UnbondingState` object> | entries list; the semantics of an element are given by its message/enum definition. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once. |
| `page` | `shared.v1.QueryPageResponseV1` object | The batch page or the pagination cursor. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `page.next_page_token` | `bytes` / base64 string | next_page_token is empty when the walk is complete. Same opaque PageTokenV1 blob as page_token, so REST projects it as ProtoJSON Base64. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-timeoutbucket"></a> `hub.TimeoutBucket`

- gRPC: `/hub.v1.Query/TimeoutBucket`
- REST: `GET /TrueOpen/hub/v1/timeout_bucket/{bucket_key}`
- Request/response: `hub.v1.QueryTimeoutBucketRequest` -> `hub.v1.QueryTimeoutBucketResponse`
- Purpose: Reads the authoritative on-chain state behind `TimeoutBucket`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `bucket_key` | `string` | bucket key; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `version` | `uint64` / JSON decimal string | version; this field is part of the protocol state or of the request. | Read the frozen version returned by a preceding Query; when creating a new version, increment monotonically from the existing maximum. | The version must be non-zero (except on compatibility paths where the protocol permits zero), and a referenced version must exist and agree with the task snapshot. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `bucket` | `hub.v1.ParameterBucketVersionViewV1` object | bucket; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `bucket.bucket_kind` | `shared.v1.BucketKind` enum | bucket kind; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `bucket.bucket_key` | `string` | bucket key; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `bucket.version` | `uint64` / JSON decimal string | version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `bucket.schema_version` | `uint32` | schema version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `bucket.effective_height` | `uint64` / JSON decimal string | effective height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `bucket.bucket_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | bucket hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `bucket.timeout_entries` | `hub.v1.TimeoutBucketEntriesV1` object | timeout entries; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `bucket.entry_count` | `uint32` | entry count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `bucket.encoded_size_bytes` | `uint32` | encoded size bytes; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `current_version` | `uint64` / JSON decimal string | current version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `pending_version` | `uint64` / JSON decimal string | pending version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-treasury"></a> `hub.Treasury`

- gRPC: `/hub.v1.Query/Treasury`
- REST: `GET /TrueOpen/hub/v1/treasury`
- Request/response: `hub.v1.QueryTreasuryRequest` -> `hub.v1.QueryTreasuryResponse`
- Purpose: Reads the authoritative on-chain state behind `Treasury`, for building the next stage's request, for auditing, or for recovery.
- Stage: The earnings finalization, claim or epoch fund allocation stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| - | empty message | no parameters | send an empty object `{}` | no business field validation |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `treasury` | `hub.v1.TreasuryState` object | treasury; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `treasury.balance` | `shared.v1.Amount` object | balance; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `treasury.treasury_version` | `uint64` / JSON decimal string | treasury version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-validatorbridgesigner"></a> `hub.ValidatorBridgeSigner`

- gRPC: `/hub.v1.Query/ValidatorBridgeSigner`
- REST: `GET /TrueOpen/hub/v1/bridge/validator_signer/{operator_address}`
- Request/response: `hub.v1.QueryValidatorBridgeSignerRequest` -> `hub.v1.QueryValidatorBridgeSignerResponse`
- Purpose: Reads the authoritative on-chain state behind `ValidatorBridgeSigner`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Taken from the Cortex Node identity or from the on-chain ModelCapability/ModelSupport state. | Must be a canonical Bech32 address whose node has a valid model and profile capability/support status. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `signer` | `hub.v1.ValidatorBridgeSignerViewV1` object | signer; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `signer.operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `signer.bridge_signer_address_raw20` | `bytes` / base64 string | bridge signer address raw20; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `signer.key_version` | `uint64` / JSON decimal string | key version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `signer.registered_height` | `uint64` / JSON decimal string | registered height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-hub-vrfkey"></a> `hub.VrfKey`

- gRPC: `/hub.v1.Query/VrfKey`
- REST: `GET /TrueOpen/hub/v1/vrf_key/{operator_address}`
- Request/response: `hub.v1.QueryVrfKeyRequest` -> `hub.v1.QueryVrfKeyResponse`
- Purpose: Reads the authoritative on-chain state behind `VrfKey`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Taken from the Cortex Node identity or from the on-chain ModelCapability/ModelSupport state. | Must be a canonical Bech32 address whose node has a valid model and profile capability/support status. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `active_vrf_pubkey` | `bytes` / base64 string | active vrf pubkey; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `active_from_epoch` | `uint64` / JSON decimal string | active from epoch; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `pending_vrf_pubkey` | `bytes` / base64 string | pending vrf pubkey; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | An absent value must be omitted; a present value must satisfy the bytes encoding and width the descriptor declares, and null or empty bytes must not be used to stand in for absence. |
| `pending_from_epoch` | `uint64` / JSON decimal string | pending from epoch; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `vrf_authorization_nonce` | `uint64` / JSON decimal string | vrf authorization nonce; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-batchsubmitverifycommit"></a> `task.BatchSubmitVerifyCommit`

- gRPC: `/task.v1.Msg/BatchSubmitVerifyCommit`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `task.v1.MsgBatchSubmitVerifyCommit` -> `task.v1.MsgBatchSubmitVerifyCommitResponse`
- Purpose: Performs the `BatchSubmitVerifyCommit` state transition. The current state must be read before calling; a retry must not be treated as naturally idempotent.
- Stage: Stage2: inference receipt, verification commit/result/reveal.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `commits` | array<`task.v1.VerifyCommitV1` object> | commits list; the semantics of an element are given by its message/enum definition. | Computed with SHA-256 / the designated hash algorithm over the canonical bytes the protocol prescribes; the original preimage must be retained for reveal and auditing. | Checked for fixed length / non-emptiness, domain separation, and agreement with the frozen on-chain commitment; recomputed and compared at reveal time. |
| `submitter_address` | `string` | submitter address; this field is part of the protocol state or of the request. | Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state. | Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `results` | array<`task.v1.BatchItemResultV1` object> | results list; the semantics of an element are given by its message/enum definition. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once. |
| `batch_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | batch digest; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-batchsubmitverifyresult"></a> `task.BatchSubmitVerifyResult`

- gRPC: `/task.v1.Msg/BatchSubmitVerifyResult`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `task.v1.MsgBatchSubmitVerifyResult` -> `task.v1.MsgBatchSubmitVerifyResultResponse`
- Purpose: Performs the `BatchSubmitVerifyResult` state transition. The current state must be read before calling; a retry must not be treated as naturally idempotent.
- Stage: Stage2: inference receipt, verification commit/result/reveal.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `receipts` | array<`task.v1.ResultReceiptV2` object> | receipts list; the semantics of an element are given by its message/enum definition. | Built from a preceding Query / frozen set or from the local batch queue; deduplicate and fix the ordering before submitting. | Bounded by the on-chain maximum count and the transaction size; validated item by item, with batch endpoints failing atomically or in isolation as their implementation defines. |
| `submitter_address` | `string` | submitter address; this field is part of the protocol state or of the request. | Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state. | Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `results` | array<`task.v1.BatchItemResultV1` object> | results list; the semantics of an element are given by its message/enum definition. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once. |
| `batch_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | batch digest; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-cancelorder"></a> `task.CancelOrder`

- gRPC: `/task.v1.Msg/CancelOrder`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `task.v1.MsgCancelOrder` -> `task.v1.MsgCancelOrderResponse`
- Purpose: Cancels an order that has not been executed yet, in a stage that permits it, and releases the corresponding budget.
- Stage: The user session and order lifecycle stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `session_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The global identifier of a task session. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `order_sequence` | `uint64` / JSON decimal string | order sequence; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `signer_address` | `string` | signer address; this field is part of the protocol state or of the request. | Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state. | Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `cancelled_sequence` | `uint64` / JSON decimal string | cancelled sequence; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `next_expected_sequence` | `uint64` / JSON decimal string | next expected sequence; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-createsession"></a> `task.CreateSession`

- gRPC: `/task.v1.Msg/CreateSession`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `task.v1.MsgCreateSession` -> `task.v1.MsgCreateSessionResponse`
- Purpose: A user creates a task session carrying escrow and a nonce.
- Stage: The user session and order lifecycle stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `signer_address` | `string` | signer address; this field is part of the protocol state or of the request. | Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state. | Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `session_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The global identifier of a task session. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `session_nonce` | `uint64` / JSON decimal string | session nonce; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-openchallengeround"></a> `task.OpenChallengeRound`

- gRPC: `/task.v1.Msg/OpenChallengeRound`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `task.v1.MsgOpenChallengeRound` -> `task.v1.MsgOpenChallengeRoundResponse`
- Purpose: Performs the `OpenChallengeRound` state transition. The current state must be read before calling; a retry must not be treated as naturally idempotent.
- Stage: The challenge, evidence-retention and dispute adjudication stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `max_total_debit` | `shared.v1.Amount` object | max total debit; this field is part of the protocol state or of the request. | Taken from the Query Params / operational batch configuration; choose a positive integer no larger than the on-chain hard ceiling. | Must be within the permitted range; Node additionally takes the smaller of the parameter ceiling and the requested value, and neither zero nor an oversized value may be used to trigger a full scan. |
| `max_total_debit.atomic_units` | `string` | atomic units; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `opener_address` | `string` | opener address; this field is part of the protocol state or of the request. | Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state. | Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `verify_round` | `uint32` | The verification round; read from the OpenVerify/VerifierAssignment state. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `round_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | round id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `funding` | `shared.v1.RoundFundingQuoteV1` object | funding; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `funding.task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `funding.verify_round` | `uint32` | The verification round; read from the OpenVerify/VerifierAssignment state. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `funding.challenge_open_bond` | `shared.v1.Amount` object | challenge open bond; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `funding.challenge_slot_fee` | `shared.v1.Amount` object | challenge slot fee; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `funding.challenge_verifier_count` | `uint32` | challenge verifier count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `funding.verifier_budget` | `shared.v1.Amount` object | verifier budget; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `funding.total_lock` | `shared.v1.Amount` object | total lock; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-reportdataunavailable"></a> `task.ReportDataUnavailable`

- gRPC: `/task.v1.Msg/ReportDataUnavailable`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `task.v1.MsgReportDataUnavailable` -> `task.v1.MsgReportDataUnavailableResponse`
- Purpose: Performs the `ReportDataUnavailable` state transition. The current state must be read before calling; a retry must not be treated as naturally idempotent.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `verify_round` | `uint32` | The verification round; read from the OpenVerify/VerifierAssignment state. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `unavailable_task_builder_bitmap` | `bytes` / base64 string | unavailable task builder bitmap; this field is part of the protocol state or of the request. | Encoded deterministically according to the corresponding protocol structure; REST/JSON uses base64. | Checked for non-emptiness/length/ceiling, plus hash, signature, Merkle proof or canonical encoding verification where applicable. |
| `submitter_address` | `string` | submitter address; this field is part of the protocol state or of the request. | Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state. | Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `report_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | report digest; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-settletask"></a> `task.SettleTask`

- gRPC: `/task.v1.Msg/SettleTask`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `task.v1.MsgSettleTask` -> `task.v1.MsgSettleTaskResponse`
- Purpose: Performs the `SettleTask` state transition. The current state must be read before calling; a retry must not be treated as naturally idempotent.
- Stage: Stage3: light settlement, fund conservation and finality.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `submitter_address` | `string` | submitter address; this field is part of the protocol state or of the request. | Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state. | Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `settlement_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The identifier of a task settlement record. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `settlement_facts_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | settlement facts hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `settlement_plan_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | settlement plan hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `verdict` | `task.v1.TaskVerdict` enum | verdict; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `refund_amount` | `shared.v1.Amount` object | refund amount; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `refund_amount.atomic_units` | `string` | atomic units; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `settlement_height` | `uint64` / JSON decimal string | settlement height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `task_finality_height` | `uint64` / JSON decimal string | task finality height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-submitbuilderevidence"></a> `task.SubmitBuilderEvidence`

- gRPC: `/task.v1.Msg/SubmitBuilderEvidence`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `task.v1.MsgSubmitBuilderEvidence` -> `shared.v1.BuilderObjectiveEvidenceReceiptV2`
- Purpose: Performs the `SubmitBuilderEvidence` state transition. The current state must be read before calling; a retry must not be treated as naturally idempotent.
- Stage: The challenge, evidence-retention and dispute adjudication stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `evidence_bytes` | `bytes` / base64 string | evidence bytes; this field is part of the protocol state or of the request. | Encoded deterministically according to the corresponding protocol structure; REST/JSON uses base64. | Checked for non-emptiness/length/ceiling, plus hash, signature, Merkle proof or canonical encoding verification where applicable. |
| `submitter_address` | `string` | submitter address; this field is part of the protocol state or of the request. | Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state. | Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `evidence_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | H_FIELDS_V1("TRUEOPEN_BUILDER_EVIDENCE_ID_V2", ...) recomputed by the Hub, raw32. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `fault_id` | optional `Hash32` / omitted when absent; lowercase 64-hex client value when present (raw 32-byte protobuf) | H_FIELDS_V1("TRUEOPEN_BUILDER_FAULT_V2", ...) recomputed by the Hub, raw32; absent when the accepted evidence created no fault row. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | An absent value must be omitted; a present value must satisfy the bytes encoding and width the descriptor declares, and null or empty bytes must not be used to stand in for absence. |
| `status` | `shared.v1.MutationStatusV1` enum | APPLIED on first acceptance, NOOP on exact replay of the same ID and digest. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-submitinferreceipt"></a> `task.SubmitInferReceipt`

- gRPC: `/task.v1.Msg/SubmitInferReceipt`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `task.v1.MsgSubmitInferReceipt` -> `task.v1.MsgSubmitInferReceiptResponse`
- Purpose: Performs the `SubmitInferReceipt` state transition. The current state must be read before calling; a retry must not be treated as naturally idempotent.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `receipt` | `task.v1.InferReceiptV2` object | receipt; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `receipt.schema_version` | `uint32` | Always 2 in the fresh Phase 0 contract. | Read the frozen version returned by a preceding Query; when creating a new version, increment monotonically from the existing maximum. | The version must be non-zero (except on compatibility paths where the protocol permits zero), and a referenced version must exist and agree with the task snapshot. |
| `receipt.chain_id` | `string` | chain id; this field is part of the protocol state or of the request. | Obtained from the successful response, the event or the corresponding Query that created the object, then persisted. | Must be non-empty, length-bounded and reference an existing object; the creation path also checks uniqueness. |
| `receipt.task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `receipt.task_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | task hash; this field is part of the protocol state or of the request. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `receipt.worker_operator_address` | `string` | worker operator address; this field is part of the protocol state or of the request. | Taken from the Worker identity frozen in the Assignment/InferReceipt. | Must agree with the task's accepted Worker and must satisfy the Worker duty service key signature check. |
| `receipt.service_authorization_nonce` | `uint64` / JSON decimal string | service authorization nonce; this field is part of the protocol state or of the request. | Query SessionNonce / the account sequence before calling, or derive it from the deterministic nonce generation rule of the protocol message to be signed. | Must be exactly the nonce expected on chain and may be consumed only once, which prevents replay. |
| `receipt.generation_params_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | generation params digest; this field is part of the protocol state or of the request. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `receipt.output_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The hash commitment over the canonical bytes of the Cortex output. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `receipt.output_size_bytes` | `uint64` / JSON decimal string | output size bytes; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `receipt.required_evidence_commitments` | array<`task.v1.EvidenceCommitmentV1` object> | required evidence commitments list; the semantics of an element are given by its message/enum definition. | Computed with SHA-256 / the designated hash algorithm over the canonical bytes the protocol prescribes; the original preimage must be retained for reveal and auditing. | Checked for fixed length / non-emptiness, domain separation, and agreement with the frozen on-chain commitment; recomputed and compared at reveal time. |
| `receipt.expiry_height` | `uint64` / JSON decimal string | expiry height; this field is part of the protocol state or of the request. | Read the frozen height from the corresponding Query/event; relative windows are computed from on-chain params and must never use local wall-clock time. | Must fall inside the window the current stage permits; too early, past the deadline, a height that moves backwards, or an overflowing computation are all rejected. |
| `receipt.service_signature` | `bytes` / base64 string | service signature; this field is part of the protocol state or of the request. | Use the current service private key of the corresponding operator/duty to sign the canonical, domain-separated protocol bytes. | Verified only against the operator's sole current on-chain ServiceKey and the duty domain; an empty signature, a wrong domain, an old key, or a replay are all rejected. |
| `receipt.generated_token_count` | `uint64` / JSON decimal string | Worker-signed generated output token count. Settlement accepts it only when the threshold result cluster independently derives the same value. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `receipt.output_leaf_count` | `uint64` / JSON decimal string | output leaf count; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `submitter_address` | `string` | submitter address; this field is part of the protocol state or of the request. | Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state. | Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `infer_receipt_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | infer receipt hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `verify_open_deadline_height` | `uint64` / JSON decimal string | verify open deadline height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-submitverifierhandraises"></a> `task.SubmitVerifierHandraises`

- gRPC: `/task.v1.Msg/SubmitVerifierHandraises`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `task.v1.MsgSubmitVerifierHandraises` -> `task.v1.MsgSubmitVerifierHandraisesResponse`
- Purpose: Performs the `SubmitVerifierHandraises` state transition. The current state must be read before calling; a retry must not be treated as naturally idempotent.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `handraises` | array<`task.v1.VerifierHandraiseV1` object> | handraises list; the semantics of an element are given by its message/enum definition. | Built from a preceding Query / frozen set or from the local batch queue; deduplicate and fix the ordering before submitting. | Bounded by the on-chain maximum count and the transaction size; validated item by item, with batch endpoints failing atomically or in isolation as their implementation defines. |
| `submitter_address` | `string` | submitter address; this field is part of the protocol state or of the request. | Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state. | Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `verify_round` | `uint32` | The verification round; read from the OpenVerify/VerifierAssignment state. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `proposal_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | proposal digest; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `added_member_count` | `uint32` | added member count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `union_count` | `uint32` | union count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-submitverifycommit"></a> `task.SubmitVerifyCommit`

- gRPC: `/task.v1.Msg/SubmitVerifyCommit`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `task.v1.MsgSubmitVerifyCommit` -> `task.v1.MsgSubmitVerifyCommitResponse`
- Purpose: Performs the `SubmitVerifyCommit` state transition. The current state must be read before calling; a retry must not be treated as naturally idempotent.
- Stage: Stage2: inference receipt, verification commit/result/reveal.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `commit` | `task.v1.VerifyCommitV1` object | commit; this field is part of the protocol state or of the request. | Computed with SHA-256 / the designated hash algorithm over the canonical bytes the protocol prescribes; the original preimage must be retained for reveal and auditing. | Checked for fixed length / non-emptiness, domain separation, and agreement with the frozen on-chain commitment; recomputed and compared at reveal time. |
| `commit.schema_version` | `uint32` | Always 1; InferReceiptV2 alone uses schema version 2. | Read the frozen version returned by a preceding Query; when creating a new version, increment monotonically from the existing maximum. | The version must be non-zero (except on compatibility paths where the protocol permits zero), and a referenced version must exist and agree with the task snapshot. |
| `commit.chain_id` | `string` | chain id; this field is part of the protocol state or of the request. | Obtained from the successful response, the event or the corresponding Query that created the object, then persisted. | Must be non-empty, length-bounded and reference an existing object; the creation path also checks uniqueness. |
| `commit.task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `commit.verify_round` | `uint32` | The verification round; read from the OpenVerify/VerifierAssignment state. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `commit.verifier_operator_address` | `string` | verifier operator address; this field is part of the protocol state or of the request. | Taken from the frozen verifier set of VerifierAssignment/ChallengeAssignment; the submitter cannot name an arbitrary payee. | Must be a canonical Bech32 address belonging to the formal verifier set of the corresponding round; duplicate addresses are rejected. |
| `commit.service_authorization_nonce` | `uint64` / JSON decimal string | service authorization nonce; this field is part of the protocol state or of the request. | Query SessionNonce / the account sequence before calling, or derive it from the deterministic nonce generation rule of the protocol message to be signed. | Must be exactly the nonce expected on chain and may be consumed only once, which prevents replay. |
| `commit.commit_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | commit hash; this field is part of the protocol state or of the request. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `commit.expiry_height` | `uint64` / JSON decimal string | expiry height; this field is part of the protocol state or of the request. | Read the frozen height from the corresponding Query/event; relative windows are computed from on-chain params and must never use local wall-clock time. | Must fall inside the window the current stage permits; too early, past the deadline, a height that moves backwards, or an overflowing computation are all rejected. |
| `commit.service_signature` | `bytes` / base64 string | service signature; this field is part of the protocol state or of the request. | Use the current service private key of the corresponding operator/duty to sign the canonical, domain-separated protocol bytes. | Verified only against the operator's sole current on-chain ServiceKey and the duty domain; an empty signature, a wrong domain, an old key, or a replay are all rejected. |
| `submitter_address` | `string` | submitter address; this field is part of the protocol state or of the request. | Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state. | Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `commit_key` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | commit key; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `verify_commit_signing_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | verify commit signing digest; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-submitverifyresult"></a> `task.SubmitVerifyResult`

- gRPC: `/task.v1.Msg/SubmitVerifyResult`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `task.v1.MsgSubmitVerifyResult` -> `task.v1.MsgSubmitVerifyResultResponse`
- Purpose: Performs the `SubmitVerifyResult` state transition. The current state must be read before calling; a retry must not be treated as naturally idempotent.
- Stage: Stage2: inference receipt, verification commit/result/reveal.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `receipt` | `task.v1.ResultReceiptV2` object | receipt; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `receipt.schema_version` | `uint32` | Always 2. | Read the frozen version returned by a preceding Query; when creating a new version, increment monotonically from the existing maximum. | The version must be non-zero (except on compatibility paths where the protocol permits zero), and a referenced version must exist and agree with the task snapshot. |
| `receipt.chain_id` | `string` | chain id; this field is part of the protocol state or of the request. | Obtained from the successful response, the event or the corresponding Query that created the object, then persisted. | Must be non-empty, length-bounded and reference an existing object; the creation path also checks uniqueness. |
| `receipt.task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `receipt.verify_round` | `uint32` | The verification round; read from the OpenVerify/VerifierAssignment state. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `receipt.verifier_operator_address` | `string` | verifier operator address; this field is part of the protocol state or of the request. | Taken from the frozen verifier set of VerifierAssignment/ChallengeAssignment; the submitter cannot name an arbitrary payee. | Must be a canonical Bech32 address belonging to the formal verifier set of the corresponding round; duplicate addresses are rejected. |
| `receipt.service_authorization_nonce` | `uint64` / JSON decimal string | service authorization nonce; this field is part of the protocol state or of the request. | Query SessionNonce / the account sequence before calling, or derive it from the deterministic nonce generation rule of the protocol message to be signed. | Must be exactly the nonce expected on chain and may be consumed only once, which prevents replay. |
| `receipt.generation_params_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | generation params digest; this field is part of the protocol state or of the request. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `receipt.metric_root` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | metric root; this field is part of the protocol state or of the request. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `receipt.metric_summary` | `task.v1.MetricSummaryV1` object | metric summary; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `receipt.aggregate_proof_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | aggregate proof hash; this field is part of the protocol state or of the request. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `receipt.verifier_evidence_bundle_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | verifier evidence bundle hash; this field is part of the protocol state or of the request. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `receipt.verifier_evidence_manifest_size_bytes` | `uint64` / JSON decimal string | verifier evidence manifest size bytes; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `receipt.salt` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | salt; this field is part of the protocol state or of the request. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `receipt.expiry_height` | `uint64` / JSON decimal string | expiry height; this field is part of the protocol state or of the request. | Read the frozen height from the corresponding Query/event; relative windows are computed from on-chain params and must never use local wall-clock time. | Must fall inside the window the current stage permits; too early, past the deadline, a height that moves backwards, or an overflowing computation are all rejected. |
| `receipt.service_signature` | `bytes` / base64 string | service signature; this field is part of the protocol state or of the request. | Use the current service private key of the corresponding operator/duty to sign the canonical, domain-separated protocol bytes. | Verified only against the operator's sole current on-chain ServiceKey and the duty domain; an empty signature, a wrong domain, an old key, or a replay are all rejected. |
| `submitter_address` | `string` | submitter address; this field is part of the protocol state or of the request. | Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state. | Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `result_receipt_signing_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | result receipt signing digest; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `result_payload_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | result payload hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-submitworkerevidence"></a> `task.SubmitWorkerEvidence`

- gRPC: `/task.v1.Msg/SubmitWorkerEvidence`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `task.v1.MsgSubmitWorkerEvidence` -> `task.v1.MsgSubmitWorkerEvidenceResponse`
- Purpose: Performs the `SubmitWorkerEvidence` state transition. The current state must be read before calling; a retry must not be treated as naturally idempotent.
- Stage: The challenge, evidence-retention and dispute adjudication stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `evidence_bytes` | `bytes` / base64 string | evidence bytes; this field is part of the protocol state or of the request. | Encoded deterministically according to the corresponding protocol structure; REST/JSON uses base64. | Checked for non-emptiness/length/ceiling, plus hash, signature, Merkle proof or canonical encoding verification where applicable. |
| `submitter_address` | `string` | submitter address; this field is part of the protocol state or of the request. | Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state. | Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `worker_operator_address` | `string` | worker operator address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `evidence_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | evidence digest; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `fault_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | fault id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-submitworkerhandraises"></a> `task.SubmitWorkerHandraises`

- gRPC: `/task.v1.Msg/SubmitWorkerHandraises`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `task.v1.MsgSubmitWorkerHandraises` -> `task.v1.MsgSubmitWorkerHandraisesResponse`
- Purpose: Performs the `SubmitWorkerHandraises` state transition. The current state must be read before calling; a retry must not be treated as naturally idempotent.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `scope` | `task.v1.WorkerHandraiseScopeV1` object | scope; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `scope.signed_order` | `task.v1.SignedOrderV2` object | signed order; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `scope.existing_task` | `task.v1.ExistingTaskRefV1` object | existing task; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `handraises` | array<`task.v1.WorkerHandraiseV1` object> | handraises list; the semantics of an element are given by its message/enum definition. | Built from a preceding Query / frozen set or from the local batch queue; deduplicate and fix the ordering before submitting. | Bounded by the on-chain maximum count and the transaction size; validated item by item, with batch endpoints failing atomically or in isolation as their implementation defines. |
| `submitter_address` | `string` | submitter address; this field is part of the protocol state or of the request. | Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state. | Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `proposal_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | proposal digest; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `added_member_count` | `uint32` | added member count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `union_count` | `uint32` | union count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `stage_status` | `task.v1.TaskCandidateStageStatusV1` enum | stage status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-sweepdeadline"></a> `task.SweepDeadline`

- gRPC: `/task.v1.Msg/SweepDeadline`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `task.v1.MsgSweepDeadline` -> `task.v1.MsgSweepDeadlineResponse`
- Purpose: Performs the `SweepDeadline` state transition. The current state must be read before calling; a retry must not be treated as naturally idempotent.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `locator` | `task.v1.DeadlineLocatorV1` object | locator selects exactly one primary object and deadline (§5.9). | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `locator.task` | `task.v1.TaskDeadlineLocator` object | Task-scoped deadline. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `locator.task_round` | `task.v1.TaskRoundDeadlineLocator` object | Verification-round close deadline. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `locator.session_lifecycle` | `task.v1.SessionLifecycleLocator` object | Session lifecycle sweep. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `submitter_address` | `string` | submitter_address is the Cosmos signer only; it never enters the locator digest, business facts, hashes or Store. | Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state. | Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `visited` | `uint32` | visited is the number of items this call examined. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `advanced` | `uint32` | advanced is the number of items whose state actually moved. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status is APPLIED or NOOP; rejects use gRPC errors. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-updatetaskparams"></a> `task.UpdateTaskParams`

- gRPC: `/task.v1.Msg/UpdateTaskParams`
- Transport: build the corresponding Msg and sign and broadcast it through `/cosmos/tx/v1beta1/txs` or the node CLI; a successful transaction only means execution succeeded at this height.
- Request/response: `task.v1.MsgUpdateTaskParams` -> `task.v1.MsgUpdateTaskParamsResponse`
- Purpose: Governance update of the Task parameters; any change of consensus behaviour must bump integration_rule_version in the same move.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: First validates the signer, the input format, the referenced state and the permitted state transition, then atomically writes Keeper state and emits events; any error rolls the message back.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `expected_version` | `uint64` / JSON decimal string | expected version; this field is part of the protocol state or of the request. | Read the frozen version returned by a preceding Query; when creating a new version, increment monotonically from the existing maximum. | The version must be non-zero (except on compatibility paths where the protocol permits zero), and a referenced version must exist and agree with the task snapshot. |
| `params` | `task.v1.TaskParamsV1` object | params; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `params.schema_version` | `uint32` | schema version; this field is part of the protocol state or of the request. | Read the frozen version returned by a preceding Query; when creating a new version, increment monotonically from the existing maximum. | The version must be non-zero (except on compatibility paths where the protocol permits zero), and a referenced version must exist and agree with the task snapshot. |
| `params.session` | `task.v1.SessionParamsV1` object | session; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `params.deadlines` | `task.v1.TaskDeadlineParamsV1` object | deadlines; this field is part of the protocol state or of the request. | Read the frozen height from the corresponding Query/event; relative windows are computed from on-chain params and must never use local wall-clock time. | Must fall inside the window the current stage permits; too early, past the deadline, a height that moves backwards, or an overflowing computation are all rejected. |
| `params.proposals` | `task.v1.TaskProposalParamsV1` object | proposals; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `params.weights` | `task.v1.CandidateWeightParamsV1` object | weights; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `params.verification` | `task.v1.VerificationParamsV1` object | verification; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `params.evidence` | `task.v1.EvidenceLimitParamsV1` object | evidence; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `params.cleanup` | `task.v1.TaskCleanupParamsV1` object | cleanup; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `params.batch` | `task.v1.BatchParamsV1` object | batch; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `params.generation` | `task.v1.GenerationLimitParamsV1` object | generation; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `params.fee` | `task.v1.TaskFeeParamsV1` object | fee; this field is part of the protocol state or of the request. | Computed from on-chain parameters, from the quote/order, or from the local balance budget; integers travel as decimal strings in JSON. | Must be a legal non-negative Coin/uint64 with a consistent denom and no overflow, and must satisfy the minimum bond, the available balance, the outstanding liabilities and the module budget limits. |
| `params.challenge` | `task.v1.ChallengeParamsV1` object | challenge; this field is part of the protocol state or of the request. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `authority` | `string` | authority; this field is part of the protocol state or of the request. | Read the authority address from the on-chain governance module configuration; an ordinary account cannot choose its own. | Must be exactly the module authority, otherwise the governance parameter or state update is rejected. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `new_version` | `uint64` / JSON decimal string | new version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `params_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | params hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `status` | `shared.v1.MutationStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-assignmentrandomness"></a> `task.AssignmentRandomness`

- gRPC: `/task.v1.Query/AssignmentRandomness`
- REST: `GET /TrueOpen/task/v1/task/{task_id}/assignment_randomness`
- Request/response: `task.v1.QueryAssignmentRandomnessRequest` -> `task.v1.QueryAssignmentRandomnessResponse`
- Purpose: Reads the authoritative on-chain state behind `AssignmentRandomness`, for building the next stage's request, for auditing, or for recovery.
- Stage: Stage1: task assignment and budget freezing.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `randomness` | `task.v1.AssignmentRandomnessViewV1` object | randomness; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `randomness.task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `randomness.assignment_randomness_height` | `uint64` / JSON decimal string | assignment randomness height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `randomness.assignment_candidate_set_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | assignment candidate set hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `randomness.beacon_randomness` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | beacon randomness; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `randomness.beacon_proof_digest` | optional `Hash32` / omitted when absent; lowercase 64-hex client value when present (raw 32-byte protobuf) | beacon proof digest; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | An absent value must be omitted; a present value must satisfy the bytes encoding and width the descriptor declares, and null or empty bytes must not be used to stand in for absence. |
| `randomness.winner_worker` | `string` | winner worker; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `randomness.winner_draw_digest` | optional `Hash32` / omitted when absent; lowercase 64-hex client value when present (raw 32-byte protobuf) | winner draw digest; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | An absent value must be omitted; a present value must satisfy the bytes encoding and width the descriptor declares, and null or empty bytes must not be used to stand in for absence. |
| `randomness.winner_confirm_height` | `uint64` / JSON decimal string | winner confirm height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-builderdataunavailable"></a> `task.BuilderDataUnavailable`

- gRPC: `/task.v1.Query/BuilderDataUnavailable`
- REST: `GET /TrueOpen/task/v1/task/{task_id}/builder_data_unavailable/{verify_round}/{builder_operator_address}`
- Request/response: `task.v1.QueryBuilderDataUnavailableRequest` -> `task.v1.QueryBuilderDataUnavailableResponse`
- Purpose: Reads the authoritative on-chain state behind `BuilderDataUnavailable`, for building the next stage's request, for auditing, or for recovery.
- Stage: The Builder registration, election, stage contribution or reward stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `verify_round` | `uint32` | The verification round; read from the OpenVerify/VerifierAssignment state. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `builder_operator_address` | `string` | builder operator address; this field is part of the protocol state or of the request. | Taken from the Builder's local account, from BuilderSet, or from a TaskBuilderSelection query result. | Must be a canonical Bech32 address; where required it is checked to be registered, validly bonded, not tombstoned, and a member of the frozen stage Builder set. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `aggregate` | `task.v1.BuilderDataUnavailableAggregateState` object | aggregate; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `aggregate.task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `aggregate.verify_round` | `uint32` | The verification round; read from the OpenVerify/VerifierAssignment state. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `aggregate.builder_operator_address` | `string` | builder operator address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `aggregate.valid_report_count` | `uint32` | valid report count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `aggregate.required_report_count` | `uint32` | required report count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `aggregate.report_digests_by_verifier_slot` | array<`task.v1.BuilderDataUnavailableSlotV1` object> | One slot per selected verifier, in selected Verifier slot order, always exactly selected_verifier_count of them. Slot i belongs to VerifierAssignmentState.selected_verifiers[i] and carries its digest only if that verifier filed a valid report. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once. |
| `aggregate.aggregate_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | aggregate hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `aggregate.threshold_reached_height` | `uint64` / JSON decimal string | threshold reached height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `aggregate.status` | `task.v1.BuilderDataUnavailableAggregateStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-dataunavailablereports"></a> `task.DataUnavailableReports`

- gRPC: `/task.v1.Query/DataUnavailableReports`
- REST: `GET /TrueOpen/task/v1/task/{task_id}/data_unavailable_reports/{verify_round}`
- Request/response: `task.v1.QueryDataUnavailableReportsRequest` -> `task.v1.QueryDataUnavailableReportsResponse`
- Purpose: Reads the authoritative on-chain state behind `DataUnavailableReports`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `verify_round` | `uint32` | The verification round; read from the OpenVerify/VerifierAssignment state. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `page` | `shared.v1.QueryPageRequestV1` object | The batch page or the pagination cursor. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `page.page_token` | `bytes` / base64 string | page_token is empty on the first page and otherwise a canonical PageTokenV1. It is an opaque variable-length cursor blob, not a Hash32, so REST projects it as ProtoJSON Base64. | Encoded deterministically according to the corresponding protocol structure; REST/JSON uses base64. | Checked for non-emptiness/length/ceiling, plus hash, signature, Merkle proof or canonical encoding verification where applicable. |
| `page.limit` | `uint32` | The ceiling on a single query/processing pass, which prevents unbounded gas and responses. | Taken from the Query Params / operational batch configuration; choose a positive integer no larger than the on-chain hard ceiling. | Must be within the permitted range; Node additionally takes the smaller of the parameter ceiling and the requested value, and neither zero nor an oversized value may be used to trigger a full scan. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `reports` | array<`task.v1.DataUnavailableReportState` object> | reports list; the semantics of an element are given by its message/enum definition. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once. |
| `page` | `shared.v1.QueryPageResponseV1` object | The batch page or the pagination cursor. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `page.next_page_token` | `bytes` / base64 string | next_page_token is empty when the walk is complete. Same opaque PageTokenV1 blob as page_token, so REST projects it as ProtoJSON Base64. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-epochtasksummary"></a> `task.EpochTaskSummary`

- gRPC: `/task.v1.Query/EpochTaskSummary`
- REST: `GET /TrueOpen/task/v1/epoch/{epoch}/task_summary`
- Request/response: `task.v1.QueryEpochTaskSummaryRequest` -> `task.v1.QueryEpochTaskSummaryResponse`
- Purpose: Reads the authoritative on-chain state behind `EpochTaskSummary`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `epoch` | `uint64` / JSON decimal string | epoch; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `running` | `task.v1.EpochTaskSummaryCursorState` object | running; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `running.epoch` | `uint64` / JSON decimal string | epoch; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `running.start_height` | `uint64` / JSON decimal string | start height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `running.end_height` | `uint64` / JSON decimal string | end height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `running.due_height` | `uint64` / JSON decimal string | due height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `running.last_settlement_height` | `uint64` / JSON decimal string | last settlement height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `running.last_task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | last task id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `running.task_count` | `uint64` / JSON decimal string | task count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `running.valid_task_count` | `uint64` / JSON decimal string | valid task count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `running.histogram` | array<`uint64` / JSON decimal string> | histogram list; the semantics of an element are given by its message/enum definition. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once. |
| `running.support_candidates` | array<`string`> | support candidates list; the semantics of an element are given by its message/enum definition. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once. |
| `running.support_candidate_seen_count` | `uint64` / JSON decimal string | support candidate seen count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `running.running_root` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | running root; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `running.visited_count` | `uint64` / JSON decimal string | visited count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt` | `task.v1.EpochTaskSummaryReceiptState` object | receipt; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `receipt.epoch` | `uint64` / JSON decimal string | epoch; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.start_height` | `uint64` / JSON decimal string | start height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.end_height` | `uint64` / JSON decimal string | end height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.due_height` | `uint64` / JSON decimal string | due height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.dispatched_height` | `uint64` / JSON decimal string | dispatched height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.summary` | `shared.v1.EpochTaskSummary` object | summary; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `receipt.source_count` | `uint64` / JSON decimal string | source count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.support_candidate_seen_count` | `uint64` / JSON decimal string | support candidate seen count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.retained_support_candidate_count` | `uint32` | retained support candidate count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.source_root` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | source root; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.receipt_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | receipt hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-evidencecleanup"></a> `task.EvidenceCleanup`

- gRPC: `/task.v1.Query/EvidenceCleanup`
- REST: `GET /TrueOpen/task/v1/task/{task_id}/cleanup`
- Request/response: `task.v1.QueryEvidenceCleanupRequest` -> `task.v1.QueryEvidenceCleanupResponse`
- Purpose: Reads the authoritative on-chain state behind `EvidenceCleanup`, for building the next stage's request, for auditing, or for recovery.
- Stage: The challenge, evidence-retention and dispute adjudication stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `cleanup` | `task.v1.TaskCleanupProgressViewV1` object | cleanup; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `cleanup.task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `cleanup.status` | `task.v1.TaskCleanupStatus` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `cleanup.phase` | `task.v1.TaskCleanupPhase` enum | phase; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `cleanup.visited_count` | `uint64` / JSON decimal string | visited count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `cleanup.deleted_count` | `uint64` / JSON decimal string | deleted count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-inferreceipt"></a> `task.InferReceipt`

- gRPC: `/task.v1.Query/InferReceipt`
- REST: `GET /TrueOpen/task/v1/task/{task_id}/infer_receipt`
- Request/response: `task.v1.QueryInferReceiptRequest` -> `task.v1.QueryInferReceiptResponse`
- Purpose: Reads the authoritative on-chain state behind `InferReceipt`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `receipt` | `task.v1.InferReceiptState` object | receipt; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `receipt.task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.winner_worker` | `string` | winner worker; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.infer_receipt_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | infer receipt hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.generation_params_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | generation params digest; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.output_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The hash commitment over the canonical bytes of the Cortex output. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.output_size_bytes` | `uint64` / JSON decimal string | output size bytes; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.generated_token_count` | `uint64` / JSON decimal string | generated token count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.evidence_commitments_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | evidence commitments hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.required_evidence_commitments` | array<`task.v1.EvidenceCommitmentV1` object> | required evidence commitments list; the semantics of an element are given by its message/enum definition. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once. |
| `receipt.evidence_commitment_count` | `uint32` | evidence commitment count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.infer_receipt_signing_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | infer receipt signing digest; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.signature_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | SHA256(raw_signature_64), retained as a 32-byte audit/exact-replay fingerprint. The 64-byte signature was verified before initial live acceptance and is not retained. This digest is not authorization state and never enters a signing/business digest; replay compares it only to identify the same previously accepted signature. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.expiry_height` | `uint64` / JSON decimal string | expiry height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.receipt_height` | `uint64` / JSON decimal string | receipt height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.output_leaf_count` | `uint64` / JSON decimal string | output leaf count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-ordersequence"></a> `task.OrderSequence`

- gRPC: `/task.v1.Query/OrderSequence`
- REST: `GET /TrueOpen/task/v1/session/{session_id}/order/{order_sequence}`
- Request/response: `task.v1.QueryOrderSequenceRequest` -> `task.v1.QueryOrderSequenceResponse`
- Purpose: Reads the authoritative on-chain state behind `OrderSequence`, for building the next stage's request, for auditing, or for recovery.
- Stage: The user session and order lifecycle stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `session_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The global identifier of a task session. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `order_sequence` | `uint64` / JSON decimal string | order sequence; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `sequence` | `task.v1.OrderSequenceState` object | sequence; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `sequence.session_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The global identifier of a task session. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `sequence.order_sequence` | `uint64` / JSON decimal string | order sequence; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `sequence.status` | `task.v1.OrderSequenceStatus` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `sequence.task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `sequence.consumed_height` | `uint64` / JSON decimal string | consumed height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `sequence.cancelled_height` | `uint64` / JSON decimal string | cancelled height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-params"></a> `task.Params`

- gRPC: `/task.v1.Query/Params`
- REST: `GET /TrueOpen/task/v1/params`
- Request/response: `task.v1.QueryTaskParamsRequest` -> `task.v1.QueryTaskParamsResponse`
- Purpose: Reads the authoritative on-chain state behind `Params`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| - | empty message | no parameters | send an empty object `{}` | no business field validation |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `params` | `task.v1.TaskParamsV1` object | params holds all the parameters of this module. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `params.schema_version` | `uint32` | schema version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `params.session` | `task.v1.SessionParamsV1` object | session; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `params.deadlines` | `task.v1.TaskDeadlineParamsV1` object | deadlines; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `params.proposals` | `task.v1.TaskProposalParamsV1` object | proposals; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `params.weights` | `task.v1.CandidateWeightParamsV1` object | weights; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `params.verification` | `task.v1.VerificationParamsV1` object | verification; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `params.evidence` | `task.v1.EvidenceLimitParamsV1` object | evidence; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `params.cleanup` | `task.v1.TaskCleanupParamsV1` object | cleanup; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `params.batch` | `task.v1.BatchParamsV1` object | batch; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `params.generation` | `task.v1.GenerationLimitParamsV1` object | generation; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `params.fee` | `task.v1.TaskFeeParamsV1` object | fee; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `params.challenge` | `task.v1.ChallengeParamsV1` object | challenge; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `meta` | `task.v1.TaskParamsMetaState` object | meta carries this module's own params version/hash/updated height. Params ownership stays per module: a client that needs a joint view calls hub.v1.Query/Params and task.v1.Query/Params at the same x-cosmos-block-height. Wire adds no app-level package importing both. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `meta.params_version` | `uint64` / JSON decimal string | params version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `meta.params_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | params hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `meta.updated_height` | `uint64` / JSON decimal string | updated height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-resultreceipt"></a> `task.ResultReceipt`

- gRPC: `/task.v1.Query/ResultReceipt`
- REST: `GET /TrueOpen/task/v1/task/{task_id}/result_receipt/{verify_round}/{verifier_operator_address}`
- Request/response: `task.v1.QueryResultReceiptRequest` -> `task.v1.QueryResultReceiptResponse`
- Purpose: Reads the authoritative on-chain state behind `ResultReceipt`, for building the next stage's request, for auditing, or for recovery.
- Stage: Stage2: inference receipt, verification commit/result/reveal.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `verify_round` | `uint32` | The verification round; read from the OpenVerify/VerifierAssignment state. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `verifier_operator_address` | `string` | verifier operator address; this field is part of the protocol state or of the request. | Taken from the frozen verifier set of VerifierAssignment/ChallengeAssignment; the submitter cannot name an arbitrary payee. | Must be a canonical Bech32 address belonging to the formal verifier set of the corresponding round; duplicate addresses are rejected. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `receipt` | `task.v1.ResultReceiptState` object | receipt; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `receipt.commit_key` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | commit key; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.verify_round` | `uint32` | The verification round; read from the OpenVerify/VerifierAssignment state. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.verifier_operator_address` | `string` | verifier operator address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.selected_verifier_index` | `uint32` | selected verifier index; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.metric_root` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | metric root; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.metric_summary` | `task.v1.MetricSummaryV1` object | metric summary; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `receipt.metric_summary_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | metric summary hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.aggregate_proof_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | aggregate proof hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.verifier_evidence_bundle_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | verifier evidence bundle hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.verifier_evidence_manifest_size_bytes` | `uint64` / JSON decimal string | verifier evidence manifest size bytes; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.salt` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | salt; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.result_payload_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | result payload hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.result_receipt_signing_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | result receipt signing digest; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.signature_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | SHA256(raw_signature_64), retained as a 32-byte audit/exact-replay fingerprint. The 64-byte signature was verified before initial live acceptance and is not retained. This digest is not authorization state and never enters a signing/business digest; replay compares it only to identify the same previously accepted signature. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.accepted_height` | `uint64` / JSON decimal string | accepted height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-roleactivetasks"></a> `task.RoleActiveTasks`

- gRPC: `/task.v1.Query/RoleActiveTasks`
- REST: `GET /TrueOpen/task/v1/role/{operator_address}/{duty}/active_tasks`
- Request/response: `task.v1.QueryRoleActiveTasksRequest` -> `task.v1.QueryRoleActiveTasksResponse`
- Purpose: Reads the authoritative on-chain state behind `RoleActiveTasks`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `operator_address` | `string` | operator address; this field is part of the protocol state or of the request. | Taken from the Cortex Node identity or from the on-chain ModelCapability/ModelSupport state. | Must be a canonical Bech32 address whose node has a valid model and profile capability/support status. |
| `duty` | `shared.v1.Duty` enum | The Cortex service duty domain (Worker/Verifier), which is also part of the service key verification domain. | Use the numeric value / name defined by the proto enum; pick it from the preceding state machine stage and never use an unrecognized custom value. | Must be a known enum value permitted by this RPC and must satisfy the current state transition; UNSPECIFIED is normally rejected. |
| `page` | `shared.v1.QueryPageRequestV1` object | The batch page or the pagination cursor. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `page.page_token` | `bytes` / base64 string | page_token is empty on the first page and otherwise a canonical PageTokenV1. It is an opaque variable-length cursor blob, not a Hash32, so REST projects it as ProtoJSON Base64. | Encoded deterministically according to the corresponding protocol structure; REST/JSON uses base64. | Checked for non-emptiness/length/ceiling, plus hash, signature, Merkle proof or canonical encoding verification where applicable. |
| `page.limit` | `uint32` | The ceiling on a single query/processing pass, which prevents unbounded gas and responses. | Taken from the Query Params / operational batch configuration; choose a positive integer no larger than the on-chain hard ceiling. | Must be within the permitted range; Node additionally takes the smaller of the parameter ceiling and the requested value, and neither zero nor an oversized value may be used to trigger a full scan. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `tasks` | array<`task.v1.ActiveTaskRefV1` object> | tasks list; the semantics of an element are given by its message/enum definition. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once. |
| `page` | `shared.v1.QueryPageResponseV1` object | The batch page or the pagination cursor. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `page.next_page_token` | `bytes` / base64 string | next_page_token is empty when the walk is complete. Same opaque PageTokenV1 blob as page_token, so REST projects it as ProtoJSON Base64. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-session"></a> `task.Session`

- gRPC: `/task.v1.Query/Session`
- REST: `GET /TrueOpen/task/v1/session/{session_id}`
- Request/response: `task.v1.QuerySessionRequest` -> `task.v1.QuerySessionResponse`
- Purpose: Reads the authoritative on-chain state behind `Session`, for building the next stage's request, for auditing, or for recovery.
- Stage: The user session and order lifecycle stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `session_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The global identifier of a task session. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `session` | `task.v1.StreamState` object | session; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `session.session_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The global identifier of a task session. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `session.owner_user_address` | `string` | owner user address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `session.next_expected_sequence` | `uint64` / JSON decimal string | next expected sequence; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `session.last_active_height` | `uint64` / JSON decimal string | last active height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `session.open_pending_count` | `uint32` | open pending count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `session.status` | `task.v1.SessionStatus` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-sessionnonce"></a> `task.SessionNonce`

- gRPC: `/task.v1.Query/SessionNonce`
- REST: `GET /TrueOpen/task/v1/session_nonce/{user_address}`
- Request/response: `task.v1.QuerySessionNonceRequest` -> `task.v1.QuerySessionNonceResponse`
- Purpose: Reads the authoritative on-chain state behind `SessionNonce`, for building the next stage's request, for auditing, or for recovery.
- Stage: The user session and order lifecycle stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `user_address` | `string` | user address; this field is part of the protocol state or of the request. | Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state. | Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `next_session_nonce` | `uint64` / JSON decimal string | next session nonce; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-sessionterminalsummary"></a> `task.SessionTerminalSummary`

- gRPC: `/task.v1.Query/SessionTerminalSummary`
- REST: `GET /TrueOpen/task/v1/session/{session_id}/terminal_summary`
- Request/response: `task.v1.QuerySessionTerminalSummaryRequest` -> `task.v1.QuerySessionTerminalSummaryResponse`
- Purpose: Reads the authoritative on-chain state behind `SessionTerminalSummary`, for building the next stage's request, for auditing, or for recovery.
- Stage: The user session and order lifecycle stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `session_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The global identifier of a task session. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `summary` | `task.v1.SessionTerminalSummaryState` object | summary; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `summary.session_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The global identifier of a task session. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `summary.owner_user_address` | `string` | owner user address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `summary.final_next_expected_sequence` | `uint64` / JSON decimal string | final next expected sequence; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `summary.sequence_count` | `uint32` | sequence count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `summary.sequence_root` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | sequence root; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `summary.consumed_count` | `uint32` | consumed count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `summary.cancelled_count` | `uint32` | cancelled count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `summary.refunded_count` | `uint32` | refunded count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `summary.settled_count` | `uint32` | settled count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `summary.closed_height` | `uint64` / JSON decimal string | closed height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `summary.compacted_height` | `uint64` / JSON decimal string | compacted height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-sessionsbyowner"></a> `task.SessionsByOwner`

- gRPC: `/task.v1.Query/SessionsByOwner`
- REST: `GET /TrueOpen/task/v1/sessions/{user_address}`
- Request/response: `task.v1.QuerySessionsByOwnerRequest` -> `task.v1.QuerySessionsByOwnerResponse`
- Purpose: Reads the authoritative on-chain state behind `SessionsByOwner`, for building the next stage's request, for auditing, or for recovery.
- Stage: The user session and order lifecycle stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `user_address` | `string` | user address; this field is part of the protocol state or of the request. | Read from the corresponding account, from Assignment/VerifierAssignment/BuilderSelection, or from Hub registration state. | Must be a canonical Bech32 address, identical to the owner/role/operator in the referenced state. |
| `page` | `shared.v1.QueryPageRequestV1` object | The batch page or the pagination cursor. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `page.page_token` | `bytes` / base64 string | page_token is empty on the first page and otherwise a canonical PageTokenV1. It is an opaque variable-length cursor blob, not a Hash32, so REST projects it as ProtoJSON Base64. | Encoded deterministically according to the corresponding protocol structure; REST/JSON uses base64. | Checked for non-emptiness/length/ceiling, plus hash, signature, Merkle proof or canonical encoding verification where applicable. |
| `page.limit` | `uint32` | The ceiling on a single query/processing pass, which prevents unbounded gas and responses. | Taken from the Query Params / operational batch configuration; choose a positive integer no larger than the on-chain hard ceiling. | Must be within the permitted range; Node additionally takes the smaller of the parameter ceiling and the requested value, and neither zero nor an oversized value may be used to trigger a full scan. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `sessions` | array<`task.v1.StreamState` object> | sessions list; the semantics of an element are given by its message/enum definition. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once. |
| `page` | `shared.v1.QueryPageResponseV1` object | The batch page or the pagination cursor. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `page.next_page_token` | `bytes` / base64 string | next_page_token is empty when the walk is complete. Same opaque PageTokenV1 blob as page_token, so REST projects it as ProtoJSON Base64. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-settlement"></a> `task.Settlement`

- gRPC: `/task.v1.Query/Settlement`
- REST: `GET /TrueOpen/task/v1/task/{task_id}/settlement`
- Request/response: `task.v1.QuerySettlementRequest` -> `task.v1.QuerySettlementResponse`
- Purpose: Reads the authoritative on-chain state behind `Settlement`, for building the next stage's request, for auditing, or for recovery.
- Stage: Stage3: light settlement, fund conservation and finality.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `settlement` | `task.v1.TaskSettlementState` object | settlement; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `settlement.task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `settlement.settlement_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The identifier of a task settlement record. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `settlement.fee_rule_version` | `uint64` / JSON decimal string | fee rule version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `settlement.effective_verify_round` | `uint32` | effective verify round; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `settlement.verdict` | `task.v1.TaskVerdict` enum | verdict; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `settlement.failure_class` | `task.v1.TaskFailureClass` enum | failure class; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `settlement.settlement_facts_cutoff_height` | `uint64` / JSON decimal string | settlement facts cutoff height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `settlement.settlement_height` | `uint64` / JSON decimal string | settlement height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `settlement.task_finality_height` | `uint64` / JSON decimal string | task finality height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `settlement.generated_token_count` | `uint64` / JSON decimal string | generated token count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `settlement.work_unit` | `uint64` / JSON decimal string | The Worker's signed audit value for the workload of this inference; it currently confers no billing entitlement. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `settlement.worker_operator_address` | `string` | worker operator address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `settlement.infer_receipt_ref_or_zero32` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | infer receipt ref or zero32; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `settlement.worker_gross` | `shared.v1.Amount` object | worker gross; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `settlement.worker_maintenance` | `shared.v1.Amount` object | worker maintenance; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `settlement.worker_net` | `shared.v1.Amount` object | worker net; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `settlement.verifier_slot_gross` | `shared.v1.Amount` object | verifier slot gross; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `settlement.verifier_payout_count` | `uint32` | verifier payout count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `settlement.gas_reimbursement_count` | `uint32` | gas reimbursement count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `settlement.gas_reimbursements_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | gas reimbursements hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `settlement.maintenance_fee` | `shared.v1.Amount` object | The settlement maintenance fee, bounded by what remains of the total budget after the other caps are deducted. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `settlement.refund_amount` | `shared.v1.Amount` object | refund amount; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `settlement.original_reserved_amount` | `shared.v1.Amount` object | original reserved amount; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `settlement.settlement_facts_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | settlement facts hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `settlement.task_round_summary_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | task round summary hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `settlement.settlement_bill_hash_or_zero32` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | settlement bill hash or zero32; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `settlement.settlement_plan_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | settlement plan hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `settlement.gas_reimbursed_total` | `shared.v1.Amount` object | Sum of TaskGasReimbursementV1.reimbursed_amount, frozen at settlement. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `plan` | `task.v1.SettlementPlanV1` object | plan; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `plan.task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `plan.settlement_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The identifier of a task settlement record. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `plan.fee_rule_version` | `uint64` / JSON decimal string | fee rule version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `plan.effective_verify_round` | `uint32` | effective verify round; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `plan.generated_token_count` | `uint64` / JSON decimal string | generated token count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `plan.work_unit` | `uint64` / JSON decimal string | The Worker's signed audit value for the workload of this inference; it currently confers no billing entitlement. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `plan.worker_gross` | `shared.v1.Amount` object | worker gross; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `plan.worker_maintenance` | `shared.v1.Amount` object | worker maintenance; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `plan.worker_net` | `shared.v1.Amount` object | worker net; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `plan.verifier_slot_gross` | `shared.v1.Amount` object | verifier slot gross; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `plan.verifier_payouts` | array<`task.v1.VerifierPayoutV1` object> | verifier payouts list; the semantics of an element are given by its message/enum definition. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once. |
| `plan.gas_reimbursements` | array<`task.v1.TaskGasReimbursementV1` object> | gas reimbursements list; the semantics of an element are given by its message/enum definition. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once. |
| `plan.gas_reimbursements_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | gas reimbursements hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `plan.maintenance_fee` | `shared.v1.Amount` object | The settlement maintenance fee, bounded by what remains of the total budget after the other caps are deducted. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `plan.refund_amount` | `shared.v1.Amount` object | refund amount; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `plan.original_reserved_amount` | `shared.v1.Amount` object | original reserved amount; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `plan.settlement_facts_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | settlement facts hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `plan.task_round_summary_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | task round summary hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `plan.settlement_bill_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | settlement bill hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-settlementfacts"></a> `task.SettlementFacts`

- gRPC: `/task.v1.Query/SettlementFacts`
- REST: `GET /TrueOpen/task/v1/task/{task_id}/settlement_facts`
- Request/response: `task.v1.QuerySettlementFactsRequest` -> `task.v1.QuerySettlementFactsResponse`
- Purpose: Reads the authoritative on-chain state behind `SettlementFacts`, for building the next stage's request, for auditing, or for recovery.
- Stage: Stage3: light settlement, fund conservation and finality.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `facts` | `task.v1.SettlementFactsRetainedState` object | facts; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `facts.task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.task_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | task hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.settlement_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The identifier of a task settlement record. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.settlement_facts_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | settlement facts hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.settlement_facts_cutoff_height` | `uint64` / JSON decimal string | settlement facts cutoff height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.settlement_height` | `uint64` / JSON decimal string | settlement height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.settlement_duty_builder_operator` | `string` | settlement duty builder operator; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.consensus_cluster_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | consensus cluster hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.recomputed_verdict` | `task.v1.TaskVerdict` enum | recomputed verdict; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.failure_class` | `task.v1.TaskFailureClass` enum | failure class; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.fault_summary_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | fault summary hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.paid_roles_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | paid roles hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.paid_role_count` | `uint32` | paid role count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.infer_receipt_ref_or_zero32` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | infer receipt ref or zero32; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.result_receipt_refs_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | result receipt refs hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.challenge_close_height` | `uint64` / JSON decimal string | challenge close height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.effective_verify_round` | `uint32` | effective verify round; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.fee_rule_version` | `uint64` / JSON decimal string | fee rule version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.generated_token_count` | `uint64` / JSON decimal string | generated token count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.work_unit` | `uint64` / JSON decimal string | The Worker's signed audit value for the workload of this inference; it currently confers no billing entitlement. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.settlement_bill_hash_or_zero32` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | settlement bill hash or zero32; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.task_round_summary_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | task round summary hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.evidence_schema_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | evidence schema hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.judgment_function_version` | `string` | judgment function version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.canonical_encoding_version` | `string` | canonical encoding version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.profile_execution_snapshot_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | profile execution snapshot hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.generation_params_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | generation params digest; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `facts.metric_aggregate_proof_version` | `string` | metric aggregate proof version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-task"></a> `task.Task`

- gRPC: `/task.v1.Query/Task`
- REST: `GET /TrueOpen/task/v1/task/{task_id}`
- Request/response: `task.v1.QueryTaskRequest` -> `task.v1.QueryTaskResponse`
- Purpose: Reads the authoritative on-chain state behind `Task`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task` | `task.v1.TaskViewV1` object | task; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `task.active` | `task.v1.TaskActiveBundleV1` object | Current composite view of an uncompacted Task. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `task.terminal` | `task.v1.TaskTerminalSummaryState` object | Retained fixed-size authority after cleanup compaction. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |

### <a id="rpc-task-taskassignment"></a> `task.TaskAssignment`

- gRPC: `/task.v1.Query/TaskAssignment`
- REST: `GET /TrueOpen/task/v1/task/{task_id}/assignment`
- Request/response: `task.v1.QueryTaskAssignmentRequest` -> `task.v1.QueryTaskAssignmentResponse`
- Purpose: Reads the authoritative on-chain state behind `TaskAssignment`, for building the next stage's request, for auditing, or for recovery.
- Stage: Stage1: task assignment and budget freezing.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `assignment` | `task.v1.TaskAssignmentViewV1` object | assignment; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `assignment.task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.assign_accept_height` | `uint64` / JSON decimal string | assign accept height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.assignment_randomness_height` | `uint64` / JSON decimal string | assignment randomness height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.winner_confirm_height` | `uint64` / JSON decimal string | winner confirm height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.infer_deadline_height` | `uint64` / JSON decimal string | infer deadline height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.assignment_fail_reason` | `task.v1.AssignmentFailureReason` enum | assignment fail reason; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.candidate_pool_snapshot_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | candidate pool snapshot id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.candidate_pool_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | candidate pool hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.assignment_candidate_set_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | assignment candidate set hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.winner_worker` | `string` | winner worker; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.winner_draw_digest` | optional `Hash32` / omitted when absent; lowercase 64-hex client value when present (raw 32-byte protobuf) | winner draw digest; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | An absent value must be omitted; a present value must satisfy the bytes encoding and width the descriptor declares, and null or empty bytes must not be used to stand in for absence. |
| `assignment.profile_execution_snapshot_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | profile execution snapshot hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.judgment_function_version` | `string` | judgment function version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.evidence_schema_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | evidence schema hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.canonical_encoding_version` | `string` | canonical encoding version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.generation_params_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | Immutable assignment fact. Worker, Verifier and settlement consumers copy this value through unchanged and never recompute it from current defaults. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.metric_aggregate_proof_version` | `string` | metric aggregate proof version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.assignment_status` | `task.v1.AssignmentStatus` enum | assignment status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.worker_infer_timeout_slash_bps` | `uint32` | worker infer timeout slash bps; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.result_reveal_missing_slash_bps` | `uint32` | result reveal missing slash bps; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-taskbudget"></a> `task.TaskBudget`

- gRPC: `/task.v1.Query/TaskBudget`
- REST: `GET /TrueOpen/task/v1/task/{task_id}/budget`
- Request/response: `task.v1.QueryTaskBudgetRequest` -> `task.v1.QueryTaskBudgetResponse`
- Purpose: Reads the authoritative on-chain state behind `TaskBudget`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `budget` | `task.v1.TaskBudgetState` object | budget; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `budget.task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `budget.fee_rule_version` | `uint64` / JSON decimal string | Registered fee rule version only; there is no default value. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `budget.original_reserved_amount` | `shared.v1.Amount` object | original reserved amount; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `budget.reserved_amount` | `shared.v1.Amount` object | reserved amount; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `budget.tx_fee_reserve_remaining` | `shared.v1.Amount` object | tx fee reserve remaining; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `budget.price_bid` | `shared.v1.Amount` object | price bid; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `budget.worker_max` | `shared.v1.Amount` object | worker max; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `budget.verify_max` | `shared.v1.Amount` object | verify max; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `budget.verify_ratio_bps_snapshot` | `uint32` | verify ratio bps snapshot; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `budget.max_output_tokens` | `uint64` / JSON decimal string | max output tokens; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `budget.maintenance_rate_bps_snapshot` | `uint32` | maintenance rate bps snapshot; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `budget.fee_policy_version_snapshot` | `uint64` / JSON decimal string | fee policy version snapshot; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `budget.max_reimbursable_fee_per_gas_snapshot` | `task.v1.FeePerGasRateV1` object | max reimbursable fee per gas snapshot; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `budget.max_reimbursement_per_tx_snapshot` | `shared.v1.Amount` object | max reimbursement per tx snapshot; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `budget.max_reimbursement_per_task_snapshot` | `shared.v1.Amount` object | max reimbursement per task snapshot; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `budget.gas_reimbursement_count` | `uint32` | gas reimbursement count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `budget.gas_reimbursed_total` | `shared.v1.Amount` object | gas reimbursed total; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `budget.budget_status` | `task.v1.TaskBudgetStatus` enum | budget status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-taskbuilders"></a> `task.TaskBuilders`

- gRPC: `/task.v1.Query/TaskBuilders`
- REST: `GET /TrueOpen/task/v1/task/{task_id}/builders`
- Request/response: `task.v1.QueryTaskBuildersRequest` -> `task.v1.QueryTaskBuildersResponse`
- Purpose: Reads the authoritative on-chain state behind `TaskBuilders`, for building the next stage's request, for auditing, or for recovery.
- Stage: The Builder registration, election, stage contribution or reward stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `selection` | `task.v1.TaskBuilderSelectionViewV1` object | selection; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `selection.task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `selection.session_anchor_block_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | session anchor block hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `selection.builder_set_id` | `string` | builder set id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `selection.builder_set_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | builder set hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `selection.selected_task_builders` | array<`string`> | Frozen order; length equals builders_per_task while body_status is ACTIVE and is empty once the body is pruned. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once. |
| `selection.selected_task_builder_count` | `uint32` | Unchanged by pruning. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `selection.selected_task_builders_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | Ordered-member commitment; retained after pruning. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `selection.created_height` | `uint64` / JSON decimal string | created height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `selection.body_status` | `shared.v1.StoredBodyStatus` enum | body status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-taskfailureclass"></a> `task.TaskFailureClass`

- gRPC: `/task.v1.Query/TaskFailureClass`
- REST: `GET /TrueOpen/task/v1/task/{task_id}/failure_class`
- Request/response: `task.v1.QueryTaskFailureClassRequest` -> `task.v1.QueryTaskFailureClassResponse`
- Purpose: Reads the authoritative on-chain state behind `TaskFailureClass`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `verify_round` | `uint32` | The verification round; read from the OpenVerify/VerifierAssignment state. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `failure` | `task.v1.TaskFailureClassState` object | failure; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `failure.task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `failure.verify_round` | `uint32` | The verification round; read from the OpenVerify/VerifierAssignment state. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `failure.model_id` | `string` | The canonical identifier of a model registered with the Hub. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `failure.profile_version` | `uint32` | The frozen version of a model's Integration Profile. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `failure.settlement_id` | optional `Hash32` / omitted when absent; lowercase 64-hex client value when present (raw 32-byte protobuf) | The identifier of a task settlement record. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | An absent value must be omitted; a present value must satisfy the bytes encoding and width the descriptor declares, and null or empty bytes must not be used to stand in for absence. |
| `failure.failure_class` | `task.v1.TaskFailureClass` enum | failure class; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `failure.freeze_signal_eligible` | `bool` | freeze signal eligible; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `failure.classification_source` | `shared.v1.FailureClassificationSource` enum | classification source; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `failure.classified_height` | `uint64` / JSON decimal string | classified height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `failure.infer_receipt_hash_or_zero32` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | infer receipt hash or zero32; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `failure.settlement_facts_hash_or_zero32` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | settlement facts hash or zero32; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `failure.settlement_id_or_zero32` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | settlement id or zero32; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `failure.superseded_by_verify_round` | `uint32` | superseded by verify round; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `failure.accepted_commit_count` | `uint32` | accepted commit count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `failure.accepted_result_receipt_count` | `uint32` | accepted result receipt count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `failure.task_finality_height` | `uint64` / JSON decimal string | task finality height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `failure.prune_height` | `uint64` / JSON decimal string | prune height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `failure.evidence_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | evidence digest; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-taskgasreimbursements"></a> `task.TaskGasReimbursements`

- gRPC: `/task.v1.Query/TaskGasReimbursements`
- REST: `GET /TrueOpen/task/v1/task/{task_id}/gas_reimbursements`
- Request/response: `task.v1.QueryTaskGasReimbursementsRequest` -> `task.v1.QueryTaskGasReimbursementsResponse`
- Purpose: Reads the authoritative on-chain state behind `TaskGasReimbursements`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `page` | `shared.v1.QueryPageRequestV1` object | The batch page or the pagination cursor. | Built according to the field rules of this nested message; amounts use a legal Coin denom and pagination uses QueryPageRequest. | The object must not be nil; type, amount, pagination ceiling and business consistency checks run recursively. |
| `page.page_token` | `bytes` / base64 string | page_token is empty on the first page and otherwise a canonical PageTokenV1. It is an opaque variable-length cursor blob, not a Hash32, so REST projects it as ProtoJSON Base64. | Encoded deterministically according to the corresponding protocol structure; REST/JSON uses base64. | Checked for non-emptiness/length/ceiling, plus hash, signature, Merkle proof or canonical encoding verification where applicable. |
| `page.limit` | `uint32` | The ceiling on a single query/processing pass, which prevents unbounded gas and responses. | Taken from the Query Params / operational batch configuration; choose a positive integer no larger than the on-chain hard ceiling. | Must be within the permitted range; Node additionally takes the smaller of the parameter ceiling and the requested value, and neither zero nor an oversized value may be used to trigger a full scan. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `items` | array<`task.v1.TaskGasReimbursementV1` object> | items list; the semantics of an element are given by its message/enum definition. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once. |
| `page` | `shared.v1.QueryPageResponseV1` object | The batch page or the pagination cursor. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `page.next_page_token` | `bytes` / base64 string | next_page_token is empty when the walk is complete. Same opaque PageTokenV1 blob as page_token, so REST projects it as ProtoJSON Base64. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-taskstage"></a> `task.TaskStage`

- gRPC: `/task.v1.Query/TaskStage`
- REST: `GET /TrueOpen/task/v1/task/{task_id}/stage`
- Request/response: `task.v1.QueryTaskStageRequest` -> `task.v1.QueryTaskStageResponse`
- Purpose: Reads the authoritative on-chain state behind `TaskStage`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `stage` | `task.v1.TaskStageViewV1` object | The Builder protocol stage (Stage1/2/3). | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `stage.task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `stage.task_phase` | `task.v1.TaskPhase` enum | task phase; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `stage.assignment_status` | `task.v1.AssignmentStatus` enum | assignment status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `stage.receipt_status` | `task.v1.ReceiptStatus` enum | receipt status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `stage.verification_status` | `task.v1.VerificationStatus` enum | verification status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `stage.settlement_status` | `task.v1.SettlementStatus` enum | settlement status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `stage.finality_status` | `shared.v1.TaskFinalityStatusV1` enum | finality status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `stage.next_deadline_kind` | `task.v1.DeadlineKindV1` enum | next deadline kind; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `stage.next_deadline_height` | `uint64` / JSON decimal string | next deadline height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `stage.updated_height` | `uint64` / JSON decimal string | updated height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `stage.effective_verify_round` | `uint32` | effective verify round; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-verificationround"></a> `task.VerificationRound`

- gRPC: `/task.v1.Query/VerificationRound`
- REST: `GET /TrueOpen/task/v1/task/{task_id}/verification_round/{verify_round}`
- Request/response: `task.v1.QueryVerificationRoundRequest` -> `task.v1.QueryVerificationRoundResponse`
- Purpose: Reads the authoritative on-chain state behind `VerificationRound`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `verify_round` | `uint32` | The verification round; read from the OpenVerify/VerifierAssignment state. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `round` | `task.v1.VerificationRoundState` object | round; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `round.task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `round.task_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | task hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `round.verify_round` | `uint32` | The verification round; read from the OpenVerify/VerifierAssignment state. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `round.round_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | round id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `round.opener_address` | `string` | opener address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `round.prev_round_facts_hash` | optional `Hash32` / omitted when absent; lowercase 64-hex client value when present (raw 32-byte protobuf) | prev round facts hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | An absent value must be omitted; a present value must satisfy the bytes encoding and width the descriptor declares, and null or empty bytes must not be used to stand in for absence. |
| `round.prev_round_verdict` | `task.v1.TaskVerdict` enum | prev round verdict; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `round.round_open_height` | `uint64` / JSON decimal string | round open height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `round.round_close_deadline_height` | `uint64` / JSON decimal string | round close deadline height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `round.closed_height` | `uint64` / JSON decimal string | closed height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `round.infer_receipt_ref` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | infer receipt ref; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `round.result_receipt_refs_hash` | optional `Hash32` / omitted when absent; lowercase 64-hex client value when present (raw 32-byte protobuf) | result receipt refs hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | An absent value must be omitted; a present value must satisfy the bytes encoding and width the descriptor declares, and null or empty bytes must not be used to stand in for absence. |
| `round.consensus_cluster_hash` | optional `Hash32` / omitted when absent; lowercase 64-hex client value when present (raw 32-byte protobuf) | consensus cluster hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | An absent value must be omitted; a present value must satisfy the bytes encoding and width the descriptor declares, and null or empty bytes must not be used to stand in for absence. |
| `round.generated_token_count` | `uint64` / JSON decimal string | generated token count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `round.verdict` | `task.v1.TaskVerdict` enum | verdict; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `round.failure_class` | `task.v1.TaskFailureClass` enum | failure class; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `round.round_outcome` | `shared.v1.RoundOutcomeV1` enum | round outcome; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `round.profile_execution_snapshot_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | profile execution snapshot hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `round.generation_params_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | generation params digest; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `round.round_facts_hash` | optional `Hash32` / omitted when absent; lowercase 64-hex client value when present (raw 32-byte protobuf) | round facts hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | An absent value must be omitted; a present value must satisfy the bytes encoding and width the descriptor declares, and null or empty bytes must not be used to stand in for absence. |
| `round.round_effect_count` | `uint32` | round effect count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `round.round_effect_plan_root` | optional `Hash32` / omitted when absent; lowercase 64-hex client value when present (raw 32-byte protobuf) | round effect plan root; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | An absent value must be omitted; a present value must satisfy the bytes encoding and width the descriptor declares, and null or empty bytes must not be used to stand in for absence. |
| `round.round_effect_root` | optional `Hash32` / omitted when absent; lowercase 64-hex client value when present (raw 32-byte protobuf) | round effect root; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | An absent value must be omitted; a present value must satisfy the bytes encoding and width the descriptor declares, and null or empty bytes must not be used to stand in for absence. |
| `round.funding_lock_hash` | optional `Hash32` / omitted when absent; lowercase 64-hex client value when present (raw 32-byte protobuf) | funding lock hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | An absent value must be omitted; a present value must satisfy the bytes encoding and width the descriptor declares, and null or empty bytes must not be used to stand in for absence. |
| `round.funding_resolution_hash` | optional `Hash32` / omitted when absent; lowercase 64-hex client value when present (raw 32-byte protobuf) | funding resolution hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | An absent value must be omitted; a present value must satisfy the bytes encoding and width the descriptor declares, and null or empty bytes must not be used to stand in for absence. |

### <a id="rpc-task-verifierassignment"></a> `task.VerifierAssignment`

- gRPC: `/task.v1.Query/VerifierAssignment`
- REST: `GET /TrueOpen/task/v1/task/{task_id}/verifier_assignment/{verify_round}`
- Request/response: `task.v1.QueryVerifierAssignmentRequest` -> `task.v1.QueryVerifierAssignmentResponse`
- Purpose: Reads the authoritative on-chain state behind `VerifierAssignment`, for building the next stage's request, for auditing, or for recovery.
- Stage: Stage1: task assignment and budget freezing.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `verify_round` | `uint32` | The verification round; read from the OpenVerify/VerifierAssignment state. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `assignment` | `task.v1.VerifierAssignmentState` object | assignment; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `assignment.task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.verify_round` | `uint32` | The verification round; read from the OpenVerify/VerifierAssignment state. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.open_verify_height` | `uint64` / JSON decimal string | open verify height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.verifier_candidate_window_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | verifier candidate window hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.verifier_legal_set_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | verifier legal set hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.selected_verifiers_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | selected verifiers hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.selection_randomness_height` | `uint64` / JSON decimal string | selection randomness height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.selection_randomness_beacon` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | selection randomness beacon; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.selected_verifiers` | array<`task.v1.SelectedVerifierV1` object> | selected verifiers list; the semantics of an element are given by its message/enum definition. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once. |
| `assignment.selected_verifier_count` | `uint32` | selected verifier count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.verifier_handraise_count` | `uint32` | verifier handraise count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.commit_deadline_height` | `uint64` / JSON decimal string | commit deadline height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.reveal_deadline_height` | `uint64` / JSON decimal string | reveal deadline height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `assignment.verify_deadline_height` | `uint64` / JSON decimal string | verify deadline height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-verifiercandidatewindow"></a> `task.VerifierCandidateWindow`

- gRPC: `/task.v1.Query/VerifierCandidateWindow`
- REST: `GET /TrueOpen/task/v1/task/{task_id}/verifier_window/{verify_round}`
- Request/response: `task.v1.QueryVerifierCandidateWindowRequest` -> `task.v1.QueryVerifierCandidateWindowResponse`
- Purpose: Reads the authoritative on-chain state behind `VerifierCandidateWindow`, for building the next stage's request, for auditing, or for recovery.
- Stage: The governance, registry or protocol operations stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `verify_round` | `uint32` | The verification round; read from the OpenVerify/VerifierAssignment state. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `window` | `task.v1.VerifierCandidateWindowState` object | window; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `window.schema_version` | `uint32` | schema version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `window.task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `window.verify_round` | `uint32` | The verification round; read from the OpenVerify/VerifierAssignment state. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `window.infer_receipt_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | infer receipt hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `window.candidate_pool_snapshot_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | candidate pool snapshot id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `window.candidate_pool_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | candidate pool hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `window.eligibility_frozen_height` | `uint64` / JSON decimal string | eligibility frozen height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `window.eligible_count` | `uint32` | eligible count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `window.verifier_window_source_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | verifier window source hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `window.eligibility_segment_count` | `uint32` | eligibility segment count; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `window.window_randomness_height` | `uint64` / JSON decimal string | window randomness height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `window.window_randomness_beacon` | optional `Hash32` / omitted when absent; lowercase 64-hex client value when present (raw 32-byte protobuf) | window randomness beacon; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | An absent value must be omitted; a present value must satisfy the bytes encoding and width the descriptor declares, and null or empty bytes must not be used to stand in for absence. |
| `window.builder_proposal_close_height` | `uint64` / JSON decimal string | builder proposal close height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `window.handraise_close_height` | `uint64` / JSON decimal string | handraise close height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `window.selection_randomness_height` | `uint64` / JSON decimal string | selection randomness height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `window.assignment_deadline_height` | `uint64` / JSON decimal string | assignment deadline height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `window.window_size` | `uint32` | window size; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `window.verifier_window_hash` | optional `Hash32` / omitted when absent; lowercase 64-hex client value when present (raw 32-byte protobuf) | verifier window hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | An absent value must be omitted; a present value must satisfy the bytes encoding and width the descriptor declares, and null or empty bytes must not be used to stand in for absence. |
| `window.status` | `task.v1.VerifierCandidateWindowStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `window.generated_height` | `uint64` / JSON decimal string | generated height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `members` | array<`task.v1.VerifierCandidateWindowMemberState` object> | members list; the semantics of an element are given by its message/enum definition. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | The list order is decided by the Keeper index; clients need pagination / stable-key deduplication and must not assume the whole set comes back at once. |

### <a id="rpc-task-verifycommit"></a> `task.VerifyCommit`

- gRPC: `/task.v1.Query/VerifyCommit`
- REST: `GET /TrueOpen/task/v1/task/{task_id}/commit/{verify_round}/{verifier_operator_address}`
- Request/response: `task.v1.QueryVerifyCommitRequest` -> `task.v1.QueryVerifyCommitResponse`
- Purpose: Reads the authoritative on-chain state behind `VerifyCommit`, for building the next stage's request, for auditing, or for recovery.
- Stage: Stage2: inference receipt, verification commit/result/reveal.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `verify_round` | `uint32` | The verification round; read from the OpenVerify/VerifierAssignment state. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |
| `verifier_operator_address` | `string` | verifier operator address; this field is part of the protocol state or of the request. | Taken from the frozen verifier set of VerifierAssignment/ChallengeAssignment; the submitter cannot name an arbitrary payee. | Must be a canonical Bech32 address belonging to the formal verifier set of the corresponding round; duplicate addresses are rejected. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `commit` | `task.v1.CommitState` object | commit; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `commit.commit_key` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | commit key; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `commit.task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `commit.verify_round` | `uint32` | The verification round; read from the OpenVerify/VerifierAssignment state. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `commit.verifier_operator_address` | `string` | verifier operator address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `commit.commit_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | commit hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `commit.commit_signing_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | commit signing digest; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `commit.signature_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | SHA256(raw_signature_64), retained as a 32-byte audit/exact-replay fingerprint. The 64-byte signature was verified before initial live acceptance and is not retained. This digest is not authorization state and never enters a signing/business digest; replay compares it only to identify the same previously accepted signature. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `commit.commit_height` | `uint64` / JSON decimal string | commit height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `commit.status` | `task.v1.CommitStatusV1` enum | status; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |

### <a id="rpc-task-workerevidence"></a> `task.WorkerEvidence`

- gRPC: `/task.v1.Query/WorkerEvidence`
- REST: `GET /TrueOpen/task/v1/task/{task_id}/worker_evidence/{worker_operator_address}/{seq}`
- Request/response: `task.v1.QueryWorkerEvidenceRequest` -> `task.v1.QueryWorkerEvidenceResponse`
- Purpose: Reads the authoritative on-chain state behind `WorkerEvidence`, for building the next stage's request, for auditing, or for recovery.
- Stage: The challenge, evidence-retention and dispute adjudication stage.
- Keeper behaviour: Reads KV state by primary key/index; absence is expressed through `found` or a gRPC error, paginated queries must be bounded by limit, and no state change is triggered.

Request parameters:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read the canonical lowercase 64-hex value from the preceding Node REST response, Nexus response, event projection, or SDK Hash32 value; Msg/SDK and native gRPC adapters decode it to raw32. | Client REST/DTO values require exactly 64 lowercase hex characters with no 0x prefix or whitespace. The decoded value must be exactly 32 bytes; protobuf transactions, Keeper, Store, and hash preimages use those raw bytes. |
| `worker_operator_address` | `string` | worker operator address; this field is part of the protocol state or of the request. | Taken from the Worker identity frozen in the Assignment/InferReceipt. | Must agree with the task's accepted Worker and must satisfy the Worker duty service key signature check. |
| `seq` | `uint64` / JSON decimal string | seq; this field is part of the protocol state or of the request. | Built by the caller from the business context; values forwarded by other participants must not be trusted blindly. | Decoded according to the protobuf type, then checked for business state, uniqueness and lifecycle by the corresponding Msg/Query Keeper. |

Response fields:

| Parameter | protobuf / JSON type | Meaning | How to obtain or build it | Node validation/return constraints |
|---|---|---|---|---|
| `receipt` | `task.v1.WorkerEvidenceReceiptState` object | receipt; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Object existence is decided by found or by gRPC NotFound semantics; the inner version/height is the authoritative snapshot of the commit it came from. |
| `receipt.schema_version` | `uint32` | schema version; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.task_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | The task identifier within a session; together with session_id it forms the task primary key. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.worker_operator_address` | `string` | worker operator address; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.evidence_kind` | `task.v1.WorkerEvidenceKindV1` enum | evidence kind; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.seq` | `uint64` / JSON decimal string | seq; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.accepted_infer_receipt_hash` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | accepted infer receipt hash; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.evidence_digest` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | evidence digest; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.fault_id` | `Hash32` / lowercase 64-hex client value (raw 32-byte protobuf) | fault id; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
| `receipt.accepted_height` | `uint64` / JSON decimal string | accepted height; this field is part of the protocol state or of the request. | Read by the Node Query Keeper from the currently committed state; a Msg response is produced by this successful transaction. | Returns the authoritative value at the committed height; uint64/int64 are still decimal strings in JSON. |
