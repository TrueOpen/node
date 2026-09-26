package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

const (
	epochTaskSummaryHistogramBucketsV1 = 6
)

func taskFinalityDelayBlocksV1() uint64 { return hubtypes.ProfileChallengeOpenWindowMaxBlocks }

func (k Keeper) epochLengthBlocks(ctx context.Context) uint64 {
	params := k.hubKeeper.GetHubParams(sdk.UnwrapSDKContext(ctx))
	if params.EpochLengthBlocks == 0 {
		return hubtypes.DefaultEpochLengthBlocks
	}
	return params.EpochLengthBlocks
}

func (k Keeper) epochTaskSummaryBounds(ctx context.Context, epoch uint64) (startHeight, endHeight, dueHeight uint64, err error) {
	length := k.epochLengthBlocks(ctx)
	startHeight, endHeight, err = shared.EpochHeightRange(epoch, length)
	if err != nil {
		return 0, 0, 0, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	dueHeight, overflow := checkedHeightAdd(endHeight, taskFinalityDelayBlocksV1())
	if overflow {
		return 0, 0, 0, errorsmod.Wrap(types.ErrInvariantBroken, "epoch summary due height overflow")
	}
	return startHeight, endHeight, dueHeight, nil
}

// registerEpochTaskSummarySource freezes the settlement-height source and its
// delayed epoch schedule. Exact replay rewrites the same KeySet entries.
func (k Keeper) registerEpochTaskSummarySource(ctx context.Context, summary types.TaskTerminalSummaryState) error {
	if summary.SettlementHeight == 0 {
		return nil
	}
	taskKey, err := taskStoreKey(summary.TaskId)
	if err != nil {
		return err
	}
	epoch := shared.EpochForHeight(summary.SettlementHeight, k.epochLengthBlocks(ctx))
	if dispatched, err := k.EpochTaskSummaryReceipt.Has(ctx, epoch); err != nil {
		return err
	} else if dispatched {
		return errorsmod.Wrapf(types.ErrInvariantBroken, "late epoch summary source for dispatched epoch %d", epoch)
	}
	_, endHeight, dueHeight, err := k.epochTaskSummaryBounds(ctx, epoch)
	if err != nil {
		return err
	}
	if summary.SettlementHeight > endHeight {
		return errorsmod.Wrap(types.ErrInvariantBroken, "epoch summary source height is outside derived epoch")
	}
	if err := k.EpochTaskSummarySourceIndex.Set(ctx, types.NewDeadlineIndexKey(summary.SettlementHeight, taskKey)); err != nil {
		return err
	}
	return k.EpochTaskSummaryScheduleIndex.Set(ctx, types.NewEpochTaskSummaryScheduleKey(dueHeight, epoch))
}

func (k Keeper) rebuildEpochTaskSummarySource(ctx context.Context, summary types.TaskTerminalSummaryState) error {
	if summary.SettlementHeight == 0 {
		return nil
	}
	epoch := shared.EpochForHeight(summary.SettlementHeight, k.epochLengthBlocks(ctx))
	if dispatched, err := k.EpochTaskSummaryReceipt.Has(ctx, epoch); err != nil || dispatched {
		return err
	}
	if cursor, err := k.EpochTaskSummaryCursor.Get(ctx, epoch); err == nil && len(cursor.LastTaskId) != 0 {
		taskKey, keyErr := taskStoreKey(summary.TaskId)
		if keyErr != nil {
			return keyErr
		}
		lastKey, keyErr := taskStoreKey(cursor.LastTaskId)
		if keyErr != nil {
			return keyErr
		}
		// bytes.Compare over the raw task_id reproduces the lexicographic order the
		// lowercase-hex keys had — hex is order-preserving — so "already consumed by
		// the epoch cursor" still means exactly the same set of sources.
		if summary.SettlementHeight < cursor.LastSettlementHeight ||
			(summary.SettlementHeight == cursor.LastSettlementHeight && bytes.Compare(taskKey, lastKey) <= 0) {
			return nil
		}
	} else if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	return k.registerEpochTaskSummarySource(ctx, summary)
}

func (k Keeper) firstDueEpochTaskSummary(ctx context.Context, currentHeight uint64) (deadlineQueueHead, bool, error) {
	iter, err := k.EpochTaskSummaryScheduleIndex.Iterate(ctx, nil)
	if err != nil {
		return deadlineQueueHead{}, false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return deadlineQueueHead{}, false, nil
	}
	key, err := iter.Key()
	if err != nil || key.K1() > currentHeight {
		return deadlineQueueHead{}, false, err
	}
	// This queue's primary_id is an epoch, not a Hash32. Big-endian is the ordered
	// byte spelling of it, exactly as the zero-padded decimal string was the
	// ordered text spelling; §5.9 only compares primary_id between items that
	// already tie on deadline_height and kind_priority, and every EndBlock queue
	// carries a distinct priority, so this component never decides on its own.
	return deadlineQueueHead{deadline: key.K1(), primaryID: shared.Uint64BE(key.K2())}, true, nil
}

func (k Keeper) processEpochTaskSummaries(ctx context.Context, currentHeight, limit uint64) (uint64, error) {
	if limit == 0 {
		return 0, nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, err
	}
	if localCap := uint64(params.Cleanup.MaxEpochTaskSummaryItemsPerBlock); limit > localCap {
		limit = localCap
	}
	visited := uint64(0)
	for visited < limit {
		iter, err := k.EpochTaskSummaryScheduleIndex.Iterate(ctx, nil)
		if err != nil {
			return visited, err
		}
		if !iter.Valid() {
			iter.Close()
			break
		}
		key, err := iter.Key()
		iter.Close()
		if err != nil {
			return visited, err
		}
		if key.K1() > currentHeight {
			break
		}
		sdkCtx := sdk.UnwrapSDKContext(ctx)
		cacheCtx, write := sdkCtx.CacheContext()
		cache := sdk.WrapSDKContext(cacheCtx)
		if err := k.processEpochTaskSummaryStep(cache, key.K2(), key.K1(), currentHeight); err != nil {
			return visited, err
		}
		write()
		visited++
	}
	return visited, nil
}

func (k Keeper) processEpochTaskSummariesWithBudget(ctx context.Context, currentHeight, limit, bytesLimit uint64) (deadlineSweepUsage, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return deadlineSweepUsage{}, err
	}
	rowBytes := params.Cleanup.MaxEpochTaskSummaryBytes
	if rowBytes == 0 || bytesLimit < rowBytes {
		return deadlineSweepUsage{}, nil
	}
	if byteCapItems := bytesLimit / rowBytes; limit > byteCapItems {
		limit = byteCapItems
	}
	visited, err := k.processEpochTaskSummaries(ctx, currentHeight, limit)
	usage := deadlineSweepUsage{visited: visited}
	var overflow bool
	usage.serializedBytes, overflow = checkedMulUint64(visited, rowBytes)
	if overflow {
		return usage, errorsmod.Wrap(types.ErrInvariantBroken, "epoch summary byte accounting overflow")
	}
	return usage, err
}

