package keeper

import (
	"fmt"
	"testing"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/stretchr/testify/require"
)

func TestDelayedSessionSetKeepsItsScheduledAuthorizationHeight(t *testing.T) {
	f := newDispatchFixture(t)
	key := secp.PrivKeyFromBytes(bytes32(4))
	raw := shared.EVMAddressFromSecp256k1PublicKey(key.PubKey())
	response, err := NewMsgServerImpl(f.keeper).UpdateSessionKey(f.ctx, sessionKeySetRequest(f.owner, f.sessionID, raw[:], 500, 4_000_000))
	require.NoError(t, err)
	effective := response.GetPendingChange().EffectiveHeight
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(effective + 5)))
	changed, err := f.keeper.applySessionPendingRow(f.ctx, sessionPendingKeyChange, sessionPendingRow{height: effective, sessionID: f.sessionID}, effective+5)
	require.NoError(t, err)
	require.True(t, changed)
	stream, err := f.keeper.ReadStream(f.ctx, f.sessionID)
	require.NoError(t, err)
	require.Equal(t, effective, stream.SessionKeySetHeight)
	order := f.signedOrder(t, 0, key).Order
	order.SessionAnchorHeight = effective + 1
	f.record(t, f.entry(t, signDispatchOrder(t, order, key), f.hub.builders[0]))
}

func TestPendingClosePreservesTakeoverUntilCloseActuallyExecutes(t *testing.T) {
	for _, beforeClose := range []bool{false, true} {
		t.Run(fmt.Sprint(beforeClose), func(t *testing.T) {
			f := newDispatchFixture(t)
			server := NewMsgServerImpl(f.keeper)
			signed := f.signedOrder(t, 0, f.delegateKey)
			takeover := func() {
				t.Helper()
				response, err := server.RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[1], Entries: []*types.DispatchRecordEntryV1{{Entry: &types.DispatchRecordEntryV1_Takeover{Takeover: &types.TakeoverEntryV1{SignedOrder: &signed}}}}})
				require.NoError(t, err)
				require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Results[0].Status)
			}
			if beforeClose {
				takeover()
			}
			_, err := server.WithdrawSession(f.ctx, &types.MsgWithdrawSession{SessionId: f.sessionID, SignerAddress: f.owner, Action: &types.MsgWithdrawSession_Close{Close: &types.CloseSessionV1{}}})
			require.NoError(t, err)
			if !beforeClose {
				f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(145))
				takeover()
			}
			stream, err := f.keeper.ReadStream(f.ctx, f.sessionID)
			require.NoError(t, err)
			require.NotNil(t, stream.GetPendingTakeover())
			pending := stream.GetPendingTakeover()
			closeHeight := stream.PendingWithdrawEffectiveHeight
			if beforeClose {
				f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(pending.ActivationHeight)))
				_, err = f.keeper.applySessionPendingRow(f.ctx, sessionPendingTakeover, sessionPendingRow{height: pending.ActivationHeight, sessionID: f.sessionID}, pending.ActivationHeight)
				require.NoError(t, err)
			}
			f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(closeHeight)))
			_, err = f.keeper.applySessionPendingRow(f.ctx, sessionPendingWithdraw, sessionPendingRow{height: closeHeight, sessionID: f.sessionID}, closeHeight)
			require.NoError(t, err)
			stream, err = f.keeper.ReadStream(f.ctx, f.sessionID)
			require.NoError(t, err)
			require.Equal(t, types.SessionStatus_SESSION_STATUS_CLOSED, stream.Status)
			require.Nil(t, stream.GetPendingTakeover())
		})
	}
}

func TestCreateSessionFundingFailuresHaveContractErrorCodes(t *testing.T) {
	for _, kind := range []string{"deposit", "builder", "limit"} {
		t.Run(kind, func(t *testing.T) {
			f := newDispatchFixture(t)
			request := &types.MsgCreateSession{SignerAddress: f.owner, InitialBuilder: f.hub.builders[0], InitialDeposit: shared.NewAmount(dispatchTestPrefund)}
			var expected error
			switch kind {
			case "deposit":
				request.InitialDeposit = shared.NewAmount(0)
				expected = types.ErrSessionMinDeposit
			case "builder":
				request.InitialBuilder = f.worker
				expected = types.ErrDispatchBuilderNotBound
			case "limit":
				params, err := f.keeper.Params.Get(f.ctx)
				require.NoError(t, err)
				params.Session.MaxActiveSessionsPerAccount = 1
				require.NoError(t, f.keeper.Params.Set(f.ctx, params))
				expected = types.ErrActiveSessionLimit
			}
			_, err := NewMsgServerImpl(f.keeper).CreateSession(f.ctx, request)
			require.ErrorIs(t, err, expected)
			nonce, err := f.keeper.ReadSessionNonce(f.ctx, f.owner)
			require.NoError(t, err)
			require.Equal(t, uint64(1), nonce.NextSessionNonce)
		})
	}
}
