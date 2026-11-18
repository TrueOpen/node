package keeper

import (
	"bytes"
	"testing"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func requestTestWithdrawal(t *testing.T, f *dispatchFixture, close bool, amount uint64) types.StreamState {
	t.Helper()
	request := &types.MsgWithdrawSession{SessionId: f.sessionID, SignerAddress: f.owner}
	if close {
		request.Action = &types.MsgWithdrawSession_Close{Close: &types.CloseSessionV1{}}
	} else {
		request.Action = &types.MsgWithdrawSession_Amount{Amount: &types.WithdrawAmountV1{Amount: shared.NewAmount(amount)}}
	}
	_, err := NewMsgServerImpl(f.keeper).WithdrawSession(f.ctx, request)
	require.NoError(t, err)
	stream, err := f.keeper.ReadStream(f.ctx, f.sessionID)
	require.NoError(t, err)
	return stream
}

func TestWithdrawalMaturityPreservesInFlightReservation(t *testing.T) {
	f := newDispatchFixture(t)
	pending := requestTestWithdrawal(t, f, false, 1_900_000)
	entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
	response, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[0], Entries: []*types.DispatchRecordEntryV1{dispatchEntryItem(entry)}})
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Results[0].Status)
	row := sessionPendingRow{height: pending.PendingWithdrawEffectiveHeight, sessionID: f.sessionID}
	changed, err := f.keeper.applySessionPendingRow(f.ctx, sessionPendingWithdraw, row, row.height-1)
	require.NoError(t, err)
	require.False(t, changed)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(row.height)))
	changed, err = f.keeper.applySessionPendingRow(f.ctx, sessionPendingWithdraw, row, row.height)
	require.NoError(t, err)
	require.True(t, changed)
	stream, err := f.keeper.ReadStream(f.ctx, f.sessionID)
	require.NoError(t, err)
	require.Equal(t, entry.SignedOrder.Order.MaxFee, stream.Balance)
	require.Equal(t, entry.SignedOrder.Order.MaxFee, stream.FrozenTotal)
	require.Equal(t, uint32(1), stream.OpenPendingCount)
	require.Equal(t, types.SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_NONE, stream.PendingWithdrawKind)
	amount, err := shared.ParseAmount(stream.Balance)
	require.NoError(t, err)
	require.Equal(t, amount, f.bank.balance(f.ctx, authtypes.NewModuleAddress(shared.TaskEscrowModuleName)))
	before := f.bank.balance(f.ctx, sdk.AccAddress(f.ownerRaw))
	changed, err = f.keeper.applySessionPendingRow(f.ctx, sessionPendingWithdraw, row, row.height+1)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, before, f.bank.balance(f.ctx, sdk.AccAddress(f.ownerRaw)), "a stale index cannot pay again")
}

func TestCloseWaitsWithoutStarvingAnotherSessionWithdrawal(t *testing.T) {
	f := newDispatchFixture(t)
	entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
	response, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[0], Entries: []*types.DispatchRecordEntryV1{dispatchEntryItem(entry)}})
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Results[0].Status)
	pending := requestTestWithdrawal(t, f, true, 0)
	otherID := bytes32(0xff)
	require.Negative(t, bytes.Compare(f.sessionID, otherID))
	other := pending
	other.SessionId = otherID
	other.NextExpectedSequence = 0
	other.OpenPendingCount = 0
	other.FrozenTotal = shared.NewAmount(0)
	other.PendingWithdrawKind = types.SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_AMOUNT
	other.PendingWithdrawAmount = shared.NewAmount(1000)
	other.XLastRecordWinner = nil
	other.LastRecordHeight = 0
	other.StickyAnchorSequence = 0
	other.StickyAnchorHeight = 0
	other.StickyRunCount = 0
	coins, err := f.keeper.sessionCoins(f.ctx, dispatchTestPrefund)
	require.NoError(t, err)
	require.NoError(t, f.bank.SendCoinsFromAccountToModule(f.ctx, sdk.AccAddress(f.ownerRaw), shared.TaskEscrowModuleName, coins))
	require.NoError(t, f.keeper.setStreamState(f.ctx, other))
	require.NoError(t, f.keeper.SessionWithdrawIndex.Set(f.ctx, collections.Join(other.PendingWithdrawEffectiveHeight, otherID)))
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(pending.PendingWithdrawEffectiveHeight)))
	result, err := f.keeper.RunEndBlockDeadlineSweeps(f.ctx, pending.PendingWithdrawEffectiveHeight, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(1), result.SessionWithdraw, "the sleeping CLOSE must not consume work before its Tasks finish")
	first, err := f.keeper.ReadStream(f.ctx, f.sessionID)
	require.NoError(t, err)
	second, err := f.keeper.ReadStream(f.ctx, otherID)
	require.NoError(t, err)
	require.Equal(t, types.SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_CLOSE, first.PendingWithdrawKind)
	require.Equal(t, types.SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_NONE, second.PendingWithdrawKind)
	require.Equal(t, shared.NewAmount(dispatchTestPrefund-1000), second.Balance)
	key := collections.Join(first.PendingWithdrawEffectiveHeight, first.SessionId)
	queued, err := f.keeper.SessionWithdrawIndex.Has(f.ctx, key)
	require.NoError(t, err)
	require.False(t, queued)
	// The final Task's shared Stream update wakes the dormant CLOSE using its
	// original scheduled height, without another full-table maintenance scan.
	first.FrozenTotal = shared.NewAmount(0)
	require.NoError(t, f.keeper.WriteStream(f.ctx, first.SessionId, first))
	require.NoError(t, f.keeper.closeStreamPendingTask(f.ctx, first.SessionId))
	queued, err = f.keeper.SessionWithdrawIndex.Has(f.ctx, key)
	require.NoError(t, err)
	require.True(t, queued)
	advanced, err := f.keeper.applySessionPendingRow(f.ctx, sessionPendingWithdraw, sessionPendingRow{height: key.K1(), sessionID: key.K2()}, key.K1())
	require.NoError(t, err)
	require.True(t, advanced)
}

