package app

// Regression test for module account balances export/import.
//
// Background:
//
//	the module-account round-trip already covers the byte fidelity of the six
//	trueopen module accounts inside `bank.GenesisState.Balances` with a
//	codec-level JSON round-trip, but that path never touches baseapp or the
//	module manager — it only marshals and unmarshals a
//	map[string]json.RawMessage once. It cannot expose bugs on the real
//	export/import path such as:
//	  - `ModuleManager.ExportGenesisForModules` not writing the six module
//	    account balances back into the exported bank genesis;
//	  - `bank.ExportGenesis` filtering or allowlisting module accounts by
//	    mistake while walking balances through the keeper;
//	  - `authtypes.NewModuleAddress(name)` computing different addresses on the
//	    InitChain and the Export side (silent divergence after a module rename);
//	  - drift between `moduleAccPerms` in depinject and the keeper's permitted
//	    list, which would already reject the initial balance of some trueopen
//	    module account during InitChain.
//
// This test covers those blind spots with a full App lifecycle:
//  1. Build a genesis with one minimal validator and one delegator genesis
//     account (otherwise staking InitGenesis fails with "validator set is
//     empty" and the full export path cannot be exercised — which is also why
//     the module-account round-trip only does a codec round-trip);
//  2. Write a distinct business_denom amount into the genesis bank balances for
//     each of the six trueopen module accounts;
//  3. Call `app.InitChain(...)` to trigger the module manager's InitGenesis;
//  4. Call `app.ExportAppStateAndValidators(false, nil, nil)`, which is the
//     actual path `noded export` uses in production;
//  5. Parse the exported bank genesis and assert all six balances survive with
//     the same amounts;
//  6. Start a second, empty app, run InitChain over the exported AppState, and
//     read each module account's current balance from the BankKeeper, which
//     further verifies that export → import agrees in both directions.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
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
)

const moduleBalanceRoundTripDenom = "umodulefixture"

// buildGenesisWithModuleBalances constructs a valid genesis state that
// (a) contains one validator + one funded delegator account so staking's
// InitGenesis does not fail with "validator set is empty", and (b) seeds each
// of the TrueOpen module accounts with a distinct non-business test-denom balance.
//
// Returns the (raw JSON, expectedBalances, validatorSetHash) tuple used by
// the tests below. The validator set hash is required by FinalizeBlock.
func buildGenesisWithModuleBalances(t *testing.T, app *App) ([]byte, map[string]sdkmath.Int, []byte) {
	t.Helper()
	cdc := app.AppCodec()

	valSet, err := simtestutil.CreateRandomValidatorSet()
	require.NoError(t, err)

	priv := secp256k1.GenPrivKey()
	genAcc := authtypes.NewBaseAccount(priv.PubKey().Address().Bytes(), priv.PubKey(), 0, 0)
	accs := []authtypes.GenesisAccount{genAcc}

	// Delegator needs bond denom to back the validator; GenesisStateWithValSet
	// bumps supply automatically to match.
	balances := []banktypes.Balance{{
		Address: genAcc.GetAddress().String(),
		Coins:   sdk.NewCoins(sdk.NewCoin(sdk.DefaultBondDenom, sdkmath.NewInt(100_000_000_000_000))),
	}}

	names := trueopenModuleAccountNames()
	expected := make(map[string]sdkmath.Int, len(names))
	for i, name := range names {
		addr := authtypes.NewModuleAddress(name).String()
		amount := sdkmath.NewInt(int64(1_000_000 + i))
		balances = append(balances, banktypes.Balance{
			Address: addr,
			Coins:   sdk.NewCoins(sdk.NewCoin(moduleBalanceRoundTripDenom, amount)),
		})
		expected[addr] = amount
	}

	genesisState, err := simtestutil.GenesisStateWithValSet(cdc, app.DefaultGenesis(), valSet, accs, balances...)
	require.NoError(t, err)

	stateBytes, err := cmtjson.MarshalIndent(genesisState, "", " ")
	require.NoError(t, err)

	return stateBytes, expected, valSet.Hash()
}

// initChainAndCommit runs a full genesis-boot cycle: InitChain writes into
// the finalizeBlockState, then FinalizeBlock + Commit persist those writes
// to the app's committed multi-store and refresh checkState. Without the
// commit step, ExportAppStateAndValidators (which reads via
// NewContextLegacy(isCheckTx=true, ...)) sees an empty state and every
// module's ExportGenesis panics on the first collections.Get lookup.
func initChainAndCommit(t *testing.T, app *App, stateBytes []byte, valHash []byte) {
	t.Helper()

	_, err := app.InitChain(&abci.RequestInitChain{
		ChainId:         SimAppChainID,
		Validators:      []abci.ValidatorUpdate{},
		ConsensusParams: simtestutil.DefaultConsensusParams,
		AppStateBytes:   stateBytes,
	})
	require.NoError(t, err, "InitChain must accept a genesis with pre-funded trueopen module accounts")

	height := app.LastBlockHeight() + 1
	_, err = app.FinalizeBlock(&abci.RequestFinalizeBlock{
		Height:             height,
		Hash:               testFinalizeBlockHash(height),
		NextValidatorsHash: valHash,
	})
	require.NoError(t, err, "FinalizeBlock must succeed on the InitChain seed block")

	_, err = app.Commit()
	require.NoError(t, err, "Commit must persist the InitChain writes into the committed multi-store")
}

