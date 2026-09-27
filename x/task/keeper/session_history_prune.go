package keeper

import (
	"bytes"
	"context"
	"errors"
	"math"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

type SessionHistoryPruneResult struct {
	VisitedCount   uint64
	CompactedCount uint64
}

type SessionTerminalSummaryPruneResult struct {
	VisitedCount uint64
	DeletedCount uint64
}

func sessionSequenceRoot(previous []byte, state types.OrderSequenceState) ([]byte, error) {
	if len(previous) != types.Hash32Len {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "session sequence root must be 32 bytes")
	}
	if state.Status == types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_UNSPECIFIED || state.Status == types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_UNUSED {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "persisted order sequence must have a consumed status")
	}
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainSessionSequenceRootV1)).Raw(
		previous,
		shared.Uint64BE(state.OrderSequence),
		shared.EnumBE(uint32(state.Status)),
		state.TaskId,
		shared.Uint64BE(state.ConsumedHeight),
		shared.Uint64BE(state.CancelledHeight),
	).Sum()
}

func incrementSessionStatusCount(cursor *types.SessionHistoryPruneCursorState, status types.OrderSequenceStatus) error {
	var target *uint32
	switch status {
	case types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_CONSUMED:
		target = &cursor.ConsumedCount
	case types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_CANCELLED:
		target = &cursor.CancelledCount
	case types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_REFUNDED:
		target = &cursor.RefundedCount
	case types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_SETTLED:
		target = &cursor.SettledCount
	default:
		return errorsmod.Wrapf(types.ErrInvariantBroken, "unsupported order sequence status %s", status)
	}
	if *target == math.MaxUint32 {
		return errorsmod.Wrap(types.ErrInvariantBroken, "session terminal status count overflow")
	}
	(*target)++
	return nil
}

func incrementSessionHistoryVisited(cursor *types.SessionHistoryPruneCursorState) error {
	if cursor.VisitedCount == math.MaxUint64 {
		return errorsmod.Wrap(types.ErrInvariantBroken, "session history visited_count overflow")
	}
	cursor.VisitedCount++
	return nil
}

func sessionFoldedSequenceCount(cursor types.SessionHistoryPruneCursorState) (uint32, error) {
	total := uint64(cursor.ConsumedCount) + uint64(cursor.CancelledCount) +
		uint64(cursor.RefundedCount) + uint64(cursor.SettledCount)
	if total > math.MaxUint32 {
		return 0, errorsmod.Wrap(types.ErrInvariantBroken, "session folded sequence count overflow")
	}
	return uint32(total), nil
}

func (k Keeper) ensureOrderBudgetTerminal(ctx context.Context, state types.OrderSequenceState) error {
	if len(state.TaskId) == 0 {
		if state.Status != types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_CANCELLED {
			return errorsmod.Wrap(types.ErrInvariantBroken, "non-cancelled order is missing task_id")
		}
		return nil
	}
	taskKey, err := taskStoreKey(state.TaskId)
	if err != nil {
		return err
	}
	budget, err := k.TaskBudget.Get(ctx, taskKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			summary, summaryErr := k.ReadTaskTerminalSummary(ctx, taskKey)
			if summaryErr != nil {
				if errors.Is(summaryErr, collections.ErrNotFound) {
					// Absence is a normal end state, not a contradiction. Both
					// records are optional by construction: TaskBudget exists only
					// once an assignment reserved escrow, so a task that failed
					// before that — verifier selection coming up short is exactly
					// such a failure — never had one; and TaskTerminalSummary is
					// written at the tail of the incremental cleanup pipeline
					// (task_cleanup.finishTaskCompaction) and then deleted again by
					// its own retention sweep. That sweep and this one run off
					// independent prune indexes, so once task terminal summary
					// retention elapses before session order retention, "neither
					// exists" is the steady state of every old task, and nothing
					// here can tell a pruned summary from one never written.
					//
					// Treating it as ErrInvariantBroken halted FinalizeBlock and
					// then refused the replay on restart — the chain could not be
					// brought back up.
					// Folding without the assertion is state-identical: the check
					// feeds neither the rolling root nor the status counts
					// (sessionSequenceRoot reads OrderSequenceState alone), so it
					// only ever asserted. The two contradictions that remain below
					// are real ones — a summary that names another task, and escrow
					// still reserved under a terminal order.
					return nil
				}
				return summaryErr
			}
			if !bytes.Equal(summary.TaskId, state.TaskId) ||
				(summary.TerminalPhase != types.TaskPhase_TASK_PHASE_SETTLED && summary.TerminalPhase != types.TaskPhase_TASK_PHASE_FAILED) {
				return errorsmod.Wrap(types.ErrInvariantBroken, "terminal order has a non-canonical TaskTerminalSummary")
			}
			return nil
		}
		return err
	}
	if budget.BudgetStatus != types.TaskBudgetStatus_TASK_BUDGET_STATUS_FINALIZED {
		return errorsmod.Wrap(types.ErrInvariantBroken, "session history cannot compact a reserved TaskBudget")
	}
	return nil
}

