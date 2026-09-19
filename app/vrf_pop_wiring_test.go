package app

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

// The Hub declares VrfPoPVerifier and refuses to register a VRF key when nothing
// supplies it, so §9.3a is only actually reachable if app wiring provides one.
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
