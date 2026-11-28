package keeper

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

const (
	// batchPrice makes the task's amounts large enough to divide: ten tokens
	// are worth 300, the verify share 75, a panel seat 25.
	batchPrice       = uint64(30_000_000)
	batchGross       = uint64(300)
	batchWorkerNet   = uint64(285) // 5% maintenance
	batchSeat        = uint64(25)
	batchSeatNet     = uint64(24)
	batchRoundFee    = uint64(75)
	batchMaxPerBlock = uint32(2)
)

// SendCoinsFromModuleToModule records the transfer and fails for an amount a
// test marked as failing.
func (b panelRecordingBank) SendCoinsFromModuleToModule(ctx context.Context, from, to string, coins sdk.Coins) error {
	amount := coins[0].Amount.Int64()
	if amount == failingBatchTransfer {
		return errors.New("transfer refused")
	}
	*b.log = append(*b.log, panelBankTransfer{from: from, to: to, amount: amount})
	return b.BankKeeper.SendCoinsFromModuleToModule(ctx, from, to, coins)
}

// failingBatchTransfer is the amount the recording bank refuses to move.
const failingBatchTransfer = int64(666)

type batchFixture struct {
	*anchorPanelFixture
	log *[]panelBankTransfer
	raw []byte
}

// newBatchFixture stages a sampled panel audit, late submission on, whose task
// is a member of its Worker's batch of the current period.
func newBatchFixture(t *testing.T, bond uint64, candidates int) *batchFixture {
	t.Helper()
	log := &[]panelBankTransfer{}
	panel := newTunedAnchorPanelFixture(t, bond, candidates, func(params *types.TaskParamsV1) {
		lateParams(lateFreeCount)(params)
		params.Verification.BatchSettlementMaxWorkersPerBlock = batchMaxPerBlock
	}, func(base *anchorOnlyFixture) {
		budget, err := base.keeper.TaskBudget.Get(base.ctx, base.taskKey)
		require.NoError(t, err)
		budget.PriceBid = shared.NewAmount(batchPrice)
		require.NoError(t, base.keeper.TaskBudget.Set(base.ctx, base.taskKey, budget))
		base.priceTokens = batchPrice
		require.NoError(t, base.keeper.joinWorkerBatch(base.ctx, base.taskKey, base.worker, base.receiptAt, base.params))
	})
	panel.keeper.bankKeeper = panelRecordingBank{BankKeeper: panel.keeper.bankKeeper, log: log}
	panel.server = msgServer{k: panel.keeper}
	raw, err := panel.keeper.sessionAddressToStore("worker", panel.worker)
	require.NoError(t, err)
	return &batchFixture{anchorPanelFixture: panel, log: log, raw: raw}
}

func (f *batchFixture) batch(t *testing.T, period uint64) types.WorkerBatchState {
	t.Helper()
	response, err := (queryServer{k: f.keeper}).WorkerBatch(f.ctx, &types.QueryWorkerBatchRequest{OperatorAddress: f.worker, Period: period})
	require.NoError(t, err)
	return response.Batch
}

func (f *batchFixture) transfers(from, to string) (total int64) {
	for _, transfer := range *f.log {
		if transfer.from == from && transfer.to == to {
			total += transfer.amount
		}
	}
	return total
}

func (f *batchFixture) credited(operator string) (total uint64) {
	for _, credit := range *f.credits {
		if credit.Beneficiary == operator {
			amount, _ := shared.ParseAmount(credit.Amount)
			total += amount
		}
	}
	return total
}

// passAndSettle takes the audit to a final PASS with two of three members
// voting and settles the task.
func (f *batchFixture) passAndSettle(t *testing.T) (types.AnchorPanelRoundState, types.TaskSettlementState) {
	t.Helper()
	f.openAndProveSampled(t, f.audit)
	f.reload(t)
	round := f.assign(t)
	f.castVotes(t, round, round.AssignmentHeight+1, votePass, votePass)
	audit := f.reload(t)
	f.sweep(t, audit.PublicationEndHeight)
	settlement, err := f.settleNow(t, audit.PublicationEndHeight+1)
	require.NoError(t, err)
	return round, settlement
}

// closes runs the period-end step at height; pays runs the payment step.
func (f *batchFixture) closes(t *testing.T, height uint64) uint64 {
	t.Helper()
	visited, err := f.keeper.processWorkerBatchCloses(f.at(int64(height)), height, 10)
	require.NoError(t, err)
	return visited
}

