package keeper_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func collectRoleFaultByTaskKeys(t *testing.T, f *fixture) []types.RoleFaultByTaskKey {
	t.Helper()
	iter, err := f.keeper.RoleFaultByTaskIndex.Iterate(f.ctx, nil)
	require.NoError(t, err)
	defer iter.Close()
	keys := make([]types.RoleFaultByTaskKey, 0, 4)
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		require.NoError(t, err)
		keys = append(keys, key)
	}
	return keys
}

// TestRoleFaultByTaskIndexIsRebuiltFromExportedPrimaries covers the derived
// direction §6.6's fault_summary_hash depends on. RoleFaultState is keyed by
// fault_id alone, so before this index existed there was no bounded way to read
// one task's fault vector inside a settlement transition. The index is derived,
// so it is deliberately not on the genesis wire: the round trip below is what
// proves the rebuild reproduces it exactly rather than quietly losing rows.
func TestRoleFaultByTaskIndexIsRebuiltFromExportedPrimaries(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	first := hubIdentity(t, 71)
	second := hubIdentity(t, 72)
	registerCortexNodeIdentityForTest(t, f, first.Address, first, testServiceBondMinInitial, 10, 0)
	registerCortexNodeIdentityForTest(t, f, second.Address, second, testServiceBondMinInitial, 10, 0)

	taskOne := hubHashBytes("role-fault-by-task-one")
	taskTwo := hubHashBytes("role-fault-by-task-two")
	seedRoleFaultForTest(t, f, first.Address, shared.DutyVerifier, taskOne, hubHashBytes("by-task-ev-a"), 20, "by-task-a")
	seedRoleFaultForTest(t, f, second.Address, shared.DutyWorker, taskOne, hubHashBytes("by-task-ev-b"), 21, "by-task-b")
	seedRoleFaultForTest(t, f, first.Address, shared.DutyVerifier, taskTwo, hubHashBytes("by-task-ev-c"), 22, "by-task-c")

	before := collectRoleFaultByTaskKeys(t, f)
	require.Len(t, before, 3)

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	require.Len(t, exported.RoleFaults, 3)

	restarted := initFixture(t)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	require.Equal(t, before, collectRoleFaultByTaskKeys(t, restarted),
		"the rebuilt by-task index must be byte identical to the one that was exported from")

	// The bounded per-task read is the consumer that makes the index worth having,
	// and it comes back in fault_id ascending order because the key is
	// (task_id hex, fault_id hex) and lowercase hex order is raw byte order.
	faults, err := restarted.keeper.RoleFaultsForTask(restarted.ctx, taskOne)
	require.NoError(t, err)
	require.Len(t, faults, 2)
	require.Negative(t, bytes.Compare(faults[0].FaultId, faults[1].FaultId))
	for _, fault := range faults {
		require.Equal(t, taskOne, fault.TaskId)
	}
	faults, err = restarted.keeper.RoleFaultsForTask(restarted.ctx, taskTwo)
	require.NoError(t, err)
	require.Len(t, faults, 1)
	require.Equal(t, first.Address, faults[0].OperatorAddress)
	faults, err = restarted.keeper.RoleFaultsForTask(restarted.ctx, hubHashBytes("role-fault-by-task-absent"))
	require.NoError(t, err)
	require.Empty(t, faults)
}

// TestRoleFaultByTaskIndexDisagreementFailsExport pins both directions of the
// index invariant. A silent disagreement here would change a task's committed
// fault vector after a restart, so it has to stop the export rather than ride
// out on the wire.
func TestRoleFaultByTaskIndexDisagreementFailsExport(t *testing.T) {
	t.Run("index row without a primary", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
		identity := hubIdentity(t, 73)
		registerCortexNodeIdentityForTest(t, f, identity.Address, identity, testServiceBondMinInitial, 10, 0)
		taskID := hubHashBytes("role-fault-by-task-orphan")
		seedRoleFaultForTest(t, f, identity.Address, shared.DutyVerifier, taskID, hubHashBytes("orphan-ev"), 20, "orphan")
		require.NoError(t, f.keeper.RoleFaultByTaskIndex.Set(f.ctx, types.NewRoleFaultByTaskKey(
			taskID, hubHashBytes("role-fault-by-task-no-primary"),
		)))
		_, err := f.keeper.ExportGenesis(f.ctx)
		require.ErrorContains(t, err, "role fault by task index")
		require.ErrorContains(t, err, "has no primary")
	})

	t.Run("primary without an index row", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
		identity := hubIdentity(t, 74)
		registerCortexNodeIdentityForTest(t, f, identity.Address, identity, testServiceBondMinInitial, 10, 0)
		taskID := hubHashBytes("role-fault-by-task-unindexed")
		state := seedRoleFaultForTest(t, f, identity.Address, shared.DutyVerifier, taskID, hubHashBytes("unindexed-ev"), 20, "unindexed")
		require.NoError(t, f.keeper.RoleFaultByTaskIndex.Remove(f.ctx, types.NewRoleFaultByTaskKey(
			taskID, state.FaultId,
		)))
		_, err := f.keeper.ExportGenesis(f.ctx)
		require.ErrorContains(t, err, "has no by-task index")
	})

	t.Run("index row under the wrong task", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
		identity := hubIdentity(t, 75)
		registerCortexNodeIdentityForTest(t, f, identity.Address, identity, testServiceBondMinInitial, 10, 0)
		taskID := hubHashBytes("role-fault-by-task-mislabelled")
		state := seedRoleFaultForTest(t, f, identity.Address, shared.DutyVerifier, taskID, hubHashBytes("mislabelled-ev"), 20, "mislabelled")
		require.NoError(t, f.keeper.RoleFaultByTaskIndex.Remove(f.ctx, types.NewRoleFaultByTaskKey(
			taskID, state.FaultId,
		)))
		require.NoError(t, f.keeper.RoleFaultByTaskIndex.Set(f.ctx, types.NewRoleFaultByTaskKey(
			hubHashBytes("role-fault-by-task-other"), state.FaultId,
		)))
		_, err := f.keeper.ExportGenesis(f.ctx)
		require.ErrorContains(t, err, "disagrees with primary")
	})
}

// TestRoleFaultPruneDeletesTheByTaskIndexRow is the deletion half of the
// contract: §6.6 licenses dropping RoleFaultState and its SlashSummary after
// record_retention_blocks, and the derived index has to go with them or the next
// export fails on an orphan row.
func TestRoleFaultPruneDeletesTheByTaskIndexRow(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	identity := hubIdentity(t, 76)
	registerCortexNodeIdentityForTest(t, f, identity.Address, identity, testServiceBondMinInitial, 10, 0)
	faultID, state, due := writeRoleFaultForPrune(t, f, identity.Address, 20, "by-task-prune")
	indexKey := types.NewRoleFaultByTaskKey(state.TaskId, faultID)
	has, err := f.keeper.RoleFaultByTaskIndex.Has(f.ctx, indexKey)
	require.NoError(t, err)
	require.True(t, has)

	f.keeper = f.keeper.WithRuntimeDependencies(nil, nil, &fixedRoleFaultConsumerGate{closed: true})
	visited, err := f.keeper.ProcessRoleFaultPrunes(f.ctx, due, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)

	has, err = f.keeper.RoleFaultByTaskIndex.Has(f.ctx, indexKey)
	require.NoError(t, err)
	require.False(t, has, "the derived row must be deleted with its primary")
	faults, err := f.keeper.RoleFaultsForTask(f.ctx, state.TaskId)
	require.NoError(t, err)
	require.Empty(t, faults, "after retention the fault vector is only provable through fault_summary_hash")
	_, err = f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
}
