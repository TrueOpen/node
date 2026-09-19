package types

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTokenIDsHashV1PublishedVectors(t *testing.T) {
	input, inputSize, err := InputTokenIDsHashV1([]uint32{1, 256})
	require.NoError(t, err)
	require.Equal(t, uint64(12), inputSize)
	require.Equal(t, "5ab982d47fc3c3b0a07e401f1c040c7fe2ef79ae93d0e44c50afe7d07a3ae901", hex.EncodeToString(input[:]))

	generated, generatedSize, err := GeneratedTokenIDsHashV1([]uint32{2, 257, 65_535})
	require.NoError(t, err)
	require.Equal(t, uint64(16), generatedSize)
	require.Equal(t, "057a76f605b3ba2fa26a92e379b5f0f802d97e379d4efc3a34e287f8e9e05efd", hex.EncodeToString(generated[:]))

	reordered, _, err := GeneratedTokenIDsHashV1([]uint32{257, 2, 65_535})
	require.NoError(t, err)
	require.NotEqual(t, generated, reordered)
}
