package keeper_test

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

type fixedRoleFaultConsumerGate struct {
	closed bool
	err    error
	task   []byte
	digest []byte
}

func (g *fixedRoleFaultConsumerGate) RoleFaultConsumersClosed(_ context.Context, taskID, evidenceDigest []byte) (bool, error) {
	g.task = append([]byte(nil), taskID...)
	g.digest = append([]byte(nil), evidenceDigest...)
	return g.closed, g.err
}

func writeRoleFaultForPrune(t *testing.T, f *fixture, operator string, recordedHeight uint64, marker string) (shared.Hash32Key, types.RoleFaultState, uint64) {
	t.Helper()
	taskID := hubHash("role-fault-prune-task-" + marker)
	evidenceDigest := hubHash("role-fault-prune-evidence-" + marker)
	taskBytes, err := hex.DecodeString(taskID)
	require.NoError(t, err)
	evidenceBytes, err := hex.DecodeString(evidenceDigest)
	require.NoError(t, err)
	seedRoleFaultForTest(t, f, operator, shared.DutyVerifier, taskBytes, evidenceBytes, recordedHeight, "prune-"+marker)
	iter, err := f.keeper.RoleFault.Iterate(f.ctx, nil)
	require.NoError(t, err)
	defer iter.Close()
	var (
		faultID shared.Hash32Key
		state   types.RoleFaultState
	)
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		require.NoError(t, err)
		projected, err := f.keeper.ProjectRoleFaultStore(entry.Value)
		require.NoError(t, err)
		if hex.EncodeToString(projected.TaskId) == taskID {
			faultID, state = entry.Key, projected
			break
		}
	}
	require.NotEmpty(t, faultID)
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	return faultID, state, recordedHeight + params.Service.RecordRetentionBlocks
}

func TestRoleFaultWriterSchedulesAndTerminalGatePrunes(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	operator := sdk.AccAddress(hubHashBytes("role-fault-prune-operator")[:20]).String()
	faultID, fault, due := writeRoleFaultForPrune(t, f, operator, 9, "one")

	has, err := f.keeper.RoleFaultPruneIndex.Has(f.ctx, types.NewRoleFaultPruneKey(due, faultID))
	require.NoError(t, err)
	require.True(t, has)

	gate := &fixedRoleFaultConsumerGate{}
	f.keeper = f.keeper.WithRuntimeDependencies(nil, nil, gate)
	visited, err := f.keeper.ProcessRoleFaultPrunes(f.ctx, due, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	require.Equal(t, fault.TaskId, gate.task)
	require.Equal(t, fault.EvidenceDigest, gate.digest)
	_, err = f.keeper.ReadRoleFaultValue(f.ctx, faultID)
	require.NoError(t, err)
	has, err = f.keeper.RoleFaultPruneIndex.Has(f.ctx, types.NewRoleFaultPruneKey(due+1, faultID))
	require.NoError(t, err)
	require.True(t, has, "a blocked terminal consumer must move the row strictly forward")

	gate.closed = true
	visited, err = f.keeper.ProcessRoleFaultPrunes(f.ctx, due+1, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	_, err = f.keeper.ReadRoleFaultValue(f.ctx, faultID)
	require.ErrorIs(t, err, collections.ErrNotFound)
	has, err = f.keeper.RoleFaultPruneIndex.Has(f.ctx, types.NewRoleFaultPruneKey(due+1, faultID))
	require.NoError(t, err)
	require.False(t, has)
}

func TestRoleFaultPruneStaleAndPoisonRowsConsumeBudget(t *testing.T) {
	t.Run("stale", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
		f.keeper = f.keeper.WithRuntimeDependencies(nil, nil, &fixedRoleFaultConsumerGate{closed: true})
		first := types.NewRoleFaultPruneKey(1, hubHashBytes("missing-role-fault-a"))
		second := types.NewRoleFaultPruneKey(1, hubHashBytes("missing-role-fault-b"))
		require.NoError(t, f.keeper.RoleFaultPruneIndex.Set(f.ctx, first))
		require.NoError(t, f.keeper.RoleFaultPruneIndex.Set(f.ctx, second))

		visited, err := f.keeper.ProcessRoleFaultPrunes(f.ctx, 1, 1)
		require.NoError(t, err)
		require.Equal(t, uint64(1), visited)
		iter, err := f.keeper.RoleFaultPruneIndex.Iterate(f.ctx, nil)
		require.NoError(t, err)
		defer iter.Close()
		require.True(t, iter.Valid(), "one stale row must remain after a one-item budget")
	})

	t.Run("poison", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
		operator := sdk.AccAddress(hubHashBytes("role-fault-poison-operator")[:20]).String()
		faultID, _, due := writeRoleFaultForPrune(t, f, operator, 10, "poison")
		require.NoError(t, f.keeper.RoleFaultPruneIndex.Set(f.ctx, types.NewRoleFaultPruneKey(due-1, faultID)))
		f.keeper = f.keeper.WithRuntimeDependencies(nil, nil, &fixedRoleFaultConsumerGate{closed: true})

		visited, err := f.keeper.ProcessRoleFaultPrunes(f.ctx, due-1, 1)
		require.Equal(t, uint64(1), visited)
		require.ErrorContains(t, err, "precedes frozen retention")
		has, hasErr := f.keeper.RoleFaultPruneIndex.Has(f.ctx, types.NewRoleFaultPruneKey(due-1, faultID))
		require.NoError(t, hasErr)
		require.True(t, has, "failed poison processing must not partially mutate state")
	})
}

