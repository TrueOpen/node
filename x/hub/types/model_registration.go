package types

import (
	"bytes"
	"encoding/hex"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// CanonicalModelProfileProjection encodes the frozen Canonical JSON V1
// projection used by the registration protocol.
func CanonicalModelProfileProjection(profile shared.ModelProfileProjection) ([]byte, error) {
	if err := ValidateModelID(profile.ModelId); err != nil {
		return nil, err
	}
	minStake, err := canonicalJSONCoin("min_stake", profile.MinStake)
	if err != nil {
		return nil, err
	}
	registrationFee, err := canonicalJSONCoin("registration_fee", profile.RegistrationFee)
	if err != nil {
		return nil, err
	}
	taskTypes := make([]string, len(profile.TaskTypes))
	for i, value := range profile.TaskTypes {
		taskTypes[i] = taskTypeName(value)
	}
	manifestHash, err := canonicalJSONHash("manifest_hash", profile.ManifestHash)
	if err != nil {
		return nil, err
	}
	tokenizerHash, err := canonicalJSONHash("tokenizer_hash", profile.TokenizerHash)
	if err != nil {
		return nil, err
	}
	schemaHash, err := canonicalJSONHash("schema_hash", profile.SchemaHash)
	if err != nil {
		return nil, err
	}
	evidenceSchemaHash, err := canonicalJSONHash("evidence_schema_hash", profile.VerificationProfile.EvidenceSchemaHash)
	if err != nil {
		return nil, err
	}
	recomputedEvidenceSchemaHash, err := EvidenceSchemaHash(profile)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(recomputedEvidenceSchemaHash, profile.VerificationProfile.EvidenceSchemaHash) {
		return nil, fmt.Errorf("evidence_schema_hash does not match the typed evidence_schema")
	}
	verification := profile.VerificationProfile
	metrics := verification.Metrics
	thresholds := profile.VerificationThresholds
	batch := profile.BatchVerification
	pricing := profile.PricingProfile
	timeout := profile.TimeoutBootstrapProfile
	evidenceRequirements := make([]any, len(verification.EvidenceSchema.RequiredInferEvidence))
	for index, requirement := range verification.EvidenceSchema.RequiredInferEvidence {
		evidenceRequirements[index] = map[string]any{
			"commitment_schema_version": requirement.CommitmentSchemaVersion,
			"evidence_kind":             evidenceKindName(requirement.EvidenceKind),
			"max_encoded_size_bytes":    requirement.MaxEncodedSizeBytes,
		}
	}
	return shared.CanonicalJSONV1(map[string]any{
		"batch_verification": map[string]any{
			"enabled":                            batch.Enabled,
			"min_sample_count":                   batch.MinSampleCount,
			"min_valid_sample_count":             batch.MinValidSampleCount,
			"pass_min_sample_pass_ratio_bps":     batch.PassMinSamplePassRatioBps,
			"reject_min_sample_reject_ratio_bps": batch.RejectMinSampleRejectRatioBps,
		},
		"challenge_open_window_blocks": profile.ChallengeOpenWindowBlocks,
		"generation_type":              generationTypeName(profile.GenerationType),
		"manifest_hash":                manifestHash,
		"min_stake":                    minStake,
		"model_id":                     profile.ModelId,
		"previous_profile_version":     profile.PreviousProfileVersion,
		"pricing_profile": map[string]any{
			"initial_output_price": pricing.InitialOutputPrice,
			"min_order_value":      pricing.MinOrderValue,
			"verify_ratio_bps":     pricing.VerifyRatioBps,
		},
		"profile_version":  profile.ProfileVersion,
		"registration_fee": registrationFee,
		"required_top_k":   profile.RequiredTopK,
		"resource_tier":    profile.ResourceTier,
		"runtime_class":    profile.RuntimeClass,
		"schema_hash":      schemaHash,
		"task_types":       taskTypes,
		"timeout_bootstrap_profile": map[string]any{
			"bootstrap_valid_until_epoch":     timeout.BootstrapValidUntilEpoch,
			"commit_timeout_bootstrap_blocks": timeout.CommitTimeoutBootstrapBlocks,
			"infer_timeout_bootstrap_blocks":  timeout.InferTimeoutBootstrapBlocks,
			"verify_timeout_bootstrap_blocks": timeout.VerifyTimeoutBootstrapBlocks,
		},
		"tokenizer_hash": tokenizerHash,
		"verification_profile": map[string]any{
			"canonical_encoding_version": verification.CanonicalEncodingVersion,
			"evidence_schema": map[string]any{
				"required_infer_evidence": evidenceRequirements,
				"schema_version":          verification.EvidenceSchema.SchemaVersion,
			},
			"evidence_schema_hash":             evidenceSchemaHash,
			"include_generated_special_tokens": verification.IncludeGeneratedSpecialTokens,
			"include_padding_tokens":           verification.IncludePaddingTokens,
			"include_prompt_tokens":            verification.IncludePromptTokens,
			"judgment_function_version":        verification.JudgmentFunctionVersion,
			"metric_aggregate_proof_version":   verification.MetricAggregateProofVersion,
			"metrics": map[string]any{
				"compare_logprob_diff": metrics.CompareLogprobDiff,
				"compare_rank_delta":   metrics.CompareRankDelta,
				"compare_topk_jaccard": metrics.CompareTopkJaccard,
				"compare_union_js":     metrics.CompareUnionJs,
				"compared_top_k":       metrics.ComparedTopK,
				"numeric_scale":        numericScaleName(metrics.NumericScale),
			},
			"require_finish_reason":    verification.RequireFinishReason,
			"require_output_token_ids": verification.RequireOutputTokenIds,
			"token_scope":              tokenScopeName(verification.TokenScope),
			"verification_mode":        verificationModeName(verification.VerificationMode),
			"verification_profile_id":  verification.VerificationProfileId,
		},
		"verification_thresholds": map[string]any{
			"pass_abs_logprob_diff_p95_max":      thresholds.PassAbsLogprobDiffP95Max,
			"pass_abs_logprob_diff_p99_max":      thresholds.PassAbsLogprobDiffP99Max,
			"pass_max_missing_compared_count":    thresholds.PassMaxMissingComparedCount,
			"pass_mean_abs_logprob_diff_max":     thresholds.PassMeanAbsLogprobDiffMax,
			"pass_min_finite_count":              thresholds.PassMinFiniteCount,
			"pass_rank_delta_nonzero_rate_max":   thresholds.PassRankDeltaNonzeroRateMax,
			"pass_topk_jaccard_mean_min":         thresholds.PassTopkJaccardMeanMin,
			"pass_union_js_p99_max":              thresholds.PassUnionJsP99Max,
			"reject_abs_logprob_diff_p95_min":    thresholds.RejectAbsLogprobDiffP95Min,
			"reject_abs_logprob_diff_p99_min":    thresholds.RejectAbsLogprobDiffP99Min,
			"reject_mean_abs_logprob_diff_min":   thresholds.RejectMeanAbsLogprobDiffMin,
			"reject_rank_delta_nonzero_rate_min": thresholds.RejectRankDeltaNonzeroRateMin,
			"reject_topk_jaccard_mean_max":       thresholds.RejectTopkJaccardMeanMax,
			"reject_union_js_p99_min":            thresholds.RejectUnionJsP99Min,
		},
	})
}

func ModelRegistrationDigest(chainID, proposer string, profile shared.ModelProfileProjection) ([]byte, []byte, error) {
	projection, err := CanonicalModelProfileProjection(profile)
	if err != nil {
		return nil, nil, err
	}
	projectionHash, err := shared.PayloadHashV1(shared.MustDomain(shared.DomainModelChainProjectionV2), projection)
	if err != nil {
		return nil, nil, err
	}
	manifestHash, err := canonicalJSONHash("manifest_hash", profile.ManifestHash)
	if err != nil {
		return nil, nil, err
	}
	payload, err := shared.CanonicalJSONV1(map[string]any{
		"chain_id":              chainID,
		"chain_projection_hash": "0x" + hex.EncodeToString(projectionHash),
		"manifest_hash":         manifestHash,
		"profile_version":       profile.ProfileVersion,
		"proposer_address":      proposer,
	})
	if err != nil {
		return nil, nil, err
	}
	digest, err := shared.PayloadHashV1(shared.MustDomain(shared.DomainModelRegistrationDigestV2), payload)
	if err != nil {
		return nil, nil, err
	}
	return digest, projection, nil
}

func canonicalJSONCoin(name string, coin sdk.Coin) (map[string]any, error) {
	if !coin.IsValid() || !coin.Amount.IsUint64() {
		return nil, fmt.Errorf("%s must be a valid Coin with a u64 amount", name)
	}
	return map[string]any{"amount": coin.Amount.Uint64(), "denom": coin.Denom}, nil
}

func canonicalJSONHash(name string, value []byte) (string, error) {
	if len(value) != 32 {
		return "", fmt.Errorf("%s must be Hash32", name)
	}
	return "0x" + hex.EncodeToString(value), nil
}

// ProfileExecutionSnapshotHash is the sole producer of
// TRUEOPEN_PROFILE_VERIFICATION_SNAPSHOT_V1.
//
// The nine top-level fields below commit a superset of §10.1's ten-item list,
// so the text is stale rather than the preimage weak, and the ruling is that
// this producer stands:
//   - canonical_encoding_version, evidence_schema_hash and
//     metric_aggregate_proof_version are not dropped. They are fields 11, 12
//     and 13 of the recursively framed verification_profile. §10.1 enumerates
//     them flat because it predates the §1.2 nested framing this producer now
//     follows, which is the other half of the same fix.
//   - manifest_hash and generation_type are the two extras. Both are
//     execution-relevant and both are carried by the retained
//     ProfileExecutionSnapshot proto, so deleting them to match the text would
//     weaken the commitment for no gain.
//
// The nested layer is not a divergence any more: canonicalVerificationProfile,
// canonicalVerificationThresholdsBytes and canonicalBatchVerification all frame
// their message recursively in proto field-number ascending order per §1.2, and
// the current monorepo formula registers exactly the nine positions below. That
// is why PreimageDivergesFromContract is false in the domain registry: the
// divergence this comment used to record was resolved on the document side, not
// waived here.
func ProfileExecutionSnapshotHash(snapshot shared.ProfileExecutionSnapshot) ([]byte, error) {
	verificationProfile, err := canonicalVerificationProfile(snapshot.VerificationProfile)
	if err != nil {
		return nil, err
	}
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainProfileVerificationSnapshotV1)).Raw(
		snapshot.ManifestHash,
		snapshot.TokenizerHash,
		[]byte(snapshot.RuntimeClass),
		shared.Uint32BE(snapshot.RequiredTopK),
		shared.EnumBE(uint32(snapshot.GenerationType)),
	).Nested(
		verificationProfile,
		canonicalVerificationThresholdsFrame(snapshot.VerificationThresholds),
		canonicalBatchVerificationFrame(snapshot.BatchVerification),
	).Raw(
		snapshot.SchemaHash,
	).Sum()
}

