package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (k Keeper) invalidateCandidatePoolsForOperator(ctx context.Context, operatorAddress string, height uint64) error {
	if height == 0 {
		height = candidatePoolContextHeight(ctx)
	}
	return k.syncCandidateSlotMembership(ctx, operatorAddress, height)
}

func (k Keeper) ProcessDirtyCandidatePools(ctx context.Context, currentHeight, limit uint64) (uint64, error) {
	return k.processCandidatePoolBuild(ctx, currentHeight, limit)
}

func (k Keeper) GetCandidatePoolSnapshot(ctx sdk.Context, snapshotID []byte) (types.CandidatePoolSnapshotState, bool) {
	snapshot, err := k.loadCandidatePoolSnapshot(sdk.WrapSDKContext(ctx), snapshotID)
	if err != nil || (snapshot.Status != candidateSnapshotActive &&
		snapshot.Status != types.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_EXPIRED) {
		return types.CandidatePoolSnapshotState{}, false
	}
	return snapshot, true
}

// HasCandidatePoolTaskRef verifies that one Task already owns the retention
// reference that keeps a locked snapshot body available across epoch expiry.
func (k Keeper) HasCandidatePoolTaskRef(ctx context.Context, taskID, snapshotID []byte) (bool, error) {
	taskKey, err := candidateHashKey(taskID)
	if err != nil {
		return false, err
	}
	snapshotKey, err := candidateHashKey(snapshotID)
	if err != nil {
		return false, err
	}
	state, err := k.CandidatePoolTaskRef.Get(ctx, types.NewCandidatePoolTaskRefKey(taskKey, snapshotKey))
	if errors.Is(err, collections.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !equalCandidateBytes(state.TaskId, taskID) || !equalCandidateBytes(state.SnapshotId, snapshotID) ||
		state.Status != types.CandidatePoolTaskRefStatus_CANDIDATE_POOL_TASK_REF_STATUS_ACQUIRED {
		return false, fmt.Errorf("candidate pool task reference is non-canonical")
	}
	return true, nil
}

func (k Keeper) GetCurrentCandidatePool(ctx sdk.Context) (types.CandidatePoolSnapshotState, bool) {
	wrapped := sdk.WrapSDKContext(ctx)
	current, err := k.CurrentCandidatePool.Get(wrapped)
	if err != nil {
		return types.CandidatePoolSnapshotState{}, false
	}
	snapshot, err := k.loadCandidatePoolSnapshot(wrapped, current.SnapshotId)
	if err != nil || snapshot.Status != candidateSnapshotActive || snapshot.Epoch != current.Epoch || !equalCandidateBytes(snapshot.PoolHash, current.PoolHash) {
		return types.CandidatePoolSnapshotState{}, false
	}
	height := uint64(ctx.BlockHeight())
	return snapshot, height >= snapshot.EffectiveHeight && height < snapshot.ExpiresHeight
}

func (k Keeper) CurrentActiveCandidatePool(ctx context.Context) (types.CandidatePoolSnapshotState, error) {
	current, err := k.CurrentCandidatePool.Get(ctx)
	if err != nil {
		return types.CandidatePoolSnapshotState{}, err
	}
	snapshot, err := k.loadCandidatePoolSnapshot(ctx, current.SnapshotId)
	if err != nil {
		return types.CandidatePoolSnapshotState{}, err
	}
	height := candidatePoolContextHeight(ctx)
	if snapshot.Status != candidateSnapshotActive || height < snapshot.EffectiveHeight || height >= snapshot.ExpiresHeight || snapshot.Epoch != current.Epoch || !equalCandidateBytes(snapshot.PoolHash, current.PoolHash) {
		return types.CandidatePoolSnapshotState{}, fmt.Errorf("current candidate pool is not ACTIVE")
	}
	return snapshot, nil
}

func (k Keeper) ResolveCandidatePoolMember(ctx context.Context, snapshotID []byte, slot uint32) (types.CandidatePoolMemberState, types.CandidateSlotBindingState, error) {
	key, err := candidateHashKey(snapshotID)
	if err != nil {
		return types.CandidatePoolMemberState{}, types.CandidateSlotBindingState{}, err
	}
	snapshot, err := k.loadCandidatePoolSnapshot(ctx, key)
	if err != nil {
		return types.CandidatePoolMemberState{}, types.CandidateSlotBindingState{}, err
	}
	if snapshot.Status == candidateSnapshotPruned || slot >= snapshot.SlotCapacity {
		return types.CandidatePoolMemberState{}, types.CandidateSlotBindingState{}, fmt.Errorf("candidate pool member body is unavailable")
	}
	member, err := k.loadCandidatePoolMember(ctx, snapshot, slot)
	if err != nil {
		return types.CandidatePoolMemberState{}, types.CandidateSlotBindingState{}, err
	}
	binding, err := k.ReadCandidateSlotBinding(ctx, types.NewCandidateSlotBindingKey(slot, member.SlotVersion))
	if err != nil {
		return types.CandidatePoolMemberState{}, types.CandidateSlotBindingState{}, err
	}
	return member, binding, nil
}

func (k Keeper) CandidatePoolSegmentBitmap(ctx context.Context, snapshotID []byte, segmentIndex uint32) ([]byte, error) {
	key, err := candidateHashKey(snapshotID)
	if err != nil {
		return nil, err
	}
	snapshot, err := k.loadCandidatePoolSnapshot(ctx, key)
	if err != nil {
		return nil, err
	}
	if snapshot.Status == candidateSnapshotPruned {
		return nil, fmt.Errorf("candidate pool body has been pruned")
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	count, err := candidateSegmentCount(snapshot.SlotCapacity, params.CandidatePool.CandidateBitmapSegmentBytes)
	if err != nil || segmentIndex >= count {
		return nil, fmt.Errorf("candidate segment index is outside snapshot geometry")
	}
	segment, err := k.CandidatePoolActiveSegment.Get(ctx, types.NewCandidatePoolSegmentKey(snapshot.Epoch, segmentIndex))
	if errors.Is(err, collections.ErrNotFound) {
		return make([]byte, params.CandidatePool.CandidateBitmapSegmentBytes), nil
	}
	if err != nil || segment.Epoch != snapshot.Epoch || segment.SegmentIndex != segmentIndex || uint32(len(segment.Bitmap)) != params.CandidatePool.CandidateBitmapSegmentBytes {
		return nil, fmt.Errorf("candidate segment state is invalid")
	}
	return append([]byte(nil), segment.Bitmap...), nil
}

// GetCandidatePoolLayout exposes the genesis-only bitmap geometry needed to map
// a stable slot to a segment without changing the shared interface in stage 1.
func (k Keeper) GetCandidatePoolLayout(ctx context.Context, snapshotID []byte) (slotCapacity, segmentBytes, segmentCount uint32, ok bool) {
	snapshotKey, err := candidateHashKey(snapshotID)
	if err != nil {
		return 0, 0, 0, false
	}
	snapshot, err := k.loadCandidatePoolSnapshot(ctx, snapshotKey)
	if err != nil || snapshot.Status == candidateSnapshotPruned {
		return 0, 0, 0, false
	}
	params, err := k.Params.Get(ctx)
	if err != nil || params.CandidatePool.CandidateBitmapSegmentBytes == 0 {
		return 0, 0, 0, false
	}
	count, err := candidateSegmentCount(snapshot.SlotCapacity, params.CandidatePool.CandidateBitmapSegmentBytes)
	if err != nil {
		return 0, 0, 0, false
	}
	return snapshot.SlotCapacity, params.CandidatePool.CandidateBitmapSegmentBytes, count, true
}

// GetCandidatePoolSegment was deleted, not renamed.
//
// It was a second, zero-caller copy of CandidatePoolSegmentBitmap: same
// snapshot load, same pruned guard, same geometry check, same omitted-segment
// materialization, differing only in returning bool instead of error and in
// taking the snapshot id as lower-hex text. The interface method is the one the
// Task module reaches through, so the copy that had to be retyped for raw keys
// was also the copy nothing used.

func (k Keeper) AcquireCandidatePoolTaskRef(ctx context.Context, taskID, snapshotID []byte, height uint64) (bool, error) {
	if len(taskID) != shared.Hash32KeySize || len(snapshotID) != shared.Hash32KeySize {
		return false, fmt.Errorf("task_id and snapshot_id must be raw Hash32")
	}
	taskKey, snapshotKey := shared.Hash32Key(taskID), shared.Hash32Key(snapshotID)
	snapshot, err := k.loadCandidatePoolSnapshot(ctx, snapshotKey)
	if err != nil {
		return false, err
	}
	if snapshot.Status != candidateSnapshotActive || height < snapshot.EffectiveHeight || height >= snapshot.ExpiresHeight {
		return false, fmt.Errorf("candidate pool snapshot is not ACTIVE at acquisition height")
	}
	key := types.NewCandidatePoolTaskRefKey(taskKey, snapshotKey)
	if existing, getErr := k.CandidatePoolTaskRef.Get(ctx, key); getErr == nil {
		if existing.Status != candidateTaskRefAcquired || !equalCandidateBytes(existing.TaskId, taskID) || !equalCandidateBytes(existing.SnapshotId, snapshot.SnapshotId) {
			return false, fmt.Errorf("candidate pool task ref replay differs")
		}
		return false, nil
	} else if !errors.Is(getErr, collections.ErrNotFound) {
		return false, getErr
	}
	if snapshot.TaskRefCount == ^uint32(0) {
		return false, fmt.Errorf("candidate pool task_ref_count overflow")
	}
	snapshot.TaskRefCount++
	if err := k.CandidatePoolSnapshot.Set(ctx, snapshotKey, snapshot); err != nil {
		return false, err
	}
	return true, k.CandidatePoolTaskRef.Set(ctx, key, types.CandidatePoolTaskRefState{
		TaskId: append([]byte(nil), taskID...), SnapshotId: append([]byte(nil), snapshot.SnapshotId...), AcquiredHeight: height, Status: candidateTaskRefAcquired,
	})
}

func (k Keeper) ReleaseCandidatePoolTaskRef(ctx context.Context, taskID, snapshotID []byte, height uint64) (bool, error) {
	if len(taskID) != shared.Hash32KeySize || len(snapshotID) != shared.Hash32KeySize {
		return false, fmt.Errorf("task_id and snapshot_id must be raw Hash32")
	}
	taskKey, snapshotKey := shared.Hash32Key(taskID), shared.Hash32Key(snapshotID)
	key := types.NewCandidatePoolTaskRefKey(taskKey, snapshotKey)
	ref, err := k.CandidatePoolTaskRef.Get(ctx, key)
	if errors.Is(err, collections.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if ref.Status != candidateTaskRefAcquired {
		return false, fmt.Errorf("candidate pool task ref has invalid status")
	}
	snapshot, err := k.loadCandidatePoolSnapshot(ctx, snapshotKey)
	if err != nil {
		return false, err
	}
	if snapshot.TaskRefCount == 0 {
		return false, fmt.Errorf("candidate pool task_ref_count underflow")
	}
	snapshot.TaskRefCount--
	if err := k.CandidatePoolSnapshot.Set(ctx, snapshotKey, snapshot); err != nil {
		return false, err
	}
	if err := k.CandidatePoolTaskRef.Remove(ctx, key); err != nil {
		return false, err
	}
	if snapshot.Status == candidateSnapshotExpired && snapshot.TaskRefCount == 0 {
		if err := k.CandidatePoolPruneIndex.Set(ctx, types.NewCandidatePoolPruneIndexKey(height, snapshotKey, types.CandidatePoolPrunePhase_CANDIDATE_POOL_PRUNE_PHASE_BODY)); err != nil {
			return false, err
		}
	}
	return true, nil
}

// ReserveCandidateSlotTaskRef increments the winner slot guard. Exact replay is
// owned by the TaskAssignment transition because the frozen Hub schema has no
// per-(task,slot) reference row.
func (k Keeper) ReserveCandidateSlotTaskRef(ctx context.Context, snapshotID []byte, slot uint32, slotVersion uint64, taskID []byte) error {
	snapshotKey, err := candidateHashKey(snapshotID)
	if err != nil {
		return err
	}
	taskKey, err := candidateHashKey(taskID)
	if err != nil {
		return err
	}
	if _, err := k.CandidatePoolTaskRef.Get(ctx, types.NewCandidatePoolTaskRefKey(taskKey, snapshotKey)); err != nil {
		return fmt.Errorf("candidate pool task ref must be acquired first: %w", err)
	}
	snapshot, err := k.loadCandidatePoolSnapshot(ctx, snapshotKey)
	if err != nil {
		return err
	}
	member, err := k.loadCandidatePoolMember(ctx, snapshot, slot)
	if err != nil || member.SlotVersion != slotVersion {
		return fmt.Errorf("candidate slot/version is not a snapshot member")
	}
	current, err := k.ReadCandidateSlotCurrent(ctx, slot)
	if err != nil || current.SlotVersion != slotVersion || current.OperatorAddress != member.OperatorAddress {
		return fmt.Errorf("candidate current slot no longer matches immutable member")
	}
	if current.ActiveTaskRefs == ^uint32(0) {
		return fmt.Errorf("candidate slot active_task_refs overflow")
	}
	current.ActiveTaskRefs++
	return k.WriteCandidateSlotCurrent(ctx, slot, current)
}

func (k Keeper) ReleaseCandidateSlotTaskRef(ctx context.Context, slot uint32, slotVersion uint64, height uint64) error {
	current, err := k.ReadCandidateSlotCurrent(ctx, slot)
	if err != nil {
		return err
	}
	if current.SlotVersion != slotVersion || current.ActiveTaskRefs == 0 {
		return fmt.Errorf("candidate slot task ref mismatch or underflow")
	}
	current.ActiveTaskRefs--
	if err := k.WriteCandidateSlotCurrent(ctx, slot, current); err != nil {
		return err
	}
	if current.Status == candidateSlotRetiring {
		return k.tryReleaseCandidateSlot(ctx, current, height)
	}
	return nil
}

func effectiveCandidateBond(bond types.ServiceBondState, currentEpoch uint64) uint64 {
	return types.EffectiveActiveBond(bond, currentEpoch)
}

func candidatePoolContextHeight(ctx context.Context) uint64 {
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	if height <= 0 {
		return 0
	}
	return uint64(height)
}

func candidatePoolChainID(ctx context.Context) string { return sdk.UnwrapSDKContext(ctx).ChainID() }

// candidateHashKey used to project a raw Hash32 into the lower-hex text the four
// candidate-pool collections were keyed on. Those keys are raw now, so the only
// thing left to do is prove the width before it reaches Hash32KeyCodec, which
// would otherwise report the same failure as an opaque encoding error.
func candidateHashKey(value []byte) (shared.Hash32Key, error) {
	if len(value) != shared.Hash32KeySize {
		return nil, fmt.Errorf("Hash32 must contain exactly 32 raw bytes")
	}
	return value, nil
}

func candidateSegmentCount(capacity, segmentBytes uint32) (uint32, error) {
	if capacity == 0 || segmentBytes == 0 {
		return 0, fmt.Errorf("candidate bitmap geometry must be positive")
	}
	bitsPerSegment := uint64(segmentBytes) * 8
	count := (uint64(capacity) + bitsPerSegment - 1) / bitsPerSegment
	if count > uint64(^uint32(0)) {
		return 0, fmt.Errorf("candidate segment count overflows uint32")
	}
	return uint32(count), nil
}

func checkedMulForCandidatePool(left, right uint64) (uint64, error) {
	value, overflow := shared.CheckedMulUint64(left, right)
	if overflow {
		return 0, fmt.Errorf("uint64 multiplication overflow")
	}
	return value, nil
}

func equalCandidateBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

const (
	candidateSnapshotReady   = types.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_READY
	candidateSnapshotActive  = types.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_ACTIVE
	candidateSnapshotExpired = types.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_EXPIRED
	candidateSnapshotPruned  = types.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_PRUNED
	candidateSlotAllocated   = types.CandidateSlotStatus_CANDIDATE_SLOT_STATUS_ALLOCATED
	candidateSlotRetiring    = types.CandidateSlotStatus_CANDIDATE_SLOT_STATUS_RETIRING
	candidateSlotFree        = types.CandidateSlotStatus_CANDIDATE_SLOT_STATUS_FREE
	candidateTaskRefAcquired = types.CandidatePoolTaskRefStatus_CANDIDATE_POOL_TASK_REF_STATUS_ACQUIRED
)
