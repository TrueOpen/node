package types

import (
	"fmt"
	"strconv"
)

// NewAmount converts checked state arithmetic back to the canonical wire form.
func NewAmount(value uint64) Amount {
	return Amount{AtomicUnits: strconv.FormatUint(value, 10)}
}

// ParseAmount converts the canonical unsigned decimal wire form to the
// checked uint64 representation used by bounded consensus arithmetic.
func ParseAmount(amount Amount) (uint64, error) {
	value := amount.AtomicUnits
	if value == "" {
		return 0, fmt.Errorf("amount atomic_units is required")
	}
	if value != "0" && value[0] == '0' {
		return 0, fmt.Errorf("amount atomic_units must not contain leading zeroes")
	}
	for i := range value {
		if value[i] < '0' || value[i] > '9' {
			return 0, fmt.Errorf("amount atomic_units must be canonical unsigned decimal")
		}
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("amount atomic_units exceeds uint64: %w", err)
	}
	return parsed, nil
}

func NewSignedAmount(value int64) SignedAmount {
	return SignedAmount{AtomicUnits: strconv.FormatInt(value, 10)}
}

func ParseSignedAmount(amount SignedAmount) (int64, error) {
	value := amount.AtomicUnits
	if value == "" {
		return 0, fmt.Errorf("signed amount atomic_units is required")
	}
	if value == "-0" || value[0] == '+' || (value[0] == '0' && len(value) > 1) || (value[0] == '-' && len(value) > 2 && value[1] == '0') {
		return 0, fmt.Errorf("signed amount atomic_units is not canonical")
	}
	start := 0
	if value[0] == '-' {
		if len(value) == 1 {
			return 0, fmt.Errorf("signed amount atomic_units is not canonical")
		}
		start = 1
	}
	for i := start; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return 0, fmt.Errorf("signed amount atomic_units must be canonical decimal")
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("signed amount atomic_units exceeds int64: %w", err)
	}
	return parsed, nil
}
