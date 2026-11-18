package types

import (
	"fmt"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// Validate checks the sole compensation ledger and its cumulative conservation.
func (s BuilderCompensationAccountState) Validate() error {
	if _, err := requireCanonicalNonEmpty("compensation Builder", s.BuilderAddress); err != nil {
		return err
	}
	values := make([]uint64, 4)
	for i, amount := range []shared.Amount{s.Balance, s.TotalFunded, s.TotalPaid, s.TotalPenalized} {
		value, err := shared.ParseAmount(amount)
		if err != nil {
			return fmt.Errorf("compensation amount: %w", err)
		}
		values[i] = value
	}
	spent, overflow := shared.CheckedAddUint64(values[2], values[3])
	if overflow {
		return fmt.Errorf("compensation cumulative spending overflow")
	}
	total, overflow := shared.CheckedAddUint64(values[0], spent)
	if overflow || total != values[1] {
		return fmt.Errorf("compensation balance and cumulative totals do not conserve funded amount")
	}
	return nil
}

// Validate checks the compact objective fault receipt without trusting a caller's
// monetary preimage or treating an absent compensation beneficiary as an address.
func (s DispatchFaultState) Validate() error {
	if _, err := requireCanonicalNonEmpty("fault Builder", s.BuilderAddress); err != nil {
		return err
	}
	if len(s.FaultId) != 32 || len(s.TaskId) != 32 || len(s.DispatchRecordHash) != 32 || len(s.ChallengedTicketHashOrZero32) != 32 || s.FaultHeight == 0 || s.Kind < shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_OMISSION || s.Kind > shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_DOUBLE_DISPATCH {
		return fmt.Errorf("dispatch fault identity is invalid")
	}
	for _, amount := range []shared.Amount{s.PenaltyAmount, s.UnfilledAmount, s.CompensatedAmount} {
		if _, err := shared.ParseAmount(amount); err != nil {
			return err
		}
	}
	if worker := s.GetCompensatedWorker(); worker != "" {
		if _, err := requireCanonicalNonEmpty("compensated Worker", worker); err != nil {
			return err
		}
		if s.Kind != shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_OMISSION {
			return fmt.Errorf("only omission faults compensate a victim")
		}
	} else if amount, _ := shared.ParseAmount(s.CompensatedAmount); amount != 0 {
		return fmt.Errorf("compensation requires a beneficiary")
	}
	return nil
}
