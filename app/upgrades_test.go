package app

import (
	"errors"
	"testing"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	upgradekeeper "cosmossdk.io/x/upgrade/keeper"
	upgradetypes "cosmossdk.io/x/upgrade/types"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	"github.com/stretchr/testify/require"

	hubmodule "github.com/TrueOpen/node/x/hub/module"
	taskmodule "github.com/TrueOpen/node/x/task/module"
)

// bootUpgradeApp boots a single-validator app whose home directory is private
// to the test: an upgrade that halts writes upgrade-info.json under the home,
// and that must never land in the developer's real node home.
func bootUpgradeApp(t *testing.T) (*App, []byte) {
	t.Helper()
	db := dbm.NewMemDB()
	t.Cleanup(func() { _ = db.Close() })

	opts := smokeAppOptions()
	opts[flags.FlagHome] = t.TempDir()
	app := New(log.NewNopLogger(), db, nil, true, opts, baseapp.SetChainID(SimAppChainID))

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
	return app, valSet.Hash()
}

func upgradeCtx(app *App) sdk.Context {
	return app.NewUncachedContext(false, cmtproto.Header{Height: app.LastBlockHeight(), ChainID: SimAppChainID})
}

// scheduleUpgrade stores a plan directly, which is what a passed governance
// proposal does through the upgrade module's Msg server.
func scheduleUpgrade(t *testing.T, app *App, name string, height int64) {
	t.Helper()
	require.NoError(t, app.UpgradeKeeper.ScheduleUpgrade(upgradeCtx(app), upgradetypes.Plan{Name: name, Height: height}))
	// The uncached context writes into the working tree, which the next block
	// reads and commits; committing the store here would skip a height.
}

func finalizeNext(app *App, valHash []byte) error {
	height := app.LastBlockHeight() + 1
	_, err := app.FinalizeBlock(&abci.RequestFinalizeBlock{
		Height:             height,
		Hash:               testFinalizeBlockHash(height),
		NextValidatorsHash: valHash,
	})
	return err
}

func TestUpgradeAuthority_IsGovernance(t *testing.T) {
	app, _ := bootUpgradeApp(t)
	govAuthority := authtypes.NewModuleAddress(govtypes.ModuleName).String()

	server := upgradekeeper.NewMsgServerImpl(app.UpgradeKeeper)
	plan := upgradetypes.Plan{Name: "v2", Height: app.LastBlockHeight() + 10}

	_, err := server.SoftwareUpgrade(upgradeCtx(app), &upgradetypes.MsgSoftwareUpgrade{
		Authority: sdk.AccAddress("not-the-gov-authority").String(),
		Plan:      plan,
	})
	require.Error(t, err, "only the governance module account may schedule an upgrade")

	_, err = server.SoftwareUpgrade(upgradeCtx(app), &upgradetypes.MsgSoftwareUpgrade{Authority: govAuthority, Plan: plan})
	require.NoError(t, err)
	stored, err := app.UpgradeKeeper.GetUpgradePlan(upgradeCtx(app))
	require.NoError(t, err)
	require.Equal(t, plan.Name, stored.Name)
}

func TestUpgrade_HandlerRunsAtPlanHeightAndKeepsState(t *testing.T) {
	app, valHash := bootUpgradeApp(t)
	advanceBlock(t, app, valHash)

	hubParamsBefore := app.HubKeeper.GetHubParams(upgradeCtx(app))
	versionsBefore, err := app.UpgradeKeeper.GetModuleVersions(upgradeCtx(app))
	require.NoError(t, err)

	var order []string
	upgradeHeight := app.LastBlockHeight() + 2
	scheduleUpgrade(t, app, "noop", upgradeHeight)

	// Blocks before the plan height are untouched by the pending plan.
	advanceBlock(t, app, valHash)

	// The old binary has no handler and halts at the plan height. The upgrade
	// module refuses a handler that is registered before that point, so the
	// new binary is swapped in only after the halt.
	require.ErrorContains(t, finalizeNext(app, valHash), `UPGRADE "noop" NEEDED`)
	app.UpgradeKeeper.SetUpgradeHandler("noop", app.upgradeHandler(upgrade{
		name: "noop",
		afterMigrations: func(sdk.Context) error {
			order = append(order, "after-migrations")
			return nil
		},
	}))
	require.Empty(t, order)

	require.Equal(t, upgradeHeight, advanceBlock(t, app, valHash))
	require.Equal(t, []string{"after-migrations"}, order)

	done, err := app.UpgradeKeeper.GetDoneHeight(upgradeCtx(app), "noop")
	require.NoError(t, err)
	require.Equal(t, upgradeHeight, done)
	_, err = app.UpgradeKeeper.GetUpgradePlan(upgradeCtx(app))
	require.ErrorIs(t, err, upgradetypes.ErrNoUpgradePlanFound, "an applied plan must be cleared")

	require.Equal(t, hubParamsBefore, app.HubKeeper.GetHubParams(upgradeCtx(app)), "a no-op upgrade must not change state")
	versionsAfter, err := app.UpgradeKeeper.GetModuleVersions(upgradeCtx(app))
	require.NoError(t, err)
	require.Equal(t, versionsBefore, versionsAfter)

	// The chain keeps producing blocks after the upgrade.
	advanceBlock(t, app, valHash)
}

func TestUpgrade_BinaryWithoutHandlerHaltsAtPlanHeight(t *testing.T) {
	app, valHash := bootUpgradeApp(t)
	scheduleUpgrade(t, app, "needed", app.LastBlockHeight()+2)
	advanceBlock(t, app, valHash)

	err := finalizeNext(app, valHash)
	require.ErrorContains(t, err, `UPGRADE "needed" NEEDED`)
}

func TestUpgrade_FailingHandlerHaltsChain(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		after func(sdk.Context) error
	}{
		{name: "post-migration step fails", after: func(sdk.Context) error { return errors.New("boom") }},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			app, valHash := bootUpgradeApp(t)
			app.UpgradeKeeper.SetUpgradeHandler("broken", app.upgradeHandler(upgrade{name: "broken", afterMigrations: testCase.after}))
			scheduleUpgrade(t, app, "broken", app.LastBlockHeight()+1)

			err := finalizeNext(app, valHash)
			require.ErrorContains(t, err, "boom")
			require.Equal(t, int64(1), app.LastBlockHeight(), "the failed upgrade block must not be committed")
		})
	}
}

func TestUpgrade_MigrationsRegisteredForBusinessModules(t *testing.T) {
	app, _ := bootUpgradeApp(t)
	versions, err := app.UpgradeKeeper.GetModuleVersions(upgradeCtx(app))
	require.NoError(t, err)
	got := make(map[string]uint64, len(versions))
	for _, version := range versions {
		got[version.Name] = version.Version
	}
	require.Equal(t, (hubmodule.AppModule{}).ConsensusVersion(), got["hub"])
	require.Equal(t, (taskmodule.AppModule{}).ConsensusVersion(), got["task"])
	require.Contains(t, got, upgradetypes.ModuleName)
}

func TestValidateUpgrades(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		upgrades []upgrade
		wantErr  string
	}{
		{name: "empty registry", upgrades: nil},
		{name: "distinct names", upgrades: []upgrade{{name: "a"}, {name: "b"}}},
		{name: "empty name", upgrades: []upgrade{{name: ""}}, wantErr: "must not be empty"},
		{name: "duplicate name", upgrades: []upgrade{{name: "a"}, {name: "a"}}, wantErr: "duplicate"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateUpgrades(testCase.upgrades)
			if testCase.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, testCase.wantErr)
		})
	}
}