func (k Keeper) processEpochTaskSummaryStep(ctx context.Context, epoch, scheduledDueHeight, currentHeight uint64) error {
	if receipt, err := k.EpochTaskSummaryReceipt.Get(ctx, epoch); err == nil {
		if receipt.Epoch != epoch {
			return errorsmod.Wrap(types.ErrInvariantBroken, "epoch summary receipt primary key mismatch")
		}
		return k.EpochTaskSummaryScheduleIndex.Remove(ctx, types.NewEpochTaskSummaryScheduleKey(scheduledDueHeight, epoch))
	} else if !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	cursor, err := k.loadOrCreateEpochTaskSummaryCursor(ctx, epoch, scheduledDueHeight)
	if err != nil {
		return err
	}
	indexKey, found, err := k.nextEpochTaskSummarySource(ctx, cursor)
	if err != nil {
		return err
	}
	if !found {
		return k.finishEpochTaskSummary(ctx, cursor, scheduledDueHeight, currentHeight)
	}
	summary, err := k.epochTaskSummarySource(ctx, indexKey.K2())
	if err != nil {
		return err
	}
	if summary.SettlementHeight != indexKey.K1() || shared.EpochForHeight(summary.SettlementHeight, k.epochLengthBlocks(ctx)) != epoch {
		return errorsmod.Wrap(types.ErrInvariantBroken, "epoch summary source index mismatch")
	}
	if summary.FailureClass < types.TaskFailureClass_TASK_FAILURE_CLASS_NONE ||
		summary.FailureClass > types.TaskFailureClass_TASK_FAILURE_CLASS_WORKER_EVIDENCE_FAULT {
		return errorsmod.Wrap(types.ErrInvariantBroken, "epoch summary source has invalid failure class")
	}
	if len(cursor.Histogram) != epochTaskSummaryHistogramBucketsV1 || len(cursor.RunningRoot) != types.Hash32Len {
		return errorsmod.Wrap(types.ErrInvariantBroken, "epoch summary cursor accumulator is non-canonical")
	}
	nextTaskCount, overflow := checkedAddUint64(cursor.TaskCount, 1)
	if overflow {
		return errorsmod.Wrap(types.ErrInvariantBroken, "epoch summary task_count overflow")
	}
	cursor.TaskCount = nextTaskCount
	bucket := int(summary.FailureClass) - int(types.TaskFailureClass_TASK_FAILURE_CLASS_NONE)
	nextBucket, overflow := checkedAddUint64(cursor.Histogram[bucket], 1)
	if overflow {
		return errorsmod.Wrap(types.ErrInvariantBroken, "epoch summary histogram overflow")
	}
	cursor.Histogram[bucket] = nextBucket
	// valid_task_count and support_candidate_seen_count are two different questions
	// and must not share one predicate. "Valid" is a property of the task's own
	// outcome - it passed verification, its optimistic finality is FINAL and it
	// carries no failure class - while support_candidate additionally requires the
	// challenge window to have closed without a reward-disqualifying fault or a
	// successful overturn (see taskTerminalSummaryDraft). Driving both counters off
	// summary.SupportCandidate made the two receipt fields provably identical, so the
	// receipt could not distinguish "the epoch produced N good tasks" from "N of them
	// also survived challenge", and every consumer of valid_task_count silently
	// inherited the challenge outcome.
	if epochTaskSummaryValidTask(summary) {
		cursor.ValidTaskCount, overflow = checkedAddUint64(cursor.ValidTaskCount, 1)
		if overflow {
			return errorsmod.Wrap(types.ErrInvariantBroken, "epoch summary valid_task_count overflow")
		}
	}
	if epochTaskSummaryValidTask(summary) {
		cursor.SupportCandidateSeenCount, overflow = checkedAddUint64(cursor.SupportCandidateSeenCount, 1)
		if overflow {
			return errorsmod.Wrap(types.ErrInvariantBroken, "epoch summary support candidate count overflow")
		}
		candidate := fmt.Sprintf("%s/v%d", strings.TrimSpace(summary.ModelId), summary.ProfileVersion)
		if summary.ModelId == "" || summary.ProfileVersion == 0 {
			return errorsmod.Wrap(types.ErrInvariantBroken, "support candidate is missing model/profile")
		}
		params, err := k.Params.Get(ctx)
		if err != nil {
			return err
		}
		if len(cursor.SupportCandidates) < int(params.Cleanup.MaxEpochTaskSummarySupportCandidates) && !containsString(cursor.SupportCandidates, candidate) {
			prospective := shared.EpochTaskSummary{
				Epoch: cursor.Epoch, TaskCount: cursor.TaskCount, ValidTaskCount: cursor.ValidTaskCount,
				Histogram: cursor.Histogram, SupportCandidates: append(append([]string(nil), cursor.SupportCandidates...), candidate),
			}
			if uint64(prospective.Size()) <= params.Cleanup.MaxEpochTaskSummaryBytes {
				cursor.SupportCandidates = prospective.SupportCandidates
			}
		}
	}
	chainID := sdk.UnwrapSDKContext(ctx).ChainID()
	sourceHash, err := epochTaskSummarySourceHash(chainID, epoch, summary)
	if err != nil {
		return err
	}
	cursor.RunningRoot, err = epochTaskSummaryFoldHash(chainID, epoch, cursor.RunningRoot, sourceHash, cursor.TaskCount)
	if err != nil {
		return err
	}
	cursor.LastSettlementHeight = indexKey.K1()
	cursor.LastTaskId = append([]byte(nil), summary.TaskId...)
	cursor.VisitedCount, overflow = checkedAddUint64(cursor.VisitedCount, 1)
	if overflow {
		return errorsmod.Wrap(types.ErrInvariantBroken, "epoch summary visited_count overflow")
	}
	if err := k.EpochTaskSummaryCursor.Set(ctx, epoch, cursor); err != nil {
		return err
	}
	return k.EpochTaskSummarySourceIndex.Remove(ctx, indexKey)
}

