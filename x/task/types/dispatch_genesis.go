package types

import (
	"bytes"
	"fmt"
)

func validateDispatchGenesisRows(genesis GenesisState) error {
	paramsForSession := genesisSessionParamsLookup(genesis)
	records, hashes, entries := map[string]DispatchRecordState{}, map[string]bool{}, map[string]bool{}
	for _, row := range genesis.DispatchRecords {
		if err := ValidateDispatchRecord(row); err != nil {
			return err
		}
		if _, ok := records[string(row.TaskId)]; ok || hashes[string(row.DispatchRecordHash)] || entries[string(row.AcceptedEntryDigest)] {
			return fmt.Errorf("duplicate dispatch record identity")
		}
		records[string(row.TaskId)], hashes[string(row.DispatchRecordHash)], entries[string(row.AcceptedEntryDigest)] = row, true, true
	}
	for _, assignment := range genesis.TaskAssignments {
		record, found := records[string(assignment.TaskId)]
		if !found || !bytes.Equal(record.DispatchRecordHash, assignment.DispatchRecordHash) || !bytes.Equal(record.DispatchTicketHash, assignment.DispatchTicketHash) || record.RecordedHeight != assignment.RecordHeight || record.Winner.OperatorAddress != assignment.WinnerWorker || !bytes.Equal(record.CandidatePoolSnapshotId, assignment.CandidatePoolSnapshotId) {
			return fmt.Errorf("Task assignment and retained dispatch record disagree")
		}
	}
	claims := map[string]DispatchCompensationClaimState{}
	sequenceClaims := map[string]bool{}
	streams := map[string]StreamState{}
	for _, row := range genesis.Streams {
		streams[string(row.SessionId)] = row
	}
	sequences := map[string]OrderSequenceState{}
	for _, row := range genesis.OrderSequences {
		sequences[fmt.Sprintf("%x/%d", row.SessionId, row.OrderSequence)] = row
	}
	cursors := map[string]SessionHistoryPruneCursorState{}
	for _, row := range genesis.SessionHistoryPruneCursors {
		cursors[string(row.SessionId)] = row
	}
	for _, row := range genesis.DispatchCompensationClaims {
		if err := ValidateDispatchClaim(row); err != nil {
			return err
		}
		if _, found := claims[string(row.DispatchTicketHash)]; found {
			return fmt.Errorf("duplicate dispatch compensation claim")
		}
		sequenceKey := fmt.Sprintf("%x/%d", row.SessionId, row.OrderSequence)
		if sequenceClaims[sequenceKey] {
			return fmt.Errorf("duplicate sequence compensation claim")
		}
		sequenceClaims[sequenceKey] = true
		var stream *StreamState
		var sequence *OrderSequenceState
		var cursor *SessionHistoryPruneCursorState
		if value, ok := streams[string(row.SessionId)]; ok {
			stream = &value
		}
		if value, ok := sequences[sequenceKey]; ok {
			sequence = &value
		}
		if value, ok := cursors[string(row.SessionId)]; ok {
			cursor = &value
		}
		params, err := paramsForSession(row.SessionId)
		if err != nil && stream != nil {
			return err
		}
		if err := ValidateDispatchClaimSessionScope(row, stream, sequence, cursor, params); err != nil {
			return err
		}
		claims[string(row.DispatchTicketHash)] = row
		if record, found := records[string(row.TaskId)]; found && bytes.Equal(record.DispatchTicketHash, row.DispatchTicketHash) {
			if err := validateClaimPaymentBeforeExclusion(row, record.RecordedHeight); err != nil {
				return err
			}
		}
	}
	rounds := map[string]bool{}
	for _, row := range genesis.DispatchRoundAcceptors {
		if err := ValidateDispatchRoundAcceptor(row); err != nil {
			return err
		}
		key := fmt.Sprintf("%x/%d/%s/%d/%d/%d", row.SessionId, row.OrderSequence, row.BuilderAddress, row.BoundChangeHeight, row.VrfEpoch, row.DispatchRound)
		if rounds[key] {
			return fmt.Errorf("duplicate round acceptor scope")
		}
		rounds[key] = true
	}
	winners := map[string]bool{}
	for _, row := range genesis.DispatchWinnerIneligible {
		if err := ValidateDispatchWinnerIneligible(row); err != nil {
			return err
		}
		if winners[string(row.DispatchTicketHash)] {
			return fmt.Errorf("duplicate winner exclusion")
		}
		winners[string(row.DispatchTicketHash)] = true
		if claim, found := claims[string(row.DispatchTicketHash)]; found {
			if err := validateClaimPaymentBeforeExclusion(claim, row.RejectedHeight); err != nil {
				return err
			}
		}
	}
	challenges := map[string]bool{}
	for _, row := range genesis.DispatchChallenges {
		if err := ValidateDispatchChallenge(row); err != nil {
			return err
		}
		if challenges[string(row.ChallengeId)] {
			return fmt.Errorf("duplicate dispatch challenge")
		}
		challenges[string(row.ChallengeId)] = true
		if row.Status == DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_AWAITING_PROOF || row.Status == DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_AWAITING_ROSTER {
			record, found := records[string(row.TaskId)]
			if !found || !bytes.Equal(record.DispatchRecordHash, row.GetDispatchRecordHash()) {
				return fmt.Errorf("open challenge lost its authoritative dispatch record")
			}
		}
	}
	receipts := map[string]bool{}
	for _, row := range genesis.TakeoverEntryReceipts {
		if err := ValidateTakeoverEntryReceipt(row); err != nil {
			return err
		}
		if receipts[string(row.TakeoverEntryDigest)] {
			return fmt.Errorf("duplicate takeover replay receipt")
		}
		receipts[string(row.TakeoverEntryDigest)] = true
	}
	return nil
}

// A later recording or ineligibility fact cannot invalidate an immutable past
// payment. Pending claims are rechecked and dismissed by their maturity handler.
func validateClaimPaymentBeforeExclusion(claim DispatchCompensationClaimState, exclusionHeight uint64) error {
	if claim.Status == DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_PAID && claim.GetResolvedHeight() >= exclusionHeight {
		// Exclusion is written by a transaction; claim payment runs afterwards
		// in EndBlock, so payment cannot precede exclusion in the same block.
		return fmt.Errorf("compensation claim was paid at or after winner exclusion")
	}
	return nil
}
