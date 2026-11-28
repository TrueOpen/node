package keeper

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/task/types"
)

const (
	pvNone   = types.AnchorVerdictV1_ANCHOR_VERDICT_V1_UNSPECIFIED
	pvPass   = types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS
	pvReject = types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT
	pvInconc = types.AnchorVerdictV1_ANCHOR_VERDICT_V1_NO_VERDICT
	pvUnavai = types.AnchorVerdictV1_ANCHOR_VERDICT_V1_UNAVAILABLE
)

var panelVoteValues = []types.AnchorVerdictV1{pvNone, pvPass, pvReject, pvUnavai}

func panelRound(kind types.AnchorPanelRoundKindV1, votes ...types.AnchorVerdictV1) types.AnchorPanelRoundState {
	round := types.AnchorPanelRoundState{PanelRound: 1, Kind: kind, AssignmentHeight: 100, VoteDeadlineHeight: 200}
	for i, v := range votes {
		member := fmt.Sprintf("verifier-%d", i)
		round.Members = append(round.Members, member)
		if v != pvNone {
			round.Votes = append(round.Votes, types.AnchorPanelVoteState{VerifierOperatorAddress: member, Verdict: v, Height: 150})
		}
	}
	return round
}

// Every combination of three members each voting nothing, PASS, REJECT or
// UNAVAILABLE, before and after the deadline, against the rule stated in words.
func TestPanelRoundResultOverEveryVoteCombination(t *testing.T) {
	initial := types.AnchorPanelRoundKindV1_ANCHOR_PANEL_ROUND_KIND_V1_INITIAL
	for _, a := range panelVoteValues {
		for _, b := range panelVoteValues {
			for _, c := range panelVoteValues {
				round := panelRound(initial, a, b, c)
				count := map[types.AnchorVerdictV1]int{}
				for _, v := range []types.AnchorVerdictV1{a, b, c} {
					count[v]++
				}
				var want types.AnchorVerdictV1
				switch {
				case count[pvReject] >= 2:
					want = pvReject
				case count[pvUnavai] >= 2:
					want = pvUnavai
				case count[pvPass] >= 2:
					want = pvPass
				}
				name := fmt.Sprintf("%d-%d-%d", a, b, c)
				before := anchorPanelRoundResult(round, 2, 200)
				after := anchorPanelRoundResult(round, 2, 201)
				if want != pvNone {
					require.Equal(t, want, before, "%s: two equal votes decide at once", name)
					require.Equal(t, want, after, "%s: and the deadline does not change it", name)
					continue
				}
				require.Equal(t, pvInconc, after, "%s: no two equal votes by the deadline is inconclusive", name)
				if count[pvNone] == 0 {
					require.Equal(t, pvInconc, before, "%s: three different votes cannot change by waiting", name)
				} else {
					require.Equal(t, pvNone, before, "%s: the round stays open until the deadline", name)
				}
			}
		}
	}
}

func TestPanelRoundResultNeverLetsAMinorityOrABadShapeDecide(t *testing.T) {
	initial := types.AnchorPanelRoundKindV1_ANCHOR_PANEL_ROUND_KIND_V1_INITIAL
	// One vote never decides a panel of three.
	for _, v := range []types.AnchorVerdictV1{pvPass, pvReject, pvUnavai} {
		require.Equal(t, pvNone, anchorPanelRoundResult(panelRound(initial, v, pvNone, pvNone), 2, 150))
	}
	// No members drawn, a zero threshold, or a threshold above the panel: inconclusive, whatever was voted.
	require.Equal(t, pvInconc, anchorPanelRoundResult(panelRound(initial), 2, 150))
	require.Equal(t, pvInconc, anchorPanelRoundResult(panelRound(initial, pvReject, pvReject, pvReject), 0, 150))
	require.Equal(t, pvInconc, anchorPanelRoundResult(panelRound(initial, pvReject, pvReject, pvReject), 4, 150))
	// A threshold at half of a panel of four cannot make both PASS and REJECT win.
	require.Equal(t, pvInconc, anchorPanelRoundResult(panelRound(initial, pvPass, pvPass, pvReject, pvReject), 2, 150))
	// A vote row of an operator that is not a member is not counted.
	round := panelRound(initial, pvReject, pvNone, pvNone)
	round.Votes = append(round.Votes, types.AnchorPanelVoteState{VerifierOperatorAddress: "stranger", Verdict: pvReject})
	require.Equal(t, pvNone, anchorPanelRoundResult(round, 2, 150))
	// A closed round reports what it closed with.
	closed := panelRound(initial, pvPass, pvPass, pvNone)
	closed.ClosedHeight, closed.Result = 160, pvPass
	closed.Votes = nil
	require.Equal(t, pvPass, anchorPanelRoundResult(closed, 2, 999))
	require.True(t, validAnchorPanelShape(3, 2))
	require.True(t, validAnchorPanelShape(1, 1))
	require.False(t, validAnchorPanelShape(0, 0))
	require.False(t, validAnchorPanelShape(3, 1))
	require.False(t, validAnchorPanelShape(4, 2))
	require.False(t, validAnchorPanelShape(3, 4))
}

