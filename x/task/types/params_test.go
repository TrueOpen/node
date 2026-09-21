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
const taskParamsGoldenHashV1 = "9c8c8cb8646e4c88f4e7b39992f97dd5d96233e0ef8b5448a726d7993aa53de0"

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
		params.Session.AnchorFreshnessWindowBlocks = 0
		require.ErrorContains(t, params.Validate(), "anchor_freshness_window_blocks")

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
	// An external anchor for the "trueopen-test" chain the shape assertions below
	// use. Those assertions restate the preimage, so they would agree with
	// TaskParamsHashV1 after any reordering applied to both; this constant does
	// not. TestTaskParamsHashGoldenIsFrozen pins the same domain on the golden
	// chain id.
	require.Equal(t, "3397961671163207046fb2177f1bda96449446bb037c8d8be1e79c3876ecf9b3",
		hex.EncodeToString(baseHash),
		"TRUEOPEN_TASK_PARAMS_V1 is a frozen consensus preimage; moving this constant is a consensus change and must be re-checked against the §1.4 domain registry")
	mutated := base
	mutated.Batch.MaxBatchCommitItems++
	mutatedHash, err := TaskParamsHashV1("trueopen-test", 1, mutated)
	require.NoError(t, err)
	require.NotEqual(t, baseHash, mutatedHash)
	versionedHash, err := TaskParamsHashV1("trueopen-test", 2, base)
	require.NoError(t, err)
	require.NotEqual(t, baseHash, versionedHash)

	typed := CanonicalTaskParamsTypedFields(base)
	require.Len(t, typed, 12)
	require.Equal(t, uint8(0), typed[0].Depth())
	for index, field := range typed[1:] {
		// typed[6] is EvidenceLimitParamsV1, whose two repeated fields are each a
		// REPEATED_V1 frame inside the section frame, so that section is one level
		// deeper than the nine all-scalar ones.
		want := uint8(1)
		if index+1 == 6 || index+1 == 10 || index+1 == 11 {
			want = 2
		}
		require.Equal(t, want, field.Depth(), "typed[%d]", index+1)
	}
	body := shared.NewCanonicalFrameBuilderV1().Field(typed...).Build()
	require.NoError(t, body.Err())
	require.Equal(t, uint8(3), body.Depth())

	// Fully flattening the schema -- no submessage frames at all -- must not
	// reproduce the digest.
	flatFields := [][]byte{[]byte("trueopen-test"), shared.Uint64BE(1)}
	flatFields = append(flatFields, flatTaskParamsFieldsForTest(base)...)
	require.NotEqual(t, baseHash, shared.CanonicalHashBytes(
		shared.MustDomain(shared.DomainTaskParamsV1),
		flatFields...,
	))

	// Framing the submessages but writing EvidenceLimitParamsV1's two repeated
	// fields as a loose count plus sibling elements -- the old flattened shape -- must
	// not reproduce it either. The default catalogs are empty, so this is the
	// weakest possible difference: two 4-byte counts against two REPEATED_V1
	// frames.
	require.NotEqual(t, baseHash, flattenedRepeatedTaskParamsHashForTest(t, "trueopen-test", 1, base))
}

