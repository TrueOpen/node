package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPhase0ParamsDecodeIgnoresRetiredEvmChainID guards the upgrade path: params
// written by a binary that still had Phase0ParamsV1.evm_chain_id (field 11)
// stay in the store until rewritten, so a node started on that data must decode
// them instead of failing at the first block that reads Hub params.
func TestPhase0ParamsDecodeIgnoresRetiredEvmChainID(t *testing.T) {
	want := DefaultHubParams().Phase0
	raw, err := want.Marshal()
	require.NoError(t, err)

	const retiredEvmChainIDTag = 11<<3 | 0                                                // field 11, varint
	legacy := append(append([]byte(nil), raw...), retiredEvmChainIDTag, 0xE9, 0xF4, 0x01) // 31337

	var got Phase0ParamsV1
	require.NoError(t, got.Unmarshal(legacy))
	require.Equal(t, want.BusinessDenom, got.BusinessDenom)
	require.Equal(t, want.MaxBypassGasPerBlock, got.MaxBypassGasPerBlock)

	params := DefaultHubParams()
	params.Phase0 = got
	require.NoError(t, params.Validate())
}
