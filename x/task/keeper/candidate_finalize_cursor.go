package keeper

import (
	"bytes"
	"fmt"
	"sort"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

type candidateSigningDigest struct {
	Slot   uint32
	Digest []byte
}

// workerProposalDigest is the canonical digest of one OPEN_TASK proposal. An
// empty proposer denotes the permissionless fallback path. OPEN_TASK has no
// data-ready attestation, so that field is encoded as an empty byte string.
func workerProposalDigest(
	chainID string,
	taskID, taskHash, snapshotID, poolHash []byte,
	proposer string,
	handraises []candidateSigningDigest,
) ([32]byte, error) {
	var zero [32]byte
	if chainID == "" || len(taskID) != types.Hash32Len || len(taskHash) != types.Hash32Len ||
		len(snapshotID) != types.Hash32Len || len(poolHash) != types.Hash32Len || len(handraises) == 0 {
		return zero, fmt.Errorf("worker proposal scope is incomplete")
	}
	ordered := append([]candidateSigningDigest(nil), handraises...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Slot < ordered[j].Slot })
	var proposerBytes []byte
	if proposer != "" {
		var err error
		proposerBytes, err = types.CanonicalOperatorAddressBytes("proposer_operator", proposer)
		if err != nil {
			return zero, err
		}
	}
	elements := make([]shared.CanonicalFieldV1, 0, len(ordered))
	builder := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainWorkerProposalV1)).Raw(
		[]byte(chainID), taskID, taskHash,
		shared.EnumBE(uint32(types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK)),
		snapshotID, poolHash, proposerBytes, nil,
		shared.Uint32BE(uint32(len(ordered))),
	)
	for index, handraise := range ordered {
		if len(handraise.Digest) != types.Hash32Len {
			return zero, fmt.Errorf("handraise digest at slot %d is not Hash32", handraise.Slot)
		}
		if index != 0 && handraise.Slot == ordered[index-1].Slot {
			return zero, fmt.Errorf("duplicate handraise slot %d", handraise.Slot)
		}
		elements = append(elements, shared.RawCanonicalFieldV1(handraise.Digest))
	}
	digest, err := builder.Nested(shared.CanonicalRepeatedFieldsV1(elements)).Sum()
	if err != nil {
		return zero, err
	}
	return [32]byte(digest), nil
}

// finalizeWorkerLegalSet validates the immutable materialized rows and derives
// the final flat legal-set commitment. It intentionally does not interpret
// TaskCandidateFinalizeCursorState.running_commitment: the contract does not
// define a resumable hash state or fold algorithm for that field.
func finalizeWorkerLegalSet(
	chainID string,
	header types.AssignmentCandidateSetState,
	union types.TaskStageHandraiseUnionState,
	facts []types.TaskCandidateFactState,
) ([32]byte, error) {
	var zero [32]byte
	if union.Stage != types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK ||
		union.Status != types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_FINALIZING {
		return zero, fmt.Errorf("worker candidate union is not finalizing")
	}
	if !bytes.Equal(header.TaskId, union.TaskId) ||
		!bytes.Equal(header.CandidatePoolSnapshotId, union.CandidatePoolSnapshotId) ||
		!bytes.Equal(header.CandidatePoolHash, union.CandidatePoolHash) ||
		!bytes.Equal(header.UnionBitmapHash, union.UnionBitmapHash) {
		return zero, fmt.Errorf("worker candidate header and union scope mismatch")
	}
	if uint32(len(facts)) != union.UnionCount || uint32(len(facts)) != header.CandidateCount {
		return zero, fmt.Errorf("materialized candidate count does not match union")
	}
	return types.AssignmentCandidateSetHash(chainID, header, facts)
}
