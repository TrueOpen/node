package types

import (
	"bytes"
	"encoding/hex"
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// Hash32Len is the byte length of every Hash32 identifier and digest in this
// module (task_id, session_id, commit_key and every §1.2 digest). Store key
// components carry these raw bytes through shared.Hash32KeyCodec; the
// lowercase 64-hex rendering survives only at explicit text boundaries.
const Hash32Len = shared.Hash32KeySize

// DefaultGenesis returns the fresh V1 genesis: parameters only.
//
// The pre-unblock default also seeded StateVersionState,
// StoreMigrationState and the default TimeoutBucketState. All three messages are
// deleted — there is no migration surface (§1.2 fresh genesis) and versioned
// parameter buckets live in x/hub, which this module now only references
// through TaskBucketRefState (§2.4/§6.7). Nothing replaces them here.
func DefaultGenesis() *GenesisState {
	return &GenesisState{Params: DefaultTaskParams()}
}

// Validate checks the fresh V1 Task genesis.
//
// Module-local structural checks live here. Keeper InitGenesis performs the
// cross-row and cross-module checks that require collections, Hub snapshots, or
// recomputed indexes.
func (gs GenesisState) Validate() error {
	if err := gs.Params.Validate(); err != nil {
		return err
	}
	if gs.ParamsMeta.ParamsVersion != 0 {
		if err := gs.ParamsMeta.Validate(); err != nil {
			return err
		}
	} else if len(gs.ParamsMeta.ParamsHash) != 0 || gs.ParamsMeta.UpdatedHeight != 0 {
		return fmt.Errorf("params meta must be entirely empty when params_version is zero")
	}
	if err := validateSessionRows(gs); err != nil {
		return err
	}
	cores, err := validateTaskCores(gs.TaskCores)
	if err != nil {
		return err
	}
	assignments, err := validateTaskAssignments(gs.TaskAssignments, cores)
	if err != nil {
		return err
	}
	if err := validateTaskBudgets(gs.TaskBudgets, assignments, cores); err != nil {
		return err
	}
	if err := validateAssignmentCandidateSets(gs.AssignmentCandidateSets, assignments); err != nil {
		return err
	}
	if err := validateInferReceipts(gs.InferReceipts, assignments, gs.TaskBudgets, gs.Params.Evidence.MaxOutputMmrLeaves); err != nil {
		return err
	}
	if err := validateWorkerEvidenceReceipts(gs.WorkerEvidenceReceipts, gs.InferReceipts, gs.Params.Evidence.MaxOutputMmrLeaves); err != nil {
		return err
	}
	verifierAssignments, err := validateVerifierAssignments(gs.VerifierAssignments, assignments)
	if err != nil {
		return err
	}
	return validateExtendedGenesisRows(gs, cores, assignments, verifierAssignments)
}

func validateSessionRows(gs GenesisState) error {
	seenNonces := map[string]struct{}{}
	for _, s := range gs.SessionNonces {
		user, err := requireCanonicalNonEmpty("session nonce user_address", s.UserAddress)
		if err != nil {
			return err
		}
		if err := addUnique(seenNonces, user, "duplicate session nonce for user_address %s", user); err != nil {
			return err
		}
	}

	streams := map[string]struct{}{}
	streamStates := map[string]StreamState{}
	for _, s := range gs.Streams {
		sid, err := requireHash32("stream session_id", s.SessionId)
		if err != nil {
			return err
		}
		if _, err := requireCanonicalNonEmpty("stream owner_user_address", s.OwnerUserAddress); err != nil {
			return err
		}
		if err := requireEnum("stream status", s.Status, SessionStatus_name); err != nil {
			return fmt.Errorf("%w for session_id %s", err, sid)
		}
		if err := addUnique(streams, sid, "duplicate stream for session_id %s", sid); err != nil {
			return err
		}
		streamStates[sid] = s
	}

	seenSeq := map[string]struct{}{}
	for _, s := range gs.OrderSequences {
		sid, err := requireHash32("order sequence session_id", s.SessionId)
		if err != nil {
			return err
		}
		if err := requireEnum("order sequence status", s.Status, OrderSequenceStatus_name); err != nil {
			return fmt.Errorf("%w for %s/%d", err, sid, s.OrderSequence)
		}
		switch s.Status {
		case OrderSequenceStatus_ORDER_SEQUENCE_STATUS_CONSUMED,
			OrderSequenceStatus_ORDER_SEQUENCE_STATUS_REFUNDED,
			OrderSequenceStatus_ORDER_SEQUENCE_STATUS_SETTLED:
			if len(s.TaskId) == 0 {
				return fmt.Errorf("order sequence %s/%d must include task_id when status is %s", sid, s.OrderSequence, s.Status)
			}
		case OrderSequenceStatus_ORDER_SEQUENCE_STATUS_CANCELLED:
			if len(s.TaskId) != 0 {
				return fmt.Errorf("cancelled order sequence %s/%d must not include task_id", sid, s.OrderSequence)
			}
		}
		if len(s.TaskId) != 0 {
			if _, err := requireHash32("order sequence task_id", s.TaskId); err != nil {
				return err
			}
		}
		key := fmt.Sprintf("%s\x00%d", sid, s.OrderSequence)
		if err := addUnique(seenSeq, key, "duplicate order sequence for %s/%d", sid, s.OrderSequence); err != nil {
			return err
		}
	}
	seenCursors := map[string]struct{}{}
	for _, cursor := range gs.SessionHistoryPruneCursors {
		sid, err := requireHash32("session history prune cursor session_id", cursor.SessionId)
		if err != nil {
			return err
		}
		stream, ok := streamStates[sid]
		if !ok || stream.Status != SessionStatus_SESSION_STATUS_CLOSED {
			return fmt.Errorf("session history prune cursor %s requires a CLOSED stream", sid)
		}
		if cursor.NextOrderSequence >= stream.NextExpectedSequence {
			return fmt.Errorf("session history prune cursor %s must remain before next_expected_sequence", sid)
		}
		if len(cursor.RollingSequenceRoot) != Hash32Len {
			return fmt.Errorf("session history prune cursor %s rolling_sequence_root must be 32 bytes", sid)
		}
		if err := addUnique(seenCursors, sid, "duplicate session history prune cursor %s", sid); err != nil {
			return err
		}
	}
	return nil
}

// validateTaskCores replaces the pre-unblock task status / task worker checks.
//
// TODO(task core migration): TaskStatusState and TaskWorkerState are deleted; the six
// sub-states are inline fields of TaskCoreState (§6.6). Two semantics were lost
// with them and must come back on this row:
//   - the winner Worker address is no longer on any core row (it moved to
//     TaskAssignmentState.winner_worker), so the "task worker references missing
//     task assignment" cross check is gone;
//   - §6.6 declares the six sub-states monotonic and mutually constrained
//     (e.g. receipt_status RECEIPT_ACCEPTED requires task_phase >=
//     RECEIPT_COMMITTED). Only per-field enum membership is checked here.
func validateTaskCores(states []TaskCoreState) (map[string]TaskCoreState, error) {
	cores := map[string]TaskCoreState{}
	for _, s := range states {
		tid, err := requireHash32("task core task_id", s.TaskId)
		if err != nil {
			return nil, err
		}
		if _, err := requireCanonicalNonEmpty("task core user_address", s.UserAddress); err != nil {
			return nil, err
		}
		if _, err := requireHash32("task core session_id", s.SessionId); err != nil {
			return nil, err
		}
		if err := shared.ValidateModelID(s.ModelId); err != nil {
			return nil, fmt.Errorf("task core %s: %w", tid, err)
		}
		if s.ProfileVersion == 0 {
			return nil, fmt.Errorf("task core %s profile_version must be greater than 0", tid)
		}
		orderValue, err := shared.ParseAmount(s.OrderValue)
		if err != nil || orderValue == 0 {
			return nil, fmt.Errorf("task core %s order_value must be a positive canonical amount", tid)
		}
		for _, field := range []struct {
			name  string
			value []byte
		}{
			{"accepted_task_hash", s.AcceptedTaskHash},
			{"accepted_input_hash", s.AcceptedInputHash},
			{"accepted_order_opening_hash", s.AcceptedOrderOpeningHash},
		} {
			if _, err := requireHash32("task core "+field.name, field.value); err != nil {
				return nil, err
			}
		}
		if err := requireEnum("task core task_type", s.TaskType, shared.TaskType_name); err != nil {
			return nil, fmt.Errorf("%w for %s", err, tid)
		}
		if err := requireEnum("task core task_phase", s.TaskPhase, TaskPhase_name); err != nil {
			return nil, fmt.Errorf("%w for %s", err, tid)
		}
		if err := requireEnum("task core assignment_status", s.AssignmentStatus, AssignmentStatus_name); err != nil {
			return nil, fmt.Errorf("%w for %s", err, tid)
		}
		if err := requireEnum("task core receipt_status", s.ReceiptStatus, ReceiptStatus_name); err != nil {
			return nil, fmt.Errorf("%w for %s", err, tid)
		}
		if err := requireEnum("task core verification_status", s.VerificationStatus, VerificationStatus_name); err != nil {
			return nil, fmt.Errorf("%w for %s", err, tid)
		}
		if err := requireEnum("task core settlement_status", s.SettlementStatus, SettlementStatus_name); err != nil {
			return nil, fmt.Errorf("%w for %s", err, tid)
		}
		if err := requireEnum("task core finality_status", s.FinalityStatus, shared.TaskFinalityStatusV1_name); err != nil {
			return nil, fmt.Errorf("%w for %s", err, tid)
		}
		if s.CreatedHeight == 0 {
			return nil, fmt.Errorf("task core %s created_height must be greater than 0", tid)
		}
		if s.UpdatedHeight < s.CreatedHeight {
			return nil, fmt.Errorf("task core %s updated_height must not precede created_height", tid)
		}
		if s.EvidenceRetentionBlocksSnapshot == 0 {
			return nil, fmt.Errorf("task core %s evidence_retention_blocks_snapshot must be positive", tid)
		}
		if s.ObjectiveForgerySlashBpsSnapshot > BasisPointsMaximum {
			return nil, fmt.Errorf("task core %s objective_forgery_slash_bps_snapshot exceeds %d", tid, BasisPointsMaximum)
		}
		if _, ok := cores[tid]; ok {
			return nil, fmt.Errorf("duplicate task core for %s", tid)
		}
		cores[tid] = s
	}
	return cores, nil
}

// validateTaskAssignments keeps the row-level assignment checks that survived.
//
// TODO(accepted order authority): the accepted order envelope no longer lives on this row, so
// every envelope-derived genesis assertion is gone and must be restored against
// TaskCoreState + the §5.13 task-id derivation:
//   - task_id == DeriveTaskIDFromRawSession(session_id, order_sequence);
//   - order_digest == sha256(order_envelope) and accepted_order_opening_hash ==
//     AcceptedTaskOrderOpeningHash(order, assignment.generation_params_digest);
//   - the 12-field equality between the row and the accepted envelope
//     (max_fee, tx_fee_reserve, infer_fee_cap, verify_fee_cap, order_value,
//     order_valid_after_height, model_id, profile_version, task_type,
//     reward_bucket, profile_resource_tier, requested_infer_timeout_blocks);
//   - signature_scheme == SignatureSchemeSecp256k1 and the user_signature /
//     accepted_order_payload_hash / accepted_item_hash presence checks.
func validateTaskAssignments(states []TaskAssignmentState, cores map[string]TaskCoreState) (map[string]TaskAssignmentState, error) {
	assignments := map[string]TaskAssignmentState{}
	for _, s := range states {
		tid, err := requireHash32("task assignment task_id", s.TaskId)
		if err != nil {
			return nil, err
		}
		core, ok := cores[tid]
		if !ok {
			return nil, fmt.Errorf("task assignment %s references missing task core", tid)
		}
		if err := requireEnumOrZero("task assignment assignment_fail_reason", s.AssignmentFailReason, AssignmentFailureReason_name); err != nil {
			return nil, fmt.Errorf("%w for %s", err, tid)
		}
		for _, field := range []struct {
			name  string
			value []byte
		}{
			{"candidate_pool_snapshot_id", s.CandidatePoolSnapshotId},
			{"candidate_pool_hash", s.CandidatePoolHash},
			{"assignment_candidate_set_hash", s.AssignmentCandidateSetHash},
			{"winner_draw_digest", s.WinnerDrawDigest},
			{"profile_execution_snapshot_hash", s.ProfileExecutionSnapshotHash},
			{"evidence_schema_hash", s.EvidenceSchemaHash},
			{"generation_params_digest", s.GenerationParamsDigest},
		} {
			if err := requireOptionalHash32("task assignment "+field.name, field.value); err != nil {
				return nil, fmt.Errorf("%w for %s", err, tid)
			}
		}
		if (core.AssignmentStatus == AssignmentStatus_ASSIGNMENT_STATUS_RANDOMNESS_PENDING ||
			core.AssignmentStatus == AssignmentStatus_ASSIGNMENT_STATUS_WORKER_ASSIGNED) && len(s.GenerationParamsDigest) != Hash32Len {
			return nil, fmt.Errorf("task assignment %s generation_params_digest must be 32 bytes while assignment is active", tid)
		}
		if s.WorkerInferTimeoutSlashBps > BasisPointsMaximum || s.ResultRevealMissingSlashBps > BasisPointsMaximum {
			return nil, fmt.Errorf("task assignment %s frozen fault slash bps exceed %d", tid, BasisPointsMaximum)
		}
		if s.WinnerWorker != "" {
			if _, err := requireCanonicalNonEmpty("task assignment winner_worker", s.WinnerWorker); err != nil {
				return nil, err
			}
		}
		if _, ok := assignments[tid]; ok {
			return nil, fmt.Errorf("duplicate task assignment for %s", tid)
		}
		assignments[tid] = s
	}
	return assignments, nil
}

// validateTaskBudgets keeps the §5.3 funds-conservation shape check.
//
// The remaining sub-accounts are exactly FOUR: assignment_priority_fee,
// tx_fee_reserve, infer_fee_cap and verify_fee_cap. maintenance_fee_cap_remaining
// and refund_remaining are deleted; gas_reimbursed_total is a cumulative total,
// not a remaining balance, so it stays out of the sum.
//
// TODO(settlement conservation): §5.3 asks for the full conservation invariant
// (original_reserved_amount == spent + refunded + remaining across the four
// sub-accounts + gas_reimbursed_total). The terminal zero-balance assertion is
// enforced below. The old blocker citation was stale — K-BLOCK-16 is closed and
// TaskSettlementState is now a stored row with Genesis field 71 — but the
// identity still cannot be re-derived here: keeper_api_contract.md §10.10c writes
// it against
// `apply_start_reserved_amount`, and neither that term nor a gas reimbursement
// total is carried on TaskSettlementState. Re-deriving it from the terms that
// are present would assert a different equation than the one the contract
// froze, so this stays open on the two missing fields rather than on a blocker.
//
// TODO(parameter bucket authority): reference_bucket_key / reference_bucket_version /
// timeout_bucket_key / timeout_bucket_version and the
// reference_fee_floor <= reference_fee_ceiling bound left this row; the bucket
// refs are now TaskBucketRefState (§6.7) and the fee window belongs to the
// x/hub reference bucket. fee_rule_version below is their only successor.
func validateTaskBudgets(states []TaskBudgetState, assignments map[string]TaskAssignmentState, cores map[string]TaskCoreState) error {
	budgets := map[string]struct{}{}
	for _, s := range states {
		tid, err := requireHash32("task budget task_id", s.TaskId)
		if err != nil {
			return err
		}
		if err := requireEnum("task budget budget_status", s.BudgetStatus, TaskBudgetStatus_name); err != nil {
			return fmt.Errorf("%w for %s", err, tid)
		}
		if _, ok := assignments[tid]; !ok {
			return fmt.Errorf("task budget %s references missing task assignment", tid)
		}
		if s.FeeRuleVersion == 0 {
			return fmt.Errorf("task budget %s fee_rule_version must be a registered nonzero version", tid)
		}
		reserved, err := shared.ParseAmount(s.ReservedAmount)
		if err != nil {
			return fmt.Errorf("task budget %s reserved_amount: %w", tid, err)
		}
		original, err := shared.ParseAmount(s.OriginalReservedAmount)
		if err != nil {
			return fmt.Errorf("task budget %s original_reserved_amount: %w", tid, err)
		}
		core, ok := cores[tid]
		if !ok {
			return fmt.Errorf("task budget %s references missing task core", tid)
		}
		orderValue, err := shared.ParseAmount(core.OrderValue)
		if err != nil || orderValue == 0 || orderValue > original {
			return fmt.Errorf("task budget %s does not cover the frozen order_value", tid)
		}
		if _, err := shared.ParseAmount(s.GasReimbursedTotal); err != nil {
			return fmt.Errorf("task budget %s gas_reimbursed_total: %w", tid, err)
		}
		for _, field := range []struct {
			name  string
			value shared.Amount
		}{
			{"tx_fee_reserve_remaining", s.TxFeeReserveRemaining},
			{"price_bid", s.PriceBid},
			{"worker_max", s.WorkerMax},
			{"verify_max", s.VerifyMax},
			{"max_reimbursement_per_tx_snapshot", s.MaxReimbursementPerTxSnapshot},
			{"max_reimbursement_per_task_snapshot", s.MaxReimbursementPerTaskSnapshot},
		} {
			if _, err := shared.ParseAmount(field.value); err != nil {
				return fmt.Errorf("task budget %s %s: %w", tid, field.name, err)
			}
		}
		// keeper_api_contract.md:4995 pins `bps<=10000` on the frozen fee params, and
		// §10.10c
		// derives maintenance/verify shares by mulDiv against 10000. An import above
		// the bound makes maintenance exceed its gross, which is the one subtraction
		// the settlement identity cannot absorb.
		if s.MaintenanceRateBpsSnapshot > BasisPointsMaximum {
			return fmt.Errorf("task budget %s maintenance_rate_bps_snapshot exceeds %d", tid, BasisPointsMaximum)
		}
		if s.VerifyRatioBpsSnapshot > BasisPointsMaximum {
			return fmt.Errorf("task budget %s verify_ratio_bps_snapshot exceeds %d", tid, BasisPointsMaximum)
		}
		if s.BudgetStatus == TaskBudgetStatus_TASK_BUDGET_STATUS_FINALIZED && reserved != 0 {
			return fmt.Errorf("finalized task budget %s must have zero remaining balances", tid)
		}
		if err := addUnique(budgets, tid, "duplicate task budget for %s", tid); err != nil {
			return err
		}
	}
	for tid := range assignments {
		if _, ok := budgets[tid]; !ok {
			return fmt.Errorf("task assignment %s references missing task budget", tid)
		}
	}
	return nil
}

// validateAssignmentCandidateSets keeps the header-level commitment equality.
//
// TODO(candidate fact migration): AssignmentCandidateSetState.candidates[] and the
// AssignmentCandidate message are deleted (§4.2 moves the frozen facts to
// TaskCandidateFactState keyed by (task_id, stage, slot)). Everything the
// pre-unblock validator asserted per candidate is therefore gone and must be
// re-asserted against task_candidate_facts:
//   - canonical ascending worker order with no duplicate worker;
//   - 0 < candidate_weight <= MaxAssignmentCandidateWeight and a non-overflowing
//     total weight (§4.5 needs totalWeight > 0 or the chain stalls);
//   - active_bond_snapshot >= min_stake_snapshot > 0,
//     performance_score_snapshot_ppm <= 1_000_000 (Ruling 21 pins 1_000_000),
//     performance_method_version != 0,
//     0 < candidate_jail_factor_snapshot_ppm <= 1_000_000,
//     bond_version_snapshot != 0, support_version_snapshot != 0;
//   - 0 < min_handraise_required <= legal_worker_handraise_count <= 210, and
//     len(candidates) <= legal_worker_handraise_count.
//
// legal_set_hash, order_digest, model_id, profile_version, task_type,
// reward_bucket, profile_resource_tier and legal_worker_handraise_count also left
// this message, so the eight-way equality against TaskAssignmentState shrank to
// the three fields both rows still carry.
func validateAssignmentCandidateSets(states []AssignmentCandidateSetState, assignments map[string]TaskAssignmentState) error {
	seen := map[string]struct{}{}
	for _, s := range states {
		tid, err := requireHash32("assignment candidate set task_id", s.TaskId)
		if err != nil {
			return err
		}
		if s.SchemaVersion == 0 {
			return fmt.Errorf("assignment candidate set %s schema_version must be greater than 0", tid)
		}
		for _, field := range []struct {
			name  string
			value []byte
		}{
			{"task_hash", s.TaskHash},
			{"candidate_pool_snapshot_id", s.CandidatePoolSnapshotId},
			{"candidate_pool_hash", s.CandidatePoolHash},
			{"union_bitmap_hash", s.UnionBitmapHash},
			{"assignment_candidate_set_hash", s.AssignmentCandidateSetHash},
		} {
			if _, err := requireHash32("assignment candidate set "+field.name, field.value); err != nil {
				return err
			}
		}
		if s.CandidateCount == 0 {
			return fmt.Errorf("assignment candidate set %s must contain at least one candidate", tid)
		}
		assignment, ok := assignments[tid]
		if !ok {
			return fmt.Errorf("assignment candidate set %s references missing task assignment", tid)
		}
		if !bytes.Equal(s.CandidatePoolSnapshotId, assignment.CandidatePoolSnapshotId) ||
			!bytes.Equal(s.CandidatePoolHash, assignment.CandidatePoolHash) ||
			!bytes.Equal(s.AssignmentCandidateSetHash, assignment.AssignmentCandidateSetHash) {
			return fmt.Errorf("assignment candidate set %s does not match task assignment commitments", tid)
		}
		if err := addUnique(seen, tid, "duplicate assignment candidate set for %s", tid); err != nil {
			return err
		}
	}
	for tid := range assignments {
		if _, ok := seen[tid]; !ok {
			return fmt.Errorf("task assignment %s is missing assignment candidate set", tid)
		}
	}
	return nil
}

// validateInferReceipts keeps the receipt/assignment binding.
//
// The pre-unblock validator also asserted
// receipt_mode ∈ {OPEN_VERIFY, RECEIPT_ONLY}, service_signature presence,
// trace_commit_root / checkpoint_commit_root / batch_log_root shape,
// accepted_item_hash == infer_receipt_hash, and token_count / work_unit > 0.
// receipt_mode, service_signature, the two commit roots, batch_log_root and
// accepted_item_hash no longer exist on this row; token_count / work_unit moved
// to the SETTLEMENT_BILL evidence leaf (Ruling 32), so metering is committed, not
// validated here. evidence_commitment_count vs
// len(required_evidence_commitments) and the evidence_commitments_hash
// derivation itself are #89 (U-2), not checked here.
func validateInferReceipts(states []InferReceiptState, assignments map[string]TaskAssignmentState, budgets []TaskBudgetState, maxOutputMMRLeaves uint64) error {
	seen := map[string]struct{}{}
	budgetByTask := make(map[string]TaskBudgetState, len(budgets))
	for _, budget := range budgets {
		budgetByTask[hex.EncodeToString(budget.TaskId)] = budget
	}
	for _, s := range states {
		tid, err := requireHash32("infer receipt task_id", s.TaskId)
		if err != nil {
			return err
		}
		assignment, ok := assignments[tid]
		if !ok {
			return fmt.Errorf("infer receipt %s references missing task assignment", tid)
		}
		if _, err := requireCanonicalNonEmpty("infer receipt winner_worker", s.WinnerWorker); err != nil {
			return err
		}
		for _, field := range []struct {
			name  string
			value []byte
		}{
			{"infer_receipt_hash", s.InferReceiptHash},
			{"output_hash", s.OutputHash},
			{"infer_receipt_signing_digest", s.InferReceiptSigningDigest},
			{"signature_digest", s.SignatureDigest},
		} {
			if _, err := requireHash32("infer receipt "+field.name, field.value); err != nil {
				return err
			}
		}
		for _, field := range []struct {
			name  string
			value []byte
		}{
			{"evidence_commitments_hash", s.EvidenceCommitmentsHash},
		} {
			if err := requireOptionalHash32("infer receipt "+field.name, field.value); err != nil {
				return fmt.Errorf("%w for %s", err, tid)
			}
		}
		if _, err := requireHash32("infer receipt generation_params_digest", s.GenerationParamsDigest); err != nil {
			return fmt.Errorf("%w for %s", err, tid)
		}
		if s.WinnerWorker != assignment.WinnerWorker {
			return fmt.Errorf("infer receipt %s winner_worker does not match task assignment", tid)
		}
		budget, hasBudget := budgetByTask[tid]
		if !hasBudget || budget.MaxOutputTokens == 0 || s.GeneratedTokenCount > budget.MaxOutputTokens {
			return fmt.Errorf("infer receipt %s generated_token_count exceeds its frozen Task budget", tid)
		}
		if s.OutputLeafCount == 0 || s.OutputLeafCount > maxOutputMMRLeaves {
			return fmt.Errorf("infer receipt %s output_leaf_count is outside the registered limit", tid)
		}
		if s.ReceiptHeight == 0 {
			return fmt.Errorf("infer receipt %s must include receipt_height", tid)
		}
		if err := addUnique(seen, tid, "duplicate infer receipt for %s", tid); err != nil {
			return err
		}
	}
	return nil
}

func validateWorkerEvidenceReceipts(states []WorkerEvidenceReceiptState, inferReceipts []InferReceiptState, maxLeaves uint64) error {
	inferByTask := make(map[string]InferReceiptState, len(inferReceipts))
	for _, receipt := range inferReceipts {
		inferByTask[hex.EncodeToString(receipt.TaskId)] = receipt
	}
	seen := make(map[string]struct{}, len(states))
	for _, state := range states {
		taskID, err := requireHash32("worker evidence receipt task_id", state.TaskId)
		if err != nil {
			return err
		}
		if state.SchemaVersion != WorkerEvidenceSchemaVersionV1 ||
			state.EvidenceKind != WorkerEvidenceKindV1_WORKER_EVIDENCE_KIND_V1_OUTPUT_CHUNK_EQUIVOCATION || state.AcceptedHeight == 0 {
			return fmt.Errorf("worker evidence receipt %s has invalid schema, kind, or height", taskID)
		}
		if _, err := requireCanonicalNonEmpty("worker evidence receipt worker_operator_address", state.WorkerOperatorAddress); err != nil {
			return err
		}
		for _, field := range []struct {
			name  string
			value []byte
		}{{"accepted_infer_receipt_hash", state.AcceptedInferReceiptHash}, {"evidence_digest", state.EvidenceDigest}, {"fault_id", state.FaultId}} {
			if _, err := requireHash32("worker evidence receipt "+field.name, field.value); err != nil {
				return err
			}
		}
		infer, ok := inferByTask[taskID]
		if !ok || infer.WinnerWorker != state.WorkerOperatorAddress ||
			!bytes.Equal(infer.InferReceiptHash, state.AcceptedInferReceiptHash) || state.Seq >= infer.OutputLeafCount ||
			infer.OutputLeafCount > maxLeaves {
			return fmt.Errorf("worker evidence receipt %s does not match its accepted InferReceipt", taskID)
		}
		identity := fmt.Sprintf("%s/%s/%d/%d", taskID, state.WorkerOperatorAddress, state.EvidenceKind, state.Seq)
		if err := addUnique(seen, identity, "duplicate worker evidence receipt %s", identity); err != nil {
			return err
		}
	}
	return nil
}

// validateVerifierAssignments keeps the deadline ordering checks.
//
// VerifierCandidateSetState is deleted; VerifierCandidateWindowState (§4.4) is
// its replacement and the Keeper Init/Export layer performs the cross-row
// source/window/member/hash validation. The sample-seed surface is gone too:
// sample_seed_status / sample_seed_ready_height / formal_verifier_set (the
// canonical 3-entry CSV) / verifier_handraise_list left this row, so
// "commit deadline must be after sample seed ready height" and the CSV3 parse
// are dropped. This types-level pass only checks local shape and ordering; the
// Keeper layer checks selected rows, counts, frozen facts and commitments.
func validateVerifierAssignments(states []VerifierAssignmentState, assignments map[string]TaskAssignmentState) (map[string]VerifierAssignmentState, error) {
	verifierAssignments := map[string]VerifierAssignmentState{}
	for _, s := range states {
		tid, err := requireHash32("verifier assignment task_id", s.TaskId)
		if err != nil {
			return nil, err
		}
		if !isGenesisVerifyRound(s.VerifyRound) {
			return nil, fmt.Errorf("verifier assignment %s verify_round is unsupported", tid)
		}
		if _, ok := assignments[tid]; !ok {
			return nil, fmt.Errorf("verifier assignment %s references missing task assignment", tid)
		}
		for _, field := range []struct {
			name  string
			value []byte
		}{
			{"verifier_candidate_window_hash", s.VerifierCandidateWindowHash},
			{"verifier_legal_set_hash", s.VerifierLegalSetHash},
			{"selected_verifiers_hash", s.SelectedVerifiersHash},
		} {
			if err := requireOptionalHash32("verifier assignment "+field.name, field.value); err != nil {
				return nil, fmt.Errorf("%w for %s/%d", err, tid, s.VerifyRound)
			}
		}
		if s.OpenVerifyHeight == 0 || s.CommitDeadlineHeight == 0 || s.VerifyDeadlineHeight == 0 {
			return nil, fmt.Errorf("verifier assignment %s/%d must have open verify, commit, and verify deadlines", tid, s.VerifyRound)
		}
		if s.VerifyDeadlineHeight <= s.CommitDeadlineHeight {
			return nil, fmt.Errorf("verifier assignment %s/%d verify deadline must be after commit deadline", tid, s.VerifyRound)
		}
		key := verifyRoundKey(tid, s.VerifyRound)
		if _, ok := verifierAssignments[key]; ok {
			return nil, fmt.Errorf("duplicate verifier assignment for %s/%d", tid, s.VerifyRound)
		}
		verifierAssignments[key] = s
	}
	return verifierAssignments, nil
}

// validateExtendedGenesisRows closes the v0.3 primary-key graph. Canonical
// commitment algorithms remain owned by their production helpers; this pass
// rejects duplicate/orphan rows and impossible lifecycle combinations before
// Keeper writes any state.
func validateExtendedGenesisRows(
	gs GenesisState,
	cores map[string]TaskCoreState,
	assignments map[string]TaskAssignmentState,
	verifierAssignments map[string]VerifierAssignmentState,
) error {
	streams := make(map[string]StreamState, len(gs.Streams))
	for _, stream := range gs.Streams {
		streams[hex.EncodeToString(stream.SessionId)] = stream
	}
	sessionSummaries := make(map[string]struct{}, len(gs.SessionTerminalSummaries))
	for _, summary := range gs.SessionTerminalSummaries {
		sid, err := requireHash32("session terminal summary session_id", summary.SessionId)
		if err != nil {
			return err
		}
		if _, live := streams[sid]; live {
			return fmt.Errorf("session %s has both stream and terminal summary", sid)
		}
		if err := addUnique(sessionSummaries, sid, "duplicate session terminal summary for %s", sid); err != nil {
			return err
		}
	}
	for tid, core := range cores {
		stream, ok := streams[hex.EncodeToString(core.SessionId)]
		if !ok {
			return fmt.Errorf("task core %s references missing stream", tid)
		}
		if stream.OwnerUserAddress != core.UserAddress {
			return fmt.Errorf("task core %s user does not match stream owner", tid)
		}
	}

	taskRows := func(label string, taskID []byte, seen map[string]struct{}, suffix string) (string, error) {
		tid, err := requireHash32(label+" task_id", taskID)
		if err != nil {
			return "", err
		}
		if _, ok := cores[tid]; !ok {
			return "", fmt.Errorf("%s %s references missing task core", label, tid)
		}
		key := tid + suffix
		if err := addUnique(seen, key, "duplicate %s primary key %s", label, key); err != nil {
			return "", err
		}
		return tid, nil
	}

	stageSeen := map[string]struct{}{}
	for _, state := range gs.TaskCandidateFacts {
		if _, err := taskRows("task candidate fact", state.TaskId, stageSeen, fmt.Sprintf("/%d/%d", state.Stage, state.Slot)); err != nil {
			return err
		}
		if err := requireEnum("task candidate fact stage", state.Stage, TaskCandidateStage_name); err != nil {
			return err
		}
		if err := requireEnum("task candidate fact duty", state.Duty, shared.Duty_name); err != nil {
			return err
		}
	}
	proposalSeen := map[string]struct{}{}
	for _, state := range gs.BuilderStageProposals {
		digest, err := requireHash32("builder proposal digest", state.ProposalDigest)
		if err != nil {
			return err
		}
		if _, err := taskRows("builder proposal", state.TaskId, proposalSeen, fmt.Sprintf("/%d/%s", state.Stage, digest)); err != nil {
			return err
		}
	}
	unionSeen := map[string]struct{}{}
	for _, state := range gs.TaskStageHandraiseUnions {
		if _, err := taskRows("handraise union", state.TaskId, unionSeen, fmt.Sprintf("/%d", state.Stage)); err != nil {
			return err
		}
		if err := requireEnum("handraise union status", state.Status, TaskCandidateStageStatusV1_name); err != nil {
			return err
		}
	}
	segmentSeen := map[string]struct{}{}
	for _, state := range gs.TaskStageHandraiseUnionSegments {
		if _, err := taskRows("handraise union segment", state.TaskId, segmentSeen, fmt.Sprintf("/%d/%d", state.Stage, state.SegmentIndex)); err != nil {
			return err
		}
	}
	cursorSeen := map[string]struct{}{}
	for _, state := range gs.TaskCandidateFinalizeCursors {
		if _, err := taskRows("candidate finalize cursor", state.TaskId, cursorSeen, fmt.Sprintf("/%d", state.Stage)); err != nil {
			return err
		}
		if err := requireEnum("candidate finalize cursor status", state.Status, FinalizeCursorStatusV1_name); err != nil {
			return err
		}
	}
	selectionSeen := map[string]struct{}{}
	for _, state := range gs.TaskBuilderSelections {
		if _, err := taskRows("task builder selection", state.TaskId, selectionSeen, ""); err != nil {
			return err
		}
		if uint32(len(state.SelectedTaskBuilders)) != state.SelectedTaskBuilderCount &&
			state.BodyStatus == shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE {
			return fmt.Errorf("task builder selection count does not match active body")
		}
	}
	bucketSeen := map[string]struct{}{}
	for _, state := range gs.TaskBucketRefs {
		if state.BucketKey == "" || state.Version == 0 {
			return fmt.Errorf("task bucket ref requires bucket_key and version")
		}
		if _, err := taskRows("task_bucket_ref", state.TaskId, bucketSeen, fmt.Sprintf("/%d/%s", state.BucketKind, state.BucketKey)); err != nil {
			return err
		}
	}

	windowSeen := map[string]struct{}{}
	for _, state := range gs.VerifierCandidateWindows {
		if !isGenesisVerifyRound(state.VerifyRound) {
			return fmt.Errorf("verifier candidate window has unsupported verify_round %d", state.VerifyRound)
		}
		if _, err := taskRows("verifier candidate window", state.TaskId, windowSeen, fmt.Sprintf("/%d", state.VerifyRound)); err != nil {
			return err
		}
		if err := requireEnum("verifier candidate window status", state.Status, VerifierCandidateWindowStatusV1_name); err != nil {
			return err
		}
	}
	eligibilitySeen := map[string]struct{}{}
	for _, state := range gs.VerifierCandidateEligibilitySegments {
		if !isGenesisVerifyRound(state.VerifyRound) {
			return fmt.Errorf("verifier eligibility segment has unsupported verify_round %d", state.VerifyRound)
		}
		if _, err := taskRows("verifier eligibility segment", state.TaskId, eligibilitySeen, fmt.Sprintf("/%d/%d", state.VerifyRound, state.SegmentIndex)); err != nil {
			return err
		}
	}
	memberSeen := map[string]struct{}{}
	for _, state := range gs.VerifierCandidateWindowMembers {
		if !isGenesisVerifyRound(state.VerifyRound) {
			return fmt.Errorf("verifier window member has unsupported verify_round %d", state.VerifyRound)
		}
		if _, err := taskRows("verifier window member", state.TaskId, memberSeen, fmt.Sprintf("/%d/%d", state.VerifyRound, state.RankIndex)); err != nil {
			return err
		}
	}

	commits := make(map[string]CommitState, len(gs.Commits))
	for _, state := range gs.Commits {
		commitKey, err := requireHash32("commit key", state.CommitKey)
		if err != nil {
			return err
		}
		tid, err := requireHash32("commit task_id", state.TaskId)
		if err != nil {
			return err
		}
		if _, ok := cores[tid]; !ok {
			return fmt.Errorf("commit %s references missing task core", commitKey)
		}
		if _, ok := verifierAssignments[verifyRoundKey(tid, state.VerifyRound)]; !ok {
			return fmt.Errorf("commit %s references missing verifier assignment", commitKey)
		}
		if _, duplicate := commits[commitKey]; duplicate {
			return fmt.Errorf("duplicate commit %s", commitKey)
		}
		commits[commitKey] = state
	}
	resultSeen := map[string]struct{}{}
	for _, state := range gs.ResultReceipts {
		commitKey, err := requireHash32("result receipt commit_key", state.CommitKey)
		if err != nil {
			return err
		}
		commit, ok := commits[commitKey]
		if !ok {
			return fmt.Errorf("result receipt %s references missing commit", commitKey)
		}
		if !bytes.Equal(state.TaskId, commit.TaskId) || state.VerifyRound != commit.VerifyRound ||
			state.VerifierOperatorAddress != commit.VerifierOperatorAddress {
			return fmt.Errorf("result receipt %s does not match commit scope", commitKey)
		}
		if err := addUnique(resultSeen, commitKey, "duplicate result receipt %s", commitKey); err != nil {
			return err
		}
	}
	reportSeen := map[string]struct{}{}
	for _, state := range gs.DataUnavailableReports {
		if !isGenesisVerifyRound(state.VerifyRound) {
			return fmt.Errorf("data unavailable report has unsupported verify_round %d", state.VerifyRound)
		}
		if _, err := taskRows("data unavailable report", state.TaskId, reportSeen, fmt.Sprintf("/%d/%s", state.VerifyRound, state.VerifierOperatorAddress)); err != nil {
			return err
		}
	}
	aggregateSeen := map[string]struct{}{}
	for _, state := range gs.BuilderDataUnavailableAggregates {
		if !isGenesisVerifyRound(state.VerifyRound) {
			return fmt.Errorf("builder data unavailable aggregate has unsupported verify_round %d", state.VerifyRound)
		}
		if _, err := taskRows("builder data unavailable aggregate", state.TaskId, aggregateSeen, fmt.Sprintf("/%d/%s", state.VerifyRound, state.BuilderOperatorAddress)); err != nil {
			return err
		}
	}

	rounds := make(map[string]VerificationRoundState, len(gs.VerificationRounds))
	for _, state := range gs.VerificationRounds {
		if !isGenesisVerifyRound(state.VerifyRound) {
			return fmt.Errorf("verification round has unsupported verify_round %d", state.VerifyRound)
		}
		tid, err := taskRows("verification round", state.TaskId, map[string]struct{}{}, "")
		if err != nil {
			return err
		}
		key := verifyRoundKey(tid, state.VerifyRound)
		if _, duplicate := rounds[key]; duplicate {
			return fmt.Errorf("duplicate verification round %s", key)
		}
		rounds[key] = state
	}
	fundingSeen := map[string]struct{}{}
	for _, state := range gs.RoundFundings {
		if state.VerifyRound != ChallengeVerifyRoundV1 {
			return fmt.Errorf("round funding is only valid for verify_round %d", ChallengeVerifyRoundV1)
		}
		tid, err := taskRows("round funding", state.TaskId, fundingSeen, fmt.Sprintf("/%d", state.VerifyRound))
		if err != nil {
			return err
		}
		if _, ok := rounds[verifyRoundKey(tid, state.VerifyRound)]; !ok {
			return fmt.Errorf("round funding references missing verification round")
		}
	}
	effectSeen := map[string]struct{}{}
	effectCount := map[string]uint32{}
	pendingEffects := map[string]uint32{}
	for _, state := range gs.RoundEconomicEffects {
		if !isGenesisVerifyRound(state.VerifyRound) {
			return fmt.Errorf("round economic effect has unsupported verify_round %d", state.VerifyRound)
		}
		tid, err := taskRows("round economic effect", state.TaskId, effectSeen, fmt.Sprintf("/%d/%d", state.VerifyRound, state.Effect.EffectIndex))
		if err != nil {
			return err
		}
		roundKey := verifyRoundKey(tid, state.VerifyRound)
		if _, ok := rounds[roundKey]; !ok {
			return fmt.Errorf("round economic effect references missing verification round")
		}
		effectCount[roundKey]++
		if state.Effect.Status == shared.RoundEconomicEffectStatusV1_ROUND_ECONOMIC_EFFECT_STATUS_V1_PENDING {
			pendingEffects[roundKey]++
		}
	}
	effectCursorSeen := map[string]struct{}{}
	effectCursorByRound := map[string]struct{}{}
	for _, state := range gs.RoundEconomicEffectApplyCursors {
		tid, err := taskRows("round economic effect cursor", state.TaskId, effectCursorSeen, fmt.Sprintf("/%d", state.VerifyRound))
		if err != nil {
			return err
		}
		key := verifyRoundKey(tid, state.VerifyRound)
		if pendingEffects[key] == 0 || state.NextApplyHeight == 0 {
			return fmt.Errorf("round economic effect cursor has no pending effect or next height")
		}
		effectCursorByRound[key] = struct{}{}
	}
	for key, count := range pendingEffects {
		if count != 0 {
			if _, ok := effectCursorByRound[key]; !ok {
				return fmt.Errorf("pending round effects %s have no apply cursor", key)
			}
		}
	}
	for key, round := range rounds {
		if effectCount[key] != round.RoundEffectCount {
			return fmt.Errorf("verification round %s effect count mismatch", key)
		}
	}

	summarySeen := map[string]struct{}{}
	for _, state := range gs.TaskRoundSummaries {
		if _, err := taskRows("task round summary", state.TaskId, summarySeen, ""); err != nil {
			return err
		}
		if state.OpenRoundCount > 2 || state.MaxClosedRound > ChallengeVerifyRoundV1 {
			return fmt.Errorf("task round summary exceeds the Phase 0 round bound")
		}
	}
	gasSeen := map[string]struct{}{}
	for _, state := range gs.TaskGasReimbursements {
		txHash, err := requireHash32("gas reimbursement tx_hash", state.TxHash)
		if err != nil {
			return err
		}
		if _, err := taskRows("gas reimbursement", state.TaskId, gasSeen, fmt.Sprintf("/%s/%d", txHash, state.ItemIndex)); err != nil {
			return err
		}
	}

	settlements := make(map[string]TaskSettlementState, len(gs.TaskSettlements))
	for _, state := range gs.TaskSettlements {
		tid, err := requireHash32("task settlement task_id", state.TaskId)
		if err != nil {
			return err
		}
		if _, ok := cores[tid]; !ok {
			return fmt.Errorf("task settlement %s references missing task core", tid)
		}
		if _, duplicate := settlements[tid]; duplicate {
			return fmt.Errorf("duplicate task settlement %s", tid)
		}
		if _, err := requireHash32("task settlement settlement_id", state.SettlementId); err != nil {
			return err
		}
		if _, err := shared.ParseAmount(state.GasReimbursedTotal); err != nil {
			return fmt.Errorf("task settlement %s gas_reimbursed_total: %w", tid, err)
		}
		settlements[tid] = state
	}
	retainedSeen := map[string]struct{}{}
	for _, state := range gs.SettlementFactsRetained {
		tid, err := taskRows("settlement facts retained", state.TaskId, retainedSeen, "")
		if err != nil {
			return err
		}
		settlement, ok := settlements[tid]
		if !ok || !bytes.Equal(state.SettlementId, settlement.SettlementId) ||
			!bytes.Equal(state.SettlementFactsHash, settlement.SettlementFactsHash) {
			return fmt.Errorf("settlement facts retained %s does not match settlement", tid)
		}
	}
	payoutSeen := map[string]struct{}{}
	for _, state := range gs.VerifierPayouts {
		tid, err := taskRows("verifier payout", state.TaskId, payoutSeen, fmt.Sprintf("/%d", state.SelectedVerifierIndex))
		if err != nil {
			return err
		}
		settlement, ok := settlements[tid]
		if !ok || !bytes.Equal(state.SettlementId, settlement.SettlementId) {
			return fmt.Errorf("verifier payout %s does not match settlement", tid)
		}
	}
	for tid, core := range cores {
		_, settled := settlements[tid]
		switch core.FinalityStatus {
		case shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_PENDING:
			if settled || core.XTaskFinalityHeight != nil {
				return fmt.Errorf("pending task %s carries final settlement state", tid)
			}
		case shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL:
			if !settled || core.XTaskFinalityHeight == nil {
				return fmt.Errorf("final task %s is missing settlement or finality height", tid)
			}
		}
	}

	failureSeen := map[string]struct{}{}
	terminalTasks := map[string]struct{}{}
	for _, state := range gs.TaskTerminalSummaries {
		tid, err := requireHash32("task terminal summary task_id", state.TaskId)
		if err != nil {
			return err
		}
		if _, live := cores[tid]; live {
			return fmt.Errorf("task %s has both active core and terminal summary", tid)
		}
		if err := addUnique(terminalTasks, tid, "duplicate task terminal summary %s", tid); err != nil {
			return err
		}
	}
	for _, state := range gs.TaskFailureClasses {
		tid, err := requireHash32("task failure class task_id", state.TaskId)
		if err != nil {
			return err
		}
		if _, live := cores[tid]; !live {
			if _, terminal := terminalTasks[tid]; !terminal {
				return fmt.Errorf("task failure class %s has no task authority", tid)
			}
		}
		if !isGenesisVerifyRound(state.VerifyRound) {
			return fmt.Errorf("task failure class has unsupported verify_round %d", state.VerifyRound)
		}
		if err := addUnique(failureSeen, verifyRoundKey(tid, state.VerifyRound), "duplicate task failure class %s/%d", tid, state.VerifyRound); err != nil {
			return err
		}
	}
	cleanupSeen := map[string]struct{}{}
	for _, state := range gs.TaskCleanupCursors {
		if _, err := taskRows("task cleanup cursor", state.TaskId, cleanupSeen, ""); err != nil {
			return err
		}
		if err := requireEnum("task cleanup cursor phase", state.Phase, TaskCleanupPhase_name); err != nil {
			return err
		}
	}

	epochCursorSeen := map[string]struct{}{}
	for _, state := range gs.EpochTaskSummaryCursors {
		key := fmt.Sprintf("%d", state.Epoch)
		if state.DueHeight == 0 || state.StartHeight > state.EndHeight {
			return fmt.Errorf("epoch task summary cursor %s has invalid bounds", key)
		}
		if err := addUnique(epochCursorSeen, key, "duplicate epoch task summary cursor %s", key); err != nil {
			return err
		}
	}
	epochReceiptSeen := map[string]struct{}{}
	for _, state := range gs.EpochTaskSummaryReceipts {
		key := fmt.Sprintf("%d", state.Epoch)
		if state.DueHeight == 0 || state.StartHeight > state.EndHeight {
			return fmt.Errorf("epoch task summary receipt %s has invalid bounds", key)
		}
		if err := addUnique(epochReceiptSeen, key, "duplicate epoch task summary receipt %s", key); err != nil {
			return err
		}
		if _, running := epochCursorSeen[key]; running {
			return fmt.Errorf("epoch %s has both summary cursor and receipt", key)
		}
	}
	return nil
}

func isGenesisVerifyRound(round uint32) bool {
	return round == VerifyRoundV1 || round == ChallengeVerifyRoundV1
}

// validateVerifierRoundRows validates the commit / result / reveal rows.
//
// Keys are commit_key now (§10.9), not (session_id, task_id, verify_round,
// verifier_operator_address); result receipts and full result reveals are keyed by
// the same commit_key and are therefore checked against the commit row.
//
// accepted_item_hash left all three rows. ResultReceiptState no longer carries
// task_id / verify_round / verifier_operator_address, so its round membership is
// reached through its CommitState. The Keeper layer additionally re-derives the
// chain-bound commit key and all retained commitments.
func verifyRoundKey(taskID string, verifyRound uint32) string {
	return fmt.Sprintf("%s\x00%d", taskID, verifyRound)
}

// requireHash32 rejects a missing or wrong-length Hash32 and returns its
// lowercase hex rendering, used here purely as an in-memory map key and as
// human-facing text in the error messages of this file. The store keys
// themselves carry the raw 32 bytes (key_codec.go).
func requireHash32(name string, value []byte) (string, error) {
	if len(value) == 0 {
		return "", fmt.Errorf("%s is required", name)
	}
	if len(value) != Hash32Len {
		return "", fmt.Errorf("%s must be %d bytes, got %d", name, Hash32Len, len(value))
	}
	return hex.EncodeToString(value), nil
}

// requireOptionalHash32 allows an absent field but rejects a wrong-length one.
func requireOptionalHash32(name string, value []byte) error {
	if len(value) == 0 {
		return nil
	}
	if len(value) != Hash32Len {
		return fmt.Errorf("%s must be %d bytes, got %d", name, Hash32Len, len(value))
	}
	return nil
}

// requireEnum rejects the zero value and any number outside the frozen enum.
// Every Task enum documents value 0 as "never written; rejects zero-value rows".
func requireEnum[T ~int32](name string, value T, names map[int32]string) error {
	if int32(value) == 0 {
		return fmt.Errorf("%s is required", name)
	}
	if _, ok := names[int32(value)]; !ok {
		return fmt.Errorf("invalid %s %d", name, int32(value))
	}
	return nil
}

// requireEnumOrZero allows the zero value for enums whose 0 is a legal state
// (AssignmentFailureReason 0 = "no failure recorded").
func requireEnumOrZero[T ~int32](name string, value T, names map[int32]string) error {
	if _, ok := names[int32(value)]; !ok {
		return fmt.Errorf("invalid %s %d", name, int32(value))
	}
	return nil
}

func addUnique(seen map[string]struct{}, key, format string, args ...any) error {
	if _, ok := seen[key]; ok {
		return fmt.Errorf(format, args...)
	}
	seen[key] = struct{}{}
	return nil
}
