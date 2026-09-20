package app

// Pins the complete split invariant registries so checks cannot be silently dropped.

import (
	"testing"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	hubkeeper "github.com/TrueOpen/node/x/hub/keeper"
	taskkeeper "github.com/TrueOpen/node/x/task/keeper"
)

func TestInvariantRegistryIsCompleteAndUnbypassable(t *testing.T) {
	app := bootAppMinimal(t)
	require.NotNil(t, app.trueopenInvariants)
	require.Len(t, app.trueopenInvariants.routes,
		len(app.HubKeeper.InvariantChecks())+len(app.TaskKeeper.InvariantChecks()))
	require.Equal(t, len(app.HubKeeper.InvariantChecks()),
		app.trueopenInvariants.countModule("hub"))
	require.Equal(t, len(app.TaskKeeper.InvariantChecks()),
		app.trueopenInvariants.countModule("task"))

	hubNames := map[string]bool{}
	for _, check := range app.HubKeeper.InvariantChecks() {
		require.NotNil(t, check.Check)
		require.False(t, hubNames[check.Name])
		hubNames[check.Name] = true
	}
	// Hub no longer registers a store-schema invariant: fresh genesis removed the
	// Hub StateVersion/StoreMigrations collections entirely (the node context document).
	// The cross-index invariants below are part of the current schema contract.
	for _, name := range []string{
		hubkeeper.InvariantRewardsEarnings,
		hubkeeper.InvariantServiceBond,
		hubkeeper.InvariantSupportAggregates, hubkeeper.InvariantTaskLiabilityIndexes,
		hubkeeper.InvariantCurrentServiceAddressIdx,
	} {
		require.Truef(t, hubNames[name], "missing hub invariant %s", name)
	}

	taskNames := map[string]bool{}
	for _, check := range app.TaskKeeper.InvariantChecks() {
		require.NotNil(t, check.Check)
		require.False(t, taskNames[check.Name])
		taskNames[check.Name] = true
	}
	// The task "store_schema_current" invariant is gone with
	// the StateVersion / StoreMigrations wire (fresh genesis, the node context document
	// §1.2 has no schema history to invariant-check). Re-add it here only if a
	// migration surface is ever reintroduced.
	for _, name := range []string{taskkeeper.InvariantEscrowReserved} {
		require.Truef(t, taskNames[name], "missing task invariant %s", name)
	}
}

func TestInvariantsHoldAfterBlockProductionAndMsgs(t *testing.T) {
	app := bootAppMinimal(t)
	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: app.LastBlockHeight() + 1})
	assertSplitInvariants(t, app, ctx)
}
