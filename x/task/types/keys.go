package types

import (
	"fmt"
	"strconv"
	"strings"

	"cosmossdk.io/collections"
	shared "github.com/TrueOpen/node/x/shared/types"
)

const (
	// ModuleName is the task-side session/task lifecycle module name.
	ModuleName = "task"

	// StoreKey is the task module KV store key.
	StoreKey = ModuleName

	// GovModuleName duplicates the gov module name to avoid importing x/gov in types.
	GovModuleName = "gov"

	// EscrowModuleName holds in-flight session/task budgets.
	EscrowModuleName = "task_escrow"

	// ChallengeEffectModuleName holds task-local challenge effect pools before
	// final hub-owned economic settlement.
	ChallengeEffectModuleName = "task_challenge_effect"
)

// The store prefix schema version is 1 for the fresh V1 genesis. The PR-era
// value 5 only existed to gate the migration surface that this revision deletes
// (StateVersionState / StoreMigrationState have no proto message any more).
//
// The two constants stay together while the fresh-genesis codec still exposes
// the minimum supported schema version; there is no legacy migration branch.
const (
	CurrentStoreSchemaVersion      = uint64(1)
	MinSupportedStoreSchemaVersion = uint64(1)
)

func FormatVersionedStorePrefix(component string, version uint64) (string, error) {
	if err := validateStorePrefixComponent(component); err != nil {
		return "", err
	}
	if version == 0 {
		return "", fmt.Errorf("store prefix version must be greater than 0")
	}
	return ModuleName + "/" + component + "/v" + strconv.FormatUint(version, 10), nil
}

func MustVersionedStorePrefix(component string, version uint64) collections.Prefix {
	prefix, err := FormatVersionedStorePrefix(component, version)
	if err != nil {
		panic(err)
	}
	return collections.NewPrefix(prefix)
}

func validateStorePrefixComponent(component string) error {
	if component == "" {
		return fmt.Errorf("store prefix component is required")
	}
	if strings.TrimSpace(component) != component {
		return fmt.Errorf("store prefix component must be canonical")
	}
	if strings.ContainsAny(component, "/\x00") {
		return fmt.Errorf("store prefix component must not contain slash or NUL")
	}
	return nil
}

