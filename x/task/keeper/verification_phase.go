package keeper

import (
	"bytes"
	"context"
	"errors"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// VerifierDeadlineFact is the deterministic per-slot input for fault reporting.
// An accepted commit with no timely result is never classified as verifier_miss.
type VerifierDeadlineFact struct {
	OperatorAddress string
	Slot            uint32
	HasCommit       bool
	HasResult       bool
	FaultType       string
}

// VerificationDeadlineFacts is the bounded result of reading one frozen
// VerifierAssignmentState and its at-most-selected-count keyed rows.
type VerificationDeadlineFacts struct {
	AcceptedCommitCount uint32
	AcceptedResultCount uint32
	VerifierFacts       []VerifierDeadlineFact
	Verdict             types.TaskVerdict
	FailureClass        types.TaskFailureClass
}

func commitDeadlineReached(currentHeight, deadlineHeight uint64) bool {
	return deadlineHeight != 0 && currentHeight >= deadlineHeight
}

var errCommitDeadlineFailureWriterUnavailable = errors.New("commit deadline failure writer is unavailable")

// StartRevealPhase is the single idempotent transition from commit collection
// to result collection. Both the all-commits and commit-deadline paths call it.
func (k Keeper) StartRevealPhase(ctx context.Context, taskKey types.TaskKey, trigger types.RevealPhaseTrigger) (bool, error) {
	return k.startRevealPhaseAtomic(ctx, taskKey, nil, trigger)
}

// startRevealPhaseFromAssignment is used by a handler that has already loaded
// the authoritative round-specific assignment.
func (k Keeper) startRevealPhaseFromAssignment(ctx context.Context, taskKey types.TaskKey, assignment types.VerifierAssignmentState, trigger types.RevealPhaseTrigger) (bool, error) {
	return k.startRevealPhaseAtomic(ctx, taskKey, &assignment, trigger)
}

func (k Keeper) startRevealPhaseAtomic(
	ctx context.Context,
	taskKey types.TaskKey,
	loadedAssignment *types.VerifierAssignmentState,
	trigger types.RevealPhaseTrigger,
) (bool, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	advanced, err := k.startRevealPhase(
		sdk.WrapSDKContext(cacheCtx), taskKey, loadedAssignment, trigger,
	)
	if errors.Is(err, errCommitDeadlineFailureWriterUnavailable) {
		// This sentinel is a deferred transition, not a failed one. The §10.5
		// availability aggregates for the round are already in the cache and must
		// survive: they are the retained evidence a later settlement spends. Only
		// the VERIFY_FAILED / REFUNDED write is missing, so commit what the
		// deadline did close and let the caller park the row.
		write()
		return false, err
	}
	if err != nil {
		return false, err
	}
	write()
	return advanced, nil
}

func (k Keeper) startRevealPhase(
	ctx context.Context,
	taskKey types.TaskKey,
	loadedAssignment *types.VerifierAssignmentState,
	trigger types.RevealPhaseTrigger,
) (bool, error) {
	if trigger != types.RevealPhaseTrigger_REVEAL_PHASE_TRIGGER_ALL_COMMITS &&
		trigger != types.RevealPhaseTrigger_REVEAL_PHASE_TRIGGER_COMMIT_DEADLINE {
		return false, errorsmod.Wrap(types.ErrInvariantBroken, "reveal phase trigger is not a registered V1 value")
	}
	core, err := k.TaskCore.Get(ctx, taskKey)
	if err != nil {
		return false, err
	}
	if len(core.TaskId) != types.Hash32Len || !bytes.Equal(core.TaskId, taskKey) {
		return false, errorsmod.Wrap(types.ErrInvariantBroken, "task core does not match the reveal transition key")
	}
	switch core.TaskPhase {
	case types.TaskPhase_TASK_PHASE_SETTLED, types.TaskPhase_TASK_PHASE_FAILED:
		return false, nil
	}

	var assignment types.VerifierAssignmentState
	if loadedAssignment != nil {
		assignment = *loadedAssignment
	} else {
		_, assignment, err = k.activeVerifierAssignment(ctx, taskKey)
		if err != nil {
			return false, err
		}
	}
	if err := validateVerifierAssignmentScope(assignment, core.TaskId, assignment.VerifyRound); err != nil {
		return false, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	if assignment.CommitDeadlineHeight == 0 || assignment.VerifyDeadlineHeight <= assignment.CommitDeadlineHeight {
		return false, errorsmod.Wrap(types.ErrInvariantBroken, "verifier assignment deadlines are incomplete")
	}

	pendingChallengeReveal := assignment.VerifyRound == types.ChallengeVerifyRoundV1 &&
		(core.VerificationStatus == types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_ASSIGNED ||
			core.VerificationStatus == types.VerificationStatus_VERIFICATION_STATUS_COMMITTING)
	if core.TaskPhase == types.TaskPhase_TASK_PHASE_SETTLING ||
		(core.TaskPhase == types.TaskPhase_TASK_PHASE_REVEALING && !pendingChallengeReveal) {
		invalidRevealDeadline := assignment.RevealDeadlineHeight == 0 || assignment.RevealDeadlineHeight > assignment.VerifyDeadlineHeight
		if assignment.VerifyRound == types.VerifyRoundV1 && assignment.RevealDeadlineHeight == assignment.VerifyDeadlineHeight {
			invalidRevealDeadline = true
		}
		if invalidRevealDeadline {
			return false, errorsmod.Wrap(types.ErrInvariantBroken, "reveal phase has no canonical deadline")
		}
		if err := removeDeadlineIndex(ctx, k.CommitDeadlineIndex, taskKey, assignment.CommitDeadlineHeight); err != nil {
			return false, err
		}
		return false, nil
	}
	if err := k.validateActiveVerifierStage(ctx, taskKey, core, assignment.VerifyRound,
		types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_ASSIGNED,
		types.VerificationStatus_VERIFICATION_STATUS_COMMITTING); err != nil {
		return false, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	if assignment.RevealDeadlineHeight != 0 {
		return false, errorsmod.Wrap(types.ErrInvariantBroken, "reveal deadline was written before the reveal phase")
	}

	height, err := currentBlockHeight(ctx)
	if err != nil {
		return false, err
	}
	facts, err := k.BuildVerificationDeadlineFacts(ctx, assignment)
	if err != nil {
		return false, err
	}
	switch trigger {
	case types.RevealPhaseTrigger_REVEAL_PHASE_TRIGGER_ALL_COMMITS:
		if facts.AcceptedCommitCount != assignment.SelectedVerifierCount {
			return false, errorsmod.Wrap(types.ErrInvariantBroken, "all-commits reveal trigger is premature")
		}
	case types.RevealPhaseTrigger_REVEAL_PHASE_TRIGGER_COMMIT_DEADLINE:
		if !commitDeadlineReached(height, assignment.CommitDeadlineHeight) {
			return false, errorsmod.Wrap(types.ErrInvalidOpenVerify, "commit deadline has not been reached")
		}
		// §10.5 closes the round's availability aggregates at the deadline itself,
		// at any accepted-commit count, and that write is deliberately inert: only
		// aggregate_hash/count/status, never a Hub fault, a Builder counter or a
		// slash. The economic effect is spent later, in §5.5's single
		// settlement/finality transaction. So it belongs ahead of the thin-round
		// sentinel below, which would otherwise silently drop the availability
		// evidence exactly on the rounds that produced the most of it.
		if _, err := k.persistDataUnavailableAggregates(ctx, core, assignment, height); err != nil {
			return false, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
		}
		unavailable, err := k.taskDataUnavailableConfirmed(ctx, taskKey, assignment)
		if err != nil {
			return false, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
		}
		if unavailable {
			forced := shared.RoundOutcomeV1_ROUND_OUTCOME_V1_UNAVAILABLE
			if assignment.VerifyRound == types.ChallengeVerifyRoundV1 {
				forced = shared.RoundOutcomeV1_ROUND_OUTCOME_V1_UNRESOLVED
			}
			_, _, err := k.closeRoundOnce(ctx, taskKey, assignment.VerifyRound,
				assignment.CommitDeadlineHeight, assignment.CommitDeadlineHeight, forced)
			return err == nil, err
		}
		threshold, err := types.ConsensusThreshold(assignment.SelectedVerifierCount)
		if err != nil {
			return false, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
		}
		if facts.AcceptedCommitCount < threshold {
			// §10.7 sends a thin round to VERIFY_FAILED / REFUNDED, never to
			// REVEALING. The sentinel is a deferral, not a dead end: the caller
			// closes the round, BuildVerificationDeadlineFacts has already stamped
			// it VERIFY_FAILED / INSUFFICIENT_VERIFIER, and MsgSettleTask performs
			// the terminal transition per ADR-0014's single settlement/finality
			// runner. What §10.7 additionally asks for inline here — a
			// TaskFailureClassState carrying classification_source = DEADLINE — is
			// deliberately not written: the settlement runner produces that row
			// under SETTLEMENT, and §10.12 gives evidence_digest exactly one
			// production point. Which side owns it is a contract question, not a
			// local one; K-BLOCK-16 is closed and is no longer the reason.
			return false, errCommitDeadlineFailureWriterUnavailable
		}
	}

	var revealDeadline uint64
	if assignment.VerifyRound == types.VerifyRoundV1 {
		params, err := k.Params.Get(ctx)
		if err != nil {
			return false, err
		}
		var overflow bool
		revealDeadline, overflow = checkedHeightAdd(height, params.Deadlines.RevealWindowBlocks)
		if overflow || revealDeadline == 0 || revealDeadline >= assignment.VerifyDeadlineHeight {
			return false, errorsmod.Wrap(types.ErrInvariantBroken, "reveal deadline is outside the frozen verify deadline")
		}
	} else {
		// Round 2 freezes its entire clock at opening. The round-close height is
		// also the reveal cutoff; Tx execution precedes the same-height EndBlock.
		revealDeadline = assignment.VerifyDeadlineHeight
		if revealDeadline == 0 || height > revealDeadline {
			return false, errorsmod.Wrap(types.ErrInvariantBroken, "challenge reveal deadline is outside the frozen round clock")
		}
	}
	assignment.RevealDeadlineHeight = revealDeadline
	advanceTaskPhase(&core, types.TaskPhase_TASK_PHASE_REVEALING)
	core.VerificationStatus = types.VerificationStatus_VERIFICATION_STATUS_REVEALING
	core.UpdatedHeight = height
	if err := k.VerifierAssignment.Set(ctx, types.NewVerifyRoundKey(taskKey, assignment.VerifyRound), assignment); err != nil {
		return false, err
	}
	if err := k.TaskCore.Set(ctx, taskKey, core); err != nil {
		return false, err
	}
	if err := addDeadlineIndex(ctx, k.RevealDeadlineIndex, taskKey, revealDeadline); err != nil {
		return false, err
	}
	if err := removeDeadlineIndex(ctx, k.CommitDeadlineIndex, taskKey, assignment.CommitDeadlineHeight); err != nil {
		return false, err
	}
	if err := emitTypedEvent(ctx, &types.EventRevealPhaseStarted{
		SessionId: append([]byte(nil), core.SessionId...), TaskId: append([]byte(nil), core.TaskId...),
		VerifyRound: assignment.VerifyRound, RevealDeadlineHeight: revealDeadline, Trigger: trigger,
	}); err != nil {
		return false, err
	}
	missingVerifierCount := assignment.SelectedVerifierCount - facts.AcceptedCommitCount
	if err := emitTypedEvent(ctx, &types.EventCommitDeadlineClosed{
		SessionId: append([]byte(nil), core.SessionId...), TaskId: append([]byte(nil), core.TaskId...),
		VerifyRound: assignment.VerifyRound, MissingVerifierCount: missingVerifierCount,
		RevealDeadlineHeight: revealDeadline,
	}); err != nil {
		return false, err
	}
	return true, nil
}

// BuildVerificationDeadlineFacts classifies each frozen selected verifier using
// direct keyed reads. It is the stable handoff to fault reporting and settlement.
func (k Keeper) BuildVerificationDeadlineFacts(ctx context.Context, assignment types.VerifierAssignmentState) (VerificationDeadlineFacts, error) {
	var out VerificationDeadlineFacts
	if err := validateVerifierAssignmentScope(assignment, assignment.TaskId, assignment.VerifyRound); err != nil {
		return out, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	if assignment.CommitDeadlineHeight == 0 {
		return out, errorsmod.Wrap(types.ErrInvariantBroken, "commit deadline is not frozen")
	}
	out.VerifierFacts = make([]VerifierDeadlineFact, 0, len(assignment.SelectedVerifiers))
	taskUnavailable, err := k.deadlineDataUnavailableExemption(ctx, assignment)
	if err != nil {
		return out, err
	}
	for _, selected := range assignment.SelectedVerifiers {
		keyBytes, err := types.DeriveCommitKey(
			sdkChainID(ctx), assignment.TaskId, assignment.VerifyRound, selected.OperatorAddress,
		)
		if err != nil {
			return out, err
		}
		key := types.NewCommitKey(keyBytes[:])
		commit, hasCommit, err := k.getCommitStateIfExists(ctx, key)
		if err != nil {
			return out, err
		}
		if hasCommit && (commit.Status != types.CommitStatusV1_COMMIT_STATUS_V1_ACCEPTED ||
			!bytes.Equal(commit.CommitKey, keyBytes[:]) || !bytes.Equal(commit.TaskId, assignment.TaskId) ||
			commit.VerifyRound != assignment.VerifyRound || commit.VerifierOperatorAddress != selected.OperatorAddress ||
			commit.CommitHeight > assignment.CommitDeadlineHeight) {
			return out, errorsmod.Wrap(types.ErrInvariantBroken, "commit row does not match its selected verifier slot")
		}
		result, hasResult, err := k.getResultReceiptStateIfExists(ctx, key)
		if err != nil {
			return out, err
		}
		if hasResult && (!hasCommit || !bytes.Equal(result.CommitKey, keyBytes[:]) || assignment.RevealDeadlineHeight == 0 ||
			result.AcceptedHeight > assignment.RevealDeadlineHeight) {
			return out, errorsmod.Wrap(types.ErrInvariantBroken, "result row does not match a timely accepted commit")
		}
		fact := VerifierDeadlineFact{OperatorAddress: selected.OperatorAddress, Slot: selected.Slot, HasCommit: hasCommit, HasResult: hasResult}
		switch {
		case !hasCommit:
			reported := false
			if taskUnavailable {
				report, found, err := k.getDataUnavailableReportIfExists(ctx,
					types.NewVerifyActorKey(types.NewTaskKey(assignment.TaskId), assignment.VerifyRound, selected.OperatorAddress))
				if err != nil {
					return out, err
				}
				reported = found && report.ReportHeight != 0 && report.ReportHeight <= assignment.CommitDeadlineHeight
			}
			if !reported {
				fact.FaultType = hubtypes.FaultTypeVerifierMiss
			}
		case !hasResult:
			fact.FaultType = hubtypes.FaultTypeCommitNoResult
		}
		if hasCommit {
			out.AcceptedCommitCount++
		}
		if hasResult {
			out.AcceptedResultCount++
		}
		out.VerifierFacts = append(out.VerifierFacts, fact)
	}
	if out.AcceptedResultCount < 2 {
		out.Verdict = types.TaskVerdict_TASK_VERDICT_VERIFY_FAILED
		out.FailureClass = types.TaskFailureClass_TASK_FAILURE_CLASS_INSUFFICIENT_VERIFIER
	}
	return out, nil
}

func (k Keeper) deadlineDataUnavailableExemption(ctx context.Context, assignment types.VerifierAssignmentState) (bool, error) {
	taskKey := types.NewTaskKey(assignment.TaskId)
	round, err := k.VerificationRound.Get(ctx, types.NewVerifyRoundKey(taskKey, assignment.VerifyRound))
	if err == nil && round.XClosedHeight != nil {
		if assignment.VerifyRound == types.VerifyRoundV1 {
			return round.GetVerdict() == types.TaskVerdict_TASK_VERDICT_VERIFY_UNAVAILABLE, nil
		}
		if round.GetRoundOutcome() != shared.RoundOutcomeV1_ROUND_OUTCOME_V1_UNRESOLVED {
			return false, nil
		}
	} else if err != nil && !errIsNotFound(err) {
		return false, err
	}
	return k.taskDataUnavailableConfirmed(ctx, taskKey, assignment)
}

func (k Keeper) allSelectedVerifierCommitsAccepted(ctx context.Context, assignment types.VerifierAssignmentState) (bool, error) {
	facts, err := k.BuildVerificationDeadlineFacts(ctx, assignment)
	if err != nil {
		return false, err
	}
	return facts.AcceptedCommitCount == assignment.SelectedVerifierCount, nil
}

func sdkChainID(ctx context.Context) string {
	return sdk.UnwrapSDKContext(ctx).ChainID()
}
