package types

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/evm/crypto/ethsecp256k1"
	dcrsecp "github.com/decred/dcrd/dcrec/secp256k1/v4"
)

const (
	SHA256HexLength            = 64
	CompressedSecp256k1KeySize = 33
)

// ValidateSHA256Hex returns the canonical lowercase sha256 hex string used by
// metadata and endpoint commitments.
func ValidateSHA256Hex(fieldName, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("%s is required", fieldName)
	}
	if trimmed != value {
		return "", fmt.Errorf("%s must not contain leading or trailing whitespace", fieldName)
	}
	if len(trimmed) != SHA256HexLength {
		return "", fmt.Errorf("%s must be %d lowercase hex chars", fieldName, SHA256HexLength)
	}
	if strings.ToLower(trimmed) != trimmed {
		return "", fmt.Errorf("%s must be lowercase hex", fieldName)
	}
	raw, err := hex.DecodeString(trimmed)
	if err != nil {
		return "", fmt.Errorf("%s must be hex: %w", fieldName, err)
	}
	if len(raw) != 32 {
		return "", fmt.Errorf("%s must decode to 32 bytes", fieldName)
	}
	return trimmed, nil
}

// ParseSecp256k1PubKeyHex parses the canonical compressed secp256k1 pubkey
// encoding used by Builder and Cortex Node service-key registrations.
func ParseSecp256k1PubKeyHex(fieldName, value string) (*secp256k1.PubKey, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, fmt.Errorf("%s is required", fieldName)
	}
	if trimmed != value {
		return nil, fmt.Errorf("%s must not contain leading or trailing whitespace", fieldName)
	}
	if strings.ToLower(trimmed) != trimmed {
		return nil, fmt.Errorf("%s must be lowercase hex", fieldName)
	}
	raw, err := hex.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("%s must be hex: %w", fieldName, err)
	}
	return ParseSecp256k1PubKeyBytes(fieldName, raw)
}

// ValidateCurrentServiceKeyBinding is the single "current_service_pubkey must
// derive current_service_address" check for every participant type that carries a
// service key (CortexNodeState §6.4 and BuilderState §6.5).
//
// The check used to live on the shared ServiceKeyBindingState row. When that
// collection was removed the derivation was re-inlined on the cortex side only,
// so a Genesis document could bind a builder's stable identity to a service
// address nobody holds the private key for: every later signature check derives
// the signer from current_service_pubkey, so such a builder is either permanently
// unable to act or, worse, acts under an address the chain attributes to someone
// else. Both participant types must go through this one implementation.
func ValidateCurrentServiceKeyBinding(scope, operatorAddress, serviceAddress string, servicePubkey []byte) error {
	canonicalServiceAddress, err := requireCanonicalNonEmpty(scope+" current_service_address", serviceAddress)
	if err != nil {
		return err
	}
	pubkey, err := ParseSecp256k1PubKeyBytes(scope+" current_service_pubkey", servicePubkey)
	if err != nil {
		return err
	}
	addressBytes, err := sdk.AccAddressFromBech32(canonicalServiceAddress)
	if err != nil || !bytes.Equal(ServicePubKeyAddress(pubkey), addressBytes) {
		return fmt.Errorf("%s %s service pubkey does not derive current_service_address", scope, operatorAddress)
	}
	return nil
}

// ServicePubKeyAddress derives the account bytes used by Builder and Cortex
// service identities. Service keys follow Ethereum's Keccak-256 address rule;
// the Cosmos SDK secp256k1 SHA-256/RIPEMD-160 address is deliberately rejected.
func ServicePubKeyAddress(pubkey *secp256k1.PubKey) []byte {
	if pubkey == nil {
		return nil
	}
	address := (&ethsecp256k1.PubKey{Key: pubkey.Bytes()}).Address()
	return append([]byte(nil), address...)
}

// ParseSecp256k1PubKeyBytes validates the canonical compressed wire encoding.
func ParseSecp256k1PubKeyBytes(fieldName string, raw []byte) (*secp256k1.PubKey, error) {
	if len(raw) != CompressedSecp256k1KeySize {
		return nil, fmt.Errorf("%s must be a %d-byte compressed secp256k1 pubkey", fieldName, CompressedSecp256k1KeySize)
	}
	if raw[0] != 0x02 && raw[0] != 0x03 {
		return nil, fmt.Errorf("%s must be a compressed secp256k1 pubkey", fieldName)
	}
	if _, err := dcrsecp.ParsePubKey(raw); err != nil {
		return nil, fmt.Errorf("%s must be a valid compressed secp256k1 pubkey: %w", fieldName, err)
	}
	return &secp256k1.PubKey{Key: append([]byte(nil), raw...)}, nil
}
