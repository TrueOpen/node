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

// candidatePoolSegmentOverheadMaxBytes covers the protobuf tag/length and the
// fixed segment metadata/hash around candidate_bitmap_segment_bytes.
const candidatePoolSegmentOverheadMaxBytes = uint64(128)

// endblockFixedRowMaxBytes bounds every fixed-shape row a Hub EndBlock processor
// decodes or writes back (ModelSupportState, SupportDeactivateCursorState,
// UnbondingState, UnbondingReceiptState, DailySupportState, MarkGateState,
// FreezeSignalState and EarningsPendingState,
// ProfileState, ModelState, CandidatePoolCurrentState, CandidatePoolDirtyState
// and the CandidatePoolSnapshot header). The widest fixed row carries a handful of
// bech32 addresses, a handful of 32-byte digests and roughly thirty uint64
// fields, i.e. well under 1 KiB; 4 KiB leaves headroom without a new parameter.
const endblockFixedRowMaxBytes = uint64(4096)

// maxEndblockRowBytes is the derived per-row serialized upper bound behind the
// EndBlock serialized-bytes budget. The bound is computed from existing
// parameters instead of introducing another consensus parameter.
// CandidatePoolActiveSegmentState and ParameterBucketVersionState are the two
// variable-length values touched by Hub EndBlock. Their bodies are bounded by
// candidate_bitmap_segment_bytes and max_parameter_bucket_update_bytes. One
// activation reads both the old and new bucket body, so the latter bound is
// counted twice:
//
//	maxEndblockRowBytes = endblockFixedRowMaxBytes
//	                    + candidate_bitmap_segment_bytes
//	                    + candidatePoolSegmentOverheadMaxBytes
//	                    + 2 * max_parameter_bucket_update_bytes
//
// The previous layout inlined the whole member vector into the
// snapshot header, so the derived bound was
//
//	4096 + candidate_slot_hard_capacity * 256 = 4096 + 4096*256 = 1_052_672 B
//	whole block: max_endblock_visited_items_total * that = 10_000 * 1_052_672
//	           = 10_526_720_000 B ~= 9.80 GiB
//
// The current layout uses one fixed CandidatePoolMemberState per stable slot
// plus a fixed-width bitmap segment, so slot capacity no longer multiplies the
// candidate portion. The independently registered bucket payload cap now
// dominates the combined bound at the defaults:
//
//	4096 + 512 + 128 + 2*1_048_576 = 2_101_888 B
//	whole block: 10_000 * 2_101_888 = 21_018_880_000 B ~= 19.57 GiB
//
// Snapshot members are one fixed row per stable slot and are never inlined into
// the header, so slot capacity must not multiply the per-row bound.
func maxEndblockRowBytes(params types.HubParamsV2) uint64 {
	return saturatingAdd(
		endblockFixedRowMaxBytes,
		saturatingAdd(
			saturatingAdd(uint64(params.CandidatePool.CandidateBitmapSegmentBytes), candidatePoolSegmentOverheadMaxBytes),
			saturatingMul(2, params.Bucket.MaxParameterBucketUpdateBytes),
		),
	)
}

// endblockBudget carries the two per-processor ceilings and the work already
// booked against them. Processors must call charge exactly once per visited row,
// including stale, poison and already-settled rows, and must pass the serialized
// size of at most one row per call. That keeps the identity
// bytes <= visited * maxEndblockRowBytes, so the total can only overshoot
// bytesLimit by the single row that crossed the line.
type endblockBudget struct {
	visitedLimit uint64
	bytesLimit   uint64
	visited      uint64
	bytes        uint64
}

func newEndblockBudget(visitedLimit, bytesLimit uint64) *endblockBudget {
	return &endblockBudget{visitedLimit: visitedLimit, bytesLimit: bytesLimit}
}

func (b *endblockBudget) exhausted() bool {
	return b.visited >= b.visitedLimit || b.bytes >= b.bytesLimit
}

func (b *endblockBudget) charge(rowBytes int) {
	b.visited++
	if rowBytes > 0 {
		b.bytes = saturatingAdd(b.bytes, uint64(rowBytes))
	}
}

func (b *endblockBudget) result() (uint64, uint64) {
	return b.visited, b.bytes
}

