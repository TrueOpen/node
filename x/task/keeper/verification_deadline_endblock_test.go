package keeper

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/task/types"
)

func verifierWindowForDeadlineTest(taskID []byte, round uint32, status types.VerifierCandidateWindowStatusV1) types.VerifierCandidateWindowState {
	window := types.VerifierCandidateWindowState{
		SchemaVersion:             1,
		TaskId:                    taskID,
		VerifyRound:               round,
		WindowRandomnessHeight:    10,
		HandraiseCloseHeight:      20,
		SelectionRandomnessHeight: 30,
		AssignmentDeadlineHeight:  40,
		Status:                    status,
	}
	if status == types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY {
		window.WindowSize = 1
		window.GeneratedHeight = 10
		window.XWindowRandomnessBeacon = &types.VerifierCandidateWindowState_WindowRandomnessBeacon{
			WindowRandomnessBeacon: bytes.Repeat([]byte{0x31}, types.Hash32Len),
		}
		window.XVerifierWindowHash = &types.VerifierCandidateWindowState_VerifierWindowHash{
			VerifierWindowHash: bytes.Repeat([]byte{0x32}, types.Hash32Len),
		}
	}
	return window
}

func TestVerifierRoundIndexStalePendingPoisonAndSuccess(t *testing.T) {
	t.Run("orphan is stale and removed", func(t *testing.T) {
		f := initInternalFixture(t)
		taskKey := taskKeyFromByte(0x81)
		key := types.NewVerifyRoundIndexKey(10, taskKey, types.VerifyRoundV1)
		require.NoError(t, f.keeper.VerifierWindowBuildIndex.Set(f.ctx, key))

		visited, err := f.keeper.processVerifierWindowBuildIndex(f.ctx, 10, 1)
		require.NoError(t, err)
		require.Equal(t, uint64(1), visited)
		has, err := f.keeper.VerifierWindowBuildIndex.Has(f.ctx, key)
		require.NoError(t, err)
		require.False(t, has)
	})

	t.Run("unopenable source frozen window is retired to the verify-open deadline", func(t *testing.T) {
		f := initInternalFixture(t)
		taskID := bytes.Repeat([]byte{0x82}, types.Hash32Len)
		taskKey := types.NewTaskKey(taskID)
		key := types.NewVerifyRoundIndexKey(10, taskKey, types.VerifyRoundV1)
		window := verifierWindowForDeadlineTest(taskID, types.VerifyRoundV1, types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_SOURCE_FROZEN)
		require.NoError(t, f.keeper.VerifierCandidateWindow.Set(f.ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1), window))
		require.NoError(t, f.keeper.VerifierWindowBuildIndex.Set(f.ctx, key))
		fallbackKey := types.NewDeadlineIndexKey(window.AssignmentDeadlineHeight, taskKey)
		require.NoError(t, f.keeper.VerifyOpenDeadlineIndex.Set(f.ctx, fallbackKey))

		// The helper window has WindowSize 0, so it can never open.
		visited, err := f.keeper.processVerifierWindowBuildIndex(f.ctx, 10, 1)
		require.NoError(t, err)
		require.Equal(t, uint64(1), visited)
		has, err := f.keeper.VerifierWindowBuildIndex.Has(f.ctx, key)
		require.NoError(t, err)
		require.False(t, has)
		has, err = f.keeper.VerifyOpenDeadlineIndex.Has(f.ctx, fallbackKey)
		require.NoError(t, err)
		require.True(t, has)
	})

	t.Run("unopenable window without a later verify-open deadline fails closed", func(t *testing.T) {
		f := initInternalFixture(t)
		taskID := bytes.Repeat([]byte{0x87}, types.Hash32Len)
		taskKey := types.NewTaskKey(taskID)
		key := types.NewVerifyRoundIndexKey(10, taskKey, types.VerifyRoundV1)
		require.NoError(t, f.keeper.VerifierCandidateWindow.Set(f.ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1),
			verifierWindowForDeadlineTest(taskID, types.VerifyRoundV1, types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_SOURCE_FROZEN)))
		require.NoError(t, f.keeper.VerifierWindowBuildIndex.Set(f.ctx, key))

		visited, err := f.keeper.processVerifierWindowBuildIndex(f.ctx, 10, 1)
		require.Error(t, err)
		require.Equal(t, uint64(1), visited)
		has, hasErr := f.keeper.VerifierWindowBuildIndex.Has(f.ctx, key)
		require.NoError(t, hasErr)
		require.True(t, has)
	})

	t.Run("source frozen window past its assignment deadline is dropped", func(t *testing.T) {
		f := initInternalFixture(t)
		taskID := bytes.Repeat([]byte{0x88}, types.Hash32Len)
		taskKey := types.NewTaskKey(taskID)
		key := types.NewVerifyRoundIndexKey(10, taskKey, types.VerifyRoundV1)
		window := verifierWindowForDeadlineTest(taskID, types.VerifyRoundV1, types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_SOURCE_FROZEN)
		require.NoError(t, f.keeper.VerifierCandidateWindow.Set(f.ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1), window))
		require.NoError(t, f.keeper.VerifierWindowBuildIndex.Set(f.ctx, key))

		// The verify-open deadline row is gone because it already failed the task.
		visited, err := f.keeper.processVerifierWindowBuildIndex(f.ctx, window.AssignmentDeadlineHeight+1, 1)
		require.NoError(t, err)
		require.Equal(t, uint64(1), visited)
		has, err := f.keeper.VerifierWindowBuildIndex.Has(f.ctx, key)
		require.NoError(t, err)
		require.False(t, has)
	})

	t.Run("unopenable head row does not block the rows behind it", func(t *testing.T) {
		f := initInternalFixture(t)
		stuckID := bytes.Repeat([]byte{0x89}, types.Hash32Len)
		stuckKey := types.NewTaskKey(stuckID)
		stuckRow := types.NewVerifyRoundIndexKey(10, stuckKey, types.VerifyRoundV1)
		stuck := verifierWindowForDeadlineTest(stuckID, types.VerifyRoundV1, types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_SOURCE_FROZEN)
		require.NoError(t, f.keeper.VerifierCandidateWindow.Set(f.ctx, types.NewVerifyRoundKey(stuckKey, types.VerifyRoundV1), stuck))
		require.NoError(t, f.keeper.VerifierWindowBuildIndex.Set(f.ctx, stuckRow))
		require.NoError(t, f.keeper.VerifyOpenDeadlineIndex.Set(f.ctx, types.NewDeadlineIndexKey(stuck.AssignmentDeadlineHeight, stuckKey)))
		orphanRow := types.NewVerifyRoundIndexKey(11, taskKeyFromByte(0x8a), types.VerifyRoundV1)
		require.NoError(t, f.keeper.VerifierWindowBuildIndex.Set(f.ctx, orphanRow))

		// One row per call, as the EndBlock scheduler drives the queue.
		for range 2 {
			visited, err := f.keeper.processVerifierWindowBuildIndex(f.ctx, 11, 1)
			require.NoError(t, err)
			require.Equal(t, uint64(1), visited)
		}
		for _, row := range []types.VerifyRoundIndexKey{stuckRow, orphanRow} {
			has, err := f.keeper.VerifierWindowBuildIndex.Has(f.ctx, row)
			require.NoError(t, err)
			require.False(t, has)
		}
	})

	t.Run("height mismatch is one bounded poison visit", func(t *testing.T) {
		f := initInternalFixture(t)
		taskID := bytes.Repeat([]byte{0x83}, types.Hash32Len)
		taskKey := types.NewTaskKey(taskID)
		window := verifierWindowForDeadlineTest(taskID, types.VerifyRoundV1, types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_SOURCE_FROZEN)
		window.WindowRandomnessHeight = 11
		key := types.NewVerifyRoundIndexKey(10, taskKey, types.VerifyRoundV1)
		require.NoError(t, f.keeper.VerifierCandidateWindow.Set(f.ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1), window))
		require.NoError(t, f.keeper.VerifierWindowBuildIndex.Set(f.ctx, key))

		visited, err := f.keeper.processVerifierWindowBuildIndex(f.ctx, 10, 1)
		require.Error(t, err)
		require.Equal(t, uint64(1), visited)
		has, hasErr := f.keeper.VerifierWindowBuildIndex.Has(f.ctx, key)
		require.NoError(t, hasErr)
		require.True(t, has)
	})

	t.Run("ready window consumes residual build index", func(t *testing.T) {
		f := initInternalFixture(t)
		taskID := bytes.Repeat([]byte{0x84}, types.Hash32Len)
		taskKey := types.NewTaskKey(taskID)
		key := types.NewVerifyRoundIndexKey(10, taskKey, types.VerifyRoundV1)
		require.NoError(t, f.keeper.VerifierCandidateWindow.Set(f.ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1),
			verifierWindowForDeadlineTest(taskID, types.VerifyRoundV1, types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY)))
		require.NoError(t, f.keeper.VerifierWindowBuildIndex.Set(f.ctx, key))

		visited, err := f.keeper.processVerifierWindowBuildIndex(f.ctx, 10, 1)
		require.NoError(t, err)
		require.Equal(t, uint64(1), visited)
		has, err := f.keeper.VerifierWindowBuildIndex.Has(f.ctx, key)
		require.NoError(t, err)
		require.False(t, has)
	})

	t.Run("unready handraise close falls through to verify-open deadline", func(t *testing.T) {
		f := initInternalFixture(t)
		taskID := bytes.Repeat([]byte{0x85}, types.Hash32Len)
		taskKey := types.NewTaskKey(taskID)
		key := types.NewVerifyRoundIndexKey(20, taskKey, types.VerifyRoundV1)
		window := verifierWindowForDeadlineTest(taskID, types.VerifyRoundV1, types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_SOURCE_FROZEN)
		require.NoError(t, f.keeper.VerifierCandidateWindow.Set(f.ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1), window))
		require.NoError(t, f.keeper.VerifierHandraiseCloseIndex.Set(f.ctx, key))
		fallbackKey := types.NewDeadlineIndexKey(window.AssignmentDeadlineHeight, taskKey)
		require.NoError(t, f.keeper.VerifyOpenDeadlineIndex.Set(f.ctx, fallbackKey))

		visited, err := f.keeper.processVerifierHandraiseCloseIndex(f.ctx, 20, 1)
		require.NoError(t, err)
		require.Equal(t, uint64(1), visited)
		has, err := f.keeper.VerifierHandraiseCloseIndex.Has(f.ctx, key)
		require.NoError(t, err)
		require.False(t, has)
		has, err = f.keeper.VerifyOpenDeadlineIndex.Has(f.ctx, fallbackKey)
		require.NoError(t, err)
		require.True(t, has)
	})

	t.Run("selection pending consumes one visit", func(t *testing.T) {
		f := initInternalFixture(t)
		taskID := bytes.Repeat([]byte{0x86}, types.Hash32Len)
		taskKey := types.NewTaskKey(taskID)
		stageKey := types.NewTaskStageKey(taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY)
		key := types.NewVerifyRoundIndexKey(30, taskKey, types.VerifyRoundV1)
		require.NoError(t, f.keeper.VerifierCandidateWindow.Set(f.ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1),
			verifierWindowForDeadlineTest(taskID, types.VerifyRoundV1, types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY)))
		require.NoError(t, f.keeper.TaskStageHandraiseUnion.Set(f.ctx, stageKey, types.TaskStageHandraiseUnionState{
			SchemaVersion: 1, TaskId: taskID, Stage: types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY,
			Status: types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_FINALIZING, WindowCloseHeight: 20,
			XSelectionRandomnessHeight: &types.TaskStageHandraiseUnionState_SelectionRandomnessHeight{SelectionRandomnessHeight: 30},
		}))
		require.NoError(t, f.keeper.TaskCandidateFinalizeCursor.Set(f.ctx, stageKey, types.TaskCandidateFinalizeCursorState{
			SchemaVersion: 1, TaskId: taskID, Stage: types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY,
			Status:            types.FinalizeCursorStatusV1_FINALIZE_CURSOR_STATUS_V1_WAITING_RANDOMNESS,
			XRandomnessHeight: &types.TaskCandidateFinalizeCursorState_RandomnessHeight{RandomnessHeight: 30},
		}))
		require.NoError(t, f.keeper.VerifierSelectionRandomnessIndex.Set(f.ctx, key))

		visited, err := f.keeper.processVerifierSelectionRandomnessIndex(f.ctx, 30, 1)
		require.NoError(t, err)
		require.Equal(t, uint64(1), visited)
		has, err := f.keeper.VerifierSelectionRandomnessIndex.Has(f.ctx, key)
		require.NoError(t, err)
		require.True(t, has)
	})
}

