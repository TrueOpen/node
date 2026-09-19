package keeper_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func TestSessionGenesisRoundTripAllowsCancelledSequenceWithoutTaskID(t *testing.T) {
	f := initFixture(t)
	genesis := tasktypes.DefaultGenesis()
	sessionID := bytes.Repeat([]byte{0x4c}, tasktypes.Hash32Len)
	genesis.Streams = []tasktypes.StreamState{{
		SessionId: sessionID, OwnerUserAddress: genesisUser, NextExpectedSequence: 1,
		LastActiveHeight: 5, Status: tasktypes.SessionStatus_SESSION_STATUS_ACTIVE,
	}}
	genesis.OrderSequences = []tasktypes.OrderSequenceState{{
		SessionId: sessionID, OrderSequence: 0,
		Status: tasktypes.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_CANCELLED, CancelledHeight: 5,
	}}
	require.NoError(t, genesis.Validate())
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
}

func TestSessionGenesisRoundTripAllowsZeroBasedTaskSequence(t *testing.T) {
	f := initFixture(t)
	genesis := taskGenesisV1(t, genesisChainID(f))
	genesis.Streams[0].NextExpectedSequence = 1
	genesis.OrderSequences[0].OrderSequence = 0
	genesis.TaskCores[0].OrderSequence = 0

	require.NoError(t, genesis.Validate())
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	require.Equal(t, uint64(0), exported.TaskCores[0].OrderSequence)
	require.Equal(t, uint64(0), exported.OrderSequences[0].OrderSequence)
}

func TestSessionGenesisRejectsDoneOrEmptyRootPruneCursor(t *testing.T) {
	for _, tc := range []struct {
		name  string
		next  uint64
		root  []byte
		match string
	}{
		{name: "done", next: 1, root: make([]byte, tasktypes.Hash32Len), match: "must remain before"},
		{name: "empty root", next: 0, root: nil, match: "must be 32 bytes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := initFixture(t)
			genesis := tasktypes.DefaultGenesis()
			sessionID := bytes.Repeat([]byte{0x4d}, tasktypes.Hash32Len)
			genesis.Streams = []tasktypes.StreamState{{
				SessionId: sessionID, OwnerUserAddress: genesisUser, NextExpectedSequence: 1,
				LastActiveHeight: 5, Status: tasktypes.SessionStatus_SESSION_STATUS_CLOSED,
			}}
			genesis.SessionHistoryPruneCursors = []tasktypes.SessionHistoryPruneCursorState{{
				SessionId: sessionID, NextOrderSequence: tc.next, RollingSequenceRoot: tc.root,
			}}
			require.ErrorContains(t, f.keeper.InitGenesis(f.ctx, *genesis), tc.match)
		})
	}
}
