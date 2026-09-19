package module

import (
	"context"

	"cosmossdk.io/core/address"
	"cosmossdk.io/core/appmodule"
	"cosmossdk.io/core/store"
	"cosmossdk.io/depinject"
	"cosmossdk.io/depinject/appconfig"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	govv1beta1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1beta1"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
)

func init() {
	appconfig.Register(
		&types.Module{},
		// Two providers: the keeper is provided separately from the module so
		// the dependency graph resolves HubKeeper -> TaskKeeper -> HubModule
		// without a cycle. The Hub *module* needs task-owned adapters
		// (FreezeSignalTaskValidator / TaskSafetyWindowProvider), which in turn
		// need the Hub keeper; splitting breaks the would-be cycle.
		appconfig.Provide(ProvideKeeper, ProvideModule),
	)
}

// KeeperInputs are the depinject inputs required to build the Hub keeper.
type KeeperInputs struct {
	depinject.In

	Config                *types.Module
	StoreService          store.KVStoreService
	TransientStoreService store.TransientStoreService
	Cdc                   codec.Codec
	AddressCodec          address.Codec

	AuthKeeper types.AuthKeeper
	BankKeeper types.BankKeeper
}

// KeeperOutputs exposes the Hub keeper (concrete, for app.go injection) and the
// same keeper bound to the HubKeeper interface, which the task module consumes
// module-to-module (the task module must not import x/hub/keeper). Keeping
// this binding at the module boundary avoids app-level providers that consume a
// module-scoped keeper, which depinject rejects as a duplicate provision.
type KeeperOutputs struct {
	depinject.Out

	HubKeeper keeper.Keeper
}

func ProvideKeeper(in KeeperInputs) KeeperOutputs {
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
	)
	return KeeperOutputs{HubKeeper: k}
}

// ModuleInputs are the depinject inputs for the Hub AppModule. The three
// cross-cutting dependencies are supplied by the app / task module:
//   - FreezeTaskValidator, TaskSafetyWindowProvider: implemented by the task keeper.
//   - ValidatorSnapshotProvider: an app-side adapter over the SDK staking keeper.
type ModuleInputs struct {
	depinject.In

	Cdc       codec.Codec
	HubKeeper keeper.Keeper

	FreezeTaskValidator       types.FreezeSignalTaskValidator
	ValidatorSnapshotProvider types.ValidatorSnapshotProvider
	TaskSafetyWindowProvider  types.TaskSafetyWindowProvider
	RoleFaultConsumerGate     types.RoleFaultConsumerGate
	// BridgeUpstream is supplied by app wiring over the pinned Hyperlane
	// keepers. It is optional so a chain without a bridge, and every direct
	// keeper fixture, still wires; ValidateBridgeUpstream reports its absence
	// rather than silently passing.
	BridgeUpstream types.BridgeUpstream `optional:"true"`
	// VrfPoPVerifier supplies §9.3a's ECVRF possession check. Hub declares the
	// interface and app wiring supplies the curve implementation, the same split
	// the beacon path uses, so the keeper never carries a second copy of the
	// suite. Optional for the same reason as BridgeUpstream: direct keeper
	// fixtures still wire, and RegisterVrfKey reports its absence rather than
	// verifying nothing.
	VrfPoPVerifier keeper.VrfPoPVerifier `optional:"true"`
	// GovernanceActionReplayChecker reads the authoritative x/gov proposal
	// lifecycle before a retained TreasurySpend receipt is pruned.
	GovernanceActionReplayChecker keeper.GovernanceActionReplayChecker `optional:"true"`
	GovernanceContentRuntime      keeper.GovernanceContentRuntime
}

type ModuleOutputs struct {
	depinject.Out

	Module     appmodule.AppModule
	GovHandler govv1beta1.HandlerRoute
	GovHooks   govtypes.GovHooksWrapper
}

func ProvideModule(in ModuleInputs) ModuleOutputs {
	deps := types.MsgServerDependencies{
		FreezeTaskValidator:       in.FreezeTaskValidator,
		ValidatorSnapshotProvider: in.ValidatorSnapshotProvider,
		TaskSafetyWindowProvider:  in.TaskSafetyWindowProvider,
		RoleFaultConsumerGate:     in.RoleFaultConsumerGate,
	}
	hub := in.HubKeeper.WithBridgeUpstream(in.BridgeUpstream)
	if in.VrfPoPVerifier != nil {
		hub = hub.WithVrfPoPVerifier(in.VrfPoPVerifier)
	}
	if in.GovernanceActionReplayChecker != nil {
		hub = hub.WithGovernanceActionReplayChecker(in.GovernanceActionReplayChecker)
	}
	m := NewAppModule(in.Cdc, hub, deps)
	runtime := in.GovernanceContentRuntime
	return ModuleOutputs{
		Module: m,
		GovHandler: govv1beta1.HandlerRoute{
			RouteKey: types.GovernanceProposalRoute,
			Handler:  runtime.HandleGovernanceContent,
		},
		GovHooks: govtypes.GovHooksWrapper{GovHooks: governanceSubmissionHooks{runtime: runtime}},
	}
}

type governanceSubmissionHooks struct {
	runtime keeper.GovernanceContentRuntime
}

func (h governanceSubmissionHooks) AfterProposalSubmission(ctx context.Context, proposalID uint64) error {
	return h.runtime.ValidateSubmittedGovernanceProposal(ctx, proposalID)
}

func (governanceSubmissionHooks) AfterProposalDeposit(context.Context, uint64, sdk.AccAddress) error {
	return nil
}

func (governanceSubmissionHooks) AfterProposalVote(context.Context, uint64, sdk.AccAddress) error {
	return nil
}

func (governanceSubmissionHooks) AfterProposalFailedMinDeposit(context.Context, uint64) error {
	return nil
}

func (governanceSubmissionHooks) AfterProposalVotingPeriodEnded(context.Context, uint64) error {
	return nil
}