func TestCloseRefundsOwnerAndSchedulesHistoryPruning(t *testing.T) {
	f := newDispatchFixture(t)
	pending := requestTestWithdrawal(t, f, true, 0)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(pending.PendingWithdrawEffectiveHeight)))
	changed, err := f.keeper.applySessionPendingRow(f.ctx, sessionPendingWithdraw, sessionPendingRow{height: pending.PendingWithdrawEffectiveHeight, sessionID: f.sessionID}, pending.PendingWithdrawEffectiveHeight)
	require.NoError(t, err)
	require.True(t, changed)
	closed, err := f.keeper.ReadStream(f.ctx, f.sessionID)
	require.NoError(t, err)
	require.Equal(t, types.SessionStatus_SESSION_STATUS_CLOSED, closed.Status)
	require.Equal(t, shared.NewAmount(0), closed.Balance)
	require.Equal(t, uint64(100_000_000), f.bank.balance(f.ctx, sdk.AccAddress(f.ownerRaw)))
	indexed, err := f.keeper.SessionByOwnerIndex.Has(f.ctx, types.NewSessionByOwnerKey(f.ownerRaw, f.sessionID))
	require.NoError(t, err)
	require.False(t, indexed)
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	indexed, err = f.keeper.SessionHistoryPruneIndex.Has(f.ctx, types.NewSessionHistoryPruneIndexKey(pending.PendingWithdrawEffectiveHeight+params.Session.SessionOrderRetentionBlocks, f.sessionID))
	require.NoError(t, err)
	require.True(t, indexed)
}

func TestDelayedSessionKeyExpiryUsesScheduledEffectiveHeight(t *testing.T) {
	for _, boundary := range []string{"already expired", "effective height", "upper bound", "above upper bound"} {
		t.Run(boundary, func(t *testing.T) {
			f := newDispatchFixture(t)
			before, err := f.keeper.ReadStream(f.ctx, f.sessionID)
			require.NoError(t, err)
			params, err := f.keeper.Params.Get(f.ctx)
			require.NoError(t, err)
			effective := uint64(123) + params.Session.SessionChangeDelayBlocks
			expiry := effective + params.Session.MaxSessionKeyBlocks
			if boundary == "already expired" {
				expiry = 130
			}
			if boundary == "effective height" {
				expiry = effective
			}
			if boundary == "above upper bound" {
				expiry++
			}
			key := secp.PrivKeyFromBytes(bytes32(4))
			address := shared.EVMAddressFromSecp256k1PublicKey(key.PubKey())
			request := &types.MsgUpdateSessionKey{SessionId: f.sessionID, SignerAddress: f.owner, Action: &types.MsgUpdateSessionKey_Set{Set: &types.SessionKeyGrantV1{SessionKey: address[:], ExpiryHeight: expiry, SpendCap: shared.NewAmount(4_000_000)}}}
			updated, err := NewMsgServerImpl(f.keeper).UpdateSessionKey(f.ctx, request)
			if boundary != "upper bound" {
				require.ErrorIs(t, err, types.ErrSessionKeyInvalid)
				after, err := f.keeper.ReadStream(f.ctx, f.sessionID)
				require.NoError(t, err)
				require.Equal(t, before, after)
				return
			}
			require.NoError(t, err, "the scheduled-height upper bound may exceed the current-height upper bound")
			require.Equal(t, effective, updated.GetPendingChange().EffectiveHeight)
			f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(effective)))
			changed, err := f.keeper.applySessionPendingRow(f.ctx, sessionPendingKeyChange, sessionPendingRow{height: effective, sessionID: f.sessionID}, effective)
			require.NoError(t, err)
			require.True(t, changed)
			stream, err := f.keeper.ReadStream(f.ctx, f.sessionID)
			require.NoError(t, err)
			require.Equal(t, address[:], stream.GetSessionKey())
			require.Equal(t, effective, stream.SessionKeySetHeight)
			require.Equal(t, expiry, stream.SessionKeyExpiryHeight)
		})
	}
}

func TestCancellationMaturityVoidsPendingTakeover(t *testing.T) {
	f := newDispatchFixture(t)
	cancelled, err := NewMsgServerImpl(f.keeper).CancelOrder(f.ctx, &types.MsgCancelOrder{SessionId: f.sessionID, OrderSequence: 0, SignerAddress: f.owner})
	require.NoError(t, err)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(140))
	signed := f.signedOrder(t, 0, f.delegateKey)
	response, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[1], Entries: []*types.DispatchRecordEntryV1{{Entry: &types.DispatchRecordEntryV1_Takeover{Takeover: &types.TakeoverEntryV1{SignedOrder: &signed}}}}})
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Results[0].Status)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(cancelled.CancelEffectiveHeight)))
	changed, err := f.keeper.applySessionPendingRow(f.ctx, sessionPendingCancel, sessionPendingRow{height: cancelled.CancelEffectiveHeight, sessionID: f.sessionID, sequence: 0}, cancelled.CancelEffectiveHeight)
	require.NoError(t, err)
	require.True(t, changed)
	stream, err := f.keeper.ReadStream(f.ctx, f.sessionID)
	require.NoError(t, err)
	require.Equal(t, uint64(1), stream.NextExpectedSequence)
	require.Nil(t, stream.GetPendingTakeover())
	require.Equal(t, f.hub.builders[0], stream.BoundBuilder)
}
