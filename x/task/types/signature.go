package types

import (
	"fmt"
	"math"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/wire/dispatch"
	"github.com/cosmos/gogoproto/proto"
)

// These aliases keep the public Task types surface tied to the shared registry.
// Digest producers below use the shared constants directly.
const (
	DomainInferReceiptV3      = shared.DomainInferReceiptV3
	DomainVerifierHandraiseV1 = shared.DomainVerifierHandraiseV1
	DomainCommitV1            = shared.DomainCommitV1
	DomainResultV3            = shared.DomainResultV3
)

// SelectedTaskBuildersHash derives the ordered Task Builder membership
// commitment registered as TRUEOPEN_SELECTED_TASK_BUILDERS_V1. The selected
// addresses are one repeated scalar field in the frozen selection order.
func SelectedTaskBuildersHash(chainID string, taskID []byte, builderSetID string, builderSetHash []byte, builders []string) ([]byte, error) {
	if chainID == "" {
		return nil, fmt.Errorf("%w: chain_id is required", ErrInvariantBroken)
	}
	if len(taskID) != Hash32Len {
		return nil, fmt.Errorf("%w: task_id must be 32 bytes", ErrInvalidTaskID)
	}
	if builderSetID == "" {
		return nil, fmt.Errorf("%w: builder_set_id is required", ErrInvariantBroken)
	}
	if len(builderSetHash) != Hash32Len {
		return nil, fmt.Errorf("%w: builder_set_hash must be 32 bytes", ErrInvariantBroken)
	}
	if len(builders) == 0 || uint64(len(builders)) > math.MaxUint32 {
		return nil, fmt.Errorf("%w: selected task builders must be a bounded non-empty list", ErrInvariantBroken)
	}
	elements := make([]shared.CanonicalFieldV1, 0, len(builders))
	for index, builder := range builders {
		operator, err := CanonicalOperatorAddressBytes("selected_task_builder", builder)
		if err != nil {
			return nil, fmt.Errorf("selected_task_builders[%d]: %w", index, err)
		}
		elements = append(elements, shared.RawCanonicalFieldV1(operator))
	}
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainSelectedTaskBuildersV1)).Raw(
		[]byte(chainID), taskID, []byte(builderSetID), builderSetHash,
	).Nested(shared.CanonicalRepeatedFieldsV1(elements)).Sum()
}

// ---- Frozen stage wire digests ----
//
// The pre-freeze infer-receipt signing-bytes helper and its hex receipt-hash wrapper
// (this file, lines 94 and 116 at ff76daa) framed uint64 values as DECIMAL TEXT and
// covered fields - infer_receipt_commit_hash, trace_commit_root,
// checkpoint_commit_root, batch_log_root, token_count, work_unit - that the frozen wire deleted
// from the wire. Both are deleted outright with no alias: an alias would let a
// caller keep producing a digest the frozen wire can never accept.

// Receipt schemas and service messages retain their independently frozen versions.
const (
	InferReceiptSchemaVersionV3      uint32 = 3
	VerifyCommitSchemaVersionV1      uint32 = 1
	ResultReceiptSchemaVersionV3     uint32 = 3
	VerifierHandraiseSchemaVersionV1 uint32 = 1
)

