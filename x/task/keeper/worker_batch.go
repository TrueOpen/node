package keeper

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// Per-Worker, per-period batch settlement.
//
// A task whose frozen parameters set batch_settlement_period_blocks joins the
// batch of (its Worker, the period its dispatch was recorded in). The task is
// settled on its own exactly as before, with one difference: what the Worker
// earned is not credited to it but held for the batch. Every other payee and
// the user's refund are paid with the task.
//
//	OPEN ──period ends──► CLOSING ──every member task final──► QUEUED ──► SETTLED
//	  └──────── a member task ends in a rejection or LATE EXPIRED ────────► FORFEITED
//
// A queued batch is paid in one credit by EndBlock, a bounded number of
// batches per block, oldest first. A forfeited batch pays the Worker nothing:
// what was held, and what later member tasks earn, goes to the treasury. A
// batch that was already paid is never taken back.
//
// Held amounts sit in the task_challenge_effect module account between a
// task's settlement and the batch's payment.

const (
	batchOpen             = types.WorkerBatchStatusV1_WORKER_BATCH_STATUS_V1_OPEN
	batchClosing          = types.WorkerBatchStatusV1_WORKER_BATCH_STATUS_V1_CLOSING
	batchSuspended        = types.WorkerBatchStatusV1_WORKER_BATCH_STATUS_V1_SUSPENDED
	batchQueued           = types.WorkerBatchStatusV1_WORKER_BATCH_STATUS_V1_QUEUED
	batchSettled          = types.WorkerBatchStatusV1_WORKER_BATCH_STATUS_V1_SETTLED
	batchForfeitedCheat   = types.WorkerBatchStatusV1_WORKER_BATCH_STATUS_V1_FORFEITED_CHEAT
	batchForfeitedTimeout = types.WorkerBatchStatusV1_WORKER_BATCH_STATUS_V1_FORFEITED_TIMEOUT

	// workerBatchSweepConservativeBytesV1 is charged per visited batch.
	workerBatchSweepConservativeBytesV1 = uint64(8 << 10)
	// workerBatchDomainV1 separates the identifier a batch's credit and its
	// treasury transfer are recorded under.
	workerBatchDomainV1 = "TRUEOPEN_WORKER_BATCH_V1"
)

func workerBatchForfeited(status types.WorkerBatchStatusV1) bool {
	return status == batchForfeitedCheat || status == batchForfeitedTimeout
}

// workerBatchEnded reports a batch nothing more will happen to.
func workerBatchEnded(status types.WorkerBatchStatusV1) bool {
	return status == batchSettled || workerBatchForfeited(status)
}

// workerBatchID is the 32-byte identifier of a batch in Hub's credit and
// treasury records.
func workerBatchID(chainID string, operator []byte, period uint64) []byte {
	h := sha256.New()
	h.Write([]byte(workerBatchDomainV1))
	h.Write([]byte(chainID))
	h.Write(operator)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], period)
	h.Write(buf[:])
	return h.Sum(nil)
}

func (k Keeper) moveHeldBatchFunds(ctx context.Context, toModule string, amount uint64) error {
	if amount == 0 {
		return nil
	}
	denom := k.hubKeeper.GetHubParams(sdk.UnwrapSDKContext(ctx)).BusinessDenom
	if denom == "" {
		return fmt.Errorf("business denomination is unavailable")
	}
	coins := sdk.NewCoins(sdk.NewCoin(denom, sdkmath.NewIntFromUint64(amount)))
	return k.bankKeeper.SendCoinsFromModuleToModule(ctx, shared.TaskChallengeEffectModuleName, toModule, coins)
}