func (k Keeper) compactSessionHistory(ctx context.Context, sessionKey types.SessionKey, currentHeight uint64, remaining *uint64) (bool, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return false, err
	}
	stream, err := k.ReadStream(ctx, sessionKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			if *remaining > 0 {
				(*remaining)--
			}
			if err := k.SessionHistoryPruneCursor.Remove(ctx, sessionKey); err != nil && !errors.Is(err, collections.ErrNotFound) {
				return false, err
			}
			return true, nil
		}
		return false, err
	}
	if stream.Status != types.SessionStatus_SESSION_STATUS_CLOSED || stream.OpenPendingCount != 0 {
		return false, errorsmod.Wrap(types.ErrInvariantBroken, "session history prune requires a closed stream without pending tasks")
	}
	cursor, err := k.SessionHistoryPruneCursor.Get(ctx, sessionKey)
	if err != nil {
		if !errors.Is(err, collections.ErrNotFound) {
			return false, err
		}
		cursor = types.SessionHistoryPruneCursorState{
			SessionId:           append([]byte(nil), stream.SessionId...),
			RollingSequenceRoot: make([]byte, types.Hash32Len),
		}
	} else if !bytes.Equal(cursor.SessionId, stream.SessionId) || len(cursor.RollingSequenceRoot) != types.Hash32Len {
		return false, errorsmod.Wrap(types.ErrInvariantBroken, "session history cursor does not match stream")
	}
	visitedBefore := *remaining

	for cursor.NextOrderSequence < stream.NextExpectedSequence && *remaining > 0 {
		sequence := cursor.NextOrderSequence
		key := types.NewOrderSequenceStateKey(sessionKey, sequence)
		state, err := k.OrderSequence.Get(ctx, key)
		(*remaining)--
		if err := incrementSessionHistoryVisited(&cursor); err != nil {
			return false, err
		}
		if err != nil {
			if errors.Is(err, collections.ErrNotFound) {
				// A gap cannot be represented in the rolling root or status counts.
				// Charge the lookup, persist the unchanged sequence position, and
				// retry in a later bounded sweep after the invariant is repaired.
				return false, k.SessionHistoryPruneCursor.Set(ctx, sessionKey, cursor)
			}
			return false, err
		}
		if !bytes.Equal(state.SessionId, stream.SessionId) || state.OrderSequence != sequence {
			return false, errorsmod.Wrap(types.ErrInvariantBroken, "order sequence primary key does not match state")
		}
		if err := k.ensureOrderBudgetTerminal(ctx, state); err != nil {
			return false, err
		}
		root, err := sessionSequenceRoot(cursor.RollingSequenceRoot, state)
		if err != nil {
			return false, err
		}
		cursor.RollingSequenceRoot = root
		if err := incrementSessionStatusCount(&cursor, state.Status); err != nil {
			return false, err
		}
		if err := k.OrderSequence.Remove(ctx, key); err != nil {
			return false, err
		}
		nextSequence, overflow := checkedSessionAddUint64(cursor.NextOrderSequence, 1)
		if overflow {
			return false, errorsmod.Wrap(types.ErrInvariantBroken, "session history sequence cursor overflow")
		}
		cursor.NextOrderSequence = nextSequence
	}

	if cursor.NextOrderSequence < stream.NextExpectedSequence {
		return false, k.SessionHistoryPruneCursor.Set(ctx, sessionKey, cursor)
	}
	if stream.NextExpectedSequence > math.MaxUint32 {
		return false, errorsmod.Wrap(types.ErrInvariantBroken, "session sequence_count exceeds uint32")
	}
	// A pre-completed/empty cursor still represents one primary item of work.
	// Charging it prevents imported zero-work cursors from bypassing the limit.
	if *remaining == visitedBefore {
		(*remaining)--
		if err := incrementSessionHistoryVisited(&cursor); err != nil {
			return false, err
		}
	}
	foldedCount, err := sessionFoldedSequenceCount(cursor)
	if err != nil {
		return false, err
	}
	if uint64(foldedCount) != cursor.NextOrderSequence || cursor.NextOrderSequence != stream.NextExpectedSequence {
		return false, errorsmod.Wrap(types.ErrInvariantBroken, "session folded counts do not match the completed sequence cursor")
	}
	summary := types.SessionTerminalSummaryState{
		SessionId:                 append([]byte(nil), stream.SessionId...),
		OwnerUserAddress:          stream.OwnerUserAddress,
		FinalNextExpectedSequence: stream.NextExpectedSequence,
		SequenceCount:             foldedCount,
		SequenceRoot:              append([]byte(nil), cursor.RollingSequenceRoot...),
		ConsumedCount:             cursor.ConsumedCount,
		CancelledCount:            cursor.CancelledCount,
		RefundedCount:             cursor.RefundedCount,
		SettledCount:              cursor.SettledCount,
		ClosedHeight:              stream.LastActiveHeight,
		CompactedHeight:           currentHeight,
	}
	pruneHeight, overflow := checkedSessionAddUint64(currentHeight, params.Session.SessionTerminalSummaryRetentionBlocks)
	if overflow {
		return false, errorsmod.Wrap(types.ErrInvariantBroken, "session terminal summary retention height overflow")
	}
	if err := k.WriteSessionTerminalSummary(ctx, sessionKey, summary); err != nil {
		return false, err
	}
	if err := k.SessionTerminalSummaryPruneIndex.Set(ctx, types.NewSessionTerminalSummaryPruneIndexKey(pruneHeight, sessionKey)); err != nil {
		return false, err
	}
	if err := k.Stream.Remove(ctx, sessionKey); err != nil {
		return false, err
	}
	if err := k.SessionHistoryPruneCursor.Remove(ctx, sessionKey); err != nil && !errors.Is(err, collections.ErrNotFound) {
		return false, err
	}
	return true, nil
}

