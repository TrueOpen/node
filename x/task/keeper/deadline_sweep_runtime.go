package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// CONTRACT-GAP: the interface contract requires every bounded loop to cap
// visited items and serialized bytes at the same time, but no serialized-byte
// budget is registered in TaskParamsV1. Keep this explicit consensus constant
// until the parameter contract is extended.
const maxDeadlineSweepBytesPerBlockV1 = uint64(1 << 20)

// deadlineSweepUsage is internal accounting returned by the bounded drivers.
// Public sweep helpers keep their historical visited-only signatures.
type deadlineSweepUsage struct {
	visited         uint64
	serializedBytes uint64
}

func deadlineIndexRowBytes(taskID types.TaskKey, roundScoped bool) uint64 {
	// The ordered key codec stores an 8-byte height, the fixed 32-byte task ID,
	// and, for round queues, a 4-byte round. Sixteen bytes conservatively cover
	// tuple framing around those fixed components. len(taskID) is measured rather
	// than hard-coded so this stays the real encoded width: since X-16 a task ID
	// costs 32 bytes here, not the 64 the lowercase-hex key used to.
	rowBytes := uint64(len(taskID)) + 8 + 16
	if roundScoped {
		rowBytes += 4
	}
	return rowBytes
}

func addDeadlineSweepBytes(usage *deadlineSweepUsage, rowBytes uint64) error {
	next, overflow := checkedAddUint64(usage.serializedBytes, rowBytes)
	if overflow {
		return errorsmod.Wrap(types.ErrInvariantBroken, "deadline sweep byte accounting overflow")
	}
	usage.serializedBytes = next
	return nil
}

func checkedMulUint64(left, right uint64) (uint64, bool) {
	return shared.CheckedMulUint64(left, right)
}

type deadlineSweepUsageRecorder struct {
	bytesLimit uint64
	usage      deadlineSweepUsage
	recorded   bool
}

type deadlineSweepUsageContextKey struct{}

func withDeadlineSweepUsageRecorder(ctx context.Context, bytesLimit uint64) (context.Context, *deadlineSweepUsageRecorder) {
	recorder := &deadlineSweepUsageRecorder{bytesLimit: bytesLimit}
	return context.WithValue(ctx, deadlineSweepUsageContextKey{}, recorder), recorder
}

// deadlineSweepOutcome is the only vocabulary a per-row handler may return.
//
// P1-07: the previous implementation had a *single* counter that was bumped only
// when a row actually advanced. A row that permanently returned a "recoverable"
// error therefore never charged the per-block budget, so with the EndBlock
// fair-share limit of one item the loop kept scanning the whole expired prefix
// every block for every validator at zero cost to the attacker. The three
// outcomes below all charge one visited item, and the two non-advancing ones
// additionally guarantee forward progress of the iteration key.
type deadlineSweepOutcome uint8

const (
	// deadlineSweepAdvanced: the primary state moved. The handler already
	// removed its own index row.
	deadlineSweepAdvanced deadlineSweepOutcome = iota
	// deadlineSweepStale: the index row can be proven not to describe the
	// primary any more (missing primary, deadline mismatch, already terminal).
	// the API contract / §2.2 line 375: count it and delete it.
	deadlineSweepStale
	// deadlineSweepPending: the row is legitimately not actionable yet (§4.5:
	// the beacon for the frozen randomness height is not published). The row
	// stays, but the visited budget is consumed so the scan is still bounded.
	deadlineSweepPending
)

type deadlineRowHandler func(ctx context.Context, taskID types.TaskKey, deadline uint64) (deadlineSweepOutcome, uint64, error)

