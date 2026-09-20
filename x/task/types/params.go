package types

import (
	"fmt"
	"strings"

	shared "github.com/TrueOpen/node/x/shared/types"
)

const (
	TaskParamsSchemaVersion = uint32(1)
	PartsPerMillion         = uint32(1_000_000)
	BasisPointsMaximum      = uint32(10_000)
	TaskFeeRuleVersionV2    = uint64(2)
	TaskFeePolicyVersionV1  = uint64(1)
)

// ValidateFrozenSlashBps is the single bound on the Builder fault rate that
// TaskBuilderSelectionState freezes and that BuilderObjectiveEvidenceFactV2
// carries to the Hub fault kernel. Both sides check it, because the value
// crosses a keeper boundary and neither module may assume the other's read.
func ValidateFrozenSlashBps(bps uint32) error {
	if bps > BasisPointsMaximum {
		return fmt.Errorf("frozen builder_fault_slash_bps must be <= %d, got %d", BasisPointsMaximum, bps)
	}
	return nil
}

const (
	DefaultMaxOrderSequencesPerActiveSession           = uint32(4096)
	DefaultSessionIdleTTLBlocks                        = uint64(100)
	DefaultSessionCloseTTLBlocks                       = uint64(60_480)
	DefaultSessionOrderRetentionBlocks                 = uint64(60_480)
	DefaultMaxSessionHistoryPruneItemsPerBlock         = uint32(256)
	DefaultSessionTerminalSummaryRetentionBlocks       = uint64(60_480)
	DefaultMaxSessionTerminalSummaryPruneItemsPerBlock = uint32(256)
	DefaultMaxSessionSweepPerBlock                     = uint32(1024)
	DefaultCancelOrderMinGas                           = uint64(50_000)
	DefaultAnchorFreshnessWindowBlocks                 = uint64(10_000)
	DefaultVerifyOpenDeadlineBlocks                    = uint64(400)
	DefaultCommitWindowBlocks                          = uint64(20)
	DefaultRevealWindowBlocks                          = uint64(10)
	DefaultCollectionWindowBlocks                      = uint64(40)
	DefaultSettleMarginBlocks                          = uint64(10)
	DefaultSelfRescueMarginBlocks                      = uint64(10)
	DefaultMaxDeadlineSweepPerBlock                    = uint32(1024)
	DefaultMaxCandidateHandraisesPerProposal           = uint32(210)
	DefaultMaxCandidateProposalBytes                   = uint64(512 << 10)
	DefaultMaxCandidateAcceptedProposalsPerStage       = uint32(16)
	DefaultMaxCandidateUnionMembersPerStage            = uint32(4096)
	DefaultMaxCandidateStageFinalizeTasksPerBlock      = uint32(16)
	DefaultMaxCandidateStageFinalizeMembersPerBlock    = uint32(4096)
	DefaultWorkerStakeWeightPPM                        = uint32(300_000)
	DefaultWorkerPerformanceWeightPPM                  = uint32(700_000)
	DefaultVerifierStakeWeightPPM                      = uint32(200_000)
	DefaultVerifierPerformanceWeightPPM                = uint32(800_000)
	DefaultVerifierCandidateRatioPPM                   = uint32(100_000)
	DefaultVerifierCandidateWindowMin                  = uint32(4)
	DefaultVerifierCandidateWindowMax                  = uint32(21)
	DefaultKStake                                      = uint32(3)
	DefaultCandidateWeightPPMMax                       = PartsPerMillion
	DefaultMaxWeightedDrawAttemptsPerSelection         = uint32(64)
	DefaultWorkerInferTimeoutSlashBPS                  = uint32(0)
	DefaultResultRevealMissingSlashBPS                 = uint32(0)
	DefaultBuilderFaultSlashBPS                        = uint32(0)
	DefaultMaxEvidenceRetentionBlocks                  = uint64(1000)
	DefaultMaxInferEvidenceCommitmentsPerReceipt       = uint32(32)
	DefaultMaxInferReceiptCommitmentBytes              = uint64(8 << 10)
	DefaultMaxSupportedEvidenceSchemaVersions          = uint32(16)
	DefaultMaxSupportedLeafOrderingVersions            = uint32(16)
	DefaultMaxOutputMMRLeaves                          = uint64(65_536)
	DefaultMinOutputStreamFrameBytes                   = uint32(16)
	DefaultMaxWorkerEvidenceBytesV1                    = uint64(1 << 20)
	DefaultMaxTaskCleanupItemsPerBlock                 = uint32(1024)
	DefaultTaskTerminalSummaryRetentionBlocks          = uint64(60_480)
	DefaultMaxTaskTerminalSummaryPruneItemsPerBlock    = uint32(256)
	DefaultMaxEpochTaskSummaryItemsPerBlock            = uint32(256)
	DefaultMaxEpochTaskSummarySupportCandidates        = uint32(64)
	DefaultMaxEpochTaskSummaryBytes                    = uint64(64 << 10)
	DefaultMaxTaskFailureClassPruneItemsPerBlock       = uint32(256)
	DefaultMaxBatchCommitItems                         = uint32(512)
	DefaultMaxBatchResultItems                         = uint32(512)
	DefaultMaxBatchCommitBytes                         = uint64(512 << 10)
	DefaultMaxBatchResultBytes                         = uint64(512 << 10)
	DefaultStopSequenceMaxItems                        = uint32(16)
	DefaultStopSequenceMaxBytesEach                    = uint32(128)
	DefaultStopSequenceMaxTotalBytes                   = uint32(1024)
	DefaultStopTokenMaxItems                           = uint32(64)
	DefaultTopKMax                                     = uint32(1000)
	DefaultGenerationMaxOutputTokens                   = uint64(131_072)
	DefaultMaxRoundEffectItemsPerBlock                 = uint32(1024)
)

