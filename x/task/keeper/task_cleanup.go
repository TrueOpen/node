package keeper

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

type evidenceCleanupProgress struct {
	processed uint64
	done      bool
}

type taskCleanupStepResult struct {
	visited uint64
	deleted uint64
	done    bool
}

func (k Keeper) runTaskCleanup(ctx context.Context, _ types.SessionKey, taskKey types.TaskKey, targetHeight, limit uint64) (evidenceCleanupProgress, error) {
	if limit == 0 {
		return evidenceCleanupProgress{}, nil
	}
	if _, err := k.TaskTerminalSummary.Get(ctx, taskKey); err == nil {
		if err := removeDeadlineIndex(ctx, k.EvidenceCleanupIndex, taskKey, targetHeight); err != nil {
			return evidenceCleanupProgress{}, err
		}
		return evidenceCleanupProgress{processed: 1, done: true}, nil
	} else if !errors.Is(err, collections.ErrNotFound) {
		return evidenceCleanupProgress{}, err
	}

	currentHeight, err := currentBlockHeight(ctx)
	if err != nil {
		return evidenceCleanupProgress{}, err
	}
	_, cursorErr := k.TaskCleanupCursor.Get(ctx, taskKey)
	if cursorErr != nil && !errors.Is(cursorErr, collections.ErrNotFound) {
		return evidenceCleanupProgress{}, cursorErr
	}
	if cursorErr == nil {
		blocked, err := k.taskCleanupBaseBlocked(ctx, taskKey)
		if err != nil {
			return evidenceCleanupProgress{}, err
		}
		if blocked {
			return evidenceCleanupProgress{}, fmt.Errorf("running task cleanup lost its terminal prerequisites")
		}
	}
	if errors.Is(cursorErr, collections.ErrNotFound) {
		if blocked, err := k.taskCleanupBlocked(ctx, taskKey); err != nil {
			return evidenceCleanupProgress{}, err
		} else if blocked {
			retry, overflow := checkedHeightAdd(currentHeight, 1)
			if overflow {
				return evidenceCleanupProgress{}, fmt.Errorf("task cleanup retry height overflow")
			}
			sdkCtx := sdk.UnwrapSDKContext(ctx)
			cacheCtx, write := sdkCtx.CacheContext()
			cache := sdk.WrapSDKContext(cacheCtx)
			if err := removeDeadlineIndex(cache, k.EvidenceCleanupIndex, taskKey, targetHeight); err != nil {
				return evidenceCleanupProgress{}, err
			}
			if err := addDeadlineIndex(cache, k.EvidenceCleanupIndex, taskKey, retry); err != nil {
				return evidenceCleanupProgress{}, err
			}
			write()
			return evidenceCleanupProgress{processed: 1}, nil
		}
	}

	cursor, err := k.loadOrCreateTaskCleanupCursor(ctx, taskKey)
	if err != nil {
		return evidenceCleanupProgress{}, err
	}
	progress := evidenceCleanupProgress{}
	for progress.processed < limit {
		sdkCtx := sdk.UnwrapSDKContext(ctx)
		cacheCtx, write := sdkCtx.CacheContext()
		cache := sdk.WrapSDKContext(cacheCtx)
		step, err := k.runTaskCleanupStep(cache, taskKey, &cursor, targetHeight, currentHeight)
		if err != nil {
			return evidenceCleanupProgress{}, err
		}
		if step.done {
			write()
			progress.done = true
			if progress.processed == 0 {
				progress.processed = 1
			}
			return progress, nil
		}
		cursor.VisitedCount, err = addCleanupCount(cursor.VisitedCount, step.visited)
		if err != nil {
			return evidenceCleanupProgress{}, err
		}
		cursor.DeletedCount, err = addCleanupCount(cursor.DeletedCount, step.deleted)
		if err != nil {
			return evidenceCleanupProgress{}, err
		}
		if err := k.TaskCleanupCursor.Set(cache, taskKey, cursor); err != nil {
			return evidenceCleanupProgress{}, err
		}
		write()
		progress.processed, err = addCleanupCount(progress.processed, step.visited)
		if err != nil {
			return evidenceCleanupProgress{}, err
		}
	}
	return progress, nil
}

func addCleanupCount(left, right uint64) (uint64, error) {
	value, overflow := checkedAddUint64(left, right)
	if overflow {
		return 0, fmt.Errorf("task cleanup counter overflow")
	}
	return value, nil
}

func (k Keeper) loadOrCreateTaskCleanupCursor(ctx context.Context, taskKey types.TaskKey) (types.TaskCleanupCursorState, error) {
	cursor, err := k.TaskCleanupCursor.Get(ctx, taskKey)
	if err == nil {
		if !bytes.Equal(cursor.TaskId, taskKey) ||
			cursor.Phase < types.TaskCleanupPhase_TASK_CLEANUP_PHASE_ROUND_STATE_AND_EFFECTS ||
			cursor.Phase > types.TaskCleanupPhase_TASK_CLEANUP_PHASE_TASK_COMPACTION {
			return types.TaskCleanupCursorState{}, fmt.Errorf("task cleanup cursor is non-canonical")
		}
		return cursor, nil
	}
	if !errors.Is(err, collections.ErrNotFound) {
		return types.TaskCleanupCursorState{}, err
	}
	cursor = types.TaskCleanupCursorState{
		TaskId: append([]byte(nil), taskKey...),
		Phase:  types.TaskCleanupPhase_TASK_CLEANUP_PHASE_ROUND_STATE_AND_EFFECTS,
	}
	if err := k.TaskCleanupCursor.Set(ctx, taskKey, cursor); err != nil {
		return types.TaskCleanupCursorState{}, err
	}
	return cursor, nil
}