// sweepSpecificTaskDeadline executes exactly one proven (deadline, task_id)
// row. MsgSweepDeadline uses this path so a locator can never advance the head
// row of the same kind for a different task.
func (k Keeper) sweepSpecificTaskDeadline(
	ctx context.Context,
	set collections.KeySet[types.DeadlineIndexKey],
	taskID types.TaskKey,
	deadline, currentHeight uint64,
	handle deadlineRowHandler,
) (visited, advanced uint64, err error) {
	if deadline > currentHeight {
		return 0, 0, nil
	}
	key := types.NewDeadlineIndexKey(deadline, taskID)
	has, err := set.Has(ctx, key)
	if err != nil || !has {
		return 0, 0, err
	}
	outcome, _, err := handle(ctx, taskID, deadline)
	if err != nil {
		return 1, 0, err
	}
	switch outcome {
	case deadlineSweepAdvanced:
		return 1, 1, nil
	case deadlineSweepStale:
		if err := removeDeadlineIndex(ctx, set, taskID, deadline); err != nil {
			return 1, 0, err
		}
		return 1, 0, nil
	case deadlineSweepPending:
		return 1, 0, nil
	default:
		return 1, 0, errorsmod.Wrap(types.ErrInvariantBroken, "unknown deadline sweep outcome")
	}
}

// sweepExpiredTaskDeadlinesWithBudget is the single bounded driver behind all
// task deadline indexes. Every visited row charges both its index key and the
// serialized primary/auxiliary bytes reported by its handler.
func (k Keeper) sweepExpiredTaskDeadlinesWithBudget(
	ctx context.Context,
	set collections.KeySet[types.DeadlineIndexKey],
	currentHeight, maxItems, maxBytes uint64,
	handle deadlineRowHandler,
) (deadlineSweepUsage, error) {
	var usage deadlineSweepUsage
	if maxItems == 0 || maxBytes == 0 {
		return usage, nil
	}

	// Rows are collected first and mutated afterwards: deleting from the same
	// KeySet the iterator walks is not safe, and the bound guarantees the slice
	// can never exceed maxItems entries. Retaining the decoded key past the
	// Next() that invalidates the iterator buffer is safe because
	// Hash32KeyCodec.Decode hands back a copy (types/key_codec.go).
	type dueRow struct {
		taskID   types.TaskKey
		deadline uint64
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
		// §7 lines 2019-2022: EndBlock processes height >= deadline_height.
		if key.K1() > currentHeight {
			break
		}
		due = append(due, dueRow{taskID: key.K2(), deadline: key.K1()})
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
		// The counter is unconditional and independent of the outcome. This is
		// the fix for P1-07 and mirrors the correct template already in
		// evidence_cleanup.go / assignment_randomness.go.
		usage.visited++
		sdkCtx := sdk.UnwrapSDKContext(ctx)
		cacheCtx, write := sdkCtx.CacheContext()
		cache := sdk.WrapSDKContext(cacheCtx)
		outcome, rowBytes, err := handle(cache, row.taskID, row.deadline)
		if err != nil {
			return usage, err
		}
		switch outcome {
		case deadlineSweepStale:
			if err := removeDeadlineIndex(cache, set, row.taskID, row.deadline); err != nil {
				return usage, err
			}
		case deadlineSweepAdvanced, deadlineSweepPending:
		default:
			return usage, errorsmod.Wrap(types.ErrInvariantBroken, "unknown deadline sweep outcome")
		}
		rowBytes, overflow := checkedAddUint64(rowBytes, deadlineIndexRowBytes(row.taskID, false))
		if overflow {
			return usage, errorsmod.Wrap(types.ErrInvariantBroken, "deadline sweep row byte accounting overflow")
		}
		if err := addDeadlineSweepBytes(&usage, rowBytes); err != nil {
			return usage, err
		}
		write()
	}
	return usage, nil
}

// sweepExpiredTaskDeadlines preserves the visited-only helper used by message
// and focused tests. EndBlock installs a recorder so nested queue processors
// also return their serialized-byte usage without changing their public shape.
func (k Keeper) sweepExpiredTaskDeadlines(
	ctx context.Context,
	set collections.KeySet[types.DeadlineIndexKey],
	currentHeight, maxItems uint64,
	handle deadlineRowHandler,
) (uint64, error) {
	bytesLimit := maxDeadlineSweepBytesPerBlockV1
	recorder, _ := ctx.Value(deadlineSweepUsageContextKey{}).(*deadlineSweepUsageRecorder)
	if recorder != nil {
		bytesLimit = recorder.bytesLimit
	}
	usage, err := k.sweepExpiredTaskDeadlinesWithBudget(ctx, set, currentHeight, maxItems, bytesLimit, handle)
	if recorder != nil {
		recorder.usage = usage
		recorder.recorded = true
	}
	return usage.visited, err
}

