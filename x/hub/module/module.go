package module

import (
	"context"
	"encoding/json"
	"fmt"

	"cosmossdk.io/core/appmodule"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"google.golang.org/grpc"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
)

var (
	_ module.AppModuleBasic     = (*AppModule)(nil)
	_ module.AppModule          = (*AppModule)(nil)
	_ module.HasGenesis         = (*AppModule)(nil)
	_ module.HasInvariants      = (*AppModule)(nil)
	_ appmodule.AppModule       = (*AppModule)(nil)
	_ appmodule.HasBeginBlocker = (*AppModule)(nil)
	_ appmodule.HasEndBlocker   = (*AppModule)(nil)
)

// AppModule owns the Hub service and block-lifecycle wiring.
type AppModule struct {
	cdc           codec.Codec
	keeper        keeper.Keeper
	msgServerDeps types.MsgServerDependencies
}

func NewAppModule(cdc codec.Codec, keeper keeper.Keeper, msgServerDeps types.MsgServerDependencies) AppModule {
	keeper = keeper.WithRuntimeDependencies(msgServerDeps.FreezeTaskValidator, msgServerDeps.ValidatorSnapshotProvider, msgServerDeps.RoleFaultConsumerGate)
	return AppModule{cdc: cdc, keeper: keeper, msgServerDeps: msgServerDeps}
}

func (AppModule) IsAppModule() {}

// IsOnePerModuleType implements the depinject.OnePerModuleType marker required by SDK app modules.
func (AppModule) IsOnePerModuleType() {}

func (AppModule) Name() string { return types.ModuleName }

func (am AppModule) RegisterInvariants(registry sdk.InvariantRegistry) {
	for _, invariant := range am.keeper.InvariantChecks() {
		check := invariant
		registry.RegisterRoute(types.ModuleName, check.Name, func(ctx sdk.Context) (string, bool) {
			if err := check.Check(sdk.WrapSDKContext(ctx)); err != nil {
				return sdk.FormatInvariant(types.ModuleName, check.Name, err.Error()), true
			}
			return sdk.FormatInvariant(types.ModuleName, check.Name, check.Description+": ok"), false
		})
	}
}

func (AppModule) RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {
	types.RegisterLegacyAminoCodec(cdc)
}

func (AppModule) RegisterGRPCGatewayRoutes(clientCtx client.Context, mux *runtime.ServeMux) {
	if err := types.RegisterQueryHandlerClient(clientCtx.CmdContext, mux, types.NewQueryClient(clientCtx)); err != nil {
		panic(err)
	}
}

func (AppModule) RegisterInterfaces(registrar codectypes.InterfaceRegistry) {
	types.RegisterInterfaces(registrar)
}

func (am AppModule) RegisterServices(registrar grpc.ServiceRegistrar) error {
	if err := validateMsgServerDependencies(am.msgServerDeps); err != nil {
		return err
	}
	types.RegisterMsgServer(registrar, keeper.NewMsgServerImpl(am.keeper, am.msgServerDeps))
	types.RegisterQueryServer(registrar, keeper.NewQueryServerImpl(am.keeper))
	return nil
}

func validateMsgServerDependencies(deps types.MsgServerDependencies) error {
	if deps.FreezeTaskValidator == nil {
		return fmt.Errorf("%s freeze task validator is required", types.ModuleName)
	}
	if deps.ValidatorSnapshotProvider == nil {
		return fmt.Errorf("%s historical validator snapshot provider is required", types.ModuleName)
	}
	if deps.TaskSafetyWindowProvider == nil {
		return fmt.Errorf("%s task safety window provider is required", types.ModuleName)
	}
	if deps.RoleFaultConsumerGate == nil {
		return fmt.Errorf("%s role fault consumer gate is required", types.ModuleName)
	}
	return nil
}

func (am AppModule) DefaultGenesis(codec.JSONCodec) json.RawMessage {
	return am.cdc.MustMarshalJSON(types.DefaultGenesis())
}

func (am AppModule) ValidateGenesis(_ codec.JSONCodec, _ client.TxEncodingConfig, bz json.RawMessage) error {
	var genState types.GenesisState
	if err := am.cdc.UnmarshalJSON(bz, &genState); err != nil {
		return fmt.Errorf("failed to unmarshal %s genesis state: %w", types.ModuleName, err)
	}
	if genState.Params.Epoch.EpochLengthBlocks == 0 {
		genState.Params.Epoch.EpochLengthBlocks = types.DefaultEpochLengthBlocks
	}
	return genState.Validate()
}

func (am AppModule) InitGenesis(ctx sdk.Context, _ codec.JSONCodec, gs json.RawMessage) {
	var genState types.GenesisState
	if err := am.cdc.UnmarshalJSON(gs, &genState); err != nil {
		panic(fmt.Errorf("failed to unmarshal %s genesis state: %w", types.ModuleName, err))
	}
	if genState.Params.Epoch.EpochLengthBlocks == 0 {
		genState.Params.Epoch.EpochLengthBlocks = types.DefaultEpochLengthBlocks
	}
	if err := am.keeper.InitGenesis(ctx, genState); err != nil {
		panic(fmt.Errorf("failed to initialize %s genesis state: %w", types.ModuleName, err))
	}
}

func (am AppModule) ExportGenesis(ctx sdk.Context, _ codec.JSONCodec) json.RawMessage {
	genState, err := am.keeper.ExportGenesis(ctx)
	if err != nil {
		panic(fmt.Errorf("failed to export %s genesis state: %w", types.ModuleName, err))
	}
	bz, err := am.cdc.MarshalJSON(genState)
	if err != nil {
		panic(fmt.Errorf("failed to marshal %s genesis state: %w", types.ModuleName, err))
	}
	return bz
}

func (AppModule) ConsensusVersion() uint64 { return 2 }

func (am AppModule) BeginBlock(ctx context.Context) error {
	return am.keeper.BeginBlocker(ctx)
}

func (am AppModule) EndBlock(ctx context.Context) error {
	return am.keeper.EndBlocker(ctx)
}