// Every store prefix is <module>/<component>/v<schema> so that no prefix can be a
// byte prefix of another one. The raw literals this revision replaces did shadow
// each other: "task_status" vs "task_status_v2" and "session_by_owner" vs
// "session_by_owner_index" would both make a range scan over the shorter prefix
// return the longer prefix's rows.
//
// Deleted in this revision (fresh genesis: deleted is deleted, no compatibility
// prefix, no placeholder, no reserved range):
//
//	state_version, store_migration      no migration surface exists.
//	task_status, task_worker,
//	task_verifiers                      the six sub-states are only on
//	                                    TaskCoreState
//	                                    (the data-structure contract); the
//	                                    two
//	                                    projections had zero readers.
//	verifier_candidate_set              replaced by the frozen verifier window
//	                                    header/segments/members (§4.4).
//	worker_reveal_receipt_state         no worker reveal Msg exists.
//	session_escrow                      Model A has no per-session balance;
//	                                    TaskBudgetState is the only ledger (§6.2).
//	task_settlement_by_height_index     no §7 index and no §5.9 DeadlineKindV1
//	                                    value maps to it; TaskSettlementState is
//	                                    reached by its (task_id) primary key.
//	                                    (settlement_state itself IS registered —
//	                                    K-BLOCK-16 closed and the row is stored.)
//	evidence_digest_state, prune_cursor_state,
//	epoch_payload_cursor_state          replaced by the bounded Task cleanup
//	                                    cursor used by bounded Task cleanup.
//	sample_ready_index,
//	worker_reveal_deadline_index        Ruling 25: not registered in §7 and no §5.9
//	                                    DeadlineKindV1 value maps to them.
//	task_by_model_index                 Ruling 25 / §2.4: a KeySet that a primary-key
//	                                    prefix scan can already page is forbidden.
//	challenge_*, task_evidence_request_*,
//	evidence_request_deadline_index     K-BLOCK-03/04: the value messages are not
//	                                    registered, so there is no internal schema
//	                                    to key yet (§2.4).
//	timeout_bucket, idx_timeout_bucket_effective
//	                                    versioned parameter buckets live in
//	                                    x/hub; this module keeps only
//	                                    TaskBucketRefState refs (§2.4/§6.7).
var (
	ParamsKey     = collections.NewPrefix("p_task")
	ParamsMetaKey = MustVersionedStorePrefix("params_meta", CurrentStoreSchemaVersion)

	// ---- Session / Order (the data-structure contract) ----
	SessionNonceKey                     = MustVersionedStorePrefix("session_nonce", CurrentStoreSchemaVersion)
	StreamStateKey                      = MustVersionedStorePrefix("stream_state", CurrentStoreSchemaVersion)
	SessionByOwnerIndexKey              = MustVersionedStorePrefix("session_by_owner_index", CurrentStoreSchemaVersion)
	SessionLifecycleIndexKeyPrefix      = MustVersionedStorePrefix("session_lifecycle_index", CurrentStoreSchemaVersion)
	SessionHistoryPruneCursorKey        = MustVersionedStorePrefix("session_history_prune_cursor", CurrentStoreSchemaVersion)
	SessionHistoryPruneIndexKey         = MustVersionedStorePrefix("session_history_prune_index", CurrentStoreSchemaVersion)
	SessionTerminalSummaryKey           = MustVersionedStorePrefix("session_terminal_summary", CurrentStoreSchemaVersion)
	SessionTerminalSummaryPruneIndexKey = MustVersionedStorePrefix("session_terminal_summary_prune_index", CurrentStoreSchemaVersion)
	// OrderSequenceStateKey used to be the raw literal "sequence_audit", which
	// named an audit log this collection is not: §6.2 makes OrderSequenceState the
	// authoritative per-sequence status row.
	OrderSequenceStateKey = MustVersionedStorePrefix("order_sequence", CurrentStoreSchemaVersion)
	TaskBudgetKeyPrefix   = MustVersionedStorePrefix("task_budget", CurrentStoreSchemaVersion)

	// ---- Task core, assignment and handraise (§4.2/§4.3/§4.6/§6.6) ----
	TaskCoreKey                       = MustVersionedStorePrefix("task_core", CurrentStoreSchemaVersion)
	TaskAssignmentKey                 = MustVersionedStorePrefix("task_assignment", CurrentStoreSchemaVersion)
	AssignmentCandidateSetKey         = MustVersionedStorePrefix("assignment_candidate_set", CurrentStoreSchemaVersion)
	TaskCandidateFactKey              = MustVersionedStorePrefix("task_candidate_fact", CurrentStoreSchemaVersion)
	BuilderStageProposalKey           = MustVersionedStorePrefix("builder_stage_proposal", CurrentStoreSchemaVersion)
	TaskStageHandraiseUnionKey        = MustVersionedStorePrefix("task_stage_handraise_union", CurrentStoreSchemaVersion)
	TaskStageHandraiseUnionSegmentKey = MustVersionedStorePrefix("task_stage_handraise_union_segment", CurrentStoreSchemaVersion)
	TaskCandidateFinalizeCursorKey    = MustVersionedStorePrefix("task_candidate_finalize_cursor", CurrentStoreSchemaVersion)
	TaskBuilderSelectionKey           = MustVersionedStorePrefix("task_builder_selection", CurrentStoreSchemaVersion)
	TaskBucketRefKey                  = MustVersionedStorePrefix("task_bucket_ref", CurrentStoreSchemaVersion)

	// ---- Receipt, verifier window and verification (§4.4/§6.6) ----
	InferReceiptKey                        = MustVersionedStorePrefix("infer_receipt", CurrentStoreSchemaVersion)
	VerifierCandidateWindowKey             = MustVersionedStorePrefix("verifier_candidate_window", CurrentStoreSchemaVersion)
	VerifierCandidateEligibilitySegmentKey = MustVersionedStorePrefix("verifier_candidate_eligibility_segment", CurrentStoreSchemaVersion)
	VerifierCandidateWindowMemberKey       = MustVersionedStorePrefix("verifier_candidate_window_member", CurrentStoreSchemaVersion)
	VerifierAssignmentKey                  = MustVersionedStorePrefix("verifier_assignment", CurrentStoreSchemaVersion)
	CommitStateKey                         = MustVersionedStorePrefix("commit_state", CurrentStoreSchemaVersion)
	ResultReceiptStateKey                  = MustVersionedStorePrefix("result_receipt_state", CurrentStoreSchemaVersion)
	DataUnavailableReportKey               = MustVersionedStorePrefix("data_unavailable_report", CurrentStoreSchemaVersion)
	BuilderDataUnavailableAggregateKey     = MustVersionedStorePrefix("builder_data_unavailable_aggregate", CurrentStoreSchemaVersion)
	WorkerEvidenceReceiptStateKey          = MustVersionedStorePrefix("worker_evidence_receipt", CurrentStoreSchemaVersion)

	// ---- Settlement facts, failure class and challenge summary (§6.6) ----
	VerificationRoundStateKey              = MustVersionedStorePrefix("verification_round", CurrentStoreSchemaVersion)
	RoundFundingStateKey                   = MustVersionedStorePrefix("round_funding", CurrentStoreSchemaVersion)
	RoundEconomicEffectStateKey            = MustVersionedStorePrefix("round_economic_effect", CurrentStoreSchemaVersion)
	RoundEconomicEffectApplyCursorStateKey = MustVersionedStorePrefix("round_economic_effect_apply_cursor", CurrentStoreSchemaVersion)
	TaskRoundSummaryStateKey               = MustVersionedStorePrefix("task_round_summary", CurrentStoreSchemaVersion)
	TaskGasReimbursementStateKey           = MustVersionedStorePrefix("task_gas_reimbursement", CurrentStoreSchemaVersion)
	VerifierPayoutStateKey                 = MustVersionedStorePrefix("verifier_payout", CurrentStoreSchemaVersion)
	TaskSettlementStateKey                 = MustVersionedStorePrefix("task_settlement", CurrentStoreSchemaVersion)
	SettlementFactsRetainedStateKey        = MustVersionedStorePrefix("settlement_facts_retained", CurrentStoreSchemaVersion)
	TaskFailureClassStateKey               = MustVersionedStorePrefix("task_failure_class", CurrentStoreSchemaVersion)
	// The scan index, and the only consumer is the bounded, cursor-stable
	// freeze-signal window scan in keeper/freeze_signal_validation.go. Its key
	// (model_id, profile_version, finality_height, failure_class, task_id) leads
	// with the profile so that scan can be bounded; it deliberately cannot serve
	// retention, which needs prune_height leading and lives on
	// TaskFailureClassWindowPruneIndexKey below. Do not merge the two: one key
	// shape cannot give both orderings.
	TaskFailureClassByProfileWindowIndexKey = MustVersionedStorePrefix("task_failure_class_by_profile_window_index", CurrentStoreSchemaVersion)
	TaskCleanupCursorStateKey               = MustVersionedStorePrefix("task_cleanup_cursor", CurrentStoreSchemaVersion)
	TaskTerminalSummaryStateKey             = MustVersionedStorePrefix("task_terminal_summary", CurrentStoreSchemaVersion)
	TaskTerminalSummaryPruneIndexKey        = MustVersionedStorePrefix("task_terminal_summary_prune_index", CurrentStoreSchemaVersion)
	EpochTaskSummaryCursorStateKey          = MustVersionedStorePrefix("epoch_task_summary_cursor", CurrentStoreSchemaVersion)
	EpochTaskSummaryReceiptStateKey         = MustVersionedStorePrefix("epoch_task_summary_receipt", CurrentStoreSchemaVersion)
	EpochTaskSummarySourceIndexKey          = MustVersionedStorePrefix("epoch_task_summary_source_index", CurrentStoreSchemaVersion)
	EpochTaskSummaryScheduleIndexKey        = MustVersionedStorePrefix("epoch_task_summary_schedule_index", CurrentStoreSchemaVersion)
	// Retention, keyed (prune_height, task_id, verify_round) so two failure
	// classifications for one Task can expire independently.
	// keeper/task_failure_prune.go can stop at the first row past the head.
	TaskFailureClassWindowPruneIndexKey = MustVersionedStorePrefix("task_failure_class_window_prune_index", CurrentStoreSchemaVersion)

	// ---- Height indexes (§7; deadline semantics in §5.9) ----
	AssignmentRandomnessIndexKey        = MustVersionedStorePrefix("assignment_randomness_index", CurrentStoreSchemaVersion)
	InferDeadlineIndexKey               = MustVersionedStorePrefix("infer_deadline_index", CurrentStoreSchemaVersion)
	VerifyOpenDeadlineIndexKey          = MustVersionedStorePrefix("verify_open_deadline_index", CurrentStoreSchemaVersion)
	CommitDeadlineIndexKey              = MustVersionedStorePrefix("commit_deadline_index", CurrentStoreSchemaVersion)
	RevealDeadlineIndexKey              = MustVersionedStorePrefix("reveal_deadline_index", CurrentStoreSchemaVersion)
	VerifyDeadlineIndexKey              = MustVersionedStorePrefix("verify_deadline_index", CurrentStoreSchemaVersion)
	ChallengeWindowCloseIndexKey        = MustVersionedStorePrefix("challenge_window_close_index", CurrentStoreSchemaVersion)
	SettlementDeadlineIndexKey          = MustVersionedStorePrefix("settlement_deadline_index", CurrentStoreSchemaVersion)
	EvidenceCleanupIndexKey             = MustVersionedStorePrefix("evidence_cleanup_index", CurrentStoreSchemaVersion)
	VerifierWindowBuildIndexKey         = MustVersionedStorePrefix("verifier_window_build_index", CurrentStoreSchemaVersion)
	VerifierHandraiseCloseIndexKey      = MustVersionedStorePrefix("verifier_handraise_close_index", CurrentStoreSchemaVersion)
	VerifierSelectionRandomnessIndexKey = MustVersionedStorePrefix("verifier_selection_randomness_index", CurrentStoreSchemaVersion)
	RoundEconomicEffectApplyIndexKey    = MustVersionedStorePrefix("round_economic_effect_apply_index", CurrentStoreSchemaVersion)
	// WorkerActiveTaskIndexKey and VerifierActiveJobIndexKey back
	// QueryRoleActiveTasks (§16.3).
	//
	// CONTRACT-GAP (Ruling 25): §7 registers neither index. VerifierActiveJobIndex is
	// kept because the §16.3 duty selector explicitly allows the VERIFIER side and
	// there is no other bounded path for it; WorkerActiveTaskIndex is its WORKER
	// twin. Document side must register both or drop the duty selector.
	WorkerActiveTaskIndexKey  = MustVersionedStorePrefix("worker_active_task_index", CurrentStoreSchemaVersion)
	VerifierActiveJobIndexKey = MustVersionedStorePrefix("verifier_active_job_index", CurrentStoreSchemaVersion)
)

