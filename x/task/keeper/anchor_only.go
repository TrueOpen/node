package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// Anchor-only verification. A Profile version that requires the
// Worker anchor commitment is verified by the anchor audit alone: the chain
// samples the task from the beacon, the Worker opens and proves a sampled task,
// and the consensus validators decide. No Verifier is drawn, so such a task
// needs no second Cortex node.
//
// The task keeps the round-1 skeleton every later step reads, with nothing in
// it for a Verifier to do:
//
//   - at the receipt, a READY candidate window with window_size 0 freezes the
//     same clock a Verifier window would (the sampling beacon height is fixed
//     by the receipt and lies in the future);
//   - at the selection randomness height, the chain writes a round-1
//     assignment with zero selected Verifiers, samples the anchor audit from the
//     beacon exactly as before, and closes round 1 as PASS on the Worker's own
//     receipt, with no challenge window (a challenge round would need Verifiers);
//   - settlement then runs as for any PASS round: at once for an unsampled
//     task, after the anchor verdict for a sampled one.
//
// The two shapes below are the only markers. Neither can be produced by the
// Verifier path: a READY Verifier window always has members, and a Verifier
// assignment always selects selected_verifier_count operators.

// errAnchorOnlyAuditNotWritten reports that the sampling step of an anchor-only
// round left no audit row. The audit is the only verification such a task has,
// so the round is not closed: the caller retires the row to the verify-open
// deadline, which fails and refunds the task without a fault.
var errAnchorOnlyAuditNotWritten = errors.New("anchor-only sampling wrote no audit row")

// anchorOnlyNoChallengeReason is why MsgOpenChallengeRound is refused for an
// anchor-only task.
const anchorOnlyNoChallengeReason = "task is verified by the anchor audit alone and has no challenge round"

// receiptIsAnchorOnly reports whether an accepted receipt belongs to an
// anchor-only Profile version. The receipt's commitments were checked one for
// one against the locked Profile, so the anchor commitment is present exactly
// when the Profile requires it.
func receiptIsAnchorOnly(commitments []types.EvidenceCommitmentV1) bool {
	_, ok := anchorCommitmentOf(types.InferReceiptState{RequiredEvidenceCommitments: commitments})
	return ok
}

// isAnchorOnlyWindow recognizes the round-1 window the receipt of an
// anchor-only task freezes.
func isAnchorOnlyWindow(window types.VerifierCandidateWindowState) bool {
	return window.VerifyRound == types.VerifyRoundV1 &&
		window.Status == types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY &&
		window.WindowSize == 0 && window.EligibleCount == 0 && window.EligibilitySegmentCount == 0 &&
		len(window.GetVerifierWindowHash()) == 0
}

// isAnchorOnlyAssignment recognizes the round-1 assignment of an anchor-only
// task: no Verifier was selected and none raised a hand.
func isAnchorOnlyAssignment(assignment types.VerifierAssignmentState) bool {
	return assignment.VerifyRound == types.VerifyRoundV1 && assignment.SelectedVerifierCount == 0 &&
		len(assignment.SelectedVerifiers) == 0 && assignment.VerifierHandraiseCount == 0 &&
		assignment.RevealDeadlineHeight == 0
}

// anchorOnlyWindowHeader builds the window an anchor-only receipt freezes. It
// carries the same clock as a Verifier window and no eligibility source.
func anchorOnlyWindowHeader(taskID, inferReceiptHash []byte, assignment types.TaskAssignmentState, height uint64, clock verifierWindowClock) types.VerifierCandidateWindowState {
	return types.VerifierCandidateWindowState{
		SchemaVersion: 1, TaskId: append([]byte(nil), taskID...), VerifyRound: types.VerifyRoundV1,
		InferReceiptHash:        append([]byte(nil), inferReceiptHash...),
		CandidatePoolSnapshotId: append([]byte(nil), assignment.CandidatePoolSnapshotId...),
		CandidatePoolHash:       append([]byte(nil), assignment.CandidatePoolHash...), EligibilityFrozenHeight: height,
		VerifierWindowSourceHash:   make([]byte, types.Hash32Len),
		WindowRandomnessHeight:     clock.WindowRandomnessHeight,
		BuilderProposalCloseHeight: clock.BuilderProposalCloseHeight,
		HandraiseCloseHeight:       clock.HandraiseCloseHeight,
		SelectionRandomnessHeight:  clock.SelectionRandomnessHeight,
		AssignmentDeadlineHeight:   clock.AssignmentDeadlineHeight,
		Status:                     types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY,
		GeneratedHeight:            height,
	}
}