const (
	MaxSessionRowsLimit                       = uint32(1_000_000)
	MaxCancelOrderMinGasLimit                 = uint64(10_000_000)
	MaxSessionTTLLimit                        = uint64(31_536_000)
	MaxRetentionBlocksLimit                   = uint64(31_536_000)
	MaxVisitedItemsPerBlockLimit              = uint32(100_000)
	MaxVerificationWindowBlocksLimit          = uint64(1_000_000)
	MaxCandidateProposalBytesLimit            = uint64(4 << 20)
	MaxCandidateProposalItemsLimit            = uint32(65_536)
	MaxCandidateUnionMembersLimit             = uint32(65_536)
	MaxWeightedDrawAttemptsLimit              = uint32(4096)
	MaxEvidenceRetentionBlocksLimit           = uint64(31_536_000)
	MaxInferEvidenceCommitmentsLimit          = uint32(1024)
	MaxInferReceiptCommitmentBytesLimit       = uint64(1 << 20)
	MaxSupportedVersionCatalogEntriesLimit    = uint32(256)
	MaxSupportedVersionTokenBytes             = 64
	MaxBatchItemsLimit                        = uint32(10_000)
	MaxBatchBytesLimit                        = uint64(4 << 20)
	MaxGenerationCollectionItemsLimit         = uint32(4096)
	MaxGenerationElementBytesLimit            = uint32(4096)
	MaxGenerationTotalBytesLimit              = uint32(1 << 20)
	MaxGenerationTopKLimit                    = uint32(1_000_000)
	MaxGenerationOutputTokensLimit            = uint64(8_388_607)
	MaxEpochTaskSummarySupportCandidatesLimit = uint32(4096)
	MaxEpochTaskSummaryBytesLimit             = uint64(1 << 20)
)

func DefaultTaskParams() TaskParamsV1 {
	return TaskParamsV1{
		SchemaVersion: TaskParamsSchemaVersion,
		Session: SessionParamsV1{
			MaxOrderSequencesPerActiveSession:           DefaultMaxOrderSequencesPerActiveSession,
			SessionIdleTtlBlocks:                        DefaultSessionIdleTTLBlocks,
			SessionCloseTtlBlocks:                       DefaultSessionCloseTTLBlocks,
			SessionOrderRetentionBlocks:                 DefaultSessionOrderRetentionBlocks,
			MaxSessionHistoryPruneItemsPerBlock:         DefaultMaxSessionHistoryPruneItemsPerBlock,
			SessionTerminalSummaryRetentionBlocks:       DefaultSessionTerminalSummaryRetentionBlocks,
			MaxSessionTerminalSummaryPruneItemsPerBlock: DefaultMaxSessionTerminalSummaryPruneItemsPerBlock,
			MaxSessionSweepPerBlock:                     DefaultMaxSessionSweepPerBlock,
			CancelOrderMinGas:                           DefaultCancelOrderMinGas,
			AnchorFreshnessWindowBlocks:                 DefaultAnchorFreshnessWindowBlocks,
		},
		Deadlines: TaskDeadlineParamsV1{
			VerifyOpenDeadlineBlocks: DefaultVerifyOpenDeadlineBlocks,
			CommitWindowBlocks:       DefaultCommitWindowBlocks,
			RevealWindowBlocks:       DefaultRevealWindowBlocks,
			CollectionWindowBlocks:   DefaultCollectionWindowBlocks,
			SettleMarginBlocks:       DefaultSettleMarginBlocks,
			SelfRescueMarginBlocks:   DefaultSelfRescueMarginBlocks,
			MaxDeadlineSweepPerBlock: DefaultMaxDeadlineSweepPerBlock,
		},
		Proposals: TaskProposalParamsV1{
			MaxCandidateHandraisesPerProposal:        DefaultMaxCandidateHandraisesPerProposal,
			MaxCandidateProposalBytes:                DefaultMaxCandidateProposalBytes,
			MaxCandidateAcceptedProposalsPerStage:    DefaultMaxCandidateAcceptedProposalsPerStage,
			MaxCandidateUnionMembersPerStage:         DefaultMaxCandidateUnionMembersPerStage,
			MaxCandidateStageFinalizeTasksPerBlock:   DefaultMaxCandidateStageFinalizeTasksPerBlock,
			MaxCandidateStageFinalizeMembersPerBlock: DefaultMaxCandidateStageFinalizeMembersPerBlock,
		},
		Weights: CandidateWeightParamsV1{
			WorkerStakeWeightPpm:                DefaultWorkerStakeWeightPPM,
			WorkerPerformanceWeightPpm:          DefaultWorkerPerformanceWeightPPM,
			VerifierStakeWeightPpm:              DefaultVerifierStakeWeightPPM,
			VerifierPerformanceWeightPpm:        DefaultVerifierPerformanceWeightPPM,
			VerifierCandidateRatioPpm:           DefaultVerifierCandidateRatioPPM,
			VerifierCandidateWindowMin:          DefaultVerifierCandidateWindowMin,
			VerifierCandidateWindowMax:          DefaultVerifierCandidateWindowMax,
			SelectedVerifierCount:               SelectedVerifierCountV1,
			KStake:                              DefaultKStake,
			CandidateWeightPpmMax:               DefaultCandidateWeightPPMMax,
			MaxWeightedDrawAttemptsPerSelection: DefaultMaxWeightedDrawAttemptsPerSelection,
		},
		Verification: VerificationParamsV1{
			WorkerInferTimeoutSlashBps:  DefaultWorkerInferTimeoutSlashBPS,
			ResultRevealMissingSlashBps: DefaultResultRevealMissingSlashBPS,
			BuilderFaultSlashBps:        DefaultBuilderFaultSlashBPS,
		},
		Evidence: EvidenceLimitParamsV1{
			MaxEvidenceRetentionBlocks:            DefaultMaxEvidenceRetentionBlocks,
			MaxInferEvidenceCommitmentsPerReceipt: DefaultMaxInferEvidenceCommitmentsPerReceipt,
			MaxInferReceiptCommitmentBytes:        DefaultMaxInferReceiptCommitmentBytes,
			MaxSupportedEvidenceSchemaVersions:    DefaultMaxSupportedEvidenceSchemaVersions,
			MaxSupportedLeafOrderingVersions:      DefaultMaxSupportedLeafOrderingVersions,
			MaxOutputMmrLeaves:                    DefaultMaxOutputMMRLeaves,
			MinOutputStreamFrameBytes:             DefaultMinOutputStreamFrameBytes,
			MaxWorkerEvidenceBytesV1:              DefaultMaxWorkerEvidenceBytesV1,
		},
		Cleanup: TaskCleanupParamsV1{
			MaxTaskCleanupItemsPerBlock:              DefaultMaxTaskCleanupItemsPerBlock,
			TaskTerminalSummaryRetentionBlocks:       DefaultTaskTerminalSummaryRetentionBlocks,
			MaxTaskTerminalSummaryPruneItemsPerBlock: DefaultMaxTaskTerminalSummaryPruneItemsPerBlock,
			MaxEpochTaskSummaryItemsPerBlock:         DefaultMaxEpochTaskSummaryItemsPerBlock,
			MaxEpochTaskSummarySupportCandidates:     DefaultMaxEpochTaskSummarySupportCandidates,
			MaxEpochTaskSummaryBytes:                 DefaultMaxEpochTaskSummaryBytes,
			MaxTaskFailureClassPruneItemsPerBlock:    DefaultMaxTaskFailureClassPruneItemsPerBlock,
		},
		Batch: BatchParamsV1{
			MaxBatchCommitItems: DefaultMaxBatchCommitItems,
			MaxBatchResultItems: DefaultMaxBatchResultItems,
			MaxBatchCommitBytes: DefaultMaxBatchCommitBytes,
			MaxBatchResultBytes: DefaultMaxBatchResultBytes,
		},
		Generation: GenerationLimitParamsV1{
			StopSequenceMaxItems:      DefaultStopSequenceMaxItems,
			StopSequenceMaxBytesEach:  DefaultStopSequenceMaxBytesEach,
			StopSequenceMaxTotalBytes: DefaultStopSequenceMaxTotalBytes,
			StopTokenMaxItems:         DefaultStopTokenMaxItems,
			TopKMax:                   DefaultTopKMax,
			MaxOutputTokens:           DefaultGenerationMaxOutputTokens,
		},
		Fee: TaskFeeParamsV1{
			FeeRuleVersion: TaskFeeRuleVersionV2, FeePolicyVersion: TaskFeePolicyVersionV1,
			MaxReimbursableFeePerGasDenominator: 1,
			MaxReimbursementPerTx:               shared.NewAmount(0), MaxReimbursementPerTask: shared.NewAmount(0),
		},
		Challenge: ChallengeParamsV1{
			MaxVerifyRound: 2, MaxOpenRoundPerTask: 1, ChallengeVerifierCount: SelectedVerifierCountV1,
			ChallengeOpenBond: shared.NewAmount(0), CandidateWindowDelayBlocks: 1, HandraiseWindowBlocks: 1,
			AssignmentDelayBlocks: 1, CommitWindowBlocks: DefaultCommitWindowBlocks,
			RevealWindowBlocks: DefaultRevealWindowBlocks, MaxRoundEffectItemsPerBlock: DefaultMaxRoundEffectItemsPerBlock,
		},
	}
}

