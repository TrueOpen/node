package types

import (
	"bytes"
	"fmt"
	"math"

	shared "github.com/TrueOpen/node/x/shared/types"
)

func rewardStateKey(epoch uint64, bucket RewardBucket) string {
	return fmt.Sprintf("%d/%d", epoch, bucket)
}

func validateRewardCompetitionState(state RewardCompetitionEpochState, params RewardParamsV1) error {
	if !IsRegisteredRewardBucket(state.RewardBucket) || len(state.OrderValueHist) != len(params.OrderValueBucketBoundaries)+1 ||
		len(state.OrderValueBucketBoundariesHash) != shared.Hash32KeySize {
		return fmt.Errorf("reward competition scope or histogram is invalid")
	}
	var total uint64
	for _, count := range state.OrderValueHist {
		if math.MaxUint64-total < count {
			return fmt.Errorf("reward competition histogram overflows")
		}
		total += count
	}
	if total != state.TotalTasks || state.UniquePayerCount > total || state.UniqueWorkerCount > total {
		return fmt.Errorf("reward competition counters are invalid")
	}
	if _, err := shared.ParseAmount(state.CumulativeFeeAmount); err != nil {
		return err
	}
	p30, err := shared.ParseAmount(state.P30Cutoff)
	if err != nil || state.RewardEpochClosed && p30 == 0 {
		return fmt.Errorf("closed reward competition requires a P30 cutoff")
	}
	return nil
}

