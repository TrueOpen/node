package keeper

import (
	"context"
	"fmt"
	"math"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// InitializeSessionKeyParams fills the legacy current limit at the configured
// activation boundary. A positive value is never overwritten.
// Versioning retains the pre-activation params for existing Task/Session refs.
func (k Keeper) InitializeSessionKeyParams(ctx context.Context) error {
	if k.sessionKeyActivationHeight == nil || *k.sessionKeyActivationHeight == 0 || sdk.UnwrapSDKContext(ctx).BlockHeight() < int64(*k.sessionKeyActivationHeight) {
		return nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	if params.Session.MaxSessionKeyBlocks != 0 {
		return nil
	}
	meta, err := k.GetTaskParamsMeta(ctx)
	if err != nil {
		return err
	}
	if meta.ParamsVersion == math.MaxUint64 {
		return errorsmod.Wrap(types.ErrInvariantBroken, "params_version overflow")
	}
	current := params
	params.Session.MaxSessionKeyBlocks = types.DefaultMaxSessionKeyBlocks
	version := meta.ParamsVersion + 1
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	hash, err := types.TaskParamsHashV1(sdkCtx.ChainID(), version, params)
	if err != nil {
		return err
	}
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return err
	}
	cacheCtx, commit := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	if err := k.archiveParamsForUpdate(cache, meta.ParamsVersion, current); err != nil {
		return err
	}
	if err := k.Params.Set(cache, params); err != nil {
		return err
	}
	if err := k.ParamsMeta.Set(cache, types.TaskParamsMetaState{ParamsVersion: version, ParamsHash: hash, UpdatedHeight: height}); err != nil {
		return err
	}
	if err := emitTypedEvent(cache, &types.EventTaskParamsUpdated{OldVersion: meta.ParamsVersion, NewVersion: version, ParamsHash: hash}); err != nil {
		return err
	}
	commit()
	return nil
}

// ConfigureSessionKeyActivation selects the first new-rules block. All nodes
// must use the same deployment value, including when replaying old blocks.
// Zero leaves legacy chains inactive; fresh positive genesis needs no bootstrap.
func (k Keeper) ConfigureSessionKeyActivation(firstHeight uint64) error {
	if !shared.IsBlockHeightV1(firstHeight) {
		return fmt.Errorf("session key activation height exceeds int64")
	}
	if k.sessionKeyActivationHeight == nil {
		return fmt.Errorf("session key activation configuration unavailable")
	}
	*k.sessionKeyActivationHeight = firstHeight
	return nil
}

// ValidateGenesis permits an old schema only with an explicit replay plan.
// InitGenesis additionally enforces the plan against the import height.
func (k Keeper) ValidateGenesis(genesis types.GenesisState) error {
	if genesis.Params.Session.MaxSessionKeyBlocks == 0 && k.sessionKeyActivationHeight != nil && *k.sessionKeyActivationHeight > 0 {
		for _, stream := range genesis.Streams {
			if stream.XSessionKey != nil || stream.SessionKeySpent.AtomicUnits != "" && stream.SessionKeySpent.AtomicUnits != "0" {
				return fmt.Errorf("legacy genesis contains session authorization")
			}
		}
		return genesis.ValidateLegacyGenesis()
	}
	return genesis.Validate()
}