func (f *batchFixture) pays(t *testing.T, height uint64) uint64 {
	t.Helper()
	visited, err := f.keeper.processWorkerBatchQueue(f.at(int64(height)), height, 10)
	require.NoError(t, err)
	return visited
}

// A member task settles on its own, but what the Worker earned is held for
// the batch: the Worker is credited once, after the period, by the queue.
// Panel members and the user are paid with the task.
func TestBatchHoldsTheWorkerNetUntilThePeriodIsOver(t *testing.T) {
	f := newBatchFixture(t, 4_000, 6)
	require.Zero(t, f.audit.SettlementPeriod, "the fixture height lies in period 0")
	batch := f.batch(t, 0)
	require.Equal(t, batchOpen, batch.Status)
	require.Equal(t, uint32(1), batch.TaskCount)
	require.Equal(t, uint32(1), batch.OpenTaskCount)

	round, settlement := f.passAndSettle(t)
	f.requireAuditedPaid(t, settlement)
	require.Equal(t, shared.NewAmount(batchWorkerNet), settlement.WorkerNet)
	require.Zero(t, f.credited(f.worker), "the Worker is not credited with the task")
	require.Equal(t, batchSeatNet, f.credited(round.Members[0]))
	require.Equal(t, batchSeatNet, f.credited(round.Members[1]))
	require.Zero(t, f.credited(round.Members[2]), "a member that did not vote is not paid")
	require.Equal(t, int64(batchWorkerNet), f.transfers(shared.TaskEscrowModuleName, shared.TaskChallengeEffectModuleName))
	require.Equal(t, int64(2*batchSeatNet), f.transfers(shared.TaskEscrowModuleName, hubtypes.RewardsModuleName))

	batch = f.batch(t, 0)
	require.Equal(t, batchOpen, batch.Status, "the period is not over")
	require.Zero(t, batch.OpenTaskCount)
	require.Equal(t, uint32(1), batch.AuditedCount)
	require.Equal(t, shared.NewAmount(batchWorkerNet), batch.WorkerDue)
	tasks, err := (queryServer{k: f.keeper}).WorkerBatchTasks(f.ctx, &types.QueryWorkerBatchTasksRequest{OperatorAddress: f.worker})
	require.NoError(t, err)
	require.Len(t, tasks.Tasks, 1)
	require.True(t, tasks.Tasks[0].Final)
	require.Equal(t, shared.NewAmount(batchWorkerNet), tasks.Tasks[0].WorkerNet)

	require.Zero(t, f.closes(t, latePeriod-1))
	require.Zero(t, f.pays(t, latePeriod-1))
	require.Equal(t, uint64(1), f.closes(t, latePeriod))
	batch = f.batch(t, 0)
	require.Equal(t, batchQueued, batch.Status)
	require.Equal(t, latePeriod, batch.QueuedHeight)
	require.Equal(t, uint64(1), f.pays(t, latePeriod))
	batch = f.batch(t, 0)
	require.Equal(t, batchSettled, batch.Status)
	require.Equal(t, latePeriod, batch.SettledHeight)
	require.Equal(t, batchWorkerNet, f.credited(f.worker), "one credit for the period")
	require.Equal(t, int64(batchWorkerNet), f.transfers(shared.TaskChallengeEffectModuleName, hubtypes.RewardsModuleName))
	require.Zero(t, f.pays(t, latePeriod+1))

	list, err := (queryServer{k: f.keeper}).WorkerBatches(f.ctx, &types.QueryWorkerBatchesRequest{OperatorAddress: f.worker})
	require.NoError(t, err)
	require.Len(t, list.Batches, 1)
	_, err = (queryServer{k: f.keeper}).WorkerBatch(f.ctx, &types.QueryWorkerBatchRequest{OperatorAddress: f.worker, Period: 7})
	require.Error(t, err)
}

