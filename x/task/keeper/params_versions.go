package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"

	"github.com/TrueOpen/node/x/task/types"
)

// A governance update of the Task params has to take effect for work that starts
// after it and must not reinterpret work that is already running: a Session
// computes the keys of its lifecycle index rows from its TTLs, and a Task
// compares frozen windows and union sizes against its caps. Both therefore read
// the params of the version they were created under, not the live row.
//
//   - ParamsHistory keeps the params of each version a later update replaced; the
//     current version lives in the Params row and is never duplicated there.
//   - TaskParamsVersion / SessionParamsVersion record the version a row was
//     created under (PinTaskParams / PinSessionParams) and are dropped when the
//     row is compacted.
//   - A row without a pointer predates versioning. The params never changed
//     before the first versioned update, so it runs under the version that update
//     replaced (LegacyParamsVersion), or under the live params until then.

// currentParamsVersion is the version of the live Params row. A chain whose params
// were never updated has no meta row, which reads as version 0.
func (k Keeper) currentParamsVersion(ctx context.Context) (uint64, error) {
	meta, err := k.GetTaskParamsMeta(ctx)
	if err != nil {
		return 0, err
	}
	return meta.ParamsVersion, nil
}

// paramsAtVersion returns the params that were in force at version.
func (k Keeper) paramsAtVersion(ctx context.Context, version uint64) (types.TaskParamsV1, error) {
	current, err := k.currentParamsVersion(ctx)
	if err != nil {
		return types.TaskParamsV1{}, err
	}
	if version == current {
		return k.Params.Get(ctx)
	}
	params, err := k.ParamsHistory.Get(ctx, version)
	if err != nil {
		return types.TaskParamsV1{}, fmt.Errorf("task params version %d is unavailable: %w", version, err)
	}
	return params, nil
}

// paramsOfRow resolves the params a Task or Session row runs under.
func (k Keeper) paramsOfRow(ctx context.Context, pointers collections.Map[types.Hash32Key, uint64], key types.Hash32Key) (types.TaskParamsV1, error) {
	version, err := pointers.Get(ctx, key)
	if err == nil {
		return k.paramsAtVersion(ctx, version)
	}
	if !errors.Is(err, collections.ErrNotFound) {
		return types.TaskParamsV1{}, err
	}
	legacy, err := k.LegacyParamsVersion.Get(ctx)
	if errors.Is(err, collections.ErrNotFound) {
		return k.Params.Get(ctx)
	}
	if err != nil {
		return types.TaskParamsV1{}, err
	}
	return k.paramsAtVersion(ctx, legacy)
}

// ParamsForTask returns the params a Task was accepted under.
func (k Keeper) ParamsForTask(ctx context.Context, taskKey types.TaskKey) (types.TaskParamsV1, error) {
	return k.paramsOfRow(ctx, k.TaskParamsVersion, taskKey)
}

// ParamsForSession returns the params a Session was created under.
func (k Keeper) ParamsForSession(ctx context.Context, sessionKey types.SessionKey) (types.TaskParamsV1, error) {
	return k.paramsOfRow(ctx, k.SessionParamsVersion, sessionKey)
}

func (k Keeper) pinParamsVersion(ctx context.Context, pointers collections.Map[types.Hash32Key, uint64], key types.Hash32Key) error {
	version, err := k.currentParamsVersion(ctx)
	if err != nil {
		return err
	}
	return pointers.Set(ctx, key, version)
}

// PinTaskParams records the current params version on a Task that is being accepted.
func (k Keeper) PinTaskParams(ctx context.Context, taskKey types.TaskKey) error {
	return k.pinParamsVersion(ctx, k.TaskParamsVersion, taskKey)
}

// PinSessionParams records the current params version on a Session being created.
func (k Keeper) PinSessionParams(ctx context.Context, sessionKey types.SessionKey) error {
	return k.pinParamsVersion(ctx, k.SessionParamsVersion, sessionKey)
}

func unpinParamsVersion(ctx context.Context, pointers collections.Map[types.Hash32Key, uint64], key types.Hash32Key) error {
	if err := pointers.Remove(ctx, key); err != nil && !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	return nil
}

// UnpinTaskParams drops the pointer of a Task that has been compacted away.
func (k Keeper) UnpinTaskParams(ctx context.Context, taskKey types.TaskKey) error {
	return unpinParamsVersion(ctx, k.TaskParamsVersion, taskKey)
}

// UnpinSessionParams drops the pointer of a Session that has been compacted away.
func (k Keeper) UnpinSessionParams(ctx context.Context, sessionKey types.SessionKey) error {
	return unpinParamsVersion(ctx, k.SessionParamsVersion, sessionKey)
}

// archiveParamsForUpdate keeps the version an update is about to replace, so rows
// created under it can still read it. The first call also fixes which version the
// rows that predate versioning belong to.
func (k Keeper) archiveParamsForUpdate(ctx context.Context, version uint64, replaced types.TaskParamsV1) error {
	if err := k.ParamsHistory.Set(ctx, version, replaced); err != nil {
		return err
	}
	has, err := k.LegacyParamsVersion.Has(ctx)
	if err != nil || has {
		return err
	}
	return k.LegacyParamsVersion.Set(ctx, version)
}