func (k Keeper) taskCleanupBlocked(ctx context.Context, taskKey types.TaskKey) (bool, error) {
	blocked, err := k.taskCleanupBaseBlocked(ctx, taskKey)
	if err != nil || blocked {
		return blocked, err
	}
	if has, err := k.RoundEconomicEffectApplyCursor.Has(ctx,
		types.NewVerifyRoundKey(taskKey, types.ChallengeVerifyRoundV1)); err != nil {
		return false, err
	} else if has {
		return true, nil
	}
	effects, err := k.roundEffectsForCleanup(ctx, taskKey)
	if err != nil {
		return false, err
	}
	if round2, err := k.VerificationRound.Get(ctx,
		types.NewVerifyRoundKey(taskKey, types.ChallengeVerifyRoundV1)); err == nil {
		if round2.XClosedHeight == nil || len(round2.GetRoundEffectRoot()) != types.Hash32Len ||
			uint32(len(effects)) != round2.RoundEffectCount {
			return true, nil
		}
		for _, effect := range effects {
			if effect.Status != shared.RoundEconomicEffectStatusV1_ROUND_ECONOMIC_EFFECT_STATUS_V1_APPLIED {
				return true, nil
			}
		}
		root, err := types.RoundEffectRoot(
			sdk.UnwrapSDKContext(ctx).ChainID(), taskKey, types.ChallengeVerifyRoundV1,
			round2.GetRoundEffectPlanRoot(), effects,
		)
		if err != nil {
			return false, err
		}
		summary, err := k.TaskRoundSummary.Get(ctx, taskKey)
		if err != nil {
			return false, err
		}
		if !bytes.Equal(root[:], round2.GetRoundEffectRoot()) ||
			!bytes.Equal(root[:], summary.Round2EffectRootOrZero32) {
			return false, fmt.Errorf("task cleanup round effect root mismatch")
		}
		funding, err := k.RoundFunding.Get(ctx,
			types.NewVerifyRoundKey(taskKey, types.ChallengeVerifyRoundV1))
		if err != nil || funding.XClosedHeight == nil ||
			len(funding.GetFundingResolutionHash()) != types.Hash32Len {
			return true, nil
		}
	} else if !errors.Is(err, collections.ErrNotFound) {
		return false, err
	}
	return false, nil
}

func (k Keeper) taskCleanupBaseBlocked(ctx context.Context, taskKey types.TaskKey) (bool, error) {
	core, err := k.TaskCore.Get(ctx, taskKey)
	if err != nil {
		return false, err
	}
	if core.FinalityStatus != shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL ||
		core.XTaskFinalityHeight == nil {
		return true, nil
	}
	settlement, err := k.TaskSettlement.Get(ctx, taskKey)
	if errors.Is(err, collections.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if settlement.TaskFinalityHeight != core.GetTaskFinalityHeight() {
		return false, fmt.Errorf("task cleanup settlement finality mismatch")
	}
	summary, err := k.TaskRoundSummary.Get(ctx, taskKey)
	if err != nil {
		return false, err
	}
	if summary.OpenRoundCount != 0 || summary.XRoundsClosedHeight == nil ||
		summary.XSettlementFactsCutoffHeight == nil {
		return true, nil
	}
	return false, nil
}

func (k Keeper) roundEffectsForCleanup(ctx context.Context, taskKey types.TaskKey) ([]shared.RoundEconomicEffectV1, error) {
	iter, err := k.RoundEconomicEffect.Iterate(ctx,
		collections.NewPrefixedTripleRange[types.Hash32Key, uint32, uint32](taskKey))
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	effects := []shared.RoundEconomicEffectV1{}
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		state, err := iter.Value()
		if err != nil {
			return nil, err
		}
		if key.K2() != types.ChallengeVerifyRoundV1 ||
			state.VerifyRound != types.ChallengeVerifyRoundV1 ||
			state.Effect.EffectIndex != key.K3() {
			return nil, fmt.Errorf("task cleanup found non-canonical round effect")
		}
		if state.Effect.EffectIndex != uint32(len(effects)) {
			return nil, fmt.Errorf("task cleanup round effects are not contiguous")
		}
		effects = append(effects, state.Effect)
	}
	return effects, nil
}

