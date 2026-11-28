package keeper

import (
	"context"
	"fmt"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// quotaLedgerStub is the Hub side of the Worker quota as the Task module sees
// it: one limit per period, reservations and releases recorded in order.
type quotaLedgerStub struct {
	limit    uint64
	reserved map[uint64]uint64
	settled  []quotaSettleCall
	reserves int
}

type quotaSettleCall struct {
	operator                         string
	period, reserved, actual, height uint64
}

func (h *dispatchTestHub) ReserveWorkerQuota(_ context.Context, _ string, period, value, _ uint64) error {
	if h.quota == nil {
		return fmt.Errorf("the quota must not be consulted when no settlement period is configured")
	}
	h.quota.reserves++
	if h.quota.reserved[period]+value > h.quota.limit {
		return fmt.Errorf("%w: test ledger", hubtypes.ErrWorkerQuotaExceeded)
	}
	h.quota.reserved[period] += value
	return nil
}

func (h *dispatchTestHub) SettleWorkerQuota(_ context.Context, operator string, period, reserved, actual, height uint64) error {
	if h.quota == nil {
		return fmt.Errorf("the quota must not be consulted when no settlement period is configured")
	}
	h.quota.settled = append(h.quota.settled, quotaSettleCall{operator, period, reserved, actual, height})
	h.quota.reserved[period] -= min(h.quota.reserved[period], reserved)
	return nil
}

func (h *dispatchTestHub) ApplyTaskRoleFault(ctx context.Context, fact hubtypes.TaskRoleFaultFact) (hubtypes.RoleFaultState, error) {
	h.roleFaults = append(h.roleFaults, fact)
	return h.internalStubHubKeeper.ApplyTaskRoleFault(ctx, fact)
}

const quotaTestPeriodBlocks = uint64(50)

func withSettlementPeriod(p *types.TaskParamsV1) {
	p.Verification.BatchSettlementPeriodBlocks = quotaTestPeriodBlocks
}

func (f *dispatchFixture) recordResult(t *testing.T, sequence uint64) (*types.DispatchEntryV1, *types.BatchItemResultV1) {
	t.Helper()
	entry := f.entry(t, f.signedOrder(t, sequence, f.delegateKey), f.hub.builders[0])
	response, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[0], Entries: []*types.DispatchRecordEntryV1{dispatchEntryItem(entry)}})
	require.NoError(t, err)
	return entry, response.Results[0]
}

// requireRefusedWithoutFault asserts the only effect of a refusal under the
// Worker bond rules: the entry is rejected as "winner not eligible". No task,
// no ineligibility receipt and no fault of the Worker exist afterwards, and
// the order sequence is still free.
func (f *dispatchFixture) requireRefusedWithoutFault(t *testing.T, entry *types.DispatchEntryV1, result *types.BatchItemResultV1) {
	t.Helper()
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_REJECTED, result.Status)
	require.Equal(t, types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_WINNER_NOT_ELIGIBLE, result.RejectCode)
	has, err := f.keeper.DispatchWinnerIneligible.Has(f.ctx, entry.Accept.DispatchTicketHash)
	require.NoError(t, err)
	require.False(t, has, "a refusal under the bond rules is not a Worker ineligibility receipt")
	require.Empty(t, f.hub.roleFaults, "a refusal under the bond rules is never a Worker fault")
	has, err = f.keeper.TaskCore.Has(f.ctx, entry.Record.TaskId)
	require.NoError(t, err)
	require.False(t, has)
	open, err := f.keeper.WorkerHasOpenObligations(f.ctx, f.worker)
	require.NoError(t, err)
	require.False(t, open)
}

// With every new parameter at zero a record is accepted exactly as before:
// the Hub quota is never consulted (the stub fails the record if it is), on
// the record and on the task's terminal path alike.
func TestDispatchRecordIgnoresTheBondRulesAtDefaults(t *testing.T) {
	f := newDispatchFixture(t)
	entry, result := f.recordResult(t, 0)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, result.Status)
	open, err := f.keeper.WorkerHasOpenObligations(f.ctx, f.worker)
	require.NoError(t, err)
	require.True(t, open)
	blocks, err := f.keeper.WorkerQuotaPeriodBlocks(f.ctx)
	require.NoError(t, err)
	require.Zero(t, blocks)

	f.timeOutInference(t, entry)
	open, err = f.keeper.WorkerHasOpenObligations(f.ctx, f.worker)
	require.NoError(t, err)
	require.False(t, open, "the final task is no longer an open obligation")
}

