package keeper

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"reflect"

	"cosmossdk.io/collections"

	"github.com/TrueOpen/node/x/hub/types"
)

func (k Keeper) processCandidatePoolBuild(ctx context.Context, height, limit uint64) (uint64, error) {
	if limit == 0 {
		return 0, nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, err
	}
	status, err := k.CandidatePoolBuildStatus.Get(ctx)
	if errors.Is(err, collections.ErrNotFound) {
		status = types.CandidatePoolBuildStatusState{Status: types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_IDLE}
		if err := k.CandidatePoolBuildStatus.Set(ctx, status); err != nil {
			return 0, err
		}
	} else if err != nil {
		return 0, err
	}

	if status.Status == types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_BUILDING {
		cursor, err := k.CandidatePoolBuildCursor.Get(ctx, status.TargetEpoch)
		if err != nil {
			return 0, err
		}
		if cursor.Status == types.CandidatePoolBuildCursorStatus_CANDIDATE_POOL_BUILD_CURSOR_STATUS_CLEANING_FAILED_DRAFT {
			return k.cleanFailedCandidateDraft(ctx, cursor, status, height, limit)
		}
		return k.advanceCandidateBuild(ctx, cursor, status, params, height, limit)
	}

	targetEpoch, start, err := k.nextCandidateBuildTarget(ctx, params, height)
	if err != nil || !start {
		return 0, err
	}
	if status.TargetEpoch == targetEpoch && (status.Status == types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_PUBLISHED || status.Status == types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_FAILED_CAPACITY) {
		return 0, nil
	}
	cursor, status, err := k.startCandidateBuild(ctx, status, params, targetEpoch, height)
	if err != nil {
		return 0, err
	}
	return k.advanceCandidateBuild(ctx, cursor, status, params, height, limit)
}

func (k Keeper) nextCandidateBuildTarget(ctx context.Context, params types.HubParamsV2, height uint64) (uint64, bool, error) {
	epochLength := normalizedEpochLengthBlocks(params)
	currentEpoch := epochForHeight(height, epochLength)
	target := currentEpoch
	if current, err := k.CurrentCandidatePool.Get(ctx); err == nil {
		if current.Epoch == currentEpoch {
			if currentEpoch == ^uint64(0) {
				return 0, false, fmt.Errorf("candidate epoch overflow")
			}
			target = currentEpoch + 1
		}
	} else if !errors.Is(err, collections.ErrNotFound) {
		return 0, false, err
	}
	startHeight, err := checkedMulForCandidatePool(target, epochLength)
	if err != nil {
		return 0, false, err
	}
	if target == currentEpoch {
		return target, true, nil
	}
	leadEnd := height + params.CandidatePool.CandidatePoolBuildLeadBlocks
	if leadEnd < height {
		leadEnd = ^uint64(0)
	}
	return target, leadEnd >= startHeight, nil
}

func (k Keeper) startCandidateBuild(ctx context.Context, status types.CandidatePoolBuildStatusState, params types.HubParamsV2, targetEpoch, height uint64) (types.CandidatePoolBuildCursorState, types.CandidatePoolBuildStatusState, error) {
	segmentCount, err := candidateSegmentCount(params.CandidatePool.CandidateSlotHardCapacity, params.CandidatePool.CandidateBitmapSegmentBytes)
	if err != nil {
		return types.CandidatePoolBuildCursorState{}, status, err
	}
	dirty := make([]byte, (segmentCount+7)/8)
	for segment := uint32(0); segment < segmentCount; segment++ {
		setCandidateDirtySegment(dirty, segment)
	}
	cursor := types.CandidatePoolBuildCursorState{
		TargetEpoch: targetEpoch, SourceRevision: status.SourceRevision, DirtySegments: dirty,
		Status: types.CandidatePoolBuildCursorStatus_CANDIDATE_POOL_BUILD_CURSOR_STATUS_BUILDING,
	}
	status.TargetEpoch = targetEpoch
	status.Status = types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_BUILDING
	status.ObservedMembers = 0
	status.UpdatedHeight = height
	status.FailureReason = types.CandidatePoolBuildFailureReason_CANDIDATE_POOL_BUILD_FAILURE_REASON_UNSPECIFIED
	if err := k.CandidatePoolBuildCursor.Set(ctx, targetEpoch, cursor); err != nil {
		return types.CandidatePoolBuildCursorState{}, status, err
	}
	if err := k.CandidatePoolBuildStatus.Set(ctx, status); err != nil {
		return types.CandidatePoolBuildCursorState{}, status, err
	}
	return cursor, status, nil
}

