package types

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"sort"
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

const modelProfileFixtureDenom = "uusdc"

func validProfileState(modelID string, profileVersion uint32) ProfileState {
	requiredHash := bytes.Repeat([]byte{1}, 32)
	state := ProfileState{
		ModelId: modelID, ProfileVersion: profileVersion,
		ManifestHash: requiredHash, TokenizerHash: requiredHash, SchemaHash: requiredHash,
		RuntimeClass: "CAUSAL_LM_PREFILL_LOGPROBS_V1", RequiredTopK: 8,
		TaskTypes:      []shared.TaskType{shared.TaskType_TASK_TYPE_TEXT_GENERATION},
		GenerationType: shared.GenerationType_GENERATION_TYPE_SAMPLED,
		ResourceTier:   1, MinStake: 1_000,
		ChallengeOpenWindowBlocks: ProfileChallengeOpenWindowMinBlocks,
		Status:                    ModelStatusRegistered, StatusSource: ProfileStatusSourceAutoSupport,
		VerificationProfile: shared.VerificationProfile{
			VerificationMode:            shared.VerificationMode_VERIFICATION_MODE_SINGLE_SAMPLE,
			TokenScope:                  shared.TokenScope_TOKEN_SCOPE_ALL_GENERATED_OUTPUT_TOKENS,
			JudgmentFunctionVersion:     "PREFILL_GENERATED_TOKEN_METRICS_V1",
			CanonicalEncodingVersion:    "CANONICAL_OUTPUT_TEXT_V1",
			MetricAggregateProofVersion: "PREFILL_METRIC_AGGREGATE_PROOF_V1",
			Metrics: shared.MetricSpec{
				ComparedTopK: 8, NumericScale: shared.NumericScale_NUMERIC_SCALE_FP_1E6,
			},
			EvidenceSchema: shared.NewWorkerValueEvidenceSchemaV1(1 << 30),
		},
		PricingProfile: shared.PricingProfile{
			InitialOutputPrice: 1, VerifyRatioBps: 1_000, MinOrderValue: 1,
		},
		RefPrice: 1, ProposerAddress: "proposer1", RegistrationDigest: requiredHash,
		RegistrationFeePaid: 1, CreatedHeight: 1, UpdatedHeight: 1,
	}
	evidenceSchemaHash, err := EvidenceSchemaHash(shared.ModelProfileProjection{
		ModelId: state.ModelId, ProfileVersion: state.ProfileVersion,
		TokenizerHash: state.TokenizerHash, RequiredTopK: state.RequiredTopK,
		GenerationType: state.GenerationType, VerificationProfile: state.VerificationProfile,
		BatchVerification: state.BatchVerification, SchemaHash: state.SchemaHash,
	})
	if err != nil {
		panic(err)
	}
	state.VerificationProfile.EvidenceSchemaHash = evidenceSchemaHash
	return state
}

func TestModelAndProfileStatusSourcesAreClosed(t *testing.T) {
	model := ModelState{
		ModelId: "model-status-source", ProposerAddress: "proposer1",
		Status: ModelStatusRegistered, StatusSource: ModelStatusSourceAutoProfile,
		LatestProfileVersion: 1, RegistrationFeePaid: 1, CreatedHeight: 1, UpdatedHeight: 1,
	}
	require.NoError(t, model.Validate())

	model.StatusSource = ModelStatusSourceGovernance
	require.ErrorContains(t, model.Validate(), "incompatible with status_source")
	model.Status = ModelStatusEmergencyFrozen
	model.StatusSource = ModelStatusSourceEmergency
	require.NoError(t, model.Validate())

	profile := validProfileState("model-status-source", 1)
	require.NoError(t, profile.Validate())
	profile.StatusSource = ProfileStatusSourceGovernance
	require.ErrorContains(t, profile.Validate(), "incompatible with status_source")
	profile.Status = ModelStatusEmergencyFrozen
	profile.StatusSource = ProfileStatusSourceEmergency
	require.NoError(t, profile.Validate())
}

