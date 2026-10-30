package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	shared "github.com/TrueOpen/node/x/shared/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

type paramsHistoryPruneQueue struct {
	k    Keeper
	last *collections.Pair[uint64, uint64]
	row  collections.Pair[uint64, uint64]
}

func (q *paramsHistoryPruneQueue) head(ctx context.Context, height uint64) (deadlineQueueHead, bool, error) {
	r := new(collections.Range[collections.Pair[uint64, uint64]])
	if q.last != nil {
		r = r.StartExclusive(*q.last)
	}
	iter, err := q.k.ParamsHistoryPruneIndex.Iterate(ctx, r)
	if err != nil {
		return deadlineQueueHead{}, false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return deadlineQueueHead{}, false, nil
	}
	key, err := iter.Key()
	if err != nil || key.K1() > height {
		return deadlineQueueHead{}, false, err
	}
	q.row = key
	q.last = &key
	return deadlineQueueHead{deadline: key.K1(), primaryID: shared.Uint64BE(key.K2())}, true, nil
}

func (q *paramsHistoryPruneQueue) run(ctx context.Context, height, limit, maxBytes uint64) (deadlineSweepUsage, error) {
	var usage deadlineSweepUsage
	if limit == 0 {
		return usage, nil
	}
	row, err := q.k.ParamsHistory.Get(ctx, q.row.K2())
	missing := errors.Is(err, collections.ErrNotFound)
	if err != nil && !missing {
		return usage, err
	}
	estimate := evidenceCleanupConservativeBytesV1 + uint64(row.Size())
	if estimate > maxBytes {
		return usage, nil
	}
	usage.visited, usage.serializedBytes = 1, estimate
	cacheCtx, write := sdk.UnwrapSDKContext(ctx).CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	if !missing {
		floor, err := q.k.paramsHistoryPruneFloor(cache, row.SupersededHeight)
		if err != nil {
			return usage, err
		}
		if row.ParamsVersion != q.row.K2() || q.row.K1() != floor || height < floor {
			return usage, fmt.Errorf("parameter history prune index disagrees with its primary")
		}
		current, err := q.k.currentParamsVersion(cache)
		if err != nil {
			return usage, err
		}
		if current == row.ParamsVersion {
			return usage, fmt.Errorf("current parameter body cannot be pruned")
		}
		count, err := q.k.paramsVersionRefCount(cache, row.ParamsVersion)
		if err != nil {
			return usage, err
		}
		if count == 0 {
			if err = q.k.ParamsHistory.Remove(cache, row.ParamsVersion); err != nil {
				return usage, err
			}
			if err = q.k.ParamsHistoryByHeightIndex.Remove(cache, collections.Join(row.UpdatedHeight, row.ParamsVersion)); err != nil {
				return usage, err
			}
		}
	}
	if err = q.k.ParamsHistoryPruneIndex.Remove(cache, q.row); err != nil {
		return usage, err
	}
	write()
	return usage, nil
}