// canonicalVerificationProfile is the §1.2 recursive frame of the required
// nested VerificationProfile message: every field of proto/shared/v1/
// model_profile.proto's VerificationProfile in field-number ascending order 1->14,
// with metrics (field 10) encoded as one recursive MetricSpec frame rather than
// flattened into its six scalars.
//
// The flattened form this replaces was the same module's second, disagreeing
// encoding of MetricSpec: canonicalMetricSpec below is the recursive encoder that
// EvidenceSchemaHash has always used. Two encodings of one message in one module
// is a consensus fork waiting for a refactor to pick the wrong one, so the
// MetricSpec frame is now sourced from that single helper.
func canonicalVerificationProfile(value shared.VerificationProfile) (shared.CanonicalFrameV1, error) {
	evidenceSchema := shared.CanonicalEvidenceSchemaTypedFrameV1(value.EvidenceSchema)
	if err := evidenceSchema.Err(); err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	frame := shared.NewCanonicalFrameBuilderV1().Raw(
		shared.Uint32BE(value.VerificationProfileId),         // 1
		[]byte(value.JudgmentFunctionVersion),                // 2
		shared.EnumBE(uint32(value.VerificationMode)),        // 3
		shared.EnumBE(uint32(value.TokenScope)),              // 4
		shared.BoolByte(value.IncludeGeneratedSpecialTokens), // 5
		shared.BoolByte(value.IncludePromptTokens),           // 6
		shared.BoolByte(value.IncludePaddingTokens),          // 7
		shared.BoolByte(value.RequireOutputTokenIds),         // 8
		shared.BoolByte(value.RequireFinishReason),           // 9
	).Nested(
		canonicalMetricSpecFrame(value.Metrics), // 10
	).Raw(
		[]byte(value.CanonicalEncodingVersion),    // 11
		value.EvidenceSchemaHash,                  // 12
		[]byte(value.MetricAggregateProofVersion), // 13
	).Nested(evidenceSchema).Build() // 14
	if err := frame.Err(); err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	return frame, nil
}

