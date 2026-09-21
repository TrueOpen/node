package types

import "fmt"

// MinProfileResourceTier / MaxProfileResourceTier are the §2 registration bounds
// on `profile_resource_tier`. Registration rejects anything outside them, so a
// stored ProfileState can always be mapped.
const (
	MinProfileResourceTier uint32 = 1
	MaxProfileResourceTier uint32 = 5
)

// RewardBucketForResourceTier is the frozen `profile_resource_tier -> RewardBucket`
// mapping and the model registration contract:
//
//	1->P0, 2->P1, 3->P2, 4->P3, 5->P4
//
// The table is written out rather than derived from the enum's numeric values.
// Both documents say so explicitly — "it must not be inferred from the enums
// coincidentally sharing values" — because the two
// enums happening to share a numbering today is a coincidence of their current
// definitions, not a protocol rule, and an arithmetic shortcut would silently
// re-map every profile the moment either enum gains a member.
func RewardBucketForResourceTier(resourceTier uint32) (RewardBucket, error) {
	switch resourceTier {
	case 1:
		return RewardBucket_REWARD_BUCKET_P0, nil
	case 2:
		return RewardBucket_REWARD_BUCKET_P1, nil
	case 3:
		return RewardBucket_REWARD_BUCKET_P2, nil
	case 4:
		return RewardBucket_REWARD_BUCKET_P3, nil
	case 5:
		return RewardBucket_REWARD_BUCKET_P4, nil
	default:
		return RewardBucket_REWARD_BUCKET_UNSPECIFIED,
			fmt.Errorf("profile_resource_tier %d is outside the registered range %d..%d",
				resourceTier, MinProfileResourceTier, MaxProfileResourceTier)
	}
}

// IsRegisteredRewardBucket reports whether a bucket is one of the five real
// competition buckets. UNSPECIFIED is never a bucket a task can be scored in.
func IsRegisteredRewardBucket(bucket RewardBucket) bool {
	switch bucket {
	case RewardBucket_REWARD_BUCKET_P0, RewardBucket_REWARD_BUCKET_P1,
		RewardBucket_REWARD_BUCKET_P2, RewardBucket_REWARD_BUCKET_P3,
		RewardBucket_REWARD_BUCKET_P4:
		return true
	default:
		return false
	}
}
