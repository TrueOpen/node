package keeper

import (
	"bytes"
	"context"
	"errors"

	"cosmossdk.io/collections"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// Task is §16.2 `QueryTask`: a read-only composite over the active Task state.
// TaskCoreState is the only primary of the six sub-statuses, so the bundle is
// assembled from it plus the two optional projections.
func (q *queryServer) Task(ctx context.Context, req *types.QueryTaskRequest) (*types.QueryTaskResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	taskKey, err := requireQueryHash32("task_id", req.TaskId)
	if err != nil {
		return nil, err
	}
	core, err := q.k.TaskCore.Get(ctx, taskKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			summary, summaryErr := q.k.ReadTaskTerminalSummary(ctx, taskKey)
			if errors.Is(summaryErr, collections.ErrNotFound) {
				return nil, status.Error(codes.NotFound, "task not found")
			}
			if summaryErr != nil {
				return nil, status.Error(codes.Internal, summaryErr.Error())
			}
			if !bytes.Equal(summary.TaskId, req.TaskId) || len(summary.SummaryHash) != types.Hash32Len {
				return nil, status.Error(codes.Internal, "task terminal summary is non-canonical")
			}
			return &types.QueryTaskResponse{Task: types.TaskViewV1{
				Value: &types.TaskViewV1_Terminal{Terminal: &summary},
			}}, nil
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !bytes.Equal(core.TaskId, req.TaskId) {
		return nil, status.Error(codes.Internal, "task_core primary key does not match task_id")
	}
	bundle := &types.TaskActiveBundleV1{Core: core}
	assignment, err := q.k.ReadTaskAssignment(ctx, taskKey)
	if err == nil {
		view := taskAssignmentView(core, assignment)
		bundle.Assignment = &view
	} else if !errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.Internal, err.Error())
	}
	requiresVerifierAssignment := verificationStatusRequiresAssignment(core.VerificationStatus)
	if requiresVerifierAssignment || core.VerificationStatus == types.VerificationStatus_VERIFICATION_STATUS_VERIFY_FAILED {
		verifierAssignment, err := q.loadVerifierAssignment(ctx, taskKey, types.VerifyRoundV1)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				if requiresVerifierAssignment {
					return nil, status.Error(codes.Internal, "task verification status requires a verifier assignment")
				}
			} else {
				return nil, err
			}
		} else {
			bundle.Round1VerifierAssignment = &verifierAssignment
		}
	}
	roundSummary, err := q.k.TaskRoundSummary.Get(ctx, taskKey)
	if err == nil {
		if !bytes.Equal(roundSummary.TaskId, core.TaskId) {
			return nil, status.Error(codes.Internal, "task round summary scope does not match task")
		}
		bundle.RoundSummary = &roundSummary
	} else if !errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.Internal, err.Error())
	}
	// Round 2 exists only once a challenge has opened, so unlike round 1 there is
	// no verification status that makes it mandatory: NotFound is the ordinary
	// answer for a task that was never challenged, not a broken invariant.
	round2Assignment, err := q.loadVerifierAssignment(ctx, taskKey, types.ChallengeVerifyRoundV1)
	if err == nil {
		bundle.Round2VerifierAssignment = &round2Assignment
	} else if status.Code(err) != codes.NotFound {
		return nil, err
	}
	return &types.QueryTaskResponse{Task: types.TaskViewV1{
		Value: &types.TaskViewV1_Active{Active: bundle},
	}}, nil
}

func (q *queryServer) EpochTaskSummary(ctx context.Context, req *types.QueryEpochTaskSummaryRequest) (*types.QueryEpochTaskSummaryResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	receipt, err := q.k.EpochTaskSummaryReceipt.Get(ctx, req.Epoch)
	if err == nil {
		if receipt.Epoch != req.Epoch || len(receipt.ReceiptHash) != types.Hash32Len {
			return nil, status.Error(codes.Internal, "epoch task summary receipt is non-canonical")
		}
		return &types.QueryEpochTaskSummaryResponse{Value: &types.QueryEpochTaskSummaryResponse_Receipt{Receipt: &receipt}}, nil
	}
	if !errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.Internal, err.Error())
	}
	cursor, err := q.k.EpochTaskSummaryCursor.Get(ctx, req.Epoch)
	if err == nil {
		if cursor.Epoch != req.Epoch || len(cursor.RunningRoot) != types.Hash32Len {
			return nil, status.Error(codes.Internal, "epoch task summary cursor is non-canonical")
		}
		return &types.QueryEpochTaskSummaryResponse{Value: &types.QueryEpochTaskSummaryResponse_Running{Running: &cursor}}, nil
	}
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "epoch task summary not found")
	}
	return nil, status.Error(codes.Internal, err.Error())
}