func TestTaskParamsHashFramesEvidenceCatalogsAsRepeatedValues(t *testing.T) {
	populated := DefaultTaskParams()
	populated.Evidence.EvidenceSchemaVersionSupported = []string{"ES_V1", "ES_V2"}
	populated.Evidence.LeafOrderingVersionSupported = []string{"LO_V1"}
	require.NoError(t, populated.Validate())

	// The elements live inside a REPEATED_V1 frame, not as siblings of the
	// evidence section's scalars. With a non-empty catalog the two shapes differ
	// in both framing and length, and the digest must follow the framed one.
	hash, err := TaskParamsHashV1("trueopen-test", 1, populated)
	require.NoError(t, err)
	require.NotEqual(t, hash, flattenedRepeatedTaskParamsHashForTest(t, "trueopen-test", 1, populated))

	// The catalogs are two independent lists: redistributing elements between
	// them is a different parameter set and must be a different digest.
	left := DefaultTaskParams()
	left.Evidence.EvidenceSchemaVersionSupported = []string{"a", "b"}
	require.NoError(t, left.Validate())
	right := DefaultTaskParams()
	right.Evidence.EvidenceSchemaVersionSupported = []string{"a"}
	right.Evidence.LeafOrderingVersionSupported = []string{"b"}
	require.NoError(t, right.Validate())
	leftHash, err := TaskParamsHashV1("trueopen-test", 1, left)
	require.NoError(t, err)
	rightHash, err := TaskParamsHashV1("trueopen-test", 1, right)
	require.NoError(t, err)
	require.NotEqual(t, leftHash, rightHash)

	// The count is synthesised from the slice, so a nil catalog and an explicitly
	// empty one are the same value rather than two encodings.
	empty := DefaultTaskParams()
	empty.Evidence.EvidenceSchemaVersionSupported = []string{}
	empty.Evidence.LeafOrderingVersionSupported = []string{}
	require.NoError(t, empty.Validate())
	emptyHash, err := TaskParamsHashV1("trueopen-test", 1, empty)
	require.NoError(t, err)
	nilHash, err := TaskParamsHashV1("trueopen-test", 1, DefaultTaskParams())
	require.NoError(t, err)
	require.Equal(t, nilHash, emptyHash)
}

// flattenedRepeatedTaskParamsHashForTest reproduces the pre-#143 evidence
// encoding: the section frame survives, but the repeated element count and the
// elements are written as siblings of the section's scalars instead of as one
// REPEATED_V1 frame.
func flattenedRepeatedTaskParamsHashForTest(t *testing.T, chainID string, version uint64, p TaskParamsV1) []byte {
	t.Helper()
	sections := canonicalTaskParamsSectionFields(p)
	fields := make([]shared.CanonicalFieldV1, 0, len(sections)+1)
	fields = append(fields, shared.RawCanonicalFieldV1(shared.Uint32BE(p.SchemaVersion)))
	for index, section := range sections {
		// sections[5] is proto field 7, EvidenceLimitParamsV1.
		if index == 5 {
			section = shared.RawCanonicalFieldsV1(flatEvidenceFieldsForTest(p)...)
		}
		fields = append(fields, shared.NestedCanonicalFieldV1(
			shared.NewCanonicalFrameBuilderV1().Field(section...).Build(),
		))
	}
	body := shared.NewCanonicalFrameBuilderV1().Field(fields...).Build()
	hash, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainTaskParamsV1)).
		Raw([]byte(chainID), shared.Uint64BE(version)).
		Nested(body).
		Sum()
	require.NoError(t, err)
	return hash
}

func TestTaskParamsHashGoldenIsFrozen(t *testing.T) {
	hash, err := TaskParamsHashV1(taskParamsGoldenChainID, 1, DefaultTaskParams())
	require.NoError(t, err)
	require.Len(t, hash, 32)
	require.Equal(t, taskParamsGoldenHashV1, hex.EncodeToString(hash))
}

func TestTaskParamsHashCoversEveryField(t *testing.T) {
	base := DefaultTaskParams()
	unmutated, err := TaskParamsHashV1(taskParamsGoldenChainID, 1, base)
	require.NoError(t, err)
	seen := map[string]string{hex.EncodeToString(unmutated): "<unmutated>"}
	mutations := taskParamsFieldMutations(t)
	require.Len(t, mutations, 84)
	for path, mutated := range mutations {
		digest := taskParamsHashUncheckedForTest(t, taskParamsGoldenChainID, 1, mutated)
		hash := hex.EncodeToString(digest)
		if previous, exists := seen[hash]; exists {
			t.Fatalf("params hash collision: %s and %s produce %s", previous, path, hash)
		}
		seen[hash] = path
	}
	require.Len(t, seen, 85)
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
			value.Set(reflect.Append(value, reflect.ValueOf("v1")))
		})
	default:
		t.Fatalf("unsupported params field %s kind %s", path, value.Kind())
	}
}

