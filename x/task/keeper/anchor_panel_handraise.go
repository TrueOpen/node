package keeper

import (
	"bytes"
	"context"
	"errors"

	errorsmod "cosmossdk.io/errors"
	"github.com/cosmos/gogoproto/proto"

	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// submitAnchorPanelHandraises accepts one Builder proposal of hand-raises for a
// Verifier-panel round. The caller has already checked the envelopes, that all
// hand-raises share one scope, and computed their signing digests.
//
// It follows SubmitVerifierHandraises step for step with three differences:
// the round's working row replaces the round-keyed Verifier stores; only a
// selected Task Builder may relay (the Worker under audit never relays for its
// own panel, so there is no self-rescue window); and a hand-raise is admitted
// on the panel eligibility rule.
func (m msgServer) submitAnchorPanelHandraises(
	ctx context.Context,
	msg *types.MsgSubmitVerifierHandraises,
	selection anchorPanelSelection,
	handraiseDigests []candidateSigningDigest,
	currentHeight uint64,
) (*types.MsgSubmitVerifierHandraisesResponse, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	taskKey := types.NewTaskKey(msg.TaskId)
	scope := &msg.Handraises[0]
	window := selection.Window
	stage := types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY

	core, err := m.k.TaskCore.Get(cache, taskKey)
	if err != nil || !bytes.Equal(core.TaskId, msg.TaskId) || len(core.AcceptedTaskHash) != types.Hash32Len {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier proposal task scope is unavailable")
	}
	assignment, err := m.k.ReadTaskAssignment(cache, taskKey)
	if err != nil || !bytes.Equal(assignment.TaskId, msg.TaskId) || assignment.WinnerWorker == "" ||
		!bytes.Equal(assignment.CandidatePoolSnapshotId, window.CandidatePoolSnapshotId) ||
		!bytes.Equal(assignment.CandidatePoolHash, window.CandidatePoolHash) || assignment.CandidatePoolRefReleased {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "panel proposal assignment scope is unavailable")
	}
	receipt, err := m.k.ReadInferReceipt(cache, taskKey)
	if err != nil || !bytes.Equal(receipt.TaskId, msg.TaskId) || !bytes.Equal(receipt.InferReceiptHash, window.InferReceiptHash) ||
		len(receipt.OutputHash) != types.Hash32Len {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "panel proposal receipt scope is unavailable")
	}
	if scope.VerifyRound != window.VerifyRound || !bytes.Equal(scope.InferReceiptHash, receipt.InferReceiptHash) ||
		!bytes.Equal(scope.OutputHash, receipt.OutputHash) || !bytes.Equal(scope.ModelId, core.ModelId) ||
		scope.ProfileVersion != core.ProfileVersion ||
		!bytes.Equal(scope.Member.CandidatePoolSnapshotId, window.CandidatePoolSnapshotId) {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier proposal does not match the frozen task scope")
	}
	params, err := m.k.ParamsForTask(cache, taskKey)
	if err != nil {
		return nil, err
	}

	// Only a selected Task Builder relays panel hand-raises.
	builderSelection, err := m.k.loadActiveTaskBuilderSelection(cache, taskKey, msg.TaskId)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "active Task Builder selection is unavailable")
	}
	proposerOperator := ""
	for _, builder := range builderSelection.SelectedTaskBuilders {
		if err := m.k.RequireCurrentServiceSubmitter(cache, shared.ParticipantTypeBuilder, builder, msg.SubmitterAddress); err == nil {
			proposerOperator = builder
			break
		}
	}
	if proposerOperator == "" {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "submitter is not a selected Task Builder current service")
	}
	proposalDigest, err := verifierProposalDigest(
		cacheCtx.ChainID(), msg.TaskId, core.AcceptedTaskHash, window.CandidatePoolSnapshotId,
		window.CandidatePoolHash, proposerOperator, nil, handraiseDigests,
	)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, err.Error())
	}
	unionCount := uint32(len(selection.Facts))
	for _, existing := range selection.Proposals {
		if bytes.Equal(existing.ProposalDigest, proposalDigest[:]) {
			// Exact replay of an accepted proposal: same answer, no write.
			return &types.MsgSubmitVerifierHandraisesResponse{
				TaskId: msg.TaskId, VerifyRound: window.VerifyRound, ProposalDigest: proposalDigest[:], UnionCount: unionCount,
			}, nil
		}
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
	if uint32(len(selection.Proposals)) >= params.Proposals.MaxCandidateAcceptedProposalsPerStage {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier proposal count exceeds the registered stage cap")
	}

	members := make(map[uint32]types.VerifierCandidateWindowMemberState, len(selection.Members))
	for _, member := range selection.Members {
		members[member.Slot] = member
	}
	existingFacts := make(map[uint32]types.TaskCandidateFactState, len(selection.Facts))
	for _, fact := range selection.Facts {
		existingFacts[fact.Slot] = fact
	}
	// A hand-raise that stopped applying after it was signed is skipped so it
	// cannot invalidate the other candidates of the same proposal.
	added := make([]types.TaskCandidateFactState, 0, len(msg.Handraises))
	newSlots := make([]uint32, 0, len(msg.Handraises))
	newSlotSet := make(map[uint32]struct{}, len(msg.Handraises))
	for _, handraise := range msg.Handraises {
		member, found := members[handraise.Member.Slot]
		if !found {
			continue
		}
		fact, err := m.k.freezeAnchorPanelCandidateFact(cache, core, assignment, receipt, window, member, handraise, params, currentHeight)
		if err != nil {
			if errors.Is(err, errCandidateNotApplicable) {
				continue
			}
			return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, err.Error())
		}
		if existing, accepted := existingFacts[fact.Slot]; accepted {
			if !proto.Equal(&existing, &fact) {
				return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier slot already has a conflicting accepted fact")
			}
			continue
		}
		added = append(added, fact)
		newSlots = append(newSlots, fact.Slot)
		newSlotSet[fact.Slot] = struct{}{}
	}
	if len(newSlots) == 0 || uint64(unionCount)+uint64(len(newSlots)) > uint64(params.Proposals.MaxCandidateUnionMembersPerStage) {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier proposal adds no member or exceeds the registered union cap")
	}
	slotCapacity, segmentBytes, segmentCount, ok := m.k.hubKeeper.GetCandidatePoolLayout(cache, window.CandidatePoolSnapshotId)
	if !ok || slotCapacity == 0 || segmentBytes == 0 || segmentCount == 0 {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "candidate pool layout is unavailable")
	}
	addedBitmapHash, err := taskStageAddedBitmapHash(cacheCtx.ChainID(), msg.TaskId, stage, proposalDigest[:], newSlots)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	selection.Facts = sortedAnchorPanelFacts(append(selection.Facts, added...))
	segments, err := anchorPanelUnionSegments(selection.Facts, slotCapacity, segmentBytes, segmentCount)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	unionCount = uint32(len(selection.Facts))
	unionHash, err := taskStageUnionBitmapHash(cacheCtx.ChainID(), msg.TaskId, stage, window.CandidatePoolSnapshotId,
		slotCapacity, segmentBytes, unionCount, segments)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	if selection.Union == nil {
		selection.Union = &types.TaskStageHandraiseUnionState{
			SchemaVersion: 1, TaskId: append([]byte(nil), msg.TaskId...), Stage: stage,
			CandidatePoolSnapshotId: append([]byte(nil), window.CandidatePoolSnapshotId...),
			CandidatePoolHash:       append([]byte(nil), window.CandidatePoolHash...),
			Status:                  types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_OPEN,
			WindowCloseHeight:       window.HandraiseCloseHeight,
			XSelectionRandomnessHeight: &types.TaskStageHandraiseUnionState_SelectionRandomnessHeight{
				SelectionRandomnessHeight: window.SelectionRandomnessHeight,
			},
			DataReadyAttestingBuilderBitmap: make([]byte, (len(builderSelection.SelectedTaskBuilders)+7)/8),
		}
	}
	selection.Union.UnionCount = unionCount
	selection.Union.AcceptedProposalCount++
	selection.Union.UnionBitmapHash = unionHash[:]
	selection.Proposals = append(selection.Proposals, types.BuilderStageProposalState{
		SchemaVersion: 1, TaskId: append([]byte(nil), msg.TaskId...), Stage: stage,
		ProposalDigest: proposalDigest[:], ProposerOperator: proposerOperator,
		AcceptedHeight: currentHeight, NewMemberCount: uint32(len(newSlots)),
	})
	if err := m.k.acquireCandidateStageDuties(cache, added, newSlotSet); err != nil {
		return nil, err
	}
	if err := m.k.saveAnchorPanelSelection(cache, taskKey, selection); err != nil {
		return nil, err
	}
	if err := emitTypedEvent(cache, &types.EventVerifierHandraisesAccepted{
		TaskId: msg.TaskId, VerifyRound: window.VerifyRound, ProposalDigest: proposalDigest[:],
		AddedBitmapHash: addedBitmapHash[:], BuilderOrWorker: proposerOperator, UnionCount: unionCount,
	}); err != nil {
		return nil, err
	}
	write()
	return &types.MsgSubmitVerifierHandraisesResponse{
		TaskId: msg.TaskId, VerifyRound: window.VerifyRound, ProposalDigest: proposalDigest[:],
		AddedMemberCount: uint32(len(newSlots)), UnionCount: unionCount,
	}, nil
}
