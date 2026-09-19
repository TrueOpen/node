package keeper

import (
	"context"
	"math"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
)

// CloseExpiredFreezeSignals closes OPEN signals only after their deadline;
// votes at vote_deadline_height remain valid.
func (k Keeper) CloseExpiredFreezeSignals(ctx context.Context, currentHeight, maxItems uint64) (uint64, error) {
	if maxItems == 0 {
		return 0, nil
	}
	processed := uint64(0)
	for processed < maxItems {
		iter, err := k.FreezeSignalDeadlineIndex.Iterate(ctx, nil)
		if err != nil {
			return processed, err
		}
		if !iter.Valid() {
			iter.Close()
			break
		}
		key, err := iter.Key()
		iter.Close()
		if err != nil {
			return processed, err
		}
		if key.K1() >= currentHeight {
			break
		}
		sdkCtx := sdk.UnwrapSDKContext(ctx)
		cacheCtx, write := sdkCtx.CacheContext()
		cache := sdk.WrapSDKContext(cacheCtx)
		state, err := k.FreezeSignalState.Get(cache, key.K2())
		if err != nil {
			return processed, errorsmod.Wrap(types.ErrInvariantBroken, "freeze deadline index references missing signal")
		}
		if state.SignalStatus != types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_OPEN || state.VoteDeadlineHeight != key.K1() {
			return processed, errorsmod.Wrap(types.ErrInvariantBroken, "freeze deadline index disagrees with signal")
		}
		if err := k.closeFreezeSignal(cache, state, types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_EXPIRED, currentHeight); err != nil {
			return processed, err
		}
		mustEmitHubEvent(cache, &types.EventFreezeSignalExpired{
			FreezeSignalId: append([]byte(nil), state.FreezeSignalId...), ModelId: state.ModelId,
			ProfileVersion: state.ProfileVersion, VoteDeadlineHeight: state.VoteDeadlineHeight,
		})
		write()
		processed++
	}
	return processed, nil
}

