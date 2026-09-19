package keeper

import (
	"bytes"
	"testing"

	"github.com/cosmos/cosmos-sdk/types/bech32"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// settlementTestAddress builds a canonical Bech32 address whose every byte is
// `value`, so a test can choose exactly where two addresses diverge.
func settlementTestAddress(t *testing.T, value byte) string {
	t.Helper()
	raw := make([]byte, 20)
	for i := range raw {
		raw[i] = value
	}
	encoded, err := bech32.ConvertAndEncode("trueopen", raw)
	require.NoError(t, err)
	return encoded
}

// Hub rejects an earnings vector that is not strictly ascending in decoded
// address bytes, and Bech32 string order is not that order: the charset
// "qpzry9x8gf2tvdw0s3jn54khce6mua7l" puts 5-bit group 0 at 'q' (0x71) and group
// 15 at '0' (0x30). The two addresses below therefore sort one way as bytes and
// the other way as strings, which is exactly the case a string sort gets wrong.
func TestSettlementEarningsCreditsSortByAddressBytesNotBech32Text(t *testing.T) {
	low := settlementTestAddress(t, 0x00)  // first group 0  -> 'q...'
	high := settlementTestAddress(t, 0x78) // first group 15 -> '0...'
	require.Greater(t, low, high, "the fixture must invert between byte order and string order")

	inputs := settlementInputs{Assignment: types.TaskAssignmentState{WinnerWorker: low}}
	plan := types.SettlementPlanV1{
		WorkerNet: shared.NewAmount(700),
		VerifierPayouts: []types.VerifierPayoutV1{
			{OperatorAddress: high, SelectedVerifierIndex: 0, Net: shared.NewAmount(120)},
		},
	}

	credits, err := settlementEarningsCredits(inputs, plan)
	require.NoError(t, err)
	require.Len(t, credits, 2)
	require.Equal(t, low, credits[0].Beneficiary, "the byte-smaller address must come first")
	require.Equal(t, high, credits[1].Beneficiary)

	first, err := types.CanonicalOperatorAddressBytes("first", credits[0].Beneficiary)
	require.NoError(t, err)
	second, err := types.CanonicalOperatorAddressBytes("second", credits[1].Beneficiary)
	require.NoError(t, err)
	require.Negative(t, bytes.Compare(first, second))
}

// Everything the plan turns into a Hub claim has to be the same number the
// escrow transfer moves into the Rewards module, or the claim ledger and the
// module balance drift apart. The two are derived independently on purpose.
func TestSettlementClaimableTotalMatchesTheCreditVector(t *testing.T) {
	worker := settlementTestAddress(t, 0x11)
	verifierA := settlementTestAddress(t, 0x22)
	verifierB := settlementTestAddress(t, 0x33)
	payer := settlementTestAddress(t, 0x44)

	inputs := settlementInputs{Assignment: types.TaskAssignmentState{WinnerWorker: worker}}
	plan := types.SettlementPlanV1{
		WorkerNet: shared.NewAmount(950),
		VerifierPayouts: []types.VerifierPayoutV1{
			{OperatorAddress: verifierA, SelectedVerifierIndex: 0, Net: shared.NewAmount(40)},
			{OperatorAddress: verifierB, SelectedVerifierIndex: 1, Net: shared.NewAmount(40)},
		},
		GasReimbursements: []types.TaskGasReimbursementV1{
			{FeePayer: payer, ReimbursedAmount: shared.NewAmount(7)},
		},
	}

	total, err := settlementClaimableTotal(plan)
	require.NoError(t, err)
	require.Equal(t, uint64(950+40+40+7), total)

	credits, err := settlementEarningsCredits(inputs, plan)
	require.NoError(t, err)
	credited := uint64(0)
	for _, credit := range credits {
		amount, err := shared.ParseAmount(credit.Amount)
		require.NoError(t, err)
		credited += amount
	}
	require.Equal(t, total, credited, "escrow transfer and claim ledger must move the same amount")
}

// One operator that is both the winner Worker and a paid Verifier is one
// beneficiary with one summed credit, not two rows Hub would reject as
// non-strictly-ascending.
func TestSettlementEarningsCreditsMergeOneOperatorHoldingTwoRoles(t *testing.T) {
	both := settlementTestAddress(t, 0x55)
	inputs := settlementInputs{Assignment: types.TaskAssignmentState{WinnerWorker: both}}
	plan := types.SettlementPlanV1{
		WorkerNet: shared.NewAmount(600),
		VerifierPayouts: []types.VerifierPayoutV1{
			{OperatorAddress: both, SelectedVerifierIndex: 2, Net: shared.NewAmount(25)},
		},
	}

	credits, err := settlementEarningsCredits(inputs, plan)
	require.NoError(t, err)
	require.Len(t, credits, 1)
	amount, err := shared.ParseAmount(credits[0].Amount)
	require.NoError(t, err)
	require.Equal(t, uint64(625), amount)

	total, err := settlementClaimableTotal(plan)
	require.NoError(t, err)
	require.Equal(t, amount, total)
}

// A verdict that pays nobody still has to produce an empty vector rather than a
// zero-amount credit, which Hub rejects outright.
func TestSettlementEarningsCreditsDropZeroAmounts(t *testing.T) {
	inputs := settlementInputs{Assignment: types.TaskAssignmentState{WinnerWorker: settlementTestAddress(t, 0x66)}}
	plan := types.SettlementPlanV1{
		WorkerNet: shared.NewAmount(0),
		VerifierPayouts: []types.VerifierPayoutV1{
			{OperatorAddress: settlementTestAddress(t, 0x77), SelectedVerifierIndex: 0, Net: shared.NewAmount(0)},
		},
	}

	credits, err := settlementEarningsCredits(inputs, plan)
	require.NoError(t, err)
	require.Empty(t, credits)

	total, err := settlementClaimableTotal(plan)
	require.NoError(t, err)
	require.Zero(t, total)
}

func TestSettlementBuilderScheduleUsesFrozenRanksThenPermissionlessFallback(t *testing.T) {
	tests := []struct {
		height         uint64
		wantIndex      uint64
		permissionless bool
	}{
		{height: 100, wantIndex: 0},
		{height: 101, wantIndex: 0},
		{height: 102, wantIndex: 0},
		{height: 103, wantIndex: 1},
		{height: 106, wantIndex: 2},
		{height: 107, wantIndex: 2, permissionless: true},
	}
	for _, test := range tests {
		index, permissionless, err := settlementBuilderSchedule(3, 2, 100, test.height)
		require.NoError(t, err)
		require.Equal(t, test.wantIndex, index)
		require.Equal(t, test.permissionless, permissionless)
	}
}

func TestSettlementPaidRolesExcludeRoundTwoDisqualifications(t *testing.T) {
	worker := settlementTestAddress(t, 0x12)
	verifierA := settlementTestAddress(t, 0x23)
	verifierB := settlementTestAddress(t, 0x34)
	inputs := settlementInputs{
		Assignment:      types.TaskAssignmentState{WinnerWorker: worker},
		EffectiveResult: roundCloseResult{Verdict: types.TaskVerdict_TASK_VERDICT_PASS},
		Round1Cluster: []types.ConsensusClusterMemberV1{
			{VerifierOperatorAddress: verifierA},
			{VerifierOperatorAddress: verifierB},
		},
		DisqualifiedOperators: []string{worker, verifierA},
	}

	roles, err := (Keeper{}).derivePaidRoles(inputs)
	require.NoError(t, err)
	require.Equal(t, []types.PaidRoleV1{{
		Duty: shared.DutyVerifier, OperatorAddress: verifierB,
	}}, roles)
}

func TestFaultSummaryCommitsAppliedHubReceiptFields(t *testing.T) {
	taskID := bytes.Repeat([]byte{0x71}, types.Hash32Len)
	empty, err := taskFaultSummaryHash("trueopen-test", taskID, nil)
	require.NoError(t, err)
	require.Len(t, empty, types.Hash32Len)
	require.NotEqual(t, make([]byte, types.Hash32Len), empty)

	fault := taskRoleFaultRecord{
		faultID:         bytes.Repeat([]byte{0x72}, types.Hash32Len),
		operatorAddress: settlementTestAddress(t, 0x73), duty: shared.DutyVerifier,
		faultClass:           hubtypes.FaultKind_FAULT_KIND_TIMEOUT,
		classificationSource: shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_SETTLEMENT,
		evidenceDigest:       bytes.Repeat([]byte{0x74}, types.Hash32Len),
		jailDelta:            1, status: hubtypes.RoleFaultStatus_ROLE_FAULT_STATUS_CONFIRMED,
	}
	withFault, err := taskFaultSummaryHash("trueopen-test", taskID, []taskRoleFaultRecord{fault})
	require.NoError(t, err)
	require.NotEqual(t, empty, withFault)
	fault.jailDelta++
	changed, err := taskFaultSummaryHash("trueopen-test", taskID, []taskRoleFaultRecord{fault})
	require.NoError(t, err)
	require.NotEqual(t, withFault, changed)
}
