package keeper_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
)

func vrfLifecycleGenesis(t *testing.T, operators ...string) types.GenesisState {
	t.Helper()
	gs := *types.DefaultGenesis()
	gs.Params.Epoch.EpochLengthBlocks = 10
	gs.Params.Epoch.DeltaWBlocks = 1
	gs.Params.Epoch.DeltaMBlocks = 2
	gs.Params.Beacon.MaxVrfKeyHistoryEpochs = 3
	for index, operator := range operators {
		gs.VrfKeys = append(gs.VrfKeys, types.VrfKeyState{
			OperatorAddress:       operator,
			ActiveVrfPubkey:       vrfPubkey(byte(index + 1)),
			ActiveFromEpoch:       0,
			XPendingVrfPubkey:     &types.VrfKeyState_PendingVrfPubkey{PendingVrfPubkey: vrfPubkey(byte(index + 11))},
			XPendingFromEpoch:     &types.VrfKeyState_PendingFromEpoch{PendingFromEpoch: 1},
			VrfAuthorizationNonce: 2,
		})
	}
	return gs
}

func TestEndBlockActivatesVrfKeyBeforeNextEpochProposal(t *testing.T) {
	operator := hubAddress(t, 0x71)

	t.Run("epoch final block activates next key", func(t *testing.T) {
		f := initFixture(t)
		gs := vrfLifecycleGenesis(t, operator)
		require.NoError(t, f.keeper.InitGenesis(f.ctx, gs))
		f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(9))

		require.NoError(t, f.keeper.EndBlocker(f.ctx))
		pubkey, err := f.keeper.ActiveVrfPubkeyForHeight(f.ctx, operator, 10)
		require.NoError(t, err)
		require.Equal(t, vrfPubkey(11), pubkey)
	})

	t.Run("non-final block leaves key pending", func(t *testing.T) {
		f := initFixture(t)
		gs := vrfLifecycleGenesis(t, operator)
		require.NoError(t, f.keeper.InitGenesis(f.ctx, gs))
		f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(8))

		require.NoError(t, f.keeper.EndBlocker(f.ctx))
		state, err := f.keeper.VrfKey.Get(f.ctx, operator)
		require.NoError(t, err)
		require.Equal(t, vrfPubkey(1), state.ActiveVrfPubkey)
		require.Equal(t, vrfPubkey(11), state.GetPendingVrfPubkey())
	})
}

func TestVrfKeyHistoryPruneLifecycle(t *testing.T) {
	operators := []string{hubAddress(t, 0x72), hubAddress(t, 0x73)}
	f := initFixture(t)
	gs := vrfLifecycleGenesis(t, operators...)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, gs))
	require.NoError(t, f.keeper.ActivateDueVrfKeys(f.ctx, 1))

	for _, operator := range operators {
		has, err := f.keeper.VrfKeyPruneIndex.Has(f.ctx, types.NewVrfKeyPruneKey(4, operator, 0))
		require.NoError(t, err)
		require.True(t, has)
	}
	require.NoError(t, f.keeper.EnsureVrfKeyPruneIndexInvariant(f.ctx))

	visited, _, err := f.keeper.ProcessVrfKeyHistoryPrunes(f.ctx, 3, 2, 1<<20)
	require.NoError(t, err)
	require.Zero(t, visited)

	visited, _, err = f.keeper.ProcessVrfKeyHistoryPrunes(f.ctx, 4, 1, 1<<20)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	visited, _, err = f.keeper.ProcessVrfKeyHistoryPrunes(f.ctx, 4, 1, 1<<20)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	visited, _, err = f.keeper.ProcessVrfKeyHistoryPrunes(f.ctx, 4, 1, 1<<20)
	require.NoError(t, err)
	require.Zero(t, visited)
	require.NoError(t, f.keeper.EnsureVrfKeyPruneIndexInvariant(f.ctx))
}

func TestVrfKeyGenesisRebuildsAndChecksPruneIndex(t *testing.T) {
	operator := hubAddress(t, 0x74)
	source := initFixture(t)
	gs := vrfLifecycleGenesis(t, operator)
	require.NoError(t, source.keeper.InitGenesis(source.ctx, gs))
	require.NoError(t, source.keeper.ActivateDueVrfKeys(source.ctx, 1))
	exported, err := source.keeper.ExportGenesis(source.ctx)
	require.NoError(t, err)

	restarted := initFixture(t)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	key := types.NewVrfKeyPruneKey(4, operator, 0)
	has, err := restarted.keeper.VrfKeyPruneIndex.Has(restarted.ctx, key)
	require.NoError(t, err)
	require.True(t, has)

	require.NoError(t, restarted.keeper.VrfKeyPruneIndex.Remove(restarted.ctx, key))
	require.ErrorContains(t, restarted.keeper.EnsureVrfKeyPruneIndexInvariant(restarted.ctx), "missing operator")
	require.NoError(t, restarted.keeper.VrfKeyPruneIndex.Set(restarted.ctx, key))
	extra := types.NewVrfKeyPruneKey(5, operator, 0)
	require.NoError(t, restarted.keeper.VrfKeyPruneIndex.Set(restarted.ctx, extra))
	require.ErrorContains(t, restarted.keeper.EnsureVrfKeyPruneIndexInvariant(restarted.ctx), "extra row")
}
