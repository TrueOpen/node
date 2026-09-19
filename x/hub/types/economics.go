package types

import (
	"fmt"
	"math"

	shared "github.com/TrueOpen/node/x/shared/types"
)

func parseEconomicsAmount(fieldName string, amount shared.Amount) (uint64, error) {
	value, err := shared.ParseAmount(amount)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", fieldName, err)
	}
	return value, nil
}

// Validate enforces the Phase 0 direct-settlement ledger. Reward epochs cannot
// create service or builder rewards, and claimable_amount is the checked total.
func (s EarningsState) Validate() error {
	if _, err := requireCanonicalNonEmpty("earnings address", s.Address); err != nil {
		return err
	}
	taskFee, err := parseEconomicsAmount("earnings claimable_task_fee", s.ClaimableTaskFee)
	if err != nil {
		return err
	}
	serviceReward, err := parseEconomicsAmount("earnings claimable_service_reward", s.ClaimableServiceReward)
	if err != nil {
		return err
	}
	builderReward, err := parseEconomicsAmount("earnings claimable_builder_reward", s.ClaimableBuilderReward)
	if err != nil {
		return err
	}
	if serviceReward != 0 || builderReward != 0 {
		return fmt.Errorf("earnings %s contains disabled epoch rewards", s.Address)
	}
	claimable, err := checkedSum(taskFee, serviceReward, builderReward)
	if err != nil {
		return fmt.Errorf("earnings %s claimable overflow: %w", s.Address, err)
	}
	claimableAmount, err := parseEconomicsAmount("earnings claimable_amount", s.ClaimableAmount)
	if err != nil {
		return err
	}
	if claimableAmount != claimable {
		return fmt.Errorf("earnings %s claimable_amount %d does not match subledger sum %d", s.Address, claimableAmount, claimable)
	}
	if claimable == 0 {
		return fmt.Errorf("earnings %s must not persist an empty ledger", s.Address)
	}
	if s.EarningsVersion == 0 || s.LastUpdatedHeight == 0 {
		return fmt.Errorf("earnings %s has invalid version or update height", s.Address)
	}
	return nil
}

func checkedSum(values ...uint64) (uint64, error) {
	total := uint64(0)
	for _, value := range values {
		if math.MaxUint64-total < value {
			return 0, fmt.Errorf("uint64 overflow")
		}
		total += value
	}
	return total, nil
}

func validateEconomicsGenesis(gs GenesisState) error {
	earnings := make(map[string]struct{}, len(gs.Earnings))
	for _, state := range gs.Earnings {
		if err := state.Validate(); err != nil {
			return err
		}
		if _, exists := earnings[state.Address]; exists {
			return fmt.Errorf("duplicate earnings for %s", state.Address)
		}
		earnings[state.Address] = struct{}{}
	}
	return nil
}