// timeOutInference lets the task's inference deadline pass and sweeps it.
func (f *dispatchFixture) timeOutInference(t *testing.T, entry *types.DispatchEntryV1) uint64 {
	t.Helper()
	assignment, err := f.keeper.ReadTaskAssignment(f.ctx, entry.Record.TaskId)
	require.NoError(t, err)
	height := assignment.InferDeadlineHeight + 1
	ctx := sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(height)))
	visited, err := f.keeper.processExpiredInferDeadlines(ctx, height, 4)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	core, err := f.keeper.TaskCore.Get(f.ctx, entry.Record.TaskId)
	require.NoError(t, err)
	require.Equal(t, shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL, core.FinalityStatus)
	return height
}

func TestPausedWinnerIsRefusedWithoutFault(t *testing.T) {
	for _, reasons := range []uint32{uint32(1), hubtypes.WorkerPauseReasonUnbonding, uint32(4), 7} {
		f := newDispatchFixture(t)
		f.hub.pauseReasons = reasons
		entry, result := f.recordResult(t, 0)
		f.requireRefusedWithoutFault(t, entry, result)

		// Once the pause is lifted the same Worker is recorded again.
		f.hub.pauseReasons = 0
		_, result = f.recordResult(t, 0)
		require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, result.Status, "reasons %d", reasons)
	}
}

// The fixture's winner has an effective bond of 4,000,000 and serves a tier 1
// Profile.
func TestBondFloorByTierRows(t *testing.T) {
	floor := func(tier uint32, amount uint64) hubtypes.WorkerBondFloorV1 {
		return hubtypes.WorkerBondFloorV1{ResourceTier: tier, MinBond: shared.NewAmount(amount)}
	}
	for _, row := range []struct {
		name    string
		floors  []hubtypes.WorkerBondFloorV1
		refused bool
	}{
		{"no floor", nil, false},
		{"floor below the bond", []hubtypes.WorkerBondFloorV1{floor(1, 3_999_999)}, false},
		{"floor equal to the bond", []hubtypes.WorkerBondFloorV1{floor(1, 4_000_000)}, false},
		{"floor above the bond", []hubtypes.WorkerBondFloorV1{floor(1, 4_000_001)}, true},
		{"floor of another tier only", []hubtypes.WorkerBondFloorV1{floor(2, 9_000_000), floor(5, 9_000_000)}, false},
		{"this tier among several", []hubtypes.WorkerBondFloorV1{floor(1, 5_000_000), floor(2, 1)}, true},
	} {
		f := newDispatchFixture(t)
		f.hub.quotaTerms.BondFloorByTier = row.floors
		entry, result := f.recordResult(t, 0)
		if row.refused {
			f.requireRefusedWithoutFault(t, entry, result)
			continue
		}
		require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, result.Status, row.name)
	}
}

func (f *dispatchFixture) orderValue(t *testing.T, entry *types.DispatchEntryV1) uint64 {
	t.Helper()
	core, err := f.keeper.TaskCore.Get(f.ctx, entry.Record.TaskId)
	require.NoError(t, err)
	value, err := shared.ParseAmount(core.OrderValue)
	require.NoError(t, err)
	require.NotZero(t, value)
	return value
}

func (f *dispatchFixture) atHeight(height uint64) {
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(height)))
}

