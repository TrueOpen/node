package app

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkmodule "github.com/cosmos/cosmos-sdk/types/module"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

type runtimeInvariantRoute struct {
	moduleName string
	name       string
	check      sdk.Invariant
}

// runtimeInvariantRegistry is the production consumer for the legacy SDK
// invariant registration surface. Cosmos SDK v0.53 keeps
// module.Manager.RegisterInvariants as a no-op, so the application must own the
// concrete registry if module routes are expected to halt FinalizeBlock.
type runtimeInvariantRegistry struct {
	routes []runtimeInvariantRoute
	seen   map[string]struct{}
}

var _ sdk.InvariantRegistry = (*runtimeInvariantRegistry)(nil)

func newRuntimeInvariantRegistry() *runtimeInvariantRegistry {
	return &runtimeInvariantRegistry{seen: make(map[string]struct{})}
}

func (r *runtimeInvariantRegistry) RegisterRoute(moduleName, route string, check sdk.Invariant) {
	if moduleName == "" || route == "" || check == nil {
		panic("runtime invariant route must have a module, name, and check")
	}
	key := moduleName + "\x00" + route
	if _, duplicate := r.seen[key]; duplicate {
		panic(fmt.Sprintf("duplicate runtime invariant route %s/%s", moduleName, route))
	}
	r.seen[key] = struct{}{}
	r.routes = append(r.routes, runtimeInvariantRoute{moduleName: moduleName, name: route, check: check})
}

func (r *runtimeInvariantRegistry) countModule(moduleName string) int {
	count := 0
	for _, route := range r.routes {
		if route.moduleName == moduleName {
			count++
		}
	}
	return count
}

func (r *runtimeInvariantRegistry) run(ctx sdk.Context) error {
	for _, route := range r.routes {
		message, broken := route.check(ctx)
		if broken {
			return fmt.Errorf("runtime invariant %s/%s failed: %s", route.moduleName, route.name, message)
		}
	}
	return nil
}

func (app *App) installRuntimeInvariants() {
	registry := newRuntimeInvariantRegistry()
	expectedRoutes := map[string]int{
		hubtypes.ModuleName:  len(app.HubKeeper.InvariantChecks()),
		tasktypes.ModuleName: len(app.TaskKeeper.InvariantChecks()),
	}
	for _, moduleName := range []string{hubtypes.ModuleName, tasktypes.ModuleName} {
		appModule, exists := app.ModuleManager.Modules[moduleName]
		if !exists {
			panic(fmt.Sprintf("runtime invariant module %s is not wired", moduleName))
		}
		withInvariants, ok := appModule.(sdkmodule.HasInvariants)
		if !ok {
			panic(fmt.Sprintf("runtime invariant module %s does not register invariants", moduleName))
		}
		withInvariants.RegisterInvariants(registry)
		if got, want := registry.countModule(moduleName), expectedRoutes[moduleName]; got != want {
			panic(fmt.Sprintf("runtime invariant route count for %s is %d, want %d", moduleName, got, want))
		}
	}
	app.trueopenInvariants = registry

	// The runtime App's EndBlocker invokes all module EndBlockers. Run the
	// registered checks afterwards so a broken invariant aborts FinalizeBlock
	// before any of that block's state can be committed.
	app.SetEndBlocker(func(ctx sdk.Context) (sdk.EndBlock, error) {
		response, err := app.App.EndBlocker(ctx)
		if err != nil {
			return response, err
		}
		if err := app.HubKeeper.FinalizeProfilePriceSamples(sdk.WrapSDKContext(ctx)); err != nil {
			return response, err
		}
		if err := registry.run(ctx); err != nil {
			return response, err
		}
		return response, nil
	})
}