// joinWorkerBatch records a newly dispatched task as a member of its Worker's
// batch of the current period. It is a no-op when the task's parameters set no
// settlement period.
func (k Keeper) joinWorkerBatch(ctx context.Context, taskKey types.TaskKey, worker string, height uint64, params types.TaskParamsV1) error {
	length := params.Verification.BatchSettlementPeriodBlocks
	if length == 0 || worker == "" {
		return nil
	}
	raw, err := k.sessionAddressToStore("worker_operator_address", worker)
	if err != nil {
		return nil
	}
	if has, err := k.WorkerBatchTask.Has(ctx, taskKey); err != nil || has {
		return err
	}
	period := height / length
	key := collections.Join(types.AddrKey(raw), period)
	batch, err := k.WorkerBatch.Get(ctx, key)
	switch {
	case errIsNotFound(err):
		batch = types.WorkerBatchState{
			OperatorAddress: worker, Period: period, Status: batchOpen,
			WorkerDue: shared.NewAmount(0), ForfeitedAmount: shared.NewAmount(0),
		}
		end, overflow := checkedHeightAdd(period*length, length)
		if overflow {
			return nil
		}
		if err := k.WorkerBatchCloseIndex.Set(ctx, collections.Join3(end, types.AddrKey(raw), period)); err != nil {
			return err
		}
	case err != nil:
		return err
	}
	if batch.TaskCount == ^uint32(0) {
		return nil
	}
	batch.TaskCount++
	batch.OpenTaskCount++
	if err := k.WorkerBatch.Set(ctx, key, batch); err != nil {
		return err
	}
	if err := k.WorkerBatchTaskIndex.Set(ctx, collections.Join3(types.AddrKey(raw), period, taskKey)); err != nil {
		return err
	}
	return k.WorkerBatchTask.Set(ctx, taskKey, types.WorkerBatchTaskState{
		OperatorAddress: worker, Period: period, TaskId: append([]byte(nil), taskKey...), WorkerNet: shared.NewAmount(0),
	})
}

// workerBatchOfTask returns the member row of a task and its batch.
func (k Keeper) workerBatchOfTask(ctx context.Context, taskKey types.TaskKey) (types.WorkerBatchTaskState, types.WorkerBatchState, collections.Pair[types.AddrKey, uint64], bool, error) {
	var key collections.Pair[types.AddrKey, uint64]
	member, err := k.WorkerBatchTask.Get(ctx, taskKey)
	if err != nil {
		if errIsNotFound(err) {
			return member, types.WorkerBatchState{}, key, false, nil
		}
		return member, types.WorkerBatchState{}, key, false, err
	}
	raw, err := k.sessionAddressToStore("worker_operator_address", member.OperatorAddress)
	if err != nil {
		return member, types.WorkerBatchState{}, key, false, nil
	}
	key = collections.Join(types.AddrKey(raw), member.Period)
	batch, err := k.WorkerBatch.Get(ctx, key)
	if err != nil {
		if errIsNotFound(err) {
			return member, batch, key, false, nil
		}
		return member, batch, key, false, err
	}
	return member, batch, key, true, nil
}

// taskSettlesInBatch reports whether a task is an open member of a batch: its
// Worker's earnings are then held for the batch instead of being credited.
func (k Keeper) taskSettlesInBatch(ctx context.Context, taskKey types.TaskKey) (bool, error) {
	member, _, _, found, err := k.workerBatchOfTask(ctx, taskKey)
	return found && !member.Final, err
}

// anchorForfeitureOf maps the audit a task settles on to the forfeiture it
// causes: a rejection is cheating, an expired late window is the timeout
// forfeiture, and nothing else forfeits.
func anchorForfeitureOf(anchor *types.AnchorAuditState) types.WorkerBatchStatusV1 {
	if anchor == nil || anchor.TaskVerdict != types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT {
		return types.WorkerBatchStatusV1_WORKER_BATCH_STATUS_V1_UNSPECIFIED
	}
	switch anchor.ResultSource {
	case types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_LATE_EXPIRED:
		return batchForfeitedTimeout
	case types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_TIMEOUT,
		types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_MATERIAL_UNAVAILABLE:
		return types.WorkerBatchStatusV1_WORKER_BATCH_STATUS_V1_UNSPECIFIED
	}
	return batchForfeitedCheat
}

