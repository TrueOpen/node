package keeper

import (
	"testing"

	"github.com/TrueOpen/node/x/task/types"
	"github.com/stretchr/testify/require"
)

// The dispatch slice includes the Session and sequence facts its claims depend
// on. Other module primaries stay outside these focused projection tests.
func exportDispatchFixtureGenesis(t *testing.T, f *dispatchFixture) *types.GenesisState {
	t.Helper()
	genesis := types.DefaultGenesis()
	stream, err := f.keeper.ReadStream(f.ctx, f.sessionID)
	require.NoError(t, err)
	genesis.Streams = []types.StreamState{stream}
	genesis.OrderSequences, err = collectMapValues(f.ctx, f.keeper.OrderSequence)
	require.NoError(t, err)
	require.NoError(t, f.keeper.exportDispatchGenesis(f.ctx, genesis))
	return genesis
}

func restoreDispatchFixtureGenesis(t *testing.T, f *dispatchFixture, genesis types.GenesisState, height uint64) {
	t.Helper()
	for _, stream := range genesis.Streams {
		require.NoError(t, f.keeper.WriteStream(f.ctx, stream.SessionId, stream))
	}
	for _, row := range genesis.OrderSequences {
		require.NoError(t, f.keeper.OrderSequence.Set(f.ctx, types.NewOrderSequenceStateKey(row.SessionId, row.OrderSequence), row))
	}
	require.NoError(t, f.keeper.initDispatchGenesis(f.ctx, genesis, height))
}