// TaskStage is §16.2 `QueryTaskStage`: the stage and next-deadline projection.
// The six statuses come from TaskCore only; a missing required sub-state is
// Internal, never a default value.
func (q *queryServer) TaskStage(ctx context.Context, req *types.QueryTaskStageRequest) (*types.QueryTaskStageResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	taskKey, err := requireQueryHash32("task_id", req.TaskId)
	if err != nil {
		return nil, err
	}
	core, err := q.k.TaskCore.Get(ctx, taskKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "task not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	if core.TaskPhase == types.TaskPhase_TASK_PHASE_UNSPECIFIED {
		return nil, status.Error(codes.Internal, "task_core has no task_phase")
	}
	view := types.TaskStageViewV1{
		TaskId:               append([]byte(nil), core.TaskId...),
		TaskPhase:            core.TaskPhase,
		AssignmentStatus:     core.AssignmentStatus,
		ReceiptStatus:        core.ReceiptStatus,
		VerificationStatus:   core.VerificationStatus,
		SettlementStatus:     core.SettlementStatus,
		FinalityStatus:       core.FinalityStatus,
		EffectiveVerifyRound: core.EffectiveVerifyRound,
		UpdatedHeight:        core.UpdatedHeight,
	}
	kind, height, found, err := q.nextTaskDeadline(ctx, taskKey, core)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if found {
		view.XNextDeadlineKind = &types.TaskStageViewV1_NextDeadlineKind{NextDeadlineKind: kind}
		view.XNextDeadlineHeight = &types.TaskStageViewV1_NextDeadlineHeight{NextDeadlineHeight: height}
	}
	return &types.QueryTaskStageResponse{Stage: view}, nil
}

