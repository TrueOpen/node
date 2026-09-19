package types_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// framingFixturePath holds the language-independent golden vectors for
// monorepo@f9b7c18 canonical_encoding_and_domain_hashing.md sections 3-10. The file carries only
// inputs and expected hex outputs so a non-Go implementation can be validated
// against the same vectors.
//
// Regenerating: run the package tests with TRUEOPEN_REGEN_FIXTURES=1. The run
// rewrites the expected hex fields and then fails on purpose, because a
// regenerated consensus fixture must be diffed against the frozen contract
// before it is committed.
const framingFixturePath = "testdata/framing_v1.json"

const fixtureRegenEnv = "TRUEOPEN_REGEN_FIXTURES"

// ---- language-independent typed value encoding ----

type fixtureValue struct {
	Type   string         `json:"type"`
	Hex    string         `json:"hex,omitempty"`
	UTF8   *string        `json:"utf8,omitempty"`
	Value  *uint64        `json:"value,omitempty"`
	Bool   *bool          `json:"bool,omitempty"`
	Count  uint64         `json:"count,omitempty"`
	Fields []fixtureValue `json:"fields,omitempty"`
}

func (v fixtureValue) encode(t *testing.T) []byte {
	t.Helper()
	switch v.Type {
	case "bytes":
		raw, err := hex.DecodeString(v.Hex)
		require.NoError(t, err, "bytes value must be lowercase hex")
		require.Equal(t, v.Hex, hex.EncodeToString(raw), "bytes value must be canonical lowercase hex")
		return raw
	case "string":
		require.NotNil(t, v.UTF8, "string value requires utf8")
		return []byte(*v.UTF8)
	case "uint32":
		require.NotNil(t, v.Value, "uint32 value requires value")
		require.LessOrEqual(t, *v.Value, uint64(^uint32(0)))
		return shared.Uint32BE(uint32(*v.Value))
	case "enum":
		require.NotNil(t, v.Value, "enum value requires value")
		require.LessOrEqual(t, *v.Value, uint64(^uint32(0)))
		return shared.EnumBE(uint32(*v.Value))
	case "uint64":
		require.NotNil(t, v.Value, "uint64 value requires value")
		return shared.Uint64BE(*v.Value)
	case "bool":
		require.NotNil(t, v.Bool, "bool value requires bool")
		return shared.BoolByte(*v.Bool)
	case "repeat":
		unit, err := hex.DecodeString(v.Hex)
		require.NoError(t, err)
		require.NotEmpty(t, unit, "repeat value requires a non-empty unit")
		return bytes.Repeat(unit, int(v.Count))
	case "frame":
		inner := make([][]byte, 0, len(v.Fields))
		for _, field := range v.Fields {
			inner = append(inner, field.encode(t))
		}
		return shared.CanonicalFrameBytes(inner...)
	default:
		t.Fatalf("unknown fixture value type %q", v.Type)
		return nil
	}
}

func encodeFixtureFields(t *testing.T, values []fixtureValue) [][]byte {
	t.Helper()
	encoded := make([][]byte, 0, len(values))
	for _, value := range values {
		encoded = append(encoded, value.encode(t))
	}
	return encoded
}

// ---- fixture schema ----

type framingFixture struct {
	Schema             string                  `json:"schema"`
	Source             string                  `json:"source"`
	Notes              []string                `json:"notes"`
	MerkleFraming      merkleFramingConstants  `json:"merkle_framing"`
	HV1                []payloadFrameVector    `json:"h_v1"`
	HFieldsV1          []fieldFrameVector      `json:"h_fields_v1"`
	RepeatedV1         []repeatedVector        `json:"repeated_v1"`
	OptionalV1         []optionalVector        `json:"optional_v1"`
	OneofV1            []oneofVector           `json:"oneof_v1"`
	MerkleRootV1       []merkleVector          `json:"merkle_root_v1"`
	MerkleStructural   []merkleStructuralCheck `json:"merkle_structural_checks"`
	MerkleRejects      []merkleReject          `json:"merkle_rejects"`
	SignatureV1        []signatureVector       `json:"signature_v1"`
	SignatureDirect    []directDigestVector    `json:"signature_direct_digest_v1"`
	SignatureRejects   []signatureReject       `json:"signature_rejects"`
	DistinctHashGroups []distinctHashGroup     `json:"distinct_hash_groups"`
}

