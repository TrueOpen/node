package keeper_test

import (
	"encoding/hex"
	"testing"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestWorkerSettlementSampleClosesAndPrunesRewardCompetition(t *testing.T) {
	f, identity, request := seedJailAdmissionLiabilityFixture(t, 9)
	request.Duty = shared.DutyWorker
	_, err := f.keeper.ReserveTaskLiabilityFromFrozenFact(f.ctx, request)
	require.NoError(t, err)
	require.NoError(t, f.keeper.ReleaseTaskLiabilities(
		f.ctx, "reward-sample-session", hex.EncodeToString(request.TaskID), 3,
	))
	fact := types.TaskSupportCompletionFact{
		TaskID: request.TaskID, OperatorAddress: identity.Address,
		ModelID: request.ModelID, ProfileVersion: request.ProfileVersion,
		Duty: shared.DutyWorker, RewardBucket: types.RewardBucket_REWARD_BUCKET_P0,
		OrderValue: request.OrderValue, Height: 3,
	}
	require.NoError(t, f.keeper.RecordTaskSupportCompletion(f.ctx, fact))
	require.NoError(t, f.keeper.RecordTaskSupportCompletion(f.ctx, fact), "exact replay must not increment the histogram")

	competitionKey := types.NewRewardCompetitionEpochKey(uint64(fact.RewardBucket), 0)
	competition, err := f.keeper.RewardCompetitionEpoch.Get(f.ctx, competitionKey)
	require.NoError(t, err)
	require.Equal(t, uint64(1), competition.TotalTasks)
	require.Equal(t, uint64(1), sumUint64(competition.OrderValueHist))
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	_, epochEnd, err := shared.EpochHeightRange(0, params.Epoch.EpochLengthBlocks)
	require.NoError(t, err)
	has, err := f.keeper.RewardEpochIndex.Has(f.ctx, types.NewRewardEpochIndexKey(epochEnd+1, 0, uint64(fact.RewardBucket)))
	require.NoError(t, err)
	require.True(t, has)

	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(epochEnd + 1)))
	visited, err := f.keeper.ProcessDueRewardEpochs(f.ctx, epochEnd+1, 3)
	require.NoError(t, err)
	require.Equal(t, uint64(3), visited)
	competition, err = f.keeper.RewardCompetitionEpoch.Get(f.ctx, competitionKey)
	require.NoError(t, err)
	require.True(t, competition.RewardEpochClosed)

	genesis, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Len(t, genesis.RewardEpochPruneIndexes, 1)
	tampered := *genesis
	tampered.RewardEpochPruneIndexes = append(
		append([]types.RewardEpochPruneIndex(nil), genesis.RewardEpochPruneIndexes...),
		genesis.RewardEpochPruneIndexes[0],
	)
	require.ErrorContains(t, tampered.Validate(), "duplicate reward prune index")
	restarted := initFixture(t)
	restarted.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(restarted.ctx).WithChainID(sdk.UnwrapSDKContext(f.ctx).ChainID()))
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *genesis))
	require.NoError(t, restarted.keeper.EnsureRewardEpochInvariant(restarted.ctx))

	pruneEpoch := uint64(params.Reward.RewardAuditRetentionEpochs)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(pruneEpoch * params.Epoch.EpochLengthBlocks)))
	visited, err = f.keeper.ProcessDueRewardEpochPrunes(f.ctx, pruneEpoch, uint64(sdk.UnwrapSDKContext(f.ctx).BlockHeight()), 3)
	require.NoError(t, err)
	require.Equal(t, uint64(3), visited)
	_, err = f.keeper.RewardCompetitionEpoch.Get(f.ctx, competitionKey)
	require.ErrorIs(t, err, collections.ErrNotFound)
	audit, err := f.keeper.RewardEpochAudit.Get(f.ctx, types.NewRewardEpochCursorKey(0, uint64(fact.RewardBucket)))
	require.NoError(t, err)
	require.Len(t, audit.AuditRoot, 32)
	require.Zero(t, audit.ContributionCount)
	require.NoError(t, f.keeper.EnsureRewardEpochInvariant(f.ctx))
}

