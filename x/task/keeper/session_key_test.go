package keeper

import (
	"bytes"
	"context"
	"math"
	"testing"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	"github.com/cosmos/evm/crypto/ethsecp256k1"
	dcrsecp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func sessionKeyContext(f *internalFixture, height int64) context.Context {
	return sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithChainID("session-key-test").WithBlockHeight(height))
}

func sessionKeySetRequest(owner string, id, key []byte, expiry, cap uint64) *types.MsgUpdateSessionKey {
	return &types.MsgUpdateSessionKey{SignerAddress: owner, SessionId: id,
		Action: &types.MsgUpdateSessionKey_Set{Set: &types.SessionKeyGrantV1{SessionKey: key, ExpiryHeight: expiry, SpendCap: shared.NewAmount(cap)}}}
}

func TestSessionKeyLifecyclePreservesAccountingAndActivity(t *testing.T) {
	f := newDispatchFixture(t)
	ctx := f.ctx
	server := NewMsgServerImpl(f.keeper)
	stream, err := f.keeper.ReadStream(ctx, f.sessionID)
	require.NoError(t, err)
	key := bytes.Clone(stream.GetSessionKey())
	stream.SessionKeySpent = shared.NewAmount(50)
	require.NoError(t, f.keeper.WriteStream(ctx, f.sessionID, stream))
	updated, err := server.UpdateSessionKey(ctx, sessionKeySetRequest(f.owner, f.sessionID, key, 510, 40))
	require.NoError(t, err)
	pending := updated.GetPendingChange()
	require.NotNil(t, pending)
	require.Equal(t, uint64(153), pending.EffectiveHeight)
	unchanged, err := f.keeper.ReadStream(ctx, f.sessionID)
	require.NoError(t, err)
	require.Equal(t, stream.SessionKeySpendCap, unchanged.SessionKeySpendCap)
	ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(ctx).WithBlockHeight(153))
	advanced, err := f.keeper.applySessionPendingRow(ctx, sessionPendingKeyChange, sessionPendingRow{height: 153, sessionID: f.sessionID}, 153)
	require.NoError(t, err)
	require.True(t, advanced)
	effective, err := f.keeper.ReadStream(ctx, f.sessionID)
	require.NoError(t, err)
	require.Equal(t, shared.NewAmount(40), effective.SessionKeySpendCap)
	require.Equal(t, uint64(121), effective.SessionKeySetHeight)
	require.Equal(t, shared.NewAmount(50), effective.SessionKeySpent)
	clear := &types.MsgUpdateSessionKey{SignerAddress: f.owner, SessionId: f.sessionID, Action: &types.MsgUpdateSessionKey_Clear{Clear: &types.ClearSessionKeyV1{}}}
	updated, err = server.UpdateSessionKey(ctx, clear)
	require.NoError(t, err)
	require.Equal(t, uint64(183), updated.GetPendingChange().EffectiveHeight)
	ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(ctx).WithBlockHeight(183))
	advanced, err = f.keeper.applySessionPendingRow(ctx, sessionPendingKeyChange, sessionPendingRow{height: 183, sessionID: f.sessionID}, 183)
	require.NoError(t, err)
	require.True(t, advanced)
	cleared, err := f.keeper.ReadStream(ctx, f.sessionID)
	require.NoError(t, err)
	require.Nil(t, cleared.XSessionKey)
	require.Zero(t, cleared.SessionKeySetHeight)
	require.Equal(t, shared.NewAmount(50), cleared.SessionKeySpent)
	updated, err = server.UpdateSessionKey(ctx, sessionKeySetRequest(f.owner, f.sessionID, key, 510, 0))
	require.NoError(t, err)
	require.Equal(t, uint64(213), updated.GetPendingChange().EffectiveHeight)
	require.Equal(t, uint64(121), cleared.LastActiveHeight)
	stored, err := f.keeper.Stream.Get(ctx, f.sessionID)
	require.NoError(t, err)
	raw, err := stored.Marshal()
	require.NoError(t, err)
	require.NotContains(t, string(raw), f.owner)
}

func TestSessionKeyInvalidUpdatesAreZeroWrite(t *testing.T) {
	f := newDispatchFixture(t)
	ctx := f.ctx
	server := NewMsgServerImpl(f.keeper)
	owner := f.owner
	created := &types.MsgCreateSessionResponse{SessionId: f.sessionID}
	original, err := f.keeper.ReadStream(ctx, created.SessionId)
	require.NoError(t, err)
	invalidCap := sessionKeySetRequest(owner, created.SessionId, bytes.Repeat([]byte{2}, 20), 200, 0)
	invalidCap.GetSet().SpendCap = shared.Amount{AtomicUnits: "01"}
	for _, req := range []*types.MsgUpdateSessionKey{
		invalidCap,
		{SignerAddress: owner, SessionId: created.SessionId},
		sessionKeySetRequest(owner, created.SessionId, make([]byte, 20), 200, 0),
		sessionKeySetRequest(owner, created.SessionId, bytes.Repeat([]byte{2}, 19), 200, 0),
		sessionKeySetRequest(owner, created.SessionId, bytes.Repeat([]byte{2}, 20), 123, 0),
		sessionKeySetRequest(owner, created.SessionId, bytes.Repeat([]byte{2}, 20), 123+30+types.DefaultMaxSessionKeyBlocks+1, 0),
	} {
		_, err := server.UpdateSessionKey(ctx, req)
		require.ErrorIs(t, err, types.ErrSessionKeyInvalid)
		stream, err := f.keeper.ReadStream(ctx, created.SessionId)
		require.NoError(t, err)
		require.Equal(t, original, stream)
	}
	other, err := f.keeper.addressCodec.BytesToString(bytes.Repeat([]byte{0x42}, 20))
	require.NoError(t, err)
	_, err = server.UpdateSessionKey(ctx, sessionKeySetRequest(other, created.SessionId, bytes.Repeat([]byte{2}, 20), 200, 0))
	require.ErrorIs(t, err, sdkerrors.ErrUnauthorized)
	original.Status = types.SessionStatus_SESSION_STATUS_CLOSED
	original.Balance = shared.NewAmount(0)
	require.NoError(t, f.keeper.WriteStream(ctx, created.SessionId, original))
	_, err = server.UpdateSessionKey(ctx, sessionKeySetRequest(owner, created.SessionId, bytes.Repeat([]byte{2}, 20), 200, 0))
	require.ErrorIs(t, err, types.ErrInvalidSessionID)
}