type merkleFramingConstants struct {
	LeafPrefix  string `json:"leaf_prefix"`
	NodePrefix  string `json:"node_prefix"`
	EmptyPrefix string `json:"empty_prefix"`
}

type payloadFrameVector struct {
	Name         string       `json:"name"`
	Domain       string       `json:"domain"`
	Payload      fixtureValue `json:"payload"`
	IncludeFrame bool         `json:"include_frame"`
	FrameHex     string       `json:"frame_hex,omitempty"`
	HashHex      string       `json:"hash_hex"`
}

type fieldFrameVector struct {
	Name         string         `json:"name"`
	Domain       string         `json:"domain"`
	Fields       []fixtureValue `json:"fields"`
	IncludeFrame bool           `json:"include_frame"`
	FrameHex     string         `json:"frame_hex,omitempty"`
	HashHex      string         `json:"hash_hex"`
}

// optionalVector is the canonical_encoding_and_domain_hashing.md §10.3 optional
// layout: absent is the single byte
// 00, present is 01 || FRAME_V1(ENC(value)) where FRAME_V1(x) = u64_be(len(x)) || x.
//
// Presence is an explicit boolean rather than "the value key is missing" on
// purpose: §10.3 needs a present optional carrying an empty value to stay
// expressible and to stay a different preimage from an absent one, and a fixture
// that inferred presence from the absence of a key could not write that case
// down at all.
type optionalVector struct {
	Name     string        `json:"name"`
	Present  bool          `json:"present"`
	Value    *fixtureValue `json:"value,omitempty"`
	FrameHex string        `json:"frame_hex"`
}

type repeatedVector struct {
	Name     string         `json:"name"`
	Elements []fixtureValue `json:"elements"`
	FrameHex string         `json:"frame_hex"`
}

type oneofVector struct {
	Name                string         `json:"name"`
	SelectedFieldNumber uint32         `json:"selected_field_number"`
	PayloadFields       []fixtureValue `json:"payload_fields"`
	FrameHex            string         `json:"frame_hex"`
}

type merkleVector struct {
	Name      string   `json:"name"`
	Domain    string   `json:"domain"`
	LeavesHex []string `json:"leaves_hex"`
	RootHex   string   `json:"root_hex"`
}

type merkleStructuralCheck struct {
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	Vector         string `json:"vector"`
	LeftVector     string `json:"left_vector,omitempty"`
	RightLeafIndex int    `json:"right_leaf_index,omitempty"`
	LeafIndex      int    `json:"leaf_index,omitempty"`
}

type merkleReject struct {
	Name      string   `json:"name"`
	Domain    string   `json:"domain"`
	LeavesHex []string `json:"leaves_hex"`
	Reason    string   `json:"reason"`
}

type signatureVector struct {
	Name               string       `json:"name"`
	PrivateKeyHex      string       `json:"private_key_hex"`
	Message            fixtureValue `json:"message"`
	PubkeyHex          string       `json:"pubkey_hex"`
	SignatureHex       string       `json:"signature_hex"`
	SignatureDigestHex string       `json:"signature_digest_hex"`
}

// directDigestVector is the canonical_encoding_and_domain_hashing.md §10.5
// direct-digest form: the signer receives
// an already-derived 32-byte SIGN_DIGEST and signs it as-is. It is a section of
// its own rather than a flag on signatureVector because the two forms have
// different verifiers - a message signature is verified by a routine that hashes
// once, a digest signature by one that does not - and mixing them would let a
// vector be validated by the wrong side of that boundary.
//
// RehashedDigestHex is the counterexample the vector exists for: it is
// SHA256(DigestHex), i.e. what Cosmos SDK's PubKey.VerifySignature computes
// internally, and the same signature must NOT verify against it.
type directDigestVector struct {
	Name                 string       `json:"name"`
	PrivateKeyHex        string       `json:"private_key_hex"`
	DigestMessage        fixtureValue `json:"digest_message"`
	DigestHex            string       `json:"digest_hex"`
	PubkeyHex            string       `json:"pubkey_hex"`
	SignatureHex         string       `json:"signature_hex"`
	SignatureDigestHex   string       `json:"signature_digest_hex"`
	RehashedDigestHex    string       `json:"rehashed_digest_hex"`
	RehashedRejectReason string       `json:"rehashed_reject_reason"`
}