// closeWorkerBatchTask records that a member task is final. workerNet is what
// the task's settlement held back for the Worker (already moved into the
// holding account by the caller); forfeiture is the forfeiture the task causes,
// if any. It is a no-op for a task that is not an open member.
func (k Keeper) closeWorkerBatchTask(
	ctx context.Context,
	taskKey types.TaskKey,
	workerNet uint64,
	audited bool,
	forfeiture types.WorkerBatchStatusV1,
	height uint64,
) error {
	member, batch, key, found, err := k.workerBatchOfTask(ctx, taskKey)
	if err != nil || !found || member.Final {
		return err
	}
	member.Final = true
	member.WorkerNet = shared.NewAmount(workerNet)
	if err := k.WorkerBatchTask.Set(ctx, taskKey, member); err != nil {
		return err
	}
	if batch.OpenTaskCount > 0 {
		batch.OpenTaskCount--
	}
	if audited && batch.AuditedCount != ^uint32(0) {
		batch.AuditedCount++
	}
	due, err := shared.ParseAmount(batch.WorkerDue)
	if err != nil {
		return err
	}
	forfeited, err := shared.ParseAmount(batch.ForfeitedAmount)
	if err != nil {
		return err
	}
	due, overflow := checkedAddUint64(due, workerNet)
	if overflow {
		return fmt.Errorf("worker batch due overflows")
	}
	if forfeiture != types.WorkerBatchStatusV1_WORKER_BATCH_STATUS_V1_UNSPECIFIED && !workerBatchEnded(batch.Status) {
		if err := k.unscheduleWorkerBatch(ctx, key, batch); err != nil {
			return err
		}
		batch.Status = forfeiture
		batch.ForfeitingTaskId = append([]byte(nil), taskKey...)
		if err := emitTypedEvent(ctx, &types.EventWorkerBatchForfeited{
			OperatorAddress: batch.OperatorAddress, Period: batch.Period, Status: forfeiture,
			ForfeitingTaskId: append([]byte(nil), taskKey...),
		}); err != nil {
			return err
		}
	}
	switch {
	case workerBatchForfeited(batch.Status):
		// Nothing is held for a forfeited batch.
		if due != 0 {
			if err := k.moveHeldBatchFunds(ctx, hubtypes.TreasuryModuleName, due); err != nil {
				return err
			}
			id := workerBatchID(sdkChainID(ctx), key.K1(), batch.Period)
			if err := k.hubKeeper.CreditTaskMaintenance(ctx, id, shared.NewAmount(due), height); err != nil {
				return err
			}
			if forfeited, overflow = checkedAddUint64(forfeited, due); overflow {
				return fmt.Errorf("worker batch forfeited amount overflows")
			}
			due = 0
		}
	case batch.Status == batchSettled:
		// A task that became final after its batch was paid (it cannot, since
		// a batch is queued only when every member is final) is paid at once.
		// worker_due of a paid batch stays the total it was paid.
		if err := k.payWorkerBatch(ctx, key, &batch, workerNet, height); err != nil {
			return err
		}
	case batch.Status == batchClosing && batch.OpenTaskCount == 0:
		batch.Status = batchQueued
		batch.QueuedHeight = height
		if err := k.WorkerBatchQueue.Set(ctx, collections.Join3(height, key.K1(), batch.Period)); err != nil {
			return err
		}
		if err := emitTypedEvent(ctx, &types.EventWorkerBatchQueued{
			OperatorAddress: batch.OperatorAddress, Period: batch.Period, WorkerDue: shared.NewAmount(due),
		}); err != nil {
			return err
		}
	}
	batch.WorkerDue = shared.NewAmount(due)
	batch.ForfeitedAmount = shared.NewAmount(forfeited)
	return k.WorkerBatch.Set(ctx, key, batch)
}

// unscheduleWorkerBatch takes a batch off the payment queue. Its period-end
// entry is left for the sweep, which skips a batch that is no longer open.
func (k Keeper) unscheduleWorkerBatch(ctx context.Context, key collections.Pair[types.AddrKey, uint64], batch types.WorkerBatchState) error {
	if batch.Status != batchQueued {
		return nil
	}
	return k.WorkerBatchQueue.Remove(ctx, collections.Join3(batch.QueuedHeight, key.K1(), key.K2()))
}

// payWorkerBatch credits amount of held earnings to the batch's Worker.
func (k Keeper) payWorkerBatch(ctx context.Context, key collections.Pair[types.AddrKey, uint64], batch *types.WorkerBatchState, amount, height uint64) error {
	if amount != 0 {
		if err := k.moveHeldBatchFunds(ctx, hubtypes.RewardsModuleName, amount); err != nil {
			return err
		}
		id := workerBatchID(sdkChainID(ctx), key.K1(), batch.Period)
		if err := k.hubKeeper.CreditTaskSettlementEarnings(ctx, id, id, []hubtypes.TaskEarningsCredit{
			{Beneficiary: batch.OperatorAddress, Amount: shared.NewAmount(amount)},
		}, height); err != nil {
			return err
		}
	}
	return emitTypedEvent(ctx, &types.EventWorkerBatchSettled{
		OperatorAddress: batch.OperatorAddress, Period: batch.Period, Paid: shared.NewAmount(amount), TaskCount: batch.TaskCount,
	})
}

