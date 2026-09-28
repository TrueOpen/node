package types_test

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestEvidenceSchemaV1CanonicalFrameGolden(t *testing.T) {
	schema := shared.NewPhase0WorkerEvidenceSchemaV1(1<<30, 1<<26)
	frame, err := shared.CanonicalEvidenceSchemaFrameV1(schema)
	require.NoError(t, err)
	// FRAME_V1(u32_be(schema_version), REPEATED_V1([value, token])):
	//   0000000000000004 00000001                  schema_version = 1
	//   000000000000006c                           the 108-byte REPEATED_V1 frame
	//     0000000000000004 00000002                  element count = 2
	//     two 40-byte requirement frames for Worker value V3 and token V1
	//
	// The requirement list used to be spliced straight into the message frame as
	// u32_be(n) followed by the elements, which is the
	// flattening
	// removed everywhere else; see CanonicalEvidenceSchemaTypedFrameV1.
	require.Equal(t, "000000000000000400000001000000000000006c000000000000000400000002000000000000002800000000000000040000000100000000000000040000000300000000000000080000000040000000000000000000002800000000000000040000000400000000000000040000000100000000000000080000000004000000", hex.EncodeToString(frame))

	mutated := schema
	mutated.RequiredInferEvidence = append([]shared.InferEvidenceRequirementV1(nil), schema.RequiredInferEvidence...)
	mutated.RequiredInferEvidence[0].MaxEncodedSizeBytes++
	changed, err := shared.CanonicalEvidenceSchemaFrameV1(mutated)
	require.NoError(t, err)
	require.NotEqual(t, frame, changed)
}

func TestEvidenceSchemaV1RejectsAmbiguousRequirements(t *testing.T) {
	valid := shared.NewPhase0WorkerEvidenceSchemaV1(1<<30, 1<<26)

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
