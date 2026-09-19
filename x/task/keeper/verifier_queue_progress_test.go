package keeper

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/task/types"
)

func TestVerifierHandraiseCloseBlockedRowDoesNotStarveReadyRowAtLimitOne(t *testing.T) {
	f := initInternalFixture(t)
	blockedID := bytes.Repeat([]byte{0x94}, types.Hash32Len)
	readyID := bytes.Repeat([]byte{0x95}, types.Hash32Len)
	blockedKey := types.NewTaskKey(blockedID)
	readyKey := types.NewTaskKey(readyID)
	blockedWindow := verifierWindowForDeadlineTest(
		blockedID, types.VerifyRoundV1,
		types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_SOURCE_FROZEN,
	)
	readyWindow := verifierWindowForDeadlineTest(
		readyID, types.VerifyRoundV1,
		types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY,
	)
	// A []byte cannot be a Go map key. The map is purely internal to this test —
	// nothing hex-encoded is ever compared against it — so the raw key bytes are
	// carried through as a Go string and converted straight back on read.
	for storeKey, window := range map[string]types.VerifierCandidateWindowState{
		string(blockedKey): blockedWindow,
		string(readyKey):   readyWindow,
	} {
		taskKey := types.TaskKey(storeKey)
		require.NoError(t, f.keeper.VerifierCandidateWindow.Set(
			f.ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1), window,
		))
		require.NoError(t, f.keeper.VerifierHandraiseCloseIndex.Set(
			f.ctx, types.NewVerifyRoundIndexKey(window.HandraiseCloseHeight, taskKey, types.VerifyRoundV1),
		))
		require.NoError(t, f.keeper.VerifyOpenDeadlineIndex.Set(
			f.ctx, types.NewDeadlineIndexKey(window.AssignmentDeadlineHeight, taskKey),
		))
	}

	visited, err := f.keeper.processVerifierHandraiseCloseIndex(f.ctx, 20, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	hasBlocked, err := f.keeper.VerifierHandraiseCloseIndex.Has(
		f.ctx, types.NewVerifyRoundIndexKey(20, blockedKey, types.VerifyRoundV1),
	)
	require.NoError(t, err)
	require.False(t, hasBlocked)
	hasReady, err := f.keeper.VerifierHandraiseCloseIndex.Has(
		f.ctx, types.NewVerifyRoundIndexKey(20, readyKey, types.VerifyRoundV1),
	)
	require.NoError(t, err)
	require.True(t, hasReady)

	visited, err = f.keeper.processVerifierHandraiseCloseIndex(f.ctx, 20, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	hasReady, err = f.keeper.VerifierHandraiseCloseIndex.Has(
		f.ctx, types.NewVerifyRoundIndexKey(20, readyKey, types.VerifyRoundV1),
	)
	require.NoError(t, err)
	require.False(t, hasReady)

	for storeKey, window := range map[string]types.VerifierCandidateWindowState{
		string(blockedKey): blockedWindow,
		string(readyKey):   readyWindow,
	} {
		hasFallback, err := f.keeper.VerifyOpenDeadlineIndex.Has(
			f.ctx, types.NewDeadlineIndexKey(window.AssignmentDeadlineHeight, types.TaskKey(storeKey)),
		)
		require.NoError(t, err)
		require.True(t, hasFallback)
	}
}

func TestVerifierHandraiseCloseRejectsSameHeightFallback(t *testing.T) {
	f := initInternalFixture(t)
	taskID := bytes.Repeat([]byte{0x96}, types.Hash32Len)
	taskKey := types.NewTaskKey(taskID)
	window := verifierWindowForDeadlineTest(
		taskID, types.VerifyRoundV1,
		types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY,
	)
	window.AssignmentDeadlineHeight = window.HandraiseCloseHeight
	closeKey := types.NewVerifyRoundIndexKey(window.HandraiseCloseHeight, taskKey, types.VerifyRoundV1)
	require.NoError(t, f.keeper.VerifierCandidateWindow.Set(
		f.ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1), window,
	))
	require.NoError(t, f.keeper.VerifierHandraiseCloseIndex.Set(f.ctx, closeKey))
	require.NoError(t, f.keeper.VerifyOpenDeadlineIndex.Set(
		f.ctx, types.NewDeadlineIndexKey(window.AssignmentDeadlineHeight, taskKey),
	))

	visited, err := f.keeper.processVerifierHandraiseCloseIndex(f.ctx, window.HandraiseCloseHeight, 1)
	require.ErrorContains(t, err, "no later verify-open deadline")
	require.Equal(t, uint64(1), visited)
	hasClose, hasErr := f.keeper.VerifierHandraiseCloseIndex.Has(f.ctx, closeKey)
	require.NoError(t, hasErr)
	require.True(t, hasClose)
}
