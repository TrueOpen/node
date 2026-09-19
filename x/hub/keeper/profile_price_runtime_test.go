package keeper_test

import (
	"bytes"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestProfilePriceSamplesAreReplaySafeAndAppliedOncePerProfile(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.Params.Set(f.ctx, types.DefaultHubParams()))
	const modelID = "price-sample-model"
	registerTestModelProfile(t, f, modelID, 1, testServiceBondMinInitial, 1)
	sdkCtx := sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(10).WithEventManager(sdk.NewEventManager())
	ctx := sdk.WrapSDKContext(sdkCtx)

	samples := []shared.ProfilePriceSampleV1{
		{TaskId: bytes.Repeat([]byte{0x11}, 32), ModelId: modelID, ProfileVersion: 1, PriceBid: 1_100},
		{TaskId: bytes.Repeat([]byte{0x12}, 32), ModelId: modelID, ProfileVersion: 1, PriceBid: 1_200},
		{TaskId: bytes.Repeat([]byte{0x13}, 32), ModelId: modelID, ProfileVersion: 1, PriceBid: 900},
	}
	for _, sample := range samples {
		require.NoError(t, f.keeper.RecordProfilePriceSample(ctx, sample))
	}
	require.NoError(t, f.keeper.RecordProfilePriceSample(ctx, samples[0]), "exact replay must not increment twice")
	conflict := samples[0]
	conflict.PriceBid++
	require.ErrorContains(t, f.keeper.RecordProfilePriceSample(ctx, conflict), "conflicting")

	visited, err := f.keeper.ProcessProfilePriceSamples(ctx, 10, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	profile, err := f.keeper.Profile.Get(ctx, types.NewProfileStateKey(modelID, 1))
	require.NoError(t, err)
	require.Equal(t, uint64(1_010), profile.RefPrice)
	require.Equal(t, uint64(10), profile.UpdatedHeight)
	require.Len(t, sdkCtx.EventManager().Events(), 1)
}
