package keeper_test

import (
	"bytes"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	proto "github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func TestSessionTerminalSummaryGenesisStoresRawOwnerAndRoundTrips(t *testing.T) {
	f := initFixture(t)
	genesis := tasktypes.DefaultGenesis()
	summary := tasktypes.SessionTerminalSummaryState{
		SessionId:                 bytes.Repeat([]byte{0x5e}, tasktypes.Hash32Len),
		OwnerUserAddress:          genesisUser,
		FinalNextExpectedSequence: 1,
		SequenceCount:             1,
		SequenceRoot:              bytes.Repeat([]byte{0x5f}, tasktypes.Hash32Len),
		CancelledCount:            1,
		ClosedHeight:              5,
		CompactedHeight:           6,
	}
	genesis.SessionTerminalSummaries = []tasktypes.SessionTerminalSummaryState{summary}
	require.NoError(t, genesis.Validate())
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	stored, err := f.keeper.SessionTerminalSummary.Get(f.ctx, tasktypes.NewSessionKey(summary.SessionId))
	require.NoError(t, err)
	raw, err := sdk.AccAddressFromBech32(summary.OwnerUserAddress)
	require.NoError(t, err)
	require.Equal(t, []byte(raw), stored.OwnerUserAddress)
	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(genesis, exported))
	restarted := initFixture(t)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	reexported, err := restarted.keeper.ExportGenesis(restarted.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(exported, reexported))
}

func TestSessionNonceGenesisUsesRawAddressKeyAndRejectsMismatchedValue(t *testing.T) {
	f := initFixture(t)
	genesis := tasktypes.DefaultGenesis()
	genesis.SessionNonces = []tasktypes.SessionNonceState{{UserAddress: genesisUser, NextSessionNonce: 7}}
	require.NoError(t, genesis.Validate())
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	raw, err := sdk.AccAddressFromBech32(genesisUser)
	require.NoError(t, err)
	key := tasktypes.AddrKey(raw)
	stored, err := f.keeper.SessionNonce.Get(f.ctx, key)
	require.NoError(t, err)
	require.Equal(t, []byte(raw), stored.UserAddress)
	iter, err := f.keeper.SessionNonce.Iterate(f.ctx, nil)
	require.NoError(t, err)
	entry, err := iter.KeyValue()
	require.NoError(t, err)
	require.Equal(t, []byte(raw), []byte(entry.Key))
	require.NoError(t, iter.Close())
	state, err := f.keeper.ReadSessionNonce(f.ctx, genesisUser)
	require.NoError(t, err)
	require.Equal(t, genesis.SessionNonces[0], state)
	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(genesis, exported))

	stored.UserAddress = bytes.Repeat([]byte{0x7f}, len(raw))
	require.NoError(t, f.keeper.SessionNonce.Set(f.ctx, key, stored))
	_, err = f.keeper.ReadSessionNonce(f.ctx, genesisUser)
	require.ErrorContains(t, err, "session nonce key/address mismatch")
	_, err = f.keeper.ExportGenesis(f.ctx)
	require.ErrorContains(t, err, "session nonce key/address mismatch")
}

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