func (k Keeper) runTaskCleanupStep(
	ctx context.Context,
	taskKey types.TaskKey,
	cursor *types.TaskCleanupCursorState,
	targetHeight, currentHeight uint64,
) (taskCleanupStepResult, error) {
	switch cursor.Phase {
	case types.TaskCleanupPhase_TASK_CLEANUP_PHASE_ROUND_STATE_AND_EFFECTS:
		if deleted, err := deleteFirstMapRow(ctx, k.RoundEconomicEffect,
			collections.NewPrefixedTripleRange[types.Hash32Key, uint32, uint32](taskKey)); err != nil || deleted {
			return taskCleanupStepResult{visited: 1, deleted: boolCount(deleted)}, err
		}
		if deleted, err := deleteFirstMapRow(ctx, k.RoundFunding,
			collections.NewPrefixedPairRange[types.Hash32Key, uint32](taskKey)); err != nil || deleted {
			return taskCleanupStepResult{visited: 1, deleted: boolCount(deleted)}, err
		}
		advanceTaskCleanupPhase(cursor)
		return taskCleanupStepResult{}, nil
	case types.TaskCleanupPhase_TASK_CLEANUP_PHASE_PROPOSALS:
		if deleted, err := deleteFirstMapRow(ctx, k.BuilderStageProposal,
			collections.NewPrefixedTripleRange[types.Hash32Key, int32, types.Hash32Key](taskKey)); err != nil || deleted {
			return taskCleanupStepResult{visited: 1, deleted: boolCount(deleted)}, err
		}
		if deleted, err := deleteFirstMapRow(ctx, k.TaskStageHandraiseUnionSegment,
			collections.NewPrefixedTripleRange[types.Hash32Key, int32, uint32](taskKey)); err != nil || deleted {
			return taskCleanupStepResult{visited: 1, deleted: boolCount(deleted)}, err
		}
		advanceTaskCleanupPhase(cursor)
		return taskCleanupStepResult{}, nil
	case types.TaskCleanupPhase_TASK_CLEANUP_PHASE_CANDIDATE_FACTS:
		if deleted, err := deleteFirstMapRow(ctx, k.TaskCandidateFact,
			collections.NewPrefixedTripleRange[types.Hash32Key, int32, uint32](taskKey)); err != nil || deleted {
			return taskCleanupStepResult{visited: 1, deleted: boolCount(deleted)}, err
		}
		if deleted, err := deleteFirstMapRow(ctx, k.TaskCandidateFinalizeCursor,
			collections.NewPrefixedPairRange[types.Hash32Key, int32](taskKey)); err != nil || deleted {
			return taskCleanupStepResult{visited: 1, deleted: boolCount(deleted)}, err
		}
		advanceTaskCleanupPhase(cursor)
		return taskCleanupStepResult{}, nil
	case types.TaskCleanupPhase_TASK_CLEANUP_PHASE_VERIFIER_WINDOW:
		return k.cleanupVerifierWindow(ctx, taskKey, cursor)
	case types.TaskCleanupPhase_TASK_CLEANUP_PHASE_RECEIPT_DETAILS:
		return k.cleanupReceiptDetails(ctx, taskKey, cursor)
	case types.TaskCleanupPhase_TASK_CLEANUP_PHASE_DATA_UNAVAILABLE:
		return k.cleanupDataUnavailable(ctx, taskKey, cursor)
	case types.TaskCleanupPhase_TASK_CLEANUP_PHASE_ACTIVE_INDEXES_AND_LIABILITIES:
		return k.cleanupResponsibilities(ctx, taskKey, cursor)
	case types.TaskCleanupPhase_TASK_CLEANUP_PHASE_TASK_COMPACTION:
		return k.compactTask(ctx, taskKey, cursor, targetHeight, currentHeight)
	default:
		return taskCleanupStepResult{}, fmt.Errorf("task cleanup cursor has invalid phase")
	}
}

func advanceTaskCleanupPhase(cursor *types.TaskCleanupCursorState) {
	cursor.Phase++
	cursor.LastProcessedKey = nil
}

func boolCount(value bool) uint64 {
	if value {
		return 1
	}
	return 0
}

func deleteFirstMapRow[K, V any](ctx context.Context, store collections.Map[K, V], rng collections.Ranger[K]) (bool, error) {
	iter, err := store.Iterate(ctx, rng)
	if err != nil {
		return false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return false, nil
	}
	key, err := iter.Key()
	if err != nil {
		return false, err
	}
	return true, store.Remove(ctx, key)
}

func (k Keeper) cleanupVerifierWindow(ctx context.Context, taskKey types.TaskKey, cursor *types.TaskCleanupCursorState) (taskCleanupStepResult, error) {
	if deleted, err := deleteFirstMapRow(ctx, k.VerifierCandidateEligibilitySegment,
		collections.NewPrefixedTripleRange[types.Hash32Key, uint32, uint32](taskKey)); err != nil || deleted {
		return taskCleanupStepResult{visited: 1, deleted: boolCount(deleted)}, err
	}
	if deleted, err := deleteFirstMapRow(ctx, k.VerifierCandidateWindowMember,
		collections.NewPrefixedTripleRange[types.Hash32Key, uint32, uint32](taskKey)); err != nil || deleted {
		return taskCleanupStepResult{visited: 1, deleted: boolCount(deleted)}, err
	}
	for _, round := range []uint32{types.VerifyRoundV1, types.ChallengeVerifyRoundV1} {
		key := types.NewVerifyRoundKey(taskKey, round)
		window, err := k.VerifierCandidateWindow.Get(ctx, key)
		if errors.Is(err, collections.ErrNotFound) {
			continue
		}
		if err != nil {
			return taskCleanupStepResult{}, err
		}
		if window.Status == types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_PRUNED {
			continue
		}
		if err := k.hubKeeper.ReleaseBeaconConsumerRef(ctx, window.WindowRandomnessHeight,
			hubtypes.BeaconConsumerKindVerifierWindow, hex32(taskKey)); err != nil {
			return taskCleanupStepResult{}, err
		}
		if err := k.hubKeeper.ReleaseBeaconConsumerRef(ctx, window.SelectionRandomnessHeight,
			hubtypes.BeaconConsumerKindVerifierSelection, hex32(taskKey)); err != nil {
			return taskCleanupStepResult{}, err
		}
		window.Status = types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_PRUNED
		window.XWindowRandomnessBeacon = nil
		if err := k.VerifierCandidateWindow.Set(ctx, key, window); err != nil {
			return taskCleanupStepResult{}, err
		}
		return taskCleanupStepResult{visited: 1}, nil
	}
	advanceTaskCleanupPhase(cursor)
	return taskCleanupStepResult{}, nil
}

func (k Keeper) cleanupReceiptDetails(ctx context.Context, taskKey types.TaskKey, cursor *types.TaskCleanupCursorState) (taskCleanupStepResult, error) {
	receipt, err := k.InferReceipt.Get(ctx, taskKey)
	if err == nil && len(receipt.RequiredEvidenceCommitments) != 0 {
		receipt.RequiredEvidenceCommitments = nil
		if err := k.InferReceipt.Set(ctx, taskKey, receipt); err != nil {
			return taskCleanupStepResult{}, err
		}
		return taskCleanupStepResult{visited: 1}, nil
	}
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return taskCleanupStepResult{}, err
	}
	advanceTaskCleanupPhase(cursor)
	return taskCleanupStepResult{}, nil
}

