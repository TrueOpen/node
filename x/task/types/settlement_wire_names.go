package types

import "fmt"

// The two tables below are the frozen wire names of TaskVerdict and
// TaskFailureClass as they enter TRUEOPEN_HUB_SETTLEMENT_INTENT_V1 through
// shared.SettlementResult.verdict / .failure_class.
//
// They exist because settlement_apply.go used to fill those two fields with
// Verdict.String() and FailureClass.String(). Those are gogoproto-generated
// names: renaming a value in proto/task/v1/settlement.proto -- a change no
// reviewer would read as consensus-affecting, and one buf breaking-change
// detection does not flag, since the number is what the wire format protects --
// would silently change the settlement intent digest on every node.
//
// The strings are deliberately identical to today's generated names, so this
// change alters no digest. What it changes is where the value comes from: a
// constant in this file, which cannot move unless someone edits this file.
//
// Adding an enum value requires adding it here too; the mapping functions
// return an error rather than a zero value so a missing entry fails the
// settlement instead of hashing "".

const (
	TaskVerdictWireUnspecified       = "TASK_VERDICT_UNSPECIFIED"
	TaskVerdictWirePass              = "TASK_VERDICT_PASS"
	TaskVerdictWireFail              = "TASK_VERDICT_FAIL"
	TaskVerdictWireNoConsensus       = "TASK_VERDICT_NO_CONSENSUS"
	TaskVerdictWireAssignTimeout     = "TASK_VERDICT_ASSIGN_TIMEOUT"
	TaskVerdictWireWorkerTimeout     = "TASK_VERDICT_WORKER_TIMEOUT"
	TaskVerdictWireVerifyFailed      = "TASK_VERDICT_VERIFY_FAILED"
	TaskVerdictWireSettlementTimeout = "TASK_VERDICT_SETTLEMENT_TIMEOUT"
	TaskVerdictWireVerifyUnavailable = "TASK_VERDICT_VERIFY_UNAVAILABLE"
)

const (
	TaskFailureClassWireUnspecified           = "TASK_FAILURE_CLASS_UNSPECIFIED"
	TaskFailureClassWireNone                  = "TASK_FAILURE_CLASS_NONE"
	TaskFailureClassWireInsufficientVerifier  = "TASK_FAILURE_CLASS_INSUFFICIENT_VERIFIER"
	TaskFailureClassWireMetricThresholdBreach = "TASK_FAILURE_CLASS_METRIC_THRESHOLD_BREACH"
	TaskFailureClassWireObjectiveFault        = "TASK_FAILURE_CLASS_OBJECTIVE_FAULT"
	TaskFailureClassWireSchemaFault           = "TASK_FAILURE_CLASS_SCHEMA_FAULT"
	TaskFailureClassWireWorkerEvidenceFault   = "TASK_FAILURE_CLASS_WORKER_EVIDENCE_FAULT"
)

// TaskVerdictWireName returns the frozen wire name of a task verdict.
func TaskVerdictWireName(verdict TaskVerdict) (string, error) {
	switch verdict {
	case TaskVerdict_TASK_VERDICT_UNSPECIFIED:
		return TaskVerdictWireUnspecified, nil
	case TaskVerdict_TASK_VERDICT_PASS:
		return TaskVerdictWirePass, nil
	case TaskVerdict_TASK_VERDICT_FAIL:
		return TaskVerdictWireFail, nil
	case TaskVerdict_TASK_VERDICT_NO_CONSENSUS:
		return TaskVerdictWireNoConsensus, nil
	case TaskVerdict_TASK_VERDICT_ASSIGN_TIMEOUT:
		return TaskVerdictWireAssignTimeout, nil
	case TaskVerdict_TASK_VERDICT_WORKER_TIMEOUT:
		return TaskVerdictWireWorkerTimeout, nil
	case TaskVerdict_TASK_VERDICT_VERIFY_FAILED:
		return TaskVerdictWireVerifyFailed, nil
	case TaskVerdict_TASK_VERDICT_SETTLEMENT_TIMEOUT:
		return TaskVerdictWireSettlementTimeout, nil
	case TaskVerdict_TASK_VERDICT_VERIFY_UNAVAILABLE:
		return TaskVerdictWireVerifyUnavailable, nil
	default:
		return "", fmt.Errorf("task verdict %d has no frozen wire name", int32(verdict))
	}
}

// TaskFailureClassWireName returns the frozen wire name of a task failure class.
func TaskFailureClassWireName(class TaskFailureClass) (string, error) {
	switch class {
	case TaskFailureClass_TASK_FAILURE_CLASS_UNSPECIFIED:
		return TaskFailureClassWireUnspecified, nil
	case TaskFailureClass_TASK_FAILURE_CLASS_NONE:
		return TaskFailureClassWireNone, nil
	case TaskFailureClass_TASK_FAILURE_CLASS_INSUFFICIENT_VERIFIER:
		return TaskFailureClassWireInsufficientVerifier, nil
	case TaskFailureClass_TASK_FAILURE_CLASS_METRIC_THRESHOLD_BREACH:
		return TaskFailureClassWireMetricThresholdBreach, nil
	case TaskFailureClass_TASK_FAILURE_CLASS_OBJECTIVE_FAULT:
		return TaskFailureClassWireObjectiveFault, nil
	case TaskFailureClass_TASK_FAILURE_CLASS_SCHEMA_FAULT:
		return TaskFailureClassWireSchemaFault, nil
	case TaskFailureClass_TASK_FAILURE_CLASS_WORKER_EVIDENCE_FAULT:
		return TaskFailureClassWireWorkerEvidenceFault, nil
	default:
		return "", fmt.Errorf("task failure class %d has no frozen wire name", int32(class))
	}
}
