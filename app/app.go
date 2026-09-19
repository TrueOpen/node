package app

import (
	"fmt"
	"io"
	"sync"

	clienthelpers "cosmossdk.io/client/v2/helpers"
	"cosmossdk.io/core/appmodule"
	"cosmossdk.io/depinject"
	"cosmossdk.io/log"
	storetypes "cosmossdk.io/store/types"

	hlcorekeeper "github.com/bcp-innovations/hyperlane-cosmos/x/core/keeper"
	hlwarpkeeper "github.com/bcp-innovations/hyperlane-cosmos/x/warp/keeper"
	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/server"
	"github.com/cosmos/cosmos-sdk/server/api"
	"github.com/cosmos/cosmos-sdk/server/config"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	distrkeeper "github.com/cosmos/cosmos-sdk/x/distribution/keeper"
	"github.com/cosmos/cosmos-sdk/x/genutil"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	govkeeper "github.com/cosmos/cosmos-sdk/x/gov/keeper"
	slashingkeeper "github.com/cosmos/cosmos-sdk/x/slashing/keeper"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
	evmcryptocodec "github.com/cosmos/evm/crypto/codec"
	"github.com/cosmos/evm/crypto/ethsecp256k1"
	"github.com/cosmos/evm/ethereum/eip712"

	appbridge "github.com/TrueOpen/node/app/bridge"
	"github.com/TrueOpen/node/app/taskevents"
	"github.com/TrueOpen/node/docs"
	hubkeeper "github.com/TrueOpen/node/x/hub/keeper"
	taskkeeper "github.com/TrueOpen/node/x/task/keeper"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	// Name is the name of the application.
	Name = "node"
	// AccountAddressPrefix is the prefix for accounts addresses.
	AccountAddressPrefix = "trueopen"
	// ChainCoinType is the coin type of the chain.
	ChainCoinType = 60
)

// DefaultNodeHome default home directories for the application daemon
var DefaultNodeHome string

var (
	_ runtime.AppI            = (*App)(nil)
	_ servertypes.Application = (*App)(nil)
)

// App extends an ABCI application, but with most of its parameters exported.
// They are exported for convenience in creating helper functions, as object
// capabilities aren't needed for testing.
type App struct {
	*runtime.App
	legacyAmino       *codec.LegacyAmino
	appCodec          codec.Codec
	txConfig          client.TxConfig
	interfaceRegistry codectypes.InterfaceRegistry

	// keepers
	AuthKeeper     authkeeper.AccountKeeper
	BankKeeper     bankkeeper.Keeper
	StakingKeeper  *stakingkeeper.Keeper
	DistrKeeper    distrkeeper.Keeper
	GovKeeper      *govkeeper.Keeper
	SlashingKeeper slashingkeeper.Keeper

	// simulation manager
	sm                 *module.SimulationManager
	HubKeeper          hubkeeper.Keeper
	TaskKeeper         taskkeeper.Keeper
	trueopenInvariants *runtimeInvariantRegistry
	taskEventServer    *taskevents.Server
	bridgeUpstream     appbridge.UpstreamAdapter
	closeOnce          sync.Once
	closeErr           error
}

func init() {
	var err error
	clienthelpers.EnvPrefix = Name
	DefaultNodeHome, err = clienthelpers.GetNodeHomeDirectory("." + Name)
	if err != nil {
		panic(err)
	}
}

