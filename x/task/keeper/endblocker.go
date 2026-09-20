package keeper

import (
	"bytes"
	"context"
	"errors"
	"sort"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/task/types"
)

type endBlockQueuePriority uint32

const (
	endBlockPriorityVerifierWindowBuild endBlockQueuePriority = iota
	endBlockPriorityVerifierHandraiseClose
	endBlockPriorityVerifierSelectionRandomness
	endBlockPriorityEpochTaskSummary    endBlockQueuePriority = 190
	endBlockPriorityTaskFailurePrune    endBlockQueuePriority = 199
	endBlockPriorityTaskSummaryPrune    endBlockQueuePriority = 200
	endBlockPrioritySessionHistoryPrune endBlockQueuePriority = 201
	endBlockPrioritySessionSummaryPrune endBlockQueuePriority = 202
)

const (
	// Assignment selection can read a bounded candidate set whose serialized
	// body is not reflected in finalizeAssignmentRandomness' row-byte result.
	// Charging the full Task byte budget makes it the last visited row of this
	// block even when the exact candidate payload is larger than the budget.
	assignmentSweepConservativeBytesV1 = maxDeadlineSweepBytesPerBlockV1
	// Regular task deadlines touch TaskCore plus a bounded set of fixed-shape
	// assignment, budget, challenge and index rows. TaskCore's actual Size is
	// already reported by the handler; this floor covers the auxiliary rows.
	taskDeadlineConservativeBytesV1 = uint64(8 << 10)
	// Evidence cleanup currently retires one fixed deadline index row. Session
	// lifecycle touches one fixed StreamState plus fixed-shape index rows. These
	// bounds are deliberately above those protobuf/key shapes.
	evidenceCleanupConservativeBytesV1  = uint64(256)
	sessionLifecycleConservativeBytesV1 = uint64(8 << 10)
)

// EndBlockSweepResult reports visited work per queue. Every field counts
// *visited* index rows, not successful transitions: the API contract /
// §4.6 make the
// per-block cap a visited-work budget so that a permanently unprocessable
// ("poison") row cannot be rescanned for free every block.
type EndBlockSweepResult struct {
	VerifierWindowBuild         uint64
	VerifierHandraiseClose      uint64
	VerifierSelectionRandomness uint64
	AssignmentRandomness        uint64
	InferDeadline               uint64
	VerifyOpenDeadline          uint64
	CommitDeadline              uint64
	RevealDeadline              uint64
	VerifyDeadline              uint64
	ChallengeWindowClose        uint64
	RoundEconomicEffect         uint64
	Settlement                  uint64
	EvidenceCleanup             uint64
	// SessionLifecycle is the §10.0b1 MARK_IDLE/CLOSE processor. It shares its
	// executor with MsgSweepDeadline(SessionLifecycleLocator); V1 does not
	// register a second MsgSessionSweep.
	SessionLifecycle    uint64
	EpochTaskSummary    uint64
	TaskFailurePrune    uint64
	TaskSummaryPrune    uint64
	SessionHistoryPrune uint64
	SessionSummaryPrune uint64
	Total               uint64
}

// primaryID is the §5.9 `primary_id` tie-break component of the queue head: the
// raw Hash32 task_id or session_id for the object queues, and the ordered
// encoding of the epoch for the epoch-summary schedule. It is never rendered, so
// it is carried as raw bytes; the lowercase-hex spelling it replaces compared
// identically because hex is order-preserving over the same digest.
type deadlineQueueHead struct {
	deadline  uint64
	primaryID []byte
}

// sameDeadlineQueueHead replaces the struct == that the string-keyed version
// used. A struct holding a slice is not comparable, and the semantics wanted
// here are "the queue head did not move", which is exactly field equality.
func sameDeadlineQueueHead(a, b deadlineQueueHead) bool {
	return a.deadline == b.deadline && bytes.Equal(a.primaryID, b.primaryID)
}

func firstDueTaskDeadline(ctx context.Context, set collections.KeySet[types.DeadlineIndexKey], currentHeight uint64) (deadlineQueueHead, bool, error) {
	iter, err := set.Iterate(ctx, nil)
	if err != nil {
		return deadlineQueueHead{}, false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return deadlineQueueHead{}, false, nil
	}
	key, err := iter.Key()
	if err != nil || key.K1() > currentHeight {
		return deadlineQueueHead{}, false, err
	}
	return deadlineQueueHead{deadline: key.K1(), primaryID: key.K2()}, true, nil
}

func firstDueVerifyRoundIndex(ctx context.Context, set collections.KeySet[types.VerifyRoundIndexKey], currentHeight uint64) (deadlineQueueHead, bool, error) {
	iter, err := set.Iterate(ctx, nil)
	if err != nil {
		return deadlineQueueHead{}, false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return deadlineQueueHead{}, false, nil
	}
	key, err := iter.Key()
	if err != nil || key.K1() > currentHeight {
		return deadlineQueueHead{}, false, err
	}
	return deadlineQueueHead{deadline: key.K1(), primaryID: key.K2()}, true, nil
}

