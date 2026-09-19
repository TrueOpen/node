package types_test

import (
	"bytes"
	"encoding/hex"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/internal/testutil/domainfixture"
	shared "github.com/TrueOpen/node/x/shared/types"
	taskkeeper "github.com/TrueOpen/node/x/task/keeper"
	"github.com/TrueOpen/node/x/task/types"
)

type producerTaskVector = domainfixture.Vector
type producerTaskField = domainfixture.Field

func TestTaskDomainFixtureBindsProductionHelpers(t *testing.T) {
	fixture := loadProducerTaskFixture(t)
	want := map[string]string{
		"infer_evidence_commitments_v1_empty":          "digest",
		"infer_evidence_commitments_v1_single":         "digest",
		"infer_evidence_commitments_v1_pair":           "digest",
		"infer_evidence_commitments_v1_pair_reordered": "reject",
		"verify_commit_v1":                             "digest",
		"worker_handraise_v1":                          "digest",
		"verifier_handraise_v1":                        "digest",
		"task_id_v1":                                   "digest",
		"metric_summary_v1_optional_presence":          "digest",
		"data_unavailable_aggregate_v1_optional_slots": "digest",
		"assignment_legal_set_v1":                      "digest",
		"task_builders_v1":                             "digest",
		"commit_key_v1":                                "digest",
		"selected_task_builders_v1":                    "digest",
	}
	bound := make(map[string]string, len(fixture.Vectors))

	for _, vector := range fixture.Vectors {
		vector := vector
		t.Run(vector.Name, func(t *testing.T) {
			require.NotEmpty(t, vector.Producer)
			switch vector.Name {
			case "infer_evidence_commitments_v1_empty", "infer_evidence_commitments_v1_single", "infer_evidence_commitments_v1_pair":
				got, err := types.EvidenceCommitmentsHash(producerTaskEvidenceCommitments(t, vector))
				require.NoError(t, err)
				require.Equal(t, vector.DigestHex, hex.EncodeToString(got[:]))
				bound[vector.Name] = "digest"

			case "infer_evidence_commitments_v1_pair_reordered":
				require.NotEmpty(t, vector.RejectedByProduction)
				_, err := types.EvidenceCommitmentsHash(producerTaskEvidenceCommitments(t, vector))
				require.ErrorContains(t, err, "ascending")
				bound[vector.Name] = "reject"

			case "verify_commit_v1":
				got, err := types.VerifyCommitSigningDigest(types.VerifyCommitV1{
					SchemaVersion:             uint32(producerTaskUint(t, vector, 0, "schema_version")),
					ChainId:                   producerTaskString(t, vector, 1, "chain_id"),
					TaskId:                    producerTaskBytes(t, vector, 2, "task_id"),
					VerifyRound:               uint32(producerTaskUint(t, vector, 3, "verify_round")),
					VerifierOperatorAddress:   producerTaskAddress(t, vector, 4, "verifier_operator_address"),
					ServiceAuthorizationNonce: producerTaskUint(t, vector, 5, "service_authorization_nonce"),
					CommitHash:                producerTaskBytes(t, vector, 6, "commit_hash"),
					ExpiryHeight:              producerTaskUint(t, vector, 7, "expiry_height"),
				})
				require.NoError(t, err)
				require.Equal(t, vector.DigestHex, hex.EncodeToString(got[:]))
				bound[vector.Name] = "digest"

			case "worker_handraise_v1":
				got, err := types.WorkerHandraiseSigningDigest(types.WorkerHandraiseV1{
					SchemaVersion:             uint32(producerTaskUint(t, vector, 0, "schema_version")),
					ChainId:                   producerTaskString(t, vector, 1, "chain_id"),
					TaskId:                    producerTaskBytes(t, vector, 2, "task_id"),
					TaskHash:                  producerTaskBytes(t, vector, 3, "task_hash"),
					ModelId:                   producerTaskString(t, vector, 4, "model_id"),
					ProfileVersion:            uint32(producerTaskUint(t, vector, 5, "profile_version")),
					Member:                    producerTaskMember(t, vector, 6),
					Duty:                      shared.Duty(producerTaskUint(t, vector, 7, "duty")),
					ServiceAuthorizationNonce: producerTaskUint(t, vector, 8, "service_authorization_nonce"),
					ExpiryHeight:              producerTaskUint(t, vector, 9, "expiry_height"),
				})
				require.NoError(t, err)
				require.Equal(t, vector.DigestHex, hex.EncodeToString(got[:]))
				bound[vector.Name] = "digest"

			case "verifier_handraise_v1":
				got, err := types.VerifierHandraiseSigningDigest(types.VerifierHandraiseV1{
					SchemaVersion:             uint32(producerTaskUint(t, vector, 0, "schema_version")),
					ChainId:                   producerTaskString(t, vector, 1, "chain_id"),
					TaskId:                    producerTaskBytes(t, vector, 2, "task_id"),
					VerifyRound:               uint32(producerTaskUint(t, vector, 3, "verify_round")),
					InferReceiptHash:          producerTaskBytes(t, vector, 4, "infer_receipt_hash"),
					OutputHash:                producerTaskBytes(t, vector, 5, "output_hash"),
					ModelId:                   producerTaskString(t, vector, 6, "model_id"),
					ProfileVersion:            uint32(producerTaskUint(t, vector, 7, "profile_version")),
					Member:                    producerTaskMember(t, vector, 8),
					Duty:                      shared.Duty(producerTaskUint(t, vector, 9, "duty")),
					ServiceAuthorizationNonce: producerTaskUint(t, vector, 10, "service_authorization_nonce"),
					ExpiryHeight:              producerTaskUint(t, vector, 11, "expiry_height"),
				})
				require.NoError(t, err)
				require.Equal(t, vector.DigestHex, hex.EncodeToString(got[:]))
				bound[vector.Name] = "digest"

			case "task_id_v1":
				got, err := types.DeriveTaskIDFromRawSession(
					producerTaskBytes(t, vector, 0, "session_id"),
					producerTaskUint(t, vector, 1, "order_sequence"),
				)
				require.NoError(t, err)
				require.Equal(t, vector.DigestHex, hex.EncodeToString(got[:]))
				bound[vector.Name] = "digest"

			case "metric_summary_v1_optional_presence":
				require.Len(t, vector.Fields, 1)
				got, err := types.MetricSummaryHash(producerTaskMetricSummary(t, vector.Fields[0]))
				require.NoError(t, err)
				require.Equal(t, vector.DigestHex, hex.EncodeToString(got[:]))
				bound[vector.Name] = "digest"

			case "data_unavailable_aggregate_v1_optional_slots":
				slots := producerTaskRepeated(t, vector, 7, "report_digest_slots", 6, "selected_verifier_count")
				digests := make([][]byte, 0, len(slots))
				for _, slot := range slots {
					digests = append(digests, producerTaskOptionalBytes(t, slot))
				}
				got, err := taskkeeper.DataUnavailableAggregateHash(
					producerTaskString(t, vector, 0, "chain_id"),
					producerTaskBytes(t, vector, 1, "task_id"),
					uint32(producerTaskUint(t, vector, 2, "verify_round")),
					taskkeeper.DataUnavailableBuilderAggregateFacts{
						BuilderOperatorAddress:      producerTaskAddress(t, vector, 3, "builder_operator_address"),
						ValidReportCount:            uint32(producerTaskUint(t, vector, 4, "valid_report_count")),
						RequiredReportCount:         uint32(producerTaskUint(t, vector, 5, "required_report_count")),
						ReportDigestsByVerifierSlot: digests,
					},
				)
				require.NoError(t, err)
				require.Equal(t, vector.DigestHex, hex.EncodeToString(got))
				bound[vector.Name] = "digest"

			case "assignment_legal_set_v1":
				elements := producerTaskRepeated(t, vector, 7, "candidates", 6, "candidate_count")
				taskID := producerTaskBytes(t, vector, 1, "task_id")
				facts := make([]types.TaskCandidateFactState, 0, len(elements))
				for _, element := range elements {
					facts = append(facts, producerTaskCandidate(t, element, taskID))
				}
				// Production sorts by slot; reverse the fixture order so the adapter
				// cannot accidentally validate an order-preserving stub.
				for left, right := 0, len(facts)-1; left < right; left, right = left+1, right-1 {
					facts[left], facts[right] = facts[right], facts[left]
				}
				got, err := types.AssignmentCandidateSetHash(
					producerTaskString(t, vector, 0, "chain_id"),
					types.AssignmentCandidateSetState{
						SchemaVersion:           types.AssignmentCandidateSchemaVersionV1,
						TaskId:                  taskID,
						TaskHash:                producerTaskBytes(t, vector, 2, "task_hash"),
						CandidatePoolSnapshotId: producerTaskBytes(t, vector, 3, "candidate_pool_snapshot_id"),
						CandidatePoolHash:       producerTaskBytes(t, vector, 4, "candidate_pool_hash"),
						UnionBitmapHash:         producerTaskBytes(t, vector, 5, "union_bitmap_hash"),
						CandidateCount:          uint32(producerTaskUint(t, vector, 6, "candidate_count")),
					},
					facts,
				)
				require.NoError(t, err)
				require.Equal(t, vector.DigestHex, hex.EncodeToString(got[:]))
				bound[vector.Name] = "digest"

			case "task_builders_v1":
				got, err := types.TaskBuilderSeed(
					producerTaskString(t, vector, 0, "chain_id"),
					producerTaskBytes(t, vector, 1, "task_id"),
					producerTaskBytes(t, vector, 2, "builder_set_hash"),
					producerTaskBytes(t, vector, 3, "session_anchor_block_hash"),
				)
				require.NoError(t, err)
				require.Equal(t, vector.DigestHex, hex.EncodeToString(got[:]))
				bound[vector.Name] = "digest"

			case "commit_key_v1":
				got, err := types.DeriveCommitKey(
					producerTaskString(t, vector, 0, "chain_id"),
					producerTaskBytes(t, vector, 1, "task_id"),
					uint32(producerTaskUint(t, vector, 2, "verify_round")),
					producerTaskAddress(t, vector, 3, "verifier_operator_address"),
				)
				require.NoError(t, err)
				require.Equal(t, vector.DigestHex, hex.EncodeToString(got[:]))
				bound[vector.Name] = "digest"

			case "selected_task_builders_v1":
				elements := producerTaskRepeated(t, vector, 4, "builders", -1, "")
				builders := make([]string, 0, len(elements))
				for _, element := range elements {
					require.Equal(t, "address", element.Type)
					require.NotEmpty(t, element.Bech32)
					builders = append(builders, element.Bech32)
				}
				got, err := types.SelectedTaskBuildersHash(
					producerTaskString(t, vector, 0, "chain_id"),
					producerTaskBytes(t, vector, 1, "task_id"),
					producerTaskString(t, vector, 2, "builder_set_id"),
					producerTaskBytes(t, vector, 3, "builder_set_hash"),
					builders,
				)
				require.NoError(t, err)
				require.Equal(t, vector.DigestHex, hex.EncodeToString(got))
				bound[vector.Name] = "digest"

			default:
				t.Fatalf("fixture vector %q has no production binding", vector.Name)
			}
		})
	}

	require.Equal(t, want, bound,
		"the fixture and producer bindings must cover exactly the same vector names")
}

