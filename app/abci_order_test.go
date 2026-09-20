package app

import (
	"reflect"
	"testing"
)

// TestBeginBlockOrderFrozen asserts the app-level BeginBlocker order matches
// TrueOpen_Node_Spec.md §14.4.
// Changing this order is a cross-boundary breaking change; the intent is that
// any accidental reordering trips the test before the diff can merge.
func TestBeginBlockOrderFrozen(t *testing.T) {
	want := []string{
		"distribution",
		"slashing",
		"staking",
		"hub",
		"task",
	}
	if !reflect.DeepEqual(BeginBlockOrder, want) {
		t.Fatalf("BeginBlockOrder = %v, want %v", BeginBlockOrder, want)
	}
}

// TestEndBlockOrderFrozen mirrors TestBeginBlockOrderFrozen for EndBlockers.
// Note that distribution is intentionally absent from EndBlock; per §14.4 the
// EndBlock chain is gov -> staking -> hub -> task.
//
// gov leads the chain (SDK convention): its EndBlocker only tallies proposals
// and executes the messages of passed proposals (e.g. MsgUpdateHubParams /
// task MsgUpdateParams). Running it first means a param change lands before
// staking and the business modules read state in the same block, and it keeps
// gov from ever sitting *between* staking and those modules — the property the
// scoreboard-ordering test locks in.
func TestEndBlockOrderFrozen(t *testing.T) {
	want := []string{
		"gov",
		"staking",
		"hub",
		"task",
	}
	if !reflect.DeepEqual(EndBlockOrder, want) {
		t.Fatalf("EndBlockOrder = %v, want %v", EndBlockOrder, want)
	}
}

// TestTrueOpenIsLastBlocker asserts the trueopen modules run last in both BeginBlock
// and EndBlock. This ordering matters because BeginBlock deadline sweep and
// EndBlock treasury rate adjustment both read validator / distribution state
// that must have settled by the time the trueopen modules run. A regression that
// inserts a module after the trueopen modules risks reading stale state. After the
// module split, task runs last (it depends on hub-settled state).
func TestTrueOpenIsLastBlocker(t *testing.T) {
	if got := BeginBlockOrder[len(BeginBlockOrder)-1]; got != "task" {
		t.Fatalf("BeginBlockOrder last = %q, want %q", got, "task")
	}
	if got := EndBlockOrder[len(EndBlockOrder)-1]; got != "task" {
		t.Fatalf("EndBlockOrder last = %q, want %q", got, "task")
	}
}