// BeginBlocker runs the future-effective activations keeper_detailed_design.md §15.1
// requires to be visible before this block's transactions. It deliberately takes
// no visited budget: the only thing registered here is the governed BuilderSet
// replacement, which is a single Item and therefore bounded by the store shape
// rather than by a cursor. Anything unbounded belongs in EndBlocker instead.
func (k Keeper) BeginBlocker(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if sdkCtx.BlockHeight() <= 0 {
		return nil
	}
	return k.ActivateDueBuilderSetReplacements(ctx, uint64(sdkCtx.BlockHeight()))
}

// EndBlocker advances bounded indexes under one shared visited-item budget and
// one shared serialized-bytes budget.
func (k Keeper) EndBlocker(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if sdkCtx.BlockHeight() <= 0 {
		return nil
	}
	height := uint64(sdkCtx.BlockHeight())
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	epochLength := normalizedEpochLengthBlocks(params)
	epoch := epochForHeight(height, epochLength)
	rowBytes := maxEndblockRowBytes(params)
	remaining := uint64(params.QueryEvent.MaxEndblockVisitedItemsTotal)
	remainingBytes := saturatingMul(remaining, rowBytes)
	run := func(local uint32, processor func(visitedLimit, bytesLimit uint64) (uint64, uint64, error)) error {
		limit := min(uint64(local), remaining)
		if limit == 0 {
			return nil
		}
		bytesLimit := min(saturatingMul(limit, rowBytes), remainingBytes)
		if bytesLimit == 0 {
			return nil
		}
		visited, consumed, err := processor(limit, bytesLimit)
		if err != nil {
			return err
		}
		if visited > limit {
			return errorsmod.Wrap(types.ErrInvariantBroken, "endblock processor exceeded its visited-item budget")
		}
		// A loop that checks its budget before each row can only cross bytesLimit
		// by the one row that crossed it, so one maxEndblockRowBytes of slack is
		// the exact tolerance; anything beyond it is a processor accounting bug.
		if consumed > saturatingAdd(bytesLimit, rowBytes) {
			return errorsmod.Wrap(types.ErrInvariantBroken, "endblock processor exceeded its serialized-bytes budget")
		}
		remaining -= visited
		if consumed >= remainingBytes {
			remainingBytes = 0
		} else {
			remainingBytes -= consumed
		}
		return nil
	}
	// cross_chain_asset_bridge_protocol.md §7.2 / keeper_data_structure_contract.md
	// §6.6a: the epoch boundary
	// activates any pending limit before the new usage window opens, then retires
	// windows past their retention under the registered per-block budget.
	// The budget here is the general EndBlock allowance, not the prune allowance:
	// activating a due limit and opening the usage window are correctness steps
	// that must happen even on a chain that prunes nothing per block.
	if err := run(params.QueryEvent.MaxEndblockVisitedItemsTotal, func(visitedLimit, _ uint64) (uint64, uint64, error) {
		visited, err := k.ProcessBridgeEpochBoundary(ctx, epoch, visitedLimit)
		return visited, 0, err
	}); err != nil {
		return err
	}
	if err := run(params.QueryEvent.MaxEndblockVisitedItemsTotal, func(visitedLimit, bytesLimit uint64) (uint64, uint64, error) {
		return k.ProcessParameterBucketActivations(ctx, height, visitedLimit, bytesLimit)
	}); err != nil {
		return err
	}
	// §9.3a step 6 fixes activation at the final EndBlock of the preceding
	// epoch. That commit must already contain epoch E+1's active key before the
	// first E+1 ProcessProposal runs; scanning with the current epoch on every
	// block would activate one block too late.
	_, epochEnd, err := epochHeightRange(epoch, epochLength)
	if err != nil {
		return err
	}
	if height == epochEnd {
		nextEpoch, err := checkedAdd(epoch, 1)
		if err != nil {
			return err
		}
		if err := k.ActivateDueVrfKeys(ctx, nextEpoch); err != nil {
			return err
		}
	}
	if err := run(params.QueryEvent.MaxEndblockVisitedItemsTotal, func(visitedLimit, bytesLimit uint64) (uint64, uint64, error) {
		return k.ProcessVrfKeyHistoryPrunes(ctx, epoch, visitedLimit, bytesLimit)
	}); err != nil {
		return err
	}
	if err := run(params.Service.MaxServiceBondEffectiveItemsPerBlock, visitedOnlyProcessor(rowBytes, func(limit uint64) (uint64, error) {
		return k.ProcessServiceBondEffectiveActivations(ctx, height, limit)
	})); err != nil {
		return err
	}
	if err := run(params.Beacon.MaxBeaconPruneItemsPerBlock, visitedOnlyProcessor(rowBytes, func(limit uint64) (uint64, error) {
		return k.ProcessBeaconPrunes(ctx, height, limit)
	})); err != nil {
		return err
	}
	if err := run(params.Bucket.MaxParameterBucketPruneItemsPerBlock, func(visitedLimit, bytesLimit uint64) (uint64, uint64, error) {
		return k.ProcessParameterBucketPrunes(ctx, height, visitedLimit, bytesLimit)
	}); err != nil {
		return err
	}
	if err := run(params.Reward.MaxRewardEpochItemsPerBlock, visitedOnlyProcessor(rowBytes, func(limit uint64) (uint64, error) {
		visited, err := k.ProcessDueRewardEpochs(ctx, height, limit)
		if err != nil || visited == limit {
			return visited, err
		}
		pruned, err := k.ProcessDueRewardEpochPrunes(ctx, epoch, height, limit-visited)
		return visited + pruned, err
	})); err != nil {
		return err
	}
	// Epoch/recipient cleanup must precede receipt pruning at the same height:
	// retained receipts are the reconstruction authority until cleanup completes.
	if err := run(params.Treasury.MaxTreasurySpendReceiptPruneItemsPerBlock, visitedOnlyProcessor(rowBytes, func(limit uint64) (uint64, error) {
		visited, err := k.ProcessTreasurySpendEpochCleanups(ctx, height, limit)
		if err != nil || visited == limit {
			return visited, err
		}
		pruned, err := k.ProcessTreasurySpendReceiptPrunes(ctx, height, limit-visited)
		return visited + pruned, err
	})); err != nil {
		return err
	}
	if err := run(params.Builder.MaxBuilderSetPruneItemsPerBlock, visitedOnlyProcessor(rowBytes, func(limit uint64) (uint64, error) {
		return k.ProcessBuilderSetPrunes(ctx, height, limit)
	})); err != nil {
		return err
	}
	if err := run(params.Builder.MaxBuilderFaultPruneItemsPerBlock, visitedOnlyProcessor(rowBytes, func(limit uint64) (uint64, error) {
		return k.ProcessBuilderFaultPrunes(ctx, height, limit)
	})); err != nil {
		return err
	}
	if err := run(params.CandidatePool.MaxCandidatePoolPruneItemsPerBlock, visitedOnlyProcessor(rowBytes, func(limit uint64) (uint64, error) {
		return k.ProcessCandidatePoolExpiries(ctx, height, limit)
	})); err != nil {
		return err
	}
	if err := run(params.Freeze.MaxFreezeSignalScheduleItemsPerBlock, visitedOnlyProcessor(rowBytes, func(limit uint64) (uint64, error) {
		return k.ProcessFreezeRiskWindowSchedules(ctx, height, limit)
	})); err != nil {
		return err
	}
	if err := run(params.Freeze.MaxFreezeSignalBuildItemsPerBlock, visitedOnlyProcessor(rowBytes, func(limit uint64) (uint64, error) {
		return k.ProcessFreezeSignalBuilds(ctx, height, limit)
	})); err != nil {
		return err
	}
	if err := run(params.Freeze.MaxFreezeSignalScheduleItemsPerBlock, visitedOnlyProcessor(rowBytes, func(limit uint64) (uint64, error) {
		return k.CloseExpiredFreezeSignals(ctx, height, limit)
	})); err != nil {
		return err
	}
	if err := run(params.Freeze.MaxFreezeSignalPruneItemsPerBlock, visitedOnlyProcessor(rowBytes, func(limit uint64) (uint64, error) {
		return k.ProcessFreezeSignalPrunes(ctx, height, limit)
	})); err != nil {
		return err
	}
	if err := run(params.Support.MaxSupportExpiryItemsPerBlock, func(visitedLimit, bytesLimit uint64) (uint64, uint64, error) {
		return k.ProcessSupportDeactivations(ctx, height, visitedLimit, bytesLimit)
	}); err != nil {
		return err
	}
	if err := run(params.Support.MaxSupportExpiryItemsPerBlock, func(visitedLimit, bytesLimit uint64) (uint64, uint64, error) {
		return k.ProcessExpiredModelSupports(ctx, epoch, height, visitedLimit, bytesLimit)
	}); err != nil {
		return err
	}
	if err := run(params.Service.MaxUnbondingMaturityItemsPerBlock, func(visitedLimit, bytesLimit uint64) (uint64, uint64, error) {
		return k.ProcessUnbondingMaturities(ctx, height, visitedLimit, bytesLimit)
	}); err != nil {
		return err
	}
	if err := run(params.Support.MaxModelSupportPruneItemsPerBlock, func(visitedLimit, bytesLimit uint64) (uint64, uint64, error) {
		return k.ProcessModelSupportPrunes(ctx, epoch, visitedLimit, bytesLimit)
	}); err != nil {
		return err
	}
	if err := run(params.Support.MaxModelSupportPruneItemsPerBlock, func(visitedLimit, bytesLimit uint64) (uint64, uint64, error) {
		return k.ProcessDailySupportExpiries(ctx, epoch, visitedLimit, bytesLimit)
	}); err != nil {
		return err
	}
	if err := run(params.Service.MaxUnbondingReceiptPruneItemsPerBlock, func(visitedLimit, bytesLimit uint64) (uint64, uint64, error) {
		return k.ProcessUnbondingReceiptPrunes(ctx, height, visitedLimit, bytesLimit)
	}); err != nil {
		return err
	}
	if err := run(params.Service.MaxRoleFaultPruneItemsPerBlock, visitedOnlyProcessor(rowBytes, func(limit uint64) (uint64, error) {
		return k.ProcessRoleFaultPrunes(ctx, height, limit)
	})); err != nil {
		return err
	}
	if err := run(params.CandidatePool.MaxCandidatePoolBuildMembersPerBlock, visitedOnlyProcessor(rowBytes, func(limit uint64) (uint64, error) {
		return k.ProcessDirtyCandidatePools(ctx, height, limit)
	})); err != nil {
		return err
	}
	if err := run(params.CandidatePool.MaxCandidatePoolPruneItemsPerBlock, visitedOnlyProcessor(rowBytes, func(limit uint64) (uint64, error) {
		return k.ProcessCandidateSnapshotPrunes(ctx, height, limit)
	})); err != nil {
		return err
	}
	// §3.4 line 245: "once due, visit at most
	// max_candidate_slot_binding_prune_items_per_block rows per block and delete the
	// binding/index".
	// Without this the released bindings and their prune index rows grow without
	// bound: the processor existed but had no EndBlock caller.
	if err := run(params.CandidatePool.MaxCandidateSlotBindingPruneItemsPerBlock, visitedOnlyProcessor(rowBytes, func(limit uint64) (uint64, error) {
		return k.ProcessCandidateSlotBindingPrunes(ctx, epoch, limit)
	})); err != nil {
		return err
	}
	return k.setEndBlockBudget(ctx, height, remaining, remainingBytes)
}

