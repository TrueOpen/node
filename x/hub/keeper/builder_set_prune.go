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

func (k Keeper) ProcessBuilderSetPrunes(ctx context.Context, currentHeight, limit uint64) (uint64, error) {
	if limit == 0 {
		return 0, nil
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	visited, err := k.processBuilderSetPrunes(sdk.WrapSDKContext(cacheCtx), currentHeight, limit)
	if err != nil {
		return visited, err
	}
	commit()
	return visited, nil
}

func (k Keeper) processBuilderSetPrunes(ctx context.Context, currentHeight, limit uint64) (uint64, error) {
	var visited uint64
	for visited < limit {
		key, found, err := k.firstDueBuilderSetPrune(ctx, currentHeight)
		if err != nil || !found {
			return visited, err
		}
		visited++
		set, err := k.GetBuilderSet(ctx, key.K2())
		if errors.Is(err, collections.ErrNotFound) {
			if err := k.BuilderSetPruneIndex.Remove(ctx, key); err != nil {
				return visited, err
			}
			continue
		}
		if err != nil {
			return visited, err
		}
		switch types.BuilderSetPrunePhase(key.K3()) {
		case types.BuilderSetPrunePhase_BUILDER_SET_PRUNE_PHASE_BODY:
			eligible, err := k.builderSetBodyPruneEligible(ctx, set, currentHeight)
			if err != nil {
				return visited, err
			}
			if !eligible {
				if err := k.BuilderSetPruneIndex.Remove(ctx, key); err != nil {
					return visited, err
				}
				continue
			}
			set.ActiveBuilders = nil
			set.BodyStatus = shared.StoredBodyStatus_STORED_BODY_STATUS_PRUNED
			set.XPrunedHeight = &types.BuilderSetState_PrunedHeight{PrunedHeight: currentHeight}
			if err := k.StoreBuilderSet(ctx, set); err != nil {
				return visited, err
			}
			if err := k.BuilderSetPruneIndex.Remove(ctx, key); err != nil {
				return visited, err
			}
			if err := k.scheduleBuilderSetHeaderPrune(ctx, set); err != nil {
				return visited, err
			}
		case types.BuilderSetPrunePhase_BUILDER_SET_PRUNE_PHASE_HEADER:
			if set.BodyStatus != shared.StoredBodyStatus_STORED_BODY_STATUS_PRUNED || set.TaskRefCount != 0 || set.GetXPrunedHeight() == nil {
				return visited, fmt.Errorf("builder set header prune is not eligible")
			}
			if current, err := k.CurrentBuilderSet.Get(ctx); err == nil && current.BuilderSetVersion == set.BuilderSetVersion {
				return visited, fmt.Errorf("current builder set cannot be pruned")
			}
			if err := k.BuilderSetByIDIndex.Remove(ctx, set.BuilderSetId); err != nil {
				return visited, err
			}
			if err := k.BuilderSetByHeightIndex.Remove(ctx, types.NewBuilderSetByHeightKey(set.EffectiveHeight, set.BuilderSetVersion)); err != nil {
				return visited, err
			}
			if err := k.BuilderSet.Remove(ctx, set.BuilderSetVersion); err != nil {
				return visited, err
			}
			if err := k.BuilderSetPruneIndex.Remove(ctx, key); err != nil {
				return visited, err
			}
		default:
			return visited, fmt.Errorf("builder set prune phase is invalid")
		}
	}
	return visited, nil
}

func (k Keeper) scheduleBuilderSetBodyPrune(ctx context.Context, set types.BuilderSetState) error {
	eligible, err := k.builderSetBodyPruneEligible(ctx, set, sdkWrappedContextHeight(ctx))
	if err != nil || !eligible {
		return err
	}
	pruneEpoch, err := k.builderSetRetentionPruneEpoch(ctx, set.GetSupersededHeight(), false)
	if err != nil {
		return err
	}
	return k.BuilderSetPruneIndex.Set(ctx, types.NewBuilderSetPruneKey(pruneEpoch, set.BuilderSetVersion, types.BuilderSetPrunePhase_BUILDER_SET_PRUNE_PHASE_BODY))
}

func (k Keeper) builderSetBodyPruneEligible(ctx context.Context, set types.BuilderSetState, height uint64) (bool, error) {
	if set.BodyStatus != shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE || set.TaskRefCount != 0 ||
		set.GetXSupersededHeight() == nil || set.GetSupersededHeight() > height {
		return false, nil
	}
	current, err := k.CurrentBuilderSet.Get(ctx)
	if err != nil {
		return false, err
	}
	if current.BuilderSetVersion == set.BuilderSetVersion {
		return false, nil
	}
	if pending, err := k.GetPendingBuilderSetReplacement(ctx); err == nil && pending.NextBuilderSetVersion == set.BuilderSetVersion {
		return false, nil
	} else if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return false, err
	}
	return true, nil
}

