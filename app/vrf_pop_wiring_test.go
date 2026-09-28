package app

import (
	"bytes"
	"crypto/ed25519"
	"testing"

	"github.com/stretchr/testify/require"

	nodeante "github.com/TrueOpen/node/app/ante"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

// The Hub declares VrfPoPVerifier and refuses to register a VRF key when nothing
// supplies it, so VRF key registration is only actually reachable if app wiring provides one.
// Without this provider RegisterVrfKey is permanently FailedPrecondition and the
// whole rotation path is dead code that still compiles.
func TestVrfPoPVerifierIsProvidedAndVerifies(t *testing.T) {
	verifier := ProvideVrfPoPVerifier()
	require.NotNil(t, verifier)

	// A well-formed-looking but bogus proof must be rejected, which is what proves
	// the provider returns a real ECVRF check rather than an accept-all stub.
	pubkey := bytes.Repeat([]byte{0x02}, hubtypes.VrfPubkeyLen)
	alpha := bytes.Repeat([]byte{0x03}, 32)
	require.Error(t, verifier.VerifyVrfPossession(pubkey, alpha, bytes.Repeat([]byte{0x04}, 80)))
	require.Error(t, verifier.VerifyVrfPossession(pubkey, alpha, nil))
}

// TestVrfPoPVerifierAcceptsAGenuineProof pins the other half of "a real ECVRF
// check": the same Prove this repo's own vrf-pop CLI and FileVrfProposerSigner
// use must verify here. Only checking rejection (above) let Prove and
// VerifyVrfPossession run on two different ECVRF suite versions (final RFC
// 9381 vs. the withdrawn IETF draft v10, which use different domain
// separators) without either test noticing: every rejection test still
// passed, and no genuine key could ever complete MsgRegisterVrfKey.
func TestVrfPoPVerifierAcceptsAGenuineProof(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	require.NoError(t, err)
	alpha := bytes.Repeat([]byte{0x03}, 32)

	proof, _, err := nodeante.Prove(priv, alpha)
	require.NoError(t, err)

	verifier := ProvideVrfPoPVerifier()
	require.NoError(t, verifier.VerifyVrfPossession(pub, alpha, proof))
}
