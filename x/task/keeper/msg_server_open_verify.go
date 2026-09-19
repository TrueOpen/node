package keeper

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// SubmitVerifierHandraises atomically admits a Builder-relayed proposal or the
// winner Worker's late self-rescue into the frozen verifier union.
func (m msgServer) SubmitVerifierHandraises(ctx context.Context, msg *types.MsgSubmitVerifierHandraises) (*types.MsgSubmitVerifierHandraisesResponse, error) {
	if msg == nil || len(msg.TaskId) != types.Hash32Len {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "task_id must be Hash32")
	}
	if len(msg.Handraises) == 0 {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier proposal must contain a handraise")
	}
	if _, _, err := m.k.canonicalAddress("submitter_address", msg.SubmitterAddress); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, err.Error())
	}
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
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier proposal exceeds the registered item or byte cap")
	}
	var previousSlot uint32
	var scope *types.VerifierHandraiseV1
	seenOperators := make(map[string]struct{}, len(msg.Handraises))
	for index := range msg.Handraises {
		handraise := &msg.Handraises[index]
		// Expiry is a first-admission gate. A persisted exact replay is identified
		// by its immutable proposal receipt before mutable phase/time checks.
		if err := validateVerifierHandraiseEnvelope(sdkCtx.ChainID(), msg.TaskId, 0, handraise); err != nil {
			return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, err.Error())
		}
		if index != 0 && handraise.Member.Slot <= previousSlot {
			return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier handraises must be strictly slot-ascending and unique")
		}
		previousSlot = handraise.Member.Slot
		if _, duplicate := seenOperators[handraise.Member.OperatorAddress]; duplicate {
			return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier handraises must have unique operators")
		}
		seenOperators[handraise.Member.OperatorAddress] = struct{}{}
		if scope == nil {
			scope = handraise
			continue
		}
		if handraise.VerifyRound != scope.VerifyRound || !bytes.Equal(handraise.InferReceiptHash, scope.InferReceiptHash) ||
			!bytes.Equal(handraise.OutputHash, scope.OutputHash) || handraise.ModelId != scope.ModelId ||
			handraise.ProfileVersion != scope.ProfileVersion ||
			!bytes.Equal(handraise.Member.CandidatePoolSnapshotId, scope.Member.CandidatePoolSnapshotId) {
			return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier handraises do not share one frozen task-round scope")
		}
	}
	handraiseDigests := make([]candidateSigningDigest, len(msg.Handraises))
	for index, handraise := range msg.Handraises {
		digest, err := types.VerifierHandraiseSigningDigest(handraise)
		if err != nil {
			return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, err.Error())
		}
		handraiseDigests[index] = candidateSigningDigest{Slot: handraise.Member.Slot, Digest: digest[:]}
	}

	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	taskKey := types.NewTaskKey(msg.TaskId)
	core, err := m.k.TaskCore.Get(cache, taskKey)
	if err != nil || !bytes.Equal(core.TaskId, msg.TaskId) || len(core.AcceptedTaskHash) != types.Hash32Len {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier proposal task scope is unavailable")
	}
	assignment, err := m.k.TaskAssignment.Get(cache, taskKey)
	if err != nil || !bytes.Equal(assignment.TaskId, msg.TaskId) || assignment.WinnerWorker == "" ||
		len(assignment.CandidatePoolSnapshotId) != types.Hash32Len || len(assignment.CandidatePoolHash) != types.Hash32Len ||
		assignment.CandidatePoolRefReleased {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "verifier proposal assignment scope is unavailable")
	}
	receipt, err := m.k.InferReceipt.Get(cache, taskKey)
	if err != nil || !bytes.Equal(receipt.TaskId, msg.TaskId) || len(receipt.InferReceiptHash) != types.Hash32Len ||
		len(receipt.OutputHash) != types.Hash32Len {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "accepted infer receipt is unavailable")
	}
	verifyRound := scope.VerifyRound
	window, err := m.k.VerifierCandidateWindow.Get(cache, types.NewVerifyRoundKey(taskKey, verifyRound))
	if err != nil || !bytes.Equal(window.TaskId, msg.TaskId) || window.VerifyRound != verifyRound ||
		!bytes.Equal(window.InferReceiptHash, receipt.InferReceiptHash) ||
		!bytes.Equal(window.CandidatePoolSnapshotId, assignment.CandidatePoolSnapshotId) ||
		!bytes.Equal(window.CandidatePoolHash, assignment.CandidatePoolHash) {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier proposal window scope is unavailable")
	}
	if scope.VerifyRound != window.VerifyRound || !bytes.Equal(scope.InferReceiptHash, receipt.InferReceiptHash) ||
		!bytes.Equal(scope.OutputHash, receipt.OutputHash) || scope.ModelId != core.ModelId ||
		scope.ProfileVersion != core.ProfileVersion ||
		!bytes.Equal(scope.Member.CandidatePoolSnapshotId, window.CandidatePoolSnapshotId) {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier proposal does not match the frozen task scope")
	}

	stage := types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY
	if replay, found, err := m.k.findAcceptedVerifierProposalReplay(
		cache, taskKey, core, assignment, window, msg.SubmitterAddress,
		handraiseDigests, params.Proposals.MaxCandidateAcceptedProposalsPerStage,
	); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, err.Error())
	} else if found {
		return replay, nil
	}

	if core.VerificationStatus != types.VerificationStatus_VERIFICATION_STATUS_VERIFY_COLLECTION_OPEN {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "task is not collecting verifier handraises")
	}
	if window.Status != types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY ||
		currentHeight < window.GeneratedHeight || currentHeight > window.HandraiseCloseHeight {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier handraise window is unavailable")
	}
	for index := range msg.Handraises {
		if currentHeight > msg.Handraises[index].ExpiryHeight {
			return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier handraise envelope is expired")
		}
	}

	stageKey := types.NewTaskStageKey(taskKey, stage)
	union, unionFound, err := m.k.loadOpenVerifierUnion(cache, stageKey, window)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	dataReady, builderIndex, proposerOperator, err := m.k.authorizeVerifierProposalSubmitter(
		cache, taskKey, assignment, &union, unionFound, window, msg.SubmitterAddress, currentHeight,
	)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, err.Error())
	}

	var attestation *bool
	if dataReady {
		value := true
		attestation = &value
	}
	proposalDigest, err := verifierProposalDigest(
		cacheCtx.ChainID(), msg.TaskId, core.AcceptedTaskHash, window.CandidatePoolSnapshotId,
		window.CandidatePoolHash, proposerOperator, attestation, handraiseDigests,
	)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, err.Error())
	}
	proposalKey := types.NewBuilderStageProposalKey(taskKey, stage, proposalDigest[:])
	proposedReceipt := types.BuilderStageProposalState{
		SchemaVersion: 1, TaskId: append([]byte(nil), msg.TaskId...), Stage: stage,
		ProposalDigest: proposalDigest[:], ProposerOperator: proposerOperator, DataReadyAttestation: dataReady,
	}

	members := make(map[uint32]types.VerifierCandidateWindowMemberState, window.WindowSize)
	for rank := uint32(0); rank < window.WindowSize; rank++ {
		member, err := m.k.VerifierCandidateWindowMember.Get(cache, types.NewVerifierWindowMemberKey(taskKey, verifyRound, rank))
		if err != nil || member.RankIndex != rank || !bytes.Equal(member.TaskId, msg.TaskId) || member.VerifyRound != verifyRound {
			return nil, errorsmod.Wrap(types.ErrInvariantBroken, "verifier window member body is unavailable")
		}
		members[member.Slot] = member
	}

	facts := make([]types.TaskCandidateFactState, len(msg.Handraises))
	newSlots := make([]uint32, 0, len(msg.Handraises))
	newSlotSet := make(map[uint32]struct{}, len(msg.Handraises))
	existingSlots := make([]uint32, 0, len(msg.Handraises))
	for index, handraise := range msg.Handraises {
		member, found := members[handraise.Member.Slot]
		if !found {
			return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier handraise member is outside the frozen window")
		}
		fact, err := m.k.freezeVerifierCandidateFact(cache, core, assignment, receipt, window, member, handraise, currentHeight)
		if err != nil {
			return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, err.Error())
		}
		facts[index] = fact
		factKey := types.NewTaskCandidateFactKey(taskKey, stage, fact.Slot)
		if existing, err := m.k.TaskCandidateFact.Get(cache, factKey); err == nil {
			if !proto.Equal(&existing, &fact) {
				return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier slot already has a conflicting accepted fact")
			}
			existingSlots = append(existingSlots, fact.Slot)
		} else if errIsNotFound(err) {
			newSlots = append(newSlots, fact.Slot)
			newSlotSet[fact.Slot] = struct{}{}
		} else {
			return nil, err
		}
	}
	slotCapacity, segmentBytes, segmentCount, ok := m.k.hubKeeper.GetCandidatePoolLayout(cache, window.CandidatePoolSnapshotId)
	if !ok || slotCapacity == 0 || segmentBytes == 0 || segmentCount == 0 {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "candidate pool layout is unavailable")
	}
	segments, err := m.k.loadTaskUnionSegments(cache, taskKey, stage, slotCapacity, segmentBytes, segmentCount)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	if unionFound {
		existingHash, err := taskStageUnionBitmapHash(cacheCtx.ChainID(), msg.TaskId, stage, window.CandidatePoolSnapshotId,
			slotCapacity, segmentBytes, union.UnionCount, segments)
		if err != nil || !bytes.Equal(existingHash[:], union.UnionBitmapHash) {
			return nil, errorsmod.Wrap(types.ErrInvariantBroken, "verifier union bitmap commitment mismatch")
		}
	}
	for _, slot := range existingSlots {
		segmentIndex, byteIndex, mask, err := bitmapLocation(slot, slotCapacity, segmentBytes)
		if err != nil || segments[segmentIndex].Bitmap[byteIndex]&mask == 0 {
			return nil, errorsmod.Wrap(types.ErrInvariantBroken, "accepted verifier fact is absent from the union bitmap")
		}
	}
	if existing, err := m.k.BuilderStageProposal.Get(cache, proposalKey); err == nil {
		if replay, replayErr := verifierProposalExactReplay(existing, proposedReceipt); replayErr != nil || !replay {
			return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "conflicting verifier proposal replay")
		}
		return &types.MsgSubmitVerifierHandraisesResponse{
			TaskId: msg.TaskId, VerifyRound: verifyRound, ProposalDigest: proposalDigest[:], UnionCount: union.UnionCount,
		}, nil
	} else if !errIsNotFound(err) {
		return nil, err
	}
	if !dataReady && unionFound && union.AcceptedProposalCount != 0 {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "winner self-rescue is unavailable after an accepted Builder proposal")
	}
	if union.AcceptedProposalCount >= params.Proposals.MaxCandidateAcceptedProposalsPerStage {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier proposal count exceeds the registered stage cap")
	}
	if len(newSlots) == 0 || uint64(union.UnionCount)+uint64(len(newSlots)) > uint64(params.Proposals.MaxCandidateUnionMembersPerStage) {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier proposal adds no member or exceeds the registered union cap")
	}
	addedBitmapHash, err := taskStageAddedBitmapHash(cacheCtx.ChainID(), msg.TaskId, stage, proposalDigest[:], newSlots)
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
	unionHash, err := taskStageUnionBitmapHash(cacheCtx.ChainID(), msg.TaskId, stage, window.CandidatePoolSnapshotId,
		slotCapacity, segmentBytes, union.UnionCount, segments)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	union.UnionBitmapHash = unionHash[:]
	if dataReady {
		union.DataReadyAttestingBuilderBitmap[builderIndex/8] |= byte(1 << (builderIndex % 8))
	}
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
			SchemaVersion: 1, TaskId: append([]byte(nil), msg.TaskId...), Stage: stage,
			SegmentIndex: segmentIndex, Bitmap: segments[segmentIndex].Bitmap,
		}
		if err := m.k.TaskStageHandraiseUnionSegment.Set(cache, types.NewTaskStageSegmentKey(taskKey, stage, segmentIndex), segment); err != nil {
			return nil, err
		}
	}
	if err := m.k.TaskStageHandraiseUnion.Set(cache, stageKey, union); err != nil {
		return nil, err
	}
	if err := m.k.BuilderStageProposal.Set(cache, proposalKey, proposedReceipt); err != nil {
		return nil, err
	}
	actor := assignment.WinnerWorker
	if dataReady {
		actor = proposerOperator
	}
	if err := emitTypedEvent(cache, &types.EventVerifierHandraisesAccepted{
		TaskId: msg.TaskId, VerifyRound: verifyRound, ProposalDigest: proposalDigest[:],
		AddedBitmapHash: addedBitmapHash[:], BuilderOrWorker: actor, UnionCount: union.UnionCount,
	}); err != nil {
		return nil, err
	}
	if verifyRound == types.VerifyRoundV1 {
		if err := m.k.recordTaskGasReimbursementIntent(cache, msg.TaskId, 0,
			types.TaskReimbursementKindV1_TASK_REIMBURSEMENT_KIND_V1_VERIFIER_HANDRAISE); err != nil {
			return nil, err
		}
	}
	write()
	return &types.MsgSubmitVerifierHandraisesResponse{
		TaskId: msg.TaskId, VerifyRound: verifyRound, ProposalDigest: proposalDigest[:],
		AddedMemberCount: uint32(len(newSlots)), UnionCount: union.UnionCount,
	}, nil
}