func (k Keeper) firstDueSessionLifecycle(ctx context.Context, currentHeight uint64) (deadlineQueueHead, bool, error) {
	iter, err := k.SessionLifecycleIndex.Iterate(ctx, nil)
	if err != nil {
		return deadlineQueueHead{}, false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return deadlineQueueHead{}, false, nil
	}
	key, err := iter.Key()
	if err != nil || key.K1() > currentHeight {
		return deadlineQueueHead{}, false, err
	}
	return deadlineQueueHead{deadline: key.K1(), primaryID: key.K2()}, true, nil
}

type verifyRoundRowHandler func(context.Context, types.TaskKey, uint32, uint64) (deadlineSweepOutcome, uint64, error)

// sweepExpiredVerifyRoundIndex applies the same visited-item semantics as task
// deadline queues to the three round-scoped Open Verify indexes. Rows are
// collected before mutation and every pending/poison/stale row consumes budget.
func (k Keeper) sweepExpiredVerifyRoundIndexWithBudget(
	ctx context.Context,
	set collections.KeySet[types.VerifyRoundIndexKey],
	currentHeight, maxItems, maxBytes uint64,
	handle verifyRoundRowHandler,
) (deadlineSweepUsage, error) {
	var usage deadlineSweepUsage
	if maxItems == 0 || maxBytes == 0 {
		return usage, nil
	}
	// Retaining the decoded task ID past the Next() that invalidates the
	// iterator buffer is safe: Hash32KeyCodec.Decode returns a copy
	// (types/key_codec.go), so the slice does not alias iterator storage.
	type dueRow struct {
		height uint64
		taskID types.TaskKey
		round  uint32
	}
	due := make([]dueRow, 0, maxItems)
	iter, err := set.Iterate(ctx, nil)
	if err != nil {
		return usage, err
	}
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			_ = iter.Close()
			return usage, err
		}
		if key.K1() > currentHeight {
			break
		}
		due = append(due, dueRow{height: key.K1(), taskID: key.K2(), round: key.K3()})
		if uint64(len(due)) >= maxItems {
			break
		}
	}
	if err := iter.Close(); err != nil {
		return usage, err
	}

	for _, row := range due {
		if usage.serializedBytes >= maxBytes {
			break
		}
		sdkCtx := sdk.UnwrapSDKContext(ctx)
		cacheCtx, write := sdkCtx.CacheContext()
		cache := sdk.WrapSDKContext(cacheCtx)
		outcome, rowBytes, err := handle(cache, row.taskID, row.round, row.height)
		if err != nil {
			usage.visited++
			return usage, err
		}
		switch outcome {
		case deadlineSweepStale:
			if err := set.Remove(cache, types.NewVerifyRoundIndexKey(row.height, row.taskID, row.round)); err != nil {
				return usage, err
			}
		case deadlineSweepAdvanced, deadlineSweepPending:
		default:
			return usage, errorsmod.Wrap(types.ErrInvariantBroken, "unknown verifier round sweep outcome")
		}
		rowBytes, overflow := checkedAddUint64(rowBytes, deadlineIndexRowBytes(row.taskID, true))
		if overflow {
			return usage, errorsmod.Wrap(types.ErrInvariantBroken, "verifier round sweep row byte accounting overflow")
		}
		remainingBytes := maxBytes - usage.serializedBytes
		if rowBytes > remainingBytes {
			break
		}
		write()
		usage.visited++
		if err := addDeadlineSweepBytes(&usage, rowBytes); err != nil {
			return usage, err
		}
	}
	return usage, nil
}

func loadVerifierWindowForRoundIndex(
	ctx context.Context,
	k Keeper,
	taskID types.TaskKey,
	round uint32,
) (types.VerifierCandidateWindowState, bool, uint64, error) {
	window, err := k.VerifierCandidateWindow.Get(ctx, types.NewVerifyRoundKey(taskID, round))
	if err != nil {
		if errIsNotFound(err) {
			return types.VerifierCandidateWindowState{}, false, 0, nil
		}
		return types.VerifierCandidateWindowState{}, false, 0, err
	}
	windowTaskID, keyErr := taskStoreKey(window.TaskId)
	if keyErr != nil || !bytes.Equal(windowTaskID, taskID) || window.VerifyRound != round || !isPhase0VerifyRound(round) || window.SchemaVersion != 1 {
		return types.VerifierCandidateWindowState{}, false, uint64(window.Size()),
			errorsmod.Wrap(types.ErrInvariantBroken, "verifier window/index scope mismatch")
	}
	return window, true, uint64(window.Size()), nil
}

func validateOpenVerifyUnionIndexScope(union types.TaskStageHandraiseUnionState, taskID types.TaskKey) error {
	unionTaskID, err := taskStoreKey(union.TaskId)
	if err != nil || !bytes.Equal(unionTaskID, taskID) || union.SchemaVersion != 1 ||
		union.Stage != types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY {
		return errorsmod.Wrap(types.ErrInvariantBroken, "verifier union/index scope mismatch")
	}
	return nil
}

func validateOpenVerifyCursorIndexScope(cursor types.TaskCandidateFinalizeCursorState, taskID types.TaskKey) error {
	cursorTaskID, err := taskStoreKey(cursor.TaskId)
	if err != nil || !bytes.Equal(cursorTaskID, taskID) || cursor.SchemaVersion != 1 ||
		cursor.Stage != types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY {
		return errorsmod.Wrap(types.ErrInvariantBroken, "verifier finalize cursor/index scope mismatch")
	}
	return nil
}

