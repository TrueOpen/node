package app

// x/gov tally boundary tests.
//
// governance_protocol.md §2 requires the chain to use Cosmos SDK x/gov's default tally
// unchanged, and states the obligation these tests discharge:
//
//	"the tests must cover the abstain, jailed and unbond boundaries, and must
//	 not write quorum, veto and the pass threshold against one shared
//	 denominator."
//
// The three denominators in x/gov/keeper/tally.go v0.53.6 are genuinely
// different, and conflating any two changes governance outcomes:
//
//	quorum    = totalVotingPower / TotalBondedTokens       (:176)
//	veto      = NoWithVeto / totalVotingPower              (:189)  abstain INCLUDED
//	threshold = Yes / (totalVotingPower - Abstain)         (:204)  abstain EXCLUDED
//
// This app supplies no CalculateVoteResultsAndVotingPowerFn, so these run
// against the SDK implementation itself. They are therefore an upgrade tripwire:
// if an SDK bump changes any denominator, they fail here rather than silently in
// production. Domain classification is a separate concern; see gov_domain_test.go.

import (
	"testing"

	"cosmossdk.io/collections"
	"cosmossdk.io/log"
	"cosmossdk.io/math"
	cmtjson "github.com/cometbft/cometbft/libs/json"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	v1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/stretchr/testify/require"
)

const (
	standardMsgURL = "/hub.v1.MsgUpdateHubParams"
	builderMsgURL  = "/hub.v1.MsgSetModelStatus"
	profileMsgURL  = "/hub.v1.MsgSetProfileStatus"
)

// --- test harness ----------------------------------------------------------

// bootAppForGovTally boots the app and returns the account holding the genesis
// delegation, i.e. the only address with meaningful stake-weighted voting power.
func bootAppForGovTally(t *testing.T) (*App, sdk.AccAddress) {
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
		Coins:   sdk.NewCoins(sdk.NewCoin(sdk.DefaultBondDenom, math.NewInt(100_000_000_000_000))),
	}}
	genesisState, err := simtestutil.GenesisStateWithValSet(app.AppCodec(), app.DefaultGenesis(), valSet,
		[]authtypes.GenesisAccount{delegAcc}, balances...)
	require.NoError(t, err)
	stateBytes, err := cmtjson.MarshalIndent(genesisState, "", " ")
	require.NoError(t, err)

	initChainAndCommit(t, app, stateBytes, valSet.Hash())
	return app, delegAcc.GetAddress()
}

func proposalWith(id uint64, typeURLs ...string) v1.Proposal {
	msgs := make([]*codectypes.Any, 0, len(typeURLs))
	for _, url := range typeURLs {
		msgs = append(msgs, &codectypes.Any{TypeUrl: url})
	}
	return v1.Proposal{Id: id, Messages: msgs}
}

func castVote(t *testing.T, app *App, ctx sdk.Context, proposalID uint64, voter sdk.AccAddress, option v1.VoteOption) {
	t.Helper()
	castWeightedVote(t, app, ctx, proposalID, voter, v1.NewNonSplitVoteOption(option))
}

// castWeightedVote stores a split vote directly. The weights are what make the
// three denominators separable with a single voter.
func castWeightedVote(t *testing.T, app *App, ctx sdk.Context, proposalID uint64, voter sdk.AccAddress, options v1.WeightedVoteOptions) {
	t.Helper()
	require.NoError(t, app.GovKeeper.Votes.Set(ctx, collections.Join(proposalID, voter), v1.Vote{
		ProposalId: proposalID,
		Voter:      voter.String(),
		Options:    options,
	}))
}

func weighted(t *testing.T, pairs ...any) v1.WeightedVoteOptions {
	t.Helper()
	require.Zero(t, len(pairs)%2, "weighted takes option/weight pairs")
	out := make(v1.WeightedVoteOptions, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		option, ok := pairs[i].(v1.VoteOption)
		require.True(t, ok)
		weight, ok := pairs[i+1].(string)
		require.True(t, ok)
		out = append(out, &v1.WeightedVoteOption{Option: option, Weight: weight})
	}
	return out
}

// theOnlyValidator returns the single genesis validator.
func theOnlyValidator(t *testing.T, app *App, ctx sdk.Context) stakingtypes.Validator {
	t.Helper()
	validators, err := app.StakingKeeper.GetAllValidators(ctx)
	require.NoError(t, err)
	require.Len(t, validators, 1, "the harness assumes exactly one genesis validator")
	return validators[0]
}