// epochTaskSummaryValidTask is the verdict/finality-only half of the
// support_candidate predicate: a task counts as valid once its own settlement
// outcome is clean, independently of what the challenge window later did to it.
// The interface contract does not spell out "valid task" for
// EpochTaskSummary.valid_task_count, so this is the Node-side definition -
// deliberately the same three terms taskTerminalSummaryDraft uses minus the
// challenge term, so that support_candidate remains a strict subset of it and
// support_candidate_seen_count <= valid_task_count holds by construction.
func epochTaskSummaryValidTask(summary types.TaskTerminalSummaryState) bool {
	return summary.Verdict == types.TaskVerdict_TASK_VERDICT_PASS &&
		summary.FinalityStatus == shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL &&
		summary.FailureClass == types.TaskFailureClass_TASK_FAILURE_CLASS_NONE
}

// epochTaskSummarySourceHash and epochTaskSummaryFoldHash take the chain id rather
// than a context. They used to unwrap the SDK context purely to read ChainID(), which
// made the two frozen preimages unreachable from anything that does not own a running
// chain: a golden vector could only re-frame its own field list and could never be fed
// to the producer that consensus actually uses. Both are pure functions of their
// arguments, so the parameter is the honest signature and the caller does the unwrap.
func epochTaskSummarySourceHash(chainID string, epoch uint64, summary types.TaskTerminalSummaryState) ([]byte, error) {
	supportCandidate := epochTaskSummaryValidTask(summary)
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainEpochTaskSummarySourceV1)).Raw(
		[]byte(chainID), shared.Uint64BE(epoch), shared.Uint64BE(summary.SettlementHeight), summary.TaskId,
		shared.EnumBE(uint32(summary.FailureClass)), shared.BoolByte(supportCandidate), []byte(summary.ModelId), shared.Uint32BE(summary.ProfileVersion)).Sum()
}