func (p TaskParamsV1) Validate() error {
	if p.SchemaVersion != TaskParamsSchemaVersion {
		return fmt.Errorf("schema_version must be %d", TaskParamsSchemaVersion)
	}
	if err := validateSessionParams(p.Session); err != nil {
		return err
	}
	if err := validateDeadlineParams(p.Deadlines); err != nil {
		return err
	}
	if err := validateProposalParams(p.Proposals); err != nil {
		return err
	}
	if err := validateWeightParams(p.Weights); err != nil {
		return err
	}
	if err := validateVerificationParams(p.Verification); err != nil {
		return err
	}
	if err := validateEvidenceParams(p.Evidence); err != nil {
		return err
	}
	if err := validateCleanupParams(p.Cleanup); err != nil {
		return err
	}
	if err := validateBatchParams(p.Batch); err != nil {
		return err
	}
	if err := validateGenerationParams(p.Generation); err != nil {
		return err
	}
	if err := validateFeeParams(p.Fee); err != nil {
		return err
	}
	return validateChallengeParams(p.Challenge, p.Weights.SelectedVerifierCount)
}

func validateFeeParams(p TaskFeeParamsV1) error {
	if p.FeeRuleVersion != TaskFeeRuleVersionV2 {
		return fmt.Errorf("fee.fee_rule_version must be %d", TaskFeeRuleVersionV2)
	}
	if p.FeePolicyVersion != TaskFeePolicyVersionV1 {
		return fmt.Errorf("fee.fee_policy_version must be %d", TaskFeePolicyVersionV1)
	}
	if p.MaintenanceRateBps > BasisPointsMaximum || p.MaxReimbursableFeePerGasDenominator == 0 {
		return fmt.Errorf("fee maintenance rate or reimbursement rate is invalid")
	}
	if _, err := shared.ParseAmount(p.MaxReimbursementPerTx); err != nil {
		return fmt.Errorf("fee.max_reimbursement_per_tx: %w", err)
	}
	if _, err := shared.ParseAmount(p.MaxReimbursementPerTask); err != nil {
		return fmt.Errorf("fee.max_reimbursement_per_task: %w", err)
	}
	return nil
}

func validateChallengeParams(p ChallengeParamsV1, selectedVerifierCount uint32) error {
	if p.MaxVerifyRound != ChallengeVerifyRoundV1 || p.MaxOpenRoundPerTask != 1 ||
		p.ChallengeVerifierCount < selectedVerifierCount || p.ChallengeVerifierCount == 0 {
		return fmt.Errorf("challenge round limits are invalid")
	}
	if _, err := shared.ParseAmount(p.ChallengeOpenBond); err != nil {
		return fmt.Errorf("challenge.challenge_open_bond: %w", err)
	}
	if p.ChallengeInconclusiveBondSlashBps > BasisPointsMaximum || p.ChallengeFaultSlashBps > BasisPointsMaximum {
		return fmt.Errorf("challenge slash bps exceeds %d", BasisPointsMaximum)
	}
	for _, item := range []struct {
		name  string
		value uint64
	}{
		{"candidate_window_delay_blocks", p.CandidateWindowDelayBlocks},
		{"handraise_window_blocks", p.HandraiseWindowBlocks},
		{"assignment_delay_blocks", p.AssignmentDelayBlocks},
		{"commit_window_blocks", p.CommitWindowBlocks},
		{"reveal_window_blocks", p.RevealWindowBlocks},
	} {
		if item.value == 0 || item.value > MaxVerificationWindowBlocksLimit {
			return fmt.Errorf("challenge.%s must be in 1..%d", item.name, MaxVerificationWindowBlocksLimit)
		}
	}
	if _, overflow := checkedAddManyUint64(
		p.CandidateWindowDelayBlocks, p.HandraiseWindowBlocks, p.AssignmentDelayBlocks,
		p.CommitWindowBlocks, p.RevealWindowBlocks,
	); overflow {
		return fmt.Errorf("challenge round window overflows")
	}
	return positiveUint32("challenge.max_round_effect_items_per_block", p.MaxRoundEffectItemsPerBlock, MaxVisitedItemsPerBlockLimit)
}

