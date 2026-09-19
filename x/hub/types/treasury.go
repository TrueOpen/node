package types

import (
	"bytes"
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
)

func IsValidTreasuryPurposeCode(value TreasuryPurposeCode) bool {
	switch value {
	case TreasuryPurposeCode_TREASURY_PURPOSE_CODE_INFRASTRUCTURE,
		TreasuryPurposeCode_TREASURY_PURPOSE_CODE_SECURITY_RESPONSE,
		TreasuryPurposeCode_TREASURY_PURPOSE_CODE_GOVERNANCE_OPERATION:
		return true
	default:
		return false
	}
}

func (s TreasurySpendReceiptState) Validate() error {
	if s.ProposalId == 0 || s.ExecutedHeight == 0 || s.PruneHeight <= s.ExecutedHeight ||
		s.TreasuryVersionAfter == 0 {
		return fmt.Errorf("treasury spend receipt lifecycle fields are invalid")
	}
	if len(s.ActionDigest) != shared.Hash32KeySize || bytes.Equal(s.ActionDigest, make([]byte, shared.Hash32KeySize)) {
		return fmt.Errorf("treasury spend receipt action_digest must be non-zero Hash32")
	}
	if _, err := requireCanonicalNonEmpty("treasury spend receipt recipient", s.Recipient); err != nil {
		return err
	}
	if amount, err := shared.ParseAmount(s.Amount); err != nil || amount == 0 {
		return fmt.Errorf("treasury spend receipt amount must be positive")
	}
	if !IsValidTreasuryPurposeCode(s.PurposeCode) {
		return fmt.Errorf("treasury spend receipt purpose_code is invalid")
	}
	return nil
}

func (s TreasurySpendProposalState) Validate() error {
	if s.ProposalId == 0 || s.ActiveReceiptCount == 0 || s.UpdatedHeight == 0 {
		return fmt.Errorf("treasury proposal accumulator lifecycle fields are invalid")
	}
	if amount, err := shared.ParseAmount(s.SpentAmount); err != nil || amount == 0 {
		return fmt.Errorf("treasury proposal accumulator amount must be positive")
	}
	return nil
}

func (s TreasurySpendEpochState) Validate() error {
	if s.RecipientRowCount == 0 || s.CleanupHeight == 0 {
		return fmt.Errorf("treasury epoch accumulator lifecycle fields are invalid")
	}
	if amount, err := shared.ParseAmount(s.SpentAmount); err != nil || amount == 0 {
		return fmt.Errorf("treasury epoch accumulator amount must be positive")
	}
	return nil
}

func (s TreasurySpendRecipientEpochState) Validate() error {
	if _, err := requireCanonicalNonEmpty("treasury recipient accumulator address", s.RecipientAddress); err != nil {
		return err
	}
	if amount, err := shared.ParseAmount(s.SpentAmount); err != nil || amount == 0 {
		return fmt.Errorf("treasury recipient accumulator amount must be positive")
	}
	return nil
}

func (s TreasurySpendEpochCleanupCursorState) Validate() error {
	if s.CleanupHeight == 0 || s.VisitedCount != s.DeletedCount {
		return fmt.Errorf("treasury cleanup cursor counters are invalid")
	}
	if s.DeletedCount == 0 {
		if s.XLastRecipientAddress != nil {
			return fmt.Errorf("empty treasury cleanup cursor must not have a last recipient")
		}
		return nil
	}
	if s.XLastRecipientAddress == nil {
		return fmt.Errorf("advanced treasury cleanup cursor requires a last recipient")
	}
	_, err := requireCanonicalNonEmpty("treasury cleanup cursor last recipient", s.GetLastRecipientAddress())
	return err
}
