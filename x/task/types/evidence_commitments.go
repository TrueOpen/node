package types

import (
	"crypto/sha256"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/cosmos/cosmos-sdk/types/bech32"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// evidenceCommitmentFrameBytesV1 is the exact framed length of one
// EvidenceCommitmentV1 element: u64_be(4)||uint32_be(evidence_kind) +
// u64_be(32)||evidence_hash_or_root + u64_be(8)||uint64_be(encoded_size_bytes).
// It is a derived constant, asserted rather than used as an encoder input, so a
// future field addition fails loudly instead of silently changing the digest.
const evidenceCommitmentFrameBytesV1 = (8 + 4) + (8 + sha256.Size) + (8 + 8)

// maxAddressCodecBytes mirrors the cosmos-sdk address length cap. §1.2 frames the
// address codec bytes, so the only structural bound the derivation can enforce is
// the codec's own.
const maxAddressCodecBytes = 255

// CanonicalEvidenceCommitmentFrameV1 encodes one EvidenceCommitmentV1 as the
// nested FieldFrameV1 that TRUEOPEN_INFER_EVIDENCE_COMMITMENTS_V1 length-frames as a
// single element.
//
// the API contract: "a required nested message recursively encodes its
// field frame in ascending schema field number order" - so the element frame carries
// NO domain prefix and writes the three
// fields of task/v1/evidence.proto in field-number order 1, 2, 3:
//
//	u64_be(4)  || uint32_be(evidence_kind)   // §1.2: enum -> uint32_be
//	u64_be(32) || evidence_hash_or_root      // §1.2: Hash32 -> raw 32 bytes
//	u64_be(8)  || uint64_be(encoded_size_bytes)
//
// The result is always evidenceCommitmentFrameBytesV1 bytes long.
//
// This is deliberately NOT the flattened shape TRUEOPEN_SERVICE_DESCRIPTOR_V1 uses for
// its endpoints (unblock_plan §4b GAP-1): that Hub domain spreads each element over
// four top-level fields, which contradicts the §1.2:96 repeated rule and is an open
// gap. The Task side follows §1.2 literally and must not copy the deviation.
func CanonicalEvidenceCommitmentFrameV1(item EvidenceCommitmentV1) ([]byte, error) {
	frame, err := CanonicalEvidenceCommitmentTypedFrameV1(item)
	if err != nil {
		return nil, err
	}
	return frame.Bytes()
}

func CanonicalEvidenceCommitmentTypedFrameV1(item EvidenceCommitmentV1) (shared.CanonicalFrameV1, error) {
	kind, err := canonicalEvidenceKind(item.EvidenceKind)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	hashOrRoot, err := canonicalHash32("evidence_hash_or_root", item.EvidenceHashOrRoot)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	frame := shared.FlatCanonicalFrameV1(
		shared.EnumBE(kind),
		hashOrRoot,
		shared.Uint64BE(item.EncodedSizeBytes),
	)
	length, err := frame.Len()
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	if length != evidenceCommitmentFrameBytesV1 {
		return shared.CanonicalFrameV1{}, fmt.Errorf(
			"evidence commitment frame must be exactly %d bytes, got %d",
			evidenceCommitmentFrameBytesV1, length,
		)
	}
	return frame, nil
}

// EvidenceCommitmentsHash derives InferReceiptV2.evidence_commitments_hash, the
// tenth field of the TRUEOPEN_INFER_RECEIPT_V2 preimage. It is Keeper-derived and is
// never a caller-submitted wire field (the API contract).
//
//	evidence_commitments_hash =
//	  H_FIELDS_V1("TRUEOPEN_INFER_EVIDENCE_COMMITMENTS_V1",
//	    uint32_be(count), REPEATED_V1(commitment[0], ... commitment[count-1]))
//
// the API contract registers the domain and points at §5.14 lines
// 1335-1338 as the one place the ordered preimage may live; the byte-exact
// expansion lives in proto/task/v1/infer_receipt.proto, which is the single
// in-repository copy. The preimage carries NO chain_id and NO task_id: it is a pure
// content commitment and the outer TRUEOPEN_INFER_RECEIPT_V2 digest already binds
// chain_id, task_id, task_hash and the worker operator.
//
// Ordering is not the caller's choice. §5.14 and task/v1/evidence.proto freeze
// the list as strictly ascending by evidence_kind with unique kinds, and a reordered
// or duplicated list is rejected here rather than silently re-sorted.
//
// len(items) == 0 stays fully defined: it writes the outer uint32_be(0) and one
// nested REPEATED_V1 frame containing its derived uint32_be(0) count. It must
// NEVER be replaced by 32 zero bytes, by an empty byte string, or by skipping
// either position. §1.2 normalises a nil and an empty required list to the same
// empty value, so a nil slice and an empty slice give the same digest.
//
// Two admission checks stay with the handler because they need state the derivation
// cannot see: count <= max_infer_evidence_commitments_per_receipt, and the kind set
// being exactly the set the locked profile requires (CONTRACT-GAP: no contract
// section states whether a profile may require the empty set, so no minimum is
// enforced here).
func EvidenceCommitmentsHash(items []EvidenceCommitmentV1) ([32]byte, error) {
	if uint64(len(items)) > uint64(math.MaxUint32) {
		return [32]byte{}, fmt.Errorf("evidence commitment count %d overflows uint32", len(items))
	}
	commitmentFrames := make([]shared.CanonicalFrameV1, len(items))
	for index, item := range items {
		if index > 0 && items[index-1].EvidenceKind >= item.EvidenceKind {
			return [32]byte{}, fmt.Errorf(
				"required_evidence_commitments must be strictly ascending by evidence_kind with unique kinds: "+
					"element %d has kind %d after kind %d",
				index, item.EvidenceKind, items[index-1].EvidenceKind,
			)
		}
		frame, err := CanonicalEvidenceCommitmentTypedFrameV1(item)
		if err != nil {
			return [32]byte{}, fmt.Errorf("required_evidence_commitments[%d]: %w", index, err)
		}
		commitmentFrames[index] = frame
	}
	repeatedCommitments := shared.CanonicalRepeatedFramesV1(commitmentFrames)
	if err := repeatedCommitments.Err(); err != nil {
		return [32]byte{}, fmt.Errorf("required_evidence_commitments: %w", err)
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainInferEvidenceCommitmentsV1)).Raw(
		shared.Uint32BE(uint32(len(items))),
	).Nested(repeatedCommitments).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

// canonicalTaskDigestV1 resolves the domain through the shared §1.4 registry and
// hashes the ordered fields with the single typed H_FIELDS_V1 builder. Resolving
// through MustDomain rather than a bare literal is what makes an unregistered or
// misspelled domain panic instead of silently producing a consensus value (§1.4
// rule 1).
//
// Every field reaching this helper is already a depth-zero encoded value, so Raw
// is the correct constructor: the domains it serves have no nested submessage
// field. A digest that does frame a submessage — the two handraises above — uses
// the builder directly with Nested so the child's depth is not erased.
//
// The error is not swallowed into a zero digest. §6's limits are the only way the
// builder can fail here, and a caller that treated a limit breach as "all-zero
// digest" would put a colliding consensus value on chain.
func canonicalTaskDigestV1(domainKey string, fields ...[]byte) ([32]byte, error) {
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(domainKey)).Raw(fields...).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

// canonicalEvidenceKind rejects the unspecified and unknown enum values §1.2
// requires ("unknown oneof/enum ... is rejected outright") and
// task/v1/evidence.proto marks
// as never written.
func canonicalEvidenceKind(kind shared.EvidenceKind) (uint32, error) {
	switch kind {
	case shared.EvidenceKind_EVIDENCE_KIND_WORKER_VALUE_OPENING,
		shared.EvidenceKind_EVIDENCE_KIND_VERIFIER_VALUE_OPENING,
		shared.EvidenceKind_EVIDENCE_KIND_SETTLEMENT_ROOT_OPENING:
		return uint32(kind), nil
	case shared.EvidenceKind_EVIDENCE_KIND_UNSPECIFIED:
		return 0, fmt.Errorf("evidence_kind must not be EVIDENCE_KIND_UNSPECIFIED")
	default:
		return 0, fmt.Errorf("evidence_kind %d is not a registered EvidenceKind value", int32(kind))
	}
}

// canonicalHash32 enforces the §1.1/§1.4 rule 3 Hash32 shape: inside a consensus
// preimage a Hash32 is raw 32 bytes, never hex text and never a shorter placeholder.
func canonicalHash32(field string, value []byte) ([]byte, error) {
	if len(value) != sha256.Size {
		return nil, fmt.Errorf("%s must be exactly %d raw bytes, got %d", field, sha256.Size, len(value))
	}
	return value, nil
}

// canonicalUTF8Field applies the §1.2 string rule: validate strict UTF-8, then take
// the bytes. No trimming, no case folding and no normalisation happens here - the
// bytes the caller signed are the bytes that are framed.
func canonicalUTF8Field(field, value string) ([]byte, error) {
	if !utf8.ValidString(value) {
		return nil, fmt.Errorf("%s must be strict UTF-8", field)
	}
	return []byte(value), nil
}

// CanonicalOperatorAddressBytes converts a Bech32 operator address into the address
// codec bytes every §1.2 preimage frames (Ruling 24). The Bech32 text is
// NEVER framed: the human-readable prefix is presentation, so two chains that share
// a prefix are separated by the chain_id field instead.
//
// The Keeper additionally checks the address against its own configured codec
// (x/task/keeper canonicalAddress) and against the authoritative assignment;
// this function is the pure, prefix-agnostic decode a non-Go consumer must
// reproduce.
func CanonicalOperatorAddressBytes(field, value string) ([]byte, error) {
	if value == "" {
		return nil, fmt.Errorf("%s must be a non-empty canonical Bech32 address", field)
	}
	if strings.TrimSpace(value) != value {
		return nil, fmt.Errorf("%s must not carry leading or trailing whitespace", field)
	}
	hrp, raw, err := bech32.DecodeAndConvert(value)
	if err != nil {
		return nil, fmt.Errorf("%s is not a decodable Bech32 address: %w", field, err)
	}
	if len(raw) == 0 || len(raw) > maxAddressCodecBytes {
		return nil, fmt.Errorf("%s decodes to %d address bytes, outside 1..%d", field, len(raw), maxAddressCodecBytes)
	}
	reencoded, err := bech32.ConvertAndEncode(hrp, raw)
	if err != nil {
		return nil, fmt.Errorf("%s could not be re-encoded: %w", field, err)
	}
	if reencoded != value {
		return nil, fmt.Errorf("%s must be the canonical Bech32 encoding of its address bytes", field)
	}
	return raw, nil
}
