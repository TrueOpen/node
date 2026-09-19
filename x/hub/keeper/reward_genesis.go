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
)

func (k Keeper) initRewardGenesis(ctx context.Context, genesis types.GenesisState) error {
	latestClosed := make(map[types.RewardBucket]types.RewardCompetitionEpochState)
	for _, state := range genesis.RewardCompetitionEpochs {
		key := types.NewRewardCompetitionEpochKey(uint64(state.RewardBucket), state.Epoch)
		if err := k.RewardCompetitionEpoch.Set(ctx, key, state); err != nil {
			return err
		}
		if !state.RewardEpochClosed {
			if err := k.scheduleRewardEpoch(ctx, state.Epoch, state.RewardBucket, genesis.Params); err != nil {
				return err
			}
		}
		previous, exists := latestClosed[state.RewardBucket]
		if state.RewardEpochClosed && (!exists || state.Epoch > previous.Epoch) {
			latestClosed[state.RewardBucket] = state
		}
	}
	for _, state := range latestClosed {
		pointer := types.RewardP30CutoffPointerState{
			RewardBucket: state.RewardBucket, CutoffEpoch: state.Epoch, P30Cutoff: state.P30Cutoff,
		}
		if err := k.RewardP30CutoffPointer.Set(ctx, uint64(state.RewardBucket), pointer); err != nil {
			return err
		}
	}
	for _, state := range genesis.RewardEpochCursors {
		key := types.NewRewardEpochCursorKey(state.Epoch, uint64(state.RewardBucket))
		if err := k.RewardEpochCursor.Set(ctx, key, state); err != nil {
			return err
		}
	}
	for _, state := range genesis.RewardEpochPruneIndexes {
		key := types.NewRewardEpochPruneIndexKey(state.PruneEpoch, state.SourceEpoch, uint64(state.RewardBucket))
		if err := k.RewardEpochPruneIndex.Set(ctx, key); err != nil {
			return err
		}
	}
	for _, state := range genesis.RewardEpochPruneCursors {
		key := types.NewRewardEpochCursorKey(state.SourceEpoch, uint64(state.RewardBucket))
		if err := k.RewardEpochPruneCursor.Set(ctx, key, state); err != nil {
			return err
		}
	}
	for _, state := range genesis.RewardEpochAudits {
		key := types.NewRewardEpochCursorKey(state.SourceEpoch, uint64(state.RewardBucket))
		if err := k.RewardEpochAudit.Set(ctx, key, state); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) exportRewardEpochPruneIndexes(ctx context.Context) ([]types.RewardEpochPruneIndex, error) {
	iter, err := k.RewardEpochPruneIndex.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()

	rows := []types.RewardEpochPruneIndex{}
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		rows = append(rows, types.RewardEpochPruneIndex{
			PruneEpoch: key.K1(), SourceEpoch: key.K2(), RewardBucket: types.RewardBucket(key.K3()),
		})
	}
	return rows, nil
}

