package keeper

import (
	"bytes"
	errorsmod "cosmossdk.io/errors"
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
		SessionKeyExpiryHeight: state.Expiry, SessionKeySetHeight: state.SetHeight, SessionKeySpendCap: shared.Amount{AtomicUnits: state.Cap}, SessionKeySpent: shared.Amount{AtomicUnits: state.Spent}}
	if state.Key != "" {
		stream.XSessionKey = &types.StreamState_SessionKey{SessionKey: sessionfixture.Hex(t, state.Key)}
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
	require.Len(t, doc.Updates, 16)
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
			case "SESSION_CLOSED":
				require.ErrorIs(t, err, types.ErrInvalidSessionID)
			default:
				require.ErrorIs(t, err, types.ErrSessionKeyInvalid)
			}
			got, err := f.keeper.ReadStream(ctx, id)
			require.NoError(t, err)
			assertVectorState(t, got, row.ExpectedState)
		})
	}
}

func TestSessionKeyAllWireAdmissionCases(t *testing.T) {
	doc := sessionfixture.Load(t)
	require.Len(t, doc.Admission, 18)
	for _, row := range doc.Admission {
		t.Run(row.Name, func(t *testing.T) {
			f, _, bank := sessionAdmissionFixture(t)
			signed := doc.SignedOrder(t, row.Order)
			f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithChainID(signed.Order.ChainId).WithBlockHeight(int64(row.Height)))
			ownerRaw, err := f.keeper.addressCodec.StringToBytes(signed.Order.UserAddress)
			require.NoError(t, err)
			f.keeper.authKeeper = sessionAdmissionAuth{account: authtypes.NewBaseAccount(ownerRaw, &ethsecp256k1.PubKey{Key: sessionfixture.Hex(t, doc.Account.PublicKey)}, 0, 0)}
			hub := f.keeper.hubKeeper.(sessionAdmissionHub)
			hub.order = &signed.Order
			f.keeper.hubKeeper = hub
			stream := vectorStream(t, f, signed.Order.SessionId, row.State)
			require.NoError(t, f.keeper.WriteStream(f.ctx, signed.Order.SessionId, stream))
			if row.AlreadyAdmitted {
				taskID, err := types.DeriveTaskIDFromRawSession(signed.Order.SessionId, signed.Order.OrderSequence)
				require.NoError(t, err)
				hash, err := types.TaskOrderHash(signed.Order)
				require.NoError(t, err)
				require.NoError(t, f.keeper.TaskCore.Set(f.ctx, taskID[:], types.TaskCoreState{TaskId: taskID[:], SessionId: signed.Order.SessionId, OrderSequence: signed.Order.OrderSequence,
					UserAddress: signed.Order.UserAddress, ModelId: signed.Order.ModelId, ProfileVersion: signed.Order.ProfileVersion, TaskType: signed.Order.TaskType, AcceptedTaskHash: hash[:], AcceptedInputHash: signed.Order.InputHash}))
			}
			if row.LaterValid != nil && !*row.LaterValid {
				failed := bank
				failed.fail = true
				f.keeper.bankKeeper = failed
			}
			err = runSessionAdmission(t, f, signed, true)
			switch row.Expected {
			case "ACCEPT_SESSION_KEY", "ACCEPT_WALLET", "ALREADY_ADMITTED":
				require.NoError(t, err)
			case "ERR_SESSION_KEY_INVALID":
				require.ErrorIs(t, err, types.ErrSessionKeyInvalid)
			case "ERR_SESSION_KEY_EXPIRED":
				require.ErrorIs(t, err, types.ErrSessionKeyExpired)
			case "ERR_SESSION_KEY_SPEND_CAP_EXCEEDED":
				require.ErrorIs(t, err, types.ErrSessionKeySpendCapExceeded)
			case "OWNER_MISMATCH", "SEQUENCE_MISMATCH":
				require.ErrorContains(t, err, "owner or sequence")
			case "SESSION_CLOSED":
				require.ErrorContains(t, err, "closed")
			case "SIGNATURE_MISMATCH":
				require.ErrorIs(t, err, types.ErrSessionKeyInvalid)
			case "AMOUNT_OVERFLOW":
				require.ErrorContains(t, err, "overflows")
			case "LATER_ADMISSION_REJECTED":
				require.ErrorContains(t, err, "injected bank failure")
			default:
				t.Fatalf("unhandled vector result %s", row.Expected)
			}
			got, readErr := f.keeper.ReadStream(f.ctx, signed.Order.SessionId)
			require.NoError(t, readErr)
			require.Equal(t, row.ExpectedSpent, got.SessionKeySpent.AtomicUnits)
			if err != nil {
				require.Equal(t, stream, got)
				require.Zero(t, bank.escrow(f.ctx))
			}
		})
	}
}