type signatureReject struct {
	Name     string `json:"name"`
	From     string `json:"from"`
	Mutation string `json:"mutation"`
	Reason   string `json:"reason"`
}

type distinctHashGroup struct {
	Name string            `json:"name"`
	Refs []distinctHashRef `json:"refs"`
}

type distinctHashRef struct {
	Section string `json:"section"`
	Vector  string `json:"vector"`
}

// ---- merkle framing recomputed from the fixture constants ----

func merkleDomainFrameFromFixture(domain string) []byte {
	framed := make([]byte, 4+len(domain))
	copy(framed[:4], shared.Uint32BE(uint32(len(domain))))
	copy(framed[4:], domain)
	return framed
}

func optionalFrameFromFixture(t *testing.T, vector optionalVector) []byte {
	t.Helper()
	if !vector.Present {
		require.Nil(t, vector.Value, "an absent optional must not carry a value")
		return shared.OptionalAbsentFrameV1()
	}
	require.NotNil(t, vector.Value, "a present optional requires a value")
	return shared.OptionalPresentFrameV1(vector.Value.encode(t))
}

func repeatedFrameFromFixture(t *testing.T, vector repeatedVector) []byte {
	t.Helper()
	encoded, err := shared.RepeatedFrameV1(encodeFixtureFields(t, vector.Elements)...)
	require.NoError(t, err)
	return encoded
}

func oneofFrameFromFixture(t *testing.T, vector oneofVector) []byte {
	t.Helper()
	encoded, err := shared.OneofFrameV1(vector.SelectedFieldNumber, encodeFixtureFields(t, vector.PayloadFields)...)
	require.NoError(t, err)
	return encoded
}

func merkleHashFromFixture(parts ...[]byte) []byte {
	digest := sha256.New()
	for _, part := range parts {
		_, _ = digest.Write(part)
	}
	return digest.Sum(nil)
}

// ---- tests ----

func TestFramingFixtureMatchesFrozenFramings(t *testing.T) {
	fixture := loadFramingFixture(t)

	require.Equal(t, "TRUEOPEN_MERKLE_LEAF_V1", fixture.MerkleFraming.LeafPrefix)
	require.Equal(t, "TRUEOPEN_MERKLE_NODE_V1", fixture.MerkleFraming.NodePrefix)
	require.Equal(t, "TRUEOPEN_MERKLE_EMPTY_V1", fixture.MerkleFraming.EmptyPrefix)

	require.NotEmpty(t, fixture.HV1)
	require.NotEmpty(t, fixture.HFieldsV1)
	require.NotEmpty(t, fixture.MerkleRootV1)
	require.NotEmpty(t, fixture.SignatureV1)

	for _, vector := range fixture.HV1 {
		t.Run("h_v1/"+vector.Name, func(t *testing.T) {
			payload := vector.Payload.encode(t)
			frame, err := shared.PayloadFrameV1(vector.Domain, payload)
			require.NoError(t, err)
			if vector.IncludeFrame {
				require.Equal(t, vector.FrameHex, hex.EncodeToString(frame))
			}
			hash, err := shared.PayloadHashV1(vector.Domain, payload)
			require.NoError(t, err)
			require.Equal(t, vector.HashHex, hex.EncodeToString(hash))
			sum := sha256.Sum256(frame)
			require.Equal(t, sum[:], hash, "H_V1 must be SHA256 of PayloadFrameV1")
		})
	}

	for _, vector := range fixture.HFieldsV1 {
		t.Run("h_fields_v1/"+vector.Name, func(t *testing.T) {
			fields := encodeFixtureFields(t, vector.Fields)
			framed := shared.CanonicalFrameBytes(append([][]byte{[]byte(vector.Domain)}, fields...)...)
			if vector.IncludeFrame {
				require.Equal(t, vector.FrameHex, hex.EncodeToString(framed))
			}
			hash := shared.CanonicalHashBytes(vector.Domain, fields...)
			require.Equal(t, vector.HashHex, hex.EncodeToString(hash))
			sum := sha256.Sum256(framed)
			require.Equal(t, sum[:], hash, "H_FIELDS_V1 must be SHA256 of FieldFrameV1")
		})
	}

	for _, vector := range fixture.MerkleRootV1 {
		t.Run("merkle_root_v1/"+vector.Name, func(t *testing.T) {
			root, err := shared.MerkleRootV1(vector.Domain, decodeHexList(t, vector.LeavesHex))
			require.NoError(t, err)
			require.Equal(t, vector.RootHex, hex.EncodeToString(root))
			require.NotEqual(t, strings.Repeat("00", sha256.Size), vector.RootHex,
				"no frozen root may be the 32-byte zero value")
		})
	}
}