// visitedOnlyProcessor adapts an EndBlock processor that does not report
// serialized bytes yet. It charges the derived worst-case row size for every
// visited item, which is deliberately conservative: because the per-processor
// bytes ceiling is itself visitedLimit * maxEndblockRowBytes, such a processor's
// bytes charge can never bind before its visited charge does, and the shared
// invariant remainingBytes >= remaining * maxEndblockRowBytes is preserved. The
// Remaining processors use this conservative adapter until their exact row-size
// accounting is folded into their own bounded executor.
func visitedOnlyProcessor(rowBytes uint64, processor func(uint64) (uint64, error)) func(uint64, uint64) (uint64, uint64, error) {
	return func(visitedLimit, _ uint64) (uint64, uint64, error) {
		visited, err := processor(visitedLimit)
		return visited, saturatingMul(visited, rowBytes), err
	}
}

// saturatingAdd already lives in hub_keeper_write.go; only the multiply is new.
func saturatingMul(a, b uint64) uint64 {
	return shared.SaturatingMulUint64(a, b)
}

// ProcessSupportDeactivations drains the bounded support-deactivation cursors
// written by EnqueueSupportDeactivation (P0-3). Both dimensions are bounded:
// profiles per model by types.MaxProfilesPerModel at enqueue time, operators per
// profile by this cursor at drain time.
//
// Every visited ModelSupportByProfileIndex row costs one visited item, including
// rows whose primary is already gone and rows that are already inactive, so a
// stale or poison row can never be rescanned for free on every block
// (node_context.md §9.4). Completing a cursor costs one visited item as well, so
// a flood of empty cursors is bounded too. A cursor is removed the moment its
// operator scan is exhausted; no permanent per-profile audit row is left behind.
func (k Keeper) ProcessSupportDeactivations(ctx context.Context, currentHeight, visitedLimit, bytesLimit uint64) (uint64, uint64, error) {
	budget := newEndblockBudget(visitedLimit, bytesLimit)
	for !budget.exhausted() {
		cursorKey, cursor, found, err := k.firstSupportDeactivateCursor(ctx)
		if err != nil {
			visited, consumed := budget.result()
			return visited, consumed, err
		}
		if !found {
			break
		}
		done, err := k.advanceSupportDeactivateCursor(ctx, cursorKey, cursor, currentHeight, budget)
		if err != nil {
			visited, consumed := budget.result()
			return visited, consumed, err
		}
		if !done {
			break
		}
	}
	visited, consumed := budget.result()
	return visited, consumed, nil
}

