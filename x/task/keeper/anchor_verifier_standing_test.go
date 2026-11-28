package keeper

import (
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

const (
	standingWindow  = uint64(5_000)
	standingTrigger = uint32(2)
)

func standingParams(params *types.TaskParamsV1) {
	lateParams(lateFreeCount)(params)
	v := &params.Verification
	v.AnchorVerifierBondMultiple = 7
	v.AnchorVerifierAllowanceWindowBlocks = standingWindow
	v.AnchorVerifierMissTriggerCount = standingTrigger
	v.AnchorVerifierMissWindowBlocks = 10 * latePeriod
	v.AnchorVerifierWrongVoteSlashBps = 2_500
}

// newStandingFixture stages a proved panel audit with every standing rule on.
func newStandingFixture(t *testing.T, candidates int) *anchorPanelFixture {
	t.Helper()
	f := newTunedAnchorPanelFixture(t, 4_000, candidates, standingParams)
	f.openAndProveSampled(t, f.audit)
	f.reload(t)
	return f
}

func (f *anchorPanelFixture) standing(t *testing.T, operator string) types.VerifierAuditStandingState {
	t.Helper()
	response, err := (queryServer{k: f.keeper}).VerifierAuditStanding(f.ctx, &types.QueryVerifierAuditStandingRequest{OperatorAddress: operator})
	require.NoError(t, err)
	return response.Standing
}

func (f *anchorPanelFixture) standingRows(t *testing.T) int {
	t.Helper()
	rows, err := collectMapValues(f.ctx, f.keeper.VerifierAuditStanding)
	require.NoError(t, err)
	return len(rows)
}

// Being drawn uses the operator's allowance by the value of the task; the bond
// must cover the multiple of what it audits inside one allowance window, and a
// new window starts from nothing.
func TestVerifierAllowanceBoundsWhatABondAudits(t *testing.T) {
	f := newStandingFixture(t, 6)
	round := f.assign(t)
	const orderValue = uint64(800)
	for _, member := range round.Members {
		require.Equal(t, shared.NewAmount(orderValue), f.standing(t, member).AuditedValue)
	}
	response, err := (queryServer{k: f.keeper}).VerifierAuditStanding(f.ctx, &types.QueryVerifierAuditStandingRequest{OperatorAddress: round.Members[0]})
	require.NoError(t, err)
	require.Equal(t, shared.NewAmount(1_000_000/7), response.AllowanceLimit)

	member, fresh := round.Members[0], f.worker
	height := round.AssignmentHeight
	allows := func(operator string, bond, at uint64) bool {
		return f.keeper.anchorVerifierStandingAllows(f.ctx, operator, orderValue, bond, at, f.params)
	}
	require.True(t, allows(fresh, 7*orderValue, height), "an operator with no history is admitted by its bond alone")
	require.False(t, allows(fresh, 7*orderValue-1, height))
	require.True(t, allows(member, 14*orderValue, height))
	require.False(t, allows(member, 14*orderValue-1, height), "the task it already audits counts")
	nextWindow := (height/standingWindow + 1) * standingWindow
	require.True(t, allows(member, 7*orderValue, nextWindow), "a new window starts from nothing")

	off := f.params
	off.Verification.AnchorVerifierBondMultiple = 0
	require.True(t, f.keeper.anchorVerifierStandingAllows(f.ctx, member, orderValue, 1, height, off), "without a multiple there is no allowance rule")
}

// A member that had the whole vote window and did not vote missed the round.
// Misses count once per settlement period; every second period with a miss
// suspends the operator for the whole next period, and no longer.
func TestMissedVotesSuspendTheVerifierForOnePeriod(t *testing.T) {
	f := newStandingFixture(t, 7)
	first := f.assign(t)
	silent := first.Members[2]
	f.castVotes(t, first, first.AssignmentHeight+1, votePass, voteReject)
	require.Empty(t, f.standing(t, silent).MissedPeriods, "the round is still open")
	f.sweep(t, first.VoteDeadlineHeight+1)
	standing := f.standing(t, silent)
	require.Equal(t, []uint64{0}, standing.MissedPeriods)
	require.Zero(t, standing.SuspendedUntilPeriod, "one period with a miss is below the trigger")
	require.Empty(t, f.standing(t, first.Members[0]).MissedPeriods, "a member that voted did not miss")

	// The same operator misses again in the same period, then in the next.
	round := types.AnchorPanelRoundState{Members: []string{silent}, VoteDeadlineHeight: 10, ClosedHeight: 11}
	require.NoError(t, f.keeper.noteAnchorPanelMisses(f.ctx, round, first.VoteDeadlineHeight+2, f.params))
	require.Equal(t, []uint64{0}, f.standing(t, silent).MissedPeriods, "a period is counted once")
	require.NoError(t, f.keeper.noteAnchorPanelMisses(f.ctx, round, latePeriod+5, f.params))
	standing = f.standing(t, silent)
	require.Equal(t, []uint64{0, 1}, standing.MissedPeriods)
	require.Equal(t, uint32(1), standing.MissTriggersInWindow)
	require.Equal(t, uint64(3), standing.SuspendedUntilPeriod)

	allows := func(at uint64) bool {
		return f.keeper.anchorVerifierStandingAllows(f.ctx, silent, 800, 1_000_000, at, f.params)
	}
	require.True(t, allows(latePeriod+6), "the suspension starts at the next period boundary")
	require.False(t, allows(2*latePeriod))
	require.False(t, allows(3*latePeriod-1))
	require.True(t, allows(3*latePeriod), "one whole period, no longer")

	// Misses older than the window are forgotten.
	require.NoError(t, f.keeper.noteAnchorPanelMisses(f.ctx, round, 12*latePeriod, f.params))
	require.Equal(t, []uint64{12}, f.standing(t, silent).MissedPeriods)
	require.Zero(t, f.standing(t, silent).MissTriggersInWindow)
	require.Empty(t, *f.faults, "a Verifier's miss is never the Worker's")
}

// A round that two members decide early did not give the third its whole
// window: it is not counted as a miss. A member that says UNAVAILABLE while
// the two others judged the material is.
func TestEarlyDecisionIsNotAMissButContradictedUnavailableIs(t *testing.T) {
	f := newStandingFixture(t, 6)
	round := f.assign(t)
	f.castVotes(t, round, round.AssignmentHeight+1, votePass, votePass)
	require.NotZero(t, f.round(t).ClosedHeight)
	require.Empty(t, f.standing(t, round.Members[2]).MissedPeriods)

	g := newStandingFixture(t, 6)
	round = g.assign(t)
	g.castVotes(t, round, round.AssignmentHeight+1, voteUnavailable, votePass, votePass)
	require.Equal(t, []uint64{0}, g.standing(t, round.Members[0]).MissedPeriods)
	require.False(t, g.standing(t, round.Members[0]).Deactivated, "UNAVAILABLE is never a wrong vote")
}

// A member whose vote is the opposite of the final verdict the panel decided
// is deactivated and drawn for nothing further. Members that voted with the
// verdict are untouched.
func TestWrongVoteDeactivatesTheVerifier(t *testing.T) {
	f := newStandingFixture(t, 6)
	round := f.assign(t)
	f.castVotes(t, round, round.AssignmentHeight+1, voteReject, votePass, votePass)
	audit := f.reload(t)
	require.False(t, f.standing(t, round.Members[0]).Deactivated, "nothing happens while the verdict is only published")
	f.sweep(t, audit.PublicationEndHeight)
	require.True(t, f.standing(t, round.Members[0]).Deactivated)
	require.False(t, f.standing(t, round.Members[1]).Deactivated)
	require.False(t, f.standing(t, round.Members[2]).Deactivated)
	require.False(t, f.keeper.anchorVerifierStandingAllows(f.ctx, round.Members[0], 1, 1_000_000_000, audit.PublicationEndHeight, f.params))
	// The sweep that visits the final audit again changes nothing.
	require.NoError(t, f.keeper.retireAnchorPanelWork(f.ctx, f.taskKey, f.reload(t)))
	require.Equal(t, 3, f.standingRows(t))
}

// A re-audit that overturns the published verdict makes the members that
// voted for it wrong; a verdict no panel decided makes nobody wrong.
func TestWrongVotesFollowOnlyAPanelDecision(t *testing.T) {
	f := newStandingFixture(t, 9)
	audit := f.publish(t, voteReject)
	first := audit.PanelRounds[0]
	_, err := f.server.RequestAnchorReaudit(f.at(int64(audit.ProvisionalHeight+1)), f.reauditMsg(f.operators[1], 1_000))
	require.NoError(t, err)
	reaudit := f.assign(t)
	f.castVotes(t, reaudit, reaudit.AssignmentHeight+1, votePass, votePass)
	require.Equal(t, votePass, f.reload(t).TaskVerdict)
	require.True(t, f.standing(t, first.Members[0]).Deactivated)
	require.True(t, f.standing(t, first.Members[1]).Deactivated)
	require.False(t, f.standing(t, reaudit.Members[0]).Deactivated)

	// Two unavailable votes, no late submission: the audit ends LATE EXPIRED.
	// The member that passed the proof it obtained is not wrong.
	g := newStandingFixture(t, 6)
	round := g.assign(t)
	g.castVotes(t, round, round.AssignmentHeight+1, votePass, voteUnavailable, voteUnavailable)
	deadline := g.reload(t).LateDeadlineHeight
	g.sweep(t, deadline)
	require.Equal(t, voteReject, g.reload(t).TaskVerdict)
	require.False(t, g.standing(t, round.Members[0]).Deactivated)
}

// With the standing parameters at zero nothing is recorded and nobody is kept
// out; standings are part of the exported state.
func TestStandingIsOffByDefaultAndIsExported(t *testing.T) {
	f := newProvedAnchorLateFixture(t, 4_000, 7)
	round := f.assign(t)
	f.castVotes(t, round, round.AssignmentHeight+1, voteReject, votePass, votePass)
	f.sweep(t, f.reload(t).PublicationEndHeight)
	require.Zero(t, f.standingRows(t))
	require.True(t, f.keeper.anchorVerifierStandingAllows(f.ctx, round.Members[0], 1, 0, 1, f.params))

	g := newStandingFixture(t, 6)
	round = g.assign(t)
	exported, err := collectMapValues(g.ctx, g.keeper.VerifierAuditStanding)
	require.NoError(t, err)
	require.Len(t, exported, 3)
	require.NoError(t, g.keeper.VerifierAuditStanding.Clear(g.ctx, nil))
	genesis := types.GenesisState{Params: g.params, VerifierAuditStandings: exported}
	require.NoError(t, g.keeper.initGenesisAnchor(g.ctx, genesis))
	require.Equal(t, shared.NewAmount(800), g.standing(t, round.Members[0]).AuditedValue)
	require.ErrorContains(t, g.keeper.initGenesisAnchor(g.ctx, genesis), "duplicate")
}