func (k Keeper) cleanupDataUnavailable(ctx context.Context, taskKey types.TaskKey, cursor *types.TaskCleanupCursorState) (taskCleanupStepResult, error) {
	if deleted, err := deleteFirstMapRow(ctx, k.DataUnavailableReport,
		collections.NewPrefixedTripleRange[types.Hash32Key, uint32, string](taskKey)); err != nil || deleted {
		return taskCleanupStepResult{visited: 1, deleted: boolCount(deleted)}, err
	}
	iter, err := k.BuilderDataUnavailableAggregate.Iterate(ctx,
		collections.NewPrefixedTripleRange[types.Hash32Key, uint32, string](taskKey))
	if err != nil {
		return taskCleanupStepResult{}, err
	}
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return taskCleanupStepResult{}, err
		}
		state, err := iter.Value()
		if err != nil {
			return taskCleanupStepResult{}, err
		}
		switch state.Status {
		case types.BuilderDataUnavailableAggregateStatusV1_BUILDER_DATA_UNAVAILABLE_AGGREGATE_STATUS_V1_CONFIRMED:
			state.ReportDigestsByVerifierSlot = nil
			state.Status = types.BuilderDataUnavailableAggregateStatusV1_BUILDER_DATA_UNAVAILABLE_AGGREGATE_STATUS_V1_PRUNED
			if err := k.BuilderDataUnavailableAggregate.Set(ctx, key, state); err != nil {
				return taskCleanupStepResult{}, err
			}
			return taskCleanupStepResult{visited: 1}, nil
		case types.BuilderDataUnavailableAggregateStatusV1_BUILDER_DATA_UNAVAILABLE_AGGREGATE_STATUS_V1_COLLECTING:
			if err := k.BuilderDataUnavailableAggregate.Remove(ctx, key); err != nil {
				return taskCleanupStepResult{}, err
			}
			return taskCleanupStepResult{visited: 1, deleted: 1}, nil
		case types.BuilderDataUnavailableAggregateStatusV1_BUILDER_DATA_UNAVAILABLE_AGGREGATE_STATUS_V1_PRUNED:
			continue
		default:
			return taskCleanupStepResult{}, fmt.Errorf("data-unavailable aggregate has invalid cleanup status")
		}
	}
	advanceTaskCleanupPhase(cursor)
	return taskCleanupStepResult{}, nil
}

func (k Keeper) cleanupResponsibilities(ctx context.Context, taskKey types.TaskKey, cursor *types.TaskCleanupCursorState) (taskCleanupStepResult, error) {
	core, err := k.TaskCore.Get(ctx, taskKey)
	if err != nil {
		return taskCleanupStepResult{}, err
	}
	assignment, err := k.TaskAssignment.Get(ctx, taskKey)
	if err != nil {
		return taskCleanupStepResult{}, err
	}
	selection, err := k.TaskBuilderSelection.Get(ctx, taskKey)
	if err != nil {
		return taskCleanupStepResult{}, err
	}
	if !assignment.CandidatePoolRefReleased || !selection.BuilderSetRefReleased {
		return taskCleanupStepResult{}, fmt.Errorf("task cleanup reached responsibility phase before admission refs were released")
	}
	if has, err := hasTaskBucketRef(ctx, k.TaskBucketRef, taskKey); err != nil {
		return taskCleanupStepResult{}, err
	} else if has {
		return taskCleanupStepResult{}, fmt.Errorf("task cleanup found a retained parameter bucket ref")
	}
	if deleted, err := deleteFirstMapRow(ctx, k.WorkerEvidenceReceipt,
		collections.NewPrefixedQuadRange[types.Hash32Key, string, int32, uint64](taskKey)); err != nil || deleted {
		return taskCleanupStepResult{visited: 1, deleted: boolCount(deleted)}, err
	}
	if hasReceipt, err := k.InferReceipt.Has(ctx, taskKey); err != nil {
		return taskCleanupStepResult{}, err
	} else if hasReceipt {
		sessionID, taskID := hex.EncodeToString(core.SessionId), hex.EncodeToString(core.TaskId)
		responsibilityID, err := WorkerOutputEvidenceResponsibilityID(sessionID, taskID, assignment.WinnerWorker)
		if err != nil {
			return taskCleanupStepResult{}, err
		}
		if err := k.hubKeeper.ReleaseServiceKeyResponsibility(
			ctx, shared.ParticipantTypeCortexNode, assignment.WinnerWorker, hex.EncodeToString(responsibilityID),
		); err != nil {
			return taskCleanupStepResult{}, err
		}
	}
	if selection.BodyStatus == shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE {
		if err := k.releaseTaskBuilderEvidenceResponsibilities(ctx, core, selection); err != nil {
			return taskCleanupStepResult{}, err
		}
		selection.SelectedTaskBuilders = nil
		selection.BodyStatus = shared.StoredBodyStatus_STORED_BODY_STATUS_PRUNED
		if err := k.TaskBuilderSelection.Set(ctx, taskKey, selection); err != nil {
			return taskCleanupStepResult{}, err
		}
	}
	if err := k.RemoveRoleActiveTaskIndexes(ctx, taskKey); err != nil {
		return taskCleanupStepResult{}, err
	}
	if err := k.removeTaskDeadlineIndexes(ctx, taskKey, assignment); err != nil {
		return taskCleanupStepResult{}, err
	}
	advanceTaskCleanupPhase(cursor)
	return taskCleanupStepResult{visited: 1}, nil
}

func hasTaskBucketRef(ctx context.Context, store collections.Map[types.TaskBucketRefKeyTriple, types.TaskBucketRefState], taskKey types.TaskKey) (bool, error) {
	iter, err := store.Iterate(ctx,
		collections.NewPrefixedTripleRange[types.Hash32Key, int32, string](taskKey))
	if err != nil {
		return false, err
	}
	defer iter.Close()
	return iter.Valid(), nil
}

