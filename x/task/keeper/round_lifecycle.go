package keeper

import (
	"context"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// closeRoundOnce is the only writer that turns an open round header into a
// terminal round. Callers run it in their own cache transaction.
func (k Keeper) closeRoundOnce(
	ctx context.Context,
	taskKey types.TaskKey,
	verifyRound uint32,
	cutoffHeight uint64,
	closedHeight uint64,
	forcedOutcome shared.RoundOutcomeV1,
) (types.VerificationRoundState, bool, error) {
	roundKey := types.NewVerifyRoundKey(taskKey, verifyRound)
	existing, err := k.VerificationRound.Get(ctx, roundKey)
	if err != nil {
		return types.VerificationRoundState{}, false, err
	}
	if existing.XClosedHeight != nil {
		return existing, false, nil
	}
	closed, result, err := k.closeVerificationRound(ctx, taskKey, verifyRound, cutoffHeight, closedHeight)
	if err != nil {
		return types.VerificationRoundState{}, false, err
	}
	if verifyRound == types.VerifyRoundV1 && forcedOutcome == shared.RoundOutcomeV1_ROUND_OUTCOME_V1_UNAVAILABLE {
		result.Verdict = types.TaskVerdict_TASK_VERDICT_VERIFY_UNAVAILABLE
		result.FailureClass = types.TaskFailureClass_TASK_FAILURE_CLASS_INSUFFICIENT_VERIFIER
		closed.XVerdict = &types.VerificationRoundState_Verdict{Verdict: result.Verdict}
		closed.XFailureClass = &types.VerificationRoundState_FailureClass{FailureClass: result.FailureClass}
		factsHash, err := types.VerifyRoundFactsHash(sdk.UnwrapSDKContext(ctx).ChainID(), closed)
		if err != nil {
			return types.VerificationRoundState{}, false, err
		}
		closed.XRoundFactsHash = &types.VerificationRoundState_RoundFactsHash{RoundFactsHash: factsHash[:]}
		result.RoundFactsHash = factsHash[:]
	}

	var outcome shared.RoundOutcomeV1
	if verifyRound == types.ChallengeVerifyRoundV1 {
		round1, err := k.VerificationRound.Get(ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1))
		if err != nil || round1.XClosedHeight == nil || !isExplicitRoundVerdict(round1.GetVerdict()) {
			return types.VerificationRoundState{}, false, errorsmod.Wrap(types.ErrInvariantBroken, "round 1 terminal verdict is unavailable")
		}
		if isExplicitRoundVerdict(result.Verdict) {
			if result.Verdict == round1.GetVerdict() {
				outcome = shared.RoundOutcomeV1_ROUND_OUTCOME_V1_CONCURRED
			} else {
				outcome = shared.RoundOutcomeV1_ROUND_OUTCOME_V1_DIVERGED
			}
		} else if forcedOutcome == shared.RoundOutcomeV1_ROUND_OUTCOME_V1_UNRESOLVED ||
			result.Verdict == types.TaskVerdict_TASK_VERDICT_NO_CONSENSUS {
			outcome = shared.RoundOutcomeV1_ROUND_OUTCOME_V1_UNRESOLVED
		} else if forcedOutcome != shared.RoundOutcomeV1_ROUND_OUTCOME_V1_UNSPECIFIED {
			outcome = forcedOutcome
		} else if result.ValidSampleCount == 0 {
			outcome = shared.RoundOutcomeV1_ROUND_OUTCOME_V1_EXPIRED
		} else {
			outcome = shared.RoundOutcomeV1_ROUND_OUTCOME_V1_UNRESOLVED
		}
		closed.XRoundOutcome = &types.VerificationRoundState_RoundOutcome{RoundOutcome: outcome}
	}

	if err := k.VerificationRound.Set(ctx, roundKey, closed); err != nil {
		return types.VerificationRoundState{}, false, err
	}
	if err := k.removeRoundDeadlineIndexes(ctx, taskKey, verifyRound); err != nil {
		return types.VerificationRoundState{}, false, err
	}
	if err := k.removeVerifierActiveJobs(ctx, taskKey, verifyRound); err != nil {
		return types.VerificationRoundState{}, false, err
	}
	if verifyRound == types.VerifyRoundV1 {
		if err := k.closeRoundOneSummary(ctx, taskKey, closed, result); err != nil {
			return types.VerificationRoundState{}, false, err
		}
	} else {
		if err := k.closeRoundTwoSummaryAndEffects(ctx, taskKey, closed, result, outcome, closedHeight); err != nil {
			return types.VerificationRoundState{}, false, err
		}
	}
	return closed, true, nil
}

