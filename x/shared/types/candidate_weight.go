package types

import (
	"fmt"
	"math/bits"
)

const candidateWeightScalePPM = uint64(1_000_000)

// WorkerCandidateWeightPolicy is frozen once in each activated epoch header.
type WorkerCandidateWeightPolicy struct {
	StakeWeightPPM       uint32
	PerformanceWeightPPM uint32
}

// Validate accepts the two endpoint policies as well as mixed policies.
func (p WorkerCandidateWeightPolicy) Validate() error {
	if uint64(p.StakeWeightPPM)+uint64(p.PerformanceWeightPPM) != candidateWeightScalePPM {
		return fmt.Errorf("Worker candidate weight coefficients must sum to 1000000")
	}
	return nil
}

// CandidateJailFactorPPM is the fixed V1 candidate scoring schedule.
func CandidateJailFactorPPM(jailCount uint64) uint32 {
	switch jailCount {
	case 0:
		return uint32(candidateWeightScalePPM)
	case 1:
		return uint32(candidateWeightScalePPM / 2)
	case 2:
		return uint32(candidateWeightScalePPM / 4)
	default:
		return 0
	}
}

// CandidateWeightPPM is shared by frozen dispatch weights and Verifier selection.
// All multiplications after the stake division are bounded by the PPM scale;
// the only unbounded product uses full-width multiplication and division.
func CandidateWeightPPM(activeBond, minStake uint64, performance, jailFactor, stakeWeight, performanceWeight, maximum uint32) (uint32, error) {
	if activeBond == 0 || minStake == 0 || performance == 0 || uint64(performance) > candidateWeightScalePPM || jailFactor == 0 || uint64(jailFactor) > candidateWeightScalePPM || uint64(stakeWeight)+uint64(performanceWeight) != candidateWeightScalePPM || maximum == 0 || uint64(maximum) > candidateWeightScalePPM {
		return 0, fmt.Errorf("candidate weight inputs are invalid")
	}
	stakeScore := candidateWeightScalePPM
	if minStake > ^uint64(0)/3 || activeBond < minStake*3 {
		high, low := bits.Mul64(activeBond, candidateWeightScalePPM)
		// activeBond < 3*minStake bounds the quotient independently of the
		// potentially overflowing denominator 3*minStake.
		scaled, _ := bits.Div64(high, low, minStake)
		stakeScore = min(candidateWeightScalePPM, scaled/3)
	}
	weighted := (stakeScore*uint64(stakeWeight) + uint64(performance)*uint64(performanceWeight)) / candidateWeightScalePPM
	weight := weighted * uint64(jailFactor) / candidateWeightScalePPM
	if weight == 0 || weight > uint64(maximum) {
		return 0, fmt.Errorf("candidate weight is outside the registered range")
	}
	return uint32(weight), nil
}