func TestVerifyOpenPublicSweepFailsClosedWithoutDeadlineLocator(t *testing.T) {
	f := initInternalFixture(t)
	taskID := bytes.Repeat([]byte{0x93}, types.Hash32Len)
	taskKey := types.NewTaskKey(taskID)
	indexKey := types.NewDeadlineIndexKey(10, taskKey)
	require.NoError(t, f.keeper.TaskCore.Set(f.ctx, taskKey, types.TaskCoreState{
		TaskId: taskID, TaskPhase: types.TaskPhase_TASK_PHASE_RECEIPT_COMMITTED,
	}))
	require.NoError(t, f.keeper.VerifyOpenDeadlineIndex.Set(f.ctx, indexKey))

	response, err := f.keeper.sweepTaskDeadline(f.ctx, &types.TaskDeadlineLocator{
		TaskId: taskID, DeadlineKind: types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_OPEN,
	}, 10)
	require.Error(t, err)
	require.Nil(t, response)
	has, err := f.keeper.VerifyOpenDeadlineIndex.Has(f.ctx, indexKey)
	require.NoError(t, err)
	require.True(t, has)
	core, err := f.keeper.TaskCore.Get(f.ctx, taskKey)
	require.NoError(t, err)
	require.Equal(t, types.TaskPhase_TASK_PHASE_RECEIPT_COMMITTED, core.TaskPhase)
}

