package module

import (
	"fmt"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"sort"

	"github.com/cosmos/cosmos-sdk/types/module"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
)

// genesisConsensusVersion is the ConsensusVersion the module shipped with at
// genesis. A state-breaking change must bump ConsensusVersion and add the
// matching entry to migrations(): the entry keyed N moves state from version N
// to N+1, and runs at the upgrade height when x/upgrade calls RunMigrations.
const genesisConsensusVersion uint64 = 2

// migrationRegistrar is the part of module.Configurator the module needs. The
// Configurator reaches RegisterServices as its grpc.ServiceRegistrar.
type migrationRegistrar interface {
	RegisterMigration(moduleName string, fromVersion uint64, handler module.MigrationHandler) error
}

// The SDK Configurator reaches RegisterServices as its grpc.ServiceRegistrar,
// so it must keep satisfying migrationRegistrar or migrations silently stop
// being registered.
var _ migrationRegistrar = module.Configurator(nil)

// migrations lists every in-place state migration of this module, keyed by the
// version it migrates from. It is empty until the first post-genesis change.
func migrations(_ keeper.Keeper) map[uint64]module.MigrationHandler {
	return map[uint64]module.MigrationHandler{
		// 2 -> 3 (ADR-0040 anchor audit): anchor_reject / anchor_timeout role
		// faults and absolute anchor slashes are new behavior with no stored
		// state to rewrite.
		2: func(sdk.Context) error { return nil },
		// 3 -> 4 (ADR-0040 v7): the anchor_content_reject role fault (jailed,
		// absolute slash, also after the reservation was released) is new
		// behavior with no stored state to rewrite.
		3: func(sdk.Context) error { return nil },
	}
}

func registerMigrations(registrar migrationRegistrar, handlers map[uint64]module.MigrationHandler) error {
	from := make([]uint64, 0, len(handlers))
	for version := range handlers {
		from = append(from, version)
	}
	sort.Slice(from, func(i, j int) bool { return from[i] < from[j] })
	for _, version := range from {
		if err := registrar.RegisterMigration(types.ModuleName, version, handlers[version]); err != nil {
			return fmt.Errorf("register %s migration from version %d: %w", types.ModuleName, version, err)
		}
	}
	return nil
}
