package keeper

import (
	"bytes"
	"context"
	"errors"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"

	"github.com/TrueOpen/node/x/task/types"
)

type SessionLifecycleSweepResult struct {
	SweptCount      uint64
	MarkedIdleCount uint64
	ClosedCount     uint64
}

type sessionLifecycleResult struct {
	markedIdle uint64
	closed     uint64
}

// sessionLifecycleDueHeights recomputes the two candidate due heights of a
// session from the *stored* stream row.
//
// SessionParamsV1 is runtime-immutable, so last_active_height plus the active
// params always reproduces the exact index key. The sweep still deletes every
// visited row before re-arming it so an imported stale row cannot accumulate.
func sessionLifecycleDueHeights(params types.TaskParamsV1, lastActiveHeight uint64) (uint64, uint64, error) {
	idleDue, idleOverflow := checkedSessionAddUint64(lastActiveHeight, params.Session.SessionIdleTtlBlocks)
	closeDue, closeOverflow := checkedSessionAddUint64(lastActiveHeight, params.Session.SessionCloseTtlBlocks)
	if idleOverflow || closeOverflow {
		return 0, 0, errorsmod.Wrap(types.ErrInvariantBroken, "session lifecycle due height overflow")
	}
	return idleDue, closeDue, nil
}

// advanceSessionLifecycleForIndex is the single §10.0b1 executor shared by
// EndBlock and MsgSweepDeadline(SessionLifecycleLocator). The caller has already
// removed the index row it is reporting, so this function is responsible for
// re-arming the authoritative row when the transition does not apply.
func (k Keeper) advanceSessionLifecycleForIndex(ctx context.Context, sessionKey types.SessionKey, action types.SessionLifecycleAction, dueHeight, currentHeight uint64) (sessionLifecycleResult, error) {
	stream, err := k.ReadStream(ctx, sessionKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			// Provably stale row: the primary is gone. Nothing to re-arm.
			return sessionLifecycleResult{}, nil
		}
		return sessionLifecycleResult{}, err
	}
	storedKey, err := sessionStoreKey(stream.SessionId)
	if err != nil || !bytes.Equal(storedKey, sessionKey) {
		return sessionLifecycleResult{}, errorsmod.Wrap(types.ErrInvariantBroken, "session lifecycle index points to mismatched stream")
	}
	if stream.OpenPendingCount != 0 || currentHeight < dueHeight {
		// §10.0b1: open_pending_count != 0 and not-yet-due are both no-ops.
		if err := k.refreshSessionLifecycleIndex(ctx, stream); err != nil {
			return sessionLifecycleResult{}, err
		}
		return sessionLifecycleResult{}, nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return sessionLifecycleResult{}, err
	}
	idleDue, closeDue, err := sessionLifecycleDueHeights(params, stream.LastActiveHeight)
	if err != nil {
		return sessionLifecycleResult{}, err
	}
	previous := stream
	switch action {
	case types.SessionLifecycleAction_SESSION_LIFECYCLE_ACTION_MARK_IDLE:
		if stream.Status != types.SessionStatus_SESSION_STATUS_ACTIVE || idleDue != dueHeight {
			if err := k.refreshSessionLifecycleIndex(ctx, stream); err != nil {
				return sessionLifecycleResult{}, err
			}
			return sessionLifecycleResult{}, nil
		}
		stream.Status = types.SessionStatus_SESSION_STATUS_IDLE
		if err := k.replaceStreamState(ctx, previous, stream); err != nil {
			return sessionLifecycleResult{}, err
		}
		if err := emitSessionDeadlineSweptEvent(ctx, stream.SessionId,
			types.DeadlineTransitionCode_DEADLINE_TRANSITION_CODE_SESSION_ACTIVE_TO_IDLE); err != nil {
			return sessionLifecycleResult{}, err
		}
		return sessionLifecycleResult{markedIdle: 1}, nil

	case types.SessionLifecycleAction_SESSION_LIFECYCLE_ACTION_CLOSE:
		if stream.Status != types.SessionStatus_SESSION_STATUS_IDLE || closeDue != dueHeight {
			if err := k.refreshSessionLifecycleIndex(ctx, stream); err != nil {
				return sessionLifecycleResult{}, err
			}
			return sessionLifecycleResult{}, nil
		}
		stream.Status = types.SessionStatus_SESSION_STATUS_CLOSED
		stream.LastActiveHeight = currentHeight
		if err := k.replaceStreamState(ctx, previous, stream); err != nil {
			return sessionLifecycleResult{}, err
		}
		// §6.2 line 854 / §7 line 2011: the CLOSED transaction removes the owner
		// index and every remaining lifecycle row of this session.
		if err := k.removeSessionByOwnerIndex(ctx, stream.OwnerUserAddress, sessionKey); err != nil {
			return sessionLifecycleResult{}, err
		}
		if err := k.removeSessionLifecycleIndexes(ctx, sessionKey, previous.LastActiveHeight); err != nil {
			return sessionLifecycleResult{}, err
		}
		eligibleHeight, overflow := checkedSessionAddUint64(currentHeight, params.Session.SessionOrderRetentionBlocks)
		if overflow {
			return sessionLifecycleResult{}, errorsmod.Wrap(types.ErrInvariantBroken, "session history retention height overflow")
		}
		if err := k.SessionHistoryPruneIndex.Set(ctx, types.NewSessionHistoryPruneIndexKey(eligibleHeight, sessionKey)); err != nil {
			return sessionLifecycleResult{}, err
		}
		if err := emitSessionDeadlineSweptEvent(ctx, stream.SessionId,
			types.DeadlineTransitionCode_DEADLINE_TRANSITION_CODE_SESSION_IDLE_TO_CLOSED); err != nil {
			return sessionLifecycleResult{}, err
		}
		return sessionLifecycleResult{closed: 1}, nil

	default:
		// Unknown action value: provably not a legal row, drop it.
		return sessionLifecycleResult{}, nil
	}
}