// processWorkerBatchCloses is the EndBlock step that ends periods: a batch
// whose period is over is queued for payment when every member task is final,
// and otherwise waits (CLOSING) for the last of them.
func (k Keeper) processWorkerBatchCloses(ctx context.Context, currentHeight, limit uint64) (uint64, error) {
	var visited uint64
	for visited < limit {
		iter, err := k.WorkerBatchCloseIndex.Iterate(ctx, collections.NewPrefixUntilTripleRange[uint64, types.AddrKey, uint64](currentHeight))
		if err != nil {
			return visited, err
		}
		if !iter.Valid() {
			return visited, iter.Close()
		}
		head, err := iter.Key()
		if closeErr := iter.Close(); err != nil || closeErr != nil {
			return visited, fmt.Errorf("worker batch close index is unreadable")
		}
		visited++
		if err := k.WorkerBatchCloseIndex.Remove(ctx, head); err != nil {
			return visited, err
		}
		key := collections.Join(head.K2(), head.K3())
		batch, err := k.WorkerBatch.Get(ctx, key)
		if err != nil || batch.Status != batchOpen {
			continue
		}
		batch.Status = batchClosing
		if batch.OpenTaskCount == 0 {
			batch.Status = batchQueued
			batch.QueuedHeight = currentHeight
			if err := k.WorkerBatchQueue.Set(ctx, collections.Join3(currentHeight, key.K1(), key.K2())); err != nil {
				return visited, err
			}
			if err := emitTypedEvent(ctx, &types.EventWorkerBatchQueued{
				OperatorAddress: batch.OperatorAddress, Period: batch.Period, WorkerDue: batch.WorkerDue,
			}); err != nil {
				return visited, err
			}
		}
		if err := k.WorkerBatch.Set(ctx, key, batch); err != nil {
			return visited, err
		}
	}
	return visited, nil
}

// processWorkerBatchQueue is the EndBlock step that pays queued batches:
// oldest queued first, ties by operator address, at most
// batch_settlement_max_workers_per_block per block (one when unset, so a
// queue can never stand still). Each batch is paid inside its own cache; a
// batch whose payment fails is taken off the queue and stays QUEUED with its
// held amount, and the batches behind it are paid.
func (k Keeper) processWorkerBatchQueue(ctx context.Context, currentHeight, limit uint64) (uint64, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, nil
	}
	perBlock := uint64(params.Verification.BatchSettlementMaxWorkersPerBlock)
	if perBlock == 0 {
		perBlock = 1
	}
	if limit > perBlock {
		limit = perBlock
	}
	var visited uint64
	for visited < limit {
		iter, err := k.WorkerBatchQueue.Iterate(ctx, nil)
		if err != nil {
			return visited, err
		}
		if !iter.Valid() {
			return visited, iter.Close()
		}
		head, err := iter.Key()
		if closeErr := iter.Close(); err != nil || closeErr != nil {
			return visited, fmt.Errorf("worker batch queue is unreadable")
		}
		visited++
		if err := k.WorkerBatchQueue.Remove(ctx, head); err != nil {
			return visited, err
		}
		key := collections.Join(head.K2(), head.K3())
		sdkCtx := sdk.UnwrapSDKContext(ctx)
		cacheCtx, write := sdkCtx.CacheContext()
		if err := k.settleQueuedWorkerBatch(sdk.WrapSDKContext(cacheCtx), key, currentHeight); err != nil {
			sdkCtx.Logger().Error("worker batch payment failed; the batch stays queued off the queue",
				"operator", fmt.Sprintf("%x", []byte(key.K1())), "period", key.K2(), "err", err)
			continue
		}
		write()
	}
	return visited, nil
}

func (k Keeper) settleQueuedWorkerBatch(ctx context.Context, key collections.Pair[types.AddrKey, uint64], height uint64) error {
	batch, err := k.WorkerBatch.Get(ctx, key)
	if err != nil {
		if errIsNotFound(err) {
			return nil
		}
		return err
	}
	if batch.Status != batchQueued || batch.OpenTaskCount != 0 {
		return nil
	}
	due, err := shared.ParseAmount(batch.WorkerDue)
	if err != nil {
		return err
	}
	if err := k.payWorkerBatch(ctx, key, &batch, due, height); err != nil {
		return err
	}
	batch.Status = batchSettled
	batch.SettledHeight = height
	return k.WorkerBatch.Set(ctx, key, batch)
}