// TestFramingFixtureOptionalV1 pins the canonical_encoding_and_domain_hashing.md
// §10.3 optional primitive as
// published bytes, so a non-Go implementation can be checked against the same two
// vectors. The frames are raw layout, not hashes: OPTIONAL_V1 is a value encoding
// that gets fed into H_FIELDS_V1 by its callers, never hashed on its own.
func TestFramingFixtureOptionalV1(t *testing.T) {
	fixture := loadFramingFixture(t)
	require.NotEmpty(t, fixture.OptionalV1)

	for _, vector := range fixture.OptionalV1 {
		t.Run("optional_v1/"+vector.Name, func(t *testing.T) {
			require.Equal(t, vector.FrameHex, hex.EncodeToString(optionalFrameFromFixture(t, vector)))
		})
	}
}

func TestFramingFixtureRepeatedAndOneofV1(t *testing.T) {
	fixture := loadFramingFixture(t)
	require.NotEmpty(t, fixture.RepeatedV1)
	require.NotEmpty(t, fixture.OneofV1)

	for _, vector := range fixture.RepeatedV1 {
		t.Run("repeated_v1/"+vector.Name, func(t *testing.T) {
			require.Equal(t, vector.FrameHex, hex.EncodeToString(repeatedFrameFromFixture(t, vector)))
		})
	}
	for _, vector := range fixture.OneofV1 {
		t.Run("oneof_v1/"+vector.Name, func(t *testing.T) {
			require.Equal(t, vector.FrameHex, hex.EncodeToString(oneofFrameFromFixture(t, vector)))
		})
	}
}

func TestFramingFixtureMerkleStructuralRules(t *testing.T) {
	fixture := loadFramingFixture(t)
	byName := make(map[string]merkleVector, len(fixture.MerkleRootV1))
	for _, vector := range fixture.MerkleRootV1 {
		byName[vector.Name] = vector
	}
	require.NotEmpty(t, fixture.MerkleStructural)

	for _, check := range fixture.MerkleStructural {
		t.Run(check.Name, func(t *testing.T) {
			vector, ok := byName[check.Vector]
			require.True(t, ok, "unknown merkle vector %q", check.Vector)
			domainFrame := merkleDomainFrameFromFixture(vector.Domain)
			leaves := decodeHexList(t, vector.LeavesHex)
			root, err := shared.MerkleRootV1(vector.Domain, leaves)
			require.NoError(t, err)

			switch check.Kind {
			case "empty_root_is_empty_prefix_hash":
				require.Empty(t, leaves)
				want := merkleHashFromFixture([]byte(fixture.MerkleFraming.EmptyPrefix), domainFrame)
				require.Equal(t, want, root)
			case "root_equals_leaf_hash":
				require.Len(t, leaves, 1)
				want := merkleHashFromFixture(
					[]byte(fixture.MerkleFraming.LeafPrefix), domainFrame, leaves[check.LeafIndex],
				)
				require.Equal(t, want, root, "a one-leaf tree root is the leaf hash, not a node hash")
			case "node_of_root_and_leaf":
				// This is the odd-node uplift rule: the trailing leaf hash is
				// promoted unchanged instead of being duplicated or zero-padded.
				left, ok := byName[check.LeftVector]
				require.True(t, ok, "unknown left merkle vector %q", check.LeftVector)
				require.Equal(t, vector.Domain, left.Domain)
				leftRoot, err := shared.MerkleRootV1(left.Domain, decodeHexList(t, left.LeavesHex))
				require.NoError(t, err)
				upliftedLeaf := merkleHashFromFixture(
					[]byte(fixture.MerkleFraming.LeafPrefix), domainFrame, leaves[check.RightLeafIndex],
				)
				want := merkleHashFromFixture(
					[]byte(fixture.MerkleFraming.NodePrefix), domainFrame, leftRoot, upliftedLeaf,
				)
				require.Equal(t, want, root)
			default:
				t.Fatalf("unknown merkle structural check kind %q", check.Kind)
			}
		})
	}

	for _, reject := range fixture.MerkleRejects {
		t.Run("reject/"+reject.Name, func(t *testing.T) {
			_, err := shared.MerkleRootV1(reject.Domain, decodeHexList(t, reject.LeavesHex))
			require.Error(t, err, reject.Reason)
		})
	}
}

