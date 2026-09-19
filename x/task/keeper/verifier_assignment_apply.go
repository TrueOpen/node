package keeper

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) finalizeVerifierAssignmentAtHeight(
	ctx context.Context,
	taskID types.TaskKey,
	window types.VerifierCandidateWindowState,
	union types.TaskStageHandraiseUnionState,
	cursor types.TaskCandidateFinalizeCursorState,
	currentHeight uint64,
) (bool, uint64, error) {
	if currentHeight < window.SelectionRandomnessHeight || !shared.IsBlockHeightV1(window.SelectionRandomnessHeight) {
		return false, 0, fmt.Errorf("verifier assignment height is outside the frozen window")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	beacon, found := k.hubKeeper.GetBeaconForDomain(
		sdkCtx, shared.DomainWeightedDrawV1, int64(window.SelectionRandomnessHeight),
	)
	if !found {
		return false, 0, nil
	}
	if currentHeight > window.AssignmentDeadlineHeight {
		return false, 0, fmt.Errorf("verifier assignment height is outside the frozen window")
	}
	if beacon.Height != int64(window.SelectionRandomnessHeight) || len(beacon.Randomness) != types.Hash32Len {
		return false, 0, errorsmod.Wrap(types.ErrInvariantBroken, "verifier selection beacon does not match its frozen height")
	}
	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	taskKey := types.NewTaskKey(taskID)
	params, err := k.Params.Get(cache)
	if err != nil {
		return false, 0, err
	}
	selectedCount, err := verifierCountForRound(params, window.VerifyRound)
	if err != nil {
		return false, 0, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	if params.Weights.MaxWeightedDrawAttemptsPerSelection == 0 ||
		union.UnionCount < selectedCount ||
		union.UnionCount > params.Proposals.MaxCandidateUnionMembersPerStage ||
		window.WindowSize == 0 || window.WindowSize > params.Weights.VerifierCandidateWindowMax {
		return false, 0, errorsmod.Wrap(types.ErrInvariantBroken, "verifier selection bounds are invalid")
	}
	if err := validateOpenVerifyCursorIndexScope(cursor, taskID); err != nil ||
		cursor.Status != types.FinalizeCursorStatusV1_FINALIZE_CURSOR_STATUS_V1_WAITING_RANDOMNESS ||
		cursor.GetRandomnessHeight() != window.SelectionRandomnessHeight ||
		cursor.MaterializedCount != union.UnionCount ||
		uint32(len(cursor.MaterializedFactChunks)) != cursor.MaterializedCount {
		if err == nil {
			err = errorsmod.Wrap(types.ErrInvariantBroken, "verifier selection cursor is non-canonical")
		}
		return false, uint64(cursor.Size()), err
	}
	core, err := k.TaskCore.Get(cache, taskKey)
	if err != nil || !bytes.Equal(core.TaskId, window.TaskId) {
		return false, 0, errorsmod.Wrap(types.ErrInvariantBroken, "verifier selection task state is inconsistent")
	}
	if err := k.validateActiveVerifierStage(cache, taskKey, core, window.VerifyRound,
		types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_SELECTION_PENDING); err != nil {
		return false, 0, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	workerAssignment, err := k.TaskAssignment.Get(cache, taskKey)
	if err != nil || !bytes.Equal(workerAssignment.TaskId, core.TaskId) ||
		!bytes.Equal(workerAssignment.CandidatePoolSnapshotId, window.CandidatePoolSnapshotId) {
		return false, 0, errorsmod.Wrap(types.ErrInvariantBroken, "verifier selection worker assignment scope is inconsistent")
	}
	members, memberBytes, err := k.loadVerifierWindowMembersForAssignment(cache, taskID, window)
	if err != nil {
		return false, memberBytes, err
	}
	facts, cursorFold, err := k.decodeVerifierFactChunks(window.TaskId, cursor.MaterializedFactChunks)
	rowBytes := memberBytes + uint64(core.Size()+workerAssignment.Size()+union.Size()+window.Size()+cursor.Size())
	if err != nil || !bytes.Equal(cursorFold, cursor.RunningCommitment) || uint32(len(facts)) != union.UnionCount {
		if err == nil {
			err = errorsmod.Wrap(types.ErrInvariantBroken, "verifier selection cursor commitment mismatch")
		}
		return false, rowBytes, err
	}
	var commitDeadline, verifyDeadline uint64
	if window.VerifyRound == types.VerifyRoundV1 {
		var overflow bool
		commitDeadline, overflow = checkedHeightAdd(currentHeight, params.Deadlines.CommitWindowBlocks)
		if overflow {
			return false, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier commit deadline overflows")
		}
		verifyDeadline, overflow = checkedHeightAdd(currentHeight, params.Deadlines.CollectionWindowBlocks)
		if overflow {
			return false, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier final deadline overflows")
		}
	} else {
		round, err := k.VerificationRound.Get(cache, types.NewVerifyRoundKey(taskKey, window.VerifyRound))
		if err != nil || round.XClosedHeight != nil || round.RoundCloseDeadlineHeight <= window.AssignmentDeadlineHeight {
			return false, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "challenge round clock is unavailable")
		}
		commitDeadline = window.AssignmentDeadlineHeight
		verifyDeadline = round.RoundCloseDeadlineHeight
	}
	assignment, err := buildVerifierAssignment(
		cacheCtx.ChainID(), window, union, members, facts, beacon.Randomness,
		selectedCount, uint64(params.Weights.MaxWeightedDrawAttemptsPerSelection),
		currentHeight, commitDeadline, verifyDeadline,
	)
	if err != nil {
		return false, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	if len(union.GetVerifierLegalSetHash()) != types.Hash32Len ||
		!bytes.Equal(union.GetVerifierLegalSetHash(), assignment.VerifierLegalSetHash) {
		return false, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier selection legal-set commitment mismatch")
	}
	factsBySlot := make(map[uint32]types.TaskCandidateFactState, len(facts))
	for _, fact := range facts {
		factsBySlot[fact.Slot] = fact
	}
	selectedFacts := make([]types.TaskCandidateFactState, len(assignment.SelectedVerifiers))
	for index, selected := range assignment.SelectedVerifiers {
		fact, exists := factsBySlot[selected.Slot]
		if !exists || fact.SlotVersion != selected.SlotVersion || fact.OperatorAddress != selected.OperatorAddress {
			return false, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "selected verifier does not match a frozen fact")
		}
		selectedFacts[index] = fact
	}
	sort.Slice(selectedFacts, func(i, j int) bool { return selectedFacts[i].Slot < selectedFacts[j].Slot })
	requests := make([]hubtypes.FrozenFactLiabilityRequest, len(selectedFacts))
	for index, fact := range selectedFacts {
		request, err := k.preflightVerifierTaskLiability(cache, core, workerAssignment, window, fact, currentHeight)
		if err != nil {
			return false, rowBytes, err
		}
		requests[index] = request
	}
	for _, request := range requests {
		if _, err := k.hubKeeper.ReserveTaskLiabilityFromFrozenFact(cache, request); err != nil {
			// Runtime-normal, not corruption: the operator's live bond may have
			// moved under the frozen fact. Callers retire the round instead of
			// aborting FinalizeBlock.
			return false, rowBytes, fmt.Errorf("%w: %s", ErrVerifierLiabilityUnavailable, err)
		}
	}
	if window.VerifyRound == types.ChallengeVerifyRoundV1 {
		round, err := k.VerificationRound.Get(cache, types.NewVerifyRoundKey(taskKey, window.VerifyRound))
		if err != nil {
			return false, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "challenge round is unavailable at assignment")
		}
		if err := k.reserveChallengeVerifierResponsibilities(cache, core, round, assignment, currentHeight); err != nil {
			return false, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
		}
	}
	assignmentKey := types.NewVerifyRoundKey(taskKey, window.VerifyRound)
	if exists, err := k.VerifierAssignment.Has(cache, assignmentKey); err != nil || exists {
		if err != nil {
			return false, rowBytes, err
		}
		return false, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "verifier assignment already exists")
	}
	if err := k.VerifierAssignment.Set(cache, assignmentKey, assignment); err != nil {
		return false, rowBytes, err
	}
	if window.VerifyRound == types.VerifyRoundV1 {
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
		}
		if exists, err := k.VerificationRound.Has(cache, assignmentKey); err != nil || exists {
			if err != nil {
				return false, rowBytes, err
			}
			return false, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "round 1 header already exists")
		}
		if err := k.VerificationRound.Set(cache, assignmentKey, round); err != nil {
			return false, rowBytes, err
		}
	}
	union.Status = types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_FINALIZED
	union.XVerifierLegalSetHash = &types.TaskStageHandraiseUnionState_VerifierLegalSetHash{
		VerifierLegalSetHash: append([]byte(nil), assignment.VerifierLegalSetHash...),
	}
	if err := k.TaskStageHandraiseUnion.Set(cache,
		types.NewTaskStageKey(taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY), union); err != nil {
		return false, rowBytes, err
	}
	if err := k.releaseCandidateStageDuties(cache, facts); err != nil {
		return false, rowBytes, err
	}
	if err := k.TaskCandidateFinalizeCursor.Remove(cache,
		types.NewTaskStageKey(taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY)); err != nil {
		return false, rowBytes, err
	}
	advanceTaskPhase(&core, types.TaskPhase_TASK_PHASE_VERIFIER_ASSIGNED)
	core.VerificationStatus = types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_ASSIGNED
	core.UpdatedHeight = currentHeight
	if err := k.TaskCore.Set(cache, taskKey, core); err != nil {
		return false, rowBytes, err
	}
	if err := addDeadlineIndex(cache, k.CommitDeadlineIndex, taskKey, commitDeadline); err != nil {
		return false, rowBytes, err
	}
	if err := addDeadlineIndex(cache, k.VerifyDeadlineIndex, taskKey, verifyDeadline); err != nil {
		return false, rowBytes, err
	}
	if err := removeDeadlineIndex(cache, k.VerifyOpenDeadlineIndex, taskKey, window.AssignmentDeadlineHeight); err != nil {
		return false, rowBytes, err
	}
	for _, selected := range assignment.SelectedVerifiers {
		if err := k.AddVerifierActiveJobIndex(cache, selected.OperatorAddress, core.TaskId); err != nil {
			return false, rowBytes, err
		}
	}
	if window.VerifyRound == types.VerifyRoundV1 {
		selection, err := k.TaskBuilderSelection.Get(cache, taskKey)
		if err != nil {
			return false, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "Task Builder selection is unavailable at verifier assignment")
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
	}
	if err := k.VerifierSelectionRandomnessIndex.Remove(cache,
		types.NewVerifyRoundIndexKey(window.SelectionRandomnessHeight, taskKey, window.VerifyRound)); err != nil {
		return false, rowBytes, err
	}
	closeKey := types.NewVerifyRoundIndexKey(window.HandraiseCloseHeight, taskKey, window.VerifyRound)
	if exists, err := k.VerifierHandraiseCloseIndex.Has(cache, closeKey); err != nil {
		return false, rowBytes, err
	} else if exists {
		if err := k.VerifierHandraiseCloseIndex.Remove(cache, closeKey); err != nil {
			return false, rowBytes, err
		}
	}
	if err := emitTypedEvent(cache, &types.EventVerifierAssignmentFinalized{
		SessionId: append([]byte(nil), core.SessionId...), TaskId: append([]byte(nil), core.TaskId...),
		VerifyRound: window.VerifyRound, SelectedVerifiersHash: append([]byte(nil), assignment.SelectedVerifiersHash...),
		VerifierLegalSetHash:      append([]byte(nil), assignment.VerifierLegalSetHash...),
		SelectionRandomnessHeight: window.SelectionRandomnessHeight,
		CommitDeadline:            commitDeadline, VerifyDeadline: verifyDeadline,
	}); err != nil {
		return false, rowBytes, err
	}
	write()
	return true, rowBytes, nil
}