func (k Keeper) closeUnavailableChallengeRound(
	ctx context.Context,
	taskKey types.TaskKey,
	closedHeight uint64,
) error {
	key := types.NewVerifyRoundKey(taskKey, types.ChallengeVerifyRoundV1)
	round, err := k.VerificationRound.Get(ctx, key)
	if err != nil {
		return err
	}
	if round.XClosedHeight != nil {
		return nil
	}
	round.XClosedHeight = &types.VerificationRoundState_ClosedHeight{ClosedHeight: closedHeight}
	round.XResultReceiptRefsHash = &types.VerificationRoundState_ResultReceiptRefsHash{ResultReceiptRefsHash: make([]byte, types.Hash32Len)}
	round.XConsensusClusterHash = &types.VerificationRoundState_ConsensusClusterHash{ConsensusClusterHash: make([]byte, types.Hash32Len)}
	round.XVerdict = &types.VerificationRoundState_Verdict{Verdict: types.TaskVerdict_TASK_VERDICT_VERIFY_UNAVAILABLE}
	round.XFailureClass = &types.VerificationRoundState_FailureClass{FailureClass: types.TaskFailureClass_TASK_FAILURE_CLASS_INSUFFICIENT_VERIFIER}
	round.XRoundOutcome = &types.VerificationRoundState_RoundOutcome{RoundOutcome: shared.RoundOutcomeV1_ROUND_OUTCOME_V1_UNAVAILABLE}
	factsHash, err := types.VerifyRoundFactsHash(sdk.UnwrapSDKContext(ctx).ChainID(), round)
	if err != nil {
		return err
	}
	round.XRoundFactsHash = &types.VerificationRoundState_RoundFactsHash{RoundFactsHash: factsHash[:]}
	if err := k.VerificationRound.Set(ctx, key, round); err != nil {
		return err
	}
	result := roundCloseResult{
		Verdict:               types.TaskVerdict_TASK_VERDICT_VERIFY_UNAVAILABLE,
		FailureClass:          types.TaskFailureClass_TASK_FAILURE_CLASS_INSUFFICIENT_VERIFIER,
		ResultReceiptRefsHash: make([]byte, types.Hash32Len), ConsensusClusterHash: make([]byte, types.Hash32Len),
		RoundFactsHash: factsHash[:],
	}
	if err := k.closeRoundTwoSummaryAndEffects(ctx, taskKey, round, result,
		shared.RoundOutcomeV1_ROUND_OUTCOME_V1_UNAVAILABLE, closedHeight); err != nil {
		return err
	}
	return k.updateCoreAfterRoundClose(ctx, taskKey, result.Verdict)
}

