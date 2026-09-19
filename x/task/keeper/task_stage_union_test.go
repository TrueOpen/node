package keeper

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func TestTaskStageUnionHashFillsAndOrdersSegments(t *testing.T) {
	taskID := bytes.Repeat([]byte{1}, 32)
	snapshotID := bytes.Repeat([]byte{2}, 32)
	segments := []taskBitmapSegment{{Index: 1, Bitmap: []byte{1}}, {Index: 0, Bitmap: []byte{3}}}
	first, err := taskStageUnionBitmapHash("trueopen-test", taskID, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK, snapshotID, 16, 1, 3, segments)
	require.NoError(t, err)
	second, err := taskStageUnionBitmapHash("trueopen-test", taskID, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK, snapshotID, 16, 1, 3, []taskBitmapSegment{segments[1], segments[0]})
	require.NoError(t, err)
	require.Equal(t, first, second)
}

func TestTaskStageAddedBitmapHashFramesEachSlot(t *testing.T) {
	taskID := bytes.Repeat([]byte{1}, 32)
	proposal := bytes.Repeat([]byte{2}, 32)
	digest, err := taskStageAddedBitmapHash("trueopen-test", taskID,
		types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK, proposal, []uint32{9, 3})
	require.NoError(t, err)
	// An external anchor: `expected` below restates the preimage, so it agrees with
	// the helper no matter how both are reordered together. The frozen hex is the
	// only value here that a reorder cannot bring along.
	require.Equal(t, "9b80359ae6b71e67ac4d805f714066c135678f14dc98b7b940059d31b9c5c621",
		hex.EncodeToString(digest[:]),
		"TRUEOPEN_TASK_STAGE_ADDED_BITMAP_V1 is a frozen consensus preimage; moving this constant is a consensus change and must be re-checked against the §1.4 domain registry")
	expected := shared.CanonicalHashBytes(
		shared.MustDomain(shared.DomainTaskStageAddedBitmapV1),
		[]byte("trueopen-test"), taskID,
		shared.EnumBE(uint32(types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK)),
		proposal, shared.Uint32BE(2),
		shared.CanonicalFrameBytes(shared.Uint32BE(2), shared.Uint32BE(3), shared.Uint32BE(9)),
	)
	require.Equal(t, [32]byte(expected), digest)
	flat := shared.CanonicalHashBytes(
		shared.MustDomain(shared.DomainTaskStageAddedBitmapV1),
		[]byte("trueopen-test"), taskID,
		shared.EnumBE(uint32(types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK)),
		proposal, shared.Uint32BE(2), shared.Uint32BE(3), shared.Uint32BE(9),
	)
	require.NotEqual(t, [32]byte(flat), digest)
}

func TestTaskStageUnionHashRejectsTrailingBits(t *testing.T) {
	_, err := taskStageUnionBitmapHash("trueopen-test", bytes.Repeat([]byte{1}, 32),
		types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK, bytes.Repeat([]byte{2}, 32),
		9, 1, 1, []taskBitmapSegment{{Index: 0, Bitmap: []byte{0}}, {Index: 1, Bitmap: []byte{2}}})
	require.ErrorContains(t, err, "trailing")
}