// loadSweepTaskCore resolves the primary of a task deadline row. A missing
// TaskCore is a provably stale index row, never a hard error.
func (k Keeper) loadSweepTaskCore(ctx context.Context, taskID types.TaskKey) (types.TaskCoreState, bool, uint64, error) {
	core, err := k.TaskCore.Get(ctx, taskID)
	if err != nil {
		if errIsNotFound(err) {
			return types.TaskCoreState{}, false, 0, nil
		}
		return types.TaskCoreState{}, false, 0, err
	}
	return core, true, uint64(core.Size()), nil
}

// ---------------------------------------------------------------------------
// 1. WORKER_INFER (InferDeadlineIndex, §10.2a)
// ---------------------------------------------------------------------------

func (k Keeper) processExpiredInferDeadlines(ctx context.Context, currentHeight, maxPerBlock uint64) (uint64, error) {
	return k.sweepExpiredTaskDeadlines(ctx, k.InferDeadlineIndex, currentHeight, maxPerBlock,
		func(ctx context.Context, taskID types.TaskKey, deadline uint64) (deadlineSweepOutcome, uint64, error) {
			return k.handleExpiredInferDeadline(ctx, taskID, deadline, currentHeight)
		})
}

func (k Keeper) handleExpiredInferDeadline(ctx context.Context, taskID types.TaskKey, deadline, currentHeight uint64) (deadlineSweepOutcome, uint64, error) {
	core, found, rowBytes, err := k.loadSweepTaskCore(ctx, taskID)
	if err != nil || !found {
		return deadlineSweepStale, rowBytes, err
	}
	if core.TaskPhase != types.TaskPhase_TASK_PHASE_WORKER_ASSIGNED ||
		core.ReceiptStatus == types.ReceiptStatus_RECEIPT_STATUS_RECEIPT_ACCEPTED {
		return deadlineSweepStale, rowBytes, nil
	}
	assignment, err := k.TaskAssignment.Get(ctx, taskID)
	if err != nil {
		if errIsNotFound(err) {
			return deadlineSweepStale, rowBytes, nil
		}
		return deadlineSweepStale, rowBytes, err
	}
	if assignment.InferDeadlineHeight != deadline {
		return deadlineSweepStale, rowBytes, nil
	}
	if hasReceipt, err := k.InferReceipt.Has(ctx, taskID); err != nil {
		return deadlineSweepStale, rowBytes, err
	} else if hasReceipt {
		return deadlineSweepStale, rowBytes, nil
	}
	core.ReceiptStatus = types.ReceiptStatus_RECEIPT_STATUS_RECEIPT_TIMEOUT
	core.AssignmentStatus = types.AssignmentStatus_ASSIGNMENT_STATUS_WORKER_TIMEOUT
	if err := k.failTaskOnDeadline(ctx, core, currentHeight,
		types.DeadlineKindV1_DEADLINE_KIND_V1_WORKER_INFER,
		types.DeadlineTransitionCode_DEADLINE_TRANSITION_CODE_WORKER_TIMEOUT,
		types.TaskFailureClass_TASK_FAILURE_CLASS_NONE); err != nil {
		return deadlineSweepAdvanced, rowBytes, err
	}
	if err := removeDeadlineIndex(ctx, k.InferDeadlineIndex, taskID, deadline); err != nil {
		return deadlineSweepAdvanced, rowBytes, err
	}
	return deadlineSweepAdvanced, rowBytes, nil
}

// ---------------------------------------------------------------------------
// 2. VERIFY_OPEN (VerifyOpenDeadlineIndex)
// ---------------------------------------------------------------------------

func (k Keeper) processExpiredVerifyOpenDeadlines(ctx context.Context, currentHeight, maxPerBlock uint64) (uint64, error) {
	return k.sweepExpiredTaskDeadlines(ctx, k.VerifyOpenDeadlineIndex, currentHeight, maxPerBlock,
		func(ctx context.Context, taskID types.TaskKey, deadline uint64) (deadlineSweepOutcome, uint64, error) {
			return k.handleExpiredVerifyOpenDeadline(ctx, taskID, deadline, currentHeight)
		})
}

