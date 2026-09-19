package types

import (
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
)

const WorkerValueCommitmentSchemaVersionV2 = uint32(2)

// WorkerValueCommitment derives the one WORKER_VALUE_OPENING commitment and
// the exact encoded_size_bytes carried by InferReceipt.
func WorkerValueCommitment(value WorkerValueCommitmentV2) ([32]byte, uint64, error) {
	var zero [32]byte
	if value.SchemaVersion != WorkerValueCommitmentSchemaVersionV2 {
		return zero, 0, fmt.Errorf("worker value schema_version must be %d", WorkerValueCommitmentSchemaVersionV2)
	}
	if value.ChainId == "" {
		return zero, 0, fmt.Errorf("chain_id must be non-empty")
	}
	worker, err := CanonicalOperatorAddressBytes("worker_operator_address", value.WorkerOperatorAddress)
	if err != nil {
		return zero, 0, err
	}
	for _, item := range []struct {
		name  string
		value []byte
	}{
		{"task_id", value.TaskId},
		{"accepted_task_hash", value.AcceptedTaskHash},
		{"generation_params_digest", value.GenerationParamsDigest},
		{"evidence_schema_hash", value.EvidenceSchemaHash},
		{"output_hash", value.OutputHash},
		{"trace_root", value.TraceRoot},
		{"checkpoint_root", value.CheckpointRoot},
		{"input_token_ids_hash", value.InputTokenIdsHash},
		{"generated_token_ids_hash", value.GeneratedTokenIdsHash},
	} {
		if _, err := canonicalHash32(item.name, item.value); err != nil {
			return zero, 0, err
		}
	}
	if value.OutputLeafCount == 0 {
		return zero, 0, fmt.Errorf("output_leaf_count must be positive")
	}
	if value.TraceEncodedSizeBytes == 0 || value.CheckpointEncodedSizeBytes == 0 ||
		value.InputTokenIdsSizeBytes == 0 || value.GeneratedTokenIdsSizeBytes == 0 {
		return zero, 0, fmt.Errorf("all four evidence artifact encoded sizes must be positive")
	}
	if err := validateTokenIDsRawSizeV1("input_token_ids_size_bytes", value.InputTokenIdsSizeBytes, nil); err != nil {
		return zero, 0, err
	}
	if err := validateTokenIDsRawSizeV1("generated_token_ids_size_bytes", value.GeneratedTokenIdsSizeBytes, &value.GeneratedTokenCount); err != nil {
		return zero, 0, err
	}
	encodedSizeBytes := uint64(0)
	for _, size := range []uint64{
		value.TraceEncodedSizeBytes,
		value.CheckpointEncodedSizeBytes,
		value.InputTokenIdsSizeBytes,
		value.GeneratedTokenIdsSizeBytes,
	} {
		var overflow bool
		encodedSizeBytes, overflow = shared.CheckedAddUint64(encodedSizeBytes, size)
		if overflow || encodedSizeBytes > shared.MaxEvidenceEncodedSizeBytesV1 {
			return zero, 0, fmt.Errorf("combined evidence artifact encoded size exceeds %d", shared.MaxEvidenceEncodedSizeBytesV1)
		}
	}
	switch value.FinishReason {
	case FinishReasonV1_FINISH_REASON_V1_EOS_TOKEN,
		FinishReasonV1_FINISH_REASON_V1_STOP_SEQUENCE,
		FinishReasonV1_FINISH_REASON_V1_MAX_OUTPUT_TOKENS,
		FinishReasonV1_FINISH_REASON_V1_MAX_OUTPUT_DURATION:
	default:
		return zero, 0, fmt.Errorf("finish_reason %d is not a successful V1 reason", value.FinishReason)
	}
	digest, err := canonicalTaskDigestV1(shared.DomainWorkerValueCommitmentV2,
		shared.Uint32BE(value.SchemaVersion),
		[]byte(value.ChainId),
		value.TaskId,
		value.AcceptedTaskHash,
		worker,
		value.GenerationParamsDigest,
		value.EvidenceSchemaHash,
		value.OutputHash,
		shared.Uint64BE(value.OutputSizeBytes),
		shared.EnumBE(uint32(value.FinishReason)),
		value.TraceRoot,
		shared.Uint64BE(value.TraceEncodedSizeBytes),
		value.CheckpointRoot,
		shared.Uint64BE(value.CheckpointEncodedSizeBytes),
		shared.Uint64BE(value.GeneratedTokenCount),
		shared.Uint64BE(value.OutputLeafCount),
		value.InputTokenIdsHash,
		value.GeneratedTokenIdsHash,
		shared.Uint64BE(value.InputTokenIdsSizeBytes),
		shared.Uint64BE(value.GeneratedTokenIdsSizeBytes),
	)
	if err != nil {
		return zero, 0, err
	}
	return digest, encodedSizeBytes, nil
}
