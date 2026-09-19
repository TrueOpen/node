package types_test

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/internal/testutil/domainfixture"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

type hubDomainVector = domainfixture.Vector
type hubFixtureField = domainfixture.Field

var hubValueBoundVectors = map[string]struct{}{
	"daily_support_confirmation_v1":    {},
	"support_profiles_v1":              {},
	"service_descriptor_v1":            {},
	"profile_verification_snapshot_v1": {},
	"evidence_schema_v1":               {},
	"beacon_checkpoint_v1":             {},
	"reward_bucket_boundaries_v1":      {},
	"reward_epoch_audit_leaf_v1":       {},
	"reward_epoch_audit_fold_v1":       {},
	"reward_epoch_audit_root_v1":       {},
	"validator_set_snapshot_v1":        {},
}

func TestHubDomainFixtureMatchesProductionHelpers(t *testing.T) {
	vectors := hubDomainVectorsByName(t)

	// bound records which vectors this test actually executed a producer for. The
	// comparison against hubValueBoundVectors at the end is what stops a subtest
	// from being deleted while the name stays on the list.
	bound := make(map[string]struct{}, len(hubValueBoundVectors))
	binds := func(name string) { bound[name] = struct{}{} }
	defer func() {
		require.Equal(t, hubValueBoundVectors, bound,
			"hubValueBoundVectors must name exactly the vectors this test binds by value")
	}()

	t.Run("CanonicalDailySupportConfirmationSigningBytes", func(t *testing.T) {
		binds("daily_support_confirmation_v1")
		vector := hubRequireVector(t, vectors, "daily_support_confirmation_v1")
		got, err := types.CanonicalDailySupportConfirmationSigningBytesV1(
			hubFieldString(t, vector, 0, "chain_id"),
			hubFieldAddressBytes(t, vector, 1, "operator_address"),
			hubFieldUint(t, vector, 2, "epoch_index"),
			hubFieldUint(t, vector, 3, "service_authorization_nonce"),
			hubFieldUint(t, vector, 4, "expiry_height"),
			hubProfileKeys(t, vector, 6, 5),
		)
		require.NoError(t, err)
		require.Equal(t, vector.DigestHex, hex.EncodeToString(got))
	})

	t.Run("CanonicalSupportedProfilesHash", func(t *testing.T) {
		binds("support_profiles_v1")
		vector := hubRequireVector(t, vectors, "support_profiles_v1")
		got, err := types.CanonicalSupportedProfilesHashV1(hubProfileKeys(t, vector, 1, 0))
		require.NoError(t, err)
		require.Equal(t, vector.DigestHex, hex.EncodeToString(got))
	})

	t.Run("CanonicalServiceDescriptorHash", func(t *testing.T) {
		binds("service_descriptor_v1")
		vector := hubRequireVector(t, vectors, "service_descriptor_v1")
		elements := hubRepeatedFields(t, vector, 4, 3, "endpoint_count")
		endpoints := make([]types.ServiceEndpointV1, 0, len(elements))
		for index, element := range elements {
			require.Equal(t, "frame", element.Type)
			require.Len(t, element.Fields, 4)
			kind := hubNestedUint(t, element, 0, fmt.Sprintf("endpoint_%d_kind", index))
			uri := element.Fields[1]
			protocol := element.Fields[2]
			tls := hubNestedBytes(t, element, 3, element.Fields[3].Name)
			require.Equal(t, fmt.Sprintf("endpoint_%d_uri", index), uri.Name)
			require.Equal(t, "string", uri.Type)
			require.NotNil(t, uri.UTF8)
			require.Equal(t, fmt.Sprintf("endpoint_%d_protocol_version", index), protocol.Name)
			require.Equal(t, "string", protocol.Type)
			require.NotNil(t, protocol.UTF8)
			endpoint := types.ServiceEndpointV1{
				EndpointKind: types.ServiceEndpointKind(kind), Uri: *uri.UTF8, ProtocolVersion: *protocol.UTF8,
			}
			if len(tls) != 0 {
				endpoint.XTlsPubkeyHash = &types.ServiceEndpointV1_TlsPubkeyHash{TlsPubkeyHash: tls}
			}
			endpoints = append(endpoints, endpoint)
		}
		got, err := types.CanonicalServiceDescriptorHash(
			shared.ParticipantType(hubFieldUint(t, vector, 0, "participant_type")),
			hubFieldAddressBytes(t, vector, 1, "operator_address"),
			hubFieldUint(t, vector, 2, "descriptor_version"),
			endpoints,
			types.ServiceParamsV1{
				MaxServiceDescriptorEndpoints:          uint32(len(endpoints)),
				MaxServiceDescriptorBytes:              4096,
				MaxServiceEndpointUriBytes:             256,
				MaxServiceEndpointProtocolVersionBytes: 64,
			},
		)
		require.NoError(t, err)
		require.Equal(t, vector.DigestHex, hex.EncodeToString(got))
	})

	// TRUEOPEN_PROFILE_VERIFICATION_SNAPSHOT_V1 is the deepest vector in the file:
	// nine outer positions, three of which are nested messages, and one of those
	// nests two more. It was needed because the domain had a frozen Go
	// golden and a per-leaf mutation gate but no cross-language vector at all -
	// model_registration_hash_test.go said so in a comment - so no other
	// implementation could reproduce the digest without reading Go.
	//
	// Publishing it is also what exposed the §4.4 flattening inside
	// CanonicalEvidenceSchemaTypedFrameV1: writing evidence_schema out as a frame
	// made the spliced count and elements visible, and the assertions below now
	// pin the repaired shape.
	t.Run("ProfileExecutionSnapshotHash", func(t *testing.T) {
		binds("profile_verification_snapshot_v1")
		vector := hubRequireVector(t, vectors, "profile_verification_snapshot_v1")

		profile := hubFieldFrame(t, vector, 5, "verification_profile")
		require.Len(t, profile.Fields, 14, "VerificationProfile frames its proto fields 1..14")
		metrics := hubNestedFrame(t, profile, 9, "metrics")
		require.Len(t, metrics.Fields, 6, "MetricSpec is one recursive frame, not six flattened scalars")
		evidenceSchema := hubNestedFrame(t, profile, 13, "evidence_schema")
		require.Len(t, evidenceSchema.Fields, 2,
			"NESTED_V1(EvidenceSchemaV1) has exactly the message's two proto fields: schema_version and the required_infer_evidence list as one §4.4 REPEATED_V1 position")

		requirementFields := hubNestedNamedRepeatedFields(t, evidenceSchema, 1, "required_infer_evidence")
		require.Len(t, requirementFields, 2,
			"two elements, so the vector proves the list length and the element order are in the preimage")
		requirements := make([]shared.InferEvidenceRequirementV1, 0, len(requirementFields))
		for index, requirement := range requirementFields {
			require.Equal(t, "frame", requirement.Type)
			require.Len(t, requirement.Fields, 3, "requirement %d", index)
			requirements = append(requirements, shared.InferEvidenceRequirementV1{
				EvidenceKind:            shared.EvidenceKind(hubNestedEnum(t, requirement, 0, "evidence_kind")),
				CommitmentSchemaVersion: uint32(hubNestedUint(t, requirement, 1, "commitment_schema_version")),
				MaxEncodedSizeBytes:     hubNestedUint(t, requirement, 2, "max_encoded_size_bytes"),
			})
		}

		thresholds := hubFieldFrame(t, vector, 6, "verification_thresholds")
		require.Len(t, thresholds.Fields, 14)
		batchVerification := hubFieldFrame(t, vector, 7, "batch_verification")
		require.Len(t, batchVerification.Fields, 5)

		snapshot := shared.ProfileExecutionSnapshot{
			ManifestHash:   hubFieldBytes(t, vector, 0, "manifest_hash"),
			TokenizerHash:  hubFieldBytes(t, vector, 1, "tokenizer_hash"),
			RuntimeClass:   hubFieldString(t, vector, 2, "runtime_class"),
			RequiredTopK:   uint32(hubFieldUint(t, vector, 3, "required_top_k")),
			GenerationType: shared.GenerationType(hubFieldEnum(t, vector, 4, "generation_type")),
			VerificationProfile: shared.VerificationProfile{
				VerificationProfileId:         uint32(hubNestedUint(t, profile, 0, "verification_profile_id")),
				JudgmentFunctionVersion:       hubNestedString(t, profile, 1, "judgment_function_version"),
				VerificationMode:              shared.VerificationMode(hubNestedEnum(t, profile, 2, "verification_mode")),
				TokenScope:                    shared.TokenScope(hubNestedEnum(t, profile, 3, "token_scope")),
				IncludeGeneratedSpecialTokens: hubNestedBool(t, profile, 4, "include_generated_special_tokens"),
				IncludePromptTokens:           hubNestedBool(t, profile, 5, "include_prompt_tokens"),
				IncludePaddingTokens:          hubNestedBool(t, profile, 6, "include_padding_tokens"),
				RequireOutputTokenIds:         hubNestedBool(t, profile, 7, "require_output_token_ids"),
				RequireFinishReason:           hubNestedBool(t, profile, 8, "require_finish_reason"),
				Metrics: shared.MetricSpec{
					CompareLogprobDiff: hubNestedBool(t, metrics, 0, "compare_logprob_diff"),
					CompareRankDelta:   hubNestedBool(t, metrics, 1, "compare_rank_delta"),
					CompareTopkJaccard: hubNestedBool(t, metrics, 2, "compare_topk_jaccard"),
					CompareUnionJs:     hubNestedBool(t, metrics, 3, "compare_union_js"),
					ComparedTopK:       uint32(hubNestedUint(t, metrics, 4, "compared_top_k")),
					NumericScale:       shared.NumericScale(hubNestedEnum(t, metrics, 5, "numeric_scale")),
				},
				CanonicalEncodingVersion:    hubNestedString(t, profile, 10, "canonical_encoding_version"),
				EvidenceSchemaHash:          hubNestedBytes(t, profile, 11, "evidence_schema_hash"),
				MetricAggregateProofVersion: hubNestedString(t, profile, 12, "metric_aggregate_proof_version"),
				EvidenceSchema: shared.EvidenceSchemaV1{
					SchemaVersion:         uint32(hubNestedUint(t, evidenceSchema, 0, "schema_version")),
					RequiredInferEvidence: requirements,
				},
			},
			VerificationThresholds: shared.VerificationThresholds{
				PassAbsLogprobDiffP95Max:      uint32(hubNestedUint(t, thresholds, 0, "pass_abs_logprob_diff_p95_max")),
				PassAbsLogprobDiffP99Max:      uint32(hubNestedUint(t, thresholds, 1, "pass_abs_logprob_diff_p99_max")),
				PassMaxMissingComparedCount:   uint32(hubNestedUint(t, thresholds, 2, "pass_max_missing_compared_count")),
				PassMeanAbsLogprobDiffMax:     uint32(hubNestedUint(t, thresholds, 3, "pass_mean_abs_logprob_diff_max")),
				PassMinFiniteCount:            uint32(hubNestedUint(t, thresholds, 4, "pass_min_finite_count")),
				PassRankDeltaNonzeroRateMax:   uint32(hubNestedUint(t, thresholds, 5, "pass_rank_delta_nonzero_rate_max")),
				PassTopkJaccardMeanMin:        uint32(hubNestedUint(t, thresholds, 6, "pass_topk_jaccard_mean_min")),
				PassUnionJsP99Max:             uint32(hubNestedUint(t, thresholds, 7, "pass_union_js_p99_max")),
				RejectAbsLogprobDiffP95Min:    uint32(hubNestedUint(t, thresholds, 8, "reject_abs_logprob_diff_p95_min")),
				RejectAbsLogprobDiffP99Min:    uint32(hubNestedUint(t, thresholds, 9, "reject_abs_logprob_diff_p99_min")),
				RejectMeanAbsLogprobDiffMin:   uint32(hubNestedUint(t, thresholds, 10, "reject_mean_abs_logprob_diff_min")),
				RejectRankDeltaNonzeroRateMin: uint32(hubNestedUint(t, thresholds, 11, "reject_rank_delta_nonzero_rate_min")),
				RejectTopkJaccardMeanMax:      uint32(hubNestedUint(t, thresholds, 12, "reject_topk_jaccard_mean_max")),
				RejectUnionJsP99Min:           uint32(hubNestedUint(t, thresholds, 13, "reject_union_js_p99_min")),
			},
			BatchVerification: shared.BatchVerification{
				Enabled:                       hubNestedBool(t, batchVerification, 0, "enabled"),
				MinSampleCount:                uint32(hubNestedUint(t, batchVerification, 1, "min_sample_count")),
				MinValidSampleCount:           uint32(hubNestedUint(t, batchVerification, 2, "min_valid_sample_count")),
				PassMinSamplePassRatioBps:     uint32(hubNestedUint(t, batchVerification, 3, "pass_min_sample_pass_ratio_bps")),
				RejectMinSampleRejectRatioBps: uint32(hubNestedUint(t, batchVerification, 4, "reject_min_sample_reject_ratio_bps")),
			},
			SchemaHash: hubFieldBytes(t, vector, 8, "schema_hash"),
		}

		got, err := types.ProfileExecutionSnapshotHash(snapshot)
		require.NoError(t, err)
		require.Equal(t, vector.DigestHex, hex.EncodeToString(got))
	})

	t.Run("EvidenceSchemaHash", func(t *testing.T) {
		binds("evidence_schema_v1")
		vector := hubRequireVector(t, vectors, "evidence_schema_v1")
		batch := hubFieldFrame(t, vector, 7, "batch_verification")
		require.Len(t, batch.Fields, 5)
		metric := hubFieldFrame(t, vector, 19, "metric_spec")
		require.Len(t, metric.Fields, 6)
		requirementFields := hubRepeatedFields(t, vector, 21, 20, "requirement_count")
		requirements := make([]shared.InferEvidenceRequirementV1, 0, len(requirementFields))
		for _, requirement := range requirementFields {
			require.Equal(t, "frame", requirement.Type)
			require.Len(t, requirement.Fields, 3)
			requirements = append(requirements, shared.InferEvidenceRequirementV1{
				EvidenceKind:            shared.EvidenceKind(hubNestedUint(t, requirement, 0, "evidence_kind")),
				CommitmentSchemaVersion: uint32(hubNestedUint(t, requirement, 1, "commitment_schema_version")),
				MaxEncodedSizeBytes:     hubNestedUint(t, requirement, 2, "max_encoded_size_bytes"),
			})
		}
		projection := shared.ModelProfileProjection{
			ModelId:        hubFieldString(t, vector, 1, "model_id"),
			ProfileVersion: uint32(hubFieldUint(t, vector, 2, "profile_version")),
			SchemaHash:     hubFieldBytes(t, vector, 3, "schema_hash"),
			TokenizerHash:  hubFieldBytes(t, vector, 4, "tokenizer_hash"),
			GenerationType: shared.GenerationType(hubFieldEnum(t, vector, 5, "generation_type")),
			RequiredTopK:   uint32(hubFieldUint(t, vector, 6, "required_top_k")),
			BatchVerification: shared.BatchVerification{
				Enabled:                       hubNestedBool(t, batch, 0, "enabled"),
				MinSampleCount:                uint32(hubNestedUint(t, batch, 1, "min_sample_count")),
				MinValidSampleCount:           uint32(hubNestedUint(t, batch, 2, "min_valid_sample_count")),
				PassMinSamplePassRatioBps:     uint32(hubNestedUint(t, batch, 3, "pass_min_sample_pass_ratio_bps")),
				RejectMinSampleRejectRatioBps: uint32(hubNestedUint(t, batch, 4, "reject_min_sample_reject_ratio_bps")),
			},
			VerificationProfile: shared.VerificationProfile{
				VerificationProfileId:         uint32(hubFieldUint(t, vector, 8, "verification_profile_id")),
				JudgmentFunctionVersion:       hubFieldString(t, vector, 9, "judgment_function_version"),
				CanonicalEncodingVersion:      hubFieldString(t, vector, 10, "canonical_encoding_version"),
				MetricAggregateProofVersion:   hubFieldString(t, vector, 11, "metric_aggregate_proof_version"),
				VerificationMode:              shared.VerificationMode(hubFieldEnum(t, vector, 12, "verification_mode")),
				TokenScope:                    shared.TokenScope(hubFieldEnum(t, vector, 13, "token_scope")),
				IncludeGeneratedSpecialTokens: hubFieldBool(t, vector, 14, "include_generated_special_tokens"),
				IncludePromptTokens:           hubFieldBool(t, vector, 15, "include_prompt_tokens"),
				IncludePaddingTokens:          hubFieldBool(t, vector, 16, "include_padding_tokens"),
				RequireOutputTokenIds:         hubFieldBool(t, vector, 17, "require_output_token_ids"),
				RequireFinishReason:           hubFieldBool(t, vector, 18, "require_finish_reason"),
				Metrics: shared.MetricSpec{
					CompareLogprobDiff: hubNestedBool(t, metric, 0, "compare_logprob_diff"),
					CompareRankDelta:   hubNestedBool(t, metric, 1, "compare_rank_delta"),
					CompareTopkJaccard: hubNestedBool(t, metric, 2, "compare_topk_jaccard"),
					CompareUnionJs:     hubNestedBool(t, metric, 3, "compare_union_js"),
					ComparedTopK:       uint32(hubNestedUint(t, metric, 4, "compared_top_k")),
					NumericScale:       shared.NumericScale(hubNestedUint(t, metric, 5, "numeric_scale")),
				},
				EvidenceSchema: shared.EvidenceSchemaV1{
					SchemaVersion:         uint32(hubFieldUint(t, vector, 0, "schema_version")),
					RequiredInferEvidence: requirements,
				},
			},
		}
		got, err := types.EvidenceSchemaHash(projection)
		require.NoError(t, err)
		require.Equal(t, vector.DigestHex, hex.EncodeToString(got))
	})

	// BeaconCheckpointStep is bound by value rather than by source shape. The
	// distinction matters for this domain in particular: its preimage opens with
	// three uint64 and continues with three 32-byte digests, so any permutation
	// inside either group produces the identical sequence of encoder calls. A
	// binding that classifies fields by encoder - which is all the source-shape
	// scan in hub_domains_binding_test.go can do for an unexported producer - is
	// blind to exactly those swaps. Feeding the vector's own inputs to the real
	// function and demanding the pinned digest back checks field identity, not
	// field encoding.
	t.Run("BeaconCheckpointStep", func(t *testing.T) {
		binds("beacon_checkpoint_v1")
		vector := hubRequireVector(t, vectors, "beacon_checkpoint_v1")
		require.Equal(t, "BeaconCheckpointStep", vector.Producer)
		require.Len(t, vector.Fields, 7)
		got, err := types.BeaconCheckpointStep(
			hubFieldUint(t, vector, 0, "checkpoint_index"),
			hubFieldUint(t, vector, 1, "start_height"),
			hubFieldUint(t, vector, 2, "current_height"),
			hubFieldBytes(t, vector, 3, "previous_root"),
			hubFieldBytes(t, vector, 4, "randomness"),
			hubFieldBytes(t, vector, 5, "proof_digest"),
			hubFieldString(t, vector, 6, "proposer_consensus_address"),
		)
		require.NoError(t, err)
		require.Equal(t, vector.DigestHex, hex.EncodeToString(got))
	})

	t.Run("RewardOrderValueBucketBoundariesHash", func(t *testing.T) {
		binds("reward_bucket_boundaries_v1")
		vector := hubRequireVector(t, vectors, "reward_bucket_boundaries_v1")
		elements := hubRepeatedFields(t, vector, 2, 1, "boundary_count")
		boundaries := make([]shared.Amount, 0, len(elements))
		for index, element := range elements {
			require.Equal(t, "frame", element.Type)
			require.Equal(t, fmt.Sprintf("boundary_%d", index), element.Name)
			require.Len(t, element.Fields, 2)
			require.Equal(t, "boundary_index", element.Fields[0].Name)
			require.Equal(t, "uint32", element.Fields[0].Type)
			require.NotNil(t, element.Fields[0].Value)
			require.Equal(t, uint64(index), *element.Fields[0].Value,
				"the framed boundary_index is the slice position the producer derives")
			amount := element.Fields[1]
			require.Equal(t, "amount", amount.Name)
			require.Equal(t, "frame", amount.Type)
			require.Len(t, amount.Fields, 1)
			require.Equal(t, "atomic_units", amount.Fields[0].Name)
			require.Equal(t, "string", amount.Fields[0].Type)
			require.NotNil(t, amount.Fields[0].UTF8)
			boundaries = append(boundaries, shared.Amount{AtomicUnits: *amount.Fields[0].UTF8})
		}

		got, err := types.RewardOrderValueBucketBoundariesHash(
			hubFieldString(t, vector, 0, "chain_id"), boundaries)
		require.NoError(t, err)
		require.Equal(t, vector.DigestHex, hex.EncodeToString(got))
	})

	// The three reward audit domains are one chain: the leaf digest is the fold's
	// leaf_hash input and the fold digest is the root's folded_root input. Binding
	// them as a chain rather than as three unrelated vectors means a change to the
	// leaf preimage cannot be absorbed by regenerating one file - the two vectors
	// downstream stop agreeing with the digests they carry as inputs, which is
	// asserted below rather than left to the reader.
	t.Run("RewardEpochAuditLeafHashFramesV1", func(t *testing.T) {
		binds("reward_epoch_audit_leaf_v1")
		vector := hubRequireVector(t, vectors, "reward_epoch_audit_leaf_v1")
		require.Len(t, vector.Fields, 6)

		primary := hubFixtureFlatFrame(t, hubFieldFrame(t, vector, 4, "primary_key_frame"))
		state := hubFixtureFlatFrame(t, hubFieldFrame(t, vector, 5, "state_frame"))
		got, err := types.RewardEpochAuditLeafHashFramesV1(
			hubFieldString(t, vector, 0, "chain_id"),
			hubFieldUint(t, vector, 1, "source_epoch"),
			types.RewardBucket(hubFieldEnum(t, vector, 2, "reward_bucket")),
			types.RewardEpochPrunePhaseV1(hubFieldEnum(t, vector, 3, "phase")),
			primary,
			state,
		)
		require.NoError(t, err)
		require.Equal(t, vector.DigestHex, hex.EncodeToString(got))

		// RewardEpochAuditLeafHash is the deprecated raw wrapper kept for external
		// sources. It takes the two frames as opaque bytes, so it can only frame
		// them at depth zero - but the bytes are the same bytes, and therefore so
		// is the digest. Pinning that here is what keeps the wrapper honest: if the
		// two entry points ever disagreed, one of them would be producing a
		// consensus value the other cannot reproduce.
		primaryBytes, err := primary.Bytes()
		require.NoError(t, err)
		stateBytes, err := state.Bytes()
		require.NoError(t, err)
		legacy, err := types.RewardEpochAuditLeafHash(
			hubFieldString(t, vector, 0, "chain_id"),
			hubFieldUint(t, vector, 1, "source_epoch"),
			types.RewardBucket(hubFieldEnum(t, vector, 2, "reward_bucket")),
			types.RewardEpochPrunePhaseV1(hubFieldEnum(t, vector, 3, "phase")),
			primaryBytes,
			stateBytes,
		)
		require.NoError(t, err)
		require.Equal(t, vector.DigestHex, hex.EncodeToString(legacy))
	})

	t.Run("RewardEpochAuditFoldHash", func(t *testing.T) {
		binds("reward_epoch_audit_fold_v1")
		vector := hubRequireVector(t, vectors, "reward_epoch_audit_fold_v1")
		require.Len(t, vector.Fields, 5)

		// previous_root is the documented all-zero seed, so this vector pins the
		// first fold of an epoch rather than an arbitrary step.
		require.Equal(t, hex.EncodeToString(types.RewardEpochAuditInitialRoot()),
			hex.EncodeToString(hubFieldBytes(t, vector, 3, "previous_root")))
		leaf := hubRequireVector(t, vectors, "reward_epoch_audit_leaf_v1")
		require.Equal(t, leaf.DigestHex, hex.EncodeToString(hubFieldBytes(t, vector, 4, "leaf_hash")),
			"the fold consumes the leaf vector's own published digest")

		got, err := types.RewardEpochAuditFoldHash(
			hubFieldString(t, vector, 0, "chain_id"),
			hubFieldUint(t, vector, 1, "source_epoch"),
			types.RewardBucket(hubFieldEnum(t, vector, 2, "reward_bucket")),
			hubFieldBytes(t, vector, 3, "previous_root"),
			hubFieldBytes(t, vector, 4, "leaf_hash"),
		)
		require.NoError(t, err)
		require.Equal(t, vector.DigestHex, hex.EncodeToString(got))
	})

	// The root's tail is five uint64 counts. Nothing about the framing separates
	// them, which makes this the clearest case in the file for why the binding has
	// to be by value: permuting eligible_task_count, mark_count, accrual_count and
	// contribution_count inside the producer produces the same five Uint64BE calls
	// in the same five positions. The vector gives all four different values so any
	// permutation moves the digest.
	t.Run("RewardEpochAuditRootHash", func(t *testing.T) {
		binds("reward_epoch_audit_root_v1")
		vector := hubRequireVector(t, vectors, "reward_epoch_audit_root_v1")
		require.Len(t, vector.Fields, 9)

		fold := hubRequireVector(t, vectors, "reward_epoch_audit_fold_v1")
		require.Equal(t, fold.DigestHex, hex.EncodeToString(hubFieldBytes(t, vector, 3, "folded_root")),
			"the root seals the fold vector's own published digest")

		counts := types.RewardEpochAuditCounts{
			EligibleTasks: hubFieldUint(t, vector, 4, "eligible_task_count"),
			Marks:         hubFieldUint(t, vector, 5, "mark_count"),
			Accruals:      hubFieldUint(t, vector, 6, "accrual_count"),
			Contributions: hubFieldUint(t, vector, 7, "contribution_count"),
		}
		// total_count is framed but is not an argument: the producer derives it.
		// Comparing the derived total with the framed one keeps the fixture from
		// publishing a total no run of the producer could ever emit.
		total, err := counts.Total()
		require.NoError(t, err)
		require.Equal(t, hubFieldUint(t, vector, 8, "total_count"), total)

		got, err := types.RewardEpochAuditRootHash(
			hubFieldString(t, vector, 0, "chain_id"),
			hubFieldUint(t, vector, 1, "source_epoch"),
			types.RewardBucket(hubFieldEnum(t, vector, 2, "reward_bucket")),
			hubFieldBytes(t, vector, 3, "folded_root"),
			counts,
		)
		require.NoError(t, err)
		require.Equal(t, vector.DigestHex, hex.EncodeToString(got))
	})

	// CanonicalValidatorSetSnapshot sorts its members and derives both the count
	// and the total voting power, so three of the four head fields are producer
	// output rather than producer input. The binding therefore hands it the members
	// in a deliberately wrong order: reaching the pinned digest is a statement that
	// the canonical sort ran, not merely that the fixture listed them sorted.
	t.Run("CanonicalValidatorSetSnapshot", func(t *testing.T) {
		binds("validator_set_snapshot_v1")
		vector := hubRequireVector(t, vectors, "validator_set_snapshot_v1")
		elements := hubRepeatedFields(t, vector, 4, 2, "validator_count")
		members := make([]types.CanonicalValidatorSnapshotMember, 0, len(elements))
		var total uint64
		for index, element := range elements {
			require.Equal(t, "frame", element.Type)
			require.Len(t, element.Fields, 2)
			address := hubNestedBytes(t, element, 0, fmt.Sprintf("validator_%d_consensus_address", index))
			power := hubNestedUint(t, element, 1, fmt.Sprintf("validator_%d_voting_power", index))
			total += power
			members = append(members, types.CanonicalValidatorSnapshotMember{
				ConsensusAddress: address,
				// SignerAddress is required by the producer's completeness check but is
				// deliberately outside the preimage, so it is supplied here instead of
				// being published as a field. The assertion below is what makes that
				// exclusion a checked property rather than a comment.
				SignerAddress: fmt.Sprintf("trueopen-fixture-validator-signer-%d", index),
				VotingPower:   power,
			})
		}
		require.Equal(t, hubFieldUint(t, vector, 3, "total_voting_power_snapshot"), total)

		scrambled := []types.CanonicalValidatorSnapshotMember{members[len(members)-1]}
		scrambled = append(scrambled, members[:len(members)-1]...)
		got, gotTotal, err := types.CanonicalValidatorSetSnapshot(
			hubFieldString(t, vector, 0, "chain_id"),
			hubFieldUint(t, vector, 1, "validator_snapshot_height"),
			scrambled,
		)
		require.NoError(t, err)
		require.Equal(t, vector.DigestHex, hex.EncodeToString(got))
		require.Equal(t, total, gotTotal)

		renamed := append([]types.CanonicalValidatorSnapshotMember(nil), members...)
		for index := range renamed {
			renamed[index].SignerAddress += "-renamed"
		}
		sameDigest, _, err := types.CanonicalValidatorSetSnapshot(
			hubFieldString(t, vector, 0, "chain_id"),
			hubFieldUint(t, vector, 1, "validator_snapshot_height"),
			renamed,
		)
		require.NoError(t, err)
		require.Equal(t, vector.DigestHex, hex.EncodeToString(sameDigest),
			"signer_address supports historical lookup and must stay out of the preimage")
	})
}