// flatEvidenceFieldsForTest is EvidenceLimitParamsV1 with both repeated fields
// written as a loose uint32_be count followed by sibling elements, i.e. without
// the REPEATED_V1 frame the canonical encoding contract requires. It
// exists only so the tests can
// show this shape is not what TaskParamsHashV1 commits to.
func flatEvidenceFieldsForTest(p TaskParamsV1) [][]byte {
	fields := [][]byte{
		shared.Uint64BE(p.Evidence.MaxEvidenceRetentionBlocks),
		shared.Uint32BE(p.Evidence.MaxInferEvidenceCommitmentsPerReceipt),
		shared.Uint64BE(p.Evidence.MaxInferReceiptCommitmentBytes),
		shared.Uint32BE(p.Evidence.MaxSupportedEvidenceSchemaVersions),
		shared.Uint32BE(p.Evidence.MaxSupportedLeafOrderingVersions),
		shared.Uint32BE(uint32(len(p.Evidence.EvidenceSchemaVersionSupported))),
	}
	for _, value := range p.Evidence.EvidenceSchemaVersionSupported {
		fields = append(fields, []byte(value))
	}
	fields = append(fields, shared.Uint32BE(uint32(len(p.Evidence.LeafOrderingVersionSupported))))
	for _, value := range p.Evidence.LeafOrderingVersionSupported {
		fields = append(fields, []byte(value))
	}
	fields = append(fields,
		shared.Uint64BE(p.Evidence.MaxOutputMmrLeaves),
		shared.Uint32BE(p.Evidence.MinOutputStreamFrameBytes),
		shared.Uint64BE(p.Evidence.MaxWorkerEvidenceBytesV1),
	)
	return fields
}

