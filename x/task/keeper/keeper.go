package keeper

import (
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/address"
	corestore "cosmossdk.io/core/store"
	"github.com/cosmos/cosmos-sdk/codec"

	shared "github.com/TrueOpen/node/x/shared/types"
	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

// Keeper owns task-local state and reaches global hub state only through HubKeeper.
//
// Collection inventory. Primary rows are exactly the ones
// task/v1/genesis.proto exports; height indexes are exactly the ones
// the protocol registers for this module (the two documented
// CONTRACT-GAP exceptions are marked in types/keys.go). Deleted collections are
// deleted: fresh genesis has no compatibility collection, no "always empty"
// collection and no placeholder.
//
// Every task-scoped collection is keyed by task_id alone and every task
// deadline index by (deadline_height, task_id). task_id is already globally
// unique: H_FIELDS_V1("TRUEOPEN_TASK_ID_V1", session_id, order_sequence), as
// frozen. The old (session_id, task_id) pair added
// a second,
// non-authoritative copy of the session binding to 15 primary and 10 index keys.
type Keeper struct {
	ValueEvidenceReceipt          collections.Map[types.ValueEvidenceReceiptKey, internaltypes.ValueEvidenceReceiptStoreState]
	ValueEvidenceWindowCloseIndex collections.KeySet[types.DeadlineIndexKey]
	// Shared across dependency-injected Keeper copies; configured before execution.
	storeService          corestore.KVStoreService
	transientStoreService corestore.TransientStoreService
	cdc                   codec.Codec
	addressCodec          address.Codec
	authority             []byte
	authKeeper            types.AuthKeeper
	bankKeeper            types.BankKeeper
	hubKeeper             types.HubKeeper

	Schema     collections.Schema
	Params     collections.Item[types.TaskParamsV1]
	ParamsMeta collections.Item[types.TaskParamsMetaState]

	// Parameter bodies and bindings are primary; counts and time indexes rebuild
	// from them at Genesis import. Current bodies are never duplicated in history.
	ParamsHistory              collections.Map[uint64, types.TaskParamsHistoryState]
	TaskParamsVersion          collections.Map[types.TaskKey, uint64]
	SessionParamsVersion       collections.Map[types.SessionStoreKey, uint64]
	ParamsVersionRefCount      collections.Map[uint64, uint64]
	ParamsHistoryByHeightIndex collections.KeySet[collections.Pair[uint64, uint64]]
	ParamsHistoryPruneIndex    collections.KeySet[collections.Pair[uint64, uint64]]

	// ---- Session / Order ----
	//
	// Stream owns prefunded balances and frozen totals; TaskBudget records each
	// reservation. The custody account holds the sum of Stream balances.
	// SessionNonce is keyed by the owner's canonical address bytes.
	SessionNonce                     collections.Map[types.AddrKey, internaltypes.SessionNonceStoreState]
	Stream                           collections.Map[types.SessionStoreKey, internaltypes.StreamStoreState]
	SessionByOwnerIndex              collections.KeySet[types.SessionByOwnerKey]
	SessionLifecycleIndex            collections.KeySet[types.SessionLifecycleIndexKey]
	OrderSequence                    collections.Map[types.OrderSequenceStateKeyPair, types.OrderSequenceState]
	SessionHistoryPruneCursor        collections.Map[types.SessionStoreKey, types.SessionHistoryPruneCursorState]
	SessionHistoryPruneIndex         collections.KeySet[types.SessionHeightIndexKey]
	SessionTerminalSummary           collections.Map[types.SessionStoreKey, internaltypes.SessionTerminalSummaryStoreState]
	SessionTerminalSummaryPruneIndex collections.KeySet[types.SessionHeightIndexKey]
	SessionWithdrawIndex             collections.KeySet[types.SessionHeightIndexKey]
	SessionKeyChangeIndex            collections.KeySet[types.SessionHeightIndexKey]
	SessionTakeoverIndex             collections.KeySet[types.SessionHeightIndexKey]
	OrderCancelIndex                 collections.KeySet[types.OrderCancelIndexKeyTriple]
	TaskBudget                       collections.Map[types.TaskBudgetKey, types.TaskBudgetState]
	DispatchRecord                   collections.Map[types.TaskKey, internaltypes.DispatchRecordStoreState]
	DispatchRecordByHash             collections.Map[types.Hash32Key, types.Hash32Key]
	DispatchRecordPrune              collections.KeySet[types.DeadlineIndexKey]
	TakeoverEntryReceipt             collections.Map[types.Hash32Key, internaltypes.TakeoverEntryReceiptStoreState]
	TakeoverEntryReceiptPrune        collections.KeySet[types.DeadlineIndexKey]
	DispatchCompensationClaim        collections.Map[types.Hash32Key, internaltypes.DispatchCompensationClaimStoreState]
	DispatchClaimRebuttal            collections.KeySet[types.DeadlineIndexKey]
	DispatchClaimPrune               collections.KeySet[types.DeadlineIndexKey]
	DispatchOpenClaimByBuilder       collections.KeySet[types.RoleActiveTaskKey]
	DispatchWinnerIneligible         collections.Map[types.Hash32Key, internaltypes.DispatchWinnerIneligibleStoreState]
	DispatchWinnerIneligiblePrune    collections.KeySet[types.DeadlineIndexKey]
	DispatchChallenge                collections.Map[types.Hash32Key, internaltypes.DispatchChallengeStoreState]
	DispatchChallengeResponse        collections.KeySet[types.DeadlineIndexKey]
	DispatchChallengePrune           collections.KeySet[types.DeadlineIndexKey]
	DispatchOpenChallengeByBuilder   collections.KeySet[types.RoleActiveTaskKey]
	DispatchUpheldScope              collections.Map[[]byte, []byte]
	DispatchPendingByTask            collections.Map[types.Hash32Key, uint64]

	// ---- Task core, assignment and handraise ----
	//
	// TaskCore is the only primary of task_phase / assignment_status /
	// receipt_status / verification_status / settlement_status / challenge_status;
	// TaskStatus, TaskWorker and TaskVerifiers were duplicate projections of it and
	// of TaskAssignment.selected worker, and are deleted.
	//
	// The candidate vector exists exactly once, as TaskCandidateFact rows keyed by
	// (task_id, stage, slot). The Verifier stage header keeps the commitment
	// header, and the union bitmap segment bodies are deleted at finalize.
	TaskCore                       collections.Map[types.TaskKey, types.TaskCoreState]
	TaskAssignment                 collections.Map[types.TaskKey, internaltypes.TaskAssignmentStoreState]
	TaskCandidateFact              collections.Map[types.TaskCandidateFactKeyTriple, internaltypes.TaskCandidateFactStoreState]
	BuilderStageProposal           collections.Map[types.BuilderStageProposalKeyTriple, internaltypes.BuilderStageProposalStoreState]
	TaskStageHandraiseUnion        collections.Map[types.TaskStageKey, types.TaskStageHandraiseUnionState]
	TaskStageHandraiseUnionSegment collections.Map[types.TaskStageSegmentKeyTriple, types.TaskStageHandraiseUnionSegmentState]
	TaskCandidateFinalizeCursor    collections.Map[types.TaskStageKey, types.TaskCandidateFinalizeCursorState]
	TaskBuilderSelection           collections.Map[types.TaskKey, internaltypes.TaskBuilderSelectionStoreState]

	// TaskBucketRef is the reference-count-only side of the versioned governance
	// parameter buckets: the bodies and the current/pending pointers
	// live in x/hub. It replaces the deleted per-module TimeoutBucket copy.
	TaskBucketRef collections.Map[types.TaskBucketRefKeyTriple, types.TaskBucketRefState]

	// ---- Receipt, verifier window and verification ----
	//
	// VerifierCandidateSet is deleted: the protocol replaces the second candidate vector
	// with a frozen fixed-length eligibility bitmap plus dense rank members.
	// Commit / ResultReceipt / FullResultReveal are keyed by the commit_key,
	// which is the only registered derivation and the only one that binds chain_id.
	InferReceipt                        collections.Map[types.TaskKey, internaltypes.InferReceiptStoreState]
	VerifierCandidateWindow             collections.Map[types.VerifyRoundKey, types.VerifierCandidateWindowState]
	VerifierCandidateEligibilitySegment collections.Map[types.VerifyRoundSegmentKey, types.VerifierCandidateEligibilitySegmentState]
	VerifierCandidateWindowMember       collections.Map[types.VerifyRoundSegmentKey, internaltypes.VerifierWindowMemberStoreState]
	VerifierAssignment                  collections.Map[types.VerifyRoundKey, internaltypes.VerifierAssignmentStoreState]
	CommitState                         collections.Map[types.CommitKey, internaltypes.CommitStoreState]
	ResultReceiptState                  collections.Map[types.CommitKey, internaltypes.ResultReceiptStoreState]
	DataUnavailableReport               collections.Map[types.VerifyActorKey, internaltypes.DataUnavailableReportStoreState]
	BuilderDataUnavailableAggregate     collections.Map[types.VerifyActorKey, internaltypes.BuilderDataUnavailableAggregateStoreState]
	WorkerEvidenceReceipt               collections.Map[types.WorkerEvidenceReceiptKey, internaltypes.WorkerEvidenceReceiptStoreState]

	// ---- Settlement facts, failure class and challenge summary ----
	//
	// TaskSettlement IS registered (below). PHASE0_PER_OUTPUT_TOKEN_V2 froze
	// SettlementPlan/State/Query/Event, and TaskSettlementState is keyed by
	// (task_id) with Genesis field 71 carrying it. The non-amount SettlementFactsV1 stays a pure value object;
	// only the amounts live in the stored row.
	VerificationRound                    collections.Map[types.VerifyRoundKey, internaltypes.VerificationRoundStoreState]
	RoundFunding                         collections.Map[types.VerifyRoundKey, internaltypes.RoundFundingStoreState]
	RoundEconomicEffect                  collections.Map[types.RoundEconomicEffectKey, types.RoundEconomicEffectState]
	RoundEconomicEffectApplyCursor       collections.Map[types.VerifyRoundKey, types.RoundEconomicEffectApplyCursorState]
	TaskRoundSummary                     collections.Map[types.TaskKey, types.TaskRoundSummaryState]
	TaskGasReimbursement                 collections.Map[types.TaskGasReimbursementKey, internaltypes.TaskGasReimbursementStoreState]
	VerifierPayout                       collections.Map[types.VerifierPayoutKey, internaltypes.VerifierPayoutStoreState]
	TaskSettlement                       collections.Map[types.TaskKey, internaltypes.TaskSettlementStoreState]
	SettlementFactsRetained              collections.Map[types.TaskKey, internaltypes.SettlementFactsStoreState]
	TaskFailureClass                     collections.Map[types.VerifyRoundKey, types.TaskFailureClassState]
	TaskFailureClassByProfileWindowIndex collections.KeySet[types.TaskFailureClassByProfileWindowKey]
	TaskCleanupCursor                    collections.Map[types.TaskKey, types.TaskCleanupCursorState]
	TaskTerminalSummary                  collections.Map[types.TaskKey, internaltypes.TaskTerminalSummaryStoreState]
	TaskTerminalSummaryPruneIndex        collections.KeySet[types.DeadlineIndexKey]
	EpochTaskSummaryCursor               collections.Map[uint64, types.EpochTaskSummaryCursorState]
	EpochTaskSummaryReceipt              collections.Map[uint64, types.EpochTaskSummaryReceiptState]
	EpochTaskSummarySourceIndex          collections.KeySet[types.DeadlineIndexKey]
	EpochTaskSummaryScheduleIndex        collections.KeySet[types.EpochTaskSummaryScheduleKey]
	TaskFailureClassWindowPruneIndex     collections.KeySet[types.TaskFailureClassPruneKey]

	// ---- Height indexes ----
	//
	// SampleReadyIndex and WorkerRevealDeadlineIndex are deleted (not a
	// registered height index, no DeadlineKindV1 value). TaskByModelIndex is
	// deleted (the protocol forbids a KeySet a primary prefix scan can already page).
	InferDeadlineIndex               collections.KeySet[types.DeadlineIndexKey]
	VerifyOpenDeadlineIndex          collections.KeySet[types.DeadlineIndexKey]
	CommitDeadlineIndex              collections.KeySet[types.DeadlineIndexKey]
	RevealDeadlineIndex              collections.KeySet[types.DeadlineIndexKey]
	VerifyDeadlineIndex              collections.KeySet[types.DeadlineIndexKey]
	ChallengeWindowCloseIndex        collections.KeySet[types.DeadlineIndexKey]
	SettlementDeadlineIndex          collections.KeySet[types.DeadlineIndexKey]
	EvidenceCleanupIndex             collections.KeySet[types.DeadlineIndexKey]
	VerifierWindowBuildIndex         collections.KeySet[types.VerifyRoundIndexKey]
	VerifierHandraiseCloseIndex      collections.KeySet[types.VerifyRoundIndexKey]
	VerifierSelectionRandomnessIndex collections.KeySet[types.VerifyRoundIndexKey]
	RoundEconomicEffectApplyIndex    collections.KeySet[types.VerifyRoundIndexKey]
	WorkerActiveTaskIndex            collections.KeySet[types.RoleActiveTaskKey]
	VerifierActiveJobIndex           collections.KeySet[types.RoleActiveTaskKey]
}

func NewKeeper(storeService corestore.KVStoreService, transientStoreService corestore.TransientStoreService, cdc codec.Codec, addressCodec address.Codec, authority []byte, authKeeper types.AuthKeeper, bankKeeper types.BankKeeper, hubKeeper types.HubKeeper) Keeper {
	if transientStoreService == nil {
		panic("task keeper requires a transient store service")
	}
	if _, err := addressCodec.BytesToString(authority); err != nil {
		panic(fmt.Sprintf("invalid %s authority address %s: %s", types.ModuleName, authority, err))
	}
	if authKeeper == nil {
		panic("task keeper requires an auth keeper")
	}
	if bankKeeper == nil {
		panic("task keeper requires a bank keeper")
	}
	if hubKeeper == nil {
		panic("task keeper requires a hub keeper")
	}

	sb := collections.NewSchemaBuilder(storeService)

	// Key codecs. Every one of them mirrors a key tuple that
	// the protocol spells out; nothing here re-encodes an integer or a
	// Hash32 as text.
	//
	// Every Hash32 component (task_id, session_id, commit_key, proposal_digest) is
	// types.Hash32KeyCodec: the raw 32 bytes, fixed width, no length prefix and no
	// NUL terminator. types/key_codec.go carries the full rationale, the reason the
	// switch away from lowercase hex leaves every iteration order unchanged, and
	// the no-migration policy that makes any later change to these codecs a state
	// migration rather than a refactor.
	//
	// Address components use raw codec bytes, while collections.StringKey remains
	// only for true text such as the governance-chosen bucket key.
	sessionOwnerKeyCodec := collections.PairKeyCodec(types.AddrKeyCodec, types.Hash32KeyCodec)
	sessionLifecycleKeyCodec := collections.TripleKeyCodec(collections.Uint64Key, types.Hash32KeyCodec, collections.Int32Key)
	sessionHeightIndexKeyCodec := collections.PairKeyCodec(collections.Uint64Key, types.Hash32KeyCodec)
	orderSequenceKeyCodec := collections.PairKeyCodec(types.Hash32KeyCodec, collections.Uint64Key)
	taskStageKeyCodec := collections.PairKeyCodec(types.Hash32KeyCodec, collections.Int32Key)
	taskStageSlotKeyCodec := collections.TripleKeyCodec(types.Hash32KeyCodec, collections.Int32Key, collections.Uint32Key)
	taskStageDigestKeyCodec := collections.TripleKeyCodec(types.Hash32KeyCodec, collections.Int32Key, types.Hash32KeyCodec)
	verifyRoundKeyCodec := collections.PairKeyCodec(types.Hash32KeyCodec, collections.Uint32Key)
	verifyRoundSegmentKeyCodec := collections.TripleKeyCodec(types.Hash32KeyCodec, collections.Uint32Key, collections.Uint32Key)
	addressStringKeyCodec := shared.AddressStringKeyCodec{AddressCodec: addressCodec}
	verifyActorKeyCodec := collections.TripleKeyCodec(types.Hash32KeyCodec, collections.Uint32Key, addressStringKeyCodec)
	workerEvidenceReceiptKeyCodec := collections.QuadKeyCodec(types.Hash32KeyCodec, addressStringKeyCodec, collections.Int32Key, collections.Uint64Key)
	verifyRoundIndexKeyCodec := collections.TripleKeyCodec(collections.Uint64Key, types.Hash32KeyCodec, collections.Uint32Key)
	taskGasReimbursementKeyCodec := collections.TripleKeyCodec(types.Hash32KeyCodec, types.Hash32KeyCodec, collections.Uint32Key)
	deadlineKeyCodec := collections.PairKeyCodec(collections.Uint64Key, types.Hash32KeyCodec)
	roleActiveTaskKeyCodec := collections.PairKeyCodec(types.AddrKeyCodec, types.Hash32KeyCodec)
	taskBucketRefKeyCodec := collections.TripleKeyCodec(types.Hash32KeyCodec, collections.Int32Key, collections.StringKey)
	failureClassOrderKeyCodec := collections.PairKeyCodec(collections.Int32Key, types.Hash32KeyCodec)
	failureClassWindowKeyCodec := collections.QuadKeyCodec(types.Hash32KeyCodec, collections.Uint32Key, collections.Uint64Key, failureClassOrderKeyCodec)
	epochTaskSummaryScheduleKeyCodec := collections.PairKeyCodec(collections.Uint64Key, collections.Uint64Key)

	k := Keeper{
		storeService: storeService, transientStoreService: transientStoreService,
		cdc:          cdc,
		addressCodec: addressCodec,
		authority:    authority,
		authKeeper:   authKeeper,
		bankKeeper:   bankKeeper,
		hubKeeper:    hubKeeper,

		Params:     collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.TaskParamsV1](cdc)),
		ParamsMeta: collections.NewItem(sb, types.ParamsMetaKey, "params_meta", codec.CollValue[types.TaskParamsMetaState](cdc)),

		ValueEvidenceReceipt:          collections.NewMap(sb, types.ValueEvidenceReceiptStateKey, "value_evidence_receipt", collections.QuadKeyCodec(types.Hash32KeyCodec, collections.Uint32Key, types.AddrKeyCodec, collections.Uint64Key), codec.CollValue[internaltypes.ValueEvidenceReceiptStoreState](cdc)),
		ValueEvidenceWindowCloseIndex: collections.NewKeySet(sb, types.ValueEvidenceWindowCloseKey, "value_evidence_window_close", collections.PairKeyCodec(collections.Uint64Key, types.Hash32KeyCodec)),
		ParamsVersionRefCount:         collections.NewMap(sb, types.ParamsVersionRefCountKey, "params_version_ref_count", collections.Uint64Key, collections.Uint64Value),
		ParamsHistoryByHeightIndex:    collections.NewKeySet(sb, types.ParamsHistoryByHeightIndexKey, "params_history_by_height", collections.PairKeyCodec(collections.Uint64Key, collections.Uint64Key)),
		ParamsHistoryPruneIndex:       collections.NewKeySet(sb, types.ParamsHistoryPruneIndexKey, "params_history_prune", collections.PairKeyCodec(collections.Uint64Key, collections.Uint64Key)),
		ParamsHistory:                 collections.NewMap(sb, types.ParamsHistoryKey, "params_history", collections.Uint64Key, codec.CollValue[types.TaskParamsHistoryState](cdc)),
		TaskParamsVersion:             collections.NewMap(sb, types.TaskParamsVersionKey, "task_params_version", types.Hash32KeyCodec, collections.Uint64Value),
		SessionParamsVersion:          collections.NewMap(sb, types.SessionParamsVersionKey, "session_params_version", types.Hash32KeyCodec, collections.Uint64Value),

		SessionNonce:                     collections.NewMap(sb, types.SessionNonceKey, "session_nonce", types.AddrKeyCodec, codec.CollValue[internaltypes.SessionNonceStoreState](cdc)),
		Stream:                           collections.NewMap(sb, types.StreamStateKey, "stream_state", types.Hash32KeyCodec, codec.CollValue[internaltypes.StreamStoreState](cdc)),
		SessionByOwnerIndex:              collections.NewKeySet(sb, types.SessionByOwnerIndexKey, "session_by_owner_index", sessionOwnerKeyCodec),
		SessionLifecycleIndex:            collections.NewKeySet(sb, types.SessionLifecycleIndexKeyPrefix, "session_lifecycle_index", sessionLifecycleKeyCodec),
		OrderSequence:                    collections.NewMap(sb, types.OrderSequenceStateKey, "order_sequence", orderSequenceKeyCodec, codec.CollValue[types.OrderSequenceState](cdc)),
		SessionHistoryPruneCursor:        collections.NewMap(sb, types.SessionHistoryPruneCursorKey, "session_history_prune_cursor", types.Hash32KeyCodec, codec.CollValue[types.SessionHistoryPruneCursorState](cdc)),
		SessionHistoryPruneIndex:         collections.NewKeySet(sb, types.SessionHistoryPruneIndexKey, "session_history_prune_index", sessionHeightIndexKeyCodec),
		SessionTerminalSummary:           collections.NewMap(sb, types.SessionTerminalSummaryKey, "session_terminal_summary", types.Hash32KeyCodec, codec.CollValue[internaltypes.SessionTerminalSummaryStoreState](cdc)),
		SessionTerminalSummaryPruneIndex: collections.NewKeySet(sb, types.SessionTerminalSummaryPruneIndexKey, "session_terminal_summary_prune_index", sessionHeightIndexKeyCodec),
		SessionWithdrawIndex:             collections.NewKeySet(sb, types.SessionWithdrawIndexKey, "session_withdraw_index", sessionHeightIndexKeyCodec),
		SessionKeyChangeIndex:            collections.NewKeySet(sb, types.SessionKeyChangeIndexKey, "session_key_change_index", sessionHeightIndexKeyCodec),
		SessionTakeoverIndex:             collections.NewKeySet(sb, types.SessionTakeoverIndexKey, "session_takeover_index", sessionHeightIndexKeyCodec),
		OrderCancelIndex:                 collections.NewKeySet(sb, types.OrderCancelIndexKey, "order_cancel_index", collections.TripleKeyCodec(collections.Uint64Key, types.Hash32KeyCodec, collections.Uint64Key)),
		TaskBudget:                       collections.NewMap(sb, types.TaskBudgetKeyPrefix, "task_budget", types.Hash32KeyCodec, codec.CollValue[types.TaskBudgetState](cdc)),
		DispatchRecord:                   collections.NewMap(sb, types.DispatchRecordKey, "dispatch_record", types.Hash32KeyCodec, codec.CollValue[internaltypes.DispatchRecordStoreState](cdc)),
		DispatchRecordByHash:             collections.NewMap(sb, types.DispatchRecordByHashKey, "dispatch_record_by_hash", types.Hash32KeyCodec, collections.BytesValue),
		DispatchRecordPrune:              collections.NewKeySet(sb, types.DispatchRecordPruneKey, "dispatch_record_prune", deadlineKeyCodec),
		TakeoverEntryReceipt:             collections.NewMap(sb, types.TakeoverEntryReceiptKey, "takeover_entry_receipt", types.Hash32KeyCodec, codec.CollValue[internaltypes.TakeoverEntryReceiptStoreState](cdc)),
		TakeoverEntryReceiptPrune:        collections.NewKeySet(sb, types.TakeoverEntryReceiptPruneKey, "takeover_entry_receipt_prune", deadlineKeyCodec),
		DispatchCompensationClaim:        collections.NewMap(sb, types.DispatchCompensationClaimKey, "dispatch_compensation_claim", types.Hash32KeyCodec, codec.CollValue[internaltypes.DispatchCompensationClaimStoreState](cdc)),
		DispatchClaimRebuttal:            collections.NewKeySet(sb, types.DispatchClaimRebuttalKey, "dispatch_claim_rebuttal", deadlineKeyCodec),
		DispatchClaimPrune:               collections.NewKeySet(sb, types.DispatchClaimPruneKey, "dispatch_claim_prune", deadlineKeyCodec),
		DispatchOpenClaimByBuilder:       collections.NewKeySet(sb, types.DispatchOpenClaimByBuilderKey, "dispatch_open_claim_by_builder", roleActiveTaskKeyCodec),
		DispatchWinnerIneligible:         collections.NewMap(sb, types.DispatchWinnerIneligibleKey, "dispatch_winner_ineligible", types.Hash32KeyCodec, codec.CollValue[internaltypes.DispatchWinnerIneligibleStoreState](cdc)),
		DispatchWinnerIneligiblePrune:    collections.NewKeySet(sb, types.DispatchWinnerIneligiblePruneKey, "dispatch_winner_ineligible_prune", deadlineKeyCodec),
		DispatchChallenge:                collections.NewMap(sb, types.DispatchChallengeKey, "dispatch_challenge", types.Hash32KeyCodec, codec.CollValue[internaltypes.DispatchChallengeStoreState](cdc)),
		DispatchChallengeResponse:        collections.NewKeySet(sb, types.DispatchChallengeResponseKey, "dispatch_challenge_response", deadlineKeyCodec),
		DispatchChallengePrune:           collections.NewKeySet(sb, types.DispatchChallengePruneKey, "dispatch_challenge_prune", deadlineKeyCodec),
		DispatchOpenChallengeByBuilder:   collections.NewKeySet(sb, types.DispatchOpenChallengeByBuilderKey, "dispatch_open_challenge_by_builder", roleActiveTaskKeyCodec),
		DispatchUpheldScope:              collections.NewMap(sb, types.DispatchUpheldScopeKey, "dispatch_upheld_scope", collections.BytesKey, collections.BytesValue),
		DispatchPendingByTask:            collections.NewMap(sb, types.DispatchPendingByTaskKey, "dispatch_pending_by_task", types.Hash32KeyCodec, collections.Uint64Value),

		TaskCore: collections.NewMap(sb, types.TaskCoreKey, "task_core", types.Hash32KeyCodec, taskCoreValueCodec{
			addressCodec: addressCodec,
			public:       codec.CollValue[types.TaskCoreState](cdc),
			private:      codec.CollValue[internaltypes.TaskCoreStoreState](cdc),
		}),
		TaskAssignment:                 collections.NewMap(sb, types.TaskAssignmentKey, "task_assignment", types.Hash32KeyCodec, codec.CollValue[internaltypes.TaskAssignmentStoreState](cdc)),
		TaskCandidateFact:              collections.NewMap(sb, types.TaskCandidateFactKey, "task_candidate_fact", taskStageSlotKeyCodec, codec.CollValue[internaltypes.TaskCandidateFactStoreState](cdc)),
		BuilderStageProposal:           collections.NewMap(sb, types.BuilderStageProposalKey, "builder_stage_proposal", taskStageDigestKeyCodec, codec.CollValue[internaltypes.BuilderStageProposalStoreState](cdc)),
		TaskStageHandraiseUnion:        collections.NewMap(sb, types.TaskStageHandraiseUnionKey, "task_stage_handraise_union", taskStageKeyCodec, codec.CollValue[types.TaskStageHandraiseUnionState](cdc)),
		TaskStageHandraiseUnionSegment: collections.NewMap(sb, types.TaskStageHandraiseUnionSegmentKey, "task_stage_handraise_union_segment", taskStageSlotKeyCodec, codec.CollValue[types.TaskStageHandraiseUnionSegmentState](cdc)),
		TaskCandidateFinalizeCursor:    collections.NewMap(sb, types.TaskCandidateFinalizeCursorKey, "task_candidate_finalize_cursor", taskStageKeyCodec, codec.CollValue[types.TaskCandidateFinalizeCursorState](cdc)),
		TaskBuilderSelection:           collections.NewMap(sb, types.TaskBuilderSelectionKey, "task_builder_selection", types.Hash32KeyCodec, codec.CollValue[internaltypes.TaskBuilderSelectionStoreState](cdc)),
		TaskBucketRef:                  collections.NewMap(sb, types.TaskBucketRefKey, "task_bucket_ref", taskBucketRefKeyCodec, codec.CollValue[types.TaskBucketRefState](cdc)),

		InferReceipt:                        collections.NewMap(sb, types.InferReceiptKey, "infer_receipt", types.Hash32KeyCodec, codec.CollValue[internaltypes.InferReceiptStoreState](cdc)),
		VerifierCandidateWindow:             collections.NewMap(sb, types.VerifierCandidateWindowKey, "verifier_candidate_window", verifyRoundKeyCodec, codec.CollValue[types.VerifierCandidateWindowState](cdc)),
		VerifierCandidateEligibilitySegment: collections.NewMap(sb, types.VerifierCandidateEligibilitySegmentKey, "verifier_candidate_eligibility_segment", verifyRoundSegmentKeyCodec, codec.CollValue[types.VerifierCandidateEligibilitySegmentState](cdc)),
		VerifierCandidateWindowMember:       collections.NewMap(sb, types.VerifierCandidateWindowMemberKey, "verifier_candidate_window_member", verifyRoundSegmentKeyCodec, codec.CollValue[internaltypes.VerifierWindowMemberStoreState](cdc)),
		VerifierAssignment:                  collections.NewMap(sb, types.VerifierAssignmentKey, "verifier_assignment", verifyRoundKeyCodec, codec.CollValue[internaltypes.VerifierAssignmentStoreState](cdc)),
		CommitState:                         collections.NewMap(sb, types.CommitStateKey, "commit_state", types.Hash32KeyCodec, codec.CollValue[internaltypes.CommitStoreState](cdc)),
		ResultReceiptState:                  collections.NewMap(sb, types.ResultReceiptStateKey, "result_receipt_state", types.Hash32KeyCodec, codec.CollValue[internaltypes.ResultReceiptStoreState](cdc)),
		DataUnavailableReport:               collections.NewMap(sb, types.DataUnavailableReportKey, "data_unavailable_report", verifyActorKeyCodec, codec.CollValue[internaltypes.DataUnavailableReportStoreState](cdc)),
		BuilderDataUnavailableAggregate:     collections.NewMap(sb, types.BuilderDataUnavailableAggregateKey, "builder_data_unavailable_aggregate", verifyActorKeyCodec, codec.CollValue[internaltypes.BuilderDataUnavailableAggregateStoreState](cdc)),
		WorkerEvidenceReceipt:               collections.NewMap(sb, types.WorkerEvidenceReceiptStateKey, "worker_evidence_receipt", workerEvidenceReceiptKeyCodec, codec.CollValue[internaltypes.WorkerEvidenceReceiptStoreState](cdc)),

		VerificationRound:                    collections.NewMap(sb, types.VerificationRoundStateKey, "verification_round", verifyRoundKeyCodec, codec.CollValue[internaltypes.VerificationRoundStoreState](cdc)),
		RoundFunding:                         collections.NewMap(sb, types.RoundFundingStateKey, "round_funding", verifyRoundKeyCodec, codec.CollValue[internaltypes.RoundFundingStoreState](cdc)),
		RoundEconomicEffect:                  collections.NewMap(sb, types.RoundEconomicEffectStateKey, "round_economic_effect", verifyRoundSegmentKeyCodec, codec.CollValue[types.RoundEconomicEffectState](cdc)),
		RoundEconomicEffectApplyCursor:       collections.NewMap(sb, types.RoundEconomicEffectApplyCursorStateKey, "round_economic_effect_apply_cursor", verifyRoundKeyCodec, codec.CollValue[types.RoundEconomicEffectApplyCursorState](cdc)),
		TaskRoundSummary:                     collections.NewMap(sb, types.TaskRoundSummaryStateKey, "task_round_summary", types.Hash32KeyCodec, codec.CollValue[types.TaskRoundSummaryState](cdc)),
		TaskGasReimbursement:                 collections.NewMap(sb, types.TaskGasReimbursementStateKey, "task_gas_reimbursement", taskGasReimbursementKeyCodec, codec.CollValue[internaltypes.TaskGasReimbursementStoreState](cdc)),
		VerifierPayout:                       collections.NewMap(sb, types.VerifierPayoutStateKey, "verifier_payout", verifyRoundKeyCodec, codec.CollValue[internaltypes.VerifierPayoutStoreState](cdc)),
		TaskSettlement:                       collections.NewMap(sb, types.TaskSettlementStateKey, "task_settlement", types.Hash32KeyCodec, codec.CollValue[internaltypes.TaskSettlementStoreState](cdc)),
		SettlementFactsRetained:              collections.NewMap(sb, types.SettlementFactsRetainedStateKey, "settlement_facts_retained", types.Hash32KeyCodec, codec.CollValue[internaltypes.SettlementFactsStoreState](cdc)),
		TaskFailureClass:                     collections.NewMap(sb, types.TaskFailureClassStateKey, "task_failure_class", verifyRoundKeyCodec, codec.CollValue[types.TaskFailureClassState](cdc)),
		TaskFailureClassByProfileWindowIndex: collections.NewKeySet(sb, types.TaskFailureClassByProfileWindowIndexKey, "task_failure_class_by_profile_window_index", failureClassWindowKeyCodec),
		TaskCleanupCursor:                    collections.NewMap(sb, types.TaskCleanupCursorStateKey, "task_cleanup_cursor", types.Hash32KeyCodec, codec.CollValue[types.TaskCleanupCursorState](cdc)),
		TaskTerminalSummary:                  collections.NewMap(sb, types.TaskTerminalSummaryStateKey, "task_terminal_summary", types.Hash32KeyCodec, codec.CollValue[internaltypes.TaskTerminalSummaryStoreState](cdc)),
		TaskTerminalSummaryPruneIndex:        collections.NewKeySet(sb, types.TaskTerminalSummaryPruneIndexKey, "task_terminal_summary_prune_index", deadlineKeyCodec),
		EpochTaskSummaryCursor:               collections.NewMap(sb, types.EpochTaskSummaryCursorStateKey, "epoch_task_summary_cursor", collections.Uint64Key, codec.CollValue[types.EpochTaskSummaryCursorState](cdc)),
		EpochTaskSummaryReceipt:              collections.NewMap(sb, types.EpochTaskSummaryReceiptStateKey, "epoch_task_summary_receipt", collections.Uint64Key, codec.CollValue[types.EpochTaskSummaryReceiptState](cdc)),
		EpochTaskSummarySourceIndex:          collections.NewKeySet(sb, types.EpochTaskSummarySourceIndexKey, "epoch_task_summary_source_index", deadlineKeyCodec),
		EpochTaskSummaryScheduleIndex:        collections.NewKeySet(sb, types.EpochTaskSummaryScheduleIndexKey, "epoch_task_summary_schedule_index", epochTaskSummaryScheduleKeyCodec),
		TaskFailureClassWindowPruneIndex:     collections.NewKeySet(sb, types.TaskFailureClassWindowPruneIndexKey, "task_failure_class_window_prune_index", verifyRoundIndexKeyCodec),

		InferDeadlineIndex:               collections.NewKeySet(sb, types.InferDeadlineIndexKey, "infer_deadline_index", deadlineKeyCodec),
		VerifyOpenDeadlineIndex:          collections.NewKeySet(sb, types.VerifyOpenDeadlineIndexKey, "verify_open_deadline_index", deadlineKeyCodec),
		CommitDeadlineIndex:              collections.NewKeySet(sb, types.CommitDeadlineIndexKey, "commit_deadline_index", deadlineKeyCodec),
		RevealDeadlineIndex:              collections.NewKeySet(sb, types.RevealDeadlineIndexKey, "reveal_deadline_index", deadlineKeyCodec),
		VerifyDeadlineIndex:              collections.NewKeySet(sb, types.VerifyDeadlineIndexKey, "verify_deadline_index", deadlineKeyCodec),
		ChallengeWindowCloseIndex:        collections.NewKeySet(sb, types.ChallengeWindowCloseIndexKey, "challenge_window_close_index", deadlineKeyCodec),
		SettlementDeadlineIndex:          collections.NewKeySet(sb, types.SettlementDeadlineIndexKey, "settlement_deadline_index", deadlineKeyCodec),
		EvidenceCleanupIndex:             collections.NewKeySet(sb, types.EvidenceCleanupIndexKey, "evidence_cleanup_index", deadlineKeyCodec),
		VerifierWindowBuildIndex:         collections.NewKeySet(sb, types.VerifierWindowBuildIndexKey, "verifier_window_build_index", verifyRoundIndexKeyCodec),
		VerifierHandraiseCloseIndex:      collections.NewKeySet(sb, types.VerifierHandraiseCloseIndexKey, "verifier_handraise_close_index", verifyRoundIndexKeyCodec),
		VerifierSelectionRandomnessIndex: collections.NewKeySet(sb, types.VerifierSelectionRandomnessIndexKey, "verifier_selection_randomness_index", verifyRoundIndexKeyCodec),
		RoundEconomicEffectApplyIndex:    collections.NewKeySet(sb, types.RoundEconomicEffectApplyIndexKey, "round_economic_effect_apply_index", verifyRoundIndexKeyCodec),
		WorkerActiveTaskIndex:            collections.NewKeySet(sb, types.WorkerActiveTaskIndexKey, "worker_active_task_index", roleActiveTaskKeyCodec),
		VerifierActiveJobIndex:           collections.NewKeySet(sb, types.VerifierActiveJobIndexKey, "verifier_active_job_index", roleActiveTaskKeyCodec),
	}

	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema
	return k
}

func (k Keeper) GetAuthority() []byte       { return k.authority }
func (k Keeper) HubKeeper() types.HubKeeper { return k.hubKeeper }
