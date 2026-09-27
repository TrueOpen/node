package types

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHubCanonicalSigningBytesRejectFieldBoundaryCollision(t *testing.T) {
	models := [][]byte{bytes.Repeat([]byte{1}, 32)}
	left := CanonicalDailySupportConfirmationSigningBytes(
		"chain|provider", []byte("address"), 1, 1, 100,
		models,
	)
	right := CanonicalDailySupportConfirmationSigningBytes(
		"chain", []byte("provider|address"), 1, 1, 100,
		models,
	)
	require.NotEqual(t, left, right)
	checked, err := CanonicalDailySupportConfirmationSigningBytesV1(
		"chain|provider", []byte("address"), 1, 1, 100,
		models,
	)
	require.NoError(t, err)
	require.Equal(t, left, checked)
	modelsHash, err := CanonicalSupportedModelsHashV1(models)
	require.NoError(t, err)
	require.Equal(t, CanonicalSupportedModelsHash(models), modelsHash)
}

func TestHubCanonicalSupportModelsMatchesWireRC1(t *testing.T) {
	models := [][]byte{bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)}
	hash, err := CanonicalSupportedModelsHashV1(models)
	require.NoError(t, err)
	require.Equal(t, "e41be74b9a7cea19e5c2f7b70daf8701f0f4204736511c80f50dd321cb07a44c", hex.EncodeToString(hash))

	address, err := hex.DecodeString("a1a2a3a4a5a6a7a8a9aaabacadaeafb0b1b2b3b4")
	require.NoError(t, err)
	signingBytes, err := CanonicalDailySupportConfirmationSigningBytesV1("trueopen-fixture-1", address, 42, 7, 2000, models)
	require.NoError(t, err)
	require.Equal(t, "5359a51fa257665864c0d99ecb477f28125e3902f842fe25a369301ef9902863", hex.EncodeToString(signingBytes))
}

func TestHubCanonicalSupportModelsRejectsNonCanonicalIDs(t *testing.T) {
	first := bytes.Repeat([]byte{1}, 32)
	second := bytes.Repeat([]byte{2}, 32)
	for _, models := range [][][]byte{
		{first, first},
		{second, first},
		{first[:31]},
	} {
		_, err := CanonicalSupportedModelsHashV1(models)
		require.Error(t, err)
		_, err = CanonicalDailySupportConfirmationSigningBytesV1("chain", []byte("address"), 1, 1, 100, models)
		require.Error(t, err)
	}
}