// workerBatchView is a batch as queries report it: a batch that waits for a
// member task's late submission shows as SUSPENDED with the count of such
// tasks. Both are read from the audits; the stored row carries CLOSING.
func (k Keeper) workerBatchView(ctx context.Context, batch types.WorkerBatchState) types.WorkerBatchState {
	if workerBatchEnded(batch.Status) || batch.Status == batchQueued {
		return batch
	}
	raw, err := k.sessionAddressToStore("worker_operator_address", batch.OperatorAddress)
	if err != nil {
		return batch
	}
	iter, err := k.WorkerLateIndex.Iterate(ctx, collections.NewPrefixedPairRange[types.AddrKey, types.Hash32Key](raw))
	if err != nil {
		return batch
	}
	defer iter.Close()
	for visited := 0; iter.Valid() && visited < anchorLatePauseScanLimit; iter.Next() {
		visited++
		key, err := iter.Key()
		if err != nil {
			return batch
		}
		member, err := k.WorkerBatchTask.Get(ctx, key.K2())
		if err != nil || member.Period != batch.Period {
			continue
		}
		if audit, err := k.AnchorAudit.Get(ctx, key.K2()); err == nil && audit.ResultHeight == 0 && audit.LateState == lateStateAwaiting {
			batch.AwaitingLateCount++
		}
	}
	if batch.AwaitingLateCount != 0 && batch.Status == batchClosing {
		batch.Status = batchSuspended
	}
	return batch
}

// cleanupWorkerBatchTask removes a task's member row at evidence cleanup once
// its batch has ended. It reports 1 while it removed something.
func (k Keeper) cleanupWorkerBatchTask(ctx context.Context, taskKey types.TaskKey) (uint64, error) {
	member, batch, key, found, err := k.workerBatchOfTask(ctx, taskKey)
	if err != nil {
		return 0, err
	}
	if has, err := k.WorkerBatchTask.Has(ctx, taskKey); err != nil || !has {
		return 0, err
	}
	if found && !workerBatchEnded(batch.Status) {
		return 0, nil
	}
	if found {
		if err := k.WorkerBatchTaskIndex.Remove(ctx, collections.Join3(key.K1(), member.Period, taskKey)); err != nil {
			return 0, err
		}
	}
	return 1, k.WorkerBatchTask.Remove(ctx, taskKey)
}

// rebuildWorkerBatchIndexes restores the derived rows of imported batches.
func (k Keeper) rebuildWorkerBatchIndexes(ctx context.Context, genesis types.GenesisState) error {
	length := genesis.Params.Verification.BatchSettlementPeriodBlocks
	for _, batch := range genesis.WorkerBatches {
		raw, err := k.sessionAddressToStore("worker batch operator_address", batch.OperatorAddress)
		if err != nil {
			return err
		}
		if _, err := shared.ParseAmount(batch.WorkerDue); err != nil {
			return fmt.Errorf("worker batch due is not an amount")
		}
		if _, err := shared.ParseAmount(batch.ForfeitedAmount); err != nil {
			return fmt.Errorf("worker batch forfeited amount is not an amount")
		}
		key := collections.Join(types.AddrKey(raw), batch.Period)
		if has, err := k.WorkerBatch.Has(ctx, key); err != nil {
			return err
		} else if has {
			return fmt.Errorf("duplicate worker batch")
		}
		if err := k.WorkerBatch.Set(ctx, key, batch); err != nil {
			return err
		}
		switch batch.Status {
		case batchOpen:
			end, overflow := checkedHeightAdd(batch.Period*length, length)
			if length == 0 || overflow {
				end = 0 // visited by the first sweep
			}
			if err := k.WorkerBatchCloseIndex.Set(ctx, collections.Join3(end, types.AddrKey(raw), batch.Period)); err != nil {
				return err
			}
		case batchQueued:
			if err := k.WorkerBatchQueue.Set(ctx, collections.Join3(batch.QueuedHeight, types.AddrKey(raw), batch.Period)); err != nil {
				return err
			}
		}
	}
	for _, member := range genesis.WorkerBatchTasks {
		raw, err := k.sessionAddressToStore("worker batch task operator_address", member.OperatorAddress)
		if err != nil {
			return err
		}
		taskKey, err := genesisHashKey("worker batch task task_id", member.TaskId)
		if err != nil {
			return err
		}
		if has, err := k.WorkerBatch.Has(ctx, collections.Join(types.AddrKey(raw), member.Period)); err != nil {
			return err
		} else if !has {
			return fmt.Errorf("worker batch task names a batch that is not imported")
		}
		if err := k.WorkerBatchTask.Set(ctx, taskKey, member); err != nil {
			return err
		}
		if err := k.WorkerBatchTaskIndex.Set(ctx, collections.Join3(types.AddrKey(raw), member.Period, taskKey)); err != nil {
			return err
		}
	}
	return nil
}

