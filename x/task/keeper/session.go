package keeper

import (
	"bytes"
	"context"
	"errors"
	"math"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func currentBlockHeight(ctx context.Context) (uint64, error) {
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	if height < 0 {
		return 0, errorsmod.Wrap(types.ErrInvariantBroken, "negative block height")
	}
	return uint64(height), nil
}

func checkedSessionAddUint64(left, right uint64) (uint64, bool) {
	if left > math.MaxUint64-right {
		return 0, true
	}
	return left + right, false
}

// DeriveSessionID implements the registered TRUEOPEN_SESSION_V1 preimage. Address
// and integer fields use their canonical bytes, never their display strings.
func DeriveSessionID(ownerAddressBytes []byte, nonce uint64) ([]byte, error) {
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainSessionV1)).Raw(
		ownerAddressBytes,
		shared.Uint64BE(nonce),
	).Sum()
}

// hash32StoreKey is the single admission gate between a raw proto Hash32 field
// and a store key: it enforces §0.1's fixed 32-byte width and returns the bytes
// unchanged. types.Hash32KeyCodec re-checks the width when it encodes, but the
// check belongs here too — this is where the error can still name the offending
// field and carry the caller's sentinel (ErrInvalidTaskID / ErrInvalidSessionID)
// instead of surfacing as an opaque codec ErrEncoding from deep inside a Set.
//
// The returned key aliases the caller's slice rather than copying it. That is
// safe in the direction this function runs: the value comes from a proto field
// or a freshly derived digest owned by the caller, the codec copies it into the
// store buffer on Encode, and nothing here retains it. The copy that does matter
// is the other direction — a key decoded out of an iterator — and that one lives
// in Hash32KeyCodec.Decode, where it cannot be forgotten.
func hash32StoreKey(field string, value []byte) (types.Hash32Key, error) {
	if len(value) != types.Hash32Len {
		return nil, errorsmod.Wrapf(types.ErrInvariantBroken, "%s must be 32 bytes", field)
	}
	return value, nil
}

func sessionStoreKey(sessionID []byte) (types.SessionKey, error) {
	key, err := hash32StoreKey("session_id", sessionID)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidSessionID, err.Error())
	}
	return key, nil
}

func taskStoreKey(taskID []byte) (types.TaskKey, error) {
	key, err := hash32StoreKey("task_id", taskID)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidTaskID, err.Error())
	}
	return key, nil
}

func (k Keeper) closeStreamPendingTask(ctx context.Context, sessionID []byte) error {
	sessionKey, err := sessionStoreKey(sessionID)
	if err != nil {
		return err
	}
	stream, err := k.ReadStream(ctx, sessionKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return errorsmod.Wrapf(types.ErrInvalidSessionID, "session %s not found", hex32(sessionKey))
		}
		return err
	}
	if stream.OpenPendingCount == 0 {
		return errorsmod.Wrapf(types.ErrInvariantBroken, "session %s open_pending_count underflow", hex32(sessionKey))
	}
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return err
	}
	// P2-08 ②: closing an in-flight task must never resurrect a terminal stream.
	// §6.2 line 851 makes open_pending_count != 0 impossible outside ACTIVE, and
	// §10.0b1 line 2340 allows reactivation from IDLE only, so the status is left
	// exactly as stored instead of being forced to ACTIVE.
	if stream.Status != types.SessionStatus_SESSION_STATUS_ACTIVE {
		return errorsmod.Wrapf(types.ErrInvariantBroken,
			"session %s is %s but still has %d in-flight orders", hex32(sessionKey), stream.Status, stream.OpenPendingCount)
	}
	previous := stream
	stream.OpenPendingCount--
	stream.LastActiveHeight = height
	return k.replaceStreamState(ctx, previous, stream)
}

func (k Keeper) setStreamState(ctx context.Context, stream types.StreamState) error {
	sessionKey, err := sessionStoreKey(stream.SessionId)
	if err != nil {
		return err
	}
	if stream.OwnerUserAddress == "" {
		return errorsmod.Wrap(types.ErrInvariantBroken, "stream owner is required")
	}
	if stream.Status == types.SessionStatus_SESSION_STATUS_UNSPECIFIED {
		return errorsmod.Wrap(types.ErrInvariantBroken, "stream status is required")
	}
	if err := k.WriteStream(ctx, sessionKey, stream); err != nil {
		return err
	}
	return k.refreshSessionLifecycleIndex(ctx, stream)
}

