package keeper

import (
	"context"
	"math"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/task/types"
)

// InitializeAnchorParams writes the ADR-0040 anchor parameters into the current
// params at the anchor audit upgrade, as a new params version: the default slash
// shares, audit floor and risk margin, and the recommended windows
// (types.WithAnchorDefaults) - not the historical Default*Window values, which
// are too short for an honest Worker under load. The windows are block counts
// sized for types.AnchorWindowBlockTime; a network with another block time must
// replace them by governance before the first anchor Profile is registered. A
// params body that already has anchor windows is left alone; the previous
// version is archived.
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
	params, err = k.repairedAnchorParams(ctx, params)
	if err != nil {
		return err
	}
	version := meta.ParamsVersion + 1
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	sdkCtx.Logger().Info("anchor params initialized; the windows are block counts for the assumed block time, replace them by governance if the network's block time differs",
		"assumed_block_time", types.AnchorWindowBlockTime.String(),
		"anchor_open_window_blocks", params.Verification.AnchorOpenWindowBlocks,
		"anchor_proof_window_blocks", params.Verification.AnchorProofWindowBlocks,
		"anchor_verdict_window_blocks", params.Verification.AnchorVerdictWindowBlocks)
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

// ensureAnchorParamsInitialized runs the anchor parameter upgrade for a chain
// that starts from a genesis written before it existed (audit F-P1-5,
// 2026-11-09): a pre-anchor body otherwise fails validation at InitChain, so a
// new binary could not cold-start from the testnet's genesis.
func (k Keeper) ensureAnchorParamsInitialized(ctx context.Context) error {
	return k.InitializeAnchorParams(ctx)
}

// repairedAnchorParams fills the anchor parameters and applies the automatic
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
