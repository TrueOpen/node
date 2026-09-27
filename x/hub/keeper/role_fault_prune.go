package keeper

import (
	"bytes"
	"context"
	"errors"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (k Keeper) scheduleRoleFaultPrune(ctx context.Context, state types.RoleFaultState) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	retention := params.Service.RecordRetentionBlocks
	pruneHeight, err := checkedAdd(state.RecordedHeight, retention)
	if retention == 0 || err != nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, "role fault prune height overflow or zero retention")
	}
	faultID := shared.Hash32Key(state.FaultId)
	return k.RoleFaultPruneIndex.Set(ctx, types.NewRoleFaultPruneKey(pruneHeight, faultID))
}

// ProcessRoleFaultPrunes visits at most limit due rows. Stale and malformed
// rows consume one visit exactly like live rows; a non-terminal task is moved
// strictly forward so an expired prefix is never rescanned for free.
func (k Keeper) ProcessRoleFaultPrunes(ctx context.Context, currentHeight, limit uint64) (uint64, error) {
	if limit == 0 {
		return 0, nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, err
	}
	if localCap := uint64(params.Service.MaxRoleFaultPruneItemsPerBlock); limit > localCap {
		limit = localCap
	}
	visited := uint64(0)
	for visited < limit {
		iter, err := k.RoleFaultPruneIndex.Iterate(ctx, nil)
		if err != nil {
			return visited, err
		}
		if !iter.Valid() {
			iter.Close()
			break
		}
		key, err := iter.Key()
		iter.Close()
		if err != nil {
			return visited, err
		}
		if key.K1() > currentHeight {
			break
		}

		// Charge before decoding or validating the row, including poison.
		visited++
		sdkCtx := sdk.UnwrapSDKContext(ctx)
		cacheCtx, write := sdkCtx.CacheContext()
		if err := k.processRoleFaultPrune(sdk.WrapSDKContext(cacheCtx), key, currentHeight, params.Service.RecordRetentionBlocks); err != nil {
			return visited, err
		}
		write()
	}
	return visited, nil
}

func (k Keeper) processRoleFaultPrune(ctx context.Context, key types.RoleFaultPruneKey, currentHeight, retention uint64) error {
	faultID := key.K2()
	state, err := k.ReadRoleFaultValue(ctx, faultID)
	if errors.Is(err, collections.ErrNotFound) {
		return k.RoleFaultPruneIndex.Remove(ctx, key)
	}
	if err != nil {
		return err
	}
	if err := state.Validate(); err != nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, "invalid role fault prune primary: "+err.Error())
	}
	if !bytes.Equal(state.FaultId, faultID) {
		return errorsmod.Wrap(types.ErrInvariantBroken, "role fault prune key does not match primary")
	}
	earliest, err := checkedAdd(state.RecordedHeight, retention)
	if retention == 0 || err != nil || key.K1() < earliest {
		return errorsmod.Wrap(types.ErrInvariantBroken, "role fault prune schedule precedes frozen retention")
	}
	if k.roleFaultConsumers == nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, "role fault consumer gate is unavailable")
	}
	closed, err := k.roleFaultConsumers.RoleFaultConsumersClosed(ctx, state.TaskId, state.EvidenceDigest)
	if err != nil {
		return err
	}
	if !closed {
		nextHeight, err := checkedAdd(currentHeight, 1)
		if err != nil {
			return errorsmod.Wrap(types.ErrInvariantBroken, "role fault prune retry height overflow")
		}
		if err := k.RoleFaultPruneIndex.Remove(ctx, key); err != nil {
			return err
		}
		return k.RoleFaultPruneIndex.Set(ctx, types.NewRoleFaultPruneKey(nextHeight, faultID))
	}
	if len(state.GetSlashSummaryId()) != 0 {
		key := types.NewSlashSummaryKey(types.SlashSourceKind_SLASH_SOURCE_KIND_ROLE_FAULT, faultID, 0)
		if err := k.SlashSummary.Remove(ctx, key); err != nil {
			if errors.Is(err, collections.ErrNotFound) {
				return errorsmod.Wrap(types.ErrInvariantBroken, "role fault slash summary is missing at prune")
			}
			return err
		}
	}
	if err := k.RoleFault.Remove(ctx, faultID); err != nil {
		return err
	}
	// §6.6 licenses this deletion only because the task's fault vector is already
	// committed in TaskFailureClassState.fault_summary_hash and copied into the
	// retained terminal summary; nothing downstream may recompute the vector from
	// the Store after this point.
	if err := k.RoleFaultByTaskIndex.Remove(ctx, types.NewRoleFaultByTaskKey(state.TaskId, faultID)); err != nil {
		return err
	}
	return k.RoleFaultPruneIndex.Remove(ctx, key)
}
