package keeper

import (
	"fmt"

	errorsmod "cosmossdk.io/errors"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

const serviceIdentitySchemaVersion = uint32(1)

func (k Keeper) requireCanonicalAddress(fieldName, value string) ([]byte, string, error) {
	return shared.CanonicalAddress(k.addressCodec, fieldName, value)
}

func requireParticipantType(participantType shared.ParticipantType) error {
	switch participantType {
	case shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, shared.ParticipantType_PARTICIPANT_TYPE_BUILDER:
		return nil
	default:
		return fmt.Errorf("participant_type must be CORTEX or BUILDER")
	}
}

// participantTypeName is the only enum -> string projection of
// ParticipantType. The string form exists purely because the cross-module
// keeper boundary is still positional/stringly typed (A-15b); since A-15a it no
// longer reaches any store key, hash preimage or proto field, so it is never a
// second closed enum.
func participantTypeName(participantType shared.ParticipantType) (string, error) {
	switch participantType {
	case shared.ParticipantType_PARTICIPANT_TYPE_CORTEX:
		return shared.ParticipantTypeCortexNode, nil
	case shared.ParticipantType_PARTICIPANT_TYPE_BUILDER:
		return shared.ParticipantTypeBuilder, nil
	default:
		return "", fmt.Errorf("participant_type must be CORTEX or BUILDER")
	}
}

// participantTypeFromName is the strict inverse of participantTypeName. It
// accepts exactly the two §9.6b enum names; the previous implementation also
// accepted a legacy "CORTEX" alias for the same enum value, which meant one
// participant had two spellings inside consensus keys.
func participantTypeFromName(value string) (shared.ParticipantType, error) {
	switch value {
	case shared.ParticipantTypeCortexNode:
		return shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, nil
	case shared.ParticipantTypeBuilder:
		return shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, nil
	default:
		return shared.ParticipantType_PARTICIPANT_TYPE_UNSPECIFIED, fmt.Errorf("participant_type must be CORTEX or BUILDER")
	}
}

func checkedAddUint32(a, b uint32) (uint32, error) {
	value, overflow := shared.CheckedAddUint32(a, b)
	if overflow {
		return 0, fmt.Errorf("uint32 addition overflow")
	}
	return value, nil
}

func parseServicePubKey(fieldName string, value []byte) (*secp256k1.PubKey, error) {
	if len(value) != secp256k1.PubKeySize {
		return nil, fmt.Errorf("%s must be exactly %d bytes", fieldName, secp256k1.PubKeySize)
	}
	pub, err := types.ParseSecp256k1PubKeyBytes(fieldName, value)
	if err != nil {
		return nil, err
	}
	return pub, nil
}

func verifyServiceKeyProof(pub *secp256k1.PubKey, digest, proof []byte) error {
	if len(proof) != 64 {
		return fmt.Errorf("service_key_proof must be exactly 64 bytes")
	}
	if err := types.VerifyStrictSecp256k1Digest(pub, digest, proof); err != nil {
		return errorsmod.Wrap(types.ErrInvalidSignature, fmt.Sprintf("invalid service key proof-of-possession: %s", err))
	}
	return nil
}

func validateServiceKeyStatus(status types.ServiceKeyStatus) error {
	switch status {
	case types.ServiceKeyStatus_SERVICE_KEY_STATUS_ACTIVE, types.ServiceKeyStatus_SERVICE_KEY_STATUS_REVOKED:
		return nil
	default:
		return fmt.Errorf("invalid service key status %s", status.String())
	}
}
