package keeper

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// valueEvidenceWindowFixture is valueEvidenceFixture with the chain at a height
// before the reveal deadline (60) the fixture froze, and the window length
// read back from the Task's parameters.
func valueEvidenceWindowFixture(t *testing.T, height int64) (*verificationFixture, uint64) {
	t.Helper()
	f, _, _ := valueEvidenceFixture(t, true)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(height))
	// closeRoundOnce re-derives the round from the Task budget and the selected
	// Verifiers' receipts; with no receipt a slot is skipped, so the round closes
	// without an explicit verdict and opens no challenge window.
	require.NoError(t, f.keeper.TaskBudget.Set(f.ctx, f.taskKey, types.TaskBudgetState{TaskId: f.taskID, MaxOutputTokens: 4}))
	// The facts hash of a closed round commits these three fields, which the
	// evidence fixture's bare round row leaves empty.
	roundKey := types.NewVerifyRoundKey(f.taskID, types.VerifyRoundV1)
	round, err := f.keeper.ReadVerificationRound(f.ctx, roundKey)
	require.NoError(t, err)
	round.InferReceiptRef, round.ProfileExecutionSnapshotHash, round.GenerationParamsDigest = bytes32(0x84), f.profileHash, f.genDigest
	require.NoError(t, f.keeper.WriteVerificationRound(f.ctx, roundKey, round))
	params, err := f.keeper.ParamsForTask(f.ctx, f.taskID)
	require.NoError(t, err)
	return f, params.Evidence.VerifierValueEvidenceWindowBlocks
}

func requireValueEvidenceClose(t *testing.T, f *verificationFixture, want uint64) types.TaskRoundSummaryState {
	t.Helper()
	summary, err := f.keeper.TaskRoundSummary.Get(f.ctx, f.taskID)
	require.NoError(t, err)
	require.Equal(t, want, summary.GetVerifierValueEvidenceCloseHeight())
	row, err := f.keeper.ValueEvidenceWindowCloseIndex.Has(f.ctx, types.NewDeadlineIndexKey(want, f.taskID))
	require.NoError(t, err)
	require.True(t, row, "the close index must hold the row for the close height")
	return summary
}

func requireNoValueEvidenceRow(t *testing.T, f *verificationFixture, height uint64) {
	t.Helper()
	row, err := f.keeper.ValueEvidenceWindowCloseIndex.Has(f.ctx, types.NewDeadlineIndexKey(height, f.taskID))
	require.NoError(t, err)
	require.False(t, row, "no close index row may remain at height %d", height)
}

// markRoundClosed records a round as closed at a height, as closeRoundOnce does,
// without running its verdict derivation.
func markRoundClosed(t *testing.T, f *verificationFixture, round uint32, height uint64) {
	t.Helper()
	key := types.NewVerifyRoundKey(f.taskID, round)
	row, err := f.keeper.ReadVerificationRound(f.ctx, key)
	require.NoError(t, err)
	row.XClosedHeight = &types.VerificationRoundState_ClosedHeight{ClosedHeight: height}
	require.NoError(t, f.keeper.WriteVerificationRound(f.ctx, key, row))
}

// Every selected Verifier revealing early closes round 1 before its reveal
// deadline. The window then runs from that close, not from the deadline nobody
// can use any more, and the Task is ready to settle that much sooner.
func TestValueEvidenceWindowStartsAtAnEarlyRoundClose(t *testing.T) {
	f, window := valueEvidenceWindowFixture(t, 20)
	const revealDeadline = 60
	requireValueEvidenceClose(t, f, revealDeadline+window)

	_, closed, err := f.keeper.closeRoundOnce(f.ctx, f.taskID, types.VerifyRoundV1, 20, 20,
		shared.RoundOutcomeV1_ROUND_OUTCOME_V1_UNSPECIFIED)
	require.NoError(t, err)
	require.True(t, closed)

	summary := requireValueEvidenceClose(t, f, 20+window)
	requireNoValueEvidenceRow(t, f, revealDeadline+window)
	require.Nil(t, summary.XVerifierValueEvidenceWindowClosedHeight)
	require.NoError(t, f.keeper.EnsureValueEvidenceInvariant(f.ctx))

	// Closing the same round again changes nothing.
	_, closed, err = f.keeper.closeRoundOnce(f.ctx, f.taskID, types.VerifyRoundV1, 20, 20,
		shared.RoundOutcomeV1_ROUND_OUTCOME_V1_UNSPECIFIED)
	require.NoError(t, err)
	require.False(t, closed)
	requireValueEvidenceClose(t, f, 20+window)

	// The window still takes evidence until its new close, and the Task finalizes
	// at that close instead of at the reveal deadline plus the window.
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(20 + window)))
	visited, err := f.keeper.processValueEvidenceWindowCloses(f.ctx, 20+window-1, 1)
	require.NoError(t, err)
	require.Zero(t, visited, "the window must not close a block early")
	visited, err = f.keeper.processValueEvidenceWindowCloses(f.ctx, 20+window, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	summary, err = f.keeper.TaskRoundSummary.Get(f.ctx, f.taskID)
	require.NoError(t, err)
	require.Equal(t, 20+window, summary.GetVerifierValueEvidenceWindowClosedHeight())
	require.Equal(t, 20+window, summary.GetRoundsClosedHeight(),
		"settlement readiness follows the shortened window, not the reveal deadline")
	require.NoError(t, f.keeper.EnsureValueEvidenceInvariant(f.ctx))
}