// verifyOpenWindowForSweep resolves the VerifierCandidateWindow row a
// VERIFY_OPEN sweep targets, using the same round derivation
// handleExpiredVerifyOpenDeadline performs. It exists so MsgSweepDeadline can
// find assignment_deadline_height from the Task primary rather than from the
// height-first index head, which belongs to whichever task happens to be first.
func (k Keeper) verifyOpenWindowForSweep(ctx context.Context, taskID types.TaskKey) (types.VerifierCandidateWindowState, error) {
	verifyRound := uint32(types.VerifyRoundV1)
	if summary, err := k.TaskRoundSummary.Get(ctx, taskID); err == nil && summary.OpenRoundCount == 1 {
		verifyRound = types.ChallengeVerifyRoundV1
	}
	return k.VerifierCandidateWindow.Get(ctx, types.NewVerifyRoundKey(taskID, verifyRound))
}

func (k Keeper) handleExpiredVerifyOpenDeadline(ctx context.Context, taskID types.TaskKey, deadline, currentHeight uint64) (deadlineSweepOutcome, uint64, error) {
	core, found, rowBytes, err := k.loadSweepTaskCore(ctx, taskID)
	if err != nil || !found {
		return deadlineSweepStale, rowBytes, err
	}
	verifyRound := uint32(types.VerifyRoundV1)
	if summary, summaryErr := k.TaskRoundSummary.Get(ctx, taskID); summaryErr == nil && summary.OpenRoundCount == 1 {
		verifyRound = types.ChallengeVerifyRoundV1
	}
	// The verify-open deadline is the assignment deadline: it exists precisely to
	// retire a task that never got a verifier assignment. Every status that
	// expectedTaskPhaseForVerificationStatus maps to RECEIPT_COMMITTED is such a
	// task, and the receipt path walks through all three of them —
	// VERIFIER_WINDOW_PENDING at the receipt, VERIFY_COLLECTION_OPEN delta_w
	// blocks later when the window materializes, VERIFIER_SELECTION_PENDING once
	// the handraise window closes — with nothing ever writing the earlier one
	// back. Accepting only VERIFIER_WINDOW_PENDING therefore made the terminal
	// key unreachable in exactly the case it was filed for: the sweep called the
	// row stale, deleted it, and left the task in RECEIPT_COMMITTED forever with
	// its order value and every task liability still locked. VERIFIER_ASSIGNED
	// and later are genuinely stale here because a real assignment exists and
	// normal settlement owns the task from then on.
	if err := k.validateActiveVerifierStage(ctx, taskID, core, verifyRound,
		types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_WINDOW_PENDING,
		types.VerificationStatus_VERIFICATION_STATUS_VERIFY_COLLECTION_OPEN,
		types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_SELECTION_PENDING); err != nil {
		return deadlineSweepStale, rowBytes, nil
	}
	window, err := k.VerifierCandidateWindow.Get(ctx, types.NewVerifyRoundKey(taskID, verifyRound))
	if err != nil {
		return deadlineSweepPending, rowBytes, err
	}
	rowBytes, overflow := checkedAddUint64(rowBytes, uint64(window.Size()))
	if overflow {
		return deadlineSweepPending, 0, errorsmod.Wrap(types.ErrInvariantBroken, "verify-open deadline row byte count overflow")
	}
	if deadline < window.AssignmentDeadlineHeight {
		return deadlineSweepStale, rowBytes, nil
	}
	if verifyRound == types.ChallengeVerifyRoundV1 {
		if err := k.closeUnavailableChallengeRound(ctx, taskID, window.AssignmentDeadlineHeight); err != nil {
			return deadlineSweepPending, rowBytes, err
		}
		if err := removeDeadlineIndex(ctx, k.VerifyOpenDeadlineIndex, taskID, deadline); err != nil {
			return deadlineSweepPending, rowBytes, err
		}
		return deadlineSweepAdvanced, rowBytes, nil
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	core.VerificationStatus = types.VerificationStatus_VERIFICATION_STATUS_VERIFY_FAILED
	if err := k.failTaskOnDeadline(cache, core, currentHeight,
		types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_OPEN,
		types.DeadlineTransitionCode_DEADLINE_TRANSITION_CODE_VERIFY_OPEN_TIMEOUT,
		types.TaskFailureClass_TASK_FAILURE_CLASS_INSUFFICIENT_VERIFIER); err != nil {
		return deadlineSweepPending, rowBytes, err
	}
	if err := removeDeadlineIndex(cache, k.VerifyOpenDeadlineIndex, taskID, deadline); err != nil {
		return deadlineSweepPending, rowBytes, err
	}
	write()
	return deadlineSweepAdvanced, rowBytes, nil
}

// ---------------------------------------------------------------------------
// 3. VERIFY_COMMIT (CommitDeadlineIndex, §10.7)
// ---------------------------------------------------------------------------

func (k Keeper) processExpiredCommitDeadlines(ctx context.Context, currentHeight, maxPerBlock uint64) (uint64, error) {
	return k.sweepExpiredTaskDeadlines(ctx, k.CommitDeadlineIndex, currentHeight, maxPerBlock,
		func(ctx context.Context, taskID types.TaskKey, deadline uint64) (deadlineSweepOutcome, uint64, error) {
			return k.handleExpiredCommitDeadline(ctx, taskID, deadline, currentHeight)
		})
}

func (k Keeper) handleExpiredCommitDeadline(ctx context.Context, taskID types.TaskKey, deadline, currentHeight uint64) (deadlineSweepOutcome, uint64, error) {
	core, found, rowBytes, err := k.loadSweepTaskCore(ctx, taskID)
	if err != nil || !found {
		return deadlineSweepStale, rowBytes, err
	}
	verifyRound, assignment, err := k.activeVerifierAssignment(ctx, taskID)
	if err != nil {
		return deadlineSweepPending, rowBytes, err
	}
	if err := k.validateActiveVerifierStage(ctx, taskID, core, verifyRound,
		types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_ASSIGNED,
		types.VerificationStatus_VERIFICATION_STATUS_COMMITTING); err != nil {
		return deadlineSweepStale, rowBytes, nil
	}
	rowBytes, overflow := checkedAddUint64(rowBytes, uint64(assignment.Size()))
	if overflow {
		return deadlineSweepPending, 0, errorsmod.Wrap(types.ErrInvariantBroken, "commit deadline row byte count overflow")
	}
	if assignment.CommitDeadlineHeight != deadline || !commitDeadlineReached(currentHeight, assignment.CommitDeadlineHeight) {
		return deadlineSweepPending, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "commit deadline index does not match its assignment")
	}
	advanced, err := k.startRevealPhaseFromAssignment(
		ctx,
		taskID,
		assignment,
		types.RevealPhaseTrigger_REVEAL_PHASE_TRIGGER_COMMIT_DEADLINE,
	)
	if errors.Is(err, errCommitDeadlineFailureWriterUnavailable) {
		forced := shared.RoundOutcomeV1_ROUND_OUTCOME_V1_UNSPECIFIED
		if verifyRound == types.ChallengeVerifyRoundV1 {
			forced = shared.RoundOutcomeV1_ROUND_OUTCOME_V1_EXPIRED
		}
		_, _, closeErr := k.closeRoundOnce(ctx, taskID, verifyRound, deadline, deadline,
			forced)
		if closeErr != nil {
			return deadlineSweepAdvanced, rowBytes, closeErr
		}
		return deadlineSweepAdvanced, rowBytes, emitDeadlineSweptEvent(ctx, core,
			types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_COMMIT,
			types.DeadlineTransitionCode_DEADLINE_TRANSITION_CODE_COMMIT_CLOSED,
			types.TaskFailureClass_TASK_FAILURE_CLASS_UNSPECIFIED)
	}
	if err != nil {
		return deadlineSweepPending, rowBytes, err
	}
	if !advanced {
		return deadlineSweepStale, rowBytes, nil
	}
	// §5.11 code 20 fires after any sweep that actually advanced state, not only
	// after the failing ones. COMMIT_CLOSED / REVEAL_CLOSED / VERIFY_ROUND_CLOSED /
	// CHALLENGE_WINDOW_CLOSED are frozen DeadlineTransitionCode values that had no
	// producer at all, so four of the ten kinds swept silently.
	return deadlineSweepAdvanced, rowBytes, emitDeadlineSweptEvent(ctx, core,
		types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_COMMIT,
		types.DeadlineTransitionCode_DEADLINE_TRANSITION_CODE_COMMIT_CLOSED,
		types.TaskFailureClass_TASK_FAILURE_CLASS_UNSPECIFIED)
}

