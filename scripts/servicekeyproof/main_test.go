package main

// Anti-drift tests for the registration proof tool.
//
// The tool is only useful if its preimage stays byte-identical to the one
// x/hub/keeper builds when it verifies a registration. That framing lives
// inline in the keeper and is not exported, so these tests pin the tool's side
// two ways:
//
//   - a golden digest, so any change to the domain constant or the canonical
//     framing trips a test instead of silently producing rejected proofs;
//   - a field-binding matrix, so a field accidentally dropped from the preimage
//     is caught even if the golden is regenerated carelessly.
//
// A green run here does NOT by itself prove the chain agrees; that is what the
// live check in docs/runbooks/builder_registration_testing.md §3.3 is for.

import (
	"encoding/hex"
	"testing"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// Fixed inputs so the digest is reproducible.
var (
	goldenChainID  = "trueopen-localnet-1"
	goldenOperator = mustHex("cb96e50aec7ce7d8b1b3d3aa66b1a3b0a2f0e1c4")
	goldenPubkey   = mustHex("02742c9e0d292123b509abe95ee6b27226dbfd86ad0120c27964881d22813712b3")
	goldenNonce    = uint64(1)
)

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// TestRegistrationDigestGolden pins the preimage. If this fails, the canonical
// framing or the TRUEOPEN_SERVICE_REGISTRATION_V1 domain changed: re-check the
// keeper's RegisterBuilder framing, update the golden, and re-run the live
// registration check before trusting the tool again.
func TestRegistrationDigestGolden(t *testing.T) {
	got := registrationDigest(goldenChainID,
		shared.ParticipantType_PARTICIPANT_TYPE_BUILDER,
		goldenOperator, goldenPubkey, goldenNonce)

	require.Len(t, got, 32, "digest must be a sha256 sum")
	require.Equal(t,
		"0a582f7a51b62bf323e2e1a029369cf7f706b9929026a94b3060b29a248de282",
		hex.EncodeToString(got),
		"registration preimage changed; see the comment on this test")
}

// TestEveryFieldIsBoundIntoTheDigest proves no bound field is a no-op. A field
// dropped from the preimage would let one proof be replayed across chains,
// roles, operators, keys or nonces.
func TestEveryFieldIsBoundIntoTheDigest(t *testing.T) {
	base := registrationDigest(goldenChainID,
		shared.ParticipantType_PARTICIPANT_TYPE_BUILDER,
		goldenOperator, goldenPubkey, goldenNonce)

	otherOperator := append([]byte(nil), goldenOperator...)
	otherOperator[0] ^= 0xff
	otherPubkey := append([]byte(nil), goldenPubkey...)
	otherPubkey[1] ^= 0xff

	cases := []struct {
		name   string
		digest []byte
	}{
		{"chain id", registrationDigest("other-chain",
			shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, goldenOperator, goldenPubkey, goldenNonce)},
		{"participant role", registrationDigest(goldenChainID,
			shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, goldenOperator, goldenPubkey, goldenNonce)},
		{"operator", registrationDigest(goldenChainID,
			shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, otherOperator, goldenPubkey, goldenNonce)},
		{"service pubkey", registrationDigest(goldenChainID,
			shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, goldenOperator, otherPubkey, goldenNonce)},
		{"nonce", registrationDigest(goldenChainID,
			shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, goldenOperator, goldenPubkey, goldenNonce+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NotEqual(t, hex.EncodeToString(base), hex.EncodeToString(tc.digest),
				"%s must be bound into the registration preimage", tc.name)
		})
	}
}

// TestProofVerifiesWithTheChainVerifier runs the tool's output through the same
// verifier the keeper calls.
func TestProofVerifiesWithTheChainVerifier(t *testing.T) {
	priv := secp256k1.GenPrivKey()
	pub := priv.PubKey().(*secp256k1.PubKey)

	digest := registrationDigest(goldenChainID,
		shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, goldenOperator, pub.Key, goldenNonce)

	proof, err := hubtypes.SignSecp256k1DigestForTest(priv, digest)
	require.NoError(t, err)
	require.NoError(t, hubtypes.VerifyStrictSecp256k1Digest(pub, digest, proof))
	require.Len(t, proof, 64, "service_key_proof must be a 64-byte R||S signature")
}

// TestTamperedProofIsRejected is the negative control: the verifier must not
// accept a proof that was altered, signed by the wrong key, or bound to a
// different digest. Without this, a no-op verifier would look like success.
func TestTamperedProofIsRejected(t *testing.T) {
	priv := secp256k1.GenPrivKey()
	pub := priv.PubKey().(*secp256k1.PubKey)
	digest := registrationDigest(goldenChainID,
		shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, goldenOperator, pub.Key, goldenNonce)
	proof, err := hubtypes.SignSecp256k1DigestForTest(priv, digest)
	require.NoError(t, err)

	t.Run("flipped byte", func(t *testing.T) {
		tampered := append([]byte(nil), proof...)
		tampered[10] ^= 0x01
		require.Error(t, hubtypes.VerifyStrictSecp256k1Digest(pub, digest, tampered))
	})

	t.Run("wrong signing key", func(t *testing.T) {
		other := secp256k1.GenPrivKey()
		otherProof, err := hubtypes.SignSecp256k1DigestForTest(other, digest)
		require.NoError(t, err)
		require.Error(t, hubtypes.VerifyStrictSecp256k1Digest(pub, digest, otherProof))
	})

	t.Run("proof bound to another chain id", func(t *testing.T) {
		otherDigest := registrationDigest("some-other-chain",
			shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, goldenOperator, pub.Key, goldenNonce)
		require.Error(t, hubtypes.VerifyStrictSecp256k1Digest(pub, otherDigest, proof))
	})
}

func TestParticipantOfAcceptsBothRoles(t *testing.T) {
	builder, builderKind, err := participantOf("builder")
	require.NoError(t, err)
	require.Equal(t, shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, builder)
	require.Equal(t, "SERVICE_ENDPOINT_KIND_NEXUS_GRPC", builderKind)

	cortex, cortexKind, err := participantOf("CORTEX")
	require.NoError(t, err)
	require.Equal(t, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, cortex)
	require.Equal(t, "SERVICE_ENDPOINT_KIND_OBJECT_GATEWAY_HTTPS", cortexKind)

	_, _, err = participantOf("validator")
	require.Error(t, err)
}

func TestServiceKeyInputModes(t *testing.T) {
	_, err := serviceKey("", false)
	require.Error(t, err, "must require one of --generate / --service-privkey-hex")

	_, err = serviceKey("aabb", true)
	require.Error(t, err, "must reject both modes at once")

	_, err = serviceKey("aabb", false)
	require.Error(t, err, "must reject a short key")

	gen, err := serviceKey("", true)
	require.NoError(t, err)
	require.Len(t, gen.Key, 32)

	reused, err := serviceKey(hex.EncodeToString(gen.Key), false)
	require.NoError(t, err)
	require.Equal(t, gen.Key, reused.Key, "an explicit key must be used verbatim")
}