// removeSessionByOwnerIndex is the only writer that deletes SessionByOwnerIndex.
// P2-08 ④: before this, nothing in the module ever removed the row, so
// QuerySessionsByOwner enumerated CLOSED sessions forever.
func (k Keeper) removeSessionByOwnerIndex(ctx context.Context, owner string, sessionKey types.SessionKey) error {
	key := types.NewSessionByOwnerKey(owner, sessionKey)
	has, err := k.SessionByOwnerIndex.Has(ctx, key)
	if err != nil || !has {
		return err
	}
	return k.SessionByOwnerIndex.Remove(ctx, key)
}

// SweepExpiredSessionLifecycle counts every due index row it visits, including
// stale and no-op rows, so poison entries cannot bypass the per-block bound.
func (k Keeper) SweepExpiredSessionLifecycle(ctx context.Context, currentHeight, limit uint64) (SessionLifecycleSweepResult, error) {
	if limit == 0 {
		return SessionLifecycleSweepResult{}, nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return SessionLifecycleSweepResult{}, err
	}
	localCap := uint64(params.Session.MaxSessionSweepPerBlock)
	if limit > localCap {
		limit = localCap
	}

	// Hash32KeyCodec.Decode copies (types/key_codec.go), so a session key held in
	// this slice past the Next() that invalidates the iterator buffer — and then
	// handed to a Remove on the same KeySet — is its own storage, not the
	// iterator's.
	type dueRow struct {
		dueHeight  uint64
		sessionKey types.SessionKey
		action     types.SessionLifecycleAction
	}
	due := make([]dueRow, 0, limit)

	iter, err := k.SessionLifecycleIndex.Iterate(ctx, nil)
	if err != nil {
		return SessionLifecycleSweepResult{}, err
	}
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			_ = iter.Close()
			return SessionLifecycleSweepResult{}, err
		}
		if key.K1() > currentHeight {
			break
		}
		due = append(due, dueRow{dueHeight: key.K1(), sessionKey: key.K2(), action: types.SessionLifecycleAction(key.K3())})
		if uint64(len(due)) >= limit {
			break
		}
	}
	if err := iter.Close(); err != nil {
		return SessionLifecycleSweepResult{}, err
	}

	result := SessionLifecycleSweepResult{}
	for _, row := range due {
		result.SweptCount++
		// The visited row is always removed first; advanceSessionLifecycleForIndex
		// re-derives and re-arms the authoritative row when the transition does
		// not apply. That is what makes an orphaned row self-healing.
		if err := removeSessionLifecycleIndexIfExists(ctx, k.SessionLifecycleIndex,
			types.NewSessionLifecycleIndexKey(row.dueHeight, row.sessionKey, row.action)); err != nil {
			return result, err
		}
		advanced, err := k.advanceSessionLifecycleForIndex(ctx, row.sessionKey, row.action, row.dueHeight, currentHeight)
		if err != nil {
			return result, err
		}
		result.MarkedIdleCount += advanced.markedIdle
		result.ClosedCount += advanced.closed
	}
	return result, nil
}

// SweepSessionLifecycleByID is the ByIDV1 branch of §5.9's
// SessionLifecycleLocator. It resolves the due row from the authoritative
// StreamState instead of scanning the index, so it is O(1).
func (k Keeper) SweepSessionLifecycleByID(ctx context.Context, sessionKey types.SessionKey, currentHeight uint64) (SessionLifecycleSweepResult, error) {
	stream, err := k.ReadStream(ctx, sessionKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return SessionLifecycleSweepResult{}, errorsmod.Wrapf(types.ErrInvalidSessionID, "session %s", hex32(sessionKey))
		}
		return SessionLifecycleSweepResult{}, err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return SessionLifecycleSweepResult{}, err
	}
	idleDue, closeDue, err := sessionLifecycleDueHeights(params, stream.LastActiveHeight)
	if err != nil {
		return SessionLifecycleSweepResult{}, err
	}

	var (
		dueHeight uint64
		action    types.SessionLifecycleAction
	)
	switch stream.Status {
	case types.SessionStatus_SESSION_STATUS_ACTIVE:
		dueHeight, action = idleDue, types.SessionLifecycleAction_SESSION_LIFECYCLE_ACTION_MARK_IDLE
	case types.SessionStatus_SESSION_STATUS_IDLE:
		dueHeight, action = closeDue, types.SessionLifecycleAction_SESSION_LIFECYCLE_ACTION_CLOSE
	default:
		// CLOSED is terminal: §10.0b1 line 2340 allows reactivation from IDLE only.
		return SessionLifecycleSweepResult{}, nil
	}
	if stream.OpenPendingCount != 0 || currentHeight < dueHeight {
		return SessionLifecycleSweepResult{}, nil
	}

	result := SessionLifecycleSweepResult{SweptCount: 1}
	if err := removeSessionLifecycleIndexIfExists(ctx, k.SessionLifecycleIndex,
		types.NewSessionLifecycleIndexKey(dueHeight, sessionKey, action)); err != nil {
		return result, err
	}
	advanced, err := k.advanceSessionLifecycleForIndex(ctx, sessionKey, action, dueHeight, currentHeight)
	if err != nil {
		return result, err
	}
	result.MarkedIdleCount = advanced.markedIdle
	result.ClosedCount = advanced.closed
	return result, nil
}