// ValidateRewardGenesisState checks exported primary/cursor coverage. Derived
// due indexes and P30 pointers are rebuilt and checked by the keeper because
// their hashes and heights require the chain context.
func ValidateRewardGenesisState(gs GenesisState) error {
	competitions := make(map[string]RewardCompetitionEpochState, len(gs.RewardCompetitionEpochs))
	for _, state := range gs.RewardCompetitionEpochs {
		if err := validateRewardCompetitionState(state, gs.Params.Reward); err != nil {
			return err
		}
		key := rewardStateKey(state.Epoch, state.RewardBucket)
		if _, duplicate := competitions[key]; duplicate {
			return fmt.Errorf("duplicate reward competition %s", key)
		}
		competitions[key] = state
	}

	runners := make(map[string]RewardEpochCursorState, len(gs.RewardEpochCursors))
	for _, cursor := range gs.RewardEpochCursors {
		key := rewardStateKey(cursor.Epoch, cursor.RewardBucket)
		competition, exists := competitions[key]
		if !exists || competition.RewardEpochClosed || !IsRegisteredRewardBucket(cursor.RewardBucket) ||
			cursor.Phase != RewardEpochPhase_REWARD_EPOCH_PHASE_HISTOGRAM_OPEN && cursor.Phase != RewardEpochPhase_REWARD_EPOCH_PHASE_CUTOFF_DERIVED ||
			cursor.LastOrderValueBucket != 0 || len(cursor.LastTaskId) != 0 {
			return fmt.Errorf("reward epoch cursor %s is invalid", key)
		}
		p30, _ := shared.ParseAmount(competition.P30Cutoff)
		if cursor.Phase == RewardEpochPhase_REWARD_EPOCH_PHASE_HISTOGRAM_OPEN && p30 != 0 ||
			cursor.Phase == RewardEpochPhase_REWARD_EPOCH_PHASE_CUTOFF_DERIVED && p30 == 0 {
			return fmt.Errorf("reward epoch cursor %s disagrees with its cutoff state", key)
		}
		if _, duplicate := runners[key]; duplicate {
			return fmt.Errorf("duplicate reward epoch cursor %s", key)
		}
		runners[key] = cursor
	}

	pruneIndexes := make(map[string]RewardEpochPruneIndex, len(gs.RewardEpochPruneIndexes))
	for _, index := range gs.RewardEpochPruneIndexes {
		key := rewardStateKey(index.SourceEpoch, index.RewardBucket)
		competition, exists := competitions[key]
		if !exists || !competition.RewardEpochClosed || !IsRegisteredRewardBucket(index.RewardBucket) || index.PruneEpoch <= index.SourceEpoch {
			return fmt.Errorf("reward prune index %s is invalid", key)
		}
		if _, duplicate := pruneIndexes[key]; duplicate {
			return fmt.Errorf("duplicate reward prune index %s", key)
		}
		pruneIndexes[key] = index
	}

	pruneCursors := make(map[string]RewardEpochPruneCursorState, len(gs.RewardEpochPruneCursors))
	for _, cursor := range gs.RewardEpochPruneCursors {
		key := rewardStateKey(cursor.SourceEpoch, cursor.RewardBucket)
		if !IsRegisteredRewardBucket(cursor.RewardBucket) || len(cursor.FoldedRoot) != shared.Hash32KeySize ||
			cursor.ContributionCount != 0 || cursor.DeletedCount > cursor.VisitedCount {
			return fmt.Errorf("reward prune cursor %s is invalid", key)
		}
		switch cursor.Phase {
		case RewardEpochPrunePhaseV1_REWARD_EPOCH_PRUNE_PHASE_V1_COMPETITION:
			competition, exists := competitions[key]
			if !exists || !competition.RewardEpochClosed || cursor.VisitedCount != 0 || cursor.DeletedCount != 0 ||
				len(cursor.LastProcessedKey) != 0 || !bytes.Equal(cursor.FoldedRoot, RewardEpochAuditInitialRoot()) {
				return fmt.Errorf("reward competition prune cursor %s is invalid", key)
			}
		case RewardEpochPrunePhaseV1_REWARD_EPOCH_PRUNE_PHASE_V1_CONTRIBUTIONS:
			if _, exists := competitions[key]; exists || cursor.VisitedCount != 1 || cursor.DeletedCount != 1 || len(cursor.LastProcessedKey) == 0 {
				return fmt.Errorf("reward contribution prune cursor %s is invalid", key)
			}
		default:
			return fmt.Errorf("reward prune cursor %s has an invalid phase", key)
		}
		if _, duplicate := pruneCursors[key]; duplicate {
			return fmt.Errorf("duplicate reward prune cursor %s", key)
		}
		if _, indexed := pruneIndexes[key]; indexed {
			return fmt.Errorf("reward prune index and cursor overlap for %s", key)
		}
		pruneCursors[key] = cursor
	}

	audits := make(map[string]struct{}, len(gs.RewardEpochAudits))
	for _, audit := range gs.RewardEpochAudits {
		key := rewardStateKey(audit.SourceEpoch, audit.RewardBucket)
		if !IsRegisteredRewardBucket(audit.RewardBucket) || len(audit.AuditRoot) != shared.Hash32KeySize ||
			len(audit.FoldedRoot) != shared.Hash32KeySize || audit.CompletedHeight == 0 ||
			audit.EligibleTaskCount != 0 || audit.MarkCount != 0 || audit.AccrualCount != 0 ||
			audit.ContributionCount != 0 || audit.TotalCount != 0 {
			return fmt.Errorf("reward epoch audit %s is invalid", key)
		}
		if _, duplicate := audits[key]; duplicate {
			return fmt.Errorf("duplicate reward epoch audit %s", key)
		}
		if _, exists := competitions[key]; exists {
			return fmt.Errorf("reward audit %s retains its competition", key)
		}
		if _, exists := pruneIndexes[key]; exists {
			return fmt.Errorf("reward audit %s retains its prune index", key)
		}
		if _, exists := pruneCursors[key]; exists {
			return fmt.Errorf("reward audit %s retains its prune cursor", key)
		}
		audits[key] = struct{}{}
	}

	for key, competition := range competitions {
		if !competition.RewardEpochClosed {
			continue
		}
		_, indexed := pruneIndexes[key]
		_, running := pruneCursors[key]
		if indexed == running {
			return fmt.Errorf("closed reward competition %s must have exactly one prune index or cursor", key)
		}
	}
	return nil
}
