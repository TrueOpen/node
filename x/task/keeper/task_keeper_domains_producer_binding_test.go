package keeper

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/internal/testutil/domainfixture"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

const taskKeeperProducerFixturePath = "testdata/task_keeper_domains_v1.json"

type taskKeeperProducerInputs struct {
	TaskBuilderCount          uint32 `json:"task_builder_count"`
	SegmentBytes              uint32 `json:"segment_bytes"`
	SlotCapacity              uint32 `json:"slot_capacity"`
	MaxAttempts               uint64 `json:"max_attempts"`
	TaskIDHex                 string `json:"task_id_hex"`
	HandraiseCloseHeight      uint64 `json:"handraise_close_height"`
	SelectionRandomnessHeight uint64 `json:"selection_randomness_height"`
}

func TestTaskKeeperDomainFixtureBindsProducers(t *testing.T) {
	vectors := domainfixture.ByName(t, taskKeeperProducerFixturePath)
	bound := make(map[string]struct{}, len(vectors))
	mark := func(name string) { bound[name] = struct{}{} }
	vector := func(name string) domainfixture.Vector {
		value, ok := vectors[name]
		require.True(t, ok, "fixture vector %q is missing", name)
		return value
	}

	t.Run("DataUnavailableBitmapHash", func(t *testing.T) {
		v := vector("data_unavailable_bitmap_v1")
		domainfixture.RequireProducer(t, v, "DataUnavailableBitmapHash")
		var inputs taskKeeperProducerInputs
		domainfixture.DecodeInputs(t, v, &inputs)
		count := uint32(v.Uint(t, 3, "task_builder_count"))
		require.Equal(t, inputs.TaskBuilderCount, count)
		got, err := DataUnavailableBitmapHash(v.String(t, 0, "chain_id"), v.Bytes(t, 1, "task_id"),
			uint32(v.Uint(t, 2, "verify_round")), count, v.Bytes(t, 4, "unavailable_task_builder_bitmap"))
		require.NoError(t, err)
		domainfixture.RequireDigest(t, v, got)
		mark(v.Name)
	})

	t.Run("DataUnavailableReportDigest", func(t *testing.T) {
		v := vector("data_unavailable_report_v1")
		domainfixture.RequireProducer(t, v, "DataUnavailableReportDigest")
		got, err := DataUnavailableReportDigest(v.String(t, 0, "chain_id"), DataUnavailableReportAttempt{
			TaskID:                            v.Bytes(t, 1, "task_id"),
			VerifyRound:                       uint32(v.Uint(t, 2, "verify_round")),
			VerifierOperatorAddress:           v.Address(t, 3, "verifier_operator_address"),
			UnavailableTaskBuilderBitmap:      v.Bytes(t, 4, "unavailable_task_builder_bitmap"),
			ServiceAuthorizationNonceSnapshot: v.Uint(t, 5, "service_authorization_nonce_snapshot"),
		})
		require.NoError(t, err)
		domainfixture.RequireDigest(t, v, got)
		mark(v.Name)
	})

	t.Run("drawWeightedCandidate", func(t *testing.T) {
		v := vector("weighted_draw_v1")
		domainfixture.RequireProducer(t, v, "drawWeightedCandidate")
		var inputs taskKeeperProducerInputs
		domainfixture.DecodeInputs(t, v, &inputs)
		for _, weight := range []uint32{1, 1 << 12, 1 << 24} {
			candidates := make([]types.TaskCandidateFactState, 1<<12)
			for slot := range candidates {
				candidates[slot] = types.TaskCandidateFactState{Slot: uint32(slot), CandidateWeight: weight}
			}
			got, err := drawWeightedCandidate(
				v.String(t, 0, "chain_id"), v.String(t, 1, "purpose"), v.Bytes(t, 2, "task_id"),
				v.Bytes(t, 3, "legal_set_hash"), v.Uint(t, 4, "randomness_height"),
				v.Bytes(t, 5, "randomness_beacon"), v.Uint(t, 6, "counter"), inputs.MaxAttempts, candidates,
			)
			require.NoError(t, err)
			require.Equal(t, v.Uint(t, 6, "counter"), got.AcceptedCounter)
			require.Equal(t, taskKeeperSlotForDigest(t, v.Digest(), candidates), got.Fact.Slot)
		}
		mark(v.Name)
	})

	t.Run("winnerDrawDigest", func(t *testing.T) {
		v := vector("winner_draw_v1")
		domainfixture.RequireProducer(t, v, "winnerDrawDigest")
		got, err := winnerDrawDigest(
			v.String(t, 0, "chain_id"), v.Bytes(t, 1, "task_id"),
			v.Bytes(t, 2, "assignment_candidate_set_hash"), v.Uint(t, 3, "assignment_randomness_height"),
			v.Bytes(t, 4, "beacon_randomness"), v.Uint(t, 5, "accepted_draw_counter"),
			types.TaskCandidateFactState{
				Slot: uint32(v.Uint(t, 6, "winner_slot")), SlotVersion: v.Uint(t, 7, "winner_slot_version"),
				OperatorAddress: v.Address(t, 8, "winner_worker"),
			},
		)
		require.NoError(t, err)
		domainfixture.RequireDigest(t, v, got[:])
		mark(v.Name)
	})

	t.Run("PaidRolesHash", func(t *testing.T) {
		v := vector("paid_roles_v1")
		domainfixture.RequireProducer(t, v, "PaidRolesHash")
		elements := taskKeeperElements(t, v, 4, 3, "paid_roles", "paid_role_count")
		roles := make([]types.PaidRoleV1, 0, len(elements))
		for index, element := range elements {
			where := fmt.Sprintf("%s.role_%d", v.Name, index)
			roles = append(roles, types.PaidRoleV1{
				Duty:            shared.Duty(taskKeeperFieldUint(t, element, 0, "duty", where)),
				OperatorAddress: taskKeeperFieldAddress(t, element, 1, "operator_address", where),
			})
		}
		got, err := types.PaidRolesHash(v.String(t, 0, "chain_id"), v.Bytes(t, 1, "task_id"),
			v.Bytes(t, 2, "settlement_id"), roles)
		require.NoError(t, err)
		domainfixture.RequireDigest(t, v, got[:])
		mark(v.Name)
	})

	t.Run("taskStageUnionBitmapHash", func(t *testing.T) {
		v := vector("task_stage_union_bitmap_v1")
		domainfixture.RequireProducer(t, v, "taskStageUnionBitmapHash")
		var inputs taskKeeperProducerInputs
		domainfixture.DecodeInputs(t, v, &inputs)
		elements := taskKeeperElements(t, v, 7, 5, "segments", "segment_count")
		segments := make([]taskBitmapSegment, 0, len(elements))
		for index, element := range elements {
			where := fmt.Sprintf("%s.segment_%d", v.Name, index)
			segments = append(segments, taskBitmapSegment{
				Index:  uint32(taskKeeperFieldUint(t, element, 0, "segment_index", where)),
				Bitmap: taskKeeperFieldBytes(t, element, 1, "segment_bitmap_bytes", where),
			})
		}
		got, err := taskStageUnionBitmapHash(
			v.String(t, 0, "chain_id"), v.Bytes(t, 1, "task_id"),
			types.TaskCandidateStage(v.Uint(t, 2, "stage")), v.Bytes(t, 3, "candidate_pool_snapshot_id"),
			uint32(v.Uint(t, 4, "slot_capacity")), inputs.SegmentBytes,
			uint32(v.Uint(t, 6, "union_count")), segments,
		)
		require.NoError(t, err)
		domainfixture.RequireDigest(t, v, got[:])
		mark(v.Name)
	})

	t.Run("DeriveSessionID", func(t *testing.T) {
		v := vector("session_v1")
		domainfixture.RequireProducer(t, v, "DeriveSessionID")
		got, err := DeriveSessionID(v.AddressBytes(t, 0, "user_address"), v.Uint(t, 1, "session_nonce"))
		require.NoError(t, err)
		domainfixture.RequireDigest(t, v, got)
		mark(v.Name)
	})

	t.Run("TaskSettlementID", func(t *testing.T) {
		v := vector("task_settlement_id_v1")
		domainfixture.RequireProducer(t, v, "TaskSettlementID")
		got, err := types.TaskSettlementID(
			v.String(t, 0, "chain_id"), v.Bytes(t, 1, "task_id"), v.Bytes(t, 2, "task_hash"),
			uint32(v.Uint(t, 3, "verify_round")), v.Uint(t, 4, "settlement_facts_cutoff_height"),
			v.Uint(t, 5, "settlement_height"),
		)
		require.NoError(t, err)
		domainfixture.RequireDigest(t, v, got[:])
		mark(v.Name)
	})

	t.Run("sessionSequenceRoot", func(t *testing.T) {
		v := vector("session_sequence_root_v1")
		domainfixture.RequireProducer(t, v, "sessionSequenceRoot")
		got, err := sessionSequenceRoot(v.Bytes(t, 0, "previous_root"), types.OrderSequenceState{
			OrderSequence: v.Uint(t, 1, "order_sequence"),
			Status:        types.OrderSequenceStatus(v.Uint(t, 2, "status")),
			TaskId:        v.Bytes(t, 3, "task_id"), ConsumedHeight: v.Uint(t, 4, "consumed_height"),
			CancelledHeight: v.Uint(t, 5, "cancelled_height"),
		})
		require.NoError(t, err)
		domainfixture.RequireDigest(t, v, got)
		mark(v.Name)
	})

	t.Run("serviceKeyResponsibilityID", func(t *testing.T) {
		v := vector("service_key_responsibility_id_v1")
		domainfixture.RequireProducer(t, v, "serviceKeyResponsibilityID")
		got, err := serviceKeyResponsibilityID(
			shared.ParticipantType(v.Uint(t, 0, "participant_type")),
			hubtypes.ServiceKeyResponsibilityKind(v.Uint(t, 1, "responsibility_kind")),
			v.String(t, 2, "session_id"), v.String(t, 3, "task_id"), v.String(t, 4, "scope_id"),
			v.String(t, 5, "operator_address"),
		)
		require.NoError(t, err)
		domainfixture.RequireDigest(t, v, got)
		mark(v.Name)
	})

	t.Run("taskFaultSummaryHash", func(t *testing.T) {
		v := vector("fault_summary_v1")
		domainfixture.RequireProducer(t, v, "taskFaultSummaryHash")
		elements := taskKeeperElements(t, v, 3, 2, "faults", "fault_count")
		faults := make([]taskRoleFaultRecord, len(elements))
		for index, element := range elements {
			where := fmt.Sprintf("%s.fault_%d", v.Name, index)
			faults[len(elements)-1-index] = taskRoleFaultRecord{
				faultID:              taskKeeperFieldBytes(t, element, 0, "fault_id", where),
				operatorAddress:      taskKeeperFieldAddress(t, element, 1, "operator_address", where),
				duty:                 shared.Duty(taskKeeperFieldUint(t, element, 2, "duty", where)),
				faultClass:           hubtypes.FaultKind(taskKeeperFieldUint(t, element, 3, "fault_class", where)),
				classificationSource: shared.FailureClassificationSource(taskKeeperFieldUint(t, element, 4, "classification_source", where)),
				evidenceDigest:       taskKeeperFieldBytes(t, element, 5, "evidence_digest", where),
				jailDelta:            uint32(taskKeeperFieldUint(t, element, 6, "jail_delta", where)),
				status:               hubtypes.RoleFaultStatus(taskKeeperFieldUint(t, element, 7, "status", where)),
			}
		}
		got, err := taskFaultSummaryHash(v.String(t, 0, "chain_id"), v.Bytes(t, 1, "task_id"), faults)
		require.NoError(t, err)
		domainfixture.RequireDigest(t, v, got)
		mark(v.Name)
	})

	t.Run("taskStageAddedBitmapHash", func(t *testing.T) {
		v := vector("task_stage_added_bitmap_v1")
		domainfixture.RequireProducer(t, v, "taskStageAddedBitmapHash")
		elements := taskKeeperElements(t, v, 5, 4, "added_slots", "added_member_count")
		slots := make([]uint32, len(elements))
		for index, element := range elements {
			slots[len(elements)-1-index] = uint32(taskKeeperFieldUint(t, element, 0, "slot", v.Name))
		}
		got, err := taskStageAddedBitmapHash(
			v.String(t, 0, "chain_id"), v.Bytes(t, 1, "task_id"), types.TaskCandidateStage(v.Uint(t, 2, "stage")),
			v.Bytes(t, 3, "proposal_digest"), slots,
		)
		require.NoError(t, err)
		domainfixture.RequireDigest(t, v, got[:])
		mark(v.Name)
	})

	t.Run("appendVerifierFactFold", func(t *testing.T) {
		leaf := vector("verifier_finalize_cursor_leaf_v1")
		fold := vector("verifier_finalize_cursor_fold_v1")
		domainfixture.RequireProducer(t, leaf, "appendVerifierFactFold")
		domainfixture.RequireProducer(t, fold, "appendVerifierFactFold")
		var inputs taskKeeperProducerInputs
		domainfixture.DecodeInputs(t, leaf, &inputs)
		taskID := domainfixture.DecodeHex(t, "task_id_hex", inputs.TaskIDHex)
		fields := leaf.Frame(t, 0, "canonical_materialized_fact_chunk").Fields
		chunk, err := encodeVerifierFactTypedChunk(taskID, types.TaskCandidateFactState{
			SchemaVersion: types.AssignmentCandidateSchemaVersionV1, TaskId: taskID,
			Stage: types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY, Duty: shared.Duty_DUTY_VERIFIER,
			Slot:                           uint32(taskKeeperFieldUint(t, fields, 0, "slot", leaf.Name)),
			SlotVersion:                    taskKeeperFieldUint(t, fields, 1, "slot_version", leaf.Name),
			OperatorAddress:                taskKeeperFieldAddress(t, fields, 2, "operator_address", leaf.Name),
			CandidateWeight:                uint32(taskKeeperFieldUint(t, fields, 3, "candidate_weight", leaf.Name)),
			ActiveBondSnapshot:             shared.NewAmount(taskKeeperFieldUint(t, fields, 4, "active_bond_snapshot", leaf.Name)),
			AvailableBondSnapshot:          shared.NewAmount(taskKeeperFieldUint(t, fields, 5, "available_bond_snapshot", leaf.Name)),
			RequiredTaskLiabilitySnapshot:  shared.NewAmount(taskKeeperFieldUint(t, fields, 6, "required_task_liability_snapshot", leaf.Name)),
			MinStakeSnapshot:               shared.NewAmount(taskKeeperFieldUint(t, fields, 7, "min_stake_snapshot", leaf.Name)),
			PerformanceScoreSnapshotPpm:    uint32(taskKeeperFieldUint(t, fields, 8, "performance_score_snapshot_ppm", leaf.Name)),
			PerformanceMethodVersion:       uint32(taskKeeperFieldUint(t, fields, 9, "performance_method_version", leaf.Name)),
			CandidateJailFactorSnapshotPpm: uint32(taskKeeperFieldUint(t, fields, 10, "candidate_jail_factor_snapshot_ppm", leaf.Name)),
			BondVersionSnapshot:            taskKeeperFieldUint(t, fields, 11, "bond_version_snapshot", leaf.Name),
			SupportVersionSnapshot:         taskKeeperFieldUint(t, fields, 12, "support_version_snapshot", leaf.Name),
			CapabilityVersionSnapshot:      taskKeeperFieldUint(t, fields, 13, "capability_version_snapshot", leaf.Name),
			HandraiseSigningDigest:         taskKeeperFieldBytes(t, fields, 14, "handraise_signing_digest", leaf.Name),
		})
		require.NoError(t, err)
		require.Equal(t, leaf.Digest(), hex.EncodeToString(fold.Bytes(t, 1, "leaf_hash")))
		got, err := appendVerifierFactFold(fold.Bytes(t, 0, "previous_commitment"), chunk,
			uint32(fold.Uint(t, 2, "materialized_count")))
		require.NoError(t, err)
		domainfixture.RequireDigest(t, fold, got)
		mark(leaf.Name)
		mark(fold.Name)
	})

	t.Run("workerProposalDigest", func(t *testing.T) {
		v := vector("worker_proposal_v1")
		domainfixture.RequireProducer(t, v, "workerProposalDigest")
		require.Equal(t, uint64(types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK), v.Uint(t, 3, "stage"))
		require.Empty(t, v.Bytes(t, 7, "data_ready_attestation_or_empty"))
		got, err := workerProposalDigest(
			v.String(t, 0, "chain_id"), v.Bytes(t, 1, "task_id"), v.Bytes(t, 2, "task_hash"),
			v.Bytes(t, 4, "candidate_pool_snapshot_id"), v.Bytes(t, 5, "candidate_pool_hash"),
			v.Address(t, 6, "proposer_operator_or_empty"), taskKeeperHandraises(t, v),
		)
		require.NoError(t, err)
		domainfixture.RequireDigest(t, v, got[:])
		mark(v.Name)
	})

	t.Run("verifierProposalDigest", func(t *testing.T) {
		v := vector("open_verify_proposal_v1")
		domainfixture.RequireProducer(t, v, "verifierProposalDigest")
		require.Equal(t, uint64(types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY), v.Uint(t, 3, "stage"))
		require.Equal(t, []byte{1}, v.Bytes(t, 7, "data_ready_attestation_or_empty"))
		attestation := true
		got, err := verifierProposalDigest(
			v.String(t, 0, "chain_id"), v.Bytes(t, 1, "task_id"), v.Bytes(t, 2, "task_hash"),
			v.Bytes(t, 4, "candidate_pool_snapshot_id"), v.Bytes(t, 5, "candidate_pool_hash"),
			v.Address(t, 6, "proposer_operator_or_empty"), &attestation, taskKeeperHandraises(t, v),
		)
		require.NoError(t, err)
		domainfixture.RequireDigest(t, v, got[:])
		mark(v.Name)
	})

	t.Run("verifierWindowSourceHash", func(t *testing.T) {
		v := vector("verifier_window_source_v1")
		domainfixture.RequireProducer(t, v, "verifierWindowSourceHash")
		var inputs taskKeeperProducerInputs
		domainfixture.DecodeInputs(t, v, &inputs)
		taskID := v.Bytes(t, 1, "task_id")
		verifyRound := uint32(v.Uint(t, 2, "verify_round"))
		elements := taskKeeperElements(t, v, 9, 8, "segments", "eligibility_segment_count")
		segments := make([]types.VerifierCandidateEligibilitySegmentState, 0, len(elements))
		for index, element := range elements {
			where := fmt.Sprintf("%s.segment_%d", v.Name, index)
			segments = append(segments, types.VerifierCandidateEligibilitySegmentState{
				TaskId: taskID, VerifyRound: verifyRound,
				SegmentIndex: uint32(taskKeeperFieldUint(t, element, 0, "segment_index", where)),
				Bitmap:       taskKeeperFieldBytes(t, element, 1, "segment_bitmap_bytes", where),
			})
		}
		got, err := verifierWindowSourceHash(v.String(t, 0, "chain_id"), types.VerifierCandidateWindowState{
			TaskId: taskID, VerifyRound: verifyRound, InferReceiptHash: v.Bytes(t, 3, "infer_receipt_hash"),
			CandidatePoolSnapshotId: v.Bytes(t, 4, "candidate_pool_snapshot_id"),
			CandidatePoolHash:       v.Bytes(t, 5, "candidate_pool_hash"),
			EligibilityFrozenHeight: v.Uint(t, 6, "eligibility_frozen_height"),
			EligibleCount:           uint32(v.Uint(t, 7, "eligible_count")),
			EligibilitySegmentCount: uint32(v.Uint(t, 8, "eligibility_segment_count")),
		}, inputs.SlotCapacity, inputs.SegmentBytes, segments)
		require.NoError(t, err)
		domainfixture.RequireDigest(t, v, got[:])
		mark(v.Name)
	})

	t.Run("verifierWindowRank", func(t *testing.T) {
		v := vector("verifier_window_rank_v1")
		domainfixture.RequireProducer(t, v, "verifierWindowRank")
		got, err := verifierWindowRank(v.String(t, 0, "chain_id"), types.VerifierCandidateWindowState{
			TaskId: v.Bytes(t, 1, "task_id"), VerifyRound: uint32(v.Uint(t, 2, "verify_round")),
			InferReceiptHash:         v.Bytes(t, 3, "infer_receipt_hash"),
			CandidatePoolSnapshotId:  v.Bytes(t, 4, "candidate_pool_snapshot_id"),
			CandidatePoolHash:        v.Bytes(t, 5, "candidate_pool_hash"),
			VerifierWindowSourceHash: v.Bytes(t, 6, "verifier_window_source_hash"),
			WindowRandomnessHeight:   v.Uint(t, 7, "verifier_window_randomness_height"),
		}, v.Bytes(t, 8, "verifier_window_randomness_beacon"), verifierWindowCandidate{
			Slot: uint32(v.Uint(t, 9, "slot")), SlotVersion: v.Uint(t, 10, "slot_version"),
			OperatorAddress: v.Address(t, 11, "operator_address"),
		})
		require.NoError(t, err)
		domainfixture.RequireDigest(t, v, got[:])
		mark(v.Name)
	})

	t.Run("verifierWindowHash", func(t *testing.T) {
		v := vector("verifier_window_v1")
		domainfixture.RequireProducer(t, v, "verifierWindowHash")
		header, randomness, members := taskKeeperVerifierWindow(t, v)
		got, err := verifierWindowHash(v.String(t, 0, "chain_id"), header, randomness, members)
		require.NoError(t, err)
		domainfixture.RequireDigest(t, v, got[:])
		mark(v.Name)
	})

	t.Run("verifierLegalSetHash", func(t *testing.T) {
		v := vector("verifier_legal_set_v1")
		window := vector("verifier_window_v1")
		domainfixture.RequireProducer(t, v, "verifierLegalSetHash")
		var inputs taskKeeperProducerInputs
		domainfixture.DecodeInputs(t, v, &inputs)
		header, randomness, members := taskKeeperVerifierWindow(t, window)
		chainID := v.String(t, 0, "chain_id")
		taskID := v.Bytes(t, 1, "task_id")
		windowHash := v.Bytes(t, 5, "verifier_window_hash")
		require.Equal(t, window.Digest(), hex.EncodeToString(windowHash))
		header.Status = types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY
		header.XWindowRandomnessBeacon = &types.VerifierCandidateWindowState_WindowRandomnessBeacon{WindowRandomnessBeacon: randomness}
		header.XVerifierWindowHash = &types.VerifierCandidateWindowState_VerifierWindowHash{VerifierWindowHash: windowHash}
		header.HandraiseCloseHeight = inputs.HandraiseCloseHeight
		header.SelectionRandomnessHeight = inputs.SelectionRandomnessHeight

		elements := taskKeeperElements(t, v, 8, 7, "candidate_facts", "candidate_count")
		facts := make([]types.TaskCandidateFactState, len(elements))
		for index, element := range elements {
			where := fmt.Sprintf("%s.candidate_%d", v.Name, index)
			facts[len(elements)-1-index] = taskKeeperCandidateFact(t, element, where, taskID, index)
		}
		got, err := verifierLegalSetHash(chainID, header, types.TaskStageHandraiseUnionState{
			SchemaVersion: types.AssignmentCandidateSchemaVersionV1, TaskId: taskID,
			Stage:                   types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY,
			CandidatePoolSnapshotId: v.Bytes(t, 3, "candidate_pool_snapshot_id"),
			CandidatePoolHash:       v.Bytes(t, 4, "candidate_pool_hash"),
			UnionCount:              uint32(len(facts)), Status: types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_FINALIZING,
			WindowCloseHeight: inputs.HandraiseCloseHeight, UnionBitmapHash: v.Bytes(t, 6, "union_bitmap_hash"),
			XSelectionRandomnessHeight: &types.TaskStageHandraiseUnionState_SelectionRandomnessHeight{
				SelectionRandomnessHeight: inputs.SelectionRandomnessHeight,
			},
		}, members, facts)
		require.NoError(t, err)
		domainfixture.RequireDigest(t, v, got[:])
		mark(v.Name)
	})

	t.Run("SelectedVerifierRefsHash", func(t *testing.T) {
		v := vector("selected_verifiers_v1")
		domainfixture.RequireProducer(t, v, "SelectedVerifierRefsHash")
		elements := taskKeeperElements(t, v, 7, 6, "selected_verifiers", "selected_count")
		refs := make([]types.SelectedVerifierV1, 0, len(elements))
		for index, element := range elements {
			where := fmt.Sprintf("%s.selected_%d", v.Name, index)
			require.Equal(t, uint64(index), taskKeeperFieldUint(t, element, 0, "selection_index", where))
			refs = append(refs, types.SelectedVerifierV1{
				Slot:            uint32(taskKeeperFieldUint(t, element, 1, "slot", where)),
				SlotVersion:     taskKeeperFieldUint(t, element, 2, "slot_version", where),
				OperatorAddress: taskKeeperFieldAddress(t, element, 3, "operator_address", where),
			})
		}
		got, err := SelectedVerifierRefsHash(
			v.String(t, 0, "chain_id"), v.Bytes(t, 1, "task_id"), uint32(v.Uint(t, 2, "verify_round")),
			v.Bytes(t, 3, "verifier_legal_set_hash"), v.Uint(t, 4, "randomness_height"),
			v.Bytes(t, 5, "randomness_beacon"), refs,
		)
		require.NoError(t, err)
		domainfixture.RequireDigest(t, v, got)
		mark(v.Name)
	})

	t.Run("ResultReceiptRefsHash", func(t *testing.T) {
		cluster := vector("consensus_cluster_v1")
		refsVector := vector("result_receipt_refs_v1")
		domainfixture.RequireProducer(t, refsVector, "ResultReceiptRefsHash")
		chainID := cluster.String(t, 0, "chain_id")
		taskID := cluster.Bytes(t, 1, "task_id")
		verifyRound := uint32(cluster.Uint(t, 2, "verify_round"))
		require.Equal(t, chainID, refsVector.String(t, 0, "chain_id"))
		require.Equal(t, taskID, refsVector.Bytes(t, 1, "task_id"))
		require.Equal(t, uint64(verifyRound), refsVector.Uint(t, 2, "verify_round"))
		clusterElements := taskKeeperElements(t, cluster, 4, 3, "cluster_members", "cluster_member_count")
		refElements := taskKeeperElements(t, refsVector, 4, 3, "result_receipt_refs", "cluster_member_count")
		require.Len(t, refElements, len(clusterElements))
		members := make([]types.ConsensusClusterMemberV1, 0, len(clusterElements))
		for index, element := range clusterElements {
			where := fmt.Sprintf("%s.member_%d", cluster.Name, index)
			refWhere := fmt.Sprintf("%s.ref_%d", refsVector.Name, index)
			member := types.ConsensusClusterMemberV1{
				SelectedVerifierIndex:      uint32(taskKeeperFieldUint(t, element, 0, "selected_verifier_index", where)),
				VerifierOperatorAddress:    taskKeeperFieldAddress(t, element, 1, "verifier_operator_address", where),
				CommitKey:                  taskKeeperFieldBytes(t, element, 2, "commit_key", where),
				ResultReceiptSigningDigest: taskKeeperFieldBytes(t, element, 3, "result_receipt_signing_digest", where),
				MetricSummaryHash:          taskKeeperFieldBytes(t, element, 4, "metric_summary_hash", where),
				SampleVerdict:              types.MetricSampleVerdictV1(taskKeeperFieldUint(t, element, 5, "sample_verdict", where)),
				AcceptedHeight:             taskKeeperFieldUint(t, element, 6, "accepted_height", where),
			}
			require.Equal(t, uint64(member.SelectedVerifierIndex), taskKeeperFieldUint(t, refElements[index], 0, "selected_verifier_index", refWhere))
			require.Equal(t, member.VerifierOperatorAddress, taskKeeperFieldAddress(t, refElements[index], 1, "verifier_operator_address", refWhere))
			require.Equal(t, member.CommitKey, taskKeeperFieldBytes(t, refElements[index], 2, "commit_key", refWhere))
			require.Equal(t, member.ResultReceiptSigningDigest, taskKeeperFieldBytes(t, refElements[index], 3, "result_receipt_signing_digest", refWhere))
			members = append(members, member)
		}
		refsHash, err := types.ResultReceiptRefsHash(chainID, taskID, verifyRound, members)
		require.NoError(t, err)
		domainfixture.RequireDigest(t, refsVector, refsHash[:])
		mark(refsVector.Name)
	})

	t.Run("epochTaskSummarySourceAndFold", func(t *testing.T) {
		source := vector("epoch_task_summary_source_v1")
		fold := vector("epoch_task_summary_fold_v1")
		domainfixture.RequireProducer(t, source, "epochTaskSummarySourceHash")
		domainfixture.RequireProducer(t, fold, "epochTaskSummaryFoldHash")
		chainID := source.String(t, 0, "chain_id")
		epoch := source.Uint(t, 1, "epoch")
		got, err := epochTaskSummarySourceHash(chainID, epoch, types.TaskTerminalSummaryState{
			SettlementHeight: source.Uint(t, 2, "settlement_height"), TaskId: source.Bytes(t, 3, "task_id"),
			FailureClass:   types.TaskFailureClass(source.Uint(t, 4, "failure_class")),
			Verdict:        types.TaskVerdict_TASK_VERDICT_PASS,
			FinalityStatus: shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL,
			ModelId:        source.String(t, 6, "model_id"), ProfileVersion: uint32(source.Uint(t, 7, "profile_version")),
		})
		require.NoError(t, err)
		require.True(t, source.BoolValue(t, 5, "support_candidate"))
		domainfixture.RequireDigest(t, source, got)
		require.Equal(t, chainID, fold.String(t, 0, "chain_id"))
		require.Equal(t, epoch, fold.Uint(t, 1, "epoch"))
		require.Equal(t, source.Digest(), hex.EncodeToString(fold.Bytes(t, 3, "source_hash")))
		folded, err := epochTaskSummaryFoldHash(chainID, epoch, fold.Bytes(t, 2, "previous_root"), got,
			fold.Uint(t, 4, "source_count"))
		require.NoError(t, err)
		domainfixture.RequireDigest(t, fold, folded)
		mark(source.Name)
		mark(fold.Name)
	})

	t.Run("epochTaskSummaryReceiptHash", func(t *testing.T) {
		v := vector("epoch_task_summary_receipt_v1")
		domainfixture.RequireProducer(t, v, "epochTaskSummaryReceiptHash")
		histogramFields := taskKeeperRepeated(t, v, 8, "histogram")
		histogram := make([]uint64, 0, len(histogramFields))
		for index, field := range histogramFields {
			require.Equal(t, fmt.Sprintf("bucket_%d", index), field.Name)
			histogram = append(histogram, field.Uint(t, v.Name+".histogram"))
		}
		candidateFields := taskKeeperRepeated(t, v, 10, "support_candidates")
		candidates := make([]string, 0, len(candidateFields))
		for index, field := range candidateFields {
			require.Equal(t, fmt.Sprintf("candidate_%d", index), field.Name)
			candidates = append(candidates, field.String(t, v.Name+".support_candidates"))
		}
		epoch := v.Uint(t, 1, "epoch")
		taskCount := v.Uint(t, 6, "task_count")
		got, err := epochTaskSummaryReceiptHash(v.String(t, 0, "chain_id"), types.EpochTaskSummaryReceiptState{
			Epoch: epoch, StartHeight: v.Uint(t, 2, "start_height"), EndHeight: v.Uint(t, 3, "end_height"),
			DueHeight: v.Uint(t, 4, "due_height"), DispatchedHeight: v.Uint(t, 5, "dispatched_height"),
			Summary: shared.EpochTaskSummary{Epoch: epoch, TaskCount: taskCount,
				ValidTaskCount: v.Uint(t, 7, "valid_task_count"), Histogram: histogram, SupportCandidates: candidates},
			SourceCount: taskCount, SupportCandidateSeenCount: v.Uint(t, 9, "support_candidate_seen_count"),
			RetainedSupportCandidateCount: uint32(len(candidates)), SourceRoot: v.Bytes(t, 11, "source_root"),
		})
		require.NoError(t, err)
		domainfixture.RequireDigest(t, v, got)
		mark(v.Name)
	})

	classification := vector("classification_evidence_digest_v1")
	require.Equal(t, "task_evidence_root_or_zero32", classification.Field(t, 7, "task_evidence_root_or_zero32").Name)
	require.Equal(t, "bytes", classification.Field(t, 9, "superseded_by_challenge_id_or_zero32").Type)
	terminal := vector("task_terminal_summary_v1")
	require.Len(t, terminal.Fields, 35,
		"remove the exception only after the fixture represents the current 45-field producer")

	cluster := vector("consensus_cluster_v1")
	domainfixture.RequireProducer(t, cluster, "ConsensusClusterHash")
	for index, element := range taskKeeperElements(t, cluster, 4, 3, "cluster_members", "cluster_member_count") {
		require.Len(t, element, 7, "cluster member %d lacks current generated_token_count", index)
	}
	exceptions := map[string]string{
		classification.Name: "fixture fields 7/9 describe the retired task-evidence-root/Hash32 challenge-id shape; the current producer frames settlement_facts_hash and Uint32(superseded_by)",
		cluster.Name:        "fixture cluster members have seven fields; the current producer appends generated_token_count as field eight",
		terminal.Name:       "fixture is the retired 35-field summary; the Phase 0 producer hashes 45 fields and requires commitments the vector does not publish",
	}
	domainfixture.RequireBoundSet(t, vectors, bound, exceptions)
}

