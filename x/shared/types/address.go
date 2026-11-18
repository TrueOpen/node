package types

import (
	"fmt"
	"strings"

	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/types/bech32"
)

type CanonicalAddressCodec interface {
	StringToBytes(string) ([]byte, error)
	BytesToString([]byte) (string, error)
}

// CanonicalBech32Address validates a canonical encoding with its supplied prefix.
// Callers still enforce their domain's address width and allowed prefix.
func CanonicalBech32Address(field, value string) ([]byte, error) {
	hrp, _, err := bech32.DecodeAndConvert(value)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", field, err)
	}
	raw, _, err := CanonicalAddress(addresscodec.NewBech32Codec(hrp), field, value)
	return raw, err
}

func CanonicalAddress(codec CanonicalAddressCodec, field, value string) ([]byte, string, error) {
	trimmed := strings.TrimSpace(value)
	if codec == nil {
		return nil, "", fmt.Errorf("%s address codec is required", field)
	}
	if trimmed == "" {
		return nil, "", fmt.Errorf("%s is required", field)
	}
	if trimmed != value {
		return nil, "", fmt.Errorf("%s must not contain leading or trailing whitespace", field)
	}
	raw, err := codec.StringToBytes(value)
	if err != nil {
		return nil, "", fmt.Errorf("invalid %s: %w", field, err)
	}
	canonical, err := codec.BytesToString(raw)
	if err != nil {
		return nil, "", fmt.Errorf("invalid %s: %w", field, err)
	}
	if canonical != value {
		return nil, "", fmt.Errorf("%s must be canonical", field)
	}
	return raw, canonical, nil
}