// nextTaskDeadline derives the single next deadline from the authoritative phase.
// It uses the §5.9 DeadlineKindV1 numbers, never a query-only copy of the enum.
//
// The phase alone does not name the pending deadline. EndBlock consumes a queue
// row as soon as current_height >= deadline_height, and three of those handlers
// then move the row *without* touching any status: handleExpiredCommitDeadline
// and handleExpiredRevealDeadline both call advanceToVerifyDeadline, which
// re-files the row under verify_deadline_height and writes neither core state
// nor an event, and rescheduleVerifyOpenDeadline re-files a VERIFY_OPEN row
// under a retry height that is never persisted. The phase therefore stays
// COMMITTING or REVEALING while the only row still queued for the task is
// VERIFY_FINAL. Each branch below reports a height only while that height is
// still in the future and otherwise falls through to the row the sweeper
// actually holds, because a consumed height reads as a permanently stuck task
// (issue 156). The two Worker branches need no such guard: their sweep always
// transitions the phase, so an overdue height there lasts a single block.
func (q *queryServer) nextTaskDeadline(ctx context.Context, taskKey types.TaskKey, core types.TaskCoreState) (types.DeadlineKindV1, uint64, bool, error) {
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return 0, 0, false, err
	}
	switch core.TaskPhase {
	case types.TaskPhase_TASK_PHASE_SETTLING:
		return q.nextSettlementDeadline(ctx, taskKey, core)
	case types.TaskPhase_TASK_PHASE_SETTLED,
		types.TaskPhase_TASK_PHASE_FAILED:
		return nextEvidenceCleanupDeadline(core)
	}
	if core.VerificationStatus == types.VerificationStatus_VERIFICATION_STATUS_VERIFICATION_PASSED ||
		core.VerificationStatus == types.VerificationStatus_VERIFICATION_STATUS_VERIFY_FAILED {
		return q.nextChallengeWindowDeadline(ctx, taskKey, core)
	}
	assignment, err := q.k.ReadTaskAssignment(ctx, taskKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return 0, 0, false, nil
		}
		return 0, 0, false, err
	}
	switch core.TaskPhase {
	case types.TaskPhase_TASK_PHASE_WORKER_ASSIGNMENT_PENDING:
		if assignment.AssignmentRandomnessHeight == 0 {
			return 0, 0, false, nil
		}
		return types.DeadlineKindV1_DEADLINE_KIND_V1_WORKER_ASSIGNMENT, assignment.AssignmentRandomnessHeight, true, nil
	case types.TaskPhase_TASK_PHASE_WORKER_ASSIGNED:
		if assignment.InferDeadlineHeight == 0 {
			return 0, 0, false, nil
		}
		return types.DeadlineKindV1_DEADLINE_KIND_V1_WORKER_INFER, assignment.InferDeadlineHeight, true, nil
	}

	switch core.VerificationStatus {
	case types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_WINDOW_PENDING,
		types.VerificationStatus_VERIFICATION_STATUS_VERIFY_COLLECTION_OPEN,
		types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_SELECTION_PENDING:
		window, err := q.k.VerifierCandidateWindow.Get(ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1))
		if err != nil {
			return 0, 0, false, err
		}
		if !bytes.Equal(window.TaskId, core.TaskId) || window.VerifyRound != types.VerifyRoundV1 {
			return 0, 0, false, errors.New("verifier candidate window scope does not match task")
		}
		if window.EligibilityFrozenHeight == 0 || window.EligibilityFrozenHeight >= window.WindowRandomnessHeight ||
			window.WindowRandomnessHeight >= window.BuilderProposalCloseHeight ||
			window.BuilderProposalCloseHeight >= window.HandraiseCloseHeight ||
			window.HandraiseCloseHeight >= window.SelectionRandomnessHeight ||
			window.SelectionRandomnessHeight >= window.AssignmentDeadlineHeight {
			return 0, 0, false, errors.New("verifier candidate window has a non-canonical deadline clock")
		}
		// The window clock is monotone (validated just above), so the pending
		// deadline is the earliest mark the status has not already passed and
		// EndBlock has not already consumed. assignment_deadline_height is the
		// terminal mark because that is the height the VerifyOpenDeadlineIndex row
		// is filed under (genesis.go, verifier_assignment_apply.go).
		marks := [...]uint64{
			window.WindowRandomnessHeight,
			window.HandraiseCloseHeight,
			window.SelectionRandomnessHeight,
			window.AssignmentDeadlineHeight,
		}
		reached := 0
		switch core.VerificationStatus {
		case types.VerificationStatus_VERIFICATION_STATUS_VERIFY_COLLECTION_OPEN:
			reached = 1
		case types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_SELECTION_PENDING:
			reached = 2
		}
		for _, mark := range marks[reached:] {
			if mark > height {
				return types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_OPEN, mark, true, nil
			}
		}
		// Past assignment_deadline_height the row is the EndBlock sweep's to
		// consume, and consuming it fails the task — after which this switch is
		// unreachable because the status is VERIFY_FAILED. The only window this
		// branch covers is the blocks between the deadline height and that sweep,
		// where the pending mark is already spent. §16.2 requires the two
		// next-deadline fields to be absent rather than
		// defaulted, so absent is the honest answer; a consumed height is not.
		return 0, 0, false, nil
	case types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_ASSIGNED,
		types.VerificationStatus_VERIFICATION_STATUS_COMMITTING,
		types.VerificationStatus_VERIFICATION_STATUS_REVEALING:
		round := core.EffectiveVerifyRound
		if round == 0 {
			round = types.VerifyRoundV1
		}
		assignment, err := q.loadVerifierAssignment(ctx, taskKey, round)
		if err != nil {
			return 0, 0, false, err
		}
		if core.VerificationStatus == types.VerificationStatus_VERIFICATION_STATUS_REVEALING {
			if assignment.RevealDeadlineHeight == 0 {
				return 0, 0, false, errors.New("revealing verifier assignment has no reveal deadline")
			}
			if assignment.RevealDeadlineHeight > height {
				return types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_REVEAL, assignment.RevealDeadlineHeight, true, nil
			}
			return advancedVerifyFinalDeadline(assignment, assignment.RevealDeadlineHeight)
		}
		if assignment.CommitDeadlineHeight == 0 {
			return 0, 0, false, errors.New("committing verifier assignment has no commit deadline")
		}
		if assignment.CommitDeadlineHeight > height {
			return types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_COMMIT, assignment.CommitDeadlineHeight, true, nil
		}
		return advancedVerifyFinalDeadline(assignment, assignment.CommitDeadlineHeight)
	default:
		return 0, 0, false, nil
	}
}