// EvidenceSchemaHash is the only producer for the readable evidence descriptor
// committed by VerificationProfile.evidence_schema_hash.
func EvidenceSchemaHash(profile shared.ModelProfileProjection) ([]byte, error) {
	if err := ValidateModelID(profile.ModelId); err != nil {
		return nil, err
	}
	if profile.ProfileVersion == 0 || len(profile.SchemaHash) != 32 || len(profile.TokenizerHash) != 32 {
		return nil, fmt.Errorf("evidence schema profile scope is incomplete")
	}
	verification := profile.VerificationProfile
	if err := shared.ValidateEvidenceSchemaV1(verification.EvidenceSchema); err != nil {
		return nil, err
	}
	builder := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainEvidenceSchemaV1)).Raw(
		shared.Uint32BE(verification.EvidenceSchema.SchemaVersion),
		[]byte(profile.ModelId),
		shared.Uint32BE(profile.ProfileVersion),
		profile.SchemaHash,
		profile.TokenizerHash,
		shared.EnumBE(uint32(profile.GenerationType)),
		shared.Uint32BE(profile.RequiredTopK),
	).Nested(canonicalBatchVerificationFrame(profile.BatchVerification)).Raw(
		shared.Uint32BE(verification.VerificationProfileId),
		[]byte(verification.JudgmentFunctionVersion),
		[]byte(verification.CanonicalEncodingVersion),
		[]byte(verification.MetricAggregateProofVersion),
		shared.EnumBE(uint32(verification.VerificationMode)),
		shared.EnumBE(uint32(verification.TokenScope)),
		shared.BoolByte(verification.IncludeGeneratedSpecialTokens),
		shared.BoolByte(verification.IncludePromptTokens),
		shared.BoolByte(verification.IncludePaddingTokens),
		shared.BoolByte(verification.RequireOutputTokenIds),
		shared.BoolByte(verification.RequireFinishReason),
	).Nested(canonicalMetricSpecFrame(verification.Metrics)).Raw(
		shared.Uint32BE(uint32(len(verification.EvidenceSchema.RequiredInferEvidence))),
	)
	elements := make([]shared.CanonicalFrameV1, 0, len(verification.EvidenceSchema.RequiredInferEvidence))
	for index, requirement := range verification.EvidenceSchema.RequiredInferEvidence {
		frame := shared.CanonicalInferEvidenceRequirementTypedFrameV1(requirement)
		if err := frame.Err(); err != nil {
			return nil, fmt.Errorf("required_infer_evidence[%d]: %w", index, err)
		}
		elements = append(elements, frame)
	}
	return builder.Nested(shared.CanonicalRepeatedFramesV1(elements)).Sum()
}