func TestFramingFixtureSignatureRoundTripAndRejects(t *testing.T) {
	fixture := loadFramingFixture(t)
	signatures := make(map[string]signatureVector, len(fixture.SignatureV1))

	for _, vector := range fixture.SignatureV1 {
		signatures[vector.Name] = vector
		t.Run("signature/"+vector.Name, func(t *testing.T) {
			privateKey := privateKeyFromFixture(t, vector.PrivateKeyHex)
			require.Equal(t, vector.PubkeyHex, hex.EncodeToString(privateKey.PubKey().Bytes()))

			message := vector.Message.encode(t)
			require.Equal(t, vector.SignatureHex, mustSignFixture(t, privateKey, message),
				"RFC6979 deterministic signing must reproduce the frozen 64-byte R||S")

			raw, err := shared.DecodeCanonicalSecp256k1SignatureHex(vector.SignatureHex)
			require.NoError(t, err)
			require.Len(t, raw, shared.CompactSecp256k1SignatureBytes)
			require.True(t, privateKey.PubKey().VerifySignature(message, raw))
			digest := sha256.Sum256(raw)
			require.Equal(t, vector.SignatureDigestHex, hex.EncodeToString(digest[:]),
				"signature_digest = SHA256(raw64), never SHA256 of the hex text")

			textDigest := sha256.Sum256([]byte(vector.SignatureHex))
			require.NotEqual(t, vector.SignatureDigestHex, hex.EncodeToString(textDigest[:]))

			gotDigest, err := shared.CanonicalSignatureDigest(raw)
			require.NoError(t, err)
			require.Equal(t, vector.SignatureDigestHex, hex.EncodeToString(gotDigest[:]))
		})
	}

	require.NotEmpty(t, fixture.SignatureRejects)
	for _, reject := range fixture.SignatureRejects {
		t.Run("signature_reject/"+reject.Name, func(t *testing.T) {
			base, ok := signatures[reject.From]
			require.True(t, ok, "unknown signature vector %q", reject.From)
			mutated := mutateFixtureSignature(t, base.SignatureHex, reject.Mutation)
			require.NotEqual(t, base.SignatureHex, mutated)

			// Every mutation is caught at the canonical boundary, so a rejected
			// signature never reaches ECDSA verification at all.
			_, err := shared.DecodeCanonicalSecp256k1SignatureHex(mutated)
			require.Error(t, err, reject.Reason)

			// Mutations that are non-canonical as bytes (zero-R, zero-S, high-S,
			// wrong length) must also be rejected by the byte-oriented verifier,
			// not only by the text boundary. Purely textual mutations such as
			// uppercase hex decode to the same valid bytes and are excluded.
			rawMutated, hexErr := hex.DecodeString(mutated)
			if hexErr == nil && shared.RequireCanonicalSecp256k1Signature(rawMutated) != nil {
				pubKey := privateKeyFromFixture(t, base.PrivateKeyHex).PubKey()
				digest := sha256.Sum256(base.Message.encode(t))
				require.Error(t, shared.VerifyStrictSecp256k1Digest(pubKey, digest[:], rawMutated), reject.Reason)
			}
		})
	}
}

