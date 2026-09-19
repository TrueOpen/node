package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

type RewardEpochRunResult struct {
	Phase    types.RewardEpochPhase
	Visited  uint64
	Advanced uint64
	Mutated  bool
}

// ProcessDueRewardEpochs advances due epoch/bucket cursors in index order. The
// index row itself costs one visited unit; RunRewardEpoch accounts for each
// state-machine transition it performs.
func (k Keeper) ProcessDueRewardEpochs(
	ctx context.Context,
	currentHeight uint64,
	visitedLimit uint64,
) (visited uint64, err error) {
	if visitedLimit == 0 {
		return 0, nil
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if sdkCtx.BlockHeight() < 0 || currentHeight > uint64(sdkCtx.BlockHeight()) {
		return 0, fmt.Errorf("reward epoch due height is ahead of block height")
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, err
	}
	end := types.NewRewardEpochIndexKey(currentHeight, math.MaxUint64, math.MaxUint64)
	iter, err := k.RewardEpochIndex.Iterate(
		ctx, new(collections.Range[types.RewardEpochIndexKeyTriple]).EndInclusive(end),
	)
	if err != nil {
		return 0, err
	}
	keys := make([]types.RewardEpochIndexKeyTriple, 0, visitedLimit)
	for ; iter.Valid() && uint64(len(keys)) < visitedLimit; iter.Next() {
		key, err := iter.Key()
		if err != nil {
			iter.Close()
			return 0, err
		}
		keys = append(keys, key)
	}
	if err := iter.Close(); err != nil {
		return 0, err
	}

	for _, key := range keys {
		if visited == visitedLimit {
			break
		}
		visited++
		epoch := key.K2()
		if key.K3() > uint64(math.MaxInt32) {
			return visited, fmt.Errorf("reward epoch index bucket overflows enum")
		}
		rewardBucket := types.RewardBucket(key.K3())
		if !validRewardBucket(rewardBucket) {
			return visited, fmt.Errorf("reward epoch index bucket is invalid")
		}
		_, epochEnd, err := epochHeightRange(epoch, normalizedEpochLengthBlocks(params))
		if err != nil || epochEnd == math.MaxUint64 || key.K1() != epochEnd+1 {
			return visited, fmt.Errorf("reward epoch index due height mismatch")
		}

		competition, competitionErr := k.RewardCompetitionEpoch.Get(
			ctx, types.NewRewardCompetitionEpochKey(uint64(rewardBucket), epoch),
		)
		if competitionErr == nil && competition.RewardEpochClosed {
			if err := k.requireClosedRewardEpochWithoutCursor(ctx, epoch, rewardBucket, competition, params); err != nil {
				return visited, err
			}
			if err := k.RewardEpochIndex.Remove(ctx, key); err != nil {
				return visited, err
			}
			continue
		}
		if competitionErr != nil && !errors.Is(competitionErr, collections.ErrNotFound) {
			return visited, competitionErr
		}

		remaining := visitedLimit - visited
		if remaining == 0 {
			break
		}
		localLimit := minUint64(remaining, uint64(params.Reward.MaxRewardEpochItemsPerBlock))
		if localLimit == 0 {
			return visited, fmt.Errorf("reward epoch item cap is zero")
		}
		result, err := k.RunRewardEpoch(ctx, epoch, rewardBucket, uint32(localLimit))
		if err != nil {
			return visited, err
		}
		if result.Visited > remaining {
			return visited, fmt.Errorf("reward epoch runner exceeded dispatcher budget")
		}
		visited += result.Visited
		if result.Phase != types.RewardEpochPhase_REWARD_EPOCH_PHASE_COMPLETE {
			hasIndex, err := k.RewardEpochIndex.Has(ctx, key)
			if err != nil {
				return visited, err
			}
			if !hasIndex {
				return visited, fmt.Errorf("open reward epoch lost its due index")
			}
		}
	}
	return visited, nil
}

// RunRewardEpoch advances the Phase 0 non-monetary reward state machine. It
// derives P30, closes the competition, and schedules retention. The removed
// eligibility, beacon/mark and accrual pipelines are deliberately unreachable.
func (k Keeper) RunRewardEpoch(
	ctx context.Context,
	epoch uint64,
	rewardBucket types.RewardBucket,
	maxItems uint32,
) (RewardEpochRunResult, error) {
	if !validRewardBucket(rewardBucket) {
		return RewardEpochRunResult{}, fmt.Errorf("reward bucket is invalid")
	}
	if maxItems == 0 {
		return RewardEpochRunResult{}, fmt.Errorf("max_items must be non-zero")
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return RewardEpochRunResult{}, err
	}
	if maxItems > params.Reward.MaxRewardEpochItemsPerBlock {
		maxItems = params.Reward.MaxRewardEpochItemsPerBlock
	}
	if maxItems == 0 {
		return RewardEpochRunResult{}, fmt.Errorf("reward epoch item cap is zero")
	}
	_, epochEnd, err := epochHeightRange(epoch, normalizedEpochLengthBlocks(params))
	if err != nil || epochEnd == math.MaxUint64 {
		return RewardEpochRunResult{}, fmt.Errorf("reward epoch end height overflows")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if sdkCtx.BlockHeight() < 0 || uint64(sdkCtx.BlockHeight()) < epochEnd+1 {
		return RewardEpochRunResult{}, fmt.Errorf("reward epoch has not ended")
	}

	cursorKey := types.NewRewardEpochCursorKey(epoch, uint64(rewardBucket))
	competitionKey := types.NewRewardCompetitionEpochKey(uint64(rewardBucket), epoch)
	boundariesHash, err := types.RewardOrderValueBucketBoundariesHash(
		sdkCtx.ChainID(), params.Reward.OrderValueBucketBoundaries,
	)
	if err != nil {
		return RewardEpochRunResult{}, err
	}

	competition, competitionErr := k.RewardCompetitionEpoch.Get(ctx, competitionKey)
	if competitionErr == nil {
		if err := validateCompetitionEpoch(
			competition, epoch, rewardBucket, params.Reward.OrderValueBucketBoundaries, boundariesHash,
		); err != nil {
			return RewardEpochRunResult{}, err
		}
		if competition.UniquePayerCount > competition.TotalTasks || competition.UniqueWorkerCount > competition.TotalTasks {
			return RewardEpochRunResult{}, fmt.Errorf("reward competition unique counts exceed total_tasks")
		}
		if competition.RewardEpochClosed {
			if err := k.requireClosedRewardEpochWithoutCursor(ctx, epoch, rewardBucket, competition, params); err != nil {
				return RewardEpochRunResult{}, err
			}
			return RewardEpochRunResult{Phase: types.RewardEpochPhase_REWARD_EPOCH_PHASE_COMPLETE}, nil
		}
	} else if !errors.Is(competitionErr, collections.ErrNotFound) {
		return RewardEpochRunResult{}, competitionErr
	}

	audit, auditErr := k.RewardEpochAudit.Get(ctx, cursorKey)
	if auditErr == nil {
		if competitionErr == nil {
			return RewardEpochRunResult{}, fmt.Errorf("pruned reward epoch still retains its competition")
		}
		if err := validateCompletedRewardAudit(audit, epoch, rewardBucket); err != nil {
			return RewardEpochRunResult{}, err
		}
		return RewardEpochRunResult{Phase: types.RewardEpochPhase_REWARD_EPOCH_PHASE_COMPLETE}, nil
	}
	if !errors.Is(auditErr, collections.ErrNotFound) {
		return RewardEpochRunResult{}, auditErr
	}

	cursor, cursorErr := k.RewardEpochCursor.Get(ctx, cursorKey)
	cursorExists := cursorErr == nil
	if cursorErr != nil {
		if !errors.Is(cursorErr, collections.ErrNotFound) {
			return RewardEpochRunResult{}, cursorErr
		}
		cursor = types.RewardEpochCursorState{
			Epoch: epoch, RewardBucket: rewardBucket,
			Phase: types.RewardEpochPhase_REWARD_EPOCH_PHASE_HISTOGRAM_OPEN,
		}
	}
	if err := validatePhase0RewardCursor(cursor, epoch, rewardBucket); err != nil {
		return RewardEpochRunResult{}, err
	}
	if cursor.Phase == types.RewardEpochPhase_REWARD_EPOCH_PHASE_HISTOGRAM_OPEN {
		if competitionErr == nil {
			p30, err := shared.ParseAmount(competition.P30Cutoff)
			if err != nil || p30 != 0 {
				return RewardEpochRunResult{}, fmt.Errorf("open reward histogram already contains a derived cutoff")
			}
		}
	} else {
		if competitionErr != nil {
			return RewardEpochRunResult{}, fmt.Errorf("derived reward cursor has no competition state")
		}
		if p30, err := shared.ParseAmount(competition.P30Cutoff); err != nil || p30 == 0 {
			return RewardEpochRunResult{}, fmt.Errorf("derived reward cursor has no P30 cutoff")
		}
	}

	before := cursor
	before.LastTaskId = append([]byte(nil), cursor.LastTaskId...)
	cacheCtx, commit := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	result := RewardEpochRunResult{Phase: cursor.Phase}
	limit := uint64(maxItems)

	for result.Visited < limit {
		switch cursor.Phase {
		case types.RewardEpochPhase_REWARD_EPOCH_PHASE_HISTOGRAM_OPEN:
			if _, err := k.deriveAndStoreRewardCutoffs(cache, sdkCtx.ChainID(), epoch, rewardBucket, params); err != nil {
				return RewardEpochRunResult{}, err
			}
			cursor.Phase = types.RewardEpochPhase_REWARD_EPOCH_PHASE_CUTOFF_DERIVED
			result.Visited++
			result.Advanced++

		case types.RewardEpochPhase_REWARD_EPOCH_PHASE_CUTOFF_DERIVED:
			if err := k.closeRewardEpoch(cache, epoch, epochEnd, rewardBucket, params); err != nil {
				return RewardEpochRunResult{}, err
			}
			result.Visited++
			result.Advanced++
			commit()
			result.Phase = types.RewardEpochPhase_REWARD_EPOCH_PHASE_COMPLETE
			result.Mutated = true
			return result, nil

		default:
			return RewardEpochRunResult{}, fmt.Errorf("reward epoch cursor phase is not supported in Phase 0")
		}
	}

	cursor.VisitedCount, err = checkedAdd(cursor.VisitedCount, result.Visited)
	if err != nil {
		return RewardEpochRunResult{}, fmt.Errorf("reward cursor visited count overflows")
	}
	cursor.AppliedCount, err = checkedAdd(cursor.AppliedCount, result.Advanced)
	if err != nil {
		return RewardEpochRunResult{}, fmt.Errorf("reward cursor applied count overflows")
	}
	result.Phase = cursor.Phase
	if cursorExists && rewardEpochCursorEqual(before, cursor) {
		return result, nil
	}
	if err := k.RewardEpochCursor.Set(cache, cursorKey, cursor); err != nil {
		return RewardEpochRunResult{}, err
	}
	commit()
	result.Mutated = true
	return result, nil
}

func (k Keeper) closeRewardEpoch(
	ctx context.Context,
	epoch uint64,
	epochEnd uint64,
	rewardBucket types.RewardBucket,
	params types.HubParamsV2,
) error {
	competitionKey := types.NewRewardCompetitionEpochKey(uint64(rewardBucket), epoch)
	competition, err := k.RewardCompetitionEpoch.Get(ctx, competitionKey)
	if err != nil {
		return err
	}
	if competition.RewardEpochClosed {
		return fmt.Errorf("reward competition is already closed")
	}
	if p30, err := shared.ParseAmount(competition.P30Cutoff); err != nil || p30 == 0 {
		return fmt.Errorf("reward competition has no derived P30 cutoff")
	}
	competition.RewardEpochClosed = true
	if err := k.RewardCompetitionEpoch.Set(ctx, competitionKey, competition); err != nil {
		return err
	}
	if err := k.advanceRewardP30CutoffPointer(ctx, competition); err != nil {
		return err
	}
	if err := k.RewardEpochCursor.Remove(ctx, types.NewRewardEpochCursorKey(epoch, uint64(rewardBucket))); err != nil {
		return err
	}
	if err := k.RewardEpochIndex.Remove(ctx, types.NewRewardEpochIndexKey(epochEnd+1, epoch, uint64(rewardBucket))); err != nil {
		return err
	}
	retention := uint64(params.Reward.RewardAuditRetentionEpochs)
	if epoch > math.MaxUint64-retention {
		return fmt.Errorf("reward audit prune epoch overflows")
	}
	if err := k.RewardEpochPruneIndex.Set(
		ctx, types.NewRewardEpochPruneIndexKey(epoch+retention, epoch, uint64(rewardBucket)),
	); err != nil {
		return err
	}
	mustEmitHubEvent(ctx, &types.EventRewardEpochClosed{
		Epoch: epoch, EligibleTaskCount: 0, MarkCount: 0,
		PerformanceUpdateCount: 0, EmissionAmount: shared.NewAmount(0),
	})
	return nil
}

func (k Keeper) requireClosedRewardEpochWithoutCursor(
	ctx context.Context,
	epoch uint64,
	rewardBucket types.RewardBucket,
	competition types.RewardCompetitionEpochState,
	params types.HubParamsV2,
) error {
	boundariesHash, err := types.RewardOrderValueBucketBoundariesHash(
		sdk.UnwrapSDKContext(ctx).ChainID(), params.Reward.OrderValueBucketBoundaries,
	)
	if err != nil {
		return err
	}
	if err := validateCompetitionEpoch(
		competition, epoch, rewardBucket, params.Reward.OrderValueBucketBoundaries, boundariesHash,
	); err != nil {
		return err
	}
	if p30, err := shared.ParseAmount(competition.P30Cutoff); err != nil || p30 == 0 {
		return fmt.Errorf("closed reward competition has an invalid P30 cutoff")
	}
	if _, err := k.RewardEpochCursor.Get(ctx, types.NewRewardEpochCursorKey(epoch, uint64(rewardBucket))); err == nil {
		return fmt.Errorf("closed reward epoch retains a cursor")
	} else if !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	return nil
}

// ProcessDueRewardEpochPrunes advances the frozen retention schedule without
// ever creating or scanning BuilderContribution state. Starting a cursor,
// deleting the competition row and sealing the audit each cost one visit.
func (k Keeper) ProcessDueRewardEpochPrunes(
	ctx context.Context,
	currentEpoch uint64,
	currentHeight uint64,
	visitedLimit uint64,
) (uint64, error) {
	if visitedLimit == 0 {
		return 0, nil
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	var visited uint64
	for visited < visitedLimit {
		cursorKey, cursor, found, err := k.firstRewardEpochPruneCursor(cache)
		if err != nil {
			return visited, err
		}
		if found {
			if err := k.processRewardEpochPruneCursorStep(cache, sdkCtx.ChainID(), currentHeight, cursorKey, cursor); err != nil {
				return visited, err
			}
			visited++
			continue
		}

		indexKey, found, err := k.firstDueRewardEpochPruneIndex(cache, currentEpoch)
		if err != nil || !found {
			if err != nil {
				return visited, err
			}
			break
		}
		rewardBucket := types.RewardBucket(indexKey.K3())
		competitionKey := types.NewRewardCompetitionEpochKey(indexKey.K3(), indexKey.K2())
		competition, err := k.RewardCompetitionEpoch.Get(cache, competitionKey)
		if errors.Is(err, collections.ErrNotFound) {
			if err := k.RewardEpochPruneIndex.Remove(cache, indexKey); err != nil {
				return visited, err
			}
			visited++
			continue
		}
		if err != nil || !competition.RewardEpochClosed || competition.Epoch != indexKey.K2() || competition.RewardBucket != rewardBucket {
			return visited, fmt.Errorf("reward prune index does not match a closed competition")
		}
		cursor = types.RewardEpochPruneCursorState{
			SourceEpoch: indexKey.K2(), RewardBucket: rewardBucket,
			Phase:      types.RewardEpochPrunePhaseV1_REWARD_EPOCH_PRUNE_PHASE_V1_COMPETITION,
			FoldedRoot: types.RewardEpochAuditInitialRoot(),
		}
		if err := k.RewardEpochPruneIndex.Remove(cache, indexKey); err != nil {
			return visited, err
		}
		if err := k.RewardEpochPruneCursor.Set(
			cache, types.NewRewardEpochCursorKey(cursor.SourceEpoch, uint64(cursor.RewardBucket)), cursor,
		); err != nil {
			return visited, err
		}
		visited++
	}
	if visited != 0 {
		write()
	}
	return visited, nil
}

func (k Keeper) firstDueRewardEpochPruneIndex(ctx context.Context, currentEpoch uint64) (types.RewardEpochPruneIndexKeyTriple, bool, error) {
	iter, err := k.RewardEpochPruneIndex.Iterate(ctx, nil)
	if err != nil {
		return types.RewardEpochPruneIndexKeyTriple{}, false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return types.RewardEpochPruneIndexKeyTriple{}, false, nil
	}
	key, err := iter.Key()
	if err != nil || key.K1() > currentEpoch {
		return types.RewardEpochPruneIndexKeyTriple{}, false, err
	}
	if key.K3() > uint64(math.MaxInt32) || !validRewardBucket(types.RewardBucket(key.K3())) {
		return types.RewardEpochPruneIndexKeyTriple{}, false, fmt.Errorf("reward prune index bucket is invalid")
	}
	return key, true, nil
}

func (k Keeper) firstRewardEpochPruneCursor(ctx context.Context) (types.RewardEpochCursorKeyPair, types.RewardEpochPruneCursorState, bool, error) {
	iter, err := k.RewardEpochPruneCursor.Iterate(ctx, nil)
	if err != nil {
		return types.RewardEpochCursorKeyPair{}, types.RewardEpochPruneCursorState{}, false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return types.RewardEpochCursorKeyPair{}, types.RewardEpochPruneCursorState{}, false, nil
	}
	entry, err := iter.KeyValue()
	return entry.Key, entry.Value, err == nil, err
}

func (k Keeper) processRewardEpochPruneCursorStep(
	ctx context.Context,
	chainID string,
	currentHeight uint64,
	cursorKey types.RewardEpochCursorKeyPair,
	cursor types.RewardEpochPruneCursorState,
) error {
	if cursor.SourceEpoch != cursorKey.K1() || uint64(cursor.RewardBucket) != cursorKey.K2() ||
		!validRewardBucket(cursor.RewardBucket) || len(cursor.FoldedRoot) != 32 ||
		cursor.ContributionCount != 0 {
		return fmt.Errorf("reward prune cursor is invalid")
	}
	switch cursor.Phase {
	case types.RewardEpochPrunePhaseV1_REWARD_EPOCH_PRUNE_PHASE_V1_COMPETITION:
		competitionKey := types.NewRewardCompetitionEpochKey(uint64(cursor.RewardBucket), cursor.SourceEpoch)
		competition, err := k.RewardCompetitionEpoch.Get(ctx, competitionKey)
		if err != nil || !competition.RewardEpochClosed || competition.Epoch != cursor.SourceEpoch || competition.RewardBucket != cursor.RewardBucket {
			return fmt.Errorf("reward prune cursor has no matching closed competition")
		}
		primaryFrame := shared.FlatCanonicalFrameV1(
			shared.EnumBE(uint32(cursor.RewardBucket)), shared.Uint64BE(cursor.SourceEpoch),
		)
		stateFrame, err := rewardCompetitionAuditStateFrame(competition)
		if err != nil {
			return err
		}
		leaf, err := types.RewardEpochAuditLeafHashFramesV1(
			chainID, cursor.SourceEpoch, cursor.RewardBucket, cursor.Phase, primaryFrame, stateFrame,
		)
		if err != nil {
			return err
		}
		cursor.FoldedRoot, err = types.RewardEpochAuditFoldHash(
			chainID, cursor.SourceEpoch, cursor.RewardBucket, cursor.FoldedRoot, leaf,
		)
		if err != nil {
			return err
		}
		cursor.VisitedCount, err = checkedAdd(cursor.VisitedCount, 1)
		if err != nil {
			return err
		}
		cursor.DeletedCount, err = checkedAdd(cursor.DeletedCount, 1)
		if err != nil {
			return err
		}
		cursor.LastProcessedKey, err = primaryFrame.Bytes()
		if err != nil {
			return err
		}
		cursor.Phase = types.RewardEpochPrunePhaseV1_REWARD_EPOCH_PRUNE_PHASE_V1_CONTRIBUTIONS
		if err := k.RewardCompetitionEpoch.Remove(ctx, competitionKey); err != nil {
			return err
		}
		if err := k.rebuildRewardP30CutoffPointer(ctx, cursor.RewardBucket); err != nil {
			return err
		}
		return k.RewardEpochPruneCursor.Set(ctx, cursorKey, cursor)

	case types.RewardEpochPrunePhaseV1_REWARD_EPOCH_PRUNE_PHASE_V1_CONTRIBUTIONS:
		// Phase 0 has no BuilderContribution primary or fold. The phase remains
		// explicit so the frozen cursor numbering is exercised and auditable.
		counts := types.RewardEpochAuditCounts{}
		auditRoot, err := types.RewardEpochAuditRootHash(
			chainID, cursor.SourceEpoch, cursor.RewardBucket, cursor.FoldedRoot, counts,
		)
		if err != nil {
			return err
		}
		audit := types.RewardEpochAuditState{
			SourceEpoch: cursor.SourceEpoch, RewardBucket: cursor.RewardBucket,
			AuditRoot: auditRoot, FoldedRoot: append([]byte(nil), cursor.FoldedRoot...),
			CompletedHeight: currentHeight,
		}
		if err := k.RewardEpochAudit.Set(ctx, cursorKey, audit); err != nil {
			return err
		}
		return k.RewardEpochPruneCursor.Remove(ctx, cursorKey)

	default:
		return fmt.Errorf("reward prune cursor phase is invalid")
	}
}

func (k Keeper) rebuildRewardP30CutoffPointer(ctx context.Context, bucket types.RewardBucket) error {
	iter, err := k.RewardCompetitionEpoch.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer iter.Close()
	var latest types.RewardCompetitionEpochState
	found := false
	for ; iter.Valid(); iter.Next() {
		state, err := iter.Value()
		if err != nil {
			return err
		}
		if state.RewardBucket == bucket && state.RewardEpochClosed && (!found || state.Epoch > latest.Epoch) {
			latest, found = state, true
		}
	}
	if !found {
		if err := k.RewardP30CutoffPointer.Remove(ctx, uint64(bucket)); err != nil && !errors.Is(err, collections.ErrNotFound) {
			return err
		}
		return nil
	}
	return k.RewardP30CutoffPointer.Set(ctx, uint64(bucket), types.RewardP30CutoffPointerState{
		RewardBucket: latest.RewardBucket, CutoffEpoch: latest.Epoch, P30Cutoff: latest.P30Cutoff,
	})
}

func rewardCompetitionAuditStateFrame(state types.RewardCompetitionEpochState) (shared.CanonicalFrameV1, error) {
	histogram := make([]shared.CanonicalFrameV1, len(state.OrderValueHist))
	for index, count := range state.OrderValueHist {
		histogram[index] = shared.FlatCanonicalFrameV1(shared.Uint64BE(count))
	}
	cumulative, err := shared.CanonicalAmountFrameV1(state.CumulativeFeeAmount)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	p30, err := shared.CanonicalAmountFrameV1(state.P30Cutoff)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	frame := shared.NewCanonicalFrameBuilderV1().
		Raw(shared.Uint64BE(state.Epoch), shared.EnumBE(uint32(state.RewardBucket))).
		Nested(shared.CanonicalRepeatedFramesV1(histogram)).
		Raw(state.OrderValueBucketBoundariesHash, shared.Uint64BE(state.TotalTasks),
			shared.Uint64BE(state.UniquePayerCount), shared.Uint64BE(state.UniqueWorkerCount)).
		Nested(cumulative).
		Raw(shared.EnumBE(uint32(state.BootstrapStatus))).
		Nested(p30).
		Raw(shared.BoolByte(state.RewardEpochClosed)).
		Build()
	if err := frame.Err(); err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	return frame, nil
}

func validatePhase0RewardCursor(cursor types.RewardEpochCursorState, epoch uint64, rewardBucket types.RewardBucket) error {
	if cursor.Epoch != epoch || cursor.RewardBucket != rewardBucket {
		return fmt.Errorf("reward epoch cursor scope mismatch")
	}
	if cursor.Phase != types.RewardEpochPhase_REWARD_EPOCH_PHASE_HISTOGRAM_OPEN &&
		cursor.Phase != types.RewardEpochPhase_REWARD_EPOCH_PHASE_CUTOFF_DERIVED {
		return fmt.Errorf("reward epoch cursor phase is not supported in Phase 0")
	}
	if cursor.LastOrderValueBucket != 0 || len(cursor.LastTaskId) != 0 {
		return fmt.Errorf("Phase 0 reward cursor must not retain eligibility scan state")
	}
	return nil
}

func validateCompletedRewardAudit(audit types.RewardEpochAuditState, epoch uint64, rewardBucket types.RewardBucket) error {
	if audit.SourceEpoch != epoch || audit.RewardBucket != rewardBucket || len(audit.AuditRoot) != 32 ||
		len(audit.FoldedRoot) != 32 || audit.CompletedHeight == 0 {
		return fmt.Errorf("reward epoch audit scope is invalid")
	}
	if audit.EligibleTaskCount != 0 || audit.MarkCount != 0 || audit.AccrualCount != 0 ||
		audit.TotalCount != audit.ContributionCount {
		return fmt.Errorf("reward epoch audit contains non-Phase 0 counts")
	}
	return nil
}

func (k Keeper) advanceRewardP30CutoffPointer(ctx context.Context, competition types.RewardCompetitionEpochState) error {
	if !competition.RewardEpochClosed || !validRewardBucket(competition.RewardBucket) {
		return fmt.Errorf("only a closed reward competition can advance the P30 pointer")
	}
	if value, err := shared.ParseAmount(competition.P30Cutoff); err != nil || value == 0 {
		return fmt.Errorf("closed reward competition has an invalid P30 cutoff")
	}
	key := uint64(competition.RewardBucket)
	existing, err := k.RewardP30CutoffPointer.Get(ctx, key)
	if err == nil {
		if existing.RewardBucket != competition.RewardBucket {
			return fmt.Errorf("P30 pointer key and state bucket disagree")
		}
		if existing.CutoffEpoch > competition.Epoch {
			return nil
		}
		if existing.CutoffEpoch == competition.Epoch {
			if existing.P30Cutoff != competition.P30Cutoff {
				return fmt.Errorf("P30 pointer conflicts with its closed competition")
			}
			return nil
		}
	} else if !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	return k.RewardP30CutoffPointer.Set(ctx, key, types.RewardP30CutoffPointerState{
		RewardBucket: competition.RewardBucket,
		CutoffEpoch:  competition.Epoch,
		P30Cutoff:    competition.P30Cutoff,
	})
}

func rewardEpochCursorEqual(a, b types.RewardEpochCursorState) bool {
	return a.Epoch == b.Epoch && a.RewardBucket == b.RewardBucket && a.Phase == b.Phase &&
		a.LastOrderValueBucket == b.LastOrderValueBucket && bytes.Equal(a.LastTaskId, b.LastTaskId) &&
		a.VisitedCount == b.VisitedCount && a.AppliedCount == b.AppliedCount
}

func (k Keeper) deriveAndStoreRewardCutoffs(
	ctx context.Context,
	chainID string,
	epoch uint64,
	rewardBucket types.RewardBucket,
	params types.HubParamsV2,
) (types.RewardCompetitionEpochState, error) {
	boundariesHash, err := types.RewardOrderValueBucketBoundariesHash(chainID, params.Reward.OrderValueBucketBoundaries)
	if err != nil {
		return types.RewardCompetitionEpochState{}, err
	}
	competition, err := k.getOrCreateRewardCompetitionEpoch(
		ctx, epoch, rewardBucket, params.Reward.OrderValueBucketBoundaries, boundariesHash,
	)
	if err != nil {
		return types.RewardCompetitionEpochState{}, err
	}
	if competition.RewardEpochClosed {
		return types.RewardCompetitionEpochState{}, fmt.Errorf("cannot derive a closed reward competition")
	}
	competition, err = DeriveRewardCompetitionCutoffs(
		competition, params.Reward.OrderValueBucketBoundaries,
		params.Reward.P30BootstrapOrderValueFloor, params.Reward.MinEpochSample,
	)
	if err != nil {
		return types.RewardCompetitionEpochState{}, err
	}
	if err := k.RewardCompetitionEpoch.Set(
		ctx, types.NewRewardCompetitionEpochKey(uint64(rewardBucket), epoch), competition,
	); err != nil {
		return types.RewardCompetitionEpochState{}, err
	}
	return competition, nil
}

func (k Keeper) scheduleRewardEpoch(
	ctx context.Context,
	epoch uint64,
	rewardBucket types.RewardBucket,
	params types.HubParamsV2,
) error {
	if !validRewardBucket(rewardBucket) {
		return fmt.Errorf("reward bucket is invalid")
	}
	_, epochEnd, err := epochHeightRange(epoch, normalizedEpochLengthBlocks(params))
	if err != nil || epochEnd == math.MaxUint64 {
		return fmt.Errorf("reward epoch due height overflows")
	}
	return k.RewardEpochIndex.Set(ctx, types.NewRewardEpochIndexKey(epochEnd+1, epoch, uint64(rewardBucket)))
}
