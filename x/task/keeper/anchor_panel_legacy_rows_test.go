package keeper

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/task/types"
)

// requireNoPanelRows asserts that every Verifier-panel collection of the
// Keeper is empty: no store key carries the prefix of any of them.
func (f *legacyHandraiseFixture) requireNoPanelRows(t *testing.T) {
	t.Helper()
	prefixes := map[string][]byte{
		"AnchorPanelSelection":      types.AnchorPanelSelectionKey.Bytes(),
		"AnchorPanelDueIndex":       types.AnchorPanelDueIndexKey.Bytes(),
		"AnchorPanelDueHeight":      types.AnchorPanelDueHeightKey.Bytes(),
		"AnchorPanelMemberIndex":    types.AnchorPanelMemberIndexKey.Bytes(),
		"AnchorUnderReviewIndex":    types.AnchorUnderReviewIndexKey.Bytes(),
		"AnchorPanelLastScanHeight": types.AnchorPanelLastScanHeightKey.Bytes(),
	}
	for _, row := range f.storeRows(t) {
		for name, prefix := range prefixes {
			require.Falsef(t, bytes.HasPrefix(row[0], prefix), "a task without a panel audit must leave no %s row, found %x", name, row[0])
		}
	}
}

// Neither an accepted hand-raise proposal of a Verifier round nor a refused
// one for a later round creates a Verifier-panel row, and the task never has
// a panel working row for the handler to route on.
func TestLegacyHandraisesLeaveNoPanelRow(t *testing.T) {
	roundOne := newLegacyHandraiseFixture(t)
	for _, verifyRound := range []uint32{types.ChallengeVerifyRoundV1, 3, 4} {
		_, _, err := roundOne.submit(t, verifyRound)
		require.ErrorIs(t, err, types.ErrInvalidOpenVerify)
		roundOne.requireNoPanelRows(t)
	}
	response, _, err := roundOne.submit(t, types.VerifyRoundV1)
	require.NoError(t, err)
	require.Equal(t, roundOne.window.WindowSize, response.AddedMemberCount)
	roundOne.requireNoPanelRows(t)

	challenge := newLegacyChallengeHandraiseFixture(t)
	_, found, err := challenge.keeper.loadAnchorPanelSelection(challenge.ctx, challenge.taskKey)
	require.NoError(t, err)
	require.False(t, found, "a challenged task has no panel working row")
	response, _, err = challenge.submit(t, types.ChallengeVerifyRoundV1)
	require.NoError(t, err)
	require.Equal(t, challenge.window.WindowSize, response.AddedMemberCount)
	challenge.requireNoPanelRows(t)
}