// ---------------------------------------------------------------------------
// 4. VERIFY_REVEAL (RevealDeadlineIndex, §10.11)
// ---------------------------------------------------------------------------

func (k Keeper) processExpiredRevealDeadlines(ctx context.Context, currentHeight, maxPerBlock uint64) (uint64, error) {
	return k.sweepExpiredTaskDeadlines(ctx, k.RevealDeadlineIndex, currentHeight, maxPerBlock,
		func(ctx context.Context, taskID types.TaskKey, deadline uint64) (deadlineSweepOutcome, uint64, error) {
			return k.handleExpiredRevealDeadline(ctx, taskID, deadline, currentHeight)
		})
}

func (k Keeper) handleExpiredRevealDeadline(ctx context.Context, taskID types.TaskKey, deadline, currentHeight uint64) (deadlineSweepOutcome, uint64, error) {
	core, found, rowBytes, err := k.loadSweepTaskCore(ctx, taskID)
	if err != nil || !found {
		return deadlineSweepStale, rowBytes, err
	}
	if core.TaskPhase != types.TaskPhase_TASK_PHASE_REVEALING {
		return deadlineSweepStale, rowBytes, nil
	}
	_, assignment, err := k.activeVerifierAssignment(ctx, taskID)
	if err != nil {
		return deadlineSweepPending, rowBytes, err
	}
	rowBytes, overflow := checkedAddUint64(rowBytes, uint64(assignment.Size()))
	if overflow {
		return deadlineSweepPending, 0, errorsmod.Wrap(types.ErrInvariantBroken, "reveal deadline row byte count overflow")
	}
	if assignment.RevealDeadlineHeight != deadline || assignment.VerifyDeadlineHeight < deadline {
		return deadlineSweepPending, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "reveal deadline index does not match its assignment")
	}
	if err := k.advanceToVerifyDeadline(ctx, k.RevealDeadlineIndex, taskID, deadline, assignment.VerifyDeadlineHeight); err != nil {
		return deadlineSweepPending, rowBytes, err
	}
	return deadlineSweepAdvanced, rowBytes, emitDeadlineSweptEvent(ctx, core,
		types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_REVEAL,
		types.DeadlineTransitionCode_DEADLINE_TRANSITION_CODE_REVEAL_CLOSED,
		types.TaskFailureClass_TASK_FAILURE_CLASS_UNSPECIFIED)
}