func epochTaskSummaryFoldHash(chainID string, epoch uint64, previousRoot, sourceHash []byte, sourceCount uint64) ([]byte, error) {
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainEpochTaskSummaryFoldV1)).Raw(
		[]byte(chainID), shared.Uint64BE(epoch), previousRoot, sourceHash, shared.Uint64BE(sourceCount)).Sum()
}

func (k Keeper) loadOrCreateEpochTaskSummaryCursor(ctx context.Context, epoch, scheduledDueHeight uint64) (types.EpochTaskSummaryCursorState, error) {
	startHeight, endHeight, dueHeight, err := k.epochTaskSummaryBounds(ctx, epoch)
	if err != nil {
		return types.EpochTaskSummaryCursorState{}, err
	}
	if dueHeight != scheduledDueHeight {
		return types.EpochTaskSummaryCursorState{}, errorsmod.Wrap(types.ErrInvariantBroken, "epoch summary schedule height mismatch")
	}
	cursor, err := k.EpochTaskSummaryCursor.Get(ctx, epoch)
	if err == nil {
		if cursor.Epoch != epoch || cursor.StartHeight != startHeight || cursor.EndHeight != endHeight || cursor.DueHeight != dueHeight {
			return types.EpochTaskSummaryCursorState{}, errorsmod.Wrap(types.ErrInvariantBroken, "epoch summary cursor scope mismatch")
		}
		return cursor, nil
	}
	if !errors.Is(err, collections.ErrNotFound) {
		return types.EpochTaskSummaryCursorState{}, err
	}
	cursor = types.EpochTaskSummaryCursorState{
		Epoch: epoch, StartHeight: startHeight, EndHeight: endHeight, DueHeight: dueHeight,
		Histogram: make([]uint64, epochTaskSummaryHistogramBucketsV1), RunningRoot: make([]byte, types.Hash32Len),
	}
	if err := k.EpochTaskSummaryCursor.Set(ctx, epoch, cursor); err != nil {
		return types.EpochTaskSummaryCursorState{}, err
	}
	return cursor, nil
}

