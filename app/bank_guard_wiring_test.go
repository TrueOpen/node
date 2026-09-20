package app

// Bank-keeper guard wiring.
//
// Four upstream modules must never see the raw bank keeper:
//
//	x/gov      -> GovernedGovBankKeeper       (fee_burn_policy = NO_USDC_BURN_V1)
//	x/staking  -> GovernedStakingBankKeeper
//	hl x/core  -> bridge.GuardedBankKeeper    (the bridge protocol)
//	hl x/warp  -> bridge.GuardedBankKeeper
//
// All four were silently unwired: depinject.BindInterface compares against its
// own fullyQualifiedTypeName, which repeats the short package name, and a
// binding that never matches is not an error — depinject falls back to the
// implicit single-implementation rule and picks bank.BaseKeeper. Compile-time
// assertions like `var _ govtypes.BankKeeper = GovernedGovBankKeeper{}` cannot
// catch that: they prove the type satisfies the interface, not that anything
// hands it over. These tests check the wiring itself.

import (
	"reflect"
	"testing"

	"cosmossdk.io/collections"
	"cosmossdk.io/depinject"
	sdkmath "cosmossdk.io/math"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	minttypes "github.com/cosmos/cosmos-sdk/x/mint/types"
	"github.com/stretchr/testify/require"

	appbridge "github.com/TrueOpen/node/app/bridge"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

// dynamicFieldType reports the concrete type behind an interface-typed field,
// including unexported ones in other packages. Only type information is read, so
// no unsafe access is needed: reflect's read-only flag blocks Interface() and
// Set(), not Type().
func dynamicFieldType(t *testing.T, owner reflect.Value, field string) reflect.Type {
	t.Helper()
	for owner.Kind() == reflect.Pointer || owner.Kind() == reflect.Interface {
		require.False(t, owner.IsNil(), "%s: owner is nil", field)
		owner = owner.Elem()
	}
	require.Equal(t, reflect.Struct, owner.Kind(), "%s: owner %v is not a struct", field, owner.Type())
	value := owner.FieldByName(field)
	require.True(t, value.IsValid(), "%v has no field %s — upstream renamed it, update this test", owner.Type(), field)
	require.Equal(t, reflect.Interface, value.Kind(), "%s is not an interface field", field)
	require.False(t, value.IsNil(), "%s is nil", field)
	return value.Elem().Type()
}

// TestUpstreamModulesReceiveTheirBankGuards is the regression test for the dead
// bindings. Before the fix every line below reported bank.BaseKeeper.
func TestUpstreamModulesReceiveTheirBankGuards(t *testing.T) {
	app := bootAppMinimal(t)
	upstream := reflect.ValueOf(app.bridgeUpstream)

	for _, tc := range []struct {
		name  string
		owner reflect.Value
		want  reflect.Type
	}{
		{"x/gov", reflect.ValueOf(app.GovKeeper), reflect.TypeOf(GovernedGovBankKeeper{})},
		{"x/staking", reflect.ValueOf(app.StakingKeeper), reflect.TypeOf(GovernedStakingBankKeeper{})},
		{"hyperlane x/core", upstream.FieldByName("core"), reflect.TypeOf(appbridge.GuardedBankKeeper{})},
		{"hyperlane x/warp", upstream.FieldByName("warp"), reflect.TypeOf(appbridge.GuardedBankKeeper{})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.True(t, tc.owner.IsValid(), "could not reach %s's keeper", tc.name)
			require.Equal(t, tc.want, dynamicFieldType(t, tc.owner, "bankKeeper"),
				"%s holds the raw bank keeper; its depinject binding is not in effect", tc.name)
		})
	}
}