// TestHubDomainFixtureCoversRegisteredIdentityDomains keeps the fixture and the
// registry in lockstep for the seven identity signing and ID domains.

func hubDomainVectorsByName(t *testing.T) map[string]hubDomainVector {
	t.Helper()
	return domainfixture.ByName(t, "testdata/hub_domains_v1.json")
}

func hubRequireVector(t *testing.T, vectors map[string]hubDomainVector, name string) hubDomainVector {
	t.Helper()
	vector, ok := vectors[name]
	require.True(t, ok, "fixture has no vector %s", name)
	return vector
}

func hubFieldString(t *testing.T, vector hubDomainVector, index int, name string) string {
	return vector.String(t, index, name)
}

func hubFieldAddressBytes(t *testing.T, vector hubDomainVector, index int, name string) []byte {
	return vector.AddressBytes(t, index, name)
}

func hubFieldBytes(t *testing.T, vector hubDomainVector, index int, name string) []byte {
	return vector.Bytes(t, index, name)
}

func hubFieldUint(t *testing.T, vector hubDomainVector, index int, name string) uint64 {
	return vector.Uint(t, index, name)
}

func hubFieldBool(t *testing.T, vector hubDomainVector, index int, name string) bool {
	return vector.BoolValue(t, index, name)
}

func hubFieldEnum(t *testing.T, vector hubDomainVector, index int, name string) uint32 {
	return uint32(vector.Uint(t, index, name))
}

