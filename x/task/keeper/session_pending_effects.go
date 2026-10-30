package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

type sessionPendingAction uint8

const (
	sessionPendingWithdraw sessionPendingAction = iota
	sessionPendingKeyChange
	sessionPendingCancel
	sessionPendingTakeover
)

type sessionPendingRow struct {
	height    uint64
	sessionID []byte
	sequence  uint64
}

func (k Keeper) removeSessionPendingIndex(ctx context.Context, action sessionPendingAction, row sessionPendingRow) error {
	key := collections.Join(row.height, row.sessionID)
	switch action {
	case sessionPendingWithdraw:
		return k.SessionWithdrawIndex.Remove(ctx, key)
	case sessionPendingKeyChange:
		return k.SessionKeyChangeIndex.Remove(ctx, key)
	case sessionPendingCancel:
		return k.OrderCancelIndex.Remove(ctx, collections.Join3(row.height, row.sessionID, row.sequence))
	case sessionPendingTakeover:
		return k.SessionTakeoverIndex.Remove(ctx, key)
	default:
		return fmt.Errorf("unknown pending session action")
	}
}

func (k Keeper) applySessionPendingRow(ctx context.Context, action sessionPendingAction, row sessionPendingRow, height uint64) (bool, error) {
	if row.height > height {
		return false, nil
	}
	stream, err := k.ReadStream(ctx, row.sessionID)
	if errors.Is(err, collections.ErrNotFound) {
		return false, k.removeSessionPendingIndex(ctx, action, row)
	}
	if err != nil {
		return false, err
	}
	if !bytes.Equal(stream.SessionId, row.sessionID) {
		return false, fmt.Errorf("pending session index/primary mismatch")
	}
	previous := stream
	switch action {
	case sessionPendingWithdraw:
		return k.applyPendingSessionWithdrawal(ctx, stream, previous, row, height)
	case sessionPendingKeyChange:
		return k.applyPendingSessionKeyChange(ctx, stream, row, height)
	case sessionPendingCancel:
		return k.applyPendingSessionCancel(ctx, stream, previous, row, height)
	case sessionPendingTakeover:
		return k.applyPendingSessionTakeover(ctx, stream, previous, row, height)
	default:
		return false, fmt.Errorf("unknown pending session action")
	}
}

func (k Keeper) closeSessionState(ctx context.Context, previous, stream types.StreamState, height uint64) error {
	if stream.OpenPendingCount != 0 || stream.Balance.AtomicUnits != "0" || stream.FrozenTotal.AtomicUnits != "0" {
		return fmt.Errorf("session cannot close with funds or in-flight tasks")
	}
	if err := k.voidPendingTakeover(ctx, &stream, types.SessionTakeoverVoidReasonV1_SESSION_TAKEOVER_VOID_REASON_V1_SESSION_CLOSED); err != nil {
		return err
	}
	if pending := stream.GetPendingSessionKeyChange(); pending != nil {
		if err := k.SessionKeyChangeIndex.Remove(ctx, collections.Join(pending.EffectiveHeight, stream.SessionId)); err != nil {
			return err
		}
		stream.XPendingSessionKeyChange = nil
	}
	if previous.PendingWithdrawEffectiveHeight != 0 {
		if err := k.SessionWithdrawIndex.Remove(ctx, collections.Join(previous.PendingWithdrawEffectiveHeight, stream.SessionId)); err != nil {
			return err
		}
	}
	key := types.NewOrderSequenceStateKey(stream.SessionId, stream.NextExpectedSequence)
	if pending, err := k.OrderSequence.Get(ctx, key); err == nil && pending.Status == types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_CANCEL_PENDING {
		if err := k.OrderCancelIndex.Remove(ctx, collections.Join3(pending.CancelEffectiveHeight, stream.SessionId, pending.OrderSequence)); err != nil {
			return err
		}
		if err := k.OrderSequence.Remove(ctx, key); err != nil {
			return err
		}
	} else if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	stream.Status = types.SessionStatus_SESSION_STATUS_CLOSED
	stream.LastActiveHeight = height
	stream.PendingWithdrawKind = types.SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_NONE
	stream.PendingWithdrawAmount = shared.NewAmount(0)
	stream.PendingWithdrawEffectiveHeight = 0
	if err := k.replaceStreamState(ctx, previous, stream); err != nil {
		return err
	}
	if err := k.removeSessionByOwnerIndex(ctx, stream.OwnerUserAddress, stream.SessionId); err != nil {
		return err
	}
	params, err := k.ParamsForSession(ctx, stream.SessionId)
	if err != nil {
		return err
	}
	eligible, overflow := shared.CheckedHeightAddV1(height, params.Session.SessionOrderRetentionBlocks)
	if overflow {
		return fmt.Errorf("session history retention height overflows")
	}
	return k.SessionHistoryPruneIndex.Set(ctx, types.NewSessionHistoryPruneIndexKey(eligible, stream.SessionId))
}

