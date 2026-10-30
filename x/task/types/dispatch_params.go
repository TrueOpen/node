package types

import (
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
)

const (
	dispatchFaultPenaltyKindCount = int(shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_DOUBLE_DISPATCH)
	dispatchChallengeKindCount    = int(shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_ROSTER_UNAVAILABLE)
)

// DefaultDispatchParams supplies a bounded test-network configuration. Public
// seeds explicitly list these values so deployment review sees the entire set.
func DefaultDispatchParams() DispatchParamsV1 {
	penalties := make([]*DispatchFaultPenaltyV1, dispatchFaultPenaltyKindCount)
	gasCaps := make([]*DispatchChallengeGasCapV1, dispatchChallengeKindCount)
	for i := range penalties {
		penalties[i] = &DispatchFaultPenaltyV1{Kind: shared.DispatchChallengeKindV1(i + 1), Amount: shared.NewAmount(10_000)}
	}
	for i := range gasCaps {
		gasCaps[i] = &DispatchChallengeGasCapV1{Kind: shared.DispatchChallengeKindV1(i + 1), GasCap: 10_000_000}
	}
	return DispatchParamsV1{
		RecordDeadlineBlocks: 20, MaxDispatchEntriesPerRecord: 16, MaxDispatchRecordBytes: 1 << 20,
		MaxSkips: 8, StickyWindowBlocks: 120, StickyMaxOrders: 8,
		DispatchCompensationClaimWindowBlocks: 60, DispatchCompensationRebuttalBlocks: 30,
		DispatchChallengeWindowBlocks: 60, DispatchChallengeResponseBlocks: 30,
		DispatchRecordRetentionBlocks: 1440, MaxDispatchRosterBytes: 1 << 20,
		MaxPendingPerSession: 4, BuilderMaxInflightPerSession: 4,
		OrderFreshnessMarginBlocks: 3, MaxPrerecordOutputTokens: 32,
		PrefillCompensationByModelTier: []*PrefillCompensationV1{
			{ResourceTier: 1, Amount: shared.NewAmount(1000)},
			{ResourceTier: 2, Amount: shared.NewAmount(2000)},
			{ResourceTier: 3, Amount: shared.NewAmount(4000)},
			{ResourceTier: 4, Amount: shared.NewAmount(8000)},
			{ResourceTier: 5, Amount: shared.NewAmount(16000)},
		},
		DispatchFaultPenaltyByKind: penalties, DispatchChallengeGasCapByKind: gasCaps,
		BuilderCompensationAccountMinBalance: shared.NewAmount(1_000_000),
		DispatchEvidenceRetentionBlocks:      1440, WorkerDispatchContradictionSlashBps: 300,
	}
}