// AppConfig returns the default app config.
func AppConfig() depinject.Config {
	return depinject.Configs(
		appConfig,
		// The app-side staking adapter for the hub's historical validator
		// snapshot dependency. The hub<->task keeper bindings are done
		// module-to-module in the module depinject providers.
		depinject.Provide(ProvideValidatorSnapshotProvider),
		// The Hyperlane modules receive the guarded bank keeper rather than the
		// raw one; see app/bridge.GuardedBankKeeper.
		depinject.Provide(ProvideGuardedBankKeeper),
		depinject.Provide(ProvideGovernedStakingBankKeeper),
		depinject.Provide(ProvideGovernedGovBankKeeper),
		depinject.Provide(ProvideBridgeUpstream),
		depinject.Provide(ProvideGovernanceActionReplayChecker),
		depinject.Provide(ProvideGovernanceContentRuntime),
		depinject.Provide(ProvideVrfPoPVerifier),
		depinject.Configs(HyperlaneBankKeeperBindings...),
		StakingBankKeeperBinding,
		GovernanceBankKeeperBinding,
		depinject.Supply(
			// supply custom module basics
			map[string]module.AppModuleBasic{
				genutiltypes.ModuleName: genutil.NewAppModuleBasic(genutiltypes.DefaultMessageValidator),
			},
		),
	)
}

// New returns a reference to an initialized App.
func New(
	logger log.Logger,
	db dbm.DB,
	traceStore io.Writer,
	loadLatest bool,
	appOpts servertypes.AppOptions,
	baseAppOptions ...func(*baseapp.BaseApp),
) *App {
	var (
		app        = &App{}
		appBuilder *runtime.AppBuilder
		bridgeCore *hlcorekeeper.Keeper
		bridgeWarp hlwarpkeeper.Keeper

		// merge the AppConfig and other configuration in one config
		appConfig = depinject.Configs(
			AppConfig(),
			depinject.Supply(
				appOpts, // supply app options
				logger,  // supply logger
				// here alternative options can be supplied to the DI container.
				// those options can be used f.e to override the default behavior of some modules.
				// for instance supplying a custom address codec for not using bech32 addresses.
				// read the depinject documentation and depinject module wiring for more information
				// on available options and how to use them.
			),
		)
	)
	taskEventConfig, err := taskevents.ConfigFromAppOptions(appOpts)
	if err != nil {
		panic(err)
	}
	beaconNodeConfig, err := BeaconNodeConfigFromAppOptions(appOpts)
	if err != nil {
		panic(err)
	}

	var appModules map[string]appmodule.AppModule
	if err := depinject.Inject(appConfig,
		&appBuilder,
		&appModules,
		&app.appCodec,
		&app.legacyAmino,
		&app.txConfig,
		&app.interfaceRegistry,
		&app.AuthKeeper,
		&app.BankKeeper,
		&app.StakingKeeper,
		&app.DistrKeeper,
		&app.GovKeeper,
		&app.SlashingKeeper,
		&app.HubKeeper,
		&app.TaskKeeper,
		&bridgeCore,
		&bridgeWarp,
	); err != nil {
		panic(err)
	}
	RegisterEthereumAccountEncoding(app.legacyAmino, app.interfaceRegistry)
	app.bridgeUpstream = appbridge.NewUpstreamAdapter(bridgeCore, bridgeWarp)

	// add to default baseapp options
	// enable optimistic execution
	baseAppOptions = append(baseAppOptions, baseapp.SetOptimisticExecution())

	// build app
	app.App = appBuilder.Build(db, traceStore, baseAppOptions...)
	// cross_chain_asset_bridge_protocol.md §3.2 and §9: only MsgProcessMessage
	// and MsgRemoteTransfer are callable Hyperlane messages. The circuit
	// breaker is consulted by the router for every message before its handler
	// runs, so a closed message has no reachable route even though the upstream
	// modules are mounted whole for their state, Genesis and Query services.
	app.MsgServiceRouter().SetCircuit(appbridge.NewMsgClosedSet(nil))
	if err := app.installBridgeAnteHandler(); err != nil {
		panic(err)
	}
	app.SetPostHandler(app.taskGasPostHandler)
	if taskEventConfig.Enabled {
		app.taskEventServer = taskevents.NewServer(logger, taskEventConfig)
	}

	/****  Module Options ****/

	// create the simulation manager and define the order of the modules for deterministic simulations
	app.sm = module.NewSimulationManagerFromAppModules(app.ModuleManager.Modules, make(map[string]module.AppModuleSimulation))
	app.sm.RegisterStoreDecoders()

	// Let runtime.App install the configured module orders and lifecycle
	// handlers before composing any application-owned wrappers. Loading the
	// latest store is deferred until every wrapper is installed, because that
	// operation seals BaseApp.
	if err := app.Load(false); err != nil {
		panic(err)
	}

	// Module-local Genesis validation cannot prove Hub/Task references agree.
	// Run the cross-module check after both modules have initialized, while the
	// InitChain transaction can still reject the complete document atomically.
	app.SetInitChainer(func(ctx sdk.Context, req *abci.RequestInitChain) (*abci.ResponseInitChain, error) {
		response, err := app.App.InitChainer(ctx, req)
		if err != nil {
			return nil, err
		}
		if err := app.EnsureCrossModuleReferences(ctx); err != nil {
			return nil, fmt.Errorf("invalid TrueOpen cross-module genesis: %w", err)
		}
		// Fresh bootstrap fixes every validator key at epoch 0. A normal state
		// export re-import also enters InitChain, but preserves a later
		// initial_height and may legitimately contain a rotated active key or one
		// pending rotation; do not reinterpret that state as fresh genesis.
		if requiresFreshGenesisVrfKeyCoverage(req) {
			if err := app.EnsureGenesisValidatorVrfKeyCoverage(ctx); err != nil {
				return nil, fmt.Errorf("invalid genesis validator VRF keys: %w", err)
			}
		}
		// cross_chain_asset_bridge_protocol.md §2.1/§3.3/§3.4/§4.4: the frozen
		// route must describe the Hyperlane objects that were actually
		// imported. Neither module can check this alone, and it has to hold
		// before the first block rather than at the first transfer.
		if err := app.HubKeeper.ValidateBridgeUpstream(ctx); err != nil {
			return nil, fmt.Errorf("invalid bridge genesis: %w", err)
		}
		return response, nil
	})
	// Install Beacon PrepareProposal / ProcessProposal / PreBlocker wiring —
	// the hooks that carry the ECVRF proof through the proposal pipeline.
	// ACCEPT/REJECT is decided solely by the committed
	// BeaconParamsV1.vrf_required_from_height and is identical across every
	// build artifact; the `mainnet` build tag only adds a local startup
	// assertion. A validator must have vrf_key.json by default; a
	// non-block-producing node may explicitly disable that assertion in
	// app.toml.
	nodeHome, _ := appOpts.Get(flags.FlagHome).(string)
	app.wireBeaconHooks(logger, nodeHome, beaconNodeConfig.VrfKeyRequired)

	app.installRuntimeInvariants()
	if loadLatest {
		if err := app.LoadLatestVersion(); err != nil {
			panic(err)
		}
	}
	if loadLatest && app.LastBlockHeight() > 0 {
		if err := app.EnsureLoadedStoreSchemas(); err != nil {
			panic(err)
		}
	}

	return app
}

