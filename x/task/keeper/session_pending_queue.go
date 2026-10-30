package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// sessionPendingQueue keeps a block-local scan position. A pending CLOSE may
// remain at the head for many blocks; it must not starve another session's row.
type sessionPendingQueue struct {
	k            Keeper
	action       sessionPendingAction
	set          collections.KeySet[types.SessionHeightIndexKey]
	lastPair     *types.SessionHeightIndexKey
	lastCancel   *types.OrderCancelIndexKeyTriple
	row          sessionPendingRow
	lastAdvanced bool
}

func (k Keeper) newSessionPendingQueue(action sessionPendingAction) *sessionPendingQueue {
	queue := &sessionPendingQueue{k: k, action: action}
	switch action {
	case sessionPendingWithdraw:
		queue.set = k.SessionWithdrawIndex
	case sessionPendingKeyChange:
		queue.set = k.SessionKeyChangeIndex
	case sessionPendingTakeover:
		queue.set = k.SessionTakeoverIndex
	}
	return queue
}

func (q *sessionPendingQueue) head(ctx context.Context, height uint64) (deadlineQueueHead, bool, error) {
	if q.action == sessionPendingCancel {
		return q.cancelHead(ctx, height)
	}
	key, found, err := nextDueHeightIndex(ctx, q.set, q.lastPair, height)
	if err != nil || !found {
		return deadlineQueueHead{}, false, err
	}
	q.lastPair = &key
	q.row = sessionPendingRow{height: key.K1(), sessionID: key.K2()}
	return deadlineQueueHead{deadline: key.K1(), primaryID: key.K2()}, true, nil
}

func (q *sessionPendingQueue) cancelHead(ctx context.Context, height uint64) (deadlineQueueHead, bool, error) {
	keyRange := new(collections.Range[types.OrderCancelIndexKeyTriple])
	if q.lastCancel != nil {
		keyRange = new(collections.Range[types.OrderCancelIndexKeyTriple]).StartExclusive(*q.lastCancel)
	}
	iter, err := q.k.OrderCancelIndex.Iterate(ctx, keyRange)
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
	q.lastCancel = &key
	q.row = sessionPendingRow{height: key.K1(), sessionID: key.K2(), sequence: key.K3()}
	primary := append(append([]byte(nil), key.K2()...), shared.Uint64BE(key.K3())...)
	return deadlineQueueHead{deadline: key.K1(), primaryID: primary}, true, nil
}

func (q *sessionPendingQueue) run(ctx context.Context, height, limit, maxBytes uint64) (deadlineSweepUsage, error) {
	var usage deadlineSweepUsage
	if limit == 0 || maxBytes < sessionLifecycleConservativeBytesV1 {
		return usage, nil
	}
	if q.row.height == 0 || q.row.height > height {
		return usage, fmt.Errorf("pending session queue has no due head")
	}
	cacheCtx, commit := sdk.UnwrapSDKContext(ctx).CacheContext()
	advanced, err := q.k.applySessionPendingRow(sdk.WrapSDKContext(cacheCtx), q.action, q.row, height)
	if err != nil {
		return usage, err
	}
	commit()
	q.lastAdvanced = advanced
	usage.visited = 1
	usage.serializedBytes = sessionLifecycleConservativeBytesV1
	return usage, nil
}

func (k Keeper) sweepSessionWithdrawalByID(ctx context.Context, sessionID []byte, height uint64) (uint64, uint64, error) {
	stream, err := k.ReadStream(ctx, sessionID)
	if err != nil {
		return 0, 0, err
	}
	if stream.PendingWithdrawEffectiveHeight == 0 || stream.PendingWithdrawEffectiveHeight > height {
		return 0, 0, nil
	}
	advanced, err := k.applySessionPendingRow(ctx, sessionPendingWithdraw, sessionPendingRow{height: stream.PendingWithdrawEffectiveHeight, sessionID: sessionID}, height)
	if err != nil {
		return 0, 0, err
	}
	if advanced {
		return 1, 1, nil
	}
	return 1, 0, nil
}

func (k Keeper) sweepSessionWithdrawalBatch(ctx context.Context, height, limit uint64) (uint64, uint64, error) {
	queue := k.newSessionPendingQueue(sessionPendingWithdraw)
	var visited, advanced uint64
	for visited < limit {
		_, found, err := queue.head(ctx, height)
		if err != nil || !found {
			return visited, advanced, err
		}
		usage, err := queue.run(ctx, height, 1, sessionLifecycleConservativeBytesV1)
		if err != nil {
			return visited, advanced, err
		}
		visited += usage.visited
		if queue.lastAdvanced {
			advanced++
		}
	}
	return visited, advanced, nil
}