func validateSessionParams(p SessionParamsV1) error {
	if err := positiveUint32("session.max_order_sequences_per_active_session", p.MaxOrderSequencesPerActiveSession, MaxSessionRowsLimit); err != nil {
		return err
	}
	if err := positiveUint64("session.session_idle_ttl_blocks", p.SessionIdleTtlBlocks, MaxSessionTTLLimit); err != nil {
		return err
	}
	if err := positiveUint64("session.session_close_ttl_blocks", p.SessionCloseTtlBlocks, MaxSessionTTLLimit); err != nil {
		return err
	}
	if p.SessionCloseTtlBlocks <= p.SessionIdleTtlBlocks {
		return fmt.Errorf("session.session_close_ttl_blocks must be greater than session.session_idle_ttl_blocks")
	}
	if err := positiveUint64("session.session_order_retention_blocks", p.SessionOrderRetentionBlocks, MaxRetentionBlocksLimit); err != nil {
		return err
	}
	if err := positiveUint32("session.max_session_history_prune_items_per_block", p.MaxSessionHistoryPruneItemsPerBlock, MaxVisitedItemsPerBlockLimit); err != nil {
		return err
	}
	if err := positiveUint64("session.session_terminal_summary_retention_blocks", p.SessionTerminalSummaryRetentionBlocks, MaxRetentionBlocksLimit); err != nil {
		return err
	}
	if err := positiveUint32("session.max_session_terminal_summary_prune_items_per_block", p.MaxSessionTerminalSummaryPruneItemsPerBlock, MaxVisitedItemsPerBlockLimit); err != nil {
		return err
	}
	if err := positiveUint32("session.max_session_sweep_per_block", p.MaxSessionSweepPerBlock, MaxVisitedItemsPerBlockLimit); err != nil {
		return err
	}
	if err := positiveUint64("session.cancel_order_min_gas", p.CancelOrderMinGas, MaxCancelOrderMinGasLimit); err != nil {
		return err
	}
	return positiveUint64("session.anchor_freshness_window_blocks", p.AnchorFreshnessWindowBlocks, MaxRetentionBlocksLimit)
}

func validateDeadlineParams(p TaskDeadlineParamsV1) error {
	for _, item := range []struct {
		name  string
		value uint64
	}{
		{"deadlines.verify_open_deadline_blocks", p.VerifyOpenDeadlineBlocks},
		{"deadlines.commit_window_blocks", p.CommitWindowBlocks},
		{"deadlines.reveal_window_blocks", p.RevealWindowBlocks},
		{"deadlines.collection_window_blocks", p.CollectionWindowBlocks},
		{"deadlines.settle_margin_blocks", p.SettleMarginBlocks},
		{"deadlines.self_rescue_margin_blocks", p.SelfRescueMarginBlocks},
	} {
		if err := positiveUint64(item.name, item.value, MaxVerificationWindowBlocksLimit); err != nil {
			return err
		}
	}
	collectionMinimum, overflow := checkedAddManyUint64(
		p.CommitWindowBlocks,
		p.RevealWindowBlocks,
		p.SettleMarginBlocks,
	)
	if overflow {
		return fmt.Errorf("deadlines collection window lower bound overflows")
	}
	if p.CollectionWindowBlocks < collectionMinimum {
		return fmt.Errorf(
			"deadlines.collection_window_blocks must be at least commit_window_blocks + reveal_window_blocks + settle_margin_blocks (%d)",
			collectionMinimum,
		)
	}
	return positiveUint32("deadlines.max_deadline_sweep_per_block", p.MaxDeadlineSweepPerBlock, MaxVisitedItemsPerBlockLimit)
}

// ValidateVerifyOpenClock checks the Task/Hub clock relation without importing
// Hub types into this package.
func ValidateVerifyOpenClock(p TaskDeadlineParamsV1, deltaWBlocks, builderProposalWindowBlocks uint64) error {
	required, overflow := checkedAddManyUint64(deltaWBlocks, deltaWBlocks, builderProposalWindowBlocks, p.SelfRescueMarginBlocks)
	if overflow {
		return fmt.Errorf("verify-open clock lower bound overflows")
	}
	if p.VerifyOpenDeadlineBlocks <= required {
		return fmt.Errorf("deadlines.verify_open_deadline_blocks must be greater than %d", required)
	}
	return nil
}

func ValidateSessionCloseHorizon(p TaskParamsV1, challengeOpenWindowBlocks uint64) error {
	required, overflow := checkedAddManyUint64(
		p.Deadlines.VerifyOpenDeadlineBlocks,
		p.Deadlines.CommitWindowBlocks,
		p.Deadlines.RevealWindowBlocks,
		p.Deadlines.CollectionWindowBlocks,
		p.Deadlines.SettleMarginBlocks,
		challengeOpenWindowBlocks,
		p.Evidence.MaxEvidenceRetentionBlocks,
	)
	if overflow {
		return fmt.Errorf("session close horizon lower bound overflows")
	}
	if p.Session.SessionCloseTtlBlocks <= required {
		return fmt.Errorf("session.session_close_ttl_blocks must be greater than responsibility horizon %d", required)
	}
	return nil
}

func validateProposalParams(p TaskProposalParamsV1) error {
	if err := positiveUint32("proposals.max_candidate_handraises_per_proposal", p.MaxCandidateHandraisesPerProposal, MaxCandidateProposalItemsLimit); err != nil {
		return err
	}
	if err := positiveUint64("proposals.max_candidate_proposal_bytes", p.MaxCandidateProposalBytes, MaxCandidateProposalBytesLimit); err != nil {
		return err
	}
	if err := positiveUint32("proposals.max_candidate_accepted_proposals_per_stage", p.MaxCandidateAcceptedProposalsPerStage, MaxCandidateProposalItemsLimit); err != nil {
		return err
	}
	if err := positiveUint32("proposals.max_candidate_union_members_per_stage", p.MaxCandidateUnionMembersPerStage, MaxCandidateUnionMembersLimit); err != nil {
		return err
	}
	if p.MaxCandidateHandraisesPerProposal > p.MaxCandidateUnionMembersPerStage {
		return fmt.Errorf("proposals.max_candidate_handraises_per_proposal must not exceed proposals.max_candidate_union_members_per_stage")
	}
	if err := positiveUint32("proposals.max_candidate_stage_finalize_tasks_per_block", p.MaxCandidateStageFinalizeTasksPerBlock, MaxVisitedItemsPerBlockLimit); err != nil {
		return err
	}
	return positiveUint32("proposals.max_candidate_stage_finalize_members_per_block", p.MaxCandidateStageFinalizeMembersPerBlock, MaxVisitedItemsPerBlockLimit)
}

