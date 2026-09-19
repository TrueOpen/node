package types

import (
	"bytes"
	"fmt"
	"math"

	shared "github.com/TrueOpen/node/x/shared/types"
)

const WorkerEvidenceSchemaVersionV1 = uint32(1)

// DecodeWorkerEvidenceV1 accepts only the canonical protobuf encoding. The
// marshal round trip rejects unknown, duplicate, out-of-order and non-minimal
// fields that the ordinary Cosmos decoder would otherwise normalize away.
func DecodeWorkerEvidenceV1(raw []byte) (WorkerEvidenceV1, error) {
	var evidence WorkerEvidenceV1
	if len(raw) == 0 {
		return evidence, fmt.Errorf("worker evidence bytes are empty")
	}
	if err := evidence.Unmarshal(raw); err != nil {
		return WorkerEvidenceV1{}, err
	}
	canonical, err := evidence.Marshal()
	if err != nil {
		return WorkerEvidenceV1{}, err
	}
	if !bytes.Equal(canonical, raw) {
		return WorkerEvidenceV1{}, fmt.Errorf("worker evidence is not canonical strict protobuf")
	}
	if evidence.SchemaVersion != WorkerEvidenceSchemaVersionV1 || evidence.GetOutputChunkEquivocation() == nil {
		return WorkerEvidenceV1{}, fmt.Errorf("worker evidence must contain the v1 output-chunk-equivocation branch")
	}
	return evidence, nil
}

func OutputChunkSigningDigest(chainID string, taskHash []byte, seq uint64, streamedMMRRoot []byte) ([32]byte, error) {
	if chainID == "" || len(taskHash) != Hash32Len || len(streamedMMRRoot) != Hash32Len {
		return [32]byte{}, fmt.Errorf("output chunk signing scope is invalid")
	}
	return canonicalTaskDigestV1(
		shared.DomainOutputChunkV1, []byte(chainID), taskHash, shared.Uint64BE(seq), streamedMMRRoot,
	)
}

func OutputChunkEquivocationDigest(evidence OutputChunkEquivocationV1) ([32]byte, error) {
	if len(evidence.TaskId) != Hash32Len || len(evidence.AcceptedInferReceiptHash) != Hash32Len ||
		len(evidence.StreamedMmrRoot) != Hash32Len || len(evidence.WorkerSignature) != 64 ||
		uint64(len(evidence.PrefixPeakProofs)) > math.MaxUint32 {
		return [32]byte{}, fmt.Errorf("output chunk equivocation scope is invalid")
	}
	proofFrames := make([]shared.CanonicalFrameV1, len(evidence.PrefixPeakProofs))
	for proofIndex, proof := range evidence.PrefixPeakProofs {
		if len(proof.PeakHash) != Hash32Len {
			return [32]byte{}, fmt.Errorf("prefix proof %d peak_hash must be Hash32", proofIndex)
		}
		pathFields := make([]shared.CanonicalFieldV1, len(proof.FinalInclusionPath))
		for pathIndex, hash := range proof.FinalInclusionPath {
			if len(hash) != Hash32Len {
				return [32]byte{}, fmt.Errorf("prefix proof %d path %d must be Hash32", proofIndex, pathIndex)
			}
			pathFields[pathIndex] = shared.RawCanonicalFieldV1(hash)
		}
		proofFrames[proofIndex] = shared.NewCanonicalFrameBuilderV1().Raw(proof.PeakHash).
			Nested(shared.CanonicalRepeatedFieldsV1(pathFields)).Build()
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainOutputChunkEquivocationV1)).Raw(
		evidence.TaskId, evidence.AcceptedInferReceiptHash, shared.Uint64BE(evidence.Seq), evidence.StreamedMmrRoot,
		evidence.WorkerSignature, shared.Uint32BE(uint32(len(proofFrames))),
	).Nested(shared.CanonicalRepeatedFramesV1(proofFrames)).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

type mmrPeakRange struct {
	start  uint64
	height uint8
}

func mmrPeakRanges(count uint64) []mmrPeakRange {
	peaks := make([]mmrPeakRange, 0, 64)
	var start uint64
	for height := 63; height >= 0; height-- {
		size := uint64(1) << uint(height)
		if count&size == 0 {
			continue
		}
		peaks = append(peaks, mmrPeakRange{start: start, height: uint8(height)})
		start += size
	}
	return peaks
}