func (q *queryServer) nextChallengeWindowDeadline(
	ctx context.Context,
	taskKey types.TaskKey,
	core types.TaskCoreState,
) (types.DeadlineKindV1, uint64, bool, error) {
	summary, err := q.k.TaskRoundSummary.Get(ctx, taskKey)
	if err != nil {
		return 0, 0, false, err
	}
	if !bytes.Equal(summary.TaskId, core.TaskId) {
		return 0, 0, false, errors.New("task round summary scope does not match task")
	}
	if summary.MaxClosedRound == types.ChallengeVerifyRoundV1 && summary.OpenRoundCount == 0 && summary.XRoundsClosedHeight == nil {
		cursor, err := q.k.RoundEconomicEffectApplyCursor.Get(
			ctx, types.NewVerifyRoundKey(taskKey, types.ChallengeVerifyRoundV1),
		)
		if err != nil {
			return 0, 0, false, err
		}
		if !bytes.Equal(cursor.TaskId, core.TaskId) || cursor.VerifyRound != types.ChallengeVerifyRoundV1 {
			return 0, 0, false, errors.New("challenge round effect cursor scope does not match task")
		}
		// Round-effect application is a bounded queue, but DeadlineKindV1 has no
		// value for that queue. Absence is the truthful projection until it moves
		// the task to SETTLING and TASK_SETTLEMENT becomes the next deadline.
		return 0, 0, false, nil
	}
	if summary.XRoundsClosedHeight != nil {
		return 0, 0, false, errors.New("finalized task rounds require the settling phase")
	}
	if summary.MaxClosedRound != types.VerifyRoundV1 || summary.OpenRoundCount != 0 ||
		summary.XChallengeOpenHeight == nil || summary.XChallengeCloseHeight == nil {
		return 0, 0, false, errors.New("round 1 challenge window is not frozen")
	}
	openHeight := summary.GetChallengeOpenHeight()
	closeHeight := summary.GetChallengeCloseHeight()
	if openHeight == 0 || closeHeight <= openHeight {
		return 0, 0, false, errors.New("round 1 challenge window is invalid")
	}
	round, err := q.k.ReadVerificationRound(ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1))
	if err != nil {
		return 0, 0, false, err
	}
	if !bytes.Equal(round.TaskId, core.TaskId) || round.VerifyRound != types.VerifyRoundV1 ||
		round.XClosedHeight == nil || round.GetClosedHeight() != openHeight ||
		round.XVerdict == nil || !isExplicitRoundVerdict(round.GetVerdict()) {
		return 0, 0, false, errors.New("round 1 challenge source is non-canonical")
	}
	if (round.GetVerdict() == types.TaskVerdict_TASK_VERDICT_PASS) !=
		(core.VerificationStatus == types.VerificationStatus_VERIFICATION_STATUS_VERIFICATION_PASSED) {
		return 0, 0, false, errors.New("round 1 verdict does not match task verification status")
	}
	return types.DeadlineKindV1_DEADLINE_KIND_V1_CHALLENGE_WINDOW_CLOSE, closeHeight, true, nil
}

func (q *queryServer) nextSettlementDeadline(
	ctx context.Context,
	taskKey types.TaskKey,
	core types.TaskCoreState,
) (types.DeadlineKindV1, uint64, bool, error) {
	summary, err := q.k.TaskRoundSummary.Get(ctx, taskKey)
	if err != nil {
		return 0, 0, false, err
	}
	if !bytes.Equal(summary.TaskId, core.TaskId) {
		return 0, 0, false, errors.New("task round summary scope does not match task")
	}
	if summary.OpenRoundCount != 0 || summary.XRoundsClosedHeight == nil || summary.GetRoundsClosedHeight() == 0 {
		return 0, 0, false, errors.New("settling task has no closed-round height")
	}
	params, err := q.k.Params.Get(ctx)
	if err != nil {
		return 0, 0, false, err
	}
	deadline, err := settlementDeadlineHeight(summary.GetRoundsClosedHeight(), params)
	if err != nil {
		return 0, 0, false, err
	}
	return types.DeadlineKindV1_DEADLINE_KIND_V1_TASK_SETTLEMENT, deadline, true, nil
}