func canonicalMetricSpecFrame(value shared.MetricSpec) shared.CanonicalFrameV1 {
	return shared.FlatCanonicalFrameV1(
		shared.BoolByte(value.CompareLogprobDiff),
		shared.BoolByte(value.CompareRankDelta),
		shared.BoolByte(value.CompareTopkJaccard),
		shared.BoolByte(value.CompareUnionJs),
		shared.Uint32BE(value.ComparedTopK),
		shared.EnumBE(uint32(value.NumericScale)),
	)
}

func canonicalVerificationThresholdsFrame(value shared.VerificationThresholds) shared.CanonicalFrameV1 {
	return shared.FlatCanonicalFrameV1(
		shared.Uint32BE(value.PassAbsLogprobDiffP95Max),
		shared.Uint32BE(value.PassAbsLogprobDiffP99Max),
		shared.Uint32BE(value.PassMaxMissingComparedCount),
		shared.Uint32BE(value.PassMeanAbsLogprobDiffMax),
		shared.Uint32BE(value.PassMinFiniteCount),
		shared.Uint32BE(value.PassRankDeltaNonzeroRateMax),
		shared.Uint32BE(value.PassTopkJaccardMeanMin),
		shared.Uint32BE(value.PassUnionJsP99Max),
		shared.Uint32BE(value.RejectAbsLogprobDiffP95Min),
		shared.Uint32BE(value.RejectAbsLogprobDiffP99Min),
		shared.Uint32BE(value.RejectMeanAbsLogprobDiffMin),
		shared.Uint32BE(value.RejectRankDeltaNonzeroRateMin),
		shared.Uint32BE(value.RejectTopkJaccardMeanMax),
		shared.Uint32BE(value.RejectUnionJsP99Min),
	)
}

func canonicalBatchVerificationFrame(value shared.BatchVerification) shared.CanonicalFrameV1 {
	return shared.FlatCanonicalFrameV1(
		shared.BoolByte(value.Enabled),
		shared.Uint32BE(value.MinSampleCount),
		shared.Uint32BE(value.MinValidSampleCount),
		shared.Uint32BE(value.PassMinSamplePassRatioBps),
		shared.Uint32BE(value.RejectMinSampleRejectRatioBps),
	)
}

func generationTypeName(value shared.GenerationType) string {
	return trimEnumPrefix(value.String(), "GENERATION_TYPE_")
}
func verificationModeName(value shared.VerificationMode) string {
	return trimEnumPrefix(value.String(), "VERIFICATION_MODE_")
}
func tokenScopeName(value shared.TokenScope) string {
	return trimEnumPrefix(value.String(), "TOKEN_SCOPE_")
}
func numericScaleName(value shared.NumericScale) string {
	return trimEnumPrefix(value.String(), "NUMERIC_SCALE_")
}
func evidenceKindName(value shared.EvidenceKind) string {
	return trimEnumPrefix(value.String(), "EVIDENCE_KIND_")
}
func trimEnumPrefix(value, prefix string) string {
	if len(value) >= len(prefix) && value[:len(prefix)] == prefix {
		return value[len(prefix):]
	}
	return value
}
