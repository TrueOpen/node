package app

import (
	runtimev1alpha1 "cosmossdk.io/api/cosmos/app/runtime/v1alpha1"
	appv1alpha1 "cosmossdk.io/api/cosmos/app/v1alpha1"
	authmodulev1 "cosmossdk.io/api/cosmos/auth/module/v1"
	bankmodulev1 "cosmossdk.io/api/cosmos/bank/module/v1"
	consensusmodulev1 "cosmossdk.io/api/cosmos/consensus/module/v1"
	distrmodulev1 "cosmossdk.io/api/cosmos/distribution/module/v1"
	genutilmodulev1 "cosmossdk.io/api/cosmos/genutil/module/v1"
	govmodulev1 "cosmossdk.io/api/cosmos/gov/module/v1"
	slashingmodulev1 "cosmossdk.io/api/cosmos/slashing/module/v1"
	stakingmodulev1 "cosmossdk.io/api/cosmos/staking/module/v1"
	txconfigv1 "cosmossdk.io/api/cosmos/tx/config/v1"
	"cosmossdk.io/depinject/appconfig"
	_ "github.com/TrueOpen/node/x/hub/module"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
	_ "github.com/TrueOpen/node/x/task/module"
	tasktypes "github.com/TrueOpen/node/x/task/types"
	_ "github.com/cosmos/cosmos-sdk/x/bank" // import for side-effects
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	_ "github.com/cosmos/cosmos-sdk/x/consensus" // import for side-effects
	consensustypes "github.com/cosmos/cosmos-sdk/x/consensus/types"
	_ "github.com/cosmos/cosmos-sdk/x/distribution" // import for side-effects
	distrtypes "github.com/cosmos/cosmos-sdk/x/distribution/types"
	_ "github.com/cosmos/cosmos-sdk/x/gov" // import for side-effects
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	minttypes "github.com/cosmos/cosmos-sdk/x/mint/types"
	_ "github.com/cosmos/cosmos-sdk/x/slashing" // import for side-effects
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	_ "github.com/cosmos/cosmos-sdk/x/staking" // import for side-effects
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	"github.com/cosmos/cosmos-sdk/runtime"
	_ "github.com/cosmos/cosmos-sdk/x/auth"           // import for side-effects
	_ "github.com/cosmos/cosmos-sdk/x/auth/tx/config" // import for side-effects
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"

	// Blank imports register the upstream depinject providers; the modules are
	// mounted by config below, not constructed here.
	_ "github.com/bcp-innovations/hyperlane-cosmos/x/core"
	_ "github.com/bcp-innovations/hyperlane-cosmos/x/warp"

	hlcoremodulev1 "github.com/bcp-innovations/hyperlane-cosmos/api/core/module/v1"
	hlwarpmodulev1 "github.com/bcp-innovations/hyperlane-cosmos/api/warp/module/v1"
	hlcoretypes "github.com/bcp-innovations/hyperlane-cosmos/x/core/types"
	hlwarptypes "github.com/bcp-innovations/hyperlane-cosmos/x/warp/types"
)

// BeginBlockOrder is the ordered list of BeginBlocker module names.
// Frozen per TrueOpen_Node_Spec.md §14.4:
//
//	distribution -> staking -> trueopen
//
// Changes must be paired with a Node spec + Keeper spec update per
// TrueOpen_Node_Spec.md §14.4 (ABCI Hook Contract).
var BeginBlockOrder = []string{
	distrtypes.ModuleName,
	slashingtypes.ModuleName,
	stakingtypes.ModuleName,
	hubtypes.ModuleName,
	tasktypes.ModuleName,
}

// EndBlockOrder is the ordered list of EndBlocker module names.
// Frozen per TrueOpen_Node_Spec.md §14.4:
//
//	staking -> trueopen
var EndBlockOrder = []string{
	govtypes.ModuleName,
	stakingtypes.ModuleName,
	hubtypes.ModuleName,
	tasktypes.ModuleName,
}