func loadProducerTaskFixture(t *testing.T) domainfixture.Fixture {
	t.Helper()
	fixture := domainfixture.Load(t, "testdata/task_domains_v1.json")
	require.Len(t, fixture.Vectors, 14)
	return fixture
}

func producerTaskFieldAt(t *testing.T, fields []producerTaskField, index int, name string) producerTaskField {
	t.Helper()
	require.Less(t, index, len(fields))
	field := fields[index]
	require.Equal(t, name, field.Name)
	return field
}

func producerTaskVectorField(t *testing.T, vector producerTaskVector, index int, name string) producerTaskField {
	t.Helper()
	return producerTaskFieldAt(t, vector.Fields, index, name)
}

func producerTaskString(t *testing.T, vector producerTaskVector, index int, name string) string {
	t.Helper()
	field := producerTaskVectorField(t, vector, index, name)
	require.Equal(t, "string", field.Type)
	require.NotNil(t, field.UTF8)
	return *field.UTF8
}

func producerTaskBytes(t *testing.T, vector producerTaskVector, index int, name string) []byte {
	t.Helper()
	field := producerTaskVectorField(t, vector, index, name)
	require.Equal(t, "bytes", field.Type)
	return producerTaskHex(t, field)
}

func producerTaskUint(t *testing.T, vector producerTaskVector, index int, name string) uint64 {
	t.Helper()
	field := producerTaskVectorField(t, vector, index, name)
	require.Contains(t, []string{"uint32", "uint64", "enum"}, field.Type)
	require.NotNil(t, field.Value)
	return *field.Value
}

