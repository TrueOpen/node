package keeper

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/task/types"
)

// A handraise window that closes short of selected_verifier_count is something a
// live network does on its own — a two-cortex devnet where one node takes the
// Worker duty leaves exactly one Verifier. The task specification 04 §5 calls that
// a dispatch
// failure. Treating it as ErrInvariantBroken aborted FinalizeBlock and stopped
// consensus, so these tests pin the boundary: short = task failure, over the
// frozen cap = still an invariant.

func shortenVerifierUnion(t *testing.T, f *internalFixture, taskKey types.TaskKey) uint32 {
	t.Helper()
	stageKey := types.NewTaskStageKey(taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY)
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	require.Greater(t, params.Weights.SelectedVerifierCount, uint32(1),
		"the fixture cannot express a short window if one handraise already suffices")
	union, err := f.keeper.TaskStageHandraiseUnion.Get(f.ctx, stageKey)
	require.NoError(t, err)
	union.UnionCount = params.Weights.SelectedVerifierCount - 1
	require.NoError(t, f.keeper.TaskStageHandraiseUnion.Set(f.ctx, stageKey, union))
	return union.UnionCount
}

func TestShortVerifierWindowIsATaskFailureNotAnInvariantBreach(t *testing.T) {
	f, taskKey, window, _, _ := verifierFinalizeFixture(t)
	shortenVerifierUnion(t, f, taskKey)

	_, _, _, err := f.keeper.finalizeVerifierLegalSetAtHeight(f.ctx, taskKey, window, 20, 3)
	require.ErrorIs(t, err, ErrInsufficientVerifierHandraises)
	require.NotErrorIs(t, err, types.ErrInvariantBroken,
		"a short window must never be able to abort FinalizeBlock")
}

func TestShortVerifierWindowRetiresItsCloseKeyAndKeepsTheChainProducing(t *testing.T) {
	f, taskKey, window, _, _ := verifierFinalizeFixture(t)
	shortenVerifierUnion(t, f, taskKey)

	// The frozen verify-open assignment deadline is the terminal key that fails
	// the task as TASK_FAILURE_CLASS_INSUFFICIENT_VERIFIER and refunds it.
	require.NoError(t, f.keeper.VerifyOpenDeadlineIndex.Set(f.ctx,
		types.NewDeadlineIndexKey(window.AssignmentDeadlineHeight, taskKey)))
	closeKey := types.NewVerifyRoundIndexKey(window.HandraiseCloseHeight, taskKey, types.VerifyRoundV1)
	require.NoError(t, f.keeper.VerifierHandraiseCloseIndex.Set(f.ctx, closeKey))

	usage, err := f.keeper.processVerifierHandraiseCloseIndexWithBudget(
		f.ctx, window.HandraiseCloseHeight, 1, maxDeadlineSweepBytesPerBlockV1)
	require.NoError(t, err, "the close sweep must not return an error to EndBlock")
	require.Equal(t, uint64(1), usage.visited)

	has, err := f.keeper.VerifierHandraiseCloseIndex.Has(f.ctx, closeKey)
	require.NoError(t, err)
	require.False(t, has,
		"a retired close key must not stay pending and hold the head of the height-ordered sweep")
}

// Without a later verify-open deadline nothing would ever retire the task, so
// that really is a store inconsistency and must stay an invariant.
func TestShortVerifierWindowWithoutAFallbackDeadlineStaysAnInvariant(t *testing.T) {
	f, taskKey, window, _, _ := verifierFinalizeFixture(t)
	shortenVerifierUnion(t, f, taskKey)
	closeKey := types.NewVerifyRoundIndexKey(window.HandraiseCloseHeight, taskKey, types.VerifyRoundV1)
	require.NoError(t, f.keeper.VerifierHandraiseCloseIndex.Set(f.ctx, closeKey))

	_, err := f.keeper.processVerifierHandraiseCloseIndexWithBudget(
		f.ctx, window.HandraiseCloseHeight, 1, maxDeadlineSweepBytesPerBlockV1)
	require.ErrorIs(t, err, types.ErrInvariantBroken)
}

// The shared executor reports a short window as "no work done" rather than as a
// caller fault, so a public caller does not see a network-shaped condition as
// its own error.
func TestRunVerifierRoundReportsAShortWindowAsNoWork(t *testing.T) {
	f, taskKey, window, _, _ := verifierFinalizeFixture(t)
	shortenVerifierUnion(t, f, taskKey)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(window.HandraiseCloseHeight)))

	result, err := f.keeper.RunVerifierRound(f.ctx, window.TaskId, types.VerifyRoundV1, window.HandraiseCloseHeight, 3)
	require.NoError(t, err)
	require.False(t, result.LegalSetFinalized)
	require.False(t, result.AssignmentCreated)

	hasCursor, err := f.keeper.TaskCandidateFinalizeCursor.Has(f.ctx,
		types.NewTaskStageKey(taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY))
	require.NoError(t, err)
	require.False(t, hasCursor, "a short window must not leave a half-built legal set behind")
}

// The upper bound is the other half of the boundary: the handraise path caps
// entrants at max_candidate_union_members_per_stage, so a stored union above it
// contradicts the code that wrote it.
func TestOversizedVerifierUnionStaysAnInvariant(t *testing.T) {
	f, taskKey, window, _, _ := verifierFinalizeFixture(t)
	stageKey := types.NewTaskStageKey(taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY)
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	union, err := f.keeper.TaskStageHandraiseUnion.Get(f.ctx, stageKey)
	require.NoError(t, err)
	union.UnionCount = params.Proposals.MaxCandidateUnionMembersPerStage + 1
	require.NoError(t, f.keeper.TaskStageHandraiseUnion.Set(f.ctx, stageKey, union))

	_, _, _, err = f.keeper.finalizeVerifierLegalSetAtHeight(f.ctx, taskKey, window, 20, 3)
	require.ErrorIs(t, err, types.ErrInvariantBroken)
	require.NotErrorIs(t, err, ErrInsufficientVerifierHandraises)
}
