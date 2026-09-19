package types

import (
	"fmt"
	"strings"
)

type CanonicalAddressCodec interface {
	StringToBytes(string) ([]byte, error)
	BytesToString([]byte) (string, error)
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

// CanonicalOptionalAddress is CanonicalAddress for a field that a caller may
// legitimately leave unset, and it exists because several challenge effect
// kinds have no counterparty on one side: TREASURY_RESIDUAL and BOND_SLASH
// carry neither address, BOND_REFUND carries no source.
//
// An unset field yields nil bytes, which frames as a zero-length field. A set
// field must be fully canonical -- whitespace is rejected, never trimmed, so a
// digest can never be asked to decide that " addr" and "addr" are the same
// account. Absence stays distinguishable from presence because a decoded
// address is never zero-length.
func CanonicalOptionalAddress(codec CanonicalAddressCodec, field, value string) ([]byte, string, error) {
	if value == "" {
		return nil, "", nil
	}
	return CanonicalAddress(codec, field, value)
}