func producerTaskAddress(t *testing.T, vector producerTaskVector, index int, name string) string {
	t.Helper()
	field := producerTaskVectorField(t, vector, index, name)
	require.Equal(t, "address", field.Type)
	require.NotEmpty(t, field.Bech32)
	return field.Bech32
}

func producerTaskHex(t *testing.T, field producerTaskField) []byte {
	t.Helper()
	decoded, err := hex.DecodeString(field.Hex)
	require.NoError(t, err, field.Name)
	require.Equal(t, field.Hex, hex.EncodeToString(decoded), field.Name)
	return decoded
}

func producerTaskNested(t *testing.T, parent producerTaskField, index int, name, kind string) producerTaskField {
	t.Helper()
	require.Equal(t, "frame", parent.Type)
	field := producerTaskFieldAt(t, parent.Fields, index, name)
	require.Equal(t, kind, field.Type)
	return field
}

func producerTaskRepeated(t *testing.T, vector producerTaskVector, index int, name string, countIndex int, countName string) []producerTaskField {
	t.Helper()
	require.Len(t, vector.Fields, index+1)
	repeated := producerTaskVectorField(t, vector, index, name)
	require.Equal(t, "frame", repeated.Type)
	require.NotEmpty(t, repeated.Fields)
	count := producerTaskFieldAt(t, repeated.Fields, 0, "element_count")
	require.Equal(t, "uint32", count.Type)
	require.NotNil(t, count.Value)
	elements := repeated.Fields[1:]
	require.Equal(t, uint64(len(elements)), *count.Value)
	if countIndex >= 0 {
		require.Equal(t, uint64(len(elements)), producerTaskUint(t, vector, countIndex, countName))
	}
	return elements
}

