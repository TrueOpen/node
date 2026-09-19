package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (k Keeper) GetParameterBucketVersion(ctx context.Context, kind shared.BucketKind, bucketKey string, version uint64) (types.ParameterBucketVersionState, error) {
	if !types.IsParameterBucketKind(kind) || version == 0 {
		return types.ParameterBucketVersionState{}, fmt.Errorf("parameter bucket kind and version are required")
	}
	if err := types.ValidateParameterBucketKey(bucketKey); err != nil {
		return types.ParameterBucketVersionState{}, err
	}
	state, err := k.ParameterBucketVersion.Get(ctx, types.NewParameterBucketVersionKey(kind, bucketKey, version))
	if err != nil {
		return types.ParameterBucketVersionState{}, err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return types.ParameterBucketVersionState{}, err
	}
	if err := types.ValidateParameterBucketVersion(state, params.Bucket, sdk.UnwrapSDKContext(ctx).ChainID()); err != nil {
		return types.ParameterBucketVersionState{}, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	if state.BucketKind != kind || state.BucketKey != bucketKey || state.Version != version {
		return types.ParameterBucketVersionState{}, errorsmod.Wrap(types.ErrInvariantBroken, "parameter bucket primary key does not match its body")
	}
	return state, nil
}

func (k Keeper) ResolveEffectiveParameterBucket(ctx context.Context, kind shared.BucketKind, bucketKey string, height uint64) (types.ParameterBucketVersionState, error) {
	if !types.IsParameterBucketKind(kind) {
		return types.ParameterBucketVersionState{}, fmt.Errorf("parameter bucket kind is invalid")
	}
	// An empty bucket_key is rejected rather than defaulted to
	// DefaultParameterBucketKey. Defaulting here made acquire/release asymmetric:
	// the acquire path resolved "" to the "default" version and incremented its
	// task_ref_count, while the matching release goes through
	// GetParameterBucketVersion and is refused by ValidateParameterBucketKey, so
	// the refcount would be pinned above zero forever and the version could never
	// be scheduled for prune. ValidateParameterBucketKey is the single gate on
	// every other entry point (GetParameterBucketVersion, the bucket Queries,
	// Genesis validation and the cross-module invariant); this call site was the
	// only one that silently rewrote its input, so both sides now fail closed on
	// "" and callers must name the bucket they mean.
	if err := types.ValidateParameterBucketKey(bucketKey); err != nil {
		return types.ParameterBucketVersionState{}, err
	}
	pointerKey := types.NewParameterBucketPointerKey(kind, bucketKey)
	current, err := k.ParameterBucketCurrentPointer.Get(ctx, pointerKey)
	if err != nil {
		return types.ParameterBucketVersionState{}, err
	}
	if current.BucketKind != kind || current.BucketKey != bucketKey || current.CurrentVersion == 0 {
		return types.ParameterBucketVersionState{}, errorsmod.Wrap(types.ErrInvariantBroken, "parameter bucket current pointer is corrupt")
	}
	selected, err := k.GetParameterBucketVersion(ctx, kind, bucketKey, current.CurrentVersion)
	if err != nil {
		return types.ParameterBucketVersionState{}, err
	}
	if !bytes.Equal(current.CurrentContentHash, selected.ContentHash) {
		return types.ParameterBucketVersionState{}, errorsmod.Wrap(types.ErrInvariantBroken, "parameter bucket current pointer hash mismatch")
	}
	if selected.EffectiveHeight > height {
		return types.ParameterBucketVersionState{}, fmt.Errorf("parameter bucket has no version effective at height %d", height)
	}
	pending, err := k.ParameterBucketPendingPointer.Get(ctx, pointerKey)
	if err == nil {
		if pending.BucketKind != kind || pending.BucketKey != bucketKey || pending.PendingVersion == 0 ||
			current.CurrentVersion == math.MaxUint64 || pending.PendingVersion != current.CurrentVersion+1 {
			return types.ParameterBucketVersionState{}, errorsmod.Wrap(types.ErrInvariantBroken, "parameter bucket pending pointer is corrupt")
		}
		pendingBody, err := k.GetParameterBucketVersion(ctx, kind, bucketKey, pending.PendingVersion)
		if err != nil {
			return types.ParameterBucketVersionState{}, err
		}
		if pendingBody.EffectiveHeight != pending.EffectiveHeight || !bytes.Equal(pendingBody.ContentHash, pending.PendingContentHash) {
			return types.ParameterBucketVersionState{}, errorsmod.Wrap(types.ErrInvariantBroken, "parameter bucket pending pointer does not match its body")
		}
		if pending.EffectiveHeight <= height {
			selected = pendingBody
		}
	} else if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return types.ParameterBucketVersionState{}, err
	}
	return selected, nil
}

