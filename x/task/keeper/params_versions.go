package keeper

import (
	"context"
	"errors"
	"fmt"
	"math"

	"cosmossdk.io/collections"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// Object bindings keep governed lifecycle behavior stable. Current parameters
// are stored once; superseded bodies serve references and recent epoch lookup.
func (k Keeper) currentParamsVersion(ctx context.Context) (uint64, error) {
	meta, err := k.GetTaskParamsMeta(ctx)
	return meta.ParamsVersion, err
}

func (k Keeper) paramsAtVersion(ctx context.Context, version uint64) (types.TaskParamsV1, error) {
	current, err := k.currentParamsVersion(ctx)
	if err != nil {
		return types.TaskParamsV1{}, err
	}
	if version == current {
		return k.Params.Get(ctx)
	}
	row, err := k.ParamsHistory.Get(ctx, version)
	if err != nil {
		return types.TaskParamsV1{}, fmt.Errorf("task params version %d is unavailable: %w", version, err)
	}
	if row.ParamsVersion != version {
		return types.TaskParamsV1{}, fmt.Errorf("parameter history key/version mismatch")
	}
	return row.Params, nil
}

func (k Keeper) paramsAtHeight(ctx context.Context, height uint64) (types.TaskParamsV1, error) {
	currentHeight, err := currentBlockHeight(ctx)
	if err != nil {
		return types.TaskParamsV1{}, err
	}
	if height > currentHeight {
		return types.TaskParamsV1{}, fmt.Errorf("parameter reference height is in the future")
	}
	meta, err := k.GetTaskParamsMeta(ctx)
	if err != nil {
		return types.TaskParamsV1{}, err
	}
	if meta.UpdatedHeight <= height {
		return k.Params.Get(ctx)
	}
	bound := new(collections.Range[collections.Pair[uint64, uint64]]).EndInclusive(collections.Join(height, uint64(math.MaxUint64))).Descending()
	iter, err := k.ParamsHistoryByHeightIndex.Iterate(ctx, bound)
	if err != nil {
		return types.TaskParamsV1{}, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return types.TaskParamsV1{}, fmt.Errorf("parameter policy at height %d is unavailable", height)
	}
	key, err := iter.Key()
	if err != nil {
		return types.TaskParamsV1{}, err
	}
	row, err := k.ParamsHistory.Get(ctx, key.K2())
	if err != nil {
		return types.TaskParamsV1{}, err
	}
	if row.UpdatedHeight != key.K1() || row.ParamsVersion != key.K2() || height >= row.SupersededHeight {
		return types.TaskParamsV1{}, fmt.Errorf("parameter height index does not cover its selector")
	}
	return row.Params, nil
}

func (k Keeper) paramsOfRow(ctx context.Context, pointers collections.Map[types.Hash32Key, uint64], key types.Hash32Key) (types.TaskParamsV1, error) {
	version, err := pointers.Get(ctx, key)
	if errors.Is(err, collections.ErrNotFound) {
		return k.Params.Get(ctx)
	}
	if err != nil {
		return types.TaskParamsV1{}, err
	}
	return k.paramsAtVersion(ctx, version)
}

// ParamsForTask returns the parameters frozen when a Task was accepted.
func (k Keeper) ParamsForTask(ctx context.Context, key types.TaskKey) (types.TaskParamsV1, error) {
	return k.paramsOfRow(ctx, k.TaskParamsVersion, key)
}

// ParamsForSession returns the parameters frozen when a Session was created.
func (k Keeper) ParamsForSession(ctx context.Context, key types.SessionStoreKey) (types.TaskParamsV1, error) {
	return k.paramsOfRow(ctx, k.SessionParamsVersion, key)
}

func (k Keeper) paramsVersionRefCount(ctx context.Context, version uint64) (uint64, error) {
	count, err := k.ParamsVersionRefCount.Get(ctx, version)
	if errors.Is(err, collections.ErrNotFound) {
		return 0, nil
	}
	return count, err
}

func (k Keeper) bindParamsVersion(ctx context.Context, pointers collections.Map[types.Hash32Key, uint64], key types.Hash32Key, version uint64) error {
	if _, err := pointers.Get(ctx, key); err == nil {
		return fmt.Errorf("parameter object binding already exists")
	} else if !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	if _, err := k.paramsAtVersion(ctx, version); err != nil {
		return err
	}
	count, err := k.paramsVersionRefCount(ctx, version)
	if err != nil {
		return err
	}
	next, overflow := shared.CheckedAddUint64(count, 1)
	if overflow {
		return fmt.Errorf("parameter version reference count overflows")
	}
	if err = pointers.Set(ctx, key, version); err != nil {
		return err
	}
	return k.ParamsVersionRefCount.Set(ctx, version, next)
}

func (k Keeper) pinParamsVersion(ctx context.Context, pointers collections.Map[types.Hash32Key, uint64], key types.Hash32Key) error {
	if version, err := pointers.Get(ctx, key); err == nil {
		_, err = k.paramsAtVersion(ctx, version)
		return err
	} else if !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	version, err := k.currentParamsVersion(ctx)
	if err != nil {
		return err
	}
	return k.bindParamsVersion(ctx, pointers, key, version)
}

// PinTaskParams records a Task's initial version, without rebinding a replay.
func (k Keeper) PinTaskParams(ctx context.Context, key types.TaskKey) error {
	return k.pinParamsVersion(ctx, k.TaskParamsVersion, key)
}

// PinSessionParams records a Session's initial version, without rebinding a replay.
func (k Keeper) PinSessionParams(ctx context.Context, key types.SessionStoreKey) error {
	return k.pinParamsVersion(ctx, k.SessionParamsVersion, key)
}

func (k Keeper) paramsHistoryPruneFloor(ctx context.Context, superseded uint64) (uint64, error) {
	length := k.hubKeeper.GetHubParams(sdkContextFrom(ctx)).EpochLengthBlocks
	if length == 0 {
		return 0, fmt.Errorf("parameter history requires a positive epoch length")
	}
	epoch, overflow := shared.CheckedAddUint64(shared.EpochForHeight(superseded, length), 1)
	if overflow {
		return 0, fmt.Errorf("parameter history epoch overflows")
	}
	start, overflow := checkedMulUint64(epoch, length)
	if overflow {
		return 0, fmt.Errorf("parameter history epoch height overflows")
	}
	floor, overflow := shared.CheckedHeightAddV1(start, 1)
	if overflow {
		return 0, fmt.Errorf("parameter history retention height overflows")
	}
	return floor, nil
}

func (k Keeper) scheduleParamsHistoryPrune(ctx context.Context, row types.TaskParamsHistoryState) error {
	count, err := k.paramsVersionRefCount(ctx, row.ParamsVersion)
	if err != nil || count != 0 {
		return err
	}
	floor, err := k.paramsHistoryPruneFloor(ctx, row.SupersededHeight)
	if err != nil {
		return err
	}
	return k.ParamsHistoryPruneIndex.Set(ctx, collections.Join(floor, row.ParamsVersion))
}

func (k Keeper) unpinParamsVersion(ctx context.Context, pointers collections.Map[types.Hash32Key, uint64], key types.Hash32Key) error {
	version, err := pointers.Get(ctx, key)
	if errors.Is(err, collections.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	count, err := k.paramsVersionRefCount(ctx, version)
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("parameter version reference count underflows")
	}
	if err = pointers.Remove(ctx, key); err != nil {
		return err
	}
	if count > 1 {
		return k.ParamsVersionRefCount.Set(ctx, version, count-1)
	}
	if err = k.ParamsVersionRefCount.Remove(ctx, version); err != nil {
		return err
	}
	current, err := k.currentParamsVersion(ctx)
	if err != nil || version == current {
		return err
	}
	row, err := k.ParamsHistory.Get(ctx, version)
	if err != nil {
		return err
	}
	return k.scheduleParamsHistoryPrune(ctx, row)
}

// UnpinTaskParams releases the version after Task compaction.
func (k Keeper) UnpinTaskParams(ctx context.Context, key types.TaskKey) error {
	return k.unpinParamsVersion(ctx, k.TaskParamsVersion, key)
}

// UnpinSessionParams releases the version after Session compaction.
func (k Keeper) UnpinSessionParams(ctx context.Context, key types.SessionStoreKey) error {
	return k.unpinParamsVersion(ctx, k.SessionParamsVersion, key)
}

func (k Keeper) archiveParamsForUpdate(ctx context.Context, version uint64, replaced types.TaskParamsV1) error {
	meta, err := k.GetTaskParamsMeta(ctx)
	if err != nil {
		return err
	}
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return err
	}
	if meta.ParamsVersion != version || meta.UpdatedHeight > height {
		return fmt.Errorf("parameter history metadata disagrees with its current version")
	}
	if exists, err := k.ParamsHistory.Has(ctx, version); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("current parameter version already has a historical body")
	}
	row := types.TaskParamsHistoryState{ParamsVersion: version, Params: replaced, UpdatedHeight: meta.UpdatedHeight, SupersededHeight: height}
	if err = k.ParamsHistory.Set(ctx, version, row); err != nil {
		return err
	}
	if err = k.ParamsHistoryByHeightIndex.Set(ctx, collections.Join(row.UpdatedHeight, version)); err != nil {
		return err
	}
	return k.scheduleParamsHistoryPrune(ctx, row)
}
