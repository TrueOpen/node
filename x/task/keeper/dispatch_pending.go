package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (k Keeper) pendingDispatchCount(ctx context.Context, taskID []byte) (uint64, error) {
	count, err := k.DispatchPendingByTask.Get(ctx, taskID)
	if errors.Is(err, collections.ErrNotFound) {
		return 0, nil
	}
	return count, err
}

func (k Keeper) adjustDispatchPendingCount(ctx context.Context, taskID []byte, wasPending, isPending bool) error {
	if wasPending == isPending {
		return nil
	}
	count, err := k.pendingDispatchCount(ctx, taskID)
	if err != nil {
		return err
	}
	if isPending {
		next, overflow := shared.CheckedAddUint64(count, 1)
		if overflow {
			return fmt.Errorf("pending dispatch count overflows")
		}
		return k.DispatchPendingByTask.Set(ctx, taskID, next)
	}
	if count == 0 {
		return fmt.Errorf("pending dispatch count underflows")
	}
	if count == 1 {
		return k.DispatchPendingByTask.Remove(ctx, taskID)
	}
	return k.DispatchPendingByTask.Set(ctx, taskID, count-1)
}