func validateDispatchParams(p DispatchParamsV1, session SessionParamsV1) error {
	limits := shared.DispatchRotationWindowHardLimits()
	for _, item := range []struct {
		name           string
		value, maximum uint64
	}{
		{"record_deadline_blocks", p.RecordDeadlineBlocks, limits.RecordDeadlineBlocks},
		{"dispatch_compensation_claim_window_blocks", p.DispatchCompensationClaimWindowBlocks, limits.DispatchCompensationClaimWindowBlocks},
		{"dispatch_compensation_rebuttal_blocks", p.DispatchCompensationRebuttalBlocks, limits.DispatchCompensationRebuttalBlocks},
		{"dispatch_challenge_window_blocks", p.DispatchChallengeWindowBlocks, limits.DispatchChallengeWindowBlocks},
		{"dispatch_challenge_response_blocks", p.DispatchChallengeResponseBlocks, limits.DispatchChallengeResponseBlocks},
		{"max_dispatch_record_bytes", p.MaxDispatchRecordBytes, MaxBatchBytesLimit},
		{"max_dispatch_roster_bytes", p.MaxDispatchRosterBytes, MaxBatchBytesLimit},
		{"sticky_window_blocks", p.StickyWindowBlocks, MaxRetentionBlocksLimit},
		{"dispatch_record_retention_blocks", p.DispatchRecordRetentionBlocks, MaxRetentionBlocksLimit},
		{"dispatch_evidence_retention_blocks", p.DispatchEvidenceRetentionBlocks, MaxRetentionBlocksLimit},
		{"max_prerecord_output_tokens", p.MaxPrerecordOutputTokens, MaxGenerationOutputTokensLimit},
	} {
		if err := positiveUint64("dispatch."+item.name, item.value, item.maximum); err != nil {
			return err
		}
	}
	for _, item := range []struct {
		name           string
		value, maximum uint32
	}{
		{"max_dispatch_entries_per_record", p.MaxDispatchEntriesPerRecord, MaxBatchItemsLimit},
		{"sticky_max_orders", p.StickyMaxOrders, MaxSessionRowsLimit},
		{"max_pending_per_session", p.MaxPendingPerSession, MaxSessionRowsLimit},
		{"builder_max_inflight_per_session", p.BuilderMaxInflightPerSession, MaxSessionRowsLimit},
	} {
		if err := positiveUint32("dispatch."+item.name, item.value, item.maximum); err != nil {
			return err
		}
	}
	if p.MaxSkips > MaxCandidateUnionMembersLimit {
		return fmt.Errorf("dispatch.max_skips exceeds %d", MaxCandidateUnionMembersLimit)
	}
	if p.OrderFreshnessMarginBlocks >= p.RecordDeadlineBlocks {
		return fmt.Errorf("dispatch.order_freshness_margin_blocks must be less than record_deadline_blocks")
	}
	if session.SessionWithdrawDelayBlocks <= p.RecordDeadlineBlocks || session.SessionChangeDelayBlocks <= p.RecordDeadlineBlocks {
		return fmt.Errorf("session withdrawal and change delays must exceed dispatch.record_deadline_blocks")
	}
	if p.WorkerDispatchContradictionSlashBps > BasisPointsMaximum {
		return fmt.Errorf("dispatch.worker_dispatch_contradiction_slash_bps exceeds %d", BasisPointsMaximum)
	}
	// Preserve exclusion facts through every supported window, including a later
	// parameter increase within this protocol version's immutable clamps.
	retentionMinimum := limits.DispatchChallengeWindowBlocks + limits.DispatchCompensationClaimWindowBlocks + limits.DispatchCompensationRebuttalBlocks
	if p.DispatchRecordRetentionBlocks <= retentionMinimum {
		return fmt.Errorf("dispatch.dispatch_record_retention_blocks must exceed %d", retentionMinimum)
	}
	evidenceMinimum := max(limits.DispatchChallengeWindowBlocks, limits.RecordDeadlineBlocks+limits.DispatchCompensationClaimWindowBlocks+limits.DispatchCompensationRebuttalBlocks)
	if p.DispatchEvidenceRetentionBlocks < evidenceMinimum {
		return fmt.Errorf("dispatch.dispatch_evidence_retention_blocks must be at least %d", evidenceMinimum)
	}
	if len(p.PrefillCompensationByModelTier) == 0 || len(p.PrefillCompensationByModelTier) > int(MaxSupportedVersionCatalogEntriesLimit) {
		return fmt.Errorf("dispatch.prefill_compensation_by_model_tier has invalid size")
	}
	var previousTier uint32
	for i, entry := range p.PrefillCompensationByModelTier {
		if entry == nil || entry.ResourceTier == 0 || i > 0 && entry.ResourceTier <= previousTier {
			return fmt.Errorf("dispatch.prefill_compensation_by_model_tier must be sorted and unique")
		}
		if _, err := shared.ParseAmount(entry.Amount); err != nil {
			return fmt.Errorf("dispatch prefill amount: %w", err)
		}
		previousTier = entry.ResourceTier
	}
	if len(p.DispatchFaultPenaltyByKind) != dispatchFaultPenaltyKindCount || len(p.DispatchChallengeGasCapByKind) != dispatchChallengeKindCount {
		return fmt.Errorf("dispatch requires five fault penalties and four challenge gas caps")
	}
	for i, entry := range p.DispatchFaultPenaltyByKind {
		if entry == nil || entry.Kind != shared.DispatchChallengeKindV1(i+1) {
			return fmt.Errorf("dispatch fault penalties must cover each kind in order")
		}
		if _, err := shared.ParseAmount(entry.Amount); err != nil {
			return fmt.Errorf("dispatch fault penalty: %w", err)
		}
	}
	for i, entry := range p.DispatchChallengeGasCapByKind {
		if entry == nil || entry.Kind != shared.DispatchChallengeKindV1(i+1) || entry.GasCap == 0 {
			return fmt.Errorf("dispatch challenge gas caps must cover each kind in order with positive gas")
		}
	}
	if _, err := shared.ParseAmount(p.BuilderCompensationAccountMinBalance); err != nil {
		return fmt.Errorf("dispatch builder compensation minimum: %w", err)
	}
	return nil
}

