package keeper

import (
	"bytes"
	"fmt"

	"github.com/TrueOpen/node/x/task/types"
)

// The decision rules of a Verifier-panel audit, as pure functions of the round
// rows. Nothing here reads state or the block height on its own, so the same
// rows always give the same outcome on every node, and every rule can be
// tested over all vote combinations.
//
// A panel round has members drawn by the chain. Each member casts at most one
// vote: PASS (the proof holds), REJECT (it does not), UNAVAILABLE (the member
// could not download the material). The round closes when `threshold` members
// agree or when its vote deadline passes.

// anchorPanelNextStep says what follows a closed round.
type anchorPanelNextStep uint8

const (
	// anchorPanelStepNone: the round is still open.
	anchorPanelStepNone anchorPanelNextStep = iota
	// anchorPanelStepProvisional: the round's PASS or REJECT becomes the
	// provisional verdict and the publication period starts.
	anchorPanelStepProvisional
	// anchorPanelStepRetry: an initial round without a conclusion; the chain
	// opens one retry round with other members.
	anchorPanelStepRetry
	// anchorPanelStepWorkerTimeout: the Worker's material was not obtainable, or
	// two rounds in a row reached no conclusion.
	anchorPanelStepWorkerTimeout
	// anchorPanelStepReauditDone: a re-audit round closed; anchorReauditOutcome
	// gives the final verdict.
	anchorPanelStepReauditDone
)

func isAnchorPanelVoteValue(v types.AnchorVerdictV1) bool {
	return v == types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS ||
		v == types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT ||
		v == types.AnchorVerdictV1_ANCHOR_VERDICT_V1_UNAVAILABLE
}

// anchorPanelMember reports whether operator is a member of the round.
func anchorPanelMember(round types.AnchorPanelRoundState, operator string) bool {
	for _, m := range round.Members {
		if m == operator {
			return true
		}
	}
	return false
}

// anchorPanelVoteOf returns the member's vote in the round, if it cast one.
func anchorPanelVoteOf(round types.AnchorPanelRoundState, operator string) (types.AnchorPanelVoteState, bool) {
	for _, v := range round.Votes {
		if v.VerifierOperatorAddress == operator {
			return v, true
		}
	}
	return types.AnchorPanelVoteState{}, false
}

// anchorPanelTally counts the votes of members only; a vote row of a
// non-member (which the message handler never writes) is ignored.
func anchorPanelTally(round types.AnchorPanelRoundState) (pass, reject, unavailable uint32) {
	for _, m := range round.Members {
		v, ok := anchorPanelVoteOf(round, m)
		if !ok {
			continue
		}
		switch v.Verdict {
		case types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS:
			pass++
		case types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT:
			reject++
		case types.AnchorVerdictV1_ANCHOR_VERDICT_V1_UNAVAILABLE:
			unavailable++
		}
	}
	return pass, reject, unavailable
}

// anchorPanelRoundResult is the result of a round at `height`:
//
//   - threshold equal votes decide at once (PASS, REJECT or UNAVAILABLE);
//   - otherwise the round stays open (UNSPECIFIED) until its vote deadline has
//     passed, and then it is inconclusive (NO_VERDICT);
//   - a round whose panel could not be drawn (no members) is inconclusive as
//     soon as it is evaluated.
//
// A threshold of zero, or above the panel size, can never be met by a vote and
// is treated like an undrawn panel rather than letting one vote decide.
func anchorPanelRoundResult(round types.AnchorPanelRoundState, threshold uint32, height uint64) types.AnchorVerdictV1 {
	if round.ClosedHeight != 0 {
		return round.Result
	}
	members := uint32(len(round.Members))
	if members == 0 || threshold == 0 || threshold > members {
		return types.AnchorVerdictV1_ANCHOR_VERDICT_V1_NO_VERDICT
	}
	pass, reject, unavailable := anchorPanelTally(round)
	// At most one value can reach a threshold above half of the panel; for a
	// threshold at or below half, PASS and REJECT could both reach it, and the
	// order below (REJECT, then UNAVAILABLE, then PASS) would make the outcome
	// depend on nothing but the counts, never on arrival order. Parameter
	// validation requires threshold > members/2, so this is defence in depth.
	switch {
	case reject >= threshold && pass >= threshold:
		return types.AnchorVerdictV1_ANCHOR_VERDICT_V1_NO_VERDICT
	case reject >= threshold:
		return types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT
	case unavailable >= threshold:
		return types.AnchorVerdictV1_ANCHOR_VERDICT_V1_UNAVAILABLE
	case pass >= threshold:
		return types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS
	}
	if round.VoteDeadlineHeight != 0 && height > round.VoteDeadlineHeight {
		return types.AnchorVerdictV1_ANCHOR_VERDICT_V1_NO_VERDICT
	}
	if pass+reject+unavailable == members {
		// Everybody voted and no value reached the threshold: waiting for the
		// deadline cannot change anything.
		return types.AnchorVerdictV1_ANCHOR_VERDICT_V1_NO_VERDICT
	}
	return types.AnchorVerdictV1_ANCHOR_VERDICT_V1_UNSPECIFIED
}

