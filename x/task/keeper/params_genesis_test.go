package keeper

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/task/types"
)

func TestGovernedParamsKeepSessionVersionAcrossGenesis(t *testing.T) {
	f := newDispatchFixture(t)
	original, err := f.keeper.ParamsForSession(f.ctx, f.sessionID)
	require.NoError(t, err)
	next := original
	next.Session.SessionIdleTtlBlocks++
	authority, err := f.keeper.addressCodec.BytesToString(f.keeper.authority)
	require.NoError(t, err)
	_, err = NewMsgServerImpl(f.keeper).UpdateTaskParams(f.ctx, &types.MsgUpdateTaskParams{Authority: authority, ExpectedVersion: 0, Params: next})
	require.NoError(t, err)
	before, err := f.keeper.ParamsForSession(f.ctx, f.sessionID)
	require.NoError(t, err)
	require.Equal(t, original.Session.SessionIdleTtlBlocks, before.Session.SessionIdleTtlBlocks)
	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	restarted := initInternalFixture(t)
	require.NoError(t, restarted.keeper.Params.Remove(restarted.ctx))
	restarted.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(restarted.ctx).WithChainID(sdkChainID(f.ctx)).WithBlockHeight(124))
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	after, err := restarted.keeper.ParamsForSession(restarted.ctx, f.sessionID)
	require.NoError(t, err)
	require.Equal(t, original.Session.SessionIdleTtlBlocks, after.Session.SessionIdleTtlBlocks, "a restart must preserve the Session's frozen parameter version")
}
