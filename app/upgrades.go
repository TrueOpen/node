package app

import (
	"context"
	"fmt"

	storetypes "cosmossdk.io/store/types"
	upgradetypes "cosmossdk.io/x/upgrade/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
)

// upgrade describes one in-place chain upgrade. It is activated by a passed
// governance proposal whose plan carries the same name; at the plan height the
// old binary halts and the new binary runs the handler before any other module
// touches state.
type upgrade struct {
	name string
	// storeUpgrades lists store keys added, renamed or deleted by this upgrade,
	// and is nil when no store key changes. A new key prefix inside an existing
	// module store does not need an entry.
	storeUpgrades *storetypes.StoreUpgrades
	// afterMigrations runs once the module migrations have succeeded, for work
	// that is not a per-module migration (for example writing the default of a
	// parameter added by this upgrade). It may be nil.
	afterMigrations func(ctx sdk.Context) error
}

// plannedUpgrades is the upgrade registry of this binary. It is empty until the
// first post-genesis change: a state-breaking change must add its entry here,
// bump the ConsensusVersion of every module it touches and register the
// matching module migration.
func (app *App) plannedUpgrades() []upgrade {
	return nil
}

// validateUpgrades rejects registry entries that would make two plans
// indistinguishable or leave a plan name that governance cannot propose.
func validateUpgrades(upgrades []upgrade) error {
	seen := make(map[string]struct{}, len(upgrades))
	for _, u := range upgrades {
		if u.name == "" {
			return fmt.Errorf("upgrade name must not be empty")
		}
		if _, dup := seen[u.name]; dup {
			return fmt.Errorf("duplicate upgrade name %q", u.name)
		}
		seen[u.name] = struct{}{}
	}
	return nil
}

// registerUpgrades installs the handler and, when the upgrade adds store keys,
// the store loader for every entry of upgrades. It must run before the latest
// version is loaded, because the store loader is consulted by that load.
func (app *App) registerUpgrades(upgrades []upgrade) error {
	if err := validateUpgrades(upgrades); err != nil {
		return err
	}
	for _, u := range upgrades {
		app.UpgradeKeeper.SetUpgradeHandler(u.name, app.upgradeHandler(u))
	}

	// The old binary writes the plan to disk when it halts. The store key set
	// has to change while the store is opened, which is earlier than any
	// handler can run, so the loader is chosen from that file.
	info, err := app.UpgradeKeeper.ReadUpgradeInfoFromDisk()
	if err != nil {
		return fmt.Errorf("read upgrade info: %w", err)
	}
	if info.Name == "" || app.UpgradeKeeper.IsSkipHeight(info.Height) {
		return nil
	}
	for _, u := range upgrades {
		if u.name == info.Name && u.storeUpgrades != nil {
			app.SetStoreLoader(upgradetypes.UpgradeStoreLoader(info.Height, u.storeUpgrades))
		}
	}
	return nil
}

func (app *App) upgradeHandler(u upgrade) upgradetypes.UpgradeHandler {
	return func(ctx context.Context, _ upgradetypes.Plan, fromVM module.VersionMap) (module.VersionMap, error) {
		toVM, err := app.ModuleManager.RunMigrations(ctx, app.Configurator(), fromVM)
		if err != nil {
			return nil, fmt.Errorf("upgrade %q: run module migrations: %w", u.name, err)
		}
		if u.afterMigrations != nil {
			if err := u.afterMigrations(sdk.UnwrapSDKContext(ctx)); err != nil {
				return nil, fmt.Errorf("upgrade %q: post-migration step: %w", u.name, err)
			}
		}
		return toVM, nil
	}
}
