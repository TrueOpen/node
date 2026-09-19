package types

import (
	"fmt"
	"math"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// RewardOrderValueBucketBoundariesHash commits an ordered, strictly increasing
// list of canonical atomic-unit boundaries. Each item is framed separately so
// the commitment remains language-neutral and unambiguous.
func RewardOrderValueBucketBoundariesHash(chainID string, boundaries []shared.Amount) ([]byte, error) {
	if chainID == "" {
		return nil, fmt.Errorf("chain id is required")
	}
	if len(boundaries) == 0 {
		return nil, fmt.Errorf("reward bucket boundaries are required")
	}
	if len(boundaries) > math.MaxUint32 {
		return nil, fmt.Errorf("reward bucket boundary count exceeds uint32")
	}

	boundaryFrames := make([]shared.CanonicalFrameV1, len(boundaries))
	var previous uint64
	for i, boundary := range boundaries {
		value, err := shared.ParseAmount(boundary)
		if err != nil {
			return nil, fmt.Errorf("reward bucket boundary %d: %w", i, err)
		}
		if i > 0 && value <= previous {
			return nil, fmt.Errorf("reward bucket boundaries must be strictly increasing")
		}
		previous = value
		amountFrame, err := shared.CanonicalAmountFrameV1(boundary)
		if err != nil {
			return nil, fmt.Errorf("reward bucket boundary %d amount frame: %w", i, err)
		}
		boundaryFrames[i] = shared.NewCanonicalFrameBuilderV1().
			Raw(shared.Uint32BE(uint32(i))).
			Nested(amountFrame).
			Build()
	}
	repeatedBoundaries := shared.CanonicalRepeatedFramesV1(boundaryFrames)
	if err := repeatedBoundaries.Err(); err != nil {
		return nil, fmt.Errorf("reward bucket boundaries: %w", err)
	}
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainRewardBucketBoundariesV1)).Raw(
		[]byte(chainID),
		shared.Uint32BE(uint32(len(boundaries))),
	).Nested(repeatedBoundaries).Sum()
}