// findAcceptedVerifierProposalReplay resolves replay identity from the retained
// proposal receipt, not from mutable current-service bindings. The prefix is
// bounded by the registered accepted-proposal cap. Once a digest matches, the
// retained proposer (or winner Worker for self-rescue) must still authorize the
// current Cosmos submitter; no live phase, deadline, eligibility or old service
// signature is reinterpreted.
func (k Keeper) findAcceptedVerifierProposalReplay(
	ctx context.Context,
	taskKey types.TaskKey,
	core types.TaskCoreState,
	assignment types.TaskAssignmentState,
	window types.VerifierCandidateWindowState,
	submitter string,
	handraises []candidateSigningDigest,
	maxProposals uint32,
) (*types.MsgSubmitVerifierHandraisesResponse, bool, error) {
	if maxProposals == 0 {
		return nil, false, fmt.Errorf("accepted verifier proposal cap is zero")
	}
	stage := types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY
	// The (task_id, stage) super-prefix is still an exact two-component match:
	// Hash32KeyCodec's non-terminal encoding is a fixed 32 bytes and the stage is
	// a fixed-width int32, so no other (task_id, stage) pair can share the
	// 36-byte bound as a prefix the way a variable-length key could.
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
			return nil, false, fmt.Errorf("retained verifier proposals exceed the registered cap")
		}
		key, err := iter.Key()
		if err != nil {
			return nil, false, err
		}
		existing, err := iter.Value()
		if err != nil {
			return nil, false, err
		}
		if !bytes.Equal(key.K1(), taskKey) || key.K2() != int32(stage) ||
			existing.SchemaVersion != 1 || !bytes.Equal(existing.TaskId, core.TaskId) || existing.Stage != stage ||
			len(existing.ProposalDigest) != types.Hash32Len || !bytes.Equal(key.K3(), existing.ProposalDigest) ||
			existing.AcceptedHeight == 0 || existing.NewMemberCount == 0 {
			return nil, false, fmt.Errorf("retained verifier proposal receipt is non-canonical")
		}

		var attestation *bool
		participantType := shared.ParticipantTypeCortexNode
		actor := assignment.WinnerWorker
		if window.VerifyRound == types.ChallengeVerifyRoundV1 {
			if existing.DataReadyAttestation || existing.ProposerOperator == "" {
				return nil, false, fmt.Errorf("challenge verifier proposal has an invalid retained proposer")
			}
			participantType = shared.ParticipantTypeBuilder
			actor = existing.ProposerOperator
		} else if existing.DataReadyAttestation {
			value := true
			attestation = &value
			participantType = shared.ParticipantTypeBuilder
			actor = existing.ProposerOperator
		} else if existing.ProposerOperator != "" {
			return nil, false, fmt.Errorf("self-rescue verifier proposal has a retained proposer")
		}
		proposalDigest, err := verifierProposalDigest(
			sdk.UnwrapSDKContext(ctx).ChainID(), core.TaskId, core.AcceptedTaskHash,
			window.CandidatePoolSnapshotId, window.CandidatePoolHash,
			existing.ProposerOperator, attestation, handraises,
		)
		if err != nil {
			return nil, false, err
		}
		if !bytes.Equal(proposalDigest[:], existing.ProposalDigest) {
			continue
		}
		proposed := types.BuilderStageProposalState{
			SchemaVersion: 1, TaskId: append([]byte(nil), core.TaskId...), Stage: stage,
			ProposalDigest: proposalDigest[:], ProposerOperator: existing.ProposerOperator,
			DataReadyAttestation: existing.DataReadyAttestation,
		}
		if replay, replayErr := verifierProposalExactReplay(existing, proposed); replayErr != nil || !replay {
			return nil, false, fmt.Errorf("conflicting verifier proposal replay")
		}
		if actor == "" {
			return nil, false, fmt.Errorf("retained verifier proposal actor is unavailable")
		}
		if participantType == shared.ParticipantTypeBuilder {
			selection, err := k.loadActiveTaskBuilderSelection(ctx, taskKey, core.TaskId)
			if err != nil {
				return nil, false, fmt.Errorf("retained verifier proposal Builder selection is unavailable")
			}
			selected := false
			for _, builder := range selection.SelectedTaskBuilders {
				if builder == actor {
					selected = true
					break
				}
			}
			if !selected {
				return nil, false, fmt.Errorf("retained verifier proposal proposer is not a selected Task Builder")
			}
		}
		if err := k.RequireCurrentServiceSubmitter(ctx, participantType, actor, submitter); err != nil {
			return nil, false, fmt.Errorf("current service submitter does not authorize retained verifier proposal: %w", err)
		}
		union, err := k.TaskStageHandraiseUnion.Get(ctx, types.NewTaskStageKey(taskKey, stage))
		if err != nil {
			return nil, false, fmt.Errorf("retained verifier union is unavailable: %w", err)
		}
		if union.SchemaVersion != 1 || union.Stage != stage || !bytes.Equal(union.TaskId, core.TaskId) ||
			!bytes.Equal(union.CandidatePoolSnapshotId, window.CandidatePoolSnapshotId) ||
			!bytes.Equal(union.CandidatePoolHash, window.CandidatePoolHash) ||
			union.AcceptedProposalCount == 0 || union.UnionCount < existing.NewMemberCount || len(union.UnionBitmapHash) != types.Hash32Len {
			return nil, false, fmt.Errorf("retained verifier union does not cover proposal receipt")
		}
		switch union.Status {
		case types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_OPEN,
			types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_FINALIZING,
			types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_FINALIZED:
		default:
			return nil, false, fmt.Errorf("retained verifier union has an invalid status")
		}
		return &types.MsgSubmitVerifierHandraisesResponse{
			TaskId: append([]byte(nil), core.TaskId...), VerifyRound: window.VerifyRound,
			ProposalDigest: append([]byte(nil), existing.ProposalDigest...), UnionCount: union.UnionCount,
		}, true, nil
	}
	return nil, false, nil
}