func producerTaskEvidenceCommitments(t *testing.T, vector producerTaskVector) []types.EvidenceCommitmentV1 {
	t.Helper()
	elements := producerTaskRepeated(t, vector, 1, "commitments", 0, "count")
	items := make([]types.EvidenceCommitmentV1, 0, len(elements))
	for _, element := range elements {
		require.Equal(t, "frame", element.Type)
		require.Len(t, element.Fields, 3)
		kind := producerTaskNested(t, element, 0, "evidence_kind", "enum")
		hash := producerTaskNested(t, element, 1, "evidence_hash_or_root", "bytes")
		size := producerTaskNested(t, element, 2, "encoded_size_bytes", "uint64")
		require.NotNil(t, kind.Value)
		require.NotNil(t, size.Value)
		items = append(items, types.EvidenceCommitmentV1{
			EvidenceKind:       shared.EvidenceKind(*kind.Value),
			EvidenceHashOrRoot: producerTaskHex(t, hash),
			EncodedSizeBytes:   *size.Value,
		})
	}
	return items
}

func producerTaskMember(t *testing.T, vector producerTaskVector, index int) types.CandidateMemberRefV1 {
	t.Helper()
	member := producerTaskVectorField(t, vector, index, "member")
	require.Equal(t, "frame", member.Type)
	require.Len(t, member.Fields, 4)
	snapshot := producerTaskNested(t, member, 0, "candidate_pool_snapshot_id", "bytes")
	slot := producerTaskNested(t, member, 1, "slot", "uint32")
	version := producerTaskNested(t, member, 2, "slot_version", "uint64")
	operator := producerTaskNested(t, member, 3, "operator_address", "address")
	require.NotNil(t, slot.Value)
	require.NotNil(t, version.Value)
	require.NotEmpty(t, operator.Bech32)
	return types.CandidateMemberRefV1{
		CandidatePoolSnapshotId: producerTaskHex(t, snapshot),
		Slot:                    uint32(*slot.Value),
		SlotVersion:             *version.Value,
		OperatorAddress:         operator.Bech32,
	}
}