func TestProfileRejectsZeroVerifyRatio(t *testing.T) {
	profile := validProfileState("model-zero-verify-ratio", 1)
	profile.PricingProfile.VerifyRatioBps = 0
	require.ErrorContains(t, profile.Validate(), "invalid profile pricing configuration")
}

func TestModelRegistrationDigestGoldenVector(t *testing.T) {
	projection := goldenModelProfileProjection(t)
	projectionBytes, err := CanonicalModelProfileProjection(projection)
	require.NoError(t, err)
	projectionHash, err := shared.PayloadHashV1(shared.MustDomain(shared.DomainModelChainProjectionV2), projectionBytes)
	require.NoError(t, err)
	require.Equal(t, "761a6e8d76d31a43d02e35f681156dfafceed361effa6bfb68133e0430c0108d", hex.EncodeToString(projectionHash))

	digest, returnedProjection, err := ModelRegistrationDigest(
		"trueopen-testnet-1",
		"trueopen1registrant000000000000000000000000000",
		projection,
	)
	require.NoError(t, err)
	require.Equal(t, projectionBytes, returnedProjection)
	require.Equal(t, "4a6ee76aa3b32fabf3eaff9851656405fb45e65ba98f39224a3e894807948873", hex.EncodeToString(digest))
}

func TestProfileExecutionSnapshotHashGolden(t *testing.T) {
	snapshot := goldenProfileExecutionSnapshot(t)

	got, err := ProfileExecutionSnapshotHash(snapshot)
	require.NoError(t, err)
	// Moved when CanonicalEvidenceSchemaTypedFrameV1 stopped
	// splicing the required_infer_evidence list into its message frame and started
	// framing it as one canonical_encoding_and_domain_hashing.md §4.4 REPEATED_V1
	// position. That list reaches this
	// digest through verification_profile field 14, which is why the nested layer
	// changed the snapshot hash and nothing above it did.
	require.Equal(t, "d01ffcff23a702e764bd8d1d5d1caa209908e1acd056ceacf6c948968ff01463", hex.EncodeToString(got))

	snapshot.GenerationType = shared.GenerationType_GENERATION_TYPE_DETERMINISTIC
	changed, err := ProfileExecutionSnapshotHash(snapshot)
	require.NoError(t, err)
	require.NotEqual(t, got, changed)
}

