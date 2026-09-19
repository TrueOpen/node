package types_test

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestEvidenceSchemaV1CanonicalFrameGolden(t *testing.T) {
	schema := shared.NewWorkerValueEvidenceSchemaV1(1 << 30)
	frame, err := shared.CanonicalEvidenceSchemaFrameV1(schema)
	require.NoError(t, err)
	// FRAME_V1(u32_be(schema_version), REPEATED_V1([requirement])):
	//   0000000000000004 00000001                  schema_version = 1
	//   000000000000003c                           the 60-byte REPEATED_V1 frame
	//     0000000000000004 00000001                  element count = 1
	//     0000000000000028                           the 40-byte requirement frame
	//       0000000000000004 00000001                  evidence_kind
	//       0000000000000004 00000002                  commitment_schema_version
	//       0000000000000008 0000000040000000          max_encoded_size_bytes
	//
	// The requirement list used to be spliced straight into the message frame as
	// u32_be(n) followed by the elements, which is the
	// canonical_encoding_and_domain_hashing.md §4.4 flattening
	// removed everywhere else; see CanonicalEvidenceSchemaTypedFrameV1.
	require.Equal(t, "000000000000000400000001000000000000003c000000000000000400000001000000000000002800000000000000040000000100000000000000040000000200000000000000080000000040000000", hex.EncodeToString(frame))

	mutated := schema
	mutated.RequiredInferEvidence = append([]shared.InferEvidenceRequirementV1(nil), schema.RequiredInferEvidence...)
	mutated.RequiredInferEvidence[0].MaxEncodedSizeBytes++
	changed, err := shared.CanonicalEvidenceSchemaFrameV1(mutated)
	require.NoError(t, err)
	require.NotEqual(t, frame, changed)
}

func TestEvidenceSchemaV1RejectsAmbiguousRequirements(t *testing.T) {
	valid := shared.NewWorkerValueEvidenceSchemaV1(1 << 30)

	empty := valid
	empty.RequiredInferEvidence = nil
	require.ErrorContains(t, shared.ValidateEvidenceSchemaV1(empty), "non-empty")

	duplicate := valid
	duplicate.RequiredInferEvidence = append(duplicate.RequiredInferEvidence, duplicate.RequiredInferEvidence[0])
	require.ErrorContains(t, shared.ValidateEvidenceSchemaV1(duplicate), "strictly ascending and unique")

	reversed := valid
	reversed.RequiredInferEvidence = []shared.InferEvidenceRequirementV1{
		{EvidenceKind: shared.EvidenceKind_EVIDENCE_KIND_VERIFIER_VALUE_OPENING, CommitmentSchemaVersion: 1, MaxEncodedSizeBytes: 1},
		{EvidenceKind: shared.EvidenceKind_EVIDENCE_KIND_WORKER_VALUE_OPENING, CommitmentSchemaVersion: 1, MaxEncodedSizeBytes: 1},
	}
	require.ErrorContains(t, shared.ValidateEvidenceSchemaV1(reversed), "strictly ascending and unique")

	unknown := valid
	unknown.RequiredInferEvidence = append([]shared.InferEvidenceRequirementV1(nil), valid.RequiredInferEvidence...)
	unknown.RequiredInferEvidence[0].EvidenceKind = shared.EvidenceKind(99)
	require.ErrorContains(t, shared.ValidateEvidenceSchemaV1(unknown), "unknown evidence_kind")

	tooLarge := valid
	tooLarge.RequiredInferEvidence = append([]shared.InferEvidenceRequirementV1(nil), valid.RequiredInferEvidence...)
	tooLarge.RequiredInferEvidence[0].MaxEncodedSizeBytes = shared.MaxEvidenceEncodedSizeBytesV1 + 1
	require.ErrorContains(t, shared.ValidateEvidenceSchemaV1(tooLarge), "max_encoded_size_bytes")
}