func (k Keeper) loadOpenVerifierUnion(
	ctx context.Context,
	stageKey types.TaskStageKey,
	window types.VerifierCandidateWindowState,
) (types.TaskStageHandraiseUnionState, bool, error) {
	union, err := k.TaskStageHandraiseUnion.Get(ctx, stageKey)
	if err == nil {
		if union.SchemaVersion != 1 || union.Stage != types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY ||
			union.Status != types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_OPEN ||
			!equalUnionHeaderScope(union, window.TaskId, window.CandidatePoolSnapshotId, window.CandidatePoolHash) ||
			union.WindowCloseHeight != window.HandraiseCloseHeight ||
			union.GetSelectionRandomnessHeight() != window.SelectionRandomnessHeight || len(union.UnionBitmapHash) != types.Hash32Len {
			return types.TaskStageHandraiseUnionState{}, false, fmt.Errorf("verifier union header is non-canonical")
		}
		return union, true, nil
	}
	if !errIsNotFound(err) {
		return types.TaskStageHandraiseUnionState{}, false, err
	}
	return types.TaskStageHandraiseUnionState{
		SchemaVersion: 1, TaskId: append([]byte(nil), window.TaskId...),
		Stage:                   types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY,
		CandidatePoolSnapshotId: append([]byte(nil), window.CandidatePoolSnapshotId...),
		CandidatePoolHash:       append([]byte(nil), window.CandidatePoolHash...),
		Status:                  types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_OPEN,
		WindowCloseHeight:       window.HandraiseCloseHeight,
		XSelectionRandomnessHeight: &types.TaskStageHandraiseUnionState_SelectionRandomnessHeight{
			SelectionRandomnessHeight: window.SelectionRandomnessHeight,
		},
	}, false, nil
}