// anchorOnlyRoundResult is what the closed round 1 of an anchor-only task
// contributes to settlement: PASS on the Worker's receipt, with the receipt's
// own generated token count as the billed work unit and no Verifier cluster.
func anchorOnlyRoundResult(infer types.InferReceiptState, round types.VerificationRoundState) roundCloseResult {
	return roundCloseResult{
		Verdict: types.TaskVerdict_TASK_VERDICT_PASS, FailureClass: types.TaskFailureClass_TASK_FAILURE_CLASS_NONE,
		GeneratedTokenCount:   infer.GeneratedTokenCount,
		ResultReceiptRefsHash: make([]byte, types.Hash32Len), ConsensusClusterHash: make([]byte, types.Hash32Len),
		RoundFactsHash: append([]byte(nil), round.GetRoundFactsHash()...),
	}
}

// finalizeAnchorOnlyRoundAtHeight is the anchor-only counterpart of
// finalizeVerifierAssignmentAtHeight. It runs in EndBlock once the selection
// randomness height is reached: it samples the anchor audit from that height's
// beacon and closes round 1 without Verifiers. It reports false while the
// beacon is not yet available.
func (k Keeper) finalizeAnchorOnlyRoundAtHeight(
	ctx context.Context,
	taskID types.TaskKey,
	window types.VerifierCandidateWindowState,
	currentHeight uint64,
) (bool, uint64, error) {
	if currentHeight < window.SelectionRandomnessHeight || !shared.IsBlockHeightV1(window.SelectionRandomnessHeight) {
		return false, 0, fmt.Errorf("anchor-only sampling height is outside the frozen window")
	}
	rowBytes := uint64(window.Size())
	if currentHeight > window.AssignmentDeadlineHeight {
		return false, rowBytes, types.ErrDeadlineExpired
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	beacon, found := k.hubKeeper.GetBeaconForDomain(
		sdkCtx, shared.DomainWeightedDrawV1, int64(window.SelectionRandomnessHeight),
	)
	if !found {
		return false, 0, nil
	}
	if beacon.Height != int64(window.SelectionRandomnessHeight) || len(beacon.Randomness) != types.Hash32Len {
		return false, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "anchor-only sampling beacon does not match its frozen height")
	}
	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	taskKey := types.NewTaskKey(taskID)
	core, err := k.TaskCore.Get(cache, taskKey)
	if err != nil || !bytes.Equal(core.TaskId, window.TaskId) {
		return false, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "anchor-only task state is inconsistent")
	}
	if err := k.validateActiveVerifierStage(cache, taskKey, core, types.VerifyRoundV1,
		types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_SELECTION_PENDING); err != nil {
		return false, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	workerAssignment, err := k.ReadTaskAssignment(cache, taskKey)
	if err != nil || !bytes.Equal(workerAssignment.TaskId, core.TaskId) {
		return false, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "anchor-only worker assignment is unavailable")
	}
	infer, err := k.ReadInferReceipt(cache, taskKey)
	if err != nil || !bytes.Equal(infer.InferReceiptHash, window.InferReceiptHash) || !receiptIsAnchorOnly(infer.RequiredEvidenceCommitments) {
		return false, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "anchor-only window has no matching anchor receipt")
	}
	rowBytes += uint64(core.Size() + workerAssignment.Size() + infer.Size())

	// zero32 is the "nothing selected" value of the three assignment commitments.
	zero := make([]byte, types.Hash32Len)
	// The assignment row keeps the strictly increasing clock every reader of
	// the row expects (open < commit < verify). No Verifier acts on it; the
	// commit deadline anchors the SETTLE Builder rank schedule.
	commitDeadline, overflowCommit := checkedHeightAdd(currentHeight, 1)
	verifyDeadline, overflowVerify := checkedHeightAdd(currentHeight, 2)
	if overflowCommit || overflowVerify {
		return false, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "anchor-only round deadline overflows")
	}
	assignment := types.VerifierAssignmentState{
		TaskId: append([]byte(nil), window.TaskId...), VerifyRound: types.VerifyRoundV1, OpenVerifyHeight: currentHeight,
		VerifierCandidateWindowHash: append([]byte(nil), zero...), VerifierLegalSetHash: append([]byte(nil), zero...),
		SelectedVerifiersHash: append([]byte(nil), zero...), SelectionRandomnessHeight: window.SelectionRandomnessHeight,
		SelectionRandomnessBeacon: append([]byte(nil), beacon.Randomness...),
		CommitDeadlineHeight:      commitDeadline, VerifyDeadlineHeight: verifyDeadline,
	}
	assignmentKey := types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1)
	if exists, err := k.VerifierAssignment.Has(cache, assignmentKey); err != nil || exists {
		if err != nil {
			return false, rowBytes, err
		}
		return false, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier assignment already exists")
	}
	if err := k.WriteVerifierAssignment(cache, assignmentKey, assignment); err != nil {
		return false, rowBytes, err
	}
	// Same draw as on the Verifier path: the coin and the positions come from
	// this height's beacon, which did not exist when the receipt was committed.
	if err := k.sampleAnchorAudit(cache, assignment); err != nil {
		return false, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	// Fail closed. sampleAnchorAudit returns without a row when it cannot size
	// the audit (the task's parameters are unavailable, or a deadline overflows).
	// On the Verifier path the round is still verified by its Verifiers; here
	// the audit is all there is, so no row means no PASS. Nothing of this
	// attempt is written.
	if sampled, err := k.AnchorAudit.Has(cache, taskKey); err != nil {
		return false, rowBytes, err
	} else if !sampled {
		return false, rowBytes, errAnchorOnlyAuditNotWritten
	}

	roundID, err := types.VerifyRoundID(cacheCtx.ChainID(), core.AcceptedTaskHash, types.VerifyRoundV1)
	if err != nil {
		return false, rowBytes, err
	}
	round := types.VerificationRoundState{
		TaskId: append([]byte(nil), core.TaskId...), TaskHash: append([]byte(nil), core.AcceptedTaskHash...),
		VerifyRound: types.VerifyRoundV1, RoundId: roundID[:], RoundOpenHeight: currentHeight,
		RoundCloseDeadlineHeight: verifyDeadline, InferReceiptRef: append([]byte(nil), window.InferReceiptHash...),
		ProfileExecutionSnapshotHash: append([]byte(nil), workerAssignment.ProfileExecutionSnapshotHash...),
		GenerationParamsDigest:       append([]byte(nil), workerAssignment.GenerationParamsDigest...),
		GeneratedTokenCount:          infer.GeneratedTokenCount,
		XClosedHeight:                &types.VerificationRoundState_ClosedHeight{ClosedHeight: currentHeight},
		XResultReceiptRefsHash:       &types.VerificationRoundState_ResultReceiptRefsHash{ResultReceiptRefsHash: append([]byte(nil), zero...)},
		XConsensusClusterHash:        &types.VerificationRoundState_ConsensusClusterHash{ConsensusClusterHash: append([]byte(nil), zero...)},
		XVerdict:                     &types.VerificationRoundState_Verdict{Verdict: types.TaskVerdict_TASK_VERDICT_PASS},
		XFailureClass:                &types.VerificationRoundState_FailureClass{FailureClass: types.TaskFailureClass_TASK_FAILURE_CLASS_NONE},
	}
	factsHash, err := types.VerifyRoundFactsHash(cacheCtx.ChainID(), round)
	if err != nil {
		return false, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	round.XRoundFactsHash = &types.VerificationRoundState_RoundFactsHash{RoundFactsHash: factsHash[:]}
	if exists, err := k.VerificationRound.Has(cache, assignmentKey); err != nil || exists {
		if err != nil {
			return false, rowBytes, err
		}
		return false, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "round 1 header already exists")
	}
	if err := k.WriteVerificationRound(cache, assignmentKey, round); err != nil {
		return false, rowBytes, err
	}

	advanceTaskPhase(&core, types.TaskPhase_TASK_PHASE_VERIFIER_ASSIGNED)
	core.UpdatedHeight = currentHeight
	if err := k.TaskCore.Set(cache, taskKey, core); err != nil {
		return false, rowBytes, err
	}
	if err := removeDeadlineIndex(cache, k.VerifyOpenDeadlineIndex, taskKey, window.AssignmentDeadlineHeight); err != nil {
		return false, rowBytes, err
	}
	if err := k.VerifierSelectionRandomnessIndex.Remove(cache,
		types.NewVerifyRoundIndexKey(window.SelectionRandomnessHeight, taskKey, types.VerifyRoundV1)); err != nil {
		return false, rowBytes, err
	}
	selection, err := k.GetTaskBuilderSelection(cache, taskKey)
	if err != nil {
		return false, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "Task Builder selection is unavailable at anchor-only sampling")
	}
	if err := k.reserveBuilderStageResponsibilities(
		cache, core, selection, serviceKeyResponsibilitySettleBuilder, currentHeight,
	); err != nil {
		return false, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	if err := k.releaseBuilderStageResponsibilities(
		cache, core, selection, serviceKeyResponsibilityOpenVerifyBuilder,
	); err != nil {
		return false, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	if err := emitTypedEvent(cache, &types.EventVerifierAssignmentFinalized{
		SessionId: append([]byte(nil), core.SessionId...), TaskId: append([]byte(nil), core.TaskId...),
		VerifyRound: types.VerifyRoundV1, SelectedVerifiersHash: append([]byte(nil), assignment.SelectedVerifiersHash...),
		VerifierLegalSetHash:      append([]byte(nil), assignment.VerifierLegalSetHash...),
		SelectionRandomnessHeight: window.SelectionRandomnessHeight,
		CommitDeadline:            assignment.CommitDeadlineHeight, VerifyDeadline: assignment.VerifyDeadlineHeight,
	}); err != nil {
		return false, rowBytes, err
	}

	// Round 1 closes here with no challenge window: a challenge round re-verifies
	// with Verifiers, which this mode does not have. The shared round-1 close
	// writes the summary, maps the verdict onto the task and finalizes it.
	if err := k.closeRoundOneSummary(cache, taskKey, round, anchorOnlyRoundResult(infer, round), false); err != nil {
		return false, rowBytes, err
	}
	write()
	return true, rowBytes, nil
}
