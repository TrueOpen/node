package types_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/task/types"
)

func TestSelectedTaskBuildersHashGoldenAndTamperResistance(t *testing.T) {
	taskID := bytes.Repeat([]byte{0x21}, types.Hash32Len)
	setHash := bytes.Repeat([]byte{0x22}, types.Hash32Len)
	builders := []string{
		sdk.AccAddress(bytes.Repeat([]byte{0xa3}, 20)).String(),
		sdk.AccAddress(bytes.Repeat([]byte{0xa4}, 20)).String(),
	}
	base, err := types.SelectedTaskBuildersHash("trueopen-test-1", taskID, "builder-set-1", setHash, builders)
	require.NoError(t, err)
	require.Equal(t, "880a9f04ab7b6d6ffcc5acf754b60726a56ec2aae2844b01c7a0d6585d4b0ca5", hex.EncodeToString(base))

	mutations := []struct {
		chainID      string
		taskID       []byte
		builderSetID string
		setHash      []byte
		builders     []string
	}{
		{"trueopen-test-2", taskID, "builder-set-1", setHash, builders},
		{"trueopen-test-1", bytes.Repeat([]byte{0x23}, types.Hash32Len), "builder-set-1", setHash, builders},
		{"trueopen-test-1", taskID, "builder-set-2", setHash, builders},
		{"trueopen-test-1", taskID, "builder-set-1", bytes.Repeat([]byte{0x24}, types.Hash32Len), builders},
		{"trueopen-test-1", taskID, "builder-set-1", setHash, []string{builders[1], builders[0]}},
	}
	for _, mutation := range mutations {
		got, err := types.SelectedTaskBuildersHash(mutation.chainID, mutation.taskID, mutation.builderSetID, mutation.setHash, mutation.builders)
		require.NoError(t, err)
		require.NotEqual(t, base, got)
	}
}
