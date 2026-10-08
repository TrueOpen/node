package module

import (
	"errors"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
)

type recordingMigrationRegistrar struct {
	calls []migrationCall
	err   error
}

type migrationCall struct {
	moduleName  string
	fromVersion uint64
}

func (r *recordingMigrationRegistrar) RegisterMigration(moduleName string, fromVersion uint64, _ module.MigrationHandler) error {
	if r.err != nil {
		return r.err
	}
	r.calls = append(r.calls, migrationCall{moduleName: moduleName, fromVersion: fromVersion})
	return nil
}

// A ConsensusVersion bump without a migration would make RunMigrations fail at
// the upgrade height, so the registered set must cover exactly
// [genesisConsensusVersion, ConsensusVersion).
func TestMigrations_CoverConsensusVersionRange(t *testing.T) {
	require.Equal(t, uint64(2), genesisConsensusVersion)
	require.GreaterOrEqual(t, (AppModule{}).ConsensusVersion(), genesisConsensusVersion)

	want := make(map[uint64]struct{})
	for from := genesisConsensusVersion; from < (AppModule{}).ConsensusVersion(); from++ {
		want[from] = struct{}{}
	}
	got := make(map[uint64]struct{})
	for from := range migrations(keeper.Keeper{}) {
		got[from] = struct{}{}
	}
	require.Equal(t, want, got)
}

func TestRegisterMigrations(t *testing.T) {
	handler := func(sdk.Context) error { return nil }
	handlers := map[uint64]module.MigrationHandler{
		3: handler,
		2: handler,
	}

	t.Run("registers every handler in ascending order", func(t *testing.T) {
		registrar := &recordingMigrationRegistrar{}
		require.NoError(t, registerMigrations(registrar, handlers))
		require.Equal(t, []migrationCall{
			{moduleName: types.ModuleName, fromVersion: 2},
			{moduleName: types.ModuleName, fromVersion: 3},
		}, registrar.calls)
	})

	t.Run("propagates a registration error", func(t *testing.T) {
		registrar := &recordingMigrationRegistrar{err: errors.New("duplicate migration")}
		require.ErrorContains(t, registerMigrations(registrar, handlers), "duplicate migration")
	})

	t.Run("no handlers registers nothing", func(t *testing.T) {
		registrar := &recordingMigrationRegistrar{}
		require.NoError(t, registerMigrations(registrar, nil))
		require.Empty(t, registrar.calls)
	})
}