// TestProfileExecutionSnapshotHashBindsEveryNestedVerificationProfileField is the
// per-position mutation gate for the nested layer fixed by X-6. There is no
// cross-language JSON fixture for TRUEOPEN_PROFILE_VERIFICATION_SNAPSHOT_V1 today, so
// this is the only thing standing between a reordered nested frame and a silent
// consensus fork: each of the fourteen VerificationProfile field positions, and
// each of the six MetricSpec positions inside field 10, must move the digest on
// its own.
func TestProfileExecutionSnapshotHashBindsEveryNestedVerificationProfileField(t *testing.T) {
	base := goldenProfileExecutionSnapshot(t)
	baseDigest, err := ProfileExecutionSnapshotHash(base)
	require.NoError(t, err)

	mutations := []struct {
		name string
		edit func(*shared.VerificationProfile)
	}{
		{"1_verification_profile_id", func(v *shared.VerificationProfile) { v.VerificationProfileId++ }},
		{"2_judgment_function_version", func(v *shared.VerificationProfile) { v.JudgmentFunctionVersion += "-x" }},
		{"3_verification_mode", func(v *shared.VerificationProfile) {
			v.VerificationMode = shared.VerificationMode_VERIFICATION_MODE_SINGLE_SAMPLE
		}},
		{"4_token_scope", func(v *shared.VerificationProfile) {
			v.TokenScope = shared.TokenScope_TOKEN_SCOPE_UNSPECIFIED
		}},
		{"5_include_generated_special_tokens", func(v *shared.VerificationProfile) {
			v.IncludeGeneratedSpecialTokens = !v.IncludeGeneratedSpecialTokens
		}},
		{"6_include_prompt_tokens", func(v *shared.VerificationProfile) { v.IncludePromptTokens = !v.IncludePromptTokens }},
		{"7_include_padding_tokens", func(v *shared.VerificationProfile) { v.IncludePaddingTokens = !v.IncludePaddingTokens }},
		{"8_require_output_token_ids", func(v *shared.VerificationProfile) { v.RequireOutputTokenIds = !v.RequireOutputTokenIds }},
		{"9_require_finish_reason", func(v *shared.VerificationProfile) { v.RequireFinishReason = !v.RequireFinishReason }},
		{"10_metrics_compare_logprob_diff", func(v *shared.VerificationProfile) {
			v.Metrics.CompareLogprobDiff = !v.Metrics.CompareLogprobDiff
		}},
		{"10_metrics_compare_rank_delta", func(v *shared.VerificationProfile) {
			v.Metrics.CompareRankDelta = !v.Metrics.CompareRankDelta
		}},
		{"10_metrics_compare_topk_jaccard", func(v *shared.VerificationProfile) {
			v.Metrics.CompareTopkJaccard = !v.Metrics.CompareTopkJaccard
		}},
		{"10_metrics_compare_union_js", func(v *shared.VerificationProfile) {
			v.Metrics.CompareUnionJs = !v.Metrics.CompareUnionJs
		}},
		{"10_metrics_compared_top_k", func(v *shared.VerificationProfile) { v.Metrics.ComparedTopK++ }},
		{"10_metrics_numeric_scale", func(v *shared.VerificationProfile) {
			v.Metrics.NumericScale = shared.NumericScale_NUMERIC_SCALE_UNSPECIFIED
		}},
		{"11_canonical_encoding_version", func(v *shared.VerificationProfile) { v.CanonicalEncodingVersion += "-x" }},
		{"12_evidence_schema_hash", func(v *shared.VerificationProfile) {
			v.EvidenceSchemaHash = append([]byte(nil), v.EvidenceSchemaHash...)
			v.EvidenceSchemaHash[0]++
		}},
		{"13_metric_aggregate_proof_version", func(v *shared.VerificationProfile) { v.MetricAggregateProofVersion += "-x" }},
		{"14_evidence_schema", func(v *shared.VerificationProfile) {
			v.EvidenceSchema = shared.NewWorkerValueEvidenceSchemaV1(1 << 29)
		}},
	}
	require.Len(t, mutations, 19, "fourteen field positions, with field 10 expanded into its six MetricSpec positions")

	seen := map[string]string{hex.EncodeToString(baseDigest): "base"}
	for _, mutation := range mutations {
		mutation := mutation
		t.Run(mutation.name, func(t *testing.T) {
			snapshot := goldenProfileExecutionSnapshot(t)
			mutation.edit(&snapshot.VerificationProfile)
			mutated, mutationErr := ProfileExecutionSnapshotHash(snapshot)
			require.NoError(t, mutationErr)
			got := hex.EncodeToString(mutated)
			previous, collided := seen[got]
			require.False(t, collided, "%s collides with %s", mutation.name, previous)
			seen[got] = mutation.name
		})
	}
}

