package types

import (
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
)

const metricFixedPointOne = uint32(1_000_000)

// JudgeMetricSample applies the immutable profile metric specification and
// thresholds to one canonical verifier summary. A rejection threshold is an
// OR condition, while every enabled pass threshold must hold for PASS.
func JudgeMetricSample(
	summary MetricSummaryV1,
	spec shared.MetricSpec,
	thresholds shared.VerificationThresholds,
) (MetricSampleVerdictV1, error) {
	if err := validateMetricJudgmentInputs(summary, spec, thresholds); err != nil {
		return MetricSampleVerdictV1_METRIC_SAMPLE_VERDICT_V1_UNSPECIFIED, err
	}

	passes := summary.FiniteCount >= thresholds.PassMinFiniteCount &&
		summary.MissingComparedCount <= thresholds.PassMaxMissingComparedCount
	rejects := false

	if spec.CompareLogprobDiff {
		passes = passes &&
			summary.MeanAbsLogprobDiffFp_1E6 <= thresholds.PassMeanAbsLogprobDiffMax &&
			summary.AbsLogprobDiffP95Fp_1E6 <= thresholds.PassAbsLogprobDiffP95Max &&
			summary.AbsLogprobDiffP99Fp_1E6 <= thresholds.PassAbsLogprobDiffP99Max
		rejects = summary.MeanAbsLogprobDiffFp_1E6 >= thresholds.RejectMeanAbsLogprobDiffMin ||
			summary.AbsLogprobDiffP95Fp_1E6 >= thresholds.RejectAbsLogprobDiffP95Min ||
			summary.AbsLogprobDiffP99Fp_1E6 >= thresholds.RejectAbsLogprobDiffP99Min
	}
	if spec.CompareRankDelta {
		passes = passes && summary.RankDeltaNonzeroRateFp_1E6 <= thresholds.PassRankDeltaNonzeroRateMax
		rejects = rejects || summary.RankDeltaNonzeroRateFp_1E6 >= thresholds.RejectRankDeltaNonzeroRateMin
	}
	if spec.CompareTopkJaccard {
		value := summary.GetTopkJaccardMeanFp_1E6()
		passes = passes && value >= thresholds.PassTopkJaccardMeanMin
		rejects = rejects || value <= thresholds.RejectTopkJaccardMeanMax
	}
	if spec.CompareUnionJs {
		value := summary.GetUnionJsP99Fp_1E6()
		passes = passes && value <= thresholds.PassUnionJsP99Max
		rejects = rejects || value >= thresholds.RejectUnionJsP99Min
	}

	if rejects {
		return MetricSampleVerdictV1_METRIC_SAMPLE_VERDICT_V1_REJECT, nil
	}
	if passes {
		return MetricSampleVerdictV1_METRIC_SAMPLE_VERDICT_V1_PASS, nil
	}
	return MetricSampleVerdictV1_METRIC_SAMPLE_VERDICT_V1_INCONCLUSIVE, nil
}

func validateMetricJudgmentInputs(
	summary MetricSummaryV1,
	spec shared.MetricSpec,
	thresholds shared.VerificationThresholds,
) error {
	if spec.NumericScale != shared.NumericScale_NUMERIC_SCALE_FP_1E6 {
		return fmt.Errorf("metric numeric scale must be FP_1E6")
	}
	if summary.RankDeltaNonzeroRateFp_1E6 > metricFixedPointOne ||
		thresholds.PassRankDeltaNonzeroRateMax > metricFixedPointOne ||
		thresholds.RejectRankDeltaNonzeroRateMin > metricFixedPointOne {
		return fmt.Errorf("rank delta rate exceeds FP_1E6 range")
	}

	if spec.CompareLogprobDiff {
		if thresholds.RejectMeanAbsLogprobDiffMin <= thresholds.PassMeanAbsLogprobDiffMax ||
			thresholds.RejectAbsLogprobDiffP95Min <= thresholds.PassAbsLogprobDiffP95Max ||
			thresholds.RejectAbsLogprobDiffP99Min <= thresholds.PassAbsLogprobDiffP99Max {
			return fmt.Errorf("logprob pass and reject thresholds overlap")
		}
	} else if summary.MeanAbsLogprobDiffFp_1E6 != 0 || summary.AbsLogprobDiffP95Fp_1E6 != 0 ||
		summary.AbsLogprobDiffP99Fp_1E6 != 0 {
		return fmt.Errorf("logprob metrics are present but disabled")
	}

	if spec.CompareRankDelta {
		if thresholds.RejectRankDeltaNonzeroRateMin <= thresholds.PassRankDeltaNonzeroRateMax {
			return fmt.Errorf("rank delta pass and reject thresholds overlap")
		}
	} else if summary.RankDeltaNonzeroRateFp_1E6 != 0 || summary.ComparedRankCount != 0 {
		return fmt.Errorf("rank metrics are present but disabled")
	}

	topkPresent := summary.XTopkJaccardMeanFp_1E6 != nil
	if spec.CompareTopkJaccard {
		if !topkPresent || spec.ComparedTopK == 0 {
			return fmt.Errorf("top-k comparison is enabled without a canonical value or profile width")
		}
		if summary.GetTopkJaccardMeanFp_1E6() > metricFixedPointOne ||
			thresholds.PassTopkJaccardMeanMin > metricFixedPointOne ||
			thresholds.RejectTopkJaccardMeanMax > metricFixedPointOne {
			return fmt.Errorf("top-k Jaccard value exceeds FP_1E6 range")
		}
		if thresholds.RejectTopkJaccardMeanMax >= thresholds.PassTopkJaccardMeanMin {
			return fmt.Errorf("top-k Jaccard pass and reject thresholds overlap")
		}
	} else if topkPresent || summary.ComparedTopkCount != 0 {
		return fmt.Errorf("top-k metrics are present but disabled")
	}

	unionPresent := summary.XUnionJsP99Fp_1E6 != nil
	if spec.CompareUnionJs {
		if !unionPresent {
			return fmt.Errorf("union JS comparison is enabled without a canonical value")
		}
		if summary.GetUnionJsP99Fp_1E6() > metricFixedPointOne ||
			thresholds.PassUnionJsP99Max > metricFixedPointOne ||
			thresholds.RejectUnionJsP99Min > metricFixedPointOne {
			return fmt.Errorf("union JS value exceeds FP_1E6 range")
		}
		if thresholds.RejectUnionJsP99Min <= thresholds.PassUnionJsP99Max {
			return fmt.Errorf("union JS pass and reject thresholds overlap")
		}
	} else if unionPresent {
		return fmt.Errorf("union JS metric is present but disabled")
	}
	return nil
}
