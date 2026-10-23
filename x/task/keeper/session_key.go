package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) validateSessionKeyGrant(ctx context.Context, grant *types.SessionKeyGrantV1, owner []byte, height uint64) error {
	if grant == nil {
		return errorsmod.Wrap(types.ErrSessionKeyInvalid, "missing grant")
	}
	if err := types.ValidateSessionKeyAddress(grant.SessionKey, owner); err != nil {
		return errorsmod.Wrap(types.ErrSessionKeyInvalid, err.Error())
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	limit := params.Session.MaxSessionKeyBlocks
	upper, overflow := shared.CheckedHeightAddV1(height, limit)
	if height == 0 || limit == 0 || overflow || grant.ExpiryHeight <= height || grant.ExpiryHeight > upper {
		return errorsmod.Wrap(types.ErrSessionKeyInvalid, "expiry is outside the current registration window")
	}
	if _, err := shared.ParseAmount(grant.SpendCap); err != nil {
		return errorsmod.Wrap(types.ErrSessionKeyInvalid, err.Error())
	}
	return nil
}

func setSessionKey(stream *types.StreamState, grant *types.SessionKeyGrantV1, height uint64) {
	if stream.XSessionKey == nil || !bytes.Equal(stream.GetSessionKey(), grant.SessionKey) {
		stream.SessionKeySetHeight = height
	}
	stream.XSessionKey = &types.StreamState_SessionKey{SessionKey: bytes.Clone(grant.SessionKey)}
	stream.SessionKeyExpiryHeight = grant.ExpiryHeight
	stream.SessionKeySpendCap = grant.SpendCap
}

func emitSessionKeyUpdated(ctx context.Context, stream types.StreamState) error {
	event := &types.EventSessionKeyUpdated{
		SessionId: bytes.Clone(stream.SessionId), Owner: stream.OwnerUserAddress,
		ExpiryHeight: stream.SessionKeyExpiryHeight, SpendCap: stream.SessionKeySpendCap,
		Spent: stream.SessionKeySpent, SetHeight: stream.SessionKeySetHeight,
	}
	if stream.XSessionKey != nil {
		event.XSessionKeyOrEmpty = &types.EventSessionKeyUpdated_SessionKeyOrEmpty{SessionKeyOrEmpty: bytes.Clone(stream.GetSessionKey())}
	}
	return emitTypedEvent(ctx, event)
}

func (m *msgServer) UpdateSessionKey(ctx context.Context, req *types.MsgUpdateSessionKey) (*types.MsgUpdateSessionKeyResponse, error) {
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrSessionKeyInvalid, "nil request")
	}
	cacheCtx, commit := sdk.UnwrapSDKContext(ctx).CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	key, err := sessionStoreKey(req.SessionId)
	if err != nil {
		return nil, err
	}
	stream, err := m.k.ReadStream(cache, key)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, errorsmod.Wrap(types.ErrInvalidSessionID, "session not found")
		}
		return nil, err
	}
	if stream.Status != types.SessionStatus_SESSION_STATUS_ACTIVE && stream.Status != types.SessionStatus_SESSION_STATUS_IDLE {
		return nil, errorsmod.Wrap(types.ErrInvalidSessionID, "session cannot update authorization")
	}
	ownerRaw, owner, err := m.k.canonicalAddress("signer_address", req.SignerAddress)
	if err != nil || owner != stream.OwnerUserAddress {
		return nil, errorsmod.Wrap(sdkerrors.ErrUnauthorized, "signer must be session owner")
	}
	height, err := currentBlockHeight(cache)
	if err != nil {
		return nil, err
	}
	previous := stream
	switch action := req.Action.(type) {
	case *types.MsgUpdateSessionKey_Set:
		if action == nil {
			return nil, errorsmod.Wrap(types.ErrSessionKeyInvalid, "missing SET")
		}
		if err := m.k.validateSessionKeyGrant(cache, action.Set, ownerRaw, height); err != nil {
			return nil, err
		}
		setSessionKey(&stream, action.Set, height)
	case *types.MsgUpdateSessionKey_Clear:
		if action == nil || action.Clear == nil {
			return nil, errorsmod.Wrap(types.ErrSessionKeyInvalid, "missing CLEAR")
		}
		stream.XSessionKey = nil
		stream.SessionKeyExpiryHeight = 0
		stream.SessionKeySetHeight = 0
		stream.SessionKeySpendCap = shared.NewAmount(0)
	default:
		return nil, errorsmod.Wrap(types.ErrSessionKeyInvalid, "exactly one SET/CLEAR action is required")
	}
	status := shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP
	if !bytes.Equal(previous.GetSessionKey(), stream.GetSessionKey()) || previous.SessionKeyExpiryHeight != stream.SessionKeyExpiryHeight ||
		previous.SessionKeySpendCap != stream.SessionKeySpendCap || previous.SessionKeySetHeight != stream.SessionKeySetHeight {
		// No lifecycle mutation: neither status, last_active_height nor indexes move.
		if err := m.k.WriteStream(cache, key, stream); err != nil {
			return nil, err
		}
		if err := emitSessionKeyUpdated(cache, stream); err != nil {
			return nil, err
		}
		status = shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED
	}
	response := &types.MsgUpdateSessionKeyResponse{ExpiryHeight: stream.SessionKeyExpiryHeight, SpendCap: stream.SessionKeySpendCap,
		Spent: stream.SessionKeySpent, SetHeight: stream.SessionKeySetHeight, Status: status}
	if stream.XSessionKey != nil {
		response.XSessionKeyOrEmpty = &types.MsgUpdateSessionKeyResponse_SessionKeyOrEmpty{SessionKeyOrEmpty: bytes.Clone(stream.GetSessionKey())}
	}
	commit()
	return response, nil
}