func taskKeeperElements(t *testing.T, v domainfixture.Vector, index, countIndex int, name, countName string) [][]domainfixture.Field {
	t.Helper()
	fields := v.Repeated(t, index, countIndex, name, countName)
	elements := make([][]domainfixture.Field, 0, len(fields))
	for _, field := range fields {
		if field.Type == "frame" {
			elements = append(elements, field.Fields)
		} else {
			elements = append(elements, []domainfixture.Field{field})
		}
	}
	return elements
}

func taskKeeperRepeated(t *testing.T, v domainfixture.Vector, index int, name string) []domainfixture.Field {
	t.Helper()
	frame := v.Frame(t, index, name)
	require.NotEmpty(t, frame.Fields)
	count := frame.Field(t, 0, "element_count")
	require.Equal(t, uint64(len(frame.Fields)-1), count.Uint(t, v.Name+"."+name+".element_count"))
	return frame.Fields[1:]
}

func taskKeeperField(t *testing.T, fields []domainfixture.Field, index int, name, where string) domainfixture.Field {
	t.Helper()
	require.Less(t, index, len(fields), where)
	field := fields[index]
	require.Equal(t, name, field.Name, where)
	return field
}

func taskKeeperFieldUint(t *testing.T, fields []domainfixture.Field, index int, name, where string) uint64 {
	t.Helper()
	return taskKeeperField(t, fields, index, name, where).Uint(t, where+"."+name)
}

