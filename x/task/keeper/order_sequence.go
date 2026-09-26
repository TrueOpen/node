package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"

	"github.com/TrueOpen/node/x/task/types"
)

// consumeOrderSequence atomically records the accepted order and advances the
// stream accounting. The enclosing Msg cache context also contains the Task and
// TaskBudget writes, so a later failure rolls the whole admission back.
func (k Keeper) consumeOrderSequence(ctx context.Context, sessionID []byte, orderSequence uint64, taskID []byte) error {
	sessionKey, err := sessionStoreKey(sessionID)
	if err != nil {
		return err
	}
	stream, err := k.ReadStream(ctx, sessionKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return errorsmod.Wrap(types.ErrInvalidSessionID, "session not found")
		}
		return err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	if err := ensureSessionCanConsumeSequence(stream, params.Session.MaxOrderSequencesPerActiveSession); err != nil {
		return err
	}
	if orderSequence != stream.NextExpectedSequence {
		return errorsmod.Wrapf(types.ErrInvalidOrderSequence, "order_sequence %d, expected %d", orderSequence, stream.NextExpectedSequence)
	}
	key := types.NewOrderSequenceStateKey(sessionKey, orderSequence)
	if existing, err := k.OrderSequence.Get(ctx, key); err == nil {
		return errorsmod.Wrapf(types.ErrInvalidOrderSequence, "order_sequence already %s", existing.Status)
	} else if !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return err
	}
	previous := stream
	if err := k.OrderSequence.Set(ctx, key, types.OrderSequenceState{
		SessionId:      append([]byte(nil), sessionID...),
		OrderSequence:  orderSequence,
		Status:         types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_CONSUMED,
		TaskId:         append([]byte(nil), taskID...),
		ConsumedHeight: height,
	}); err != nil {
		return err
	}
	nextSequence, overflow := checkedSessionAddUint64(stream.NextExpectedSequence, 1)
	if overflow {
		return errorsmod.Wrap(types.ErrInvariantBroken, "session next_expected_sequence overflow")
	}
	nextPending, overflow := checkedSessionAddUint64(uint64(stream.OpenPendingCount), 1)
	if overflow || nextPending > uint64(^uint32(0)) {
		return errorsmod.Wrap(types.ErrInvariantBroken, "session open_pending_count overflow")
	}
	stream.NextExpectedSequence = nextSequence
	stream.OpenPendingCount = uint32(nextPending)
	stream.LastActiveHeight = height
	stream.Status = types.SessionStatus_SESSION_STATUS_ACTIVE
	return k.replaceStreamState(ctx, previous, stream)
}

func (k Keeper) finalizeOrderSequenceState(ctx context.Context, taskID []byte, status types.OrderSequenceStatus) error {
	if status != types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_REFUNDED && status != types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_SETTLED {
		return errorsmod.Wrap(types.ErrInvalidOrderSequence, "terminal order status must be REFUNDED or SETTLED")
	}
	taskKey, err := taskStoreKey(taskID)
	if err != nil {
		return err
	}
	core, err := k.TaskCore.Get(ctx, taskKey)
	if err != nil {
		return errorsmod.Wrap(types.ErrTaskNotFound, "task core not found for order sequence")
	}
	sessionKey, err := sessionStoreKey(core.SessionId)
	if err != nil {
		return err
	}
	key := types.NewOrderSequenceStateKey(sessionKey, core.OrderSequence)
	state, err := k.OrderSequence.Get(ctx, key)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return errorsmod.Wrap(types.ErrInvalidOrderSequence, "consumed order sequence is missing")
		}
		return err
	}
	if string(state.SessionId) != string(core.SessionId) || state.OrderSequence != core.OrderSequence || string(state.TaskId) != string(taskID) {
		return errorsmod.Wrap(types.ErrInvalidOrderSequence, "order sequence identity does not match task core")
	}
	if state.Status == status {
		return nil
	}
	if state.Status != types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_CONSUMED {
		return errorsmod.Wrapf(types.ErrInvalidOrderSequence, "order sequence is %s for another task", state.Status)
	}
	state.Status = status
	return k.OrderSequence.Set(ctx, key, state)
}