func (k Keeper) applyPendingSessionWithdrawal(ctx context.Context, stream, previous types.StreamState, row sessionPendingRow, height uint64) (bool, error) {
	if stream.PendingWithdrawEffectiveHeight != row.height || stream.PendingWithdrawKind == types.SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_NONE {
		return false, k.removeSessionPendingIndex(ctx, sessionPendingWithdraw, row)
	}
	kind := stream.PendingWithdrawKind
	if kind == types.SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_CLOSE && stream.OpenPendingCount != 0 {
		return false, nil
	}
	balance, err := shared.ParseAmount(stream.Balance)
	if err != nil {
		return false, err
	}
	frozen, err := shared.ParseAmount(stream.FrozenTotal)
	if err != nil || frozen > balance {
		return false, fmt.Errorf("session withdrawal has invalid frozen funds")
	}
	amount, err := shared.ParseAmount(stream.PendingWithdrawAmount)
	if err != nil {
		return false, err
	}
	payout := min(amount, balance-frozen)
	if kind == types.SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_CLOSE {
		if frozen != 0 {
			return false, fmt.Errorf("session without in-flight tasks retains frozen funds")
		}
		payout = balance
	}
	if payout != 0 {
		owner, err := k.sessionAddressToStore("session withdrawal owner", stream.OwnerUserAddress)
		if err != nil {
			return false, err
		}
		coins, err := k.sessionCoins(ctx, payout)
		if err != nil {
			return false, err
		}
		if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, shared.TaskEscrowModuleName, sdk.AccAddress(owner), coins); err != nil {
			return false, err
		}
	}
	stream.Balance = shared.NewAmount(balance - payout)
	stream.PendingWithdrawKind = types.SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_NONE
	stream.PendingWithdrawAmount = shared.NewAmount(0)
	stream.PendingWithdrawEffectiveHeight = 0
	if err := k.removeSessionPendingIndex(ctx, sessionPendingWithdraw, row); err != nil {
		return false, err
	}
	if kind == types.SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_CLOSE {
		if err := k.closeSessionState(ctx, previous, stream, height); err != nil {
			return false, err
		}
	} else if err := k.WriteStream(ctx, row.sessionID, stream); err != nil {
		return false, err
	}
	return true, emitTypedEvent(ctx, &types.EventSessionWithdrawEffective{SessionId: row.sessionID, Kind: kind, PaidAmount: shared.NewAmount(payout), RemainingBalance: stream.Balance})
}

func (k Keeper) applyPendingSessionKeyChange(ctx context.Context, stream types.StreamState, row sessionPendingRow, height uint64) (bool, error) {
	pending := stream.GetPendingSessionKeyChange()
	if pending == nil || pending.EffectiveHeight != row.height {
		return false, k.removeSessionPendingIndex(ctx, sessionPendingKeyChange, row)
	}
	switch pending.Kind {
	case types.SessionKeyChangeKindV1_SESSION_KEY_CHANGE_KIND_V1_SET:
		if pending.GetGrant() == nil {
			return false, fmt.Errorf("pending session SET grant is missing")
		}
		setSessionKey(&stream, pending.GetGrant(), height)
	case types.SessionKeyChangeKindV1_SESSION_KEY_CHANGE_KIND_V1_CLEAR:
		stream.XSessionKey = nil
		stream.SessionKeySetHeight = 0
		stream.SessionKeyExpiryHeight = 0
		stream.SessionKeySpendCap = shared.NewAmount(0)
	default:
		return false, fmt.Errorf("pending session key change kind is invalid")
	}
	stream.XPendingSessionKeyChange = nil
	if err := k.WriteStream(ctx, row.sessionID, stream); err != nil {
		return false, err
	}
	if err := k.removeSessionPendingIndex(ctx, sessionPendingKeyChange, row); err != nil {
		return false, err
	}
	return true, emitSessionKeyUpdated(ctx, stream)
}

