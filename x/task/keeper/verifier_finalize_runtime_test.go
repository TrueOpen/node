package keeper

import (
	"bytes"
	"errors"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func verifierFinalizeFact(taskID []byte, slot uint32, marker byte) types.TaskCandidateFactState {
	return types.TaskCandidateFactState{
		SchemaVersion: 1, TaskId: taskID, Stage: types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY,
		Slot: slot, SlotVersion: 1, OperatorAddress: sdk.AccAddress(bytes.Repeat([]byte{marker}, 20)).String(),
		Duty: shared.Duty_DUTY_VERIFIER, CandidateWeight: 300_000 + slot,
		ActiveBondSnapshot: shared.NewAmount(100), AvailableBondSnapshot: shared.NewAmount(90),
		RequiredTaskLiabilitySnapshot: shared.NewAmount(10), MinStakeSnapshot: shared.NewAmount(20),
		PerformanceScoreSnapshotPpm:    types.PerformanceScoreDefaultPpm,
		PerformanceMethodVersion:       types.PerformanceMethodRawQ16V1,
		CandidateJailFactorSnapshotPpm: 1_000_000, BondVersionSnapshot: 1,
		SupportVersionSnapshot: 1, CapabilityVersionSnapshot: 1,
		HandraiseSigningDigest: bytes.Repeat([]byte{marker + 10}, types.Hash32Len),
	}
}

func verifierFinalizeFixture(t *testing.T) (*internalFixture, types.TaskKey, types.VerifierCandidateWindowState, []types.VerifierCandidateWindowMemberState, []types.TaskCandidateFactState) {
	t.Helper()
	f := initInternalFixture(t)
	sdkCtx := sdk.UnwrapSDKContext(f.ctx).WithChainID("verifier-finalize-chain")
	f.ctx = sdk.WrapSDKContext(sdkCtx)
	taskID := bytes.Repeat([]byte{0xa5}, types.Hash32Len)
	taskKey := types.NewTaskKey(taskID)
	facts := []types.TaskCandidateFactState{
		verifierFinalizeFact(taskID, 1, 0x31),
		verifierFinalizeFact(taskID, 2, 0x32),
		verifierFinalizeFact(taskID, 3, 0x33),
	}
	members := make([]types.VerifierCandidateWindowMemberState, len(facts))
	for index, fact := range facts {
		members[index] = types.VerifierCandidateWindowMemberState{
			SchemaVersion: 1, TaskId: taskID, VerifyRound: types.VerifyRoundV1,
			RankIndex: uint32(index), Slot: fact.Slot, SlotVersion: fact.SlotVersion,
			OperatorAddress:    fact.OperatorAddress,
			VerifierWindowRank: bytes.Repeat([]byte{byte(index + 1)}, types.Hash32Len),
		}
	}
	window := types.VerifierCandidateWindowState{
		SchemaVersion: 1, TaskId: taskID, VerifyRound: types.VerifyRoundV1,
		InferReceiptHash:         bytes.Repeat([]byte{0x41}, types.Hash32Len),
		CandidatePoolSnapshotId:  bytes.Repeat([]byte{0x42}, types.Hash32Len),
		CandidatePoolHash:        bytes.Repeat([]byte{0x43}, types.Hash32Len),
		VerifierWindowSourceHash: bytes.Repeat([]byte{0x44}, types.Hash32Len),
		EligibleCount:            3, WindowRandomnessHeight: 10, WindowSize: 3, GeneratedHeight: 10,
		HandraiseCloseHeight: 20, SelectionRandomnessHeight: 30, AssignmentDeadlineHeight: 100,
		Status: types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY,
		XWindowRandomnessBeacon: &types.VerifierCandidateWindowState_WindowRandomnessBeacon{
			WindowRandomnessBeacon: bytes.Repeat([]byte{0x45}, types.Hash32Len),
		},
	}
	sessionID := bytes.Repeat([]byte{0xa4}, types.Hash32Len)
	builders := []string{
		sdk.AccAddress(bytes.Repeat([]byte{0xb1}, 20)).String(),
		sdk.AccAddress(bytes.Repeat([]byte{0xb2}, 20)).String(),
		sdk.AccAddress(bytes.Repeat([]byte{0xb3}, 20)).String(),
	}
	builderSetHash := bytes.Repeat([]byte{0xb4}, types.Hash32Len)
	selectedBuildersHash, err := types.SelectedTaskBuildersHash(
		sdkCtx.ChainID(), taskID, "builder-set-v1", builderSetHash, builders,
	)
	require.NoError(t, err)
	windowHash, err := verifierWindowHash(sdkCtx.ChainID(), window, window.GetWindowRandomnessBeacon(), members)
	require.NoError(t, err)
	window.XVerifierWindowHash = &types.VerifierCandidateWindowState_VerifierWindowHash{VerifierWindowHash: windowHash[:]}
	bitmapSegments := []taskBitmapSegment{{Index: 0, Bitmap: []byte{0x0e}}}
	bitmapHash, err := taskStageUnionBitmapHash(
		sdkCtx.ChainID(), taskID, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY,
		window.CandidatePoolSnapshotId, 8, 1, 3, bitmapSegments,
	)
	require.NoError(t, err)
	union := types.TaskStageHandraiseUnionState{
		SchemaVersion: 1, TaskId: taskID, Stage: types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY,
		CandidatePoolSnapshotId: window.CandidatePoolSnapshotId, CandidatePoolHash: window.CandidatePoolHash,
		UnionCount: 3, AcceptedProposalCount: 1,
		Status:            types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_OPEN,
		WindowCloseHeight: 20, UnionBitmapHash: bitmapHash[:],
		XSelectionRandomnessHeight: &types.TaskStageHandraiseUnionState_SelectionRandomnessHeight{SelectionRandomnessHeight: 30},
	}
	require.NoError(t, f.keeper.TaskCore.Set(f.ctx, taskKey, types.TaskCoreState{
		TaskId: taskID, SessionId: sessionID, TaskPhase: types.TaskPhase_TASK_PHASE_RECEIPT_COMMITTED,
		ReceiptStatus:      types.ReceiptStatus_RECEIPT_STATUS_RECEIPT_ACCEPTED,
		VerificationStatus: types.VerificationStatus_VERIFICATION_STATUS_VERIFY_COLLECTION_OPEN,
	}))
	require.NoError(t, f.keeper.StoreTaskBuilderSelection(f.ctx, taskKey, types.TaskBuilderSelectionState{
		TaskId: taskID, BuilderSetId: "builder-set-v1", BuilderSetHash: builderSetHash,
		SelectedTaskBuilders: builders, SelectedTaskBuilderCount: uint32(len(builders)),
		SelectedTaskBuildersHash: selectedBuildersHash, BodyStatus: shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE,
	}))
	require.NoError(t, f.keeper.VerifierCandidateWindow.Set(f.ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1), window))
	require.NoError(t, f.keeper.TaskStageHandraiseUnion.Set(f.ctx,
		types.NewTaskStageKey(taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY), union))
	require.NoError(t, f.keeper.TaskStageHandraiseUnionSegment.Set(f.ctx,
		types.NewTaskStageSegmentKey(taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY, 0),
		types.TaskStageHandraiseUnionSegmentState{
			SchemaVersion: 1, TaskId: taskID, Stage: types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY,
			SegmentIndex: 0, Bitmap: []byte{0x0e},
		}))
	for _, fact := range facts {
		require.NoError(t, f.keeper.WriteTaskCandidateFact(f.ctx,
			types.NewTaskCandidateFactKey(taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY, fact.Slot), fact))
	}
	for _, member := range members {
		require.NoError(t, f.keeper.WriteVerifierWindowMember(f.ctx,
			types.NewVerifierWindowMemberKey(taskKey, types.VerifyRoundV1, member.RankIndex), member))
	}
	require.NoError(t, f.keeper.VerifierHandraiseCloseIndex.Set(f.ctx,
		types.NewVerifyRoundIndexKey(20, taskKey, types.VerifyRoundV1)))
	return f, taskKey, window, members, facts
}

