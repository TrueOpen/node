package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
)

type modelProfileJSON struct {
	BatchVerification       shared.BatchVerification       `json:"batch_verification"`
	ChallengeOpenWindow     uint64                         `json:"challenge_open_window_blocks"`
	GenerationType          string                         `json:"generation_type"`
	ManifestHash            string                         `json:"manifest_hash"`
	MinStake                modelProfileCoinJSON           `json:"min_stake"`
	ModelID                 string                         `json:"model_id"`
	PreviousProfileVersion  uint32                         `json:"previous_profile_version"`
	PricingProfile          modelProfilePricingJSON        `json:"pricing_profile"`
	ProfileVersion          uint32                         `json:"profile_version"`
	RegistrationFee         modelProfileCoinJSON           `json:"registration_fee"`
	RequiredTopK            uint32                         `json:"required_top_k"`
	ResourceTier            uint32                         `json:"resource_tier"`
	RuntimeClass            string                         `json:"runtime_class"`
	SchemaHash              string                         `json:"schema_hash"`
	TaskTypes               []string                       `json:"task_types"`
	TimeoutBootstrapProfile shared.TimeoutBootstrapProfile `json:"timeout_bootstrap_profile"`
	TokenizerHash           string                         `json:"tokenizer_hash"`
	VerificationProfile     modelProfileVerificationJSON   `json:"verification_profile"`
	VerificationThresholds  shared.VerificationThresholds  `json:"verification_thresholds"`
}

type modelProfileCoinJSON struct {
	Amount uint64 `json:"amount"`
	Denom  string `json:"denom"`
}

type modelProfilePricingJSON struct {
	InitialOutputPrice uint64 `json:"initial_output_price"`
	MinOrderValue      uint64 `json:"min_order_value"`
	VerifyRatioBps     uint32 `json:"verify_ratio_bps"`
}

type modelProfileVerificationJSON struct {
	CanonicalEncodingVersion      string                         `json:"canonical_encoding_version"`
	EvidenceSchema                modelProfileEvidenceSchemaJSON `json:"evidence_schema"`
	EvidenceSchemaHash            string                         `json:"evidence_schema_hash"`
	IncludeGeneratedSpecialTokens bool                           `json:"include_generated_special_tokens"`
	IncludePaddingTokens          bool                           `json:"include_padding_tokens"`
	IncludePromptTokens           bool                           `json:"include_prompt_tokens"`
	JudgmentFunctionVersion       string                         `json:"judgment_function_version"`
	MetricAggregateProofVersion   string                         `json:"metric_aggregate_proof_version"`
	Metrics                       modelProfileMetricJSON         `json:"metrics"`
	RequireFinishReason           bool                           `json:"require_finish_reason"`
	RequireOutputTokenIDs         bool                           `json:"require_output_token_ids"`
	TokenScope                    string                         `json:"token_scope"`
	VerificationMode              string                         `json:"verification_mode"`
	VerificationProfileID         uint32                         `json:"verification_profile_id"`
}

type modelProfileEvidenceSchemaJSON struct {
	SchemaVersion         uint32                                `json:"schema_version"`
	RequiredInferEvidence []modelProfileEvidenceRequirementJSON `json:"required_infer_evidence"`
}

type modelProfileEvidenceRequirementJSON struct {
	EvidenceKind            string `json:"evidence_kind"`
	CommitmentSchemaVersion uint32 `json:"commitment_schema_version"`
	MaxEncodedSizeBytes     uint64 `json:"max_encoded_size_bytes"`
}

type modelProfileMetricJSON struct {
	CompareLogprobDiff bool   `json:"compare_logprob_diff"`
	CompareRankDelta   bool   `json:"compare_rank_delta"`
	CompareTopKJaccard bool   `json:"compare_topk_jaccard"`
	CompareUnionJS     bool   `json:"compare_union_js"`
	ComparedTopK       uint32 `json:"compared_top_k"`
	NumericScale       string `json:"numeric_scale"`
}