// anchorPanelStepAfter maps a closed round to what follows it.
func anchorPanelStepAfter(kind types.AnchorPanelRoundKindV1, result types.AnchorVerdictV1) (anchorPanelNextStep, types.AnchorTimeoutReasonV1) {
	none := types.AnchorTimeoutReasonV1_ANCHOR_TIMEOUT_REASON_V1_UNSPECIFIED
	if result == types.AnchorVerdictV1_ANCHOR_VERDICT_V1_UNSPECIFIED {
		return anchorPanelStepNone, none
	}
	if kind == types.AnchorPanelRoundKindV1_ANCHOR_PANEL_ROUND_KIND_V1_REAUDIT {
		return anchorPanelStepReauditDone, none
	}
	switch result {
	case types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS, types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT:
		return anchorPanelStepProvisional, none
	case types.AnchorVerdictV1_ANCHOR_VERDICT_V1_UNAVAILABLE:
		return anchorPanelStepWorkerTimeout, types.AnchorTimeoutReasonV1_ANCHOR_TIMEOUT_REASON_V1_PANEL_UNAVAILABLE
	default: // inconclusive
		if kind == types.AnchorPanelRoundKindV1_ANCHOR_PANEL_ROUND_KIND_V1_INITIAL {
			return anchorPanelStepRetry, none
		}
		return anchorPanelStepWorkerTimeout, types.AnchorTimeoutReasonV1_ANCHOR_TIMEOUT_REASON_V1_PANEL_INCONCLUSIVE
	}
}

// anchorReauditOutcome combines a provisional verdict with the result of the
// re-audit round: the final verdict, whether the provisional verdict was
// overturned (the opener's fee is refunded and the earlier voters of the
// overturned value voted wrongly), and the result source to record.
//
// A re-audit that reaches no conclusion, or cannot obtain the material, leaves
// the provisional verdict in force and overturns nothing.
func anchorReauditOutcome(provisional, reaudit types.AnchorVerdictV1) (final types.AnchorVerdictV1, overturned bool, source types.AnchorResultSourceV1) {
	isDecision := func(v types.AnchorVerdictV1) bool {
		return v == types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS || v == types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT
	}
	if isDecision(provisional) && isDecision(reaudit) && reaudit != provisional {
		return reaudit, true, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_REAUDIT
	}
	return provisional, false, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_VERIFIER_PANEL
}

// anchorPanelMissedMembers lists, in member order, the members of a closed
// round that count as having missed it: those that cast no vote, and those
// that voted UNAVAILABLE although every other member obtained the material
// (voted PASS or REJECT). In a panel of one there is no other member to
// contradict an UNAVAILABLE vote, so it is never counted.
func anchorPanelMissedMembers(round types.AnchorPanelRoundState) []string {
	var missed []string
	for _, m := range round.Members {
		v, voted := anchorPanelVoteOf(round, m)
		if !voted {
			missed = append(missed, m)
			continue
		}
		if v.Verdict != types.AnchorVerdictV1_ANCHOR_VERDICT_V1_UNAVAILABLE || len(round.Members) < 2 {
			continue
		}
		othersObtained := true
		for _, other := range round.Members {
			if other == m {
				continue
			}
			ov, ok := anchorPanelVoteOf(round, other)
			if !ok || (ov.Verdict != types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS && ov.Verdict != types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT) {
				othersObtained = false
				break
			}
		}
		if othersObtained {
			missed = append(missed, m)
		}
	}
	return missed
}

// anchorPanelWrongVoters lists, in round then member order and without
// duplicates, the members whose PASS or REJECT vote is the opposite of the
// final verdict. UNAVAILABLE is never a wrong vote, and a final verdict that
// is not PASS or REJECT makes nobody wrong.
func anchorPanelWrongVoters(rounds []types.AnchorPanelRoundState, final types.AnchorVerdictV1) []string {
	var opposite types.AnchorVerdictV1
	switch final {
	case types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS:
		opposite = types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT
	case types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT:
		opposite = types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS
	default:
		return nil
	}
	seen := map[string]struct{}{}
	var wrong []string
	for _, round := range rounds {
		for _, m := range round.Members {
			v, ok := anchorPanelVoteOf(round, m)
			if !ok || v.Verdict != opposite {
				continue
			}
			if _, dup := seen[m]; dup {
				continue
			}
			seen[m] = struct{}{}
			wrong = append(wrong, m)
		}
	}
	return wrong
}