// ---- Key types ----
//
// task_id is a globally unique Hash32 derived with H_FIELDS_V1 from the raw
// session_id Hash32 and uint64_be(order_sequence), so session_id in a task key
// is redundant.
// Every task-scoped collection is keyed by task_id alone and every task deadline
// index by (deadline_height, task_id).
//
// Hash32 key components (task_id, session_id, commit_key, proposal_digest) are
// the raw 32 bytes, carried by Hash32Key and encoded by the fixed-width
// Hash32KeyCodec in key_codec.go. §0.1 forbids storing a Hash32 as an arbitrary
// string, and the lowercase-64-hex form this revision deletes doubled every one
// of these components — including inside the IAVL inner nodes, which carry the
// same keys — on a keyspace §A.2-35 sizes at millions of task rows.
//
// The switch is behaviour-neutral on ordering: lowercase hex is order-preserving
// over the underlying bytes and Hash32KeyCodec is fixed-width, so every range
// scan and every iteration order is byte-for-byte the same order as before. See
// key_codec.go for why fixed width (rather than collections.BytesKey) is what
// makes that true, and for the no-migration policy that makes changing any of
// this post-launch a state migration.

// TaskKey = task_id.
type TaskKey = Hash32Key

func NewTaskKey(taskID Hash32Key) TaskKey { return taskID }