// A period that ends while a member task is still open waits for it. While
// the task waits for a late submission the batch shows as suspended. It is
// queued by the task's settlement and paid by the next queue run.
func TestBatchWaitsForItsLastOpenTask(t *testing.T) {
	f := newBatchFixture(t, 4_000, 6)
	recorded := f.timeOutTheProof(t)
	require.Equal(t, uint64(1), f.closes(t, latePeriod))
	batch := f.batch(t, 0)
	require.Equal(t, batchSuspended, batch.Status, "a member task waits for its late submission")
	require.Equal(t, uint32(1), batch.AwaitingLateCount)
	require.Zero(t, f.pays(t, latePeriod), "nothing is queued")

	_, err := f.proveAt(recorded+1, "p")
	require.NoError(t, err)
	require.Equal(t, batchClosing, f.batch(t, 0).Status)
	round := f.assign(t)
	f.castVotes(t, round, round.AssignmentHeight+1, votePass, votePass)
	audit := f.reload(t)
	f.sweep(t, audit.PublicationEndHeight)
	settledAt := latePeriod + 50
	if audit.PublicationEndHeight+1 > settledAt {
		settledAt = audit.PublicationEndHeight + 1
	}
	settlement, err := f.settleNow(t, settledAt)
	require.NoError(t, err)
	f.requireAuditedPaid(t, settlement)
	batch = f.batch(t, 0)
	require.Equal(t, batchQueued, batch.Status)
	require.Equal(t, settledAt, batch.QueuedHeight)
	require.Equal(t, uint64(1), f.pays(t, settledAt))
	require.Equal(t, batchSettled, f.batch(t, 0).Status)
	require.Equal(t, batchWorkerNet, f.credited(f.worker))
}

// addMember adds another, already final member with the given net to the
// fixture Worker's batch of period 0, as its settlement would have.
func (f *batchFixture) addMember(t *testing.T, tag byte, net uint64) types.TaskKey {
	t.Helper()
	taskKey := types.NewTaskKey(bytes.Repeat([]byte{tag}, types.Hash32Len))
	require.NoError(t, f.keeper.joinWorkerBatch(f.ctx, taskKey, f.worker, f.receiptAt, f.params))
	require.NoError(t, f.keeper.closeWorkerBatchTask(f.ctx, taskKey, net, false, types.WorkerBatchStatusV1_WORKER_BATCH_STATUS_V1_UNSPECIFIED, f.receiptAt))
	return taskKey
}

// A member task that ends in a rejection forfeits the batch: what was held
// goes to the treasury, what later members earn goes there too, and the
// Worker is never credited for the period.
func TestRejectedTaskForfeitsTheBatch(t *testing.T) {
	const bond = uint64(4_000)
	f := newBatchFixture(t, bond, 6)
	f.addMember(t, 0xb1, 100)
	require.Equal(t, shared.NewAmount(100), f.batch(t, 0).WorkerDue)

	f.openAndProveSampled(t, f.audit)
	f.reload(t)
	round := f.assign(t)
	f.castVotes(t, round, round.AssignmentHeight+1, voteReject, voteReject)
	audit := f.reload(t)
	f.sweep(t, audit.PublicationEndHeight)
	settlement, err := f.settleNow(t, audit.PublicationEndHeight+1)
	require.NoError(t, err)
	require.Equal(t, types.TaskVerdict_TASK_VERDICT_FAIL, settlement.Verdict)
	require.Equal(t, shared.NewAmount(0), settlement.WorkerNet)
	require.Len(t, *f.faults, 1)
	require.Equal(t, hubtypes.FaultTypeAnchorReject, (*f.faults)[0].FaultType)
	// The panel that judged the task is paid from what the user reserved.
	require.Equal(t, batchSeatNet, f.credited(round.Members[0]))
	require.Equal(t, shared.NewAmount(f.reserved-batchRoundFee), settlement.RefundAmount)

	batch := f.batch(t, 0)
	require.Equal(t, batchForfeitedCheat, batch.Status)
	require.Equal(t, f.taskID, batch.ForfeitingTaskId)
	require.Equal(t, shared.NewAmount(0), batch.WorkerDue)
	require.Equal(t, shared.NewAmount(100), batch.ForfeitedAmount)
	require.Equal(t, int64(100), f.transfers(shared.TaskChallengeEffectModuleName, hubtypes.TreasuryModuleName))

	f.addMember(t, 0xb2, 50)
	batch = f.batch(t, 0)
	require.Equal(t, shared.NewAmount(150), batch.ForfeitedAmount, "a member that becomes final later earns the Worker nothing either")
	require.Equal(t, int64(150), f.transfers(shared.TaskChallengeEffectModuleName, hubtypes.TreasuryModuleName))

	require.Equal(t, uint64(1), f.closes(t, latePeriod))
	require.Zero(t, f.pays(t, latePeriod))
	require.Equal(t, batchForfeitedCheat, f.batch(t, 0).Status)
	require.Zero(t, f.credited(f.worker))
}

