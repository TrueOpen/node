package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (k Keeper) HasBuilderSetTaskRef(ctx context.Context, taskID []byte, builderSetID string) (bool, error) {
	taskKey, version, err := k.builderSetRefKey(ctx, taskID, builderSetID)
	if err != nil {
		return false, err
	}
	ref, err := k.BuilderSetTaskRef.Get(ctx, types.NewBuilderSetTaskRefKey(taskKey, version))
	if errors.Is(err, collections.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := validateBuilderSetTaskRef(ref, taskID, version); err != nil {
		return false, err
	}
	return true, nil
}

func (k Keeper) AcquireBuilderSetTaskRef(ctx context.Context, taskID []byte, builderSetID string, builderSetHash []byte, height uint64) (bool, error) {
	if height == 0 || len(builderSetHash) != shared.Hash32KeySize {
		return false, fmt.Errorf("builder set reference height and hash are required")
	}
	taskKey, version, err := k.builderSetRefKey(ctx, taskID, builderSetID)
	if err != nil {
		return false, err
	}
	key := types.NewBuilderSetTaskRefKey(taskKey, version)
	if existing, err := k.BuilderSetTaskRef.Get(ctx, key); err == nil {
		if err := validateBuilderSetTaskRef(existing, taskID, version); err != nil {
			return false, fmt.Errorf("builder set task reference replay differs: %w", err)
		}
		set, err := k.BuilderSet.Get(ctx, version)
		if err != nil || !bytes.Equal(set.BuilderSetHash, builderSetHash) {
			return false, fmt.Errorf("builder set task reference replay hash differs")
		}
		return false, nil
	} else if !errors.Is(err, collections.ErrNotFound) {
		return false, err
	}
	set, err := k.BuilderSet.Get(ctx, version)
	if err != nil {
		return false, err
	}
	if err := k.validateBuilderSetState(ctx, set, version); err != nil || set.BodyStatus != shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE {
		return false, fmt.Errorf("builder set body is unavailable")
	}
	if set.BuilderSetId != builderSetID || !bytes.Equal(set.BuilderSetHash, builderSetHash) {
		return false, fmt.Errorf("builder set locator does not match retained state")
	}
	if set.TaskRefCount == math.MaxUint32 {
		return false, fmt.Errorf("builder set task_ref_count overflow")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	set.TaskRefCount++
	if err := k.BuilderSet.Set(cache, version, set); err != nil {
		return false, err
	}
	if err := k.BuilderSetTaskRef.Set(cache, key, types.BuilderSetTaskRefState{
		TaskId: append([]byte(nil), taskID...), BuilderSetVersion: version, AcquiredHeight: height,
	}); err != nil {
		return false, err
	}
	commit()
	return true, nil
}

func (k Keeper) ReleaseBuilderSetTaskRef(ctx context.Context, taskID []byte, builderSetID string, builderSetHash []byte, height uint64) (bool, error) {
	if height == 0 || len(builderSetHash) != shared.Hash32KeySize {
		return false, fmt.Errorf("builder set release height and hash are required")
	}
	taskKey, version, err := k.builderSetRefKey(ctx, taskID, builderSetID)
	if err != nil {
		return false, err
	}
	key := types.NewBuilderSetTaskRefKey(taskKey, version)
	ref, err := k.BuilderSetTaskRef.Get(ctx, key)
	if errors.Is(err, collections.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := validateBuilderSetTaskRef(ref, taskID, version); err != nil {
		return false, err
	}
	set, err := k.BuilderSet.Get(ctx, version)
	if err != nil || set.BuilderSetId != builderSetID || !bytes.Equal(set.BuilderSetHash, builderSetHash) {
		return false, fmt.Errorf("builder set reference does not match retained state")
	}
	if set.TaskRefCount == 0 {
		return false, fmt.Errorf("builder set task_ref_count underflow")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	set.TaskRefCount--
	if err := k.BuilderSet.Set(cache, version, set); err != nil {
		return false, err
	}
	if err := k.BuilderSetTaskRef.Remove(cache, key); err != nil {
		return false, err
	}
	if set.TaskRefCount == 0 {
		if err := k.scheduleBuilderSetBodyPrune(cache, set); err != nil {
			return false, err
		}
	}
	commit()
	return true, nil
}

func (k Keeper) builderSetRefKey(ctx context.Context, taskID []byte, builderSetID string) (shared.Hash32Key, uint64, error) {
	if len(taskID) != shared.Hash32KeySize {
		return nil, 0, fmt.Errorf("task_id must be raw Hash32")
	}
	if builderSetID == "" {
		return nil, 0, fmt.Errorf("builder_set_id is required")
	}
	version, err := k.BuilderSetByIDIndex.Get(ctx, builderSetID)
	if err != nil {
		return nil, 0, err
	}
	if version == 0 {
		return nil, 0, fmt.Errorf("builder_set_id index contains zero version")
	}
	return append(shared.Hash32Key(nil), taskID...), version, nil
}

func validateBuilderSetTaskRef(ref types.BuilderSetTaskRefState, taskID []byte, version uint64) error {
	if !bytes.Equal(ref.TaskId, taskID) || ref.BuilderSetVersion != version || ref.AcquiredHeight == 0 {
		return fmt.Errorf("builder set task reference key does not match state")
	}
	return nil
}