func (k Keeper) scheduleBuilderSetHeaderPrune(ctx context.Context, set types.BuilderSetState) error {
	if set.BodyStatus != shared.StoredBodyStatus_STORED_BODY_STATUS_PRUNED || set.TaskRefCount != 0 || set.GetXPrunedHeight() == nil {
		return fmt.Errorf("builder set header prune is not schedulable")
	}
	pruneEpoch, err := k.builderSetRetentionPruneEpoch(ctx, set.GetPrunedHeight(), true)
	if err != nil {
		return err
	}
	return k.BuilderSetPruneIndex.Set(ctx, types.NewBuilderSetPruneKey(pruneEpoch, set.BuilderSetVersion, types.BuilderSetPrunePhase_BUILDER_SET_PRUNE_PHASE_HEADER))
}

func (k Keeper) builderSetRetentionPruneEpoch(ctx context.Context, baseHeight uint64, header bool) (uint64, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, err
	}
	epochs := uint64(params.Builder.BuilderSetRetentionEpochs)
	if header {
		epochs = uint64(params.Builder.BuilderSetHeaderRetentionEpochs)
	}
	baseEpoch := epochForHeight(baseHeight, normalizedEpochLengthBlocks(params))
	if baseEpoch > ^uint64(0)-epochs {
		return 0, fmt.Errorf("builder set retention epoch overflow")
	}
	return baseEpoch + epochs, nil
}

func (k Keeper) firstDueBuilderSetPrune(ctx context.Context, height uint64) (types.BuilderSetPruneKeyTriple, bool, error) {
	epochLength, err := k.epochLengthBlocks(ctx)
	if err != nil {
		return types.BuilderSetPruneKeyTriple{}, false, err
	}
	iter, err := k.BuilderSetPruneIndex.Iterate(ctx, nil)
	if err != nil {
		return types.BuilderSetPruneKeyTriple{}, false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return types.BuilderSetPruneKeyTriple{}, false, nil
	}
	key, err := iter.Key()
	if err != nil {
		return types.BuilderSetPruneKeyTriple{}, false, err
	}
	dueHeight, _, err := epochHeightRange(key.K1(), epochLength)
	if err != nil {
		return key, false, nil
	}
	return key, dueHeight <= height, nil
}

func (k Keeper) rebuildBuilderSetIndexes(ctx context.Context, sets []types.BuilderSetState) error {
	for _, set := range sets {
		if err := k.validateBuilderSetState(ctx, set, set.BuilderSetVersion); err != nil {
			return err
		}
		if _, err := k.BuilderSetByIDIndex.Get(ctx, set.BuilderSetId); err == nil {
			return fmt.Errorf("duplicate builder_set_id %q", set.BuilderSetId)
		} else if !errors.Is(err, collections.ErrNotFound) {
			return err
		}
		if err := k.BuilderSetByIDIndex.Set(ctx, set.BuilderSetId, set.BuilderSetVersion); err != nil {
			return err
		}
		if err := k.BuilderSetByHeightIndex.Set(ctx, types.NewBuilderSetByHeightKey(set.EffectiveHeight, set.BuilderSetVersion), set.BuilderSetId); err != nil {
			return err
		}
		if set.BodyStatus == shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE && set.TaskRefCount == 0 && set.GetXSupersededHeight() != nil {
			if err := k.scheduleBuilderSetBodyPrune(ctx, set); err != nil {
				return err
			}
		} else if set.BodyStatus == shared.StoredBodyStatus_STORED_BODY_STATUS_PRUNED {
			if err := k.scheduleBuilderSetHeaderPrune(ctx, set); err != nil {
				return err
			}
		}
	}
	return nil
}