// ---------------------------------------------------------------------------
// 5. VERIFY_FINAL (VerifyDeadlineIndex, §10.13)
// ---------------------------------------------------------------------------

func (k Keeper) processExpiredVerifyDeadlines(ctx context.Context, currentHeight, maxPerBlock uint64) (uint64, error) {
	return k.sweepExpiredTaskDeadlines(ctx, k.VerifyDeadlineIndex, currentHeight, maxPerBlock,
		func(ctx context.Context, taskID types.TaskKey, deadline uint64) (deadlineSweepOutcome, uint64, error) {
			return k.handleExpiredVerifyDeadline(ctx, taskID, deadline, currentHeight)
		})
}

func (k Keeper) handleExpiredVerifyDeadline(ctx context.Context, taskID types.TaskKey, deadline, currentHeight uint64) (deadlineSweepOutcome, uint64, error) {
	core, found, rowBytes, err := k.loadSweepTaskCore(ctx, taskID)
	if err != nil || !found {
		return deadlineSweepStale, rowBytes, err
	}
	switch core.TaskPhase {
	case types.TaskPhase_TASK_PHASE_VERIFIER_ASSIGNED,
		types.TaskPhase_TASK_PHASE_COMMITTING,
		types.TaskPhase_TASK_PHASE_REVEALING:
	default:
		return deadlineSweepStale, rowBytes, nil
	}
	// Hash32KeyCodec is fail-closed on width, so a key that came out of the index
	// is already 32 bytes; the check stays because executeTaskSettlement takes the
	// raw ID and this is the last point that can still name the queue it came from.
	if len(taskID) != types.Hash32Len {
		return deadlineSweepStale, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verify deadline task key is invalid")
	}
	verifyRound, assignment, err := k.activeVerifierAssignment(ctx, taskID)
	if err != nil {
		return deadlineSweepPending, rowBytes, err
	}
	rowBytes, overflow := checkedAddUint64(rowBytes, uint64(assignment.Size()))
	if overflow {
		return deadlineSweepPending, 0, errorsmod.Wrap(types.ErrInvariantBroken, "verify deadline row byte count overflow")
	}
	if deadline != assignment.VerifyDeadlineHeight {
		return deadlineSweepStale, rowBytes, nil
	}
	forced := shared.RoundOutcomeV1_ROUND_OUTCOME_V1_UNSPECIFIED
	if verifyRound == types.ChallengeVerifyRoundV1 {
		forced = shared.RoundOutcomeV1_ROUND_OUTCOME_V1_EXPIRED
	}
	if _, _, err = k.closeRoundOnce(ctx, taskID, verifyRound, deadline, deadline, forced); err != nil {
		return deadlineSweepAdvanced, rowBytes, err
	}
	return deadlineSweepAdvanced, rowBytes, emitDeadlineSweptEvent(ctx, core,
		types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_FINAL,
		types.DeadlineTransitionCode_DEADLINE_TRANSITION_CODE_VERIFY_ROUND_CLOSED,
		types.TaskFailureClass_TASK_FAILURE_CLASS_UNSPECIFIED)
}

