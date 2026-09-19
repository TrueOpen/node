package keeper

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
)

func (k Keeper) setEndBlockBudget(ctx context.Context, height, remainingItems, remainingBytes uint64) error {
	cacheCtx, write := sdk.UnwrapSDKContext(ctx).CacheContext()
	wrapped := sdk.WrapSDKContext(cacheCtx)
	if err := k.EndBlockBudgetHeight.Set(wrapped, height); err != nil {
		return err
	}
	if err := k.EndBlockBudgetRemainingItems.Set(wrapped, remainingItems); err != nil {
		return err
	}
	if err := k.EndBlockBudgetRemainingBytes.Set(wrapped, remainingBytes); err != nil {
		return err
	}
	write()
	return nil
}

func (k Keeper) GetEndBlockBudget(ctx context.Context, height uint64) (types.EndBlockBudgetSnapshot, error) {
	storedHeight, err := k.EndBlockBudgetHeight.Get(ctx)
	if err != nil {
		return types.EndBlockBudgetSnapshot{}, fmt.Errorf("endblock budget is unavailable: %w", err)
	}
	if storedHeight != height {
		return types.EndBlockBudgetSnapshot{}, fmt.Errorf("endblock budget height %d does not match %d", storedHeight, height)
	}
	remainingItems, err := k.EndBlockBudgetRemainingItems.Get(ctx)
	if err != nil {
		return types.EndBlockBudgetSnapshot{}, fmt.Errorf("endblock item budget is unavailable: %w", err)
	}
	remainingBytes, err := k.EndBlockBudgetRemainingBytes.Get(ctx)
	if err != nil {
		return types.EndBlockBudgetSnapshot{}, fmt.Errorf("endblock byte budget is unavailable: %w", err)
	}
	return types.EndBlockBudgetSnapshot{Height: height, RemainingItems: remainingItems, RemainingBytes: remainingBytes}, nil
}

func (k Keeper) ConsumeEndBlockBudget(ctx context.Context, height, visitedItems, serializedBytes uint64) error {
	budget, err := k.GetEndBlockBudget(ctx, height)
	if err != nil {
		return err
	}
	if visitedItems > budget.RemainingItems {
		return fmt.Errorf("endblock item budget exceeded: consumed %d, remaining %d", visitedItems, budget.RemainingItems)
	}
	remainingBytes := uint64(0)
	if serializedBytes < budget.RemainingBytes {
		remainingBytes = budget.RemainingBytes - serializedBytes
	}
	return k.setEndBlockBudget(ctx, height, budget.RemainingItems-visitedItems, remainingBytes)
}