func (k Keeper) closeFreezeSignal(ctx context.Context, state types.FreezeSignalState, status types.FreezeSignalStatus, closedHeight uint64) error {
	if state.SignalStatus != types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_OPEN ||
		(status != types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_ACCEPTED && status != types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_REJECTED && status != types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_EXPIRED) {
		return errorsmod.Wrap(types.ErrInvariantBroken, "invalid freeze signal terminal transition")
	}
	oldIndex := types.NewFreezeSignalByProfileKey(state.ModelId, state.ProfileVersion, state.SignalStatus, state.RiskWindowEndHeight, state.FreezeSignalId)
	if err := k.FreezeSignalByProfileIndex.Remove(ctx, oldIndex); err != nil {
		return err
	}
	state.SignalStatus = status
	state.ClosedHeight = closedHeight
	if err := k.FreezeSignalState.Set(ctx, state.FreezeSignalId, state); err != nil {
		return err
	}
	if err := k.FreezeSignalByProfileIndex.Set(ctx, types.NewFreezeSignalByProfileKey(state.ModelId, state.ProfileVersion, status, state.RiskWindowEndHeight, state.FreezeSignalId)); err != nil {
		return err
	}
	if err := removeFreezeSignalDeadlineIndexIfExists(ctx, k.FreezeSignalDeadlineIndex, types.NewFreezeSignalDeadlineKey(state.VoteDeadlineHeight, state.FreezeSignalId)); err != nil {
		return err
	}
	windowKey := types.NewFreezeSignalByWindowKey(state.ModelId, state.ProfileVersion, state.RiskWindowId)
	binding, err := k.FreezeSignalByWindow.Get(ctx, windowKey)
	if err != nil || binding.Phase != types.FreezeSignalWindowPhase_FREEZE_SIGNAL_WINDOW_PHASE_OPEN || !bytesEqual(binding.GetFreezeSignalId(), state.FreezeSignalId) {
		return errorsmod.Wrap(types.ErrInvariantBroken, "freeze window binding disagrees with closing signal")
	}
	binding.Phase = types.FreezeSignalWindowPhase_FREEZE_SIGNAL_WINDOW_PHASE_CLOSED
	if err := k.FreezeSignalByWindow.Set(ctx, windowKey, binding); err != nil {
		return err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	pruneHeight, overflow := checkedAddHubUint64(closedHeight, params.Freeze.FreezeSignalDetailRetentionBlocks)
	if overflow {
		return errorsmod.Wrap(types.ErrInvariantBroken, "freeze signal detail prune height overflow")
	}
	if err := k.FreezeSignalPruneIndex.Set(ctx, types.NewFreezeSignalPruneKey(pruneHeight, state.FreezeSignalId, types.FreezeSignalPrunePhase_FREEZE_SIGNAL_PRUNE_PHASE_VOTES)); err != nil {
		return err
	}
	profile, err := k.GetProfile(ctx, state.ModelId, state.ProfileVersion)
	if err != nil {
		return err
	}
	return k.scheduleNextFreezeRiskWindow(ctx, profile)
}

func (k Keeper) ProcessFreezeSignalPrunes(ctx context.Context, currentHeight, maxItems uint64) (uint64, error) {
	if maxItems == 0 {
		return 0, nil
	}
	visited := uint64(0)
	for visited < maxItems {
		iter, err := k.FreezeSignalPruneIndex.Iterate(ctx, nil)
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
		sdkCtx := sdk.UnwrapSDKContext(ctx)
		cacheCtx, write := sdkCtx.CacheContext()
		cache := sdk.WrapSDKContext(cacheCtx)
		var used uint64
		switch types.FreezeSignalPrunePhase(key.K3()) {
		case types.FreezeSignalPrunePhase_FREEZE_SIGNAL_PRUNE_PHASE_VOTES:
			used, err = k.pruneFreezeSignalVotes(cache, key, currentHeight, maxItems-visited)
		case types.FreezeSignalPrunePhase_FREEZE_SIGNAL_PRUNE_PHASE_HEADER:
			used, err = k.pruneFreezeSignalHeader(cache, key)
		default:
			err = errorsmod.Wrap(types.ErrInvariantBroken, "freeze prune index has invalid phase")
		}
		if err != nil {
			return visited, err
		}
		if used == 0 {
			return visited, errorsmod.Wrap(types.ErrInvariantBroken, "freeze prune made no progress")
		}
		write()
		visited += used
	}
	return visited, nil
}

func (k Keeper) pruneFreezeSignalVotes(ctx context.Context, pruneKey types.FreezeSignalPruneKey, currentHeight, limit uint64) (uint64, error) {
	signalID := pruneKey.K2()
	signal, err := k.FreezeSignalState.Get(ctx, signalID)
	if err != nil || signal.SignalStatus == types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_OPEN || signal.ClosedHeight == 0 {
		return 0, errorsmod.Wrap(types.ErrInvariantBroken, "freeze vote prune references a missing or open signal")
	}
	iter, err := k.EmergencyFreezeVoteState.Iterate(ctx, collections.NewPrefixedPairRange[[]byte, []byte](signalID))
	if err != nil {
		return 0, err
	}
	keys := make([]types.EmergencyFreezeVoteKey, 0, min(limit, uint64(math.MaxInt)))
	for ; iter.Valid() && uint64(len(keys)) < limit; iter.Next() {
		key, err := iter.Key()
		if err != nil {
			iter.Close()
			return 0, err
		}
		keys = append(keys, key)
	}
	more := iter.Valid()
	iter.Close()
	for _, key := range keys {
		if err := k.EmergencyFreezeVoteState.Remove(ctx, key); err != nil {
			return 0, err
		}
	}
	used := uint64(len(keys))
	if more {
		return used, nil
	}
	if err := k.FreezeSignalPruneIndex.Remove(ctx, pruneKey); err != nil {
		return 0, err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, err
	}
	headerHeight, overflow := checkedAddHubUint64(signal.ClosedHeight, params.Freeze.FreezeSignalSummaryRetentionBlocks)
	if overflow {
		return 0, errorsmod.Wrap(types.ErrInvariantBroken, "freeze signal summary prune height overflow")
	}
	if headerHeight < currentHeight {
		headerHeight = currentHeight
	}
	if err := k.FreezeSignalPruneIndex.Set(ctx, types.NewFreezeSignalPruneKey(headerHeight, signalID, types.FreezeSignalPrunePhase_FREEZE_SIGNAL_PRUNE_PHASE_HEADER)); err != nil {
		return 0, err
	}
	if used == 0 {
		used = 1
	}
	return used, nil
}

func (k Keeper) pruneFreezeSignalHeader(ctx context.Context, pruneKey types.FreezeSignalPruneKey) (uint64, error) {
	signalID := pruneKey.K2()
	iter, err := k.EmergencyFreezeVoteState.Iterate(ctx, collections.NewPrefixedPairRange[[]byte, []byte](signalID))
	if err != nil {
		return 0, err
	}
	hasVotes := iter.Valid()
	iter.Close()
	if hasVotes {
		return 0, errorsmod.Wrap(types.ErrInvariantBroken, "freeze signal header prune precedes vote prune")
	}
	signal, err := k.FreezeSignalState.Get(ctx, signalID)
	if err != nil {
		return 0, errorsmod.Wrap(types.ErrInvariantBroken, "freeze header prune references missing signal")
	}
	if err := k.FreezeSignalByProfileIndex.Remove(ctx, types.NewFreezeSignalByProfileKey(signal.ModelId, signal.ProfileVersion, signal.SignalStatus, signal.RiskWindowEndHeight, signalID)); err != nil {
		return 0, err
	}
	if err := k.FreezeSignalByWindow.Remove(ctx, types.NewFreezeSignalByWindowKey(signal.ModelId, signal.ProfileVersion, signal.RiskWindowId)); err != nil {
		return 0, err
	}
	if err := k.FreezeSignalState.Remove(ctx, signalID); err != nil {
		return 0, err
	}
	if err := k.FreezeSignalPruneIndex.Remove(ctx, pruneKey); err != nil {
		return 0, err
	}
	return 1, nil
}

func removeFreezeSignalDeadlineIndexIfExists(ctx context.Context, set collections.KeySet[types.FreezeSignalDeadlineKey], key types.FreezeSignalDeadlineKey) error {
	exists, err := set.Has(ctx, key)
	if err != nil || !exists {
		return err
	}
	return set.Remove(ctx, key)
}

func bytesEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