func (k Keeper) authorizeVerifierProposalSubmitter(
	ctx context.Context,
	taskKey types.TaskKey,
	assignment types.TaskAssignmentState,
	union *types.TaskStageHandraiseUnionState,
	unionFound bool,
	window types.VerifierCandidateWindowState,
	submitter string,
	currentHeight uint64,
) (bool, uint32, string, error) {
	selection, err := k.loadActiveTaskBuilderSelection(ctx, taskKey, window.TaskId)
	if err != nil {
		return false, 0, "", fmt.Errorf("active Task Builder selection is unavailable")
	}
	bitmapBytes := (len(selection.SelectedTaskBuilders) + 7) / 8
	if unionFound {
		if len(union.DataReadyAttestingBuilderBitmap) != bitmapBytes {
			return false, 0, "", fmt.Errorf("verifier union Builder bitmap width mismatch")
		}
	} else {
		union.DataReadyAttestingBuilderBitmap = make([]byte, bitmapBytes)
	}
	if currentHeight <= window.BuilderProposalCloseHeight {
		for index, builder := range selection.SelectedTaskBuilders {
			if err := k.RequireCurrentServiceSubmitter(ctx, shared.ParticipantTypeBuilder, builder, submitter); err == nil {
				// Challenge rounds have no new data-ready declaration. The same
				// selected Builder carrier is reused, but only round 1 contributes
				// availability attesters.
				return window.VerifyRound == types.VerifyRoundV1, uint32(index), builder, nil
			}
		}
		return false, 0, "", fmt.Errorf("submitter is not a selected Task Builder current service")
	}
	if err := k.RequireCurrentServiceSubmitter(
		ctx, shared.ParticipantTypeCortexNode, assignment.WinnerWorker, submitter,
	); err != nil {
		return false, 0, "", fmt.Errorf("submitter is not the winner Worker current service")
	}
	return false, 0, "", nil
}

