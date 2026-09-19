package types

import (
	"fmt"
	"unicode/utf8"

	shared "github.com/TrueOpen/node/x/shared/types"
)

const (
	GenerationParamsSchemaVersionV1 = uint32(1)
	TemperatureMilliMaxV1           = uint32(2_000)
	TopPPMMinV1                     = uint32(1)
	TopPPMMaxV1                     = uint32(1_000_000)
	PenaltyMilliMinV1               = int32(-2_000)
	PenaltyMilliMaxV1               = int32(2_000)
	RepetitionPenaltyPPMMinV1       = uint32(100_000)
	RepetitionPenaltyPPMMaxV1       = uint32(2_000_000)
)

// CanonicalGenerationParamsV1 returns the exact canonical_json_v1 payload
// frozen by keeper_api_contract.md §5.6. Defaults are explicit order facts: this
// helper never fills runtime defaults and never reads mutable profile state.
func CanonicalGenerationParamsV1(
	modelID string,
	profileVersion uint32,
	taskType shared.TaskType,
	outputBudgetBucket uint32,
	params GenerationParamsV1,
	limits GenerationLimitParamsV1,
) ([]byte, error) {
	if err := validateGenerationParams(limits); err != nil {
		return nil, err
	}
	if err := shared.ValidateModelID(modelID); err != nil {
		return nil, err
	}
	if profileVersion == 0 {
		return nil, fmt.Errorf("profile_version must be positive")
	}
	taskTypeName, err := generationTaskTypeName(taskType)
	if err != nil {
		return nil, err
	}
	if err := validateGenerationParamsProtocolV1(params); err != nil {
		return nil, err
	}
	decoding := params.DecodingParams
	if decoding.TopK > limits.TopKMax {
		return nil, fmt.Errorf("top_k exceeds registered limit %d", limits.TopKMax)
	}
	if uint64(len(decoding.StopSequences)) > uint64(limits.StopSequenceMaxItems) {
		return nil, fmt.Errorf("stop_sequences exceeds registered item limit %d", limits.StopSequenceMaxItems)
	}
	stopSequences := append([]string{}, decoding.StopSequences...)
	var stopSequenceBytes uint64
	for index, value := range stopSequences {
		if uint64(len(value)) > uint64(limits.StopSequenceMaxBytesEach) {
			return nil, fmt.Errorf("stop_sequences[%d] exceeds registered byte limit %d", index, limits.StopSequenceMaxBytesEach)
		}
		stopSequenceBytes += uint64(len(value))
		if stopSequenceBytes > uint64(limits.StopSequenceMaxTotalBytes) {
			return nil, fmt.Errorf("stop_sequences exceeds registered total byte limit %d", limits.StopSequenceMaxTotalBytes)
		}
	}
	if uint64(len(decoding.StopTokenIds)) > uint64(limits.StopTokenMaxItems) {
		return nil, fmt.Errorf("stop_token_ids exceeds registered item limit %d", limits.StopTokenMaxItems)
	}
	stopTokenIDs := make([]any, len(decoding.StopTokenIds))
	for index, value := range decoding.StopTokenIds {
		stopTokenIDs[index] = value
	}

	return shared.CanonicalJSONV1(map[string]any{
		"decoding_params": map[string]any{
			"frequency_penalty_milli": decoding.FrequencyPenaltyMilli,
			"presence_penalty_milli":  decoding.PresencePenaltyMilli,
			"repetition_penalty_ppm":  decoding.RepetitionPenaltyPpm,
			"sampling_enabled":        decoding.SamplingEnabled,
			"seed":                    decoding.Seed,
			"stop_sequences":          stopSequences,
			"stop_token_ids":          stopTokenIDs,
			"temperature_milli":       decoding.TemperatureMilli,
			"top_k":                   decoding.TopK,
			"top_p_ppm":               decoding.TopPPpm,
		},
		"generation_params_schema_version": params.GenerationParamsSchemaVersion,
		"max_output_duration":              params.MaxOutputDuration,
		"max_output_tokens":                params.MaxOutputTokens,
		"model_id":                         modelID,
		"output_budget_bucket":             outputBudgetBucket,
		"profile_version":                  profileVersion,
		"task_type":                        taskTypeName,
	})
}

