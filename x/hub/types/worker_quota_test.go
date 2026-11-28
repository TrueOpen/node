package types

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestWorkerPeriodQuotaRows(t *testing.T) {
	for _, row := range []struct {
		name string
		bond uint64
		bps  uint32
		want uint64
	}{
		{"no coefficient", 7_000, 0, 0},
		{"one seventh, rounded down", 7_000, RecommendedWorkerQuotaPeriodValueBpsOfBond, 999},
		{"whole bond", 7_000, 10_000, 7_000},
		{"above the bond", 7_000, 20_000, 14_000},
		{"zero bond", 0, 1_428, 0},
		{"large bond does not wrap", math.MaxUint64, 10_000, math.MaxUint64},
		{"result beyond uint64 saturates", math.MaxUint64, MaxWorkerQuotaPeriodValueBpsOfBond, math.MaxUint64},
	} {
		require.Equal(t, row.want, WorkerPeriodQuota(row.bond, row.bps), row.name)
	}
}

func TestWorkerQuotaRemainingRows(t *testing.T) {
	for _, row := range []struct {
		name                           string
		quota, reserved, settled, want uint64
	}{
		{"nothing used", 100, 0, 0, 100},
		{"reserved only", 100, 30, 0, 70},
		{"reserved and settled", 100, 30, 20, 50},
		{"exactly used", 100, 60, 40, 0},
		{"over used after a slash lowered the quota", 100, 90, 40, 0},
		{"usage sum overflows", 100, math.MaxUint64, 2, 0},
		{"no quota", 0, 0, 0, 0},
	} {
		require.Equal(t, row.want, WorkerQuotaRemaining(row.quota, row.reserved, row.settled), row.name)
	}
}

func TestWorkerSettlementPeriodRows(t *testing.T) {
	_, ok := WorkerSettlementPeriod(500, 0)
	require.False(t, ok, "no period length means no period")
	for _, row := range []struct{ height, blocks, want uint64 }{{0, 100, 0}, {99, 100, 0}, {100, 100, 1}, {250, 100, 2}} {
		period, ok := WorkerSettlementPeriod(row.height, row.blocks)
		require.True(t, ok)
		require.Equal(t, row.want, period)
	}
}

func TestWorkerBondFloorByTier(t *testing.T) {
	terms := WorkerQuotaTerms{BondFloorByTier: []WorkerBondFloorV1{
		{ResourceTier: 2, MinBond: shared.NewAmount(500)}, {ResourceTier: 4, MinBond: shared.NewAmount(9_000)},
	}}
	require.Equal(t, uint64(0), terms.BondFloor(1), "a tier without an entry has no floor")
	require.Equal(t, uint64(500), terms.BondFloor(2))
	require.Equal(t, uint64(9_000), terms.BondFloor(4))
	require.Equal(t, uint64(0), WorkerQuotaTerms{}.BondFloor(2))
}

func TestPauseReasonKeepsTheFlagInStep(t *testing.T) {
	var bond ServiceBondState
	bond.SetPauseReason(WorkerPauseReasonUnbonding, true)
	require.True(t, bond.DispatchPaused)
	bond.SetPauseReason(uint32(1), true)
	bond.SetPauseReason(WorkerPauseReasonUnbonding, false)
	require.True(t, bond.DispatchPaused, "another reason still holds the pause")
	require.Equal(t, uint32(1), bond.PauseReasons)
	bond.SetPauseReason(uint32(1), false)
	require.False(t, bond.DispatchPaused)
	require.Zero(t, bond.PauseReasons)

	require.NoError(t, validateDispatchPause(ServiceBondState{}))
	require.NoError(t, validateDispatchPause(ServiceBondState{DispatchPaused: true, PauseReasons: 7}))
	require.Error(t, validateDispatchPause(ServiceBondState{DispatchPaused: true}))
	require.Error(t, validateDispatchPause(ServiceBondState{PauseReasons: 2}))
	require.Error(t, validateDispatchPause(ServiceBondState{DispatchPaused: true, PauseReasons: 8}))
}

