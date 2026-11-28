package keeper

import (
	"bytes"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
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

// publish takes a fresh fixture to a published verdict.
func (f *anchorPanelFixture) publish(t *testing.T, verdict types.AnchorVerdictV1) types.AnchorAuditState {
	t.Helper()
	round := f.assign(t)
	f.castVotes(t, round, round.AssignmentHeight+1, verdict, verdict)
	audit := f.reload(t)
	require.Equal(t, verdict, audit.ProvisionalVerdict)
	return audit
}

func (f *anchorPanelFixture) reauditMsg(opener string, maxFee uint64) *types.MsgRequestAnchorReaudit {
	return &types.MsgRequestAnchorReaudit{TaskId: f.taskID, OpenerAddress: opener, MaxFee: shared.NewAmount(maxFee)}
}

// panelFee is the verify share of the fixture task: 10 tokens at price 1 with
// a 25% verify ratio, rounded down.
func (f *anchorPanelFixture) panelFee() uint64 {
	gross := anchorOnlyGenerated * f.priceTokens / 1_000_000
	return gross * uint64(f.verifyRatio) / 10_000
}

// A published REJECT is re-audited by other members who pass the proof: the
// re-audit's decision is final, the Worker is paid and not slashed, and the
// opener gets its fee back.
func TestReauditOverturnsAPublishedReject(t *testing.T) {
	f := newAnchorPanelFixture(t, 4_000, 9)
	audit := f.publish(t, voteReject)
	opener := sdk.AccAddress(bytes.Repeat([]byte{0xd1}, 20)).String()
	fee := f.panelFee()
	require.Equal(t, uint64(2), fee)

	_, err := f.server.RequestAnchorReaudit(f.at(int64(audit.ProvisionalHeight+1)), f.reauditMsg(opener, fee-1))
	require.ErrorContains(t, err, "below the re-audit fee")
	response, err := f.server.RequestAnchorReaudit(f.at(int64(audit.ProvisionalHeight+1)), f.reauditMsg(opener, fee))
	require.NoError(t, err)
	require.Equal(t, uint32(3), response.PanelRound)
	require.Equal(t, shared.NewAmount(fee), response.Fee)
	_, err = f.server.RequestAnchorReaudit(f.at(int64(audit.ProvisionalHeight+2)), f.reauditMsg(opener, fee))
	require.ErrorContains(t, err, "already requested", "an audit has one re-audit")

	audit = f.reload(t)
	require.True(t, audit.ReauditRequested)
	require.Equal(t, opener, audit.ReauditOpener)
	require.Equal(t, "2", audit.ReauditFee)
	first := audit.PanelRounds[0]
	reaudit := f.assign(t)
	require.Equal(t, types.AnchorPanelRoundKindV1_ANCHOR_PANEL_ROUND_KIND_V1_REAUDIT, reaudit.Kind)
	for _, member := range reaudit.Members {
		require.NotContains(t, first.Members, member)
	}
	// The publication window ends while the re-audit runs: nothing becomes final.
	_, err = f.settleNow(t, audit.PublicationEndHeight+1)
	require.ErrorContains(t, err, "pending until")
	f.castVotes(t, reaudit, reaudit.AssignmentHeight+1, votePass, votePass)

	audit = f.reload(t)
	require.Equal(t, votePass, audit.TaskVerdict)
	require.Equal(t, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_REAUDIT, audit.ResultSource)
	require.Equal(t, voteReject, audit.ProvisionalVerdict, "the overturned verdict stays on record")
	require.Equal(t, "0", audit.ReauditFee, "the fee went back to the opener")
	f.requireNoPanelWork(t)
	settlement, err := f.settleNow(t, audit.FinalHeight+1)
	require.NoError(t, err)
	f.requirePaidInFull(t, settlement)
	require.Empty(t, *f.faults)
}

// A re-audit that confirms, or reaches no decision, leaves the published
// verdict standing and keeps the fee.
func TestReauditThatDoesNotOverturnLeavesThePublishedVerdict(t *testing.T) {
	for name, votes := range map[string][]types.AnchorVerdictV1{
		"confirms":    {voteReject, voteReject},
		"no decision": {votePass, voteReject, voteUnavailable},
		"unavailable": {voteUnavailable, voteUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			const bond = uint64(4_000)
			f := newAnchorPanelFixture(t, bond, 9)
			audit := f.publish(t, voteReject)
			opener := sdk.AccAddress(bytes.Repeat([]byte{0xd1}, 20)).String()
			_, err := f.server.RequestAnchorReaudit(f.at(int64(audit.ProvisionalHeight+1)), f.reauditMsg(opener, 100))
			require.NoError(t, err)
			reaudit := f.assign(t)
			f.castVotes(t, reaudit, reaudit.AssignmentHeight+1, votes...)
			audit = f.reload(t)
			require.Equal(t, voteReject, audit.TaskVerdict)
			require.Equal(t, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_VERIFIER_PANEL, audit.ResultSource)
			require.Equal(t, "2", audit.ReauditFee, "the fee is not returned")
			settlement, err := f.settleNow(t, audit.FinalHeight+1)
			require.NoError(t, err)
			f.requireWorkerFaulted(t, settlement, hubtypes.FaultTypeAnchorReject, f.params.Verification.AnchorRejectSlashBps, bond)
		})
	}
}

