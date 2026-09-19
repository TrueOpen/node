package types

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestParseModelProfileProjectionJSONRoundTrip(t *testing.T) {
	projection := goldenModelProfileProjection(t)
	canonical, err := CanonicalModelProfileProjection(projection)
	require.NoError(t, err)
	projectionJSON := marshalModelProfileProjectionJSON(t, projection)

	parsed, err := ParseModelProfileProjectionJSON(projectionJSON)
	require.NoError(t, err)
	require.Equal(t, modelProfileFixtureDenom, parsed.MinStake.Denom)
	require.Equal(t, modelProfileFixtureDenom, parsed.RegistrationFee.Denom)
	roundTrip, err := CanonicalModelProfileProjection(parsed)
	require.NoError(t, err)
	require.Equal(t, canonical, roundTrip)

	quote := string(rune(34))
	withUnknown := bytes.Replace(projectionJSON, []byte("{"), []byte("{"+quote+"unknown"+quote+":1,"), 1)
	_, err = ParseModelProfileProjectionJSON(withUnknown)
	require.Error(t, err)

	modelIDField := quote + "model_id" + quote + ":"
	duplicateModelID := bytes.Replace(
		projectionJSON,
		[]byte(modelIDField),
		[]byte(modelIDField+quote+"other"+quote+","+modelIDField),
		1,
	)
	_, err = ParseModelProfileProjectionJSON(duplicateModelID)
	require.ErrorContains(t, err, "duplicate JSON field model_id")

	withWrongDenom := bytes.Replace(projectionJSON, []byte(quote+modelProfileFixtureDenom+quote), []byte(quote+"uatom"+quote), 1)
	_, err = ParseModelProfileProjectionJSON(withWrongDenom)
	require.ErrorContains(t, err, "must use the same denom")

	withAlternateDenom := bytes.ReplaceAll(projectionJSON, []byte(quote+modelProfileFixtureDenom+quote), []byte(quote+"uatom"+quote))
	parsed, err = ParseModelProfileProjectionJSON(withAlternateDenom)
	require.NoError(t, err)
	require.Equal(t, "uatom", parsed.MinStake.Denom)
	require.Equal(t, "uatom", parsed.RegistrationFee.Denom)
	alternateCanonical, err := CanonicalModelProfileProjection(parsed)
	require.NoError(t, err)
	require.Contains(t, string(alternateCanonical), quote+"denom"+quote+":"+quote+"uatom"+quote)
	require.NotContains(t, string(alternateCanonical), modelProfileFixtureDenom)

	withInvalidDenom := bytes.ReplaceAll(projectionJSON, []byte(quote+modelProfileFixtureDenom+quote), []byte(quote+"x"+quote))
	_, err = ParseModelProfileProjectionJSON(withInvalidDenom)
	require.ErrorContains(t, err, "denom is invalid")
}

