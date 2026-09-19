package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"

	"github.com/TrueOpen/node/x/task/types"
)

// The `len(taskID) == 0` guard is the byte-key spelling of the previous
// `taskID == ""` unset test. It must stay a length test rather than a
// bytes.Equal against a zero-length slice, because bytes.Equal(nil, []byte{})
// is true and would collapse "unset" with a value the codec rejects anyway.
func addDeadlineIndex(ctx context.Context, set collections.KeySet[types.DeadlineIndexKey], taskID types.TaskKey, deadline uint64) error {
	if len(taskID) == 0 || deadline == 0 {
		return nil
	}
	return set.Set(ctx, types.NewDeadlineIndexKey(deadline, taskID))
}

func removeDeadlineIndex(ctx context.Context, set collections.KeySet[types.DeadlineIndexKey], taskID types.TaskKey, deadline uint64) error {
	if len(taskID) == 0 || deadline == 0 {
		return nil
	}
	key := types.NewDeadlineIndexKey(deadline, taskID)
	has, err := set.Has(ctx, key)
	if err != nil || !has {
		return err
	}
	return set.Remove(ctx, key)
}

// The exported Add*/Remove* wrappers keep the legacy (sessionID, taskID) arity so
// evidence-cleanup callers and the internal test API remain source compatible;
// Ruling 23 makes the session component of the key unused. New production code
// calls addDeadlineIndex directly with the Task primary.

func (k Keeper) AddEvidenceCleanupIndex(ctx context.Context, _ types.SessionKey, taskID types.TaskKey, deadline uint64) error {
	return addDeadlineIndex(ctx, k.EvidenceCleanupIndex, taskID, deadline)
}

// expiredDeadlineFn receives the raw Hash32 keys. A nil sessionID means the
// TaskCore row is gone, which is the byte-key spelling of the previous ""
// sentinel; callers must test it with len() rather than bytes.Equal.
type expiredDeadlineFn func(sessionID types.SessionKey, taskID types.TaskKey, deadline uint64) (bool, error)

// iterateExpiredTaskDeadlines is the read-only walk used by the evidence cleanup
// cursor. Every bounded *mutating* sweep goes through
// Keeper.sweepExpiredTaskDeadlines instead, which collects the due rows before
// mutating and charges a visited item per row (P1-07).
func (k Keeper) iterateExpiredTaskDeadlines(ctx context.Context, set collections.KeySet[types.DeadlineIndexKey], currentHeight uint64, fn expiredDeadlineFn) error {
	iter, err := set.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return err
		}
		if key.K1() > currentHeight {
			return nil
		}
		taskID := key.K2()
		var sessionID types.SessionKey
		core, err := k.TaskCore.Get(ctx, types.NewTaskKey(taskID))
		if err == nil {
			sessionID = core.SessionId
		} else if !errors.Is(err, collections.ErrNotFound) {
			return err
		}
		stop, err := fn(sessionID, taskID, key.K1())
		if err != nil {
			return err
		}
		if stop {
			return nil
		}
	}
	return nil
}

func (k Keeper) IterateExpiredEvidenceCleanup(ctx context.Context, height uint64, fn expiredDeadlineFn) error {
	return k.iterateExpiredTaskDeadlines(ctx, k.EvidenceCleanupIndex, height, fn)
}

// Ruling 25: SampleReadyIndex and WorkerRevealDeadlineIndex are deleted (§7 does
// not register them and §5.9 has no DeadlineKindV1 value for either), and the
// CHALLENGE_* / EVIDENCE_REQUEST no-op bridges are gone with the challenge
// slice. §5.9 lines 989-994 keep the enum values and locator shapes frozen so
// that verification work cannot invent a second deadline enum, but nothing in V1 may create
// those objects.
// Future challenge work may add the real indexes for DeadlineKindV1 3 / 4 / 12
// only when K-BLOCK-03/04 closes.

func errIsNotFound(err error) bool { return errors.Is(err, collections.ErrNotFound) }
