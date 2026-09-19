package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func metricJudgmentFixture() (types.MetricSummaryV1, shared.MetricSpec, shared.VerificationThresholds) {
	summary := types.MetricSummaryV1{
		FiniteCount: 100, MissingComparedCount: 1,
		MeanAbsLogprobDiffFp_1E6: 100, AbsLogprobDiffP95Fp_1E6: 200, AbsLogprobDiffP99Fp_1E6: 300,
		RankDeltaNonzeroRateFp_1E6: 400,
		XTopkJaccardMeanFp_1E6:     &types.MetricSummaryV1_TopkJaccardMeanFp_1E6{TopkJaccardMeanFp_1E6: 900_000},
		XUnionJsP99Fp_1E6:          &types.MetricSummaryV1_UnionJsP99Fp_1E6{UnionJsP99Fp_1E6: 1_000},
		ComparedTopkCount:          100, ComparedRankCount: 100,
	}
	spec := shared.MetricSpec{
		CompareLogprobDiff: true, CompareRankDelta: true, CompareTopkJaccard: true, CompareUnionJs: true,
		ComparedTopK: 10, NumericScale: shared.NumericScale_NUMERIC_SCALE_FP_1E6,
	}
	thresholds := shared.VerificationThresholds{
		PassMinFiniteCount: 80, PassMaxMissingComparedCount: 2,
		PassMeanAbsLogprobDiffMax: 200, PassAbsLogprobDiffP95Max: 300, PassAbsLogprobDiffP99Max: 400,
		PassRankDeltaNonzeroRateMax: 500, PassTopkJaccardMeanMin: 800_000, PassUnionJsP99Max: 2_000,
		RejectMeanAbsLogprobDiffMin: 1_000, RejectAbsLogprobDiffP95Min: 1_100, RejectAbsLogprobDiffP99Min: 1_200,
		RejectRankDeltaNonzeroRateMin: 1_500, RejectTopkJaccardMeanMax: 700_000, RejectUnionJsP99Min: 3_000,
	}
	return summary, spec, thresholds
}

func TestJudgeMetricSamplePassRequiresEveryEnabledThreshold(t *testing.T) {
	summary, spec, thresholds := metricJudgmentFixture()
	verdict, err := types.JudgeMetricSample(summary, spec, thresholds)
	require.NoError(t, err)
	require.Equal(t, types.MetricSampleVerdictV1_METRIC_SAMPLE_VERDICT_V1_PASS, verdict)

	summary.MissingComparedCount = thresholds.PassMaxMissingComparedCount + 1
	verdict, err = types.JudgeMetricSample(summary, spec, thresholds)
	require.NoError(t, err)
	require.Equal(t, types.MetricSampleVerdictV1_METRIC_SAMPLE_VERDICT_V1_INCONCLUSIVE, verdict)
}

func TestJudgeMetricSampleRejectsWhenAnySevereBoundaryIsReached(t *testing.T) {
	summary, spec, thresholds := metricJudgmentFixture()
	summary.AbsLogprobDiffP99Fp_1E6 = thresholds.RejectAbsLogprobDiffP99Min
	verdict, err := types.JudgeMetricSample(summary, spec, thresholds)
	require.NoError(t, err)
	require.Equal(t, types.MetricSampleVerdictV1_METRIC_SAMPLE_VERDICT_V1_REJECT, verdict)

	summary, spec, thresholds = metricJudgmentFixture()
	summary.XTopkJaccardMeanFp_1E6 = &types.MetricSummaryV1_TopkJaccardMeanFp_1E6{
		TopkJaccardMeanFp_1E6: thresholds.RejectTopkJaccardMeanMax,
	}
	verdict, err = types.JudgeMetricSample(summary, spec, thresholds)
	require.NoError(t, err)
	require.Equal(t, types.MetricSampleVerdictV1_METRIC_SAMPLE_VERDICT_V1_REJECT, verdict)
}

func TestJudgeMetricSampleValidatesPresenceAndNonoverlappingThresholds(t *testing.T) {
	summary, spec, thresholds := metricJudgmentFixture()
	summary.XUnionJsP99Fp_1E6 = nil
	_, err := types.JudgeMetricSample(summary, spec, thresholds)
	require.ErrorContains(t, err, "union JS")

	summary, spec, thresholds = metricJudgmentFixture()
	thresholds.RejectMeanAbsLogprobDiffMin = thresholds.PassMeanAbsLogprobDiffMax
	_, err = types.JudgeMetricSample(summary, spec, thresholds)
	require.ErrorContains(t, err, "overlap")

	summary, spec, thresholds = metricJudgmentFixture()
	spec.CompareTopkJaccard = false
	_, err = types.JudgeMetricSample(summary, spec, thresholds)
	require.ErrorContains(t, err, "disabled")
}
