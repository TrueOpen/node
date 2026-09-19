package keeper

import (
	"bytes"
	"context"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/task/types"
)

// finalizeOpenTaskCandidateUnion freezes the accepted-time union into the sole
// assignment candidate commitment. All callers run it inside a cache context.
func (k Keeper) finalizeOpenTaskCandidateUnion(ctx context.Context, taskKey types.TaskKey, finalizeHeight uint64) error {
	stage := types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK
	stageKey := types.NewTaskStageKey(taskKey, stage)
	union, err := k.TaskStageHandraiseUnion.Get(ctx, stageKey)
	if err != nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, "worker union is unavailable at finalize")
	}
	if union.Status == types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_FINALIZED {
		return nil
	}
	if union.Status != types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_OPEN ||
		union.UnionCount == 0 || finalizeHeight < union.WindowCloseHeight || len(union.UnionBitmapHash) != types.Hash32Len {
		return errorsmod.Wrap(types.ErrInvariantBroken, "worker union is not ready to finalize")
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	if union.UnionCount > params.Proposals.MaxCandidateUnionMembersPerStage ||
		union.UnionCount > params.Proposals.MaxCandidateStageFinalizeMembersPerBlock {
		return errorsmod.Wrap(types.ErrInvariantBroken, "worker union exceeds the registered bounded finalize budget")
	}
	core, err := k.TaskCore.Get(ctx, taskKey)
	if err != nil || !bytes.Equal(core.TaskId, union.TaskId) || core.TaskPhase != types.TaskPhase_TASK_PHASE_WORKER_ASSIGNMENT_PENDING {
		return errorsmod.Wrap(types.ErrInvariantBroken, "worker task core is unavailable at finalize")
	}
	assignment, err := k.TaskAssignment.Get(ctx, taskKey)
	if err != nil || !bytes.Equal(assignment.TaskId, union.TaskId) ||
		!bytes.Equal(assignment.CandidatePoolSnapshotId, union.CandidatePoolSnapshotId) ||
		!bytes.Equal(assignment.CandidatePoolHash, union.CandidatePoolHash) || len(assignment.GenerationParamsDigest) != types.Hash32Len {
		return errorsmod.Wrap(types.ErrInvariantBroken, "worker assignment scope is unavailable at finalize")
	}
	if len(assignment.AssignmentCandidateSetHash) != 0 || assignment.AssignAcceptHeight != 0 {
		return errorsmod.Wrap(types.ErrInvariantBroken, "worker assignment already has a conflicting legal set")
	}
	slotCapacity, segmentBytes, segmentCount, ok := k.hubKeeper.GetCandidatePoolLayout(ctx, union.CandidatePoolSnapshotId)
	if !ok || slotCapacity == 0 || segmentBytes == 0 || segmentCount == 0 {
		return errorsmod.Wrap(types.ErrInvariantBroken, "candidate pool layout is unavailable at finalize")
	}
	segments, err := k.loadTaskUnionSegments(ctx, taskKey, stage, slotCapacity, segmentBytes, segmentCount)
	if err != nil {
		return err
	}
	recomputed, err := taskStageUnionBitmapHash(
		sdk.UnwrapSDKContext(ctx).ChainID(), union.TaskId, stage, union.CandidatePoolSnapshotId,
		slotCapacity, segmentBytes, union.UnionCount, segments,
	)
	if err != nil || !bytes.Equal(recomputed[:], union.UnionBitmapHash) {
		return errorsmod.Wrap(types.ErrInvariantBroken, "worker union bitmap commitment mismatch at finalize")
	}
	union.Status = types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_FINALIZING
	header := types.AssignmentCandidateSetState{
		SchemaVersion: types.AssignmentCandidateSchemaVersionV1,
		TaskId:        append([]byte(nil), core.TaskId...), TaskHash: append([]byte(nil), core.AcceptedTaskHash...),
		CandidatePoolSnapshotId: append([]byte(nil), union.CandidatePoolSnapshotId...),
		CandidatePoolHash:       append([]byte(nil), union.CandidatePoolHash...),
		UnionBitmapHash:         append([]byte(nil), union.UnionBitmapHash...), CandidateCount: union.UnionCount,
	}
	facts, err := k.loadWorkerCandidateFacts(ctx, taskKey, union.UnionCount)
	if err != nil {
		return err
	}
	legalSetHash, err := finalizeWorkerLegalSet(sdk.UnwrapSDKContext(ctx).ChainID(), header, union, facts)
	if err != nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	header.AssignmentCandidateSetHash = legalSetHash[:]
	if err := k.AssignmentCandidateSet.Set(ctx, taskKey, header); err != nil {
		return err
	}
	for segmentIndex := uint32(0); segmentIndex < segmentCount; segmentIndex++ {
		if err := k.TaskStageHandraiseUnionSegment.Remove(ctx, types.NewTaskStageSegmentKey(taskKey, stage, segmentIndex)); err != nil {
			return err
		}
	}
	union.Status = types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_FINALIZED
	if err := k.TaskStageHandraiseUnion.Set(ctx, stageKey, union); err != nil {
		return err
	}
	if err := k.releaseCandidateStageDuties(ctx, facts); err != nil {
		return err
	}
	// §10.1 FinalizeAssignWindow: the frozen candidate set is only half of what
	// this transition owes. Without the randomness height and its index the
	// EndBlock WORKER_ASSIGNMENT queue can never reach this Task, so the winner is
	// never drawn and the user's max_fee stays in escrow with no infer deadline,
	// no settlement deadline and therefore no refund path either.
	randomnessHeight, overflow := checkedHeightAdd(finalizeHeight, k.hubKeeper.GetHubParams(sdk.UnwrapSDKContext(ctx)).DeltaWBlocks)
	if overflow || randomnessHeight <= finalizeHeight {
		return errorsmod.Wrap(types.ErrInvariantBroken, "assignment randomness height is out of range")
	}
	assignment.AssignAcceptHeight = finalizeHeight
	assignment.AssignmentRandomnessHeight = randomnessHeight
	assignment.AssignmentCandidateSetHash = legalSetHash[:]
	if err := k.TaskAssignment.Set(ctx, taskKey, assignment); err != nil {
		return err
	}
	if err := addDeadlineIndex(ctx, k.AssignmentRandomnessIndex, taskKey, randomnessHeight); err != nil {
		return err
	}
	core.AssignmentStatus = types.AssignmentStatus_ASSIGNMENT_STATUS_RANDOMNESS_PENDING
	core.UpdatedHeight = finalizeHeight
	return k.TaskCore.Set(ctx, taskKey, core)
}