func validateWeightParams(p CandidateWeightParamsV1) error {
	if uint64(p.WorkerStakeWeightPpm)+uint64(p.WorkerPerformanceWeightPpm) != uint64(PartsPerMillion) {
		return fmt.Errorf("weights worker weights must sum to %d", PartsPerMillion)
	}
	if uint64(p.VerifierStakeWeightPpm)+uint64(p.VerifierPerformanceWeightPpm) != uint64(PartsPerMillion) {
		return fmt.Errorf("weights verifier weights must sum to %d", PartsPerMillion)
	}
	if p.VerifierCandidateRatioPpm == 0 || p.VerifierCandidateRatioPpm > PartsPerMillion {
		return fmt.Errorf("weights.verifier_candidate_ratio_ppm must be in 1..%d", PartsPerMillion)
	}
	if p.VerifierCandidateWindowMin == 0 || p.VerifierCandidateWindowMax < p.VerifierCandidateWindowMin {
		return fmt.Errorf("weights verifier candidate window is invalid")
	}
	if p.SelectedVerifierCount != SelectedVerifierCountV1 {
		return fmt.Errorf("weights.selected_verifier_count must be %d", SelectedVerifierCountV1)
	}
	if p.VerifierCandidateWindowMax < p.SelectedVerifierCount {
		return fmt.Errorf("weights.verifier_candidate_window_max must cover selected_verifier_count")
	}
	if p.KStake != DefaultKStake {
		return fmt.Errorf("weights.k_stake must be %d", DefaultKStake)
	}
	if p.CandidateWeightPpmMax != PartsPerMillion {
		return fmt.Errorf("weights.candidate_weight_ppm_max must be %d", PartsPerMillion)
	}
	return positiveUint32("weights.max_weighted_draw_attempts_per_selection", p.MaxWeightedDrawAttemptsPerSelection, MaxWeightedDrawAttemptsLimit)
}

func validateVerificationParams(p VerificationParamsV1) error {
	for _, item := range []struct {
		name  string
		value uint32
	}{
		{"verification.worker_infer_timeout_slash_bps", p.WorkerInferTimeoutSlashBps},
		{"verification.result_reveal_missing_slash_bps", p.ResultRevealMissingSlashBps},
		{"verification.builder_fault_slash_bps", p.BuilderFaultSlashBps},
	} {
		if item.value > BasisPointsMaximum {
			return fmt.Errorf("%s must be <= %d", item.name, BasisPointsMaximum)
		}
	}
	return nil
}

func validateEvidenceParams(p EvidenceLimitParamsV1) error {
	if err := positiveUint64("evidence.max_evidence_retention_blocks", p.MaxEvidenceRetentionBlocks, MaxEvidenceRetentionBlocksLimit); err != nil {
		return err
	}
	if err := positiveUint32("evidence.max_infer_evidence_commitments_per_receipt", p.MaxInferEvidenceCommitmentsPerReceipt, MaxInferEvidenceCommitmentsLimit); err != nil {
		return err
	}
	if err := positiveUint64("evidence.max_infer_receipt_commitment_bytes", p.MaxInferReceiptCommitmentBytes, MaxInferReceiptCommitmentBytesLimit); err != nil {
		return err
	}
	if err := positiveUint32("evidence.max_supported_evidence_schema_versions", p.MaxSupportedEvidenceSchemaVersions, MaxSupportedVersionCatalogEntriesLimit); err != nil {
		return err
	}
	if err := positiveUint32("evidence.max_supported_leaf_ordering_versions", p.MaxSupportedLeafOrderingVersions, MaxSupportedVersionCatalogEntriesLimit); err != nil {
		return err
	}
	if uint32(len(p.EvidenceSchemaVersionSupported)) > p.MaxSupportedEvidenceSchemaVersions {
		return fmt.Errorf("evidence.evidence_schema_version_supported exceeds its count cap")
	}
	if uint32(len(p.LeafOrderingVersionSupported)) > p.MaxSupportedLeafOrderingVersions {
		return fmt.Errorf("evidence.leaf_ordering_version_supported exceeds its count cap")
	}
	if err := validateCanonicalTokens("evidence.evidence_schema_version_supported", p.EvidenceSchemaVersionSupported); err != nil {
		return err
	}
	if err := validateCanonicalTokens("evidence.leaf_ordering_version_supported", p.LeafOrderingVersionSupported); err != nil {
		return err
	}
	if p.MaxOutputMmrLeaves == 0 {
		return fmt.Errorf("evidence.max_output_mmr_leaves must be positive")
	}
	if p.MinOutputStreamFrameBytes == 0 {
		return fmt.Errorf("evidence.min_output_stream_frame_bytes must be positive")
	}
	if p.MaxWorkerEvidenceBytesV1 == 0 {
		return fmt.Errorf("evidence.max_worker_evidence_bytes_v1 must be positive")
	}
	return nil
}

func validateCleanupParams(p TaskCleanupParamsV1) error {
	if err := positiveUint32("cleanup.max_task_cleanup_items_per_block", p.MaxTaskCleanupItemsPerBlock, MaxVisitedItemsPerBlockLimit); err != nil {
		return err
	}
	if err := positiveUint64("cleanup.task_terminal_summary_retention_blocks", p.TaskTerminalSummaryRetentionBlocks, MaxRetentionBlocksLimit); err != nil {
		return err
	}
	if err := positiveUint32("cleanup.max_task_terminal_summary_prune_items_per_block", p.MaxTaskTerminalSummaryPruneItemsPerBlock, MaxVisitedItemsPerBlockLimit); err != nil {
		return err
	}
	if err := positiveUint32("cleanup.max_epoch_task_summary_items_per_block", p.MaxEpochTaskSummaryItemsPerBlock, MaxVisitedItemsPerBlockLimit); err != nil {
		return err
	}
	if err := positiveUint32("cleanup.max_epoch_task_summary_support_candidates", p.MaxEpochTaskSummarySupportCandidates, MaxEpochTaskSummarySupportCandidatesLimit); err != nil {
		return err
	}
	if err := positiveUint64("cleanup.max_epoch_task_summary_bytes", p.MaxEpochTaskSummaryBytes, MaxEpochTaskSummaryBytesLimit); err != nil {
		return err
	}
	return positiveUint32("cleanup.max_task_failure_class_prune_items_per_block", p.MaxTaskFailureClassPruneItemsPerBlock, MaxVisitedItemsPerBlockLimit)
}

