package keeper

import (
	"bytes"
	"context"
	"fmt"
	"math"

	errorsmod "cosmossdk.io/errors"

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
