package types

import (
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
)

const WorkerValueCommitmentSchemaVersionV3 = uint32(3)

// WorkerValueCommitment derives the B-level Worker value commitment.
func WorkerValueCommitment(value WorkerValueCommitmentV3) ([32]byte, uint64, error) {
	var zero [32]byte
	if value.SchemaVersion != WorkerValueCommitmentSchemaVersionV3 {
		return zero, 0, fmt.Errorf("worker value schema_version must be %d", WorkerValueCommitmentSchemaVersionV3)
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
		{"evidence_schema_hash", value.EvidenceSchemaHash},
		{"worker_value_root", value.WorkerValueRoot},
	} {
		if _, err := canonicalHash32(item.name, item.value); err != nil {
			return zero, 0, err
		}
	}
	if value.WorkerValuesEncodedSizeBytes == 0 ||
		value.WorkerValuesEncodedSizeBytes > shared.MaxEvidenceEncodedSizeBytesV1 {
		return zero, 0, fmt.Errorf("worker_values_encoded_size_bytes is invalid")
	}
	digest, err := canonicalTaskDigestV1(shared.DomainWorkerValueCommitmentV3,
		shared.Uint32BE(value.SchemaVersion),
		[]byte(value.ChainId),
		value.TaskId,
		value.AcceptedTaskHash,
		worker,
		value.EvidenceSchemaHash,
		value.WorkerValueRoot,
		shared.Uint64BE(value.WorkerValuesEncodedSizeBytes),
	)
	if err != nil {
		return zero, 0, err
	}
	return digest, value.WorkerValuesEncodedSizeBytes, nil
}