func (k Keeper) processVerifierWindowBuildIndex(ctx context.Context, currentHeight, maxItems uint64) (uint64, error) {
	usage, err := k.processVerifierWindowBuildIndexWithBudget(ctx, currentHeight, maxItems, maxDeadlineSweepBytesPerBlockV1)
	return usage.visited, err
}

func (k Keeper) processVerifierWindowBuildIndexWithBudget(ctx context.Context, currentHeight, maxItems, maxBytes uint64) (deadlineSweepUsage, error) {
	return k.sweepExpiredVerifyRoundIndexWithBudget(ctx, k.VerifierWindowBuildIndex, currentHeight, maxItems, maxBytes,
		func(ctx context.Context, taskID types.TaskKey, round uint32, height uint64) (deadlineSweepOutcome, uint64, error) {
			window, found, rowBytes, err := loadVerifierWindowForRoundIndex(ctx, k, taskID, round)
			if err != nil || !found {
				return deadlineSweepStale, rowBytes, err
			}
			if window.WindowRandomnessHeight != height {
				return deadlineSweepPending, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier window build height mismatch")
			}
			switch window.Status {
			case types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY:
				if len(window.GetWindowRandomnessBeacon()) != types.Hash32Len ||
					len(window.GetVerifierWindowHash()) != types.Hash32Len || window.WindowSize == 0 || window.GeneratedHeight < height {
					return deadlineSweepPending, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "READY verifier window body is incomplete")
				}
				return deadlineSweepStale, rowBytes, nil
			case types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_PRUNED:
				return deadlineSweepStale, rowBytes, nil
			case types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_SOURCE_FROZEN:
				if len(window.GetWindowRandomnessBeacon()) != 0 || len(window.GetVerifierWindowHash()) != 0 || window.GeneratedHeight != 0 {
					return deadlineSweepPending, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "SOURCE_FROZEN verifier window has generated fields")
				}
				materialized, materializedBytes, err := k.materializeVerifierWindowAtHeight(ctx, taskID, round, currentHeight)
				rowBytes += materializedBytes
				if err != nil {
					return deadlineSweepPending, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
				}
				if materialized {
					return deadlineSweepAdvanced, rowBytes, nil
				}
				return deadlineSweepPending, rowBytes, nil
			default:
				return deadlineSweepPending, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "invalid verifier window status")
			}
		})
}

// retireVerifierRowToFallbackDeadline drops a verifier queue key for a frozen
// window that can no longer be finalized — it closed with fewer entrants than
// selected_verifier_count, or a selected verifier can no longer back its duty
// (ErrVerifierLiabilityUnavailable). It is the same disposal the empty-union
// case below already performs, for the same reason: the legal set cannot change
// after the close height, so the frozen verify-open assignment deadline is the
// sole remaining terminal key and fails the task as
// TASK_FAILURE_CLASS_INSUFFICIENT_VERIFIER.
//
// Returning deadlineSweepPending instead would re-run the finalize attempt on
// every block and hold the head of the close sweep — the rows are drained in
// height order — until that deadline fires.
//
// The guard is not cosmetic: if the later deadline is already past or was never
// indexed, nothing else would ever retire this task, so that genuinely is a
// store inconsistency and keeps ErrInvariantBroken.
func (k Keeper) retireVerifierRowToFallbackDeadline(
	ctx context.Context,
	taskID types.TaskKey,
	window types.VerifierCandidateWindowState,
	height, rowBytes uint64,
) (deadlineSweepOutcome, uint64, error) {
	fallbackKey := types.NewDeadlineIndexKey(window.AssignmentDeadlineHeight, taskID)
	scheduled, err := k.VerifyOpenDeadlineIndex.Has(ctx, fallbackKey)
	if err != nil {
		return deadlineSweepPending, rowBytes, err
	}
	if window.AssignmentDeadlineHeight <= height || !scheduled {
		return deadlineSweepPending, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken,
			"verifier window has no later verify-open deadline")
	}
	return deadlineSweepStale, rowBytes, nil
}

func (k Keeper) processVerifierHandraiseCloseIndex(ctx context.Context, currentHeight, maxItems uint64) (uint64, error) {
	usage, err := k.processVerifierHandraiseCloseIndexWithBudget(ctx, currentHeight, maxItems, maxDeadlineSweepBytesPerBlockV1)
	return usage.visited, err
}