func (k Keeper) loadVerifierWindowMembersForAssignment(
	ctx context.Context,
	taskID types.TaskKey,
	window types.VerifierCandidateWindowState,
) ([]types.VerifierCandidateWindowMemberState, uint64, error) {
	if window.WindowSize == 0 {
		return nil, 0, fmt.Errorf("verifier window is empty")
	}
	members := make([]types.VerifierCandidateWindowMemberState, 0, window.WindowSize)
	var rowBytes uint64
	for rank := uint32(0); rank < window.WindowSize; rank++ {
		member, err := k.VerifierCandidateWindowMember.Get(ctx, types.NewVerifierWindowMemberKey(taskID, window.VerifyRound, rank))
		if err != nil {
			return nil, rowBytes, err
		}
		rowBytes += uint64(member.Size())
		members = append(members, member)
	}
	return members, rowBytes, nil
}

func (k Keeper) preflightVerifierTaskLiability(
	ctx context.Context,
	core types.TaskCoreState,
	workerAssignment types.TaskAssignmentState,
	window types.VerifierCandidateWindowState,
	fact types.TaskCandidateFactState,
	height uint64,
) (hubtypes.FrozenFactLiabilityRequest, error) {
	var request hubtypes.FrozenFactLiabilityRequest
	if fact.OperatorAddress == workerAssignment.WinnerWorker {
		return request, errorsmod.Wrap(types.ErrInvariantBroken, "winner worker cannot be selected as verifier")
	}
	operatorBytes, operator, err := k.canonicalAddress("verifier_operator_address", fact.OperatorAddress)
	if err != nil || operator != fact.OperatorAddress {
		return request, errorsmod.Wrap(types.ErrInvariantBroken, "selected verifier address is non-canonical")
	}
	poolMember, binding, err := k.hubKeeper.ResolveCandidatePoolMember(ctx, window.CandidatePoolSnapshotId, fact.Slot)
	if err != nil || poolMember.Slot != fact.Slot || poolMember.SlotVersion != fact.SlotVersion || poolMember.OperatorAddress != operator ||
		binding.Slot != fact.Slot || binding.SlotVersion != fact.SlotVersion || binding.OperatorAddress != operator ||
		!bytes.Equal(poolMember.BindingHash, binding.BindingHash) {
		return request, errorsmod.Wrap(types.ErrInvariantBroken, "selected verifier no longer matches immutable pool binding")
	}
	hubParams := k.hubKeeper.GetHubParams(sdk.UnwrapSDKContext(ctx))
	if _, err := k.loadVerifierEligibilityFacts(ctx, core, operatorBytes, operator, hubParams); err != nil {
		// Every branch of this re-check reads live state that may legitimately
		// have moved since the handraise froze the fact — support freshness
		// expiring by epoch, a revoked capability, a frozen profile, or the same
		// available-bond movement handled below. None of it is store corruption,
		// so it must fail the round rather than the block.
		return request, fmt.Errorf("%w: %s", ErrVerifierLiabilityUnavailable, err)
	}
	required, err := shared.ParseAmount(fact.RequiredTaskLiabilitySnapshot)
	if err != nil {
		return request, err
	}
	activeBond, err := shared.ParseAmount(fact.ActiveBondSnapshot)
	if err != nil {
		return request, err
	}
	availableBond, err := shared.ParseAmount(fact.AvailableBondSnapshot)
	if err != nil {
		return request, err
	}
	minStake, err := shared.ParseAmount(fact.MinStakeSnapshot)
	if err != nil {
		return request, err
	}
	orderValue, err := shared.ParseAmount(core.OrderValue)
	if err != nil {
		return request, err
	}
	request = hubtypes.FrozenFactLiabilityRequest{
		TaskID: core.TaskId, CandidatePoolSnapshotID: window.CandidatePoolSnapshotId,
		OperatorAddress: fact.OperatorAddress, Duty: fact.Duty, OrderValue: orderValue, Slot: fact.Slot, SlotVersion: fact.SlotVersion,
		RequiredTaskLiability: required, ActiveBondSnapshot: activeBond, AvailableBondSnapshot: availableBond,
		MinStakeSnapshot: minStake, BondVersionSnapshot: fact.BondVersionSnapshot,
		CapabilityVersionSnapshot: fact.CapabilityVersionSnapshot, SupportVersionSnapshot: fact.SupportVersionSnapshot,
		ModelID: core.ModelId, ProfileVersion: core.ProfileVersion, Height: height,
	}
	return request, nil
}
