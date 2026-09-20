package keeper

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// The ledger hook that enforces the bridge guard can see the amount and the
// denom, but not the Hyperlane message id or the counterparty — those live in
// the Msg, which the bank keeper never receives. The decorated Msg boundary
// records them here first, and the guard consumes them in the same order the
// messages execute.
//
// A transient store is used for the same reason the Task gas post-handler uses
// one (the API contract): the queue is block-scoped scratch space and
// must never reach the app hash. It is *not* self-cleaning across transactions,
// which is what ResetBridgeTransferIntents exists to handle.
const (
	bridgeTransferIntentPrefix  byte = 0x40
	bridgeTransferCursorPrefix  byte = 0x41
	bridgeOutboundPendingPrefix byte = 0x42
)

func bridgeQueueKey(prefix byte, index uint32) []byte {
	key := make([]byte, 5)
	key[0] = prefix
	binary.BigEndian.PutUint32(key[1:], index)
	return key
}

// ResetBridgeTransferIntents drops anything left over from an earlier
// transaction in this block.
//
// BaseApp commits the ante phase's writes even when the message phase then
// fails, while the message phase's own writes — including the cursor advance —
// are rolled back. A transaction whose bridge message failed would therefore
// leave its intent behind with the cursor still pointing at it, and the next
// bridge transaction would consume that stale entry and record someone else's
// message id and recipient. Resetting at the start of every transaction that
// carries a bridge message closes that window; transactions without one never
// pay for it.
func (k Keeper) ResetBridgeTransferIntents(ctx context.Context) error {
	if err := setBridgeIntentCounter(ctx, k, bridgeTransferIntentPrefix, 0); err != nil {
		return err
	}
	if err := setBridgeIntentCounter(ctx, k, bridgeTransferCursorPrefix, 0); err != nil {
		return err
	}
	return setBridgeIntentCounter(ctx, k, bridgeOutboundPendingPrefix, 0)
}

// PushBridgeTransferIntent records the message-level facts of one bridge
// transfer. It is called from the decorated Msg boundary before the upstream
// handler runs.
func (k Keeper) PushBridgeTransferIntent(ctx context.Context, transfer BridgeTransferContext) error {
	store := k.transientStoreService.OpenTransientStore(ctx)
	count, err := bridgeIntentCounter(ctx, k, bridgeTransferIntentPrefix)
	if err != nil {
		return err
	}
	encoded, err := encodeBridgeTransferIntent(transfer)
	if err != nil {
		return err
	}
	if err := store.Set(bridgeQueueKey(bridgeTransferIntentPrefix, count), encoded); err != nil {
		return err
	}
	return setBridgeIntentCounter(ctx, k, bridgeTransferIntentPrefix, count+1)
}

// TakeBridgeTransferIntent consumes the next recorded transfer. A missing entry
// is not a soft failure: it means business_denom was about to move without a
// decorated bridge message behind it, which §5.1 does not permit at all.
func (k Keeper) TakeBridgeTransferIntent(ctx context.Context) (BridgeTransferContext, bool) {
	store := k.transientStoreService.OpenTransientStore(ctx)
	cursor, err := bridgeIntentCounter(ctx, k, bridgeTransferCursorPrefix)
	if err != nil {
		return BridgeTransferContext{}, false
	}
	count, err := bridgeIntentCounter(ctx, k, bridgeTransferIntentPrefix)
	if err != nil || cursor >= count {
		return BridgeTransferContext{}, false
	}
	raw, err := store.Get(bridgeQueueKey(bridgeTransferIntentPrefix, cursor))
	if err != nil || raw == nil {
		return BridgeTransferContext{}, false
	}
	transfer, err := decodeBridgeTransferIntent(raw)
	if err != nil {
		return BridgeTransferContext{}, false
	}
	if err := setBridgeIntentCounter(ctx, k, bridgeTransferCursorPrefix, cursor+1); err != nil {
		return BridgeTransferContext{}, false
	}
	return transfer, true
}

func bridgeIntentCounter(ctx context.Context, k Keeper, prefix byte) (uint32, error) {
	store := k.transientStoreService.OpenTransientStore(ctx)
	raw, err := store.Get([]byte{prefix, 0xff})
	if err != nil {
		return 0, err
	}
	if len(raw) != 4 {
		return 0, nil
	}
	return binary.BigEndian.Uint32(raw), nil
}