// TestFramingFixtureDirectDigestSignature pins
// canonical_encoding_and_domain_hashing.md §10.5: a SIGN_DIGEST is
// signed and verified as-is.
//
// The negative half is the reason the vector exists. The task specification §10.3
// makes
// "verifying against SHA256(digest) fails" a precondition for adoption, because
// the generic Cosmos SDK entry point PubKey.VerifySignature hashes whatever it is
// given before verifying. Handing it a digest therefore verifies against
// SHA256(digest) and silently checks the wrong statement. The three signature_v1
// vectors cannot cover this: they use a different key and go through
// message-based signing, where hashing once is exactly right.
func TestFramingFixtureDirectDigestSignature(t *testing.T) {
	fixture := loadFramingFixture(t)
	require.NotEmpty(t, fixture.SignatureDirect)

	for _, vector := range fixture.SignatureDirect {
		t.Run("signature_direct_digest_v1/"+vector.Name, func(t *testing.T) {
			privateKey := privateKeyFromFixture(t, vector.PrivateKeyHex)
			require.Equal(t, vector.PubkeyHex, hex.EncodeToString(privateKey.PubKey().Bytes()))

			digestSum := sha256.Sum256(vector.DigestMessage.encode(t))
			digest := digestSum[:]
			require.Equal(t, vector.DigestHex, hex.EncodeToString(digest),
				"digest_hex must be SHA256 of the published digest_message")

			signature, err := shared.SignSecp256k1DigestForTest(privateKey, digest)
			require.NoError(t, err)
			require.Equal(t, vector.SignatureHex, hex.EncodeToString(signature),
				"RFC6979 deterministic signing over the digest must reproduce the frozen 64-byte R||S")

			raw, err := shared.DecodeCanonicalSecp256k1SignatureHex(vector.SignatureHex)
			require.NoError(t, err)
			require.Equal(t, raw, signature)
			require.NoError(t, shared.VerifyStrictSecp256k1Digest(privateKey.PubKey(), digest, raw))

			gotDigest, err := shared.CanonicalSignatureDigest(raw)
			require.NoError(t, err)
			require.Equal(t, vector.SignatureDigestHex, hex.EncodeToString(gotDigest[:]))

			// The adoption precondition: one extra hash must not verify.
			rehashed := sha256.Sum256(digest)
			require.Equal(t, vector.RehashedDigestHex, hex.EncodeToString(rehashed[:]))
			require.NotEmpty(t, vector.RehashedRejectReason)
			require.Error(t, shared.VerifyStrictSecp256k1Digest(privateKey.PubKey(), rehashed[:], raw),
				vector.RehashedRejectReason)
			require.False(t, privateKey.PubKey().VerifySignature(digest, raw),
				"PubKey.VerifySignature hashes its input, so it verifies the wrong statement about a SIGN_DIGEST")

			// Signing the digest as a message is a different signature, so the two
			// paths cannot be swapped without moving the published bytes.
			asMessage, err := shared.SignWithSecp256k1ForTest(privateKey, digest)
			require.NoError(t, err)
			require.NotEqual(t, vector.SignatureHex, hex.EncodeToString(asMessage))
			require.True(t, privateKey.PubKey().VerifySignature(digest, asMessage),
				"the message-signed counterexample is what the rehashing verifier does accept")
		})
	}
}

func TestFramingFixtureDistinctHashGroups(t *testing.T) {
	fixture := loadFramingFixture(t)
	require.NotEmpty(t, fixture.DistinctHashGroups)

	lookup := make(map[string]string)
	for _, vector := range fixture.HV1 {
		lookup["h_v1/"+vector.Name] = vector.HashHex
	}
	for _, vector := range fixture.HFieldsV1 {
		lookup["h_fields_v1/"+vector.Name] = vector.HashHex
	}
	for _, vector := range fixture.MerkleRootV1 {
		lookup["merkle_root_v1/"+vector.Name] = vector.RootHex
	}

	for _, group := range fixture.DistinctHashGroups {
		t.Run(group.Name, func(t *testing.T) {
			require.GreaterOrEqual(t, len(group.Refs), 2)
			seen := make(map[string]string, len(group.Refs))
			for _, ref := range group.Refs {
				key := ref.Section + "/" + ref.Vector
				digest, ok := lookup[key]
				require.True(t, ok, "unknown fixture reference %q", key)
				require.NotEmpty(t, digest)
				previous, collided := seen[digest]
				require.False(t, collided, "%s collides with %s", key, previous)
				seen[digest] = key
			}
		})
	}
}

