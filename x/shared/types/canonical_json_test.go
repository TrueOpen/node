package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanonicalJSONV1(t *testing.T) {
	encoded, err := CanonicalJSONV1(map[string]any{
		"z": uint64(7),
		"a": map[string]any{"text": "<&", "enabled": true},
		"m": []string{"b", "a"},
	})
	require.NoError(t, err)
	require.Equal(t, `{"a":{"enabled":true,"text":"<&"},"m":["b","a"],"z":7}`, string(encoded))

	_, err = CanonicalJSONV1(map[string]any{"bad": 1.5})
	require.ErrorContains(t, err, "not allowed")
	_, err = CanonicalJSONV1(map[string]any{"bad": nil})
	require.ErrorContains(t, err, "null")
}
