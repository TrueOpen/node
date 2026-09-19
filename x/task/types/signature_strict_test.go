package types_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// TestVerifyStrictSecp256k1DigestRoundTrip exercises the canonical happy-path:
// sign one already-derived digest directly and verify the raw 64 bytes.
func TestVerifyStrictSecp256k1DigestRoundTrip(t *testing.T) {
	priv := secp256k1.GenPrivKey()
	pub := priv.PubKey()

	digest := sha256.Sum256([]byte("strict-signature-round-trip"))
	sig, err := types.SignSecp256k1DigestForTest(priv, digest[:])
	require.NoError(t, err)

	require.NoError(t, types.VerifyStrictSecp256k1Digest(pub, digest[:], sig))
	require.False(t, pub.VerifySignature(digest[:], sig),
		"a direct-digest signature must not verify through the double-hashing Cosmos message path")
	doubleHashed, err := priv.Sign(digest[:])
	require.NoError(t, err)
	require.Error(t, types.VerifyStrictSecp256k1Digest(pub, digest[:], doubleHashed),
		"a Cosmos message signature must not verify as a direct-digest signature")
}

// TestVerifyStrictSecp256k1DigestRejectsWrongDigest confirms a signature over
// digest A does not verify against digest B.
func TestVerifyStrictSecp256k1DigestRejectsWrongDigest(t *testing.T) {
	priv := secp256k1.GenPrivKey()
	pub := priv.PubKey()
	digestA := sha256.Sum256([]byte("message-a"))
	digestB := sha256.Sum256([]byte("message-b"))

	sig, err := types.SignSecp256k1DigestForTest(priv, digestA[:])
	require.NoError(t, err)

	err = types.VerifyStrictSecp256k1Digest(pub, digestB[:], sig)
	require.ErrorIs(t, err, types.ErrInvalidSignature)
	require.Contains(t, err.Error(), "does not verify")
}

// TestVerifyStrictSecp256k1SignatureRejectsWrongKey confirms a signature
// from one private key does not verify against another's public key.
func TestVerifyStrictSecp256k1DigestRejectsWrongKey(t *testing.T) {
	signer := secp256k1.GenPrivKey()
	attacker := secp256k1.GenPrivKey()

	digest := sha256.Sum256([]byte("hello"))
	sig, _ := types.SignSecp256k1DigestForTest(signer, digest[:])

	err := types.VerifyStrictSecp256k1Digest(attacker.PubKey(), digest[:], sig)
	require.ErrorIs(t, err, types.ErrInvalidSignature)
}

func TestVerifyStrictSecp256k1DigestRejectsRecoveryByte(t *testing.T) {
	priv := secp256k1.GenPrivKey()
	pub := priv.PubKey()

	digest := sha256.Sum256([]byte("test-msg"))
	sig, err := types.SignSecp256k1DigestForTest(priv, digest[:])
	require.NoError(t, err)

	with65 := append(sig, 0x01) // simulate recovery suffix
	require.Len(t, with65, 65)

	err = types.VerifyStrictSecp256k1Digest(pub, digest[:], with65)
	require.ErrorIs(t, err, types.ErrInvalidSignature)
	require.Contains(t, err.Error(), "exactly 64 bytes")
}

func TestVerifyStrictSecp256k1DigestRejectsHighS(t *testing.T) {
	priv := secp256k1.GenPrivKey()
	digest := sha256.Sum256([]byte("high-s-test"))
	sig, err := types.SignSecp256k1DigestForTest(priv, digest[:])
	require.NoError(t, err)

	order, ok := new(big.Int).SetString("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141", 16)
	require.True(t, ok)
	s := new(big.Int).SetBytes(sig[32:])
	highS := new(big.Int).Sub(order, s).FillBytes(make([]byte, 32))
	copy(sig[32:], highS)

	err = types.VerifyStrictSecp256k1Digest(priv.PubKey(), digest[:], sig)
	require.ErrorIs(t, err, types.ErrInvalidSignature)
	require.Contains(t, err.Error(), "low-S")
}

// TestVerifyStrictSecp256k1SignatureRejectsEmpty confirms strict mode is
// not lenient — empty signatures are an error here.
func TestVerifyStrictSecp256k1DigestRejectsEmpty(t *testing.T) {
	pub := secp256k1.GenPrivKey().PubKey()
	digest := sha256.Sum256([]byte("msg"))
	err := types.VerifyStrictSecp256k1Digest(pub, digest[:], nil)
	require.ErrorIs(t, err, types.ErrInvalidSignature)
	require.Contains(t, err.Error(), "exactly 64 bytes")

	err = types.VerifyStrictSecp256k1Digest(pub, digest[:], []byte{})
	require.ErrorIs(t, err, types.ErrInvalidSignature)
	require.Contains(t, err.Error(), "exactly 64 bytes")
}

// The hex boundary is the only place a signature can still be text, so the
// text-shaped rejects that used to live on the verifier are pinned there.
func TestDecodeCanonicalSecp256k1SignatureHexRejectsNonCanonicalText(t *testing.T) {
	priv := secp256k1.GenPrivKey()
	digest := sha256.Sum256([]byte("msg"))
	sig, err := types.SignSecp256k1DigestForTest(priv, digest[:])
	require.NoError(t, err)

	_, err = shared.DecodeCanonicalSecp256k1SignatureHex(" " + hex.EncodeToString(sig))
	require.ErrorContains(t, err, "whitespace")

	_, err = shared.DecodeCanonicalSecp256k1SignatureHex("")
	require.ErrorContains(t, err, "required")

	_, err = shared.DecodeCanonicalSecp256k1SignatureHex("user-signature")
	require.ErrorContains(t, err, "invalid signature hex")
}

// TestVerifyStrictSecp256k1SignatureRejectsBadLength confirms the wrong byte
// count is rejected with a clear length error.
func TestVerifyStrictSecp256k1DigestRejectsBadLength(t *testing.T) {
	pub := secp256k1.GenPrivKey().PubKey()
	digest := sha256.Sum256([]byte("msg"))
	err := types.VerifyStrictSecp256k1Digest(pub, digest[:], bytes.Repeat([]byte{0xab}, 32))
	require.ErrorIs(t, err, types.ErrInvalidSignature)
	require.Contains(t, err.Error(), "exactly 64 bytes")
}

// TestVerifyStrictSecp256k1SignatureRejectsNilPubKey confirms the explicit
// nil-pubkey guard.
func TestVerifyStrictSecp256k1DigestRejectsNilPubKey(t *testing.T) {
	priv := secp256k1.GenPrivKey()
	digest := sha256.Sum256([]byte("msg"))
	sig, _ := types.SignSecp256k1DigestForTest(priv, digest[:])
	err := types.VerifyStrictSecp256k1Digest(nil, digest[:], sig)
	require.ErrorIs(t, err, types.ErrInvalidSignature)
	require.Contains(t, err.Error(), "public key must be secp256k1")
}
