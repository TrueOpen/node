package keeper

import (
	"bytes"
	"context"
	"fmt"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// SubmitWorkerHandraises ORs legal Worker handraises into the authoritative
// OPEN_TASK union (keeper_api_contract.md §10.1). A signed_order creates the Task
// atomically; an existing_task can only append to that frozen scope.
//
// Nothing but `submitter_address` is taken from the request: the Task Builder
// slate, candidate pool, pool hash, candidate weights and deadlines are all read
// back from state that the first admission froze.
func (m msgServer) SubmitWorkerHandraises(ctx context.Context, msg *types.MsgSubmitWorkerHandraises) (*types.MsgSubmitWorkerHandraisesResponse, error) {
	if msg == nil || len(msg.Handraises) == 0 {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "worker proposal must contain a handraise")
	}
	if _, _, err := m.k.canonicalAddress("submitter_address", msg.SubmitterAddress); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, err.Error())
	}
	proposalScope, err := resolveWorkerProposalScope(msg.Scope)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, err.Error())
	}
	existing := &proposalScope.ref
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	currentHeight, err := currentBlockHeight(ctx)
	if err != nil {
		return nil, err
	}
	params, err := m.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if uint32(len(msg.Handraises)) > params.Proposals.MaxCandidateHandraisesPerProposal ||
		uint64(msg.Size()) > params.Proposals.MaxCandidateProposalBytes {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "worker proposal exceeds the registered item or byte cap")
	}

	var previousSlot uint32
	var scope *types.WorkerHandraiseV1
	seenOperators := make(map[string]struct{}, len(msg.Handraises))
	for index := range msg.Handraises {
		handraise := &msg.Handraises[index]
		// Expiry is a first-admission gate, not a replay gate: a persisted exact
		// replay is identified by its immutable proposal receipt before any
		// mutable phase or height check runs.
		if err := validateWorkerHandraiseEnvelope(sdkCtx.ChainID(), existing.TaskId, existing.TaskHash, handraise); err != nil {
			return nil, errorsmod.Wrap(types.ErrInvalidAssignment, err.Error())
		}
		if index != 0 && handraise.Member.Slot <= previousSlot {
			return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "worker handraises must be strictly slot-ascending and unique")
		}
		previousSlot = handraise.Member.Slot
		if _, duplicate := seenOperators[handraise.Member.OperatorAddress]; duplicate {
			return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "worker handraises must have unique operators")
		}
		seenOperators[handraise.Member.OperatorAddress] = struct{}{}
		if scope == nil {
			scope = handraise
			continue
		}
		if handraise.ModelId != scope.ModelId || handraise.ProfileVersion != scope.ProfileVersion ||
			!bytes.Equal(handraise.Member.CandidatePoolSnapshotId, scope.Member.CandidatePoolSnapshotId) {
			return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "worker handraises do not share one frozen task scope")
		}
	}
	handraiseDigests := make([]candidateSigningDigest, len(msg.Handraises))
	for index, handraise := range msg.Handraises {
		digest, err := types.WorkerHandraiseSigningDigest(handraise)
		if err != nil {
			return nil, errorsmod.Wrap(types.ErrInvalidAssignment, err.Error())
		}
		handraiseDigests[index] = candidateSigningDigest{Slot: handraise.Member.Slot, Digest: digest[:]}
	}

	stage := types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK
	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	taskKey := types.NewTaskKey(existing.TaskId)
	if proposalScope.signed != nil {
		if err := m.k.admitSignedWorkerOrder(cache, &proposalScope, msg.Handraises[0], params, currentHeight); err != nil {
			return nil, errorsmod.Wrap(types.ErrInvalidAssignment, err.Error())
		}
	}
	core, err := m.k.TaskCore.Get(cache, taskKey)
	if err != nil || !bytes.Equal(core.TaskId, existing.TaskId) ||
		len(core.AcceptedTaskHash) != types.Hash32Len || !bytes.Equal(core.AcceptedTaskHash, existing.TaskHash) {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "worker proposal task scope is unavailable")
	}
	assignment, err := m.k.TaskAssignment.Get(cache, taskKey)
	if err != nil || !bytes.Equal(assignment.TaskId, existing.TaskId) ||
		len(assignment.CandidatePoolSnapshotId) != types.Hash32Len || len(assignment.CandidatePoolHash) != types.Hash32Len ||
		assignment.CandidatePoolRefReleased {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "worker proposal assignment scope is unavailable")
	}
	if scope.ModelId != core.ModelId || scope.ProfileVersion != core.ProfileVersion ||
		!bytes.Equal(scope.Member.CandidatePoolSnapshotId, assignment.CandidatePoolSnapshotId) {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "worker proposal does not match the frozen task scope")
	}
	selection, err := m.k.loadActiveTaskBuilderSelection(cache, taskKey, core.TaskId)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "active Task Builder selection is unavailable")
	}

	if replay, found, err := m.k.findAcceptedWorkerProposalReplay(
		cache, taskKey, core, assignment, msg.SubmitterAddress,
		handraiseDigests, params.Proposals.MaxCandidateAcceptedProposalsPerStage,
	); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, err.Error())
	} else if found {
		return replay, nil
	}
	if proposalScope.signed != nil && proposalScope.existingAtStart {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "an accepted Task requires existing_task scope for a new proposal")
	}

	if core.TaskPhase != types.TaskPhase_TASK_PHASE_WORKER_ASSIGNMENT_PENDING ||
		core.AssignmentStatus != types.AssignmentStatus_ASSIGNMENT_STATUS_NONE || core.XTaskFinalityHeight != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidTaskStatus, "task is not collecting worker handraises")
	}
	for index := range msg.Handraises {
		if currentHeight > msg.Handraises[index].ExpiryHeight {
			return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "worker handraise envelope is expired")
		}
	}

	stageKey := types.NewTaskStageKey(taskKey, stage)
	union, err := m.k.TaskStageHandraiseUnion.Get(cache, stageKey)
	if err != nil {
		// The union is written by the first admission together with the Task, so a
		// follow-up proposal never creates one.
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "worker union is unavailable")
	}
	if union.SchemaVersion != 1 || union.Stage != stage ||
		union.Status != types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_OPEN ||
		!equalUnionHeaderScope(union, core.TaskId, assignment.CandidatePoolSnapshotId, assignment.CandidatePoolHash) ||
		(proposalScope.signed == nil && union.AcceptedProposalCount == 0) || len(union.UnionBitmapHash) != types.Hash32Len {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "worker union header is non-canonical")
	}
	if union.WindowCloseHeight == 0 || (currentHeight > union.WindowCloseHeight && !proposalScope.isFallback) {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "worker handraise window is closed")
	}

	// The snapshot body must still be retained by this Task's own ref; §10.1 does
	// not require it to still be the ACTIVE epoch pool.
	pool, ok := m.k.hubKeeper.GetCandidatePoolSnapshot(cacheCtx, assignment.CandidatePoolSnapshotId)
	if !ok || !bytes.Equal(pool.SnapshotId, assignment.CandidatePoolSnapshotId) ||
		!bytes.Equal(pool.PoolHash, assignment.CandidatePoolHash) || pool.SlotCapacity == 0 {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "frozen candidate pool snapshot is unavailable")
	}
	if held, err := m.k.hubKeeper.HasCandidatePoolTaskRef(cache, core.TaskId, assignment.CandidatePoolSnapshotId); err != nil || !held {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "candidate pool task ref is not held")
	}
	var builder string
	if !proposalScope.isFallback {
		builder, err = m.k.authorizeOpenTaskBuilder(cache, selection, msg.SubmitterAddress)
		if err != nil {
			return nil, errorsmod.Wrap(types.ErrInvalidAssignment, err.Error())
		}
	}
	orderValue, err := shared.ParseAmount(core.OrderValue)
	if err != nil || orderValue == 0 {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "frozen order_value is unavailable")
	}

	proposalDigest, err := workerProposalDigest(
		cacheCtx.ChainID(), core.TaskId, core.AcceptedTaskHash,
		assignment.CandidatePoolSnapshotId, assignment.CandidatePoolHash, builder, handraiseDigests,
	)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, err.Error())
	}
	proposedReceipt := types.BuilderStageProposalState{
		SchemaVersion: 1, TaskId: append([]byte(nil), core.TaskId...), Stage: stage,
		ProposalDigest: proposalDigest[:], ProposerOperator: builder,
	}

	facts := make([]types.TaskCandidateFactState, len(msg.Handraises))
	newSlots := make([]uint32, 0, len(msg.Handraises))
	newSlotSet := make(map[uint32]struct{}, len(msg.Handraises))
	existingSlots := make([]uint32, 0, len(msg.Handraises))
	for index, handraise := range msg.Handraises {
		fact, err := m.k.freezeWorkerCandidateFact(cache, pool, handraise, orderValue, currentHeight)
		if err != nil {
			return nil, errorsmod.Wrap(types.ErrInvalidAssignment, err.Error())
		}
		facts[index] = fact
		factKey := types.NewTaskCandidateFactKey(taskKey, stage, fact.Slot)
		if persisted, err := m.k.TaskCandidateFact.Get(cache, factKey); err == nil {
			// An accepted fact is immutable; a second proposal may re-list the same
			// slot only if it reproduces the accepted-time fact byte for byte.
			if !proto.Equal(&persisted, &fact) {
				return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "worker slot already has a conflicting accepted fact")
			}
			existingSlots = append(existingSlots, fact.Slot)
		} else if errIsNotFound(err) {
			newSlots = append(newSlots, fact.Slot)
			newSlotSet[fact.Slot] = struct{}{}
		} else {
			return nil, err
		}
	}

	slotCapacity, segmentBytes, segmentCount, ok := m.k.hubKeeper.GetCandidatePoolLayout(cache, assignment.CandidatePoolSnapshotId)
	if !ok || slotCapacity == 0 || segmentBytes == 0 || segmentCount == 0 {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "candidate pool layout is unavailable")
	}
	segments, err := m.k.loadTaskUnionSegments(cache, taskKey, stage, slotCapacity, segmentBytes, segmentCount)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	existingHash, err := taskStageUnionBitmapHash(cacheCtx.ChainID(), core.TaskId, stage,
		assignment.CandidatePoolSnapshotId, slotCapacity, segmentBytes, union.UnionCount, segments)
	if err != nil || !bytes.Equal(existingHash[:], union.UnionBitmapHash) {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "worker union bitmap commitment mismatch")
	}
	for _, slot := range existingSlots {
		segmentIndex, byteIndex, mask, err := bitmapLocation(slot, slotCapacity, segmentBytes)
		if err != nil || segments[segmentIndex].Bitmap[byteIndex]&mask == 0 {
			return nil, errorsmod.Wrap(types.ErrInvariantBroken, "accepted worker fact is absent from the union bitmap")
		}
	}
	if union.AcceptedProposalCount >= params.Proposals.MaxCandidateAcceptedProposalsPerStage {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "worker proposal count exceeds the registered stage cap")
	}
	if len(newSlots) == 0 || uint64(union.UnionCount)+uint64(len(newSlots)) > uint64(params.Proposals.MaxCandidateUnionMembersPerStage) {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "worker proposal adds no member or exceeds the registered union cap")
	}
	addedBitmapHash, err := taskStageAddedBitmapHash(cacheCtx.ChainID(), core.TaskId, stage, proposalDigest[:], newSlots)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	touched := make(map[uint32]struct{}, len(newSlots))
	for _, slot := range newSlots {
		segmentIndex, byteIndex, mask, err := bitmapLocation(slot, slotCapacity, segmentBytes)
		if err != nil {
			return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
		}
		segments[segmentIndex].Bitmap[byteIndex] |= mask
		touched[segmentIndex] = struct{}{}
	}
	union.UnionCount += uint32(len(newSlots))
	union.AcceptedProposalCount++
	unionHash, err := taskStageUnionBitmapHash(cacheCtx.ChainID(), core.TaskId, stage,
		assignment.CandidatePoolSnapshotId, slotCapacity, segmentBytes, union.UnionCount, segments)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	union.UnionBitmapHash = unionHash[:]
	proposedReceipt.AcceptedHeight = currentHeight
	proposedReceipt.NewMemberCount = uint32(len(newSlots))

	for _, fact := range facts {
		if _, added := newSlotSet[fact.Slot]; !added {
			continue
		}
		if err := m.k.TaskCandidateFact.Set(cache, types.NewTaskCandidateFactKey(taskKey, stage, fact.Slot), fact); err != nil {
			return nil, err
		}
	}
	if err := m.k.acquireCandidateStageDuties(cache, facts, newSlotSet); err != nil {
		return nil, err
	}
	for segmentIndex := range touched {
		segment := types.TaskStageHandraiseUnionSegmentState{
			SchemaVersion: 1, TaskId: append([]byte(nil), core.TaskId...), Stage: stage,
			SegmentIndex: segmentIndex, Bitmap: segments[segmentIndex].Bitmap,
		}
		if err := m.k.TaskStageHandraiseUnionSegment.Set(cache, types.NewTaskStageSegmentKey(taskKey, stage, segmentIndex), segment); err != nil {
			return nil, err
		}
	}
	if err := m.k.TaskStageHandraiseUnion.Set(cache, stageKey, union); err != nil {
		return nil, err
	}
	if err := m.k.BuilderStageProposal.Set(cache, types.NewBuilderStageProposalKey(taskKey, stage, proposalDigest[:]), proposedReceipt); err != nil {
		return nil, err
	}
	// No Builder contribution is credited here. keeper_api_contract.md:3398 says a
	// fallback submitter adds none, and :3635 / keeper_data_structure_contract.md:1564
	// go further: Phase 0 does
	// not create BuilderContributionState at all, because ADR-0010 must freeze a
	// fresh stage/writer/window schema rather than have it back-derived from
	// Phase 0 events. The BuilderStageProposal receipt written just above, plus
	// the acceptance event below, are the audit trail.
	if err := emitTypedEvent(cache, &types.EventWorkerHandraisesAccepted{
		SessionId: append([]byte(nil), core.SessionId...), TaskId: append([]byte(nil), core.TaskId...),
		OrderSequence: core.OrderSequence, ProposalDigest: proposalDigest[:], AddedBitmapHash: addedBitmapHash[:],
		XBuilderOrEmpty: &types.EventWorkerHandraisesAccepted_BuilderOrEmpty{BuilderOrEmpty: builder},
		UnionCount:      union.UnionCount,
	}); err != nil {
		return nil, err
	}
	if err := m.k.recordTaskGasReimbursementIntent(
		cache, core.TaskId, 0,
		types.TaskReimbursementKindV1_TASK_REIMBURSEMENT_KIND_V1_WORKER_HANDRAISE,
	); err != nil {
		return nil, err
	}
	if proposalScope.isFallback {
		if err := m.k.finalizeOpenTaskCandidateUnion(cache, taskKey, currentHeight); err != nil {
			return nil, err
		}
		if err := removeDeadlineIndex(cache, m.k.AssignmentRandomnessIndex, taskKey, union.WindowCloseHeight); err != nil {
			return nil, err
		}
		union, err = m.k.TaskStageHandraiseUnion.Get(cache, stageKey)
		if err != nil {
			return nil, err
		}
	}
	write()
	return &types.MsgSubmitWorkerHandraisesResponse{
		TaskId: append([]byte(nil), core.TaskId...), ProposalDigest: proposalDigest[:],
		AddedMemberCount: uint32(len(newSlots)), UnionCount: union.UnionCount, StageStatus: union.Status,
	}, nil
}