// SessionKey = session_id. Primary of StreamState, SessionNonceState's twin
// on the owner side, SessionHistoryPruneCursorState and
// SessionTerminalSummaryState.
type SessionKey = Hash32Key

func NewSessionKey(sessionID Hash32Key) SessionKey { return sessionID }

// DeadlineIndexKey = (deadline_height, task_id). §5.9 makes the sweep order
// (deadline_height, kind_priority, primary_id); kind_priority is fixed per index,
// so ascending iteration of one index is already the frozen order.
type DeadlineIndexKey = collections.Pair[uint64, Hash32Key]

func NewDeadlineIndexKey(deadlineHeight uint64, taskID Hash32Key) DeadlineIndexKey {
	return collections.Join(deadlineHeight, taskID)
}

// EpochTaskSummaryScheduleKey = (due_height, epoch).
type EpochTaskSummaryScheduleKey = collections.Pair[uint64, uint64]

func NewEpochTaskSummaryScheduleKey(dueHeight, epoch uint64) EpochTaskSummaryScheduleKey {
	return collections.Join(dueHeight, epoch)
}

// TaskBudgetKey = task_id (§6.2). TaskBudgetState is the only funding ledger, so
// it is keyed exactly like the task it funds.
type TaskBudgetKey = Hash32Key

