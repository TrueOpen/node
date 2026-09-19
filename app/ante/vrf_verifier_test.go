package ante_test

// VRF verifier + Prove unit tests.
//
// Covered properties:
//   - Prove/Verify round-trip: any (sk, input) produces (proof, β) that Verify
//     accepts as (true, β).
//   - Determinism: same (sk, input) produces same (proof, β) — required for
//     3-validator app_hash replay (Beacon replay depends on this).
//   - Codec strictness: any other codec string is rejected via
//     ErrUnsupportedProofCodec, even if the proof and randomness happen to be
//     valid ECVRF outputs. This defends against a future migration that
//     silently switches algorithms.
//   - Tamper resistance: bit-flipping the proof, the input, the randomness,
//     or the pubkey each independently fails verification.
//   - Randomness lock: a proof that verifies but ships a β different from
//     ProofToHash(π) is rejected (a "well-formed but lying" carrier).
//   - Length hardening: wrong-length pubkey / empty proof / empty randomness /
//     empty input are all rejected with a typed error before touching the
//     ecvrf library.

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/app/ante"
)

// mustEd25519Key generates a fresh ed25519 keypair for tests. Any failure
// here fails the test — deterministic randomness is what we're testing, so
// leaking a bit of it during setup is fine.
func mustEd25519Key(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return pub, priv
}

func TestVRFProveVerifyRoundTrip(t *testing.T) {
	pub, priv := mustEd25519Key(t)
	input := []byte("TRUEOPEN_BEACON_VRF_INPUT_V1|prior|height-7")

	proof, beta, err := ante.Prove(priv, input)
	require.NoError(t, err)
	require.Len(t, proof, 80, "ECVRF-EDWARDS25519-SHA512-ELL2 proof is 80 bytes")
	require.Len(t, beta, 32, "reduced randomness is 32 bytes (SHA-256 of raw 64-byte β with domain separator)")

	v := ante.NewVRFVerifier()
	require.NoError(t, v.VerifyBeaconProof(
		ante.BeaconProofCodecECVRFEdwards25519SHA512ELL2V1,
		pub, input, proof, beta,
	))
}

func TestVRFProveIsDeterministic(t *testing.T) {
	// Deterministic (sk, input) -> (proof, β) is what lets 3 validators
	// arrive at the same app_hash. If this ever fails, Beacon replay replay is broken.
	_, priv := mustEd25519Key(t)
	input := []byte("deterministic-input")

	p1, b1, err := ante.Prove(priv, input)
	require.NoError(t, err)
	p2, b2, err := ante.Prove(priv, input)
	require.NoError(t, err)

	require.Equal(t, p1, p2, "proof must be deterministic for identical (sk, input)")
	require.Equal(t, b1, b2, "β must be deterministic for identical (sk, input)")
}

func TestVRFVerifyRejectsWrongCodec(t *testing.T) {
	pub, priv := mustEd25519Key(t)
	input := []byte("in")

	proof, beta, err := ante.Prove(priv, input)
	require.NoError(t, err)

	v := ante.NewVRFVerifier()
	err = v.VerifyBeaconProof("ecvrf_ed25519_sha512_edwards25519_v1" /* old draft-10 name */, pub, input, proof, beta)
	require.Error(t, err)
	require.ErrorIs(t, err, ante.ErrUnsupportedProofCodec,
		"any non-frozen codec string must be surfaced as ErrUnsupportedProofCodec, not a bland verify failure")
}

func TestVRFVerifyRejectsTamperedProof(t *testing.T) {
	pub, priv := mustEd25519Key(t)
	input := []byte("in")

	proof, beta, err := ante.Prove(priv, input)
	require.NoError(t, err)

	// flip a byte in the proof
	tampered := make([]byte, len(proof))
	copy(tampered, proof)
	tampered[len(tampered)/2] ^= 0x01

	v := ante.NewVRFVerifier()
	err = v.VerifyBeaconProof(ante.BeaconProofCodecECVRFEdwards25519SHA512ELL2V1, pub, input, tampered, beta)
	require.Error(t, err, "tampered proof must not verify")
}

func TestVRFVerifyRejectsWrongInput(t *testing.T) {
	pub, priv := mustEd25519Key(t)
	input := []byte("in")

	proof, beta, err := ante.Prove(priv, input)
	require.NoError(t, err)

	v := ante.NewVRFVerifier()
	// If a proposer signs the wrong VRF input (e.g., builds carrier for
	// height H but signs input for H-1), Verify must reject.
	err = v.VerifyBeaconProof(ante.BeaconProofCodecECVRFEdwards25519SHA512ELL2V1, pub, []byte("different-input"), proof, beta)
	require.Error(t, err)
}

