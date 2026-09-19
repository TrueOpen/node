package keeper_test

import (
	"testing"

	"cosmossdk.io/collections"
	proto "github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/task/types"
)

func TestGenesisV03RoundTripRebuildsDerivedIndexes(t *testing.T) {
	source := initFixture(t)
	input := taskGenesisV1(t, genesisChainID(source))
	require.NoError(t, source.keeper.InitGenesis(source.ctx, *input))

	exported, err := source.keeper.ExportGenesis(source.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(input, exported))

	taskKey := types.NewTaskKey(input.TaskCores[0].TaskId)
	inferKey := types.NewDeadlineIndexKey(input.TaskAssignments[0].InferDeadlineHeight, taskKey)
	has, err := source.keeper.InferDeadlineIndex.Has(source.ctx, inferKey)
	require.NoError(t, err)
	require.True(t, has)
	has, err = source.keeper.WorkerActiveTaskIndex.Has(source.ctx,
		types.NewWorkerActiveTaskKey(input.TaskAssignments[0].WinnerWorker, taskKey))
	require.NoError(t, err)
	require.True(t, has)
	has, err = source.keeper.SessionByOwnerIndex.Has(source.ctx,
		types.NewSessionByOwnerKey(input.Streams[0].OwnerUserAddress, input.Streams[0].SessionId))
	require.NoError(t, err)
	require.True(t, has)

	restarted := initFixture(t)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	reexported, err := restarted.keeper.ExportGenesis(restarted.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(exported, reexported))
}

func TestGenesisV03RebuildsVerifierWindowIndexes(t *testing.T) {
	f := initFixture(t)
	input := verifierSourceGenesisV1(t, genesisChainID(f))
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *input))

	window := input.VerifierCandidateWindows[0]
	taskKey := types.NewTaskKey(window.TaskId)
	has, err := f.keeper.VerifierWindowBuildIndex.Has(f.ctx,
		types.NewVerifyRoundIndexKey(window.WindowRandomnessHeight, taskKey, window.VerifyRound))
	require.NoError(t, err)
	require.True(t, has)
	has, err = f.keeper.VerifierHandraiseCloseIndex.Has(f.ctx,
		types.NewVerifyRoundIndexKey(window.HandraiseCloseHeight, taskKey, window.VerifyRound))
	require.NoError(t, err)
	require.True(t, has)
	has, err = f.keeper.VerifyOpenDeadlineIndex.Has(f.ctx,
		types.NewDeadlineIndexKey(window.AssignmentDeadlineHeight, taskKey))
	require.NoError(t, err)
	require.True(t, has)
}

func TestGenesisV03RejectedImportLeavesNoTaskWrites(t *testing.T) {
	f := initFixture(t)
	input := taskGenesisV1(t, genesisChainID(f))
	input.TaskCores = append(input.TaskCores, input.TaskCores[0])

	err := f.keeper.InitGenesis(f.ctx, *input)
	require.ErrorContains(t, err, "duplicate task core")
	_, err = f.keeper.Params.Get(f.ctx)
	require.ErrorIs(t, err, collections.ErrNotFound)
	has, err := f.keeper.TaskCore.Has(f.ctx, types.NewTaskKey(input.TaskCores[0].TaskId))
	require.NoError(t, err)
	require.False(t, has)
}
