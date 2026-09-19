package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
)

func (k Keeper) replaceServiceBondEffectiveIndex(ctx context.Context, previous *types.ServiceBondState, next types.ServiceBondState) error {
	if previous != nil && previous.EffectiveBondEpoch != 0 {
		if err := k.ServiceBondEffectiveIndex.Remove(ctx, types.NewServiceBondEffectiveKey(previous.EffectiveBondEpoch, previous.OperatorAddress)); err != nil &&
			!errors.Is(err, collections.ErrNotFound) {
			return err
		}
	}
	if next.EffectiveBondEpoch == 0 || next.EffectiveActiveBond == next.ActiveBond {
		return nil
	}
	return k.ServiceBondEffectiveIndex.Set(ctx, types.NewServiceBondEffectiveKey(next.EffectiveBondEpoch, next.OperatorAddress))
}

// ProcessServiceBondEffectiveActivations makes delayed stake effective and
// re-evaluates candidate-slot ownership without scanning all service bonds.
func (k Keeper) ProcessServiceBondEffectiveActivations(ctx context.Context, currentHeight, visitedLimit uint64) (uint64, error) {
	if visitedLimit == 0 {
		return 0, nil
	}
	epochLength, err := k.epochLengthBlocks(ctx)
	if err != nil {
		return 0, err
	}
	currentEpoch := epochForHeight(currentHeight, epochLength)
	iter, err := k.ServiceBondEffectiveIndex.Iterate(ctx, nil)
	if err != nil {
		return 0, err
	}
	keys := make([]types.ServiceBondEffectiveKeyPair, 0, visitedLimit)
	for ; iter.Valid() && uint64(len(keys)) < visitedLimit; iter.Next() {
		key, err := iter.Key()
		if err != nil {
			iter.Close()
			return 0, err
		}
		if key.K1() > currentEpoch {
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
		bond, err := k.ServiceBond.Get(cache, types.NewServiceBondKey(key.K2()))
		if errors.Is(err, collections.ErrNotFound) {
			if err := k.ServiceBondEffectiveIndex.Remove(cache, key); err != nil {
				return uint64(index + 1), err
			}
			commit()
			continue
		}
		if err != nil {
			return uint64(index + 1), err
		}
		if bond.OperatorAddress != key.K2() {
			return uint64(index + 1), fmt.Errorf("service bond effective index does not match its primary")
		}
		if bond.EffectiveBondEpoch != key.K1() {
			if err := k.ServiceBondEffectiveIndex.Remove(cache, key); err != nil {
				return uint64(index + 1), err
			}
			commit()
			continue
		}
		types.NormalizeEffectiveActiveBond(&bond, currentEpoch)
		if err := bond.Validate(); err != nil {
			return uint64(index + 1), err
		}
		if err := k.ServiceBond.Set(cache, types.NewServiceBondKey(bond.OperatorAddress), bond); err != nil {
			return uint64(index + 1), err
		}
		if err := k.reconcileSupportsAfterBondChange(
			cache, bond.OperatorAddress, bond.ActiveBond, currentHeight, types.ModelSupportDeactivateBondBelowMin,
		); err != nil {
			return uint64(index + 1), err
		}
		if err := k.syncCandidateSlotMembership(cache, bond.OperatorAddress, currentHeight); err != nil {
			return uint64(index + 1), err
		}
		if err := k.ServiceBondEffectiveIndex.Remove(cache, key); err != nil {
			return uint64(index + 1), err
		}
		commit()
	}
	return uint64(len(keys)), nil
}

func (k Keeper) EnsureServiceBondEffectiveIndexInvariant(ctx context.Context) error {
	bonds, err := k.ServiceBond.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; bonds.Valid(); bonds.Next() {
		entry, err := bonds.KeyValue()
		if err != nil {
			bonds.Close()
			return err
		}
		if entry.Key != entry.Value.OperatorAddress {
			bonds.Close()
			return fmt.Errorf("service bond effective primary identity mismatch")
		}
		if entry.Value.EffectiveActiveBond != entry.Value.ActiveBond {
			has, err := k.ServiceBondEffectiveIndex.Has(ctx, types.NewServiceBondEffectiveKey(entry.Value.EffectiveBondEpoch, entry.Key))
			if err != nil || !has {
				bonds.Close()
				return fmt.Errorf("service bond %s is missing its effective-epoch index", entry.Key)
			}
		}
	}
	if err := bonds.Close(); err != nil {
		return err
	}

	indexes, err := k.ServiceBondEffectiveIndex.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer indexes.Close()
	for ; indexes.Valid(); indexes.Next() {
		key, err := indexes.Key()
		if err != nil {
			return err
		}
		bond, err := k.ServiceBond.Get(ctx, types.NewServiceBondKey(key.K2()))
		if err != nil || bond.OperatorAddress != key.K2() || bond.EffectiveBondEpoch != key.K1() {
			return fmt.Errorf("service bond effective index is orphaned or mismatched")
		}
	}
	return nil
}
