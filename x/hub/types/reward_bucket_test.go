package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

// parameter_table.md line 143 and model_registration_chain_structure_and_manifest.md §475/§710 freeze this table and
// forbid deriving it from the enum values. The table is asserted literally so a
// later enum edit on either side cannot quietly re-bucket every profile.
func TestRewardBucketMappingIsTheFrozenTable(t *testing.T) {
	for tier, want := range map[uint32]hubtypes.RewardBucket{
		1: hubtypes.RewardBucket_REWARD_BUCKET_P0,
		2: hubtypes.RewardBucket_REWARD_BUCKET_P1,
		3: hubtypes.RewardBucket_REWARD_BUCKET_P2,
		4: hubtypes.RewardBucket_REWARD_BUCKET_P3,
		5: hubtypes.RewardBucket_REWARD_BUCKET_P4,
	} {
		got, err := hubtypes.RewardBucketForResourceTier(tier)
		require.NoErrorf(t, err, "tier %d", tier)
		require.Equalf(t, want, got, "tier %d", tier)
	}
}

// Registration rejects tiers outside 1..5, so anything else reaching the mapping
// is a corrupted row rather than a value to guess a bucket for.
func TestRewardBucketMappingRejectsUnregisteredTiers(t *testing.T) {
	for _, tier := range []uint32{0, 6, 7, 255} {
		_, err := hubtypes.RewardBucketForResourceTier(tier)
		require.Errorf(t, err, "tier %d must not map to any bucket", tier)
	}
}

func TestIsRegisteredRewardBucketExcludesUnspecified(t *testing.T) {
	require.False(t, hubtypes.IsRegisteredRewardBucket(hubtypes.RewardBucket_REWARD_BUCKET_UNSPECIFIED))
	for _, bucket := range []hubtypes.RewardBucket{
		hubtypes.RewardBucket_REWARD_BUCKET_P0, hubtypes.RewardBucket_REWARD_BUCKET_P1,
		hubtypes.RewardBucket_REWARD_BUCKET_P2, hubtypes.RewardBucket_REWARD_BUCKET_P3,
		hubtypes.RewardBucket_REWARD_BUCKET_P4,
	} {
		require.True(t, hubtypes.IsRegisteredRewardBucket(bucket))
	}
}
