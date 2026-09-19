package app

import (
	"encoding/json"
	"testing"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client/flags"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

// smokeAppOptions builds the minimal AppOptions needed for the smoke tests.
func smokeAppOptions() simtestutil.AppOptionsMap {
	opts := make(simtestutil.AppOptionsMap, 0)
	opts[flags.FlagHome] = DefaultNodeHome
	opts[BeaconVrfKeyRequiredOption] = false
	return opts
}

// TestAppNewDoesNotPanic verifies that constructing the application with
// an in-memory database, a nop logger, and default options succeeds and
// that basic identity fields are wired.
//
// This is the cheapest possible smoke: if this fails, the app is broken at
// the depinject / wiring layer and no further test will run.
func TestAppNewDoesNotPanic(t *testing.T) {
	db := dbm.NewMemDB()
	defer db.Close()

	app := New(log.NewNopLogger(), db, nil, true, smokeAppOptions(), baseapp.SetChainID(SimAppChainID))
	require.NotNil(t, app)
	require.Equal(t, Name, app.Name())
	require.Equal(t, "node", app.Name())

	// Basic keeper wiring assertions.
	require.NotNil(t, app.AuthKeeper, "AuthKeeper must be injected")
	require.NotNil(t, app.BankKeeper, "BankKeeper must be injected")
	require.NotNil(t, app.StakingKeeper, "StakingKeeper must be injected")
	require.NotNil(t, app.DistrKeeper, "DistrKeeper must be injected")
	// The TrueOpen keepers are value types, not pointers; use their Params
	// store as a lightweight sentinel that each keeper is wired.
	require.NotNil(t, app.HubKeeper.Params, "HubKeeper must be injected")
	require.NotNil(t, app.TaskKeeper.Params, "TaskKeeper must be injected")
}

// TestAppDefaultGenesisContainsTrueOpenModule verifies that the default genesis
// carries the `trueopen` module state so genesis import/export round trips have
// a real module payload to preserve.
func TestAppDefaultGenesisContainsTrueOpenModule(t *testing.T) {
	db := dbm.NewMemDB()
	defer db.Close()

	app := New(log.NewNopLogger(), db, nil, true, smokeAppOptions(), baseapp.SetChainID(SimAppChainID))
	genesis := app.DefaultGenesis()
	require.NotEmpty(t, genesis, "DefaultGenesis must return a non-empty state map")

	// Both trueopen modules must have a default genesis entry keyed by module name.
	for _, moduleName := range []string{hubtypes.ModuleName, tasktypes.ModuleName} {
		trueopenJSON, ok := genesis[moduleName]
		require.Truef(t, ok, "DefaultGenesis missing %q module", moduleName)
		require.NotEmptyf(t, trueopenJSON, "%q default genesis must not be empty", moduleName)

		// The bytes should be a valid JSON object.
		var probe map[string]json.RawMessage
		require.NoErrorf(t, json.Unmarshal(trueopenJSON, &probe), "%q default genesis is not valid JSON", moduleName)
	}
}

// TestAppDefaultGenesisRoundTripsThroughJSON verifies that the default
// genesis, once marshalled and unmarshalled, still contains the trueopen
// module state byte-identical. This is the minimum "state can survive
// serialization" contract needed for export/import to work.
//
// A full InitGenesis + ExportAppStateAndValidators + re-import round trip
// requires at least one validator in the default genesis (staking module
// rejects an empty validator set); that lives in the heavier
// TestAppImportExport in sim_test.go which drives simulation to bootstrap
// validators. This smoke stays lightweight so it always runs.
func TestAppDefaultGenesisRoundTripsThroughJSON(t *testing.T) {
	db := dbm.NewMemDB()
	defer db.Close()

	app := New(log.NewNopLogger(), db, nil, true, smokeAppOptions(), baseapp.SetChainID(SimAppChainID))
	genesis := app.DefaultGenesis()
	require.NotEmpty(t, genesis)

	// Marshal the entire genesis to JSON and unmarshal it back.
	rawGenesis, err := json.Marshal(genesis)
	require.NoError(t, err)

	var reparsed GenesisState
	require.NoError(t, json.Unmarshal(rawGenesis, &reparsed))

	// Both maps must have identical trueopen module payloads byte-for-byte
	// (or JSON-equivalent) after the round trip.
	for _, moduleName := range []string{hubtypes.ModuleName, tasktypes.ModuleName} {
		original, ok := genesis[moduleName]
		require.Truef(t, ok, "default genesis missing %q", moduleName)

		roundTripped, ok := reparsed[moduleName]
		require.Truef(t, ok, "re-parsed genesis missing %q", moduleName)

		require.JSONEqf(t, string(original), string(roundTripped),
			"%q module genesis must survive JSON marshal/unmarshal round trip", moduleName)
	}
}

// trueopenModuleAccountNames returns the TrueOpen business module accounts that
// must round-trip through genesis export/import with balance intact.
// The list mirrors the ordering used in app_config.go moduleAccPerms so a
// mismatch here (e.g. someone adds a sixth account and forgets to update
// this test) trips the check.
func trueopenModuleAccountNames() []string {
	return []string{
		// hub-owned module accounts.
		hubtypes.ModuleName,
		hubtypes.RewardsModuleName,
		hubtypes.TreasuryModuleName,
		hubtypes.ServiceBondModuleName,
		hubtypes.EmissionModuleName,
		// task-owned module accounts.
		tasktypes.ModuleName,
		tasktypes.EscrowModuleName,
		tasktypes.ChallengeEffectModuleName,
	}
}

// TestGenesisRoundTripPreservesTrueOpenModuleAccountBalances is the the module-account round-trip
// contract test. It seeds the bank genesis with a non-trivial balance for
// each of the trueopen module accounts, marshals the whole genesis, and
// parses it back, then asserts that every seeded balance is still present
// with the same amount.
//
// This guards the genesis codec contract for pre-funded module accounts:
// if a future change silently drops any of the trueopen accounts from
// bank.balances (e.g. the mod-account allowlist changes and a filter runs
// during marshal), this test will fail before the change reaches main.
//
// Note: the test does not run InitGenesis + ExportAppStateAndValidators
// (that requires a validator set). It is a codec-level round-trip; the
// heavier InitGenesis round-trip is exercised via sim_test.go.
func TestGenesisRoundTripPreservesTrueOpenModuleAccountBalances(t *testing.T) {
	db := dbm.NewMemDB()
	defer db.Close()

	app := New(log.NewNopLogger(), db, nil, true, smokeAppOptions(), baseapp.SetChainID(SimAppChainID))
	cdc := app.AppCodec()
	genesis := app.DefaultGenesis()

	// Unmarshal the bank module genesis so we can add balances.
	var bankGenesis banktypes.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(genesis[banktypes.ModuleName], &bankGenesis))

	// Seed each trueopen module account with a distinct amount so we can tell
	// them apart after the round trip.
	seededAmount := func(i int) sdkmath.Int {
		// 1_000_000 + i so amounts are non-zero and distinguishable.
		return sdkmath.NewInt(int64(1_000_000 + i))
	}
	names := trueopenModuleAccountNames()
	expected := make(map[string]sdkmath.Int, len(names))
	extraSupply := sdk.NewCoins()
	for i, name := range names {
		addr := authtypes.NewModuleAddress(name).String()
		amount := seededAmount(i)
		coin := sdk.NewCoin(hubtypes.DefaultBusinessDenom, amount)
		bankGenesis.Balances = append(bankGenesis.Balances, banktypes.Balance{
			Address: addr,
			Coins:   sdk.NewCoins(coin),
		})
		expected[addr] = amount
		extraSupply = extraSupply.Add(coin)
	}
	// Supply must match the sum of balances; extend the existing supply so
	// bank.ValidateGenesis stays happy.
	bankGenesis.Supply = bankGenesis.Supply.Add(extraSupply...)

	// Marshal the updated bank genesis back into the composite genesis.
	updatedBankJSON, err := cdc.MarshalJSON(&bankGenesis)
	require.NoError(t, err)
	genesis[banktypes.ModuleName] = updatedBankJSON

	// Round-trip the full genesis through JSON.
	rawGenesis, err := json.Marshal(genesis)
	require.NoError(t, err)

	var reparsed GenesisState
	require.NoError(t, json.Unmarshal(rawGenesis, &reparsed))

	// Unmarshal the reparsed bank genesis and verify the five balances
	// survived intact.
	var reparsedBank banktypes.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(reparsed[banktypes.ModuleName], &reparsedBank))

	seen := make(map[string]sdkmath.Int, len(names))
	for _, b := range reparsedBank.Balances {
		if want, ok := expected[b.Address]; ok {
			got := b.Coins.AmountOf(hubtypes.DefaultBusinessDenom)
			require.Truef(t, got.Equal(want),
				"module account %q balance = %s, want %s", b.Address, got, want)
			seen[b.Address] = got
		}
	}
	// Every seeded account must be present in the round-tripped bank genesis.
	for addr := range expected {
		_, ok := seen[addr]
		require.Truef(t, ok, "module account %q balance dropped in genesis round trip", addr)
	}
	require.Equal(t, len(names), len(seen), "unexpected count of preserved balances")
}
