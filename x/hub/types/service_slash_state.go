package types

import (
	"bytes"
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// Validate checks the immutable audit receipt independently of its collection
// key. Genesis and Keeper invariants additionally prove key/reference closure.
func (s SlashSummaryState) Validate() error {
	if err := validateRequiredHash32("slash summary slash_summary_id", s.SlashSummaryId); err != nil {
		return err
	}
	if _, err := requireCanonicalNonEmpty("slash summary operator_address", s.OperatorAddress); err != nil {
		return err
	}
	if !IsValidDuty(s.Duty) {
		return fmt.Errorf("slash summary duty is invalid")
	}
	if taskID := s.GetTaskId(); len(taskID) != 0 && len(taskID) != 32 {
		return fmt.Errorf("slash summary task_id must be empty or 32 bytes")
	}
	switch s.SourceKind {
	case SlashSourceKind_SLASH_SOURCE_KIND_ROLE_FAULT:
		if _, err := SlashSummarySourceKey(s.SourceKind, s.SourceId); err != nil ||
			s.EffectIndex != 0 || len(s.GetTaskId()) != 32 || !bytes.Equal(s.SlashSummaryId, s.SourceId) ||
			s.Destination != SlashDestination_SLASH_DESTINATION_TREASURY {
			return fmt.Errorf("role-fault slash summary source identity is invalid")
		}
	case SlashSourceKind_SLASH_SOURCE_KIND_CHALLENGE_EFFECT:
		if _, err := SlashSummarySourceKey(s.SourceKind, s.SourceId); err != nil ||
			s.EffectIndex == 0 || len(s.GetTaskId()) != 32 ||
			s.Destination != SlashDestination_SLASH_DESTINATION_CHALLENGE_EFFECT_POOL {
			return fmt.Errorf("challenge-effect slash summary source identity is invalid")
		}
	default:
		return fmt.Errorf("slash summary source_kind is invalid")
	}
	if s.Destination != SlashDestination_SLASH_DESTINATION_TREASURY &&
		s.Destination != SlashDestination_SLASH_DESTINATION_CHALLENGE_EFFECT_POOL {
		return fmt.Errorf("slash summary destination is invalid")
	}
	requested, err := shared.ParseAmount(s.RequestedAmount)
	if err != nil {
		return fmt.Errorf("slash summary requested_amount: %w", err)
	}
	if requested == 0 {
		return fmt.Errorf("slash summary requested_amount must be positive")
	}
	pending, err := shared.ParseAmount(s.PendingTaskEarningsDebit)
	if err != nil {
		return fmt.Errorf("slash summary pending_task_earnings_debit: %w", err)
	}
	active, err := shared.ParseAmount(s.ActiveBondDebit)
	if err != nil {
		return fmt.Errorf("slash summary active_bond_debit: %w", err)
	}
	unbonding, err := shared.ParseAmount(s.UnbondingDebit)
	if err != nil {
		return fmt.Errorf("slash summary unbonding_debit: %w", err)
	}
	claimable, err := shared.ParseAmount(s.ClaimableEarningsDebit)
	if err != nil {
		return fmt.Errorf("slash summary claimable_earnings_debit: %w", err)
	}
	applied, err := shared.ParseAmount(s.AppliedAmount)
	if err != nil {
		return fmt.Errorf("slash summary applied_amount: %w", err)
	}
	unfilled, err := shared.ParseAmount(s.UnfilledAmount)
	if err != nil {
		return fmt.Errorf("slash summary unfilled_amount: %w", err)
	}
	layerTotal, err := checkedSlashSummaryAdd(pending, active, unbonding, claimable)
	if err != nil || layerTotal != applied {
		return fmt.Errorf("slash summary layer debit total does not match applied_amount")
	}
	if applied > requested || requested-applied != unfilled {
		return fmt.Errorf("slash summary applied/unfilled amounts do not match requested_amount")
	}
	if s.AppliedHeight == 0 || s.BondVersion == 0 {
		return fmt.Errorf("slash summary height and bond_version must be positive")
	}
	return nil
}

// SlashSummarySourceKey projects source_id onto its collection key. Both source
// kinds now hold a raw Hash32 — ROLE_FAULT the fault_id, CHALLENGE_EFFECT the
// challenge_id that ChallengeEconomicEffectBatch also spells as `bytes` — so one
// rule replaces the old Hash32-or-canonical-UTF-8 split. That split had become
// unreachable in its second half: the only writer passes batch.ChallengeId, and
// a 32-byte digest almost never survives utf8.Valid.
//
// The lowercase hex is the collection key alone and never enters a digest.
// Stage 3 swaps in a Hash32 key codec and this projection goes with it.
// SlashSummarySourceKey returns the raw source_id after proving it is a Hash32.
// It used to hex it, because that text was the store key component; the key is
// raw now, so the validation is all that is left.
func SlashSummarySourceKey(kind SlashSourceKind, sourceID []byte) (shared.Hash32Key, error) {
	switch kind {
	case SlashSourceKind_SLASH_SOURCE_KIND_ROLE_FAULT, SlashSourceKind_SLASH_SOURCE_KIND_CHALLENGE_EFFECT:
		if err := validateRequiredHash32("slash summary source_id", sourceID); err != nil {
			return nil, err
		}
		return sourceID, nil
	default:
		return nil, fmt.Errorf("slash summary source_kind is invalid")
	}
}

func checkedSlashSummaryAdd(values ...uint64) (uint64, error) {
	var total uint64
	for _, value := range values {
		if ^uint64(0)-total < value {
			return 0, fmt.Errorf("slash summary amount overflow")
		}
		total += value
	}
	return total, nil
}
