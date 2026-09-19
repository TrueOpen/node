package types

import (
	"bytes"
	"fmt"
	"sort"

	shared "github.com/TrueOpen/node/x/shared/types"
)

const (
	AssignmentCandidateSchemaVersionV1 uint32 = 1
	MaxAssignmentCandidateWeight       uint32 = 1_000_000
	PerformanceScoreDefaultPpm         uint32 = 1_000_000
	PerformanceMethodRawQ16V1          uint32 = 1
)

// AssignmentCandidateSetHash derives the sole task-local Worker legal-set
// commitment. Facts are encoded in slot order and are never copied into the
// AssignmentCandidateSetState header.
func AssignmentCandidateSetHash(chainID string, header AssignmentCandidateSetState, facts []TaskCandidateFactState) ([32]byte, error) {
	var zero [32]byte
	if chainID == "" {
		return zero, fmt.Errorf("chain_id is required")
	}
	for name, value := range map[string][]byte{
		"task_id": header.TaskId, "task_hash": header.TaskHash,
		"candidate_pool_snapshot_id": header.CandidatePoolSnapshotId,
		"candidate_pool_hash":        header.CandidatePoolHash, "union_bitmap_hash": header.UnionBitmapHash,
	} {
		if len(value) != Hash32Len {
			return zero, fmt.Errorf("%s must be exactly %d raw bytes", name, Hash32Len)
		}
	}
	if header.SchemaVersion != AssignmentCandidateSchemaVersionV1 {
		return zero, fmt.Errorf("unsupported assignment candidate schema version %d", header.SchemaVersion)
	}
	if int(header.CandidateCount) != len(facts) {
		return zero, fmt.Errorf("candidate_count %d does not match %d facts", header.CandidateCount, len(facts))
	}

	ordered := append([]TaskCandidateFactState(nil), facts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Slot < ordered[j].Slot })
	candidateFrames := make([]shared.CanonicalFrameV1, len(ordered))
	var previousSlot uint32
	for index, fact := range ordered {
		if err := validateAssignmentCandidateFact(header.TaskId, fact); err != nil {
			return zero, fmt.Errorf("candidate fact %d: %w", index, err)
		}
		if index != 0 && fact.Slot <= previousSlot {
			return zero, fmt.Errorf("candidate slots must be unique")
		}
		previousSlot = fact.Slot
		operator, err := CanonicalOperatorAddressBytes("operator_address", fact.OperatorAddress)
		if err != nil {
			return zero, err
		}
		activeBond, err := shared.ParseAmount(fact.ActiveBondSnapshot)
		if err != nil {
			return zero, fmt.Errorf("active_bond_snapshot: %w", err)
		}
		availableBond, err := shared.ParseAmount(fact.AvailableBondSnapshot)
		if err != nil {
			return zero, fmt.Errorf("available_bond_snapshot: %w", err)
		}
		requiredLiability, err := shared.ParseAmount(fact.RequiredTaskLiabilitySnapshot)
		if err != nil {
			return zero, fmt.Errorf("required_task_liability_snapshot: %w", err)
		}
		minStake, err := shared.ParseAmount(fact.MinStakeSnapshot)
		if err != nil {
			return zero, fmt.Errorf("min_stake_snapshot: %w", err)
		}
		candidateFrames[index] = shared.FlatCanonicalFrameV1(
			shared.Uint32BE(fact.Slot), shared.Uint64BE(fact.SlotVersion), operator,
			shared.Uint32BE(fact.CandidateWeight), shared.Uint64BE(activeBond),
			shared.Uint64BE(availableBond), shared.Uint64BE(requiredLiability), shared.Uint64BE(minStake),
			shared.Uint32BE(fact.PerformanceScoreSnapshotPpm), shared.Uint32BE(fact.PerformanceMethodVersion),
			shared.Uint32BE(fact.CandidateJailFactorSnapshotPpm), shared.Uint64BE(fact.BondVersionSnapshot),
			shared.Uint64BE(fact.SupportVersionSnapshot))
	}
	repeatedCandidates := shared.CanonicalRepeatedFramesV1(candidateFrames)
	if err := repeatedCandidates.Err(); err != nil {
		return zero, fmt.Errorf("assignment candidates: %w", err)
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainAssignmentLegalSetV1)).Raw(
		[]byte(chainID), header.TaskId, header.TaskHash,
		header.CandidatePoolSnapshotId, header.CandidatePoolHash, header.UnionBitmapHash,
		shared.Uint32BE(header.CandidateCount),
	).Nested(repeatedCandidates).Sum()
	if err != nil {
		return zero, err
	}
	return [32]byte(digest), nil
}

func validateAssignmentCandidateFact(taskID []byte, fact TaskCandidateFactState) error {
	if fact.SchemaVersion != AssignmentCandidateSchemaVersionV1 {
		return fmt.Errorf("unsupported schema version %d", fact.SchemaVersion)
	}
	if !bytes.Equal(taskID, fact.TaskId) {
		return fmt.Errorf("task_id mismatch")
	}
	if fact.Stage != TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK {
		return fmt.Errorf("stage must be OPEN_TASK")
	}
	if fact.SlotVersion == 0 || fact.CandidateWeight == 0 || fact.CandidateWeight > MaxAssignmentCandidateWeight {
		return fmt.Errorf("slot_version and candidate_weight must be in range")
	}
	if fact.Duty != shared.Duty_DUTY_WORKER {
		return fmt.Errorf("duty must be WORKER")
	}
	if fact.PerformanceScoreSnapshotPpm != PerformanceScoreDefaultPpm || fact.PerformanceMethodVersion != PerformanceMethodRawQ16V1 {
		return fmt.Errorf("unsupported performance snapshot")
	}
	if fact.CandidateJailFactorSnapshotPpm == 0 || fact.CandidateJailFactorSnapshotPpm > 1_000_000 {
		return fmt.Errorf("candidate_jail_factor_snapshot_ppm must be in 1..1000000")
	}
	if fact.BondVersionSnapshot == 0 || fact.CapabilityVersionSnapshot == 0 || fact.SupportVersionSnapshot == 0 {
		return fmt.Errorf("bond, capability, and support versions must be non-zero")
	}
	if len(fact.HandraiseSigningDigest) != Hash32Len {
		return fmt.Errorf("handraise_signing_digest must be exactly %d raw bytes", Hash32Len)
	}
	return nil
}