// An expired late window forfeits the batch as a timeout forfeiture. A timeout
// past the free count, which has no late window, fails its task only.
func TestLateExpiredForfeitsAndARepeatTimeoutDoesNot(t *testing.T) {
	f := newBatchFixture(t, 4_000, 6)
	f.addMember(t, 0xb1, 100)
	require.Equal(t, uint64(1), f.sweep(t, f.audit.OpeningDeadlineHeight+1))
	deadline := f.reload(t).LateDeadlineHeight
	_, err := f.settleNow(t, deadline)
	require.NoError(t, err)
	batch := f.batch(t, 0)
	require.Equal(t, batchForfeitedTimeout, batch.Status)
	require.Equal(t, shared.NewAmount(100), batch.ForfeitedAmount)

	log := &[]panelBankTransfer{}
	panel := newTunedAnchorPanelFixture(t, 4_000, 6, lateParams(0), func(base *anchorOnlyFixture) {
		require.NoError(t, base.keeper.joinWorkerBatch(base.ctx, base.taskKey, base.worker, base.receiptAt, base.params))
	})
	g := &batchFixture{anchorPanelFixture: panel, log: log}
	g.addMember(t, 0xb1, 100)
	recorded := g.audit.OpeningDeadlineHeight + 1
	require.Equal(t, uint64(1), g.sweep(t, recorded))
	_, err = g.settleNow(t, recorded+1)
	require.NoError(t, err)
	batch = g.batch(t, 0)
	require.Equal(t, batchOpen, batch.Status, "a missed deadline alone forfeits nothing")
	require.Equal(t, shared.NewAmount(100), batch.WorkerDue)
}

// A batch that was paid is never taken back.
func TestPaidBatchIsNotTakenBack(t *testing.T) {
	f := newBatchFixture(t, 4_000, 6)
	f.passAndSettle(t)
	f.closes(t, latePeriod)
	f.pays(t, latePeriod)
	require.Equal(t, batchSettled, f.batch(t, 0).Status)
	late := types.NewTaskKey(bytes.Repeat([]byte{0xb3}, types.Hash32Len))
	require.NoError(t, f.keeper.joinWorkerBatch(f.ctx, late, f.worker, f.receiptAt, f.params))
	require.NoError(t, f.keeper.closeWorkerBatchTask(f.ctx, late, 0, true, batchForfeitedCheat, latePeriod+1))
	batch := f.batch(t, 0)
	require.Equal(t, batchSettled, batch.Status)
	require.Empty(t, batch.ForfeitingTaskId)
	require.Equal(t, batchWorkerNet, f.credited(f.worker))
}

// Verifiers that stay silent, in both rounds, forfeit nothing and cost the
// Worker nothing: the task ends without a verdict and its earnings are paid
// with the batch.
func TestSilentPanelsDoNotTouchTheBatch(t *testing.T) {
	f := newBatchFixture(t, 4_000, 12)
	f.openAndProveSampled(t, f.audit)
	f.reload(t)
	audit := f.silence(t)
	settlement, err := f.settleNow(t, audit.LateDeadlineHeight)
	require.NoError(t, err)
	f.requireAuditedPaid(t, settlement)
	batch := f.batch(t, 0)
	require.Equal(t, shared.NewAmount(batchWorkerNet), batch.WorkerDue)
	require.Empty(t, batch.ForfeitingTaskId)
	f.closes(t, latePeriod)
	f.pays(t, latePeriod)
	require.Equal(t, batchSettled, f.batch(t, 0).Status)
	require.Equal(t, batchWorkerNet, f.credited(f.worker))
	require.Empty(t, *f.faults)
}

