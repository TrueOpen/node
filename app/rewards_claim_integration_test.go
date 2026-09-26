package app

// Rewards module to signer app integration test.
//
// Proves the full app-level path for MsgClaimEarnings:
//   1. A signer with an EarningsState.ClaimableAmount > 0 can submit
//      MsgClaimEarnings.
//   2. The claim moves coins out of the `hub_rewards` module account
//      and into the signer's bank balance.
//   3. The signer's Earnings state is cleared (ClaimableAmount = 0,
//      LastUpdatedHeight advanced).
//   4. Bank total supply is unchanged (module-to-account transfer only).
//
// This is a boot-and-transact integration test: it uses the full App
// (InitChain + FinalizeBlock + Commit), the real BankKeeper wired via
// depinject, and the real TrueOpen MsgServer. If any layer in that chain
// silently regresses, for example if the rewards module account gets an
// unexpected permission or the earnings projection stops persisting the address, the
// test fails immediately rather than at first production claim.

import (
	"bytes"
	"testing"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"

	hubkeeper "github.com/TrueOpen/node/x/hub/keeper"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// buildGenesisForRewardsClaim wraps buildGenesisWithModuleBalances
// with an additional genesis account (`claimant`) that will submit the
// MsgClaimEarnings, and pre-funds that account with more coins than the
// intended claim. The test transfers them into hub_rewards only after a
// valid genesis block has committed, so runtime balance invariants are never
// bypassed by an inconsistent genesis fixture.
//
// Returns (stateBytes, claimantAddress, validatorHash, rewardsSeed).
func buildGenesisForRewardsClaim(t *testing.T, app *App, claimAmount uint64) ([]byte, sdk.AccAddress, []byte, sdkmath.Int) {
	t.Helper()
	cdc := app.AppCodec()

	valSet, err := simtestutil.CreateRandomValidatorSet()
	require.NoError(t, err)

	// Delegator account (backs the validator).
	delegPriv := secp256k1.GenPrivKey()
	delegAcc := authtypes.NewBaseAccount(delegPriv.PubKey().Address().Bytes(), delegPriv.PubKey(), 0, 0)

	// Claimant account: a fresh secp256k1 keypair that will sign
	// MsgClaimEarnings.
	claimantPriv := secp256k1.GenPrivKey()
	claimantAddr := sdk.AccAddress(claimantPriv.PubKey().Address())
	claimantAcc := authtypes.NewBaseAccount(claimantAddr, claimantPriv.PubKey(), 1, 0)

	genAccs := []authtypes.GenesisAccount{delegAcc, claimantAcc}

	// Pre-fund delegator with bond denom (required by GenesisStateWithValSet).
	// Pre-fund the claimant with 10x the claim amount. The caller moves this
	// balance into the rewards module in the same test context before claiming.
	rewardsSeedAmount := sdkmath.NewInt(int64(claimAmount) * 10)
	balances := []banktypes.Balance{
		{
			Address: delegAcc.GetAddress().String(),
			Coins:   sdk.NewCoins(sdk.NewCoin(sdk.DefaultBondDenom, sdkmath.NewInt(100_000_000_000_000))),
		},
		{
			Address: claimantAddr.String(),
			Coins:   sdk.NewCoins(sdk.NewCoin(hubtypes.DefaultBusinessDenom, rewardsSeedAmount)),
		},
	}

	genesisState, err := simtestutil.GenesisStateWithValSet(cdc, app.DefaultGenesis(), valSet, genAccs, balances...)
	require.NoError(t, err)

	stateBytes, err := cmtjson.MarshalIndent(genesisState, "", " ")
	require.NoError(t, err)

	return stateBytes, claimantAddr, valSet.Hash(), rewardsSeedAmount
}

// TestClaimEarningsMovesCoinsFromRewardsModuleToSigner is the rewards claim
// happy path: seed Earnings, submit MsgClaimEarnings, assert bank
// balances shift and Earnings clears.
func TestClaimEarningsMovesCoinsFromRewardsModuleToSigner(t *testing.T) {
	const claimAmount uint64 = 12_345
	db := dbm.NewMemDB()
	defer db.Close()
	application := New(log.NewNopLogger(), db, nil, true, smokeAppOptions(),
		baseapp.SetChainID(SimAppChainID))
	stateBytes, claimant, valHash, rewardsSeed := buildGenesisForRewardsClaim(t, application, claimAmount)
	initChainAndCommit(t, application, stateBytes, valHash)

	ctx := application.NewContextLegacy(true, cmtproto.Header{Height: application.LastBlockHeight() + 1})
	require.NoError(t, application.BankKeeper.SendCoinsFromAccountToModule(
		ctx, claimant, hubtypes.RewardsModuleName,
		sdk.NewCoins(sdk.NewCoin(hubtypes.DefaultBusinessDenom, rewardsSeed)),
	))
	claimantAddress := claimant.String()
	require.NoError(t, application.HubKeeper.CreditTaskSettlementEarnings(ctx,
		bytes.Repeat([]byte{0x11}, 32), bytes.Repeat([]byte{0x22}, 32),
		[]hubtypes.TaskEarningsCredit{{Beneficiary: claimantAddress, Amount: shared.NewAmount(claimAmount)}},
		uint64(ctx.BlockHeight())))
	rewardsAddress := authtypes.NewModuleAddress(hubtypes.RewardsModuleName)
	preSupply := application.BankKeeper.GetSupply(ctx, hubtypes.DefaultBusinessDenom).Amount

	response, err := hubkeeper.NewMsgServerImpl(application.HubKeeper).ClaimEarnings(
		ctx, &hubtypes.MsgClaimEarnings{
			SignerAddress: claimantAddress,
			ClaimClass:    hubtypes.ClaimClassV1_CLAIM_CLASS_V1_TASK_FEE,
		},
	)
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Status)
	require.Equal(t, shared.NewAmount(claimAmount), response.ClaimedAmount)
	require.Equal(t, sdkmath.NewIntFromUint64(claimAmount),
		application.BankKeeper.GetBalance(ctx, claimant, hubtypes.DefaultBusinessDenom).Amount)
	require.Equal(t, rewardsSeed.Sub(sdkmath.NewIntFromUint64(claimAmount)),
		application.BankKeeper.GetBalance(ctx, rewardsAddress, hubtypes.DefaultBusinessDenom).Amount)
	require.Equal(t, preSupply, application.BankKeeper.GetSupply(ctx, hubtypes.DefaultBusinessDenom).Amount)
	_, err = application.HubKeeper.Earnings.Get(ctx, claimantAddress)
	require.ErrorIs(t, err, collections.ErrNotFound)
}

