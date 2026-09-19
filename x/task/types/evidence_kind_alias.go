package types

import shared "github.com/TrueOpen/node/x/shared/types"

// EvidenceKind moved to shared so Hub Profile descriptors and Task
// receipts use one numeric authority. These aliases keep existing Go clients
// source-compatible while generated clients migrate their import path.
type EvidenceKind = shared.EvidenceKind

const (
	EvidenceKind_EVIDENCE_KIND_UNSPECIFIED             = shared.EvidenceKind_EVIDENCE_KIND_UNSPECIFIED
	EvidenceKind_EVIDENCE_KIND_WORKER_VALUE_OPENING    = shared.EvidenceKind_EVIDENCE_KIND_WORKER_VALUE_OPENING
	EvidenceKind_EVIDENCE_KIND_VERIFIER_VALUE_OPENING  = shared.EvidenceKind_EVIDENCE_KIND_VERIFIER_VALUE_OPENING
	EvidenceKind_EVIDENCE_KIND_SETTLEMENT_ROOT_OPENING = shared.EvidenceKind_EVIDENCE_KIND_SETTLEMENT_ROOT_OPENING
)

var (
	EvidenceKind_name  = shared.EvidenceKind_name
	EvidenceKind_value = shared.EvidenceKind_value
)
