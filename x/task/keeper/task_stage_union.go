package keeper

import (
	"bytes"
	"fmt"
	"sort"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

type taskBitmapSegment struct {
	Index  uint32
	Bitmap []byte
}

func bitmapSegmentCount(slotCapacity, segmentBytes uint32) (uint32, error) {
	if slotCapacity == 0 || segmentBytes == 0 {
		return 0, fmt.Errorf("slot capacity and segment bytes must be positive")
	}
	bits := uint64(segmentBytes) * 8
	return uint32((uint64(slotCapacity) + bits - 1) / bits), nil
}

func bitmapLocation(slot, slotCapacity, segmentBytes uint32) (uint32, uint32, byte, error) {
	if slot >= slotCapacity {
		return 0, 0, 0, fmt.Errorf("slot %d exceeds capacity %d", slot, slotCapacity)
	}
	if segmentBytes == 0 {
		return 0, 0, 0, fmt.Errorf("segment bytes must be positive")
	}
	bitsPerSegment := uint64(segmentBytes) * 8
	segment := uint32(uint64(slot) / bitsPerSegment)
	within := uint32(uint64(slot) % bitsPerSegment)
	return segment, within / 8, byte(1 << (within % 8)), nil
}

func validateBitmapTrailingBits(slotCapacity, segmentBytes uint32, segments []taskBitmapSegment) error {
	segmentCount, err := bitmapSegmentCount(slotCapacity, segmentBytes)
	if err != nil {
		return err
	}
	if uint32(len(segments)) != segmentCount {
		return fmt.Errorf("segment count %d does not match layout %d", len(segments), segmentCount)
	}
	for index, segment := range segments {
		if segment.Index != uint32(index) || len(segment.Bitmap) != int(segmentBytes) {
			return fmt.Errorf("segment %d has non-canonical index or length", index)
		}
	}
	usedBits := uint32(uint64(slotCapacity) % (uint64(segmentBytes) * 8))
	if usedBits == 0 {
		return nil
	}
	last := segments[len(segments)-1].Bitmap
	fullBytes := usedBits / 8
	remainingBits := usedBits % 8
	start := fullBytes
	if remainingBits != 0 {
		if last[fullBytes]&^byte((1<<remainingBits)-1) != 0 {
			return fmt.Errorf("non-zero trailing bits in final segment")
		}
		start++
	}
	for _, value := range last[start:] {
		if value != 0 {
			return fmt.Errorf("non-zero trailing bytes in final segment")
		}
	}
	return nil
}

func taskStageUnionBitmapHash(
	chainID string,
	taskID []byte,
	stage types.TaskCandidateStage,
	snapshotID []byte,
	slotCapacity, segmentBytes, unionCount uint32,
	segments []taskBitmapSegment,
) ([32]byte, error) {
	var zero [32]byte
	if chainID == "" || len(taskID) != types.Hash32Len || len(snapshotID) != types.Hash32Len {
		return zero, fmt.Errorf("union bitmap scope is incomplete")
	}
	if stage != types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK && stage != types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY {
		return zero, fmt.Errorf("candidate stage is invalid")
	}
	ordered := append([]taskBitmapSegment(nil), segments...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Index < ordered[j].Index })
	if err := validateBitmapTrailingBits(slotCapacity, segmentBytes, ordered); err != nil {
		return zero, err
	}
	var actualCount uint32
	elements := make([]shared.CanonicalFrameV1, 0, len(ordered))
	builder := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainTaskStageUnionBitmapV1)).Raw(
		[]byte(chainID), taskID, shared.EnumBE(uint32(stage)), snapshotID,
		shared.Uint32BE(slotCapacity), shared.Uint32BE(uint32(len(ordered))), shared.Uint32BE(unionCount),
	)
	for _, segment := range ordered {
		for _, value := range segment.Bitmap {
			actualCount += uint32(bitsSet8(value))
		}
		elements = append(elements, shared.FlatCanonicalFrameV1(shared.Uint32BE(segment.Index), segment.Bitmap))
	}
	if actualCount != unionCount {
		return zero, fmt.Errorf("union_count %d does not match bitmap population %d", unionCount, actualCount)
	}
	digest, err := builder.Nested(shared.CanonicalRepeatedFramesV1(elements)).Sum()
	if err != nil {
		return zero, err
	}
	return [32]byte(digest), nil
}

func taskStageAddedBitmapHash(
	chainID string, taskID []byte, stage types.TaskCandidateStage, proposalDigest []byte, slots []uint32,
) ([32]byte, error) {
	var zero [32]byte
	if chainID == "" || len(taskID) != types.Hash32Len || len(proposalDigest) != types.Hash32Len {
		return zero, fmt.Errorf("added bitmap scope is incomplete")
	}
	ordered := append([]uint32(nil), slots...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	elements := make([]shared.CanonicalFieldV1, 0, len(ordered))
	builder := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainTaskStageAddedBitmapV1)).Raw(
		[]byte(chainID), taskID, shared.EnumBE(uint32(stage)), proposalDigest, shared.Uint32BE(uint32(len(ordered))),
	)
	for index, slot := range ordered {
		if index != 0 && slot == ordered[index-1] {
			return zero, fmt.Errorf("added slots must be unique")
		}
		elements = append(elements, shared.RawCanonicalFieldV1(shared.Uint32BE(slot)))
	}
	digest, err := builder.Nested(shared.CanonicalRepeatedFieldsV1(elements)).Sum()
	if err != nil {
		return zero, err
	}
	return [32]byte(digest), nil
}

func bitsSet8(value byte) int {
	value = value - ((value >> 1) & 0x55)
	value = (value & 0x33) + ((value >> 2) & 0x33)
	return int((value + (value >> 4)) & 0x0f)
}

func equalUnionHeaderScope(header types.TaskStageHandraiseUnionState, taskID, snapshotID, poolHash []byte) bool {
	return bytes.Equal(header.TaskId, taskID) && bytes.Equal(header.CandidatePoolSnapshotId, snapshotID) && bytes.Equal(header.CandidatePoolHash, poolHash)
}