// A round that closes at or after its reveal deadline, which is what a missing
// reveal does, keeps the window frozen when it entered revealing.
func TestValueEvidenceWindowKeepsItsDeadlineWhenARoundClosesLate(t *testing.T) {
	for _, closedHeight := range []uint64{60, 61, 200} {
		f, window := valueEvidenceWindowFixture(t, int64(closedHeight))
		_, closed, err := f.keeper.closeRoundOnce(f.ctx, f.taskID, types.VerifyRoundV1, closedHeight, closedHeight,
			shared.RoundOutcomeV1_ROUND_OUTCOME_V1_UNSPECIFIED)
		require.NoError(t, err)
		require.True(t, closed)
		requireValueEvidenceClose(t, f, 60+window)
		require.NoError(t, f.keeper.EnsureValueEvidenceInvariant(f.ctx))
	}
}

func TestShortenValueEvidenceWindowLeavesUnshortenableWindowsAlone(t *testing.T) {
	t.Run("never entered revealing", func(t *testing.T) {
		f, window := valueEvidenceWindowFixture(t, 20)
		assignment := f.assignment
		assignment.RevealDeadlineHeight = 0
		require.NoError(t, f.keeper.WriteVerifierAssignment(f.ctx, types.NewVerifyRoundKey(f.taskID, 1), assignment))
		require.NoError(t, f.keeper.shortenValueEvidenceWindow(f.ctx, f.taskID, types.VerifyRoundV1, 20))
		requireValueEvidenceClose(t, f, 60+window)
	})
	t.Run("window already closed", func(t *testing.T) {
		f, window := valueEvidenceWindowFixture(t, 20)
		summary, err := f.keeper.TaskRoundSummary.Get(f.ctx, f.taskID)
		require.NoError(t, err)
		summary.XVerifierValueEvidenceWindowClosedHeight = &types.TaskRoundSummaryState_VerifierValueEvidenceWindowClosedHeight{
			VerifierValueEvidenceWindowClosedHeight: 60 + window,
		}
		require.NoError(t, f.keeper.TaskRoundSummary.Set(f.ctx, f.taskID, summary))
		require.NoError(t, f.keeper.shortenValueEvidenceWindow(f.ctx, f.taskID, types.VerifyRoundV1, 20))
		got, err := f.keeper.TaskRoundSummary.Get(f.ctx, f.taskID)
		require.NoError(t, err)
		require.Equal(t, 60+window, got.GetVerifierValueEvidenceCloseHeight())
	})
	t.Run("already shorter", func(t *testing.T) {
		f, window := valueEvidenceWindowFixture(t, 20)
		require.NoError(t, f.keeper.shortenValueEvidenceWindow(f.ctx, f.taskID, types.VerifyRoundV1, 20))
		requireValueEvidenceClose(t, f, 20+window)
		// A later close height cannot lengthen what an earlier one cut.
		require.NoError(t, f.keeper.shortenValueEvidenceWindow(f.ctx, f.taskID, types.VerifyRoundV1, 30))
		requireValueEvidenceClose(t, f, 20+window)
		requireNoValueEvidenceRow(t, f, 30+window)
	})
	t.Run("task without a summary", func(t *testing.T) {
		f, _ := valueEvidenceWindowFixture(t, 20)
		require.NoError(t, f.keeper.TaskRoundSummary.Remove(f.ctx, f.taskID))
		require.NoError(t, f.keeper.shortenValueEvidenceWindow(f.ctx, f.taskID, types.VerifyRoundV1, 20))
	})
}