// stageQueued writes a queued batch of another Worker with the given due.
func (f *batchFixture) stageQueued(t *testing.T, tag byte, queuedAt, due uint64) string {
	t.Helper()
	raw := bytes.Repeat([]byte{tag}, 20)
	operator := sdk.AccAddress(raw).String()
	require.NoError(t, f.keeper.WorkerBatch.Set(f.ctx, collections.Join(types.AddrKey(raw), uint64(0)), types.WorkerBatchState{
		OperatorAddress: operator, Status: batchQueued, QueuedHeight: queuedAt, TaskCount: 1,
		WorkerDue: shared.NewAmount(due), ForfeitedAmount: shared.NewAmount(0),
	}))
	require.NoError(t, f.keeper.WorkerBatchQueue.Set(f.ctx, collections.Join3(queuedAt, types.AddrKey(raw), uint64(0))))
	return operator
}

// The queue pays a bounded number of batches per block, oldest first, ties by
// operator address. A batch whose payment fails does not stop the block or
// the batches behind it.
func TestBatchQueueIsBoundedOrderedAndSurvivesAFailure(t *testing.T) {
	f := newBatchFixture(t, 4_000, 6)
	third := f.stageQueued(t, 0xc3, 20, 30)
	first := f.stageQueued(t, 0xc2, 10, 10)
	second := f.stageQueued(t, 0xc1, 20, 20)
	broken := f.stageQueued(t, 0xc0, 20, uint64(failingBatchTransfer))

	require.Equal(t, uint64(batchMaxPerBlock), f.pays(t, 100))
	require.Equal(t, uint64(10), f.credited(first), "the oldest batch is paid first")
	require.Zero(t, f.credited(broken), "the failing batch is skipped")
	require.Zero(t, f.credited(second))
	require.Equal(t, uint64(batchMaxPerBlock), f.pays(t, 101))
	require.Equal(t, uint64(20), f.credited(second))
	require.Equal(t, uint64(30), f.credited(third))
	require.Zero(t, f.pays(t, 102))

	raw := bytes.Repeat([]byte{0xc0}, 20)
	stuck, err := f.keeper.WorkerBatch.Get(f.ctx, collections.Join(types.AddrKey(raw), uint64(0)))
	require.NoError(t, err)
	require.Equal(t, batchQueued, stuck.Status, "its held amount is untouched")
	require.Equal(t, shared.NewAmount(uint64(failingBatchTransfer)), stuck.WorkerDue)

	// With the per-block parameter unset the queue still moves, one a block.
	params := f.params
	params.Verification.BatchSettlementMaxWorkersPerBlock = 0
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))
	f.stageQueued(t, 0xc4, 30, 1)
	f.stageQueued(t, 0xc5, 30, 2)
	require.Equal(t, uint64(1), f.pays(t, 103))
}

// Batches and their member rows are exported; an import restores the period
// end and the payment queue.
func TestBatchesSurviveExportAndImport(t *testing.T) {
	f := newBatchFixture(t, 4_000, 6)
	f.addMember(t, 0xb1, 100)
	queued := f.stageQueued(t, 0xc1, 20, 20)
	batches, err := collectMapValues(f.ctx, f.keeper.WorkerBatch)
	require.NoError(t, err)
	members, err := collectMapValues(f.ctx, f.keeper.WorkerBatchTask)
	require.NoError(t, err)
	require.Len(t, batches, 2)
	require.Len(t, members, 2)

	require.NoError(t, f.keeper.WorkerBatch.Clear(f.ctx, nil))
	require.NoError(t, f.keeper.WorkerBatchTask.Clear(f.ctx, nil))
	require.NoError(t, f.keeper.WorkerBatchTaskIndex.Clear(f.ctx, nil))
	require.NoError(t, f.keeper.WorkerBatchCloseIndex.Clear(f.ctx, nil))
	require.NoError(t, f.keeper.WorkerBatchQueue.Clear(f.ctx, nil))

	genesis := types.GenesisState{Params: f.params, WorkerBatches: batches, WorkerBatchTasks: members}
	require.NoError(t, f.keeper.rebuildWorkerBatchIndexes(f.ctx, genesis))
	require.ErrorContains(t, f.keeper.rebuildWorkerBatchIndexes(f.ctx, genesis), "duplicate")
	tasks, err := (queryServer{k: f.keeper}).WorkerBatchTasks(f.ctx, &types.QueryWorkerBatchTasksRequest{OperatorAddress: f.worker, Limit: 1})
	require.NoError(t, err)
	require.Len(t, tasks.Tasks, 1)
	require.NotEmpty(t, tasks.Next)
	rest, err := (queryServer{k: f.keeper}).WorkerBatchTasks(f.ctx, &types.QueryWorkerBatchTasksRequest{OperatorAddress: f.worker, StartAfter: tasks.Next})
	require.NoError(t, err)
	require.Len(t, rest.Tasks, 1)
	require.Equal(t, uint64(1), f.pays(t, 100))
	require.Equal(t, uint64(20), f.credited(queued))
	require.Equal(t, uint64(1), f.closes(t, latePeriod), "the open batch's period end was restored")

	orphan := types.WorkerBatchTaskState{OperatorAddress: f.worker, Period: 9, TaskId: bytes.Repeat([]byte{0xb9}, types.Hash32Len)}
	require.ErrorContains(t, f.keeper.rebuildWorkerBatchIndexes(f.ctx, types.GenesisState{WorkerBatchTasks: []types.WorkerBatchTaskState{orphan}}), "not imported")
}

