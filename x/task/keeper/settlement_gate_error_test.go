package keeper

// The first two gates of loadSettlementInputs must report different errors.
//
// This is not wording fastidiousness. The submitter of MsgSettleTask has nothing to
// read but the error text: the contract's §10.10a step 2 lists "verify
// phase=SETTLING" and "no conflicting terminal state exists" as two separate
// things, because the correct reactions to them are opposite -- the former means
// "come back once the challenge window closes", the latter means "this is already
// final, stop sending". Once the two are merged into "task already has a terminal
// settlement", a task sitting in REVEALING tells the Builder it has already been
// settled, and all the Builder can do is keep blindly retrying: that is why the
// remote devnet carried 2-3 failing settle transactions with code=1104 in every
// block while that chain's MsgSettleTask success count stayed at 0.

import (
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func TestLoadSettlementInputsSeparatesTooEarlyFromAlreadyFinal(t *testing.T) {
	taskKey := taskKeyFromByte(0x5a)
	taskID := append([]byte(nil), taskKey[:]...)

	t.Run("phase has not reached SETTLING yet", func(t *testing.T) {
		f := initInternalFixture(t)
		require.NoError(t, f.keeper.TaskCore.Set(f.ctx, taskKey, types.TaskCoreState{
			TaskId:           taskID,
			TaskPhase:        types.TaskPhase_TASK_PHASE_REVEALING,
			SettlementStatus: types.SettlementStatus_SETTLEMENT_STATUS_NONE,
			FinalityStatus:   shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_PENDING,
		}))

		_, err := f.keeper.loadSettlementInputs(f.ctx, taskKey, 100)
		require.ErrorIs(t, err, types.ErrInvalidTaskStatus)
		require.ErrorContains(t, err, "TASK_PHASE_REVEALING")
		require.NotContains(t, err.Error(), "already has a terminal settlement",
			"coming too early must not be reported as already settled, that would make the submitter retry forever")
	})

	t.Run("already final", func(t *testing.T) {
		f := initInternalFixture(t)
		require.NoError(t, f.keeper.TaskCore.Set(f.ctx, taskKey, types.TaskCoreState{
			TaskId:              taskID,
			TaskPhase:           types.TaskPhase_TASK_PHASE_SETTLING,
			SettlementStatus:    types.SettlementStatus_SETTLEMENT_STATUS_FINALIZED,
			FinalityStatus:      shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL,
			XTaskFinalityHeight: &types.TaskCoreState_TaskFinalityHeight{TaskFinalityHeight: 90},
		}))

		_, err := f.keeper.loadSettlementInputs(f.ctx, taskKey, 100)
		require.ErrorIs(t, err, types.ErrInvalidTaskStatus)
		require.ErrorContains(t, err, "already has a terminal settlement")
	})
}

// While the challenge window is still open the close height must be reported back:
// the submitter uses it to work out the height at which to retry next, otherwise it
// can only spin on a timer -- which is exactly where those 1800 blocks of spinning
// on the remote devnet came from.
func TestLoadSettlementInputsReportsChallengeCloseHeight(t *testing.T) {
	f := initInternalFixture(t)
	taskKey := taskKeyFromByte(0x5b)

	require.NoError(t, f.keeper.TaskCore.Set(f.ctx, taskKey, types.TaskCoreState{
		TaskId:           append([]byte(nil), taskKey[:]...),
		TaskPhase:        types.TaskPhase_TASK_PHASE_SETTLING,
		SettlementStatus: types.SettlementStatus_SETTLEMENT_STATUS_NONE,
		FinalityStatus:   shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_PENDING,
	}))
	require.NoError(t, f.keeper.TaskRoundSummary.Set(f.ctx, taskKey, types.TaskRoundSummaryState{
		TaskId:                append([]byte(nil), taskKey[:]...),
		OpenRoundCount:        0,
		XRoundsClosedHeight:   &types.TaskRoundSummaryState_RoundsClosedHeight{RoundsClosedHeight: 18257},
		XChallengeOpenHeight:  &types.TaskRoundSummaryState_ChallengeOpenHeight{ChallengeOpenHeight: 16457},
		XChallengeCloseHeight: &types.TaskRoundSummaryState_ChallengeCloseHeight{ChallengeCloseHeight: 18257},
	}))

	_, err := f.keeper.loadSettlementInputs(f.ctx, taskKey, 16600)
	require.ErrorIs(t, err, types.ErrInvalidTaskStatus)
	require.ErrorContains(t, err, "until height 18257")
}