// A challenge round extends the window as before and its own early close cuts it
// again; the window always follows the round that revealed last.
func TestValueEvidenceWindowFollowsTheLastRoundToReveal(t *testing.T) {
	f, window := valueEvidenceWindowFixture(t, 20)
	require.NoError(t, f.keeper.shortenValueEvidenceWindow(f.ctx, f.taskID, types.VerifyRoundV1, 20))
	markRoundClosed(t, f, types.VerifyRoundV1, 20)
	requireValueEvidenceClose(t, f, 20+window)
	require.NoError(t, f.keeper.EnsureValueEvidenceInvariant(f.ctx))

	// Round 2 enters revealing with a frozen reveal deadline of 400.
	second := f.assignment
	second.VerifyRound = types.ChallengeVerifyRoundV1
	second.RevealDeadlineHeight = 400
	require.NoError(t, f.keeper.WriteVerifierAssignment(f.ctx, types.NewVerifyRoundKey(f.taskID, 2), second))
	core, err := f.keeper.TaskCore.Get(f.ctx, f.taskID)
	require.NoError(t, err)
	require.NoError(t, f.keeper.WriteVerificationRound(f.ctx, types.NewVerifyRoundKey(f.taskID, 2), types.VerificationRoundState{
		TaskId: f.taskID, TaskHash: core.AcceptedTaskHash, VerifyRound: 2, RoundId: bytes32(0x91),
		RoundOpenHeight: 21, RoundCloseDeadlineHeight: 400, GeneratedTokenCount: 2,
	}))
	require.NoError(t, f.keeper.extendValueEvidenceWindow(f.ctx, f.taskID, 400))
	requireValueEvidenceClose(t, f, 400+window)
	requireNoValueEvidenceRow(t, f, 20+window)
	require.NoError(t, f.keeper.EnsureValueEvidenceInvariant(f.ctx))

	// Round 2 closing on its deadline changes nothing.
	require.NoError(t, f.keeper.shortenValueEvidenceWindow(f.ctx, f.taskID, types.ChallengeVerifyRoundV1, 400))
	requireValueEvidenceClose(t, f, 400+window)

	// Round 2 closing early cuts the window to its own close, which is still
	// after round 1's, so the later round decides.
	markRoundClosed(t, f, types.ChallengeVerifyRoundV1, 300)
	require.NoError(t, f.keeper.shortenValueEvidenceWindow(f.ctx, f.taskID, types.ChallengeVerifyRoundV1, 300))
	requireValueEvidenceClose(t, f, 300+window)
	requireNoValueEvidenceRow(t, f, 400+window)
	require.NoError(t, f.keeper.EnsureValueEvidenceInvariant(f.ctx))
}

// The invariant runs at Genesis import and as a crisis check, so a Task whose
// round closed before the window could be cut must stay valid, and a window that
// is neither shape must not.
func TestValueEvidenceInvariantAcceptsBothWindowShapes(t *testing.T) {
	f, window := valueEvidenceWindowFixture(t, 20)
	markRoundClosed(t, f, types.VerifyRoundV1, 20)

	// Written before the rule existed: closed early, window ran to the deadline.
	requireValueEvidenceClose(t, f, 60+window)
	require.NoError(t, f.keeper.EnsureValueEvidenceInvariant(f.ctx))

	// Written by the rule: window cut at the close.
	require.NoError(t, f.keeper.shortenValueEvidenceWindow(f.ctx, f.taskID, types.VerifyRoundV1, 20))
	requireValueEvidenceClose(t, f, 20+window)
	require.NoError(t, f.keeper.EnsureValueEvidenceInvariant(f.ctx))

	// Neither: one block off the cut.
	summary, err := f.keeper.TaskRoundSummary.Get(f.ctx, f.taskID)
	require.NoError(t, err)
	summary.XVerifierValueEvidenceCloseHeight = &types.TaskRoundSummaryState_VerifierValueEvidenceCloseHeight{VerifierValueEvidenceCloseHeight: 20 + window + 1}
	require.NoError(t, f.keeper.TaskRoundSummary.Set(f.ctx, f.taskID, summary))
	require.Error(t, f.keeper.EnsureValueEvidenceInvariant(f.ctx))
}

// A round that is still open has no close to cut at, so only the deadline shape
// is valid for it.
func TestValueEvidenceInvariantRejectsACutWindowOnAnOpenRound(t *testing.T) {
	f, window := valueEvidenceWindowFixture(t, 20)
	require.NoError(t, f.keeper.shortenValueEvidenceWindow(f.ctx, f.taskID, types.VerifyRoundV1, 20))
	requireValueEvidenceClose(t, f, 20+window)
	require.Error(t, f.keeper.EnsureValueEvidenceInvariant(f.ctx),
		"round 1 is not closed, so nothing justifies a window shorter than its deadline")
}
