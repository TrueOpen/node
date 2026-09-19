package ante

// proposer-VRF Prove/Verify wrapper (Node-owned, app/ante layer).
//
// The BeaconProofVerifier interface is frozen in x/hub/keeper
// (beacon_runtime.go); Beacon canonical state is Hub-owned. This wrapper
// supplies the concrete ECVRF implementation. Per RFC 9381 §5.4.1.2 the
// algorithm is ECVRF-EDWARDS25519-SHA512-ELL2 (suite string 0x04), delivered
// via github.com/oasisprotocol/curve25519-voi.
//
// The Prove side lives here too, so the PrepareProposal handler can call
// Prove(sk, input) with the same code path that VerifyBeaconProof asserts
// against — this guarantees the emitted randomness matches what will later
// validate. Keeper must never sign, only verify.

import (
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"

	voied25519 "github.com/oasisprotocol/curve25519-voi/primitives/ed25519"
	"github.com/oasisprotocol/curve25519-voi/primitives/ed25519/extra/ecvrf"

	hubkeeper "github.com/TrueOpen/node/x/hub/keeper"
)

// randomnessReductionDomain namespaces the SHA-256 that reduces the raw
// 64-byte β from ECVRF_proof_to_hash down to the 32-byte randomness Hub stores
// raw and projects publicly as BeaconState.RandomnessHex. Node performs a
// deterministic domain-separated SHA-256
// reduction. The domain string is frozen and NEVER shared with any other hash
// context.
const randomnessReductionDomain = "TRUEOPEN_BEACON_RANDOMNESS_V1"

// BeaconProofCodecECVRFEdwards25519SHA512ELL2V1 is the frozen proof_codec
// string that identifies RFC 9381 §5.4.1.2 (suite 0x04, ELL2 encode-to-curve).
// The BeaconCarrier.proof_codec field emitted by PrepareProposal MUST use this
// exact literal; Hub's VerifyBeaconProof rejects any other codec.
const BeaconProofCodecECVRFEdwards25519SHA512ELL2V1 = "ecvrf_edwards25519_sha512_ell2_v1"

// ErrUnsupportedProofCodec is returned when a carrier arrives with a
// proof_codec value we do not implement.
var ErrUnsupportedProofCodec = errors.New("unsupported proof_codec")

// vrfVerifier implements hubkeeper.BeaconProofVerifier for
// ECVRF-EDWARDS25519-SHA512-ELL2. It is stateless and safe for concurrent use.
type vrfVerifier struct{}

// NewVRFVerifier returns the singleton verifier wired into the Node app's
// PreBlocker / ProcessProposal handlers.
func NewVRFVerifier() hubkeeper.BeaconProofVerifier {
	return vrfVerifier{}
}

// VerifyBeaconProof implements hubkeeper.BeaconProofVerifier.
//
// Verification succeeds only when both ecvrf.Verify accepts the proof and the
// resulting β equals the carrier's declared randomness. Any mismatch returns a
// non-nil error and the beacon is rejected.
func (vrfVerifier) VerifyBeaconProof(codec string, pubkey, input, proof, randomness []byte) error {
	if codec != BeaconProofCodecECVRFEdwards25519SHA512ELL2V1 {
		return fmt.Errorf("%w: got %q, want %q", ErrUnsupportedProofCodec, codec, BeaconProofCodecECVRFEdwards25519SHA512ELL2V1)
	}
	if len(pubkey) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid proposer pubkey length: got %d, want %d", len(pubkey), ed25519.PublicKeySize)
	}
	if len(proof) == 0 {
		return errors.New("empty proof")
	}
	if len(randomness) == 0 {
		return errors.New("empty randomness")
	}

	// oasis ecvrf uses its own ed25519.PublicKey type; it is a []byte alias
	// wire-compatible with crypto/ed25519.PublicKey. Convert without copying.
	ok, betaRaw := ecvrf.Verify(voied25519.PublicKey(pubkey), proof, input)
	if !ok {
		return errors.New("ecvrf proof rejected")
	}
	beta := reduceRandomness(betaRaw)
	if !constantTimeEqualBytes(beta, randomness) {
		return errors.New("randomness mismatch: proof verifies but declared β differs")
	}
	return nil
}

// Prove is the Node-only helper the PrepareProposal handler calls to build a
// fresh BeaconCarrier. It is intentionally NOT part of the
// hubkeeper.BeaconProofVerifier interface — Keeper must never sign, only verify.
//
// Returns (proof, randomness). Both are the raw bytes that will be hex-encoded
// into carrier.ProofHex and carrier.RandomnessHex.
func Prove(sk ed25519.PrivateKey, input []byte) (proof, randomness []byte, err error) {
	if len(sk) != ed25519.PrivateKeySize {
		return nil, nil, fmt.Errorf("invalid proposer privkey length: got %d, want %d", len(sk), ed25519.PrivateKeySize)
	}
	if len(input) == 0 {
		return nil, nil, errors.New("empty vrf input")
	}
	// Same []byte-compatible conversion for the private key.
	proof = ecvrf.Prove(voied25519.PrivateKey(sk), input)
	betaRaw, err := ecvrf.ProofToHash(proof)
	if err != nil {
		return nil, nil, fmt.Errorf("ecvrf proof-to-hash: %w", err)
	}
	return proof, reduceRandomness(betaRaw), nil
}

// reduceRandomness collapses the raw 64-byte ECVRF proof-to-hash output into
// the 32-byte randomness Hub stores. Deterministic and domain-separated.
func reduceRandomness(betaRaw []byte) []byte {
	h := sha256.New()
	h.Write([]byte(randomnessReductionDomain))
	h.Write([]byte{0x00})
	h.Write(betaRaw)
	sum := h.Sum(nil)
	return sum
}

// constantTimeEqualBytes compares two byte slices in constant time on the body.
func constantTimeEqualBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