func TestVerifierLegalSetCursorMatchesSinglePassAcrossRestarts(t *testing.T) {
	f, taskKey, window, members, facts := verifierFinalizeFixture(t)
	stageKey := types.NewTaskStageKey(taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY)

	advanced, visited, _, err := f.keeper.finalizeVerifierLegalSetAtHeight(f.ctx, taskKey, window, 20, 1)
	require.NoError(t, err)
	require.False(t, advanced)
	require.Equal(t, uint64(1), visited)
	cursor, err := f.keeper.TaskCandidateFinalizeCursor.Get(f.ctx, stageKey)
	require.NoError(t, err)
	require.Equal(t, uint32(1), cursor.MaterializedCount)
	encoded, err := cursor.Marshal()
	require.NoError(t, err)
	var restarted types.TaskCandidateFinalizeCursorState
	require.NoError(t, restarted.Unmarshal(encoded))
	require.NoError(t, f.keeper.TaskCandidateFinalizeCursor.Set(f.ctx, stageKey, restarted))

	advanced, visited, _, err = f.keeper.finalizeVerifierLegalSetAtHeight(f.ctx, taskKey, window, 21, 1)
	require.NoError(t, err)
	require.False(t, advanced)
	require.Equal(t, uint64(1), visited)
	advanced, visited, _, err = f.keeper.finalizeVerifierLegalSetAtHeight(f.ctx, taskKey, window, 22, 1)
	require.NoError(t, err)
	require.True(t, advanced)
	require.Equal(t, uint64(1), visited)

	union, err := f.keeper.TaskStageHandraiseUnion.Get(f.ctx, stageKey)
	require.NoError(t, err)
	hashUnion := union
	hashUnion.Status = types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_FINALIZING
	expected, err := verifierLegalSetHash(sdk.UnwrapSDKContext(f.ctx).ChainID(), window, hashUnion, members, facts)
	require.NoError(t, err)
	require.Equal(t, expected[:], union.GetVerifierLegalSetHash())
	cursor, err = f.keeper.TaskCandidateFinalizeCursor.Get(f.ctx, stageKey)
	require.NoError(t, err)
	require.Equal(t, types.FinalizeCursorStatusV1_FINALIZE_CURSOR_STATUS_V1_WAITING_RANDOMNESS, cursor.Status)
	require.NotEqual(t, expected[:], cursor.RunningCommitment)
	hasSegment, err := f.keeper.TaskStageHandraiseUnionSegment.Has(f.ctx,
		types.NewTaskStageSegmentKey(taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY, 0))
	require.NoError(t, err)
	require.False(t, hasSegment)
	hasSelection, err := f.keeper.VerifierSelectionRandomnessIndex.Has(f.ctx,
		types.NewVerifyRoundIndexKey(30, taskKey, types.VerifyRoundV1))
	require.NoError(t, err)
	require.True(t, hasSelection)

	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(22))
	replay, err := f.keeper.RunVerifierRound(f.ctx, window.TaskId, types.VerifyRoundV1, 22, 1)
	require.NoError(t, err)
	require.Equal(t, VerifierRoundRunResult{}, replay)
}

