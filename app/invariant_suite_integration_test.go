package app

// Runs the split Hub/Task invariant registries against production App wiring.

import (
	"testing"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

func bootAppWithModuleBalances(t *testing.T, moduleBalances map[string]int64) *App {
	t.Helper()
	db := dbm.NewMemDB()
	t.Cleanup(func() { _ = db.Close() })
	app := New(log.NewNopLogger(), db, nil, true, smokeAppOptions(), baseapp.SetChainID(SimAppChainID))
	valSet, err := simtestutil.CreateRandomValidatorSet()
	require.NoError(t, err)
	priv := secp256k1.GenPrivKey()
	account := authtypes.NewBaseAccount(priv.PubKey().Address().Bytes(), priv.PubKey(), 0, 0)
	totalModuleBalance := int64(0)
	for _, amount := range moduleBalances {
		require.GreaterOrEqual(t, amount, int64(0))
		totalModuleBalance += amount
	}
	accountCoins := sdk.NewCoins(sdk.NewCoin(sdk.DefaultBondDenom, sdkmath.NewInt(100_000_000_000_000)))
	if totalModuleBalance > 0 {
		accountCoins = accountCoins.Add(sdk.NewCoin(hubtypes.DefaultBusinessDenom, sdkmath.NewInt(totalModuleBalance)))
	}
	balances := []banktypes.Balance{{Address: account.GetAddress().String(), Coins: accountCoins}}
	genesis, err := simtestutil.GenesisStateWithValSet(app.AppCodec(), app.DefaultGenesis(), valSet, []authtypes.GenesisAccount{account}, balances...)
	require.NoError(t, err)
	raw, err := cmtjson.MarshalIndent(genesis, "", " ")
	require.NoError(t, err)
	initChainAndCommit(t, app, raw, valSet.Hash())
	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: app.LastBlockHeight() + 1})
	for name, amount := range moduleBalances {
		if amount == 0 {
			continue
		}
		require.NoError(t, app.BankKeeper.SendCoinsFromAccountToModule(ctx, account.GetAddress(), name,
			sdk.NewCoins(sdk.NewCoin(hubtypes.DefaultBusinessDenom, sdkmath.NewInt(amount)))))
	}
	return app
}

func assertSplitInvariants(t *testing.T, app *App, ctx sdk.Context) {
	t.Helper()
	hubChecks := app.HubKeeper.InvariantChecks()
	taskChecks := app.TaskKeeper.InvariantChecks()
	require.NotEmpty(t, hubChecks)
	require.NotEmpty(t, taskChecks)
	for _, check := range hubChecks {
		require.NoErrorf(t, check.Check(ctx), "hub invariant %s", check.Name)
	}
	for _, check := range taskChecks {
		require.NoErrorf(t, check.Check(ctx), "task invariant %s", check.Name)
	}
}

func TestInvariantSuiteGreenOnDefaultGenesisApp(t *testing.T) {
	app := bootAppMinimal(t)
	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: app.LastBlockHeight() + 1})
	assertSplitInvariants(t, app, ctx)
}

func TestRuntimeInvariantRegistryRejectsBrokenFinalizeBlock(t *testing.T) {
	db := dbm.NewMemDB()
	t.Cleanup(func() { _ = db.Close() })
	app := New(log.NewNopLogger(), db, nil, true, smokeAppOptions(), baseapp.SetChainID(SimAppChainID))

	valSet, err := simtestutil.CreateRandomValidatorSet()
	require.NoError(t, err)
	priv := secp256k1.GenPrivKey()
	account := authtypes.NewBaseAccount(priv.PubKey().Address().Bytes(), priv.PubKey(), 0, 0)
	balances := []banktypes.Balance{{
		Address: account.GetAddress().String(),
		Coins: sdk.NewCoins(
			sdk.NewCoin(sdk.DefaultBondDenom, sdkmath.NewInt(100_000_000_000_000)),
			sdk.NewCoin(hubtypes.DefaultBusinessDenom, sdkmath.OneInt()),
		),
	}}
	genesis, err := simtestutil.GenesisStateWithValSet(app.AppCodec(), app.DefaultGenesis(), valSet,
		[]authtypes.GenesisAccount{account}, balances...)
	require.NoError(t, err)
	raw, err := cmtjson.MarshalIndent(genesis, "", " ")
	require.NoError(t, err)
	_, err = app.InitChain(&abci.RequestInitChain{
		ChainId: SimAppChainID, ConsensusParams: simtestutil.DefaultConsensusParams, AppStateBytes: raw,
	})
	require.NoError(t, err)
	ctx := app.NewContextLegacy(false, cmtproto.Header{Height: app.LastBlockHeight() + 1})
	require.NoError(t, app.BankKeeper.SendCoinsFromAccountToModule(
		ctx, account.GetAddress(), hubtypes.TreasuryModuleName,
		sdk.NewCoins(sdk.NewCoin(hubtypes.DefaultBusinessDenom, sdkmath.OneInt())),
	))

	height := app.LastBlockHeight() + 1
	_, err = app.FinalizeBlock(&abci.RequestFinalizeBlock{
		Height: height, Hash: testFinalizeBlockHash(height), NextValidatorsHash: valSet.Hash(),
	})
	require.ErrorContains(t, err, "runtime invariant hub/treasury_spend_receipt failed")
}
