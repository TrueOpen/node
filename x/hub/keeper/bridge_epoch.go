package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// ProcessBridgeEpochBoundary is the §6.6a ordering, executed once per block and
// idempotent within an epoch: activate the pending limit *first*, then open the
// new usage window, then retire windows past their retention.
//
// The order is the whole point. Opening the window first would let the first
// transfer of a new epoch be measured against the outgoing limit, which is
// exactly the drift the pending/effective split exists to prevent.
func (k Keeper) ProcessBridgeEpochBoundary(ctx context.Context, epoch uint64, limit uint64) (uint64, error) {
	if _, err := k.BridgeRoute.Get(ctx); err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			// No bridge on this chain: nothing to schedule and nothing to prune.
			return 0, nil
		}
		return 0, err
	}
	if err := k.ActivateDueBridgeLimit(ctx, epoch); err != nil {
		return 0, err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, err
	}
	if err := k.openBridgeUsageWindow(ctx, epoch); err != nil {
		return 0, err
	}
	if epoch > 0 {
		if err := k.CloseBridgeUsageEpoch(ctx, epoch-1, params.Bridge.BridgeUsageRetentionEpochs); err != nil {
			return 0, err
		}
	}
	budget := params.Bridge.MaxBridgeUsagePruneItemsPerBlock
	if budget == 0 {
		return 0, nil
	}
	if uint64(budget) > limit {
		budget = uint32(limit)
	}
	visited, err := k.PruneDueBridgeUsage(ctx, epoch, budget)
	return uint64(visited), err
}

// openBridgeUsageWindow materialises the current epoch's row so a query can tell
// "no transfers yet" from "no window". It never reopens a closed window.
func (k Keeper) openBridgeUsageWindow(ctx context.Context, epoch uint64) error {
	if _, err := k.BridgeEpochUsage.Get(ctx, epoch); err == nil {
		return nil
	} else if !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	return k.BridgeEpochUsage.Set(ctx, epoch, types.BridgeEpochUsageState{
		RewardEpoch: epoch, InboundUsed: shared.NewAmount(0), OutboundUsed: shared.NewAmount(0),
	})
}
