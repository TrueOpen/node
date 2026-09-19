package types_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

type generationParamsGoldenV1 struct {
	Schema             string                            `json:"schema"`
	ModelID            string                            `json:"model_id"`
	ProfileVersion     uint32                            `json:"profile_version"`
	TaskType           string                            `json:"task_type"`
	OutputBudgetBucket uint32                            `json:"output_budget_bucket"`
	Params             tasktypes.GenerationParamsV1      `json:"params"`
	Limits             tasktypes.GenerationLimitParamsV1 `json:"limits"`
	CanonicalJSON      string                            `json:"canonical_json"`
	DigestHex          string                            `json:"digest_hex"`
}

// Regenerating: run the package tests with TRUEOPEN_REGEN_FIXTURES=1. The run
// rewrites the derived canonical JSON and digest and then fails on purpose,
// because a regenerated consensus fixture must be diffed against the frozen
// contract before it is committed.
const generationParamsRegenEnv = "TRUEOPEN_REGEN_FIXTURES"

func TestGenerationParamsV1Golden(t *testing.T) {
	const fixturePath = "testdata/generation_params_v1.json"
	raw, err := os.ReadFile(fixturePath)
	require.NoError(t, err)
	var fixture generationParamsGoldenV1
	require.NoError(t, json.Unmarshal(raw, &fixture))
	require.Equal(t, "trueopen-task-generation-params-v1", fixture.Schema)
	require.Equal(t, "CHAT", fixture.TaskType)

	payload, err := tasktypes.CanonicalGenerationParamsV1(
		fixture.ModelID,
		fixture.ProfileVersion,
		shared.TaskType_TASK_TYPE_CHAT,
		fixture.OutputBudgetBucket,
		fixture.Params,
		fixture.Limits,
	)
	require.NoError(t, err)

	if os.Getenv(generationParamsRegenEnv) == "1" {
		regenerateGenerationParamsFixture(t, fixturePath, raw, fixture, payload)
	}
	require.Equal(t, fixture.CanonicalJSON, string(payload))

	digest, err := tasktypes.GenerationParamsDigest(
		fixture.ModelID,
		fixture.ProfileVersion,
		shared.TaskType_TASK_TYPE_CHAT,
		fixture.OutputBudgetBucket,
		fixture.Params,
		fixture.Limits,
	)
	require.NoError(t, err)
	require.Equal(t, fixture.DigestHex, hex.EncodeToString(digest))

	spec, ok := shared.DomainSpecFor(shared.DomainTaskGenerationParamsV1)
	require.True(t, ok)
	require.Equal(t, shared.FramingHV1, spec.Framing)
}

func TestGenerationParamsV1RejectsNonCanonicalInputs(t *testing.T) {
	limits := tasktypes.DefaultTaskParams().Generation
	valid := tasktypes.GenerationParamsV1{
		GenerationParamsSchemaVersion: tasktypes.GenerationParamsSchemaVersionV1,
		MaxOutputTokens:               128,
		MaxOutputDuration:             10_000,
		DecodingParams: tasktypes.DecodingParamsV1{
			TemperatureMilli:     1_000,
			TopPPpm:              1_000_000,
			RepetitionPenaltyPpm: 1_000_000,
			StopSequences:        []string{"a", "b"},
			StopTokenIds:         []uint32{1, 2},
		},
	}

	assertRejected := func(t *testing.T, params tasktypes.GenerationParamsV1) {
		t.Helper()
		_, err := tasktypes.CanonicalGenerationParamsV1(
			"qwen3-test", 1, shared.TaskType_TASK_TYPE_CHAT, 1, params, limits,
		)
		require.Error(t, err)
	}

	t.Run("unsorted stop sequences", func(t *testing.T) {
		params := valid
		params.DecodingParams.StopSequences = []string{"b", "a"}
		assertRejected(t, params)
	})
	t.Run("duplicate stop token", func(t *testing.T) {
		params := valid
		params.DecodingParams.StopTokenIds = []uint32{1, 1}
		assertRejected(t, params)
	})
	t.Run("implicit top p default", func(t *testing.T) {
		params := valid
		params.DecodingParams.TopPPpm = 0
		assertRejected(t, params)
	})
	t.Run("out of range penalty", func(t *testing.T) {
		params := valid
		params.DecodingParams.PresencePenaltyMilli = tasktypes.PenaltyMilliMaxV1 + 1
		assertRejected(t, params)
	})
	t.Run("unsupported task type", func(t *testing.T) {
		_, err := tasktypes.CanonicalGenerationParamsV1(
			"qwen3-test", 1, shared.TaskType_TASK_TYPE_EMBEDDING, 1, valid, limits,
		)
		require.Error(t, err)
	})
}

// regenerateGenerationParamsFixture rewrites the two derived fields in place.
// Everything else in the file is input and is left untouched, so the diff a
// reviewer reads is exactly the consensus bytes that moved.
func regenerateGenerationParamsFixture(
	t *testing.T,
	path string,
	raw []byte,
	fixture generationParamsGoldenV1,
	payload []byte,
) {
	t.Helper()

	digest, err := tasktypes.GenerationParamsDigest(
		fixture.ModelID,
		fixture.ProfileVersion,
		shared.TaskType_TASK_TYPE_CHAT,
		fixture.OutputBudgetBucket,
		fixture.Params,
		fixture.Limits,
	)
	require.NoError(t, err)

	// Decode into an ordered-agnostic map so the untouched keys keep whatever
	// the published file carried.
	var document map[string]any
	require.NoError(t, json.Unmarshal(raw, &document))
	document["canonical_json"] = string(payload)
	document["digest_hex"] = hex.EncodeToString(digest)

	encoded, err := json.MarshalIndent(document, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, append(encoded, '\n'), 0o644))
	t.Fatalf("regenerated %s; unset %s and review the diff against the frozen contract before committing",
		path, generationParamsRegenEnv)
}
