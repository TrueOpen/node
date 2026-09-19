package keeper_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func TestEmptyVerifierUnionFallbackSurvivesGenesisRoundTrip(t *testing.T) {
	f := initFixture(t)
	genesis := verifierReadyGenesisV1(t, genesisChainID(f))
	window := genesis.VerifierCandidateWindows[0]
	genesis.VerifierAssignments = nil
	unions := genesis.TaskStageHandraiseUnions[:0]
	for _, union := range genesis.TaskStageHandraiseUnions {
		if union.Stage != tasktypes.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY {
			unions = append(unions, union)
		}
	}
	genesis.TaskStageHandraiseUnions = unions
	proposals := genesis.BuilderStageProposals[:0]
	for _, proposal := range genesis.BuilderStageProposals {
		if proposal.Stage != tasktypes.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY {
			proposals = append(proposals, proposal)
		}
	}
	genesis.BuilderStageProposals = proposals
	facts := genesis.TaskCandidateFacts[:0]
	for _, fact := range genesis.TaskCandidateFacts {
		if fact.Stage != tasktypes.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY {
			facts = append(facts, fact)
		}
	}
	genesis.TaskCandidateFacts = facts
	genesis.TaskCores[0].TaskPhase = tasktypes.TaskPhase_TASK_PHASE_RECEIPT_COMMITTED
	genesis.TaskCores[0].VerificationStatus = tasktypes.VerificationStatus_VERIFICATION_STATUS_VERIFY_COLLECTION_OPEN
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))

	taskKey := taskKeyOf(window.TaskId)
	closeKey := tasktypes.NewVerifyRoundIndexKey(window.HandraiseCloseHeight, taskKey, window.VerifyRound)
	fallbackKey := tasktypes.NewDeadlineIndexKey(window.AssignmentDeadlineHeight, taskKey)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(window.HandraiseCloseHeight)))
	require.NoError(t, f.keeper.EndBlocker(f.ctx))
	hasClose, err := f.keeper.VerifierHandraiseCloseIndex.Has(f.ctx, closeKey)
	require.NoError(t, err)
	require.False(t, hasClose)
	hasFallback, err := f.keeper.VerifyOpenDeadlineIndex.Has(f.ctx, fallbackKey)
	require.NoError(t, err)
	require.True(t, hasFallback)

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	restarted := initFixture(t)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	hasClose, err = restarted.keeper.VerifierHandraiseCloseIndex.Has(restarted.ctx, closeKey)
	require.NoError(t, err)
	require.True(t, hasClose, "derived close key is safe to replay after export/import")
	hasFallback, err = restarted.keeper.VerifyOpenDeadlineIndex.Has(restarted.ctx, fallbackKey)
	require.NoError(t, err)
	require.True(t, hasFallback)

	restarted.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(restarted.ctx).WithBlockHeight(int64(window.HandraiseCloseHeight)))
	require.NoError(t, restarted.keeper.EndBlocker(restarted.ctx))
	hasClose, err = restarted.keeper.VerifierHandraiseCloseIndex.Has(restarted.ctx, closeKey)
	require.NoError(t, err)
	require.False(t, hasClose)
	hasFallback, err = restarted.keeper.VerifyOpenDeadlineIndex.Has(restarted.ctx, fallbackKey)
	require.NoError(t, err)
	require.True(t, hasFallback)
}
