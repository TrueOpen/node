package keeper

import (
	"context"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

func (k Keeper) processVerifierWindowBuildIndex(ctx context.Context, currentHeight, maxItems uint64) (uint64, error) {
	usage, err := k.processVerifierWindowBuildIndexWithBudget(ctx, currentHeight, maxItems, maxDeadlineSweepBytesPerBlockV1)
	return usage.visited, err
}

func (k Keeper) processVerifierHandraiseCloseIndex(ctx context.Context, currentHeight, maxItems uint64) (uint64, error) {
	usage, err := k.processVerifierHandraiseCloseIndexWithBudget(ctx, currentHeight, maxItems, maxDeadlineSweepBytesPerBlockV1)
	return usage.visited, err
}

func (k Keeper) processVerifierSelectionRandomnessIndex(ctx context.Context, currentHeight, maxItems uint64) (uint64, error) {
	usage, err := k.processVerifierSelectionRandomnessIndexWithBudget(ctx, currentHeight, maxItems, maxDeadlineSweepBytesPerBlockV1)
	return usage.visited, err
}

func (k Keeper) runEndBlockDeadlineSweeps(
	ctx context.Context,
	currentHeight, maxPerBlock, maxSerializedBytes uint64,
) (EndBlockSweepResult, uint64, error) {
	globalBudget := uint64(k.hubKeeper.GetHubParams(sdk.UnwrapSDKContext(ctx)).MaxEndblockVisitedItemsTotal)
	return k.runEndBlockDeadlineSweepsWithBudget(ctx, currentHeight, maxPerBlock, maxSerializedBytes, globalBudget)
}