// A re-audit can be asked only while a verdict is published.
func TestReauditIsOpenOnlyDuringPublication(t *testing.T) {
	f := newAnchorPanelFixture(t, 4_000, 9)
	opener := sdk.AccAddress(bytes.Repeat([]byte{0xd1}, 20)).String()
	_, err := f.server.RequestAnchorReaudit(f.at(int64(f.audit.WorkerProofHeight+1)), f.reauditMsg(opener, 100))
	require.ErrorContains(t, err, "no published provisional verdict")
	audit := f.publish(t, votePass)
	_, err = f.server.RequestAnchorReaudit(f.at(int64(audit.PublicationEndHeight)), f.reauditMsg(opener, 100))
	require.ErrorContains(t, err, "no published provisional verdict", "the window is closed at its end height")
	_, err = f.server.RequestAnchorReaudit(f.at(int64(audit.PublicationEndHeight-1)), f.reauditMsg(opener, 100))
	require.NoError(t, err)
}

// With no operator left for a third panel the re-audit cannot be formed: the
// published verdict stands and the opener is not charged.
func TestReauditWithoutAPanelReturnsTheFee(t *testing.T) {
	f := newAnchorPanelFixture(t, 4_000, 4)
	audit := f.publish(t, votePass)
	opener := sdk.AccAddress(bytes.Repeat([]byte{0xd1}, 20)).String()
	_, err := f.server.RequestAnchorReaudit(f.at(int64(audit.ProvisionalHeight+1)), f.reauditMsg(opener, 100))
	require.NoError(t, err)
	audit = f.reload(t)
	require.Equal(t, votePass, audit.TaskVerdict)
	require.Equal(t, "0", audit.ReauditFee)
	require.Empty(t, audit.PanelRounds[1].Members)
	require.Equal(t, noVerdict, audit.PanelRounds[1].Result)
	f.requireNoPanelWork(t)
}

// Fewer eligible operators than seats: no panel can be formed in either round
// and the audit ends without a verdict. The Worker is paid.
func TestTooFewEligibleOperatorsEndsWithoutAVerdict(t *testing.T) {
	f := newAnchorPanelFixtureBeforeProof(t, 4_000, 5)
	for _, operator := range f.candidates[:3] {
		f.hubState.notAudit[operator] = true
	}
	f.openAndProveSampled(t, f.audit)
	audit := f.reload(t)
	require.Len(t, audit.PanelRounds, 1)
	require.Equal(t, noVerdict, audit.PanelRounds[0].Result, "two eligible operators cannot seat three")
	require.Equal(t, audit.WorkerProofHeight, audit.PanelRounds[0].ClosedHeight)
	f.sweep(t, audit.WorkerProofHeight)
	audit = f.reload(t)
	require.Len(t, audit.PanelRounds, 2)
	require.Equal(t, noVerdict, audit.TaskVerdict)
	settlement, err := f.settleNow(t, audit.ResultHeight+1)
	require.NoError(t, err)
	f.requirePaidInFull(t, settlement)
	require.Empty(t, *f.faults)
	f.requireNoPanelWork(t)
}