func (k Keeper) firstSupportDeactivateCursor(ctx context.Context) (types.ProfileStateKeyPair, types.SupportDeactivateCursorState, bool, error) {
	var (
		emptyKey   types.ProfileStateKeyPair
		emptyState types.SupportDeactivateCursorState
	)
	iter, err := k.SupportDeactivateCursor.Iterate(ctx, nil)
	if err != nil {
		return emptyKey, emptyState, false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return emptyKey, emptyState, false, nil
	}
	entry, err := iter.KeyValue()
	if err != nil {
		return emptyKey, emptyState, false, err
	}
	return entry.Key, entry.Value, true, nil
}

// advanceSupportDeactivateCursor resumes one profile's operator scan from
// last_operator_address (exclusive) and returns whether the cursor finished.
// Progress is persisted whenever the budget runs out mid-profile, so the next
// block continues instead of rescanning from the first operator.
func (k Keeper) advanceSupportDeactivateCursor(
	ctx context.Context,
	cursorKey types.ProfileStateKeyPair,
	cursor types.SupportDeactivateCursorState,
	currentHeight uint64,
	budget *endblockBudget,
) (bool, error) {
	if cursor.ModelId == "" || cursor.ProfileVersion == 0 || cursor.Reason == "" {
		return false, errorsmod.Wrap(types.ErrInvariantBroken, "support deactivate cursor is missing its model/profile/reason scope")
	}
	model, modelErr := k.Model.Get(ctx, cursor.ModelId)
	profile, profileErr := k.Profile.Get(ctx, cursorKey)
	if modelErr != nil || profileErr != nil {
		return false, errorsmod.Wrap(types.ErrInvariantBroken, "support deactivate cursor references missing model/profile")
	}
	if cursor.Reason == types.ModelSupportDeactivateFrozen &&
		types.IsModelProfileStatusOpen(model.Status) && types.IsModelProfileStatusOpen(profile.Status) {
		// Governance may unfreeze while a bounded fan-out is in flight. Once
		// both live gates are open again, the old freeze intent no longer owns
		// the remaining declarations and the cursor must retire immediately.
		budget.charge(cursor.Size())
		return true, k.SupportDeactivateCursor.Remove(ctx, cursorKey)
	}
	advanced := false
	for !budget.exhausted() {
		operator, found, err := k.nextProfileSupportOperator(ctx, cursor.ModelId, cursor.ProfileVersion, cursor.LastOperatorAddress)
		if err != nil {
			return false, err
		}
		if !found {
			// Closing the cursor is the visited item that pays for having read it.
			budget.charge(cursor.Size())
			return true, k.SupportDeactivateCursor.Remove(ctx, cursorKey)
		}
		state, exists, err := k.loadModelSupport(ctx, operator, cursor.ModelId, cursor.ProfileVersion)
		if err != nil {
			return false, err
		}
		rowBytes := 0
		if exists {
			rowBytes = state.Size()
			if state.DeclaredSupport || state.SupportActive || state.ActiveSupportStakeSnapshot != 0 || state.EligibleSupportStakeSnapshot != 0 {
				updated, err := k.DeactivateModelSupport(ctx, operator, cursor.ModelId, cursor.ProfileVersion, cursor.Reason, currentHeight)
				if err != nil {
					return false, err
				}
				rowBytes = max(rowBytes, updated.Size())
			}
		}
		budget.charge(rowBytes)
		visitedCount, err := checkedAdd(cursor.VisitedCount, 1)
		if err != nil {
			return false, err
		}
		cursor.VisitedCount = visitedCount
		cursor.LastOperatorAddress = operator
		advanced = true
	}
	if advanced {
		return false, k.SupportDeactivateCursor.Set(ctx, cursorKey, cursor)
	}
	return false, nil
}

