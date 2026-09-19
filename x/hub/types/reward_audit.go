package types

import (
	"bytes"
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
)

const rewardAuditHashSize = 32

// RewardEpochAuditLeafHash commits one canonical primary/state pair before it
// is removed by the bounded reward retention runner.
//
// Deprecated: node production uses RewardEpochAuditLeafHashFramesV1 so nested
// primary/state provenance reaches the depth guard. This raw wrapper remains
// temporarily for external source compatibility: the frames arrive as opaque
// bytes, so they can only be framed at depth zero and the caller keeps the
// depth guard it would have got from the typed entry point.
func RewardEpochAuditLeafHash(
	chainID string,
	sourceEpoch uint64,
	rewardBucket RewardBucket,
	phase RewardEpochPrunePhaseV1,
	primaryKeyFrame []byte,
	stateFrame []byte,
) ([]byte, error) {
	if err := validateRewardEpochAuditLeafScope(chainID, rewardBucket, phase); err != nil {
		return nil, err
	}
	if len(primaryKeyFrame) == 0 || len(stateFrame) == 0 {
		return nil, fmt.Errorf("reward audit primary and state frames are required")
	}
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainRewardEpochAuditLeafV1)).Raw(
		[]byte(chainID),
		shared.Uint64BE(sourceEpoch),
		shared.EnumBE(uint32(rewardBucket)),
		shared.EnumBE(uint32(phase)),
		primaryKeyFrame,
		stateFrame,
	).Sum()
}

// RewardEpochAuditLeafHashFramesV1 is the node-internal typed path. It retains
// primary/state nesting provenance through the leaf root.
func RewardEpochAuditLeafHashFramesV1(
	chainID string,
	sourceEpoch uint64,
	rewardBucket RewardBucket,
	phase RewardEpochPrunePhaseV1,
	primaryKeyFrame shared.CanonicalFrameV1,
	stateFrame shared.CanonicalFrameV1,
) ([]byte, error) {
	if err := validateRewardEpochAuditLeafScope(chainID, rewardBucket, phase); err != nil {
		return nil, err
	}
	primaryLength, err := primaryKeyFrame.Len()
	if err != nil {
		return nil, err
	}
	stateLength, err := stateFrame.Len()
	if err != nil {
		return nil, err
	}
	if primaryLength == 0 || stateLength == 0 {
		return nil, fmt.Errorf("reward audit primary and state frames are required")
	}
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainRewardEpochAuditLeafV1)).Raw(
		[]byte(chainID),
		shared.Uint64BE(sourceEpoch),
		shared.EnumBE(uint32(rewardBucket)),
		shared.EnumBE(uint32(phase)),
	).Nested(primaryKeyFrame, stateFrame).Sum()
}

func validateRewardEpochAuditLeafScope(chainID string, rewardBucket RewardBucket, phase RewardEpochPrunePhaseV1) error {
	if chainID == "" {
		return fmt.Errorf("chain id is required")
	}
	if rewardBucket < RewardBucket_REWARD_BUCKET_P0 || rewardBucket > RewardBucket_REWARD_BUCKET_P4 {
		return fmt.Errorf("reward bucket is invalid")
	}
	if phase < RewardEpochPrunePhaseV1_REWARD_EPOCH_PRUNE_PHASE_V1_COMPETITION ||
		phase > RewardEpochPrunePhaseV1_REWARD_EPOCH_PRUNE_PHASE_V1_CONTRIBUTIONS {
		return fmt.Errorf("reward audit phase is invalid")
	}
	return nil
}

// RewardEpochAuditFoldHash appends one leaf to the running commitment. The
// first call uses RewardEpochAuditInitialRoot as previousRoot.
func RewardEpochAuditFoldHash(
	chainID string,
	sourceEpoch uint64,
	rewardBucket RewardBucket,
	previousRoot []byte,
	leafHash []byte,
) ([]byte, error) {
	if chainID == "" {
		return nil, fmt.Errorf("chain id is required")
	}
	if rewardBucket < RewardBucket_REWARD_BUCKET_P0 || rewardBucket > RewardBucket_REWARD_BUCKET_P4 {
		return nil, fmt.Errorf("reward bucket is invalid")
	}
	if len(previousRoot) != rewardAuditHashSize || len(leafHash) != rewardAuditHashSize {
		return nil, fmt.Errorf("reward audit roots must be Hash32")
	}
	if bytes.Equal(leafHash, make([]byte, rewardAuditHashSize)) {
		return nil, fmt.Errorf("reward audit leaf hash must be non-zero")
	}
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainRewardEpochAuditFoldV1)).Raw(
		[]byte(chainID),
		shared.Uint64BE(sourceEpoch),
		shared.EnumBE(uint32(rewardBucket)),
		previousRoot,
		leafHash,
	).Sum()
}

func RewardEpochAuditInitialRoot() []byte {
	return make([]byte, rewardAuditHashSize)
}

type RewardEpochAuditCounts struct {
	EligibleTasks uint64
	Marks         uint64
	Accruals      uint64
	Contributions uint64
}

func (c RewardEpochAuditCounts) Total() (uint64, error) {
	total := c.EligibleTasks
	for _, value := range []uint64{c.Marks, c.Accruals, c.Contributions} {
		if value > ^uint64(0)-total {
			return 0, fmt.Errorf("reward audit count overflows")
		}
		total += value
	}
	return total, nil
}

// RewardEpochAuditRootHash seals the final fold with per-phase counts. This
// summary is retained after the source bodies and cursor are deleted.
func RewardEpochAuditRootHash(
	chainID string,
	sourceEpoch uint64,
	rewardBucket RewardBucket,
	foldedRoot []byte,
	counts RewardEpochAuditCounts,
) ([]byte, error) {
	if chainID == "" {
		return nil, fmt.Errorf("chain id is required")
	}
	if rewardBucket < RewardBucket_REWARD_BUCKET_P0 || rewardBucket > RewardBucket_REWARD_BUCKET_P4 {
		return nil, fmt.Errorf("reward bucket is invalid")
	}
	if len(foldedRoot) != rewardAuditHashSize {
		return nil, fmt.Errorf("folded root must be Hash32")
	}
	total, err := counts.Total()
	if err != nil {
		return nil, err
	}
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainRewardEpochAuditRootV1)).Raw(
		[]byte(chainID),
		shared.Uint64BE(sourceEpoch),
		shared.EnumBE(uint32(rewardBucket)),
		foldedRoot,
		shared.Uint64BE(counts.EligibleTasks),
		shared.Uint64BE(counts.Marks),
		shared.Uint64BE(counts.Accruals),
		shared.Uint64BE(counts.Contributions),
		shared.Uint64BE(total),
	).Sum()
}