// anchorPanelPriorMembers is every operator that sat on any round of the task;
// none of them may be drawn again for the same task.
func anchorPanelPriorMembers(rounds []types.AnchorPanelRoundState) map[string]struct{} {
	prior := map[string]struct{}{}
	for _, round := range rounds {
		for _, m := range round.Members {
			prior[m] = struct{}{}
		}
	}
	return prior
}

// validateAnchorPanelVote checks one vote against the audit row and the round
// it names. It returns replay = true for an exact repetition of the member's
// recorded vote (the caller answers NOOP), and an error for anything else that
// must not be recorded.
func validateAnchorPanelVote(state types.AnchorAuditState, round types.AnchorPanelRoundState, msg *types.MsgSubmitAnchorPanelVote, height uint64) (replay bool, err error) {
	if !isAnchorPanelVoteValue(msg.Verdict) {
		return false, fmt.Errorf("vote must be PASS, REJECT or UNAVAILABLE")
	}
	if round.PanelRound == 0 || round.PanelRound != msg.PanelRound {
		return false, fmt.Errorf("vote names panel round %d, not round %d", msg.PanelRound, round.PanelRound)
	}
	if len(state.OpeningHash) != types.Hash32Len || len(state.WorkerProofHash) != types.Hash32Len {
		return false, fmt.Errorf("audit has no recorded opening and proof")
	}
	if !bytes.Equal(state.OpeningHash, msg.OpeningHash) || !bytes.Equal(state.WorkerProofHash, msg.ProofHash) {
		return false, fmt.Errorf("vote names a different opening or proof than the chain recorded")
	}
	if !anchorPanelMember(round, msg.VerifierOperatorAddress) {
		return false, fmt.Errorf("operator is not a member of panel round %d", round.PanelRound)
	}
	if round.AssignmentHeight == 0 || height < round.AssignmentHeight {
		return false, fmt.Errorf("panel round %d is not assigned yet", round.PanelRound)
	}
	if round.VoteDeadlineHeight != 0 && height > round.VoteDeadlineHeight {
		return false, fmt.Errorf("vote deadline %d of panel round %d has passed", round.VoteDeadlineHeight, round.PanelRound)
	}
	if msg.Verdict == types.AnchorVerdictV1_ANCHOR_VERDICT_V1_UNAVAILABLE {
		if len(msg.Verdicts) != 0 {
			return false, fmt.Errorf("an UNAVAILABLE vote carries no position verdicts")
		}
	} else {
		if len(msg.Verdicts) != len(state.Positions) {
			return false, fmt.Errorf("vote must judge exactly the %d sampled positions", len(state.Positions))
		}
		anyReject := false
		for i, pv := range msg.Verdicts {
			if pv.Position != state.Positions[i] {
				return false, fmt.Errorf("position verdict %d names position %d, the audit sampled %d", i, pv.Position, state.Positions[i])
			}
			switch pv.Verdict {
			case types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS:
			case types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT:
				anyReject = true
			default:
				return false, fmt.Errorf("position verdict %d must be PASS or REJECT", i)
			}
		}
		if anyReject != (msg.Verdict == types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT) {
			return false, fmt.Errorf("a REJECT vote needs at least one rejected position and a PASS vote none")
		}
	}
	if existing, voted := anchorPanelVoteOf(round, msg.VerifierOperatorAddress); voted {
		if existing.Verdict == msg.Verdict && anchorPositionVerdictsEqual(existing.Verdicts, msg.Verdicts) {
			return true, nil
		}
		return false, fmt.Errorf("member already voted in panel round %d; a vote cannot be changed", round.PanelRound)
	}
	return false, nil
}

func anchorPositionVerdictsEqual(a, b []types.AnchorPositionVerdictV1) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Position != b[i].Position || a[i].Verdict != b[i].Verdict {
			return false
		}
	}
	return true
}

// validAnchorPanelShape reports whether a panel size and threshold describe a
// majority panel: at least one member, and a threshold above half of the panel
// and not above it, so two different values can never both reach it.
func validAnchorPanelShape(size, threshold uint32) bool {
	return size > 0 && threshold > size/2 && threshold <= size
}