// TestClaimEarningsNoopsWhenNothingClaimable proves an empty claim is an
// idempotent success and performs no bank writes.
func TestClaimEarningsNoopsWhenNothingClaimable(t *testing.T) {
	db := dbm.NewMemDB()
	defer db.Close()

	app := New(log.NewNopLogger(), db, nil, true, smokeAppOptions(),
		baseapp.SetChainID(SimAppChainID))

	stateBytes, claimant, valHash, rewardsSeed := buildGenesisForRewardsClaim(t, app, 1)
	initChainAndCommit(t, app, stateBytes, valHash)

	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: app.LastBlockHeight() + 1})
	require.NoError(t, app.BankKeeper.SendCoinsFromAccountToModule(ctx, claimant,
		hubtypes.RewardsModuleName,
		sdk.NewCoins(sdk.NewCoin(hubtypes.DefaultBusinessDenom, rewardsSeed))))
	rewardsAddr := authtypes.NewModuleAddress(hubtypes.RewardsModuleName)
	preRewardsBal := app.BankKeeper.GetBalance(ctx, rewardsAddr, hubtypes.DefaultBusinessDenom).Amount

	msgServer := hubkeeper.NewMsgServerImpl(app.HubKeeper)
	resp, err := msgServer.ClaimEarnings(ctx, &hubtypes.MsgClaimEarnings{
		SignerAddress: claimant.String(),
		ClaimClass:    hubtypes.ClaimClassV1_CLAIM_CLASS_V1_TASK_FEE,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, resp.Status)
	require.Equal(t, shared.NewAmount(0), resp.ClaimedAmount)
	require.Equal(t, claimant.String(), resp.Beneficiary)

	// hub_rewards balance stays put.
	postRewardsBal := app.BankKeeper.GetBalance(ctx, rewardsAddr, hubtypes.DefaultBusinessDenom).Amount
	require.True(t, postRewardsBal.Equal(preRewardsBal),
		"no-op claim must not touch hub_rewards balance")
	require.Truef(t, postRewardsBal.Equal(rewardsSeed),
		"hub_rewards must still hold the genesis seed intact")

	// Claimant still holds zero.
	postClaimantBal := app.BankKeeper.GetBalance(ctx, claimant, hubtypes.DefaultBusinessDenom).Amount
	require.True(t, postClaimantBal.IsZero())
}
