package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

type dispatchDeadlineAction uint8

const (
	dispatchClaimMaturity dispatchDeadlineAction = iota
	dispatchChallengeMaturity
	dispatchRecordPruning
	dispatchClaimPruning
	dispatchChallengePruning
	dispatchWinnerPruning
	dispatchTakeoverPruning
)

type dispatchDeadlineQueue struct {
	k      Keeper
	action dispatchDeadlineAction
	set    collections.KeySet[types.DeadlineIndexKey]
	last   *types.DeadlineIndexKey
	row    types.DeadlineIndexKey
}

func (k Keeper) newDispatchDeadlineQueue(action dispatchDeadlineAction) *dispatchDeadlineQueue {
	q := &dispatchDeadlineQueue{k: k, action: action}
	switch action {
	case dispatchClaimMaturity:
		q.set = k.DispatchClaimRebuttal
	case dispatchChallengeMaturity:
		q.set = k.DispatchChallengeResponse
	case dispatchRecordPruning:
		q.set = k.DispatchRecordPrune
	case dispatchClaimPruning:
		q.set = k.DispatchClaimPrune
	case dispatchChallengePruning:
		q.set = k.DispatchChallengePrune
	case dispatchWinnerPruning:
		q.set = k.DispatchWinnerIneligiblePrune
	case dispatchTakeoverPruning:
		q.set = k.TakeoverEntryReceiptPrune
	}
	return q
}

func nextDueHeightIndex(ctx context.Context, set collections.KeySet[types.DeadlineIndexKey], after *types.DeadlineIndexKey, height uint64) (types.DeadlineIndexKey, bool, error) {
	rangeKey := new(collections.Range[types.DeadlineIndexKey])
	if after != nil {
		rangeKey = rangeKey.StartExclusive(*after)
	}
	iter, err := set.Iterate(ctx, rangeKey)
	if err != nil {
		return types.DeadlineIndexKey{}, false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return types.DeadlineIndexKey{}, false, nil
	}
	key, err := iter.Key()
	return key, err == nil && key.K1() <= height, err
}

func (q *dispatchDeadlineQueue) head(ctx context.Context, height uint64) (deadlineQueueHead, bool, error) {
	key, found, err := nextDueHeightIndex(ctx, q.set, q.last, height)
	if err != nil || !found {
		return deadlineQueueHead{}, false, err
	}
	q.last, q.row = &key, key
	return deadlineQueueHead{deadline: key.K1(), primaryID: bytes.Clone(key.K2())}, true, nil
}

func (q *dispatchDeadlineQueue) run(ctx context.Context, height, limit, maxBytes uint64) (deadlineSweepUsage, error) {
	var usage deadlineSweepUsage
	if limit == 0 {
		return usage, nil
	}
	estimate := taskDeadlineConservativeBytesV1
	if q.action == dispatchRecordPruning {
		row, err := q.k.DispatchRecord.Get(ctx, q.row.K2())
		if err != nil {
			return usage, err
		}
		var overflow bool
		estimate, overflow = checkedAddUint64(estimate, uint64(row.Size()))
		if overflow {
			return usage, fmt.Errorf("dispatch cleanup byte bound overflows")
		}
	}
	if estimate > maxBytes {
		return usage, nil
	}
	cacheCtx, commit := sdk.UnwrapSDKContext(ctx).CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	var err error
	switch q.action {
	case dispatchClaimMaturity:
		err = q.k.matureDispatchClaim(cache, q.row.K2(), q.row.K1(), height)
	case dispatchChallengeMaturity:
		err = q.k.matureDispatchChallenge(cache, q.row.K2(), q.row.K1(), height)
	default:
		err = q.prune(cache, height)
	}
	usage.visited, usage.serializedBytes = 1, estimate
	if err == nil {
		commit()
	}
	return usage, err
}

func (q *dispatchDeadlineQueue) prune(ctx context.Context, height uint64) error {
	id := q.row.K2()
	params, err := q.k.Params.Get(ctx)
	if err != nil {
		return err
	}
	var base, floor uint64
	var remove func() error
	switch q.action {
	case dispatchRecordPruning:
		row, err := q.k.readDispatchRecord(ctx, id)
		if err != nil {
			return err
		}
		pending, err := q.k.pendingDispatchCount(ctx, id)
		if err != nil {
			return err
		}
		core, err := q.k.TaskCore.Has(ctx, id)
		if err != nil {
			return err
		}
		if pending != 0 || core {
			return nil
		}
		base = row.RecordedHeight
		var overflow bool
		floor, overflow = checkedHeightAdd(row.ClaimWindowCloseHeight, 1)
		if overflow {
			return fmt.Errorf("record retention floor overflows")
		}
		remove = func() error {
			if err := q.k.DispatchRecordByHash.Remove(ctx, row.DispatchRecordHash); err != nil {
				return err
			}
			return q.k.DispatchRecord.Remove(ctx, id)
		}
	case dispatchClaimPruning:
		row, err := q.k.readDispatchClaim(ctx, id)
		if err != nil {
			return err
		}
		if row.Status == types.DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_PENDING_REBUTTAL {
			return fmt.Errorf("pending claim has a prune index")
		}
		base = row.GetResolvedHeight()
		floor = row.ClaimWindowCloseHeight
		remove = func() error { return q.k.DispatchCompensationClaim.Remove(ctx, id) }
	case dispatchChallengePruning:
		row, err := q.k.readDispatchChallenge(ctx, id)
		if err != nil {
			return err
		}
		if dispatchChallengePending(row.Status) {
			return fmt.Errorf("open challenge has a prune index")
		}
		base = row.GetResolvedHeight()
		remove = func() error {
			if row.Status == types.DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_UPHELD {
				scope, err := q.k.dispatchUpheldScopeKey(row)
				if err != nil {
					return err
				}
				current, err := q.k.DispatchUpheldScope.Get(ctx, scope)
				if err != nil || !bytes.Equal(current, id) {
					return fmt.Errorf("upheld scope index/primary mismatch")
				}
				if err := q.k.DispatchUpheldScope.Remove(ctx, scope); err != nil {
					return err
				}
			}
			return q.k.DispatchChallenge.Remove(ctx, id)
		}
	case dispatchWinnerPruning:
		row, err := q.k.DispatchWinnerIneligible.Get(ctx, id)
		if err != nil {
			return err
		}
		base = row.RejectedHeight
		floor = row.ClaimWindowCloseHeight
		remove = func() error { return q.k.DispatchWinnerIneligible.Remove(ctx, id) }
	case dispatchTakeoverPruning:
		row, err := q.k.TakeoverEntryReceipt.Get(ctx, id)
		if err != nil {
			return err
		}
		base = max(row.ActivationHeight, row.OrderExpireHeight)
		remove = func() error { return q.k.TakeoverEntryReceipt.Remove(ctx, id) }
	default:
		return fmt.Errorf("unknown dispatch prune action")
	}
	minimum, overflow := shared.CheckedHeightAddV1(base, params.Dispatch.DispatchRecordRetentionBlocks)
	if overflow {
		return fmt.Errorf("dispatch prune boundary overflows")
	}
	minimum = max(minimum, floor)
	if height < minimum {
		if err := q.set.Remove(ctx, q.row); err != nil {
			return err
		}
		return q.set.Set(ctx, collections.Join(minimum, id))
	}
	if err := remove(); err != nil && !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	return q.set.Remove(ctx, q.row)
}