func (k Keeper) loadTaskUnionSegments(
	ctx context.Context,
	taskKey types.TaskKey,
	stage types.TaskCandidateStage,
	slotCapacity, segmentBytes, segmentCount uint32,
) ([]taskBitmapSegment, error) {
	if len(taskKey) != types.Hash32Len {
		return nil, fmt.Errorf("task union key is invalid")
	}
	expectedCount, err := bitmapSegmentCount(slotCapacity, segmentBytes)
	if err != nil || expectedCount != segmentCount {
		return nil, fmt.Errorf("candidate pool bitmap layout is inconsistent")
	}
	segments := make([]taskBitmapSegment, segmentCount)
	for segmentIndex := uint32(0); segmentIndex < segmentCount; segmentIndex++ {
		segments[segmentIndex] = taskBitmapSegment{Index: segmentIndex, Bitmap: make([]byte, segmentBytes)}
		stored, err := k.TaskStageHandraiseUnionSegment.Get(ctx, types.NewTaskStageSegmentKey(taskKey, stage, segmentIndex))
		if err == nil {
			if stored.SchemaVersion != 1 || !bytes.Equal(stored.TaskId, taskKey) || stored.Stage != stage ||
				stored.SegmentIndex != segmentIndex || uint32(len(stored.Bitmap)) != segmentBytes {
				return nil, fmt.Errorf("task union segment %d is non-canonical", segmentIndex)
			}
			segments[segmentIndex].Bitmap = append([]byte(nil), stored.Bitmap...)
		} else if !errIsNotFound(err) {
			return nil, err
		}
	}
	if err := validateBitmapTrailingBits(slotCapacity, segmentBytes, segments); err != nil {
		return nil, err
	}
	return segments, nil
}