func (k Keeper) processVerifierHandraiseCloseIndexWithBudget(ctx context.Context, currentHeight, maxItems, maxBytes uint64) (deadlineSweepUsage, error) {
	return k.sweepExpiredVerifyRoundIndexWithBudget(ctx, k.VerifierHandraiseCloseIndex, currentHeight, maxItems, maxBytes,
		func(ctx context.Context, taskID types.TaskKey, round uint32, height uint64) (deadlineSweepOutcome, uint64, error) {
			window, found, rowBytes, err := loadVerifierWindowForRoundIndex(ctx, k, taskID, round)
			if err != nil || !found {
				return deadlineSweepStale, rowBytes, err
			}
			if window.HandraiseCloseHeight != height {
				return deadlineSweepPending, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier handraise close height mismatch")
			}
			if window.Status == types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_PRUNED {
				return deadlineSweepStale, rowBytes, nil
			}
			if assigned, err := k.VerifierAssignment.Has(ctx, types.NewVerifyRoundKey(taskID, round)); err != nil {
				return deadlineSweepPending, rowBytes, err
			} else if assigned {
				return deadlineSweepStale, rowBytes, nil
			}
			if window.Status == types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_SOURCE_FROZEN {
				fallbackKey := types.NewDeadlineIndexKey(window.AssignmentDeadlineHeight, taskID)
				scheduled, err := k.VerifyOpenDeadlineIndex.Has(ctx, fallbackKey)
				if err != nil {
					return deadlineSweepPending, rowBytes, err
				}
				if window.AssignmentDeadlineHeight <= height || !scheduled {
					return deadlineSweepPending, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "unready verifier window has no later verify-open deadline")
				}
				// No candidate window can open after handraises close. Drop this old
				// queue key and let the already-frozen assignment deadline perform
				// the terminal transition instead of blocking later close rows.
				return deadlineSweepStale, rowBytes, nil
			}
			if window.Status != types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY {
				return deadlineSweepPending, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "handraise close has invalid verifier window status")
			}
			unionKey := types.NewTaskStageKey(taskID, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY)
			union, err := k.TaskStageHandraiseUnion.Get(ctx, unionKey)
			if err != nil {
				if errIsNotFound(err) {
					fallbackKey := types.NewDeadlineIndexKey(window.AssignmentDeadlineHeight, taskID)
					scheduled, fallbackErr := k.VerifyOpenDeadlineIndex.Has(ctx, fallbackKey)
					if fallbackErr != nil {
						return deadlineSweepPending, rowBytes, fallbackErr
					}
					if window.AssignmentDeadlineHeight <= height || !scheduled {
						return deadlineSweepPending, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "empty verifier union has no later verify-open deadline")
					}
					// Nobody entered the frozen window. The union cannot become
					// non-empty after this close height, so the later verify-open
					// deadline is the sole remaining retry/terminal key.
					return deadlineSweepStale, rowBytes, nil
				}
				return deadlineSweepPending, rowBytes, err
			}
			rowBytes += uint64(union.Size())
			if err := validateOpenVerifyUnionIndexScope(union, taskID); err != nil {
				return deadlineSweepPending, rowBytes, err
			}
			if union.WindowCloseHeight != height || union.GetSelectionRandomnessHeight() != window.SelectionRandomnessHeight {
				return deadlineSweepPending, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier union/index scope mismatch")
			}
			if union.Status == types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_OPEN {
				params, err := k.Params.Get(ctx)
				if err != nil {
					return deadlineSweepPending, rowBytes, err
				}
				advanced, _, applyBytes, err := k.finalizeVerifierLegalSetAtHeight(
					ctx, taskID, window, currentHeight,
					uint64(params.Proposals.MaxCandidateStageFinalizeMembersPerBlock),
				)
				rowBytes += applyBytes
				if errors.Is(err, ErrInsufficientVerifierHandraises) {
					return k.retireVerifierRowToFallbackDeadline(ctx, taskID, window, height, rowBytes)
				}
				if err != nil {
					return deadlineSweepPending, rowBytes, err
				}
				if advanced {
					return deadlineSweepAdvanced, rowBytes, nil
				}
				return deadlineSweepPending, rowBytes, nil
			}
			if union.Status != types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_FINALIZING {
				return deadlineSweepPending, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "invalid verifier union status at handraise close")
			}
			cursor, err := k.TaskCandidateFinalizeCursor.Get(ctx, unionKey)
			if err != nil {
				return deadlineSweepPending, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "finalizing verifier union has no cursor")
			}
			rowBytes += uint64(cursor.Size())
			if err := validateOpenVerifyCursorIndexScope(cursor, taskID); err != nil {
				return deadlineSweepPending, rowBytes, err
			}
			if cursor.Status == types.FinalizeCursorStatusV1_FINALIZE_CURSOR_STATUS_V1_RUNNING {
				params, err := k.Params.Get(ctx)
				if err != nil {
					return deadlineSweepPending, rowBytes, err
				}
				advanced, _, applyBytes, err := k.finalizeVerifierLegalSetAtHeight(
					ctx, taskID, window, currentHeight,
					uint64(params.Proposals.MaxCandidateStageFinalizeMembersPerBlock),
				)
				rowBytes += applyBytes
				if errors.Is(err, ErrInsufficientVerifierHandraises) {
					return k.retireVerifierRowToFallbackDeadline(ctx, taskID, window, height, rowBytes)
				}
				if err != nil {
					return deadlineSweepPending, rowBytes, err
				}
				if advanced {
					return deadlineSweepAdvanced, rowBytes, nil
				}
				return deadlineSweepPending, rowBytes, nil
			}
			if cursor.Status != types.FinalizeCursorStatusV1_FINALIZE_CURSOR_STATUS_V1_WAITING_RANDOMNESS ||
				cursor.GetRandomnessHeight() != window.SelectionRandomnessHeight {
				return deadlineSweepPending, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier finalize cursor is non-canonical")
			}
			selectionKey := types.NewVerifyRoundIndexKey(window.SelectionRandomnessHeight, taskID, round)
			selectionIndexed, err := k.VerifierSelectionRandomnessIndex.Has(ctx, selectionKey)
			if err != nil {
				return deadlineSweepPending, rowBytes, err
			}
			if !selectionIndexed {
				return deadlineSweepPending, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "waiting verifier cursor has no selection index")
			}
			return deadlineSweepStale, rowBytes, nil
		})
}

