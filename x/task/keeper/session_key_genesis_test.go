package keeper_test

import (
	"bytes"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	proto "github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/keeper"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func TestSessionKeyRotationGenesisDoesNotReauthorizeAcceptedTask(t *testing.T) {
	f := initFixture(t)
	genesis := taskGenesisV1(t, genesisChainID(f))
	stream := &genesis.Streams[0]
	stream.XSessionKey = &tasktypes.StreamState_SessionKey{SessionKey: bytes.Repeat([]byte{0x41}, 20)}
	stream.SessionKeySetHeight = 2
	stream.SessionKeyExpiryHeight = 9
	stream.SessionKeySpent = shared.NewAmount(1000)
	stream.SessionKeySpendCap = shared.NewAmount(500)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(7))
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	ctx := sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(10))
	_, err := keeper.NewMsgServerImpl(f.keeper).UpdateSessionKey(ctx, &tasktypes.MsgUpdateSessionKey{
		SignerAddress: genesisUser, SessionId: stream.SessionId,
		Action: &tasktypes.MsgUpdateSessionKey_Clear{Clear: &tasktypes.ClearSessionKeyV1{}},
	})
	require.NoError(t, err, "expired keys can still be cleared")
	exported, err := f.keeper.ExportGenesis(ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	require.Equal(t, genesis.TaskCores, exported.TaskCores)
	require.Equal(t, genesis.TaskBudgets, exported.TaskBudgets)
	require.Equal(t, shared.NewAmount(1000), exported.Streams[0].SessionKeySpent)
	require.NotNil(t, exported.Streams[0].XSessionKey)
	require.NotNil(t, exported.Streams[0].GetPendingSessionKeyChange())
	restarted := initFixture(t)
	restarted.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(restarted.ctx).WithBlockHeight(11))
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	again, err := restarted.keeper.ExportGenesis(restarted.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(exported, again))
}

func TestSessionKeyGenesisRejectsFutureRegistrationWithoutWrites(t *testing.T) {
	f := initFixture(t)
	genesis := taskGenesisV1(t, genesisChainID(f))
	stream := &genesis.Streams[0]
	stream.XSessionKey = &tasktypes.StreamState_SessionKey{SessionKey: bytes.Repeat([]byte{0x41}, 20)}
	stream.SessionKeySetHeight = 10
	stream.SessionKeyExpiryHeight = 20
	stream.SessionKeySpendCap = shared.NewAmount(0)
	stream.SessionKeySpent = shared.NewAmount(0)
	ctx := sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(10))
	require.ErrorContains(t, f.keeper.InitGenesis(ctx, *genesis), "set_height exceeds")
	exists, err := f.keeper.Stream.Has(ctx, stream.SessionId)
	require.NoError(t, err)
	require.False(t, exists)
}