func (k Keeper) replaceStreamState(ctx context.Context, previous, stream types.StreamState) error {
	if len(previous.SessionId) != 0 {
		previousKey, err := sessionStoreKey(previous.SessionId)
		if err != nil {
			return err
		}
		streamKey, err := sessionStoreKey(stream.SessionId)
		if err != nil {
			return err
		}
		if !bytes.Equal(previousKey, streamKey) || previous.OwnerUserAddress != stream.OwnerUserAddress {
			return errorsmod.Wrap(types.ErrInvariantBroken, "stream identity is immutable")
		}
		if err := k.removeSessionLifecycleIndexes(ctx, previousKey, previous.LastActiveHeight); err != nil {
			return err
		}
	}
	return k.setStreamState(ctx, stream)
}

func (k Keeper) refreshSessionLifecycleIndex(ctx context.Context, stream types.StreamState) error {
	sessionKey, err := sessionStoreKey(stream.SessionId)
	if err != nil {
		return err
	}
	if err := k.removeSessionLifecycleIndexes(ctx, sessionKey, stream.LastActiveHeight); err != nil {
		return err
	}
	if stream.OpenPendingCount != 0 || stream.Status == types.SessionStatus_SESSION_STATUS_CLOSED {
		return nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	idleDue, closeDue, err := sessionLifecycleDueHeights(params, stream.LastActiveHeight)
	if err != nil {
		return err
	}
	switch stream.Status {
	case types.SessionStatus_SESSION_STATUS_ACTIVE:
		return k.SessionLifecycleIndex.Set(ctx, types.NewSessionLifecycleIndexKey(idleDue, sessionKey, types.SessionLifecycleAction_SESSION_LIFECYCLE_ACTION_MARK_IDLE))
	case types.SessionStatus_SESSION_STATUS_IDLE:
		return k.SessionLifecycleIndex.Set(ctx, types.NewSessionLifecycleIndexKey(closeDue, sessionKey, types.SessionLifecycleAction_SESSION_LIFECYCLE_ACTION_CLOSE))
	default:
		return errorsmod.Wrapf(types.ErrInvariantBroken, "unsupported session status %s", stream.Status)
	}
}

// removeSessionLifecycleIndexes deletes both candidate rows derived from the
// stored last_active_height. Both actions are attempted because a reactivation
// may cross the ACTIVE/IDLE boundary.
func (k Keeper) removeSessionLifecycleIndexes(ctx context.Context, sessionKey types.SessionKey, lastActiveHeight uint64) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	idleDue, closeDue, err := sessionLifecycleDueHeights(params, lastActiveHeight)
	if err != nil {
		return err
	}
	if err := removeSessionLifecycleIndexIfExists(ctx, k.SessionLifecycleIndex, types.NewSessionLifecycleIndexKey(idleDue, sessionKey, types.SessionLifecycleAction_SESSION_LIFECYCLE_ACTION_MARK_IDLE)); err != nil {
		return err
	}
	return removeSessionLifecycleIndexIfExists(ctx, k.SessionLifecycleIndex, types.NewSessionLifecycleIndexKey(closeDue, sessionKey, types.SessionLifecycleAction_SESSION_LIFECYCLE_ACTION_CLOSE))
}

func removeSessionLifecycleIndexIfExists(ctx context.Context, set collections.KeySet[types.SessionLifecycleIndexKey], key types.SessionLifecycleIndexKey) error {
	has, err := set.Has(ctx, key)
	if err != nil || !has {
		return err
	}
	return set.Remove(ctx, key)
}

func ensureSessionCanConsumeSequence(stream types.StreamState, maxSequences uint32) error {
	if stream.Status == types.SessionStatus_SESSION_STATUS_CLOSED {
		return errorsmod.Wrap(types.ErrInvalidSessionID, "closed session cannot consume an order sequence")
	}
	if maxSequences == 0 {
		return errorsmod.Wrap(types.ErrInvariantBroken, "max_order_sequences_per_active_session must be positive")
	}
	if stream.NextExpectedSequence >= uint64(maxSequences) {
		return errorsmod.Wrapf(types.ErrInvalidOrderSequence, "session reached the limit of %d order sequences", maxSequences)
	}
	if stream.OpenPendingCount == math.MaxUint32 {
		return errorsmod.Wrap(types.ErrInvariantBroken, "session open_pending_count overflow")
	}
	return nil
}