// A member task keeps its rows until its batch is paid or forfeited; then its
// member row goes with the rest.
func TestBatchMemberIsRetainedUntilTheBatchEnds(t *testing.T) {
	f := newBatchFixture(t, 4_000, 6)
	f.passAndSettle(t)
	blocked, err := f.keeper.taskCleanupBlocked(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.True(t, blocked, "the batch is not paid yet")
	removed, err := f.keeper.cleanupWorkerBatchTask(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.Zero(t, removed)

	f.closes(t, latePeriod)
	f.pays(t, latePeriod)
	removed, err = f.keeper.cleanupWorkerBatchTask(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.Equal(t, uint64(1), removed)
	has, err := f.keeper.WorkerBatchTask.Has(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.False(t, has)
	removed, err = f.keeper.cleanupWorkerBatchTask(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.Zero(t, removed)
}

// Without a settlement period in its parameters a task joins no batch and its
// Worker is credited with the task, as before.
func TestTaskWithoutASettlementPeriodIsPaidAtOnce(t *testing.T) {
	f := newAnchorPanelFixture(t, 4_000, 6)
	require.NoError(t, f.keeper.joinWorkerBatch(f.ctx, f.taskKey, f.worker, f.receiptAt, f.params))
	has, err := f.keeper.WorkerBatchTask.Has(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.False(t, has)
	round := f.assign(t)
	f.castVotes(t, round, round.AssignmentHeight+1, votePass, votePass)
	audit := f.reload(t)
	f.sweep(t, audit.PublicationEndHeight)
	settlement, err := f.settleNow(t, audit.PublicationEndHeight+1)
	require.NoError(t, err)
	f.requireAuditedPaid(t, settlement)
	credited := false
	for _, credit := range *f.credits {
		credited = credited || credit.Beneficiary == f.worker
	}
	require.True(t, credited, "the Worker is credited by the task's own settlement")
}

// A dispatch recorded under parameters with a settlement period joins the
// Worker's batch of that period; without one it joins none.
func TestRecordedDispatchJoinsTheWorkerBatch(t *testing.T) {
	for _, period := range []uint64{0, 1_000} {
		f := newDispatchFixture(t)
		params, err := f.keeper.Params.Get(f.ctx)
		require.NoError(t, err)
		params.Verification.BatchSettlementPeriodBlocks = period
		require.NoError(t, f.keeper.Params.Set(f.ctx, params))
		entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
		_, err = NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, &types.MsgRecordDispatch{
			SubmitterAddress: f.hub.builders[0], Entries: []*types.DispatchRecordEntryV1{dispatchEntryItem(entry)},
		})
		require.NoError(t, err)
		member, err := f.keeper.WorkerBatchTask.Get(f.ctx, types.NewTaskKey(entry.Record.TaskId))
		if period == 0 {
			require.True(t, errIsNotFound(err))
			continue
		}
		require.NoError(t, err)
		winner := entry.WinnerTicket.Target.OperatorAddress
		require.Equal(t, winner, member.OperatorAddress)
		height := uint64(sdk.UnwrapSDKContext(f.ctx).BlockHeight())
		require.Equal(t, height/period, member.Period)
		response, err := (queryServer{k: f.keeper}).WorkerBatch(f.ctx, &types.QueryWorkerBatchRequest{OperatorAddress: winner, Period: member.Period})
		require.NoError(t, err)
		require.Equal(t, uint32(1), response.Batch.OpenTaskCount)
	}
}
