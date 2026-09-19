package types

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestAssignmentCandidateSetHashFramesEachFact(t *testing.T) {
	taskID := bytes.Repeat([]byte{1}, Hash32Len)
	header := AssignmentCandidateSetState{
		SchemaVersion: AssignmentCandidateSchemaVersionV1,
		TaskId:        taskID, TaskHash: bytes.Repeat([]byte{2}, Hash32Len),
		CandidatePoolSnapshotId: bytes.Repeat([]byte{3}, Hash32Len),
		CandidatePoolHash:       bytes.Repeat([]byte{4}, Hash32Len),
		UnionBitmapHash:         bytes.Repeat([]byte{5}, Hash32Len), CandidateCount: 1,
	}
	fact := TaskCandidateFactState{
		SchemaVersion: AssignmentCandidateSchemaVersionV1, TaskId: taskID,
		Stage: TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK,
		Slot:  7, SlotVersion: 11,
		OperatorAddress:    "trueopen15zs69gay5kn2029f4246etdw47ctrv4ns6facc",
		Duty:               1,
		ActiveBondSnapshot: shared.NewAmount(30), AvailableBondSnapshot: shared.NewAmount(20),
		RequiredTaskLiabilitySnapshot: shared.NewAmount(4), MinStakeSnapshot: shared.NewAmount(10),
		PerformanceScoreSnapshotPpm:    PerformanceScoreDefaultPpm,
		PerformanceMethodVersion:       PerformanceMethodRawQ16V1,
		CandidateJailFactorSnapshotPpm: 500_000, BondVersionSnapshot: 13, CapabilityVersionSnapshot: 15,
		SupportVersionSnapshot: 17, CandidateWeight: 400_000,
		HandraiseSigningDigest: bytes.Repeat([]byte{6}, Hash32Len),
	}
	digest, err := AssignmentCandidateSetHash("trueopen-test", header, []TaskCandidateFactState{fact})
	require.NoError(t, err)
	// An external anchor: `expected` below rebuilds the nested preimage field by
	// field, so it agrees with AssignmentCandidateSetHash after any reordering
	// applied to both. The frozen hex is the value that cannot follow such an edit.
	require.Equal(t, "e103d372ca7f23a8cb40ae2a15dc63b8d3c7d686d55a8e81473a388b75e8884f",
		hex.EncodeToString(digest[:]),
		"TRUEOPEN_ASSIGNMENT_LEGAL_SET_V1 is a frozen consensus preimage; moving this constant is a consensus change and must be re-checked against the §1.4 domain registry")
	operator, err := CanonicalOperatorAddressBytes("operator_address", fact.OperatorAddress)
	require.NoError(t, err)
	factFields := [][]byte{
		shared.Uint32BE(fact.Slot), shared.Uint64BE(fact.SlotVersion), operator,
		shared.Uint32BE(fact.CandidateWeight), shared.Uint64BE(30), shared.Uint64BE(20),
		shared.Uint64BE(4), shared.Uint64BE(10), shared.Uint32BE(1_000_000),
		shared.Uint32BE(1), shared.Uint32BE(500_000), shared.Uint64BE(13), shared.Uint64BE(17),
	}
	prefix := [][]byte{[]byte("trueopen-test"), header.TaskId, header.TaskHash,
		header.CandidatePoolSnapshotId, header.CandidatePoolHash, header.UnionBitmapHash, shared.Uint32BE(1)}
	repeated := shared.CanonicalFrameBytes(shared.Uint32BE(1), shared.CanonicalFrameBytes(factFields...))
	nestedFields := append(append([][]byte(nil), prefix...), repeated)
	expected := shared.CanonicalHashBytes(shared.MustDomain(shared.DomainAssignmentLegalSetV1), nestedFields...)
	require.Equal(t, [32]byte(expected), digest)
	flatFields := append(append([][]byte(nil), prefix...), factFields...)
	flat := shared.CanonicalHashBytes(shared.MustDomain(shared.DomainAssignmentLegalSetV1), flatFields...)
	require.NotEqual(t, [32]byte(flat), digest)
}