// VerifyOutputChunkEquivocationProof verifies every uniquely-positioned prefix
// peak against the accepted final root, then returns whether the valid signed
// prefix root conflicts with that accepted tree.
func VerifyOutputChunkEquivocationProof(
	finalLeafCount uint64, finalRoot []byte, seq uint64, streamedRoot []byte, proofs []OutputMMRPeakProofV1,
) (bool, error) {
	if finalLeafCount == 0 || len(finalRoot) != Hash32Len || len(streamedRoot) != Hash32Len || seq == math.MaxUint64 {
		return false, fmt.Errorf("output MMR proof scope is invalid")
	}
	prefixCount := seq + 1
	if prefixCount > finalLeafCount {
		return false, fmt.Errorf("output chunk seq exceeds the accepted leaf count")
	}
	finalPeaks := mmrPeakRanges(finalLeafCount)
	prefixPeaks := mmrPeakRanges(prefixCount)
	if len(proofs) != len(prefixPeaks) {
		return false, fmt.Errorf("prefix proof count %d, expected %d", len(proofs), len(prefixPeaks))
	}
	for proofIndex, prefix := range prefixPeaks {
		proof := proofs[proofIndex]
		if len(proof.PeakHash) != Hash32Len {
			return false, fmt.Errorf("prefix proof %d peak_hash must be Hash32", proofIndex)
		}
		owner := -1
		prefixEnd := prefix.start + (uint64(1) << prefix.height)
		for finalIndex, candidate := range finalPeaks {
			candidateEnd := candidate.start + (uint64(1) << candidate.height)
			if prefix.start >= candidate.start && prefixEnd <= candidateEnd {
				owner = finalIndex
				break
			}
		}
		if owner < 0 || finalPeaks[owner].height < prefix.height {
			return false, fmt.Errorf("prefix proof %d has no final peak", proofIndex)
		}
		treePathLength := int(finalPeaks[owner].height - prefix.height)
		hasRightSuffix := owner+1 < len(finalPeaks)
		expectedPathLength := treePathLength + owner
		if hasRightSuffix {
			expectedPathLength++
		}
		if len(proof.FinalInclusionPath) != expectedPathLength {
			return false, fmt.Errorf("prefix proof %d path length %d, expected %d", proofIndex, len(proof.FinalInclusionPath), expectedPathLength)
		}
		current := append([]byte(nil), proof.PeakHash...)
		pathIndex := 0
		for level := int(prefix.height); level < int(finalPeaks[owner].height); level++ {
			sibling := proof.FinalInclusionPath[pathIndex]
			if len(sibling) != Hash32Len {
				return false, fmt.Errorf("prefix proof %d sibling %d must be Hash32", proofIndex, pathIndex)
			}
			var err error
			if (prefix.start>>uint(level))&1 == 0 {
				current, err = shared.MMRNodeV1(shared.DomainOutputMMRV1, current, sibling)
			} else {
				current, err = shared.MMRNodeV1(shared.DomainOutputMMRV1, sibling, current)
			}
			if err != nil {
				return false, err
			}
			pathIndex++
		}
		if hasRightSuffix {
			right := proof.FinalInclusionPath[pathIndex]
			if len(right) != Hash32Len {
				return false, fmt.Errorf("prefix proof %d right suffix must be Hash32", proofIndex)
			}
			current, _ = shared.MMRNodeV1(shared.DomainOutputMMRV1, current, right)
			pathIndex++
		}
		for leftIndex := owner - 1; leftIndex >= 0; leftIndex-- {
			left := proof.FinalInclusionPath[pathIndex]
			if len(left) != Hash32Len {
				return false, fmt.Errorf("prefix proof %d left peak must be Hash32", proofIndex)
			}
			current, _ = shared.MMRNodeV1(shared.DomainOutputMMRV1, left, current)
			pathIndex++
		}
		if !bytes.Equal(current, finalRoot) {
			return false, fmt.Errorf("prefix proof %d does not reconstruct the accepted final root", proofIndex)
		}
	}
	actualPrefixRoot := append([]byte(nil), proofs[len(proofs)-1].PeakHash...)
	for index := len(proofs) - 2; index >= 0; index-- {
		var err error
		actualPrefixRoot, err = shared.MMRNodeV1(shared.DomainOutputMMRV1, proofs[index].PeakHash, actualPrefixRoot)
		if err != nil {
			return false, err
		}
	}
	return !bytes.Equal(actualPrefixRoot, streamedRoot), nil
}