func validateBatchParams(p BatchParamsV1) error {
	if err := positiveUint32("batch.max_batch_commit_items", p.MaxBatchCommitItems, MaxBatchItemsLimit); err != nil {
		return err
	}
	if err := positiveUint32("batch.max_batch_result_items", p.MaxBatchResultItems, MaxBatchItemsLimit); err != nil {
		return err
	}
	if err := positiveUint64("batch.max_batch_commit_bytes", p.MaxBatchCommitBytes, MaxBatchBytesLimit); err != nil {
		return err
	}
	return positiveUint64("batch.max_batch_result_bytes", p.MaxBatchResultBytes, MaxBatchBytesLimit)
}

func validateGenerationParams(p GenerationLimitParamsV1) error {
	if err := positiveUint32("generation.stop_sequence_max_items", p.StopSequenceMaxItems, MaxGenerationCollectionItemsLimit); err != nil {
		return err
	}
	if err := positiveUint32("generation.stop_sequence_max_bytes_each", p.StopSequenceMaxBytesEach, MaxGenerationElementBytesLimit); err != nil {
		return err
	}
	if err := positiveUint32("generation.stop_sequence_max_total_bytes", p.StopSequenceMaxTotalBytes, MaxGenerationTotalBytesLimit); err != nil {
		return err
	}
	product := uint64(p.StopSequenceMaxItems) * uint64(p.StopSequenceMaxBytesEach)
	if uint64(p.StopSequenceMaxTotalBytes) > product {
		return fmt.Errorf("generation.stop_sequence_max_total_bytes must not exceed items times bytes_each")
	}
	if err := positiveUint32("generation.stop_token_max_items", p.StopTokenMaxItems, MaxGenerationCollectionItemsLimit); err != nil {
		return err
	}
	if err := positiveUint32("generation.top_k_max", p.TopKMax, MaxGenerationTopKLimit); err != nil {
		return err
	}
	return positiveUint64("generation.max_output_tokens", p.MaxOutputTokens, MaxGenerationOutputTokensLimit)
}

func validateCanonicalTokens(name string, values []string) error {
	for i, value := range values {
		if value == "" || value != strings.TrimSpace(value) || len(value) > MaxSupportedVersionTokenBytes {
			return fmt.Errorf("%s entries must be non-empty canonical tokens of at most %d bytes", name, MaxSupportedVersionTokenBytes)
		}
		if i > 0 && values[i-1] >= value {
			return fmt.Errorf("%s must be sorted and unique", name)
		}
	}
	return nil
}

func positiveUint32(name string, value, maximum uint32) error {
	if value == 0 || value > maximum {
		return fmt.Errorf("%s must be in 1..%d", name, maximum)
	}
	return nil
}

func positiveUint64(name string, value, maximum uint64) error {
	if value == 0 || value > maximum {
		return fmt.Errorf("%s must be in 1..%d", name, maximum)
	}
	return nil
}

func checkedAddManyUint64(values ...uint64) (uint64, bool) {
	total := uint64(0)
	for _, value := range values {
		next, overflow := shared.CheckedAddUint64(total, value)
		if overflow {
			return 0, true
		}
		total = next
	}
	return total, false
}

// GenesisOnlyTaskParamsChanged identifies V1 geometry frozen at genesis.
func GenesisOnlyTaskParamsChanged(current, next TaskParamsV1) (string, bool) {
	switch {
	case current.Weights.SelectedVerifierCount != next.Weights.SelectedVerifierCount:
		return "weights.selected_verifier_count", true
	case current.Weights.KStake != next.Weights.KStake:
		return "weights.k_stake", true
	case current.Weights.CandidateWeightPpmMax != next.Weights.CandidateWeightPpmMax:
		return "weights.candidate_weight_ppm_max", true
	case current.Fee.FeeRuleVersion != next.Fee.FeeRuleVersion:
		return "fee.fee_rule_version", true
	case current.Challenge.MaxVerifyRound != next.Challenge.MaxVerifyRound:
		return "challenge.max_verify_round", true
	case current.Challenge.MaxOpenRoundPerTask != next.Challenge.MaxOpenRoundPerTask:
		return "challenge.max_open_round_per_task", true
	case current.Evidence.MaxOutputMmrLeaves != next.Evidence.MaxOutputMmrLeaves:
		return "evidence.max_output_mmr_leaves", true
	case current.Evidence.MinOutputStreamFrameBytes != next.Evidence.MinOutputStreamFrameBytes:
		return "evidence.min_output_stream_frame_bytes", true
	case current.Evidence.MaxWorkerEvidenceBytesV1 != next.Evidence.MaxWorkerEvidenceBytesV1:
		return "evidence.max_worker_evidence_bytes_v1", true
	default:
		return "", false
	}
}

// RuntimeImmutableTaskParamsChanged rejects live changes whose values are not
// frozen into every affected Task or Session row yet.
func RuntimeImmutableTaskParamsChanged(current, next TaskParamsV1) (string, bool) {
	currentSession := current.Session
	nextSession := next.Session
	currentSession.AnchorFreshnessWindowBlocks = 0
	nextSession.AnchorFreshnessWindowBlocks = 0
	currentEvidence := current.Evidence
	nextEvidence := next.Evidence
	currentEvidence.MaxOutputMmrLeaves = 0
	currentEvidence.MinOutputStreamFrameBytes = 0
	currentEvidence.MaxWorkerEvidenceBytesV1 = 0
	nextEvidence.MaxOutputMmrLeaves = 0
	nextEvidence.MinOutputStreamFrameBytes = 0
	nextEvidence.MaxWorkerEvidenceBytesV1 = 0
	switch {
	case !currentSession.Equal(nextSession):
		return "session", true
	case !current.Deadlines.Equal(next.Deadlines):
		return "deadlines", true
	case !current.Proposals.Equal(next.Proposals):
		return "proposals", true
	case !current.Weights.Equal(next.Weights):
		return "weights", true
	case !current.Verification.Equal(next.Verification):
		return "verification", true
	case !currentEvidence.Equal(nextEvidence):
		return "evidence", true
	case !current.Cleanup.Equal(next.Cleanup):
		return "cleanup", true
	default:
		return "", false
	}
}