// Each eligibility leg keeps an operator out of the window, and the Worker is
// never in it.
func TestPanelWindowHoldsOnlyEligibleOperators(t *testing.T) {
	f := newAnchorPanelFixtureBeforeProof(t, 4_000, 9)
	f.hubState.notAudit[f.candidates[0]] = true
	f.hubState.stale[f.candidates[1]] = true
	f.hubState.lowBond[f.candidates[2]] = true
	f.hubState.waiting[f.candidates[3]] = true
	f.hubState.tombstoned[f.candidates[4]] = true
	f.openAndProveSampled(t, f.audit)
	selection := f.selection(t)
	require.Equal(t, uint32(4), selection.Window.EligibleCount)
	f.sweep(t, selection.Window.WindowRandomnessHeight)
	selection = f.selection(t)
	var members []string
	for _, member := range selection.Members {
		members = append(members, member.OperatorAddress)
	}
	require.ElementsMatch(t, f.candidates[5:], members)
}

// Where the separation rule is in force, a block-producer identity is not
// eligible for a panel.
func TestPanelWindowLeavesOutBlockProducers(t *testing.T) {
	f := newAnchorPanelFixtureBeforeProof(t, 4_000, 5)
	f.hubState.separation = true
	raw, err := sdk.AccAddressFromBech32(f.candidates[0])
	require.NoError(t, err)
	f.staking.powers[string(raw)] = 1
	f.openAndProveSampled(t, f.audit)
	require.Equal(t, uint32(4), f.selection(t).Window.EligibleCount)
}

// Hand-raises reach a panel round only through a selected Task Builder, only
// while the window is open, and only for window members that are still
// eligible. The Worker under audit cannot relay for its own panel.
func TestPanelHandraiseRules(t *testing.T) {
	f := newAnchorPanelFixture(t, 4_000, 6)
	selection := f.selection(t)
	open := selection.Window.WindowRandomnessHeight
	submit := func(height uint64, submitter string, handraises []types.VerifierHandraiseV1) (*types.MsgSubmitVerifierHandraisesResponse, error) {
		return f.server.SubmitVerifierHandraises(f.at(int64(height)), &types.MsgSubmitVerifierHandraises{
			TaskId: f.taskID, Handraises: handraises, SubmitterAddress: submitter,
		})
	}
	f.sweep(t, open)
	selection = f.selection(t)
	all := f.handraises(t, selection)
	require.Len(t, all, int(selection.Window.WindowSize))

	_, err := submit(open, f.worker, all)
	require.ErrorContains(t, err, "not a selected Task Builder", "the Worker under audit never relays")
	_, err = submit(open, f.candidates[0], all)
	require.ErrorContains(t, err, "not a selected Task Builder")
	_, err = submit(selection.Window.HandraiseCloseHeight+1, f.builders[0], all)
	require.ErrorContains(t, err, "handraise window is unavailable")

	// An operator that lost eligibility, or whose signature does not verify, is
	// skipped; the rest of the proposal is accepted.
	f.hubState.notAudit[all[0].Member.OperatorAddress] = true
	f.hubState.badSig[all[1].Member.OperatorAddress] = true
	response, err := submit(open, f.builders[0], all)
	require.NoError(t, err)
	require.Equal(t, uint32(len(all)-2), response.AddedMemberCount)
	require.Equal(t, uint32(len(all)-2), response.UnionCount)

	// Exact replay: same answer, nothing written.
	before := f.selection(t)
	replay, err := submit(open, f.builders[0], all)
	require.NoError(t, err)
	require.Equal(t, response.ProposalDigest, replay.ProposalDigest)
	require.Zero(t, replay.AddedMemberCount)
	after := f.selection(t)
	require.Equal(t, before.Facts, after.Facts)
	require.Len(t, after.Proposals, 1)

	// A second Builder adds nobody new.
	_, err = submit(open, f.builders[1], all[2:])
	require.ErrorContains(t, err, "adds no member")

	// A hand-raise signed for another round does not reach this one.
	other := f.handraises(t, selection, all[2].Member.OperatorAddress)
	other[0].VerifyRound = selection.PanelRound + 1
	_, err = submit(open, f.builders[1], other)
	require.ErrorContains(t, err, "envelope is invalid")
	for operator, count := range f.hubState.duties {
		accepted := false
		for _, fact := range after.Facts {
			accepted = accepted || fact.OperatorAddress == operator
		}
		require.Equal(t, accepted, count == 1, "one pending stage duty per accepted hand-raiser")
	}
}

