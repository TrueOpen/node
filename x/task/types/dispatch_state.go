package types

import (
	"bytes"
	"fmt"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// ValidateDispatchRecord checks retained admission facts without reconstructing
// signed envelopes or consulting today's parameters.
func ValidateDispatchRecord(row DispatchRecordState) error {
	for _, hash := range [][]byte{row.TaskId, row.DispatchRecordHash, row.DispatchTicketHash, row.CandidatePoolSnapshotId, row.VrfOutput, row.AcceptedEntryDigest} {
		if len(hash) != Hash32Len {
			return fmt.Errorf("dispatch record identity must be raw32")
		}
	}
	if _, err := requireCanonicalNonEmpty("dispatch Builder", row.BuilderAddress); err != nil {
		return err
	}
	if row.ServiceAuthorizationNonce == 0 || row.BoundChangeHeight == 0 || row.BoundChangeHeight > row.RecordedHeight || row.ReferenceHeight == 0 || row.VrfEpoch == 0 || row.RecordedHeight < row.EarliestSubmitHeight || row.EarliestSubmitHeight == 0 || row.ClaimWindowCloseHeight < row.RecordedHeight || len(row.VrfProof) != 80 || row.RandomnessScheme != DispatchRandomnessSchemeV1_DISPATCH_RANDOMNESS_SCHEME_V1_BUILDER_VRF || row.StickySnapshot == nil || row.Winner == nil || row.StickyRunCount == 0 || len(row.Rounds) == 0 {
		return fmt.Errorf("dispatch record scope or clock is invalid")
	}
	validateMember := func(member *CandidateMemberRefV1) error {
		if member == nil || !bytes.Equal(member.CandidatePoolSnapshotId, row.CandidatePoolSnapshotId) || member.SlotVersion == 0 {
			return fmt.Errorf("dispatch member scope is invalid")
		}
		_, err := requireCanonicalNonEmpty("dispatch member", member.OperatorAddress)
		return err
	}
	if err := validateMember(row.Winner); err != nil {
		return err
	}
	if winner := row.StickySnapshot.GetLastRecordWinner(); row.StickySnapshot.XLastRecordWinner != nil {
		if _, err := requireCanonicalNonEmpty("sticky snapshot winner", winner); err != nil {
			return err
		}
		if row.StickySnapshot.AnchorHeight == 0 || row.StickySnapshot.RunCount == 0 {
			return fmt.Errorf("sticky snapshot winner lacks its anchor")
		}
	} else if row.StickySnapshot.AnchorHeight != 0 || row.StickySnapshot.AnchorSequence != 0 || row.StickySnapshot.RunCount != 0 {
		return fmt.Errorf("empty sticky snapshot contains an anchor")
	}
	seen := map[string]bool{}
	var previous uint32
	count, accepted := 0, 0
	for i, round := range row.Rounds {
		if round == nil || round.DispatchRound != uint32(i) || len(round.HandraiseRootDigest) != Hash32Len || len(round.RosterHash) != Hash32Len || len(round.Attempts) == 0 {
			return fmt.Errorf("dispatch round structure is invalid")
		}
		for j, attempt := range round.Attempts {
			if attempt == nil || len(attempt.DispatchTicketHash) != Hash32Len || len(attempt.RejectDigestOrZero32) != Hash32Len || count != 0 && attempt.Rank <= previous || seen[string(attempt.DispatchTicketHash)] {
				return fmt.Errorf("dispatch attempt rank or ticket identity is invalid")
			}
			if err := validateMember(attempt.Member); err != nil {
				return err
			}
			previous, count = attempt.Rank, count+1
			seen[string(attempt.DispatchTicketHash)] = true
			if attempt.Outcome < DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_ACCEPTED || attempt.Outcome > DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_REVOKED {
				return fmt.Errorf("unknown dispatch attempt outcome")
			}
			zeroReject := bytes.Equal(attempt.RejectDigestOrZero32, make([]byte, Hash32Len))
			if (attempt.Outcome == DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_REJECTED) == zeroReject || (attempt.Outcome == DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_TIMEOUT) != (attempt.TimeoutTierOrZero != 0) {
				return fmt.Errorf("dispatch attempt proof fields disagree with outcome")
			}
			if attempt.Outcome == DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_ACCEPTED {
				accepted++
				if i != len(row.Rounds)-1 || j != len(round.Attempts)-1 || row.WinnerRound != round.DispatchRound || !bytes.Equal(attempt.DispatchTicketHash, row.DispatchTicketHash) || !bytes.Equal(attempt.Member.CandidatePoolSnapshotId, row.Winner.CandidatePoolSnapshotId) || attempt.Member.Slot != row.Winner.Slot || attempt.Member.SlotVersion != row.Winner.SlotVersion || attempt.Member.OperatorAddress != row.Winner.OperatorAddress {
					return fmt.Errorf("dispatch winner is not the sole final accepted attempt")
				}
			}
		}
	}
	if accepted != 1 {
		return fmt.Errorf("retained dispatch must have exactly one acceptance")
	}
	return nil
}

// ValidateTakeoverEntryReceipt checks the immutable replay and prune inputs.
func ValidateTakeoverEntryReceipt(row TakeoverEntryReceiptState) error {
	for _, hash := range [][]byte{row.TakeoverEntryDigest, row.SessionId, row.OrderTaskHash} {
		if len(hash) != Hash32Len {
			return fmt.Errorf("takeover receipt identity must be raw32")
		}
	}
	if row.RegisteredHeight == 0 || row.ActivationHeight <= row.RegisteredHeight || row.OrderExpireHeight < row.RegisteredHeight || row.BoundChangeHeightAtRegistration == 0 || row.BoundChangeHeightAtRegistration > row.RegisteredHeight {
		return fmt.Errorf("takeover receipt clock is invalid")
	}
	_, err := requireCanonicalNonEmpty("takeover candidate", row.CandidateOperator)
	return err
}

// ValidateDispatchClaim checks replay identity, monetary conservation and status
// presence for both transaction writes and Genesis imports.
func ValidateDispatchClaim(row DispatchCompensationClaimState) error {
	for _, hash := range [][]byte{row.DispatchTicketHash, row.TaskId, row.SessionId} {
		if len(hash) != Hash32Len {
			return fmt.Errorf("claim identity must be raw32")
		}
	}
	for _, address := range []string{row.BuilderAddress, row.WorkerOperatorAddress} {
		if _, err := requireCanonicalNonEmpty("dispatch operator", address); err != nil {
			return err
		}
	}
	if row.BuilderSetId == "" || row.EarliestSubmitHeight == 0 || row.EarliestSubmitHeight >= row.ClaimedHeight || row.RebuttalDeadlineHeight <= row.ClaimedHeight || row.ClaimWindowCloseHeight < row.ClaimedHeight {
		return fmt.Errorf("claim scope or heights are invalid")
	}
	amount, err := shared.ParseAmount(row.Amount)
	if err != nil || amount == 0 {
		return fmt.Errorf("claim amount must be positive and canonical")
	}
	paid, err := shared.ParseAmount(row.PaidAmount)
	if err != nil {
		return err
	}
	unfilled, err := shared.ParseAmount(row.UnfilledAmount)
	if err != nil {
		return err
	}
	if address := row.GetRebuttedByOrEmpty(); row.XRebuttedByOrEmpty != nil {
		if _, err := requireCanonicalNonEmpty("dispatch operator", address); err != nil {
			return err
		}
	}
	switch row.Status {
	case DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_PENDING_REBUTTAL:
		if row.XResolvedHeight != nil || row.XRebuttedByOrEmpty != nil || paid != 0 || unfilled != 0 {
			return fmt.Errorf("pending claim contains terminal facts")
		}
	case DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_PAID:
		sum, overflow := shared.CheckedAddUint64(paid, unfilled)
		if overflow || sum != amount || row.GetResolvedHeight() <= row.RebuttalDeadlineHeight || row.XRebuttedByOrEmpty != nil {
			return fmt.Errorf("paid claim does not conserve its requested amount")
		}
	case DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_DISMISSED:
		if paid != 0 || unfilled != 0 || row.GetResolvedHeight() < row.ClaimedHeight {
			return fmt.Errorf("dismissed claim contains payment or lacks resolution")
		}
	default:
		return fmt.Errorf("unknown compensation claim status")
	}
	return nil
}

// ValidateDispatchWinnerIneligible checks the authenticated exclusion receipt.
func ValidateDispatchWinnerIneligible(row DispatchWinnerIneligibleState) error {
	if len(row.DispatchTicketHash) != Hash32Len || len(row.TaskId) != Hash32Len || row.RejectedHeight == 0 || row.ClaimWindowCloseHeight == 0 || row.Reason < DispatchWinnerIneligibleReasonV1_DISPATCH_WINNER_INELIGIBLE_REASON_V1_BOND_INSUFFICIENT || row.Reason > DispatchWinnerIneligibleReasonV1_DISPATCH_WINNER_INELIGIBLE_REASON_V1_TOMBSTONED {
		return fmt.Errorf("winner ineligibility receipt is invalid")
	}
	_, err := requireCanonicalNonEmpty("dispatch Worker", row.WorkerOperatorAddress)
	return err
}

// ValidateDispatchChallenge checks kind-specific selectors and terminal facts.
func ValidateDispatchChallenge(row DispatchChallengeState) error {
	if len(row.ChallengeId) != Hash32Len || len(row.TaskId) != Hash32Len || row.OpenedHeight == 0 {
		return fmt.Errorf("challenge identity is invalid")
	}
	for _, address := range []string{row.ChallengerAddress, row.BuilderAddress} {
		if _, err := requireCanonicalNonEmpty("dispatch operator", address); err != nil {
			return err
		}
	}
	if row.XDispatchRecordHash == nil || len(row.GetDispatchRecordHash()) != Hash32Len {
		return fmt.Errorf("challenge must retain its record hash")
	}
	switch row.Kind {
	case shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_OMISSION:
		if row.XDispatchRound == nil || row.XAttemptRank != nil || row.XVictimOperatorAddress == nil || row.XChallengedTicketHash != nil {
			return fmt.Errorf("invalid omission selectors")
		}
		if _, err := requireCanonicalNonEmpty("omission victim", row.GetVictimOperatorAddress()); err != nil {
			return err
		}
	case shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_DRAW, shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_ROSTER_UNAVAILABLE:
		if row.XDispatchRound == nil || row.XAttemptRank != nil || row.XVictimOperatorAddress != nil || row.XChallengedTicketHash != nil {
			return fmt.Errorf("invalid round challenge selectors")
		}
	case shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_SKIP:
		if row.XAttemptRank == nil || row.XDispatchRound != nil || row.XVictimOperatorAddress != nil || row.XChallengedTicketHash == nil || len(row.GetChallengedTicketHash()) != Hash32Len {
			return fmt.Errorf("invalid skip selectors")
		}
	case shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_DOUBLE_DISPATCH:
		if row.XAttemptRank != nil || row.XDispatchRound != nil || row.XVictimOperatorAddress != nil || row.XChallengedTicketHash != nil {
			return fmt.Errorf("invalid double dispatch selectors")
		}
	default:
		return fmt.Errorf("unknown dispatch challenge kind")
	}
	switch row.Status {
	case DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_AWAITING_PROOF, DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_AWAITING_ROSTER:
		if row.XResolvedHeight != nil || row.XFaultId != nil || row.GetProofDeadlineHeight() <= row.OpenedHeight {
			return fmt.Errorf("invalid pending challenge facts")
		}
		if (row.Status == DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_AWAITING_PROOF) != (row.Kind == shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_SKIP) || (row.Status == DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_AWAITING_ROSTER) != (row.Kind == shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_ROSTER_UNAVAILABLE) {
			return fmt.Errorf("pending challenge kind/status mismatch")
		}
	case DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_UPHELD:
		if !bytes.Equal(row.GetFaultId(), row.ChallengeId) || row.GetResolvedHeight() < row.OpenedHeight {
			return fmt.Errorf("upheld challenge must reference its first challenge identity")
		}
	case DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_DISMISSED, DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_EXPIRED:
		if row.XFaultId != nil || row.GetResolvedHeight() < row.OpenedHeight {
			return fmt.Errorf("invalid terminal challenge facts")
		}
	default:
		return fmt.Errorf("unknown dispatch challenge status")
	}
	return nil
}