func requiresFreshGenesisVrfKeyCoverage(req *abci.RequestInitChain) bool {
	return req == nil || req.InitialHeight <= 1
}

// RegisterEthereumAccountEncoding extends the runtime-generated SDK codecs with
// the account key and tx extension types used by Ethereum wallets. The SDK
// crypto registrations already exist at this point, so only the EVM additions
// are installed to avoid duplicate Amino registrations.
func RegisterEthereumAccountEncoding(amino *codec.LegacyAmino, interfaces codectypes.InterfaceRegistry) {
	evmcryptocodec.RegisterInterfaces(interfaces)
	eip712.RegisterInterfaces(interfaces)
	amino.RegisterConcrete(&ethsecp256k1.PubKey{}, ethsecp256k1.PubKeyName, nil)
	amino.RegisterConcrete(&ethsecp256k1.PrivKey{}, ethsecp256k1.PrivKeyName, nil)
}

// EnsureLoadedStoreSchemas rejects Task stores that do not match the exact
// schema supported by this binary. Hub uses a fresh-genesis-only contract and
// intentionally has no migration/version compatibility state.
func (app *App) EnsureLoadedStoreSchemas() error {
	// The chain-id is load-bearing, not decoration: cross-module invariants
	// recompute frozen commitments whose preimage covers it (Builder duty
	// selection is one), so a header without it does not under-check them, it
	// makes them underivable and reports consistent state as broken. baseapp
	// takes the value from --chain-id or the genesis file (see
	// server.DefaultBaseappOptions), which is set before this runs; InitChain
	// gets it from the request, which is why the genesis path never saw this.
	chainID := app.BaseApp.ChainID()
	if chainID == "" {
		return fmt.Errorf("refusing to verify TrueOpen state without a chain-id; pass --chain-id or restore the genesis file")
	}
	ctx := sdk.WrapSDKContext(app.NewContextLegacy(true, cmtproto.Header{
		Height:  app.LastBlockHeight(),
		ChainID: chainID,
	}))
	if err := app.TaskKeeper.EnsureCurrentStoreSchema(ctx); err != nil {
		return fmt.Errorf("refusing to load incompatible task store; delete/reset the node database and initialize from fresh genesis: %w", err)
	}
	if err := app.EnsureCrossModuleReferences(ctx); err != nil {
		return fmt.Errorf("refusing to load inconsistent TrueOpen cross-module state: %w", err)
	}
	return nil
}

