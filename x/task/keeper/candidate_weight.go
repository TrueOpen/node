package keeper

import (
	"fmt"
	"math/bits"

	"github.com/TrueOpen/node/x/task/types"
)

func candidateJailFactorPpm(jailCount uint64) uint32 {
	switch jailCount {
	case 0:
		return 1_000_000
	case 1:
		return 500_000
	case 2:
		return 250_000
	default:
		return 0
	}
}

func workerCandidateWeightPpm(activeBond, minStake uint64, performance, jailFactor uint32) (uint32, error) {
	if activeBond == 0 || minStake == 0 || performance == 0 || performance > 1_000_000 || jailFactor == 0 || jailFactor > 1_000_000 {
		return 0, fmt.Errorf("candidate weight inputs are invalid")
	}
	stakeScore := stakeScorePpm(activeBond, minStake)
	weighted := (stakeScore*30 + uint64(performance)*70) / 100
	weight := weighted * uint64(jailFactor) / 1_000_000
	if weight == 0 || weight > uint64(types.MaxAssignmentCandidateWeight) {
		return 0, fmt.Errorf("candidate weight is outside the registered range")
	}
	return uint32(weight), nil
}

// stakeScorePpm computes floor(activeBond * 1e6 / (3 * minStake)), capped at
// 1e6, without ever materializing the potentially overflowing denominator.
func stakeScorePpm(activeBond, minStake uint64) uint64 {
	if activeBond == 0 || minStake == 0 {
		return 0
	}
	if minStake <= ^uint64(0)/3 && activeBond >= minStake*3 {
		return 1_000_000
	}
	high, low := bits.Mul64(activeBond, 1_000_000)
	// activeBond < 3*minStake here, so high is strictly below minStake and
	// bits.Div64 cannot overflow its uint64 quotient.
	scaledByMinStake, _ := bits.Div64(high, low, minStake)
	stakeScore := scaledByMinStake / 3
	if stakeScore > 1_000_000 {
		return 1_000_000
	}
	return stakeScore
}