func hubFieldFrame(t *testing.T, vector hubDomainVector, index int, name string) hubFixtureField {
	return vector.Frame(t, index, name)
}

func hubRepeatedFields(t *testing.T, vector hubDomainVector, head, countIndex int, countName string) []hubFixtureField {
	return vector.Repeated(t, head, countIndex, vector.Fields[head].Name, countName)
}

func hubNestedBytes(t *testing.T, frame hubFixtureField, index int, name string) []byte {
	return frame.Field(t, index, name).Bytes(t, name)
}

func hubNestedUint(t *testing.T, frame hubFixtureField, index int, name string) uint64 {
	return frame.Field(t, index, name).Uint(t, name)
}

func hubNestedEnum(t *testing.T, frame hubFixtureField, index int, name string) uint32 {
	return uint32(frame.Field(t, index, name).Uint(t, name))
}

func hubNestedString(t *testing.T, frame hubFixtureField, index int, name string) string {
	return frame.Field(t, index, name).String(t, name)
}

func hubNestedBool(t *testing.T, frame hubFixtureField, index int, name string) bool {
	return frame.Field(t, index, name).BoolValue(t, name)
}

func hubNestedFrame(t *testing.T, frame hubFixtureField, index int, name string) hubFixtureField {
	field := frame.Field(t, index, name)
	field.Frame(t, name)
	return field
}