func nextEvidenceCleanupDeadline(core types.TaskCoreState) (types.DeadlineKindV1, uint64, bool, error) {
	if core.FinalityStatus != shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL ||
		core.XTaskFinalityHeight == nil || core.GetTaskFinalityHeight() == 0 ||
		core.EvidenceRetentionBlocksSnapshot == 0 {
		return 0, 0, false, errors.New("terminal task has no evidence cleanup clock")
	}
	deadline, overflow := checkedHeightAdd(core.GetTaskFinalityHeight(), core.EvidenceRetentionBlocksSnapshot)
	if overflow || deadline <= core.GetTaskFinalityHeight() {
		return 0, 0, false, errors.New("evidence cleanup deadline is invalid")
	}
	return types.DeadlineKindV1_DEADLINE_KIND_V1_EVIDENCE_CLEANUP, deadline, true, nil
}

// advancedVerifyFinalDeadline names the row advanceToVerifyDeadline leaves behind
// once a commit or reveal deadline has been consumed: the phase is unchanged and
// the task's only queued deadline is VERIFY_FINAL. loadVerifierAssignmentV1
// already rejects a row whose verify deadline is not strictly past the commit and
// reveal ones, so the ordering check below is unreachable defense that keeps this
// helper total rather than letting a zero height pass for a real deadline.
func advancedVerifyFinalDeadline(
	assignment types.VerifierAssignmentState,
	consumed uint64,
) (types.DeadlineKindV1, uint64, bool, error) {
	if assignment.VerifyDeadlineHeight <= consumed {
		return 0, 0, false, errors.New("verifier assignment has no verify deadline past its consumed phase deadline")
	}
	return types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_FINAL, assignment.VerifyDeadlineHeight, true, nil
}

func verificationStatusRequiresAssignment(status types.VerificationStatus) bool {
	switch status {
	case types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_ASSIGNED,
		types.VerificationStatus_VERIFICATION_STATUS_COMMITTING,
		types.VerificationStatus_VERIFICATION_STATUS_REVEALING,
		types.VerificationStatus_VERIFICATION_STATUS_NO_CONSENSUS,
		types.VerificationStatus_VERIFICATION_STATUS_VERIFICATION_PASSED:
		return true
	default:
		return false
	}
}