func setBridgeIntentCounter(ctx context.Context, k Keeper, prefix byte, value uint32) error {
	store := k.transientStoreService.OpenTransientStore(ctx)
	raw := make([]byte, 4)
	binary.BigEndian.PutUint32(raw, value)
	return store.Set([]byte{prefix, 0xff}, raw)
}

// encodeBridgeTransferIntent uses the shared canonical framing so a malformed
// entry cannot be silently reinterpreted as a different transfer.
func encodeBridgeTransferIntent(transfer BridgeTransferContext) ([]byte, error) {
	if transfer.Direction != BridgeInbound && transfer.Direction != BridgeOutbound {
		return nil, fmt.Errorf("bridge transfer intent needs an explicit direction")
	}
	if transfer.Direction == BridgeInbound && len(transfer.MessageID) != shared.Hash32KeySize {
		return nil, fmt.Errorf("bridge transfer intent message_id must be 32 bytes")
	}
	if transfer.Direction == BridgeOutbound && len(transfer.MessageID) != 0 {
		return nil, fmt.Errorf("outbound message_id is assigned only after upstream dispatch")
	}
	return shared.FlatCanonicalFrameV1(
		[]byte{byte(transfer.Direction)},
		transfer.MessageID,
		[]byte(transfer.Counterpart),
		transfer.DestinationRecipient,
	).Bytes()
}

func decodeBridgeTransferIntent(raw []byte) (BridgeTransferContext, error) {
	fields, err := shared.DecodeCanonicalFrameBytes(raw, 4, uint64(types.MaxBridgeIntentFieldBytes))
	if err != nil {
		return BridgeTransferContext{}, err
	}
	if len(fields[0]) != 1 {
		return BridgeTransferContext{}, fmt.Errorf("bridge transfer intent direction is malformed")
	}
	return BridgeTransferContext{
		Direction:            BridgeDirection(fields[0][0]),
		MessageID:            fields[1],
		Counterpart:          string(fields[2]),
		DestinationRecipient: fields[3],
	}, nil
}

// PushPendingOutboundBridgeTransfer records a burn that passed every pre-ledger
// guard. The upstream handler assigns its message ID later in the same cached
// transaction, so accounting is completed by the application post-handler.
func (k Keeper) PushPendingOutboundBridgeTransfer(ctx context.Context, transfer BridgeTransferContext) error {
	if transfer.Direction != BridgeOutbound || transfer.Amount == 0 || transfer.Denom == "" || len(transfer.MessageID) != 0 {
		return fmt.Errorf("pending outbound bridge transfer is malformed")
	}
	encoded, err := shared.FlatCanonicalFrameV1(
		[]byte(transfer.Counterpart), transfer.DestinationRecipient,
		[]byte(transfer.Denom), shared.Uint64BE(transfer.Amount),
	).Bytes()
	if err != nil {
		return err
	}
	count, err := bridgeIntentCounter(ctx, k, bridgeOutboundPendingPrefix)
	if err != nil {
		return err
	}
	store := k.transientStoreService.OpenTransientStore(ctx)
	if err := store.Set(bridgeQueueKey(bridgeOutboundPendingPrefix, count), encoded); err != nil {
		return err
	}
	return setBridgeIntentCounter(ctx, k, bridgeOutboundPendingPrefix, count+1)
}

func (k Keeper) pendingOutboundBridgeTransfers(ctx context.Context) ([]BridgeTransferContext, error) {
	count, err := bridgeIntentCounter(ctx, k, bridgeOutboundPendingPrefix)
	if err != nil {
		return nil, err
	}
	store := k.transientStoreService.OpenTransientStore(ctx)
	out := make([]BridgeTransferContext, 0, count)
	for index := uint32(0); index < count; index++ {
		raw, err := store.Get(bridgeQueueKey(bridgeOutboundPendingPrefix, index))
		if err != nil || raw == nil {
			return nil, fmt.Errorf("pending outbound bridge transfer %d is unavailable", index)
		}
		fields, err := shared.DecodeCanonicalFrameBytes(raw, 4, uint64(types.MaxBridgeIntentFieldBytes))
		if err != nil {
			return nil, fmt.Errorf("pending outbound bridge transfer %d: %w", index, err)
		}
		if len(fields[3]) != 8 {
			return nil, fmt.Errorf("pending outbound bridge transfer %d amount is malformed", index)
		}
		out = append(out, BridgeTransferContext{
			Direction: BridgeOutbound, Counterpart: string(fields[0]),
			DestinationRecipient: fields[1], Denom: string(fields[2]),
			Amount: binary.BigEndian.Uint64(fields[3]),
		})
	}
	return out, nil
}