func goldenProfileExecutionSnapshot(t *testing.T) shared.ProfileExecutionSnapshot {
	t.Helper()
	return shared.ProfileExecutionSnapshot{
		ManifestHash:   []byte{0x01, 0x02},
		TokenizerHash:  []byte{0x03, 0x04},
		RuntimeClass:   "transformers-causal-lm",
		RequiredTopK:   64,
		GenerationType: shared.GenerationType_GENERATION_TYPE_SAMPLED,
		VerificationProfile: shared.VerificationProfile{
			VerificationProfileId:         7,
			JudgmentFunctionVersion:       "judgment-1",
			VerificationMode:              shared.VerificationMode_VERIFICATION_MODE_BATCH_SAMPLES,
			TokenScope:                    shared.TokenScope_TOKEN_SCOPE_ALL_GENERATED_OUTPUT_TOKENS,
			IncludeGeneratedSpecialTokens: true,
			IncludePromptTokens:           false,
			IncludePaddingTokens:          true,
			RequireOutputTokenIds:         true,
			RequireFinishReason:           true,
			Metrics: shared.MetricSpec{
				CompareLogprobDiff: true,
				CompareRankDelta:   true,
				CompareTopkJaccard: true,
				CompareUnionJs:     true,
				ComparedTopK:       64,
				NumericScale:       shared.NumericScale_NUMERIC_SCALE_FP_1E6,
			},
			CanonicalEncodingVersion:    "canonical-1",
			EvidenceSchemaHash:          []byte{0x05, 0x06},
			MetricAggregateProofVersion: "aggregate-1",
			EvidenceSchema:              shared.NewWorkerValueEvidenceSchemaV1(1 << 30),
		},
		VerificationThresholds: shared.VerificationThresholds{
			PassMinFiniteCount:            1,
			PassMaxMissingComparedCount:   2,
			PassMeanAbsLogprobDiffMax:     3,
			PassAbsLogprobDiffP95Max:      4,
			PassAbsLogprobDiffP99Max:      5,
			PassRankDeltaNonzeroRateMax:   6,
			PassTopkJaccardMeanMin:        7,
			PassUnionJsP99Max:             8,
			RejectMeanAbsLogprobDiffMin:   9,
			RejectAbsLogprobDiffP95Min:    10,
			RejectAbsLogprobDiffP99Min:    11,
			RejectRankDeltaNonzeroRateMin: 12,
			RejectTopkJaccardMeanMax:      13,
			RejectUnionJsP99Min:           14,
		},
		BatchVerification: shared.BatchVerification{
			Enabled:                       true,
			MinSampleCount:                15,
			MinValidSampleCount:           16,
			PassMinSamplePassRatioBps:     17,
			RejectMinSampleRejectRatioBps: 18,
		},
		SchemaHash: []byte{0x07, 0x08},
	}
}

// TestCanonicalVerificationProfileFramesProtoFieldNumberOrder is the X-6
// regression gate for the nested layer of TRUEOPEN_PROFILE_VERIFICATION_SNAPSHOT_V1.
// §1.2 requires a required nested message to encode its field frames recursively
// in schema field-number ascending order, so VerificationProfile must frame
// 1->14 with metrics (field 10) as one recursive MetricSpec frame. The module
// already owns the correct MetricSpec encoder (canonicalMetricSpecFrame, used by
// EvidenceSchemaHash); comparing against it here is what stops the module from
// carrying two different encodings of the same message.
func TestCanonicalVerificationProfileFramesProtoFieldNumberOrder(t *testing.T) {
	value := goldenModelProfileProjection(t).VerificationProfile
	evidenceSchema, err := shared.CanonicalEvidenceSchemaFrameV1(value.EvidenceSchema)
	require.NoError(t, err)
	metricSpec, err := canonicalMetricSpecFrame(value.Metrics).Bytes()
	require.NoError(t, err)
	want := shared.CanonicalFrameBytes(
		shared.Uint32BE(value.VerificationProfileId),         // 1
		[]byte(value.JudgmentFunctionVersion),                // 2
		shared.EnumBE(uint32(value.VerificationMode)),        // 3
		shared.EnumBE(uint32(value.TokenScope)),              // 4
		shared.BoolByte(value.IncludeGeneratedSpecialTokens), // 5
		shared.BoolByte(value.IncludePromptTokens),           // 6
		shared.BoolByte(value.IncludePaddingTokens),          // 7
		shared.BoolByte(value.RequireOutputTokenIds),         // 8
		shared.BoolByte(value.RequireFinishReason),           // 9
		metricSpec,                                // 10
		[]byte(value.CanonicalEncodingVersion),    // 11
		value.EvidenceSchemaHash,                  // 12
		[]byte(value.MetricAggregateProofVersion), // 13
		evidenceSchema,                            // 14
	)
	gotFrame, err := canonicalVerificationProfile(value)
	require.NoError(t, err)
	// Four, not three: evidence_schema (field 14) now frames its requirement list
	// as a REPEATED_V1 frame whose elements are themselves frames, so the deepest
	// path is profile -> evidence_schema -> repeated -> requirement.
	require.Equal(t, uint8(4), gotFrame.Depth())
	got, err := gotFrame.Bytes()
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Contains(t, string(got), string(metricSpec),
		"metrics must appear as one recursive frame, not six flattened fields")
}