func (k Keeper) removeRoundDeadlineIndexes(ctx context.Context, taskKey types.TaskKey, verifyRound uint32) error {
	assignment, err := k.VerifierAssignment.Get(ctx, types.NewVerifyRoundKey(taskKey, verifyRound))
	if err != nil {
		return err
	}
	for _, item := range []struct {
		set    collections.KeySet[types.DeadlineIndexKey]
		height uint64
	}{
		{k.CommitDeadlineIndex, assignment.CommitDeadlineHeight},
		{k.RevealDeadlineIndex, assignment.RevealDeadlineHeight},
		{k.VerifyDeadlineIndex, assignment.VerifyDeadlineHeight},
	} {
		if item.height == 0 {
			continue
		}
		if err := removeDeadlineIndex(ctx, item.set, taskKey, item.height); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) removeVerifierActiveJobs(ctx context.Context, taskKey types.TaskKey, verifyRound uint32) error {
	assignment, err := k.VerifierAssignment.Get(ctx, types.NewVerifyRoundKey(taskKey, verifyRound))
	if err != nil {
		return err
	}
	for _, selected := range assignment.SelectedVerifiers {
		if err := removeRoleActiveTaskKey(ctx, k.VerifierActiveJobIndex,
			types.NewVerifierActiveJobKey(selected.OperatorAddress, taskKey)); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) closeRoundOneSummary(
	ctx context.Context,
	taskKey types.TaskKey,
	round types.VerificationRoundState,
	result roundCloseResult,
) error {
	if _, err := k.TaskRoundSummary.Get(ctx, taskKey); err == nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, "round summary exists before round 1 closes")
	} else if !errIsNotFound(err) {
		return err
	}
	summary := types.TaskRoundSummaryState{
		TaskId: append([]byte(nil), round.TaskId...), MaxClosedRound: types.VerifyRoundV1,
		Round1FactsHashOrZero32:  append([]byte(nil), result.RoundFactsHash...),
		Round2FactsHashOrZero32:  make([]byte, types.Hash32Len),
		Round2EffectRootOrZero32: make([]byte, types.Hash32Len),
	}
	if isExplicitRoundVerdict(result.Verdict) {
		assignment, err := k.TaskAssignment.Get(ctx, taskKey)
		if err != nil || assignment.ChallengeOpenWindowBlocksSnapshot == 0 {
			return errorsmod.Wrap(types.ErrInvariantBroken, "challenge window snapshot is unavailable")
		}
		challengeClose, overflow := checkedHeightAdd(round.GetClosedHeight(), assignment.ChallengeOpenWindowBlocksSnapshot)
		if overflow {
			return errorsmod.Wrap(types.ErrInvariantBroken, "challenge close height overflows")
		}
		summary.XChallengeOpenHeight = &types.TaskRoundSummaryState_ChallengeOpenHeight{ChallengeOpenHeight: round.GetClosedHeight()}
		summary.XChallengeCloseHeight = &types.TaskRoundSummaryState_ChallengeCloseHeight{ChallengeCloseHeight: challengeClose}
		if err := addDeadlineIndex(ctx, k.ChallengeWindowCloseIndex, taskKey, challengeClose); err != nil {
			return err
		}
	}
	if err := k.TaskRoundSummary.Set(ctx, taskKey, summary); err != nil {
		return err
	}
	if err := emitTypedEvent(ctx, &types.EventVerificationRoundClosed{
		TaskId: append([]byte(nil), round.TaskId...), VerifyRound: types.VerifyRoundV1,
		Verdict: round.GetVerdict(), RoundFactsHash: append([]byte(nil), round.GetRoundFactsHash()...),
		RoundEffectRoot: make([]byte, types.Hash32Len), ClosedHeight: round.GetClosedHeight(),
	}); err != nil {
		return err
	}
	if !isExplicitRoundVerdict(result.Verdict) {
		// A non-explicit round-1 verdict opens no challenge window, so the task goes
		// straight to finalization — but the core status still has to move. Skipping
		// it left verification_status parked on COMMITTING/REVEALING for a round that
		// is already closed, which is exactly the "already FINAL but still pending"
		// shape §16.1
		// forbids a Query from showing. The mapping is the same one the settlement
		// path applies a moment later, so this only moves the write earlier.
		if err := k.updateCoreAfterRoundClose(ctx, taskKey, result.Verdict); err != nil {
			return err
		}
		return k.finalizeTaskRounds(ctx, taskKey)
	}
	return k.updateCoreAfterRoundClose(ctx, taskKey, result.Verdict)
}