// The Worker bond rules are off on a chain that does not set them, and the
// default parameter body is the one earlier chains committed to.
func TestWorkerBondParametersDefaultToOff(t *testing.T) {
	service := DefaultHubParams().Service
	require.Zero(t, service.WorkerQuotaPeriodValueBpsOfBond)
	require.Zero(t, service.WorkerBondFloorRevenuePeriods)
	require.False(t, service.StopDispatchOnUnbondRequest)
	require.False(t, service.UnbondRequiresNoOpenTasks)
	require.Empty(t, service.WorkerBondFloorByTier)
	require.Equal(t, uint64(302_400), service.ServiceUnbondingPeriodBlocks)
}

func TestWorkerBondParameterValidationRows(t *testing.T) {
	floor := func(tier uint32, amount uint64) WorkerBondFloorV1 {
		return WorkerBondFloorV1{ResourceTier: tier, MinBond: shared.NewAmount(amount)}
	}
	for _, row := range []struct {
		name   string
		mutate func(*ServiceParamsV1)
		valid  bool
	}{
		{"recommended values", func(p *ServiceParamsV1) {
			p.WorkerQuotaPeriodValueBpsOfBond = RecommendedWorkerQuotaPeriodValueBpsOfBond
			p.WorkerBondFloorRevenuePeriods = RecommendedWorkerBondFloorRevenuePeriods
			p.StopDispatchOnUnbondRequest, p.UnbondRequiresNoOpenTasks = true, true
			p.WorkerBondFloorByTier = []WorkerBondFloorV1{floor(1, 10), floor(3, 30), floor(5, 50)}
		}, true},
		{"coefficient at the bound", func(p *ServiceParamsV1) { p.WorkerQuotaPeriodValueBpsOfBond = MaxWorkerQuotaPeriodValueBpsOfBond }, true},
		{"coefficient above the bound", func(p *ServiceParamsV1) { p.WorkerQuotaPeriodValueBpsOfBond = MaxWorkerQuotaPeriodValueBpsOfBond + 1 }, false},
		{"tier zero", func(p *ServiceParamsV1) { p.WorkerBondFloorByTier = []WorkerBondFloorV1{floor(0, 10)} }, false},
		{"tier above the range", func(p *ServiceParamsV1) { p.WorkerBondFloorByTier = []WorkerBondFloorV1{floor(6, 10)} }, false},
		{"tier listed twice", func(p *ServiceParamsV1) { p.WorkerBondFloorByTier = []WorkerBondFloorV1{floor(2, 10), floor(2, 20)} }, false},
		{"tiers out of order", func(p *ServiceParamsV1) { p.WorkerBondFloorByTier = []WorkerBondFloorV1{floor(3, 10), floor(2, 20)} }, false},
		{"zero floor", func(p *ServiceParamsV1) { p.WorkerBondFloorByTier = []WorkerBondFloorV1{floor(2, 0)} }, false},
		{"malformed floor", func(p *ServiceParamsV1) {
			p.WorkerBondFloorByTier = []WorkerBondFloorV1{{ResourceTier: 2, MinBond: shared.Amount{}}}
		}, false},
	} {
		params := DefaultHubParams()
		row.mutate(&params.Service)
		if row.valid {
			require.NoError(t, params.Validate(), row.name)
		} else {
			require.Error(t, params.Validate(), row.name)
		}
	}
}

func TestWorkerQuotaGenesisRows(t *testing.T) {
	require.NoError(t, ValidateWorkerQuotaGenesis(nil))
	rows := []WorkerQuotaState{{OperatorAddress: "a", Period: 1, ReservedValue: 5}, {OperatorAddress: "a", Period: 2}, {OperatorAddress: "b", Period: 1}}
	require.NoError(t, ValidateWorkerQuotaGenesis(rows))
	require.Error(t, ValidateWorkerQuotaGenesis(append(rows, WorkerQuotaState{OperatorAddress: "a", Period: 2})), "duplicate row")
	require.Error(t, ValidateWorkerQuotaGenesis([]WorkerQuotaState{{OperatorAddress: " a", Period: 1}}), "operator not canonical")
	require.Error(t, ValidateWorkerQuotaGenesis([]WorkerQuotaState{{Period: 1}}), "operator missing")
	require.Error(t, ValidateWorkerQuotaGenesis([]WorkerQuotaState{{OperatorAddress: "a", ReservedValue: math.MaxUint64, SettledValue: 1}}), "usage overflow")
}