func TestEvidenceSchemaHashGoldenAndScopeBinding(t *testing.T) {
	projection := goldenModelProfileProjection(t)
	got, err := EvidenceSchemaHash(projection)
	require.NoError(t, err)
	require.Equal(t, "1a50512bbf59345321280a92bec7ccd6ed5c5020f28ca578d96973eab1070dc1", hex.EncodeToString(got))
	require.Equal(t, got, projection.VerificationProfile.EvidenceSchemaHash)

	mutated := projection
	mutated.VerificationProfile.EvidenceSchema.RequiredInferEvidence = append(
		[]shared.InferEvidenceRequirementV1(nil),
		projection.VerificationProfile.EvidenceSchema.RequiredInferEvidence...,
	)
	mutated.VerificationProfile.EvidenceSchema.RequiredInferEvidence[0].MaxEncodedSizeBytes++
	changed, err := EvidenceSchemaHash(mutated)
	require.NoError(t, err)
	require.NotEqual(t, got, changed)

	mutated = projection
	mutated.ModelId = "hf-qwen3-8b-other"
	changed, err = EvidenceSchemaHash(mutated)
	require.NoError(t, err)
	require.NotEqual(t, got, changed)

	invalid := projection
	invalid.VerificationProfile.EvidenceSchema.RequiredInferEvidence = append(
		invalid.VerificationProfile.EvidenceSchema.RequiredInferEvidence,
		invalid.VerificationProfile.EvidenceSchema.RequiredInferEvidence[0],
	)
	_, err = EvidenceSchemaHash(invalid)
	require.ErrorContains(t, err, "strictly ascending and unique")
}

func TestModelProfileCanonicalCrossLanguageVector(t *testing.T) {
	type taskTypeVector struct {
		Number int32  `json:"number"`
		Name   string `json:"name"`
	}
	type vector struct {
		Schema                    string           `json:"schema"`
		TaskTypesNumericOrder     []taskTypeVector `json:"task_types_numeric_order"`
		VerificationProfileFields []string         `json:"verification_profile_fields"`
		CanonicalProjection       json.RawMessage  `json:"canonical_projection"`
		EvidenceSchemaHash        string           `json:"evidence_schema_hash"`
	}
	raw, err := os.ReadFile("testdata/model_profile_canonical_v2.json")
	require.NoError(t, err)
	var fixture vector
	require.NoError(t, json.Unmarshal(raw, &fixture))
	require.Equal(t, "trueopen-model-profile-canonical-v2", fixture.Schema)

	orderedTaskTypes := make([]shared.TaskType, len(fixture.TaskTypesNumericOrder))
	for index, item := range fixture.TaskTypesNumericOrder {
		parsed, err := parseModelProfileTaskType(item.Name)
		require.NoError(t, err)
		require.Equal(t, item.Number, int32(parsed))
		orderedTaskTypes[index] = parsed
	}
	state := validProfileState("model-a", 1)
	state.TaskTypes = orderedTaskTypes
	require.NoError(t, state.Validate())

	projection := goldenModelProfileProjection(t)
	canonical, err := CanonicalModelProfileProjection(projection)
	require.NoError(t, err)

	var compactFixture bytes.Buffer
	require.NoError(t, json.Compact(&compactFixture, fixture.CanonicalProjection))
	parsedFixture, err := ParseModelProfileProjectionJSON(compactFixture.Bytes())
	require.NoError(t, err)
	require.Equal(t, modelProfileFixtureDenom, parsedFixture.MinStake.Denom)
	require.Equal(t, modelProfileFixtureDenom, parsedFixture.RegistrationFee.Denom)
	require.Equal(t, fixture.EvidenceSchemaHash, "0x"+hex.EncodeToString(parsedFixture.VerificationProfile.EvidenceSchemaHash))

	currentEvidenceHash, err := EvidenceSchemaHash(parsedFixture)
	require.NoError(t, err)
	require.Equal(t, parsedFixture.VerificationProfile.EvidenceSchemaHash, currentEvidenceHash)
	reencodedFixture, err := CanonicalModelProfileProjection(parsedFixture)
	require.NoError(t, err)
	require.Equal(t, canonical, reencodedFixture)
	var top map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(canonical, &top))
	var verification map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(top["verification_profile"], &verification))
	fields := make([]string, 0, len(verification))
	for field := range verification {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	require.Equal(t, fixture.VerificationProfileFields, fields)

	delete(verification, "evidence_schema")
	top["verification_profile"], err = json.Marshal(verification)
	require.NoError(t, err)
	withoutTypedSchema, err := json.Marshal(top)
	require.NoError(t, err)
	_, err = ParseModelProfileProjectionJSON(withoutTypedSchema)
	require.ErrorContains(t, err, "expected exactly 14 fields, got 13")
}