func (k Keeper) nextEpochTaskSummarySource(ctx context.Context, cursor types.EpochTaskSummaryCursorState) (types.DeadlineIndexKey, bool, error) {
	// The height-only bounds must stay *height-only*. Hash32KeyCodec is fail-closed
	// on width, so the previous `""` placeholder task_id is now an encoding error
	// rather than a zero-length component; collections.PairPrefix leaves K2 unset
	// and encodes just the 8-byte height, which is byte-for-byte the bound the
	// NUL-terminated StringKey produced for an empty second component.
	rng := (&collections.Range[types.DeadlineIndexKey]{}).
		StartInclusive(collections.PairPrefix[uint64, types.Hash32Key](cursor.StartHeight))
	if len(cursor.LastTaskId) != 0 {
		lastKey, err := taskStoreKey(cursor.LastTaskId)
		if err != nil {
			return types.DeadlineIndexKey{}, false, err
		}
		rng = (&collections.Range[types.DeadlineIndexKey]{}).StartExclusive(types.NewDeadlineIndexKey(cursor.LastSettlementHeight, lastKey))
	}
	if cursor.EndHeight == math.MaxUint64 {
		return types.DeadlineIndexKey{}, false, errorsmod.Wrap(types.ErrInvariantBroken, "epoch summary end range overflow")
	}
	rng.EndExclusive(collections.PairPrefix[uint64, types.Hash32Key](cursor.EndHeight + 1))
	iter, err := k.EpochTaskSummarySourceIndex.Iterate(ctx, rng)
	if err != nil {
		return types.DeadlineIndexKey{}, false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return types.DeadlineIndexKey{}, false, nil
	}
	key, err := iter.Key()
	return key, err == nil, err
}

func (k Keeper) epochTaskSummarySource(ctx context.Context, taskKey types.TaskKey) (types.TaskTerminalSummaryState, error) {
	if summary, err := k.ReadTaskTerminalSummary(ctx, taskKey); err == nil {
		return summary, nil
	} else if errors.Is(err, collections.ErrNotFound) {
		return types.TaskTerminalSummaryState{}, errorsmod.Wrap(types.ErrInvariantBroken, "epoch source is missing terminal summary")
	} else {
		return types.TaskTerminalSummaryState{}, err
	}
}