func (k Keeper) processVerifierSelectionRandomnessIndex(ctx context.Context, currentHeight, maxItems uint64) (uint64, error) {
	usage, err := k.processVerifierSelectionRandomnessIndexWithBudget(ctx, currentHeight, maxItems, maxDeadlineSweepBytesPerBlockV1)
	return usage.visited, err
}

func (k Keeper) processVerifierSelectionRandomnessIndexWithBudget(ctx context.Context, currentHeight, maxItems, maxBytes uint64) (deadlineSweepUsage, error) {
	return k.sweepExpiredVerifyRoundIndexWithBudget(ctx, k.VerifierSelectionRandomnessIndex, currentHeight, maxItems, maxBytes,
		func(ctx context.Context, taskID types.TaskKey, round uint32, height uint64) (deadlineSweepOutcome, uint64, error) {
			if assigned, err := k.VerifierAssignment.Has(ctx, types.NewVerifyRoundKey(taskID, round)); err != nil {
				return deadlineSweepPending, 0, err
			} else if assigned {
				return deadlineSweepStale, 0, nil
			}
			window, found, rowBytes, err := loadVerifierWindowForRoundIndex(ctx, k, taskID, round)
			if err != nil || !found {
				return deadlineSweepStale, rowBytes, err
			}
			if window.SelectionRandomnessHeight != height {
				return deadlineSweepPending, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier selection height mismatch")
			}
			if window.Status == types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_PRUNED {
				return deadlineSweepStale, rowBytes, nil
			}
			if window.Status != types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY {
				return deadlineSweepPending, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier selection requires a READY window")
			}
			stageKey := types.NewTaskStageKey(taskID, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY)
			union, err := k.TaskStageHandraiseUnion.Get(ctx, stageKey)
			if err != nil {
				return deadlineSweepPending, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier selection has no frozen union")
			}
			cursor, err := k.TaskCandidateFinalizeCursor.Get(ctx, stageKey)
			if err != nil {
				return deadlineSweepPending, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier selection has no finalize cursor")
			}
			rowBytes += uint64(union.Size() + cursor.Size())
			if err := validateOpenVerifyUnionIndexScope(union, taskID); err != nil {
				return deadlineSweepPending, rowBytes, err
			}
			if err := validateOpenVerifyCursorIndexScope(cursor, taskID); err != nil {
				return deadlineSweepPending, rowBytes, err
			}
			if union.Status != types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_FINALIZING ||
				union.WindowCloseHeight != window.HandraiseCloseHeight || union.GetSelectionRandomnessHeight() != height ||
				cursor.Status != types.FinalizeCursorStatusV1_FINALIZE_CURSOR_STATUS_V1_WAITING_RANDOMNESS ||
				cursor.GetRandomnessHeight() != height {
				return deadlineSweepPending, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier selection frozen state is non-canonical")
			}
			advanced, applyBytes, err := k.finalizeVerifierAssignmentAtHeight(ctx, taskID, window, union, cursor, currentHeight)
			rowBytes += applyBytes
			if errors.Is(err, ErrVerifierLiabilityUnavailable) {
				// A selected verifier lost its live backing after the legal set
				// froze. There is no redraw, so retire this row like a short
				// window and let the verify-open deadline fail the task.
				return k.retireVerifierRowToFallbackDeadline(ctx, taskID, window, height, rowBytes)
			}
			if err != nil {
				return deadlineSweepPending, rowBytes, err
			}
			if !advanced {
				return deadlineSweepPending, rowBytes, nil
			}
			return deadlineSweepAdvanced, rowBytes, nil
		})
}

func (k Keeper) EndBlocker(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	blockHeight := sdkCtx.BlockHeight()
	if blockHeight <= 0 {
		return nil
	}
	if err := k.EnsureCurrentStoreSchema(ctx); err != nil {
		return err
	}
	currentHeight := uint64(blockHeight)
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	maxPerBlock := uint64(params.Deadlines.MaxDeadlineSweepPerBlock)
	if maxPerBlock == 0 {
		maxPerBlock = uint64(types.DefaultMaxDeadlineSweepPerBlock)
	}
	budget, err := k.hubKeeper.GetEndBlockBudget(ctx, currentHeight)
	if err != nil {
		return err
	}
	result, serializedBytes, err := k.runEndBlockDeadlineSweepsWithBudget(
		ctx, currentHeight, maxPerBlock,
		min(maxDeadlineSweepBytesPerBlockV1, budget.RemainingBytes),
		budget.RemainingItems,
	)
	if err != nil {
		return err
	}
	err = k.hubKeeper.ConsumeEndBlockBudget(ctx, currentHeight, result.Total, serializedBytes)
	return err
}

// RunEndBlockDeadlineSweeps is the fixed EndBlock schedule. `maxPerBlock` is the
// local per-queue sub-limit; the shared `max_endblock_visited_items_total` cap is
// applied on top of it. The serialized-byte limit is also shared by the complete
// schedule rather than restarted for each queue.
func (k Keeper) RunEndBlockDeadlineSweeps(ctx context.Context, currentHeight uint64, maxPerBlock uint64) (EndBlockSweepResult, error) {
	globalBudget := uint64(k.hubKeeper.GetHubParams(sdk.UnwrapSDKContext(ctx)).MaxEndblockVisitedItemsTotal)
	result, _, err := k.runEndBlockDeadlineSweepsWithBudget(ctx, currentHeight, maxPerBlock, maxDeadlineSweepBytesPerBlockV1, globalBudget)
	return result, err
}