var (
	moduleAccPerms = []*authmodulev1.ModuleAccountPermission{
		{Account: authtypes.FeeCollectorName},
		{Account: distrtypes.ModuleName},
		{Account: govtypes.ModuleName},
		{Account: minttypes.ModuleName, Permissions: []string{authtypes.Minter}},
		{Account: stakingtypes.BondedPoolName, Permissions: []string{authtypes.Burner, stakingtypes.ModuleName}},
		{Account: stakingtypes.NotBondedPoolName, Permissions: []string{authtypes.Burner, stakingtypes.ModuleName}},
		// hub-owned accounts
		{Account: hubtypes.ModuleName},
		{Account: hubtypes.RewardsModuleName},
		{Account: hubtypes.EmissionModuleName},
		{Account: hubtypes.TreasuryModuleName},
		{Account: hubtypes.ServiceBondModuleName},
		// The warp module account is the single legal mint/burn authority for
		// business_denom (cross_chain_asset_bridge_protocol.md §5.1). Cosmos
		// permissions are not denom-scoped, so app/bridge.GuardedBankKeeper is
		// what actually closes the set; this grant only makes the one legal
		// path possible.
		{Account: hlwarptypes.ModuleName, Permissions: []string{authtypes.Minter, authtypes.Burner}},
		// task-owned accounts
		{Account: tasktypes.ModuleName},
		{Account: tasktypes.EscrowModuleName},
		{Account: tasktypes.ChallengeEffectModuleName},
	}

	// blocked account addresses
	blockAccAddrs = []string{
		authtypes.FeeCollectorName,
		distrtypes.ModuleName,
		stakingtypes.BondedPoolName,
		stakingtypes.NotBondedPoolName,
		// TrueOpen business accounts must only move through keeper-controlled module
		// transfers; ordinary MsgSend into these accounts would break balance
		// invariants such as escrow == sum(reserved).
		hubtypes.ModuleName,
		hubtypes.RewardsModuleName,
		hubtypes.EmissionModuleName,
		hubtypes.TreasuryModuleName,
		hubtypes.ServiceBondModuleName,
		tasktypes.ModuleName,
		tasktypes.EscrowModuleName,
		tasktypes.ChallengeEffectModuleName,
		// §5.1 requires the warp account to be blocked: an ordinary MsgSend into
		// it would add business_denom the conservation identity cannot account
		// for.
		hlwarptypes.ModuleName,
		// We allow the following module accounts to receive funds:
		// govtypes.ModuleName
	}

	// application configuration (used by depinject)
	appConfig = appconfig.Compose(&appv1alpha1.Config{
		Modules: []*appv1alpha1.ModuleConfig{
			{
				Name: runtime.ModuleName,
				Config: appconfig.WrapAny(&runtimev1alpha1.Module{
					AppName: Name,
					// NOTE: upgrade module is required to be prioritized
					PreBlockers: []string{
						authtypes.ModuleName,
					},
					// During begin block slashing happens after distr.BeginBlocker so that
					// there is nothing left over in the validator fee pool, so as to keep the
					// CanWithdrawInvariant invariant.
					// NOTE: staking module is required if HistoricalEntries param > 0
					// Hook order is frozen in BeginBlockOrder / EndBlockOrder above;
					// changing the order requires a coordinated Node + Keeper spec update
					// (see TrueOpen_Node_Spec.md §14.4).
					BeginBlockers: BeginBlockOrder,
					EndBlockers:   EndBlockOrder,
					// The following is mostly only needed when ModuleName != StoreKey name.
					OverrideStoreKeys: []*runtimev1alpha1.StoreKeyConfig{
						{
							ModuleName: authtypes.ModuleName,
							KvStoreKey: "acc",
						},
					},
					// NOTE: The genutils module must occur after staking so that pools are
					// properly initialized with tokens from genesis accounts.
					// NOTE: The genutils module must also occur after auth so that it can access the params from auth.
					InitGenesis: []string{
						consensustypes.ModuleName,
						authtypes.ModuleName,
						banktypes.ModuleName,
						distrtypes.ModuleName,
						stakingtypes.ModuleName,
						slashingtypes.ModuleName,
						govtypes.ModuleName,
						genutiltypes.ModuleName,
						// The canonical USDC path: core owns the Mailbox and ISM,
						// warp owns the Synthetic token that references it, and the
						// TrueOpen bridge rows are validated against both.
						hlcoretypes.ModuleName,
						hlwarptypes.ModuleName,
						// chain modules
						hubtypes.ModuleName,
						tasktypes.ModuleName},
				}),
			},
			{
				Name: authtypes.ModuleName,
				Config: appconfig.WrapAny(&authmodulev1.Module{
					Bech32Prefix:                AccountAddressPrefix,
					ModuleAccountPermissions:    moduleAccPerms,
					EnableUnorderedTransactions: false,
					// By default modules authority is the governance module. This is configurable with the following:
					// Authority: "group", // A custom module authority can be set using a module name
					// Authority: "cosmos1cwwv22j5ca08ggdv9c2uky355k908694z577tv", // or a specific address
				}),
			},
			{
				Name: banktypes.ModuleName,
				Config: appconfig.WrapAny(&bankmodulev1.Module{
					BlockedModuleAccountsOverride: blockAccAddrs,
				}),
			},
			{
				Name: stakingtypes.ModuleName,
				Config: appconfig.WrapAny(&stakingmodulev1.Module{
					// NOTE: specifying a prefix is only necessary when using bech32 addresses
					// If not specfied, the auth Bech32Prefix appended with "valoper" and "valcons" is used by default
					Bech32PrefixValidator: AccountAddressPrefix + "valoper",
					Bech32PrefixConsensus: AccountAddressPrefix + "valcons",
				}),
			},
			{
				Name:   slashingtypes.ModuleName,
				Config: appconfig.WrapAny(&slashingmodulev1.Module{}),
			},
			{
				Name:   govtypes.ModuleName,
				Config: appconfig.WrapAny(&govmodulev1.Module{}),
			},
			{
				Name:   "tx",
				Config: appconfig.WrapAny(&txconfigv1.Config{}),
			},
			{
				Name:   genutiltypes.ModuleName,
				Config: appconfig.WrapAny(&genutilmodulev1.Module{}),
			},
			{
				Name:   distrtypes.ModuleName,
				Config: appconfig.WrapAny(&distrmodulev1.Module{}),
			},
			{
				Name:   consensustypes.ModuleName,
				Config: appconfig.WrapAny(&consensusmodulev1.Module{}),
			},
			{
				Name:   hubtypes.ModuleName,
				Config: appconfig.WrapAny(&hubtypes.Module{}),
			},
			{
				Name:   tasktypes.ModuleName,
				Config: appconfig.WrapAny(&tasktypes.Module{}),
			},
			// The pinned upstream Hyperlane modules are mounted as-is: Node does
			// not fork their proto, state or handlers (§3.2). Their public Msg
			// surface is narrowed by app/bridge.MsgClosedSet and their ledger
			// access by app/bridge.GuardedBankKeeper.
			{
				Name:   hlcoretypes.ModuleName,
				Config: appconfig.WrapAny(&hlcoremodulev1.Module{}),
			},
			{
				Name: hlwarptypes.ModuleName,
				// §3.3: TrueOpen holds the Synthetic side only. A Collateral token here
				// would put collateral on both sides of the same asset and break the
				// §5 conservation identity, so it is excluded at the module config.
				Config: appconfig.WrapAny(&hlwarpmodulev1.Module{
					EnabledTokens: []int32{int32(hlwarptypes.HYP_TOKEN_TYPE_SYNTHETIC)},
				}),
			}},
	})
)