func (k Keeper) ProcessExpiredModelSupports(ctx context.Context, currentEpoch, currentHeight, visitedLimit, bytesLimit uint64) (uint64, uint64, error) {
	budget := newEndblockBudget(visitedLimit, bytesLimit)
	keys, err := dueKeys(ctx, k.ModelSupportExpiryIndex, currentEpoch, visitedLimit)
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
		expiresEpoch, suffix := key.K1(), key.K2()
		operator, modelID, version := suffix.K1(), suffix.K2(), suffix.K3()
		state, err := k.ModelSupport.Get(cache, suffix)
		if err != nil {
			if errors.Is(err, collections.ErrNotFound) {
				// A dangling derived key carries no business state. Retire it so an
				// interrupted legacy cleanup cannot halt every later EndBlock.
				if err := k.ModelSupportExpiryIndex.Remove(cache, key); err != nil && !errors.Is(err, collections.ErrNotFound) {
					visited, consumed := budget.result()
					return visited, consumed, err
				}
				write()
				budget.charge(0)
				continue
			}
			visited, consumed := budget.result()
			return visited, consumed, err
		}
		rowBytes := state.Size()
		if !state.DeclaredSupport || state.SupportFreshUntilEpoch != expiresEpoch {
			if err := k.ModelSupportExpiryIndex.Remove(cache, key); err != nil {
				visited, consumed := budget.result()
				return visited, consumed, err
			}
			write()
			budget.charge(rowBytes)
			continue
		}
		updated, err := k.DeactivateModelSupport(cache, operator, modelID, version, types.ModelSupportDeactivateExpired, currentHeight)
		if err != nil {
			visited, consumed := budget.result()
			return visited, consumed, err
		}
		write()
		budget.charge(max(rowBytes, updated.Size()))
	}
	visited, consumed := budget.result()
	return visited, consumed, nil
}

