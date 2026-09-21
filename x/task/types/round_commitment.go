package types

import (
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// VerifyRoundID derives the only Phase 0 round identity. The round number is
// part of the preimage, so no caller-provided challenge identifier is needed.
func VerifyRoundID(chainID string, taskHash []byte, verifyRound uint32) ([32]byte, error) {
	chain, err := canonicalUTF8Field("chain_id", chainID)
	if err != nil {
		return [32]byte{}, err
	}
	task, err := canonicalHash32("task_hash", taskHash)
	if err != nil {
		return [32]byte{}, err
	}
	if !isPhase0VerifyRound(verifyRound) {
		return [32]byte{}, fmt.Errorf("verify_round must be 1 or 2")
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainVerifyRoundIDV1)).
		Raw(chain, task, shared.Uint32BE(verifyRound)).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

// VerifyRoundFactsHash commits the immutable terminal facts of one round. The
// close field is the frozen round-close deadline named by the Wire vector; the
// actual terminal height remains the independent closed_height state field.
func VerifyRoundFactsHash(chainID string, round VerificationRoundState) ([32]byte, error) {
	chain, err := canonicalUTF8Field("chain_id", chainID)
	if err != nil {
		return [32]byte{}, err
	}
	fields := []struct {
		name  string
		value []byte
	}{
		{"task_id", round.TaskId},
		{"task_hash", round.TaskHash},
		{"infer_receipt_ref", round.InferReceiptRef},
		{"result_receipt_refs_hash", round.GetResultReceiptRefsHash()},
		{"consensus_cluster_hash", round.GetConsensusClusterHash()},
		{"profile_execution_snapshot_hash", round.ProfileExecutionSnapshotHash},
		{"generation_params_digest", round.GenerationParamsDigest},
	}
	canonical := make([][]byte, len(fields))
	for i, field := range fields {
		canonical[i], err = canonicalHash32(field.name, field.value)
		if err != nil {
			return [32]byte{}, err
		}
	}
	if !isPhase0VerifyRound(round.VerifyRound) {
		return [32]byte{}, fmt.Errorf("verify_round must be 1 or 2")
	}
	if round.RoundOpenHeight == 0 || round.RoundCloseDeadlineHeight < round.RoundOpenHeight {
		return [32]byte{}, fmt.Errorf("round heights are invalid")
	}
	if _, ok := round.XClosedHeight.(*VerificationRoundState_ClosedHeight); !ok {
		return [32]byte{}, fmt.Errorf("closed_height must be present")
	}
	if _, ok := round.XVerdict.(*VerificationRoundState_Verdict); !ok || !isTerminalTaskVerdict(round.GetVerdict()) {
		return [32]byte{}, fmt.Errorf("verdict must be an explicit terminal verdict")
	}
	if _, ok := round.XFailureClass.(*VerificationRoundState_FailureClass); !ok {
		return [32]byte{}, fmt.Errorf("failure_class must be present")
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainVerifyRoundFactsV1)).Raw(
		chain, canonical[0], canonical[1], shared.Uint32BE(round.VerifyRound),
		shared.Uint64BE(round.RoundOpenHeight), shared.Uint64BE(round.RoundCloseDeadlineHeight),
		canonical[2], canonical[3], canonical[4], shared.Uint64BE(round.GeneratedTokenCount),
		shared.EnumBE(uint32(round.GetVerdict())), shared.EnumBE(uint32(round.GetFailureClass())), canonical[5], canonical[6],
	).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

func TaskRoundSummaryHash(chainID string, summary TaskRoundSummaryState) ([32]byte, error) {
	chain, err := canonicalUTF8Field("chain_id", chainID)
	if err != nil {
		return [32]byte{}, err
	}
	taskID, err := canonicalHash32("task_id", summary.TaskId)
	if err != nil {
		return [32]byte{}, err
	}
	round1, err := canonicalHash32("round1_facts_hash_or_zero32", summary.Round1FactsHashOrZero32)
	if err != nil {
		return [32]byte{}, err
	}
	round2, err := canonicalHash32("round2_facts_hash_or_zero32", summary.Round2FactsHashOrZero32)
	if err != nil {
		return [32]byte{}, err
	}
	effectRoot, err := canonicalHash32("round2_effect_root_or_zero32", summary.Round2EffectRootOrZero32)
	if err != nil {
		return [32]byte{}, err
	}
	if summary.MaxClosedRound > ChallengeVerifyRoundV1 || summary.OpenRoundCount > 1 || summary.EffectiveVerifyRound > ChallengeVerifyRoundV1 {
		return [32]byte{}, fmt.Errorf("task round summary counters are invalid")
	}
	roundsClosed, ok := summary.XRoundsClosedHeight.(*TaskRoundSummaryState_RoundsClosedHeight)
	if !ok {
		return [32]byte{}, fmt.Errorf("rounds_closed_height must be present")
	}
	challengeOpen := shared.OptionalAbsentCanonicalFieldV1()
	if value, present := summary.XChallengeOpenHeight.(*TaskRoundSummaryState_ChallengeOpenHeight); present {
		challengeOpen = shared.OptionalPresentCanonicalFieldV1(shared.Uint64BE(value.ChallengeOpenHeight))
	}
	challengeClose := shared.OptionalAbsentCanonicalFieldV1()
	if value, present := summary.XChallengeCloseHeight.(*TaskRoundSummaryState_ChallengeCloseHeight); present {
		challengeClose = shared.OptionalPresentCanonicalFieldV1(shared.Uint64BE(value.ChallengeCloseHeight))
	}
	if (summary.XChallengeOpenHeight == nil) != (summary.XChallengeCloseHeight == nil) {
		return [32]byte{}, fmt.Errorf("challenge open and close heights must have matching presence")
	}
	if summary.GetChallengeCloseHeight() != 0 && summary.GetChallengeCloseHeight() < summary.GetChallengeOpenHeight() {
		return [32]byte{}, fmt.Errorf("challenge close height precedes open height")
	}
	outcome := shared.RoundOutcomeV1_ROUND_OUTCOME_V1_UNSPECIFIED
	if value, present := summary.XRound2Outcome.(*TaskRoundSummaryState_Round2Outcome); present {
		outcome = value.Round2Outcome
	}
	if outcome < shared.RoundOutcomeV1_ROUND_OUTCOME_V1_UNSPECIFIED || outcome > shared.RoundOutcomeV1_ROUND_OUTCOME_V1_EXPIRED {
		return [32]byte{}, fmt.Errorf("round2_outcome is invalid")
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainTaskRoundSummaryV1)).Raw(
		chain, taskID, shared.Uint32BE(summary.MaxClosedRound), shared.Uint32BE(summary.OpenRoundCount),
		shared.Uint32BE(summary.EffectiveVerifyRound),
	).Field(challengeOpen, challengeClose).Raw(
		shared.Uint64BE(roundsClosed.RoundsClosedHeight), round1, round2,
		shared.EnumBE(uint32(outcome)), effectRoot,
	).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

func RoundFundingLockHash(chainID string, funding RoundFundingState) ([32]byte, error) {
	chain, taskID, opener, err := canonicalRoundFundingScope(chainID, funding)
	if err != nil {
		return [32]byte{}, err
	}
	bond, err := shared.CanonicalAmountFrameV1(funding.ChallengeOpenBond)
	if err != nil {
		return [32]byte{}, fmt.Errorf("challenge_open_bond: %w", err)
	}
	budget, err := shared.CanonicalAmountFrameV1(funding.VerifierBudget)
	if err != nil {
		return [32]byte{}, fmt.Errorf("verifier_budget: %w", err)
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainRoundFundingLockV1)).
		Raw(chain, taskID, shared.Uint32BE(funding.VerifyRound), opener).Nested(bond, budget).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

func RoundFundingResolutionHash(chainID string, funding RoundFundingState) ([32]byte, error) {
	chain, taskID, _, err := canonicalRoundFundingScope(chainID, funding)
	if err != nil {
		return [32]byte{}, err
	}
	outcome, ok := funding.XRoundOutcome.(*RoundFundingState_RoundOutcome)
	if !ok || outcome.RoundOutcome < shared.RoundOutcomeV1_ROUND_OUTCOME_V1_CONCURRED || outcome.RoundOutcome > shared.RoundOutcomeV1_ROUND_OUTCOME_V1_EXPIRED {
		return [32]byte{}, fmt.Errorf("round_outcome must be terminal")
	}
	amounts := []struct {
		name  string
		value shared.Amount
	}{
		{"verifier_paid", funding.VerifierPaid}, {"bond_refund", funding.BondRefund},
		{"bond_slash", funding.BondSlash}, {"opener_recovery", funding.OpenerRecovery},
		{"treasury_residual", funding.TreasuryResidual},
	}
	frames := make([]shared.CanonicalFrameV1, len(amounts))
	for i, amount := range amounts {
		frames[i], err = shared.CanonicalAmountFrameV1(amount.value)
		if err != nil {
			return [32]byte{}, fmt.Errorf("%s: %w", amount.name, err)
		}
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainRoundFundingResolutionV1)).Raw(
		chain, taskID, shared.Uint32BE(funding.VerifyRound), shared.EnumBE(uint32(outcome.RoundOutcome)),
	).Nested(frames...).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

func RoundEffectPlanRoot(chainID string, taskID []byte, verifyRound uint32, effects []shared.RoundEconomicEffectV1) ([32]byte, error) {
	return roundEffectCommitment(chainID, taskID, verifyRound, effects, false)
}

func RoundEffectRoot(chainID string, taskID []byte, verifyRound uint32, planRoot []byte, effects []shared.RoundEconomicEffectV1) ([32]byte, error) {
	chain, err := canonicalUTF8Field("chain_id", chainID)
	if err != nil {
		return [32]byte{}, err
	}
	task, err := canonicalHash32("task_id", taskID)
	if err != nil {
		return [32]byte{}, err
	}
	plan, err := canonicalHash32("round_effect_plan_root", planRoot)
	if err != nil {
		return [32]byte{}, err
	}
	if verifyRound != ChallengeVerifyRoundV1 {
		return [32]byte{}, fmt.Errorf("round effects are only valid for verify_round 2")
	}
	frames := make([]shared.CanonicalFrameV1, len(effects))
	for i, effect := range effects {
		if effect.EffectIndex != uint32(i) {
			return [32]byte{}, fmt.Errorf("effect index %d is not contiguous", effect.EffectIndex)
		}
		if effect.Status != shared.RoundEconomicEffectStatusV1_ROUND_ECONOMIC_EFFECT_STATUS_V1_APPLIED {
			return [32]byte{}, fmt.Errorf("effect %d is not applied", i)
		}
		applied, err := shared.CanonicalAmountFrameV1(effect.AppliedAmount)
		if err != nil {
			return [32]byte{}, fmt.Errorf("effect %d applied_amount: %w", i, err)
		}
		unfilled, err := shared.CanonicalAmountFrameV1(effect.UnfilledAmount)
		if err != nil {
			return [32]byte{}, fmt.Errorf("effect %d unfilled_amount: %w", i, err)
		}
		frames[i] = shared.NewCanonicalFrameBuilderV1().Raw(shared.Uint32BE(effect.EffectIndex)).Nested(applied, unfilled).
			Raw(shared.EnumBE(uint32(effect.Status))).Build()
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainRoundEffectRootV1)).Raw(
		chain, task, shared.Uint32BE(verifyRound), plan, shared.Uint32BE(uint32(len(effects))),
	).Nested(shared.CanonicalRepeatedFramesV1(frames)).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

func roundEffectCommitment(chainID string, taskID []byte, verifyRound uint32, effects []shared.RoundEconomicEffectV1, _ bool) ([32]byte, error) {
	chain, err := canonicalUTF8Field("chain_id", chainID)
	if err != nil {
		return [32]byte{}, err
	}
	task, err := canonicalHash32("task_id", taskID)
	if err != nil {
		return [32]byte{}, err
	}
	if verifyRound != ChallengeVerifyRoundV1 {
		return [32]byte{}, fmt.Errorf("round effects are only valid for verify_round 2")
	}
	frames := make([]shared.CanonicalFrameV1, len(effects))
	for i, effect := range effects {
		if effect.EffectIndex != uint32(i) {
			return [32]byte{}, fmt.Errorf("effect index %d is not contiguous", effect.EffectIndex)
		}
		if effect.EffectKind < shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_EARNING_DISQUALIFICATION ||
			effect.EffectKind > shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_TREASURY_RESIDUAL {
			return [32]byte{}, fmt.Errorf("effect %d kind is invalid", i)
		}
		source, err := canonicalOptionalRoundEffectAddress("source_address", effect.XSourceAddress, effect.GetSourceAddress())
		if err != nil {
			return [32]byte{}, fmt.Errorf("effect %d: %w", i, err)
		}
		destination, err := canonicalOptionalRoundEffectAddress("destination_address", effect.XDestinationAddress, effect.GetDestinationAddress())
		if err != nil {
			return [32]byte{}, fmt.Errorf("effect %d: %w", i, err)
		}
		requested, err := shared.CanonicalAmountFrameV1(effect.RequestedAmount)
		if err != nil {
			return [32]byte{}, fmt.Errorf("effect %d requested_amount: %w", i, err)
		}
		frames[i] = shared.NewCanonicalFrameBuilderV1().Raw(
			shared.Uint32BE(effect.EffectIndex), shared.EnumBE(uint32(effect.EffectKind)),
		).Field(source, destination).Nested(requested).Build()
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainRoundEffectPlanV1)).Raw(
		chain, task, shared.Uint32BE(verifyRound), shared.Uint32BE(uint32(len(effects))),
	).Nested(shared.CanonicalRepeatedFramesV1(frames)).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

func canonicalRoundFundingScope(chainID string, funding RoundFundingState) ([]byte, []byte, []byte, error) {
	chain, err := canonicalUTF8Field("chain_id", chainID)
	if err != nil {
		return nil, nil, nil, err
	}
	taskID, err := canonicalHash32("task_id", funding.TaskId)
	if err != nil {
		return nil, nil, nil, err
	}
	if funding.VerifyRound != ChallengeVerifyRoundV1 {
		return nil, nil, nil, fmt.Errorf("round funding is only valid for verify_round 2")
	}
	opener, err := CanonicalOperatorAddressBytes("opener", funding.OpenerAddress)
	if err != nil {
		return nil, nil, nil, err
	}
	return chain, taskID, opener, nil
}

func canonicalOptionalRoundEffectAddress(field string, presence interface{}, value string) (shared.CanonicalFieldV1, error) {
	if presence == nil {
		return shared.OptionalAbsentCanonicalFieldV1(), nil
	}
	raw, err := CanonicalOperatorAddressBytes(field, value)
	if err != nil {
		return shared.CanonicalFieldV1{}, err
	}
	return shared.OptionalPresentCanonicalFieldV1(raw), nil
}

func isPhase0VerifyRound(round uint32) bool {
	return round == VerifyRoundV1 || round == ChallengeVerifyRoundV1
}

func isTerminalTaskVerdict(verdict TaskVerdict) bool {
	return verdict > TaskVerdict_TASK_VERDICT_UNSPECIFIED && verdict <= TaskVerdict_TASK_VERDICT_VERIFY_UNAVAILABLE
}