// SessionByOwnerKey = (owner_user_address, session_id) (§6.2). Only ACTIVE/IDLE
// streams are in this index.
type SessionByOwnerKey = collections.Pair[string, Hash32Key]

func NewSessionByOwnerKey(owner string, sessionID Hash32Key) SessionByOwnerKey {
	return collections.Join(owner, sessionID)
}

// SessionLifecycleIndexKey = (due_height, session_id, action) (§6.2). action is
// the SessionLifecycleAction enum, not free text, so MARK_IDLE and CLOSE cannot
// collide with an unregistered third spelling.
type SessionLifecycleIndexKey = collections.Triple[uint64, Hash32Key, int32]

func NewSessionLifecycleIndexKey(dueHeight uint64, sessionID Hash32Key, action SessionLifecycleAction) SessionLifecycleIndexKey {
	return collections.Join3(dueHeight, sessionID, int32(action))
}

// SessionHeightIndexKey = (height, session_id). Shared shape of
// SessionHistoryPruneIndex(eligible_height, session_id) and
// SessionTerminalSummaryPruneIndex(prune_height, session_id) (§6.2/§7).
type SessionHeightIndexKey = collections.Pair[uint64, Hash32Key]

func NewSessionHistoryPruneIndexKey(eligibleHeight uint64, sessionID Hash32Key) SessionHeightIndexKey {
	return collections.Join(eligibleHeight, sessionID)
}

func NewSessionTerminalSummaryPruneIndexKey(pruneHeight uint64, sessionID Hash32Key) SessionHeightIndexKey {
	return collections.Join(pruneHeight, sessionID)
}