func validateVerifierHandraiseEnvelope(chainID string, taskID []byte, currentHeight uint64, handraise *types.VerifierHandraiseV1) error {
	if handraise == nil || handraise.SchemaVersion != types.VerifierHandraiseSchemaVersionV1 ||
		handraise.ChainId != chainID || !bytes.Equal(handraise.TaskId, taskID) || !isPhase0VerifyRound(handraise.VerifyRound) ||
		len(handraise.InferReceiptHash) != types.Hash32Len || len(handraise.OutputHash) != types.Hash32Len ||
		handraise.ModelId == "" || handraise.ProfileVersion == 0 ||
		len(handraise.Member.CandidatePoolSnapshotId) != types.Hash32Len || handraise.Member.SlotVersion == 0 ||
		handraise.Duty != shared.Duty_DUTY_VERIFIER || handraise.ExpiryHeight == 0 || currentHeight > handraise.ExpiryHeight ||
		len(handraise.ServiceSignature) != 64 {
		return fmt.Errorf("verifier handraise envelope is invalid")
	}
	if _, err := types.CanonicalOperatorAddressBytes("verifier_operator_address", handraise.Member.OperatorAddress); err != nil {
		return err
	}
	if _, err := types.VerifierHandraiseSigningDigest(*handraise); err != nil {
		return err
	}
	return nil
}