func (k Keeper) closeRoundTwoSummaryAndEffects(
	ctx context.Context,
	taskKey types.TaskKey,
	round types.VerificationRoundState,
	result roundCloseResult,
	outcome shared.RoundOutcomeV1,
	height uint64,
) error {
	summary, err := k.TaskRoundSummary.Get(ctx, taskKey)
	if err != nil || summary.MaxClosedRound != types.VerifyRoundV1 || summary.OpenRoundCount != 1 {
		return errorsmod.Wrap(types.ErrInvariantBroken, "challenge round summary is unavailable")
	}
	summary.MaxClosedRound = types.ChallengeVerifyRoundV1
	summary.OpenRoundCount = 0
	summary.Round2FactsHashOrZero32 = append([]byte(nil), result.RoundFactsHash...)
	summary.XRound2Outcome = &types.TaskRoundSummaryState_Round2Outcome{Round2Outcome: outcome}
	if err := k.TaskRoundSummary.Set(ctx, taskKey, summary); err != nil {
		return err
	}
	core, err := k.TaskCore.Get(ctx, taskKey)
	if err != nil {
		return err
	}
	if outcome == shared.RoundOutcomeV1_ROUND_OUTCOME_V1_DIVERGED {
		if _, err := k.freezeRoundDivergenceClassification(ctx, taskKey, core, round); err != nil {
			return err
		}
	}
	if err := k.freezeRoundEconomicEffects(ctx, taskKey, round, result, outcome, height); err != nil {
		return err
	}
	return k.updateCoreAfterRoundClose(ctx, taskKey, result.Verdict)
}

func (k Keeper) freezeRoundDivergenceClassification(
	ctx context.Context,
	taskKey types.TaskKey,
	core types.TaskCoreState,
	round types.VerificationRoundState,
) ([]byte, error) {
	assignment, err := k.VerifierAssignment.Get(ctx, types.NewVerifyRoundKey(taskKey, round.VerifyRound))
	if err != nil {
		return nil, err
	}
	facts, err := k.BuildVerificationDeadlineFacts(ctx, assignment)
	if err != nil {
		return nil, err
	}
	infer, err := k.InferReceipt.Get(ctx, taskKey)
	if err != nil {
		return nil, err
	}
	failureClass := round.GetFailureClass()
	if round.GetPrevRoundVerdict() == types.TaskVerdict_TASK_VERDICT_PASS && round.GetVerdict() == types.TaskVerdict_TASK_VERDICT_FAIL {
		failureClass = types.TaskFailureClass_TASK_FAILURE_CLASS_WORKER_EVIDENCE_FAULT
	}
	digest, err := classificationEvidenceDigest(
		sdk.UnwrapSDKContext(ctx).ChainID(), core.TaskId, round.VerifyRound, failureClass,
		shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_VERIFICATION_ROUND,
		round.GetClosedHeight(), infer.InferReceiptHash, make([]byte, types.Hash32Len), make([]byte, types.Hash32Len),
		0, facts.AcceptedCommitCount, facts.AcceptedResultCount,
	)
	if err != nil {
		return nil, err
	}
	freezeEligible, known := types.FreezeSignalEligibilityForTaskFailureClass(failureClass)
	if !known {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "round divergence failure class is not registered")
	}
	row := types.TaskFailureClassState{
		TaskId: append([]byte(nil), core.TaskId...), VerifyRound: round.VerifyRound,
		ModelId: core.ModelId, ProfileVersion: core.ProfileVersion, FailureClass: failureClass,
		FreezeSignalEligible: freezeEligible,
		ClassificationSource: shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_VERIFICATION_ROUND,
		ClassifiedHeight:     round.GetClosedHeight(), InferReceiptHashOrZero32: append([]byte(nil), infer.InferReceiptHash...),
		SettlementFactsHashOrZero32: make([]byte, types.Hash32Len), SettlementIdOrZero32: make([]byte, types.Hash32Len),
		AcceptedCommitCount: facts.AcceptedCommitCount, AcceptedResultReceiptCount: facts.AcceptedResultCount,
		EvidenceDigest: digest,
	}
	if err := k.TaskFailureClass.Set(ctx, types.NewVerifyRoundKey(taskKey, round.VerifyRound), row); err != nil {
		return nil, err
	}
	if err := emitTypedEvent(ctx, &types.EventTaskFailureClassUpdated{
		SessionId: append([]byte(nil), core.SessionId...), TaskId: append([]byte(nil), core.TaskId...),
		VerifyRound: round.VerifyRound, FailureClass: failureClass,
		ClassificationSource: row.ClassificationSource, EvidenceDigest: append([]byte(nil), digest...),
		FreezeSignalEligible: freezeEligible,
	}); err != nil {
		return nil, err
	}
	return digest, nil
}

