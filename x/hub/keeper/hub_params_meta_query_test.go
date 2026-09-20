package keeper_test

import (
	"context"
	"testing"

	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
)

// staticTaskSafetyWindows is the cross-module dependency UpdateHubParams needs
// for ValidateServiceUnbondingCoverage. Zero windows keep the coverage check
// dependent only on the hub's own defaults, which is what this test is about.
type staticTaskSafetyWindows types.TaskSafetyWindows

func (w staticTaskSafetyWindows) GetTaskSafetyWindows(context.Context) (types.TaskSafetyWindows, error) {
	return types.TaskSafetyWindows(w), nil
}

// TestHubParamsQuerySurfacesParamsVersion pins the only channel a client has for
// learning params_version.
//
// MsgUpdateHubParams is optimistic concurrency: expected_version must equal the
// stored params_version or the message is rejected with
// ErrHubParamsVersionMismatch. hub.v1.Query/Params is the one RPC that can
// report that number, and it used to leave Meta at its zero value — so every
// client read version 0 forever and the second update on any chain could only
// ever fail. task.v1.Query/Params already answers with its own Meta; this is
// the same contract.
//
// The loop below therefore takes expected_version from nothing but the query,
// twice. Against the unfixed handler the second round fails.
func TestHubParamsQuerySurfacesParamsVersion(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))

	query := keeper.NewQueryServerImpl(f.keeper)
	msg := keeper.NewMsgServerImpl(f.keeper, types.MsgServerDependencies{
		ValidatorSnapshotProvider: deterministicValidatorSnapshots{historicalEntries: 10_000},
		TaskSafetyWindowProvider:  staticTaskSafetyWindows{},
	})
	authority := authtypes.NewModuleAddress(types.GovModuleName).String()

	fresh, err := query.Params(f.ctx, &types.QueryHubParamsRequest{})
	require.NoError(t, err)
	require.Zero(t, fresh.Meta.ParamsVersion,
		"a store that predates the first update has no meta row; 0 is the only expected_version that can match")
	require.Empty(t, fresh.Meta.ParamsHash)

	// max_model_support_prune_items_per_block is purely operational — not in
	// GenesisOnlyHubParamsChanged — so the only thing that can reject these calls
	// is the version check itself.
	for round, prune := range []uint32{7, 9} {
		current, err := query.Params(f.ctx, &types.QueryHubParamsRequest{})
		require.NoError(t, err)
		require.EqualValues(t, round, current.Meta.ParamsVersion, "round %d: query must report the stored version", round)

		next := current.Params
		next.Support.MaxModelSupportPruneItemsPerBlock = prune
		response, err := msg.UpdateHubParams(f.ctx, &types.MsgUpdateHubParams{
			Authority:       authority,
			Params:          next,
			ExpectedVersion: current.Meta.ParamsVersion,
		})
		require.NoError(t, err, "round %d: expected_version came straight from the query and must match", round)
		require.EqualValues(t, round+1, response.NewVersion)

		after, err := query.Params(f.ctx, &types.QueryHubParamsRequest{})
		require.NoError(t, err)
		require.Equal(t, response.NewVersion, after.Meta.ParamsVersion,
			"round %d: the query must move with the update, otherwise the next one is unverifiable", round)
		require.Equal(t, response.ParamsHash, after.Meta.ParamsHash)
		require.Equal(t, prune, after.Params.Support.MaxModelSupportPruneItemsPerBlock)
	}

	// The mismatch branch still has to bite: a stale version must not pass.
	stale, err := query.Params(f.ctx, &types.QueryHubParamsRequest{})
	require.NoError(t, err)
	_, err = msg.UpdateHubParams(f.ctx, &types.MsgUpdateHubParams{
		Authority:       authority,
		Params:          stale.Params,
		ExpectedVersion: stale.Meta.ParamsVersion - 1,
	})
	require.ErrorIs(t, err, types.ErrHubParamsVersionMismatch)
}