// OrderSequenceStateKeyPair = (session_id, order_sequence) (§6.2). Ascending
// order_sequence inside one session is the fold order of
// TRUEOPEN_SESSION_SEQUENCE_ROOT_V1.
type OrderSequenceStateKeyPair = collections.Pair[Hash32Key, uint64]

func NewOrderSequenceStateKey(sessionID Hash32Key, orderSequence uint64) OrderSequenceStateKeyPair {
	return collections.Join(sessionID, orderSequence)
}

// TaskStageKey = (task_id, stage) (§4.2/§4.6). Key of
// TaskStageHandraiseUnionState and TaskCandidateFinalizeCursorState.
type TaskStageKey = collections.Pair[Hash32Key, int32]

func NewTaskStageKey(taskID Hash32Key, stage TaskCandidateStage) TaskStageKey {
	return collections.Join(taskID, int32(stage))
}

// TaskCandidateFactKeyTriple = (task_id, stage, slot) (§4.2). Ascending slot
// inside one stage is the §4.3 finalize and §4.5 draw order, so the frozen facts
// are read by a bounded prefix scan and never re-sorted in memory.
type TaskCandidateFactKeyTriple = collections.Triple[Hash32Key, int32, uint32]

func NewTaskCandidateFactKey(taskID Hash32Key, stage TaskCandidateStage, slot uint32) TaskCandidateFactKeyTriple {
	return collections.Join3(taskID, int32(stage), slot)
}

// TaskStageSegmentKeyTriple = (task_id, stage, segment_index) (§4.2).
type TaskStageSegmentKeyTriple = collections.Triple[Hash32Key, int32, uint32]

func NewTaskStageSegmentKey(taskID Hash32Key, stage TaskCandidateStage, segmentIndex uint32) TaskStageSegmentKeyTriple {
	return collections.Join3(taskID, int32(stage), segmentIndex)
}

// BuilderStageProposalKeyTriple = (task_id, stage, proposal_digest) (§4.2).
type BuilderStageProposalKeyTriple = collections.Triple[Hash32Key, int32, Hash32Key]

func NewBuilderStageProposalKey(taskID Hash32Key, stage TaskCandidateStage, proposalDigest Hash32Key) BuilderStageProposalKeyTriple {
	return collections.Join3(taskID, int32(stage), proposalDigest)
}

// VerifyRoundKey = (task_id, verify_round) (§4.4/§6.6). Key of
// VerifierCandidateWindowState and VerifierAssignmentState.
type VerifyRoundKey = collections.Pair[Hash32Key, uint32]

func NewVerifyRoundKey(taskID Hash32Key, verifyRound uint32) VerifyRoundKey {
	return collections.Join(taskID, verifyRound)
}

// VerifyRoundSegmentKey = (task_id, verify_round, segment_index) for the frozen
// eligibility bitmap, and rank_index for the window members (§4.4). rank_index is
// dense in [0, window_size) so the member rows are read in rank order.
type VerifyRoundSegmentKey = collections.Triple[Hash32Key, uint32, uint32]

func NewVerifierEligibilitySegmentKey(taskID Hash32Key, verifyRound, segmentIndex uint32) VerifyRoundSegmentKey {
	return collections.Join3(taskID, verifyRound, segmentIndex)
}

func NewVerifierWindowMemberKey(taskID Hash32Key, verifyRound, rankIndex uint32) VerifyRoundSegmentKey {
	return collections.Join3(taskID, verifyRound, rankIndex)
}

// VerifyActorKey = (task_id, verify_round, operator_address) (§6.6). Key of
// VerifyResultState, DataUnavailableReportState and
// BuilderDataUnavailableAggregateState. The address is the stable operator, never
// the service address.
type VerifyActorKey = collections.Triple[Hash32Key, uint32, string]

func NewVerifyActorKey(taskID Hash32Key, verifyRound uint32, operatorAddress string) VerifyActorKey {
	return collections.Join3(taskID, verifyRound, operatorAddress)
}

