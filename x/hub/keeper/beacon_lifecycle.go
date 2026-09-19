package keeper

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
)

func (k Keeper) persistBeaconStoreState(ctx context.Context, stored internaltypes.BeaconStoreState) error {
	if err := validateBeaconStoreState(stored, false); err != nil {
		return err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return fmt.Errorf("load beacon lifecycle parameters: %w", err)
	}
	if stored.Height > math.MaxUint64-params.Beacon.BeaconRetentionBlocks {
		return fmt.Errorf("beacon prune height overflows")
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	if err := k.advanceBeaconCheckpoint(cache, stored, params.Beacon.BeaconCheckpointIntervalBlocks); err != nil {
		return err
	}
	if err := k.Beacon.Set(cache, stored.Height, stored); err != nil {
		return err
	}
	if err := k.BeaconPruneIndex.Set(cache, types.NewBeaconPruneKey(
		stored.Height+params.Beacon.BeaconRetentionBlocks, stored.Height,
	)); err != nil {
		return err
	}
	commit()
	return nil
}

func (k Keeper) advanceBeaconCheckpoint(ctx context.Context, state internaltypes.BeaconStoreState, interval uint64) error {
	if interval == 0 {
		return fmt.Errorf("beacon checkpoint interval must be positive")
	}
	index := (state.Height - 1) / interval
	start := index*interval + 1
	cursor, err := k.BeaconCheckpointCursor.Get(ctx)
	cursorExists := err == nil
	if errors.Is(err, collections.ErrNotFound) {
		if state.Height != start {
			return fmt.Errorf("beacon checkpoint %d begins at height %d, got %d", index, start, state.Height)
		}
		cursor = types.BeaconCheckpointCursorState{
			CheckpointIndex: index,
			StartHeight:     start,
			RollingRoot:     make([]byte, sha256.Size),
		}
	} else if err != nil {
		return err
	}
	if cursorExists {
		if err := types.ValidateBeaconCheckpointCursorState(cursor, interval); err != nil {
			return err
		}
	}
	if cursor.CheckpointIndex != index || cursor.StartHeight != start ||
		cursor.BeaconCount >= interval || cursor.LastHeight == math.MaxUint64 ||
		cursor.LastHeight+1 != state.Height && cursor.BeaconCount != 0 {
		return fmt.Errorf("beacon checkpoint %d continuation is invalid", index)
	}
	if cursor.BeaconCount == 0 && state.Height != start {
		return fmt.Errorf("beacon checkpoint %d start is invalid", index)
	}
	root, err := types.BeaconCheckpointStep(
		index, start, state.Height, cursor.RollingRoot,
		state.Randomness, state.ProofDigest, state.ProposerConsensusAddress,
	)
	if err != nil {
		return err
	}
	cursor.LastHeight = state.Height
	cursor.BeaconCount++
	cursor.RollingRoot = root
	if cursor.BeaconCount == interval {
		checkpoint := types.BeaconCheckpointState{
			CheckpointIndex: cursor.CheckpointIndex,
			StartHeight:     cursor.StartHeight,
			EndHeight:       cursor.LastHeight,
			BeaconCount:     cursor.BeaconCount,
			CheckpointRoot:  append([]byte(nil), cursor.RollingRoot...),
		}
		if err := types.ValidateBeaconCheckpointState(checkpoint, interval); err != nil {
			return err
		}
		exists, err := k.BeaconCheckpoint.Has(ctx, index)
		if err != nil {
			return err
		}
		if exists {
			return fmt.Errorf("beacon checkpoint %d is already closed", index)
		}
		if err := k.BeaconCheckpoint.Set(ctx, index, checkpoint); err != nil {
			return err
		}
		if cursorExists {
			return k.BeaconCheckpointCursor.Remove(ctx)
		}
		return nil
	}
	return k.BeaconCheckpointCursor.Set(ctx, cursor)
}

func (k Keeper) AcquireBeaconConsumerRef(ctx context.Context, height uint64, kind types.BeaconConsumerKind, consumerID string) error {
	if err := types.ValidateBeaconConsumerRef(height, kind, consumerID); err != nil {
		return err
	}
	return k.BeaconConsumerRef.Set(ctx, types.NewBeaconConsumerRefKey(height, kind, consumerID))
}

func (k Keeper) ReleaseBeaconConsumerRef(ctx context.Context, height uint64, kind types.BeaconConsumerKind, consumerID string) error {
	if err := types.ValidateBeaconConsumerRef(height, kind, consumerID); err != nil {
		return err
	}
	return k.BeaconConsumerRef.Remove(ctx, types.NewBeaconConsumerRefKey(height, kind, consumerID))
}

func (k Keeper) HasBeaconConsumerRefs(ctx context.Context, height uint64) (bool, error) {
	if height == 0 {
		return false, fmt.Errorf("beacon height must be > 0")
	}
	iter, err := k.BeaconConsumerRef.Iterate(
		ctx, collections.NewPrefixedTripleRange[uint64, uint32, string](height),
	)
	if err != nil {
		return false, err
	}
	defer iter.Close()
	return iter.Valid(), nil
}

func (k Keeper) ProcessBeaconPrunes(ctx context.Context, currentHeight, visitedLimit uint64) (uint64, error) {
	if visitedLimit == 0 {
		return 0, nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, err
	}
	iter, err := k.BeaconPruneIndex.Iterate(
		ctx, new(collections.Range[types.BeaconPruneKey]).EndInclusive(types.NewBeaconPruneKey(currentHeight, math.MaxUint64)),
	)
	if err != nil {
		return 0, err
	}
	keys := make([]types.BeaconPruneKey, 0, visitedLimit)
	for ; iter.Valid() && uint64(len(keys)) < visitedLimit; iter.Next() {
		key, err := iter.Key()
		if err != nil {
			iter.Close()
			return 0, err
		}
		keys = append(keys, key)
	}
	iter.Close()

	visited := uint64(0)
	for _, key := range keys {
		visited++
		beaconHeight := key.K2()
		state, err := k.Beacon.Get(ctx, beaconHeight)
		if errors.Is(err, collections.ErrNotFound) {
			if err := k.BeaconPruneIndex.Remove(ctx, key); err != nil {
				return visited, err
			}
			continue
		}
		if err != nil {
			return visited, err
		}
		if err := validateBeaconStoreState(state, false); err != nil {
			return visited, err
		}
		if state.Height != beaconHeight {
			return visited, fmt.Errorf("beacon prune index identity mismatch")
		}
		blocked, err := k.HasBeaconConsumerRefs(ctx, beaconHeight)
		if err != nil {
			return visited, err
		}
		if blocked {
			if currentHeight > math.MaxUint64-params.Beacon.BeaconRetentionBlocks {
				return visited, fmt.Errorf("beacon prune reschedule height overflows")
			}
			if err := k.BeaconPruneIndex.Remove(ctx, key); err != nil {
				return visited, err
			}
			if err := k.BeaconPruneIndex.Set(ctx, types.NewBeaconPruneKey(
				currentHeight+params.Beacon.BeaconRetentionBlocks, beaconHeight,
			)); err != nil {
				return visited, err
			}
			continue
		}
		if err := k.Beacon.Remove(ctx, beaconHeight); err != nil {
			return visited, err
		}
		if err := k.BeaconPruneIndex.Remove(ctx, key); err != nil {
			return visited, err
		}
	}
	return visited, nil
}
