package keeper

import (
	"bytes"
	"context"
	"fmt"

	"cosmossdk.io/collections"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// requestSessionWithdrawal is shared by explicit withdrawals and TTL closure.
// Replacing AMOUNT with CLOSE restarts the delay from this registration height.
func (k Keeper) requestSessionWithdrawal(ctx context.Context, stream *types.StreamState, kind types.SessionWithdrawKindV1, amount shared.Amount, height, delay uint64) error {
	if stream.PendingWithdrawKind == types.SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_CLOSE {
		return types.ErrSessionWithdrawPending
	}
	if kind == types.SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_AMOUNT && stream.PendingWithdrawKind == kind {
		return types.ErrSessionWithdrawPending
	}
	effective, overflow := shared.CheckedHeightAddV1(height, delay)
	if overflow || delay == 0 {
		return fmt.Errorf("invalid withdrawal effective height")
	}
	value, err := shared.ParseAmount(amount)
	if err != nil {
		return err
	}
	switch kind {
	case types.SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_AMOUNT:
		balance, err := shared.ParseAmount(stream.Balance)
		if err != nil {
			return err
		}
		frozen, err := shared.ParseAmount(stream.FrozenTotal)
		if err != nil || frozen > balance {
			return fmt.Errorf("session reservation exceeds its balance")
		}
		if value == 0 || value > balance-frozen {
			return fmt.Errorf("withdrawal amount exceeds available session funds")
		}
	case types.SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_CLOSE:
		if value != 0 {
			return fmt.Errorf("CLOSE cannot carry a fixed withdrawal amount")
		}
	default:
		return fmt.Errorf("withdrawal kind is invalid")
	}
	if stream.PendingWithdrawEffectiveHeight != 0 {
		if err := k.SessionWithdrawIndex.Remove(ctx, collections.Join(stream.PendingWithdrawEffectiveHeight, stream.SessionId)); err != nil {
			return err
		}
	}
	stream.PendingWithdrawKind = kind
	stream.PendingWithdrawAmount = amount
	stream.PendingWithdrawEffectiveHeight = effective
	if err := k.syncSessionWithdrawalIndex(ctx, *stream); err != nil {
		return err
	}
	return emitTypedEvent(ctx, &types.EventSessionWithdrawRequested{SessionId: bytes.Clone(stream.SessionId), Kind: kind, Amount: amount, EffectiveHeight: effective})
}

// A blocked CLOSE sleeps in its existing primary. The shared Stream writer
// re-enqueues its original effective height when the last Task closes, so it
// cannot repeatedly consume the maintenance budget while doing no work.
func (k Keeper) syncSessionWithdrawalIndex(ctx context.Context, stream types.StreamState) error {
	if stream.PendingWithdrawKind == types.SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_NONE {
		return nil
	}
	key := collections.Join(stream.PendingWithdrawEffectiveHeight, stream.SessionId)
	if stream.PendingWithdrawKind == types.SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_CLOSE && stream.OpenPendingCount != 0 {
		return k.SessionWithdrawIndex.Remove(ctx, key)
	}
	return k.SessionWithdrawIndex.Set(ctx, key)
}
