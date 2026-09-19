package module

import (
	"cosmossdk.io/core/address"
	"cosmossdk.io/core/appmodule"
	"cosmossdk.io/core/store"
	"cosmossdk.io/depinject"
	"cosmossdk.io/depinject/appconfig"
	"github.com/cosmos/cosmos-sdk/codec"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/TrueOpen/node/x/task/keeper"
	"github.com/TrueOpen/node/x/task/types"
)

func init() {
	appconfig.Register(
		&types.Module{},
		appconfig.Provide(ProvideModule),
	)
}

// ModuleInputs are the depinject inputs for the task module.
//
// HubKeeper is a task-owned view of the hub (types.HubKeeper). It is provided by
// the app, which binds the concrete hub keeper to this interface — the task
// module must never import x/hub/keeper (dependency-direction rule 2).
type ModuleInputs struct {
	depinject.In

	Config                *types.Module
	StoreService          store.KVStoreService
	TransientStoreService store.TransientStoreService
	Cdc                   codec.Codec
	AddressCodec          address.Codec

	AuthKeeper types.AuthKeeper
	BankKeeper types.BankKeeper
	HubKeeper  types.HubKeeper
}

// ModuleOutputs exposes the task keeper and module (the proven single-keeper
// module-provider shape). The two hub-facing adapter interfaces the task keeper
// implements (FreezeSignalTaskValidator / TaskSafetyWindowProvider) are bound at
// container scope in app.go, alongside the HubKeeper and ValidatorSnapshot
// bindings — keeping all cross-module wiring in one place and out of the
// module-scoped providers.
// ModuleOutputs exposes only the concrete task keeper and module. The hub
// module consumes the task keeper through its FreezeSignalTaskValidator /
// TaskSafetyWindowProvider interface INPUTS — depinject matches the concrete
// task keeper (which implements both) to those interfaces. Emitting interface
// outputs here would make depinject double-register keeper.Keeper (the concrete
// value behind each interface field) and panic with a duplicate provision.
type ModuleOutputs struct {
	depinject.Out

	TaskKeeper keeper.Keeper
	Module     appmodule.AppModule
}

func ProvideModule(in ModuleInputs) ModuleOutputs {
	// default to governance authority if not provided
	authority := authtypes.NewModuleAddress(types.GovModuleName)
	if in.Config.Authority != "" {
		authority = authtypes.NewModuleAddressOrBech32Address(in.Config.Authority)
	}

	k := keeper.NewKeeper(
		in.StoreService,
		in.TransientStoreService,
		in.Cdc,
		in.AddressCodec,
		authority,
		in.AuthKeeper,
		in.BankKeeper,
		in.HubKeeper,
	)
	m := NewAppModule(in.Cdc, k)

	return ModuleOutputs{
		TaskKeeper: k,
		Module:     m,
	}
}
