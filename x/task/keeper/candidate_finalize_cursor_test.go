package keeper

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func TestWorkerProposalDigestUsesSlotOrder(t *testing.T) {
	taskID := bytes.Repeat([]byte{1}, types.Hash32Len)
	taskHash := bytes.Repeat([]byte{2}, types.Hash32Len)
	snapshotID := bytes.Repeat([]byte{3}, types.Hash32Len)
	poolHash := bytes.Repeat([]byte{4}, types.Hash32Len)
	first := candidateSigningDigest{Slot: 3, Digest: bytes.Repeat([]byte{5}, types.Hash32Len)}
	second := candidateSigningDigest{Slot: 9, Digest: bytes.Repeat([]byte{6}, types.Hash32Len)}

	forward, err := workerProposalDigest("trueopen-test", taskID, taskHash, snapshotID, poolHash, "", []candidateSigningDigest{first, second})
	require.NoError(t, err)
	reverse, err := workerProposalDigest("trueopen-test", taskID, taskHash, snapshotID, poolHash, "", []candidateSigningDigest{second, first})
	require.NoError(t, err)
	require.Equal(t, forward, reverse)
	// An external anchor. The `expected` value below is this test spelling the
	// preimage out a second time, so it moves with any reordering that is applied
	// to both sides; the frozen hex does not move, which is what makes a field
	// reorder visible in the diff instead of self-consistent.
	require.Equal(t, "f022250f77b82c08889c9df0c472a96b2909fe85f2b12e8060e61b592ae20438",
		hex.EncodeToString(forward[:]),
		"TRUEOPEN_WORKER_PROPOSAL_V1 is a frozen consensus preimage; moving this constant is a consensus change and must be re-checked against the §1.4 domain registry")

	expected := shared.CanonicalHashBytes(
		shared.MustDomain(shared.DomainWorkerProposalV1),
		[]byte("trueopen-test"), taskID, taskHash,
		shared.EnumBE(uint32(types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK)),
		snapshotID, poolHash, nil, nil, shared.Uint32BE(2),
		shared.CanonicalFrameBytes(shared.Uint32BE(2), first.Digest, second.Digest),
	)
	require.Equal(t, [32]byte(expected), forward)
	flat := shared.CanonicalHashBytes(
		shared.MustDomain(shared.DomainWorkerProposalV1),
		[]byte("trueopen-test"), taskID, taskHash,
		shared.EnumBE(uint32(types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK)),
		snapshotID, poolHash, nil, nil, shared.Uint32BE(2), first.Digest, second.Digest,
	)
	require.NotEqual(t, [32]byte(flat), forward)
}

func TestWorkerProposalDigestRejectsDuplicateSlot(t *testing.T) {
	hash := bytes.Repeat([]byte{1}, types.Hash32Len)
	_, err := workerProposalDigest("trueopen-test", hash, hash, hash, hash, "", []candidateSigningDigest{
		{Slot: 1, Digest: hash}, {Slot: 1, Digest: hash},
	})
	require.ErrorContains(t, err, "duplicate")
}