func (k Keeper) advanceCandidateBuild(ctx context.Context, cursor types.CandidatePoolBuildCursorState, status types.CandidatePoolBuildStatusState, params types.HubParamsV2, height, limit uint64) (uint64, error) {
	segmentCount, err := candidateSegmentCount(params.CandidatePool.CandidateSlotHardCapacity, params.CandidatePool.CandidateBitmapSegmentBytes)
	if err != nil {
		return 0, err
	}
	segment, found := nextCandidateDirtySegment(cursor.DirtySegments, cursor.NextSegment, segmentCount)
	if !found {
		return 0, k.finalizeCandidateBuild(ctx, cursor, status, params, height)
	}
	segmentBits := uint64(params.CandidatePool.CandidateBitmapSegmentBytes) * 8
	segmentStart := uint64(segment) * segmentBits
	segmentEnd := segmentStart + segmentBits
	if segmentEnd > uint64(params.CandidatePool.CandidateSlotHardCapacity) {
		segmentEnd = uint64(params.CandidatePool.CandidateSlotHardCapacity)
	}
	// CONTRACT-GAP: the frozen build cursor has no within-segment build
	// position. NextCleanupKey is reused here as the only durable position;
	// adding a dedicated field requires a schema revision.
	nextSlot, err := candidateBuildNextSlot(cursor.NextCleanupKey, segmentStart, segmentEnd)
	if err != nil {
		return 0, err
	}
	segmentState, err := k.CandidatePoolActiveSegment.Get(ctx, types.NewCandidatePoolSegmentKey(cursor.TargetEpoch, segment))
	if errors.Is(err, collections.ErrNotFound) || nextSlot == segmentStart {
		segmentState = types.CandidatePoolActiveSegmentState{
			Epoch: cursor.TargetEpoch, SegmentIndex: segment,
			Bitmap: make([]byte, params.CandidatePool.CandidateBitmapSegmentBytes),
		}
	} else if err != nil {
		return 0, err
	}
	if uint32(len(segmentState.Bitmap)) != params.CandidatePool.CandidateBitmapSegmentBytes {
		return 0, fmt.Errorf("candidate draft segment has invalid bitmap length")
	}

	visited := uint64(0)
	for nextSlot < segmentEnd && visited < limit {
		slot := uint32(nextSlot)
		memberKey := types.NewCandidatePoolMemberKey(cursor.TargetEpoch, slot)
		current, getErr := k.CandidateSlotCurrent.Get(ctx, slot)
		active := getErr == nil && current.Status == candidateSlotAllocated
		if getErr != nil && !errors.Is(getErr, collections.ErrNotFound) {
			return visited, getErr
		}
		if active {
			eligible, err := k.isGlobalCandidateMember(ctx, current.OperatorAddress, cursor.TargetEpoch)
			if err != nil {
				return visited, err
			}
			active = eligible
		}
		bit := nextSlot - segmentStart
		if active {
			binding, err := k.CandidateSlotBinding.Get(ctx, types.NewCandidateSlotBindingKey(slot, current.SlotVersion))
			if err != nil {
				return visited, err
			}
			if binding.OperatorAddress != current.OperatorAddress || binding.ReleasedHeight != 0 {
				return visited, fmt.Errorf("allocated slot has invalid immutable binding")
			}
			segmentState.Bitmap[bit/8] |= byte(1) << uint(bit%8)
			if err := k.CandidatePoolMember.Set(ctx, memberKey, types.CandidatePoolMemberState{
				Epoch: cursor.TargetEpoch, Slot: slot, SlotVersion: current.SlotVersion,
				OperatorAddress: current.OperatorAddress, BindingHash: append([]byte(nil), binding.BindingHash...),
			}); err != nil {
				return visited, err
			}
		} else {
			segmentState.Bitmap[bit/8] &^= byte(1) << uint(bit%8)
			if err := k.CandidatePoolMember.Remove(ctx, memberKey); err != nil && !errors.Is(err, collections.ErrNotFound) {
				return visited, err
			}
		}
		nextSlot++
		visited++
		cursor.VisitedCount++
	}
	if nextSlot < segmentEnd {
		cursor.NextSegment = segment
		cursor.NextCleanupKey = uint32CursorKey(uint32(nextSlot))
		if err := k.CandidatePoolActiveSegment.Set(ctx, types.NewCandidatePoolSegmentKey(cursor.TargetEpoch, segment), segmentState); err != nil {
			return visited, err
		}
		return visited, k.CandidatePoolBuildCursor.Set(ctx, cursor.TargetEpoch, cursor)
	}

	// CONTRACT-GAP: the canonical segment hash frames every member in the
	// segment, but the frozen cursor cannot persist a SHA-256 continuation or
	// a member-scan position. This read therefore cannot be charged to and
	// resumed under the per-block visited limit.
	refs, err := k.candidateMembersForSegment(ctx, cursor.TargetEpoch, segment, params.CandidatePool.CandidateBitmapSegmentBytes)
	if err != nil {
		return visited, err
	}
	segmentHash, err := types.CandidatePoolSegmentHash(cursor.TargetEpoch, segment, segmentState.Bitmap, uint32(len(refs)), refs)
	if err != nil {
		return visited, err
	}
	if candidateBitmapEmpty(segmentState.Bitmap) {
		if err := k.CandidatePoolActiveSegment.Remove(ctx, types.NewCandidatePoolSegmentKey(cursor.TargetEpoch, segment)); err != nil && !errors.Is(err, collections.ErrNotFound) {
			return visited, err
		}
	} else {
		segmentState.SegmentHash = segmentHash
		if err := k.CandidatePoolActiveSegment.Set(ctx, types.NewCandidatePoolSegmentKey(cursor.TargetEpoch, segment), segmentState); err != nil {
			return visited, err
		}
	}
	clearCandidateDirtySegment(cursor.DirtySegments, segment)
	cursor.NextSegment = segment + 1
	cursor.NextCleanupKey = nil
	cursor.SourceRevision = status.SourceRevision
	if err := k.CandidatePoolBuildCursor.Set(ctx, cursor.TargetEpoch, cursor); err != nil {
		return visited, err
	}
	if _, found := nextCandidateDirtySegment(cursor.DirtySegments, cursor.NextSegment, segmentCount); !found {
		return visited, k.finalizeCandidateBuild(ctx, cursor, status, params, height)
	}
	return visited, nil
}