func (k Keeper) ProcessUnbondingMaturities(ctx context.Context, currentHeight, visitedLimit, bytesLimit uint64) (uint64, uint64, error) {
	budget := newEndblockBudget(visitedLimit, bytesLimit)
	keys, err := dueTripleKeys(ctx, k.UnbondingMaturityIndex, currentHeight, visitedLimit)
	if err != nil {
		return 0, 0, err
	}
	for _, key := range keys {
		if budget.exhausted() {
			break
		}
		matureHeight, operator, id := key.K1(), key.K2(), key.K3()
		stateKey := types.NewUnbondingKey(operator, id)
		state, err := k.Unbonding.Get(ctx, stateKey)
		if err != nil {
			visited, consumed := budget.result()
			return visited, consumed, errorsmod.Wrap(types.ErrInvariantBroken, "unbonding maturity index references missing primary")
		}
		rowBytes := state.Size()
		if state.MatureHeight != matureHeight || state.OperatorAddress != operator || !bytes.Equal(state.UnbondingId, id) {
			visited, consumed := budget.result()
			return visited, consumed, errorsmod.Wrap(types.ErrInvariantBroken, "unbonding maturity index identity mismatch")
		}
		if state.Status == types.UnbondingStatusOpen {
			oldStatusKey := types.NewUnbondingByOperatorStatusKey(operator, state.Status, state.MatureHeight, id)
			state.Status = types.UnbondingStatusMature
			if err := k.Unbonding.Set(ctx, stateKey, state); err != nil {
				visited, consumed := budget.result()
				return visited, consumed, err
			}
			rowBytes = max(rowBytes, state.Size())
			if err := k.UnbondingByOperatorStatusIndex.Remove(ctx, oldStatusKey); err != nil {
				visited, consumed := budget.result()
				return visited, consumed, err
			}
			if err := k.UnbondingByOperatorStatusIndex.Set(ctx, types.NewUnbondingByOperatorStatusKey(operator, state.Status, state.MatureHeight, id)); err != nil {
				visited, consumed := budget.result()
				return visited, consumed, err
			}
		} else if state.Status != types.UnbondingStatusMature {
			visited, consumed := budget.result()
			return visited, consumed, errorsmod.Wrap(types.ErrInvariantBroken, "unbonding maturity index references invalid status")
		}
		if err := k.UnbondingMaturityIndex.Remove(ctx, key); err != nil {
			visited, consumed := budget.result()
			return visited, consumed, err
		}
		budget.charge(rowBytes)
	}
	visited, consumed := budget.result()
	return visited, consumed, nil
}

