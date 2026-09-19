package types

import (
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
)

const verifierResultPayloadVersionV1 = "VERIFIER_RESULT_REVEAL_V1"

// VerifierResultPayloadInput contains the authoritative inputs to the opaque
// canonical verifier payload. Caller-supplied ResultReceiptV2 fields and
// Keeper-derived task/assignment facts are deliberately combined only here.
type VerifierResultPayloadInput struct {
	ChainID                           string
	TaskID                            []byte
	TaskHash                          []byte
	VerifyRound                       uint32
	SelectedVerifierIndex             uint32
	VerifierOperatorAddress           string
	InferReceiptHash                  []byte
	ProfileExecutionSnapshotHash      []byte
	GenerationParamsDigest            []byte
	MetricRoot                        []byte
	MetricLeafCount                   uint32
	MetricSummaryHash                 []byte
	AggregateProofHash                []byte
	VerifierEvidenceBundleHash        []byte
	VerifierEvidenceManifestSizeBytes uint64
}

// VerifierResultPayloadHash derives the H_V1 commitment to the canonical
// verifier payload published by Wire v0.3.0.
func VerifierResultPayloadHash(input VerifierResultPayloadInput) ([32]byte, error) {
	chainID, err := canonicalUTF8Field("chain_id", input.ChainID)
	if err != nil {
		return [32]byte{}, err
	}
	verifier, err := CanonicalOperatorAddressBytes("verifier_operator_address", input.VerifierOperatorAddress)
	if err != nil {
		return [32]byte{}, err
	}
	hashes := []struct {
		name  string
		value []byte
	}{
		{"task_id", input.TaskID},
		{"task_hash", input.TaskHash},
		{"infer_receipt_hash", input.InferReceiptHash},
		{"profile_execution_snapshot_hash", input.ProfileExecutionSnapshotHash},
		{"generation_params_digest", input.GenerationParamsDigest},
		{"metric_root", input.MetricRoot},
		{"metric_summary_hash", input.MetricSummaryHash},
		{"aggregate_proof_hash", input.AggregateProofHash},
		{"verifier_evidence_bundle_hash", input.VerifierEvidenceBundleHash},
	}
	canonical := make([][]byte, len(hashes))
	for i, item := range hashes {
		canonical[i], err = canonicalHash32(item.name, item.value)
		if err != nil {
			return [32]byte{}, err
		}
	}
	payload, err := shared.FlatCanonicalFrameV1(
		[]byte(verifierResultPayloadVersionV1), chainID,
		canonical[0], canonical[1], shared.Uint32BE(input.VerifyRound),
		shared.Uint32BE(input.SelectedVerifierIndex), verifier,
		canonical[2], canonical[3], canonical[4], canonical[5],
		shared.Uint32BE(input.MetricLeafCount), canonical[6], canonical[7],
		canonical[8], shared.Uint64BE(input.VerifierEvidenceManifestSizeBytes),
	).Bytes()
	if err != nil {
		return [32]byte{}, err
	}
	digest, err := shared.PayloadHashV1(shared.MustDomain(shared.DomainVerifierResultPayloadV1), payload)
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

// CanonicalMetricSummaryFrameV1 recursively frames MetricSummaryV1 in schema
// field-number order. Optional fields 7 and 8 retain explicit presence so an
// absent metric cannot collide with a present zero value.
func CanonicalMetricSummaryFrameV1(summary MetricSummaryV1) ([]byte, error) {
	frame, err := CanonicalMetricSummaryTypedFrameV1(summary)
	if err != nil {
		return nil, err
	}
	return frame.Bytes()
}

func CanonicalMetricSummaryTypedFrameV1(summary MetricSummaryV1) (shared.CanonicalFrameV1, error) {
	topK, err := canonicalOptionalUint32(
		"metric_summary.topk_jaccard_mean_fp_1e6",
		summary.XTopkJaccardMeanFp_1E6,
	)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	unionJS, err := canonicalOptionalUint32(
		"metric_summary.union_js_p99_fp_1e6",
		summary.XUnionJsP99Fp_1E6,
	)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	frame := shared.NewCanonicalFrameBuilderV1().Raw(
		shared.Uint32BE(summary.FiniteCount),
		shared.Uint32BE(summary.MissingComparedCount),
		shared.Uint32BE(summary.MeanAbsLogprobDiffFp_1E6),
		shared.Uint32BE(summary.AbsLogprobDiffP95Fp_1E6),
		shared.Uint32BE(summary.AbsLogprobDiffP99Fp_1E6),
		shared.Uint32BE(summary.RankDeltaNonzeroRateFp_1E6),
	).Field(topK, unionJS).Raw(
		shared.Uint32BE(summary.ComparedTopkCount),
		shared.Uint32BE(summary.ComparedRankCount),
	).Build()
	return frame, frame.Err()
}

// MetricSummaryHash derives the only TRUEOPEN_METRIC_SUMMARY_V1 commitment. The
// canonical nested message is one top-level H_FIELDS_V1 field.
func MetricSummaryHash(summary MetricSummaryV1) ([32]byte, error) {
	frame, err := CanonicalMetricSummaryTypedFrameV1(summary)
	if err != nil {
		return [32]byte{}, err
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainMetricSummaryV1)).Nested(frame).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

// ResultReceiptSigningDigest derives the frozen TRUEOPEN_RESULT_V2 signing
// digest. service_signature is deliberately excluded.
func ResultReceiptSigningDigest(receipt ResultReceiptV2) ([32]byte, error) {
	chainID, err := canonicalUTF8Field("chain_id", receipt.ChainId)
	if err != nil {
		return [32]byte{}, err
	}
	taskID, err := canonicalHash32("task_id", receipt.TaskId)
	if err != nil {
		return [32]byte{}, err
	}
	verifier, err := CanonicalOperatorAddressBytes("verifier_operator_address", receipt.VerifierOperatorAddress)
	if err != nil {
		return [32]byte{}, err
	}
	generationParamsDigest, err := canonicalHash32("generation_params_digest", receipt.GenerationParamsDigest)
	if err != nil {
		return [32]byte{}, err
	}
	metricRoot, err := canonicalHash32("metric_root", receipt.MetricRoot)
	if err != nil {
		return [32]byte{}, err
	}
	metricSummary, err := CanonicalMetricSummaryTypedFrameV1(receipt.MetricSummary)
	if err != nil {
		return [32]byte{}, err
	}
	aggregateProofHash, err := canonicalHash32("aggregate_proof_hash", receipt.AggregateProofHash)
	if err != nil {
		return [32]byte{}, err
	}
	verifierEvidenceBundleHash, err := canonicalHash32("verifier_evidence_bundle_hash", receipt.VerifierEvidenceBundleHash)
	if err != nil {
		return [32]byte{}, err
	}
	salt, err := canonicalHash32("salt", receipt.Salt)
	if err != nil {
		return [32]byte{}, err
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainResultV2)).Raw(
		shared.Uint32BE(receipt.SchemaVersion),
		chainID,
		taskID,
		shared.Uint32BE(receipt.VerifyRound),
		verifier,
		shared.Uint64BE(receipt.ServiceAuthorizationNonce),
		generationParamsDigest,
		metricRoot,
	).Nested(metricSummary).Raw(
		aggregateProofHash,
		verifierEvidenceBundleHash,
		shared.Uint64BE(receipt.VerifierEvidenceManifestSizeBytes),
		salt,
		shared.Uint64BE(receipt.ExpiryHeight),
	).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

// DeriveCommitKey derives the chain-bound primary key shared by CommitState,
// ResultReceiptState and FullResultRevealState.
func DeriveCommitKey(chainID string, taskID []byte, verifyRound uint32, verifierOperatorAddress string) ([32]byte, error) {
	chain, err := canonicalUTF8Field("chain_id", chainID)
	if err != nil {
		return [32]byte{}, err
	}
	task, err := canonicalHash32("task_id", taskID)
	if err != nil {
		return [32]byte{}, err
	}
	verifier, err := CanonicalOperatorAddressBytes("verifier_operator_address", verifierOperatorAddress)
	if err != nil {
		return [32]byte{}, err
	}
	return canonicalTaskDigestV1(
		shared.DomainCommitKeyV1,
		chain,
		task,
		shared.Uint32BE(verifyRound),
		verifier,
	)
}

// ResultCommitmentHash derives the V2 commitment opened by ResultReceiptV2.
func ResultCommitmentHash(
	chainID string,
	taskID []byte,
	taskHash []byte,
	verifyRound uint32,
	verifierOperatorAddress string,
	resultPayloadHash []byte,
	salt []byte,
) ([32]byte, error) {
	chain, err := canonicalUTF8Field("chain_id", chainID)
	if err != nil {
		return [32]byte{}, err
	}
	task, err := canonicalHash32("task_id", taskID)
	if err != nil {
		return [32]byte{}, err
	}
	taskDigest, err := canonicalHash32("task_hash", taskHash)
	if err != nil {
		return [32]byte{}, err
	}
	verifier, err := CanonicalOperatorAddressBytes("verifier_operator_address", verifierOperatorAddress)
	if err != nil {
		return [32]byte{}, err
	}
	payloadHash, err := canonicalHash32("result_payload_hash", resultPayloadHash)
	if err != nil {
		return [32]byte{}, err
	}
	canonicalSalt, err := canonicalHash32("salt", salt)
	if err != nil {
		return [32]byte{}, err
	}
	return canonicalTaskDigestV1(
		shared.DomainResultCommitmentV2,
		chain,
		task,
		taskDigest,
		shared.Uint32BE(verifyRound),
		verifier,
		payloadHash,
		canonicalSalt,
	)
}

func canonicalOptionalUint32(field string, value interface{}) (shared.CanonicalFieldV1, error) {
	switch typed := value.(type) {
	case nil:
		return shared.RawCanonicalFieldV1(shared.OptionalAbsentFrameV1()), nil
	case *MetricSummaryV1_TopkJaccardMeanFp_1E6:
		if typed == nil {
			return shared.CanonicalFieldV1{}, fmt.Errorf("%s has a nil selected payload", field)
		}
		return shared.PrefixedNestedCanonicalFieldV1(
			[]byte{1}, shared.FlatCanonicalFrameV1(shared.Uint32BE(typed.TopkJaccardMeanFp_1E6)),
		), nil
	case *MetricSummaryV1_UnionJsP99Fp_1E6:
		if typed == nil {
			return shared.CanonicalFieldV1{}, fmt.Errorf("%s has a nil selected payload", field)
		}
		return shared.PrefixedNestedCanonicalFieldV1(
			[]byte{1}, shared.FlatCanonicalFrameV1(shared.Uint32BE(typed.UnionJsP99Fp_1E6)),
		), nil
	default:
		return shared.CanonicalFieldV1{}, fmt.Errorf("%s has an unknown optional payload %T", field, value)
	}
}