func producerTaskMetricSummary(t *testing.T, frame producerTaskField) types.MetricSummaryV1 {
	t.Helper()
	require.Equal(t, "frame", frame.Type)
	require.Len(t, frame.Fields, 10)
	u32 := func(index int, name string) uint32 {
		field := producerTaskNested(t, frame, index, name, "uint32")
		require.NotNil(t, field.Value)
		return uint32(*field.Value)
	}
	summary := types.MetricSummaryV1{
		FiniteCount:                u32(0, "finite_count"),
		MissingComparedCount:       u32(1, "missing_compared_count"),
		MeanAbsLogprobDiffFp_1E6:   u32(2, "mean_abs_logprob_diff_fp_1e6"),
		AbsLogprobDiffP95Fp_1E6:    u32(3, "abs_logprob_diff_p95_fp_1e6"),
		AbsLogprobDiffP99Fp_1E6:    u32(4, "abs_logprob_diff_p99_fp_1e6"),
		RankDeltaNonzeroRateFp_1E6: u32(5, "rank_delta_nonzero_rate_fp_1e6"),
		ComparedTopkCount:          u32(8, "compared_topk_count"),
		ComparedRankCount:          u32(9, "compared_rank_count"),
	}
	if value, present := producerTaskOptionalUint32(t, frame.Fields[6], "topk_jaccard_mean_fp_1e6"); present {
		summary.XTopkJaccardMeanFp_1E6 = &types.MetricSummaryV1_TopkJaccardMeanFp_1E6{TopkJaccardMeanFp_1E6: value}
	}
	if value, present := producerTaskOptionalUint32(t, frame.Fields[7], "union_js_p99_fp_1e6"); present {
		summary.XUnionJsP99Fp_1E6 = &types.MetricSummaryV1_UnionJsP99Fp_1E6{UnionJsP99Fp_1E6: value}
	}
	return summary
}

func producerTaskOptionalUint32(t *testing.T, field producerTaskField, name string) (uint32, bool) {
	t.Helper()
	require.Equal(t, name, field.Name)
	require.Equal(t, "optional", field.Type)
	require.NotNil(t, field.Present)
	if !*field.Present {
		require.Empty(t, field.Fields)
		return 0, false
	}
	require.Len(t, field.Fields, 1)
	value := field.Fields[0]
	require.Equal(t, "uint32", value.Type)
	require.NotNil(t, value.Value)
	return uint32(*value.Value), true
}

func producerTaskOptionalBytes(t *testing.T, field producerTaskField) []byte {
	t.Helper()
	require.Equal(t, "optional", field.Type)
	require.NotNil(t, field.Present)
	if !*field.Present {
		require.Empty(t, field.Fields)
		return nil
	}
	require.Len(t, field.Fields, 1)
	require.Equal(t, "bytes", field.Fields[0].Type)
	return producerTaskHex(t, field.Fields[0])
}

func producerTaskCandidate(t *testing.T, frame producerTaskField, taskID []byte) types.TaskCandidateFactState {
	t.Helper()
	require.Equal(t, "frame", frame.Type)
	require.Len(t, frame.Fields, 13)
	u64 := func(index int, name, kind string) uint64 {
		field := producerTaskNested(t, frame, index, name, kind)
		require.NotNil(t, field.Value)
		return *field.Value
	}
	amount := func(index int, name string) shared.Amount {
		return shared.Amount{AtomicUnits: strconv.FormatUint(u64(index, name, "uint64"), 10)}
	}
	operator := producerTaskNested(t, frame, 2, "operator_address", "address")
	return types.TaskCandidateFactState{
		SchemaVersion:                  types.AssignmentCandidateSchemaVersionV1,
		TaskId:                         taskID,
		Stage:                          types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK,
		Duty:                           shared.Duty_DUTY_WORKER,
		Slot:                           uint32(u64(0, "slot", "uint32")),
		SlotVersion:                    u64(1, "slot_version", "uint64"),
		OperatorAddress:                operator.Bech32,
		CandidateWeight:                uint32(u64(3, "candidate_weight", "uint32")),
		ActiveBondSnapshot:             amount(4, "active_bond_snapshot"),
		AvailableBondSnapshot:          amount(5, "available_bond_snapshot"),
		RequiredTaskLiabilitySnapshot:  amount(6, "required_task_liability_snapshot"),
		MinStakeSnapshot:               amount(7, "min_stake_snapshot"),
		PerformanceScoreSnapshotPpm:    uint32(u64(8, "performance_score_snapshot_ppm", "uint32")),
		PerformanceMethodVersion:       uint32(u64(9, "performance_method_version", "uint32")),
		CandidateJailFactorSnapshotPpm: uint32(u64(10, "candidate_jail_factor_snapshot_ppm", "uint32")),
		BondVersionSnapshot:            u64(11, "bond_version_snapshot", "uint64"),
		SupportVersionSnapshot:         u64(12, "support_version_snapshot", "uint64"),
		CapabilityVersionSnapshot:      1,
		HandraiseSigningDigest:         bytes.Repeat([]byte{0x11}, types.Hash32Len),
	}
}
