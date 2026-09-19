package types

import "testing"

// TestTaskVerdictWireNamesAreExhaustive fails when a value is added to
// TaskVerdict in proto/task/v1/settlement.proto without a frozen wire name.
// Without this, a new value would reach TRUEOPEN_HUB_SETTLEMENT_INTENT_V1 as an
// error at settlement time rather than at build time.
func TestTaskVerdictWireNamesAreExhaustive(t *testing.T) {
	for value := range TaskVerdict_name {
		if _, err := TaskVerdictWireName(TaskVerdict(value)); err != nil {
			t.Fatalf("TaskVerdict %d (%s) has no frozen wire name: %v", value, TaskVerdict_name[value], err)
		}
	}
}

func TestTaskFailureClassWireNamesAreExhaustive(t *testing.T) {
	for value := range TaskFailureClass_name {
		if _, err := TaskFailureClassWireName(TaskFailureClass(value)); err != nil {
			t.Fatalf("TaskFailureClass %d (%s) has no frozen wire name: %v", value, TaskFailureClass_name[value], err)
		}
	}
}

// TestSettlementWireNamesAreFrozenNotGenerated is the reason the two tables
// exist. Today every frozen name equals the generated name, so the check reads
// as a tautology -- it is not. The generated side is regenerated from the
// .proto on every buf run; the frozen side is a constant. If a rename ever
// makes these disagree, this test says so, and the frozen value is the one that
// consensus keeps using.
func TestSettlementWireNamesAreFrozenNotGenerated(t *testing.T) {
	for value, generated := range TaskVerdict_name {
		frozen, err := TaskVerdictWireName(TaskVerdict(value))
		if err != nil {
			t.Fatalf("TaskVerdict %d: %v", value, err)
		}
		if frozen != generated {
			t.Errorf("TaskVerdict %d frozen wire name %q no longer matches generated name %q; "+
				"the digest keeps the frozen name -- update the .proto or this test, not the constant", value, frozen, generated)
		}
	}
	for value, generated := range TaskFailureClass_name {
		frozen, err := TaskFailureClassWireName(TaskFailureClass(value))
		if err != nil {
			t.Fatalf("TaskFailureClass %d: %v", value, err)
		}
		if frozen != generated {
			t.Errorf("TaskFailureClass %d frozen wire name %q no longer matches generated name %q; "+
				"the digest keeps the frozen name -- update the .proto or this test, not the constant", value, frozen, generated)
		}
	}
}