func verifierProposalDigest(
	chainID string,
	taskID, taskHash, snapshotID, poolHash []byte,
	proposerOperator string,
	dataReadyAttestation *bool,
	handraises []candidateSigningDigest,
) ([32]byte, error) {
	var zero [32]byte
	if chainID == "" || len(taskID) != types.Hash32Len || len(taskHash) != types.Hash32Len ||
		len(snapshotID) != types.Hash32Len || len(poolHash) != types.Hash32Len || len(handraises) == 0 {
		return zero, fmt.Errorf("open verify proposal scope is incomplete")
	}
	var proposer []byte
	if proposerOperator != "" {
		var err error
		proposer, err = types.CanonicalOperatorAddressBytes("proposer_operator", proposerOperator)
		if err != nil {
			return zero, err
		}
	}
	var attestation []byte
	if dataReadyAttestation != nil {
		if !*dataReadyAttestation {
			return zero, fmt.Errorf("present data-ready attestation must be true")
		}
		attestation = []byte{1}
	}
	ordered := append([]candidateSigningDigest(nil), handraises...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Slot < ordered[j].Slot })
	elements := make([]shared.CanonicalFieldV1, 0, len(ordered))
	builder := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainOpenVerifyProposalV1)).Raw(
		[]byte(chainID), taskID, taskHash,
		shared.EnumBE(uint32(types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY)),
		snapshotID, poolHash, proposer, attestation, shared.Uint32BE(uint32(len(ordered))),
	)
	for index, handraise := range ordered {
		if len(handraise.Digest) != types.Hash32Len || (index != 0 && handraise.Slot == ordered[index-1].Slot) {
			return zero, fmt.Errorf("open verify handraise digests are invalid or duplicate")
		}
		elements = append(elements, shared.RawCanonicalFieldV1(handraise.Digest))
	}
	digest, err := builder.Nested(shared.CanonicalRepeatedFieldsV1(elements)).Sum()
	if err != nil {
		return zero, err
	}
	return [32]byte(digest), nil
}

// verifierProposalExactReplay accepts only the request-derived scope committed by
// proposal_digest. accepted_height and new_member_count are first-accept state,
// not replay inputs; a later exact replay preserves them without a write.
func verifierProposalExactReplay(existing, proposed types.BuilderStageProposalState) (bool, error) {
	if existing.Stage != types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY ||
		proposed.Stage != types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY ||
		len(existing.ProposalDigest) != types.Hash32Len || len(proposed.ProposalDigest) != types.Hash32Len ||
		!bytes.Equal(existing.ProposalDigest, proposed.ProposalDigest) || existing.AcceptedHeight == 0 || existing.NewMemberCount == 0 {
		return false, fmt.Errorf("open verify proposal replay digest mismatch")
	}
	if existing.SchemaVersion != proposed.SchemaVersion || !bytes.Equal(existing.TaskId, proposed.TaskId) ||
		existing.ProposerOperator != proposed.ProposerOperator || existing.DataReadyAttestation != proposed.DataReadyAttestation {
		return false, fmt.Errorf("open verify proposal digest conflicts with persisted receipt")
	}
	return true, nil
}
