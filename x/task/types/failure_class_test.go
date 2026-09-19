package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/task/types"
)

func TestFreezeSignalEligibilityForTaskFailureClass(t *testing.T) {
	for _, class := range []types.TaskFailureClass{
		types.TaskFailureClass_TASK_FAILURE_CLASS_NONE,
		types.TaskFailureClass_TASK_FAILURE_CLASS_INSUFFICIENT_VERIFIER,
	} {
		eligible, known := types.FreezeSignalEligibilityForTaskFailureClass(class)
		require.True(t, known)
		require.False(t, eligible)
	}
	for _, class := range []types.TaskFailureClass{
		types.TaskFailureClass_TASK_FAILURE_CLASS_METRIC_THRESHOLD_BREACH,
		types.TaskFailureClass_TASK_FAILURE_CLASS_OBJECTIVE_FAULT,
		types.TaskFailureClass_TASK_FAILURE_CLASS_SCHEMA_FAULT,
		types.TaskFailureClass_TASK_FAILURE_CLASS_WORKER_EVIDENCE_FAULT,
	} {
		eligible, known := types.FreezeSignalEligibilityForTaskFailureClass(class)
		require.True(t, known)
		require.True(t, eligible)
	}
	for _, class := range []types.TaskFailureClass{
		types.TaskFailureClass_TASK_FAILURE_CLASS_UNSPECIFIED,
		types.TaskFailureClass(99),
	} {
		eligible, known := types.FreezeSignalEligibilityForTaskFailureClass(class)
		require.False(t, known)
		require.False(t, eligible)
	}
}
