package keeper

import (
	"bytes"
	"testing"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	"github.com/TrueOpen/node/x/task/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func setWorkerPolicy(t *testing.T, f *internalFixture, stake uint32) {
	t.Helper()
	updateParams(t, f, func(p *types.TaskParamsV1) {
		p.Weights.WorkerStakeWeightPpm = stake
		p.Weights.WorkerPerformanceWeightPpm = 1_000_000 - stake
	})
}

func TestParamsHistorySurvivesReferencesAndPrunesAfterEpochFloor(t *testing.T) {
	f := initInternalFixture(t)
	length := hubtypes.DefaultHubParams().Epoch.EpochLengthBlocks
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithChainID("params-history-test").WithBlockHeight(int64(length + 10)))
	id := bytes.Repeat([]byte{0x61}, 32)
	original, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	require.NoError(t, f.keeper.PinSessionParams(f.ctx, id))
	setWorkerPolicy(t, f, 400_000)
	frozen, err := f.keeper.GetWorkerCandidateWeightPolicy(f.ctx, length)
	require.NoError(t, err)
	require.Equal(t, original.Weights.WorkerStakeWeightPpm, frozen.StakeWeightPPM)
	current, err := f.keeper.GetWorkerCandidateWeightPolicy(f.ctx, length+10)
	require.NoError(t, err)
	require.Equal(t, uint32(400_000), current.StakeWeightPPM)
	queue := &paramsHistoryPruneQueue{k: f.keeper}
	_, found, err := queue.head(f.ctx, 2*length+1)
	require.NoError(t, err)
	require.False(t, found, "a referenced body has no prune row")
	require.NoError(t, f.keeper.UnpinSessionParams(f.ctx, id))
	queue = &paramsHistoryPruneQueue{k: f.keeper}
	_, found, err = queue.head(f.ctx, 2*length)
	require.NoError(t, err)
	require.False(t, found, "recent epoch lookup must retain an unreferenced body")
	_, found, err = queue.head(f.ctx, 2*length+1)
	require.NoError(t, err)
	require.True(t, found)
	usage, err := queue.run(f.ctx, 2*length+1, 1, 1<<20)
	require.NoError(t, err)
	require.Equal(t, uint64(1), usage.visited)
	exists, err := f.keeper.ParamsHistory.Has(f.ctx, 0)
	require.NoError(t, err)
	require.False(t, exists)
	exists, err = f.keeper.ParamsVersionRefCount.Has(f.ctx, 0)
	require.NoError(t, err)
	require.False(t, exists)
	_, err = f.keeper.paramsAtVersion(f.ctx, 0)
	require.Error(t, err, "a removed historical body cannot fall back to current parameters")
}

func TestParamsHeightLookupChoosesLastUpdateAtSameHeight(t *testing.T) {
	f := initInternalFixture(t)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithChainID("params-history-test").WithBlockHeight(100))
	setWorkerPolicy(t, f, 400_000)
	setWorkerPolicy(t, f, 500_000)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(150))
	setWorkerPolicy(t, f, 600_000)
	policy, err := f.keeper.GetWorkerCandidateWeightPolicy(f.ctx, 100)
	require.NoError(t, err)
	require.Equal(t, uint32(500_000), policy.StakeWeightPPM)
	policy, err = f.keeper.GetWorkerCandidateWeightPolicy(f.ctx, 99)
	require.NoError(t, err)
	require.Equal(t, uint32(300_000), policy.StakeWeightPPM)
	_, err = f.keeper.GetWorkerCandidateWeightPolicy(f.ctx, 151)
	require.Error(t, err)
}

func TestGenesisRejectsParameterHistoryAndBindingCorruption(t *testing.T) {
	f := newDispatchFixture(t)
	setWorkerPolicy(t, f.internalFixture, 400_000)
	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Len(t, exported.ParamsHistory, 1)
	require.Len(t, exported.SessionParamsVersionBindings, 1)
	for _, test := range []struct {
		name   string
		change func(*types.GenesisState)
	}{
		{"missing history", func(g *types.GenesisState) { g.ParamsHistory = nil }},
		{"duplicate history", func(g *types.GenesisState) { g.ParamsHistory = append(g.ParamsHistory, g.ParamsHistory[0]) }},
		{"current override", func(g *types.GenesisState) {
			g.SessionParamsVersionBindings[0].ParamsVersion = g.ParamsMeta.ParamsVersion
		}},
		{"unknown object", func(g *types.GenesisState) { g.SessionParamsVersionBindings[0].ObjectId = bytes.Repeat([]byte{9}, 32) }},
		{"future history", func(g *types.GenesisState) { g.ParamsHistory[0].SupersededHeight++ }},
	} {
		t.Run(test.name, func(t *testing.T) {
			clone := *exported
			clone.ParamsHistory = append([]types.TaskParamsHistoryState(nil), exported.ParamsHistory...)
			clone.SessionParamsVersionBindings = append([]types.ParamsVersionBindingState(nil), exported.SessionParamsVersionBindings...)
			test.change(&clone)
			require.Error(t, types.ValidateParamsVersionGenesis(clone))
		})
	}
	require.NoError(t, f.keeper.ParamsVersionRefCount.Set(f.ctx, 0, 2))
	require.ErrorContains(t, f.keeper.EnsureParamsVersionInvariant(f.ctx), "reference count")
}
