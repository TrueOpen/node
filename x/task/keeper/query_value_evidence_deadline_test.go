package keeper_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/task/keeper"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func TestStageReportsPendingValueEvidenceBeforeRoundEffects(t *testing.T) {
	for _, test := range []struct {
		name                  string
		round                 uint32
		height, valueDeadline uint64
		kind                  tasktypes.DeadlineKindV1
		deadline              uint64
	}{
		{"public challenge first", 1, 79, 120, tasktypes.DeadlineKindV1_DEADLINE_KIND_V1_CHALLENGE_WINDOW_CLOSE, 80},
		{"public challenge consumed", 1, 80, 120, tasktypes.DeadlineKindV1_DEADLINE_KIND_V1_VERIFIER_VALUE_EVIDENCE_WINDOW_CLOSE, 120},
		{"value evidence first", 1, 55, 60, tasktypes.DeadlineKindV1_DEADLINE_KIND_V1_VERIFIER_VALUE_EVIDENCE_WINDOW_CLOSE, 60},
		{"round two waiting without cursor", 2, 100, 120, tasktypes.DeadlineKindV1_DEADLINE_KIND_V1_VERIFIER_VALUE_EVIDENCE_WINDOW_CLOSE, 120},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := initFixture(t)
			genesis := taskGenesisV1(t, genesisChainID(f))
			require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
			core := genesis.TaskCores[0]
			key := taskKeyOf(core.TaskId)
			core.TaskPhase = tasktypes.TaskPhase_TASK_PHASE_REVEALING
			core.VerificationStatus = tasktypes.VerificationStatus_VERIFICATION_STATUS_VERIFICATION_PASSED
			require.NoError(t, f.keeper.TaskCore.Set(f.ctx, key, core))
			require.NoError(t, f.keeper.WriteVerificationRound(f.ctx, tasktypes.NewVerifyRoundKey(key, 1), tasktypes.VerificationRoundState{TaskId: core.TaskId, VerifyRound: 1,
				XClosedHeight: &tasktypes.VerificationRoundState_ClosedHeight{ClosedHeight: 50}, XVerdict: &tasktypes.VerificationRoundState_Verdict{Verdict: tasktypes.TaskVerdict_TASK_VERDICT_PASS}}))
			require.NoError(t, f.keeper.TaskRoundSummary.Set(f.ctx, key, tasktypes.TaskRoundSummaryState{TaskId: core.TaskId, MaxClosedRound: test.round,
				XChallengeOpenHeight: &tasktypes.TaskRoundSummaryState_ChallengeOpenHeight{ChallengeOpenHeight: 50}, XChallengeCloseHeight: &tasktypes.TaskRoundSummaryState_ChallengeCloseHeight{ChallengeCloseHeight: 80},
				XVerifierValueEvidenceCloseHeight: &tasktypes.TaskRoundSummaryState_VerifierValueEvidenceCloseHeight{VerifierValueEvidenceCloseHeight: test.valueDeadline}}))
			ctx := sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(test.height)))
			response, err := keeper.NewQueryServerImpl(f.keeper).TaskStage(ctx, &tasktypes.QueryTaskStageRequest{TaskId: core.TaskId})
			require.NoError(t, err)
			require.Equal(t, test.kind, response.Stage.GetNextDeadlineKind())
			require.Equal(t, test.deadline, response.Stage.GetNextDeadlineHeight())
		})
	}
}