func TestPanelStepAfterEachRoundKindAndResult(t *testing.T) {
	initial := types.AnchorPanelRoundKindV1_ANCHOR_PANEL_ROUND_KIND_V1_INITIAL
	retry := types.AnchorPanelRoundKindV1_ANCHOR_PANEL_ROUND_KIND_V1_RETRY
	reaudit := types.AnchorPanelRoundKindV1_ANCHOR_PANEL_ROUND_KIND_V1_REAUDIT
	unavailable := types.AnchorTimeoutReasonV1_ANCHOR_TIMEOUT_REASON_V1_PANEL_UNAVAILABLE
	inconclusive := types.AnchorTimeoutReasonV1_ANCHOR_TIMEOUT_REASON_V1_PANEL_INCONCLUSIVE
	none := types.AnchorTimeoutReasonV1_ANCHOR_TIMEOUT_REASON_V1_UNSPECIFIED
	cases := []struct {
		kind   types.AnchorPanelRoundKindV1
		result types.AnchorVerdictV1
		step   anchorPanelNextStep
		reason types.AnchorTimeoutReasonV1
	}{
		{initial, pvNone, anchorPanelStepNone, none},
		{initial, pvPass, anchorPanelStepProvisional, none},
		{initial, pvReject, anchorPanelStepProvisional, none},
		{initial, pvUnavai, anchorPanelStepWorkerTimeout, unavailable},
		{initial, pvInconc, anchorPanelStepRetry, none},
		{retry, pvPass, anchorPanelStepProvisional, none},
		{retry, pvReject, anchorPanelStepProvisional, none},
		{retry, pvUnavai, anchorPanelStepWorkerTimeout, unavailable},
		{retry, pvInconc, anchorPanelStepWorkerTimeout, inconclusive},
		{reaudit, pvNone, anchorPanelStepNone, none},
		{reaudit, pvPass, anchorPanelStepReauditDone, none},
		{reaudit, pvReject, anchorPanelStepReauditDone, none},
		{reaudit, pvUnavai, anchorPanelStepReauditDone, none},
		{reaudit, pvInconc, anchorPanelStepReauditDone, none},
	}
	for _, tc := range cases {
		step, reason := anchorPanelStepAfter(tc.kind, tc.result)
		require.Equal(t, tc.step, step, "kind %v result %v", tc.kind, tc.result)
		require.Equal(t, tc.reason, reason, "kind %v result %v", tc.kind, tc.result)
	}
}

func TestReauditOutcomeOverEveryPair(t *testing.T) {
	panel := types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_VERIFIER_PANEL
	re := types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_REAUDIT
	for _, provisional := range []types.AnchorVerdictV1{pvPass, pvReject} {
		for _, second := range []types.AnchorVerdictV1{pvPass, pvReject, pvUnavai, pvInconc} {
			final, overturned, source := anchorReauditOutcome(provisional, second)
			opposite := (second == pvPass || second == pvReject) && second != provisional
			if opposite {
				require.Equal(t, second, final)
				require.True(t, overturned)
				require.Equal(t, re, source)
			} else {
				require.Equal(t, provisional, final, "a re-audit without an opposite decision leaves the verdict")
				require.False(t, overturned)
				require.Equal(t, panel, source)
			}
		}
	}
}

func TestPanelMissedMembersOverEveryVoteCombination(t *testing.T) {
	initial := types.AnchorPanelRoundKindV1_ANCHOR_PANEL_ROUND_KIND_V1_INITIAL
	obtained := func(v types.AnchorVerdictV1) bool { return v == pvPass || v == pvReject }
	for _, a := range panelVoteValues {
		for _, b := range panelVoteValues {
			for _, c := range panelVoteValues {
				votes := []types.AnchorVerdictV1{a, b, c}
				var want []string
				for i, v := range votes {
					others := true
					for j, o := range votes {
						if j != i && !obtained(o) {
							others = false
						}
					}
					if v == pvNone || (v == pvUnavai && others) {
						want = append(want, fmt.Sprintf("verifier-%d", i))
					}
				}
				require.Equal(t, want, anchorPanelMissedMembers(panelRound(initial, a, b, c)), "%d-%d-%d", a, b, c)
			}
		}
	}
	// A single-member panel has nobody to contradict "cannot download".
	require.Empty(t, anchorPanelMissedMembers(panelRound(initial, pvUnavai)))
}

func TestPanelWrongVotersNeverIncludeUnavailableOrTheWinningSide(t *testing.T) {
	initial := types.AnchorPanelRoundKindV1_ANCHOR_PANEL_ROUND_KIND_V1_INITIAL
	first := panelRound(initial, pvPass, pvPass, pvReject)
	second := panelRound(types.AnchorPanelRoundKindV1_ANCHOR_PANEL_ROUND_KIND_V1_REAUDIT, pvReject, pvUnavai, pvReject)
	second.PanelRound = 2
	second.Members = []string{"verifier-3", "verifier-4", "verifier-5"}
	for i := range second.Votes {
		second.Votes[i].VerifierOperatorAddress = second.Members[[]int{0, 1, 2}[i]]
	}
	rounds := []types.AnchorPanelRoundState{first, second}
	require.Equal(t, []string{"verifier-0", "verifier-1"}, anchorPanelWrongVoters(rounds, pvReject))
	require.Equal(t, []string{"verifier-2", "verifier-3", "verifier-5"}, anchorPanelWrongVoters(rounds, pvPass))
	require.Empty(t, anchorPanelWrongVoters(rounds, pvInconc))
	require.Empty(t, anchorPanelWrongVoters(rounds, pvUnavai))
	prior := anchorPanelPriorMembers(rounds)
	require.Len(t, prior, 6)
}

