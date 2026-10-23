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
	f := initInternalFixture(t)
	ctx := sessionKeyContext(f, 100)
	server := NewMsgServerImpl(f.keeper)
	owner := sessionTestOwner(t, f)
	key := bytes.Repeat([]byte{0x41}, 20)
	created, err := server.CreateSession(ctx, &types.MsgCreateSession{SignerAddress: owner,
		SessionKey: &types.SessionKeyGrantV1{SessionKey: key, ExpiryHeight: 200, SpendCap: shared.NewAmount(100)}})
	require.NoError(t, err)
	stream, err := f.keeper.ReadStream(ctx, created.SessionId)
	require.NoError(t, err)
	require.Equal(t, key, stream.GetSessionKey())
	require.Equal(t, uint64(100), stream.SessionKeySetHeight)
	stream.SessionKeySpent = shared.NewAmount(50)
	require.NoError(t, f.keeper.WriteStream(ctx, created.SessionId, stream))
	ctx = sessionKeyContext(f, 110)
	response, err := server.UpdateSessionKey(ctx, sessionKeySetRequest(owner, created.SessionId, key, 210, 40))
	require.NoError(t, err)
	require.Equal(t, uint64(100), response.SetHeight)
	require.Equal(t, shared.NewAmount(50), response.Spent)
	before := len(sdk.UnwrapSDKContext(ctx).EventManager().Events())
	response, err = server.UpdateSessionKey(ctx, sessionKeySetRequest(owner, created.SessionId, key, 210, 40))
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, response.Status)
	require.Len(t, sdk.UnwrapSDKContext(ctx).EventManager().Events(), before)
	clear := &types.MsgUpdateSessionKey{SignerAddress: owner, SessionId: created.SessionId, Action: &types.MsgUpdateSessionKey_Clear{Clear: &types.ClearSessionKeyV1{}}}
	response, err = server.UpdateSessionKey(ctx, clear)
	require.NoError(t, err)
	require.Nil(t, response.XSessionKeyOrEmpty)
	require.Zero(t, response.SetHeight)
	require.Equal(t, shared.NewAmount(50), response.Spent)
	response, err = server.UpdateSessionKey(ctx, sessionKeySetRequest(owner, created.SessionId, key, 220, 0))
	require.NoError(t, err)
	require.Equal(t, uint64(110), response.SetHeight, "same-block CLEAR/SET creates a new authorization")
	stream, err = f.keeper.ReadStream(ctx, created.SessionId)
	require.NoError(t, err)
	require.Equal(t, uint64(100), stream.LastActiveHeight)
	require.Equal(t, shared.NewAmount(50), stream.SessionKeySpent)
	stored, err := f.keeper.Stream.Get(ctx, created.SessionId)
	require.NoError(t, err)
	raw, err := stored.Marshal()
	require.NoError(t, err)
	require.NotContains(t, string(raw), owner)
	require.Equal(t, key, stored.GetSessionKey())
}

func TestSessionKeyInvalidUpdatesAreZeroWrite(t *testing.T) {
	f := initInternalFixture(t)
	ctx := sessionKeyContext(f, 10)
	server := NewMsgServerImpl(f.keeper)
	owner := sessionTestOwner(t, f)
	created, err := server.CreateSession(ctx, &types.MsgCreateSession{SignerAddress: owner})
	require.NoError(t, err)
	original, err := f.keeper.ReadStream(ctx, created.SessionId)
	require.NoError(t, err)
	for _, req := range []*types.MsgUpdateSessionKey{
		{SignerAddress: owner, SessionId: created.SessionId},
		sessionKeySetRequest(owner, created.SessionId, make([]byte, 20), 20, 0),
		sessionKeySetRequest(owner, created.SessionId, bytes.Repeat([]byte{2}, 19), 20, 0),
		sessionKeySetRequest(owner, created.SessionId, bytes.Repeat([]byte{2}, 20), 10, 0),
		sessionKeySetRequest(owner, created.SessionId, bytes.Repeat([]byte{2}, 20), 10+types.DefaultMaxSessionKeyBlocks+1, 0),
	} {
		_, err := server.UpdateSessionKey(ctx, req)
		require.ErrorIs(t, err, types.ErrSessionKeyInvalid)
		stream, err := f.keeper.ReadStream(ctx, created.SessionId)
		require.NoError(t, err)
		require.Equal(t, original, stream)
	}
	other, err := f.keeper.addressCodec.BytesToString(bytes.Repeat([]byte{0x42}, 20))
	require.NoError(t, err)
	_, err = server.UpdateSessionKey(ctx, sessionKeySetRequest(other, created.SessionId, bytes.Repeat([]byte{2}, 20), 20, 0))
	require.ErrorIs(t, err, sdkerrors.ErrUnauthorized)
	original.Status = types.SessionStatus_SESSION_STATUS_CLOSED
	require.NoError(t, f.keeper.WriteStream(ctx, created.SessionId, original))
	_, err = server.UpdateSessionKey(ctx, sessionKeySetRequest(owner, created.SessionId, bytes.Repeat([]byte{2}, 20), 20, 0))
	require.ErrorIs(t, err, types.ErrInvalidSessionID)
}

