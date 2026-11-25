package keeper

import (
	"testing"

	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	"github.com/TrueOpen/node/x/task/types"
)

// Two of three members pass the proof: the verdict is published, waits out the
// publication window and becomes final. The Worker is paid in full and nobody
// is slashed. Until then settlement is held.
func TestPanelPassIsPublishedThenFinalAndPaysTheWorker(t *testing.T) {
	f := newAnchorPanelFixture(t, 4_000, 6)
	audit := f.audit
	require.Equal(t, uint32(2), audit.PanelRound, "the first panel round is verify round 2")
	require.Len(t, audit.PanelRounds, 1)
	require.Equal(t, types.AnchorPanelRoundKindV1_ANCHOR_PANEL_ROUND_KIND_V1_INITIAL, audit.PanelRounds[0].Kind)
	require.Equal(t, audit.VerdictDeadlineHeight+1, f.settlementDeadline(t))

	round := f.assign(t)
	for _, member := range round.Members {
		require.NotEqual(t, f.worker, member, "the Worker never audits its own task")
	}
	voteAt := round.AssignmentHeight + 1
	response := f.castVotes(t, round, voteAt, votePass)
	require.False(t, response.RoundClosed)
	_, err := f.settleNow(t, voteAt)
	require.ErrorContains(t, err, "pending until")
	response = f.castVotes(t, round, voteAt, votePass, votePass)
	require.True(t, response.RoundClosed, "the second equal vote decides the round")
	require.Equal(t, votePass, response.RoundResult)

	audit = f.reload(t)
	require.Equal(t, votePass, audit.ProvisionalVerdict)
	require.Equal(t, voteAt, audit.ProvisionalHeight)
	require.Equal(t, voteAt+panelPublication, audit.PublicationEndHeight)
	require.Zero(t, audit.ResultHeight, "a published verdict is not final")
	_, err = f.settleNow(t, audit.PublicationEndHeight-1)
	require.ErrorContains(t, err, "pending until", "no settlement during the publication window")

	require.Equal(t, uint64(1), f.sweep(t, audit.PublicationEndHeight))
	audit = f.reload(t)
	require.Equal(t, votePass, audit.TaskVerdict)
	require.Equal(t, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_VERIFIER_PANEL, audit.ResultSource)
	require.Equal(t, audit.PublicationEndHeight, audit.ResultHeight)
	require.Equal(t, audit.ResultHeight, audit.FinalHeight)
	f.requireNoPanelWork(t)

	settlement, err := f.settleNow(t, audit.FinalHeight+1)
	require.NoError(t, err)
	f.requirePaidInFull(t, settlement)
	require.Empty(t, *f.faults)
}

// Two of three members reject: after publication the task fails and the Worker
// takes the reject slash, once.
func TestPanelRejectSlashesTheWorkerOnce(t *testing.T) {
	const bond = uint64(4_000)
	f := newAnchorPanelFixture(t, bond, 6)
	round := f.assign(t)
	voteAt := round.AssignmentHeight + 1
	f.castVotes(t, round, voteAt, voteReject, votePass, voteReject)
	audit := f.reload(t)
	require.Equal(t, voteReject, audit.ProvisionalVerdict)
	require.Empty(t, *f.faults, "nothing is slashed while the verdict is only published")
	f.sweep(t, audit.PublicationEndHeight)
	settlement, err := f.settleNow(t, audit.PublicationEndHeight+1)
	require.NoError(t, err)
	f.requireWorkerFaulted(t, settlement, hubtypes.FaultTypeAnchorReject, f.params.Verification.AnchorRejectSlashBps, bond)
	f.requireNoPanelWork(t)
}

// One vote each way is no conclusion: the chain draws a second panel from
// other operators, once, and that panel decides.
func TestPanelWithoutConclusionIsReassignedOnceToOtherMembers(t *testing.T) {
	f := newAnchorPanelFixture(t, 4_000, 7)
	first := f.assign(t)
	voteAt := first.AssignmentHeight + 1
	response := f.castVotes(t, first, voteAt, votePass, voteReject, voteUnavailable)
	require.True(t, response.RoundClosed, "three votes with no two equal close the round at once")
	require.Equal(t, noVerdict, response.RoundResult)
	audit := f.reload(t)
	require.Len(t, audit.PanelRounds, 1, "the re-assignment is opened by the sweep, not by the vote")
	require.Zero(t, audit.ResultHeight)

	require.Equal(t, uint64(1), f.sweep(t, voteAt))
	audit = f.reload(t)
	require.Len(t, audit.PanelRounds, 2)
	second := audit.PanelRounds[1]
	require.Equal(t, uint32(3), second.PanelRound)
	require.Equal(t, types.AnchorPanelRoundKindV1_ANCHOR_PANEL_ROUND_KIND_V1_RETRY, second.Kind)
	second = f.assign(t)
	for _, member := range second.Members {
		require.NotContains(t, first.Members, member, "a re-assigned panel has no member of the first one")
	}
	f.castVotes(t, second, second.AssignmentHeight+1, votePass, votePass)
	audit = f.reload(t)
	require.Equal(t, votePass, audit.ProvisionalVerdict)
	f.sweep(t, audit.PublicationEndHeight)
	settlement, err := f.settleNow(t, audit.PublicationEndHeight+1)
	require.NoError(t, err)
	f.requirePaidInFull(t, settlement)
	require.Empty(t, *f.faults)
}