func (k Keeper) runEndBlockDeadlineSweeps(
	ctx context.Context,
	currentHeight, maxPerBlock, maxSerializedBytes uint64,
) (EndBlockSweepResult, uint64, error) {
	globalBudget := uint64(k.hubKeeper.GetHubParams(sdk.UnwrapSDKContext(ctx)).MaxEndblockVisitedItemsTotal)
	return k.runEndBlockDeadlineSweepsWithBudget(ctx, currentHeight, maxPerBlock, maxSerializedBytes, globalBudget)
}

func (k Keeper) runEndBlockDeadlineSweepsWithBudget(
	ctx context.Context,
	currentHeight, maxPerBlock, maxSerializedBytes, globalBudget uint64,
) (EndBlockSweepResult, uint64, error) {
	var result EndBlockSweepResult
	if maxPerBlock == 0 || maxSerializedBytes == 0 || globalBudget == 0 {
		return result, 0, nil
	}
	if maxPerBlock > globalBudget {
		maxPerBlock = globalBudget
	}

	type sweepQueue struct {
		priority endBlockQueuePriority
		head     func(context.Context, uint64) (deadlineQueueHead, bool, error)
		run      func(context.Context, uint64, uint64, uint64) (deadlineSweepUsage, error)
		add      func(uint64)
	}
	recordedRun := func(
		processor func(context.Context, uint64, uint64) (uint64, error),
		minimumBytes uint64,
	) func(context.Context, uint64, uint64, uint64) (deadlineSweepUsage, error) {
		return func(ctx context.Context, height, limit, bytesLimit uint64) (deadlineSweepUsage, error) {
			recordedCtx, recorder := withDeadlineSweepUsageRecorder(ctx, bytesLimit)
			visited, err := processor(recordedCtx, height, limit)
			if !recorder.recorded {
				return deadlineSweepUsage{}, errorsmod.Wrap(types.ErrInvariantBroken, "deadline queue did not report serialized-byte usage")
			}
			if recorder.usage.visited != visited {
				return recorder.usage, errorsmod.Wrap(types.ErrInvariantBroken, "deadline queue visited accounting mismatch")
			}
			if visited != 0 && recorder.usage.serializedBytes < minimumBytes {
				recorder.usage.serializedBytes = minimumBytes
			}
			return recorder.usage, err
		}
	}
	conservativeRun := func(
		processor func(context.Context, uint64, uint64) (uint64, error),
		rowBytes uint64,
	) func(context.Context, uint64, uint64, uint64) (deadlineSweepUsage, error) {
		return func(ctx context.Context, height, limit, _ uint64) (deadlineSweepUsage, error) {
			visited, err := processor(ctx, height, limit)
			usage := deadlineSweepUsage{visited: visited}
			if visited != 0 {
				bytes, overflow := checkedMulUint64(visited, rowBytes)
				if overflow {
					return usage, errorsmod.Wrap(types.ErrInvariantBroken, "deadline queue conservative byte accounting overflow")
				}
				usage.serializedBytes = bytes
			}
			return usage, err
		}
	}
	taskHead := func(set collections.KeySet[types.DeadlineIndexKey]) func(context.Context, uint64) (deadlineQueueHead, bool, error) {
		return func(ctx context.Context, height uint64) (deadlineQueueHead, bool, error) {
			return firstDueTaskDeadline(ctx, set, height)
		}
	}
	verifyRoundHead := func(set collections.KeySet[types.VerifyRoundIndexKey]) func(context.Context, uint64) (deadlineQueueHead, bool, error) {
		return func(ctx context.Context, height uint64) (deadlineQueueHead, bool, error) {
			return firstDueVerifyRoundIndex(ctx, set, height)
		}
	}
	sessionHead := func(set collections.KeySet[types.SessionHeightIndexKey]) func(context.Context, uint64) (deadlineQueueHead, bool, error) {
		return func(ctx context.Context, height uint64) (deadlineQueueHead, bool, error) {
			iter, err := set.Iterate(ctx, nil)
			if err != nil {
				return deadlineQueueHead{}, false, err
			}
			defer iter.Close()
			if !iter.Valid() {
				return deadlineQueueHead{}, false, nil
			}
			key, err := iter.Key()
			if err != nil || key.K1() > height {
				return deadlineQueueHead{}, false, err
			}
			return deadlineQueueHead{deadline: key.K1(), primaryID: key.K2()}, true, nil
		}
	}
	queues := []sweepQueue{
		{endBlockPriorityVerifierWindowBuild, verifyRoundHead(k.VerifierWindowBuildIndex), k.processVerifierWindowBuildIndexWithBudget, func(n uint64) { result.VerifierWindowBuild += n }},
		{endBlockPriorityVerifierHandraiseClose, verifyRoundHead(k.VerifierHandraiseCloseIndex), k.processVerifierHandraiseCloseIndexWithBudget, func(n uint64) { result.VerifierHandraiseClose += n }},
		{endBlockPriorityVerifierSelectionRandomness, verifyRoundHead(k.VerifierSelectionRandomnessIndex), k.processVerifierSelectionRandomnessIndexWithBudget, func(n uint64) { result.VerifierSelectionRandomness += n }},
		{endBlockDeadlinePriority(types.DeadlineKindV1_DEADLINE_KIND_V1_WORKER_ASSIGNMENT), taskHead(k.AssignmentRandomnessIndex), recordedRun(k.processExpiredAssignmentRandomness, assignmentSweepConservativeBytesV1), func(n uint64) { result.AssignmentRandomness += n }},
		{endBlockDeadlinePriority(types.DeadlineKindV1_DEADLINE_KIND_V1_WORKER_INFER), taskHead(k.InferDeadlineIndex), recordedRun(k.processExpiredInferDeadlines, taskDeadlineConservativeBytesV1), func(n uint64) { result.InferDeadline += n }},
		{endBlockDeadlinePriority(types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_OPEN), taskHead(k.VerifyOpenDeadlineIndex), recordedRun(k.processExpiredVerifyOpenDeadlines, taskDeadlineConservativeBytesV1), func(n uint64) { result.VerifyOpenDeadline += n }},
		{endBlockDeadlinePriority(types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_COMMIT), taskHead(k.CommitDeadlineIndex), recordedRun(k.processExpiredCommitDeadlines, taskDeadlineConservativeBytesV1), func(n uint64) { result.CommitDeadline += n }},
		{endBlockDeadlinePriority(types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_REVEAL), taskHead(k.RevealDeadlineIndex), recordedRun(k.processExpiredRevealDeadlines, taskDeadlineConservativeBytesV1), func(n uint64) { result.RevealDeadline += n }},
		{endBlockDeadlinePriority(types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_FINAL), taskHead(k.VerifyDeadlineIndex), recordedRun(k.processExpiredVerifyDeadlines, taskDeadlineConservativeBytesV1), func(n uint64) { result.VerifyDeadline += n }},
		{endBlockDeadlinePriority(types.DeadlineKindV1_DEADLINE_KIND_V1_CHALLENGE_WINDOW_CLOSE), taskHead(k.ChallengeWindowCloseIndex), recordedRun(k.processExpiredChallengeWindowCloses, taskDeadlineConservativeBytesV1), func(n uint64) { result.ChallengeWindowClose += n }},
		{endBlockQueuePriority(21), verifyRoundHead(k.RoundEconomicEffectApplyIndex), k.processRoundEconomicEffectApplyIndexWithBudget, func(n uint64) { result.RoundEconomicEffect += n }},
		{endBlockDeadlinePriority(types.DeadlineKindV1_DEADLINE_KIND_V1_TASK_SETTLEMENT), taskHead(k.SettlementDeadlineIndex), recordedRun(k.processExpiredSettlementDeadlines, taskDeadlineConservativeBytesV1), func(n uint64) { result.Settlement += n }},
		{endBlockDeadlinePriority(types.DeadlineKindV1_DEADLINE_KIND_V1_EVIDENCE_CLEANUP), taskHead(k.EvidenceCleanupIndex), conservativeRun(k.processEvidenceCleanupEndBlock, evidenceCleanupConservativeBytesV1), func(n uint64) { result.EvidenceCleanup += n }},
		{endBlockPriorityEpochTaskSummary, k.firstDueEpochTaskSummary, k.processEpochTaskSummariesWithBudget, func(n uint64) { result.EpochTaskSummary += n }},
		{endBlockPriorityTaskFailurePrune, k.firstDueTaskFailurePrune, conservativeRun(k.SweepTaskFailureClassPrune, evidenceCleanupConservativeBytesV1), func(n uint64) { result.TaskFailurePrune += n }},
		{endBlockPriorityTaskSummaryPrune, taskHead(k.TaskTerminalSummaryPruneIndex), conservativeRun(k.SweepTaskTerminalSummaryPrune, evidenceCleanupConservativeBytesV1), func(n uint64) { result.TaskSummaryPrune += n }},
		{endBlockDeadlinePriority(types.DeadlineKindV1_DEADLINE_KIND_V1_SESSION_LIFECYCLE), k.firstDueSessionLifecycle, conservativeRun(k.processExpiredSessionLifecycle, sessionLifecycleConservativeBytesV1), func(n uint64) { result.SessionLifecycle += n }},
		{endBlockPrioritySessionHistoryPrune, sessionHead(k.SessionHistoryPruneIndex), conservativeRun(k.processSessionHistoryPruneEndBlock, sessionLifecycleConservativeBytesV1), func(n uint64) { result.SessionHistoryPrune += n }},
		{endBlockPrioritySessionSummaryPrune, sessionHead(k.SessionTerminalSummaryPruneIndex), conservativeRun(k.processSessionSummaryPruneEndBlock, sessionLifecycleConservativeBytesV1), func(n uint64) { result.SessionSummaryPrune += n }},
	}
	// K-BLOCK-03/04: no ACTIVE entry may create Challenge or EvidenceRequest
	// objects, so CHALLENGE_RESOLVE / CHALLENGE_CLOSE / EVIDENCE_REQUEST queues
	// are intentionally unreachable and must not be scheduled. They may be added
	// only with the future wire/state contract that closes those blockers.
	heads := make([]deadlineQueueHead, len(queues))
	ready := make([]bool, len(queues))
	blocked := make([]bool, len(queues))
	queueVisited := make([]uint64, len(queues))
	for i, queue := range queues {
		head, found, err := queue.head(ctx, currentHeight)
		if err != nil {
			return result, 0, err
		}
		heads[i], ready[i] = head, found
	}

	serializedBytes := uint64(0)
	for result.Total < globalBudget && serializedBytes < maxSerializedBytes {
		type queuedWork struct {
			height     uint64
			priority   endBlockQueuePriority
			primaryID  []byte
			queueIndex int
		}
		items := make([]queuedWork, 0, len(queues))
		for i, queue := range queues {
			if ready[i] && !blocked[i] && queueVisited[i] < maxPerBlock {
				items = append(items, queuedWork{height: heads[i].deadline, priority: queue.priority, primaryID: heads[i].primaryID, queueIndex: i})
			}
		}
		if len(items) == 0 {
			break
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].height != items[j].height {
				return items[i].height < items[j].height
			}
			if items[i].priority != items[j].priority {
				return items[i].priority < items[j].priority
			}
			// Raw-byte order over the Hash32 primary ID. Lowercase hex was
			// order-preserving, so the frozen fair-share tie-break resolves the
			// same pair of queues in the same direction as before X-16.
			return bytes.Compare(items[i].primaryID, items[j].primaryID) < 0
		})
		selected := items[0].queueIndex
		queue := queues[selected]
		previousHead := heads[selected]
		usage, err := queue.run(ctx, currentHeight, 1, maxSerializedBytes-serializedBytes)
		if err != nil {
			return result, serializedBytes, err
		}
		if usage.visited > 1 {
			return result, serializedBytes, errorsmod.Wrap(types.ErrInvariantBroken, "deadline queue exceeded its fair-share visited budget")
		}
		if usage.visited == 0 {
			break
		}
		if usage.serializedBytes == 0 {
			return result, serializedBytes, errorsmod.Wrap(types.ErrInvariantBroken, "visited deadline row reported zero serialized bytes")
		}
		nextBytes, overflow := checkedAddUint64(serializedBytes, usage.serializedBytes)
		if overflow {
			return result, serializedBytes, errorsmod.Wrap(types.ErrInvariantBroken, "endblock deadline byte accounting overflow")
		}
		serializedBytes = nextBytes
		queueVisited[selected] += usage.visited
		queue.add(usage.visited)
		result.Total += usage.visited
		head, found, err := queue.head(ctx, currentHeight)
		if err != nil {
			return result, serializedBytes, err
		}
		heads[selected], ready[selected] = head, found
		if found && sameDeadlineQueueHead(head, previousHead) {
			// A legitimate pending row stays indexed. Charge it once this block,
			// then let other frozen-order queues make progress.
			blocked[selected] = true
		}
	}

	return result, serializedBytes, nil
}