func hubNestedNamedRepeatedFields(t *testing.T, frame hubFixtureField, index int, name string) []hubFixtureField {
	field := frame.Field(t, index, name)
	fields := field.Frame(t, name)
	require.NotEmpty(t, fields)
	require.Equal(t, "element_count", fields[0].Name)
	require.Equal(t, uint64(len(fields)-1), fields[0].Uint(t, name+".element_count"))
	return fields[1:]
}

func hubFixtureFlatFrame(t *testing.T, field hubFixtureField) shared.CanonicalFrameV1 {
	t.Helper()
	fields := field.Frame(t, field.Name)
	encoded := make([][]byte, len(fields))
	for index, nested := range fields {
		where := fmt.Sprintf("%s.%s", field.Name, nested.Name)
		switch nested.Type {
		case "bytes", "address":
			encoded[index] = nested.Bytes(t, where)
		case "string":
			encoded[index] = []byte(nested.String(t, where))
		case "uint32":
			encoded[index] = shared.Uint32BE(uint32(nested.Uint(t, where)))
		case "uint64":
			encoded[index] = shared.Uint64BE(nested.Uint(t, where))
		case "enum":
			encoded[index] = shared.EnumBE(uint32(nested.Uint(t, where)))
		case "bool":
			encoded[index] = shared.BoolByte(nested.BoolValue(t, where))
		default:
			t.Fatalf("%s has unsupported nested type %q", where, nested.Type)
		}
	}
	frameValue := shared.FlatCanonicalFrameV1(encoded...)
	require.NoError(t, frameValue.Err())
	return frameValue
}

