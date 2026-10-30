package types

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"math"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/wire/dispatch"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// Validate checks a pending replacement against its fixed activation and current identity.
func (s PendingServiceKeyRotationState) Validate(epochLength, currentNonce uint64, currentAddress string) error {
	if s.ParticipantType != shared.ParticipantType_PARTICIPANT_TYPE_CORTEX && s.ParticipantType != shared.ParticipantType_PARTICIPANT_TYPE_BUILDER {
		return fmt.Errorf("invalid pending rotation role")
	}
	if _, err := requireCanonicalNonEmpty("pending rotation operator", s.OperatorAddress); err != nil {
		return err
	}
	if currentNonce == 0 || currentNonce == math.MaxUint64 || s.CurrentServiceAuthorizationNonce != currentNonce || s.NextServiceAuthorizationNonce != currentNonce+1 {
		return fmt.Errorf("pending rotation current/next nonce mismatch")
	}
	pub, err := ParseSecp256k1PubKeyBytes("pending service pubkey", s.NewServicePubkey)
	if err != nil {
		return err
	}
	if sdk.AccAddress(ServicePubKeyAddress(pub)).String() == currentAddress {
		return fmt.Errorf("pending service key must differ from current")
	}
	if epochLength == 0 || !shared.IsBlockHeightV1(s.EffectiveHeight) {
		return fmt.Errorf("invalid pending rotation epoch/height")
	}
	start, _, err := shared.EpochHeightRange(shared.EpochForHeight(s.RegisteredHeight, epochLength), epochLength)
	if err != nil {
		return err
	}
	height, err := dispatch.ServiceKeyRotationHeight(s.RegisteredHeight, start, epochLength)
	if err != nil {
		return err
	}
	if s.EffectiveHeight != height {
		return fmt.Errorf("pending rotation does not match its fixed activation epoch")
	}
	return nil
}

// ServiceKeyNonceMatches resolves only the current or retained previous generation.
func ServiceKeyNonceMatches(current, previous, nonce uint64) bool {
	return nonce != 0 && (nonce == current || previous != 0 && nonce == previous)
}

// ValidatePreviousServiceKeyBinding checks the compact, single-generation proof identity.
func ValidatePreviousServiceKeyBinding(current uint64, currentPubkey, pubkey []byte, nonce, retired uint64) error {
	if len(pubkey) == 0 && nonce == 0 && retired == 0 {
		return nil
	}
	if current <= 1 || nonce != current-1 || !shared.IsBlockHeightV1(retired) || bytes.Equal(pubkey, currentPubkey) {
		return fmt.Errorf("invalid previous service key generation")
	}
	_, err := ParseSecp256k1PubKeyBytes("previous service pubkey", pubkey)
	return err
}

// ResolveProofKey selects proof authority without granting live submission permission.
func (s CurrentServiceKeySnapshot) ResolveProofKey(nonce uint64) (dispatch.ProofKeyBinding, error) {
	key := dispatch.ProofKeyBinding{ServiceAuthorizationNonce: s.AuthorizationNonce}
	if nonce != 0 && nonce == s.PreviousAuthorizationNonce {
		key.PublicKey = bytes.Clone(s.PreviousServicePubkey)
		key.ServiceAuthorizationNonce = nonce
		key.Status = dispatch.ProofKeyPrevious
		key.PreviousRetiredHeight = s.PreviousRetiredHeight
		if s.PreviousRetiredHeight == 0 {
			return dispatch.ProofKeyBinding{}, dispatch.ErrProofKeyUnavailable
		}
	} else {
		if s.AuthorizationNonce == 0 || nonce != 0 && nonce != s.AuthorizationNonce {
			return dispatch.ProofKeyBinding{}, dispatch.ErrProofKeyUnavailable
		}
		var err error
		key.PublicKey, err = hex.DecodeString(s.ServicePubkey)
		if err != nil || hex.EncodeToString(key.PublicKey) != s.ServicePubkey {
			return dispatch.ProofKeyBinding{}, fmt.Errorf("%w: noncanonical current proof key", dispatch.ErrProofKeyUnavailable)
		}
		switch s.Status {
		case ServiceKeyStatusActive:
			key.Status = dispatch.ProofKeyActive
		case ServiceKeyStatusRevoked:
			key.Status = dispatch.ProofKeyRevoked
		default:
			return dispatch.ProofKeyBinding{}, dispatch.ErrProofKeyUnavailable
		}
	}
	if _, err := ParseSecp256k1PubKeyBytes("service proof pubkey", key.PublicKey); err != nil {
		return dispatch.ProofKeyBinding{}, fmt.Errorf("%w: %w", dispatch.ErrProofKeyUnavailable, err)
	}
	return key, nil
}

// IsServiceKeyEvidenceResponsibility identifies holds that retain a proof generation.
func IsServiceKeyEvidenceResponsibility(kind ServiceKeyResponsibilityKind) bool {
	switch kind {
	case ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_BUS_OBJECTIVE_EVIDENCE,
		ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_WORKER_OUTPUT_EVIDENCE,
		ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_CHALLENGE_VERIFIER:
		return true
	default:
		return false
	}
}
