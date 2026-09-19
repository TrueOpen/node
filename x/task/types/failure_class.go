package types

// FreezeSignalEligibilityForTaskFailureClass derives the stored
// freeze_signal_eligible bit from the closed V1 failure taxonomy. The second
// result is false for UNSPECIFIED and unknown numeric values so callers can
// distinguish a legitimate non-counting class from corrupted state.
func FreezeSignalEligibilityForTaskFailureClass(class TaskFailureClass) (eligible bool, known bool) {
	switch class {
	case TaskFailureClass_TASK_FAILURE_CLASS_NONE,
		TaskFailureClass_TASK_FAILURE_CLASS_INSUFFICIENT_VERIFIER:
		return false, true
	case TaskFailureClass_TASK_FAILURE_CLASS_METRIC_THRESHOLD_BREACH,
		TaskFailureClass_TASK_FAILURE_CLASS_OBJECTIVE_FAULT,
		TaskFailureClass_TASK_FAILURE_CLASS_SCHEMA_FAULT,
		TaskFailureClass_TASK_FAILURE_CLASS_WORKER_EVIDENCE_FAULT:
		return true, true
	default:
		return false, false
	}
}
