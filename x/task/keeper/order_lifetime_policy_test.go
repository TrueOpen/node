package keeper

import (
	"testing"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
	"github.com/TrueOpen/wire/dispatch"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func TestExpiredEpochOrderCannotRegisterALateTakeover(t *testing.T) {
	f := newDispatchFixture(t)
	order := f.signedOrder(t, 0, f.ownerKey).Order
	order.OrderExpireHeight = 600
	signed := signDispatchOrder(t, order, f.ownerKey)
	dispatchFixtureHeight(f, 470)
	before, err := f.keeper.ReadStream(f.ctx, f.sessionID)
	require.NoError(t, err)
	response, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[1], Entries: []*types.DispatchRecordEntryV1{{Entry: &types.DispatchRecordEntryV1_Takeover{Takeover: &types.TakeoverEntryV1{SignedOrder: &signed}}}}})
	require.NoError(t, err)
	require.Equal(t, types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_ORDER_SIGNATURE_INVALID, response.Results[0].RejectCode)
	after, err := f.keeper.ReadStream(f.ctx, f.sessionID)
	require.NoError(t, err)
	require.Equal(t, before, after)
	retention, err := dispatch.PoolBodyRetentionBlocks(shared.DispatchRotationWindowHardLimits())
	require.NoError(t, err)
	require.Greater(t, retention, dispatch.MaxOrderLifetimeBlocksV1)
}

func TestOrderLifetimeCoversTheFrozenRecordingSpan(t *testing.T) {
	f := newDispatchFixture(t)
	params, err := f.keeper.ParamsForSession(f.ctx, f.sessionID)
	require.NoError(t, err)
	for _, short := range []bool{true, false} {
		order := f.signedOrder(t, 0, f.ownerKey).Order
		order.OrderExpireHeight = order.EarliestSubmitHeight + params.Dispatch.RecordDeadlineBlocks
		if short {
			order.OrderExpireHeight--
		}
		signed := signDispatchOrder(t, order, f.ownerKey)
		_, err := f.keeper.prepareDispatchOrder(f.ctx, signed, f.hub.builders[0], params, order.EarliestSubmitHeight)
		if short {
			require.ErrorContains(t, err, "recording span")
		} else {
			require.NoError(t, err)
		}
	}
}

func TestSessionQueryRecoversFrozenAdmissionPolicyAfterGovernance(t *testing.T) {
	f := newDispatchFixture(t)
	query := NewQueryServerImpl(f.keeper)
	before, err := query.Session(f.ctx, &types.QuerySessionRequest{SessionId: f.sessionID})
	require.NoError(t, err)
	require.Zero(t, before.ParamsVersion)
	require.Equal(t, uint64(120), before.Params.Dispatch.MaxOrderLifetimeBlocks)
	updateDispatchTestParams(t, f, func(p *types.TaskParamsV1) {
		p.Dispatch.MaxOrderLifetimeBlocks = 160
		p.Dispatch.RecordDeadlineBlocks = 40
		p.Session.SessionChangeDelayBlocks, p.Session.SessionWithdrawDelayBlocks = 60, 60
	})
	after, err := query.Session(f.ctx, &types.QuerySessionRequest{SessionId: f.sessionID})
	require.NoError(t, err)
	require.Equal(t, before.Params, after.Params)
	require.Equal(t, before.ParamsVersion, after.ParamsVersion)
	current, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(160), current.Dispatch.MaxOrderLifetimeBlocks)
	order := f.signedOrder(t, 0, f.ownerKey).Order
	order.OrderExpireHeight = order.EarliestSubmitHeight + 121
	signed := signDispatchOrder(t, order, f.ownerKey)
	_, err = f.keeper.prepareDispatchOrder(f.ctx, signed, f.hub.builders[0], current, 123)
	require.ErrorContains(t, err, "frozen lifetime")
	created, err := NewMsgServerImpl(f.keeper).CreateSession(f.ctx, &types.MsgCreateSession{SignerAddress: f.owner, InitialBuilder: f.hub.builders[0], InitialDeposit: shared.NewAmount(dispatchTestPrefund)})
	require.NoError(t, err)
	newPolicy, err := query.Session(f.ctx, &types.QuerySessionRequest{SessionId: created.SessionId})
	require.NoError(t, err)
	require.Equal(t, uint64(1), newPolicy.ParamsVersion)
	require.Equal(t, current, newPolicy.Params)
	require.NoError(t, f.keeper.ParamsHistory.Remove(f.ctx, 0))
	_, err = query.Session(f.ctx, &types.QuerySessionRequest{SessionId: f.sessionID})
	require.Error(t, err, "a missing historical body cannot become current policy")
}

func TestTaskGovernanceCannotOutrunBuilderSetNotice(t *testing.T) {
	f := newDispatchFixture(t)
	before, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	next := before
	next.Dispatch.MaxOrderLifetimeBlocks = 180
	_, err = NewMsgServerImpl(f.keeper).UpdateTaskParams(f.ctx, &types.MsgUpdateTaskParams{Authority: sdk.AccAddress(f.keeper.authority).String(), Params: next})
	require.ErrorIs(t, err, types.ErrInvariantBroken)
	after, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, before, after)
}