func (k Keeper) ProcessModelSupportPrunes(ctx context.Context, currentEpoch, visitedLimit, bytesLimit uint64) (uint64, uint64, error) {
	budget := newEndblockBudget(visitedLimit, bytesLimit)
	keys, err := dueSupportPruneKeys(ctx, k.ModelSupportPruneIndex, currentEpoch, visitedLimit)
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
		supportKey := key.K2()
		state, err := k.ModelSupport.Get(cache, supportKey)
		if err != nil && !errors.Is(err, collections.ErrNotFound) {
			visited, consumed := budget.result()
			return visited, consumed, err
		}
		rowBytes := 0
		if err == nil {
			rowBytes = state.Size()
			// A row can legitimately come back to life inside its retention
			// window: deactivate at epoch e writes a prune key at
			// e + model_support_row_retention_epochs, and the operator may
			// re-declare at e+1 and keep refreshing. The prune key that was
			// scheduled for the dead row is then simply stale, so it is dropped
			// and the row is left alone; a later deactivation schedules a fresh
			// prune key. Treating this as a broken invariant would halt the chain
			// on a reachable, legal sequence.
			if !state.SupportActive && state.ActiveSupportStakeSnapshot == 0 && state.EligibleSupportStakeSnapshot == 0 && state.SupportFreshUntilEpoch <= currentEpoch {
				if err := k.ModelSupport.Remove(cache, supportKey); err != nil {
					visited, consumed := budget.result()
					return visited, consumed, err
				}
				if state.SupportFreshUntilEpoch != 0 {
					if err := k.ModelSupportExpiryIndex.Remove(cache, types.NewModelSupportExpiryIndexKey(state.SupportFreshUntilEpoch, state.OperatorAddress, state.ModelId, state.ProfileVersion)); err != nil && !errors.Is(err, collections.ErrNotFound) {
						visited, consumed := budget.result()
						return visited, consumed, err
					}
				}
				if err := k.ProfileCapability.Remove(cache, types.NewProfileCapabilityKey(state.OperatorAddress, state.ModelId, state.ProfileVersion)); err != nil && !errors.Is(err, collections.ErrNotFound) {
					visited, consumed := budget.result()
					return visited, consumed, err
				}
				if err := k.ModelSupportByProfileIndex.Remove(cache, types.NewModelSupportByProfileIndexKey(state.ModelId, state.ProfileVersion, state.OperatorAddress)); err != nil && !errors.Is(err, collections.ErrNotFound) {
					visited, consumed := budget.result()
					return visited, consumed, err
				}
				if err := k.ModelSupportByOperatorIndex.Remove(cache, types.NewModelSupportByOperatorIndexKey(state.OperatorAddress, state.ModelId, state.ProfileVersion)); err != nil && !errors.Is(err, collections.ErrNotFound) {
					visited, consumed := budget.result()
					return visited, consumed, err
				}
			}
		} else {
			// If an older cleanup removed the primary first, all same-identity
			// projections are orphans and can deterministically self-heal here.
			operator, modelID, version := supportKey.K1(), supportKey.K2(), supportKey.K3()
			if err := k.ProfileCapability.Remove(cache, types.NewProfileCapabilityKey(operator, modelID, version)); err != nil && !errors.Is(err, collections.ErrNotFound) {
				visited, consumed := budget.result()
				return visited, consumed, err
			}
			if err := k.ModelSupportByProfileIndex.Remove(cache, types.NewModelSupportByProfileIndexKey(modelID, version, operator)); err != nil && !errors.Is(err, collections.ErrNotFound) {
				visited, consumed := budget.result()
				return visited, consumed, err
			}
			if err := k.ModelSupportByOperatorIndex.Remove(cache, types.NewModelSupportByOperatorIndexKey(operator, modelID, version)); err != nil && !errors.Is(err, collections.ErrNotFound) {
				visited, consumed := budget.result()
				return visited, consumed, err
			}
		}
		if err := k.ModelSupportPruneIndex.Remove(cache, key); err != nil {
			visited, consumed := budget.result()
			return visited, consumed, err
		}
		write()
		budget.charge(rowBytes)
	}
	visited, consumed := budget.result()
	return visited, consumed, nil
}

