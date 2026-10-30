package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// ProcessVrfKeyHistoryPrunes removes retired VRF keys after their configured
// audit window. The index is epoch-led and charged to the shared EndBlock
// visited/bytes budget, so a backlog continues deterministically next block.
func (k Keeper) ProcessVrfKeyHistoryPrunes(ctx context.Context, currentEpoch, visitedLimit, bytesLimit uint64) (uint64, uint64, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, 0, err
	}
	budget := newEndblockBudget(visitedLimit, bytesLimit)
	keys, err := dueVrfKeyPruneKeys(ctx, k.VrfKeyPruneIndex, currentEpoch, visitedLimit)
	if err != nil {
		return 0, 0, err
	}
	for _, key := range keys {
		if budget.exhausted() {
			break
		}
		sdkCtx := sdk.UnwrapSDKContext(ctx)
		cacheCtx, write := sdkCtx.CacheContext()
		cache := sdk.WrapSDKContext(cacheCtx)
		historyKey := types.NewVrfKeyHistoryKey(shared.ParticipantType(key.K2()), key.K3(), key.K4())
		history, err := k.GetVrfKeyHistory(cache, historyKey)
		if err != nil {
			if errors.Is(err, collections.ErrNotFound) {
				return budget.resultWithError(errorsmod.Wrap(types.ErrInvariantBroken, "VRF key prune index references missing history"))
			}
			return budget.resultWithError(err)
		}
		expectedPruneEpoch, err := checkedAdd(history.RetiredAtEpoch, uint64(params.Beacon.MaxVrfKeyHistoryEpochs))
		if err != nil {
			return budget.resultWithError(err)
		}
		if int32(history.ParticipantType) != key.K2() || history.OperatorAddress != key.K3() || history.EffectiveFromEpoch != key.K4() || expectedPruneEpoch != key.K1() {
			return budget.resultWithError(errorsmod.Wrap(types.ErrInvariantBroken, "VRF key prune index does not match retained history"))
		}
		if err := k.VrfKeyHistory.Remove(cache, historyKey); err != nil {
			return budget.resultWithError(err)
		}
		if err := k.VrfKeyPruneIndex.Remove(cache, key); err != nil {
			return budget.resultWithError(err)
		}
		write()
		budget.charge(history.Size())
	}
	visited, consumed := budget.result()
	return visited, consumed, nil
}

func dueVrfKeyPruneKeys(ctx context.Context, index collections.KeySet[types.VrfKeyPruneKeyQuad], currentEpoch, limit uint64) ([]types.VrfKeyPruneKeyQuad, error) {
	iter, err := index.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	keys := make([]types.VrfKeyPruneKeyQuad, 0, limit)
	for ; iter.Valid() && uint64(len(keys)) < limit; iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		if key.K1() > currentEpoch {
			break
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func (b *endblockBudget) resultWithError(err error) (uint64, uint64, error) {
	visited, consumed := b.result()
	return visited, consumed, err
}