// findAcceptedWorkerProposalReplay resolves replay identity from the immutable
// proposal receipt, never from live phase, window or eligibility state. The
// prefix scan is bounded by the registered accepted-proposal cap. A retained
// Builder proposal still has to be authorized by its proposer's current service
// account; the permissionless first-admission path retains no proposer, so its
// receipt replays on the digest alone and writes nothing either way.
func (k Keeper) findAcceptedWorkerProposalReplay(
	ctx context.Context,
	taskKey types.TaskKey,
	core types.TaskCoreState,
	assignment types.TaskAssignmentState,
	submitter string,
	handraises []candidateSigningDigest,
	maxProposals uint32,
) (*types.MsgSubmitWorkerHandraisesResponse, bool, error) {
	if maxProposals == 0 {
		return nil, false, fmt.Errorf("accepted worker proposal cap is zero")
	}
	stage := types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK
	// The (task_id, stage) super-prefix is an exact two-component match: Hash32's
	// non-terminal encoding is a fixed 32 bytes and the stage is a fixed-width
	// int32, so no other pair can share this bound as a prefix.
	iter, err := k.BuilderStageProposal.Iterate(ctx,
		collections.NewSuperPrefixedTripleRange[types.Hash32Key, int32, types.Hash32Key](taskKey, int32(stage)))
	if err != nil {
		return nil, false, err
	}
	defer iter.Close()

	visited := uint32(0)
	for ; iter.Valid(); iter.Next() {
		visited++
		if visited > maxProposals {
			return nil, false, fmt.Errorf("retained worker proposals exceed the registered cap")
		}
		key, err := iter.Key()
		if err != nil {
			return nil, false, err
		}
		retained, err := iter.Value()
		if err != nil {
			return nil, false, err
		}
		if !bytes.Equal(key.K1(), taskKey) || key.K2() != int32(stage) ||
			retained.SchemaVersion != 1 || !bytes.Equal(retained.TaskId, core.TaskId) || retained.Stage != stage ||
			len(retained.ProposalDigest) != types.Hash32Len || !bytes.Equal(key.K3(), retained.ProposalDigest) ||
			retained.AcceptedHeight == 0 || retained.NewMemberCount == 0 || retained.DataReadyAttestation {
			return nil, false, fmt.Errorf("retained worker proposal receipt is non-canonical")
		}
		proposalDigest, err := workerProposalDigest(
			sdk.UnwrapSDKContext(ctx).ChainID(), core.TaskId, core.AcceptedTaskHash,
			assignment.CandidatePoolSnapshotId, assignment.CandidatePoolHash, retained.ProposerOperator, handraises,
		)
		if err != nil {
			return nil, false, err
		}
		if !bytes.Equal(proposalDigest[:], retained.ProposalDigest) {
			continue
		}
		if retained.ProposerOperator != "" {
			if err := k.RequireCurrentServiceSubmitter(
				ctx, shared.ParticipantTypeBuilder, retained.ProposerOperator, submitter,
			); err != nil {
				return nil, false, fmt.Errorf("current service submitter does not authorize retained worker proposal: %w", err)
			}
		}
		union, err := k.TaskStageHandraiseUnion.Get(ctx, types.NewTaskStageKey(taskKey, stage))
		if err != nil {
			return nil, false, fmt.Errorf("retained worker union is unavailable: %w", err)
		}
		if union.SchemaVersion != 1 || union.Stage != stage || !bytes.Equal(union.TaskId, core.TaskId) ||
			!equalUnionHeaderScope(union, core.TaskId, assignment.CandidatePoolSnapshotId, assignment.CandidatePoolHash) ||
			union.AcceptedProposalCount == 0 || union.UnionCount < retained.NewMemberCount ||
			len(union.UnionBitmapHash) != types.Hash32Len {
			return nil, false, fmt.Errorf("retained worker union does not cover proposal receipt")
		}
		switch union.Status {
		case types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_OPEN,
			types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_FINALIZING,
			types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_FINALIZED:
		default:
			return nil, false, fmt.Errorf("retained worker union has an invalid status")
		}
		// A replay reports the retained first-accept member count, not a recount at
		// the current height, and adds nothing to the union.
		return &types.MsgSubmitWorkerHandraisesResponse{
			TaskId: append([]byte(nil), core.TaskId...), ProposalDigest: append([]byte(nil), retained.ProposalDigest...),
			AddedMemberCount: 0, UnionCount: union.UnionCount, StageStatus: union.Status,
		}, true, nil
	}
	return nil, false, nil
}