// --- abstain ---------------------------------------------------------------

// TestTallyAbstainIsExcludedFromThresholdDenominator is load-bearing on the
// threshold denominator alone: yes and abstain are equal, so
//
//	Yes / (total - Abstain) = 0.5P / 0.5P = 1.0   > 0.5  -> PASSES
//
// while the same numbers under a shared denominator would be
//
//	Yes / total             = 0.5P / 1.0P = 0.5   not > 0.5 -> FAILS
//
// The proposal passing is therefore only possible if abstain is excluded.
func TestTallyAbstainIsExcludedFromThresholdDenominator(t *testing.T) {
	app, voter := bootAppForGovTally(t)
	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: 2})

	params, err := app.GovKeeper.Params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, "0.500000000000000000", params.Threshold,
		"the arithmetic below is written against the default 1/2 threshold")

	proposal := proposalWith(400, standardMsgURL)
	castWeightedVote(t, app, ctx, proposal.Id, voter,
		weighted(t, v1.OptionYes, "0.5", v1.OptionAbstain, "0.5"))

	passes, _, tally, err := app.GovKeeper.Tally(ctx, proposal)
	require.NoError(t, err)
	require.Equal(t, tally.YesCount, tally.AbstainCount, "the split must be exactly half and half")
	require.True(t, passes,
		"yes == abstain passes only when abstain is excluded from the threshold denominator")
}

// TestTallyAbstainIsIncludedInVetoDenominator is the mirror image, load-bearing
// on the veto denominator:
//
//	NoWithVeto / total          = 0.3P / 1.0P = 0.300  not > 0.334 -> NOT vetoed
//	NoWithVeto / (total-Abstain)= 0.3P / 0.3P = 1.000      > 0.334 -> vetoed
//
// so a non-vetoed outcome proves abstain is inside the veto denominator.
// burnDeposits is asserted rather than `passes` because the proposal fails
// either way (zero yes); only the veto flag distinguishes the two denominators.
func TestTallyAbstainIsIncludedInVetoDenominator(t *testing.T) {
	app, voter := bootAppForGovTally(t)
	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: 2})

	params, err := app.GovKeeper.Params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, "0.334000000000000000", params.VetoThreshold,
		"the arithmetic below is written against the default 1/3 veto threshold")
	require.True(t, params.BurnVoteVeto,
		"burnDeposits only reports the veto path while burn_vote_veto is on")

	proposal := proposalWith(401, standardMsgURL)
	castWeightedVote(t, app, ctx, proposal.Id, voter,
		weighted(t, v1.OptionNoWithVeto, "0.3", v1.OptionAbstain, "0.7"))

	passes, burnDeposits, _, err := app.GovKeeper.Tally(ctx, proposal)
	require.NoError(t, err)
	require.False(t, passes)
	require.False(t, burnDeposits,
		"30%% veto against the full voting power is below the threshold; a veto here would mean abstain was dropped from the denominator")
}

// --- jailed ----------------------------------------------------------------

// TestTallyJailedValidatorLeavesNumeratorButStaysInQuorumDenominator pins the
// asymmetry ADR-0018 Decision 3 calls out as a Phase 0 liveness risk: getCurrentValidators
// iterates the power index (tally.go:122), which jailing removes, so the jailed
// validator's delegations contribute nothing; but its tokens are still in the
// bonded pool, so they keep inflating the quorum denominator until it unbonds.
func TestTallyJailedValidatorLeavesNumeratorButStaysInQuorumDenominator(t *testing.T) {
	app, voter := bootAppForGovTally(t)
	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: 2})

	bondedBefore, err := app.StakingKeeper.TotalBondedTokens(ctx)
	require.NoError(t, err)
	require.True(t, bondedBefore.IsPositive())

	// Control: the same vote passes while the validator is not jailed.
	control := proposalWith(410, standardMsgURL)
	castVote(t, app, ctx, control.Id, voter, v1.OptionYes)
	passes, _, controlTally, err := app.GovKeeper.Tally(ctx, control)
	require.NoError(t, err)
	require.True(t, passes)
	require.NotEqual(t, "0", controlTally.YesCount)

	validator := theOnlyValidator(t, app, ctx)
	consAddr, err := validator.GetConsAddr()
	require.NoError(t, err)
	require.NoError(t, app.StakingKeeper.Jail(ctx, consAddr))

	proposal := proposalWith(411, standardMsgURL)
	castVote(t, app, ctx, proposal.Id, voter, v1.OptionYes)
	passes, _, tally, err := app.GovKeeper.Tally(ctx, proposal)
	require.NoError(t, err)
	require.Equal(t, "0", tally.YesCount,
		"a jailed validator is off the power index, so delegations to it carry no voting power")
	require.False(t, passes, "zero voting power cannot reach quorum")

	bondedAfter, err := app.StakingKeeper.TotalBondedTokens(ctx)
	require.NoError(t, err)
	require.True(t, bondedAfter.Equal(bondedBefore),
		"jailing must NOT shrink the quorum denominator; it only empties the numerator")
}

