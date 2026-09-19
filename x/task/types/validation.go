package types

import (
	"fmt"
	"strings"
)

func requireCanonicalNonEmpty(fieldName, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("%s is required", fieldName)
	}
	if trimmed != value {
		return "", fmt.Errorf("%s must not contain leading or trailing whitespace", fieldName)
	}
	if strings.ContainsRune(value, '\x00') {
		return "", fmt.Errorf("%s must not contain NUL", fieldName)
	}
	return value, nil
}