// ParseModelProfileProjectionJSON decodes the frozen canonical JSON shape
// used by manifests, genesis seeds, and the public registration CLI.
func ParseModelProfileProjectionJSON(data []byte) (shared.ModelProfileProjection, error) {
	if err := rejectDuplicateModelProfileJSONKeys(data); err != nil {
		return shared.ModelProfileProjection{}, err
	}
	if err := validateModelProfileJSONShape(data); err != nil {
		return shared.ModelProfileProjection{}, err
	}
	var input modelProfileJSON
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return shared.ModelProfileProjection{}, fmt.Errorf("decode model profile projection: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return shared.ModelProfileProjection{}, fmt.Errorf("model profile projection must contain one JSON object")
	}

	manifestHash, err := decodeModelProfileHash("manifest_hash", input.ManifestHash, false)
	if err != nil {
		return shared.ModelProfileProjection{}, err
	}
	tokenizerHash, err := decodeModelProfileHash("tokenizer_hash", input.TokenizerHash, false)
	if err != nil {
		return shared.ModelProfileProjection{}, err
	}
	schemaHash, err := decodeModelProfileHash("schema_hash", input.SchemaHash, false)
	if err != nil {
		return shared.ModelProfileProjection{}, err
	}
	evidenceSchemaHash, err := decodeModelProfileHash("verification_profile.evidence_schema_hash", input.VerificationProfile.EvidenceSchemaHash, false)
	if err != nil {
		return shared.ModelProfileProjection{}, err
	}
	if err := sdk.ValidateDenom(input.MinStake.Denom); err != nil {
		return shared.ModelProfileProjection{}, fmt.Errorf("min_stake denom is invalid: %w", err)
	}
	if err := sdk.ValidateDenom(input.RegistrationFee.Denom); err != nil {
		return shared.ModelProfileProjection{}, fmt.Errorf("registration_fee denom is invalid: %w", err)
	}
	if input.MinStake.Denom != input.RegistrationFee.Denom {
		return shared.ModelProfileProjection{}, fmt.Errorf("min_stake and registration_fee must use the same denom")
	}

	taskTypes := make([]shared.TaskType, len(input.TaskTypes))
	for i, value := range input.TaskTypes {
		taskTypes[i], err = parseModelProfileTaskType(value)
		if err != nil {
			return shared.ModelProfileProjection{}, err
		}
	}
	generationType, err := parseModelProfileGenerationType(input.GenerationType)
	if err != nil {
		return shared.ModelProfileProjection{}, err
	}
	verificationMode, err := parseModelProfileVerificationMode(input.VerificationProfile.VerificationMode)
	if err != nil {
		return shared.ModelProfileProjection{}, err
	}
	tokenScope, err := parseModelProfileTokenScope(input.VerificationProfile.TokenScope)
	if err != nil {
		return shared.ModelProfileProjection{}, err
	}
	numericScale, err := parseModelProfileNumericScale(input.VerificationProfile.Metrics.NumericScale)
	if err != nil {
		return shared.ModelProfileProjection{}, err
	}
	evidenceRequirements := make([]shared.InferEvidenceRequirementV1, len(input.VerificationProfile.EvidenceSchema.RequiredInferEvidence))
	for index, requirement := range input.VerificationProfile.EvidenceSchema.RequiredInferEvidence {
		kind, err := parseModelProfileEvidenceKind(requirement.EvidenceKind)
		if err != nil {
			return shared.ModelProfileProjection{}, err
		}
		evidenceRequirements[index] = shared.InferEvidenceRequirementV1{
			EvidenceKind: kind, CommitmentSchemaVersion: requirement.CommitmentSchemaVersion,
			MaxEncodedSizeBytes: requirement.MaxEncodedSizeBytes,
		}
	}

	return shared.ModelProfileProjection{
		ModelId: input.ModelID, ProfileVersion: input.ProfileVersion,
		ManifestHash: manifestHash, TokenizerHash: tokenizerHash, RuntimeClass: input.RuntimeClass,
		RequiredTopK: input.RequiredTopK, TaskTypes: taskTypes, GenerationType: generationType,
		ResourceTier:              input.ResourceTier,
		MinStake:                  sdk.NewCoin(input.MinStake.Denom, sdkmath.NewIntFromUint64(input.MinStake.Amount)),
		ChallengeOpenWindowBlocks: input.ChallengeOpenWindow,
		VerificationProfile: shared.VerificationProfile{
			VerificationProfileId:   input.VerificationProfile.VerificationProfileID,
			JudgmentFunctionVersion: input.VerificationProfile.JudgmentFunctionVersion,
			VerificationMode:        verificationMode, TokenScope: tokenScope,
			IncludeGeneratedSpecialTokens: input.VerificationProfile.IncludeGeneratedSpecialTokens,
			IncludePromptTokens:           input.VerificationProfile.IncludePromptTokens,
			IncludePaddingTokens:          input.VerificationProfile.IncludePaddingTokens,
			RequireOutputTokenIds:         input.VerificationProfile.RequireOutputTokenIDs,
			RequireFinishReason:           input.VerificationProfile.RequireFinishReason,
			Metrics: shared.MetricSpec{
				CompareLogprobDiff: input.VerificationProfile.Metrics.CompareLogprobDiff,
				CompareRankDelta:   input.VerificationProfile.Metrics.CompareRankDelta,
				CompareTopkJaccard: input.VerificationProfile.Metrics.CompareTopKJaccard,
				CompareUnionJs:     input.VerificationProfile.Metrics.CompareUnionJS,
				ComparedTopK:       input.VerificationProfile.Metrics.ComparedTopK,
				NumericScale:       numericScale,
			},
			CanonicalEncodingVersion:    input.VerificationProfile.CanonicalEncodingVersion,
			EvidenceSchemaHash:          evidenceSchemaHash,
			MetricAggregateProofVersion: input.VerificationProfile.MetricAggregateProofVersion,
			EvidenceSchema: shared.EvidenceSchemaV1{
				SchemaVersion:         input.VerificationProfile.EvidenceSchema.SchemaVersion,
				RequiredInferEvidence: evidenceRequirements,
			},
		},
		VerificationThresholds: input.VerificationThresholds,
		BatchVerification:      input.BatchVerification,
		PricingProfile: shared.PricingProfile{
			InitialOutputPrice: input.PricingProfile.InitialOutputPrice,
			VerifyRatioBps:     input.PricingProfile.VerifyRatioBps,
			MinOrderValue:      input.PricingProfile.MinOrderValue,
		},
		TimeoutBootstrapProfile: input.TimeoutBootstrapProfile,
		SchemaHash:              schemaHash, PreviousProfileVersion: input.PreviousProfileVersion,
		RegistrationFee: sdk.NewCoin(input.RegistrationFee.Denom, sdkmath.NewIntFromUint64(input.RegistrationFee.Amount)),
	}, nil
}

func rejectDuplicateModelProfileJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanModelProfileJSONValue(decoder); err != nil {
		return fmt.Errorf("decode model profile projection: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("model profile projection must contain one JSON object")
	}
	return nil
}

func scanModelProfileJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("JSON object key must be a string")
			}
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("duplicate JSON field %s", key)
			}
			seen[key] = struct{}{}
			if err := scanModelProfileJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return fmt.Errorf("unterminated JSON object")
		}
	case '[':
		for decoder.More() {
			if err := scanModelProfileJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return fmt.Errorf("unterminated JSON array")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}

func validateModelProfileJSONShape(data []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("decode model profile projection: %w", err)
	}
	if err := requireModelProfileFields(root,
		"batch_verification", "challenge_open_window_blocks", "generation_type",
		"manifest_hash", "min_stake", "model_id", "previous_profile_version",
		"pricing_profile", "profile_version", "registration_fee", "required_top_k", "resource_tier",
		"runtime_class", "schema_hash", "task_types", "timeout_bootstrap_profile", "tokenizer_hash",
		"verification_profile", "verification_thresholds",
	); err != nil {
		return err
	}
	for field, required := range map[string][]string{
		"batch_verification":        {"enabled", "min_sample_count", "min_valid_sample_count", "pass_min_sample_pass_ratio_bps", "reject_min_sample_reject_ratio_bps"},
		"min_stake":                 {"amount", "denom"},
		"registration_fee":          {"amount", "denom"},
		"pricing_profile":           {"initial_output_price", "min_order_value", "verify_ratio_bps"},
		"timeout_bootstrap_profile": {"bootstrap_valid_until_epoch", "commit_timeout_bootstrap_blocks", "infer_timeout_bootstrap_blocks", "verify_timeout_bootstrap_blocks"},
		"verification_thresholds": {
			"pass_abs_logprob_diff_p95_max", "pass_abs_logprob_diff_p99_max", "pass_max_missing_compared_count",
			"pass_mean_abs_logprob_diff_max", "pass_min_finite_count", "pass_rank_delta_nonzero_rate_max",
			"pass_topk_jaccard_mean_min", "pass_union_js_p99_max", "reject_abs_logprob_diff_p95_min",
			"reject_abs_logprob_diff_p99_min", "reject_mean_abs_logprob_diff_min",
			"reject_rank_delta_nonzero_rate_min", "reject_topk_jaccard_mean_max", "reject_union_js_p99_min",
		},
	} {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(root[field], &object); err != nil {
			return fmt.Errorf("%s must be a JSON object", field)
		}
		if err := requireModelProfileFields(object, required...); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
	}
	var verification map[string]json.RawMessage
	if err := json.Unmarshal(root["verification_profile"], &verification); err != nil {
		return fmt.Errorf("verification_profile must be a JSON object")
	}
	if err := requireModelProfileFields(verification,
		"canonical_encoding_version", "evidence_schema", "evidence_schema_hash", "include_generated_special_tokens",
		"include_padding_tokens", "include_prompt_tokens", "judgment_function_version",
		"metric_aggregate_proof_version", "metrics", "require_finish_reason", "require_output_token_ids",
		"token_scope", "verification_mode", "verification_profile_id",
	); err != nil {
		return fmt.Errorf("verification_profile: %w", err)
	}
	var evidenceSchema map[string]json.RawMessage
	if err := json.Unmarshal(verification["evidence_schema"], &evidenceSchema); err != nil {
		return fmt.Errorf("verification_profile.evidence_schema must be a JSON object")
	}
	if err := requireModelProfileFields(evidenceSchema, "required_infer_evidence", "schema_version"); err != nil {
		return fmt.Errorf("verification_profile.evidence_schema: %w", err)
	}
	var evidenceRequirements []map[string]json.RawMessage
	if err := json.Unmarshal(evidenceSchema["required_infer_evidence"], &evidenceRequirements); err != nil {
		return fmt.Errorf("verification_profile.evidence_schema.required_infer_evidence must be an array")
	}
	for index, requirement := range evidenceRequirements {
		if err := requireModelProfileFields(requirement, "commitment_schema_version", "evidence_kind", "max_encoded_size_bytes"); err != nil {
			return fmt.Errorf("verification_profile.evidence_schema.required_infer_evidence[%d]: %w", index, err)
		}
	}
	var metrics map[string]json.RawMessage
	if err := json.Unmarshal(verification["metrics"], &metrics); err != nil {
		return fmt.Errorf("verification_profile.metrics must be a JSON object")
	}
	if err := requireModelProfileFields(metrics,
		"compare_logprob_diff", "compare_rank_delta", "compare_topk_jaccard",
		"compare_union_js", "compared_top_k", "numeric_scale",
	); err != nil {
		return fmt.Errorf("verification_profile.metrics: %w", err)
	}
	return nil
}