// Fewer hand-raises than seats: no panel is drawn and the round ends with no
// conclusion.
func TestTooFewHandraisesEndTheRoundWithoutAConclusion(t *testing.T) {
	f := newAnchorPanelFixture(t, 4_000, 7)
	selection := f.selection(t)
	f.sweep(t, selection.Window.WindowRandomnessHeight)
	selection = f.selection(t)
	two := f.handraises(t, selection)[:2]
	_, err := f.server.SubmitVerifierHandraises(f.at(int64(selection.Window.HandraiseCloseHeight)), &types.MsgSubmitVerifierHandraises{
		TaskId: f.taskID, Handraises: two, SubmitterAddress: f.builders[0],
	})
	require.NoError(t, err)
	f.sweep(t, selection.Window.SelectionRandomnessHeight)
	audit := f.reload(t)
	require.Equal(t, noVerdict, audit.PanelRounds[0].Result)
	require.Empty(t, audit.PanelRounds[0].Members)
	require.Len(t, audit.PanelRounds, 2, "the re-assignment follows")
	for operator, count := range f.hubState.duties {
		require.Zerof(t, count, "duty of %s released when the round closed", operator)
	}
}

// The draw is a function of the frozen facts and the beacon: two chains with
// the same state draw the same panel, in the same order.
func TestPanelDrawIsDeterministic(t *testing.T) {
	first := newAnchorPanelFixture(t, 4_000, 8).assign(t)
	second := newAnchorPanelFixture(t, 4_000, 8).assign(t)
	require.Equal(t, first.Members, second.Members)
	require.Equal(t, first.SelectedVerifiersHash, second.SelectedVerifiersHash)
	require.Len(t, first.SelectedVerifiersHash, types.Hash32Len)
}

// The draw weighs each hand-raiser by the same stake, performance and jail
// factor formula as a Verifier draw: a lower performance score is a lower
// frozen weight.
func TestPanelHandraiseWeightFollowsPerformance(t *testing.T) {
	f := newAnchorPanelFixture(t, 4_000, 6)
	weak := f.candidates[0]
	f.hubState.score[weak] = 1
	selection := f.selection(t)
	f.sweep(t, selection.Window.WindowRandomnessHeight)
	selection = f.selection(t)
	_, err := f.server.SubmitVerifierHandraises(f.at(int64(selection.Window.HandraiseCloseHeight)), &types.MsgSubmitVerifierHandraises{
		TaskId: f.taskID, Handraises: f.handraises(t, selection), SubmitterAddress: f.builders[0],
	})
	require.NoError(t, err)
	var weakWeight, peerWeight uint32
	for _, fact := range f.selection(t).Facts {
		if fact.OperatorAddress == weak {
			weakWeight = fact.CandidateWeight
		} else {
			peerWeight = fact.CandidateWeight
		}
	}
	require.NotZero(t, weakWeight)
	require.Less(t, weakWeight, peerWeight)
	expected, err := shared.CandidateWeightPPM(1_000_000, 1, 1, shared.CandidateJailFactorPPM(0),
		f.params.Weights.VerifierStakeWeightPpm, f.params.Weights.VerifierPerformanceWeightPpm, f.params.Weights.CandidateWeightPpmMax)
	require.NoError(t, err)
	require.Equal(t, expected, weakWeight)
}
