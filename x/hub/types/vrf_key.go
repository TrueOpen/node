package types

import (
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// VrfPubkeyLen is the Ed25519 point width §9.3a step 2 requires.
const VrfPubkeyLen = 32

// VrfKeyPoPDigest is the §1.4 `TRUEOPEN_VRF_KEY_POP_V1` preimage: the ECVRF alpha a
// validator must produce a possession proof over.
//
// The nonce is inside it, which is what makes step 4's replay rule decidable —
// the same nonce with the same key is the same proof, and the same nonce with a
// different key is a different alpha and therefore a conflict rather than a
// silent overwrite.
func VrfKeyPoPDigest(chainID, operatorAddress string, vrfPubkey []byte, authorizationNonce uint64) ([32]byte, error) {
	if chainID == "" {
		return [32]byte{}, fmt.Errorf("chain_id must not be empty")
	}
	operator, err := bridgeAddressBytes("operator_address", operatorAddress)
	if err != nil {
		return [32]byte{}, err
	}
	if len(vrfPubkey) != VrfPubkeyLen {
		return [32]byte{}, fmt.Errorf("vrf_pubkey must be exactly %d bytes, got %d", VrfPubkeyLen, len(vrfPubkey))
	}
	if authorizationNonce == 0 {
		return [32]byte{}, fmt.Errorf("vrf_authorization_nonce must be greater than zero")
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainVRFKeyPoPV1)).Raw(
		[]byte(chainID), operator, append([]byte(nil), vrfPubkey...), shared.Uint64BE(authorizationNonce),
	).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

// ValidateVrfKeyState is the Genesis/import shape check. §9.3a keeps the raw VRF
// private key out of every message, state and query, so the only thing stored is
// the public point and the epoch it becomes authoritative in.
func ValidateVrfKeyState(state VrfKeyState) error {
	if state.OperatorAddress == "" {
		return fmt.Errorf("vrf key operator_address is required")
	}
	if len(state.ActiveVrfPubkey) != 0 && len(state.ActiveVrfPubkey) != VrfPubkeyLen {
		return fmt.Errorf("vrf key active_vrf_pubkey must be %d bytes", VrfPubkeyLen)
	}
	pendingKey := state.XPendingVrfPubkey != nil
	pendingEpoch := state.XPendingFromEpoch != nil
	// §9.3a step 5 writes the pending key and its activation epoch together, so a
	// half-present pending would describe a rotation nothing can schedule.
	if pendingKey != pendingEpoch {
		return fmt.Errorf("vrf key pending pubkey and pending epoch must be both present or both absent")
	}
	if pendingKey {
		if len(state.GetPendingVrfPubkey()) != VrfPubkeyLen {
			return fmt.Errorf("vrf key pending_vrf_pubkey must be %d bytes", VrfPubkeyLen)
		}
		if state.GetPendingFromEpoch() <= state.ActiveFromEpoch {
			return fmt.Errorf("vrf key pending_from_epoch must be after active_from_epoch")
		}
	}
	if state.VrfAuthorizationNonce == 0 {
		return fmt.Errorf("vrf key vrf_authorization_nonce must be greater than zero")
	}
	return nil
}