// InferReceiptSigningDigest derives the receipt hash and its signing digest from
// the same frozen projection. Evidence enters through EvidenceCommitmentsHash;
// the final field binds the accepted dispatch attempt. The signature is excluded.
func InferReceiptSigningDigest(receipt InferReceiptV3) ([32]byte, error) {
	chainID, err := canonicalUTF8Field("chain_id", receipt.ChainId)
	if err != nil {
		return [32]byte{}, err
	}
	taskID, err := canonicalHash32("task_id", receipt.TaskId)
	if err != nil {
		return [32]byte{}, err
	}
	taskHash, err := canonicalHash32("task_hash", receipt.TaskHash)
	if err != nil {
		return [32]byte{}, err
	}
	worker, err := CanonicalOperatorAddressBytes("worker_operator_address", receipt.WorkerOperatorAddress)
	if err != nil {
		return [32]byte{}, err
	}
	generationParamsDigest, err := canonicalHash32("generation_params_digest", receipt.GenerationParamsDigest)
	if err != nil {
		return [32]byte{}, err
	}
	outputHash, err := canonicalHash32("output_hash", receipt.OutputHash)
	if err != nil {
		return [32]byte{}, err
	}
	dispatchTicketHash, err := canonicalHash32("dispatch_ticket_hash", receipt.DispatchTicketHash)
	if err != nil {
		return [32]byte{}, err
	}
	evidenceCommitmentsHash, err := EvidenceCommitmentsHash(receipt.RequiredEvidenceCommitments)
	if err != nil {
		return [32]byte{}, err
	}
	return canonicalTaskDigestV1(
		shared.DomainInferReceiptV3,
		shared.Uint32BE(receipt.SchemaVersion),
		chainID,
		taskID,
		taskHash,
		worker,
		shared.Uint64BE(receipt.ServiceAuthorizationNonce),
		generationParamsDigest,
		outputHash,
		shared.Uint64BE(receipt.OutputSizeBytes),
		evidenceCommitmentsHash[:],
		shared.Uint64BE(receipt.ExpiryHeight),
		shared.Uint64BE(receipt.GeneratedTokenCount),
		shared.Uint64BE(receipt.OutputLeafCount),
		receipt.OutputKeyCommitment,
		receipt.WorkerTokenKeyCommitment,
		receipt.WorkerValueKeyCommitment,
		receipt.CiphertextOutputRoot,
		dispatchTicketHash,
	)
}

// VerifyCommitSigningDigest is the verifier commit digest:
//
//	verify_commit_signing_digest =
//	  H_FIELDS_V1("TRUEOPEN_COMMIT_V1",
//	    schema_version, chain_id, task_id, verify_round,
//	    verifier_operator_address, service_authorization_nonce,
//	    commit_hash, expiry_height)
//
// Eight fields; service_signature (wire field 9) is excluded. verify_round is
// uint32_be, never decimal text. commit_hash must be the canonical
// TRUEOPEN_RESULT_COMMITMENT_V1 value: a non-empty placeholder is forbidden, and the
// Hash32 length check here is the structural half of that rule.
func VerifyCommitSigningDigest(commit VerifyCommitV1) ([32]byte, error) {
	chainID, err := canonicalUTF8Field("chain_id", commit.ChainId)
	if err != nil {
		return [32]byte{}, err
	}
	taskID, err := canonicalHash32("task_id", commit.TaskId)
	if err != nil {
		return [32]byte{}, err
	}
	verifier, err := CanonicalOperatorAddressBytes("verifier_operator_address", commit.VerifierOperatorAddress)
	if err != nil {
		return [32]byte{}, err
	}
	commitHash, err := canonicalHash32("commit_hash", commit.CommitHash)
	if err != nil {
		return [32]byte{}, err
	}
	return canonicalTaskDigestV1(
		shared.DomainCommitV1,
		shared.Uint32BE(commit.SchemaVersion),
		chainID,
		taskID,
		shared.Uint32BE(commit.VerifyRound),
		verifier,
		shared.Uint64BE(commit.ServiceAuthorizationNonce),
		commitHash,
		shared.Uint64BE(commit.ExpiryHeight),
	)
}

// VerifierHandraiseSigningDigest uses the canonical shared wire projection.
func VerifierHandraiseSigningDigest(handraise VerifierHandraiseV1) ([32]byte, error) {
	raw, err := proto.Marshal(&handraise)
	if err != nil {
		return [32]byte{}, err
	}
	return dispatch.SigningDigest("task.v1.VerifierHandraiseV1", raw)
}