func taskKeeperFieldBytes(t *testing.T, fields []domainfixture.Field, index int, name, where string) []byte {
	t.Helper()
	return taskKeeperField(t, fields, index, name, where).Bytes(t, where+"."+name)
}

func taskKeeperFieldAddress(t *testing.T, fields []domainfixture.Field, index int, name, where string) string {
	t.Helper()
	field := taskKeeperField(t, fields, index, name, where)
	require.Equal(t, "address", field.Type, where)
	require.NotEmpty(t, field.Bech32, where)
	return field.Bech32
}

func taskKeeperHandraises(t *testing.T, v domainfixture.Vector) []candidateSigningDigest {
	t.Helper()
	elements := taskKeeperElements(t, v, 9, 8, "handraise_digests", "handraise_count")
	handraises := make([]candidateSigningDigest, len(elements))
	for index, element := range elements {
		handraises[len(elements)-1-index] = candidateSigningDigest{
			Slot: uint32(index + 1), Digest: taskKeeperFieldBytes(t, element, 0, "handraise_signing_digest", v.Name),
		}
	}
	return handraises
}

func taskKeeperVerifierWindow(t *testing.T, v domainfixture.Vector) (
	types.VerifierCandidateWindowState, []byte, []types.VerifierCandidateWindowMemberState,
) {
	t.Helper()
	taskID := v.Bytes(t, 1, "task_id")
	verifyRound := uint32(v.Uint(t, 2, "verify_round"))
	header := types.VerifierCandidateWindowState{
		SchemaVersion: 1, TaskId: taskID, VerifyRound: verifyRound,
		InferReceiptHash:         v.Bytes(t, 3, "infer_receipt_hash"),
		CandidatePoolSnapshotId:  v.Bytes(t, 4, "candidate_pool_snapshot_id"),
		CandidatePoolHash:        v.Bytes(t, 5, "candidate_pool_hash"),
		VerifierWindowSourceHash: v.Bytes(t, 6, "verifier_window_source_hash"),
		EligibleCount:            uint32(v.Uint(t, 7, "eligible_count")),
		WindowRandomnessHeight:   v.Uint(t, 8, "verifier_window_randomness_height"),
		WindowSize:               uint32(v.Uint(t, 10, "window_size")),
	}
	elements := taskKeeperElements(t, v, 11, 10, "members", "window_size")
	members := make([]types.VerifierCandidateWindowMemberState, 0, len(elements))
	for index, element := range elements {
		where := fmt.Sprintf("%s.member_%d", v.Name, index)
		members = append(members, types.VerifierCandidateWindowMemberState{
			SchemaVersion: 1, TaskId: taskID, VerifyRound: verifyRound,
			RankIndex:          uint32(taskKeeperFieldUint(t, element, 0, "rank_index", where)),
			Slot:               uint32(taskKeeperFieldUint(t, element, 1, "slot", where)),
			SlotVersion:        taskKeeperFieldUint(t, element, 2, "slot_version", where),
			OperatorAddress:    taskKeeperFieldAddress(t, element, 3, "operator_address", where),
			VerifierWindowRank: taskKeeperFieldBytes(t, element, 4, "verifier_window_rank", where),
		})
	}
	return header, v.Bytes(t, 9, "verifier_window_randomness_beacon"), members
}