func TestVerifierLegalSetCursorRejectsCorruptedRestartChunk(t *testing.T) {
	f, taskKey, window, _, _ := verifierFinalizeFixture(t)
	stageKey := types.NewTaskStageKey(taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY)
	_, _, _, err := f.keeper.finalizeVerifierLegalSetAtHeight(f.ctx, taskKey, window, 20, 1)
	require.NoError(t, err)
	cursor, err := f.keeper.TaskCandidateFinalizeCursor.Get(f.ctx, stageKey)
	require.NoError(t, err)
	cursor.MaterializedFactChunks[0][len(cursor.MaterializedFactChunks[0])-1] ^= 0x01
	require.NoError(t, f.keeper.TaskCandidateFinalizeCursor.Set(f.ctx, stageKey, cursor))
	_, _, _, err = f.keeper.finalizeVerifierLegalSetAtHeight(f.ctx, taskKey, window, 21, 1)
	require.ErrorContains(t, err, "cursor commitment mismatch")
}

func TestVerifierRoundRowDoesNotCommitPastSharedByteBudget(t *testing.T) {
	f, taskKey, window, _, _ := verifierFinalizeFixture(t)
	usage, err := f.keeper.processVerifierHandraiseCloseIndexWithBudget(f.ctx, 20, 1, 1)
	require.NoError(t, err)
	require.Zero(t, usage.visited)
	require.Zero(t, usage.serializedBytes)
	stageKey := types.NewTaskStageKey(taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY)
	hasCursor, err := f.keeper.TaskCandidateFinalizeCursor.Has(f.ctx, stageKey)
	require.NoError(t, err)
	require.False(t, hasCursor)
	union, err := f.keeper.TaskStageHandraiseUnion.Get(f.ctx, stageKey)
	require.NoError(t, err)
	require.Equal(t, types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_OPEN, union.Status)
	storedWindow, err := f.keeper.VerifierCandidateWindow.Get(f.ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1))
	require.NoError(t, err)
	require.Equal(t, window, storedWindow)
}