func TestProfileStateRejectsNonCanonicalIDsAndEnums(t *testing.T) {
	for _, modelID := range []string{"Model-A", "/model-a", "model/a", "model a", "\u6a21\u578b", "a" + string(bytes.Repeat([]byte{'a'}, 128))} {
		state := validProfileState("model-a", 1)
		state.ModelId = modelID
		require.ErrorContains(t, state.Validate(), "model_id must match")
	}

	projection := goldenModelProfileProjection(t)
	projection.ModelId = "hf/model-a"
	_, err := CanonicalModelProfileProjection(projection)
	require.ErrorContains(t, err, "model_id must match")

	state := validProfileState("model-a", 1)
	state.TaskTypes = []shared.TaskType{
		shared.TaskType_TASK_TYPE_TEXT_GENERATION,
		shared.TaskType_TASK_TYPE_CHAT,
		shared.TaskType_TASK_TYPE_EMBEDDING,
		shared.TaskType_TASK_TYPE_CLASSIFICATION,
		shared.TaskType_TASK_TYPE_IMAGE_GENERATION,
		shared.TaskType_TASK_TYPE_MULTIMODAL,
	}
	require.NoError(t, state.Validate())

	state.TaskTypes = []shared.TaskType{
		shared.TaskType_TASK_TYPE_CHAT,
		shared.TaskType_TASK_TYPE_TEXT_GENERATION,
	}
	require.ErrorContains(t, state.Validate(), "task_types must be sorted")

	state.TaskTypes = []shared.TaskType{
		shared.TaskType_TASK_TYPE_TEXT_GENERATION,
		shared.TaskType_TASK_TYPE_TEXT_GENERATION,
	}
	require.ErrorContains(t, state.Validate(), "task_types must be sorted")

	state = validProfileState("model-a", 1)
	state.TaskTypes = []shared.TaskType{shared.TaskType(99)}
	require.ErrorContains(t, state.Validate(), "task_types must be sorted")

	state = validProfileState("model-a", 1)
	state.GenerationType = shared.GenerationType(99)
	require.ErrorContains(t, state.Validate(), "generation_type must be specified")

	state = validProfileState("model-a", 1)
	state.PricingProfile.VerifyRatioBps = 10_001
	require.ErrorContains(t, state.Validate(), "invalid profile pricing configuration")
}

