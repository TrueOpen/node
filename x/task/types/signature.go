package types

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// These aliases keep the public Task types surface tied to the shared registry.
// Digest producers below use the shared constants directly.
const (
	DomainInferReceiptV2      = shared.DomainInferReceiptV2
	DomainWorkerHandraiseV1   = shared.DomainWorkerHandraiseV1
	DomainVerifierHandraiseV1 = shared.DomainVerifierHandraiseV1
	DomainCommitV1            = shared.DomainCommitV1
	DomainResultV2            = shared.DomainResultV2
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

// ---- Frozen stage wire digests (the API contract) ----
//
// The pre-freeze infer-receipt signing-bytes helper and its hex receipt-hash wrapper
// (this file, lines 94 and 116 at ff76daa) framed uint64 values as DECIMAL TEXT and
// covered fields - infer_receipt_commit_hash, trace_commit_root,
// checkpoint_commit_root, batch_log_root, token_count, work_unit - that §5.14 deleted
// from the wire. Both are deleted outright with no alias: an alias would let a
// caller keep producing a digest the frozen wire can never accept.

// InferReceiptV2 is the only stage wire with schema_version 2. Handraises and
// VerifyCommit remain at 1, while ResultReceipt remains at 2. Digest derivation
// stays total; handlers enforce the exact version before admitting a message.
const (
	InferReceiptSchemaVersionV2      uint32 = 2
	VerifyCommitSchemaVersionV1      uint32 = 1
	ResultReceiptSchemaVersionV2     uint32 = 2
	WorkerHandraiseSchemaVersionV1   uint32 = 1
	VerifierHandraiseSchemaVersionV1 uint32 = 1
)

// InferReceiptSigningDigest is the one ordered preimage of both
// infer_receipt_signing_digest and infer_receipt_hash - §5.14 lines 1345-1351 write
// them as a single equality, so a second "business hash" must never be derived:
//
//	infer_receipt_hash = infer_receipt_signing_digest =
//	  H_FIELDS_V1("TRUEOPEN_INFER_RECEIPT_V2",
//	    schema_version, chain_id, task_id, task_hash,
//	    worker_operator_address, service_authorization_nonce,
//	    generation_params_digest, output_hash, output_size_bytes,
//	    evidence_commitments_hash, expiry_height, generated_token_count,
//	    output_leaf_count)
//
// Thirteen fields; service_signature (wire field 12) is excluded. The tenth field is
// NOT a wire field: required_evidence_commitments (wire field 10) enters only through
// the Keeper-derived EvidenceCommitmentsHash, so the signature still covers the whole
// typed list. worker_operator_address is framed as address codec bytes, not Bech32
// text (Ruling 24).
func InferReceiptSigningDigest(receipt InferReceiptV2) ([32]byte, error) {
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
	evidenceCommitmentsHash, err := EvidenceCommitmentsHash(receipt.RequiredEvidenceCommitments)
	if err != nil {
		return [32]byte{}, err
	}
	return canonicalTaskDigestV1(
		shared.DomainInferReceiptV2,
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
	)
}

// VerifyCommitSigningDigest is the §5.14 lines 1353-1357 verifier commit digest:
//
//	verify_commit_signing_digest =
//	  H_FIELDS_V1("TRUEOPEN_COMMIT_V1",
//	    schema_version, chain_id, task_id, verify_round,
//	    verifier_operator_address, service_authorization_nonce,
//	    commit_hash, expiry_height)
//
// Eight fields; service_signature (wire field 9) is excluded. verify_round is
// uint32_be, never decimal text. commit_hash must be the canonical
// TRUEOPEN_RESULT_COMMITMENT_V1 value: §5.14 forbids a "non-empty placeholder", and the
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

// WorkerHandraiseSigningDigest is
// H_FIELDS_V1("TRUEOPEN_WORKER_HANDRAISE_V1", canonical WorkerHandraiseV1 excluding
// service_signature) with the ten fields in the §4.1 lines 531-541 order:
//
//	schema_version, chain_id, task_id, task_hash, model_id, profile_version,
//	member, duty, service_authorization_nonce, expiry_height
//
// member is a required nested CandidateMemberRefV1 and is therefore framed
// recursively per §1.2, and duty is framed as uint32_be per the §1.2 enum rule.
// §4.1 pins duty = WORKER for this wire; that equality is an admission check and is
// left to the handler so the derivation stays total, exactly as for schema_version.
func WorkerHandraiseSigningDigest(handraise WorkerHandraiseV1) ([32]byte, error) {
	chainID, err := canonicalUTF8Field("chain_id", handraise.ChainId)
	if err != nil {
		return [32]byte{}, err
	}
	taskID, err := canonicalHash32("task_id", handraise.TaskId)
	if err != nil {
		return [32]byte{}, err
	}
	taskHash, err := canonicalHash32("task_hash", handraise.TaskHash)
	if err != nil {
		return [32]byte{}, err
	}
	modelID, err := canonicalUTF8Field("model_id", handraise.ModelId)
	if err != nil {
		return [32]byte{}, err
	}
	member, err := CanonicalCandidateMemberRefTypedFrameV1(handraise.Member)
	if err != nil {
		return [32]byte{}, err
	}
	duty, err := canonicalDuty(handraise.Duty)
	if err != nil {
		return [32]byte{}, err
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainWorkerHandraiseV1)).Raw(
		shared.Uint32BE(handraise.SchemaVersion),
		chainID,
		taskID,
		taskHash,
		modelID,
		shared.Uint32BE(handraise.ProfileVersion),
	).Nested(member).Raw(
		shared.EnumBE(duty),
		shared.Uint64BE(handraise.ServiceAuthorizationNonce),
		shared.Uint64BE(handraise.ExpiryHeight),
	).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

// VerifierHandraiseSigningDigest is
// H_FIELDS_V1("TRUEOPEN_VERIFIER_HANDRAISE_V1", canonical VerifierHandraiseV1 excluding
// service_signature) with the twelve fields in the §4.1 lines 543-556 order:
//
//	schema_version, chain_id, task_id, verify_round, infer_receipt_hash, output_hash,
//	model_id, profile_version, member, duty, service_authorization_nonce,
//	expiry_height
//
// Same framing rules as WorkerHandraiseSigningDigest; §4.1 pins duty = VERIFIER for
// this wire.
func VerifierHandraiseSigningDigest(handraise VerifierHandraiseV1) ([32]byte, error) {
	chainID, err := canonicalUTF8Field("chain_id", handraise.ChainId)
	if err != nil {
		return [32]byte{}, err
	}
	taskID, err := canonicalHash32("task_id", handraise.TaskId)
	if err != nil {
		return [32]byte{}, err
	}
	inferReceiptHash, err := canonicalHash32("infer_receipt_hash", handraise.InferReceiptHash)
	if err != nil {
		return [32]byte{}, err
	}
	outputHash, err := canonicalHash32("output_hash", handraise.OutputHash)
	if err != nil {
		return [32]byte{}, err
	}
	modelID, err := canonicalUTF8Field("model_id", handraise.ModelId)
	if err != nil {
		return [32]byte{}, err
	}
	member, err := CanonicalCandidateMemberRefTypedFrameV1(handraise.Member)
	if err != nil {
		return [32]byte{}, err
	}
	duty, err := canonicalDuty(handraise.Duty)
	if err != nil {
		return [32]byte{}, err
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainVerifierHandraiseV1)).Raw(
		shared.Uint32BE(handraise.SchemaVersion),
		chainID,
		taskID,
		shared.Uint32BE(handraise.VerifyRound),
		inferReceiptHash,
		outputHash,
		modelID,
		shared.Uint32BE(handraise.ProfileVersion),
	).Nested(member).Raw(
		shared.EnumBE(duty),
		shared.Uint64BE(handraise.ServiceAuthorizationNonce),
		shared.Uint64BE(handraise.ExpiryHeight),
	).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

// CanonicalCandidateMemberRefFrameV1 encodes the required nested
// CandidateMemberRefV1 of §4.1 as a FieldFrameV1 with NO domain prefix and its four
// fields in ascending schema field-number order (§1.2):
//
//	u64_be(32) || candidate_pool_snapshot_id
//	u64_be(4)  || uint32_be(slot)
//	u64_be(8)  || uint64_be(slot_version)
//	u64_be(n)  || operator_address codec bytes
//
// The handraise carries candidate_pool_snapshot_id only; §4.1 forbids an asserted
// pool-hash copy because the snapshot ID already commits to it.
func CanonicalCandidateMemberRefFrameV1(member CandidateMemberRefV1) ([]byte, error) {
	frame, err := CanonicalCandidateMemberRefTypedFrameV1(member)
	if err != nil {
		return nil, err
	}
	return frame.Bytes()
}

func CanonicalCandidateMemberRefTypedFrameV1(member CandidateMemberRefV1) (shared.CanonicalFrameV1, error) {
	snapshotID, err := canonicalHash32("member.candidate_pool_snapshot_id", member.CandidatePoolSnapshotId)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	operator, err := CanonicalOperatorAddressBytes("member.operator_address", member.OperatorAddress)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	return shared.FlatCanonicalFrameV1(
		snapshotID,
		shared.Uint32BE(member.Slot),
		shared.Uint64BE(member.SlotVersion),
		operator,
	), nil
}

// canonicalDuty rejects the unspecified and unknown Duty values §1.2 requires to be
// rejected. It does not enforce which of the two live duties belongs to which wire;
// see WorkerHandraiseSigningDigest.
func canonicalDuty(duty shared.Duty) (uint32, error) {
	switch duty {
	case shared.Duty_DUTY_WORKER, shared.Duty_DUTY_VERIFIER:
		return uint32(duty), nil
	case shared.Duty_DUTY_UNSPECIFIED:
		return 0, fmt.Errorf("duty must not be DUTY_UNSPECIFIED")
	default:
		return 0, fmt.Errorf("duty %d is not a registered Duty value", int32(duty))
	}
}

// IsCanonicalSHA256Hex reports whether value is a lowercase, 32-byte SHA-256 digest.
func IsCanonicalSHA256Hex(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == sha256.Size && hex.EncodeToString(raw) == value
}
