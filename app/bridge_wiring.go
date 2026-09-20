package app

import (
	"cosmossdk.io/depinject"

	hlcorekeeper "github.com/bcp-innovations/hyperlane-cosmos/x/core/keeper"
	hlcoretypes "github.com/bcp-innovations/hyperlane-cosmos/x/core/types"
	hlwarpkeeper "github.com/bcp-innovations/hyperlane-cosmos/x/warp/keeper"
	hlwarptypes "github.com/bcp-innovations/hyperlane-cosmos/x/warp/types"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	nodeante "github.com/TrueOpen/node/app/ante"
	appbridge "github.com/TrueOpen/node/app/bridge"
	hubkeeper "github.com/TrueOpen/node/x/hub/keeper"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

// HyperlaneBankKeepers is what the two upstream Hyperlane modules receive
// instead of the raw bank keeper.
//
// cross_chain_asset_bridge_protocol.md §5.1 makes the warp module the only
// legal mint/burn authority for business_denom, and observes that a Cosmos
// Minter grant is not scoped to a denom. Interposing here is what turns that
// open grant into the closed set the
// contract requires, and it is also where the §7 guard and the §5.2 conservation
// identity run — inside the upstream handler's own context, so a refusal leaves
// no mint, no usage and no supply movement behind.
// ProvideGuardedBankKeeper returns the concrete guard. The two upstream
// ModuleInputs ask for their own BankKeeper interfaces, and AppConfig binds both
// of those interface names to this type — an interface-typed output would
// instead collide with the bank module's own BaseKeeper, which already satisfies
// them.
func ProvideGuardedBankKeeper(bank bankkeeper.Keeper, hub hubkeeper.Keeper) appbridge.GuardedBankKeeper {
	return appbridge.NewGuardedBankKeeper(bank, hub)
}

// HyperlaneBankKeeperBindings names the two upstream interfaces that must
// resolve to the guard rather than to the plain bank keeper. See
// bindGuardedInterface for why the names are derived rather than written.
var HyperlaneBankKeeperBindings = []depinject.Config{
	bindGuardedInterface[hlcoretypes.BankKeeper, appbridge.GuardedBankKeeper](),
	bindGuardedInterface[hlwarptypes.BankKeeper, appbridge.GuardedBankKeeper](),
}

var StakingBankKeeperBinding = bindGuardedInterface[stakingtypes.BankKeeper, GovernedStakingBankKeeper]()

// ProvideBridgeUpstream hands the Hub the read-only projection of the Hyperlane
// objects its route guard compares against. Supplying it here is what keeps
// x/hub free of any Hyperlane import (cross_chain_asset_bridge_protocol.md §3.2).
func ProvideBridgeUpstream(core *hlcorekeeper.Keeper, warp hlwarpkeeper.Keeper) hubtypes.BridgeUpstream {
	return appbridge.NewUpstreamAdapter(core, warp)
}

// ProvideVrfPoPVerifier supplies §9.3a's ECVRF possession check to the Hub
// module, mirroring how the beacon path receives its verifier: the curve suite
// lives in app/ante and the keeper only holds the interface. Without this
// provider RegisterVrfKey is permanently FailedPrecondition, which is exactly
// what "verifier is not installed" is there to report — but on a real chain it
// would mean no validator could ever register a VRF key.
func ProvideVrfPoPVerifier() hubkeeper.VrfPoPVerifier {
	return nodeante.NewVRFPoPVerifier()
}

// keep the upstream interface names honest: if either moves, this stops
// compiling instead of silently leaving the plain bank keeper in place.
var (
	_ hlcoretypes.BankKeeper = appbridge.GuardedBankKeeper{}
	_ hlwarptypes.BankKeeper = appbridge.GuardedBankKeeper{}
)
