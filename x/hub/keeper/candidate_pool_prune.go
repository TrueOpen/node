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

// ProcessCandidatePoolExpiries activates the one published READY snapshot and
// expires due ACTIVE snapshots. Every inspected header/index row consumes limit.
func (k Keeper) ProcessCandidatePoolExpiries(ctx context.Context, currentHeight, limit uint64) (uint64, error) {
	if limit == 0 {
		return 0, nil
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	visited, err := k.processCandidatePoolExpiries(sdk.WrapSDKContext(cacheCtx), currentHeight, limit)
	if err != nil {
		return visited, err
	}
	commit()
	return visited, nil
}

func (k Keeper) processCandidatePoolExpiries(ctx context.Context, currentHeight, limit uint64) (uint64, error) {
	visited := uint64(0)
	for visited < limit {
		key, found, err := k.firstDueCandidateExpiry(ctx, currentHeight)
		if err != nil {
			return visited, err
		}
		if !found {
			activated, err := k.activateReadyCandidatePool(ctx, currentHeight, limit-visited)
			return visited + activated, err
		}
		visited++
		snapshot, err := k.CandidatePoolSnapshot.Get(ctx, key.K2())
		if errors.Is(err, collections.ErrNotFound) {
			if err := k.CandidatePoolExpiryIndex.Remove(ctx, key); err != nil {
				return visited, err
			}
			continue
		}
		if err != nil {
			return visited, err
		}
		if snapshot.ExpiresHeight != key.K1() || snapshot.Status == candidateSnapshotPruned {
			return visited, fmt.Errorf("candidate expiry index does not match snapshot")
		}
		wasActive := snapshot.Status == candidateSnapshotActive
		if wasActive {
			snapshot.Status = candidateSnapshotExpired
			if err := k.CandidatePoolSnapshot.Set(ctx, key.K2(), snapshot); err != nil {
				return visited, err
			}
			if current, getErr := k.CurrentCandidatePool.Get(ctx); getErr == nil && equalCandidateBytes(current.SnapshotId, snapshot.SnapshotId) {
				if err := k.CurrentCandidatePool.Remove(ctx); err != nil {
					return visited, err
				}
			} else if getErr != nil && !errors.Is(getErr, collections.ErrNotFound) {
				return visited, getErr
			}
		} else if snapshot.Status == candidateSnapshotReady {
			snapshot.Status = candidateSnapshotExpired
			if err := k.CandidatePoolSnapshot.Set(ctx, key.K2(), snapshot); err != nil {
				return visited, err
			}
			// A READY snapshot expiring un-activated is the singleton the
			// PUBLISHED build status points at. Expiring it here while leaving
			// the status at PUBLISHED strands activateReadyCandidatePool: the
			// expiry index row it resolves through is removed just below, so from
			// the next block on it fails with "no expiry index row" forever, and
			// nothing else ever resets the status. The build slot is released in
			// the same transaction that retires the snapshot.
			if err := k.releasePublishedCandidateBuild(ctx, snapshot.Epoch, currentHeight); err != nil {
				return visited, err
			}
		} else if snapshot.Status != candidateSnapshotExpired {
			return visited, fmt.Errorf("candidate expiry index references invalid lifecycle status")
		}
		if err := k.CandidatePoolExpiryIndex.Remove(ctx, key); err != nil {
			return visited, err
		}
		if snapshot.TaskRefCount == 0 {
			if err := k.CandidatePoolPruneIndex.Set(ctx, types.NewCandidatePoolPruneIndexKey(currentHeight, key.K2(), types.CandidatePoolPrunePhase_CANDIDATE_POOL_PRUNE_PHASE_BODY)); err != nil {
				return visited, err
			}
		}
		if wasActive {
			mustEmitHubEvent(ctx, &types.EventCandidatePoolExpired{
				Epoch: snapshot.Epoch, SnapshotId: append([]byte(nil), snapshot.SnapshotId...),
				PoolHash: append([]byte(nil), snapshot.PoolHash...), ExpiresHeight: snapshot.ExpiresHeight,
			})
		}
	}
	return visited, nil
}

func (k Keeper) activateReadyCandidatePool(ctx context.Context, height, limit uint64) (uint64, error) {
	status, err := k.CandidatePoolBuildStatus.Get(ctx)
	if errors.Is(err, collections.ErrNotFound) || status.Status != types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_PUBLISHED {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, err
	}
	if status.TargetEpoch == ^uint64(0) {
		return 0, fmt.Errorf("candidate target epoch overflow")
	}
	expiresHeight, err := checkedMulForCandidatePool(status.TargetEpoch+1, normalizedEpochLengthBlocks(params))
	if err != nil {
		return 0, err
	}
	// Every snapshot for target_epoch has the uniquely derived next-epoch expiry
	// height. Prefixing the existing expiry index therefore avoids repeatedly
	// scanning retained headers without inventing an epoch-to-snapshot index.
	iter, err := k.CandidatePoolExpiryIndex.Iterate(ctx, collections.NewPrefixedPairRange[uint64, shared.Hash32Key](expiresHeight))
	if err != nil {
		return 0, err
	}
	defer iter.Close()
	visited := uint64(0)
	if iter.Valid() && visited < limit {
		visited++
		indexKey, err := iter.Key()
		if err != nil {
			return visited, err
		}
		snapshot, err := k.CandidatePoolSnapshot.Get(ctx, indexKey.K2())
		if err != nil {
			return visited, err
		}
		if snapshot.Epoch != status.TargetEpoch || snapshot.ExpiresHeight != expiresHeight || snapshot.Status != candidateSnapshotReady {
			return visited, fmt.Errorf("published candidate build does not resolve to its READY snapshot")
		}
		if height < snapshot.EffectiveHeight {
			return visited, nil
		}
		if height >= snapshot.ExpiresHeight {
			status.Status = types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_IDLE
			status.UpdatedHeight = height
			if err := k.CandidatePoolBuildStatus.Set(ctx, status); err != nil {
				return visited, err
			}
			return visited, nil
		}
		// Finalization verified and committed the immutable body before publishing
		// READY. No body writer exists after that transition, so activation must not
		// perform a second unbudgeted full-body scan.
		if current, getErr := k.CurrentCandidatePool.Get(ctx); getErr == nil && !equalCandidateBytes(current.SnapshotId, snapshot.SnapshotId) {
			return visited, fmt.Errorf("another candidate pool is still current")
		} else if getErr != nil && !errors.Is(getErr, collections.ErrNotFound) {
			return visited, getErr
		}
		snapshot.Status = candidateSnapshotActive
		snapshot.PublishedHeight = height
		if err := k.CandidatePoolSnapshot.Set(ctx, indexKey.K2(), snapshot); err != nil {
			return visited, err
		}
		if err := k.CurrentCandidatePool.Set(ctx, types.CurrentCandidatePoolState{
			Epoch: snapshot.Epoch, SnapshotId: append([]byte(nil), snapshot.SnapshotId...), PoolHash: append([]byte(nil), snapshot.PoolHash...),
		}); err != nil {
			return visited, err
		}
		status.Status = types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_IDLE
		status.UpdatedHeight = height
		if err := k.CandidatePoolBuildStatus.Set(ctx, status); err != nil {
			return visited, err
		}
		mustEmitHubEvent(ctx, &types.EventCandidatePoolPublished{
			Epoch: snapshot.Epoch, SnapshotId: append([]byte(nil), snapshot.SnapshotId...),
			PoolHash: append([]byte(nil), snapshot.PoolHash...), ActiveCount: snapshot.ActiveCount,
			EffectiveHeight: snapshot.EffectiveHeight, ExpiresHeight: snapshot.ExpiresHeight,
		})
		return visited, nil
	}
	if !iter.Valid() && height >= expiresHeight {
		// The prune runner removes the expiry index row at exactly this height, so
		// a missing row once the window has passed is a completed retirement rather
		// than a torn write. Before the window it is unreachable — nothing removes
		// the row early — so that case still fails loudly below.
		//
		// The condition is the iterator, not `visited == 0`: the caller passes
		// `limit-visited`, so a zero budget also leaves visited at 0 while the row
		// is still there, and releasing the build slot on that would cancel a pool
		// that is merely waiting for the next block's budget.
		if err := k.releasePublishedCandidateBuild(ctx, status.TargetEpoch, height); err != nil {
			return visited, err
		}
		return visited, nil
	}
	return visited, fmt.Errorf("published candidate build has no expiry index row")
}

// releasePublishedCandidateBuild returns the singleton build slot to IDLE when
// the epoch it was published for can no longer be activated. It is a no-op
// unless the status is still PUBLISHED for that same epoch, so a later epoch's
// build is never cancelled by an older snapshot's retirement.
func (k Keeper) releasePublishedCandidateBuild(ctx context.Context, epoch, height uint64) error {
	status, err := k.CandidatePoolBuildStatus.Get(ctx)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil
		}
		return err
	}
	if status.Status != types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_PUBLISHED ||
		status.TargetEpoch != epoch {
		return nil
	}
	status.Status = types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_IDLE
	status.UpdatedHeight = height
	return k.CandidatePoolBuildStatus.Set(ctx, status)
}