// The fixture uses dedicated test domains; they must never be mistaken for
// registered business domains (keeper_api_contract.md §1.4 rule 1).
func TestFramingFixtureUsesOnlyTestDomains(t *testing.T) {
	fixture := loadFramingFixture(t)
	domains := make(map[string]struct{})
	for _, vector := range fixture.HV1 {
		domains[vector.Domain] = struct{}{}
	}
	for _, vector := range fixture.HFieldsV1 {
		domains[vector.Domain] = struct{}{}
	}
	for _, vector := range fixture.MerkleRootV1 {
		domains[vector.Domain] = struct{}{}
	}
	for domain := range domains {
		require.True(t, strings.HasPrefix(domain, "TRUEOPEN_TEST_"),
			"framing fixture domain %q must be a test domain", domain)
		_, registered := shared.DomainSpecFor(domain)
		require.False(t, registered, "%q must not appear in DomainRegistryV1", domain)
	}
}

// ---- helpers ----

func loadFramingFixture(t *testing.T) framingFixture {
	t.Helper()
	raw, err := os.ReadFile(framingFixturePath)
	require.NoError(t, err)
	var fixture framingFixture
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	require.NoError(t, decoder.Decode(&fixture))
	require.Equal(t, "trueopen.shared.framing.v1", fixture.Schema)
	if os.Getenv(fixtureRegenEnv) == "1" {
		regenerateFramingFixture(t, fixture)
	}
	return fixture
}

func decodeHexList(t *testing.T, values []string) [][]byte {
	t.Helper()
	if len(values) == 0 {
		return nil
	}
	decoded := make([][]byte, 0, len(values))
	for _, value := range values {
		raw, err := hex.DecodeString(value)
		require.NoError(t, err)
		decoded = append(decoded, raw)
	}
	return decoded
}

func privateKeyFromFixture(t *testing.T, privateKeyHex string) *secp256k1.PrivKey {
	t.Helper()
	raw, err := hex.DecodeString(privateKeyHex)
	require.NoError(t, err)
	require.Len(t, raw, 32)
	return &secp256k1.PrivKey{Key: raw}
}

func mustSignFixture(t *testing.T, privateKey *secp256k1.PrivKey, message []byte) string {
	t.Helper()
	signature, err := shared.SignWithSecp256k1ForTest(privateKey, message)
	require.NoError(t, err)
	return hex.EncodeToString(signature)
}

// mutateFixtureSignature implements the named, language-independent mutations
// documented in the fixture "notes" section.
func mutateFixtureSignature(t *testing.T, signatureHex, mutation string) string {
	t.Helper()
	raw, err := hex.DecodeString(signatureHex)
	require.NoError(t, err)

	switch mutation {
	case "uppercase_hex":
		return strings.ToUpper(signatureHex)
	case "prepend_space":
		return " " + signatureHex
	case "append_space":
		return signatureHex + " "
	case "append_zero_byte":
		return hex.EncodeToString(append(append([]byte(nil), raw...), 0))
	case "prepend_recovery_byte":
		return hex.EncodeToString(append([]byte{0}, raw...))
	case "drop_last_byte":
		return hex.EncodeToString(raw[:len(raw)-1])
	case "drop_last_hex_char":
		return signatureHex[:len(signatureHex)-1]
	case "replace_last_hex_char_with_z":
		return signatureHex[:len(signatureHex)-1] + "z"
	case "negate_s":
		// s -> N - s produces the equally valid but non-canonical high-S form.
		mutated := append([]byte(nil), raw...)
		copy(mutated[32:], negateFixtureScalar(raw[32:]))
		return hex.EncodeToString(mutated)
	case "zero_r":
		mutated := append([]byte(nil), raw...)
		for i := 0; i < 32; i++ {
			mutated[i] = 0
		}
		return hex.EncodeToString(mutated)
	case "zero_s":
		mutated := append([]byte(nil), raw...)
		for i := 32; i < 64; i++ {
			mutated[i] = 0
		}
		return hex.EncodeToString(mutated)
	default:
		t.Fatalf("unknown signature mutation %q", mutation)
		return ""
	}
}

// secp256k1GroupOrder is the curve order N used by the negate_s mutation.
var secp256k1GroupOrder = [32]byte{
	0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
	0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xfe,
	0xba, 0xae, 0xdc, 0xe6, 0xaf, 0x48, 0xa0, 0x3b,
	0xbf, 0xd2, 0x5e, 0x8c, 0xd0, 0x36, 0x41, 0x41,
}

