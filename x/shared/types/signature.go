package types

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	dcrsecp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	dcrecdsa "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
)

const CompactSecp256k1SignatureBytes = 64

// SignatureSchemeSecp256k1 is the only accepted signature_scheme literal.
// The task specification §7.3 requires it to be byte-for-byte lowercase ASCII
// "secp256k1" and
// rejects aliases and case variants, so comparisons must be exact equality
// against this constant, never a case-folded or trimmed match.
const SignatureSchemeSecp256k1 = "secp256k1"

// RequireCanonicalSecp256k1Signature enforces the unique compact R||S wire form
// on raw signature bytes: exactly 64 bytes, R and S in range and non-zero, and
// low-S. Every verifier and the hex boundary below call it, so a signature that
// reaches ECDSA has always passed these checks.
func RequireCanonicalSecp256k1Signature(signature []byte) error {
	if len(signature) != CompactSecp256k1SignatureBytes {
		return fmt.Errorf("secp256k1 signature must be exactly 64 bytes in compact R||S form, got %d", len(signature))
	}
	var r, s dcrsecp256k1.ModNScalar
	if overflow := r.SetByteSlice(signature[:32]); overflow || r.IsZero() {
		return fmt.Errorf("secp256k1 signature R must be non-zero and in range")
	}
	if overflow := s.SetByteSlice(signature[32:]); overflow || s.IsZero() || s.IsOverHalfOrder() {
		return fmt.Errorf("secp256k1 signature S must be non-zero, in range, and low-S")
	}
	return nil
}

// DecodeCanonicalSecp256k1SignatureHex is the one and only text boundary for
// signatures. Only CLI input, Genesis documents and protocol vectors may cross
// it; consensus paths carry raw bytes end to end and call the byte-oriented
// helpers directly.
func DecodeCanonicalSecp256k1SignatureHex(signature string) ([]byte, error) {
	if signature == "" {
		return nil, fmt.Errorf("signature is required")
	}
	if strings.TrimSpace(signature) != signature {
		return nil, fmt.Errorf("signature must not contain surrounding whitespace")
	}
	raw, err := hex.DecodeString(signature)
	if err != nil {
		return nil, fmt.Errorf("invalid signature hex: %w", err)
	}
	if hex.EncodeToString(raw) != signature {
		return nil, fmt.Errorf("signature hex must be lowercase canonical form")
	}
	if err := RequireCanonicalSecp256k1Signature(raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// VerifyStrictSecp256k1Digest verifies ECDSA directly over an already-derived
// Hash32. Cosmos SDK's PubKey.VerifySignature hashes its input again, so it must
// not be used for contracts that explicitly say "sign SIGN_DIGEST".
func VerifyStrictSecp256k1Digest(pubKey cryptotypes.PubKey, digest, signature []byte) error {
	if len(digest) != 32 {
		return fmt.Errorf("secp256k1 signing digest must be exactly 32 bytes")
	}
	sdkPubKey, ok := pubKey.(*secp256k1.PubKey)
	if !ok || sdkPubKey == nil {
		return fmt.Errorf("public key must be secp256k1")
	}
	if err := RequireCanonicalSecp256k1Signature(signature); err != nil {
		return err
	}
	parsedPubKey, err := dcrsecp256k1.ParsePubKey(sdkPubKey.Key)
	if err != nil {
		return fmt.Errorf("invalid secp256k1 public key: %w", err)
	}
	var r, s dcrsecp256k1.ModNScalar
	r.SetByteSlice(signature[:32])
	s.SetByteSlice(signature[32:])
	if !dcrecdsa.NewSignature(&r, &s).Verify(digest, parsedPubKey) {
		return fmt.Errorf("signature does not verify against signing digest")
	}
	return nil
}

// CanonicalSignatureDigest hashes raw signature bytes, never their hex text.
// It returns the array rather than a slice so a caller cannot accidentally
// alias it into Store state.
func CanonicalSignatureDigest(signature []byte) ([32]byte, error) {
	if err := RequireCanonicalSecp256k1Signature(signature); err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(signature), nil
}

// SignWithSecp256k1ForTest signs message bytes using the Cosmos convention,
// which hashes the message once inside Sign. It exists only so tests can build
// the message-oriented counterexamples that prove the digest path is different.
func SignWithSecp256k1ForTest(priv *secp256k1.PrivKey, message []byte) ([]byte, error) {
	return priv.Sign(message)
}

// SignSecp256k1DigestForTest is the deterministic direct-digest counterpart to
// VerifyStrictSecp256k1Digest. It is test-only protocol-vector support.
func SignSecp256k1DigestForTest(priv *secp256k1.PrivKey, digest []byte) ([]byte, error) {
	if priv == nil || len(priv.Key) != 32 || len(digest) != 32 {
		return nil, fmt.Errorf("private key and 32-byte signing digest are required")
	}
	compact := dcrecdsa.SignCompact(dcrsecp256k1.PrivKeyFromBytes(priv.Key), digest, false)
	return append([]byte(nil), compact[1:]...), nil
}