func requireModelProfileFields(object map[string]json.RawMessage, fields ...string) error {
	if len(object) != len(fields) {
		return fmt.Errorf("expected exactly %d fields, got %d", len(fields), len(object))
	}
	for _, field := range fields {
		if _, exists := object[field]; !exists {
			return fmt.Errorf("required field %s is missing", field)
		}
	}
	return nil
}

func decodeModelProfileHash(field, value string, allowZero bool) ([]byte, error) {
	if len(value) != 2+sha256.Size*2 || !strings.HasPrefix(value, "0x") || strings.ToLower(value) != value {
		return nil, fmt.Errorf("%s must be lowercase 0x-prefixed Hash32", field)
	}
	decoded, err := hex.DecodeString(value[2:])
	if err != nil {
		return nil, fmt.Errorf("%s must be lowercase 0x-prefixed Hash32", field)
	}
	if !allowZero && bytes.Equal(decoded, make([]byte, sha256.Size)) {
		return nil, fmt.Errorf("%s must be non-zero", field)
	}
	return decoded, nil
}

func parseModelProfileTaskType(value string) (shared.TaskType, error) {
	switch value {
	case "TEXT_GENERATION":
		return shared.TaskType_TASK_TYPE_TEXT_GENERATION, nil
	case "CHAT":
		return shared.TaskType_TASK_TYPE_CHAT, nil
	case "EMBEDDING":
		return shared.TaskType_TASK_TYPE_EMBEDDING, nil
	case "CLASSIFICATION":
		return shared.TaskType_TASK_TYPE_CLASSIFICATION, nil
	case "IMAGE_GENERATION":
		return shared.TaskType_TASK_TYPE_IMAGE_GENERATION, nil
	case "MULTIMODAL":
		return shared.TaskType_TASK_TYPE_MULTIMODAL, nil
	default:
		return shared.TaskType_TASK_TYPE_UNSPECIFIED, fmt.Errorf("unsupported task_type %q", value)
	}
}