func TestSameSessionKeyCanBeRegisteredIndependently(t *testing.T) {
	f := initInternalFixture(t)
	ctx := sessionKeyContext(f, 10)
	server := NewMsgServerImpl(f.keeper)
	owner := sessionTestOwner(t, f)
	grant := &types.SessionKeyGrantV1{SessionKey: bytes.Repeat([]byte{2}, 20), ExpiryHeight: 100, SpendCap: shared.NewAmount(50)}
	first, err := server.CreateSession(ctx, &types.MsgCreateSession{SignerAddress: owner, SessionKey: grant})
	require.NoError(t, err)
	second, err := server.CreateSession(ctx, &types.MsgCreateSession{SignerAddress: owner, SessionKey: grant})
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

func TestSessionKeyInitializationUsesCurrentParamsForLegacySession(t *testing.T) {
	f := initInternalFixture(t)
	require.NoError(t, f.keeper.ConfigureSessionKeyActivation(100))
	ctx := sessionKeyContext(f, 100)
	params, err := f.keeper.Params.Get(ctx)
	require.NoError(t, err)
	params.Session.MaxSessionKeyBlocks = 0
	require.NoError(t, f.keeper.Params.Set(ctx, params))
	server := NewMsgServerImpl(f.keeper)
	owner := sessionTestOwner(t, f)
	created, err := server.CreateSession(ctx, &types.MsgCreateSession{SignerAddress: owner})
	require.NoError(t, err)
	require.NoError(t, f.keeper.InitializeSessionKeyParams(ctx))
	meta, err := f.keeper.GetTaskParamsMeta(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), meta.ParamsVersion)
	pinned, err := f.keeper.ParamsForSession(ctx, created.SessionId)
	require.NoError(t, err)
	require.Zero(t, pinned.Session.MaxSessionKeyBlocks)
	require.Equal(t, params.Session.SessionCloseTtlBlocks, pinned.Session.SessionCloseTtlBlocks)
	_, err = server.UpdateSessionKey(ctx, sessionKeySetRequest(owner, created.SessionId, bytes.Repeat([]byte{2}, 20), 200, 0))
	require.NoError(t, err)
	require.NoError(t, f.keeper.InitializeSessionKeyParams(ctx))
	repeated, err := f.keeper.GetTaskParamsMeta(ctx)
	require.NoError(t, err)
	require.Equal(t, meta, repeated)
	current, err := f.keeper.Params.Get(ctx)
	require.NoError(t, err)
	hash, err := types.TaskParamsHashV1("session-key-test", meta.ParamsVersion, current)
	require.NoError(t, err)
	require.Equal(t, hash, meta.ParamsHash)
	// A governance-chosen positive limit is not restored to the binary default.
	f.ctx = ctx
	updateParams(t, f, func(p *types.TaskParamsV1) { p.Session.MaxSessionKeyBlocks = 20 })
	require.NoError(t, f.keeper.InitializeSessionKeyParams(ctx))
	grantBeforeRenewal, err := f.keeper.ReadStream(ctx, created.SessionId)
	require.NoError(t, err)
	require.Equal(t, uint64(200), grantBeforeRenewal.SessionKeyExpiryHeight, "lowering the registration window does not shorten an existing grant")
	_, err = server.UpdateSessionKey(ctx, sessionKeySetRequest(owner, created.SessionId, bytes.Repeat([]byte{2}, 20), 121, 0))
	require.ErrorIs(t, err, types.ErrSessionKeyInvalid)
	_, err = server.UpdateSessionKey(ctx, sessionKeySetRequest(owner, created.SessionId, bytes.Repeat([]byte{2}, 20), 120, 0))
	require.NoError(t, err)
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

func TestSessionKeyWalletStoragePreservesLegacyBytes(t *testing.T) {
	f := initInternalFixture(t)
	owner := sessionTestOwner(t, f)
	state := types.StreamState{SessionId: bytes.Repeat([]byte{7}, types.Hash32Len), OwnerUserAddress: owner,
		NextExpectedSequence: 3, LastActiveHeight: 10, Status: types.SessionStatus_SESSION_STATUS_ACTIVE}
	stored, err := f.keeper.streamToStore(state)
	require.NoError(t, err)
	require.Empty(t, stored.SessionKeySpendCapAtomicUnits)
	require.Empty(t, stored.SessionKeySpentAtomicUnits)
	legacy := stored
	legacy.XSessionKey = nil
	legacy.SessionKeyExpiryHeight = 0
	legacy.SessionKeySetHeight = 0
	legacy.SessionKeySpendCapAtomicUnits = ""
	legacy.SessionKeySpentAtomicUnits = ""
	expected, err := legacy.Marshal()
	require.NoError(t, err)
	actual, err := stored.Marshal()
	require.NoError(t, err)
	require.Equal(t, expected, actual)
	projected, err := f.keeper.ProjectStreamStore(stored)
	require.NoError(t, err)
	require.Equal(t, shared.NewAmount(0), projected.SessionKeySpendCap)
	require.Equal(t, shared.NewAmount(0), projected.SessionKeySpent)
	roundTrip, err := f.keeper.streamToStore(projected)
	require.NoError(t, err)
	encoded, err := roundTrip.Marshal()
	require.NoError(t, err)
	require.Equal(t, expected, encoded)
}

func TestSessionKeyActivationPreservesPreBoundaryStore(t *testing.T) {
	f := initInternalFixture(t)
	require.NoError(t, f.keeper.ConfigureSessionKeyActivation(101))
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	params.Session.MaxSessionKeyBlocks = 0
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))
	snapshot := func(ctx context.Context) map[string]string {
		it, err := f.keeper.storeService.OpenKVStore(ctx).Iterator(nil, nil)
		require.NoError(t, err)
		defer it.Close()
		out := map[string]string{}
		for ; it.Valid(); it.Next() {
			out[string(it.Key())] = string(it.Value())
		}
		return out
	}
	before := snapshot(f.ctx)
	for _, height := range []int64{1, 50, 100} {
		ctx := sessionKeyContext(f, height)
		require.NoError(t, f.keeper.InitializeSessionKeyParams(ctx))
		require.Equal(t, before, snapshot(ctx), "no writes before activation")
	}
	ctx := sessionKeyContext(f, 101)
	require.NoError(t, f.keeper.InitializeSessionKeyParams(ctx))
	after := snapshot(ctx)
	require.NotEqual(t, before, after)
	current, err := f.keeper.Params.Get(ctx)
	require.NoError(t, err)
	require.Positive(t, current.Session.MaxSessionKeyBlocks)
	require.NoError(t, f.keeper.InitializeSessionKeyParams(sessionKeyContext(f, 102)))
	require.Equal(t, after, snapshot(ctx), "restart does not reinitialize")
	require.Error(t, f.keeper.ConfigureSessionKeyActivation(math.MaxUint64))
}

func TestSessionKeyBootstrapDisabledWithoutDeploymentHeight(t *testing.T) {
	f := initInternalFixture(t)
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	params.Session.MaxSessionKeyBlocks = 0
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))
	require.NoError(t, f.keeper.InitializeSessionKeyParams(sessionKeyContext(f, 100)))
	got, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	require.Equal(t, params, got)
	genesis := types.DefaultGenesis()
	genesis.Params = params
	require.Error(t, f.keeper.ValidateGenesis(*genesis))
	require.NoError(t, f.keeper.ConfigureSessionKeyActivation(101))
	require.NoError(t, f.keeper.ValidateGenesis(*genesis))
}
