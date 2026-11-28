package keeper_test

import (
	"context"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

const quotaTestHeight = uint64(100)

type settlementViewStub struct {
	periodBlocks uint64
	open         map[string]bool
	err          error
}

func (s settlementViewStub) WorkerQuotaPeriodBlocks(context.Context) (uint64, error) {
	return s.periodBlocks, s.err
}

func (s settlementViewStub) WorkerHasOpenObligations(_ context.Context, operator string) (bool, error) {
	return s.open[operator], s.err
}

// quotaFixture registers one Worker with a bond of seven times the minimum,
// effective at once, and applies the given service parameter changes.
func quotaFixture(t *testing.T, mutate func(*types.ServiceParamsV1)) (*fixture, string) {
	t.Helper()
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	operator := hubIdentity(t, 221)
	registerCortexNodeIdentityForTest(t, f, operator.Address, hubIdentity(t, 222), 7*testServiceBondMinInitial, quotaTestHeight, 0)
	activateServiceBondForTest(t, f, operator.Address, 0)
	if mutate != nil {
		params, err := f.keeper.Params.Get(f.ctx)
		require.NoError(t, err)
		mutate(&params.Service)
		require.NoError(t, params.Validate())
		require.NoError(t, f.keeper.Params.Set(f.ctx, params))
	}
	return f, operator.Address
}

func quotaRows(t *testing.T, f *fixture) []types.WorkerQuotaState {
	t.Helper()
	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	return exported.WorkerQuotas
}

func wholeBondQuota(p *types.ServiceParamsV1) { p.WorkerQuotaPeriodValueBpsOfBond = 10_000 }

// Without a coefficient nothing is reserved, nothing is refused and no row is
// written, whatever the order value.
func TestWorkerQuotaIsOffWithoutACoefficient(t *testing.T) {
	f, operator := quotaFixture(t, nil)
	require.NoError(t, f.keeper.ReserveWorkerQuota(f.ctx, operator, 3, 1<<60, quotaTestHeight))
	require.NoError(t, f.keeper.SettleWorkerQuota(f.ctx, operator, 3, 1<<60, 5, quotaTestHeight))
	require.Empty(t, quotaRows(t, f))
}

func TestWorkerQuotaReserveAndSettle(t *testing.T) {
	f, operator := quotaFixture(t, func(p *types.ServiceParamsV1) {
		p.WorkerQuotaPeriodValueBpsOfBond = types.RecommendedWorkerQuotaPeriodValueBpsOfBond
	})
	quota := types.WorkerPeriodQuota(7*testServiceBondMinInitial, types.RecommendedWorkerQuotaPeriodValueBpsOfBond)
	require.Equal(t, 7*testServiceBondMinInitial*1428/10_000, quota)

	// Up to the quota is accepted, one unit more is refused, and the refusal
	// leaves the row as it was.
	require.NoError(t, f.keeper.ReserveWorkerQuota(f.ctx, operator, 5, quota-10, quotaTestHeight))
	require.ErrorIs(t, f.keeper.ReserveWorkerQuota(f.ctx, operator, 5, 11, quotaTestHeight+1), types.ErrWorkerQuotaExceeded)
	require.Equal(t, []types.WorkerQuotaState{{OperatorAddress: operator, Period: 5, ReservedValue: quota - 10, UpdatedHeight: quotaTestHeight}}, quotaRows(t, f))
	require.NoError(t, f.keeper.ReserveWorkerQuota(f.ctx, operator, 5, 10, quotaTestHeight+1))
	require.ErrorIs(t, f.keeper.ReserveWorkerQuota(f.ctx, operator, 5, 1, quotaTestHeight+1), types.ErrWorkerQuotaExceeded)

	// Another period has its own quota.
	require.NoError(t, f.keeper.ReserveWorkerQuota(f.ctx, operator, 6, quota, quotaTestHeight+2))

	// Finality releases the reservation down to the actual value, which frees
	// the difference for new orders in the same period.
	require.NoError(t, f.keeper.SettleWorkerQuota(f.ctx, operator, 5, quota-10, 40, quotaTestHeight+3))
	rows := quotaRows(t, f)
	require.Equal(t, types.WorkerQuotaState{OperatorAddress: operator, Period: 5, ReservedValue: 10, SettledValue: 40, UpdatedHeight: quotaTestHeight + 3}, rows[0])
	require.NoError(t, f.keeper.ReserveWorkerQuota(f.ctx, operator, 5, quota-50, quotaTestHeight+4))
	require.ErrorIs(t, f.keeper.ReserveWorkerQuota(f.ctx, operator, 5, 1, quotaTestHeight+4), types.ErrWorkerQuotaExceeded)
}

// Releasing never fails and never goes below zero: a task recorded before the
// quota existed has no reservation, and an actual value is capped at the
// reserved one.
func TestWorkerQuotaSettleIsTotal(t *testing.T) {
	f, operator := quotaFixture(t, wholeBondQuota)
	require.NoError(t, f.keeper.SettleWorkerQuota(f.ctx, operator, 9, 500, 100, quotaTestHeight))
	require.Empty(t, quotaRows(t, f), "no row is created by a release")

	require.NoError(t, f.keeper.ReserveWorkerQuota(f.ctx, operator, 9, 100, quotaTestHeight))
	require.NoError(t, f.keeper.SettleWorkerQuota(f.ctx, operator, 9, 500, 900, quotaTestHeight+1))
	require.Equal(t, []types.WorkerQuotaState{{OperatorAddress: operator, Period: 9, SettledValue: 500, UpdatedHeight: quotaTestHeight + 1}}, quotaRows(t, f))
}

// A slash lowers the quota for new orders. Orders already recorded keep their
// reservation and settle normally; nothing is refused or faulted for them.
func TestWorkerQuotaAfterBondLossKeepsRecordedOrders(t *testing.T) {
	f, operator := quotaFixture(t, wholeBondQuota)
	require.NoError(t, f.keeper.ReserveWorkerQuota(f.ctx, operator, 2, 6*testServiceBondMinInitial, quotaTestHeight))

	bond, err := f.keeper.GetServiceBondState(f.ctx, operator)
	require.NoError(t, err)
	bond.ActiveBond, bond.EffectiveActiveBond = testServiceBondMinInitial, testServiceBondMinInitial
	require.NoError(t, f.keeper.WriteServiceBondValue(f.ctx, operator, bond))

	require.ErrorIs(t, f.keeper.ReserveWorkerQuota(f.ctx, operator, 2, 1, quotaTestHeight+1), types.ErrWorkerQuotaExceeded)
	require.NoError(t, f.keeper.SettleWorkerQuota(f.ctx, operator, 2, 6*testServiceBondMinInitial, 6*testServiceBondMinInitial, quotaTestHeight+2))
	require.Equal(t, 6*testServiceBondMinInitial, quotaRows(t, f)[0].SettledValue)
	faults, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Empty(t, faults.RoleFaults)
}

func TestWorkerQuotaRowsAreDeletedAfterTheRetention(t *testing.T) {
	f, operator := quotaFixture(t, wholeBondQuota)
	for period := uint64(1); period <= 3; period++ {
		require.NoError(t, f.keeper.ReserveWorkerQuota(f.ctx, operator, period, 10, quotaTestHeight))
	}
	// Period 2 is final; periods 1 and 3 still hold a reservation.
	require.NoError(t, f.keeper.SettleWorkerQuota(f.ctx, operator, 2, 10, 10, quotaTestHeight))
	require.NoError(t, f.keeper.ReserveWorkerQuota(f.ctx, operator, 3+types.WorkerQuotaRetentionPeriods, 10, quotaTestHeight))
	periods := []uint64{}
	for _, row := range quotaRows(t, f) {
		periods = append(periods, row.Period)
	}
	require.Equal(t, []uint64{1, 3, 3 + types.WorkerQuotaRetentionPeriods}, periods, "only the settled row behind the retention is deleted")
}

func TestWorkerQuotaQuery(t *testing.T) {
	f, operator := quotaFixture(t, wholeBondQuota)
	bondValue := 7 * testServiceBondMinInitial
	require.NoError(t, f.keeper.ReserveWorkerQuota(f.ctx, operator, 2, 300, quotaTestHeight))
	require.NoError(t, f.keeper.ReserveWorkerQuota(f.ctx, operator, 1, 50, quotaTestHeight))
	require.NoError(t, f.keeper.SettleWorkerQuota(f.ctx, operator, 2, 100, 60, quotaTestHeight))
	ctx := heightCtx(f, 250)

	// No settlement period known: the stored usage is reported, nothing is
	// enforced.
	response, err := keeper.NewQueryServerImpl(f.keeper).WorkerQuota(ctx, &types.QueryWorkerQuotaRequest{OperatorAddress: operator, Period: 2})
	require.NoError(t, err)
	require.Equal(t, &types.QueryWorkerQuotaResponse{Period: 2, ReservedValue: 200, SettledValue: 60}, response)

	// Period 0 in the request is the current period: 250 / 100 = 2.
	query := keeper.NewQueryServerImpl(f.keeper.WithWorkerSettlementView(settlementViewStub{periodBlocks: 100}))
	response, err = query.WorkerQuota(ctx, &types.QueryWorkerQuotaRequest{OperatorAddress: operator})
	require.NoError(t, err)
	require.Equal(t, &types.QueryWorkerQuotaResponse{Period: 2, Quota: bondValue, ReservedValue: 200, SettledValue: 60, Remaining: bondValue - 260, Enforced: true}, response)
	response, err = query.WorkerQuota(ctx, &types.QueryWorkerQuotaRequest{OperatorAddress: operator, Period: 1})
	require.NoError(t, err)
	require.Equal(t, &types.QueryWorkerQuotaResponse{Period: 1, Quota: bondValue, ReservedValue: 50, Remaining: bondValue - 50, Enforced: true}, response)
	response, err = query.WorkerQuota(ctx, &types.QueryWorkerQuotaRequest{OperatorAddress: operator, Period: 7})
	require.NoError(t, err)
	require.Equal(t, bondValue, response.Remaining, "a period without a row has the whole quota left")

	// The pause flags mirror the bond.
	bond, err := f.keeper.GetServiceBondState(f.ctx, operator)
	require.NoError(t, err)
	bond.SetPauseReason(uint32(1), true)
	require.NoError(t, f.keeper.WriteServiceBondValue(f.ctx, operator, bond))
	response, err = query.WorkerQuota(ctx, &types.QueryWorkerQuotaRequest{OperatorAddress: operator})
	require.NoError(t, err)
	require.True(t, response.DispatchPaused)
	require.Equal(t, uint32(1), response.PauseReasons)

	_, err = query.WorkerQuota(ctx, &types.QueryWorkerQuotaRequest{OperatorAddress: hubAddress(t, 0x77)})
	require.ErrorContains(t, err, "service bond not found")
	_, err = query.WorkerQuota(ctx, &types.QueryWorkerQuotaRequest{OperatorAddress: "not-an-address"})
	require.Error(t, err)
	_, err = query.WorkerQuota(ctx, nil)
	require.Error(t, err)
}

// With the coefficient off the query answers as before the quota existed.
func TestWorkerQuotaQueryWithoutACoefficient(t *testing.T) {
	f, operator := quotaFixture(t, nil)
	query := keeper.NewQueryServerImpl(f.keeper.WithWorkerSettlementView(settlementViewStub{periodBlocks: 100}))
	response, err := query.WorkerQuota(heightCtx(f, 250), &types.QueryWorkerQuotaRequest{OperatorAddress: operator})
	require.NoError(t, err)
	require.Equal(t, &types.QueryWorkerQuotaResponse{Period: 2}, response)
}

func unstakeForTest(t *testing.T, f *fixture, operator string, amount, height uint64) *types.MsgBeginServiceUnstakeResponse {
	t.Helper()
	response, err := keeper.NewMsgServerImpl(f.keeper).BeginServiceUnstake(heightCtx(f, height), &types.MsgBeginServiceUnstake{
		OperatorAddress: operator, Amount: shared.NewAmount(amount),
	})
	require.NoError(t, err)
	return response
}

func withdrawForTest(f *fixture, view types.WorkerSettlementView, operator string, id []byte, height uint64) error {
	_, err := keeper.NewMsgServerImpl(f.keeper, types.MsgServerDependencies{WorkerSettlementView: view}).WithdrawServiceUnbonded(heightCtx(f, height), &types.MsgWithdrawServiceUnbonded{
		OperatorAddress: operator,
		Locator:         types.UnbondingLocatorV1{Locator: &types.UnbondingLocatorV1_ById{ById: &shared.ByIDV1{Id: id}}},
	})
	return err
}

// An unbonding request leaves the pause flags alone unless the chain turned
// stop_dispatch_on_unbond_request on.
func TestUnbondingRequestDoesNotPauseByDefault(t *testing.T) {
	f, operator := quotaFixture(t, nil)
	unstakeForTest(t, f, operator, testServiceBondMinInitial, quotaTestHeight)
	bond, err := f.keeper.GetServiceBondState(f.ctx, operator)
	require.NoError(t, err)
	require.False(t, bond.DispatchPaused)
	require.Zero(t, bond.PauseReasons)
}

func TestUnbondingRequestPausesDispatchUntilTheLastEntryEnds(t *testing.T) {
	f, operator := quotaFixture(t, func(p *types.ServiceParamsV1) { p.StopDispatchOnUnbondRequest = true })
	first := unstakeForTest(t, f, operator, testServiceBondMinInitial, quotaTestHeight)
	second := unstakeForTest(t, f, operator, testServiceBondMinInitial, quotaTestHeight+1)
	paused := func() (bool, uint32) {
		bond, err := f.keeper.GetServiceBondState(f.ctx, operator)
		require.NoError(t, err)
		return bond.DispatchPaused, bond.PauseReasons
	}
	on, reasons := paused()
	require.True(t, on)
	require.Equal(t, types.WorkerPauseReasonUnbonding, reasons)

	// Another reason set meanwhile survives the end of the unbonding.
	bond, err := f.keeper.GetServiceBondState(f.ctx, operator)
	require.NoError(t, err)
	bond.SetPauseReason(uint32(1), true)
	require.NoError(t, f.keeper.WriteServiceBondValue(f.ctx, operator, bond))

	require.NoError(t, withdrawForTest(f, nil, operator, first.UnbondingId, second.MatureHeight))
	on, reasons = paused()
	require.True(t, on, "one entry is still outstanding")
	require.Equal(t, types.WorkerPauseReasonUnbonding|uint32(1), reasons)

	require.NoError(t, withdrawForTest(f, nil, operator, second.UnbondingId, second.MatureHeight))
	on, reasons = paused()
	require.True(t, on)
	require.Equal(t, uint32(1), reasons, "only the unbonding reason is cleared")
}

func TestUnbondingWithdrawalWaitsForOpenObligationsWhenRequired(t *testing.T) {
	for _, row := range []struct {
		name     string
		required bool
		view     types.WorkerSettlementView
		refused  bool
	}{
		{"rule off, obligations open", false, settlementViewStub{open: map[string]bool{"*": true}}, false},
		{"rule on, no view bound", true, nil, false},
		{"rule on, nothing open", true, settlementViewStub{}, false},
		{"rule on, obligations open", true, settlementViewStub{open: map[string]bool{"*": true}}, true},
	} {
		f, operator := quotaFixture(t, func(p *types.ServiceParamsV1) { p.UnbondRequiresNoOpenTasks = row.required })
		if stub, ok := row.view.(settlementViewStub); ok && stub.open != nil {
			stub.open = map[string]bool{operator: true}
			row.view = stub
		}
		unbonding := unstakeForTest(t, f, operator, testServiceBondMinInitial, quotaTestHeight)
		err := withdrawForTest(f, row.view, operator, unbonding.UnbondingId, unbonding.MatureHeight)
		if row.refused {
			require.ErrorIs(t, err, types.ErrPendingResponsibility, row.name)
			// The entry stays, and is withdrawn once the obligations are gone.
			require.NoError(t, withdrawForTest(f, settlementViewStub{}, operator, unbonding.UnbondingId, unbonding.MatureHeight), row.name)
		} else {
			require.NoError(t, err, row.name)
		}
	}
}

// Quota rows and a paused bond survive export and import unchanged.
func TestWorkerQuotaGenesisRoundTrip(t *testing.T) {
	f, operator := quotaFixture(t, func(p *types.ServiceParamsV1) {
		wholeBondQuota(p)
		p.StopDispatchOnUnbondRequest = true
	})
	require.NoError(t, f.keeper.ReserveWorkerQuota(f.ctx, operator, 4, 70, quotaTestHeight))
	require.NoError(t, f.keeper.ReserveWorkerQuota(f.ctx, operator, 5, 30, quotaTestHeight))
	require.NoError(t, f.keeper.SettleWorkerQuota(f.ctx, operator, 4, 20, 15, quotaTestHeight+1))
	unstakeForTest(t, f, operator, testServiceBondMinInitial, quotaTestHeight+1)

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	require.Len(t, exported.WorkerQuotas, 2)

	restarted := initFixture(t)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	reexported, err := restarted.keeper.ExportGenesis(restarted.ctx)
	require.NoError(t, err)
	require.Equal(t, exported.WorkerQuotas, reexported.WorkerQuotas)
	bond, err := restarted.keeper.GetServiceBondState(restarted.ctx, operator)
	require.NoError(t, err)
	require.True(t, bond.DispatchPaused)

	// The imported reservation is released like any other.
	require.NoError(t, restarted.keeper.SettleWorkerQuota(restarted.ctx, operator, 5, 30, 30, quotaTestHeight+2))
	require.ErrorIs(t, restarted.keeper.ReserveWorkerQuota(sdk.UnwrapSDKContext(restarted.ctx), operator, 5, 1<<62, quotaTestHeight+2), types.ErrWorkerQuotaExceeded)
}
