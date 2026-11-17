package types

import (
	"encoding/hex"
	"reflect"
	"strings"
	"testing"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/stretchr/testify/require"
)

const taskParamsGoldenChainID = "trueopen-task-params-golden"

func TestDefaultTaskParamsValidate(t *testing.T) {
	params := DefaultTaskParams()
	require.NoError(t, params.Validate())
	require.NoError(t, ValidateVerifyOpenClock(params.Deadlines, 100, 100))
}

func TestTaskParamsValidateCrossFieldBounds(t *testing.T) {
	t.Run("weight sum", func(t *testing.T) {
		params := DefaultTaskParams()
		params.Weights.WorkerStakeWeightPpm--
		require.ErrorContains(t, params.Validate(), "worker weights")
	})
	t.Run("version catalog order", func(t *testing.T) {
		params := DefaultTaskParams()
		params.Evidence.EvidenceSchemaVersionSupported = []string{"v2", "v1"}
		require.ErrorContains(t, params.Validate(), "sorted and unique")
	})
	t.Run("generation total", func(t *testing.T) {
		params := DefaultTaskParams()
		params.Generation.StopSequenceMaxTotalBytes = 4096
		params.Generation.StopSequenceMaxItems = 1
		require.ErrorContains(t, params.Validate(), "items times bytes_each")
	})
	t.Run("verify-open clock", func(t *testing.T) {
		params := DefaultTaskParams()
		params.Deadlines.VerifyOpenDeadlineBlocks = 310
		require.Error(t, ValidateVerifyOpenClock(params.Deadlines, 100, 100))
	})
	t.Run("collection covers commit reveal and settle", func(t *testing.T) {
		params := DefaultTaskParams()
		params.Deadlines.CollectionWindowBlocks = 39
		require.ErrorContains(t, params.Validate(), "collection_window_blocks")
		params.Deadlines.CollectionWindowBlocks = 40
		require.NoError(t, params.Validate())
	})
	t.Run("cancel order gas guard", func(t *testing.T) {
		params := DefaultTaskParams()
		params.Session.CancelOrderMinGas = 0
		require.ErrorContains(t, params.Validate(), "cancel_order_min_gas")
		params.Session.CancelOrderMinGas = MaxCancelOrderMinGasLimit + 1
		require.ErrorContains(t, params.Validate(), "cancel_order_min_gas")
	})
	t.Run("fresh schema limits", func(t *testing.T) {
		params := DefaultTaskParams()
		params.Session.SessionChangeDelayBlocks = 0
		require.ErrorContains(t, params.Validate(), "session_change_delay_blocks")

		params = DefaultTaskParams()
		params.Evidence.MaxOutputMmrLeaves = 0
		require.ErrorContains(t, params.Validate(), "max_output_mmr_leaves")

		params = DefaultTaskParams()
		params.Evidence.MinOutputStreamFrameBytes = 0
		require.ErrorContains(t, params.Validate(), "min_output_stream_frame_bytes")

		params = DefaultTaskParams()
		params.Evidence.MaxWorkerEvidenceBytesV1 = 0
		require.ErrorContains(t, params.Validate(), "max_worker_evidence_bytes_v1")

		params = DefaultTaskParams()
		params.Generation.MaxOutputTokens = MaxGenerationOutputTokensLimit + 1
		require.ErrorContains(t, params.Validate(), "generation.max_output_tokens")
	})
}

func TestTaskParamsHashCoversGroupedFields(t *testing.T) {
	base := DefaultTaskParams()
	baseHash, err := TaskParamsHashV1("trueopen-test", 1, base)
	require.NoError(t, err)
	mutated := base
	mutated.Batch.MaxBatchCommitItems++
	mutatedHash, err := TaskParamsHashV1("trueopen-test", 1, mutated)
	require.NoError(t, err)
	require.NotEqual(t, baseHash, mutatedHash)
	versionedHash, err := TaskParamsHashV1("trueopen-test", 2, base)
	require.NoError(t, err)
	require.NotEqual(t, baseHash, versionedHash)

	typed := CanonicalTaskParamsTypedFields(base)
	require.Len(t, typed, 13)
	require.Equal(t, uint8(0), typed[0].Depth())
	for index, field := range typed[1:] {
		// typed[6] is EvidenceLimitParamsV1, whose two repeated fields are each a
		// REPEATED_V1 frame inside the section frame, so that section is one level
		// deeper than the nine all-scalar ones.
		want := uint8(1)
		if index+1 == 1 || index+1 == 6 || index+1 == 10 || index+1 == 11 || index+1 == 12 {
			want = 2
		}
		if index+1 == 12 {
			want = 4
		}
		require.Equal(t, want, field.Depth(), "typed[%d]", index+1)
	}
	body := shared.NewCanonicalFrameBuilderV1().Field(typed...).Build()
	require.NoError(t, body.Err())
	require.Equal(t, uint8(5), body.Depth())

}

