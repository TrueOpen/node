package types

import "fmt"

const (
	EvidenceSchemaVersionV1              = uint32(1)
	WorkerValueCommitmentSchemaVersionV2 = uint32(2)
	MaxEvidenceEncodedSizeBytesV1        = uint64(1 << 40)
)

func NewWorkerValueEvidenceSchemaV1(maxEncodedSizeBytes uint64) EvidenceSchemaV1 {
	return EvidenceSchemaV1{
		SchemaVersion: EvidenceSchemaVersionV1,
		RequiredInferEvidence: []InferEvidenceRequirementV1{{
			EvidenceKind:            EvidenceKind_EVIDENCE_KIND_WORKER_VALUE_OPENING,
			CommitmentSchemaVersion: WorkerValueCommitmentSchemaVersionV2,
			MaxEncodedSizeBytes:     maxEncodedSizeBytes,
		}},
	}
}

// ValidateEvidenceSchemaV1 validates the generic descriptor shape. A profile
// family may impose a stricter exact requirement set on top of this check.
func ValidateEvidenceSchemaV1(schema EvidenceSchemaV1) error {
	if schema.SchemaVersion != EvidenceSchemaVersionV1 {
		return fmt.Errorf("evidence schema_version must be %d", EvidenceSchemaVersionV1)
	}
	if len(schema.RequiredInferEvidence) == 0 {
		return fmt.Errorf("required_infer_evidence must be non-empty")
	}
	previous := EvidenceKind_EVIDENCE_KIND_UNSPECIFIED
	for index, requirement := range schema.RequiredInferEvidence {
		if !IsKnownEvidenceKind(requirement.EvidenceKind) {
			return fmt.Errorf("required_infer_evidence[%d] has unknown evidence_kind %d", index, requirement.EvidenceKind)
		}
		if index > 0 && requirement.EvidenceKind <= previous {
			return fmt.Errorf("required_infer_evidence must be strictly ascending and unique")
		}
		if requirement.CommitmentSchemaVersion == 0 {
			return fmt.Errorf("required_infer_evidence[%d] commitment_schema_version must be positive", index)
		}
		if requirement.MaxEncodedSizeBytes == 0 || requirement.MaxEncodedSizeBytes > MaxEvidenceEncodedSizeBytesV1 {
			return fmt.Errorf("required_infer_evidence[%d] max_encoded_size_bytes must be in 1..%d", index, MaxEvidenceEncodedSizeBytesV1)
		}
		previous = requirement.EvidenceKind
	}
	return nil
}

func IsKnownEvidenceKind(kind EvidenceKind) bool {
	switch kind {
	case EvidenceKind_EVIDENCE_KIND_WORKER_VALUE_OPENING,
		EvidenceKind_EVIDENCE_KIND_VERIFIER_VALUE_OPENING,
		EvidenceKind_EVIDENCE_KIND_SETTLEMENT_ROOT_OPENING:
		return true
	default:
		return false
	}
}

func CanonicalInferEvidenceRequirementFrameV1(requirement InferEvidenceRequirementV1) ([]byte, error) {
	return CanonicalInferEvidenceRequirementTypedFrameV1(requirement).Bytes()
}

func CanonicalInferEvidenceRequirementTypedFrameV1(requirement InferEvidenceRequirementV1) CanonicalFrameV1 {
	if !IsKnownEvidenceKind(requirement.EvidenceKind) {
		return canonicalFrameErrorV1(fmt.Errorf("unknown evidence_kind %d", requirement.EvidenceKind))
	}
	if requirement.CommitmentSchemaVersion == 0 {
		return canonicalFrameErrorV1(fmt.Errorf("commitment_schema_version must be positive"))
	}
	if requirement.MaxEncodedSizeBytes == 0 || requirement.MaxEncodedSizeBytes > MaxEvidenceEncodedSizeBytesV1 {
		return canonicalFrameErrorV1(fmt.Errorf("max_encoded_size_bytes must be in 1..%d", MaxEvidenceEncodedSizeBytesV1))
	}
	return FlatCanonicalFrameV1(
		EnumBE(uint32(requirement.EvidenceKind)),
		Uint32BE(requirement.CommitmentSchemaVersion),
		Uint64BE(requirement.MaxEncodedSizeBytes),
	)
}

func CanonicalEvidenceSchemaFrameV1(schema EvidenceSchemaV1) ([]byte, error) {
	return CanonicalEvidenceSchemaTypedFrameV1(schema).Bytes()
}

// CanonicalEvidenceSchemaTypedFrameV1 is NESTED_V1(EvidenceSchemaV1): the two
// proto fields of the message, schema_version and the required_infer_evidence
// list, in field-number order.
//
// The list is ONE position. canonical_encoding_and_domain_hashing.md §4.4 fixes
// REPEATED_V1([e1..en]) = FRAME_V1(u32_be(n), ENC(e1), ..., ENC(en)), so the
// count and the elements live inside their own frame. This function used to write
// FRAME_V1(schema_version, u32_be(n), ENC(e1), ..., ENC(en)) instead - the count
// and every element spliced into the message frame as siblings of
// schema_version - which is the §4.4 violation removed from 30 other
// shapes.
//
// It survived that audit because it is not a top-level domain preimage: it is
// field 14 of VerificationProfile, which is field 6 of ProfileExecutionSnapshot,
// and the differential's assertRepeatedTailIsOnePosition only counts a DOMAIN's
// outer positions. The cross-language vector for
// TRUEOPEN_PROFILE_VERIFICATION_SNAPSHOT_V1 is what made the nested layer visible,
// and publishing the spliced bytes as a golden would have frozen the violation
// into every other implementation. The audit total is therefore 31, not 30.
//
// The same module already had the compliant encoding: EvidenceSchemaHash in
// x/hub/types/model_registration.go frames the identical list through
// CanonicalRepeatedFramesV1. Two encodings of one message in one module is the
// consensus fork the MetricSpec refactor note in canonicalVerificationProfile
// warns about; there is now one.
func CanonicalEvidenceSchemaTypedFrameV1(schema EvidenceSchemaV1) CanonicalFrameV1 {
	if err := ValidateEvidenceSchemaV1(schema); err != nil {
		return canonicalFrameErrorV1(err)
	}
	elements := make([]CanonicalFrameV1, 0, len(schema.RequiredInferEvidence))
	for index, requirement := range schema.RequiredInferEvidence {
		frame := CanonicalInferEvidenceRequirementTypedFrameV1(requirement)
		if err := frame.Err(); err != nil {
			return canonicalFrameErrorV1(fmt.Errorf("required_infer_evidence[%d]: %w", index, err))
		}
		elements = append(elements, frame)
	}
	return NewCanonicalFrameBuilderV1().
		Raw(Uint32BE(schema.SchemaVersion)).
		Nested(CanonicalRepeatedFramesV1(elements)).
		Build()
}