func parseModelProfileEvidenceKind(value string) (shared.EvidenceKind, error) {
	switch value {
	case "WORKER_VALUE_OPENING":
		return shared.EvidenceKind_EVIDENCE_KIND_WORKER_VALUE_OPENING, nil
	case "VERIFIER_VALUE_OPENING":
		return shared.EvidenceKind_EVIDENCE_KIND_VERIFIER_VALUE_OPENING, nil
	case "SETTLEMENT_ROOT_OPENING":
		return shared.EvidenceKind_EVIDENCE_KIND_SETTLEMENT_ROOT_OPENING, nil
	default:
		return shared.EvidenceKind_EVIDENCE_KIND_UNSPECIFIED, fmt.Errorf("unsupported evidence_kind %q", value)
	}
}

func parseModelProfileGenerationType(value string) (shared.GenerationType, error) {
	switch value {
	case "DETERMINISTIC":
		return shared.GenerationType_GENERATION_TYPE_DETERMINISTIC, nil
	case "SAMPLED":
		return shared.GenerationType_GENERATION_TYPE_SAMPLED, nil
	default:
		return shared.GenerationType_GENERATION_TYPE_UNSPECIFIED, fmt.Errorf("unsupported generation_type %q", value)
	}
}

func parseModelProfileVerificationMode(value string) (shared.VerificationMode, error) {
	switch value {
	case "SINGLE_SAMPLE":
		return shared.VerificationMode_VERIFICATION_MODE_SINGLE_SAMPLE, nil
	case "BATCH_SAMPLES":
		return shared.VerificationMode_VERIFICATION_MODE_BATCH_SAMPLES, nil
	default:
		return shared.VerificationMode_VERIFICATION_MODE_UNSPECIFIED, fmt.Errorf("unsupported verification_mode %q", value)
	}
}

func parseModelProfileTokenScope(value string) (shared.TokenScope, error) {
	if value == "ALL_GENERATED_OUTPUT_TOKENS" {
		return shared.TokenScope_TOKEN_SCOPE_ALL_GENERATED_OUTPUT_TOKENS, nil
	}
	return shared.TokenScope_TOKEN_SCOPE_UNSPECIFIED, fmt.Errorf("unsupported token_scope %q", value)
}

func parseModelProfileNumericScale(value string) (shared.NumericScale, error) {
	if value == "FP_1E6" {
		return shared.NumericScale_NUMERIC_SCALE_FP_1E6, nil
	}
	return shared.NumericScale_NUMERIC_SCALE_UNSPECIFIED, fmt.Errorf("unsupported numeric_scale %q", value)
}