// SweepSessionHistoryPrune folds CLOSED session order rows in ascending
// sequence order. Every row lookup consumes the visited budget, including a
// missing row, and the cursor is deleted rather than persisted as DONE.
func (k Keeper) SweepSessionHistoryPrune(ctx context.Context, currentHeight, limit uint64) (SessionHistoryPruneResult, error) {
	if limit == 0 {
		return SessionHistoryPruneResult{}, nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return SessionHistoryPruneResult{}, err
	}
	localCap := uint64(params.Session.MaxSessionHistoryPruneItemsPerBlock)
	if limit > localCap {
		limit = localCap
	}
	iter, err := k.SessionHistoryPruneIndex.Iterate(ctx, nil)
	if err != nil {
		return SessionHistoryPruneResult{}, err
	}
	defer iter.Close()

	remaining := limit
	result := SessionHistoryPruneResult{}
	for ; iter.Valid() && remaining > 0; iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return SessionHistoryPruneResult{}, err
		}
		if key.K1() > currentHeight {
			break
		}
		before := remaining
		done, err := k.compactSessionHistory(ctx, key.K2(), currentHeight, &remaining)
		if err != nil {
			return SessionHistoryPruneResult{}, err
		}
		result.VisitedCount += before - remaining
		if done {
			if err := k.SessionHistoryPruneIndex.Remove(ctx, key); err != nil {
				return SessionHistoryPruneResult{}, err
			}
			result.CompactedCount++
		}
	}
	return result, nil
}

// SweepSessionTerminalSummaryPrune deletes due summary rows with visited-work
// accounting. Missing summaries are stale rows and still consume one item.
func (k Keeper) SweepSessionTerminalSummaryPrune(ctx context.Context, currentHeight, limit uint64) (SessionTerminalSummaryPruneResult, error) {
	if limit == 0 {
		return SessionTerminalSummaryPruneResult{}, nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return SessionTerminalSummaryPruneResult{}, err
	}
	localCap := uint64(params.Session.MaxSessionTerminalSummaryPruneItemsPerBlock)
	if limit > localCap {
		limit = localCap
	}
	iter, err := k.SessionTerminalSummaryPruneIndex.Iterate(ctx, nil)
	if err != nil {
		return SessionTerminalSummaryPruneResult{}, err
	}
	defer iter.Close()
	result := SessionTerminalSummaryPruneResult{}
	for ; iter.Valid() && result.VisitedCount < limit; iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return SessionTerminalSummaryPruneResult{}, err
		}
		if key.K1() > currentHeight {
			break
		}
		result.VisitedCount++
		if has, err := k.SessionTerminalSummary.Has(ctx, key.K2()); err != nil {
			return SessionTerminalSummaryPruneResult{}, err
		} else if has {
			if err := k.SessionTerminalSummary.Remove(ctx, key.K2()); err != nil {
				return SessionTerminalSummaryPruneResult{}, err
			}
			result.DeletedCount++
		}
		if err := k.SessionTerminalSummaryPruneIndex.Remove(ctx, key); err != nil {
			return SessionTerminalSummaryPruneResult{}, err
		}
	}
	return result, nil
}