// TestGovDepositForfeitureReachesTreasury is the behavioural half: it drives the
// exact call x/gov's EndBlocker makes on the veto path. genesis_seed.go sets
// burn_vote_veto = true, so this runs on the first vetoed proposal of any chain,
// and with the raw bank keeper it panics inside EndBlocker — a chain halt, not a
// failed transaction.
func TestGovDepositForfeitureReachesTreasury(t *testing.T) {
	app := bootAppMinimal(t)
	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: 2})

	const forfeited = 1_000_000
	const proposalID uint64 = 900
	denom := app.HubKeeper.GetHubParams(ctx).BusinessDenom
	require.NotEmpty(t, denom)
	coins := sdk.NewCoins(sdk.NewCoin(denom, sdkmath.NewInt(forfeited)))

	// x/mint is the only module account with Minter, and the unit-test genesis
	// funds the bond denom only, so business_denom has to be created here.
	require.NoError(t, app.BankKeeper.MintCoins(ctx, minttypes.ModuleName, coins))
	require.NoError(t, app.BankKeeper.SendCoinsFromModuleToModule(ctx, minttypes.ModuleName, govtypes.ModuleName, coins))

	depositor := sdk.AccAddress(make([]byte, 20))
	require.NoError(t, app.GovKeeper.Deposits.Set(ctx, collections.Join(proposalID, depositor),
		govv1.Deposit{ProposalId: proposalID, Depositor: depositor.String(), Amount: coins}))

	// math.Int carries a *big.Int, and a zero built by subtraction is not
	// reflect.DeepEqual to one built by parsing; compare the decimal strings.
	moduleBalance := func(module string) sdkmath.Int {
		addr := app.AuthKeeper.GetModuleAddress(module)
		require.NotNil(t, addr)
		return app.BankKeeper.GetBalance(ctx, addr, denom).Amount
	}
	govBefore := moduleBalance(govtypes.ModuleName)
	treasuryBefore := moduleBalance(hubtypes.TreasuryModuleName)
	supplyBefore := app.BankKeeper.GetSupply(ctx, denom).Amount
	stateBefore, err := app.HubKeeper.Treasury.Get(ctx)
	require.NoError(t, err)

	var burnErr error
	require.NotPanics(t, func() {
		burnErr = app.GovKeeper.DeleteAndBurnDeposits(ctx, proposalID)
	}, "x/gov calls this from EndBlocker; a panic here halts the chain")
	require.NoError(t, burnErr)

	stateAfter, err := app.HubKeeper.Treasury.Get(ctx)
	require.NoError(t, err)

	require.Equal(t, govBefore.SubRaw(forfeited).String(), moduleBalance(govtypes.ModuleName).String(),
		"the forfeited deposit must leave the gov module account")
	require.Equal(t, treasuryBefore.AddRaw(forfeited).String(), moduleBalance(hubtypes.TreasuryModuleName).String(),
		"it must land in trueopen_treasury")
	require.Equal(t, supplyBefore.String(), app.BankKeeper.GetSupply(ctx, denom).Amount.String(),
		"fee_burn_policy = NO_USDC_BURN_V1: business_denom supply must not shrink")
	require.NotEqual(t, stateBefore.TreasuryVersion, stateAfter.TreasuryVersion,
		"the treasury ledger must record the inflow, not just the bank transfer")
}

// TestGovModuleAccountHasNoBurnerPermission pins the other half of the
// invariant. The missing permission is what turns an unwired guard into a loud
// failure instead of silently destroying USDC; granting Burner would be the
// wrong way to make the panic above go away.
func TestGovModuleAccountHasNoBurnerPermission(t *testing.T) {
	app := bootAppMinimal(t)
	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: 2})

	account, ok := app.AuthKeeper.GetModuleAccount(ctx, govtypes.ModuleName).(*authtypes.ModuleAccount)
	require.True(t, ok)
	require.False(t, account.HasPermission(authtypes.Burner),
		"granting Burner to gov would let a missing GovernedGovBankKeeper burn USDC silently; "+
			"the deposit must be routed to trueopen_treasury instead")
}

// TestDepinjectTypeNameMatchesUpstream pins depinjectTypeName against the real
// container. depinject's fullyQualifiedTypeName is unexported, so the only
// honest check is whether a binding built from our copy actually takes effect.
// The negative case shows what the app used to do: the shape everyone writes by
// hand resolves to nothing, and depinject reports no error for it.
func TestDepinjectTypeNameMatchesUpstream(t *testing.T) {
	require.Equal(t,
		"github.com/TrueOpen/node/app/app.BindingProbeGuard",
		depinjectTypeName(reflect.TypeOf(BindingProbeGuard{})),
		"the short package name repeats; this is the shape depinject compares against")

	// Two implementations, so the implicit single-implementation rule cannot
	// resolve the interface on its own. Whether the binding is alive is then the
	// difference between an injected value and an ambiguity error, instead of
	// being invisible the way it was in the app.
	resolve := func(t *testing.T, binding depinject.Config) (reflect.Type, error) {
		t.Helper()
		var got BindingProbeIface
		err := depinject.Inject(depinject.Configs(
			depinject.Provide(
				func() BindingProbeGuard { return BindingProbeGuard{} },
				func() BindingProbeRaw { return BindingProbeRaw{} },
			),
			binding,
		), &got)
		if err != nil {
			return nil, err
		}
		return reflect.TypeOf(got), nil
	}

	t.Run("derived names bind", func(t *testing.T) {
		got, err := resolve(t, bindGuardedInterface[BindingProbeIface, BindingProbeGuard]())
		require.NoError(t, err)
		require.Equal(t, reflect.TypeOf(BindingProbeGuard{}), got)
	})

	t.Run("hand-written names do not", func(t *testing.T) {
		// Exactly the spelling every binding in this app carried before the fix:
		// one "/<shortpkg>" short. depinject registers it without complaint and
		// then never looks it up.
		_, err := resolve(t, depinject.BindInterface(
			"github.com/TrueOpen/node/app.BindingProbeIface",
			"github.com/TrueOpen/node/app.BindingProbeGuard",
		))
		require.Error(t, err)
		require.Contains(t, err.Error(), "Multiple implementations found",
			"the binding was ignored entirely — depinject fell through to the implicit rule")
	})
}

type BindingProbeIface interface{ probe() }

type BindingProbeGuard struct{}

func (BindingProbeGuard) probe() {}

type BindingProbeRaw struct{}

func (BindingProbeRaw) probe() {}
