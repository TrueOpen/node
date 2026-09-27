package types_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

func TestBuilderSetIDByteCapIsValidatedAndHashed(t *testing.T) {
	params := hubtypes.DefaultHubParams()
	require.Equal(t, uint32(128), params.Builder.MaxBuilderSetIdBytes)
	require.NoError(t, params.Validate())
	baseline, err := hubtypes.HubParamsHash("trueopen-test", 1, params)
	require.NoError(t, err)
	params.Builder.MaxBuilderSetIdBytes = 129
	require.NoError(t, params.Validate())
	changed, err := hubtypes.HubParamsHash("trueopen-test", 1, params)
	require.NoError(t, err)
	require.False(t, bytes.Equal(baseline, changed))
	params.Builder.MaxBuilderSetIdBytes = 0
	require.ErrorContains(t, params.Validate(), "builder parameters are invalid")
}

func TestGenesisRejectsBuilderSetIDAboveByteCap(t *testing.T) {
	genesis := hubtypes.DefaultGenesis()
	genesis.BuilderSets = []hubtypes.BuilderSetState{{
		BuilderSetVersion: 1,
		BuilderSetId:      strings.Repeat("x", int(genesis.Params.Builder.MaxBuilderSetIdBytes)+1),
	}}
	require.ErrorContains(t, genesis.Validate(), "invalid header")
}
