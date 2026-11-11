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

// InitializeAnchorParams writes the ADR-0040 anchor parameter defaults into the
// current params at the anchor audit upgrade. A params body that already has
// anchor windows is left alone; the previous version is archived.
func (k Keeper) InitializeAnchorParams(ctx context.Context) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	if params.Verification.AnchorOpenWindowBlocks != 0 {
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
	// A chain upgrading straight past v6 runs 3->4 and 4->5 in one RunMigrations:
	// the body must already validate under the v7 rules here (4->5 is then a no-op).
	params, err = k.repairedAnchorParams(ctx, params)
	if err != nil {
		return err
	}
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
	if err := k.archiveParamsForUpdate(ctx, meta.ParamsVersion, current); err != nil {
		return err
	}
	if err := k.Params.Set(ctx, params); err != nil {
		return err
	}
	if err := k.ParamsMeta.Set(ctx, types.TaskParamsMetaState{ParamsVersion: version, ParamsHash: hash, UpdatedHeight: height}); err != nil {
		return err
	}
	return emitTypedEvent(ctx, &types.EventTaskParamsUpdated{OldVersion: meta.ParamsVersion, NewVersion: version, ParamsHash: hash})
}

// InitializeAnchorContentParams writes the ADR-0040 v7 content-track parameter
// defaults into the current params at the content verdict upgrade. A params
// body that already samples a positive number of positions is left alone; the
// previous version is archived. Tasks keep the params version they were created
// under, so audits already in flight keep their v6 shape (no content track).
func (k Keeper) InitializeAnchorContentParams(ctx context.Context) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	if params.Verification.AnchorPositionsPerTask != 0 {
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
	params, err = k.repairedAnchorParams(ctx, params)
	if err != nil {
		return err
	}
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
	if err := k.archiveParamsForUpdate(ctx, meta.ParamsVersion, current); err != nil {
		return err
	}
	if err := k.Params.Set(ctx, params); err != nil {
		return err
	}
	if err := k.ParamsMeta.Set(ctx, types.TaskParamsMetaState{ParamsVersion: version, ParamsHash: hash, UpdatedHeight: height}); err != nil {
		return err
	}
	return emitTypedEvent(ctx, &types.EventTaskParamsUpdated{OldVersion: meta.ParamsVersion, NewVersion: version, ParamsHash: hash})
}

// ensureAnchorParamsInitialized runs the anchor parameter upgrades for a chain
// that starts from a genesis written before they existed (audit F-P1-5,
// 2026-11-09): a pre-anchor body otherwise fails validation at InitChain, so a
// new binary could not cold-start from the testnet's genesis.
func (k Keeper) ensureAnchorParamsInitialized(ctx context.Context) error {
	if err := k.InitializeAnchorParams(ctx); err != nil {
		return err
	}
	return k.InitializeAnchorContentParams(ctx)
}

// repairedAnchorParams fills the anchor defaults and applies the automatic
// repairs (types.RepairAnchorParams), logging each one, and refuses with a
// pointer to the offline preview when the body still does not validate: the
// params commitment is only defined over valid bodies, so an unrepairable body
// must stop the upgrade before the height rather than halt the chain at it
// (audit F-P1-4, 2026-11-09).
func (k Keeper) repairedAnchorParams(ctx context.Context, params types.TaskParamsV1) (types.TaskParamsV1, error) {
	repaired, notes := types.RepairAnchorParams(params)
	logger := sdk.UnwrapSDKContext(ctx).Logger()
	for _, note := range notes {
		logger.Error("anchor params upgrade repaired the live params body; confirm it by governance", "repair", note)
	}
	if err := repaired.Validate(); err != nil {
		return params, errorsmod.Wrap(err, "anchor params upgrade: the live params body does not validate under the new rules and cannot be repaired automatically; run types.RepairAnchorParams on it offline before the upgrade height")
	}
	return repaired, nil
}