func (k Keeper) AcquireParameterBucketTaskRef(ctx context.Context, kind shared.BucketKind, bucketKey string, version, height uint64) error {
	effective, err := k.ResolveEffectiveParameterBucket(ctx, kind, bucketKey, height)
	if err != nil {
		return err
	}
	if effective.Version != version {
		return fmt.Errorf("parameter bucket version %d is not effective at height %d", version, height)
	}
	if effective.TaskRefCount == math.MaxUint64 {
		return errorsmod.Wrap(types.ErrInvariantBroken, "parameter bucket task_ref_count overflow")
	}
	effective.TaskRefCount++
	return k.ParameterBucketVersion.Set(ctx, types.NewParameterBucketVersionKey(kind, effective.BucketKey, version), effective)
}

func (k Keeper) ReleaseParameterBucketTaskRef(ctx context.Context, kind shared.BucketKind, bucketKey string, version, height uint64) error {
	state, err := k.GetParameterBucketVersion(ctx, kind, bucketKey, version)
	if err != nil {
		return err
	}
	if state.TaskRefCount == 0 {
		return errorsmod.Wrap(types.ErrInvariantBroken, "parameter bucket task_ref_count underflow")
	}
	state.TaskRefCount--
	if err := k.ParameterBucketVersion.Set(ctx, types.NewParameterBucketVersionKey(kind, bucketKey, version), state); err != nil {
		return err
	}
	if state.TaskRefCount == 0 {
		return k.scheduleParameterBucketPruneIfEligible(ctx, state, height)
	}
	return nil
}