func TestTaskParamsHashCoversEveryField(t *testing.T) {
	base := DefaultTaskParams()
	unmutated, err := TaskParamsHashV1(taskParamsGoldenChainID, 1, base)
	require.NoError(t, err)
	seen := map[string]string{hex.EncodeToString(unmutated): "<unmutated>"}
	mutations := taskParamsFieldMutations(t)
	require.NotEmpty(t, mutations)
	for path, mutated := range mutations {
		digest := taskParamsHashUncheckedForTest(t, taskParamsGoldenChainID, 1, mutated)
		hash := hex.EncodeToString(digest)
		if previous, exists := seen[hash]; exists {
			t.Fatalf("params hash collision: %s and %s produce %s", previous, path, hash)
		}
		seen[hash] = path
	}
	require.Len(t, seen, len(mutations)+1)
}

func taskParamsHashUncheckedForTest(t *testing.T, chainID string, version uint64, params TaskParamsV1) []byte {
	t.Helper()
	body := shared.NewCanonicalFrameBuilderV1().Field(CanonicalTaskParamsTypedFields(params)...).Build()
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainTaskParamsV1)).
		Raw([]byte(chainID), shared.Uint64BE(version)).Nested(body).Sum()
	require.NoError(t, err)
	return digest
}

func taskParamsFieldMutations(t *testing.T) map[string]TaskParamsV1 {
	t.Helper()
	var paths []string
	scratch := DefaultTaskParams()
	walkTaskParamsLeaves(t, "params", reflect.ValueOf(&scratch).Elem(), func(path string, _ func()) {
		paths = append(paths, path)
	})
	mutations := make(map[string]TaskParamsV1, len(paths))
	for _, target := range paths {
		mutated := DefaultTaskParams()
		applied := false
		walkTaskParamsLeaves(t, "params", reflect.ValueOf(&mutated).Elem(), func(path string, mutate func()) {
			if path == target && !applied {
				mutate()
				applied = true
			}
		})
		require.True(t, applied, "no mutation applied for %s", target)
		mutations[target] = mutated
	}
	return mutations
}

func walkTaskParamsLeaves(t *testing.T, path string, value reflect.Value, visit func(string, func())) {
	t.Helper()
	switch value.Kind() {
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			walkTaskParamsLeaves(t, path+"."+value.Type().Field(i).Name, value.Field(i), visit)
		}
	case reflect.Pointer:
		walkTaskParamsLeaves(t, path, value.Elem(), visit)
	case reflect.Int32, reflect.Int64:
		visit(path, func() { value.SetInt(value.Int() + 1) })
	case reflect.Uint32, reflect.Uint64:
		visit(path, func() { value.SetUint(value.Uint() + 1) })
	case reflect.Bool:
		visit(path, func() { value.SetBool(!value.Bool()) })
	case reflect.String:
		visit(path, func() {
			if strings.HasSuffix(path, ".AtomicUnits") {
				if value.String() == "0" {
					value.SetString("1")
				} else {
					value.SetString(value.String() + "0")
				}
				return
			}
			value.SetString(value.String() + "x")
		})
	case reflect.Slice:
		for i := 0; i < value.Len(); i++ {
			walkTaskParamsLeaves(t, path+"[item]", value.Index(i), visit)
		}
		visit(path+"[length]", func() {
			element := reflect.Zero(value.Type().Elem())
			if value.Len() != 0 {
				element = value.Index(0)
			}
			value.Set(reflect.Append(value, element))
		})
	default:
		t.Fatalf("unsupported params field %s kind %s", path, value.Kind())
	}
}

// flatEvidenceFieldsForTest is EvidenceLimitParamsV1 with both repeated fields
// written as a loose uint32_be count followed by sibling elements, i.e. without
// the REPEATED_V1 frame the canonical encoding requires. It
// exists only so the tests can
// show this shape is not what TaskParamsHashV1 commits to.
func TestTaskParamsUpdateClassifiers(t *testing.T) {
	current := DefaultTaskParams()
	next := current
	next.Weights.SelectedVerifierCount++
	field, changed := GenesisOnlyTaskParamsChanged(current, next)
	require.True(t, changed)
	require.Equal(t, "weights.selected_verifier_count", field)

	// Windows, TTLs and caps are frozen per Task and Session, so they are free to
	// change; only the EpochTaskSummary budgets, which one process accumulates over
	// several blocks, stay fixed.
	next = current
	next.Deadlines.RevealWindowBlocks++
	next.Session.SessionIdleTtlBlocks++
	next.Proposals.MaxCandidateUnionMembersPerStage++
	next.Cleanup.TaskTerminalSummaryRetentionBlocks++
	_, changed = RuntimeImmutableTaskParamsChanged(current, next)
	require.False(t, changed)

	next = current
	next.Cleanup.MaxEpochTaskSummaryBytes++
	group, changed := RuntimeImmutableTaskParamsChanged(current, next)
	require.True(t, changed)
	require.Equal(t, "cleanup.max_epoch_task_summary_bytes", group)

	next = current
	next.Generation.TopKMax++
	_, changed = RuntimeImmutableTaskParamsChanged(current, next)
	require.False(t, changed)

	next = current
	next.Dispatch.OrderFreshnessMarginBlocks++
	_, changed = RuntimeImmutableTaskParamsChanged(current, next)
	require.False(t, changed)

	next = current
	next.Evidence.MaxOutputMmrLeaves++
	field, changed = GenesisOnlyTaskParamsChanged(current, next)
	require.True(t, changed)
	require.Equal(t, "evidence.max_output_mmr_leaves", field)

}