func TestVerifierRoundRunnerRejectsCallerSuppliedHeight(t *testing.T) {
	f, taskKey, window, _, _ := verifierFinalizeFixture(t)
	_, err := f.keeper.RunVerifierRound(f.ctx, window.TaskId, types.VerifyRoundV1, 20, 1)
	require.ErrorContains(t, err, "authoritative block height")
	hasCursor, err := f.keeper.TaskCandidateFinalizeCursor.Has(f.ctx,
		types.NewTaskStageKey(taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY))
	require.NoError(t, err)
	require.False(t, hasCursor)
}

// verifierSelectionReady is one task driven to the point where the legal set is
// frozen and only the selection-time liability reservation is left.
type verifierSelectionReady struct {
	f              *internalFixture
	taskKey        types.TaskKey
	window         types.VerifierCandidateWindowState
	union          types.TaskStageHandraiseUnionState
	cursor         types.TaskCandidateFinalizeCursorState
	factCount      int
	membersBySlot  map[uint32]hubtypes.CandidatePoolMemberState
	bindingsBySlot map[uint32]hubtypes.CandidateSlotBindingState
}

func verifierSelectionReadyFixture(t *testing.T) verifierSelectionReady {
	t.Helper()
	f, taskKey, window, _, facts := verifierFinalizeFixture(t)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(30))
	stageKey := types.NewTaskStageKey(taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY)
	core, err := f.keeper.TaskCore.Get(f.ctx, taskKey)
	require.NoError(t, err)
	core.ModelId = "model-a"
	core.ProfileVersion = 1
	core.OrderValue = shared.NewAmount(10)
	core.AcceptedTaskHash = bytes.Repeat([]byte{0x51}, types.Hash32Len)
	require.NoError(t, f.keeper.TaskCore.Set(f.ctx, taskKey, core))
	winner := sdk.AccAddress(bytes.Repeat([]byte{0x61}, 20)).String()
	require.NoError(t, f.keeper.WriteTaskAssignment(f.ctx, taskKey, types.TaskAssignmentState{
		TaskId: window.TaskId, CandidatePoolSnapshotId: window.CandidatePoolSnapshotId,
		CandidatePoolHash: window.CandidatePoolHash, WinnerWorker: winner,
	}))
	require.NoError(t, f.keeper.VerifyOpenDeadlineIndex.Set(f.ctx,
		types.NewDeadlineIndexKey(window.AssignmentDeadlineHeight, taskKey)))
	advanced, _, _, err := f.keeper.finalizeVerifierLegalSetAtHeight(f.ctx, taskKey, window, 30, 3)
	require.NoError(t, err)
	require.True(t, advanced)

	ready := verifierSelectionReady{f: f, taskKey: taskKey, window: window, factCount: len(facts)}
	ready.union, err = f.keeper.TaskStageHandraiseUnion.Get(f.ctx, stageKey)
	require.NoError(t, err)
	ready.cursor, err = f.keeper.TaskCandidateFinalizeCursor.Get(f.ctx, stageKey)
	require.NoError(t, err)
	ready.membersBySlot = make(map[uint32]hubtypes.CandidatePoolMemberState, len(facts))
	ready.bindingsBySlot = make(map[uint32]hubtypes.CandidateSlotBindingState, len(facts))
	for _, fact := range facts {
		bindingHash := bytes.Repeat([]byte{byte(fact.Slot + 0x70)}, types.Hash32Len)
		ready.membersBySlot[fact.Slot] = hubtypes.CandidatePoolMemberState{
			Slot: fact.Slot, SlotVersion: fact.SlotVersion, OperatorAddress: fact.OperatorAddress,
			BindingHash: bindingHash,
		}
		ready.bindingsBySlot[fact.Slot] = hubtypes.CandidateSlotBindingState{
			Slot: fact.Slot, SlotVersion: fact.SlotVersion, OperatorAddress: fact.OperatorAddress,
			BindingHash: bindingHash,
		}
	}
	return ready
}

