package types

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMinStakeGracePeriodIsValidatedAndCommitted(t *testing.T) {
	params := DefaultHubParams()
	require.Equal(t, uint64(100), params.Service.MinStakeGracePeriodBlocks)
	require.NoError(t, params.Validate())

	base, err := HubParamsHash("trueopen-params-grace-test", 1, params)
	require.NoError(t, err)
	params.Service.MinStakeGracePeriodBlocks++
	require.NoError(t, params.Validate())
	changed, err := HubParamsHash("trueopen-params-grace-test", 1, params)
	require.NoError(t, err)
	require.False(t, bytes.Equal(base, changed))

	params.Service.MinStakeGracePeriodBlocks = 0
	require.ErrorContains(t, params.Validate(), "service parameters are invalid")
}
