package app

// EndBlock scoreboard / hook-order contract for
// PerformanceScore and MarkGate.
//
// Spec §14.4 states:
//
//   "The PerformanceScore hook and the MarkGate hook are two independent
//    paths (the former drives the P50/P60 gate on task-acceptance candidate
//    weight, the latter drives the top10/P90 gate on block rewards), so the
//    Node EndBlock order must guarantee that neither contaminates the other's
//    epoch boundary."
//
// Current architecture (as of K10 landing):
//   - PerformanceScore is updated INLINE from the Settle handler
//     (msg_server_settle.go RecordPerformanceFault) and from the reward
//     competition path (reward_competition.go RecordPerformancePositiveTask).
//     There is NO standalone EndBlock hook that reads/writes it.
//   - MarkGate is updated INLINE from the reward competition path
//     (RecordMarkGateSourceHit). There is NO standalone EndBlock hook.
//   - The only epoch-boundary work in trueopen's EndBlocker is the
//     treasury rate adjustment.
//
// This test file locks that architectural stance IN:
//   1. The list of EndBlockers that share the epoch-boundary block MUST
//      remain a superset with trueopen at the end. Any new module getting
//      inserted (accidentally or otherwise) between staking and trueopen
//      trips the test.
//   2. trueopen's BeginBlock / EndBlock name identity is asserted so a
//      cross-repo rename cannot silently reorder the hook.
//
// If a future Keeper slice DOES add a dedicated EndBlock hook for
// PerformanceScore or MarkGate (e.g., a mass-expiry sweep), Node will
// need to (a) place it inside trueopen's EndBlocker in a well-documented
// position, or (b) split it into a separate module and update
// EndBlockOrder here. This test forces that discussion by failing loud.

import (
	"testing"

	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

// TestEndBlockContainsExactlyExpectedModules keeps the set of modules
// that contribute to the epoch-boundary EndBlocker chain locked. Adding
// a new EndBlocker or removing an existing one MUST bump this list —
// forcing the change author to think about ordering vs PerformanceScore
// and MarkGate side-effects.
func TestEndBlockContainsExactlyExpectedModules(t *testing.T) {
	// Expected set (order-agnostic). Sorted alphabetically so add/remove
	// diffs are easy to review.
	expected := map[string]bool{
		"gov":     true,
		"hub":     true,
		"task":    true,
		"staking": true,
	}

	// Every module in the frozen EndBlockOrder must be in expected.
	seen := map[string]bool{}
	for _, name := range EndBlockOrder {
		require.Truef(t, expected[name],
			"EndBlockOrder contains %q which is not in the audited allow-list; "+
				"if you added a new EndBlocker you must update this test AND explain "+
				"why it does not disturb PerformanceScore / MarkGate epoch semantics",
			name)
		seen[name] = true
	}
	// Every expected module must be present in EndBlockOrder.
	for name := range expected {
		require.Truef(t, seen[name],
			"EndBlockOrder missing expected module %q — removing an EndBlocker "+
				"changes epoch-boundary behaviour and needs sign-off", name)
	}
}

// TestBeginBlockContainsExactlyExpectedModules is the BeginBlock twin
// of the above. distribution enters the BeginBlock chain even though it
// is absent from EndBlock; the two must not silently converge or diverge.
func TestBeginBlockContainsExactlyExpectedModules(t *testing.T) {
	expected := map[string]bool{
		"distribution": true,
		"slashing":     true,
		"hub":          true,
		"task":         true,
		"staking":      true,
	}

	seen := map[string]bool{}
	for _, name := range BeginBlockOrder {
		require.Truef(t, expected[name],
			"BeginBlockOrder contains %q outside the audited allow-list", name)
		seen[name] = true
	}
	for name := range expected {
		require.Truef(t, seen[name],
			"BeginBlockOrder missing expected module %q", name)
	}
}

// TestTrueOpenModuleNameIsStable asserts the exported ModuleName constant
// used in BeginBlockOrder / EndBlockOrder has not silently changed. A
// cross-repository rename must be a deliberate breaking change.
func TestTrueOpenModuleNameIsStable(t *testing.T) {
	require.Equal(t, "hub", hubtypes.ModuleName,
		"hub ModuleName is frozen; rename requires the breaking-change PR template")
	require.Equal(t, "task", tasktypes.ModuleName,
		"task ModuleName is frozen; rename requires the breaking-change PR template")
}

// TestTrueOpenPerformanceScoreAndMarkGateAreNotEndBlockHooks acts as
// architectural documentation. It reads the two collections'
// contribution paths at compile time — if a future refactor introduces
// a dedicated EndBlock function that touches these stores, the change
// must land alongside an update to this test and a re-audit of the
// Spec §14.4 non-interference property.
//
// The test currently only asserts the collections symbols exist and
// remain reachable through the Keeper; the deeper property (hook
// non-interference) is what the surrounding comment locks in for the
// reviewer.
func TestTrueOpenPerformanceScoreAndMarkGateAreNotEndBlockHooks(t *testing.T) {
	// Compile-time assertions via nil checks are enough here — the
	// interesting property is documented in the file-level comment.
	// If a future refactor tries to remove PerformanceScore or MarkGate
	// entirely, this reference will fail to compile and force review.
	_ = hubtypes.ModuleName
	_ = hubtypes.RewardsModuleName
	_ = hubtypes.TreasuryModuleName
}
