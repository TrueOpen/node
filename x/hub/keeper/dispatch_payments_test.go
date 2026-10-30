package keeper_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestDispatchFaultCompensatesBeforeIndependentPenalty(t *testing.T) {
	for _, test := range []struct {
		name                   string
		balance, paid, penalty uint64
	}{
		{"funded", 100, 60, 30}, {"penalty shortfall", 70, 60, 10},
		{"compensation shortfall", 40, 40, 0}, {"empty", 0, 0, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := initFixture(t)
			require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
			builder := hubIdentity(t, 241)
			registerBuilderIdentityForTest(t, f, builder, 10)
			worker := hubIdentity(t, 242).Address
			f.bank.seedModule(types.BuilderCompensationModuleName, test.balance)
			require.NoError(t, f.keeper.WriteBuilderCompensationAccount(f.ctx, types.BuilderCompensationAccountState{BuilderAddress: builder.Address, Balance: shared.NewAmount(test.balance), TotalFunded: shared.NewAmount(test.balance), TotalPaid: shared.NewAmount(0), TotalPenalized: shared.NewAmount(0)}))
			request := types.DispatchFaultRequest{SessionID: bytes.Repeat([]byte{1}, 32), Penalty: 30, Compensation: 60, RetentionBlocks: 100, Fault: types.DispatchFaultState{
				BuilderAddress: builder.Address, FaultId: bytes.Repeat([]byte{2}, 32), Kind: shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_OMISSION,
				TaskId: bytes.Repeat([]byte{3}, 32), DispatchRecordHash: bytes.Repeat([]byte{4}, 32), ChallengedTicketHashOrZero32: make([]byte, 32),
				PenaltyAmount: shared.NewAmount(0), UnfilledAmount: shared.NewAmount(0), CompensatedAmount: shared.NewAmount(0), FaultHeight: 20,
				XCompensatedWorker: &types.DispatchFaultState_CompensatedWorker{CompensatedWorker: worker},
			}}
			row, applied, err := f.keeper.ApplyDispatchFault(f.ctx, request)
			require.NoError(t, err)
			require.True(t, applied)
			require.Equal(t, shared.NewAmount(test.paid), row.CompensatedAmount)
			require.Equal(t, shared.NewAmount(test.penalty), row.PenaltyAmount)
			require.Equal(t, shared.NewAmount(30-test.penalty), row.UnfilledAmount)
			require.Equal(t, test.paid, f.bank.moduleBalance(types.RewardsModuleName))
			require.Equal(t, test.penalty, f.bank.moduleBalance(types.TreasuryModuleName))
			account, err := f.keeper.GetBuilderCompensationAccount(f.ctx, builder.Address)
			require.NoError(t, err)
			require.Equal(t, shared.NewAmount(test.paid), account.TotalPaid)
			require.Equal(t, shared.NewAmount(test.penalty), account.TotalPenalized)
			require.NoError(t, f.keeper.EnsureBuilderCompensationInvariant(f.ctx))
			require.NoError(t, f.keeper.EnsureRewardsEarningsInvariant(f.ctx))
			require.NoError(t, f.keeper.EnsureTreasurySpendReceiptInvariant(f.ctx))
			replay, applied, err := f.keeper.ApplyDispatchFault(f.ctx, request)
			require.NoError(t, err)
			require.False(t, applied)
			require.Equal(t, row, replay)
			require.Equal(t, test.paid, f.bank.moduleBalance(types.RewardsModuleName))
			require.NoError(t, f.keeper.EnsureTreasurySpendReceiptInvariant(f.ctx))
		})
	}
}