// --- unbond ----------------------------------------------------------------

// TestTallyUnbondingLeavesTheQuorumDenominator is the other half of the same
// asymmetry: unbonding is what actually removes tokens from TotalBondedTokens,
// and therefore the only thing that restores governance liveness after a
// validator stops voting.
func TestTallyUnbondingLeavesTheQuorumDenominator(t *testing.T) {
	app, voter := bootAppForGovTally(t)
	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: 2})

	bondedBefore, err := app.StakingKeeper.TotalBondedTokens(ctx)
	require.NoError(t, err)
	require.True(t, bondedBefore.IsPositive())

	validator := theOnlyValidator(t, app, ctx)
	valAddr, err := app.StakingKeeper.ValidatorAddressCodec().StringToBytes(validator.GetOperator())
	require.NoError(t, err)
	delegation, err := app.StakingKeeper.GetDelegation(ctx, voter, sdk.ValAddress(valAddr))
	require.NoError(t, err)
	require.True(t, delegation.Shares.IsPositive())

	_, removed, err := app.StakingKeeper.Undelegate(ctx, voter, sdk.ValAddress(valAddr), delegation.Shares)
	require.NoError(t, err)
	require.True(t, removed.IsPositive())

	bondedAfter, err := app.StakingKeeper.TotalBondedTokens(ctx)
	require.NoError(t, err)
	require.True(t, bondedAfter.LT(bondedBefore),
		"unbonded tokens must leave the quorum denominator")
	require.True(t, bondedBefore.Sub(bondedAfter).Equal(removed),
		"exactly the undelegated amount leaves the denominator")
}

// --- the three denominators are not the same expression ---------------------

// TestTallyDenominatorsAreDistinct states the rule positively on one tally:
// with yes 0.4 / abstain 0.4 / veto 0.2 the three ratios are 1.0, 0.667 and 0.2,
// three different numbers. Any refactor that collapses two of them changes at
// least one of these.
func TestTallyDenominatorsAreDistinct(t *testing.T) {
	app, voter := bootAppForGovTally(t)
	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: 2})

	proposal := proposalWith(420, standardMsgURL)
	castWeightedVote(t, app, ctx, proposal.Id, voter,
		weighted(t, v1.OptionYes, "0.4", v1.OptionAbstain, "0.4", v1.OptionNoWithVeto, "0.2"))

	bonded, err := app.StakingKeeper.TotalBondedTokens(ctx)
	require.NoError(t, err)
	_, _, tally, err := app.GovKeeper.Tally(ctx, proposal)
	require.NoError(t, err)

	yes, ok := math.NewIntFromString(tally.YesCount)
	require.True(t, ok)
	abstain, ok := math.NewIntFromString(tally.AbstainCount)
	require.True(t, ok)
	veto, ok := math.NewIntFromString(tally.NoWithVetoCount)
	require.True(t, ok)
	no, ok := math.NewIntFromString(tally.NoCount)
	require.True(t, ok)

	participated := yes.Add(abstain).Add(veto).Add(no)
	require.True(t, participated.IsPositive())

	quorumRatio := math.LegacyNewDecFromInt(participated).Quo(math.LegacyNewDecFromInt(bonded))
	vetoRatio := math.LegacyNewDecFromInt(veto).Quo(math.LegacyNewDecFromInt(participated))
	passRatio := math.LegacyNewDecFromInt(yes).Quo(math.LegacyNewDecFromInt(participated.Sub(abstain)))

	require.False(t, quorumRatio.Equal(vetoRatio))
	require.False(t, quorumRatio.Equal(passRatio))
	require.False(t, vetoRatio.Equal(passRatio))
	// 0.4 / (0.4 + 0.2) = 2/3; LegacyDec.Quo rounds the last place up.
	require.Equal(t, "0.666666666666666667", passRatio.String())
	require.Equal(t, "0.200000000000000000", vetoRatio.String())
}