func (k Keeper) updateCoreAfterRoundClose(ctx context.Context, taskKey types.TaskKey, verdict types.TaskVerdict) error {
	core, err := k.TaskCore.Get(ctx, taskKey)
	if err != nil {
		return err
	}
	switch verdict {
	case types.TaskVerdict_TASK_VERDICT_PASS:
		core.VerificationStatus = types.VerificationStatus_VERIFICATION_STATUS_VERIFICATION_PASSED
	case types.TaskVerdict_TASK_VERDICT_NO_CONSENSUS:
		core.VerificationStatus = types.VerificationStatus_VERIFICATION_STATUS_NO_CONSENSUS
	default:
		core.VerificationStatus = types.VerificationStatus_VERIFICATION_STATUS_VERIFY_FAILED
	}
	return k.TaskCore.Set(ctx, taskKey, core)
}

// finalizeTaskRounds freezes the sole Task-level round commitment and schedules
// settlement. The protocol height is derived from the summary, never from the
// block in which a bounded runner reaches this helper.
func (k Keeper) finalizeTaskRounds(ctx context.Context, taskKey types.TaskKey) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	summary, err := k.TaskRoundSummary.Get(ctx, taskKey)
	if err != nil {
		return err
	}
	if summary.XRoundsClosedHeight != nil {
		return nil
	}
	if summary.OpenRoundCount != 0 || summary.MaxClosedRound == 0 {
		return errorsmod.Wrap(types.ErrInvalidTaskStatus, "task still has an open round")
	}
	var effectiveRound uint32
	var roundsClosedHeight uint64
	if summary.MaxClosedRound == types.VerifyRoundV1 {
		round1, err := k.VerificationRound.Get(ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1))
		if err != nil || round1.XClosedHeight == nil {
			return errorsmod.Wrap(types.ErrInvariantBroken, "closed round 1 is unavailable")
		}
		effectiveRound = types.VerifyRoundV1
		if summary.XChallengeCloseHeight != nil {
			roundsClosedHeight = summary.GetChallengeCloseHeight()
		} else {
			roundsClosedHeight = round1.GetClosedHeight()
		}
	} else if summary.MaxClosedRound == types.ChallengeVerifyRoundV1 {
		round2, err := k.VerificationRound.Get(ctx, types.NewVerifyRoundKey(taskKey, types.ChallengeVerifyRoundV1))
		if err != nil || round2.XClosedHeight == nil || len(round2.GetRoundEffectRoot()) != types.Hash32Len {
			return errorsmod.Wrap(types.ErrInvalidTaskStatus, "challenge round effects are not closed")
		}
		if _, err := k.RoundEconomicEffectApplyCursor.Get(ctx, types.NewVerifyRoundKey(taskKey, types.ChallengeVerifyRoundV1)); err == nil {
			return errorsmod.Wrap(types.ErrInvalidTaskStatus, "challenge round effect cursor is still active")
		} else if !errIsNotFound(err) {
			return err
		}
		effectiveRound = types.VerifyRoundV1
		if summary.GetRound2Outcome() == shared.RoundOutcomeV1_ROUND_OUTCOME_V1_DIVERGED {
			effectiveRound = types.ChallengeVerifyRoundV1
		}
		roundsClosedHeight = round2.GetClosedHeight()
	} else {
		return errorsmod.Wrap(types.ErrInvariantBroken, "max_closed_round is invalid")
	}
	if roundsClosedHeight == 0 {
		return errorsmod.Wrap(types.ErrInvariantBroken, "rounds_closed_height is zero")
	}
	summary.EffectiveVerifyRound = effectiveRound
	summary.XRoundsClosedHeight = &types.TaskRoundSummaryState_RoundsClosedHeight{RoundsClosedHeight: roundsClosedHeight}
	summary.XSettlementFactsCutoffHeight = &types.TaskRoundSummaryState_SettlementFactsCutoffHeight{SettlementFactsCutoffHeight: roundsClosedHeight}
	hash, err := types.TaskRoundSummaryHash(sdkCtx.ChainID(), summary)
	if err != nil {
		return err
	}
	summary.XTaskRoundSummaryHash = &types.TaskRoundSummaryState_TaskRoundSummaryHash{TaskRoundSummaryHash: hash[:]}
	if err := k.TaskRoundSummary.Set(ctx, taskKey, summary); err != nil {
		return err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	settlementDeadline, err := settlementDeadlineHeight(roundsClosedHeight, params)
	if err != nil {
		return err
	}
	if err := addDeadlineIndex(ctx, k.SettlementDeadlineIndex, taskKey, settlementDeadline); err != nil {
		return err
	}
	core, err := k.TaskCore.Get(ctx, taskKey)
	if err != nil {
		return err
	}
	core.TaskPhase = types.TaskPhase_TASK_PHASE_SETTLING
	core.EffectiveVerifyRound = effectiveRound
	core.UpdatedHeight = roundsClosedHeight
	return k.TaskCore.Set(ctx, taskKey, core)
}

