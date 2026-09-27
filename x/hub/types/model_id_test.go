package types

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeriveModelIDV1MatchesWireRC1(t *testing.T) {
	address, err := hex.DecodeString("1a642f0e3c3af545e7acbd38b07251b3990914f1")
	require.NoError(t, err)
	modelID, err := DeriveModelIDV1("trueopen-golden-1", "HUGGINGFACE", "trueopen/golden-model", address)
	require.NoError(t, err)
	require.Equal(t, "c65241d19b257f935ddea99ea59a19175b4b29751e259853d403fe59f04f4e4f", hex.EncodeToString(modelID))

	for _, input := range []struct {
		chainID, provider, repoID string
		address                   []byte
	}{
		{"", "HUGGINGFACE", "trueopen/golden-model", address},
		{"trueopen-golden-1", "huggingface", "trueopen/golden-model", address},
		{"trueopen-golden-1", "OCI", "trueopen/golden-model", address},
		{"trueopen-golden-1", "HUGGINGFACE", "trueopen/golden/model", address},
		{"trueopen-golden-1", "HUGGINGFACE", "trueopen/golden model", address},
		{"trueopen-golden-1", "HUGGINGFACE", "trueopen/golden-model", address[:19]},
	} {
		_, err := DeriveModelIDV1(input.chainID, input.provider, input.repoID, input.address)
		require.Error(t, err)
	}
}