func TestValidatePanelVote(t *testing.T) {
	hash := func(b byte) []byte { h := make([]byte, types.Hash32Len); h[0] = b; return h }
	state := types.AnchorAuditState{OpeningHash: hash(1), WorkerProofHash: hash(2), Positions: []uint64{7, 42}}
	round := panelRound(types.AnchorPanelRoundKindV1_ANCHOR_PANEL_ROUND_KIND_V1_INITIAL, pvNone, pvNone, pvNone)
	pass := func() *types.MsgSubmitAnchorPanelVote {
		return &types.MsgSubmitAnchorPanelVote{PanelRound: 1, OpeningHash: hash(1), ProofHash: hash(2), Verdict: pvPass,
			Verdicts:                []types.AnchorPositionVerdictV1{{Position: 7, Verdict: pvPass}, {Position: 42, Verdict: pvPass}},
			VerifierOperatorAddress: "verifier-1"}
	}
	replay, err := validateAnchorPanelVote(state, round, pass(), 150)
	require.NoError(t, err)
	require.False(t, replay)

	bad := map[string]func(m *types.MsgSubmitAnchorPanelVote) uint64{
		"not a vote value":         func(m *types.MsgSubmitAnchorPanelVote) uint64 { m.Verdict = pvInconc; return 150 },
		"wrong round":              func(m *types.MsgSubmitAnchorPanelVote) uint64 { m.PanelRound = 2; return 150 },
		"other opening":            func(m *types.MsgSubmitAnchorPanelVote) uint64 { m.OpeningHash = hash(9); return 150 },
		"other proof":              func(m *types.MsgSubmitAnchorPanelVote) uint64 { m.ProofHash = hash(9); return 150 },
		"not a member":             func(m *types.MsgSubmitAnchorPanelVote) uint64 { m.VerifierOperatorAddress = "stranger"; return 150 },
		"before assignment":        func(m *types.MsgSubmitAnchorPanelVote) uint64 { return 99 },
		"after the deadline":       func(m *types.MsgSubmitAnchorPanelVote) uint64 { return 201 },
		"missing a position":       func(m *types.MsgSubmitAnchorPanelVote) uint64 { m.Verdicts = m.Verdicts[:1]; return 150 },
		"wrong position":           func(m *types.MsgSubmitAnchorPanelVote) uint64 { m.Verdicts[1].Position = 43; return 150 },
		"PASS with a rejected row": func(m *types.MsgSubmitAnchorPanelVote) uint64 { m.Verdicts[0].Verdict = pvReject; return 150 },
		"REJECT with no rejected row": func(m *types.MsgSubmitAnchorPanelVote) uint64 {
			m.Verdict = pvReject
			return 150
		},
		"position verdict UNAVAILABLE": func(m *types.MsgSubmitAnchorPanelVote) uint64 { m.Verdicts[0].Verdict = pvUnavai; return 150 },
		"UNAVAILABLE with positions":   func(m *types.MsgSubmitAnchorPanelVote) uint64 { m.Verdict = pvUnavai; return 150 },
	}
	for name, mutate := range bad {
		msg := pass()
		height := mutate(msg)
		_, err := validateAnchorPanelVote(state, round, msg, height)
		require.Error(t, err, name)
	}
	// The deadline height itself is still inside the window; an UNAVAILABLE vote without positions is valid.
	unavailable := pass()
	unavailable.Verdict, unavailable.Verdicts = pvUnavai, nil
	_, err = validateAnchorPanelVote(state, round, unavailable, 200)
	require.NoError(t, err)
	// No opening or proof on record: nothing to vote on.
	_, err = validateAnchorPanelVote(types.AnchorAuditState{Positions: state.Positions}, round, pass(), 150)
	require.Error(t, err)

	// A recorded vote: the same vote again is a replay, a different one is refused.
	voted := round
	voted.Votes = []types.AnchorPanelVoteState{{VerifierOperatorAddress: "verifier-1", Verdict: pvPass, Verdicts: pass().Verdicts}}
	replay, err = validateAnchorPanelVote(state, voted, pass(), 160)
	require.NoError(t, err)
	require.True(t, replay)
	changed := pass()
	changed.Verdict = pvReject
	changed.Verdicts[0].Verdict = pvReject
	_, err = validateAnchorPanelVote(state, voted, changed, 160)
	require.Error(t, err)
	_, err = validateAnchorPanelVote(state, voted, unavailable, 160)
	require.Error(t, err)
}