// WorkerEvidenceReceiptKey = (task_id, worker_operator_address,
// evidence_kind, seq). It gives both point lookup and bounded first-seq scan.
type WorkerEvidenceReceiptKey = collections.Quad[Hash32Key, string, int32, uint64]

func NewWorkerEvidenceReceiptKey(taskID Hash32Key, worker string, kind WorkerEvidenceKindV1, seq uint64) WorkerEvidenceReceiptKey {
	return collections.Join4(taskID, worker, int32(kind), seq)
}

// VerifyRoundIndexKey = (height, task_id, verify_round). Shared shape of
// VerifierWindowBuildIndex(window_randomness_height, ...),
// VerifierHandraiseCloseIndex(handraise_close_height, ...) and
// VerifierSelectionRandomnessIndex(selection_randomness_height, ...) (§4.4/§7).
type VerifyRoundIndexKey = collections.Triple[uint64, Hash32Key, uint32]

func NewVerifyRoundIndexKey(height uint64, taskID Hash32Key, verifyRound uint32) VerifyRoundIndexKey {
	return collections.Join3(height, taskID, verifyRound)
}

type TaskFailureClassPruneKey = collections.Triple[uint64, Hash32Key, uint32]

func NewTaskFailureClassPruneKey(height uint64, taskID Hash32Key, verifyRound uint32) TaskFailureClassPruneKey {
	return collections.Join3(height, taskID, verifyRound)
}

// RoundEconomicEffectKey = (task_id, verify_round, effect_index).
type RoundEconomicEffectKey = collections.Triple[Hash32Key, uint32, uint32]

func NewRoundEconomicEffectKey(taskID Hash32Key, verifyRound, effectIndex uint32) RoundEconomicEffectKey {
	return collections.Join3(taskID, verifyRound, effectIndex)
}

// TaskGasReimbursementKey = (task_id, tx_hash, item_index).
type TaskGasReimbursementKey = collections.Triple[Hash32Key, Hash32Key, uint32]

func NewTaskGasReimbursementKey(taskID, txHash Hash32Key, itemIndex uint32) TaskGasReimbursementKey {
	return collections.Join3(taskID, txHash, itemIndex)
}

// VerifierPayoutKey = (task_id, selected_verifier_index).
type VerifierPayoutKey = collections.Pair[Hash32Key, uint32]

func NewVerifierPayoutKey(taskID Hash32Key, selectedVerifierIndex uint32) VerifierPayoutKey {
	return collections.Join(taskID, selectedVerifierIndex)
}

// CommitKey = commit_key, the Hash32 of
// H_FIELDS_V1("TRUEOPEN_COMMIT_KEY_V1", chain_id, task_id, uint32_be(verify_round),
// verifier_operator_address_bytes) (§10.9). It is the sole primary key of
// CommitState / ResultReceiptState, so the previous
// (session_id, task_id, verify_round, verifier) quad is replaced rather than kept
// alongside it: §10.9 forbids a second key derivation, and the quad had no
// chain_id and therefore no cross-chain replay separation.
type CommitKey = Hash32Key

func NewCommitKey(commitKey Hash32Key) CommitKey { return commitKey }

// TaskBucketRefKeyTriple = (task_id, bucket_kind, bucket_key) (§6.7). The bodies
// and pointers live in x/hub; this module only holds the reference.
//
// bucket_key stays a string: it is a governance-chosen bucket name
// ("default", ...) owned by x/hub, not a Hash32 and not an address.
type TaskBucketRefKeyTriple = collections.Triple[Hash32Key, int32, string]

func NewTaskBucketRefKey(taskID Hash32Key, bucketKind int32, bucketKey string) TaskBucketRefKeyTriple {
	return collections.Join3(taskID, bucketKind, bucketKey)
}

// TaskFailureClassWindowOrderKey preserves the frozen
// (failure_class, task_id) suffix ordering inside one finality height.
type TaskFailureClassWindowOrderKey = collections.Pair[int32, Hash32Key]

