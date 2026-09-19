package keeper_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
)

func TestTaskFacingHubParamsProjectOpenVerifyClockInputs(t *testing.T) {
	f := initFixture(t)
	params := types.DefaultHubParams()
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))

	snapshot := f.keeper.GetHubParams(sdk.UnwrapSDKContext(f.ctx))
	require.Equal(t, params.Epoch.DeltaWBlocks, snapshot.DeltaWBlocks)
	require.Equal(t, params.Builder.OpenVerifyBuilderProposalWindowBlocks, snapshot.OpenVerifyBuilderProposalWindowBlocks)
	require.Equal(t, params.Builder.SettlementBuilderGraceBlocks, snapshot.SettlementBuilderGraceBlocks)
	require.Equal(t, params.Phase0.BusinessDenom, snapshot.BusinessDenom)
}