func settlementDeadlineHeight(roundsClosedHeight uint64, params types.TaskParamsV1) (uint64, error) {
	deadline, overflow := checkedHeightAdd(roundsClosedHeight, params.Deadlines.SettleMarginBlocks)
	if overflow || deadline == 0 || deadline <= roundsClosedHeight {
		return 0, errorsmod.Wrap(types.ErrInvariantBroken, "settlement deadline is invalid")
	}
	return deadline, nil
}

func (k Keeper) taskDataUnavailableConfirmed(
	ctx context.Context,
	taskKey types.TaskKey,
	assignment types.VerifierAssignmentState,
) (bool, error) {
	selection, err := k.loadActiveTaskBuilderSelection(ctx, taskKey, assignment.TaskId)
	if err != nil {
		return false, err
	}
	required := make([]bool, len(selection.SelectedTaskBuilders))
	if assignment.VerifyRound == types.ChallengeVerifyRoundV1 {
		for index := range required {
			required[index] = true
		}
	} else {
		union, err := k.TaskStageHandraiseUnion.Get(ctx,
			types.NewTaskStageKey(taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY))
		if err != nil {
			return false, err
		}
		attesterCount := 0
		for index := range required {
			if union.DataReadyAttestingBuilderBitmap[index/8]&(1<<uint(index%8)) != 0 {
				required[index] = true
				attesterCount++
			}
		}
		if attesterCount == 0 {
			for index := range required {
				required[index] = true
			}
		}
	}
	for index, builder := range selection.SelectedTaskBuilders {
		if !required[index] {
			continue
		}
		aggregate, err := k.BuilderDataUnavailableAggregate.Get(ctx,
			types.NewVerifyActorKey(taskKey, assignment.VerifyRound, builder))
		if err != nil {
			return false, nil
		}
		if aggregate.RequiredReportCount == 0 || aggregate.ValidReportCount < aggregate.RequiredReportCount ||
			len(aggregate.AggregateHash) != types.Hash32Len {
			return false, nil
		}
	}
	return true, nil
}
