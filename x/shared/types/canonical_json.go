package types

import (
	"bytes"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// CanonicalJSONV1 encodes the deliberately small JSON value set used by
// consensus contracts. Objects must be map[string]any so their keys are
// emitted in UTF-8 byte order by encoding/json.
func CanonicalJSONV1(value any) ([]byte, error) {
	if err := validateCanonicalJSONValue(value); err != nil {
		return nil, err
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("encode canonical JSON: %w", err)
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'}), nil
}

func validateCanonicalJSONValue(value any) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if !utf8.ValidString(key) {
				return fmt.Errorf("canonical JSON object key must be valid UTF-8")
			}
			if err := validateCanonicalJSONValue(child); err != nil {
				return fmt.Errorf("canonical JSON field %q: %w", key, err)
			}
		}
		return nil
	case []any:
		for i, child := range typed {
			if err := validateCanonicalJSONValue(child); err != nil {
				return fmt.Errorf("canonical JSON array item %d: %w", i, err)
			}
		}
		return nil
	case []string:
		for i, child := range typed {
			if !utf8.ValidString(child) {
				return fmt.Errorf("canonical JSON array item %d must be valid UTF-8", i)
			}
		}
		return nil
	case string:
		if !utf8.ValidString(typed) {
			return fmt.Errorf("canonical JSON string must be valid UTF-8")
		}
		return nil
	case bool, uint8, uint16, uint32, uint64, uint, int8, int16, int32:
		return nil
	case int:
		if typed < 0 {
			return fmt.Errorf("canonical JSON integer must be non-negative")
		}
		return nil
	case int64:
		if typed < 0 {
			return fmt.Errorf("canonical JSON integer must be non-negative")
		}
		return nil
	case nil:
		return fmt.Errorf("canonical JSON null is not allowed")
	default:
		return fmt.Errorf("canonical JSON value type %T is not allowed", value)
	}
}