func (k Keeper) applyPendingSessionCancel(ctx context.Context, stream, previous types.StreamState, row sessionPendingRow, height uint64) (bool, error) {
	key := types.NewOrderSequenceStateKey(row.sessionID, row.sequence)
	sequence, err := k.OrderSequence.Get(ctx, key)
	if errors.Is(err, collections.ErrNotFound) {
		return false, k.removeSessionPendingIndex(ctx, sessionPendingCancel, row)
	}
	if err != nil {
		return false, err
	}
	if sequence.Status != types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_CANCEL_PENDING || sequence.CancelEffectiveHeight != row.height {
		return false, k.removeSessionPendingIndex(ctx, sessionPendingCancel, row)
	}
	if stream.Status == types.SessionStatus_SESSION_STATUS_CLOSED {
		return false, fmt.Errorf("closed session retained a pending order cancellation")
	}
	if stream.NextExpectedSequence != row.sequence {
		return false, fmt.Errorf("pending order cancellation is not the next session sequence")
	}
	next, overflow := checkedSessionAddUint64(stream.NextExpectedSequence, 1)
	if overflow {
		return false, fmt.Errorf("cancelled session sequence overflows")
	}
	sequence.Status = types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_CANCELLED
	sequence.CancelledHeight = height
	sequence.CancelEffectiveHeight = 0
	stream.NextExpectedSequence = next
	stream.LastActiveHeight = height
	stream.Status = types.SessionStatus_SESSION_STATUS_ACTIVE
	if err := k.voidPendingTakeover(ctx, &stream, types.SessionTakeoverVoidReasonV1_SESSION_TAKEOVER_VOID_REASON_V1_ORDER_CANCELLED); err != nil {
		return false, err
	}
	if err := k.OrderSequence.Set(ctx, key, sequence); err != nil {
		return false, err
	}
	if err := k.replaceStreamState(ctx, previous, stream); err != nil {
		return false, err
	}
	if err := k.removeSessionPendingIndex(ctx, sessionPendingCancel, row); err != nil {
		return false, err
	}
	taskID, err := types.DeriveTaskIDFromRawSession(row.sessionID, row.sequence)
	if err != nil {
		return false, err
	}
	return true, emitTypedEvent(ctx, &types.EventOrderCancelled{SessionId: row.sessionID, OrderSequence: row.sequence, TaskId: taskID[:]})
}

func (k Keeper) applyPendingSessionTakeover(ctx context.Context, stream, previous types.StreamState, row sessionPendingRow, height uint64) (bool, error) {
	pending := stream.GetPendingTakeover()
	if pending == nil || pending.ActivationHeight != row.height {
		return false, k.removeSessionPendingIndex(ctx, sessionPendingTakeover, row)
	}
	if stream.Status == types.SessionStatus_SESSION_STATUS_CLOSED || stream.NextExpectedSequence != pending.OrderSequence {
		reason := types.SessionTakeoverVoidReasonV1_SESSION_TAKEOVER_VOID_REASON_V1_ORDER_RECORDED
		if stream.Status == types.SessionStatus_SESSION_STATUS_CLOSED {
			reason = types.SessionTakeoverVoidReasonV1_SESSION_TAKEOVER_VOID_REASON_V1_SESSION_CLOSED
		} else if state, err := k.OrderSequence.Get(ctx, types.NewOrderSequenceStateKey(row.sessionID, pending.OrderSequence)); err == nil && state.Status == types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_CANCELLED {
			reason = types.SessionTakeoverVoidReasonV1_SESSION_TAKEOVER_VOID_REASON_V1_ORDER_CANCELLED
		} else if err != nil && !errors.Is(err, collections.ErrNotFound) {
			return false, err
		}
		if err := k.voidPendingTakeover(ctx, &stream, reason); err != nil {
			return false, err
		}
		return true, k.WriteStream(ctx, row.sessionID, stream)
	}
	oldBuilder := stream.BoundBuilder
	stream.BoundBuilder = pending.CandidateBuilder
	stream.BoundChangeHeight = height
	stream.LastRecordHeight = height
	stream.LastActiveHeight = height
	stream.XPendingTakeover = nil
	if err := k.replaceStreamState(ctx, previous, stream); err != nil {
		return false, err
	}
	if err := k.removeSessionPendingIndex(ctx, sessionPendingTakeover, row); err != nil {
		return false, err
	}
	return true, emitTypedEvent(ctx, &types.EventSessionBuilderBound{SessionId: row.sessionID, XOldBuilderOrEmpty: &types.EventSessionBuilderBound_OldBuilderOrEmpty{OldBuilderOrEmpty: oldBuilder}, NewBuilder: stream.BoundBuilder, Trigger: types.SessionBuilderBoundTriggerV1_SESSION_BUILDER_BOUND_TRIGGER_V1_TAKEOVER, BoundChangeHeight: height})
}