func (k Keeper) scheduleParameterBucketPruneIfEligible(ctx context.Context, state types.ParameterBucketVersionState, height uint64) error {
	pointerKey := types.NewParameterBucketPointerKey(state.BucketKind, state.BucketKey)
	current, err := k.ParameterBucketCurrentPointer.Get(ctx, pointerKey)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	if err == nil && current.CurrentVersion == state.Version {
		return nil
	}
	pending, err := k.ParameterBucketPendingPointer.Get(ctx, pointerKey)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	if err == nil && pending.PendingVersion == state.Version {
		return nil
	}
	if state.TaskRefCount != 0 {
		return nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	pruneHeight, overflow := checkedAddHubUint64(state.CreatedHeight, params.Bucket.ParameterBucketVersionRetentionBlocks)
	if overflow {
		return errorsmod.Wrap(types.ErrInvariantBroken, "parameter bucket prune height overflow")
	}
	if pruneHeight < height {
		pruneHeight = height
	}
	return k.ParameterBucketPruneIndex.Set(ctx, types.NewParameterBucketPruneIndexKey(pruneHeight, state.BucketKind, state.BucketKey, state.Version))
}

func (k Keeper) ProcessParameterBucketActivations(ctx context.Context, currentHeight, visitedLimit, bytesLimit uint64) (uint64, uint64, error) {
	budget := newEndblockBudget(visitedLimit, bytesLimit)
	keys, err := dueParameterBucketEffectiveKeys(ctx, k.ParameterBucketEffectiveIndex, currentHeight, visitedLimit)
	if err != nil {
		return 0, 0, err
	}
	for _, key := range keys {
		if budget.exhausted() {
			break
		}
		kind := shared.BucketKind(key.K2())
		pointerKey := types.NewParameterBucketPointerKey(kind, key.K3())
		pending, err := k.ParameterBucketPendingPointer.Get(ctx, pointerKey)
		if errors.Is(err, collections.ErrNotFound) {
			if err := k.ParameterBucketEffectiveIndex.Remove(ctx, key); err != nil {
				return budgetErrorResult(budget, err)
			}
			budget.charge(0)
			continue
		}
		if err != nil {
			return budgetErrorResult(budget, err)
		}
		if pending.BucketKind != kind || pending.BucketKey != key.K3() || pending.PendingVersion != key.K4() || pending.EffectiveHeight != key.K1() {
			return budgetErrorResult(budget, errorsmod.Wrap(types.ErrInvariantBroken, "parameter bucket effective index mismatch"))
		}
		body, err := k.GetParameterBucketVersion(ctx, kind, pending.BucketKey, pending.PendingVersion)
		if err != nil {
			return budgetErrorResult(budget, err)
		}
		if body.EffectiveHeight != pending.EffectiveHeight || !bytes.Equal(body.ContentHash, pending.PendingContentHash) {
			return budgetErrorResult(budget, errorsmod.Wrap(types.ErrInvariantBroken, "parameter bucket pending body mismatch"))
		}
		current, err := k.ParameterBucketCurrentPointer.Get(ctx, pointerKey)
		if err != nil {
			return budgetErrorResult(budget, err)
		}
		old, err := k.GetParameterBucketVersion(ctx, kind, current.BucketKey, current.CurrentVersion)
		if err != nil {
			return budgetErrorResult(budget, err)
		}
		if current.BucketKind != kind || current.BucketKey != pending.BucketKey ||
			current.CurrentVersion == math.MaxUint64 || pending.PendingVersion != current.CurrentVersion+1 ||
			!bytes.Equal(current.CurrentContentHash, old.ContentHash) {
			return budgetErrorResult(budget, errorsmod.Wrap(types.ErrInvariantBroken, "parameter bucket current pointer does not match activation"))
		}
		if err := k.ParameterBucketCurrentPointer.Set(ctx, pointerKey, types.ParameterBucketCurrentPointerState{
			BucketKind: kind, BucketKey: pending.BucketKey, CurrentVersion: pending.PendingVersion, CurrentContentHash: append([]byte(nil), pending.PendingContentHash...),
		}); err != nil {
			return budgetErrorResult(budget, err)
		}
		if err := k.ParameterBucketPendingPointer.Remove(ctx, pointerKey); err != nil {
			return budgetErrorResult(budget, err)
		}
		if err := k.ParameterBucketEffectiveIndex.Remove(ctx, key); err != nil {
			return budgetErrorResult(budget, err)
		}
		if err := k.scheduleParameterBucketPruneIfEligible(ctx, old, currentHeight); err != nil {
			return budgetErrorResult(budget, err)
		}
		budget.charge(pending.Size() + body.Size() + current.Size() + old.Size())
	}
	visited, consumed := budget.result()
	return visited, consumed, nil
}

func (k Keeper) ProcessParameterBucketPrunes(ctx context.Context, currentHeight, visitedLimit, bytesLimit uint64) (uint64, uint64, error) {
	budget := newEndblockBudget(visitedLimit, bytesLimit)
	keys, err := dueParameterBucketPruneKeys(ctx, k.ParameterBucketPruneIndex, currentHeight, visitedLimit)
	if err != nil {
		return 0, 0, err
	}
	for _, key := range keys {
		if budget.exhausted() {
			break
		}
		kind := shared.BucketKind(key.K2())
		versionKey := types.NewParameterBucketVersionKey(kind, key.K3(), key.K4())
		state, err := k.ParameterBucketVersion.Get(ctx, versionKey)
		rowBytes := 0
		if errors.Is(err, collections.ErrNotFound) {
			if err := k.ParameterBucketPruneIndex.Remove(ctx, key); err != nil {
				return budgetErrorResult(budget, err)
			}
			budget.charge(0)
			continue
		}
		if err != nil {
			return budgetErrorResult(budget, err)
		}
		rowBytes = state.Size()
		eligible, err := k.isParameterBucketPruneEligible(ctx, state)
		if err != nil {
			return budgetErrorResult(budget, err)
		}
		if eligible {
			if err := k.ParameterBucketVersion.Remove(ctx, versionKey); err != nil {
				return budgetErrorResult(budget, err)
			}
		}
		if err := k.ParameterBucketPruneIndex.Remove(ctx, key); err != nil {
			return budgetErrorResult(budget, err)
		}
		budget.charge(rowBytes)
	}
	visited, consumed := budget.result()
	return visited, consumed, nil
}

func (k Keeper) isParameterBucketPruneEligible(ctx context.Context, state types.ParameterBucketVersionState) (bool, error) {
	if state.TaskRefCount != 0 {
		return false, nil
	}
	pointerKey := types.NewParameterBucketPointerKey(state.BucketKind, state.BucketKey)
	current, err := k.ParameterBucketCurrentPointer.Get(ctx, pointerKey)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return false, err
	}
	if err == nil && current.CurrentVersion == state.Version {
		return false, nil
	}
	pending, err := k.ParameterBucketPendingPointer.Get(ctx, pointerKey)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return false, err
	}
	return err != nil || pending.PendingVersion != state.Version, nil
}

func dueParameterBucketEffectiveKeys(ctx context.Context, index collections.KeySet[types.ParameterBucketEffectiveIndexKeyQuad], height, limit uint64) ([]types.ParameterBucketEffectiveIndexKeyQuad, error) {
	iter, err := index.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	keys := make([]types.ParameterBucketEffectiveIndexKeyQuad, 0, limit)
	for ; iter.Valid() && uint64(len(keys)) < limit; iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		if key.K1() > height {
			break
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func dueParameterBucketPruneKeys(ctx context.Context, index collections.KeySet[types.ParameterBucketPruneIndexKeyQuad], height, limit uint64) ([]types.ParameterBucketPruneIndexKeyQuad, error) {
	iter, err := index.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	keys := make([]types.ParameterBucketPruneIndexKeyQuad, 0, limit)
	for ; iter.Valid() && uint64(len(keys)) < limit; iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		if key.K1() > height {
			break
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func budgetErrorResult(budget *endblockBudget, err error) (uint64, uint64, error) {
	visited, consumed := budget.result()
	return visited, consumed, err
}