func hubProfileKeys(t *testing.T, vector hubDomainVector, repeatedIndex, countIndex int) []types.ProfileKeyV1 {
	t.Helper()
	elements := vector.Repeated(t, repeatedIndex, countIndex, vector.Fields[repeatedIndex].Name, vector.Fields[countIndex].Name)
	profiles := make([]types.ProfileKeyV1, len(elements))
	for index, element := range elements {
		fields := element.Frame(t, fmt.Sprintf("profile_%d", index))
		profiles[index] = types.ProfileKeyV1{
			ModelId:        fields[0].String(t, fmt.Sprintf("profile_%d.model_id", index)),
			ProfileVersion: uint32(fields[1].Uint(t, fmt.Sprintf("profile_%d.profile_version", index))),
		}
	}
	return profiles
}

func TestHubDomainFixtureProducerBindingsAccountForEveryVector(t *testing.T) {
	vectors := hubDomainVectorsByName(t)
	producers := map[string]string{
		"service_registration_v1":          "serviceRegistrationDigest",
		"service_key_rotation_v1":          "serviceKeyRotationDigest",
		"service_descriptor_v1":            "CanonicalServiceDescriptorHash",
		"unbonding_id_v1":                  "serviceUnbondingID",
		"unbonding_receipt_v1":             "unbondingReceiptHash",
		"daily_support_confirmation_v1":    "CanonicalDailySupportConfirmationSigningBytesV1",
		"support_profiles_v1":              "CanonicalSupportedProfilesHashV1",
		"parameter_bucket_v1_timeout":      "ParameterBucketContentHash",
		"beacon_checkpoint_v1":             "BeaconCheckpointStep",
		"reward_bucket_boundaries_v1":      "RewardOrderValueBucketBoundariesHash",
		"reward_epoch_audit_leaf_v1":       "RewardEpochAuditLeafHashFramesV1",
		"reward_epoch_audit_fold_v1":       "RewardEpochAuditFoldHash",
		"reward_epoch_audit_root_v1":       "RewardEpochAuditRootHash",
		"validator_set_snapshot_v1":        "CanonicalValidatorSetSnapshot",
		"evidence_schema_v1":               "EvidenceSchemaHash",
		"profile_verification_snapshot_v1": "ProfileExecutionSnapshotHash",
	}
	bound := make(map[string]struct{}, len(producers)-1)
	for name, producer := range producers {
		vector, ok := vectors[name]
		require.True(t, ok, "producer metadata %s has no fixture vector", name)
		domainfixture.RequireProducer(t, vector, producer)
		if name != "parameter_bucket_v1_timeout" {
			bound[name] = struct{}{}
		}
	}
	domainfixture.RequireBoundSet(t, vectors, bound, map[string]string{
		"parameter_bucket_v1_timeout": "fixture still carries retired ReferenceBucket entries while the current TIMEOUT producer hashes timeout_blocks",
	})
}

