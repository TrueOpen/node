package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) authorizeSessionOrder(ctx context.Context, signed types.SignedOrderV2, height uint64) (dispatchOrderAdmission, error) {
	var result dispatchOrderAdmission
	order := signed.Order
	if order.ChainId != sdkContextFrom(ctx).ChainID() || height > order.OrderExpireHeight {
		return result, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_ORDER_SIGNATURE_INVALID, fmt.Errorf("order chain or expiry is invalid"))
	}
	taskHash, err := types.TaskOrderHash(order)
	if err != nil {
		return result, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_ORDER_AMOUNT_INVALID, err)
	}
	taskID, err := types.DeriveTaskIDFromRawSession(order.SessionId, order.OrderSequence)
	if err != nil {
		return result, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_SESSION_INVALID, err)
	}
	ownerBytes, owner, err := k.canonicalAddress("user_address", order.UserAddress)
	if err != nil {
		return result, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_ORDER_SIGNATURE_INVALID, err)
	}
	stream, err := k.ReadStream(ctx, types.NewSessionStoreKey(order.SessionId))
	if errors.Is(err, collections.ErrNotFound) {
		return result, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_SESSION_INVALID, err)
	}
	if err != nil {
		return result, err
	}
	if stream.OwnerUserAddress != owner || (stream.Status != types.SessionStatus_SESSION_STATUS_ACTIVE && stream.Status != types.SessionStatus_SESSION_STATUS_IDLE) {
		return result, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_SESSION_INVALID, fmt.Errorf("order owner or session status is invalid"))
	}
	account := k.authKeeper.GetAccount(ctx, sdk.AccAddress(ownerBytes))
	if account == nil || account.GetPubKey() == nil {
		return result, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_ORDER_SIGNATURE_INVALID, fmt.Errorf("owner account public key is required"))
	}
	denom := k.hubKeeper.GetHubParams(sdkContextFrom(ctx)).BusinessDenom
	if denom == "" {
		return result, fmt.Errorf("business denomination is unavailable")
	}
	recovered, err := types.RecoverSignedOrderV2EIP712(denom, signed, taskHash[:])
	if err != nil {
		return result, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_ORDER_SIGNATURE_INVALID, err)
	}
	delegated, nextSpent, err := authorizeOrderSigner(stream, ownerBytes, recovered, account.GetPubKey(), order, height)
	if err != nil {
		if _, business := dispatchRejectionCode(err); business {
			return result, err
		}
		return result, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_ORDER_SIGNATURE_INVALID, err)
	}
	return dispatchOrderAdmission{order: order, taskID: taskID, taskHash: taskHash, stream: stream, delegated: delegated, nextSpent: nextSpent}, nil
}

func (k Keeper) requireUnusedDispatchSequence(ctx context.Context, admission dispatchOrderAdmission) error {
	if admission.order.OrderSequence > admission.stream.NextExpectedSequence {
		return rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_SEQUENCE_NOT_NEXT, fmt.Errorf("order_sequence is ahead of next_expected_sequence"))
	}
	if admission.order.OrderSequence < admission.stream.NextExpectedSequence {
		return rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_SEQUENCE_CONSUMED, fmt.Errorf("order_sequence is behind next_expected_sequence"))
	}
	params, err := k.ParamsForSession(ctx, types.NewSessionStoreKey(admission.order.SessionId))
	if err != nil {
		return err
	}
	if admission.stream.NextExpectedSequence >= uint64(params.Session.MaxOrderSequencesPerActiveSession) {
		return rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_ORDER_SEQUENCE_LIMIT, fmt.Errorf("session order sequence limit reached"))
	}
	state, err := k.OrderSequence.Get(ctx, types.NewOrderSequenceStateKey(admission.order.SessionId, admission.order.OrderSequence))
	if errors.Is(err, collections.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if state.Status != types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_UNUSED && state.Status != types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_CANCEL_PENDING {
		return rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_SEQUENCE_CONSUMED, fmt.Errorf("order sequence is already consumed"))
	}
	if !bytes.Equal(state.SessionId, admission.order.SessionId) || state.OrderSequence != admission.order.OrderSequence {
		return fmt.Errorf("order sequence key/value mismatch")
	}
	return nil
}