// No member of either panel votes. Silence says nothing about the Worker: the
// audit ends without a verdict, the Worker is paid and not slashed.
func TestSilentPanelsNeverCostTheWorker(t *testing.T) {
	f := newAnchorPanelFixture(t, 4_000, 7)
	first := f.assign(t)
	require.Zero(t, f.sweep(t, first.VoteDeadlineHeight), "the round is open through its deadline height")
	// One visit closes the silent round, the next opens its re-assignment.
	require.Equal(t, uint64(2), f.sweep(t, first.VoteDeadlineHeight+1))
	audit := f.reload(t)
	require.Equal(t, noVerdict, audit.PanelRounds[0].Result)
	require.Len(t, audit.PanelRounds, 2)
	second := f.assign(t)
	require.Equal(t, uint64(1), f.sweep(t, second.VoteDeadlineHeight+1))
	audit = f.reload(t)
	require.Equal(t, noVerdict, audit.TaskVerdict)
	require.Equal(t, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_VERIFIER_PANEL, audit.ResultSource)
	require.Equal(t, types.AnchorTimeoutReasonV1_ANCHOR_TIMEOUT_REASON_V1_PANEL_INCONCLUSIVE, audit.TimeoutReason)
	require.NotZero(t, audit.ResultHeight)
	require.Zero(t, audit.ProvisionalHeight)
	f.requireNoPanelWork(t)
	settlement, err := f.settleNow(t, audit.ResultHeight+1)
	require.NoError(t, err)
	f.requirePaidInFull(t, settlement)
	require.Empty(t, *f.faults)
}

// Two members could not obtain the material the Worker recorded. Keeping it
// retrievable is the Worker's duty: the audit ends as material unavailable,
// which settles like a missed deadline (not paid, the smaller slash).
func TestTwoUnavailableVotesEndAsMaterialUnavailable(t *testing.T) {
	const bond = uint64(4_000)
	f := newAnchorPanelFixture(t, bond, 6)
	round := f.assign(t)
	voteAt := round.AssignmentHeight + 1
	response := f.castVotes(t, round, voteAt, voteUnavailable, voteUnavailable)
	require.True(t, response.RoundClosed)
	audit := f.reload(t)
	require.Equal(t, voteReject, audit.TaskVerdict)
	require.Equal(t, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_MATERIAL_UNAVAILABLE, audit.ResultSource)
	require.Equal(t, types.AnchorTimeoutReasonV1_ANCHOR_TIMEOUT_REASON_V1_PANEL_UNAVAILABLE, audit.TimeoutReason)
	require.Len(t, audit.PanelRounds, 1, "no re-assignment after an unavailable round")
	settlement, err := f.settleNow(t, voteAt+1)
	require.NoError(t, err)
	f.requireWorkerFaulted(t, settlement, hubtypes.FaultTypeAnchorTimeout, f.params.Verification.AnchorTimeoutSlashBps, bond)
	f.requireNoPanelWork(t)
}

// One unavailable vote next to a pass and a missing vote is no conclusion, not
// a statement about the Worker.
func TestOneUnavailableVoteIsNotAConclusion(t *testing.T) {
	f := newAnchorPanelFixture(t, 4_000, 7)
	round := f.assign(t)
	f.castVotes(t, round, round.AssignmentHeight+1, voteUnavailable, votePass)
	require.Zero(t, f.round(t).ClosedHeight)
	f.sweep(t, round.VoteDeadlineHeight+1)
	audit := f.reload(t)
	require.Equal(t, noVerdict, audit.PanelRounds[0].Result)
	require.Zero(t, audit.ResultHeight)
	require.Len(t, audit.PanelRounds, 2, "no conclusion leads to the re-assignment, never to a fault")
	require.Empty(t, *f.faults)
}