func (k Keeper) advanceToVerifyDeadline(
	ctx context.Context,
	source collections.KeySet[types.DeadlineIndexKey],
	taskID types.TaskKey,
	sourceHeight, verifyHeight uint64,
) error {
	if verifyHeight <= sourceHeight {
		return errorsmod.Wrap(types.ErrInvariantBroken, "verify deadline does not follow the consumed phase deadline")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	if err := removeDeadlineIndex(cache, source, taskID, sourceHeight); err != nil {
		return err
	}
	if err := addDeadlineIndex(cache, k.VerifyDeadlineIndex, taskID, verifyHeight); err != nil {
		return err
	}
	write()
	return nil
}

func (k Keeper) processExpiredChallengeWindowCloses(ctx context.Context, currentHeight, maxPerBlock uint64) (uint64, error) {
	return k.sweepExpiredTaskDeadlines(ctx, k.ChallengeWindowCloseIndex, currentHeight, maxPerBlock,
		func(ctx context.Context, taskID types.TaskKey, deadline uint64) (deadlineSweepOutcome, uint64, error) {
			// The core is read first, before anything is written, so a task whose
			// primary is already gone retires the index row instead of aborting the
			// block after the round has been finalized. emitDeadlineSweptEvent only
			// reads session_id/task_id, neither of which finalizeTaskRounds moves.
			core, found, rowBytes, err := k.loadSweepTaskCore(ctx, taskID)
			if err != nil || !found {
				return deadlineSweepStale, rowBytes, err
			}
			summary, err := k.TaskRoundSummary.Get(ctx, taskID)
			if err != nil {
				if errIsNotFound(err) {
					return deadlineSweepStale, rowBytes, nil
				}
				return deadlineSweepPending, rowBytes, err
			}
			rowBytes += uint64(summary.Size())
			if deadline != summary.GetChallengeCloseHeight() {
				return deadlineSweepPending, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "challenge close index disagrees with summary")
			}
			if summary.OpenRoundCount != 0 || summary.MaxClosedRound != types.VerifyRoundV1 {
				return deadlineSweepStale, rowBytes, nil
			}
			if err := k.finalizeTaskRounds(ctx, taskID); err != nil {
				return deadlineSweepPending, rowBytes, err
			}
			if err := removeDeadlineIndex(ctx, k.ChallengeWindowCloseIndex, taskID, deadline); err != nil {
				return deadlineSweepPending, rowBytes, err
			}
			return deadlineSweepAdvanced, rowBytes, emitDeadlineSweptEvent(ctx, core,
				types.DeadlineKindV1_DEADLINE_KIND_V1_CHALLENGE_WINDOW_CLOSE,
				types.DeadlineTransitionCode_DEADLINE_TRANSITION_CODE_CHALLENGE_WINDOW_CLOSED,
				types.TaskFailureClass_TASK_FAILURE_CLASS_UNSPECIFIED)
		})
}