// Authorization fails before candidate verification. This exercises the public
// handler and ABCI conversion rather than calling only the authorization helper.
func TestSessionKeyErrorsReachPublicHandraisesABCI(t *testing.T) {
	for _, want := range []error{types.ErrSessionKeyInvalid, types.ErrSessionKeyExpired, types.ErrSessionKeySpendCapExceeded} {
		t.Run(want.Error(), func(t *testing.T) {
			f, signed, bank := sessionAdmissionFixture(t)
			stream, err := f.keeper.ReadStream(f.ctx, signed.Order.SessionId)
			require.NoError(t, err)
			switch want {
			case types.ErrSessionKeyInvalid:
				stream.SessionKeySetHeight = 101
			case types.ErrSessionKeyExpired:
				stream.SessionKeyExpiryHeight = 110
			case types.ErrSessionKeySpendCapExceeded:
				stream.SessionKeySpendCap = shared.NewAmount(1)
			}
			require.NoError(t, f.keeper.WriteStream(f.ctx, signed.Order.SessionId, stream))
			hash, err := types.TaskOrderHash(signed.Order)
			require.NoError(t, err)
			taskID, err := types.DeriveTaskIDFromRawSession(signed.Order.SessionId, signed.Order.OrderSequence)
			require.NoError(t, err)
			operator, err := f.keeper.addressCodec.BytesToString(bytes.Repeat([]byte{0x61}, 20))
			require.NoError(t, err)
			handraise := types.WorkerHandraiseV1{SchemaVersion: types.WorkerHandraiseSchemaVersionV1, ChainId: signed.Order.ChainId, TaskId: taskID[:], TaskHash: hash[:], ModelId: signed.Order.ModelId, ProfileVersion: 1,
				Member: types.CandidateMemberRefV1{CandidatePoolSnapshotId: bytes.Repeat([]byte{0x47}, 32), Slot: 1, SlotVersion: 1, OperatorAddress: operator}, Duty: shared.Duty_DUTY_WORKER, ServiceAuthorizationNonce: 1, ExpiryHeight: 300, ServiceSignature: make([]byte, 64)}
			_, err = NewMsgServerImpl(f.keeper).SubmitWorkerHandraises(f.ctx, &types.MsgSubmitWorkerHandraises{SubmitterAddress: operator, Scope: types.WorkerHandraiseScopeV1{Scope: &types.WorkerHandraiseScopeV1_SignedOrder{SignedOrder: &signed}}, Handraises: []types.WorkerHandraiseV1{handraise}})
			require.ErrorIs(t, err, want)
			space, code, _ := errorsmod.ABCIInfo(err, false)
			wantSpace, wantCode, _ := errorsmod.ABCIInfo(want, false)
			require.Equal(t, wantSpace, space)
			require.Equal(t, wantCode, code)
			after, readErr := f.keeper.ReadStream(f.ctx, signed.Order.SessionId)
			require.NoError(t, readErr)
			require.Equal(t, stream, after)
			require.Zero(t, bank.escrow(f.ctx))
		})
	}
}
