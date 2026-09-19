package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	tasktypes "github.com/TrueOpen/node/x/task/types"
)

// The threshold is ceil(2N/3) over the *frozen* selected-verifier count. The
// table pins the rounding boundary in both directions because an off-by-one here
// silently changes who can settle a task.
func TestConsensusThresholdIsCeilTwoThirds(t *testing.T) {
	for _, tc := range []struct {
		selected uint32
		want     uint32
	}{
		{selected: 1, want: 1},
		{selected: 2, want: 2},
		{selected: 3, want: 2},
		{selected: 4, want: 3},
		{selected: 5, want: 4},
		{selected: 6, want: 4},
		{selected: 7, want: 5},
		{selected: 9, want: 6},
		{selected: 10, want: 7},
	} {
		got, err := tasktypes.ConsensusThreshold(tc.selected)
		require.NoError(t, err)
		require.Equalf(t, tc.want, got, "selected=%d", tc.selected)
	}

	_, err := tasktypes.ConsensusThreshold(0)
	require.Error(t, err, "a round with no formal verifiers has no threshold")
}

func sample(t *testing.T, index uint32, verdict tasktypes.MetricSampleVerdictV1, count uint64) tasktypes.VerifierSampleV1 {
	t.Helper()
	return tasktypes.VerifierSampleV1{
		SelectedVerifierIndex:   index,
		VerifierOperatorAddress: verificationAddress(t, 0x30+byte(index)),
		CommitKey:               make([]byte, tasktypes.Hash32Len),
		ResultReceiptDigest:     make([]byte, tasktypes.Hash32Len),
		MetricSummaryHash:       make([]byte, tasktypes.Hash32Len),
		SampleVerdict:           verdict,
		AcceptedHeight:          10,
		GeneratedTokenCount:     count,
	}
}

func TestAggregateVerifierConsensusClustersOnTheCompleteKey(t *testing.T) {
	pass := tasktypes.MetricSampleVerdictV1_METRIC_SAMPLE_VERDICT_V1_PASS
	reject := tasktypes.MetricSampleVerdictV1_METRIC_SAMPLE_VERDICT_V1_REJECT
	inconclusive := tasktypes.MetricSampleVerdictV1_METRIC_SAMPLE_VERDICT_V1_INCONCLUSIVE

	t.Run("pass cluster freezes its own count", func(t *testing.T) {
		got, err := tasktypes.AggregateVerifierConsensus(3, []tasktypes.VerifierSampleV1{
			sample(t, 0, pass, 321), sample(t, 1, pass, 321), sample(t, 2, reject, 999),
		})
		require.NoError(t, err)
		require.Equal(t, tasktypes.TaskVerdict_TASK_VERDICT_PASS, got.Verdict)
		require.Equal(t, tasktypes.TaskFailureClass_TASK_FAILURE_CLASS_NONE, got.FailureClass)
		require.Equal(t, uint64(321), got.GeneratedTokenCount)
		require.Len(t, got.Members, 2)
		require.Equal(t, uint32(0), got.Members[0].SelectedVerifierIndex)
		require.Equal(t, uint32(1), got.Members[1].SelectedVerifierIndex)
	})

	t.Run("same verdict different count is not one cluster", func(t *testing.T) {
		// Three PASS samples, but the counts disagree, so no complete key reaches
		// the threshold of 2. This is the rule that stops an attacker from
		// splitting the billed work unit while still showing a PASS majority.
		got, err := tasktypes.AggregateVerifierConsensus(3, []tasktypes.VerifierSampleV1{
			sample(t, 0, pass, 100), sample(t, 1, pass, 200), sample(t, 2, pass, 300),
		})
		require.NoError(t, err)
		require.Equal(t, tasktypes.TaskVerdict_TASK_VERDICT_NO_CONSENSUS, got.Verdict)
		require.Equal(t, tasktypes.TaskFailureClass_TASK_FAILURE_CLASS_OBJECTIVE_FAULT, got.FailureClass)
		require.Zero(t, got.GeneratedTokenCount)
		require.Empty(t, got.Members)
	})

	t.Run("reject cluster is a metric threshold breach", func(t *testing.T) {
		got, err := tasktypes.AggregateVerifierConsensus(3, []tasktypes.VerifierSampleV1{
			sample(t, 0, reject, 7), sample(t, 1, reject, 7),
		})
		require.NoError(t, err)
		require.Equal(t, tasktypes.TaskVerdict_TASK_VERDICT_FAIL, got.Verdict)
		require.Equal(t, tasktypes.TaskFailureClass_TASK_FAILURE_CLASS_METRIC_THRESHOLD_BREACH, got.FailureClass)
		require.Equal(t, uint64(7), got.GeneratedTokenCount)
	})

	t.Run("inconclusive never becomes a task verdict", func(t *testing.T) {
		got, err := tasktypes.AggregateVerifierConsensus(3, []tasktypes.VerifierSampleV1{
			sample(t, 0, inconclusive, 5), sample(t, 1, inconclusive, 5), sample(t, 2, inconclusive, 5),
		})
		require.NoError(t, err)
		require.Equal(t, tasktypes.TaskVerdict_TASK_VERDICT_NO_CONSENSUS, got.Verdict)
		require.Empty(t, got.Members)
	})

	t.Run("too few reveals is insufficient verifier not no-consensus", func(t *testing.T) {
		got, err := tasktypes.AggregateVerifierConsensus(3, []tasktypes.VerifierSampleV1{sample(t, 0, pass, 11)})
		require.NoError(t, err)
		require.Equal(t, tasktypes.TaskVerdict_TASK_VERDICT_VERIFY_FAILED, got.Verdict)
		require.Equal(t, tasktypes.TaskFailureClass_TASK_FAILURE_CLASS_INSUFFICIENT_VERIFIER, got.FailureClass)
	})

	t.Run("rejects malformed slates before judging", func(t *testing.T) {
		_, err := tasktypes.AggregateVerifierConsensus(2, []tasktypes.VerifierSampleV1{
			sample(t, 0, pass, 1), sample(t, 0, pass, 1),
		})
		require.ErrorContains(t, err, "appears twice")

		_, err = tasktypes.AggregateVerifierConsensus(2, []tasktypes.VerifierSampleV1{sample(t, 5, pass, 1)})
		require.ErrorContains(t, err, "outside the frozen set")

		_, err = tasktypes.AggregateVerifierConsensus(1, []tasktypes.VerifierSampleV1{
			sample(t, 0, tasktypes.MetricSampleVerdictV1_METRIC_SAMPLE_VERDICT_V1_UNSPECIFIED, 1),
		})
		require.ErrorContains(t, err, "no judged sample verdict")
	})
}
