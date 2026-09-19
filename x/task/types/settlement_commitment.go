package types

import (
	"bytes"
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// TaskSettlementID derives the single TRUEOPEN_TASK_SETTLEMENT_ID_V1 identity that
// every settlement fact, plan, event and query references. Both heights are part
// of the preimage, so one task settled at a different cutoff is a different
// settlement rather than a silent overwrite of the first.
func TaskSettlementID(chainID string, taskID, taskHash []byte, verifyRound uint32, cutoffHeight, settlementHeight uint64) ([32]byte, error) {
	chain, err := canonicalUTF8Field("chain_id", chainID)
	if err != nil {
		return [32]byte{}, err
	}
	task, err := canonicalHash32("task_id", taskID)
	if err != nil {
		return [32]byte{}, err
	}
	hash, err := canonicalHash32("task_hash", taskHash)
	if err != nil {
		return [32]byte{}, err
	}
	// Round zero is reserved for the formal pre-verification failure settlement;
	// ordinary and challenged verification use rounds one and two.
	if verifyRound > ChallengeVerifyRoundV1 {
		return [32]byte{}, fmt.Errorf("verify_round must be 0, 1, or 2")
	}
	if cutoffHeight == 0 || settlementHeight < cutoffHeight {
		return [32]byte{}, fmt.Errorf("settlement heights are invalid")
	}
	return canonicalTaskDigestV1(
		shared.DomainTaskSettlementIDV1,
		chain,
		task,
		hash,
		shared.Uint32BE(verifyRound),
		shared.Uint64BE(cutoffHeight),
		shared.Uint64BE(settlementHeight),
	)
}

func SettlementBillHash(bill TaskSettlementBillV1) ([32]byte, error) {
	worker, err := CanonicalOperatorAddressBytes("worker_operator_address", bill.WorkerOperatorAddress)
	if err != nil {
		return [32]byte{}, err
	}
	inferReceipt, err := canonicalHash32("infer_receipt_ref", bill.InferReceiptRef)
	if err != nil {
		return [32]byte{}, err
	}
	return canonicalTaskDigestV1(
		shared.DomainSettlementBillV1,
		worker,
		inferReceipt,
		shared.Uint64BE(bill.FeeRuleVersion),
		shared.Uint64BE(bill.GeneratedTokenCount),
		shared.Uint64BE(bill.WorkUnit),
	)
}

func GasReimbursementHash(chainID string, item TaskGasReimbursementV1) ([32]byte, error) {
	chain, err := canonicalUTF8Field("chain_id", chainID)
	if err != nil {
		return [32]byte{}, err
	}
	frame, err := canonicalGasReimbursementFrame(item)
	if err != nil {
		return [32]byte{}, err
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainGasReimbursementV1)).
		Raw(chain).Nested(frame).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

func GasReimbursementsHash(chainID string, taskID []byte, items []TaskGasReimbursementV1) ([32]byte, error) {
	chain, err := canonicalUTF8Field("chain_id", chainID)
	if err != nil {
		return [32]byte{}, err
	}
	task, err := canonicalHash32("task_id", taskID)
	if err != nil {
		return [32]byte{}, err
	}
	frames := make([]shared.CanonicalFrameV1, len(items))
	var previousTx []byte
	var previousIndex uint32
	for i, item := range items {
		if !bytes.Equal(item.TaskId, taskID) {
			return [32]byte{}, fmt.Errorf("gas_reimbursements[%d] task_id does not match", i)
		}
		if i > 0 {
			cmp := bytes.Compare(previousTx, item.TxHash)
			if cmp > 0 || (cmp == 0 && previousIndex >= item.ItemIndex) {
				return [32]byte{}, fmt.Errorf("gas reimbursements must be strictly ordered by tx_hash and item_index")
			}
		}
		frames[i], err = canonicalGasReimbursementFrame(item)
		if err != nil {
			return [32]byte{}, fmt.Errorf("gas_reimbursements[%d]: %w", i, err)
		}
		previousTx = item.TxHash
		previousIndex = item.ItemIndex
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainGasReimbursementsV1)).
		Raw(chain, task, shared.Uint32BE(uint32(len(items)))).
		Nested(shared.CanonicalRepeatedFramesV1(frames)).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

func SettlementFactsHash(chainID string, settlementID []byte, facts SettlementFactsV1) ([32]byte, error) {
	chain, err := canonicalUTF8Field("chain_id", chainID)
	if err != nil {
		return [32]byte{}, err
	}
	id, err := canonicalHash32("settlement_id", settlementID)
	if err != nil {
		return [32]byte{}, err
	}
	frame, err := canonicalSettlementFactsFrame(facts)
	if err != nil {
		return [32]byte{}, err
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainSettlementFactsV1)).
		Raw(chain, id).Nested(frame).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

func SettlementPlanHash(chainID string, plan SettlementPlanV1) ([32]byte, error) {
	chain, err := canonicalUTF8Field("chain_id", chainID)
	if err != nil {
		return [32]byte{}, err
	}
	id, err := canonicalHash32("settlement_id", plan.SettlementId)
	if err != nil {
		return [32]byte{}, err
	}
	frame, err := canonicalSettlementPlanFrame(plan)
	if err != nil {
		return [32]byte{}, err
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainSettlementPlanV1)).
		Raw(chain, id).Nested(frame).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

func canonicalGasReimbursementFrame(item TaskGasReimbursementV1) (shared.CanonicalFrameV1, error) {
	taskID, err := canonicalHash32("task_id", item.TaskId)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	txHash, err := canonicalHash32("tx_hash", item.TxHash)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	if item.ReimbursementKind < TaskReimbursementKindV1_TASK_REIMBURSEMENT_KIND_V1_WORKER_HANDRAISE ||
		item.ReimbursementKind > TaskReimbursementKindV1_TASK_REIMBURSEMENT_KIND_V1_VERIFY_RESULT {
		return shared.CanonicalFrameV1{}, fmt.Errorf("reimbursement_kind is invalid")
	}
	feePayer, err := CanonicalOperatorAddressBytes("fee_payer", item.FeePayer)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	actual, err := shared.CanonicalAmountFrameV1(item.ActualFeePaid)
	if err != nil {
		return shared.CanonicalFrameV1{}, fmt.Errorf("actual_fee_paid: %w", err)
	}
	necessary, err := shared.CanonicalAmountFrameV1(item.NecessaryFee)
	if err != nil {
		return shared.CanonicalFrameV1{}, fmt.Errorf("necessary_fee: %w", err)
	}
	reimbursed, err := shared.CanonicalAmountFrameV1(item.ReimbursedAmount)
	if err != nil {
		return shared.CanonicalFrameV1{}, fmt.Errorf("reimbursed_amount: %w", err)
	}
	frame := shared.NewCanonicalFrameBuilderV1().Raw(
		taskID, txHash, shared.Uint32BE(item.ItemIndex), shared.EnumBE(uint32(item.ReimbursementKind)),
		feePayer, shared.Uint64BE(item.GasBasis),
	).Nested(actual, necessary, reimbursed).Raw(
		shared.Uint64BE(item.FeePolicyVersion), shared.Uint64BE(item.AcceptedHeight),
	).Build()
	return frame, frame.Err()
}

func canonicalSettlementFactsFrame(facts SettlementFactsV1) (shared.CanonicalFrameV1, error) {
	taskID, err := canonicalHash32("task_id", facts.TaskId)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	settler, err := CanonicalOperatorAddressBytes("settlement_duty_builder_operator", facts.SettlementDutyBuilderOperator)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	clusterHash, err := canonicalHash32("consensus_cluster_hash", facts.ConsensusClusterHash)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	faultHash, err := canonicalHash32("fault_summary_hash", facts.FaultSummaryHash)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	inferReceipt, err := canonicalHash32("infer_receipt_ref", facts.InferReceiptRef)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	resultRefs, err := canonicalHash32("result_receipt_refs_hash", facts.ResultReceiptRefsHash)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	paidRoles := make([]shared.CanonicalFrameV1, len(facts.PaidRoles))
	for i, role := range facts.PaidRoles {
		operator, err := CanonicalOperatorAddressBytes("paid_role.operator_address", role.OperatorAddress)
		if err != nil {
			return shared.CanonicalFrameV1{}, fmt.Errorf("paid_roles[%d]: %w", i, err)
		}
		paidRoles[i] = shared.FlatCanonicalFrameV1(shared.EnumBE(uint32(role.Duty)), operator)
	}
	frame := shared.NewCanonicalFrameBuilderV1().Raw(
		taskID, shared.Uint32BE(facts.VerifyRound), shared.Uint64BE(facts.SettlementHeight),
		shared.Uint64BE(facts.SettlementFactsCutoffHeight), settler, clusterHash,
		shared.EnumBE(uint32(facts.Verdict)), shared.EnumBE(uint32(facts.FailureClass)), faultHash,
	).Nested(shared.CanonicalRepeatedFramesV1(paidRoles)).Raw(
		inferReceipt, resultRefs, shared.Uint64BE(facts.ChallengeCloseHeight),
	).Build()
	return frame, frame.Err()
}

func canonicalSettlementPlanFrame(plan SettlementPlanV1) (shared.CanonicalFrameV1, error) {
	taskID, err := canonicalHash32("task_id", plan.TaskId)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	settlementID, err := canonicalHash32("settlement_id", plan.SettlementId)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	gasHash, err := canonicalHash32("gas_reimbursements_hash", plan.GasReimbursementsHash)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	factsHash, err := canonicalHash32("settlement_facts_hash", plan.SettlementFactsHash)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	roundHash, err := canonicalHash32("task_round_summary_hash", plan.TaskRoundSummaryHash)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	billHash, err := canonicalHash32("settlement_bill_hash", plan.SettlementBillHash)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	amountFrames := make([]shared.CanonicalFrameV1, 0, 8)
	for _, item := range []struct {
		name   string
		amount shared.Amount
	}{
		{"worker_gross", plan.WorkerGross}, {"worker_maintenance", plan.WorkerMaintenance},
		{"worker_net", plan.WorkerNet}, {"verifier_slot_gross", plan.VerifierSlotGross},
		{"maintenance_fee", plan.MaintenanceFee}, {"refund_amount", plan.RefundAmount},
		{"original_reserved_amount", plan.OriginalReservedAmount},
	} {
		frame, err := shared.CanonicalAmountFrameV1(item.amount)
		if err != nil {
			return shared.CanonicalFrameV1{}, fmt.Errorf("%s: %w", item.name, err)
		}
		amountFrames = append(amountFrames, frame)
	}
	payouts := make([]shared.CanonicalFrameV1, len(plan.VerifierPayouts))
	for i, payout := range plan.VerifierPayouts {
		payouts[i], err = canonicalVerifierPayoutFrame(payout)
		if err != nil {
			return shared.CanonicalFrameV1{}, fmt.Errorf("verifier_payouts[%d]: %w", i, err)
		}
	}
	reimbursements := make([]shared.CanonicalFrameV1, len(plan.GasReimbursements))
	for i, reimbursement := range plan.GasReimbursements {
		reimbursements[i], err = canonicalGasReimbursementFrame(reimbursement)
		if err != nil {
			return shared.CanonicalFrameV1{}, fmt.Errorf("gas_reimbursements[%d]: %w", i, err)
		}
	}
	frame := shared.NewCanonicalFrameBuilderV1().Raw(
		taskID, settlementID, shared.Uint64BE(plan.FeeRuleVersion), shared.Uint32BE(plan.EffectiveVerifyRound),
		shared.Uint64BE(plan.GeneratedTokenCount), shared.Uint64BE(plan.WorkUnit),
	).Nested(amountFrames[0], amountFrames[1], amountFrames[2], amountFrames[3]).
		Nested(shared.CanonicalRepeatedFramesV1(payouts), shared.CanonicalRepeatedFramesV1(reimbursements)).
		Raw(gasHash).Nested(amountFrames[4], amountFrames[5], amountFrames[6]).
		Raw(factsHash, roundHash, billHash).Build()
	return frame, frame.Err()
}

func canonicalVerifierPayoutFrame(payout VerifierPayoutV1) (shared.CanonicalFrameV1, error) {
	operator, err := CanonicalOperatorAddressBytes("operator_address", payout.OperatorAddress)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	gross, err := shared.CanonicalAmountFrameV1(payout.Gross)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	maintenance, err := shared.CanonicalAmountFrameV1(payout.Maintenance)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	net, err := shared.CanonicalAmountFrameV1(payout.Net)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	frame := shared.NewCanonicalFrameBuilderV1().Raw(operator, shared.Uint32BE(payout.SelectedVerifierIndex)).
		Nested(gross, maintenance, net).Build()
	return frame, frame.Err()
}

// PaidRolesHash commits the settlement's paid-role vector so
// SettlementFactsRetainedState can keep the count and the digest instead of the
// full array after cleanup. The caller supplies the already sorted, unique vector
// from SettlementFactsV1; this producer re-checks the ordering rather than
// re-sorting, so a caller that lost the frozen order fails here instead of
// silently committing a different vector.
func PaidRolesHash(chainID string, taskID, settlementID []byte, roles []PaidRoleV1) ([32]byte, error) {
	chain, err := canonicalUTF8Field("chain_id", chainID)
	if err != nil {
		return [32]byte{}, err
	}
	task, err := canonicalHash32("task_id", taskID)
	if err != nil {
		return [32]byte{}, err
	}
	settlement, err := canonicalHash32("settlement_id", settlementID)
	if err != nil {
		return [32]byte{}, err
	}
	frames := make([]shared.CanonicalFrameV1, len(roles))
	var previous []byte
	var previousDuty shared.Duty
	for i, role := range roles {
		operator, err := CanonicalOperatorAddressBytes("operator_address", role.OperatorAddress)
		if err != nil {
			return [32]byte{}, fmt.Errorf("paid role %d: %w", i, err)
		}
		if i > 0 {
			if role.Duty < previousDuty ||
				(role.Duty == previousDuty && bytes.Compare(operator, previous) <= 0) {
				return [32]byte{}, fmt.Errorf("paid roles must ascend by (duty, operator bytes)")
			}
		}
		previous, previousDuty = operator, role.Duty
		frames[i] = shared.NewCanonicalFrameBuilderV1().
			Raw(shared.EnumBE(uint32(role.Duty)), operator).Build()
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainPaidRolesV1)).Raw(
		chain, task, settlement, shared.Uint32BE(uint32(len(roles))),
	).Nested(shared.CanonicalRepeatedFramesV1(frames)).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}
