package types

import shared "github.com/TrueOpen/node/x/shared/types"

// IsValueEvidenceFault identifies a fault whose original proof is a value
// receipt, so that receipt must retain the original digest and height.
func IsValueEvidenceFault(fault RoleFaultState) bool {
	return fault.ClassificationSource == shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_OBJECTIVE_EVIDENCE &&
		(fault.Duty == shared.DutyWorker && fault.FaultClass == shared.FaultKind_FAULT_KIND_WORKER_VALUES_INVALID || fault.Duty == shared.DutyVerifier && fault.FaultClass == shared.FaultKind_FAULT_KIND_VERIFIER_VALUE_MISMATCH)
}

// IsMergedValueEvidenceFault identifies the fault families sharing one
// Task/duty/operator consequence. Callers independently check that exact scope.
func IsMergedValueEvidenceFault(fault RoleFaultState) bool {
	if IsValueEvidenceFault(fault) {
		return true
	}
	if fault.Duty != shared.DutyWorker && fault.Duty != shared.DutyVerifier {
		return false
	}
	return fault.FaultClass == shared.FaultKind_FAULT_KIND_EQUIVOCATION && fault.ClassificationSource == shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_OBJECTIVE_EVIDENCE ||
		fault.FaultClass == shared.FaultKind_FAULT_KIND_INVALID_RESULT && fault.ClassificationSource == shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_VERIFICATION_ROUND
}