func (k Keeper) processSessionHistoryPruneEndBlock(ctx context.Context, currentHeight, limit uint64) (uint64, error) {
	result, err := k.SweepSessionHistoryPrune(ctx, currentHeight, limit)
	return result.VisitedCount, err
}

func (k Keeper) processSessionSummaryPruneEndBlock(ctx context.Context, currentHeight, limit uint64) (uint64, error) {
	result, err := k.SweepSessionTerminalSummaryPrune(ctx, currentHeight, limit)
	return result.VisitedCount, err
}

// processExpiredSessionLifecycle adapts the §10.0b1 session lifecycle executor to
// the EndBlock queue signature. It is the *same* function MsgSweepDeadline calls.
func (k Keeper) processExpiredSessionLifecycle(ctx context.Context, currentHeight uint64, maxPerBlock uint64) (uint64, error) {
	sweep, err := k.SweepExpiredSessionLifecycle(ctx, currentHeight, maxPerBlock)
	if err != nil {
		return 0, err
	}
	return sweep.SweptCount, nil
}

func (k Keeper) processEvidenceCleanupEndBlock(ctx context.Context, currentHeight, limit uint64) (uint64, error) {
	if limit == 0 {
		return 0, nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, err
	}
	if cap := uint64(params.Cleanup.MaxTaskCleanupItemsPerBlock); limit > cap {
		limit = cap
	}
	var processed uint64
	for processed < limit {
		head, found, err := firstDueTaskDeadline(ctx, k.EvidenceCleanupIndex, currentHeight)
		if err != nil || !found {
			return processed, err
		}
		taskID := types.TaskKey(head.primaryID)
		var sessionID types.SessionKey
		if core, err := k.TaskCore.Get(ctx, taskID); err == nil {
			sessionID = core.SessionId
		}
		progress, err := k.runTaskCleanup(ctx, sessionID, taskID, head.deadline, limit-processed)
		if err != nil {
			return processed, err
		}
		if progress.processed == 0 {
			break
		}
		processed, err = checkedCleanupProgress(processed, progress.processed)
		if err != nil {
			return 0, err
		}
	}
	return processed, nil
}

func checkedCleanupProgress(left, right uint64) (uint64, error) {
	value, overflow := checkedAddUint64(left, right)
	if overflow {
		return 0, errorsmod.Wrap(types.ErrInvariantBroken, "evidence cleanup visited count overflows")
	}
	return value, nil
}
