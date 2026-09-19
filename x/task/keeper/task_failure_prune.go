package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) firstDueTaskFailurePrune(ctx context.Context, currentHeight uint64) (deadlineQueueHead, bool, error) {
	iter, err := k.TaskFailureClassWindowPruneIndex.Iterate(ctx, nil)
	if err != nil {
		return deadlineQueueHead{}, false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return deadlineQueueHead{}, false, nil
	}
	key, err := iter.Key()
	if err != nil || key.K1() > currentHeight {
		return deadlineQueueHead{}, false, err
	}
	return deadlineQueueHead{deadline: key.K1(), primaryID: key.K2()}, true, nil
}

func (k Keeper) SweepTaskFailureClassPrune(ctx context.Context, currentHeight, limit uint64) (uint64, error) {
	if limit == 0 {
		return 0, nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, err
	}
	if cap := uint64(params.Cleanup.MaxTaskFailureClassPruneItemsPerBlock); limit > cap {
		limit = cap
	}
	retention := k.hubKeeper.GetHubParams(sdk.UnwrapSDKContext(ctx)).FreezeFailureIndexRetentionBlocks
	visited := uint64(0)
	for visited < limit {
		iter, err := k.TaskFailureClassWindowPruneIndex.Iterate(ctx, nil)
		if err != nil {
			return visited, err
		}
		if !iter.Valid() {
			iter.Close()
			break
		}
		key, err := iter.Key()
		iter.Close()
		if err != nil {
			return visited, err
		}
		if key.K1() > currentHeight {
			break
		}
		sdkCtx := sdk.UnwrapSDKContext(ctx)
		cacheCtx, write := sdkCtx.CacheContext()
		if err := k.pruneTaskFailureClass(sdk.WrapSDKContext(cacheCtx), key, currentHeight, retention); err != nil {
			return visited, err
		}
		write()
		visited++
	}
	return visited, nil
}

func (k Keeper) pruneTaskFailureClass(ctx context.Context, schedule types.TaskFailureClassPruneKey, currentHeight, retention uint64) error {
	taskKey, round := schedule.K2(), schedule.K3()
	primaryKey := types.NewVerifyRoundKey(taskKey, round)
	state, err := k.TaskFailureClass.Get(ctx, primaryKey)
	if errors.Is(err, collections.ErrNotFound) {
		return k.TaskFailureClassWindowPruneIndex.Remove(ctx, schedule)
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(state.TaskId, taskKey) || state.VerifyRound != round ||
		state.XPruneHeight == nil || state.GetPruneHeight() != schedule.K1() ||
		state.XTaskFinalityHeight == nil || state.GetTaskFinalityHeight() > state.GetPruneHeight() {
		return fmt.Errorf("failure class prune schedule mismatch")
	}
	protected, err := k.hubKeeper.IsFreezeFailureWindowProtected(
		ctx, state.ModelId, state.ProfileVersion, state.GetTaskFinalityHeight(),
	)
	if err != nil {
		return err
	}
	if protected {
		if retention == 0 {
			return fmt.Errorf("freeze failure retention is zero")
		}
		next, overflow := checkedHeightAdd(currentHeight, retention)
		if overflow {
			return fmt.Errorf("failure class protected prune height overflow")
		}
		if err := k.TaskFailureClassWindowPruneIndex.Remove(ctx, schedule); err != nil {
			return err
		}
		state.XPruneHeight = &types.TaskFailureClassState_PruneHeight{PruneHeight: next}
		if err := k.TaskFailureClass.Set(ctx, primaryKey, state); err != nil {
			return err
		}
		return k.TaskFailureClassWindowPruneIndex.Set(ctx,
			types.NewTaskFailureClassPruneKey(next, taskKey, round))
	}
	if state.SupersededByVerifyRound == 0 &&
		state.FailureClass != types.TaskFailureClass_TASK_FAILURE_CLASS_NONE {
		if err := k.TaskFailureClassByProfileWindowIndex.Remove(ctx,
			types.NewTaskFailureClassByProfileWindowKey(
				state.ModelId, state.ProfileVersion, state.GetTaskFinalityHeight(),
				state.FailureClass, taskKey,
			)); err != nil {
			return err
		}
	}
	if err := k.TaskFailureClass.Remove(ctx, primaryKey); err != nil {
		return err
	}
	return k.TaskFailureClassWindowPruneIndex.Remove(ctx, schedule)
}
