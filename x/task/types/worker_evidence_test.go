package types

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOutputChunkEquivocationPublishedVector(t *testing.T) {
	var fixture struct {
		Positive struct {
			ChainID         string `json:"chain_id"`
			TaskID          string `json:"task_id_hex"`
			TaskHash        string `json:"task_hash_hex"`
			AcceptedReceipt string `json:"accepted_infer_receipt_hash_hex"`
			OutputHash      string `json:"accepted_output_hash_hex"`
			OutputLeafCount uint64 `json:"output_leaf_count"`
			Seq             uint64 `json:"seq"`
			StreamedRoot    string `json:"streamed_mmr_root_hex"`
			Signature       string `json:"worker_signature_raw64_hex"`
			ChunkDigest     string `json:"chunk_signing_digest_hex"`
			EvidenceDigest  string `json:"evidence_digest_hex"`
			Proofs          []struct {
				Peak string   `json:"peak_hash_hex"`
				Path []string `json:"final_inclusion_path_hex"`
			} `json:"proofs"`
		} `json:"positive"`
	}
	raw, err := os.ReadFile("testdata/output_chunk_equivocation_v1.json")
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &fixture))
	p := fixture.Positive
	decode := func(value string) []byte {
		result, decodeErr := hex.DecodeString(value)
		require.NoError(t, decodeErr)
		return result
	}
	proofs := make([]OutputMMRPeakProofV1, len(p.Proofs))
	for i, proof := range p.Proofs {
		proofs[i].PeakHash = decode(proof.Peak)
		for _, path := range proof.Path {
			proofs[i].FinalInclusionPath = append(proofs[i].FinalInclusionPath, decode(path))
		}
	}
	evidence := OutputChunkEquivocationV1{
		TaskId: decode(p.TaskID), AcceptedInferReceiptHash: decode(p.AcceptedReceipt), Seq: p.Seq,
		StreamedMmrRoot: decode(p.StreamedRoot), WorkerSignature: decode(p.Signature), PrefixPeakProofs: proofs,
	}
	digest, err := OutputChunkEquivocationDigest(evidence)
	require.NoError(t, err)
	require.Equal(t, p.EvidenceDigest, hex.EncodeToString(digest[:]))
	chunkDigest, err := OutputChunkSigningDigest(p.ChainID, decode(p.TaskHash), p.Seq, evidence.StreamedMmrRoot)
	require.NoError(t, err)
	require.Equal(t, p.ChunkDigest, hex.EncodeToString(chunkDigest[:]))
	conflict, err := VerifyOutputChunkEquivocationProof(p.OutputLeafCount, decode(p.OutputHash), p.Seq, evidence.StreamedMmrRoot, proofs)
	require.NoError(t, err)
	require.True(t, conflict)

	wire := WorkerEvidenceV1{SchemaVersion: WorkerEvidenceSchemaVersionV1,
		Evidence: &WorkerEvidenceV1_OutputChunkEquivocation{OutputChunkEquivocation: &evidence}}
	encoded, err := wire.Marshal()
	require.NoError(t, err)
	_, err = DecodeWorkerEvidenceV1(encoded)
	require.NoError(t, err)
	_, err = DecodeWorkerEvidenceV1(append(encoded, 0x78, 0x01))
	require.ErrorContains(t, err, "canonical")
}