func TestVerifierSelectionRollsBackTaskWritesWhenLiabilityReservationFails(t *testing.T) {
	ready := verifierSelectionReadyFixture(t)
	f, taskKey, window := ready.f, ready.taskKey, ready.window
	union, cursor := ready.union, ready.cursor
	membersBySlot, bindingsBySlot := ready.membersBySlot, ready.bindingsBySlot
	stageKey := types.NewTaskStageKey(taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY)
	reservations := make([]hubtypes.FrozenFactLiabilityRequest, 0, ready.factCount)
	attempts := 0
	f.keeper.hubKeeper = verifierAdmissionHubStub{
		membersBySlot: membersBySlot, bindingsBySlot: bindingsBySlot,
		selectionBeacon: hubtypes.BeaconSnapshot{Height: 30, Randomness: bytes.Repeat([]byte{0x52}, types.Hash32Len)},
		reservations:    &reservations, reservationErr: errors.New("liability rejected"),
		reservationAttempts: &attempts, reservationFailureAttempt: 2,
	}
	created, _, err := f.keeper.finalizeVerifierAssignmentAtHeight(f.ctx, taskKey, window, union, cursor, 30)
	require.ErrorContains(t, err, "liability rejected")
	require.ErrorIs(t, err, ErrVerifierLiabilityUnavailable,
		"a live-bond rejection must be classified, not propagated as a block-level failure")
	require.False(t, created)
	require.Equal(t, 2, attempts)
	require.Len(t, reservations, 1)
	require.Equal(t, uint32(1), reservations[0].Slot, "liability preflight and reservation order must be slot ascending")
	hasAssignment, err := f.keeper.VerifierAssignment.Has(f.ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1))
	require.NoError(t, err)
	require.False(t, hasAssignment)
	coreAfterFailure, err := f.keeper.TaskCore.Get(f.ctx, taskKey)
	require.NoError(t, err)
	require.Equal(t, types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_SELECTION_PENDING, coreAfterFailure.VerificationStatus)
	hasCursor, err := f.keeper.TaskCandidateFinalizeCursor.Has(f.ctx, stageKey)
	require.NoError(t, err)
	require.True(t, hasCursor)
	hasCommitDeadline, err := f.keeper.CommitDeadlineIndex.Has(f.ctx, types.NewDeadlineIndexKey(50, taskKey))
	require.NoError(t, err)
	require.False(t, hasCommitDeadline)

	reservations = nil
	attempts = 0
	responsibilities := make([]hubtypes.ServiceKeyResponsibilityState, 0, testBuildersPerStageResponsibility)
	responsibilityReleases := make([]string, 0, testBuildersPerStageResponsibility)
	f.keeper.hubKeeper = verifierAdmissionHubStub{
		membersBySlot: membersBySlot, bindingsBySlot: bindingsBySlot,
		selectionBeacon: hubtypes.BeaconSnapshot{Height: 30, Randomness: bytes.Repeat([]byte{0x52}, types.Hash32Len)},
		reservations:    &reservations, reservationAttempts: &attempts,
		responsibilities: &responsibilities, responsibilityReleases: &responsibilityReleases,
	}
	created, _, err = f.keeper.finalizeVerifierAssignmentAtHeight(f.ctx, taskKey, window, union, cursor, 30)
	require.NoError(t, err)
	require.True(t, created)
	require.Len(t, reservations, 3)
	require.Len(t, responsibilities, testBuildersPerStageResponsibility)
	require.Len(t, responsibilityReleases, testBuildersPerStageResponsibility)
	for _, responsibility := range responsibilities {
		require.Equal(t, serviceKeyResponsibilitySettleBuilder, responsibility.ResponsibilityKind)
		require.Equal(t, uint64(30), responsibility.CreatedHeight)
	}
	require.Equal(t, []uint32{1, 2, 3}, []uint32{reservations[0].Slot, reservations[1].Slot, reservations[2].Slot})
	hasAssignment, err = f.keeper.VerifierAssignment.Has(f.ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1))
	require.NoError(t, err)
	require.True(t, hasAssignment)
	hasCursor, err = f.keeper.TaskCandidateFinalizeCursor.Has(f.ctx, stageKey)
	require.NoError(t, err)
	require.False(t, hasCursor)
	replay, err := f.keeper.RunVerifierRound(f.ctx, window.TaskId, types.VerifyRoundV1, 30, 1)
	require.NoError(t, err)
	require.Equal(t, VerifierRoundRunResult{}, replay)
	require.Equal(t, 3, attempts)
	require.Len(t, responsibilities, testBuildersPerStageResponsibility, "assignment replay must not reserve settlement duties twice")
	require.Len(t, responsibilityReleases, testBuildersPerStageResponsibility, "assignment replay must not release prior duties twice")
}

