package types

import "fmt"

const modelIDGrammar = "^[a-z0-9][a-z0-9_-]{0,127}$"

// ValidateModelID enforces one canonical identifier that is safe as both a URI
// path segment and a single NATS subject token.
func ValidateModelID(value string) error {
	if len(value) == 0 || len(value) > 128 || !isModelIDAlphanumeric(value[0]) {
		return fmt.Errorf("model_id must match %s", modelIDGrammar)
	}
	for i := 1; i < len(value); i++ {
		if !isModelIDAlphanumeric(value[i]) && value[i] != '_' && value[i] != '-' {
			return fmt.Errorf("model_id must match %s", modelIDGrammar)
		}
	}
	return nil
}

func isModelIDAlphanumeric(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}