// CanonicalTaskParamsTypedFields is the H_FIELDS_V1 field vector of TaskParamsV1:
// schema_version as a scalar of the outer message, then one nested FRAME_V1 per
// submessage in ascending proto field-number order (fields 2 to 11).
//
// It is the only view of this encoding. The domain used to carry a second,
// hand-written []byte projection alongside it; keeping two orderings of one
// schema in sync by hand is what let a repeated field land in the
// wrong shape here while the flat view looked untouched, so the projection is
// gone rather than re-derived.
func CanonicalTaskParamsTypedFields(p TaskParamsV1) []shared.CanonicalFieldV1 {
	sections := canonicalTaskParamsSectionFields(p)
	fields := make([]shared.CanonicalFieldV1, 0, len(sections)+1)
	fields = append(fields, shared.RawCanonicalFieldV1(shared.Uint32BE(p.SchemaVersion)))
	for _, section := range sections {
		fields = append(fields, shared.NestedCanonicalFieldV1(
			shared.NewCanonicalFrameBuilderV1().Field(section...).Build(),
		))
	}
	return fields
}

// canonicalTaskParamsStringList encodes one `repeated string` field as the
// the canonical encoding contract REPEATED_V1 container:
// FRAME_V1(uint32_be(count), ENC(e_1), ..., ENC(e_n)), where ENC of a string
// element is its own UTF-8 bytes.
//
// The container is a field of the enclosing submessage, not a run of siblings
// inside it. Writing the count and then the elements straight into the parent
// frame -- which is what EvidenceLimitParamsV1 did for both of its repeated
// fields -- puts no boundary in the preimage between "this list ended" and "the
// next field began", so two different evidence catalogs, or a catalog and the
// scalar that follows it, can produce the same digest. The count is synthesised
// from the slice by the shared encoder, so it cannot disagree with the elements,
// and a nil list and an empty list are the same value.
func canonicalTaskParamsStringList(values []string) shared.CanonicalFieldV1 {
	elements := make([]shared.CanonicalFieldV1, len(values))
	for index, value := range values {
		elements[index] = shared.RawCanonicalFieldV1([]byte(value))
	}
	return shared.NestedCanonicalFieldV1(shared.CanonicalRepeatedFieldsV1(elements))
}

// canonicalTaskParamsSectionFields is the single source of canonical field order
// for TaskParamsV1's ten submessages, each listed in ascending proto
// field-number order.
func canonicalTaskParamsSectionFields(p TaskParamsV1) [][]shared.CanonicalFieldV1 {
	return [][]shared.CanonicalFieldV1{
		// field 2: SessionParamsV1
		shared.RawCanonicalFieldsV1(
			shared.Uint32BE(p.Session.MaxOrderSequencesPerActiveSession),
			shared.Uint64BE(p.Session.SessionIdleTtlBlocks),
			shared.Uint64BE(p.Session.SessionCloseTtlBlocks),
			shared.Uint64BE(p.Session.SessionOrderRetentionBlocks),
			shared.Uint32BE(p.Session.MaxSessionHistoryPruneItemsPerBlock),
			shared.Uint64BE(p.Session.SessionTerminalSummaryRetentionBlocks),
			shared.Uint32BE(p.Session.MaxSessionTerminalSummaryPruneItemsPerBlock),
			shared.Uint32BE(p.Session.MaxSessionSweepPerBlock),
			shared.Uint64BE(p.Session.CancelOrderMinGas),
			shared.Uint64BE(p.Session.AnchorFreshnessWindowBlocks),
		),
		// field 3: TaskDeadlineParamsV1
		shared.RawCanonicalFieldsV1(
			shared.Uint64BE(p.Deadlines.VerifyOpenDeadlineBlocks),
			shared.Uint64BE(p.Deadlines.CommitWindowBlocks),
			shared.Uint64BE(p.Deadlines.RevealWindowBlocks),
			shared.Uint64BE(p.Deadlines.CollectionWindowBlocks),
			shared.Uint64BE(p.Deadlines.SettleMarginBlocks),
			shared.Uint64BE(p.Deadlines.SelfRescueMarginBlocks),
			shared.Uint32BE(p.Deadlines.MaxDeadlineSweepPerBlock),
		),
		// field 4: TaskProposalParamsV1
		shared.RawCanonicalFieldsV1(
			shared.Uint32BE(p.Proposals.MaxCandidateHandraisesPerProposal),
			shared.Uint64BE(p.Proposals.MaxCandidateProposalBytes),
			shared.Uint32BE(p.Proposals.MaxCandidateAcceptedProposalsPerStage),
			shared.Uint32BE(p.Proposals.MaxCandidateUnionMembersPerStage),
			shared.Uint32BE(p.Proposals.MaxCandidateStageFinalizeTasksPerBlock),
			shared.Uint32BE(p.Proposals.MaxCandidateStageFinalizeMembersPerBlock),
		),
		// field 5: CandidateWeightParamsV1
		shared.RawCanonicalFieldsV1(
			shared.Uint32BE(p.Weights.WorkerStakeWeightPpm),
			shared.Uint32BE(p.Weights.WorkerPerformanceWeightPpm),
			shared.Uint32BE(p.Weights.VerifierStakeWeightPpm),
			shared.Uint32BE(p.Weights.VerifierPerformanceWeightPpm),
			shared.Uint32BE(p.Weights.VerifierCandidateRatioPpm),
			shared.Uint32BE(p.Weights.VerifierCandidateWindowMin),
			shared.Uint32BE(p.Weights.VerifierCandidateWindowMax),
			shared.Uint32BE(p.Weights.SelectedVerifierCount),
			shared.Uint32BE(p.Weights.KStake),
			shared.Uint32BE(p.Weights.CandidateWeightPpmMax),
			shared.Uint32BE(p.Weights.MaxWeightedDrawAttemptsPerSelection),
		),
		// field 6: VerificationParamsV1
		shared.RawCanonicalFieldsV1(
			shared.Uint32BE(p.Verification.WorkerInferTimeoutSlashBps),
			shared.Uint32BE(p.Verification.ResultRevealMissingSlashBps),
			shared.Uint32BE(p.Verification.BuilderFaultSlashBps),
		),
		// field 7: EvidenceLimitParamsV1, whose fields 7 and 8 are repeated.
		append(
			shared.RawCanonicalFieldsV1(
				shared.Uint64BE(p.Evidence.MaxEvidenceRetentionBlocks),
				shared.Uint32BE(p.Evidence.MaxInferEvidenceCommitmentsPerReceipt),
				shared.Uint64BE(p.Evidence.MaxInferReceiptCommitmentBytes),
				shared.Uint32BE(p.Evidence.MaxSupportedEvidenceSchemaVersions),
				shared.Uint32BE(p.Evidence.MaxSupportedLeafOrderingVersions),
			),
			canonicalTaskParamsStringList(p.Evidence.EvidenceSchemaVersionSupported),
			canonicalTaskParamsStringList(p.Evidence.LeafOrderingVersionSupported),
			shared.RawCanonicalFieldV1(shared.Uint64BE(p.Evidence.MaxOutputMmrLeaves)),
			shared.RawCanonicalFieldV1(shared.Uint32BE(p.Evidence.MinOutputStreamFrameBytes)),
			shared.RawCanonicalFieldV1(shared.Uint64BE(p.Evidence.MaxWorkerEvidenceBytesV1)),
		),
		// field 8: TaskCleanupParamsV1
		shared.RawCanonicalFieldsV1(
			shared.Uint32BE(p.Cleanup.MaxTaskCleanupItemsPerBlock),
			shared.Uint64BE(p.Cleanup.TaskTerminalSummaryRetentionBlocks),
			shared.Uint32BE(p.Cleanup.MaxTaskTerminalSummaryPruneItemsPerBlock),
			shared.Uint32BE(p.Cleanup.MaxEpochTaskSummaryItemsPerBlock),
			shared.Uint32BE(p.Cleanup.MaxEpochTaskSummarySupportCandidates),
			shared.Uint64BE(p.Cleanup.MaxEpochTaskSummaryBytes),
			shared.Uint32BE(p.Cleanup.MaxTaskFailureClassPruneItemsPerBlock),
		),
		// field 9: BatchParamsV1
		shared.RawCanonicalFieldsV1(
			shared.Uint32BE(p.Batch.MaxBatchCommitItems),
			shared.Uint32BE(p.Batch.MaxBatchResultItems),
			shared.Uint64BE(p.Batch.MaxBatchCommitBytes),
			shared.Uint64BE(p.Batch.MaxBatchResultBytes),
		),
		// field 10: GenerationLimitParamsV1
		shared.RawCanonicalFieldsV1(
			shared.Uint32BE(p.Generation.StopSequenceMaxItems),
			shared.Uint32BE(p.Generation.StopSequenceMaxBytesEach),
			shared.Uint32BE(p.Generation.StopSequenceMaxTotalBytes),
			shared.Uint32BE(p.Generation.StopTokenMaxItems),
			shared.Uint32BE(p.Generation.TopKMax),
			shared.Uint64BE(p.Generation.MaxOutputTokens),
		),
		// field 11: TaskFeeParamsV1
		canonicalTaskFeeParamsFields(p.Fee),
		// field 12: ChallengeParamsV1
		canonicalChallengeParamsFields(p.Challenge),
	}
}

