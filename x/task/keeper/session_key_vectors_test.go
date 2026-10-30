package keeper

import (
	"bytes"
	"encoding/hex"
	"github.com/TrueOpen/node/internal/testutil/sessionfixture"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/evm/crypto/ethsecp256k1"
	"github.com/stretchr/testify/require"
	"testing"
)

func vectorStream(t *testing.T, f *internalFixture, id []byte, state sessionfixture.State) types.StreamState {
	t.Helper()
	owner, err := f.keeper.addressCodec.BytesToString(sessionfixture.Hex(t, state.Owner))
	require.NoError(t, err)
	status := types.SessionStatus_SESSION_STATUS_ACTIVE
	if state.Status == "IDLE" {
		status = types.SessionStatus_SESSION_STATUS_IDLE
	}
	if state.Status == "CLOSED" {
		status = types.SessionStatus_SESSION_STATUS_CLOSED
	}
	stream := types.StreamState{SessionId: id, OwnerUserAddress: owner, Status: status, LastActiveHeight: 980, NextExpectedSequence: state.NextSequence,
		SessionKeyExpiryHeight: state.Expiry, SessionKeySetHeight: state.SetHeight, SessionKeySpendCap: shared.Amount{AtomicUnits: state.Cap}, SessionKeySpent: shared.Amount{AtomicUnits: state.Spent},
		PendingWithdrawKind: types.SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_NONE}
	if state.Key != "" {
		stream.XSessionKey = &types.StreamState_SessionKey{SessionKey: sessionfixture.Hex(t, state.Key)}
	}
	if state.Pending != nil {
		stream.XPendingSessionKeyChange = &types.StreamState_PendingSessionKeyChange{PendingSessionKeyChange: &types.PendingSessionKeyChangeV1{Kind: types.SessionKeyChangeKindV1_SESSION_KEY_CHANGE_KIND_V1_CLEAR, EffectiveHeight: state.Pending.EffectiveHeight}}
	}
	stream.BoundBuilder = owner
	stream.BoundChangeHeight = 1
	stream.Balance = shared.NewAmount(1_000_000)
	stream.FrozenTotal = shared.NewAmount(0)
	stream.PendingWithdrawAmount = shared.NewAmount(0)
	if stream.Status == types.SessionStatus_SESSION_STATUS_CLOSED {
		stream.Balance = shared.NewAmount(0)
	}
	return stream
}

func assertVectorState(t *testing.T, stream types.StreamState, want sessionfixture.State) {
	t.Helper()
	require.Equal(t, want.Key, hex.EncodeToString(stream.GetSessionKey()))
	require.Equal(t, want.Expiry, stream.SessionKeyExpiryHeight)
	require.Equal(t, want.SetHeight, stream.SessionKeySetHeight)
	require.Equal(t, want.Cap, stream.SessionKeySpendCap.AtomicUnits)
	require.Equal(t, want.Spent, stream.SessionKeySpent.AtomicUnits)
	require.Equal(t, want.NextSequence, stream.NextExpectedSequence)
}

func TestSessionKeyAllWireUpdateCases(t *testing.T) {
	doc := sessionfixture.Load(t)
	require.Len(t, doc.Updates, 19)
	for _, row := range doc.Updates {
		t.Run(row.Name, func(t *testing.T) {
			f := initInternalFixture(t)
			ctx := sessionKeyContext(f, int64(row.Height))
			id := bytes.Repeat([]byte{0x17}, 32)
			stream := vectorStream(t, f, id, row.State)
			require.NoError(t, f.keeper.WriteStream(ctx, id, stream))
			params, err := f.keeper.Params.Get(ctx)
			require.NoError(t, err)
			params.Session.MaxSessionKeyBlocks = row.MaxBlocks
			params.Session.SessionChangeDelayBlocks = row.Delay
			require.NoError(t, f.keeper.Params.Set(ctx, params))
			signer, err := f.keeper.addressCodec.BytesToString(sessionfixture.Hex(t, row.Signer))
			require.NoError(t, err)
			req := &types.MsgUpdateSessionKey{SignerAddress: signer, SessionId: id}
			if row.Action == "set" {
				req = sessionKeySetRequest(signer, id, sessionfixture.Hex(t, row.Grant.Key), row.Grant.Expiry, 0)
				req.GetSet().SpendCap = shared.Amount{AtomicUnits: row.Grant.Cap}
			}
			if row.Action == "clear" {
				req.Action = &types.MsgUpdateSessionKey_Clear{Clear: &types.ClearSessionKeyV1{}}
			}
			response, err := NewMsgServerImpl(f.keeper).UpdateSessionKey(ctx, req)
			switch row.Expected {
			case "APPLIED":
				require.NoError(t, err)
				require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Status)
			case "NOOP":
				require.NoError(t, err)
				require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, response.Status)
			case "UNAUTHORIZED":
				require.ErrorIs(t, err, sdkerrors.ErrUnauthorized)
			case "ERR_SESSION_CHANGE_PENDING":
				require.ErrorIs(t, err, types.ErrSessionChangePending)
			case "SESSION_CLOSED":
				require.ErrorIs(t, err, types.ErrInvalidSessionID)
			default:
				require.ErrorIs(t, err, types.ErrSessionKeyInvalid)
			}
			got, err := f.keeper.ReadStream(ctx, id)
			require.NoError(t, err)
			assertVectorState(t, got, row.ExpectedState)
			if row.EffectiveState != nil {
				require.NotNil(t, got.GetPendingSessionKeyChange())
				require.Equal(t, row.EffectiveHeight, got.GetPendingSessionKeyChange().EffectiveHeight)
				effectiveCtx := sessionKeyContext(f, int64(row.EffectiveHeight))
				advanced, err := f.keeper.applySessionPendingRow(effectiveCtx, sessionPendingKeyChange, sessionPendingRow{height: row.EffectiveHeight, sessionID: id}, row.EffectiveHeight)
				require.NoError(t, err)
				require.True(t, advanced)
				effective, err := f.keeper.ReadStream(effectiveCtx, id)
				require.NoError(t, err)
				assertVectorState(t, effective, *row.EffectiveState)
			}

		})
	}
}

