package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func normalizedEpochLengthBlocks(params types.HubParamsV2) uint64 {
	if params.Epoch.EpochLengthBlocks == 0 {
		return types.DefaultEpochLengthBlocks
	}
	return params.Epoch.EpochLengthBlocks
}

func (k Keeper) epochLengthBlocks(ctx context.Context) (uint64, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.DefaultEpochLengthBlocks, nil
		}
		return 0, err
	}
	return normalizedEpochLengthBlocks(params), nil
}

func epochForHeight(height, epochLength uint64) uint64 {
	return shared.EpochForHeight(height, epochLength)
}

func epochHeightRange(epoch, epochLength uint64) (uint64, uint64, error) {
	return shared.EpochHeightRange(epoch, epochLength)
}