func canonicalTaskFeeParamsFields(p TaskFeeParamsV1) []shared.CanonicalFieldV1 {
	fields := shared.RawCanonicalFieldsV1(
		shared.Uint64BE(p.FeeRuleVersion), shared.Uint32BE(p.MaintenanceRateBps),
		shared.Uint64BE(p.FeePolicyVersion), shared.Uint64BE(p.MaxReimbursableFeePerGasNumerator),
		shared.Uint64BE(p.MaxReimbursableFeePerGasDenominator),
	)
	return append(fields, canonicalTaskParamsAmountFields(p.MaxReimbursementPerTx, p.MaxReimbursementPerTask)...)
}

func canonicalChallengeParamsFields(p ChallengeParamsV1) []shared.CanonicalFieldV1 {
	fields := shared.RawCanonicalFieldsV1(
		shared.Uint32BE(p.MaxVerifyRound), shared.Uint32BE(p.MaxOpenRoundPerTask), shared.Uint32BE(p.ChallengeVerifierCount),
	)
	fields = append(fields, canonicalTaskParamsAmountFields(p.ChallengeOpenBond)...)
	return append(fields, shared.RawCanonicalFieldsV1(
		shared.Uint32BE(p.ChallengeInconclusiveBondSlashBps), shared.Uint32BE(p.ChallengeFaultSlashBps),
		shared.Uint64BE(p.CandidateWindowDelayBlocks), shared.Uint64BE(p.HandraiseWindowBlocks),
		shared.Uint64BE(p.AssignmentDelayBlocks), shared.Uint64BE(p.CommitWindowBlocks),
		shared.Uint64BE(p.RevealWindowBlocks), shared.Uint32BE(p.MaxRoundEffectItemsPerBlock),
	)...)
}

func canonicalTaskParamsAmountFields(amounts ...shared.Amount) []shared.CanonicalFieldV1 {
	fields := make([]shared.CanonicalFieldV1, len(amounts))
	for i, amount := range amounts {
		frame, _ := shared.CanonicalAmountFrameV1(amount)
		fields[i] = shared.NestedCanonicalFieldV1(frame)
	}
	return fields
}

// TaskParamsHashV1 is the single producer of TaskParamsMetaState.params_hash and
// of the value echoed by MsgUpdateTaskParamsResponse and event 111.
func TaskParamsHashV1(chainID string, version uint64, params TaskParamsV1) ([]byte, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	body := shared.NewCanonicalFrameBuilderV1().Field(CanonicalTaskParamsTypedFields(params)...).Build()
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainTaskParamsV1)).
		Raw([]byte(chainID), shared.Uint64BE(version)).
		Nested(body).
		Sum()
}

func (s TaskParamsMetaState) Validate() error {
	if s.ParamsVersion == 0 {
		return fmt.Errorf("params meta params_version must be greater than 0")
	}
	if len(s.ParamsHash) != 32 {
		return fmt.Errorf("params meta params_hash must be 32 bytes")
	}
	return nil
}