// WorkerBatch returns one batch of a Worker.
func (q queryServer) WorkerBatch(ctx context.Context, req *types.QueryWorkerBatchRequest) (*types.QueryWorkerBatchResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "worker batch request is required")
	}
	raw, err := q.k.sessionAddressToStore("operator_address", req.OperatorAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	batch, err := q.k.WorkerBatch.Get(ctx, collections.Join(types.AddrKey(raw), req.Period))
	if err != nil {
		if errIsNotFound(err) {
			return nil, status.Error(codes.NotFound, "no batch of this Worker for the period")
		}
		return nil, err
	}
	return &types.QueryWorkerBatchResponse{Batch: q.k.workerBatchView(ctx, batch)}, nil
}

// WorkerBatches pages over a Worker's batches, latest period first.
func (q queryServer) WorkerBatches(ctx context.Context, req *types.QueryWorkerBatchesRequest) (*types.QueryWorkerBatchesResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "worker batches request is required")
	}
	raw, err := q.k.sessionAddressToStore("operator_address", req.OperatorAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	rng := collections.NewPrefixedPairRange[types.AddrKey, uint64](types.AddrKey(raw)).Descending()
	if req.StartBefore != 0 {
		rng = rng.EndExclusive(req.StartBefore)
	}
	iter, err := q.k.WorkerBatch.Iterate(ctx, rng)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	limit := anchorPageLimit(req.Limit)
	out := &types.QueryWorkerBatchesResponse{}
	for ; iter.Valid(); iter.Next() {
		batch, err := iter.Value()
		if err != nil {
			return nil, err
		}
		if len(out.Batches) >= limit {
			out.Next = out.Batches[len(out.Batches)-1].Period
			break
		}
		out.Batches = append(out.Batches, q.k.workerBatchView(ctx, batch))
	}
	return out, nil
}

// WorkerBatchTasks pages over the member tasks of one batch.
func (q queryServer) WorkerBatchTasks(ctx context.Context, req *types.QueryWorkerBatchTasksRequest) (*types.QueryWorkerBatchTasksResponse, error) {
	if req == nil || (len(req.StartAfter) != 0 && len(req.StartAfter) != types.Hash32Len) {
		return nil, status.Error(codes.InvalidArgument, "worker batch tasks request is invalid")
	}
	raw, err := q.k.sessionAddressToStore("operator_address", req.OperatorAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	rng := collections.NewSuperPrefixedTripleRange[types.AddrKey, uint64, types.Hash32Key](types.AddrKey(raw), req.Period)
	if len(req.StartAfter) == types.Hash32Len {
		// From the given task on; the loop stops at the end of the batch.
		rng = new(collections.Range[collections.Triple[types.AddrKey, uint64, types.Hash32Key]]).
			StartExclusive(collections.Join3(types.AddrKey(raw), req.Period, types.NewTaskKey(req.StartAfter)))
	}
	iter, err := q.k.WorkerBatchTaskIndex.Iterate(ctx, rng)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	limit := anchorPageLimit(req.Limit)
	out := &types.QueryWorkerBatchTasksResponse{}
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		if key.K2() != req.Period || string(key.K1()) != string(raw) {
			break
		}
		if len(out.Tasks) >= limit {
			out.Next = append([]byte(nil), out.Tasks[len(out.Tasks)-1].TaskId...)
			break
		}
		member, err := q.k.WorkerBatchTask.Get(ctx, key.K3())
		if err != nil {
			continue
		}
		out.Tasks = append(out.Tasks, member)
	}
	return out, nil
}