func (k Keeper) removeTaskDeadlineIndexes(ctx context.Context, taskKey types.TaskKey, assignment types.TaskAssignmentState) error {
	deadlines := []struct {
		set    collections.KeySet[types.DeadlineIndexKey]
		height uint64
	}{
		{k.AssignmentRandomnessIndex, assignment.AssignmentRandomnessHeight},
		{k.InferDeadlineIndex, assignment.InferDeadlineHeight},
	}
	for _, deadline := range deadlines {
		if deadline.height != 0 {
			if err := deadline.set.Remove(ctx, types.NewDeadlineIndexKey(deadline.height, taskKey)); err != nil {
				return err
			}
		}
	}
	for _, round := range []uint32{types.VerifyRoundV1, types.ChallengeVerifyRoundV1} {
		roundKey := types.NewVerifyRoundKey(taskKey, round)
		if window, err := k.VerifierCandidateWindow.Get(ctx, roundKey); err == nil {
			for _, remove := range []func() error{
				func() error {
					return k.VerifierWindowBuildIndex.Remove(ctx, types.NewVerifyRoundIndexKey(window.WindowRandomnessHeight, taskKey, round))
				},
				func() error {
					return k.VerifierHandraiseCloseIndex.Remove(ctx, types.NewVerifyRoundIndexKey(window.HandraiseCloseHeight, taskKey, round))
				},
				func() error {
					return k.VerifierSelectionRandomnessIndex.Remove(ctx, types.NewVerifyRoundIndexKey(window.SelectionRandomnessHeight, taskKey, round))
				},
				func() error {
					return k.VerifyOpenDeadlineIndex.Remove(ctx, types.NewDeadlineIndexKey(window.AssignmentDeadlineHeight, taskKey))
				},
			} {
				if err := remove(); err != nil {
					return err
				}
			}
		} else if !errors.Is(err, collections.ErrNotFound) {
			return err
		}
		if verifier, err := k.VerifierAssignment.Get(ctx, roundKey); err == nil {
			if err := k.CommitDeadlineIndex.Remove(ctx, types.NewDeadlineIndexKey(verifier.CommitDeadlineHeight, taskKey)); err != nil {
				return err
			}
			if verifier.RevealDeadlineHeight != 0 {
				if err := k.RevealDeadlineIndex.Remove(ctx, types.NewDeadlineIndexKey(verifier.RevealDeadlineHeight, taskKey)); err != nil {
					return err
				}
			}
			if err := k.VerifyDeadlineIndex.Remove(ctx, types.NewDeadlineIndexKey(verifier.VerifyDeadlineHeight, taskKey)); err != nil {
				return err
			}
		} else if !errors.Is(err, collections.ErrNotFound) {
			return err
		}
	}
	if summary, err := k.TaskRoundSummary.Get(ctx, taskKey); err == nil {
		if summary.XChallengeCloseHeight != nil {
			if err := k.ChallengeWindowCloseIndex.Remove(ctx, types.NewDeadlineIndexKey(summary.GetChallengeCloseHeight(), taskKey)); err != nil {
				return err
			}
		}
		if summary.XSettlementFactsCutoffHeight != nil {
			params, err := k.Params.Get(ctx)
			if err != nil {
				return err
			}
			deadline, err := settlementDeadlineHeight(summary.GetRoundsClosedHeight(), params)
			if err != nil {
				return err
			}
			if err := k.SettlementDeadlineIndex.Remove(ctx, types.NewDeadlineIndexKey(deadline, taskKey)); err != nil {
				return err
			}
		}
	} else if !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	return nil
}

func (k Keeper) compactTask(ctx context.Context, taskKey types.TaskKey, cursor *types.TaskCleanupCursorState, targetHeight, currentHeight uint64) (taskCleanupStepResult, error) {
	if step, pending, err := k.cleanupCompactionBodies(ctx, taskKey, cursor); err != nil || pending {
		return step, err
	}
	summary, commitKeys, err := k.buildTaskTerminalSummary(ctx, taskKey, currentHeight)
	if err != nil {
		return taskCleanupStepResult{}, err
	}
	if err := k.removeCompactedTaskDetails(ctx, taskKey, commitKeys); err != nil {
		return taskCleanupStepResult{}, err
	}
	if err := k.TaskTerminalSummary.Set(ctx, taskKey, summary); err != nil {
		return taskCleanupStepResult{}, err
	}
	if err := k.registerEpochTaskSummarySource(ctx, summary); err != nil {
		return taskCleanupStepResult{}, err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return taskCleanupStepResult{}, err
	}
	pruneHeight, overflow := checkedHeightAdd(currentHeight, params.Cleanup.TaskTerminalSummaryRetentionBlocks)
	if overflow {
		return taskCleanupStepResult{}, fmt.Errorf("task terminal summary prune height overflow")
	}
	if err := k.TaskTerminalSummaryPruneIndex.Set(ctx,
		types.NewDeadlineIndexKey(pruneHeight, taskKey)); err != nil {
		return taskCleanupStepResult{}, err
	}
	if err := k.TaskCleanupCursor.Remove(ctx, taskKey); err != nil {
		return taskCleanupStepResult{}, err
	}
	if err := removeDeadlineIndex(ctx, k.EvidenceCleanupIndex, taskKey, targetHeight); err != nil {
		return taskCleanupStepResult{}, err
	}
	return taskCleanupStepResult{visited: 1, deleted: 1, done: true}, nil
}