// One period's order value is bounded by the quota. The order that does not
// fit is refused; the order recorded before it is untouched, settles its
// reservation at finality and frees the quota for the next order.
func TestQuotaRefusesOnlyTheNewRecord(t *testing.T) {
	f := newDispatchFixtureAtHeight(t, 121, withSettlementPeriod)
	f.hub.quota = &quotaLedgerStub{limit: 1 << 62, reserved: map[uint64]uint64{}}
	first, result := f.recordResult(t, 0)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, result.Status)
	value := f.orderValue(t, first)
	period := uint64(123) / quotaTestPeriodBlocks
	require.Equal(t, map[uint64]uint64{period: value}, f.hub.quota.reserved, "the reservation is the order value, in the period of the record height")

	// Room for one and a half orders: the second does not fit.
	f.hub.quota.limit = value + value/2
	second, result := f.recordResult(t, 1)
	f.requireRefusedWithoutFaultWhileHolding(t, second, result)
	require.Equal(t, map[uint64]uint64{period: value}, f.hub.quota.reserved)
	core, err := f.keeper.TaskCore.Get(f.ctx, first.Record.TaskId)
	require.NoError(t, err)
	require.Equal(t, types.TaskPhase_TASK_PHASE_INFER_PENDING, core.TaskPhase, "the recorded order is not affected by the refusal")

	// The next period starts with its own quota.
	f.atHeight(3 * quotaTestPeriodBlocks)
	third, result := f.recordResult(t, 1)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, result.Status)
	require.Equal(t, map[uint64]uint64{period: value, 3: f.orderValue(t, third)}, f.hub.quota.reserved)
}

func (f *dispatchFixture) requireRefusedWithoutFaultWhileHolding(t *testing.T, entry *types.DispatchEntryV1, result *types.BatchItemResultV1) {
	t.Helper()
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_REJECTED, result.Status)
	require.Equal(t, types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_WINNER_NOT_ELIGIBLE, result.RejectCode)
	has, err := f.keeper.DispatchWinnerIneligible.Has(f.ctx, entry.Accept.DispatchTicketHash)
	require.NoError(t, err)
	require.False(t, has)
	require.Empty(t, f.hub.roleFaults)
	has, err = f.keeper.TaskCore.Has(f.ctx, entry.Record.TaskId)
	require.NoError(t, err)
	require.False(t, has)
}

// A task that ends without a receipt releases its whole reservation: the
// period is the one of the record height, the actual value is zero. The only
// fault is the existing inference-timeout fault of that task.
func TestQuotaReservationIsReleasedWhenTheTaskFails(t *testing.T) {
	f := newDispatchFixtureAtHeight(t, 121, withSettlementPeriod)
	f.hub.quota = &quotaLedgerStub{limit: 1 << 62, reserved: map[uint64]uint64{}}
	entry, result := f.recordResult(t, 0)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, result.Status)
	value := f.orderValue(t, entry)

	height := f.timeOutInference(t, entry)
	require.Equal(t, []quotaSettleCall{{operator: f.worker, period: 123 / quotaTestPeriodBlocks, reserved: value, actual: 0, height: height}}, f.hub.quota.settled)
	require.Zero(t, f.hub.quota.reserved[123/quotaTestPeriodBlocks])
	require.Len(t, f.hub.roleFaults, 1)
	require.Equal(t, hubtypes.FaultTypeWorkerInferTimeout, f.hub.roleFaults[0].FaultType)
}

// bumpTaskParams installs a new parameter version the way a governance update
// does: the replaced body is archived and tasks pinned to it keep reading it.
func (f *dispatchFixture) bumpTaskParams(t *testing.T, mutate func(*types.TaskParamsV1)) {
	t.Helper()
	meta, err := f.keeper.GetTaskParamsMeta(f.ctx)
	require.NoError(t, err)
	current, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	next := current
	mutate(&next)
	require.NoError(t, next.Validate())
	require.NoError(t, f.keeper.archiveParamsForUpdate(f.ctx, meta.ParamsVersion, current))
	require.NoError(t, f.keeper.Params.Set(f.ctx, next))
	hash, err := types.TaskParamsHashV1(sdk.UnwrapSDKContext(f.ctx).ChainID(), meta.ParamsVersion+1, next)
	require.NoError(t, err)
	require.NoError(t, f.keeper.ParamsMeta.Set(f.ctx, types.TaskParamsMetaState{ParamsVersion: meta.ParamsVersion + 1, ParamsHash: hash, UpdatedHeight: uint64(sdk.UnwrapSDKContext(f.ctx).BlockHeight())}))
}