// validateWorkerHandraiseEnvelope checks everything about one handraise that is
// decidable from the request plus the Task scope, before any state is read. The
// expiry height is deliberately not checked here; see the caller.
func validateWorkerHandraiseEnvelope(chainID string, taskID, taskHash []byte, handraise *types.WorkerHandraiseV1) error {
	if handraise == nil || handraise.SchemaVersion != types.WorkerHandraiseSchemaVersionV1 ||
		handraise.ChainId != chainID || !bytes.Equal(handraise.TaskId, taskID) || !bytes.Equal(handraise.TaskHash, taskHash) ||
		handraise.ModelId == "" || handraise.ProfileVersion == 0 ||
		len(handraise.Member.CandidatePoolSnapshotId) != types.Hash32Len || handraise.Member.SlotVersion == 0 ||
		handraise.Duty != shared.Duty_DUTY_WORKER || handraise.ExpiryHeight == 0 ||
		len(handraise.ServiceSignature) != 64 {
		return fmt.Errorf("worker handraise envelope is invalid")
	}
	if _, err := types.CanonicalOperatorAddressBytes("worker_operator_address", handraise.Member.OperatorAddress); err != nil {
		return err
	}
	if _, err := types.WorkerHandraiseSigningDigest(*handraise); err != nil {
		return err
	}
	return nil
}
