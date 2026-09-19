package keeper

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func TestPreVerificationFailureCommitmentsAreDeterministic(t *testing.T) {
	taskID := bytes.Repeat([]byte{0x61}, types.Hash32Len)
	infer := bytes.Repeat([]byte{0x62}, types.Hash32Len)
	settlement := bytes.Repeat([]byte{0x63}, types.Hash32Len)
	first, err := preVerificationEvidenceDigest(
		"failure-presence", taskID,
		types.TaskFailureClass_TASK_FAILURE_CLASS_INSUFFICIENT_VERIFIER,
		shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_DEADLINE,
		10, infer, settlement,
	)
	require.NoError(t, err)
	second, err := preVerificationEvidenceDigest(
		"failure-presence", taskID,
		types.TaskFailureClass_TASK_FAILURE_CLASS_INSUFFICIENT_VERIFIER,
		shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_DEADLINE,
		10, infer, settlement,
	)
	require.NoError(t, err)
	require.Equal(t, first, second)

	mutated := append([]byte(nil), infer...)
	mutated[0] ^= 1
	changed, err := preVerificationEvidenceDigest(
		"failure-presence", taskID,
		types.TaskFailureClass_TASK_FAILURE_CLASS_INSUFFICIENT_VERIFIER,
		shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_DEADLINE,
		10, mutated, settlement,
	)
	require.NoError(t, err)
	require.NotEqual(t, first, changed)
}

func TestPreVerificationVerdictUsesDeadlineKind(t *testing.T) {
	tests := map[types.DeadlineKindV1]types.TaskVerdict{
		types.DeadlineKindV1_DEADLINE_KIND_V1_WORKER_ASSIGNMENT: types.TaskVerdict_TASK_VERDICT_ASSIGN_TIMEOUT,
		types.DeadlineKindV1_DEADLINE_KIND_V1_WORKER_INFER:      types.TaskVerdict_TASK_VERDICT_WORKER_TIMEOUT,
		types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_OPEN:       types.TaskVerdict_TASK_VERDICT_VERIFY_UNAVAILABLE,
	}
	for kind, want := range tests {
		got, err := preVerificationVerdict(kind)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
}