// TaskFailureClassByProfileWindowKey = (model_id, profile_version,
// finality_height, (failure_class, task_id)).
//
// model_id stays a string: it is a Hub-owned model identifier, not a Hash32.
type TaskFailureClassByProfileWindowKey = collections.Quad[string, uint32, uint64, TaskFailureClassWindowOrderKey]

func NewTaskFailureClassByProfileWindowKey(modelID string, profileVersion uint32, finalityHeight uint64, failureClass TaskFailureClass, taskID Hash32Key) TaskFailureClassByProfileWindowKey {
	return collections.Join4(modelID, profileVersion, finalityHeight, collections.Join(int32(failureClass), taskID))
}

// RoleActiveTaskKey = (operator_address, task_id) for QueryRoleActiveTasks
// (§16.3). session_id is gone with Ruling 23.
type RoleActiveTaskKey = collections.Pair[string, Hash32Key]

func NewWorkerActiveTaskKey(workerAddress string, taskID Hash32Key) RoleActiveTaskKey {
	return collections.Join(workerAddress, taskID)
}

func NewVerifierActiveJobKey(verifierAddress string, taskID Hash32Key) RoleActiveTaskKey {
	return collections.Join(verifierAddress, taskID)
}

const (
	TaskStatusAssignRandomnessPending = "ASSIGN_RANDOMNESS_PENDING"
	TaskStatusAssigned                = "ASSIGNED"
	TaskStatusReceiptOnlyAccepted     = "RECEIPT_ONLY_ACCEPTED"
	TaskStatusVerifying               = "VERIFYING"
	TaskStatusSettled                 = "SETTLED"
	TaskStatusVerifyFailed            = "VERIFY_FAILED"
	VerificationPhaseRevealing        = "REVEALING"
)

const (
	AssignmentStatusRandomnessPending = "RANDOMNESS_PENDING"
	AssignmentStatusAssigned          = "ASSIGNED"
	AssignmentStatusAssignTimeout     = "ASSIGN_TIMEOUT"
)

const (
	ReceiptModeReceiptOnly = "RECEIPT_ONLY"
	ReceiptModeOpenVerify  = "OPEN_VERIFY"
)

const (
	SessionStatusActive = "ACTIVE"
	SessionStatusIdle   = "IDLE"
	SessionStatusClosed = "CLOSED"
)

const (
	SessionLifecycleActionMarkIdle = "MARK_IDLE"
	SessionLifecycleActionClose    = "CLOSE"
)

const (
	OrderSequenceStatusUnused    = "UNUSED"
	OrderSequenceStatusConsumed  = "CONSUMED"
	OrderSequenceStatusCancelled = "CANCELLED"
	OrderSequenceStatusRefunded  = "REFUNDED"
	OrderSequenceStatusSettled   = "SETTLED"
)

const (
	TaskBudgetStatusReserved  = "RESERVED"
	TaskBudgetStatusFinalized = "FINALIZED"
)

const (
	// v1.1 numbers the original verification as round 1 and the single
	// funded challenge as round 2. Zero is always invalid.
	VerifyRoundV1                uint32 = 1
	ChallengeVerifyRoundV1       uint32 = 2
	SelectedVerifierCountV1      uint32 = 3
	VerifierCandidateWindowMaxV1        = uint64(21)
)

const (
	DefaultTimeoutBucketKey    = "default"
	BucketSourceGenesis        = "GENESIS"
	BucketSourceGovernance     = "GOVERNANCE"
	BucketSourceDebugAuthority = "DEBUG_AUTHORITY"
)

// Domain literals are deliberately absent from this file.
// x/shared/types/domain_registry.go owns every one of them, and a second
// spelling of a domain is a second authority even when nothing references it.
// Reach a domain through shared.MustDomain, for example
// shared.MustDomain(shared.DomainSessionV1).
// SignatureSchemeSecp256k1 aliases the shared literal so signed_order and the
// shared framing layer can never drift into two spellings of one scheme.
const SignatureSchemeSecp256k1 = shared.SignatureSchemeSecp256k1