// The msg-driven executor shares finalizeVerifierAssignmentAtHeight with the
// EndBlock selection sweep, so a live-bond rejection must not surface as a
// caller fault here either. The sweep side of the same halt is pinned by
// TestVerifierSelectionPublishesOnlyAfterFrozenLiabilityReservations.
func TestRunVerifierRoundReportsNoAssignmentWhenLiabilityUnavailable(t *testing.T) {
	ready := verifierSelectionReadyFixture(t)
	f, window := ready.f, ready.window
	attempts := 0
	f.keeper.hubKeeper = verifierAdmissionHubStub{
		membersBySlot: ready.membersBySlot, bindingsBySlot: ready.bindingsBySlot,
		selectionBeacon:     hubtypes.BeaconSnapshot{Height: 30, Randomness: bytes.Repeat([]byte{0x52}, types.Hash32Len)},
		reservationErr:      errors.New("current service bond does not match or cover the frozen liability fact"),
		reservationAttempts: &attempts, reservationFailureAttempt: 1,
	}
	result, err := f.keeper.RunVerifierRound(f.ctx, window.TaskId, types.VerifyRoundV1, 30, 3)
	require.NoError(t, err)
	require.False(t, result.AssignmentCreated)
	hasAssignment, err := f.keeper.VerifierAssignment.Has(f.ctx,
		types.NewVerifyRoundKey(ready.taskKey, types.VerifyRoundV1))
	require.NoError(t, err)
	require.False(t, hasAssignment)
}