func (k Keeper) finalizeCandidateBuild(ctx context.Context, cursor types.CandidatePoolBuildCursorState, status types.CandidatePoolBuildStatusState, params types.HubParamsV2, height uint64) error {
	if cursor.SourceRevision != status.SourceRevision {
		return fmt.Errorf("candidate build source revision changed without a dirty segment")
	}
	epochLength := normalizedEpochLengthBlocks(params)
	targetStart, err := checkedMulForCandidatePool(cursor.TargetEpoch, epochLength)
	if err != nil {
		return err
	}
	effectiveHeight := height + 1
	if effectiveHeight == 0 {
		return fmt.Errorf("candidate effective height overflow")
	}
	if targetStart > effectiveHeight {
		effectiveHeight = targetStart
	}
	if cursor.TargetEpoch == ^uint64(0) {
		return fmt.Errorf("candidate target epoch overflow")
	}
	expiresHeight, err := checkedMulForCandidatePool(cursor.TargetEpoch+1, epochLength)
	if err != nil {
		return err
	}
	if effectiveHeight >= expiresHeight {
		cursor.Status = types.CandidatePoolBuildCursorStatus_CANDIDATE_POOL_BUILD_CURSOR_STATUS_CLEANING_FAILED_DRAFT
		cursor.FailureReason = types.CandidatePoolBuildFailureReason_CANDIDATE_POOL_BUILD_FAILURE_REASON_NO_EFFECTIVE_WINDOW
		return k.CandidatePoolBuildCursor.Set(ctx, cursor.TargetEpoch, cursor)
	}

	// CONTRACT-GAP: final canonical hashes require the full ordered member and
	// segment streams. The frozen cursor stores neither hash continuations nor
	// finalization scan positions, so this phase cannot be made incrementally
	// bounded without a schema change.
	members, err := k.candidateMembersForEpoch(ctx, cursor.TargetEpoch)
	if err != nil {
		return err
	}
	segments, err := k.candidateSegmentsForHash(ctx, cursor.TargetEpoch, params.CandidatePool.CandidateSlotHardCapacity, params.CandidatePool.CandidateBitmapSegmentBytes)
	if err != nil {
		return err
	}
	bitmapHash, err := types.CandidateActiveBitmapHash(params.CandidatePool.CandidateSlotHardCapacity, uint32(len(segments)), segments)
	if err != nil {
		return err
	}
	memberHash, err := types.CandidateMemberSetHash(uint32(len(members)), members)
	if err != nil {
		return err
	}
	poolHash, err := types.CandidatePoolHash(candidatePoolChainID(ctx), cursor.TargetEpoch, params.CandidatePool.CandidateSlotHardCapacity, effectiveHeight, expiresHeight, uint32(len(members)), bitmapHash, memberHash)
	if err != nil {
		return err
	}
	snapshotID, err := types.CandidatePoolSnapshotID(candidatePoolChainID(ctx), cursor.TargetEpoch, poolHash)
	if err != nil {
		return err
	}
	snapshotKey, _ := candidateHashKey(snapshotID)
	snapshot := types.CandidatePoolSnapshotState{
		SchemaVersion: 1, Epoch: cursor.TargetEpoch, SnapshotId: snapshotID, PoolHash: poolHash,
		Status: candidateSnapshotReady, SlotCapacity: params.CandidatePool.CandidateSlotHardCapacity,
		ActiveCount: uint32(len(members)), ActiveBitmapHash: bitmapHash, MemberSetHash: memberHash,
		SourceRevision: cursor.SourceRevision, BuildStartedHeight: status.UpdatedHeight,
		EffectiveHeight: effectiveHeight, ExpiresHeight: expiresHeight,
	}
	if existing, getErr := k.CandidatePoolSnapshot.Get(ctx, snapshotKey); getErr == nil {
		if !reflect.DeepEqual(existing, snapshot) {
			return fmt.Errorf("candidate snapshot id collision")
		}
	} else if !errors.Is(getErr, collections.ErrNotFound) {
		return getErr
	} else if err := k.CandidatePoolSnapshot.Set(ctx, snapshotKey, snapshot); err != nil {
		return err
	}
	for _, member := range members {
		bindingKey := types.NewCandidateSlotBindingKey(member.Slot, member.SlotVersion)
		binding, err := k.CandidateSlotBinding.Get(ctx, bindingKey)
		if err != nil || binding.SnapshotRefCount == ^uint32(0) {
			return fmt.Errorf("candidate binding refcount unavailable or overflow")
		}
		binding.SnapshotRefCount++
		if err := k.CandidateSlotBinding.Set(ctx, bindingKey, binding); err != nil {
			return err
		}
	}
	if err := k.CandidatePoolExpiryIndex.Set(ctx, types.NewCandidatePoolExpiryIndexKey(expiresHeight, snapshotKey)); err != nil {
		return err
	}
	status.Status = types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_PUBLISHED
	status.ObservedMembers = uint32(len(members))
	status.UpdatedHeight = height
	status.FailureReason = types.CandidatePoolBuildFailureReason_CANDIDATE_POOL_BUILD_FAILURE_REASON_UNSPECIFIED
	if err := k.CandidatePoolBuildStatus.Set(ctx, status); err != nil {
		return err
	}
	return k.CandidatePoolBuildCursor.Remove(ctx, cursor.TargetEpoch)
}