func (k Keeper) EnsureRewardEpochInvariant(ctx context.Context) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	competitions, err := collectMapValues[types.HardwareTierEpochKey, types.RewardCompetitionEpochState](ctx, k.RewardCompetitionEpoch)
	if err != nil {
		return err
	}
	runners, err := collectMapValues[types.RewardEpochCursorKeyPair, types.RewardEpochCursorState](ctx, k.RewardEpochCursor)
	if err != nil {
		return err
	}
	pruneIndexes, err := k.exportRewardEpochPruneIndexes(ctx)
	if err != nil {
		return err
	}
	pruneCursors, err := collectMapValues[types.RewardEpochCursorKeyPair, types.RewardEpochPruneCursorState](ctx, k.RewardEpochPruneCursor)
	if err != nil {
		return err
	}
	audits, err := collectMapValues[types.RewardEpochCursorKeyPair, types.RewardEpochAuditState](ctx, k.RewardEpochAudit)
	if err != nil {
		return err
	}
	if err := types.ValidateRewardGenesisState(types.GenesisState{
		Params: params, RewardCompetitionEpochs: competitions, RewardEpochCursors: runners,
		RewardEpochPruneIndexes: pruneIndexes, RewardEpochPruneCursors: pruneCursors, RewardEpochAudits: audits,
	}); err != nil {
		return err
	}

	chainID := sdk.UnwrapSDKContext(ctx).ChainID()
	latest := make(map[types.RewardBucket]types.RewardCompetitionEpochState)
	for _, competition := range competitions {
		boundariesHash, err := types.RewardOrderValueBucketBoundariesHash(chainID, params.Reward.OrderValueBucketBoundaries)
		if err != nil || !bytes.Equal(boundariesHash, competition.OrderValueBucketBoundariesHash) {
			return fmt.Errorf("reward competition boundaries hash mismatch")
		}
		_, epochEnd, err := epochHeightRange(competition.Epoch, normalizedEpochLengthBlocks(params))
		if err != nil || epochEnd == math.MaxUint64 {
			return fmt.Errorf("reward competition epoch range is invalid")
		}
		dueKey := types.NewRewardEpochIndexKey(epochEnd+1, competition.Epoch, uint64(competition.RewardBucket))
		hasDue, err := k.RewardEpochIndex.Has(ctx, dueKey)
		if err != nil || hasDue == competition.RewardEpochClosed {
			return fmt.Errorf("reward competition due-index coverage is invalid")
		}
		if competition.RewardEpochClosed {
			if current, ok := latest[competition.RewardBucket]; !ok || competition.Epoch > current.Epoch {
				latest[competition.RewardBucket] = competition
			}
		}
	}
	dueIndexes, err := k.RewardEpochIndex.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; dueIndexes.Valid(); dueIndexes.Next() {
		key, err := dueIndexes.Key()
		if err != nil {
			dueIndexes.Close()
			return err
		}
		competition, err := k.RewardCompetitionEpoch.Get(ctx, types.NewRewardCompetitionEpochKey(key.K3(), key.K2()))
		if err != nil || competition.RewardEpochClosed || competition.Epoch != key.K2() || uint64(competition.RewardBucket) != key.K3() {
			dueIndexes.Close()
			return fmt.Errorf("reward due index does not match an open competition")
		}
	}
	if err := dueIndexes.Close(); err != nil {
		return err
	}

	pointers, err := k.RewardP30CutoffPointer.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	seenPointers := make(map[types.RewardBucket]struct{})
	for ; pointers.Valid(); pointers.Next() {
		entry, err := pointers.KeyValue()
		if err != nil {
			pointers.Close()
			return err
		}
		expected, ok := latest[entry.Value.RewardBucket]
		if !ok || entry.Key != uint64(entry.Value.RewardBucket) || entry.Value.CutoffEpoch != expected.Epoch ||
			entry.Value.P30Cutoff != expected.P30Cutoff {
			pointers.Close()
			return fmt.Errorf("reward P30 pointer does not match the latest retained competition")
		}
		seenPointers[entry.Value.RewardBucket] = struct{}{}
	}
	if err := pointers.Close(); err != nil {
		return err
	}
	if len(seenPointers) != len(latest) {
		return fmt.Errorf("reward P30 pointer coverage is incomplete")
	}

	for _, audit := range audits {
		root, err := types.RewardEpochAuditRootHash(
			chainID, audit.SourceEpoch, audit.RewardBucket, audit.FoldedRoot, types.RewardEpochAuditCounts{},
		)
		if err != nil || !bytes.Equal(root, audit.AuditRoot) {
			return fmt.Errorf("reward epoch audit root is invalid")
		}
	}
	return k.ensureRewardPruneIndexReverseCoverage(ctx)
}

func (k Keeper) ensureRewardPruneIndexReverseCoverage(ctx context.Context) error {
	indexes, err := k.RewardEpochPruneIndex.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer indexes.Close()
	for ; indexes.Valid(); indexes.Next() {
		key, err := indexes.Key()
		if err != nil {
			return err
		}
		competition, err := k.RewardCompetitionEpoch.Get(ctx, types.NewRewardCompetitionEpochKey(key.K3(), key.K2()))
		if err != nil || !competition.RewardEpochClosed || competition.Epoch != key.K2() || uint64(competition.RewardBucket) != key.K3() {
			return fmt.Errorf("reward prune index does not match a closed competition")
		}
		if _, err := k.RewardEpochPruneCursor.Get(ctx, types.NewRewardEpochCursorKey(key.K2(), key.K3())); err == nil {
			return fmt.Errorf("reward prune index overlaps its cursor")
		} else if !errors.Is(err, collections.ErrNotFound) {
			return err
		}
	}
	return nil
}