// TestWorkerSettlementSampleTargetsExecutionEpochNotFactHeightEpoch pins the
// fix for node issue #16: fact.Height is the task's finality height
// (rounds_closed_height), which can already be behind whichever epoch is open
// by the time settlement actually records the completion. Deriving the
// reward-competition sample's target epoch from fact.Height instead of the
// executing block height sends the sample at a since-closed epoch, and
// RewardCompetitionEpoch refuses to accept it -- that error is wrapped as
// ErrInvariantBroken by settlement, which halts the chain instead of just
// failing one message.
func TestWorkerSettlementSampleTargetsExecutionEpochNotFactHeightEpoch(t *testing.T) {
	f, identity, request := seedJailAdmissionLiabilityFixture(t, 15)
	request.Duty = shared.DutyWorker
	_, err := f.keeper.ReserveTaskLiabilityFromFrozenFact(f.ctx, request)
	require.NoError(t, err)
	require.NoError(t, f.keeper.ReleaseTaskLiabilities(
		f.ctx, "reward-sample-late-session", hex.EncodeToString(request.TaskID), 3,
	))

	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	epochLength := params.Epoch.EpochLengthBlocks
	const factHeight = uint64(3)
	factEpoch := shared.EpochForHeight(factHeight, epochLength)
	require.Zero(t, factEpoch, "fact.Height must fall in epoch 0 for this scenario")

	boundariesHash, err := types.RewardOrderValueBucketBoundariesHash(
		sdk.UnwrapSDKContext(f.ctx).ChainID(), params.Reward.OrderValueBucketBoundaries,
	)
	require.NoError(t, err)
	fact := types.TaskSupportCompletionFact{
		TaskID: request.TaskID, OperatorAddress: identity.Address,
		ModelID: request.ModelID, ProfileVersion: request.ProfileVersion,
		Duty: shared.DutyWorker, RewardBucket: types.RewardBucket_REWARD_BUCKET_P0,
		OrderValue: request.OrderValue, Height: factHeight,
	}
	closedCompetition := types.RewardCompetitionEpochState{
		Epoch:                          factEpoch,
		RewardBucket:                   fact.RewardBucket,
		OrderValueHist:                 make([]uint64, len(params.Reward.OrderValueBucketBoundaries)+1),
		OrderValueBucketBoundariesHash: boundariesHash,
		CumulativeFeeAmount:            shared.NewAmount(0),
		BootstrapStatus:                types.RewardCompetitionBootstrapStatus_REWARD_COMPETITION_BOOTSTRAP_STATUS_BOOTSTRAP,
		P30Cutoff:                      shared.NewAmount(0),
		RewardEpochClosed:              true,
	}
	require.NoError(t, f.keeper.RewardCompetitionEpoch.Set(
		f.ctx, types.NewRewardCompetitionEpochKey(uint64(fact.RewardBucket), factEpoch), closedCompetition,
	))

	_, epochEnd, err := shared.EpochHeightRange(factEpoch, epochLength)
	require.NoError(t, err)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(epochEnd + 1)))
	require.NotZero(t, shared.EpochForHeight(epochEnd+1, epochLength), "settlement must now be executing past epoch 0")

	require.NoError(t, f.keeper.RecordTaskSupportCompletion(f.ctx, fact))

	executionEpoch := shared.EpochForHeight(epochEnd+1, epochLength)
	competition, err := f.keeper.RewardCompetitionEpoch.Get(
		f.ctx, types.NewRewardCompetitionEpochKey(uint64(fact.RewardBucket), executionEpoch),
	)
	require.NoError(t, err)
	require.Equal(t, uint64(1), competition.TotalTasks)
}

func sumUint64(values []uint64) uint64 {
	var total uint64
	for _, value := range values {
		total += value
	}
	return total
}