func (k Keeper) ProcessCandidateSnapshotPrunes(ctx context.Context, currentHeight, limit uint64) (uint64, error) {
	if limit == 0 {
		return 0, nil
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	visited, err := k.processCandidateSnapshotPrunes(sdk.WrapSDKContext(cacheCtx), currentHeight, limit)
	if err != nil {
		return visited, err
	}
	commit()
	return visited, nil
}

func (k Keeper) processCandidateSnapshotPrunes(ctx context.Context, currentHeight, limit uint64) (uint64, error) {
	visited := uint64(0)
	for visited < limit {
		key, found, err := k.firstDueCandidatePrune(ctx, currentHeight)
		if err != nil || !found {
			return visited, err
		}
		phase := types.CandidatePoolPrunePhase(key.K3())
		snapshot, err := k.CandidatePoolSnapshot.Get(ctx, key.K2())
		if errors.Is(err, collections.ErrNotFound) {
			visited++
			if err := k.CandidatePoolPruneIndex.Remove(ctx, key); err != nil {
				return visited, err
			}
			continue
		}
		if err != nil {
			return visited, err
		}
		switch phase {
		case types.CandidatePoolPrunePhase_CANDIDATE_POOL_PRUNE_PHASE_BODY:
			if snapshot.Status != candidateSnapshotExpired || snapshot.TaskRefCount != 0 {
				return visited, fmt.Errorf("candidate body prune is not eligible")
			}
			member, memberFound, err := k.firstCandidateEpochMember(ctx, snapshot.Epoch)
			if err != nil {
				return visited, err
			}
			if memberFound {
				visited++
				if err := k.removeCandidateSnapshotMember(ctx, member, currentHeight); err != nil {
					return visited, err
				}
				continue
			}
			segmentKey, segmentFound, err := k.firstCandidateEpochSegment(ctx, snapshot.Epoch)
			if err != nil {
				return visited, err
			}
			if segmentFound {
				visited++
				if err := k.CandidatePoolActiveSegment.Remove(ctx, segmentKey); err != nil {
					return visited, err
				}
				continue
			}
			visited++
			snapshot.Status = candidateSnapshotPruned
			snapshot.PrunedHeight = currentHeight
			if err := k.CandidatePoolSnapshot.Set(ctx, key.K2(), snapshot); err != nil {
				return visited, err
			}
			if err := k.CandidatePoolPruneIndex.Remove(ctx, key); err != nil {
				return visited, err
			}
			params, err := k.Params.Get(ctx)
			if err != nil {
				return visited, err
			}
			retention, err := checkedMulForCandidatePool(uint64(params.CandidatePool.CandidatePoolHeaderRetentionEpochs), normalizedEpochLengthBlocks(params))
			if err != nil || currentHeight > ^uint64(0)-retention {
				return visited, fmt.Errorf("candidate header prune height overflow")
			}
			if err := k.CandidatePoolPruneIndex.Set(ctx, types.NewCandidatePoolPruneIndexKey(currentHeight+retention, key.K2(), types.CandidatePoolPrunePhase_CANDIDATE_POOL_PRUNE_PHASE_HEADER)); err != nil {
				return visited, err
			}
			mustEmitHubEvent(ctx, &types.EventCandidatePoolPruned{
				Epoch: snapshot.Epoch, SnapshotId: append([]byte(nil), snapshot.SnapshotId...),
				PoolHash: append([]byte(nil), snapshot.PoolHash...), PrunedHeight: snapshot.PrunedHeight,
			})
		case types.CandidatePoolPrunePhase_CANDIDATE_POOL_PRUNE_PHASE_HEADER:
			visited++
			if snapshot.Status != candidateSnapshotPruned || snapshot.TaskRefCount != 0 {
				return visited, fmt.Errorf("candidate header prune is not eligible")
			}
			if err := k.CandidatePoolSnapshot.Remove(ctx, key.K2()); err != nil {
				return visited, err
			}
			if err := k.CandidatePoolPruneIndex.Remove(ctx, key); err != nil {
				return visited, err
			}
		default:
			return visited, fmt.Errorf("candidate prune phase is invalid")
		}
	}
	return visited, nil
}

func (k Keeper) ProcessCandidateSlotBindingPrunes(ctx context.Context, currentEpoch, limit uint64) (uint64, error) {
	visited := uint64(0)
	for visited < limit {
		key, found, err := k.firstDueCandidateBindingPrune(ctx, currentEpoch)
		if err != nil || !found {
			return visited, err
		}
		visited++
		bindingKey := types.NewCandidateSlotBindingKey(key.K2(), key.K3())
		binding, err := k.CandidateSlotBinding.Get(ctx, bindingKey)
		if errors.Is(err, collections.ErrNotFound) {
			if err := k.CandidateSlotBindingPruneIndex.Remove(ctx, key); err != nil {
				return visited, err
			}
			continue
		}
		if err != nil {
			return visited, err
		}
		if binding.ReleasedHeight == 0 || binding.SnapshotRefCount != 0 {
			return visited, fmt.Errorf("candidate binding prune is not eligible")
		}
		if current, getErr := k.CandidateSlotCurrent.Get(ctx, key.K2()); getErr == nil && current.SlotVersion == key.K3() && current.Status != candidateSlotFree {
			return visited, fmt.Errorf("candidate binding is still current and allocated")
		} else if getErr != nil && !errors.Is(getErr, collections.ErrNotFound) {
			return visited, getErr
		}
		if err := k.CandidateSlotBinding.Remove(ctx, bindingKey); err != nil {
			return visited, err
		}
		if err := k.CandidateSlotBindingPruneIndex.Remove(ctx, key); err != nil {
			return visited, err
		}
	}
	return visited, nil
}

func (k Keeper) cleanFailedCandidateDraft(ctx context.Context, cursor types.CandidatePoolBuildCursorState, status types.CandidatePoolBuildStatusState, height, limit uint64) (uint64, error) {
	visited := uint64(0)
	for visited < limit {
		member, found, err := k.firstCandidateEpochMember(ctx, cursor.TargetEpoch)
		if err != nil {
			return visited, err
		}
		if found {
			visited++
			if err := k.CandidatePoolMember.Remove(ctx, types.NewCandidatePoolMemberKey(member.Epoch, member.Slot)); err != nil {
				return visited, err
			}
			continue
		}
		segmentKey, found, err := k.firstCandidateEpochSegment(ctx, cursor.TargetEpoch)
		if err != nil {
			return visited, err
		}
		if found {
			visited++
			if err := k.CandidatePoolActiveSegment.Remove(ctx, segmentKey); err != nil {
				return visited, err
			}
			continue
		}
		visited++
		status.Status = types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_FAILED_CAPACITY
		status.UpdatedHeight = height
		status.FailureReason = cursor.FailureReason
		if err := k.CandidatePoolBuildStatus.Set(ctx, status); err != nil {
			return visited, err
		}
		return visited, k.CandidatePoolBuildCursor.Remove(ctx, cursor.TargetEpoch)
	}
	return visited, nil
}

func (k Keeper) removeCandidateSnapshotMember(ctx context.Context, member types.CandidatePoolMemberState, height uint64) error {
	bindingKey := types.NewCandidateSlotBindingKey(member.Slot, member.SlotVersion)
	binding, err := k.CandidateSlotBinding.Get(ctx, bindingKey)
	if err != nil || binding.SnapshotRefCount == 0 {
		return fmt.Errorf("candidate binding refcount missing or underflow")
	}
	binding.SnapshotRefCount--
	if err := k.CandidateSlotBinding.Set(ctx, bindingKey, binding); err != nil {
		return err
	}
	if err := k.CandidatePoolMember.Remove(ctx, types.NewCandidatePoolMemberKey(member.Epoch, member.Slot)); err != nil {
		return err
	}
	if binding.SnapshotRefCount == 0 {
		current, getErr := k.CandidateSlotCurrent.Get(ctx, member.Slot)
		if getErr == nil && current.SlotVersion == member.SlotVersion && current.Status == candidateSlotRetiring {
			return k.tryReleaseCandidateSlot(ctx, current, height)
		}
		if getErr != nil && !errors.Is(getErr, collections.ErrNotFound) {
			return getErr
		}
	}
	return nil
}

func (k Keeper) firstDueCandidateExpiry(ctx context.Context, height uint64) (types.CandidatePoolExpiryIndexKeyPair, bool, error) {
	iter, err := k.CandidatePoolExpiryIndex.Iterate(ctx, nil)
	if err != nil {
		return types.CandidatePoolExpiryIndexKeyPair{}, false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return types.CandidatePoolExpiryIndexKeyPair{}, false, nil
	}
	key, err := iter.Key()
	return key, err == nil && key.K1() <= height, err
}

func (k Keeper) firstDueCandidatePrune(ctx context.Context, height uint64) (types.CandidatePoolPruneIndexKeyTriple, bool, error) {
	iter, err := k.CandidatePoolPruneIndex.Iterate(ctx, nil)
	if err != nil {
		return types.CandidatePoolPruneIndexKeyTriple{}, false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return types.CandidatePoolPruneIndexKeyTriple{}, false, nil
	}
	key, err := iter.Key()
	return key, err == nil && key.K1() <= height, err
}

func (k Keeper) firstDueCandidateBindingPrune(ctx context.Context, epoch uint64) (types.CandidateSlotBindingPruneIndexKeyTriple, bool, error) {
	iter, err := k.CandidateSlotBindingPruneIndex.Iterate(ctx, nil)
	if err != nil {
		return types.CandidateSlotBindingPruneIndexKeyTriple{}, false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return types.CandidateSlotBindingPruneIndexKeyTriple{}, false, nil
	}
	key, err := iter.Key()
	return key, err == nil && key.K1() <= epoch, err
}

func (k Keeper) firstCandidateEpochMember(ctx context.Context, epoch uint64) (types.CandidatePoolMemberState, bool, error) {
	iter, err := k.CandidatePoolMember.Iterate(ctx, collections.NewPrefixedPairRange[uint64, uint32](epoch))
	if err != nil {
		return types.CandidatePoolMemberState{}, false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return types.CandidatePoolMemberState{}, false, nil
	}
	value, err := iter.Value()
	return value, err == nil, err
}

func (k Keeper) firstCandidateEpochSegment(ctx context.Context, epoch uint64) (types.CandidatePoolSegmentKeyPair, bool, error) {
	iter, err := k.CandidatePoolActiveSegment.Iterate(ctx, collections.NewPrefixedPairRange[uint64, uint32](epoch))
	if err != nil {
		return types.CandidatePoolSegmentKeyPair{}, false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return types.CandidatePoolSegmentKeyPair{}, false, nil
	}
	key, err := iter.Key()
	return key, err == nil, err
}