func (k Keeper) processExpiredSettlementDeadlines(ctx context.Context, currentHeight, maxPerBlock uint64) (uint64, error) {
	return k.sweepExpiredTaskDeadlines(ctx, k.SettlementDeadlineIndex, currentHeight, maxPerBlock,
		func(ctx context.Context, taskID types.TaskKey, deadline uint64) (deadlineSweepOutcome, uint64, error) {
			advanced, err := k.settleTaskFromDeadline(ctx, taskID, deadline, currentHeight)
			if err != nil {
				return deadlineSweepPending, 0, err
			}
			if advanced {
				if err := removeDeadlineIndex(ctx, k.SettlementDeadlineIndex, taskID, deadline); err != nil {
					return deadlineSweepPending, 0, err
				}
				return deadlineSweepAdvanced, taskDeadlineConservativeBytesV1, nil
			}
			return deadlineSweepStale, taskDeadlineConservativeBytesV1, nil
		})
}

// ---------------------------------------------------------------------------
// shared terminal path
// ---------------------------------------------------------------------------

// failTaskOnDeadline is the deterministic pre-verification failure path. Once a
// verifier assignment exists, verify deadline closure uses normal settlement.
func (k Keeper) failTaskOnDeadline(
	ctx context.Context,
	core types.TaskCoreState,
	currentHeight uint64,
	kind types.DeadlineKindV1,
	transition types.DeadlineTransitionCode,
	failureClass types.TaskFailureClass,
) error {
	result, err := k.writePreVerificationFailure(ctx, core, currentHeight, kind, failureClass)
	if err != nil {
		return err
	}
	if !result.Applied {
		return nil
	}
	return emitDeadlineSweptEvent(ctx, result.Core, kind, transition, failureClass)
}

func emitDeadlineSweptEvent(
	ctx context.Context,
	core types.TaskCoreState,
	kind types.DeadlineKindV1,
	transition types.DeadlineTransitionCode,
	failureClass types.TaskFailureClass,
) error {
	event := &types.EventDeadlineSwept{
		DeadlineKind:   kind,
		TransitionCode: transition,
		FailureClass:   failureClass,
	}
	if len(core.SessionId) == types.Hash32Len {
		event.XSessionId = &types.EventDeadlineSwept_SessionId{SessionId: append([]byte(nil), core.SessionId...)}
	}
	if len(core.TaskId) == types.Hash32Len {
		event.XTaskId = &types.EventDeadlineSwept_TaskId{TaskId: append([]byte(nil), core.TaskId...)}
	}
	return emitTypedEvent(ctx, event)
}

func emitSessionDeadlineSweptEvent(
	ctx context.Context,
	sessionID []byte,
	transition types.DeadlineTransitionCode,
) error {
	event := &types.EventDeadlineSwept{
		DeadlineKind:   types.DeadlineKindV1_DEADLINE_KIND_V1_SESSION_LIFECYCLE,
		TransitionCode: transition,
		FailureClass:   types.TaskFailureClass_TASK_FAILURE_CLASS_NONE,
	}
	if len(sessionID) == types.Hash32Len {
		event.XSessionId = &types.EventDeadlineSwept_SessionId{SessionId: append([]byte(nil), sessionID...)}
	}
	return emitTypedEvent(ctx, event)
}