func negateFixtureScalar(scalar []byte) []byte {
	result := make([]byte, 32)
	borrow := 0
	for i := 31; i >= 0; i-- {
		difference := int(secp256k1GroupOrder[i]) - int(scalar[i]) - borrow
		if difference < 0 {
			difference += 256
			borrow = 1
		} else {
			borrow = 0
		}
		result[i] = byte(difference)
	}
	return result
}

func regenerateFramingFixture(t *testing.T, fixture framingFixture) {
	t.Helper()
	for i := range fixture.HV1 {
		vector := &fixture.HV1[i]
		payload := vector.Payload.encode(t)
		frame, err := shared.PayloadFrameV1(vector.Domain, payload)
		require.NoError(t, err)
		vector.FrameHex = ""
		if vector.IncludeFrame {
			vector.FrameHex = hex.EncodeToString(frame)
		}
		hash, err := shared.PayloadHashV1(vector.Domain, payload)
		require.NoError(t, err)
		vector.HashHex = hex.EncodeToString(hash)
	}
	for i := range fixture.HFieldsV1 {
		vector := &fixture.HFieldsV1[i]
		fields := encodeFixtureFields(t, vector.Fields)
		vector.FrameHex = ""
		if vector.IncludeFrame {
			framed := shared.CanonicalFrameBytes(append([][]byte{[]byte(vector.Domain)}, fields...)...)
			vector.FrameHex = hex.EncodeToString(framed)
		}
		vector.HashHex = hex.EncodeToString(shared.CanonicalHashBytes(vector.Domain, fields...))
	}
	for i := range fixture.OptionalV1 {
		vector := &fixture.OptionalV1[i]
		vector.FrameHex = hex.EncodeToString(optionalFrameFromFixture(t, *vector))
	}
	for i := range fixture.RepeatedV1 {
		vector := &fixture.RepeatedV1[i]
		vector.FrameHex = hex.EncodeToString(repeatedFrameFromFixture(t, *vector))
	}
	for i := range fixture.OneofV1 {
		vector := &fixture.OneofV1[i]
		vector.FrameHex = hex.EncodeToString(oneofFrameFromFixture(t, *vector))
	}
	for i := range fixture.MerkleRootV1 {
		vector := &fixture.MerkleRootV1[i]
		root, err := shared.MerkleRootV1(vector.Domain, decodeHexList(t, vector.LeavesHex))
		require.NoError(t, err)
		vector.RootHex = hex.EncodeToString(root)
	}
	for i := range fixture.SignatureV1 {
		vector := &fixture.SignatureV1[i]
		privateKey := privateKeyFromFixture(t, vector.PrivateKeyHex)
		vector.PubkeyHex = hex.EncodeToString(privateKey.PubKey().Bytes())
		vector.SignatureHex = mustSignFixture(t, privateKey, vector.Message.encode(t))
		raw, err := hex.DecodeString(vector.SignatureHex)
		require.NoError(t, err)
		digest := sha256.Sum256(raw)
		vector.SignatureDigestHex = hex.EncodeToString(digest[:])
	}
	for i := range fixture.SignatureDirect {
		vector := &fixture.SignatureDirect[i]
		privateKey := privateKeyFromFixture(t, vector.PrivateKeyHex)
		vector.PubkeyHex = hex.EncodeToString(privateKey.PubKey().Bytes())
		digest := sha256.Sum256(vector.DigestMessage.encode(t))
		vector.DigestHex = hex.EncodeToString(digest[:])
		signature, err := shared.SignSecp256k1DigestForTest(privateKey, digest[:])
		require.NoError(t, err)
		vector.SignatureHex = hex.EncodeToString(signature)
		signatureDigest := sha256.Sum256(signature)
		vector.SignatureDigestHex = hex.EncodeToString(signatureDigest[:])
		rehashed := sha256.Sum256(digest[:])
		vector.RehashedDigestHex = hex.EncodeToString(rehashed[:])
	}
	writeFixtureJSON(t, framingFixturePath, fixture)
}

func writeFixtureJSON(t *testing.T, path string, value any) {
	t.Helper()
	encoded, err := json.MarshalIndent(value, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, append(encoded, '\n'), 0o644))
	t.Fatalf(
		"regenerated %s; unset %s and review the diff against the frozen contract before committing",
		path, fixtureRegenEnv,
	)
}
