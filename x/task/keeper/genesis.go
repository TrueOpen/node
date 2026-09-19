package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// InitGenesis imports every Wire v0.3 Task primary. Derived indexes are rebuilt
// exclusively from those primaries. The cache context makes a rejected import a
// zero-write operation.
func (k Keeper) InitGenesis(ctx context.Context, genesis types.GenesisState) error {
	if err := genesis.Validate(); err != nil {
		return err
	}
	if err := k.assertFreshGenesisStore(ctx); err != nil {
		return err
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	if err := k.initGenesisPrimaries(cache, genesis); err != nil {
		return err
	}
	if err := k.validateGenesisHubRefs(cache, genesis); err != nil {
		return err
	}
	if err := k.rebuildGenesisIndexes(cache, genesis); err != nil {
		return err
	}
	write()
	return nil
}

func (k Keeper) initGenesisPrimaries(ctx context.Context, genesis types.GenesisState) error {
	if genesis.ParamsMeta.ParamsVersion != 0 {
		expected, err := types.TaskParamsHashV1(sdk.UnwrapSDKContext(ctx).ChainID(), genesis.ParamsMeta.ParamsVersion, genesis.Params)
		if err != nil {
			return err
		}
		if !bytes.Equal(expected, genesis.ParamsMeta.ParamsHash) {
			return fmt.Errorf("params meta hash does not match TaskParamsV1")
		}
	}
	if err := k.Params.Set(ctx, genesis.Params); err != nil {
		return err
	}
	if genesis.ParamsMeta.ParamsVersion != 0 {
		if err := k.ParamsMeta.Set(ctx, genesis.ParamsMeta); err != nil {
			return err
		}
	}

	for _, state := range genesis.SessionNonces {
		if err := setUnique(ctx, k.SessionNonce, state.UserAddress, state, "session nonce"); err != nil {
			return err
		}
	}
	for _, state := range genesis.Streams {
		key, err := genesisHashKey("stream session_id", state.SessionId)
		if err != nil {
			return err
		}
		if err := setUnique(ctx, k.Stream, key, state, "stream"); err != nil {
			return err
		}
	}
	for _, state := range genesis.OrderSequences {
		sessionID, err := genesisHashKey("order sequence session_id", state.SessionId)
		if err != nil {
			return err
		}
		if err := setUnique(ctx, k.OrderSequence, types.NewOrderSequenceStateKey(sessionID, state.OrderSequence), state, "order sequence"); err != nil {
			return err
		}
	}
	for _, state := range genesis.TaskBudgets {
		key, err := genesisHashKey("task budget task_id", state.TaskId)
		if err != nil {
			return err
		}
		if err := setUnique(ctx, k.TaskBudget, key, state, "task budget"); err != nil {
			return err
		}
	}
	for _, state := range genesis.SessionHistoryPruneCursors {
		key, err := genesisHashKey("session history cursor session_id", state.SessionId)
		if err != nil {
			return err
		}
		if err := setUnique(ctx, k.SessionHistoryPruneCursor, key, state, "session history cursor"); err != nil {
			return err
		}
	}
	for _, state := range genesis.SessionTerminalSummaries {
		key, err := genesisHashKey("session terminal summary session_id", state.SessionId)
		if err != nil {
			return err
		}
		if err := setUnique(ctx, k.SessionTerminalSummary, key, state, "session terminal summary"); err != nil {
			return err
		}
	}

	for _, state := range genesis.TaskCores {
		if err := setTaskPrimary(ctx, k.TaskCore, state.TaskId, state, "task core"); err != nil {
			return err
		}
	}
	for _, state := range genesis.TaskAssignments {
		if err := setTaskPrimary(ctx, k.TaskAssignment, state.TaskId, state, "task assignment"); err != nil {
			return err
		}
	}
	for _, state := range genesis.AssignmentCandidateSets {
		if err := setTaskPrimary(ctx, k.AssignmentCandidateSet, state.TaskId, state, "assignment candidate set"); err != nil {
			return err
		}
	}
	for _, state := range genesis.InferReceipts {
		if err := setTaskPrimary(ctx, k.InferReceipt, state.TaskId, state, "infer receipt"); err != nil {
			return err
		}
	}
	for _, state := range genesis.WorkerEvidenceReceipts {
		taskID, err := genesisHashKey("worker evidence receipt task_id", state.TaskId)
		if err != nil {
			return err
		}
		key := types.NewWorkerEvidenceReceiptKey(taskID, state.WorkerOperatorAddress, state.EvidenceKind, state.Seq)
		if err := setUnique(ctx, k.WorkerEvidenceReceipt, key, state, "worker evidence receipt"); err != nil {
			return err
		}
	}
	for _, state := range genesis.TaskCandidateFacts {
		taskID, err := genesisHashKey("task candidate fact task_id", state.TaskId)
		if err != nil {
			return err
		}
		key := types.NewTaskCandidateFactKey(taskID, state.Stage, state.Slot)
		if err := setUnique(ctx, k.TaskCandidateFact, key, state, "task candidate fact"); err != nil {
			return err
		}
	}
	for _, state := range genesis.BuilderStageProposals {
		taskID, err := genesisHashKey("builder proposal task_id", state.TaskId)
		if err != nil {
			return err
		}
		digest, err := genesisHashKey("builder proposal digest", state.ProposalDigest)
		if err != nil {
			return err
		}
		key := types.NewBuilderStageProposalKey(taskID, state.Stage, digest)
		if err := setUnique(ctx, k.BuilderStageProposal, key, state, "builder proposal"); err != nil {
			return err
		}
	}
	for _, state := range genesis.TaskStageHandraiseUnions {
		taskID, err := genesisHashKey("handraise union task_id", state.TaskId)
		if err != nil {
			return err
		}
		if err := setUnique(ctx, k.TaskStageHandraiseUnion, types.NewTaskStageKey(taskID, state.Stage), state, "handraise union"); err != nil {
			return err
		}
	}
	for _, state := range genesis.TaskStageHandraiseUnionSegments {
		taskID, err := genesisHashKey("handraise union segment task_id", state.TaskId)
		if err != nil {
			return err
		}
		key := types.NewTaskStageSegmentKey(taskID, state.Stage, state.SegmentIndex)
		if err := setUnique(ctx, k.TaskStageHandraiseUnionSegment, key, state, "handraise union segment"); err != nil {
			return err
		}
	}
	for _, state := range genesis.TaskCandidateFinalizeCursors {
		taskID, err := genesisHashKey("candidate finalize cursor task_id", state.TaskId)
		if err != nil {
			return err
		}
		if err := setUnique(ctx, k.TaskCandidateFinalizeCursor, types.NewTaskStageKey(taskID, state.Stage), state, "candidate finalize cursor"); err != nil {
			return err
		}
	}
	for _, state := range genesis.TaskBuilderSelections {
		if err := setTaskPrimary(ctx, k.TaskBuilderSelection, state.TaskId, state, "task builder selection"); err != nil {
			return err
		}
	}
	for _, state := range genesis.TaskBucketRefs {
		taskID, err := genesisHashKey("task bucket ref task_id", state.TaskId)
		if err != nil {
			return err
		}
		key := types.NewTaskBucketRefKey(taskID, int32(state.BucketKind), state.BucketKey)
		if err := setUnique(ctx, k.TaskBucketRef, key, state, "task bucket ref"); err != nil {
			return err
		}
	}

	for _, state := range genesis.VerifierCandidateWindows {
		if err := setRoundPrimary(ctx, k.VerifierCandidateWindow, state.TaskId, state.VerifyRound, state, "verifier candidate window"); err != nil {
			return err
		}
	}
	for _, state := range genesis.VerifierCandidateEligibilitySegments {
		taskID, err := genesisHashKey("verifier eligibility segment task_id", state.TaskId)
		if err != nil {
			return err
		}
		key := types.NewVerifierEligibilitySegmentKey(taskID, state.VerifyRound, state.SegmentIndex)
		if err := setUnique(ctx, k.VerifierCandidateEligibilitySegment, key, state, "verifier eligibility segment"); err != nil {
			return err
		}
	}
	for _, state := range genesis.VerifierCandidateWindowMembers {
		taskID, err := genesisHashKey("verifier window member task_id", state.TaskId)
		if err != nil {
			return err
		}
		key := types.NewVerifierWindowMemberKey(taskID, state.VerifyRound, state.RankIndex)
		if err := setUnique(ctx, k.VerifierCandidateWindowMember, key, state, "verifier window member"); err != nil {
			return err
		}
	}
	for _, state := range genesis.VerifierAssignments {
		if err := setRoundPrimary(ctx, k.VerifierAssignment, state.TaskId, state.VerifyRound, state, "verifier assignment"); err != nil {
			return err
		}
	}
	for _, state := range genesis.Commits {
		key, err := genesisHashKey("commit key", state.CommitKey)
		if err != nil {
			return err
		}
		if err := setUnique(ctx, k.CommitState, key, state, "commit"); err != nil {
			return err
		}
	}
	for _, state := range genesis.ResultReceipts {
		key, err := genesisHashKey("result receipt commit_key", state.CommitKey)
		if err != nil {
			return err
		}
		if err := setUnique(ctx, k.ResultReceiptState, key, state, "result receipt"); err != nil {
			return err
		}
	}
	for _, state := range genesis.DataUnavailableReports {
		taskID, err := genesisHashKey("data unavailable report task_id", state.TaskId)
		if err != nil {
			return err
		}
		key := types.NewVerifyActorKey(taskID, state.VerifyRound, state.VerifierOperatorAddress)
		if err := setUnique(ctx, k.DataUnavailableReport, key, state, "data unavailable report"); err != nil {
			return err
		}
	}
	for _, state := range genesis.BuilderDataUnavailableAggregates {
		taskID, err := genesisHashKey("builder data unavailable aggregate task_id", state.TaskId)
		if err != nil {
			return err
		}
		key := types.NewVerifyActorKey(taskID, state.VerifyRound, state.BuilderOperatorAddress)
		if err := setUnique(ctx, k.BuilderDataUnavailableAggregate, key, state, "builder data unavailable aggregate"); err != nil {
			return err
		}
	}

	for _, state := range genesis.VerificationRounds {
		if err := setRoundPrimary(ctx, k.VerificationRound, state.TaskId, state.VerifyRound, state, "verification round"); err != nil {
			return err
		}
	}
	for _, state := range genesis.RoundFundings {
		if err := setRoundPrimary(ctx, k.RoundFunding, state.TaskId, state.VerifyRound, state, "round funding"); err != nil {
			return err
		}
	}
	for _, state := range genesis.RoundEconomicEffects {
		taskID, err := genesisHashKey("round economic effect task_id", state.TaskId)
		if err != nil {
			return err
		}
		key := types.NewRoundEconomicEffectKey(taskID, state.VerifyRound, state.Effect.EffectIndex)
		if err := setUnique(ctx, k.RoundEconomicEffect, key, state, "round economic effect"); err != nil {
			return err
		}
	}
	for _, state := range genesis.RoundEconomicEffectApplyCursors {
		if err := setRoundPrimary(ctx, k.RoundEconomicEffectApplyCursor, state.TaskId, state.VerifyRound, state, "round economic effect cursor"); err != nil {
			return err
		}
	}
	for _, state := range genesis.TaskRoundSummaries {
		if err := setTaskPrimary(ctx, k.TaskRoundSummary, state.TaskId, state, "task round summary"); err != nil {
			return err
		}
	}
	for _, state := range genesis.TaskGasReimbursements {
		taskID, err := genesisHashKey("gas reimbursement task_id", state.TaskId)
		if err != nil {
			return err
		}
		txHash, err := genesisHashKey("gas reimbursement tx_hash", state.TxHash)
		if err != nil {
			return err
		}
		key := types.NewTaskGasReimbursementKey(taskID, txHash, state.ItemIndex)
		if err := setUnique(ctx, k.TaskGasReimbursement, key, state, "task gas reimbursement"); err != nil {
			return err
		}
	}
	for _, state := range genesis.VerifierPayouts {
		taskID, err := genesisHashKey("verifier payout task_id", state.TaskId)
		if err != nil {
			return err
		}
		key := types.NewVerifierPayoutKey(taskID, state.SelectedVerifierIndex)
		if err := setUnique(ctx, k.VerifierPayout, key, state, "verifier payout"); err != nil {
			return err
		}
	}
	for _, state := range genesis.TaskSettlements {
		if err := setTaskPrimary(ctx, k.TaskSettlement, state.TaskId, state, "task settlement"); err != nil {
			return err
		}
	}
	for _, state := range genesis.SettlementFactsRetained {
		if err := setTaskPrimary(ctx, k.SettlementFactsRetained, state.TaskId, state, "settlement facts retained"); err != nil {
			return err
		}
	}
	for _, state := range genesis.TaskFailureClasses {
		if err := setRoundPrimary(ctx, k.TaskFailureClass, state.TaskId, state.VerifyRound, state, "task failure class"); err != nil {
			return err
		}
	}
	for _, state := range genesis.TaskCleanupCursors {
		if err := setTaskPrimary(ctx, k.TaskCleanupCursor, state.TaskId, state, "task cleanup cursor"); err != nil {
			return err
		}
	}
	for _, state := range genesis.TaskTerminalSummaries {
		if err := setTaskPrimary(ctx, k.TaskTerminalSummary, state.TaskId, state, "task terminal summary"); err != nil {
			return err
		}
	}
	for _, state := range genesis.EpochTaskSummaryCursors {
		if err := setUnique(ctx, k.EpochTaskSummaryCursor, state.Epoch, state, "epoch task summary cursor"); err != nil {
			return err
		}
	}
	for _, state := range genesis.EpochTaskSummaryReceipts {
		if err := setUnique(ctx, k.EpochTaskSummaryReceipt, state.Epoch, state, "epoch task summary receipt"); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) validateGenesisHubRefs(ctx context.Context, genesis types.GenesisState) error {
	assignments := make(map[string]types.TaskAssignmentState, len(genesis.TaskAssignments))
	budgets := make(map[string]types.TaskBudgetState, len(genesis.TaskBudgets))
	for _, state := range genesis.TaskAssignments {
		assignments[genesisHex(state.TaskId)] = state
		pool, ok := k.hubKeeper.GetCandidatePoolSnapshot(sdk.UnwrapSDKContext(ctx), state.CandidatePoolSnapshotId)
		if !ok || !bytes.Equal(pool.SnapshotId, state.CandidatePoolSnapshotId) ||
			!bytes.Equal(pool.PoolHash, state.CandidatePoolHash) {
			return fmt.Errorf("task assignment references a missing or conflicting Hub candidate pool")
		}
		held, err := k.hubKeeper.HasCandidatePoolTaskRef(ctx, state.TaskId, state.CandidatePoolSnapshotId)
		if err != nil {
			return err
		}
		if held == state.CandidatePoolRefReleased {
			return fmt.Errorf("candidate pool Task ref does not match released state")
		}
	}
	for _, state := range genesis.TaskBudgets {
		budgets[genesisHex(state.TaskId)] = state
	}
	for _, state := range genesis.TaskBuilderSelections {
		set, err := k.hubKeeper.GetBuilderSetByID(sdk.UnwrapSDKContext(ctx), state.BuilderSetId)
		if err != nil || !bytes.Equal(set.BuilderSetHash, state.BuilderSetHash) {
			return fmt.Errorf("task Builder selection references a missing or conflicting Hub BuilderSet")
		}
		held, err := k.hubKeeper.HasBuilderSetTaskRef(ctx, state.TaskId, state.BuilderSetId)
		if err != nil {
			return err
		}
		if held == state.BuilderSetRefReleased {
			return fmt.Errorf("BuilderSet Task ref does not match released state")
		}
	}

	type bucketRefGroup struct {
		kind    shared.BucketKind
		key     string
		version uint64
		count   uint64
	}
	groups := map[string]*bucketRefGroup{}
	for _, ref := range genesis.TaskBucketRefs {
		groupKey := fmt.Sprintf("%d/%s/%d", ref.BucketKind, ref.BucketKey, ref.Version)
		group := groups[groupKey]
		if group == nil {
			group = &bucketRefGroup{kind: ref.BucketKind, key: ref.BucketKey, version: ref.Version}
			groups[groupKey] = group
		}
		group.count++
		bucket, err := k.hubKeeper.GetParameterBucketVersion(ctx, ref.BucketKind, ref.BucketKey, ref.Version)
		if err != nil {
			return fmt.Errorf("missing Hub parameter bucket version: %w", err)
		}
		if bucket.BucketKind != ref.BucketKind || bucket.BucketKey != ref.BucketKey ||
			bucket.Version != ref.Version || bucket.SchemaVersion != hubtypes.ParameterBucketSchemaVersionV1 {
			return fmt.Errorf("Hub parameter bucket version conflicts with Task ref")
		}
		if ref.BucketKind == shared.BucketKind_BUCKET_KIND_TIMEOUT {
			assignment, ok := assignments[genesisHex(ref.TaskId)]
			budget, budgetOK := budgets[genesisHex(ref.TaskId)]
			if !ok || !budgetOK {
				return fmt.Errorf("timeout bucket ref has no Task assignment/budget")
			}
			timeout, ok := timeoutForWorkUnits(bucket.TimeoutEntries.Entries, budget.MaxOutputTokens)
			if !ok || assignment.InferTimeoutBlocks != timeout {
				return fmt.Errorf("infer timeout does not match the retained bucket")
			}
		}
	}
	for _, group := range groups {
		bucket, err := k.hubKeeper.GetParameterBucketVersion(ctx, group.kind, group.key, group.version)
		if err != nil {
			return fmt.Errorf("missing Hub parameter bucket version: %w", err)
		}
		if bucket.TaskRefCount != group.count {
			return fmt.Errorf("Hub parameter bucket task_ref_count=%d, imported Task refs=%d", bucket.TaskRefCount, group.count)
		}
	}
	return nil
}

func timeoutForWorkUnits(entries []hubtypes.TimeoutBucketEntryV1, workUnits uint64) (uint64, bool) {
	for _, entry := range entries {
		if workUnits <= entry.UpperWorkUnitsInclusive {
			return entry.TimeoutBlocks, true
		}
	}
	return 0, false
}

func (k Keeper) rebuildGenesisIndexes(ctx context.Context, genesis types.GenesisState) error {
	coreByTask := make(map[string]types.TaskCoreState, len(genesis.TaskCores))
	terminalByTask := make(map[string]types.TaskTerminalSummaryState, len(genesis.TaskTerminalSummaries))

	for _, core := range genesis.TaskCores {
		coreByTask[genesisHex(core.TaskId)] = core
	}
	for _, terminal := range genesis.TaskTerminalSummaries {
		terminalByTask[genesisHex(terminal.TaskId)] = terminal
	}

	cursorSessions := make(map[string]struct{}, len(genesis.SessionHistoryPruneCursors))
	for _, cursor := range genesis.SessionHistoryPruneCursors {
		cursorSessions[genesisHex(cursor.SessionId)] = struct{}{}
	}
	for _, stream := range genesis.Streams {
		sessionID, _ := genesisHashKey("stream session_id", stream.SessionId)
		switch stream.Status {
		case types.SessionStatus_SESSION_STATUS_ACTIVE, types.SessionStatus_SESSION_STATUS_IDLE:
			if err := k.SessionByOwnerIndex.Set(ctx, types.NewSessionByOwnerKey(stream.OwnerUserAddress, sessionID)); err != nil {
				return err
			}
			if err := k.refreshSessionLifecycleIndex(ctx, stream); err != nil {
				return err
			}
		case types.SessionStatus_SESSION_STATUS_CLOSED:
			height := stream.LastActiveHeight
			if _, running := cursorSessions[genesisHex(stream.SessionId)]; !running {
				var overflow bool
				height, overflow = checkedHeightAdd(stream.LastActiveHeight, genesis.Params.Session.SessionOrderRetentionBlocks)
				if overflow {
					return fmt.Errorf("session history retention height overflow")
				}
			}
			if err := k.SessionHistoryPruneIndex.Set(ctx, types.NewSessionHistoryPruneIndexKey(height, sessionID)); err != nil {
				return err
			}
		}
	}
	for _, summary := range genesis.SessionTerminalSummaries {
		sessionID, _ := genesisHashKey("session terminal summary session_id", summary.SessionId)
		pruneHeight, overflow := checkedHeightAdd(summary.CompactedHeight, genesis.Params.Session.SessionTerminalSummaryRetentionBlocks)
		if overflow {
			return fmt.Errorf("session terminal summary retention height overflow")
		}
		if err := k.SessionTerminalSummaryPruneIndex.Set(ctx, types.NewSessionTerminalSummaryPruneIndexKey(pruneHeight, sessionID)); err != nil {
			return err
		}
	}

	for _, assignment := range genesis.TaskAssignments {
		taskID, _ := genesisHashKey("task assignment task_id", assignment.TaskId)
		core := coreByTask[genesisHex(assignment.TaskId)]
		if core.AssignmentStatus == types.AssignmentStatus_ASSIGNMENT_STATUS_NONE &&
			core.TaskPhase == types.TaskPhase_TASK_PHASE_WORKER_ASSIGNMENT_PENDING {
			union, err := k.TaskStageHandraiseUnion.Get(ctx, types.NewTaskStageKey(taskID, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK))
			if err != nil || union.Status != types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_OPEN ||
				union.WindowCloseHeight == 0 || assignment.AssignmentRandomnessHeight != union.WindowCloseHeight {
				return fmt.Errorf("pending worker proposal window is missing its canonical close height")
			}
			if err := setGenesisDeadlineIndex(ctx, k.AssignmentRandomnessIndex, "assignment_randomness_index", union.WindowCloseHeight, taskID); err != nil {
				return err
			}
		}
		if core.AssignmentStatus == types.AssignmentStatus_ASSIGNMENT_STATUS_RANDOMNESS_PENDING {
			if err := setGenesisDeadlineIndex(ctx, k.AssignmentRandomnessIndex, "assignment_randomness_index", assignment.AssignmentRandomnessHeight, taskID); err != nil {
				return err
			}
		}
		if core.AssignmentStatus == types.AssignmentStatus_ASSIGNMENT_STATUS_WORKER_ASSIGNED &&
			core.ReceiptStatus == types.ReceiptStatus_RECEIPT_STATUS_NONE {
			if err := setGenesisDeadlineIndex(ctx, k.InferDeadlineIndex, "infer_deadline_index", assignment.InferDeadlineHeight, taskID); err != nil {
				return err
			}
		}
		if core.FinalityStatus == shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_PENDING && assignment.WinnerWorker != "" {
			if err := k.WorkerActiveTaskIndex.Set(ctx, types.NewWorkerActiveTaskKey(assignment.WinnerWorker, taskID)); err != nil {
				return err
			}
		}
	}

	for _, window := range genesis.VerifierCandidateWindows {
		taskID, _ := genesisHashKey("verifier candidate window task_id", window.TaskId)
		// Hub's BeaconConsumerRef KeySet has no Genesis field and no rebuild, yet
		// every live window holds two of its rows — acquired in msg_server_receipt /
		// msg_server_challenge and released in task_cleanup on exactly the
		// not-PRUNED condition used here. Without this, an export/import leaves the
		// set empty and ProcessBeaconPrunes reads "nobody references this height"
		// for beacons these windows still have to reproduce their randomness from.
		//
		// Hub initializes before Task (app_config module order), so its store is
		// ready; the refs are cheap to re-acquire because the keys are derived, not
		// stored.
		if window.Status != types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_PRUNED {
			// Both heights are checked here rather than left to Hub's beacon-ref
			// validator, so an import carrying a live window with no randomness
			// height fails naming the window instead of surfacing as a beacon
			// error three modules away. Genesis has no other check on these two
			// fields, and a live window without them can never reproduce its
			// randomness.
			windowHeight, err := requireGenesisIndexHeight("verifier window randomness", window.WindowRandomnessHeight, taskID)
			if err != nil {
				return err
			}
			selectionHeight, err := requireGenesisIndexHeight("verifier selection randomness", window.SelectionRandomnessHeight, taskID)
			if err != nil {
				return err
			}
			if err := k.hubKeeper.AcquireBeaconConsumerRef(ctx, windowHeight,
				hubtypes.BeaconConsumerKindVerifierWindow, hex32(taskID)); err != nil {
				return err
			}
			if err := k.hubKeeper.AcquireBeaconConsumerRef(ctx, selectionHeight,
				hubtypes.BeaconConsumerKindVerifierSelection, hex32(taskID)); err != nil {
				return err
			}
		}
		_, assigned := findVerifierAssignment(genesis.VerifierAssignments, window.TaskId, window.VerifyRound)
		if !assigned && window.Status == types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_SOURCE_FROZEN {
			if err := setGenesisVerifyRoundIndex(ctx, k.VerifierWindowBuildIndex, "verifier_window_build_index", window.WindowRandomnessHeight, taskID, window.VerifyRound); err != nil {
				return err
			}
		}
		if !assigned && window.Status != types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_PRUNED {
			core := coreByTask[genesisHex(window.TaskId)]
			if core.VerificationStatus == types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_SELECTION_PENDING {
				if err := setGenesisVerifyRoundIndex(ctx, k.VerifierSelectionRandomnessIndex, "verifier_selection_randomness_index", window.SelectionRandomnessHeight, taskID, window.VerifyRound); err != nil {
					return err
				}
			} else if err := setGenesisVerifyRoundIndex(ctx, k.VerifierHandraiseCloseIndex, "verifier_handraise_close_index", window.HandraiseCloseHeight, taskID, window.VerifyRound); err != nil {
				return err
			}
			if err := setGenesisDeadlineIndex(ctx, k.VerifyOpenDeadlineIndex, "verify_open_deadline_index", window.AssignmentDeadlineHeight, taskID); err != nil {
				return err
			}
		}
	}

	maxVerifierRound := make(map[string]uint32, len(genesis.VerifierAssignments))
	for _, assignment := range genesis.VerifierAssignments {
		task := genesisHex(assignment.TaskId)
		if assignment.VerifyRound > maxVerifierRound[task] {
			maxVerifierRound[task] = assignment.VerifyRound
		}
	}
	for _, assignment := range genesis.VerifierAssignments {
		taskID, _ := genesisHashKey("verifier assignment task_id", assignment.TaskId)
		core := coreByTask[genesisHex(assignment.TaskId)]
		if assignment.VerifyRound != maxVerifierRound[genesisHex(assignment.TaskId)] {
			continue
		}
		if core.FinalityStatus == shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_PENDING {
			for _, selected := range assignment.SelectedVerifiers {
				if err := k.VerifierActiveJobIndex.Set(ctx, types.NewVerifierActiveJobKey(selected.OperatorAddress, taskID)); err != nil {
					return err
				}
			}
		}
		switch core.VerificationStatus {
		case types.VerificationStatus_VERIFICATION_STATUS_COMMITTING:
			if err := setGenesisDeadlineIndex(ctx, k.CommitDeadlineIndex, "commit_deadline_index", assignment.CommitDeadlineHeight, taskID); err != nil {
				return err
			}
			if err := setGenesisDeadlineIndex(ctx, k.VerifyDeadlineIndex, "verify_deadline_index", assignment.VerifyDeadlineHeight, taskID); err != nil {
				return err
			}
		case types.VerificationStatus_VERIFICATION_STATUS_REVEALING:
			if err := setGenesisDeadlineIndex(ctx, k.RevealDeadlineIndex, "reveal_deadline_index", assignment.RevealDeadlineHeight, taskID); err != nil {
				return err
			}
			if err := setGenesisDeadlineIndex(ctx, k.VerifyDeadlineIndex, "verify_deadline_index", assignment.VerifyDeadlineHeight, taskID); err != nil {
				return err
			}
		}
	}

	for _, cursor := range genesis.RoundEconomicEffectApplyCursors {
		taskID, _ := genesisHashKey("round economic effect cursor task_id", cursor.TaskId)
		if err := setGenesisVerifyRoundIndex(ctx, k.RoundEconomicEffectApplyIndex, "round_economic_effect_apply_index", cursor.NextApplyHeight, taskID, cursor.VerifyRound); err != nil {
			return err
		}
	}
	for _, failure := range genesis.TaskFailureClasses {
		taskID, _ := genesisHashKey("task failure class task_id", failure.TaskId)
		effectiveRound := uint32(0)
		if core, ok := coreByTask[genesisHex(failure.TaskId)]; ok {
			effectiveRound = core.EffectiveVerifyRound
		} else if terminal, ok := terminalByTask[genesisHex(failure.TaskId)]; ok {
			effectiveRound = terminal.EffectiveVerifyRound
		}
		if failure.XTaskFinalityHeight != nil && failure.SupersededByVerifyRound == 0 &&
			failure.FailureClass != types.TaskFailureClass_TASK_FAILURE_CLASS_NONE &&
			(effectiveRound == 0 || effectiveRound == failure.VerifyRound) {
			if err := k.TaskFailureClassByProfileWindowIndex.Set(ctx,
				types.NewTaskFailureClassByProfileWindowKey(failure.ModelId, failure.ProfileVersion, failure.GetTaskFinalityHeight(), failure.FailureClass, taskID)); err != nil {
				return err
			}
		}
		if failure.XPruneHeight != nil {
			if err := k.TaskFailureClassWindowPruneIndex.Set(ctx,
				types.NewTaskFailureClassPruneKey(failure.GetPruneHeight(), taskID, failure.VerifyRound)); err != nil {
				return err
			}
		}
	}
	for _, core := range genesis.TaskCores {
		if core.FinalityStatus != shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL || core.XTaskFinalityHeight == nil {
			continue
		}
		taskID, _ := genesisHashKey("task core task_id", core.TaskId)
		cleanupHeight, overflow := checkedHeightAdd(core.GetTaskFinalityHeight(), core.EvidenceRetentionBlocksSnapshot)
		if overflow {
			return fmt.Errorf("task evidence cleanup height overflow")
		}
		if err := setGenesisDeadlineIndex(ctx, k.EvidenceCleanupIndex, "evidence_cleanup_index", cleanupHeight, taskID); err != nil {
			return err
		}
	}
	for _, summary := range genesis.TaskRoundSummaries {
		taskID, _ := genesisHashKey("task round summary task_id", summary.TaskId)
		if summary.XChallengeCloseHeight != nil && summary.XRoundsClosedHeight == nil {
			if err := setGenesisDeadlineIndex(ctx, k.ChallengeWindowCloseIndex, "challenge_window_close_index", summary.GetChallengeCloseHeight(), taskID); err != nil {
				return err
			}
		}
		if summary.XRoundsClosedHeight != nil {
			if _, settled := findTaskSettlement(genesis.TaskSettlements, summary.TaskId); !settled {
				deadline, err := settlementDeadlineHeight(summary.GetRoundsClosedHeight(), genesis.Params)
				if err != nil {
					return err
				}
				if err := setGenesisDeadlineIndex(ctx, k.SettlementDeadlineIndex, "settlement_deadline_index", deadline, taskID); err != nil {
					return err
				}
			}
		}
	}
	for _, terminal := range genesis.TaskTerminalSummaries {
		taskID, _ := genesisHashKey("task terminal summary task_id", terminal.TaskId)
		pruneHeight, overflow := checkedHeightAdd(terminal.CompactedHeight, genesis.Params.Cleanup.TaskTerminalSummaryRetentionBlocks)
		if overflow {
			return fmt.Errorf("task terminal summary retention height overflow")
		}
		if err := setGenesisDeadlineIndex(ctx, k.TaskTerminalSummaryPruneIndex, "task_terminal_summary_prune_index", pruneHeight, taskID); err != nil {
			return err
		}
		// rebuildEpochTaskSummarySource is the one writer that knows which sources
		// are already folded: it skips an epoch whose receipt was dispatched and a
		// task the epoch cursor has already walked past. Setting the index directly
		// resurrected both classes, so an export/import re-emitted summaries the
		// exporting chain had permanently retired. It also writes the schedule row
		// for the epoch, which is why the cursor-derived loop that used to follow
		// is gone: deriving the schedule only from EpochTaskSummaryCursors left any
		// epoch that had sources but no cursor row permanently undispatched.
		//
		// The receipts and cursors it consults are imported above (lines ~350-360),
		// so they are in place by the time this runs.
		if err := k.rebuildEpochTaskSummarySource(ctx, terminal); err != nil {
			return err
		}
	}
	for _, cursor := range genesis.EpochTaskSummaryCursors {
		// A mid-dispatch epoch keeps its own schedule row even when every one of its
		// sources has already been consumed, or the walk would never be finished.
		if err := k.EpochTaskSummaryScheduleIndex.Set(ctx, types.NewEpochTaskSummaryScheduleKey(cursor.DueHeight, cursor.Epoch)); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) assertFreshGenesisStore(ctx context.Context) error {
	if _, err := k.Params.Get(ctx); err == nil {
		return fmt.Errorf("task store is already initialized; delete/reset node data before applying fresh genesis")
	} else if !errors.Is(err, collections.ErrNotFound) {
		return fmt.Errorf("check existing task params: %w", err)
	}
	return nil
}

// ExportGenesis emits only primary rows. Collection iteration is canonical key
// order, which is the required order for every repeated Genesis field.
func (k Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	genesis := types.DefaultGenesis()
	var err error
	if genesis.Params, err = k.Params.Get(ctx); err != nil {
		return nil, err
	}
	if genesis.ParamsMeta, err = k.GetTaskParamsMeta(ctx); err != nil {
		return nil, err
	}
	if genesis.SessionNonces, err = collectMapValues(ctx, k.SessionNonce); err != nil {
		return nil, err
	}
	if genesis.Streams, err = collectMapValues(ctx, k.Stream); err != nil {
		return nil, err
	}
	if genesis.OrderSequences, err = collectMapValues(ctx, k.OrderSequence); err != nil {
		return nil, err
	}
	if genesis.TaskBudgets, err = collectMapValues(ctx, k.TaskBudget); err != nil {
		return nil, err
	}
	if genesis.SessionHistoryPruneCursors, err = collectMapValues(ctx, k.SessionHistoryPruneCursor); err != nil {
		return nil, err
	}
	if genesis.SessionTerminalSummaries, err = collectMapValues(ctx, k.SessionTerminalSummary); err != nil {
		return nil, err
	}
	if genesis.TaskCores, err = collectMapValues(ctx, k.TaskCore); err != nil {
		return nil, err
	}
	if genesis.TaskAssignments, err = collectMapValues(ctx, k.TaskAssignment); err != nil {
		return nil, err
	}
	if genesis.AssignmentCandidateSets, err = collectMapValues(ctx, k.AssignmentCandidateSet); err != nil {
		return nil, err
	}
	if genesis.InferReceipts, err = collectMapValues(ctx, k.InferReceipt); err != nil {
		return nil, err
	}
	if genesis.WorkerEvidenceReceipts, err = collectMapValues(ctx, k.WorkerEvidenceReceipt); err != nil {
		return nil, err
	}
	if genesis.VerifierAssignments, err = collectMapValues(ctx, k.VerifierAssignment); err != nil {
		return nil, err
	}
	if genesis.Commits, err = collectMapValues(ctx, k.CommitState); err != nil {
		return nil, err
	}
	if genesis.ResultReceipts, err = collectMapValues(ctx, k.ResultReceiptState); err != nil {
		return nil, err
	}
	if genesis.TaskCandidateFacts, err = collectMapValues(ctx, k.TaskCandidateFact); err != nil {
		return nil, err
	}
	if genesis.BuilderStageProposals, err = collectMapValues(ctx, k.BuilderStageProposal); err != nil {
		return nil, err
	}
	if genesis.TaskStageHandraiseUnions, err = collectMapValues(ctx, k.TaskStageHandraiseUnion); err != nil {
		return nil, err
	}
	if genesis.TaskStageHandraiseUnionSegments, err = collectMapValues(ctx, k.TaskStageHandraiseUnionSegment); err != nil {
		return nil, err
	}
	if genesis.TaskCandidateFinalizeCursors, err = collectMapValues(ctx, k.TaskCandidateFinalizeCursor); err != nil {
		return nil, err
	}
	if genesis.TaskBuilderSelections, err = collectMapValues(ctx, k.TaskBuilderSelection); err != nil {
		return nil, err
	}
	if genesis.VerifierCandidateWindows, err = collectMapValues(ctx, k.VerifierCandidateWindow); err != nil {
		return nil, err
	}
	if genesis.VerifierCandidateEligibilitySegments, err = collectMapValues(ctx, k.VerifierCandidateEligibilitySegment); err != nil {
		return nil, err
	}
	if genesis.VerifierCandidateWindowMembers, err = collectMapValues(ctx, k.VerifierCandidateWindowMember); err != nil {
		return nil, err
	}
	if genesis.DataUnavailableReports, err = collectMapValues(ctx, k.DataUnavailableReport); err != nil {
		return nil, err
	}
	if genesis.BuilderDataUnavailableAggregates, err = collectMapValues(ctx, k.BuilderDataUnavailableAggregate); err != nil {
		return nil, err
	}
	if genesis.TaskFailureClasses, err = collectMapValues(ctx, k.TaskFailureClass); err != nil {
		return nil, err
	}
	if genesis.TaskBucketRefs, err = collectMapValues(ctx, k.TaskBucketRef); err != nil {
		return nil, err
	}
	if genesis.TaskCleanupCursors, err = collectMapValues(ctx, k.TaskCleanupCursor); err != nil {
		return nil, err
	}
	if genesis.TaskTerminalSummaries, err = collectMapValues(ctx, k.TaskTerminalSummary); err != nil {
		return nil, err
	}
	if genesis.EpochTaskSummaryCursors, err = collectMapValues(ctx, k.EpochTaskSummaryCursor); err != nil {
		return nil, err
	}
	if genesis.EpochTaskSummaryReceipts, err = collectMapValues(ctx, k.EpochTaskSummaryReceipt); err != nil {
		return nil, err
	}
	if genesis.TaskGasReimbursements, err = collectMapValues(ctx, k.TaskGasReimbursement); err != nil {
		return nil, err
	}
	if genesis.VerificationRounds, err = collectMapValues(ctx, k.VerificationRound); err != nil {
		return nil, err
	}
	if genesis.RoundFundings, err = collectMapValues(ctx, k.RoundFunding); err != nil {
		return nil, err
	}
	if genesis.RoundEconomicEffects, err = collectMapValues(ctx, k.RoundEconomicEffect); err != nil {
		return nil, err
	}
	if genesis.RoundEconomicEffectApplyCursors, err = collectMapValues(ctx, k.RoundEconomicEffectApplyCursor); err != nil {
		return nil, err
	}
	if genesis.TaskRoundSummaries, err = collectMapValues(ctx, k.TaskRoundSummary); err != nil {
		return nil, err
	}
	if genesis.VerifierPayouts, err = collectMapValues(ctx, k.VerifierPayout); err != nil {
		return nil, err
	}
	if genesis.TaskSettlements, err = collectMapValues(ctx, k.TaskSettlement); err != nil {
		return nil, err
	}
	if genesis.SettlementFactsRetained, err = collectMapValues(ctx, k.SettlementFactsRetained); err != nil {
		return nil, err
	}
	if err := genesis.Validate(); err != nil {
		return nil, fmt.Errorf("exported task genesis is invalid: %w", err)
	}
	return genesis, nil
}

func setTaskPrimary[V any](ctx context.Context, m collections.Map[types.TaskKey, V], raw []byte, value V, label string) error {
	key, err := genesisHashKey(label+" task_id", raw)
	if err != nil {
		return err
	}
	return setUnique(ctx, m, key, value, label)
}

func setRoundPrimary[V any](ctx context.Context, m collections.Map[types.VerifyRoundKey, V], raw []byte, round uint32, value V, label string) error {
	taskID, err := genesisHashKey(label+" task_id", raw)
	if err != nil {
		return err
	}
	if !isPhase0VerifyRound(round) {
		return fmt.Errorf("%s verify_round must be 1 or 2", label)
	}
	return setUnique(ctx, m, types.NewVerifyRoundKey(taskID, round), value, label)
}

func setUnique[K, V any](ctx context.Context, m collections.Map[K, V], key K, value V, label string) error {
	has, err := m.Has(ctx, key)
	if err != nil {
		return err
	}
	if has {
		return fmt.Errorf("duplicate %s primary key", label)
	}
	return m.Set(ctx, key, value)
}

func collectMapValues[K, V any](ctx context.Context, m collections.Map[K, V]) ([]V, error) {
	iter, err := m.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	values := []V{}
	for ; iter.Valid(); iter.Next() {
		value, err := iter.Value()
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func genesisHashKey(name string, value []byte) (types.Hash32Key, error) {
	if len(value) != types.Hash32Len {
		return nil, fmt.Errorf("%s must be Hash32", name)
	}
	return append(types.Hash32Key(nil), value...), nil
}

func genesisHex(value []byte) string {
	return fmt.Sprintf("%x", value)
}

func findVerifierAssignment(states []types.VerifierAssignmentState, taskID []byte, round uint32) (types.VerifierAssignmentState, bool) {
	for _, state := range states {
		if state.VerifyRound == round && bytes.Equal(state.TaskId, taskID) {
			return state, true
		}
	}
	return types.VerifierAssignmentState{}, false
}

func findTaskSettlement(states []types.TaskSettlementState, taskID []byte) (types.TaskSettlementState, bool) {
	for _, state := range states {
		if bytes.Equal(state.TaskId, taskID) {
			return state, true
		}
	}
	return types.TaskSettlementState{}, false
}

// requireGenesisIndexHeight rejects a height-0 deadline/index row during the
// Genesis rebuild.
//
// The runtime writers go through addDeadlineIndex, which drops height 0 on the
// floor precisely because a row at height 0 is due at every height: the sweep
// would re-select it every block for the life of the chain. The Genesis rebuild
// calls the KeySets directly and so never had that floor. It cannot simply
// reuse the silent skip either — each rebuild below is already gated on a status
// that makes the height mandatory (a COMMITTING task has a commit deadline), so
// a zero here is an incoherent import, and skipping it would produce a chain
// whose indexes no longer round-trip its own state.
func requireGenesisIndexHeight(name string, height uint64, taskID types.TaskKey) (uint64, error) {
	if height == 0 {
		return 0, fmt.Errorf("genesis %s index row for task %s has height 0", name, genesisHex(taskID))
	}
	return height, nil
}

func setGenesisDeadlineIndex(
	ctx context.Context,
	set collections.KeySet[types.DeadlineIndexKey],
	name string,
	height uint64,
	taskID types.TaskKey,
) error {
	checked, err := requireGenesisIndexHeight(name, height, taskID)
	if err != nil {
		return err
	}
	return set.Set(ctx, types.NewDeadlineIndexKey(checked, taskID))
}

func setGenesisVerifyRoundIndex(
	ctx context.Context,
	set collections.KeySet[types.VerifyRoundIndexKey],
	name string,
	height uint64,
	taskID types.TaskKey,
	verifyRound uint32,
) error {
	checked, err := requireGenesisIndexHeight(name, height, taskID)
	if err != nil {
		return err
	}
	return set.Set(ctx, types.NewVerifyRoundIndexKey(checked, taskID, verifyRound))
}