func testFinalizeBlockHash(height int64) []byte {
	var encodedHeight [8]byte
	binary.BigEndian.PutUint64(encodedHeight[:], uint64(height))
	digest := sha256.Sum256(append([]byte("trueopen-test-finalize-block"), encodedHeight[:]...))
	return digest[:]
}

// TestAppExportPreservesTrueOpenModuleAccountBalances runs the full production
// export pipeline (InitChain -> ExportAppStateAndValidators) and asserts every
// trueopen module account balance survives verbatim in the exported bank
// genesis.
//
// This closes the gap that TestGenesisRoundTripPreservesTrueOpenModuleAccountBalances
// (a codec-only round-trip) leaves open: any bug that lives inside
// ExportGenesis for the bank keeper, or any drift between the moduleAccPerms
// allowlist and the six trueopen account names, will surface here rather than
// in the module-account round-trip.
func TestAppExportPreservesTrueOpenModuleAccountBalances(t *testing.T) {
	db := dbm.NewMemDB()
	defer db.Close()

	app := New(log.NewNopLogger(), db, nil, true, smokeAppOptions(),
		baseapp.SetChainID(SimAppChainID))
	require.NotNil(t, app)

	stateBytes, expected, valHash := buildGenesisWithModuleBalances(t, app)
	initChainAndCommit(t, app, stateBytes, valHash)

	exported, err := app.ExportAppStateAndValidators(false, nil, nil)
	require.NoError(t, err, "ExportAppStateAndValidators must succeed on a live app")

	var exportedGenesis GenesisState
	require.NoError(t, json.Unmarshal(exported.AppState, &exportedGenesis),
		"exported AppState must unmarshal into the composite GenesisState map")

	rawBank, ok := exportedGenesis[banktypes.ModuleName]
	require.Truef(t, ok, "exported genesis must contain the %q module", banktypes.ModuleName)

	var exportedBank banktypes.GenesisState
	require.NoError(t, app.AppCodec().UnmarshalJSON(rawBank, &exportedBank))

	seen := make(map[string]sdkmath.Int, len(expected))
	for _, b := range exportedBank.Balances {
		if want, ok := expected[b.Address]; ok {
			got := b.Coins.AmountOf(moduleBalanceRoundTripDenom)
			require.Truef(t, got.Equal(want),
				"module account %q balance in exported state = %s, want %s", b.Address, got, want)
			seen[b.Address] = got
		}
	}
	for addr := range expected {
		_, ok := seen[addr]
		require.Truef(t, ok, "module account %q missing from exported bank genesis (export path silently dropped a trueopen account)", addr)
	}
	require.Equal(t, len(expected), len(seen),
		"exported bank genesis must contain exactly the six trueopen module accounts we seeded, no more no less")
}

// TestAppExportedGenesisReImportsTrueOpenModuleAccountBalances proves that the
// export produced by app A can be re-imported into a fresh app B, and that a
// live bank keeper query on app B reports every trueopen module account balance
// with the value that was seeded on app A.
//
// This exercises the *symmetric* half of the export/import contract that the module-account round-trip
// cannot: not "does the JSON survive marshal/unmarshal" but "does the
// exported JSON, when replayed through a fresh app's InitChain, land the
// balances in the actual bank keeper store where downstream hub and task handlers
// will read them from".
func TestAppExportedGenesisReImportsTrueOpenModuleAccountBalances(t *testing.T) {
	sourceDB := dbm.NewMemDB()
	defer sourceDB.Close()

	sourceApp := New(log.NewNopLogger(), sourceDB, nil, true, smokeAppOptions(),
		baseapp.SetChainID(SimAppChainID))

	stateBytes, expected, valHash := buildGenesisWithModuleBalances(t, sourceApp)
	initChainAndCommit(t, sourceApp, stateBytes, valHash)

	exported, err := sourceApp.ExportAppStateAndValidators(false, nil, nil)
	require.NoError(t, err)

	// Bring up a completely fresh app on a new DB and hand it the exported
	// AppState as its genesis input. This is the same shape a validator uses
	// when replaying `noded export` output on a new node.
	targetDB := dbm.NewMemDB()
	defer targetDB.Close()

	targetApp := New(log.NewNopLogger(), targetDB, nil, true, smokeAppOptions(),
		baseapp.SetChainID(SimAppChainID))

	// Reuse the source validator set hash — after re-import the validator
	// set is deterministic and identical to the source's InitChain output,
	// so we can feed the same NextValidatorsHash when finalizing.
	initChainAndCommit(t, targetApp, exported.AppState, valHash)

	// Read balances via the bank keeper — this is the surface the trueopen
	// keeper actually calls into at runtime, so proving it here rules out
	// InitGenesis silently filling only the genesis JSON and not the store.
	ctx := targetApp.NewContextLegacy(true, cmtproto.Header{Height: targetApp.LastBlockHeight() + 1})
	for addr, want := range expected {
		accAddr, err := sdk.AccAddressFromBech32(addr)
		require.NoError(t, err)
		got := targetApp.BankKeeper.GetBalance(ctx, accAddr, moduleBalanceRoundTripDenom)
		require.Truef(t, got.Amount.Equal(want),
			"target app bank keeper reports module account %q balance = %s, want %s", addr, got.Amount, want)
	}
}