// authorizeOrderSigner returns whether admission consumes delegated allowance.
// No writes occur until all subsequent admission work succeeds in the Msg cache.
func authorizeOrderSigner(stream types.StreamState, owner []byte, recovered types.EIP712RecoveredSigner, accountKey types.TaskOrderEIP712AccountPublicKey, order types.TaskOrderV3, height uint64) (bool, uint64, error) {
	ownerKey, err := types.TaskOrderAccountPublicKey(accountKey)
	if err != nil {
		return false, 0, err
	}
	if bytes.Equal(recovered.Address, owner) {
		if !bytes.Equal(recovered.PublicKey, ownerKey) {
			return false, 0, fmt.Errorf("recovered signer public key does not match the user account public key")
		}
		return false, 0, nil
	}
	if stream.XSessionKey == nil {
		return false, 0, types.ErrSessionKeyInvalid
	}
	if !bytes.Equal(recovered.Address, stream.GetSessionKey()) || order.SessionAnchorHeight < stream.SessionKeySetHeight {
		return false, 0, types.ErrSessionKeyInvalid
	}
	if height > stream.SessionKeyExpiryHeight {
		return false, 0, types.ErrSessionKeyExpired
	}
	spent, err := shared.ParseAmount(stream.SessionKeySpent)
	if err != nil {
		return false, 0, err
	}
	cap, err := shared.ParseAmount(stream.SessionKeySpendCap)
	if err != nil {
		return false, 0, err
	}
	fee, err := shared.ParseAmount(order.MaxFee)
	if err != nil {
		return false, 0, err
	}
	if fee > math.MaxUint64-spent {
		if cap != 0 {
			return false, 0, types.ErrSessionKeySpendCapExceeded
		}
		return false, 0, errorsmod.Wrap(types.ErrInvalidAssignment, "session key cumulative max_fee overflows uint64")
	}
	if cap != 0 && spent+fee > cap {
		return false, 0, types.ErrSessionKeySpendCapExceeded
	}
	return true, spent + fee, nil
}