func marshalModelProfileProjectionJSON(t *testing.T, profile shared.ModelProfileProjection) []byte {
	t.Helper()
	taskTypes := make([]string, len(profile.TaskTypes))
	for i, value := range profile.TaskTypes {
		taskTypes[i] = trimEnumPrefix(value.String(), "TASK_TYPE_")
	}
	evidenceRequirements := make([]any, len(profile.VerificationProfile.EvidenceSchema.RequiredInferEvidence))
	for index, requirement := range profile.VerificationProfile.EvidenceSchema.RequiredInferEvidence {
		evidenceRequirements[index] = map[string]any{
			"commitment_schema_version": requirement.CommitmentSchemaVersion,
			"evidence_kind":             evidenceKindName(requirement.EvidenceKind),
			"max_encoded_size_bytes":    requirement.MaxEncodedSizeBytes,
		}
	}
	projection := map[string]any{
		"batch_verification": map[string]any{
			"enabled": profile.BatchVerification.Enabled, "min_sample_count": profile.BatchVerification.MinSampleCount,
			"min_valid_sample_count":             profile.BatchVerification.MinValidSampleCount,
			"pass_min_sample_pass_ratio_bps":     profile.BatchVerification.PassMinSamplePassRatioBps,
			"reject_min_sample_reject_ratio_bps": profile.BatchVerification.RejectMinSampleRejectRatioBps,
		},
		"challenge_open_window_blocks": profile.ChallengeOpenWindowBlocks,
		"generation_type":              generationTypeName(profile.GenerationType),
		"manifest_hash":                testHashHex(profile.ManifestHash),
		"min_stake": map[string]any{
			"amount": json.Number(profile.MinStake.Amount.String()), "denom": profile.MinStake.Denom,
		},
		"model_id": profile.ModelId, "previous_profile_version": profile.PreviousProfileVersion,
		"pricing_profile": map[string]any{
			"initial_output_price": profile.PricingProfile.InitialOutputPrice,
			"min_order_value":      profile.PricingProfile.MinOrderValue,
			"verify_ratio_bps":     profile.PricingProfile.VerifyRatioBps,
		},
		"profile_version": profile.ProfileVersion,
		"registration_fee": map[string]any{
			"amount": json.Number(profile.RegistrationFee.Amount.String()), "denom": profile.RegistrationFee.Denom,
		},
		"required_top_k": profile.RequiredTopK, "resource_tier": profile.ResourceTier,
		"runtime_class": profile.RuntimeClass, "schema_hash": testHashHex(profile.SchemaHash), "task_types": taskTypes,
		"timeout_bootstrap_profile": map[string]any{
			"bootstrap_valid_until_epoch":     profile.TimeoutBootstrapProfile.BootstrapValidUntilEpoch,
			"commit_timeout_bootstrap_blocks": profile.TimeoutBootstrapProfile.CommitTimeoutBootstrapBlocks,
			"infer_timeout_bootstrap_blocks":  profile.TimeoutBootstrapProfile.InferTimeoutBootstrapBlocks,
			"verify_timeout_bootstrap_blocks": profile.TimeoutBootstrapProfile.VerifyTimeoutBootstrapBlocks,
		},
		"tokenizer_hash": testHashHex(profile.TokenizerHash),
		"verification_profile": map[string]any{
			"canonical_encoding_version": profile.VerificationProfile.CanonicalEncodingVersion,
			"evidence_schema": map[string]any{
				"required_infer_evidence": evidenceRequirements,
				"schema_version":          profile.VerificationProfile.EvidenceSchema.SchemaVersion,
			},
			"evidence_schema_hash":             testHashHex(profile.VerificationProfile.EvidenceSchemaHash),
			"include_generated_special_tokens": profile.VerificationProfile.IncludeGeneratedSpecialTokens,
			"include_padding_tokens":           profile.VerificationProfile.IncludePaddingTokens,
			"include_prompt_tokens":            profile.VerificationProfile.IncludePromptTokens,
			"judgment_function_version":        profile.VerificationProfile.JudgmentFunctionVersion,
			"metric_aggregate_proof_version":   profile.VerificationProfile.MetricAggregateProofVersion,
			"metrics": map[string]any{
				"compare_logprob_diff": profile.VerificationProfile.Metrics.CompareLogprobDiff,
				"compare_rank_delta":   profile.VerificationProfile.Metrics.CompareRankDelta,
				"compare_topk_jaccard": profile.VerificationProfile.Metrics.CompareTopkJaccard,
				"compare_union_js":     profile.VerificationProfile.Metrics.CompareUnionJs,
				"compared_top_k":       profile.VerificationProfile.Metrics.ComparedTopK,
				"numeric_scale":        numericScaleName(profile.VerificationProfile.Metrics.NumericScale),
			},
			"require_finish_reason":    profile.VerificationProfile.RequireFinishReason,
			"require_output_token_ids": profile.VerificationProfile.RequireOutputTokenIds,
			"token_scope":              tokenScopeName(profile.VerificationProfile.TokenScope),
			"verification_mode":        verificationModeName(profile.VerificationProfile.VerificationMode),
			"verification_profile_id":  profile.VerificationProfile.VerificationProfileId,
		},
		"verification_thresholds": map[string]any{
			"pass_abs_logprob_diff_p95_max":      profile.VerificationThresholds.PassAbsLogprobDiffP95Max,
			"pass_abs_logprob_diff_p99_max":      profile.VerificationThresholds.PassAbsLogprobDiffP99Max,
			"pass_max_missing_compared_count":    profile.VerificationThresholds.PassMaxMissingComparedCount,
			"pass_mean_abs_logprob_diff_max":     profile.VerificationThresholds.PassMeanAbsLogprobDiffMax,
			"pass_min_finite_count":              profile.VerificationThresholds.PassMinFiniteCount,
			"pass_rank_delta_nonzero_rate_max":   profile.VerificationThresholds.PassRankDeltaNonzeroRateMax,
			"pass_topk_jaccard_mean_min":         profile.VerificationThresholds.PassTopkJaccardMeanMin,
			"pass_union_js_p99_max":              profile.VerificationThresholds.PassUnionJsP99Max,
			"reject_abs_logprob_diff_p95_min":    profile.VerificationThresholds.RejectAbsLogprobDiffP95Min,
			"reject_abs_logprob_diff_p99_min":    profile.VerificationThresholds.RejectAbsLogprobDiffP99Min,
			"reject_mean_abs_logprob_diff_min":   profile.VerificationThresholds.RejectMeanAbsLogprobDiffMin,
			"reject_rank_delta_nonzero_rate_min": profile.VerificationThresholds.RejectRankDeltaNonzeroRateMin,
			"reject_topk_jaccard_mean_max":       profile.VerificationThresholds.RejectTopkJaccardMeanMax,
			"reject_union_js_p99_min":            profile.VerificationThresholds.RejectUnionJsP99Min,
		},
	}
	encoded, err := json.Marshal(projection)
	require.NoError(t, err)
	return encoded
}

func testHashHex(value []byte) string { return "0x" + hex.EncodeToString(value) }