func taskKeeperCandidateFact(t *testing.T, fields []domainfixture.Field, where string, taskID []byte, index int) types.TaskCandidateFactState {
	t.Helper()
	return types.TaskCandidateFactState{
		SchemaVersion: types.AssignmentCandidateSchemaVersionV1, TaskId: taskID,
		Stage: types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY, Duty: shared.Duty_DUTY_VERIFIER,
		Slot:                           uint32(taskKeeperFieldUint(t, fields, 0, "slot", where)),
		SlotVersion:                    taskKeeperFieldUint(t, fields, 1, "slot_version", where),
		OperatorAddress:                taskKeeperFieldAddress(t, fields, 2, "operator_address", where),
		CandidateWeight:                uint32(taskKeeperFieldUint(t, fields, 3, "candidate_weight", where)),
		ActiveBondSnapshot:             shared.NewAmount(taskKeeperFieldUint(t, fields, 4, "active_bond_snapshot", where)),
		AvailableBondSnapshot:          shared.NewAmount(taskKeeperFieldUint(t, fields, 5, "available_bond_snapshot", where)),
		RequiredTaskLiabilitySnapshot:  shared.NewAmount(taskKeeperFieldUint(t, fields, 6, "required_task_liability_snapshot", where)),
		MinStakeSnapshot:               shared.NewAmount(taskKeeperFieldUint(t, fields, 7, "min_stake_snapshot", where)),
		PerformanceScoreSnapshotPpm:    uint32(taskKeeperFieldUint(t, fields, 8, "performance_score_snapshot_ppm", where)),
		PerformanceMethodVersion:       uint32(taskKeeperFieldUint(t, fields, 9, "performance_method_version", where)),
		CandidateJailFactorSnapshotPpm: uint32(taskKeeperFieldUint(t, fields, 10, "candidate_jail_factor_snapshot_ppm", where)),
		BondVersionSnapshot:            taskKeeperFieldUint(t, fields, 11, "bond_version_snapshot", where),
		SupportVersionSnapshot:         taskKeeperFieldUint(t, fields, 12, "support_version_snapshot", where),
		CapabilityVersionSnapshot:      uint64(index + 1),
		HandraiseSigningDigest:         bytes.Repeat([]byte{byte(0xd0 + index)}, types.Hash32Len),
	}
}

func taskKeeperSlotForDigest(t *testing.T, digestHex string, candidates []types.TaskCandidateFactState) uint32 {
	t.Helper()
	digest := domainfixture.DecodeHex(t, "weighted_draw_v1.digest_hex", digestHex)
	var total uint64
	for _, candidate := range candidates {
		total += uint64(candidate.CandidateWeight)
	}
	x := binary.BigEndian.Uint64(digest[:8])
	require.GreaterOrEqual(t, x, -total%total)
	target := x % total
	var cumulative uint64
	for _, candidate := range candidates {
		cumulative += uint64(candidate.CandidateWeight)
		if cumulative > target {
			return candidate.Slot
		}
	}
	t.Fatal("weighted draw target is outside cumulative weight")
	return 0
}
