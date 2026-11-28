package types

import (
	"errors"
	"fmt"
	"math/bits"

	"cosmossdk.io/collections"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// WorkerPauseReasonUnbonding is the bit of ServiceBondState.pause_reasons set
// while a service unbonding of the operator is outstanding (bit 1 is a late
// submission, bit 4 a jail). dispatch_paused is true exactly when at least one
// bit is set.
const (
	WorkerPauseReasonUnbonding = uint32(2)
	workerPauseKnownBits       = uint32(1 | 2 | 4)
)

// Suggested values for a chain that turns the Worker bond rules on. They are
// not defaults: every one of these parameters is zero on a chain that does not
// set it, and a zero parameter leaves that rule off.
const (
	// RecommendedWorkerQuotaPeriodValueBpsOfBond lets one settlement period
	// carry order value up to one seventh of the effective bond.
	RecommendedWorkerQuotaPeriodValueBpsOfBond = uint32(1_428)
	// RecommendedWorkerBondFloorRevenuePeriods documents that a tier floor is
	// sized as seven settlement periods of full-load revenue.
	RecommendedWorkerBondFloorRevenuePeriods = uint32(7)
	// RecommendedServiceUnbondingPeriodBlocks is fourteen days of one-second
	// blocks.
	RecommendedServiceUnbondingPeriodBlocks = uint64(1_209_600)
	// MaxWorkerQuotaPeriodValueBpsOfBond bounds the coefficient at ten times
	// the bond per period.
	MaxWorkerQuotaPeriodValueBpsOfBond = uint32(100_000)
	// WorkerQuotaRetentionPeriods is how many periods behind the newest one a
	// quota row with nothing reserved is kept before it is deleted.
	WorkerQuotaRetentionPeriods = uint64(16)
)

// ErrWorkerQuotaExceeded refuses a new dispatch record whose order value does
// not fit in what is left of the Worker's quota for the period.
var ErrWorkerQuotaExceeded = errors.New("worker order quota for the settlement period is exhausted")

type WorkerQuotaKeyPair = collections.Pair[string, uint64]

func NewWorkerQuotaKey(operatorAddress string, period uint64) WorkerQuotaKeyPair {
	return collections.Join(operatorAddress, period)
}

// WorkerQuotaTerms is what a dispatch record needs to know about the bond
// rules; it is read together with the rest of the Hub parameters.
type WorkerQuotaTerms struct {
	PeriodValueBpsOfBond uint32
	BondFloorByTier      []WorkerBondFloorV1
}

// BondFloor returns the minimum effective bond of a Worker serving Profiles of
// the given resource tier; zero when no floor is set for that tier.
func (t WorkerQuotaTerms) BondFloor(resourceTier uint32) uint64 {
	for _, entry := range t.BondFloorByTier {
		if entry.ResourceTier == resourceTier {
			floor, err := AmountToUint64(entry.MinBond, true)
			if err != nil {
				return 0
			}
			return floor
		}
	}
	return 0
}

// WorkerPeriodQuota is effective_bond * bps / 10000, saturating.
func WorkerPeriodQuota(effectiveBond uint64, bps uint32) uint64 {
	hi, lo := bits.Mul64(effectiveBond, uint64(bps))
	if hi >= 10_000 {
		return ^uint64(0)
	}
	quota, _ := bits.Div64(hi, lo, 10_000)
	return quota
}

// WorkerQuotaRemaining is quota - reserved - settled, floored at zero.
func WorkerQuotaRemaining(quota, reserved, settled uint64) uint64 {
	used := reserved + settled
	if used < reserved || used >= quota {
		return 0
	}
	return quota - used
}

// WorkerSettlementPeriod is the settlement period a height falls in; ok is
// false when no period length is configured.
func WorkerSettlementPeriod(height, periodBlocks uint64) (uint64, bool) {
	if periodBlocks == 0 {
		return 0, false
	}
	return height / periodBlocks, true
}

// SetPauseReason switches one pause reason and keeps dispatch_paused in step.
func (s *ServiceBondState) SetPauseReason(reason uint32, on bool) {
	if on {
		s.PauseReasons |= reason
	} else {
		s.PauseReasons &^= reason
	}
	s.DispatchPaused = s.PauseReasons != 0
}

func validateDispatchPause(s ServiceBondState) error {
	if s.PauseReasons&^workerPauseKnownBits != 0 || s.DispatchPaused != (s.PauseReasons != 0) {
		return fmt.Errorf("service bond dispatch pause flags are inconsistent")
	}
	return nil
}

func validateWorkerBondParams(p ServiceParamsV1) error {
	if p.WorkerQuotaPeriodValueBpsOfBond > MaxWorkerQuotaPeriodValueBpsOfBond {
		return fmt.Errorf("worker_quota_period_value_bps_of_bond exceeds %d", MaxWorkerQuotaPeriodValueBpsOfBond)
	}
	previous := uint32(0)
	for _, entry := range p.WorkerBondFloorByTier {
		if entry.ResourceTier < MinProfileResourceTier || entry.ResourceTier > MaxProfileResourceTier || entry.ResourceTier <= previous {
			return fmt.Errorf("worker_bond_floor_by_tier must list each resource tier once, in increasing order")
		}
		if _, err := AmountToUint64(entry.MinBond, false); err != nil {
			return fmt.Errorf("worker_bond_floor_by_tier[%d].min_bond: %w", entry.ResourceTier, err)
		}
		previous = entry.ResourceTier
	}
	return nil
}

// ValidateWorkerQuotaGenesis checks the exported quota rows: a canonical
// operator and one row per (operator, period).
func ValidateWorkerQuotaGenesis(rows []WorkerQuotaState) error {
	type rowKey struct {
		operator string
		period   uint64
	}
	seen := make(map[rowKey]struct{}, len(rows))
	for _, row := range rows {
		if _, err := requireCanonicalNonEmpty("worker quota operator_address", row.OperatorAddress); err != nil {
			return err
		}
		if _, overflow := shared.CheckedAddUint64(row.ReservedValue, row.SettledValue); overflow {
			return fmt.Errorf("worker quota of %s period %d overflows", row.OperatorAddress, row.Period)
		}
		key := rowKey{row.OperatorAddress, row.Period}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate worker quota row for %s period %d", row.OperatorAddress, row.Period)
		}
		seen[key] = struct{}{}
	}
	return nil
}