func flatTaskParamsFieldsForTest(p TaskParamsV1) [][]byte {
	fields := [][]byte{
		shared.Uint32BE(p.SchemaVersion),
		shared.Uint32BE(p.Session.MaxOrderSequencesPerActiveSession),
		shared.Uint64BE(p.Session.SessionIdleTtlBlocks),
		shared.Uint64BE(p.Session.SessionCloseTtlBlocks),
		shared.Uint64BE(p.Session.SessionOrderRetentionBlocks),
		shared.Uint32BE(p.Session.MaxSessionHistoryPruneItemsPerBlock),
		shared.Uint64BE(p.Session.SessionTerminalSummaryRetentionBlocks),
		shared.Uint32BE(p.Session.MaxSessionTerminalSummaryPruneItemsPerBlock),
		shared.Uint32BE(p.Session.MaxSessionSweepPerBlock),
		shared.Uint64BE(p.Session.CancelOrderMinGas),
		shared.Uint64BE(p.Session.AnchorFreshnessWindowBlocks),
		shared.Uint64BE(p.Deadlines.VerifyOpenDeadlineBlocks),
		shared.Uint64BE(p.Deadlines.CommitWindowBlocks),
		shared.Uint64BE(p.Deadlines.RevealWindowBlocks),
		shared.Uint64BE(p.Deadlines.CollectionWindowBlocks),
		shared.Uint64BE(p.Deadlines.SettleMarginBlocks),
		shared.Uint64BE(p.Deadlines.SelfRescueMarginBlocks),
		shared.Uint32BE(p.Deadlines.MaxDeadlineSweepPerBlock),
		shared.Uint32BE(p.Proposals.MaxCandidateHandraisesPerProposal),
		shared.Uint64BE(p.Proposals.MaxCandidateProposalBytes),
		shared.Uint32BE(p.Proposals.MaxCandidateAcceptedProposalsPerStage),
		shared.Uint32BE(p.Proposals.MaxCandidateUnionMembersPerStage),
		shared.Uint32BE(p.Proposals.MaxCandidateStageFinalizeTasksPerBlock),
		shared.Uint32BE(p.Proposals.MaxCandidateStageFinalizeMembersPerBlock),
		shared.Uint32BE(p.Weights.WorkerStakeWeightPpm),
		shared.Uint32BE(p.Weights.WorkerPerformanceWeightPpm),
		shared.Uint32BE(p.Weights.VerifierStakeWeightPpm),
		shared.Uint32BE(p.Weights.VerifierPerformanceWeightPpm),
		shared.Uint32BE(p.Weights.VerifierCandidateRatioPpm),
		shared.Uint32BE(p.Weights.VerifierCandidateWindowMin),
		shared.Uint32BE(p.Weights.VerifierCandidateWindowMax),
		shared.Uint32BE(p.Weights.SelectedVerifierCount),
		shared.Uint32BE(p.Weights.KStake),
		shared.Uint32BE(p.Weights.CandidateWeightPpmMax),
		shared.Uint32BE(p.Weights.MaxWeightedDrawAttemptsPerSelection),
		shared.Uint32BE(p.Verification.WorkerInferTimeoutSlashBps),
		shared.Uint32BE(p.Verification.ResultRevealMissingSlashBps),
		shared.Uint32BE(p.Verification.BuilderFaultSlashBps),
	}
	fields = append(fields, flatEvidenceFieldsForTest(p)...)
	return append(fields,
		shared.Uint32BE(p.Cleanup.MaxTaskCleanupItemsPerBlock),
		shared.Uint64BE(p.Cleanup.TaskTerminalSummaryRetentionBlocks),
		shared.Uint32BE(p.Cleanup.MaxTaskTerminalSummaryPruneItemsPerBlock),
		shared.Uint32BE(p.Cleanup.MaxEpochTaskSummaryItemsPerBlock),
		shared.Uint32BE(p.Cleanup.MaxEpochTaskSummarySupportCandidates),
		shared.Uint64BE(p.Cleanup.MaxEpochTaskSummaryBytes),
		shared.Uint32BE(p.Cleanup.MaxTaskFailureClassPruneItemsPerBlock),
		shared.Uint32BE(p.Batch.MaxBatchCommitItems),
		shared.Uint32BE(p.Batch.MaxBatchResultItems),
		shared.Uint64BE(p.Batch.MaxBatchCommitBytes),
		shared.Uint64BE(p.Batch.MaxBatchResultBytes),
		shared.Uint32BE(p.Generation.StopSequenceMaxItems),
		shared.Uint32BE(p.Generation.StopSequenceMaxBytesEach),
		shared.Uint32BE(p.Generation.StopSequenceMaxTotalBytes),
		shared.Uint32BE(p.Generation.StopTokenMaxItems),
		shared.Uint32BE(p.Generation.TopKMax),
		shared.Uint64BE(p.Generation.MaxOutputTokens),
	)
}

func TestTaskParamsUpdateClassifiers(t *testing.T) {
	current := DefaultTaskParams()
	next := current
	next.Weights.SelectedVerifierCount++
	field, changed := GenesisOnlyTaskParamsChanged(current, next)
	require.True(t, changed)
	require.Equal(t, "weights.selected_verifier_count", field)

	next = current
	next.Deadlines.RevealWindowBlocks++
	group, changed := RuntimeImmutableTaskParamsChanged(current, next)
	require.True(t, changed)
	require.Equal(t, "deadlines", group)

	next = current
	next.Generation.TopKMax++
	_, changed = RuntimeImmutableTaskParamsChanged(current, next)
	require.False(t, changed)

	next = current
	next.Session.AnchorFreshnessWindowBlocks++
	_, changed = RuntimeImmutableTaskParamsChanged(current, next)
	require.False(t, changed)

	next = current
	next.Evidence.MaxOutputMmrLeaves++
	field, changed = GenesisOnlyTaskParamsChanged(current, next)
	require.True(t, changed)
	require.Equal(t, "evidence.max_output_mmr_leaves", field)

}