// A frozen window that can never open must not pin the head of the build queue:
// the EndBlock scheduler revisits the head first, so a row it never removes
// starves every row behind it.
func TestEndBlockUnopenableWindowDoesNotStarveLaterWindows(t *testing.T) {
	f := initInternalFixture(t)
	rows := make([]types.VerifyRoundIndexKey, 0, 3)
	for i := byte(0); i < 3; i++ {
		taskID := bytes.Repeat([]byte{0x90 + i}, types.Hash32Len)
		taskKey := types.NewTaskKey(taskID)
		window := verifierWindowForDeadlineTest(taskID, types.VerifyRoundV1, types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_SOURCE_FROZEN)
		window.WindowRandomnessHeight = 10 + uint64(i)
		row := types.NewVerifyRoundIndexKey(window.WindowRandomnessHeight, taskKey, types.VerifyRoundV1)
		require.NoError(t, f.keeper.VerifierCandidateWindow.Set(f.ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1), window))
		require.NoError(t, f.keeper.VerifierWindowBuildIndex.Set(f.ctx, row))
		require.NoError(t, f.keeper.VerifyOpenDeadlineIndex.Set(f.ctx, types.NewDeadlineIndexKey(window.AssignmentDeadlineHeight, taskKey)))
		rows = append(rows, row)
	}

	result, err := f.keeper.RunEndBlockDeadlineSweeps(f.ctx, 12, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(3), result.VerifierWindowBuild)
	for _, row := range rows {
		has, err := f.keeper.VerifierWindowBuildIndex.Has(f.ctx, row)
		require.NoError(t, err)
		require.False(t, has)
	}
}