// LegacyAmino returns App's amino codec.
func (app *App) LegacyAmino() *codec.LegacyAmino {
	return app.legacyAmino
}

// AppCodec returns App's app codec.
//
// NOTE: This is solely to be used for testing purposes as it may be desirable
// for modules to register their own custom testing types.
func (app *App) AppCodec() codec.Codec {
	return app.appCodec
}

// InterfaceRegistry returns App's InterfaceRegistry.
func (app *App) InterfaceRegistry() codectypes.InterfaceRegistry {
	return app.interfaceRegistry
}

// TxConfig returns App's TxConfig
func (app *App) TxConfig() client.TxConfig {
	return app.txConfig
}

// GetKey returns the KVStoreKey for the provided store key.
func (app *App) GetKey(storeKey string) *storetypes.KVStoreKey {
	kvStoreKey, ok := app.UnsafeFindStoreKey(storeKey).(*storetypes.KVStoreKey)
	if !ok {
		return nil
	}
	return kvStoreKey
}

// SimulationManager implements the SimulationApp interface
func (app *App) SimulationManager() *module.SimulationManager {
	return app.sm
}

// RegisterAPIRoutes registers all application module routes with the provided
// API server.
func (app *App) RegisterAPIRoutes(apiSvr *api.Server, apiConfig config.APIConfig) {
	app.App.RegisterAPIRoutes(apiSvr, apiConfig)
	// The protobuf binary and Store representation of Hash32 stays raw bytes.
	// Human-facing TrueOpen REST routes use canonical lowercase 64-hex while all
	// explicitly classified non-Hash32 bytes retain protobuf JSON base64.
	apiSvr.Router.PathPrefix("/TrueOpen/").Handler(newHash32RESTHandler(apiSvr.GRPCGatewayRouter))
	// register swagger API in app.go so that other applications can override easily
	if err := server.RegisterSwaggerAPI(apiSvr.ClientCtx, apiSvr.Router, apiConfig.Swagger); err != nil {
		panic(err)
	}

	// register app's OpenAPI routes.
	docs.RegisterOpenAPIService(Name, apiSvr.Router)
}

// GetMaccPerms returns a copy of the module account permissions
//
// NOTE: This is solely to be used for testing purposes.
func GetMaccPerms() map[string][]string {
	dup := make(map[string][]string)
	for _, perms := range moduleAccPerms {
		dup[perms.GetAccount()] = perms.GetPermissions()
	}

	return dup
}

// BlockedAddresses returns all the app's blocked account addresses.
func BlockedAddresses() map[string]bool {
	result := make(map[string]bool)

	if len(blockAccAddrs) > 0 {
		for _, addr := range blockAccAddrs {
			result[addr] = true
		}
	} else {
		for addr := range GetMaccPerms() {
			result[addr] = true
		}
	}

	return result
}