func TestRoleFaultPruneIndexRebuildSurvivesRestart(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	identity := hubIdentity(t, 91)
	registerCortexNodeIdentityForTest(t, f, identity.Address, identity, testServiceBondMinInitial, 10, 0)
	faultID, _, due := writeRoleFaultForPrune(t, f, identity.Address, 20, "restart")
	f.keeper = f.keeper.WithRuntimeDependencies(nil, nil, &fixedRoleFaultConsumerGate{})
	visited, err := f.keeper.ProcessRoleFaultPrunes(f.ctx, due, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	has, err := f.keeper.RoleFaultPruneIndex.Has(f.ctx, types.NewRoleFaultPruneKey(due+1, faultID))
	require.NoError(t, err)
	require.True(t, has)

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Len(t, exported.RoleFaults, 1)
	storedFault, err := f.keeper.RoleFault.Get(f.ctx, faultID)
	require.NoError(t, err)
	operatorBytes, err := sdk.AccAddressFromBech32(exported.RoleFaults[0].OperatorAddress)
	require.NoError(t, err)
	require.Equal(t, []byte(operatorBytes), storedFault.OperatorAddress)

	restarted := initFixture(t)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	has, err = restarted.keeper.RoleFaultPruneIndex.Has(restarted.ctx, types.NewRoleFaultPruneKey(due, faultID))
	require.NoError(t, err)
	require.True(t, has, "restart must rebuild the exact derived index from the primary")
	has, err = restarted.keeper.RoleFaultPruneIndex.Has(restarted.ctx, types.NewRoleFaultPruneKey(due+1, faultID))
	require.NoError(t, err)
	require.False(t, has, "derived retry indexes are not exported as independent state")
	_, err = restarted.keeper.ReadRoleFaultValue(restarted.ctx, faultID)
	require.NoError(t, err)
}

func TestRoleFaultPruneIndexExportRejectsEitherMissingDirection(t *testing.T) {
	t.Run("primary without index", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
		operator := sdk.AccAddress(hubHashBytes("role-fault-missing-index-operator")[:20]).String()
		faultID, _, due := writeRoleFaultForPrune(t, f, operator, 40, "missing-index")
		require.NoError(t, f.keeper.RoleFaultPruneIndex.Remove(f.ctx, types.NewRoleFaultPruneKey(due, faultID)))

		_, err := f.keeper.ExportGenesis(f.ctx)
		require.ErrorContains(t, err, "has no prune index")
	})

	t.Run("index without primary", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
		faultID := hubHashBytes("role-fault-missing-primary")
		require.NoError(t, f.keeper.RoleFaultPruneIndex.Set(f.ctx, types.NewRoleFaultPruneKey(41, faultID)))

		_, err := f.keeper.ExportGenesis(f.ctx)
		require.ErrorContains(t, err, "has no primary")
	})
}

func TestRoleFaultPruneSharesEndBlockVisitedCap(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	params.QueryEvent.MaxEndblockVisitedItemsTotal = 1
	params.Service.MaxRoleFaultPruneItemsPerBlock = 1
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))
	f.keeper = f.keeper.WithRuntimeDependencies(nil, nil, &fixedRoleFaultConsumerGate{closed: true})
	for _, marker := range []string{"a", "b"} {
		key := types.NewRoleFaultPruneKey(1, hubHashBytes("role-fault-endblock-stale-"+marker))
		require.NoError(t, f.keeper.RoleFaultPruneIndex.Set(f.ctx, key))
	}
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(1))

	require.NoError(t, f.keeper.EndBlocker(f.ctx))
	budget, err := f.keeper.GetEndBlockBudget(f.ctx, 1)
	require.NoError(t, err)
	require.Zero(t, budget.RemainingItems)
	rows, err := f.keeper.RoleFaultPruneIndex.Iterate(f.ctx, nil)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Valid(), "one stale row must remain after the shared one-item cap")
}

func TestRoleFaultPruneGateErrorsAreAtomicAndCharged(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	operator := sdk.AccAddress(hubHashBytes("role-fault-error-operator")[:20]).String()
	faultID, _, due := writeRoleFaultForPrune(t, f, operator, 30, "error")
	f.keeper = f.keeper.WithRuntimeDependencies(nil, nil, &fixedRoleFaultConsumerGate{err: errors.New("gate failure")})

	visited, err := f.keeper.ProcessRoleFaultPrunes(f.ctx, due, 1)
	require.Equal(t, uint64(1), visited)
	require.ErrorContains(t, err, "gate failure")
	_, err = f.keeper.ReadRoleFaultValue(f.ctx, faultID)
	require.NoError(t, err)
}
