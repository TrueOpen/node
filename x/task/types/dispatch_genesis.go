package types

import (
	"bytes"
	"fmt"
)

func validateDispatchGenesisRows(genesis GenesisState) error {
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
	for _, row := range genesis.DispatchCompensationClaims {
		if err := ValidateDispatchClaim(row); err != nil {
			return err
		}
		if _, found := claims[string(row.DispatchTicketHash)]; found {
			return fmt.Errorf("duplicate dispatch compensation claim")
		}
		claims[string(row.DispatchTicketHash)] = row
		if record, found := records[string(row.TaskId)]; found && bytes.Equal(record.DispatchTicketHash, row.DispatchTicketHash) && row.Status != DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_DISMISSED {
			return fmt.Errorf("recorded winner has a payable compensation claim")
		}
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
		if claim, found := claims[string(row.DispatchTicketHash)]; found && claim.Status != DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_DISMISSED {
			return fmt.Errorf("ineligible winner has a payable compensation claim")
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