// validateGenerationParamsProtocolV1 enforces the profile-independent Task V1
// rules shared by the H_FIELDS TaskOrder projection and the H_V1 generation
// params projection. Profile-specific caps remain in CanonicalGenerationParamsV1.
func validateGenerationParamsProtocolV1(params GenerationParamsV1) error {
	if params.GenerationParamsSchemaVersion != GenerationParamsSchemaVersionV1 {
		return fmt.Errorf("generation_params_schema_version must be %d", GenerationParamsSchemaVersionV1)
	}
	if params.MaxOutputTokens == 0 || params.MaxOutputDuration == 0 {
		return fmt.Errorf("max_output_tokens and max_output_duration must be positive")
	}
	decoding := params.DecodingParams
	if decoding.TemperatureMilli > TemperatureMilliMaxV1 {
		return fmt.Errorf("temperature_milli exceeds %d", TemperatureMilliMaxV1)
	}
	if decoding.TopPPpm < TopPPMMinV1 || decoding.TopPPpm > TopPPMMaxV1 {
		return fmt.Errorf("top_p_ppm must be in %d..%d", TopPPMMinV1, TopPPMMaxV1)
	}
	if decoding.PresencePenaltyMilli < PenaltyMilliMinV1 || decoding.PresencePenaltyMilli > PenaltyMilliMaxV1 ||
		decoding.FrequencyPenaltyMilli < PenaltyMilliMinV1 || decoding.FrequencyPenaltyMilli > PenaltyMilliMaxV1 {
		return fmt.Errorf("presence and frequency penalties must be in %d..%d", PenaltyMilliMinV1, PenaltyMilliMaxV1)
	}
	if decoding.RepetitionPenaltyPpm < RepetitionPenaltyPPMMinV1 || decoding.RepetitionPenaltyPpm > RepetitionPenaltyPPMMaxV1 {
		return fmt.Errorf("repetition_penalty_ppm must be in %d..%d", RepetitionPenaltyPPMMinV1, RepetitionPenaltyPPMMaxV1)
	}
	for index, value := range decoding.StopSequences {
		if !utf8.ValidString(value) {
			return fmt.Errorf("stop_sequences[%d] must be valid UTF-8", index)
		}
		if index > 0 && decoding.StopSequences[index-1] >= value {
			return fmt.Errorf("stop_sequences must be UTF-8 byte ascending and unique")
		}
	}
	for index, value := range decoding.StopTokenIds {
		if index > 0 && decoding.StopTokenIds[index-1] >= value {
			return fmt.Errorf("stop_token_ids must be numerically ascending and unique")
		}
	}
	return nil
}

// GenerationParamsDigest derives the task-level generation commitment. Once a
// TaskAssignmentState stores this value, Worker, Verifier and settlement paths
// copy it through unchanged; they never recompute historical assignments from
// current defaults or parameters.
func GenerationParamsDigest(
	modelID string,
	profileVersion uint32,
	taskType shared.TaskType,
	outputBudgetBucket uint32,
	params GenerationParamsV1,
	limits GenerationLimitParamsV1,
) ([]byte, error) {
	payload, err := CanonicalGenerationParamsV1(modelID, profileVersion, taskType, outputBudgetBucket, params, limits)
	if err != nil {
		return nil, err
	}
	return shared.PayloadHashV1(shared.MustDomain(shared.DomainTaskGenerationParamsV1), payload)
}

func generationTaskTypeName(taskType shared.TaskType) (string, error) {
	switch taskType {
	case shared.TaskType_TASK_TYPE_TEXT_GENERATION:
		return "TEXT_GENERATION", nil
	case shared.TaskType_TASK_TYPE_CHAT:
		return "CHAT", nil
	default:
		return "", fmt.Errorf("task_type %s is unsupported by GenerationParamsV1", taskType)
	}
}
