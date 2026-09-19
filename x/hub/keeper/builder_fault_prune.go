package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
)

// ProcessBuilderFaultPrunes retires terminal objective-fault receipts under a
// visited-row budget. Jail/slash consequences remain on BuilderState/BuilderBond;
// the retained receipt is audit material, not an authority after acceptance.
func (k Keeper) ProcessBuilderFaultPrunes(ctx context.Context, currentHeight, visitedLimit uint64) (uint64, error) {
	if visitedLimit == 0 {
		return 0, nil
	}
	iter, err := k.BuilderFaultPruneIndex.Iterate(ctx, nil)
	if err != nil {
		return 0, err
	}
	keys := make([]types.BuilderFaultPruneKeyTriple, 0, visitedLimit)
	for ; iter.Valid() && uint64(len(keys)) < visitedLimit; iter.Next() {
		key, err := iter.Key()
		if err != nil {
			iter.Close()
			return 0, err
		}
		if key.K1() > currentHeight {
			break
		}
		keys = append(keys, key)
	}
	if err := iter.Close(); err != nil {
		return 0, err
	}

	for index, key := range keys {
		sdkCtx := sdk.UnwrapSDKContext(ctx)
		cacheCtx, commit := sdkCtx.CacheContext()
		cache := sdk.WrapSDKContext(cacheCtx)
		primaryKey := types.NewBuilderFaultKey(key.K2(), key.K3())
		fault, err := k.BuilderFault.Get(cache, primaryKey)
		if errors.Is(err, collections.ErrNotFound) {
			if err := k.BuilderFaultPruneIndex.Remove(cache, key); err != nil {
				return uint64(index + 1), err
			}
			commit()
			continue
		}
		if err != nil {
			return uint64(index + 1), err
		}
		if err := fault.Validate(); err != nil || fault.PruneHeight != key.K1() ||
			fault.BuilderAddress != key.K2() || !bytes.Equal(fault.FaultId, key.K3()) {
			return uint64(index + 1), fmt.Errorf("builder fault prune index does not match its primary")
		}
		if err := k.BuilderFault.Remove(cache, primaryKey); err != nil {
			return uint64(index + 1), err
		}
		if err := k.BuilderFaultPruneIndex.Remove(cache, key); err != nil {
			return uint64(index + 1), err
		}
		commit()
	}
	return uint64(len(keys)), nil
}

func (k Keeper) EnsureBuilderFaultPruneInvariant(ctx context.Context) error {
	faults, err := k.BuilderFault.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; faults.Valid(); faults.Next() {
		entry, err := faults.KeyValue()
		if err != nil {
			faults.Close()
			return err
		}
		faultID := entry.Value.FaultId
		if err := entry.Value.Validate(); err != nil || entry.Key.K1() != entry.Value.BuilderAddress || !bytes.Equal(entry.Key.K2(), faultID) {
			faults.Close()
			return fmt.Errorf("builder fault primary is invalid")
		}
		has, err := k.BuilderFaultPruneIndex.Has(ctx, types.NewBuilderFaultPruneKey(entry.Value.PruneHeight, entry.Value.BuilderAddress, faultID))
		if err != nil || !has {
			faults.Close()
			return fmt.Errorf("builder fault primary is missing its prune index")
		}
	}
	if err := faults.Close(); err != nil {
		return err
	}

	indexes, err := k.BuilderFaultPruneIndex.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer indexes.Close()
	for ; indexes.Valid(); indexes.Next() {
		key, err := indexes.Key()
		if err != nil {
			return err
		}
		fault, err := k.BuilderFault.Get(ctx, types.NewBuilderFaultKey(key.K2(), key.K3()))
		if err != nil || fault.PruneHeight != key.K1() || fault.BuilderAddress != key.K2() ||
			!bytes.Equal(fault.FaultId, key.K3()) {
			return fmt.Errorf("builder fault prune index is orphaned or mismatched")
		}
	}
	return nil
}