// Full dispatch replay and later-failure rollback are covered by dispatch_admission_test.go.
// This matrix binds the shared order-authorization boundary to wire's signed bytes.
func TestSessionKeyWireOrderAuthorizationCases(t *testing.T) {
	doc := sessionfixture.Load(t)
	replayCases := 0
	for _, row := range doc.Admission {
		if row.AlreadyAdmitted {
			replayCases++
			continue
		}
		t.Run(row.Name, func(t *testing.T) {
			f := initInternalFixture(t)
			signed := doc.SignedOrder(t, row.Order)
			f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithChainID(signed.Order.ChainId).WithBlockHeight(int64(row.Height)))
			ownerRaw, err := f.keeper.addressCodec.StringToBytes(signed.Order.UserAddress)
			require.NoError(t, err)
			f.keeper.authKeeper = dispatchTestAuth{account: authtypes.NewBaseAccount(ownerRaw, &ethsecp256k1.PubKey{Key: sessionfixture.Hex(t, doc.Account.PublicKey)}, 0, 0)}
			stream := vectorStream(t, f, signed.Order.SessionId, row.State)
			require.NoError(t, f.keeper.WriteStream(f.ctx, signed.Order.SessionId, stream))
			admission, err := f.keeper.authorizeSessionOrder(f.ctx, signed, row.Height)
			if err == nil {
				err = f.keeper.requireUnusedDispatchSequence(f.ctx, admission)
			}
			switch row.Expected {
			case "ACCEPT_SESSION_KEY":
				require.NoError(t, err)
				require.True(t, admission.delegated)
				require.Equal(t, row.ExpectedSpent, shared.NewAmount(admission.nextSpent).AtomicUnits)
			case "ACCEPT_WALLET":
				require.NoError(t, err)
				require.False(t, admission.delegated)
			case "LATER_ADMISSION_REJECTED":
				require.NoError(t, err, "authorization succeeds; the later transaction must roll back separately")
			case "ERR_SESSION_KEY_INVALID", "SIGNATURE_MISMATCH":
				require.ErrorIs(t, err, types.ErrSessionKeyInvalid)
			case "ERR_SESSION_KEY_EXPIRED":
				require.ErrorIs(t, err, types.ErrSessionKeyExpired)
			case "ERR_SESSION_KEY_SPEND_CAP_EXCEEDED":
				require.ErrorIs(t, err, types.ErrSessionKeySpendCapExceeded)
			case "OWNER_MISMATCH", "SESSION_CLOSED":
				code, business := dispatchRejectionCode(err)
				require.True(t, business)
				require.Equal(t, types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_SESSION_INVALID, code)
			case "SEQUENCE_MISMATCH":
				code, business := dispatchRejectionCode(err)
				require.True(t, business)
				require.Equal(t, types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_SEQUENCE_CONSUMED, code)
			case "ERR_SESSION_CHANGE_PENDING":
				require.ErrorIs(t, err, types.ErrSessionChangePending)
			case "AMOUNT_OVERFLOW":
				require.ErrorContains(t, err, "overflow")
			default:
				t.Fatalf("unhandled authorization vector result %s", row.Expected)
			}
			after, readErr := f.keeper.ReadStream(f.ctx, signed.Order.SessionId)
			require.NoError(t, readErr)
			require.Equal(t, stream, after, "preflight must leave allowance and session accounting untouched")
		})
	}
	require.Equal(t, 1, replayCases, "the public replay integration test owns the one replay-only vector")
}

// Per-item authorization failures are returned through the public dispatch result.
func TestSessionKeyErrorsReachDispatchItemResults(t *testing.T) {
	for _, want := range []types.DispatchRejectCodeV1{types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_SESSION_KEY_INVALID, types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_SESSION_KEY_EXPIRED, types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_SESSION_KEY_SPEND_CAP_EXCEEDED} {
		t.Run(want.String(), func(t *testing.T) {
			f := newDispatchFixture(t)
			entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
			stream, err := f.keeper.ReadStream(f.ctx, f.sessionID)
			require.NoError(t, err)
			switch want {
			case types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_SESSION_KEY_INVALID:
				stream.SessionKeySetHeight = 124
			case types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_SESSION_KEY_EXPIRED:
				stream.SessionKeyExpiryHeight = 122
			case types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_SESSION_KEY_SPEND_CAP_EXCEEDED:
				stream.SessionKeySpendCap = shared.NewAmount(1)
			}
			require.NoError(t, f.keeper.WriteStream(f.ctx, f.sessionID, stream))
			response, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[0], Entries: []*types.DispatchRecordEntryV1{dispatchEntryItem(entry)}})
			require.NoError(t, err)
			require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_REJECTED, response.Results[0].Status)
			require.Equal(t, want, response.Results[0].RejectCode)
			after, err := f.keeper.ReadStream(f.ctx, f.sessionID)
			require.NoError(t, err)
			require.Equal(t, stream, after)
		})
	}
}
