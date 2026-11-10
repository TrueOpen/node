package keeper

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func frozenParamsTask(t *testing.T, f *internalFixture, marker byte, phase types.TaskPhase) (types.TaskKey, []byte) {
	t.Helper()
	id := bytes.Repeat([]byte{marker}, types.Hash32Len)
	key := types.NewTaskKey(id)
	require.NoError(t, f.keeper.TaskCore.Set(f.ctx, key, types.TaskCoreState{TaskId: id, TaskPhase: phase}))
	return key, id
}

func TestTaskFrozenParamsFollowsTheVersionTheTaskBoundNotCurrentParams(t *testing.T) {
	f := initInternalFixture(t)
	server := NewQueryServerImpl(f.keeper)
	original, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)

	oldKey, oldID := frozenParamsTask(t, f, 0x31, types.TaskPhase_TASK_PHASE_INFER_PENDING)
	require.NoError(t, f.keeper.PinTaskParams(f.ctx, oldKey))
	updated := updateParams(t, f, func(p *types.TaskParamsV1) {
		bond, err := shared.ParseAmount(p.Challenge.ChallengeOpenBond)
		require.NoError(t, err)
		p.Challenge.ChallengeOpenBond = shared.NewAmount(bond + 1)
		p.Weights.WorkerStakeWeightPpm = 400_000
		p.Weights.WorkerPerformanceWeightPpm = 600_000
	})
	newKey, newID := frozenParamsTask(t, f, 0x32, types.TaskPhase_TASK_PHASE_INFER_PENDING)
	require.NoError(t, f.keeper.PinTaskParams(f.ctx, newKey))

	old, err := server.TaskFrozenParams(f.ctx, &types.QueryTaskFrozenParamsRequest{TaskId: oldID})
	require.NoError(t, err)
	require.Equal(t, uint64(0), old.ParamsVersion, "version 0 is a valid binding")
	require.Equal(t, original, old.Params, "an older Task never reads the current body")
	require.Equal(t, original.Challenge, old.Params.Challenge)

	fresh, err := server.TaskFrozenParams(f.ctx, &types.QueryTaskFrozenParamsRequest{TaskId: newID})
	require.NoError(t, err)
	require.Equal(t, uint64(1), fresh.ParamsVersion)
	require.Equal(t, updated, fresh.Params)

	current, err := server.Params(f.ctx, &types.QueryTaskParamsRequest{})
	require.NoError(t, err)
	require.Equal(t, updated, current.Params, "the module params query keeps returning the current body")
	require.NotEqual(t, current.Params, old.Params)
}

func TestTaskFrozenParamsServesATerminalTaskUntilItIsCompacted(t *testing.T) {
	f := initInternalFixture(t)
	server := NewQueryServerImpl(f.keeper)
	key, id := frozenParamsTask(t, f, 0x33, types.TaskPhase_TASK_PHASE_SETTLED)
	require.NoError(t, f.keeper.PinTaskParams(f.ctx, key))

	response, err := server.TaskFrozenParams(f.ctx, &types.QueryTaskFrozenParamsRequest{TaskId: id})
	require.NoError(t, err, "a terminal Task that is not compacted keeps its binding")
	require.Equal(t, uint64(0), response.ParamsVersion)

	require.NoError(t, f.keeper.UnpinTaskParams(f.ctx, key))
	require.NoError(t, f.keeper.TaskCore.Remove(f.ctx, key))
	_, err = server.TaskFrozenParams(f.ctx, &types.QueryTaskFrozenParamsRequest{TaskId: id})
	require.Equal(t, codes.NotFound, status.Code(err), "a compacted Task has no frozen parameters to read")
}

func TestTaskFrozenParamsRejectsUnknownAndMalformedSelectors(t *testing.T) {
	f := initInternalFixture(t)
	server := NewQueryServerImpl(f.keeper)

	_, err := server.TaskFrozenParams(f.ctx, nil)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	for _, id := range [][]byte{nil, bytes.Repeat([]byte{0x34}, types.Hash32Len-1), bytes.Repeat([]byte{0x34}, types.Hash32Len+1)} {
		_, err = server.TaskFrozenParams(f.ctx, &types.QueryTaskFrozenParamsRequest{TaskId: id})
		require.Equal(t, codes.InvalidArgument, status.Code(err), "len %d", len(id))
	}
	_, err = server.TaskFrozenParams(f.ctx, &types.QueryTaskFrozenParamsRequest{TaskId: bytes.Repeat([]byte{0x35}, types.Hash32Len)})
	require.Equal(t, codes.NotFound, status.Code(err))
}

// A live Task without its binding must not be answered with current parameters:
// the shared row helper would do exactly that, and an exact lock quote computed
// from them could disagree with the chain.
func TestTaskFrozenParamsReportsABrokenBindingInsteadOfCurrentParams(t *testing.T) {
	f := initInternalFixture(t)
	server := NewQueryServerImpl(f.keeper)
	_, id := frozenParamsTask(t, f, 0x36, types.TaskPhase_TASK_PHASE_INFER_PENDING)

	_, err := server.TaskFrozenParams(f.ctx, &types.QueryTaskFrozenParamsRequest{TaskId: id})
	require.Equal(t, codes.Internal, status.Code(err))
	require.Contains(t, err.Error(), "no parameter version binding")
}

func TestTaskFrozenParamsReportsAMissingHistoricalBody(t *testing.T) {
	f := initInternalFixture(t)
	server := NewQueryServerImpl(f.keeper)
	key, id := frozenParamsTask(t, f, 0x37, types.TaskPhase_TASK_PHASE_INFER_PENDING)
	require.NoError(t, f.keeper.PinTaskParams(f.ctx, key))
	updateParams(t, f, func(p *types.TaskParamsV1) {
		p.Weights.WorkerStakeWeightPpm = 400_000
		p.Weights.WorkerPerformanceWeightPpm = 600_000
	})
	require.NoError(t, f.keeper.ParamsHistory.Remove(f.ctx, 0))

	_, err := server.TaskFrozenParams(f.ctx, &types.QueryTaskFrozenParamsRequest{TaskId: id})
	require.Equal(t, codes.Internal, status.Code(err), "a missing body wraps not-found but is never a NotFound answer")
	require.Contains(t, err.Error(), "unavailable")
}
