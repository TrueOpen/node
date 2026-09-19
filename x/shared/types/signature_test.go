package types_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/asn1"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestStrictSecp256k1DirectDigestGolden(t *testing.T) {
	privateKey := &secp256k1.PrivKey{Key: bytes.Repeat([]byte{0x11}, 32)}
	digest := sha256.Sum256([]byte("trueopen-builder-envelope-direct-digest-v1"))
	signature, err := shared.SignSecp256k1DigestForTest(privateKey, digest[:])
	require.NoError(t, err)
	require.Equal(t, "41e689b04cc024b116f52edaf669278c3983bafadf98e28472e75891bad00766341244286d3dd72cb6397b606b592e62deec758e2afbf27f0cb1ce4331f5a0ee", hex.EncodeToString(signature))
	require.NoError(t, shared.VerifyStrictSecp256k1Digest(privateKey.PubKey(), digest[:], signature))
	require.False(t, privateKey.PubKey().VerifySignature(digest[:], signature),
		"the Cosmos message verifier hashes digest bytes a second time")
}

// TestVerifyStrictSecp256k1DigestHasNoImplicitSecondHash is the acceptance
// precondition for the task specification §10.3: the digest verifier must consume
// SIGN_DIGEST
// exactly as handed to it. An implementation that hashes once more would still
// pass a naive round-trip, so the counterexamples below pin both directions —
// neither convention may ever accept the other's signature.
func TestVerifyStrictSecp256k1DigestHasNoImplicitSecondHash(t *testing.T) {
	privateKey := secp256k1.GenPrivKey()
	pubKey := privateKey.PubKey()
	digest := sha256.Sum256([]byte("trueopen-no-implicit-second-hash-v1"))

	directDigestSig, err := shared.SignSecp256k1DigestForTest(privateKey, digest[:])
	require.NoError(t, err)
	require.NoError(t, shared.VerifyStrictSecp256k1Digest(pubKey, digest[:], directDigestSig))

	doubleHash := sha256.Sum256(digest[:])
	require.Error(t, shared.VerifyStrictSecp256k1Digest(pubKey, doubleHash[:], directDigestSig),
		"verification must not re-hash the digest before ECDSA")

	messageSig, err := shared.SignWithSecp256k1ForTest(privateKey, digest[:])
	require.NoError(t, err)
	require.Error(t, shared.VerifyStrictSecp256k1Digest(pubKey, digest[:], messageSig),
		"a Cosmos message signature must not verify as a direct-digest signature")
	require.False(t, pubKey.VerifySignature(digest[:], directDigestSig),
		"a direct-digest signature must not verify through the double-hashing Cosmos message path")
}

func TestVerifyStrictSecp256k1DigestRejectsSignaturesForAlternateMaterial(t *testing.T) {
	privateKey := secp256k1.GenPrivKey()
	pubKey := privateKey.PubKey()
	digest := sha256.Sum256([]byte("task-order-preimage"))
	correctSignature, err := shared.SignSecp256k1DigestForTest(privateKey, digest[:])
	require.NoError(t, err)
	require.NoError(t, shared.VerifyStrictSecp256k1Digest(pubKey, digest[:], correctSignature))

	for name, material := range map[string][]byte{
		"digest_as_message":  digest[:],
		"digest_hex":         []byte(hex.EncodeToString(digest[:])),
		"protobuf_bytes":     {0x0a, 0x03, 'f', 'o', 'o'},
		"json_bytes":         []byte(`{"task_hash":"..."}`),
		"signed_order_bytes": append([]byte("signed-order:"), digest[:]...),
	} {
		t.Run(name, func(t *testing.T) {
			wrongSignature, err := shared.SignWithSecp256k1ForTest(privateKey, material)
			require.NoError(t, err)
			require.True(t, pubKey.VerifySignature(material, wrongSignature),
				"the counterexample must be a valid signature for the wrong material")
			require.Error(t, shared.VerifyStrictSecp256k1Digest(pubKey, digest[:], wrongSignature),
				"a signature for alternate material must fail against the real task_hash")
		})
	}
	// The exact task_order_preimage is deliberately not an alternate in this
	// table: a normal message signer hashes it once, producing task_hash itself,
	// so that signature is mathematically a valid task_hash signature. The Task
	// rule rejects only a preimage-verification convention whose result does not
	// verify against the real 32-byte task_hash.
}