// TaskAssignment is §16.2 `QueryTaskAssignment`. §16.2 requires that while the
// draw is pending the three winner fields are *absent*, not defaulted, and that
// the internal ref-release flag stays hidden.
func (q *queryServer) TaskAssignment(ctx context.Context, req *types.QueryTaskAssignmentRequest) (*types.QueryTaskAssignmentResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	taskKey, err := requireQueryHash32("task_id", req.TaskId)
	if err != nil {
		return nil, err
	}
	core, err := q.k.TaskCore.Get(ctx, taskKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "task not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	assignment, err := q.k.ReadTaskAssignment(ctx, taskKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "task assignment not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryTaskAssignmentResponse{Assignment: taskAssignmentView(core, assignment)}, nil
}

func taskAssignmentView(core types.TaskCoreState, assignment types.TaskAssignmentState) types.TaskAssignmentViewV1 {
	view := types.TaskAssignmentViewV1{
		TaskId:                       append([]byte(nil), assignment.TaskId...),
		AssignAcceptHeight:           assignment.AssignAcceptHeight,
		AssignmentRandomnessHeight:   assignment.AssignmentRandomnessHeight,
		CandidatePoolSnapshotId:      append([]byte(nil), assignment.CandidatePoolSnapshotId...),
		CandidatePoolHash:            append([]byte(nil), assignment.CandidatePoolHash...),
		AssignmentCandidateSetHash:   append([]byte(nil), assignment.AssignmentCandidateSetHash...),
		ProfileExecutionSnapshotHash: append([]byte(nil), assignment.ProfileExecutionSnapshotHash...),
		JudgmentFunctionVersion:      assignment.JudgmentFunctionVersion,
		EvidenceSchemaHash:           append([]byte(nil), assignment.EvidenceSchemaHash...),
		CanonicalEncodingVersion:     assignment.CanonicalEncodingVersion,
		GenerationParamsDigest:       append([]byte(nil), assignment.GenerationParamsDigest...),
		MetricAggregateProofVersion:  assignment.MetricAggregateProofVersion,
		AssignmentStatus:             core.AssignmentStatus,
		WorkerInferTimeoutSlashBps:   assignment.WorkerInferTimeoutSlashBps,
		ResultRevealMissingSlashBps:  assignment.ResultRevealMissingSlashBps,
	}
	if assignment.WinnerWorker != "" {
		view.XWinnerWorker = &types.TaskAssignmentViewV1_WinnerWorker{WinnerWorker: assignment.WinnerWorker}
	}
	if len(assignment.WinnerDrawDigest) == types.Hash32Len {
		view.XWinnerDrawDigest = &types.TaskAssignmentViewV1_WinnerDrawDigest{WinnerDrawDigest: append([]byte(nil), assignment.WinnerDrawDigest...)}
	}
	if assignment.WinnerConfirmHeight != 0 {
		view.XWinnerConfirmHeight = &types.TaskAssignmentViewV1_WinnerConfirmHeight{WinnerConfirmHeight: assignment.WinnerConfirmHeight}
	}
	if assignment.InferDeadlineHeight != 0 {
		view.XInferDeadlineHeight = &types.TaskAssignmentViewV1_InferDeadlineHeight{InferDeadlineHeight: assignment.InferDeadlineHeight}
	}
	if assignment.AssignmentFailReason != types.AssignmentFailureReason_ASSIGNMENT_FAILURE_REASON_UNSPECIFIED {
		view.XAssignmentFailReason = &types.TaskAssignmentViewV1_AssignmentFailReason{AssignmentFailReason: assignment.AssignmentFailReason}
	}
	return view
}

// AssignmentRandomness is §16.4 `QueryAssignmentRandomness`: the frozen height,
// beacon and draw digest only. field 7 projects `winner_draw_digest`, whose sole
// producer is the §10.2 `TRUEOPEN_WINNER_DRAW_V1` preimage.
func (q *queryServer) AssignmentRandomness(ctx context.Context, req *types.QueryAssignmentRandomnessRequest) (*types.QueryAssignmentRandomnessResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	taskKey, err := requireQueryHash32("task_id", req.TaskId)
	if err != nil {
		return nil, err
	}
	assignment, err := q.k.ReadTaskAssignment(ctx, taskKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "task assignment not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	view := types.AssignmentRandomnessViewV1{
		TaskId:                     append([]byte(nil), assignment.TaskId...),
		AssignmentRandomnessHeight: assignment.AssignmentRandomnessHeight,
		AssignmentCandidateSetHash: append([]byte(nil), assignment.AssignmentCandidateSetHash...),
	}
	if assignment.AssignmentRandomnessHeight != 0 && shared.IsBlockHeightV1(assignment.AssignmentRandomnessHeight) {
		beacon, ok := q.k.hubKeeper.GetBeaconForDomain(
			sdkContextFrom(ctx), shared.DomainWeightedDrawV1, int64(assignment.AssignmentRandomnessHeight),
		)
		if !ok || len(beacon.Randomness) != types.Hash32Len {
			return nil, status.Error(codes.Internal, "assignment beacon is unavailable")
		}
		view.BeaconRandomness = append([]byte(nil), beacon.Randomness...)
		// beacon_proof_digest is optional since wire v0.2.1 and carries protobuf
		// presence, so the dev-only placeholder source projects it as absent
		// rather than as an empty or zero32 value. proposer_vrf_v1 is the only
		// source that produces one, and GetBeaconForDomain already refuses a
		// snapshot whose digest is not exactly 32 bytes.
		if len(beacon.ProofDigest) == types.Hash32Len {
			view.XBeaconProofDigest = &types.AssignmentRandomnessViewV1_BeaconProofDigest{
				BeaconProofDigest: append([]byte(nil), beacon.ProofDigest...),
			}
		} else if beacon.SourceTag != hubtypes.BeaconSourcePlaceholderBlockHashV1 {
			return nil, status.Error(codes.Internal, "assignment beacon proof digest is unavailable")
		}
	}
	if assignment.WinnerWorker != "" {
		view.XWinnerWorker = &types.AssignmentRandomnessViewV1_WinnerWorker{WinnerWorker: assignment.WinnerWorker}
	}
	if len(assignment.WinnerDrawDigest) == types.Hash32Len {
		view.XWinnerDrawDigest = &types.AssignmentRandomnessViewV1_WinnerDrawDigest{WinnerDrawDigest: append([]byte(nil), assignment.WinnerDrawDigest...)}
	}
	if assignment.WinnerConfirmHeight != 0 {
		view.XWinnerConfirmHeight = &types.AssignmentRandomnessViewV1_WinnerConfirmHeight{WinnerConfirmHeight: assignment.WinnerConfirmHeight}
	}
	return &types.QueryAssignmentRandomnessResponse{Randomness: view}, nil
}

// TaskBudget is §16.2 `QueryTaskBudget`: the single authoritative ledger row. A
// broken amount is Internal, never a partial view.
func (q *queryServer) TaskBudget(ctx context.Context, req *types.QueryTaskBudgetRequest) (*types.QueryTaskBudgetResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	taskKey, err := requireQueryHash32("task_id", req.TaskId)
	if err != nil {
		return nil, err
	}
	budget, err := q.k.TaskBudget.Get(ctx, taskKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "task budget not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	if err := validateTaskBudgetAmounts(budget); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryTaskBudgetResponse{Budget: budget}, nil
}

// TaskBuilders is §16.2 `QueryTaskBuilders`. The three body states are frozen:
//
//   - ACTIVE  -> exactly `builders_per_task` members; an empty array with ACTIVE
//     is an invariant break (`Internal`), *never* an empty page;
//   - PRUNED  -> members empty but count / hash / builder_set_id /
//     builder_set_hash / session_anchor_block_hash / created_height are all
//     retained; this is *not* NotFound;
//   - missing -> NotFound.
//
// If the body is present but `selected_task_builders_hash` cannot be recomputed
// from the resident members, that is `Internal` (§6.5 lines 1135-1147).
func (q *queryServer) TaskBuilders(ctx context.Context, req *types.QueryTaskBuildersRequest) (*types.QueryTaskBuildersResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	taskKey, err := requireQueryHash32("task_id", req.TaskId)
	if err != nil {
		return nil, err
	}
	selection, err := q.k.GetTaskBuilderSelection(ctx, taskKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "task builder selection not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !bytes.Equal(selection.TaskId, req.TaskId) {
		return nil, status.Error(codes.Internal, "task_builder_selection primary key does not match task_id")
	}
	if len(selection.SelectedTaskBuildersHash) != types.Hash32Len {
		return nil, status.Error(codes.Internal, "task_builder_selection has no ordered-member commitment")
	}

	switch selection.BodyStatus {
	case shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE:
		if err := validateActiveTaskBuilderSelection(sdkContextFrom(ctx).ChainID(), selection, req.TaskId); err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
	case shared.StoredBodyStatus_STORED_BODY_STATUS_PRUNED:
		if len(selection.SelectedTaskBuilders) != 0 {
			return nil, status.Error(codes.Internal, "PRUNED task builder selection still has resident members")
		}
		if selection.SelectedTaskBuilderCount == 0 || len(selection.BuilderSetHash) != types.Hash32Len ||
			len(selection.SessionAnchorBlockHash) != types.Hash32Len ||
			selection.BuilderSetId == "" || selection.CreatedHeight == 0 {
			return nil, status.Error(codes.Internal, "PRUNED task builder selection lost a retained header field")
		}
	default:
		return nil, status.Error(codes.Internal, "task builder selection has no body_status")
	}

	// body_status ACTIVE/PRUNED both project; `builder_set_ref_released` is an
	// internal field and is deliberately not in the view schema.
	return &types.QueryTaskBuildersResponse{Selection: types.TaskBuilderSelectionViewV1{
		TaskId:                   append([]byte(nil), selection.TaskId...),
		SessionAnchorBlockHash:   append([]byte(nil), selection.SessionAnchorBlockHash...),
		BuilderSetId:             selection.BuilderSetId,
		BuilderSetHash:           append([]byte(nil), selection.BuilderSetHash...),
		SelectedTaskBuilders:     append([]string(nil), selection.SelectedTaskBuilders...),
		SelectedTaskBuilderCount: selection.SelectedTaskBuilderCount,
		SelectedTaskBuildersHash: append([]byte(nil), selection.SelectedTaskBuildersHash...),
		CreatedHeight:            selection.CreatedHeight,
		BodyStatus:               selection.BodyStatus,
	}}, nil
}
