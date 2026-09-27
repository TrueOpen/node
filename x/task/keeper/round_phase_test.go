package keeper

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/task/types"
)

func TestChallengeVerifierStageKeepsTaskPhaseMonotonic(t *testing.T) {
	f := initInternalFixture(t)
	taskID := bytes.Repeat([]byte{0x43}, types.Hash32Len)
	taskKey := types.NewTaskKey(taskID)
	core := types.TaskCoreState{
		TaskId:             taskID,
		TaskPhase:          types.TaskPhase_TASK_PHASE_REVEALING,
		VerificationStatus: types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_WINDOW_PENDING,
	}
	require.NoError(t, f.keeper.TaskRoundSummary.Set(f.ctx, taskKey, types.TaskRoundSummaryState{
		TaskId: taskID, MaxClosedRound: types.VerifyRoundV1, OpenRoundCount: 1,
	}))
	require.NoError(t, f.keeper.WriteVerificationRound(f.ctx,
		types.NewVerifyRoundKey(taskKey, types.ChallengeVerifyRoundV1),
		types.VerificationRoundState{TaskId: taskID, VerifyRound: types.ChallengeVerifyRoundV1},
	))

	require.NoError(t, f.keeper.validateActiveVerifierStage(
		f.ctx, taskKey, core, types.ChallengeVerifyRoundV1,
		types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_WINDOW_PENDING,
	))

	advanceTaskPhase(&core, types.TaskPhase_TASK_PHASE_VERIFIER_ASSIGNED)
	require.Equal(t, types.TaskPhase_TASK_PHASE_REVEALING, core.TaskPhase)
	advanceTaskPhase(&core, types.TaskPhase_TASK_PHASE_COMMITTING)
	require.Equal(t, types.TaskPhase_TASK_PHASE_REVEALING, core.TaskPhase)

	rewound := core
	rewound.TaskPhase = types.TaskPhase_TASK_PHASE_RECEIPT_COMMITTED
	require.Error(t, f.keeper.validateActiveVerifierStage(
		f.ctx, taskKey, rewound, types.ChallengeVerifyRoundV1,
		types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_WINDOW_PENDING,
	))
}

func TestRoundOneVerifierStageRetainsPhaseStatusPairing(t *testing.T) {
	f := initInternalFixture(t)
	taskID := bytes.Repeat([]byte{0x44}, types.Hash32Len)
	core := types.TaskCoreState{
		TaskId:             taskID,
		TaskPhase:          types.TaskPhase_TASK_PHASE_RECEIPT_COMMITTED,
		VerificationStatus: types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_WINDOW_PENDING,
	}
	require.NoError(t, f.keeper.validateActiveVerifierStage(
		f.ctx, types.NewTaskKey(taskID), core, types.VerifyRoundV1,
		types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_WINDOW_PENDING,
	))
	core.TaskPhase = types.TaskPhase_TASK_PHASE_REVEALING
	require.Error(t, f.keeper.validateActiveVerifierStage(
		f.ctx, types.NewTaskKey(taskID), core, types.VerifyRoundV1,
		types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_WINDOW_PENDING,
	))
}
