package keeper_test

import (
	"bytes"
	"testing"

	"cosmossdk.io/collections"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestCompensationAggregateRebuildAndCorruption(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	for i, amount := range []uint64{10, 20} {
		builder := hubIdentity(t, byte(235+i))
		registerBuilderIdentityForTest(t, f, builder, 10)
		state := types.BuilderCompensationAccountState{BuilderAddress: builder.Address, Balance: shared.NewAmount(amount), TotalFunded: shared.NewAmount(amount), TotalPaid: shared.NewAmount(0), TotalPenalized: shared.NewAmount(0)}
		require.NoError(t, f.keeper.WriteBuilderCompensationAccount(f.ctx, state))
		require.NoError(t, f.keeper.WriteBuilderCompensationAccount(f.ctx, state), "replacement must not double count")
	}
	f.bank.seedModule(types.BuilderCompensationModuleName, 30)
	require.NoError(t, f.keeper.EnsureBuilderCompensationInvariant(f.ctx))
	require.NoError(t, f.keeper.EnsureBuilderCompensationLedgerInvariant(f.ctx))
	genesis, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	restarted := initFixture(t)
	restarted.bank.seedModule(types.BuilderCompensationModuleName, 30)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *genesis))
	require.NoError(t, restarted.keeper.EnsureBuilderCompensationLedgerInvariant(restarted.ctx))
	require.NoError(t, f.keeper.BuilderCompensationBalance.Set(f.ctx, 29))
	require.Error(t, f.keeper.EnsureBuilderCompensationInvariant(f.ctx))
	require.ErrorContains(t, f.keeper.EnsureBuilderCompensationLedgerInvariant(f.ctx), "aggregate")
}

func TestDispatchFaultPruneIndexRejectsOrphan(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	require.NoError(t, f.keeper.EnsureDispatchFaultPruneInvariant(f.ctx))
	key := collections.Join3(uint64(40), hubIdentity(t, 238).Address, bytes.Repeat([]byte{4}, 32))
	require.NoError(t, f.keeper.DispatchFaultPruneIndex.Set(f.ctx, key))
	require.ErrorContains(t, f.keeper.EnsureDispatchFaultPruneInvariant(f.ctx), "no primary")
}
