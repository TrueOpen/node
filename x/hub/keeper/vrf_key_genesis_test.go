package keeper_test

// §9.3a Genesis import/export tests for the VRF registry.
//
// The core property pinned here is "the genesis can start a chain": when a
// production genesis sets vrf_required_from_height to 1, the very first block
// already requires the proposer to have an active VRF public key. If InitGenesis
// does not persist GenesisState.vrf_keys, ActiveVrfPubkeyForHeight always returns
// ErrNoActiveVrfKey, and the chain deadlocks at height 1 with no on-chain remedy
// available (MsgRegisterVrfKey itself needs a block to be produced first).

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
)

// genesisWithVrfKeys hangs one active operator and one operator mid-rotation onto
// the default genesis.
func genesisWithVrfKeys(t *testing.T) (gs types.GenesisState, active, rotating string) {
	t.Helper()
	active = hubAddress(t, 0x61)
	rotating = hubAddress(t, 0x62)

	gs = *types.DefaultGenesis()
	gs.VrfKeys = []types.VrfKeyState{
		{
			OperatorAddress:       active,
			ActiveVrfPubkey:       vrfPubkey(0x01),
			ActiveFromEpoch:       0,
			VrfAuthorizationNonce: 1,
		},
		{
			OperatorAddress:       rotating,
			ActiveVrfPubkey:       vrfPubkey(0x02),
			ActiveFromEpoch:       0,
			XPendingVrfPubkey:     &types.VrfKeyState_PendingVrfPubkey{PendingVrfPubkey: vrfPubkey(0x03)},
			XPendingFromEpoch:     &types.VrfKeyState_PendingFromEpoch{PendingFromEpoch: 4},
			VrfAuthorizationNonce: 2,
		},
	}
	return gs, active, rotating
}

// TestVrfKeyGenesisBootsAValidatorThatCanProposeImmediately covers genesis protocol
// §4 "Genesis writes VrfKeyState(effective_from_epoch=0) for every entry": right
// after the import, the verification public key already resolves at height 1.
func TestVrfKeyGenesisBootsAValidatorThatCanProposeImmediately(t *testing.T) {
	f := initFixture(t)
	gs, active, _ := genesisWithVrfKeys(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, gs))

	pubkey, err := f.keeper.ActiveVrfPubkeyForHeight(f.ctx, active, 1)
	require.NoError(t, err, "the active public key imported from genesis must be usable on the first block, otherwise a chain with vrf_required_from_height=1 cannot start")
	require.Equal(t, vrfPubkey(0x01), pubkey)
}

// TestVrfKeyGenesisRebuildsTheActivationIndex covers the requirement in
// genesis.pb.go:104-108: the index is not imported with the genesis but rebuilt
// from the pending pairs. A missing index row does not surface as an error but as
// that rotation never activating -- the only way to verify it is that sweeping the
// due epoch really does promote the key.
func TestVrfKeyGenesisRebuildsTheActivationIndex(t *testing.T) {
	f := initFixture(t)
	gs, _, rotating := genesisWithVrfKeys(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, gs))

	has, err := f.keeper.VrfKeyActivationIndex.Has(f.ctx, types.NewVrfKeyActivationKey(4, rotating))
	require.NoError(t, err)
	require.True(t, has, "a pending rotation must rebuild its activation index row")

	require.NoError(t, f.keeper.ActivateDueVrfKeys(f.ctx, 4))
	state, err := f.keeper.VrfKey.Get(f.ctx, rotating)
	require.NoError(t, err)
	require.Equal(t, vrfPubkey(0x03), state.ActiveVrfPubkey, "the rebuilt index must make the rotation actually take effect at its epoch")
	require.Nil(t, state.XPendingVrfPubkey)
}

// TestVrfKeyGenesisRoundTrips guarantees that an export can be fed back into an
// import: the upgrade / state-export paths depend on it.
func TestVrfKeyGenesisRoundTrips(t *testing.T) {
	f := initFixture(t)
	gs, _, _ := genesisWithVrfKeys(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, gs))

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, gs.VrfKeys, exported.VrfKeys)

	f2 := initFixture(t)
	require.NoError(t, f2.keeper.InitGenesis(f2.ctx, *exported))
	reexported, err := f2.keeper.ExportGenesis(f2.ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, exported.VrfKeys, reexported.VrfKeys)
	require.ElementsMatch(t, exported.VrfKeyHistory, reexported.VrfKeyHistory)
}

// TestEnsureVrfKeyActivationIndexInvariantCatchesBothDirections: an extra row makes
// ActivateDueVrfKeys revisit a key with no reachable state every epoch, a missing
// row makes the rotation never take effect, and both directions must be caught.
func TestEnsureVrfKeyActivationIndexInvariantCatchesBothDirections(t *testing.T) {
	t.Run("index has an extra row", func(t *testing.T) {
		f := initFixture(t)
		gs, active, _ := genesisWithVrfKeys(t)
		require.NoError(t, f.keeper.InitGenesis(f.ctx, gs))

		// The active operator has no pending rotation, yet carries an index row.
		require.NoError(t, f.keeper.VrfKeyActivationIndex.Set(f.ctx, types.NewVrfKeyActivationKey(9, active)))
		require.ErrorContains(t, f.keeper.EnsureVrfKeyActivationIndexInvariant(f.ctx), "no pending rotation")
	})

	t.Run("index is missing a row", func(t *testing.T) {
		f := initFixture(t)
		gs, _, rotating := genesisWithVrfKeys(t)
		require.NoError(t, f.keeper.InitGenesis(f.ctx, gs))

		require.NoError(t, f.keeper.VrfKeyActivationIndex.Remove(f.ctx, types.NewVrfKeyActivationKey(4, rotating)))
		require.ErrorContains(t, f.keeper.EnsureVrfKeyActivationIndexInvariant(f.ctx), "missing operator")
	})
}

// TestVrfKeyGenesisValidationRejectsUnusableRows: bad rows that can never be fixed
// once written into the genesis must be caught during Validate, rather than being
// discovered after the chain is up as a validator that can never propose a block.
func TestVrfKeyGenesisValidationRejectsUnusableRows(t *testing.T) {
	operator := hubAddress(t, 0x63)

	t.Run("neither an active nor a pending public key", func(t *testing.T) {
		gs := *types.DefaultGenesis()
		gs.VrfKeys = []types.VrfKeyState{{OperatorAddress: operator, VrfAuthorizationNonce: 1}}
		require.Error(t, gs.Validate())
	})

	t.Run("the same operator appears twice", func(t *testing.T) {
		gs := *types.DefaultGenesis()
		row := types.VrfKeyState{OperatorAddress: operator, ActiveVrfPubkey: vrfPubkey(0x01), VrfAuthorizationNonce: 1}
		gs.VrfKeys = []types.VrfKeyState{row, row}
		require.Error(t, gs.Validate())
	})

	t.Run("history row has no matching VrfKeyState", func(t *testing.T) {
		gs := *types.DefaultGenesis()
		gs.VrfKeyHistory = []types.VrfKeyHistoryState{{
			OperatorAddress:    operator,
			VrfPubkey:          vrfPubkey(0x01),
			EffectiveFromEpoch: 1,
			RetiredAtEpoch:     2,
		}}
		require.Error(t, gs.Validate())
	})
}
