package keeper_test

import (
	"testing"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func TestExpiredCandidateBodyRetentionCancelsAndRestoresSafely(t *testing.T) {
	f, _, request := seedJailAdmissionLiabilityFixture(t, 8)
	_, err := f.keeper.ReleaseCandidatePoolTaskRef(f.ctx, request.TaskID, request.CandidatePoolSnapshotID, 3)
	require.NoError(t, err)
	snapshot, err := f.keeper.CandidatePoolSnapshot.Get(f.ctx, request.CandidatePoolSnapshotID)
	require.NoError(t, err)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(snapshot.ExpiresHeight)))
	_, err = f.keeper.ProcessCandidatePoolExpiries(f.ctx, snapshot.ExpiresHeight, 100)
	require.NoError(t, err)
	due, err := f.keeper.CandidatePoolBodyPruneHeight.Get(f.ctx, snapshot.SnapshotId)
	require.NoError(t, err)
	require.Greater(t, due, snapshot.ExpiresHeight+120)
	lateTask := hubHashBytes("late-retained-pool-task")
	created, err := f.keeper.AcquireCandidatePoolTaskRef(f.ctx, lateTask, snapshot.SnapshotId, snapshot.ExpiresHeight+1)
	require.NoError(t, err)
	require.True(t, created)
	_, err = f.keeper.ProcessCandidateSnapshotPrunes(f.ctx, due, 100)
	require.NoError(t, err)
	_, _, err = f.keeper.ResolveCandidatePoolMember(f.ctx, snapshot.SnapshotId, request.Slot)
	require.NoError(t, err)
	_, err = f.keeper.ReleaseCandidatePoolTaskRef(f.ctx, lateTask, snapshot.SnapshotId, due+1)
	require.NoError(t, err)
	next, err := f.keeper.CandidatePoolBodyPruneHeight.Get(f.ctx, snapshot.SnapshotId)
	require.NoError(t, err)
	require.Greater(t, next, due+120)
	visited, err := f.keeper.ProcessCandidateSnapshotPrunes(f.ctx, next-1, 100)
	require.NoError(t, err)
	require.Zero(t, visited)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(next)))
	_, err = f.keeper.ProcessCandidateSnapshotPrunes(f.ctx, next, 1)
	require.NoError(t, err)
	_, err = f.keeper.AcquireCandidatePoolTaskRef(f.ctx, lateTask, snapshot.SnapshotId, next)
	require.ErrorIs(t, err, hubtypes.ErrCandidatePoolUnavailable, "a partial body cannot acquire a fresh consumer")
	require.NoError(t, f.keeper.EnsureCandidatePoolBodyInvariant(f.ctx))
	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NoError(t, exported.Validate())
	restored := initCandidatePoolFixture(t)
	restored.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(restored.ctx).WithBlockHeight(int64(next)))
	require.NoError(t, restored.keeper.InitGenesis(restored.ctx, *exported))
	_, err = restored.keeper.AcquireCandidatePoolTaskRef(restored.ctx, lateTask, snapshot.SnapshotId, next)
	require.ErrorIs(t, err, hubtypes.ErrCandidatePoolUnavailable)
	_, err = restored.keeper.ProcessCandidateSnapshotPrunes(restored.ctx, next, 100)
	require.NoError(t, err)
	pruned, err := restored.keeper.CandidatePoolSnapshot.Get(restored.ctx, snapshot.SnapshotId)
	require.NoError(t, err)
	require.Equal(t, hubtypes.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_PRUNED, pruned.Status)
}

func TestCandidateGenesisRejectsMissingWeightInputs(t *testing.T) {
	f, _, _ := seedJailAdmissionLiabilityFixture(t, 8)
	genesis, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NotEmpty(t, genesis.CandidatePoolMembers)
	genesis.CandidatePoolMembers[0].WeightInputs = nil
	require.ErrorContains(t, genesis.Validate(), "weight inputs are required")
	other := initCandidatePoolFixture(t)
	require.ErrorContains(t, other.keeper.InitGenesis(other.ctx, *genesis), "weight inputs are required")
}
