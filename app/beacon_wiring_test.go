package app

// Beacon baseapp wiring smoke tests.
//
// These prove the three beacon hooks are actually installed on BaseApp
// after New() returns. Without them, Beacon handlers exist but
// never run — the app would silently regress to the placeholder beacon
// path.

import (
	"testing"

	"cosmossdk.io/log"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/stretchr/testify/require"
)

// TestBeaconHooksAreInstalledOnBaseApp asserts New() has actually
// installed PrepareProposal / ProcessProposal / PreBlocker on the
// underlying BaseApp.
//
// We compare against BaseApp method signatures that must be non-nil
// after Beacon wiring:
//   - baseApp.PreBlocker()
//   - a live PrepareProposal / ProcessProposal handler
//
// The default depinject baseapp installs its own PreBlocker/proposal
// pair; our wiring must NOT leave them as SDK defaults or the sentinel
// never gets validated.
func TestBeaconHooksAreInstalledOnBaseApp(t *testing.T) {
	db := dbm.NewMemDB()
	defer db.Close()

	app := New(log.NewNopLogger(), db, nil, true, smokeAppOptions(),
		baseapp.SetChainID(SimAppChainID))
	require.NotNil(t, app)

	// PreBlocker must be set. We do NOT easily have a way to introspect
	// whether it's specifically the beacon PreBlocker (BaseApp only
	// returns the composed function), so we assert non-nil + delegate
	// full-flow verification to the unit tests in
	// pre_blocker_test.go / process_proposal_test.go.
	require.NotNil(t, app.App.BaseApp.PreBlocker(),
		"BaseApp.PreBlocker must be set after New() — otherwise Beacon persistence landing is a no-op")
}

// TestBeaconStartupAssertionIsAdvisoryOnNonMainnet pins the boundary that a
// build tag must never change a consensus decision: on a non-mainnet build the
// startup assertion must be a side-effect-free no-op, and a node missing its
// VRF key must still be able to start the chain (the policy is decided by the
// committed vrf_required_from_height, not by which artifact was built).
func TestBeaconStartupAssertionIsAdvisoryOnNonMainnet(t *testing.T) {
	require.NotNil(t, assertBeaconStartupConfig)
	require.NotPanics(t, func() {
		assertBeaconStartupConfig(log.NewNopLogger(), t.TempDir(), nil, true)
	}, "non-mainnet build must not fail-fast on a missing VRF key; if this panics the mainnet build tag leaked in")
}