// ProcessDailySupportExpiries and ProcessUnbondingReceiptPrunes never decode the
// value they retire, so they report zero serialized bytes: the visited-item
// budget is the only cost they incur.
func (k Keeper) ProcessDailySupportExpiries(ctx context.Context, currentEpoch, visitedLimit, bytesLimit uint64) (uint64, uint64, error) {
	budget := newEndblockBudget(visitedLimit, bytesLimit)
	keys, err := dueDailyKeys(ctx, k.DailySupportExpiryIndex, currentEpoch, visitedLimit)
	if err != nil {
		return 0, 0, err
	}
	for _, key := range keys {
		if budget.exhausted() {
			break
		}
		// DailySupportExpiryIndex is (expiry_epoch, operator_address, support_epoch)
		// per keeper_data_structure_contract.md §6.1, so the primary key is (K3, K2).
		stateKey := types.NewDailySupportKey(key.K3(), key.K2())
		if err := k.DailySupport.Remove(ctx, stateKey); err != nil && !errors.Is(err, collections.ErrNotFound) {
			visited, consumed := budget.result()
			return visited, consumed, err
		}
		if err := k.DailySupportExpiryIndex.Remove(ctx, key); err != nil {
			visited, consumed := budget.result()
			return visited, consumed, err
		}
		budget.charge(0)
	}
	visited, consumed := budget.result()
	return visited, consumed, nil
}

func (k Keeper) ProcessUnbondingReceiptPrunes(ctx context.Context, currentHeight, visitedLimit, bytesLimit uint64) (uint64, uint64, error) {
	budget := newEndblockBudget(visitedLimit, bytesLimit)
	keys, err := dueReceiptKeys(ctx, k.UnbondingReceiptPruneIndex, currentHeight, visitedLimit)
	if err != nil {
		return 0, 0, err
	}
	for _, key := range keys {
		if budget.exhausted() {
			break
		}
		if err := k.UnbondingReceipt.Remove(ctx, key.K2()); err != nil && !errors.Is(err, collections.ErrNotFound) {
			visited, consumed := budget.result()
			return visited, consumed, err
		}
		if err := k.UnbondingReceiptPruneIndex.Remove(ctx, key); err != nil {
			visited, consumed := budget.result()
			return visited, consumed, err
		}
		budget.charge(0)
	}
	visited, consumed := budget.result()
	return visited, consumed, nil
}

func dueKeys(ctx context.Context, index collections.KeySet[types.ModelSupportExpiryIndexKeyPair], height, limit uint64) ([]types.ModelSupportExpiryIndexKeyPair, error) {
	iter, err := index.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	keys := make([]types.ModelSupportExpiryIndexKeyPair, 0, limit)
	for ; iter.Valid() && uint64(len(keys)) < limit; iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		if key.K1() > height {
			break
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func dueTripleKeys(ctx context.Context, index collections.KeySet[types.UnbondingMaturityIndexKeyTriple], height, limit uint64) ([]types.UnbondingMaturityIndexKeyTriple, error) {
	iter, err := index.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	keys := make([]types.UnbondingMaturityIndexKeyTriple, 0, limit)
	for ; iter.Valid() && uint64(len(keys)) < limit; iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		if key.K1() > height {
			break
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func dueSupportPruneKeys(ctx context.Context, index collections.KeySet[types.ModelSupportPruneIndexKeyPair], epoch, limit uint64) ([]types.ModelSupportPruneIndexKeyPair, error) {
	iter, err := index.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	keys := make([]types.ModelSupportPruneIndexKeyPair, 0, limit)
	for ; iter.Valid() && uint64(len(keys)) < limit; iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		if key.K1() > epoch {
			break
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func dueDailyKeys(ctx context.Context, index collections.KeySet[types.DailySupportExpiryIndexKeyTriple], epoch, limit uint64) ([]types.DailySupportExpiryIndexKeyTriple, error) {
	iter, err := index.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	keys := make([]types.DailySupportExpiryIndexKeyTriple, 0, limit)
	for ; iter.Valid() && uint64(len(keys)) < limit; iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		if key.K1() > epoch {
			break
		}
		keys = append(keys, key)
	}
	return keys, nil
}

func dueReceiptKeys(ctx context.Context, index collections.KeySet[types.UnbondingReceiptPruneKey], height, limit uint64) ([]types.UnbondingReceiptPruneKey, error) {
	iter, err := index.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	keys := make([]types.UnbondingReceiptPruneKey, 0, limit)
	for ; iter.Valid() && uint64(len(keys)) < limit; iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		if key.K1() > height {
			break
		}
		keys = append(keys, key)
	}
	return keys, nil
}
