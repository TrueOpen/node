package app

// RewardEligibility query integration (MarkGate).
//
// Spec §14 wants Node to expose the authoritative MarkGate /
// epoch-eligibility read-side. This file proves the handler works
// end-to-end through the depinject-wired app — keeper unit tests exercise
// the collections math, but not the codec + query server + address
// validation layers a live client hits.
//
// The PerformanceScore half of this file is deleted. Ruling 21
// fixes performance_score_snapshot_ppm at the neutral 1_000_000 with
// PERFORMANCE_RAW_Q16_V1, so there is no PerformanceScoreState collection and
// no QueryPerformanceScore rpc left to exercise. If a stored performance score is reintroduced,
// stored performance score, restore the seeded-state and error-contract cases
// here (they were: exact-field readback, nil request, empty role_address,
// empty role, unknown (address, role)).

import (
	"testing"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"
)

// bootAppMinimal spins up a validator + delegator genesis and returns
// the running app. Meant for read-side query tests that don't touch bank
// or reward pipelines directly.
func bootAppMinimal(t *testing.T) *App {
	t.Helper()
	db := dbm.NewMemDB()
	t.Cleanup(func() { _ = db.Close() })

	app := New(log.NewNopLogger(), db, nil, true, smokeAppOptions(),
		baseapp.SetChainID(SimAppChainID))

	valSet, err := simtestutil.CreateRandomValidatorSet()
	require.NoError(t, err)
	delegPriv := secp256k1.GenPrivKey()
	delegAcc := authtypes.NewBaseAccount(delegPriv.PubKey().Address().Bytes(), delegPriv.PubKey(), 0, 0)
	balances := []banktypes.Balance{{
		Address: delegAcc.GetAddress().String(),
		Coins:   sdk.NewCoins(sdk.NewCoin(sdk.DefaultBondDenom, sdkmath.NewInt(100_000_000_000_000))),
	}}
	genesisState, err := simtestutil.GenesisStateWithValSet(app.AppCodec(), app.DefaultGenesis(), valSet,
		[]authtypes.GenesisAccount{delegAcc}, balances...)
	require.NoError(t, err)
	stateBytes, err := cmtjson.MarshalIndent(genesisState, "", " ")
	require.NoError(t, err)

	initChainAndCommit(t, app, stateBytes, valSet.Hash())
	return app
}