func TestSameSessionKeyCanBeRegisteredIndependently(t *testing.T) {
	f := newDispatchFixture(t)
	ctx := f.ctx
	server := NewMsgServerImpl(f.keeper)
	owner := f.owner
	grant := &types.SessionKeyGrantV1{SessionKey: bytes.Repeat([]byte{2}, 20), ExpiryHeight: 500, SpendCap: shared.NewAmount(50)}
	first, err := server.CreateSession(ctx, &types.MsgCreateSession{SignerAddress: owner, SessionKey: grant, InitialBuilder: f.hub.builders[0], InitialDeposit: shared.NewAmount(dispatchTestPrefund)})
	require.NoError(t, err)
	second, err := server.CreateSession(ctx, &types.MsgCreateSession{SignerAddress: owner, SessionKey: grant, InitialBuilder: f.hub.builders[0], InitialDeposit: shared.NewAmount(dispatchTestPrefund)})
	require.NoError(t, err)
	require.NotEqual(t, first.SessionId, second.SessionId)
	_, err = server.UpdateSessionKey(ctx, &types.MsgUpdateSessionKey{SignerAddress: owner, SessionId: first.SessionId,
		Action: &types.MsgUpdateSessionKey_Clear{Clear: &types.ClearSessionKeyV1{}}})
	require.NoError(t, err)
	other, err := f.keeper.ReadStream(ctx, second.SessionId)
	require.NoError(t, err)
	require.Equal(t, grant.SessionKey, other.GetSessionKey())
	require.Equal(t, shared.NewAmount(0), other.SessionKeySpent)
}

func TestSessionKeyOrderAuthorizationBoundaries(t *testing.T) {
	owner := bytes.Repeat([]byte{1}, 20)
	key := bytes.Repeat([]byte{2}, 20)
	publicKey := &ethsecp256k1.PubKey{Key: dcrsecp256k1.PrivKeyFromBytes(bytes.Repeat([]byte{1}, 32)).PubKey().SerializeCompressed()}
	for _, tc := range []struct {
		name      string
		mutate    func(*types.StreamState, *types.TaskOrderV3, *types.EIP712RecoveredSigner)
		want      error
		delegated bool
	}{
		{"exact cap inclusive expiry", func(*types.StreamState, *types.TaskOrderV3, *types.EIP712RecoveredSigner) {}, nil, true},
		{"expired", func(s *types.StreamState, _ *types.TaskOrderV3, _ *types.EIP712RecoveredSigner) {
			s.SessionKeyExpiryHeight = 99
		}, types.ErrSessionKeyExpired, false},
		{"reregistered key", func(s *types.StreamState, _ *types.TaskOrderV3, _ *types.EIP712RecoveredSigner) {
			s.SessionKeySetHeight = 100
		}, types.ErrSessionKeyInvalid, false},
		{"rotated key", func(s *types.StreamState, _ *types.TaskOrderV3, _ *types.EIP712RecoveredSigner) {
			s.XSessionKey = &types.StreamState_SessionKey{SessionKey: owner}
		}, types.ErrSessionKeyInvalid, false},
		{"cap short", func(s *types.StreamState, _ *types.TaskOrderV3, _ *types.EIP712RecoveredSigner) {
			s.SessionKeySpendCap = shared.NewAmount(29)
		}, types.ErrSessionKeySpendCapExceeded, false},
		{"unlimited", func(s *types.StreamState, _ *types.TaskOrderV3, _ *types.EIP712RecoveredSigner) {
			s.SessionKeySpendCap = shared.NewAmount(0)
		}, nil, true},
		{"unlimited overflow", func(s *types.StreamState, _ *types.TaskOrderV3, _ *types.EIP712RecoveredSigner) {
			s.SessionKeySpendCap = shared.NewAmount(0)
			s.SessionKeySpent = shared.NewAmount(math.MaxUint64)
		}, types.ErrInvalidAssignment, false},
		{"wallet ignores grant", func(s *types.StreamState, _ *types.TaskOrderV3, r *types.EIP712RecoveredSigner) {
			r.Address = owner
			r.PublicKey = publicKey.Bytes()
			s.SessionKeyExpiryHeight = 1
		}, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := types.StreamState{XSessionKey: &types.StreamState_SessionKey{SessionKey: key}, SessionKeySetHeight: 10, SessionKeyExpiryHeight: 100, SessionKeySpent: shared.NewAmount(10), SessionKeySpendCap: shared.NewAmount(30)}
			order := types.TaskOrderV3{SessionAnchorHeight: 99, MaxFee: shared.NewAmount(20)}
			recovered := types.EIP712RecoveredSigner{Address: key}
			tc.mutate(&stream, &order, &recovered)
			delegated, spent, err := authorizeOrderSigner(stream, owner, recovered, publicKey, order, 100)
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
				_, code, _ := errorsmod.ABCIInfo(err, false)
				require.NotZero(t, code)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.delegated, delegated)
			if delegated {
				require.Equal(t, uint64(30), spent)
			}
		})
	}
}
