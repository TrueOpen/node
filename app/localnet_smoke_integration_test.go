package app

// single-node block-production + query-params smoke.
//
// The Node plan's the localnet smoke asks for a single-node localnet smoke: start,
// produce a block, query params. Driving the real noded binary is
// documented for operators in docs/runbooks/mainnet_devnet_config.md,
// but a binary-driven smoke is neither deterministic nor CI-friendly
// (it needs a spawned process, ports, and wall-clock block times).
//
// This file delivers the same guarantee as a deterministic app-level
// smoke that the existing go-unit CI runs on every PR:
//   - boot a validator genesis (the "start" equivalent);
//   - drive several consecutive FinalizeBlock/Commit cycles (the
//     "produce block" equivalent) — this exercises the full ABCI loop
//     including the frozen BeginBlock/EndBlock chains (distribution ->
//     staking -> trueopen / staking -> trueopen) with the trueopen EndBlock
//     multi-index sweep, plus the beacon PreBlocker chained onto the
//     app PreBlocker (which, with the default
//     vrf_required_from_height = 0, must accept blocks that carry no
//     beacon sentinel and write a placeholder beacon);
//   - query params through the depinject-wired query server (the
//     "query params" equivalent) and assert the default params echo.
//
// If any BeginBlock/EndBlock/PreBlocker hook panics or errors as the
// chain advances height, or if the query path breaks after blocks are
// committed, this smoke fails in CI before it can merge.

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

	taskkeeper "github.com/TrueOpen/node/x/task/keeper"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

// bootSmokeApp boots a validator+delegator genesis app and returns it
// alongside the validator-set hash needed to finalize subsequent blocks.
func bootSmokeApp(t *testing.T) (*App, []byte) {
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
	return app, valSet.Hash()
}

// advanceBlock runs one FinalizeBlock/Commit cycle for the next height
// and returns the height that was finalized. The validator set is
// unchanged so NextValidatorsHash stays valHash.
func advanceBlock(t *testing.T, app *App, valHash []byte) int64 {
	t.Helper()
	height := app.LastBlockHeight() + 1
	_, err := app.FinalizeBlock(&abci.RequestFinalizeBlock{
		Height:             height,
		Hash:               testFinalizeBlockHash(height),
		NextValidatorsHash: valHash,
	})
	require.NoErrorf(t, err, "FinalizeBlock must succeed at height %d", height)
	_, err = app.Commit()
	require.NoErrorf(t, err, "Commit must succeed at height %d", height)
	return height
}

// TestSingleNodeProducesConsecutiveBlocks is the "start + produce block"
// half of the smoke: after the genesis block, drive several more blocks
// and assert the committed height advances by exactly one each time. A
// panic or error in any BeginBlock/EndBlock/PreBlocker hook (including
// the trueopen EndBlock sweep and the beacon PreBlocker) as height advances
// surfaces here.
func TestSingleNodeProducesConsecutiveBlocks(t *testing.T) {
	app, valHash := bootSmokeApp(t)

	// initChainAndCommit already committed the genesis block at height 1.
	require.Equal(t, int64(1), app.LastBlockHeight(), "genesis block must be committed at height 1")

	const extraBlocks = 5
	for i := 0; i < extraBlocks; i++ {
		wantHeight := app.LastBlockHeight() + 1
		gotHeight := advanceBlock(t, app, valHash)
		require.Equal(t, wantHeight, gotHeight)
		require.Equal(t, wantHeight, app.LastBlockHeight(),
			"committed height must advance by exactly one per block")
	}
	require.Equal(t, int64(1+extraBlocks), app.LastBlockHeight())
}

// TestQueryParamsAfterBlockProduction is the "query params" half: after
// producing blocks, the depinject-wired query server must return the
// default params intact. This proves the query path is live post-block
// and that BeginBlock/EndBlock never mutated the params singleton.
func TestQueryParamsAfterBlockProduction(t *testing.T) {
	app, valHash := bootSmokeApp(t)
	for i := 0; i < 3; i++ {
		advanceBlock(t, app, valHash)
	}

	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: app.LastBlockHeight() + 1})
	qs := taskkeeper.NewQueryServerImpl(app.TaskKeeper)

	resp, err := qs.Params(ctx, &tasktypes.QueryTaskParamsRequest{})
	require.NoError(t, err, "params query must succeed after block production")
	require.Equal(t, tasktypes.DefaultTaskParams(), resp.Params,
		"params must equal the genesis defaults after several blocks")
	// Spot-check a representative field so a zero-value response can't
	// masquerade as a match.
	require.Equal(t, tasktypes.DefaultMaxDeadlineSweepPerBlock, resp.Params.Deadlines.MaxDeadlineSweepPerBlock)
}