func (k Keeper) cleanupCompactionBodies(ctx context.Context, taskKey types.TaskKey, cursor *types.TaskCleanupCursorState) (taskCleanupStepResult, bool, error) {
	stage := byte(0)
	if len(cursor.LastProcessedKey) != 0 {
		if len(cursor.LastProcessedKey) != 1 || cursor.LastProcessedKey[0] > 5 {
			return taskCleanupStepResult{}, false, fmt.Errorf("task compaction cursor is invalid")
		}
		stage = cursor.LastProcessedKey[0]
	}
	for stage <= 5 {
		var deleted bool
		var err error
		switch stage {
		case 0:
			deleted, err = k.deleteOneTaskCommitDetail(ctx, taskKey)
		case 1:
			deleted, err = deleteFirstMapRow(ctx, k.TaskGasReimbursement,
				collections.NewPrefixedTripleRange[types.Hash32Key, types.Hash32Key, uint32](taskKey))
		case 2:
			deleted, err = deleteFirstMapRow(ctx, k.VerifierPayout,
				collections.NewPrefixedPairRange[types.Hash32Key, uint32](taskKey))
		case 3:
			deleted, err = deleteFirstMapRow(ctx, k.TaskStageHandraiseUnion,
				collections.NewPrefixedPairRange[types.Hash32Key, int32](taskKey))
		case 4:
			deleted, err = deleteFirstMapRow(ctx, k.BuilderDataUnavailableAggregate,
				collections.NewPrefixedTripleRange[types.Hash32Key, uint32, string](taskKey))
		case 5:
			deleted, err = deleteFirstMapRow(ctx, k.VerifierCandidateWindow,
				collections.NewPrefixedPairRange[types.Hash32Key, uint32](taskKey))
		}
		if err != nil {
			return taskCleanupStepResult{}, false, err
		}
		if deleted {
			cursor.LastProcessedKey = []byte{stage}
			return taskCleanupStepResult{visited: 1, deleted: 1}, true, nil
		}
		stage++
		cursor.LastProcessedKey = []byte{stage}
	}
	return taskCleanupStepResult{}, false, nil
}

func (k Keeper) deleteOneTaskCommitDetail(ctx context.Context, taskKey types.TaskKey) (bool, error) {
	for _, round := range []uint32{types.VerifyRoundV1, types.ChallengeVerifyRoundV1} {
		assignment, err := k.VerifierAssignment.Get(ctx, types.NewVerifyRoundKey(taskKey, round))
		if errors.Is(err, collections.ErrNotFound) {
			continue
		}
		if err != nil {
			return false, err
		}
		for _, selected := range assignment.SelectedVerifiers {
			commit, err := types.DeriveCommitKey(sdk.UnwrapSDKContext(ctx).ChainID(), taskKey, round, selected.OperatorAddress)
			if err != nil {
				return false, err
			}
			key := types.NewCommitKey(commit[:])
			if has, err := k.ResultReceiptState.Has(ctx, key); err != nil {
				return false, err
			} else if has {
				return true, k.ResultReceiptState.Remove(ctx, key)
			}
			if has, err := k.CommitState.Has(ctx, key); err != nil {
				return false, err
			} else if has {
				return true, k.CommitState.Remove(ctx, key)
			}
		}
	}
	return false, nil
}

