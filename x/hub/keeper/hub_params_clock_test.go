package keeper_test

import (
	"testing"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"
)

func TestHubGovernanceRejectsIncompatibleVerifyOpenClock(t *testing.T) {
	f := initFixture(t)
	genesis := types.DefaultGenesis()
	genesis.Params.Epoch.DeltaWBlocks = 1
	genesis.Params.Builder.OpenVerifyBuilderProposalWindowBlocks = 10
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	before, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	next := before
	next.Builder.OpenVerifyBuilderProposalWindowBlocks = 120
	server := keeper.NewMsgServerImpl(f.keeper, types.MsgServerDependencies{ValidatorSnapshotProvider: deterministicValidatorSnapshots{historicalEntries: 10_000}, TaskSafetyWindowProvider: staticTaskSafetyWindows{VerifyOpenDeadlineBlocks: 120, SelfRescueMarginBlocks: 10}})
	_, err = server.UpdateHubParams(f.ctx, &types.MsgUpdateHubParams{Authority: authtypes.NewModuleAddress(types.GovModuleName).String(), Params: next})
	require.Error(t, err, "Hub updates must not invalidate the Task verifier clock")
	after, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, before, after)
	meta, err := f.keeper.GetHubParamsMeta(f.ctx)
	require.NoError(t, err)
	require.Zero(t, meta.ParamsVersion)
}

func TestHubGovernanceCannotShortenRetainedOrderNotice(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	server := keeper.NewMsgServerImpl(f.keeper, types.MsgServerDependencies{ValidatorSnapshotProvider: deterministicValidatorSnapshots{historicalEntries: 10_000}, TaskSafetyWindowProvider: staticTaskSafetyWindows{MinimumBuilderSetLeadBlocks: 161}})
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	params.Builder.BuilderSetUpdateLeadBlocks = 160
	_, err = server.UpdateHubParams(f.ctx, &types.MsgUpdateHubParams{Authority: authtypes.NewModuleAddress(types.GovModuleName).String(), Params: params})
	require.ErrorIs(t, err, shared.ErrBuilderSetNoticeTooShort)
	params.Builder.BuilderSetUpdateLeadBlocks = 161
	_, err = server.UpdateHubParams(f.ctx, &types.MsgUpdateHubParams{Authority: authtypes.NewModuleAddress(types.GovModuleName).String(), Params: params})
	require.NoError(t, err)
}

// VRF may only be made mandatory from a future height: the blocks between an
// already reached activation height and the update carry placeholder beacons
// only, which stop being readable the moment the parameter takes effect.
func TestHubGovernanceRejectsVrfActivationAtAReachedHeight(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	server := keeper.NewMsgServerImpl(f.keeper, types.MsgServerDependencies{ValidatorSnapshotProvider: deterministicValidatorSnapshots{historicalEntries: 10_000}, TaskSafetyWindowProvider: staticTaskSafetyWindows{}})
	authority := authtypes.NewModuleAddress(types.GovModuleName).String()
	ctx := sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(100))
	params, err := f.keeper.Params.Get(ctx)
	require.NoError(t, err)
	require.Zero(t, params.Beacon.VrfRequiredFromHeight)

	for _, reached := range []uint64{1, 99, 100} {
		next := params
		next.Beacon.VrfRequiredFromHeight = reached
		_, err = server.UpdateHubParams(ctx, &types.MsgUpdateHubParams{Authority: authority, Params: next})
		require.ErrorIs(t, err, types.ErrInvalidBeacon, "height %d", reached)
	}
	after, err := f.keeper.Params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, params, after)

	next := params
	next.Beacon.VrfRequiredFromHeight = 101
	_, err = server.UpdateHubParams(ctx, &types.MsgUpdateHubParams{Authority: authority, Params: next})
	require.NotErrorIs(t, err, types.ErrInvalidBeacon)
}