func (k Keeper) finishEpochTaskSummary(ctx context.Context, cursor types.EpochTaskSummaryCursorState, scheduledDueHeight, currentHeight uint64) error {
	summary := shared.EpochTaskSummary{
		Epoch: cursor.Epoch, TaskCount: cursor.TaskCount, ValidTaskCount: cursor.ValidTaskCount,
		Histogram: append([]uint64(nil), cursor.Histogram...), SupportCandidates: append([]string(nil), cursor.SupportCandidates...),
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	if uint64(summary.Size()) > params.Cleanup.MaxEpochTaskSummaryBytes {
		return errorsmod.Wrap(types.ErrInvariantBroken, "epoch task summary exceeds frozen byte cap")
	}
	receipt := types.EpochTaskSummaryReceiptState{
		Epoch: cursor.Epoch, StartHeight: cursor.StartHeight, EndHeight: cursor.EndHeight, DueHeight: cursor.DueHeight,
		DispatchedHeight: currentHeight, Summary: summary, SourceCount: cursor.TaskCount,
		SupportCandidateSeenCount:     cursor.SupportCandidateSeenCount,
		RetainedSupportCandidateCount: uint32(len(cursor.SupportCandidates)), SourceRoot: append([]byte(nil), cursor.RunningRoot...),
	}
	receiptHash, err := epochTaskSummaryReceiptHash(sdk.UnwrapSDKContext(ctx).ChainID(), receipt)
	if err != nil {
		return err
	}
	receipt.ReceiptHash = receiptHash
	if err := k.EpochTaskSummaryReceipt.Set(ctx, cursor.Epoch, receipt); err != nil {
		return err
	}
	if err := k.EpochTaskSummaryCursor.Remove(ctx, cursor.Epoch); err != nil {
		return err
	}
	if err := k.EpochTaskSummaryScheduleIndex.Remove(ctx, types.NewEpochTaskSummaryScheduleKey(scheduledDueHeight, cursor.Epoch)); err != nil {
		return err
	}
	return emitTypedEvent(ctx, &summary)
}

// epochTaskSummaryReceiptHash takes the chain id for the same reason
// epochTaskSummarySourceHash and epochTaskSummaryFoldHash above do: the receipt
// preimage is a pure function of the receipt, and unwrapping an SDK context purely
// to read ChainID() made it unreachable from anything without a running chain.
func epochTaskSummaryReceiptHash(chainID string, receipt types.EpochTaskSummaryReceiptState) ([]byte, error) {
	summary := receipt.Summary
	if uint64(receipt.RetainedSupportCandidateCount) != uint64(len(summary.SupportCandidates)) {
		return nil, fmt.Errorf("retained support candidate count does not match summary")
	}
	histogram := make([]shared.CanonicalFieldV1, len(summary.Histogram))
	for index, bucket := range summary.Histogram {
		histogram[index] = shared.RawCanonicalFieldV1(shared.Uint64BE(bucket))
	}
	supportCandidates := make([]shared.CanonicalFieldV1, len(summary.SupportCandidates))
	for index, candidate := range summary.SupportCandidates {
		supportCandidates[index] = shared.RawCanonicalFieldV1([]byte(candidate))
	}
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainEpochTaskSummaryReceiptV1)).Raw(
		[]byte(chainID), shared.Uint64BE(receipt.Epoch), shared.Uint64BE(receipt.StartHeight),
		shared.Uint64BE(receipt.EndHeight), shared.Uint64BE(receipt.DueHeight), shared.Uint64BE(receipt.DispatchedHeight),
		shared.Uint64BE(summary.TaskCount), shared.Uint64BE(summary.ValidTaskCount),
	).Nested(shared.CanonicalRepeatedFieldsV1(histogram)).Raw(
		shared.Uint64BE(receipt.SupportCandidateSeenCount),
	).Nested(shared.CanonicalRepeatedFieldsV1(supportCandidates)).Raw(receipt.SourceRoot).Sum()
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
