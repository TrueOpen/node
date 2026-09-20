package types

import (
	"fmt"
	"math"
	"math/bits"
)

// This file holds the single authoritative bond-exposure vocabulary shared by
// candidate eligibility, task-liability reservation, unstake gating and slash.
//
// Before this consolidation the codebase carried at least four divergent
// readings of "how much bond is usable": candidate eligibility used the
// epoch-effective bond minus reserved liability, unstake gating used the raw
// active bond, slash capped itself at active bond minus reserved liability, and
// support weighting used the effective bond without subtracting reservations.
// Those disagreed for any operator with a pending top-up or an open
// reservation, which is a consensus-relevant divergence.
//
// Authority:
//   - the data-structure contract: available_bond = active_bond - reserved_liability
//   - the API contract: handraise/proposal accepted only when
//     available_bond >= min_stake and available_bond >= required_task_liability;
//     unstake may only consume bond that is not reserved.
//   - the data-structure contract: support_vote_weight uses effective_active_bond
//     (i.e. it deliberately does NOT subtract reserved liability).
//
// Every caller must use these functions instead of recomputing the arithmetic
// inline, so a future change to the vocabulary cannot silently apply to only
// some of the paths.

// SlashByFraction applies numerator/denominator to amount using 128-bit
// intermediate math so a large bond cannot overflow before the division.
// Saturates at MaxUint64 rather than wrapping.
func SlashByFraction(amount, numerator, denominator uint64) uint64 {
	if amount == 0 || numerator == 0 || denominator == 0 {
		return 0
	}
	high, low := bits.Mul64(amount, numerator)
	if high >= denominator {
		return math.MaxUint64
	}
	quotient, _ := bits.Div64(high, low, denominator)
	return quotient
}

// EffectiveActiveBond returns the bond amount that is already in force for the
// given epoch. A top-up only takes effect at effective_bond_epoch, so earlier
// epochs must keep reading the frozen previous amount; this is what makes
// "a bond top-up takes effect in the next epoch" (§10.0c) observable without a
// second stored history row.
func EffectiveActiveBond(bond ServiceBondState, currentEpoch uint64) uint64 {
	if currentEpoch < bond.EffectiveBondEpoch {
		return bond.EffectiveActiveBond
	}
	return bond.ActiveBond
}

// AvailableBond is the effective bond that is not already committed to an open
// task-liability reservation. It is the single quantity that candidate
// eligibility and unstake gating must both consult (§B.1.3).
//
// Reserved liability may legitimately exceed the effective bond immediately
// after a slash lowered active_bond while reservations were still open, so the
// subtraction is checked and reported rather than wrapped.
func AvailableBond(bond ServiceBondState, currentEpoch uint64) (uint64, error) {
	effective := EffectiveActiveBond(bond, currentEpoch)
	if bond.ReservedLiability > effective {
		return 0, fmt.Errorf(
			"reserved_liability %d exceeds effective active bond %d for operator %s",
			bond.ReservedLiability, effective, bond.OperatorAddress,
		)
	}
	return effective - bond.ReservedLiability, nil
}

// RequiredTaskLiability returns the complete Phase 0 exposure for one accepted
// duty. The order-value leg rounds up so the reservation never under-covers the
// business value; the objective-slash leg rounds down by contract.
func RequiredTaskLiability(
	effectiveBond, orderValue uint64,
	orderCoverageBps, objectiveSlashBps uint32,
	minLiabilityFloor uint64,
) (uint64, error) {
	orderExposure, err := ceilMulDivU64(orderValue, uint64(orderCoverageBps), ObjectiveForgerySlashDenom)
	if err != nil {
		return 0, fmt.Errorf("task liability order-value exposure: %w", err)
	}
	exposure := SlashByFraction(effectiveBond, uint64(objectiveSlashBps), ObjectiveForgerySlashDenom)
	if exposure < minLiabilityFloor {
		exposure = minLiabilityFloor
	}
	if exposure < orderExposure {
		exposure = orderExposure
	}
	return exposure, nil
}

func ceilMulDivU64(value, multiplier, denominator uint64) (uint64, error) {
	if denominator == 0 {
		return 0, fmt.Errorf("denominator must be greater than zero")
	}
	high, low := bits.Mul64(value, multiplier)
	if high >= denominator {
		return 0, fmt.Errorf("result overflows uint64")
	}
	quotient, remainder := bits.Div64(high, low, denominator)
	if remainder != 0 {
		if quotient == math.MaxUint64 {
			return 0, fmt.Errorf("rounded result overflows uint64")
		}
		quotient++
	}
	return quotient, nil
}
