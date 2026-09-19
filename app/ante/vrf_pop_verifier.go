package ante

import (
	"errors"
	"fmt"

	"github.com/oasisprotocol/curve25519-voi/primitives/ed25519"
	"github.com/oasisprotocol/curve25519-voi/primitives/ed25519/extra/ecvrf"

	hubkeeper "github.com/TrueOpen/node/x/hub/keeper"
)

// vrfPoPVerifier implements hubkeeper.VrfPoPVerifier for the same RFC 9381
// §5.4.1.2 suite the beacon uses. Keeping both on one suite is deliberate: a
// key proven under one construction and used under another would prove nothing
// about the key that actually signs beacons.
type vrfPoPVerifier struct{}

// NewVRFPoPVerifier returns the possession-proof verifier wired into the Hub
// keeper for MsgRegisterVrfKey.
func NewVRFPoPVerifier() hubkeeper.VrfPoPVerifier { return vrfPoPVerifier{} }

// VerifyVrfPossession accepts only a proof that verifies under the submitted
// public key over the caller-derived alpha. It deliberately does not look at
// the resulting beta: possession is the whole claim being checked here, and the
// randomness a key later produces is the beacon path's concern.
func (vrfPoPVerifier) VerifyVrfPossession(pubkey, alpha, proof []byte) error {
	if len(pubkey) != ed25519.PublicKeySize {
		return fmt.Errorf("vrf pubkey must be %d bytes, got %d", ed25519.PublicKeySize, len(pubkey))
	}
	if len(alpha) == 0 {
		return errors.New("vrf possession alpha must not be empty")
	}
	if len(proof) == 0 {
		return errors.New("vrf possession proof must not be empty")
	}
	public := ed25519.PublicKey(append([]byte(nil), pubkey...))
	if ok, _ := ecvrf.Verify_v10(public, proof, alpha); !ok {
		return errors.New("vrf possession proof does not verify under the submitted public key")
	}
	return nil
}