func (k Keeper) candidateMembersForSegment(ctx context.Context, epoch uint64, segment, segmentBytes uint32) ([]types.CandidatePoolMemberRef, error) {
	bitsPerSegment := uint64(segmentBytes) * 8
	start, end := uint64(segment)*bitsPerSegment, uint64(segment+1)*bitsPerSegment
	if start > uint64(^uint32(0)) || end > uint64(^uint32(0))+1 {
		return nil, fmt.Errorf("candidate segment slot range overflows uint32")
	}
	rangeBySlot := collections.NewPrefixedPairRange[uint64, uint32](epoch).StartInclusive(uint32(start))
	if end <= uint64(^uint32(0)) {
		rangeBySlot = rangeBySlot.EndExclusive(uint32(end))
	}
	iter, err := k.CandidatePoolMember.Iterate(ctx, rangeBySlot)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	refs := make([]types.CandidatePoolMemberRef, 0)
	for ; iter.Valid(); iter.Next() {
		state, err := iter.Value()
		if err != nil {
			return nil, err
		}
		operatorBytes, canonical, err := k.requireCanonicalAddress("operator_address", state.OperatorAddress)
		if err != nil || canonical != state.OperatorAddress {
			return nil, fmt.Errorf("candidate member has invalid operator address")
		}
		refs = append(refs, types.CandidatePoolMemberRef{
			Slot: state.Slot, SlotVersion: state.SlotVersion,
			OperatorBytes: operatorBytes, BindingHash: append([]byte(nil), state.BindingHash...),
		})
	}
	return refs, nil
}