func TestParameterBucketFixtureExceptionStillRepresentsRetiredEntries(t *testing.T) {
	vector := hubRequireVector(t, hubDomainVectorsByName(t), "parameter_bucket_v1_timeout")
	elements := vector.Repeated(t, 6, 5, "entries", "entry_count")
	entries := make([]types.TimeoutBucketEntryV1, len(elements))
	for index, element := range elements {
		fields := element.Frame(t, fmt.Sprintf("entry_%d", index))
		upper := fields[0].Uint(t, "upper_work_units_inclusive")
		require.Equal(t, "reference_price", fields[1].Name)
		priceFields := fields[1].Frame(t, "reference_price")
		require.Len(t, priceFields, 1)
		price := priceFields[0].String(t, "reference_price.atomic_units")
		timeout, err := strconv.ParseUint(price, 10, 64)
		require.NoError(t, err)
		entries[index] = types.TimeoutBucketEntryV1{UpperWorkUnitsInclusive: upper, TimeoutBlocks: timeout}
	}
	got, err := types.ParameterBucketContentHash(vector.String(t, 0, "chain_id"), types.ParameterBucketVersionState{
		SchemaVersion:   types.ParameterBucketSchemaVersionV1,
		BucketKind:      shared.BucketKind(vector.Uint(t, 1, "bucket_kind")),
		BucketKey:       vector.String(t, 2, "bucket_key"),
		Version:         vector.Uint(t, 3, "new_version"),
		EffectiveHeight: vector.Uint(t, 4, "effective_height"),
		EntryCount:      uint32(len(entries)),
		TimeoutEntries:  types.TimeoutBucketEntriesV1{Entries: entries},
	})
	require.NoError(t, err)
	require.NotEqual(t, vector.Digest(), hex.EncodeToString(got),
		"remove the explicit exception once the fixture publishes timeout_blocks")
}
