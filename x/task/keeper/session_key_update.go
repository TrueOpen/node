package keeper

import (
	"bytes"
	"context"
	"fmt"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (m *msgServer) UpdateSessionKey(ctx context.Context, req *types.MsgUpdateSessionKey) (*types.MsgUpdateSessionKeyResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}
	cacheCtx, commit := sdk.UnwrapSDKContext(ctx).CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	stream, owner, err := m.k.sessionOwner(cache, req.SessionId, req.SignerAddress)
	if err != nil {
		return nil, err
	}
	if stream.GetPendingSessionKeyChange() != nil {
		return nil, types.ErrSessionChangePending
	}
	height, err := currentBlockHeight(cache)
	if err != nil {
		return nil, err
	}
	change := &types.PendingSessionKeyChangeV1{}
	immediate, unchanged := false, false
	switch action := req.Action.(type) {
	case *types.MsgUpdateSessionKey_Set:
		if action == nil || action.Set == nil {
			return nil, fmt.Errorf("SET requires a grant")
		}
		grant := action.Set
		oldCap, err := shared.ParseAmount(stream.SessionKeySpendCap)
		if err != nil {
			return nil, err
		}
		newCap, err := shared.ParseAmount(grant.SpendCap)
		if err != nil {
			return nil, errorsmod.Wrap(types.ErrSessionKeyInvalid, err.Error())
		}
		sameKey := stream.XSessionKey != nil && bytes.Equal(stream.GetSessionKey(), grant.SessionKey)
		immediate = sameKey && grant.ExpiryHeight >= stream.SessionKeyExpiryHeight && (newCap == 0 || oldCap != 0 && newCap >= oldCap)
		unchanged = immediate && grant.ExpiryHeight == stream.SessionKeyExpiryHeight && newCap == oldCap
		baseHeight := height
		if !immediate {
			effective, err := m.k.sessionKeyChangeHeight(cache, height)
			if err != nil {
				return nil, err
			}
			baseHeight = effective
			change.EffectiveHeight = effective
		}
		if err := m.k.validateSessionKeyGrant(cache, grant, owner, baseHeight); err != nil {
			return nil, err
		}
		change.Kind = types.SessionKeyChangeKindV1_SESSION_KEY_CHANGE_KIND_V1_SET
		change.XGrant = &types.PendingSessionKeyChangeV1_Grant{Grant: &types.SessionKeyGrantV1{SessionKey: bytes.Clone(grant.SessionKey), ExpiryHeight: grant.ExpiryHeight, SpendCap: grant.SpendCap}}
	case *types.MsgUpdateSessionKey_Clear:
		if action == nil || action.Clear == nil {
			return nil, fmt.Errorf("CLEAR requires an action")
		}
		unchanged = stream.XSessionKey == nil
		change.Kind = types.SessionKeyChangeKindV1_SESSION_KEY_CHANGE_KIND_V1_CLEAR
	default:
		return nil, errorsmod.Wrap(types.ErrSessionKeyInvalid, "exactly one SET/CLEAR action is required")
	}
	status := shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP
	if !unchanged {
		if immediate {
			setSessionKey(&stream, change.GetGrant(), height)
			if err := emitSessionKeyUpdated(cache, stream); err != nil {
				return nil, err
			}
		} else {
			if change.EffectiveHeight == 0 {
				effective, err := m.k.sessionKeyChangeHeight(cache, height)
				if err != nil {
					return nil, err
				}
				change.EffectiveHeight = effective
			}
			effective := change.EffectiveHeight
			stream.XPendingSessionKeyChange = &types.StreamState_PendingSessionKeyChange{PendingSessionKeyChange: change}
			if err := m.k.SessionKeyChangeIndex.Set(cache, collections.Join(effective, req.SessionId)); err != nil {
				return nil, err
			}
			if err := emitTypedEvent(cache, &types.EventSessionKeyChangeRequested{SessionId: bytes.Clone(req.SessionId), Kind: change.Kind, EffectiveHeight: effective}); err != nil {
				return nil, err
			}
		}
		if err := m.k.WriteStream(cache, types.NewSessionStoreKey(req.SessionId), stream); err != nil {
			return nil, err
		}
		status = shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED
	}
	response := &types.MsgUpdateSessionKeyResponse{ExpiryHeight: stream.SessionKeyExpiryHeight, SpendCap: stream.SessionKeySpendCap, Spent: stream.SessionKeySpent, SetHeight: stream.SessionKeySetHeight, Status: status}
	if stream.XSessionKey != nil {
		response.XSessionKeyOrEmpty = &types.MsgUpdateSessionKeyResponse_SessionKeyOrEmpty{SessionKeyOrEmpty: bytes.Clone(stream.GetSessionKey())}
	}
	if pending := stream.GetPendingSessionKeyChange(); pending != nil {
		response.XEffectiveHeight = &types.MsgUpdateSessionKeyResponse_EffectiveHeight{EffectiveHeight: pending.EffectiveHeight}
		response.XPendingChange = &types.MsgUpdateSessionKeyResponse_PendingChange{PendingChange: pending}
	}
	commit()
	return response, nil
}

func (k Keeper) sessionKeyChangeHeight(ctx context.Context, height uint64) (uint64, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, err
	}
	effective, overflow := shared.CheckedHeightAddV1(height, params.Session.SessionChangeDelayBlocks)
	if overflow || params.Session.SessionChangeDelayBlocks == 0 {
		return 0, fmt.Errorf("invalid session key effective height")
	}
	return effective, nil
}