func (k Keeper) buildTaskTerminalSummary(ctx context.Context, taskKey types.TaskKey, currentHeight uint64) (types.TaskTerminalSummaryState, []types.CommitKey, error) {
	core, err := k.TaskCore.Get(ctx, taskKey)
	if err != nil {
		return types.TaskTerminalSummaryState{}, nil, err
	}
	assignment, err := k.TaskAssignment.Get(ctx, taskKey)
	if err != nil {
		return types.TaskTerminalSummaryState{}, nil, err
	}
	selection, err := k.TaskBuilderSelection.Get(ctx, taskKey)
	if err != nil {
		return types.TaskTerminalSummaryState{}, nil, err
	}
	settlement, err := k.TaskSettlement.Get(ctx, taskKey)
	if err != nil {
		return types.TaskTerminalSummaryState{}, nil, err
	}
	retained, err := k.SettlementFactsRetained.Get(ctx, taskKey)
	if err != nil {
		return types.TaskTerminalSummaryState{}, nil, err
	}
	roundSummary, err := k.TaskRoundSummary.Get(ctx, taskKey)
	if err != nil {
		return types.TaskTerminalSummaryState{}, nil, err
	}
	builderSet, err := k.hubKeeper.GetBuilderSetByID(sdk.UnwrapSDKContext(ctx), selection.BuilderSetId)
	if err != nil || !bytes.Equal(builderSet.BuilderSetHash, selection.BuilderSetHash) {
		return types.TaskTerminalSummaryState{}, nil, fmt.Errorf("task terminal BuilderSet authority is unavailable")
	}
	zero32 := make([]byte, types.Hash32Len)
	round1Selected := append([]byte(nil), zero32...)
	round2Selected := append([]byte(nil), zero32...)
	commitKeys := []types.CommitKey{}
	for _, round := range []uint32{types.VerifyRoundV1, types.ChallengeVerifyRoundV1} {
		verifier, err := k.VerifierAssignment.Get(ctx, types.NewVerifyRoundKey(taskKey, round))
		if errors.Is(err, collections.ErrNotFound) {
			continue
		}
		if err != nil {
			return types.TaskTerminalSummaryState{}, nil, err
		}
		if round == types.VerifyRoundV1 {
			round1Selected = append([]byte(nil), verifier.SelectedVerifiersHash...)
		} else {
			round2Selected = append([]byte(nil), verifier.SelectedVerifiersHash...)
		}
		for _, selected := range verifier.SelectedVerifiers {
			key, err := types.DeriveCommitKey(
				sdk.UnwrapSDKContext(ctx).ChainID(), taskKey, round, selected.OperatorAddress,
			)
			if err != nil {
				return types.TaskTerminalSummaryState{}, nil, err
			}
			commitKeys = append(commitKeys, key[:])
		}
	}
	fundingResolution := append([]byte(nil), zero32...)
	if round2, err := k.VerificationRound.Get(ctx,
		types.NewVerifyRoundKey(taskKey, types.ChallengeVerifyRoundV1)); err == nil {
		fundingResolution = append([]byte(nil), round2.GetFundingResolutionHash()...)
	} else if !errors.Is(err, collections.ErrNotFound) {
		return types.TaskTerminalSummaryState{}, nil, err
	}

	summary := types.TaskTerminalSummaryState{
		TaskId: append([]byte(nil), core.TaskId...), SessionId: append([]byte(nil), core.SessionId...),
		OrderSequence: core.OrderSequence, TaskHash: append([]byte(nil), core.AcceptedTaskHash...),
		TerminalPhase: core.TaskPhase, Verdict: settlement.Verdict, FailureClass: settlement.FailureClass,
		SettlementStatus: core.SettlementStatus, FinalityStatus: core.FinalityStatus,
		EffectiveVerifyRound: core.EffectiveVerifyRound,
		ModelId:              core.ModelId, ProfileVersion: core.ProfileVersion, TaskType: core.TaskType,
		CandidatePoolSnapshotId:             append([]byte(nil), assignment.CandidatePoolSnapshotId...),
		CandidatePoolHash:                   append([]byte(nil), assignment.CandidatePoolHash...),
		AssignmentCandidateSetHash:          append([]byte(nil), assignment.AssignmentCandidateSetHash...),
		CandidatePoolRefReleased:            assignment.CandidatePoolRefReleased,
		Round1SelectedVerifiersHashOrZero32: round1Selected,
		Round2SelectedVerifiersHashOrZero32: round2Selected,
		BuilderSetVersion:                   builderSet.BuilderSetVersion, BuilderSetId: selection.BuilderSetId,
		BuilderSetHash:               append([]byte(nil), selection.BuilderSetHash...),
		SelectedTaskBuildersHash:     append([]byte(nil), selection.SelectedTaskBuildersHash...),
		ProfileExecutionSnapshotHash: append([]byte(nil), retained.ProfileExecutionSnapshotHash...),
		GenerationParamsDigest:       append([]byte(nil), retained.GenerationParamsDigest...),
		InferReceiptRefOrZero32:      append([]byte(nil), settlement.InferReceiptRefOrZero32...),
		GeneratedTokenCount:          settlement.GeneratedTokenCount,
		SettlementId:                 append([]byte(nil), settlement.SettlementId...),
		SettlementFactsCutoffHeight:  settlement.SettlementFactsCutoffHeight,
		SettlementHeight:             settlement.SettlementHeight, TaskFinalityHeight: settlement.TaskFinalityHeight,
		SettlementFactsHash:                 append([]byte(nil), settlement.SettlementFactsHash...),
		SettlementPlanHash:                  append([]byte(nil), settlement.SettlementPlanHash...),
		SettlementBillHashOrZero32:          append([]byte(nil), settlement.SettlementBillHashOrZero32...),
		TaskRoundSummaryHash:                append([]byte(nil), settlement.TaskRoundSummaryHash...),
		Round1FactsHashOrZero32:             append([]byte(nil), roundSummary.Round1FactsHashOrZero32...),
		Round2FactsHashOrZero32:             append([]byte(nil), roundSummary.Round2FactsHashOrZero32...),
		RoundEffectRoot:                     append([]byte(nil), roundSummary.Round2EffectRootOrZero32...),
		Round2FundingResolutionHashOrZero32: fundingResolution,
		FaultSummaryHash:                    append([]byte(nil), retained.FaultSummaryHash...),
		CreatedHeight:                       core.CreatedHeight, CompactedHeight: currentHeight,
	}
	if settlement.XWorkerOperatorAddress != nil {
		summary.XWinnerWorker = &types.TaskTerminalSummaryState_WinnerWorker{
			WinnerWorker: settlement.GetWorkerOperatorAddress(),
		}
	}
	if roundSummary.XRound2Outcome != nil {
		summary.XRound2Outcome = &types.TaskTerminalSummaryState_Round2Outcome{
			Round2Outcome: roundSummary.GetRound2Outcome(),
		}
	}
	digest, err := k.taskTerminalSummaryHash(ctx, summary)
	if err != nil {
		return types.TaskTerminalSummaryState{}, nil, err
	}
	summary.SummaryHash = digest
	return summary, commitKeys, nil
}

func (k Keeper) taskTerminalSummaryHash(ctx context.Context, state types.TaskTerminalSummaryState) ([]byte, error) {
	hashes := [][]byte{
		state.TaskId, state.SessionId, state.TaskHash,
		state.CandidatePoolSnapshotId, state.CandidatePoolHash, state.AssignmentCandidateSetHash,
		state.Round1SelectedVerifiersHashOrZero32, state.Round2SelectedVerifiersHashOrZero32,
		state.BuilderSetHash, state.SelectedTaskBuildersHash, state.ProfileExecutionSnapshotHash,
		state.GenerationParamsDigest, state.InferReceiptRefOrZero32, state.SettlementId,
		state.SettlementFactsHash, state.SettlementPlanHash, state.SettlementBillHashOrZero32,
		state.TaskRoundSummaryHash, state.Round1FactsHashOrZero32, state.Round2FactsHashOrZero32,
		state.RoundEffectRoot, state.Round2FundingResolutionHashOrZero32, state.FaultSummaryHash,
	}
	for _, value := range hashes {
		if len(value) != types.Hash32Len {
			return nil, fmt.Errorf("task terminal summary contains a non-Hash32 commitment")
		}
	}
	winner := shared.OptionalAbsentCanonicalFieldV1()
	if state.XWinnerWorker != nil {
		address, err := types.CanonicalOperatorAddressBytes("winner_worker", state.GetWinnerWorker())
		if err != nil {
			return nil, err
		}
		winner = shared.OptionalPresentCanonicalFieldV1(address)
	}
	round2Outcome := shared.RoundOutcomeV1_ROUND_OUTCOME_V1_UNSPECIFIED
	if state.XRound2Outcome != nil {
		round2Outcome = state.GetRound2Outcome()
	}
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainTaskTerminalSummaryV1)).Raw(
		[]byte(sdk.UnwrapSDKContext(ctx).ChainID()), state.TaskId, state.SessionId,
		shared.Uint64BE(state.OrderSequence), state.TaskHash,
		shared.EnumBE(uint32(state.TerminalPhase)), shared.EnumBE(uint32(state.Verdict)),
		shared.EnumBE(uint32(state.FailureClass)), shared.EnumBE(uint32(state.SettlementStatus)),
		shared.EnumBE(uint32(state.FinalityStatus)), shared.Uint32BE(state.EffectiveVerifyRound),
		[]byte(state.ModelId), shared.Uint32BE(state.ProfileVersion), shared.EnumBE(uint32(state.TaskType)),
		state.CandidatePoolSnapshotId, state.CandidatePoolHash, state.AssignmentCandidateSetHash,
		shared.BoolByte(state.CandidatePoolRefReleased),
	).Field(winner).Raw(
		state.Round1SelectedVerifiersHashOrZero32, state.Round2SelectedVerifiersHashOrZero32,
		shared.Uint64BE(state.BuilderSetVersion), []byte(state.BuilderSetId), state.BuilderSetHash,
		state.SelectedTaskBuildersHash, state.ProfileExecutionSnapshotHash, state.GenerationParamsDigest,
		state.InferReceiptRefOrZero32, shared.Uint64BE(state.GeneratedTokenCount), state.SettlementId,
		shared.Uint64BE(state.SettlementFactsCutoffHeight), shared.Uint64BE(state.SettlementHeight),
		shared.Uint64BE(state.TaskFinalityHeight), state.SettlementFactsHash, state.SettlementPlanHash,
		state.SettlementBillHashOrZero32, state.TaskRoundSummaryHash, state.Round1FactsHashOrZero32,
		state.Round2FactsHashOrZero32, shared.EnumBE(uint32(round2Outcome)), state.RoundEffectRoot,
		state.Round2FundingResolutionHashOrZero32, state.FaultSummaryHash,
		shared.Uint64BE(state.CreatedHeight), shared.Uint64BE(state.CompactedHeight),
	).Sum()
}