func TestStrictSecp256k1SignatureRoundTripAndDigest(t *testing.T) {
	privateKey := secp256k1.GenPrivKey()
	message := shared.CanonicalHashBytes("TRUEOPEN_TEST_V1", []byte("chain"), []byte("scope"))
	signature, err := shared.SignWithSecp256k1ForTest(privateKey, message)
	require.NoError(t, err)

	require.True(t, privateKey.PubKey().VerifySignature(message, signature))
	digest, err := shared.CanonicalSignatureDigest(signature)
	require.NoError(t, err)
	require.Equal(t, sha256.Sum256(signature), digest)
}

func TestStrictSecp256k1SignatureRejectsNonCanonicalAndInvalidScalars(t *testing.T) {
	privateKey := secp256k1.GenPrivKey()
	message := []byte("strict-signature")
	raw, err := shared.SignWithSecp256k1ForTest(privateKey, message)
	require.NoError(t, err)
	signature := hex.EncodeToString(raw)

	order, ok := new(big.Int).SetString("FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141", 16)
	require.True(t, ok)
	s := new(big.Int).SetBytes(raw[32:])
	highS := new(big.Int).Sub(order, s).FillBytes(make([]byte, 32))
	highSRaw := append([]byte(nil), raw...)
	copy(highSRaw[32:], highS)

	zeroR := append([]byte(nil), raw...)
	clear(zeroR[:32])
	zeroS := append([]byte(nil), raw...)
	clear(zeroS[32:])
	orderBytes := order.FillBytes(make([]byte, 32))
	orderR := append([]byte(nil), raw...)
	copy(orderR[:32], orderBytes)
	orderS := append([]byte(nil), raw...)
	copy(orderS[32:], orderBytes)
	der, err := asn1.Marshal(struct {
		R *big.Int
		S *big.Int
	}{new(big.Int).SetBytes(raw[:32]), new(big.Int).SetBytes(raw[32:])})
	require.NoError(t, err)

	// The first two mutations only exist as text, so the hex boundary is the one
	// place that can reject them; the rest must be rejected on the raw bytes too,
	// because that is the form consensus actually carries.
	textOnly := map[string]string{
		"uppercase":  strings.ToUpper(signature),
		"whitespace": " " + signature,
	}
	for name, invalid := range textOnly {
		t.Run(name, func(t *testing.T) {
			_, err := shared.DecodeCanonicalSecp256k1SignatureHex(invalid)
			require.Error(t, err)
		})
	}

	scalars := map[string][]byte{
		"recovery": append(append([]byte(nil), raw...), 0x00),
		"der":      der,
		"zero_r":   zeroR,
		"zero_s":   zeroS,
		"high_s":   highSRaw,
		"order_r":  orderR,
		"order_s":  orderS,
	}
	for name, invalid := range scalars {
		t.Run(name, func(t *testing.T) {
			require.Error(t, shared.RequireCanonicalSecp256k1Signature(invalid))
			_, err := shared.DecodeCanonicalSecp256k1SignatureHex(hex.EncodeToString(invalid))
			require.Error(t, err)
			_, err = shared.CanonicalSignatureDigest(invalid)
			require.Error(t, err)
		})
	}
}

func TestStrictSecp256k1SignatureRejectsWrongKeyType(t *testing.T) {
	privateKey := secp256k1.GenPrivKey()
	digest := sha256.Sum256([]byte("wrong-curve"))
	signature, err := shared.SignSecp256k1DigestForTest(privateKey, digest[:])
	require.NoError(t, err)

	err = shared.VerifyStrictSecp256k1Digest(ed25519.GenPrivKey().PubKey(), digest[:], signature)
	require.ErrorContains(t, err, "must be secp256k1")
}