func canonicalDispatchParamsFields(p DispatchParamsV1) []shared.CanonicalFieldV1 {
	fields := shared.RawCanonicalFieldsV1(
		shared.Uint64BE(p.RecordDeadlineBlocks), shared.Uint32BE(p.MaxDispatchEntriesPerRecord), shared.Uint64BE(p.MaxDispatchRecordBytes),
		shared.Uint32BE(p.MaxSkips), shared.Uint64BE(p.StickyWindowBlocks), shared.Uint32BE(p.StickyMaxOrders),
		shared.Uint64BE(p.DispatchCompensationClaimWindowBlocks), shared.Uint64BE(p.DispatchCompensationRebuttalBlocks),
		shared.Uint64BE(p.DispatchChallengeWindowBlocks), shared.Uint64BE(p.DispatchChallengeResponseBlocks),
		shared.Uint64BE(p.DispatchRecordRetentionBlocks), shared.Uint64BE(p.MaxDispatchRosterBytes),
		shared.Uint32BE(p.MaxPendingPerSession), shared.Uint32BE(p.BuilderMaxInflightPerSession),
		shared.Uint64BE(p.OrderFreshnessMarginBlocks), shared.Uint64BE(p.MaxPrerecordOutputTokens),
	)
	prefill := make([]shared.CanonicalFieldV1, len(p.PrefillCompensationByModelTier))
	for i, entry := range p.PrefillCompensationByModelTier {
		prefill[i] = canonicalDispatchAmountEntry(entry.ResourceTier, entry.Amount)
	}
	penalties := make([]shared.CanonicalFieldV1, len(p.DispatchFaultPenaltyByKind))
	for i, entry := range p.DispatchFaultPenaltyByKind {
		penalties[i] = canonicalDispatchAmountEntry(uint32(entry.Kind), entry.Amount)
	}
	gasCaps := make([]shared.CanonicalFieldV1, len(p.DispatchChallengeGasCapByKind))
	for i, entry := range p.DispatchChallengeGasCapByKind {
		gasCaps[i] = shared.NestedCanonicalFieldV1(shared.NewCanonicalFrameBuilderV1().Raw(shared.Uint32BE(uint32(entry.Kind)), shared.Uint64BE(entry.GasCap)).Build())
	}
	for _, entries := range [][]shared.CanonicalFieldV1{prefill, penalties, gasCaps} {
		fields = append(fields, shared.NestedCanonicalFieldV1(shared.CanonicalRepeatedFieldsV1(entries)))
	}
	fields = append(fields, canonicalTaskParamsAmountFields(p.BuilderCompensationAccountMinBalance)...)
	return append(fields, shared.RawCanonicalFieldsV1(shared.Uint64BE(p.DispatchEvidenceRetentionBlocks), shared.Uint32BE(p.WorkerDispatchContradictionSlashBps))...)
}

func canonicalDispatchAmountEntry(key uint32, amount shared.Amount) shared.CanonicalFieldV1 {
	fields := append(shared.RawCanonicalFieldsV1(shared.Uint32BE(key)), canonicalTaskParamsAmountFields(amount)...)
	return shared.NestedCanonicalFieldV1(shared.NewCanonicalFrameBuilderV1().Field(fields...).Build())
}