func goldenModelProfileProjection(t *testing.T) shared.ModelProfileProjection {
	t.Helper()
	projection := shared.ModelProfileProjection{
		ModelId:        "hf-qwen3-8b-test",
		ProfileVersion: 1,
		ManifestHash:   mustDecodeRegistrationHash(t, "9b0148865efde2dbf366305733ee5275dc247e3b9af8def770955d3758b52031"),
		TokenizerHash:  bytes.Repeat([]byte{0x44}, 32),
		RuntimeClass:   "CAUSAL_LM_PREFILL_LOGPROBS_V1",
		RequiredTopK:   20,
		TaskTypes: []shared.TaskType{
			shared.TaskType_TASK_TYPE_TEXT_GENERATION,
			shared.TaskType_TASK_TYPE_CHAT,
		},
		GenerationType:            shared.GenerationType_GENERATION_TYPE_SAMPLED,
		ResourceTier:              2,
		MinStake:                  sdk.NewCoin(modelProfileFixtureDenom, sdkmath.NewInt(1_000_000)),
		ChallengeOpenWindowBlocks: 100,
		VerificationProfile: shared.VerificationProfile{
			VerificationProfileId:         1,
			JudgmentFunctionVersion:       "PREFILL_GENERATED_TOKEN_METRICS_V1",
			VerificationMode:              shared.VerificationMode_VERIFICATION_MODE_SINGLE_SAMPLE,
			TokenScope:                    shared.TokenScope_TOKEN_SCOPE_ALL_GENERATED_OUTPUT_TOKENS,
			IncludeGeneratedSpecialTokens: true,
			RequireOutputTokenIds:         true,
			RequireFinishReason:           true,
			Metrics: shared.MetricSpec{
				CompareLogprobDiff: true,
				CompareRankDelta:   true,
				CompareTopkJaccard: true,
				CompareUnionJs:     true,
				ComparedTopK:       20,
				NumericScale:       shared.NumericScale_NUMERIC_SCALE_FP_1E6,
			},
			CanonicalEncodingVersion:    "CANONICAL_OUTPUT_TEXT_V1",
			MetricAggregateProofVersion: "PREFILL_METRIC_AGGREGATE_PROOF_V1",
			EvidenceSchema:              shared.NewWorkerValueEvidenceSchemaV1(1 << 30),
		},
		VerificationThresholds: shared.VerificationThresholds{
			PassMinFiniteCount:            16,
			PassMeanAbsLogprobDiffMax:     50_000,
			PassAbsLogprobDiffP95Max:      100_000,
			PassAbsLogprobDiffP99Max:      200_000,
			PassRankDeltaNonzeroRateMax:   50_000,
			PassTopkJaccardMeanMin:        900_000,
			PassUnionJsP99Max:             50_000,
			RejectMeanAbsLogprobDiffMin:   300_000,
			RejectAbsLogprobDiffP95Min:    500_000,
			RejectAbsLogprobDiffP99Min:    800_000,
			RejectRankDeltaNonzeroRateMin: 300_000,
			RejectTopkJaccardMeanMax:      600_000,
			RejectUnionJsP99Min:           200_000,
		},
		PricingProfile: shared.PricingProfile{
			InitialOutputPrice: 10,
			VerifyRatioBps:     1_000,
			MinOrderValue:      1_000,
		},
		TimeoutBootstrapProfile: shared.TimeoutBootstrapProfile{
			InferTimeoutBootstrapBlocks:  100,
			VerifyTimeoutBootstrapBlocks: 50,
			CommitTimeoutBootstrapBlocks: 20,
			BootstrapValidUntilEpoch:     1_000,
		},
		SchemaHash:      bytes.Repeat([]byte{0x99}, 32),
		RegistrationFee: sdk.NewCoin(modelProfileFixtureDenom, sdkmath.NewInt(1_000_000)),
	}
	evidenceSchemaHash, err := EvidenceSchemaHash(projection)
	require.NoError(t, err)
	projection.VerificationProfile.EvidenceSchemaHash = evidenceSchemaHash
	return projection
}

func mustDecodeRegistrationHash(t *testing.T, value string) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(value)
	require.NoError(t, err)
	require.Len(t, decoded, 32)
	return decoded
}