func (k Keeper) candidateMembersForEpoch(ctx context.Context, epoch uint64) ([]types.CandidatePoolMemberRef, error) {
	iter, err := k.CandidatePoolMember.Iterate(ctx, collections.NewPrefixedPairRange[uint64, uint32](epoch))
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	refs := make([]types.CandidatePoolMemberRef, 0)
	for ; iter.Valid(); iter.Next() {
		state, err := iter.Value()
		if err != nil {
			return nil, err
		}
		operatorBytes, canonical, err := k.requireCanonicalAddress("operator_address", state.OperatorAddress)
		if err != nil || canonical != state.OperatorAddress {
			return nil, fmt.Errorf("candidate member has invalid operator address")
		}
		binding, err := k.CandidateSlotBinding.Get(ctx, types.NewCandidateSlotBindingKey(state.Slot, state.SlotVersion))
		if err != nil || binding.OperatorAddress != state.OperatorAddress || !equalCandidateBytes(binding.BindingHash, state.BindingHash) {
			return nil, fmt.Errorf("candidate member immutable binding is invalid")
		}
		wantBindingHash, err := types.CandidateSlotBindingHash(state.Slot, state.SlotVersion, operatorBytes, binding.AllocatedEpoch)
		if err != nil || !equalCandidateBytes(wantBindingHash, state.BindingHash) {
			return nil, fmt.Errorf("candidate member binding hash is invalid")
		}
		refs = append(refs, types.CandidatePoolMemberRef{
			Slot: state.Slot, SlotVersion: state.SlotVersion, OperatorBytes: operatorBytes,
			BindingHash: append([]byte(nil), state.BindingHash...),
		})
	}
	return refs, nil
}