// An order recorded before the upgrade turned the settlement period on has no
// reservation. It ends under its own frozen parameters: the quota is not
// consulted for it and nothing is refused or faulted because of the switch.
func TestOrderInFlightAcrossTheSwitchKeepsItsOwnRules(t *testing.T) {
	f := newDispatchFixture(t)
	before, result := f.recordResult(t, 0)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, result.Status)

	f.atHeight(130)
	f.bumpTaskParams(t, withSettlementPeriod)
	f.hub.quota = &quotaLedgerStub{limit: 1 << 62, reserved: map[uint64]uint64{}}
	after, result := f.recordResult(t, 1)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, result.Status)
	require.Equal(t, 1, f.hub.quota.reserves)

	f.timeOutInference(t, before)
	require.Empty(t, f.hub.quota.settled, "the earlier order never held a reservation")
	f.timeOutInference(t, after)
	require.Len(t, f.hub.quota.settled, 1)

	// And the other way round: an order recorded with a period keeps releasing
	// after the period is switched off again.
	f.atHeight(135)
	third, result := f.recordResult(t, 2)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, result.Status)
	f.bumpTaskParams(t, func(p *types.TaskParamsV1) { p.Verification.BatchSettlementPeriodBlocks = 0 })
	f.timeOutInference(t, third)
	require.Len(t, f.hub.quota.settled, 2)
}

func TestSettledOrderValueRows(t *testing.T) {
	for _, row := range []struct {
		name          string
		tokens, price uint64
		ratio         uint32
		want          uint64
	}{
		{"nothing generated", 0, 2_000_000, 2500, 0},
		{"gross plus verify share", 10, 1_000_000, 2500, 12},
		{"shares round down", 7, 1_500_000, 1000, 11},
		{"no verify share", 10, 1_000_000, 0, 10},
	} {
		got, err := settledOrderValue(types.SettlementPlanV1{GeneratedTokenCount: row.tokens}, types.TaskBudgetState{PriceBid: shared.NewAmount(row.price), VerifyRatioBpsSnapshot: row.ratio})
		require.NoError(t, err, row.name)
		require.Equal(t, row.want, got, row.name)
	}
	_, err := settledOrderValue(types.SettlementPlanV1{GeneratedTokenCount: 1 << 63}, types.TaskBudgetState{PriceBid: shared.NewAmount(1 << 63)})
	require.Error(t, err)
}

type settleRecordingHub struct {
	types.HubKeeper
	calls *[]quotaSettleCall
}

func (h settleRecordingHub) SettleWorkerQuota(_ context.Context, operator string, period, reserved, actual, height uint64) error {
	*h.calls = append(*h.calls, quotaSettleCall{operator, period, reserved, actual, height})
	return nil
}

// settleUnsampled runs an anchor-only task from receipt to settlement.
func settleUnsampled(t *testing.T, f *anchorOnlyFixture) uint64 {
	t.Helper()
	f.submitReceipt(t)
	f.sampleRound(t)
	deadline, err := settlementDeadlineHeight(f.window.SelectionRandomnessHeight, f.params)
	require.NoError(t, err)
	visited, err := f.keeper.processExpiredSettlementDeadlines(f.at(int64(deadline)), deadline, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	settlement, err := f.keeper.ReadTaskSettlement(f.ctx, f.taskKey)
	require.NoError(t, err)
	f.requirePaidInFull(t, settlement)
	return deadline
}

// A settled task releases its reservation down to the order value of what was
// generated: 10 tokens at price 1 plus the 25% verify share, against a
// reservation of the order value 800.
func TestQuotaReservationSettlesAtTheActualValue(t *testing.T) {
	f := newAnchorOnlyFixture(t, 1_000_000_000, true)
	f.params.Verification.BatchSettlementPeriodBlocks = quotaTestPeriodBlocks
	f.zeroAuditRate(t)
	calls := []quotaSettleCall{}
	f.keeper.hubKeeper = settleRecordingHub{HubKeeper: f.keeper.hubKeeper, calls: &calls}
	f.server = msgServer{k: f.keeper}
	height := settleUnsampled(t, f)
	require.Equal(t, []quotaSettleCall{{operator: f.worker, period: 0, reserved: 800, actual: 12, height: height}}, calls)
	require.Empty(t, *f.faults)
}

// Without a settlement period the settlement path never reaches the quota:
// the base stub has no quota methods and would panic.
func TestSettlementIgnoresTheQuotaAtDefaults(t *testing.T) {
	f := newAnchorOnlyFixture(t, 1_000_000_000, true)
	f.zeroAuditRate(t)
	settleUnsampled(t, f)
}