func (k Keeper) removeCompactedTaskDetails(ctx context.Context, taskKey types.TaskKey, commitKeys []types.CommitKey) error {
	_ = commitKeys // variable-length commit/result bodies were removed by the cursor above.
	if err := removeAllMapRows(ctx, k.VerifierAssignment,
		collections.NewPrefixedPairRange[types.Hash32Key, uint32](taskKey)); err != nil {
		return err
	}
	if err := removeAllMapRows(ctx, k.VerificationRound,
		collections.NewPrefixedPairRange[types.Hash32Key, uint32](taskKey)); err != nil {
		return err
	}
	for _, remove := range []func(context.Context, types.TaskKey) error{
		k.AssignmentCandidateSet.Remove,
		k.InferReceipt.Remove,
		k.TaskRoundSummary.Remove,
		k.TaskSettlement.Remove,
		k.SettlementFactsRetained.Remove,
		k.TaskBuilderSelection.Remove,
		k.TaskAssignment.Remove,
		k.TaskBudget.Remove,
		k.TaskCore.Remove,
	} {
		if err := remove(ctx, taskKey); err != nil {
			return err
		}
	}
	return nil
}

func removeAllMapRows[K, V any](ctx context.Context, store collections.Map[K, V], rng collections.Ranger[K]) error {
	iter, err := store.Iterate(ctx, rng)
	if err != nil {
		return err
	}
	defer iter.Close()
	keys := []K{}
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return err
		}
		keys = append(keys, key)
	}
	for _, key := range keys {
		if err := store.Remove(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) SweepTaskTerminalSummaryPrune(ctx context.Context, currentHeight, limit uint64) (uint64, error) {
	if limit == 0 {
		return 0, nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, err
	}
	if max := uint64(params.Cleanup.MaxTaskTerminalSummaryPruneItemsPerBlock); limit > max {
		limit = max
	}
	visited := uint64(0)
	iter, err := k.TaskTerminalSummaryPruneIndex.Iterate(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer iter.Close()
	for ; iter.Valid() && visited < limit; iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return visited, err
		}
		if key.K1() > currentHeight {
			break
		}
		visited++
		sdkCtx := sdk.UnwrapSDKContext(ctx)
		cacheCtx, write := sdkCtx.CacheContext()
		cache := sdk.WrapSDKContext(cacheCtx)
		summary, err := k.TaskTerminalSummary.Get(cache, key.K2())
		if errors.Is(err, collections.ErrNotFound) {
			if err := k.TaskTerminalSummaryPruneIndex.Remove(cache, key); err != nil {
				return visited, err
			}
			write()
			continue
		}
		if err != nil {
			return visited, err
		}
		epochLength := k.hubKeeper.GetHubParams(cacheCtx).EpochLengthBlocks
		if epochLength == 0 {
			return visited, fmt.Errorf("epoch length is unavailable")
		}
		epoch := shared.EpochForHeight(summary.SettlementHeight, epochLength)
		consumed, err := k.EpochTaskSummaryReceipt.Has(cache, epoch)
		if err != nil {
			return visited, err
		}
		if !consumed {
			retry, overflow := checkedHeightAdd(currentHeight, 1)
			if overflow {
				return visited, fmt.Errorf("task terminal summary prune retry overflow")
			}
			if err := k.TaskTerminalSummaryPruneIndex.Remove(cache, key); err != nil {
				return visited, err
			}
			if err := k.TaskTerminalSummaryPruneIndex.Set(cache, types.NewDeadlineIndexKey(retry, key.K2())); err != nil {
				return visited, err
			}
			write()
			continue
		}
		if err := k.TaskTerminalSummary.Remove(cache, key.K2()); err != nil {
			return visited, err
		}
		if err := k.EpochTaskSummarySourceIndex.Remove(cache,
			types.NewDeadlineIndexKey(summary.SettlementHeight, key.K2())); err != nil {
			return visited, err
		}
		if err := k.TaskTerminalSummaryPruneIndex.Remove(cache, key); err != nil {
			return visited, err
		}
		write()
	}
	return visited, nil
}