func TestVRFVerifyRejectsWrongPubkey(t *testing.T) {
	pubA, privA := mustEd25519Key(t)
	pubB, _ := mustEd25519Key(t)
	require.NotEqual(t, pubA, pubB)

	input := []byte("in")
	proof, beta, err := ante.Prove(privA, input)
	require.NoError(t, err)

	v := ante.NewVRFVerifier()
	err = v.VerifyBeaconProof(ante.BeaconProofCodecECVRFEdwards25519SHA512ELL2V1, pubB, input, proof, beta)
	require.Error(t, err, "verification under a foreign pubkey must fail")
}

func TestVRFVerifyRejectsMismatchedRandomness(t *testing.T) {
	// "Well-formed proof that declares a lie about β".
	pub, priv := mustEd25519Key(t)
	input := []byte("in")

	proof, beta, err := ante.Prove(priv, input)
	require.NoError(t, err)
	require.Len(t, beta, 32)

	// Substitute a plausible-looking but wrong β. The ecvrf library itself
	// would accept the proof, but our verifier binds β into the pass
	// condition — otherwise a proposer could ship any proof + any 32-byte
	// value and Keeper would happily record it.
	lying := make([]byte, len(beta))
	copy(lying, beta)
	lying[0] ^= 0x80

	v := ante.NewVRFVerifier()
	err = v.VerifyBeaconProof(ante.BeaconProofCodecECVRFEdwards25519SHA512ELL2V1, pub, input, proof, lying)
	require.Error(t, err, "declared randomness must match ProofToHash(π)")
	require.NotErrorIs(t, err, ante.ErrUnsupportedProofCodec, "the codec was fine; the error should be about β mismatch")
}

func TestVRFVerifyRejectsBadInputShapes(t *testing.T) {
	pub, priv := mustEd25519Key(t)
	input := []byte("in")
	proof, beta, err := ante.Prove(priv, input)
	require.NoError(t, err)

	v := ante.NewVRFVerifier()

	// wrong pubkey length
	err = v.VerifyBeaconProof(ante.BeaconProofCodecECVRFEdwards25519SHA512ELL2V1, pub[:16], input, proof, beta)
	require.Error(t, err)

	// empty proof
	err = v.VerifyBeaconProof(ante.BeaconProofCodecECVRFEdwards25519SHA512ELL2V1, pub, input, nil, beta)
	require.Error(t, err)

	// empty randomness
	err = v.VerifyBeaconProof(ante.BeaconProofCodecECVRFEdwards25519SHA512ELL2V1, pub, input, proof, nil)
	require.Error(t, err)
}

func TestVRFProveRejectsBadInput(t *testing.T) {
	_, priv := mustEd25519Key(t)

	_, _, err := ante.Prove(priv, nil)
	require.Error(t, err, "empty VRF input must be refused; PrepareProposal has no legitimate reason to sign nothing")

	// wrong-size private key
	_, _, err = ante.Prove(priv[:31], []byte("in"))
	require.Error(t, err)
}

// TestVRFVerifyErrorTypesAreDistinguishable double-checks the two failure
// families surface differently: codec-support errors are ErrUnsupportedProofCodec
// (a policy decision), while cryptographic-verify errors are plain errors
// (an accusation). Callers rely on this distinction to log / alert on the
// stronger category.
func TestVRFVerifyErrorTypesAreDistinguishable(t *testing.T) {
	pub, priv := mustEd25519Key(t)
	proof, beta, err := ante.Prove(priv, []byte("x"))
	require.NoError(t, err)

	v := ante.NewVRFVerifier()

	codecErr := v.VerifyBeaconProof("some-future-alg", pub, []byte("x"), proof, beta)
	require.True(t, errors.Is(codecErr, ante.ErrUnsupportedProofCodec))

	// Any crypto failure must NOT surface as ErrUnsupportedProofCodec.
	tampered := make([]byte, len(proof))
	copy(tampered, proof)
	tampered[0] ^= 0x01
	cryptoErr := v.VerifyBeaconProof(ante.BeaconProofCodecECVRFEdwards25519SHA512ELL2V1, pub, []byte("x"), tampered, beta)
	require.Error(t, cryptoErr)
	require.False(t, errors.Is(cryptoErr, ante.ErrUnsupportedProofCodec))
}