func (k Keeper) candidateSegmentsForHash(ctx context.Context, epoch uint64, capacity, segmentBytes uint32) ([]types.CandidatePoolSegmentRef, error) {
	count, err := candidateSegmentCount(capacity, segmentBytes)
	if err != nil {
		return nil, err
	}
	segments := make([]types.CandidatePoolSegmentRef, count)
	for i := uint32(0); i < count; i++ {
		segments[i] = types.CandidatePoolSegmentRef{SegmentIndex: i, Bitmap: make([]byte, segmentBytes)}
		state, err := k.CandidatePoolActiveSegment.Get(ctx, types.NewCandidatePoolSegmentKey(epoch, i))
		if errors.Is(err, collections.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if state.Epoch != epoch || state.SegmentIndex != i || uint32(len(state.Bitmap)) != segmentBytes {
			return nil, fmt.Errorf("candidate segment state is invalid")
		}
		segments[i].Bitmap = append([]byte(nil), state.Bitmap...)
	}
	return segments, nil
}

func candidateBuildNextSlot(encoded []byte, segmentStart, segmentEnd uint64) (uint64, error) {
	if len(encoded) == 0 {
		return segmentStart, nil
	}
	if len(encoded) != 4 {
		return 0, fmt.Errorf("candidate build slot cursor must be four bytes")
	}
	next := uint64(binary.BigEndian.Uint32(encoded))
	if next < segmentStart || next > segmentEnd {
		return 0, fmt.Errorf("candidate build slot cursor is outside current segment")
	}
	return next, nil
}

func uint32CursorKey(value uint32) []byte {
	key := make([]byte, 4)
	binary.BigEndian.PutUint32(key, value)
	return key
}

func setCandidateDirtySegment(bitmap []byte, segment uint32) {
	if int(segment/8) < len(bitmap) {
		bitmap[segment/8] |= byte(1) << uint(segment%8)
	}
}

func clearCandidateDirtySegment(bitmap []byte, segment uint32) {
	if int(segment/8) < len(bitmap) {
		bitmap[segment/8] &^= byte(1) << uint(segment%8)
	}
}

func nextCandidateDirtySegment(bitmap []byte, start, count uint32) (uint32, bool) {
	for pass := 0; pass < 2; pass++ {
		from, to := start, count
		if pass == 1 {
			from, to = 0, start
		}
		for segment := from; segment < to; segment++ {
			if bitmap[segment/8]&(byte(1)<<uint(segment%8)) != 0 {
				return segment, true
			}
		}
	}
	return 0, false
}

func candidateBitmapEmpty(bitmap []byte) bool {
	for _, value := range bitmap {
		if bits.OnesCount8(value) != 0 {
			return false
		}
	}
	return true
}

func candidateMemberRefsWithinSegment(all []types.CandidatePoolMemberRef, segment, segmentBytes uint32) []types.CandidatePoolMemberRef {
	segmentBits := uint64(segmentBytes) * 8
	start, end := uint64(segment)*segmentBits, uint64(segment+1)*segmentBits
	refs := make([]types.CandidatePoolMemberRef, 0)
	for _, member := range all {
		if uint64(member.Slot) >= start && uint64(member.Slot) < end {
			refs = append(refs, member)
		}
	}
	return refs
}

func validateCandidateSegmentTrailingBits(segment uint32, bitmap []byte, capacity uint32) error {
	segmentBits := uint64(len(bitmap)) * 8
	if segmentBits == 0 {
		return fmt.Errorf("candidate segment bitmap must not be empty")
	}
	start := uint64(segment) * segmentBits
	if start >= uint64(capacity) {
		return fmt.Errorf("candidate segment %d starts beyond slot capacity", segment)
	}
	used := uint64(capacity) - start
	if used >= segmentBits {
		return nil
	}
	for bit := used; bit < segmentBits; bit++ {
		if bitmap[bit/8]&(byte(1)<<uint(bit%8)) != 0 {
			return fmt.Errorf("candidate segment %d has non-zero trailing bit %d", segment, bit)
		}
	}
	return nil
}
