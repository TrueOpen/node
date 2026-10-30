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

func (m *msgServer) cancelOrder(ctx context.Context, req *types.MsgCancelOrder) (*types.MsgCancelOrderResponse, error) {
	stream, _, err := m.k.sessionOwner(ctx, req.SessionId, req.SignerAddress)
	if err != nil {
		return nil, err
	}
	params, err := m.k.ParamsForSession(ctx, types.NewSessionStoreKey(req.SessionId))
	if err != nil {
		return nil, err
	}
	if req.OrderSequence != stream.NextExpectedSequence || req.OrderSequence >= uint64(params.Session.MaxOrderSequencesPerActiveSession) {
		return nil, errorsmod.Wrap(types.ErrInvalidOrderSequence, "cancel requires the next unused sequence")
	}
	key := types.NewOrderSequenceStateKey(types.NewSessionStoreKey(req.SessionId), req.OrderSequence)
	if previous, err := m.k.OrderSequence.Get(ctx, key); err == nil {
		if !bytes.Equal(previous.SessionId, req.SessionId) || previous.OrderSequence != req.OrderSequence {
			return nil, errorsmod.Wrap(types.ErrInvariantBroken, "cancel sequence key/value mismatch")
		}
		if previous.Status == types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_CANCEL_PENDING {
			return nil, types.ErrSessionChangePending
		}
		if previous.Status != types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_UNUSED {
			return nil, errorsmod.Wrap(types.ErrInvalidOrderSequence, "order sequence is already consumed")
		}
	} else if !errIsNotFound(err) {
		return nil, err
	}
	currentParams, err := m.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return nil, err
	}
	effective, overflow := shared.CheckedHeightAddV1(height, currentParams.Session.SessionChangeDelayBlocks)
	if overflow || currentParams.Session.SessionChangeDelayBlocks == 0 {
		return nil, fmt.Errorf("invalid order cancellation effective height")
	}
	gasMeter := sdk.UnwrapSDKContext(ctx).GasMeter()
	if gasMeter.GasRemaining() < currentParams.Session.CancelOrderMinGas {
		return nil, errorsmod.Wrap(types.ErrInvalidOrderSequence, "cancel order has insufficient remaining gas")
	}
	gasMeter.ConsumeGas(currentParams.Session.CancelOrderMinGas, "cancel order guard")
	if err := m.k.OrderSequence.Set(ctx, key, types.OrderSequenceState{SessionId: bytes.Clone(req.SessionId), OrderSequence: req.OrderSequence, Status: types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_CANCEL_PENDING, CancelEffectiveHeight: effective}); err != nil {
		return nil, err
	}
	if err := m.k.OrderCancelIndex.Set(ctx, collections.Join3(effective, req.SessionId, req.OrderSequence)); err != nil {
		return nil, err
	}
	previous := stream
	stream.LastActiveHeight, stream.Status = height, types.SessionStatus_SESSION_STATUS_ACTIVE
	if err := m.k.replaceStreamState(ctx, previous, stream); err != nil {
		return nil, err
	}
	if err := emitTypedEvent(ctx, &types.EventOrderCancelRequested{SessionId: bytes.Clone(req.SessionId), OrderSequence: req.OrderSequence, CancelEffectiveHeight: effective}); err != nil {
		return nil, err
	}
	return &types.MsgCancelOrderResponse{CancelledSequence: req.OrderSequence, NextExpectedSequence: stream.NextExpectedSequence, CancelEffectiveHeight: effective, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED}, nil
}
