package keeper

import (
	"bytes"
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/task/types"
)

func verifierCountForRound(params types.TaskParamsV1, verifyRound uint32) (uint32, error) {
	switch verifyRound {
	case types.VerifyRoundV1:
		if params.Weights.SelectedVerifierCount != types.SelectedVerifierCountV1 {
			return 0, fmt.Errorf("round 1 selected verifier count is invalid")
		}
		return params.Weights.SelectedVerifierCount, nil
	case types.ChallengeVerifyRoundV1:
		if params.Challenge.ChallengeVerifierCount < params.Weights.SelectedVerifierCount {
			return 0, fmt.Errorf("challenge verifier count is below round 1 count")
		}
		return params.Challenge.ChallengeVerifierCount, nil
	default:
		return 0, fmt.Errorf("verify_round must be 1 or 2")
	}
}

func advanceTaskPhase(core *types.TaskCoreState, next types.TaskPhase) {
	if core.TaskPhase < next {
		core.TaskPhase = next
	}
}

func expectedTaskPhaseForVerificationStatus(status types.VerificationStatus) (types.TaskPhase, bool) {
	switch status {
	case types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_WINDOW_PENDING,
		types.VerificationStatus_VERIFICATION_STATUS_VERIFY_COLLECTION_OPEN,
		types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_SELECTION_PENDING:
		return types.TaskPhase_TASK_PHASE_RECEIPT_COMMITTED, true
	case types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_ASSIGNED:
		return types.TaskPhase_TASK_PHASE_VERIFIER_ASSIGNED, true
	case types.VerificationStatus_VERIFICATION_STATUS_COMMITTING:
		return types.TaskPhase_TASK_PHASE_COMMITTING, true
	case types.VerificationStatus_VERIFICATION_STATUS_REVEALING:
		return types.TaskPhase_TASK_PHASE_REVEALING, true
	default:
		return types.TaskPhase_TASK_PHASE_UNSPECIFIED, false
	}
}

// validateActiveVerifierStage keeps round 1's phase/status pairing while round
// 2 is identified by its authoritative summary and open round row. A challenge
// round deliberately leaves task_phase at the round-1 REVEALING high-water mark.
func (k Keeper) validateActiveVerifierStage(
	ctx context.Context,
	taskKey types.TaskKey,
	core types.TaskCoreState,
	verifyRound uint32,
	allowedStatuses ...types.VerificationStatus,
) error {
	allowed := false
	for _, candidate := range allowedStatuses {
		if core.VerificationStatus == candidate {
			allowed = true
			break
		}
	}
	if !allowed {
		return fmt.Errorf("verification status is not valid for the active stage")
	}

	switch verifyRound {
	case types.VerifyRoundV1:
		expectedPhase, ok := expectedTaskPhaseForVerificationStatus(core.VerificationStatus)
		if !ok || core.TaskPhase != expectedPhase {
			return fmt.Errorf("round 1 task phase does not match verification status")
		}
		return nil
	case types.ChallengeVerifyRoundV1:
		if core.TaskPhase != types.TaskPhase_TASK_PHASE_REVEALING {
			return fmt.Errorf("challenge round changed the task phase high-water mark")
		}
		summary, err := k.TaskRoundSummary.Get(ctx, taskKey)
		if err != nil {
			return fmt.Errorf("challenge round summary is unavailable: %w", err)
		}
		if summary.MaxClosedRound != types.VerifyRoundV1 || summary.OpenRoundCount != 1 {
			return fmt.Errorf("challenge round summary is not open")
		}
		round, err := k.ReadVerificationRound(ctx, types.NewVerifyRoundKey(taskKey, verifyRound))
		if err != nil {
			return fmt.Errorf("challenge round is unavailable: %w", err)
		}
		if round.VerifyRound != verifyRound || round.XClosedHeight != nil ||
			!bytes.Equal(round.TaskId, core.TaskId) {
			return fmt.Errorf("challenge round state is not active")
		}
		return nil
	default:
		return fmt.Errorf("verify_round must be 1 or 2")
	}
}

func (k Keeper) activeVerifierAssignment(
	ctx context.Context,
	taskKey types.TaskKey,
) (uint32, types.VerifierAssignmentState, error) {
	for _, verifyRound := range []uint32{types.ChallengeVerifyRoundV1, types.VerifyRoundV1} {
		round, roundErr := k.ReadVerificationRound(ctx, types.NewVerifyRoundKey(taskKey, verifyRound))
		if roundErr == nil && round.XClosedHeight != nil {
			continue
		}
		assignment, err := k.ReadVerifierAssignment(ctx, types.NewVerifyRoundKey(taskKey, verifyRound))
		if err == nil {
			if err := validateVerifierAssignmentScope(assignment, taskKey, verifyRound); err != nil {
				return 0, types.VerifierAssignmentState{}, err
			}
			return verifyRound, assignment, nil
		}
	}
	return 0, types.VerifierAssignmentState{}, fmt.Errorf("active verifier assignment is unavailable")
}

// roundCloseResult is what one closed verification round contributes to the rest
// of the task: the terminal judgment, the frozen work unit and the two cluster
// commitments the settlement facts reference.
type roundCloseResult struct {
	Verdict               types.TaskVerdict
	FailureClass          types.TaskFailureClass
	GeneratedTokenCount   uint64
	ValidSampleCount      uint32
	Samples               []types.VerifierSampleV1
	Members               []types.ConsensusClusterMemberV1
	ResultReceiptRefsHash []byte
	ConsensusClusterHash  []byte
	RoundFactsHash        []byte
}

// closeVerificationRound is the single producer of a closed VerificationRoundState.
//
// Every input is re-derived from authoritative rows on each call — the selected
// verifier slate, each member's Commit/ResultReceipt pair and each metric summary
// judgment — so a closed round is a pure function of committed state rather than
// of whichever deadline runner happened to reach it first. Two validators that
// close the same round at the same cutoff therefore write byte-identical facts.
func (k Keeper) closeVerificationRound(
	ctx context.Context,
	taskKey types.TaskKey,
	verifyRound uint32,
	cutoffHeight, height uint64,
) (types.VerificationRoundState, roundCloseResult, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	roundKey := types.NewVerifyRoundKey(taskKey, verifyRound)
	round, err := k.ReadVerificationRound(ctx, roundKey)
	if err != nil {
		return types.VerificationRoundState{}, roundCloseResult{}, fmt.Errorf("verification round %d is unavailable: %w", verifyRound, err)
	}
	if round.XClosedHeight != nil {
		return types.VerificationRoundState{}, roundCloseResult{}, fmt.Errorf("verification round %d is already closed", verifyRound)
	}
	core, err := k.TaskCore.Get(ctx, taskKey)
	if err != nil {
		return types.VerificationRoundState{}, roundCloseResult{}, fmt.Errorf("task core is unavailable: %w", err)
	}
	assignment, err := k.ReadVerifierAssignment(ctx, roundKey)
	if err != nil {
		return types.VerificationRoundState{}, roundCloseResult{}, fmt.Errorf("verifier assignment is unavailable: %w", err)
	}
	if err := validateVerifierAssignmentScope(assignment, round.TaskId, verifyRound); err != nil {
		return types.VerificationRoundState{}, roundCloseResult{}, err
	}

	samples, err := k.collectRoundSamples(ctx, sdkCtx, core, assignment, verifyRound, cutoffHeight)
	if err != nil {
		return types.VerificationRoundState{}, roundCloseResult{}, err
	}
	consensus, err := types.AggregateVerifierConsensus(assignment.SelectedVerifierCount, samples)
	if err != nil {
		return types.VerificationRoundState{}, roundCloseResult{}, err
	}

	result := roundCloseResult{
		Verdict: consensus.Verdict, FailureClass: consensus.FailureClass,
		GeneratedTokenCount: consensus.GeneratedTokenCount, ValidSampleCount: uint32(len(samples)),
		Samples: append([]types.VerifierSampleV1(nil), samples...), Members: consensus.Members,
		// A round that produced no threshold cluster still needs a well-formed
		// 32-byte commitment slot; zero32 is the contract's "no cluster" value and
		// keeps the facts frame length-stable.
		ResultReceiptRefsHash: make([]byte, types.Hash32Len),
		ConsensusClusterHash:  make([]byte, types.Hash32Len),
	}
	if len(consensus.Members) > 0 {
		refs, err := types.ResultReceiptRefsHash(sdkCtx.ChainID(), round.TaskId, verifyRound, consensus.Members)
		if err != nil {
			return types.VerificationRoundState{}, roundCloseResult{}, err
		}
		cluster, err := types.ConsensusClusterHash(sdkCtx.ChainID(), round.TaskId, verifyRound, consensus.Members)
		if err != nil {
			return types.VerificationRoundState{}, roundCloseResult{}, err
		}
		result.ResultReceiptRefsHash, result.ConsensusClusterHash = refs[:], cluster[:]
	}

	round.XClosedHeight = &types.VerificationRoundState_ClosedHeight{ClosedHeight: height}
	round.XResultReceiptRefsHash = &types.VerificationRoundState_ResultReceiptRefsHash{ResultReceiptRefsHash: result.ResultReceiptRefsHash}
	round.XConsensusClusterHash = &types.VerificationRoundState_ConsensusClusterHash{ConsensusClusterHash: result.ConsensusClusterHash}
	round.GeneratedTokenCount = consensus.GeneratedTokenCount
	round.XVerdict = &types.VerificationRoundState_Verdict{Verdict: consensus.Verdict}
	round.XFailureClass = &types.VerificationRoundState_FailureClass{FailureClass: consensus.FailureClass}

	factsHash, err := types.VerifyRoundFactsHash(sdkCtx.ChainID(), round)
	if err != nil {
		return types.VerificationRoundState{}, roundCloseResult{}, err
	}
	round.XRoundFactsHash = &types.VerificationRoundState_RoundFactsHash{RoundFactsHash: factsHash[:]}
	result.RoundFactsHash = factsHash[:]
	return round, result, nil
}

// collectRoundSamples rebuilds the judged sample of every formally selected
// verifier. A slot is skipped — not failed — when it has no commit, no receipt,
// or a receipt accepted after the frozen cutoff: the threshold is taken over the
// frozen selected count, so a missing slot lowers the cluster, never the bar.
func (k Keeper) collectRoundSamples(
	ctx context.Context,
	sdkCtx sdk.Context,
	core types.TaskCoreState,
	assignment types.VerifierAssignmentState,
	verifyRound uint32,
	cutoffHeight uint64,
) ([]types.VerifierSampleV1, error) {
	taskKey := types.NewTaskKey(core.TaskId)
	taskAssignment, err := k.ReadTaskAssignment(ctx, taskKey)
	if err != nil {
		return nil, fmt.Errorf("task assignment is unavailable: %w", err)
	}
	budget, err := k.TaskBudget.Get(ctx, taskKey)
	if err != nil {
		return nil, fmt.Errorf("task budget is unavailable: %w", err)
	}
	profile, found := k.hubKeeper.GetProfileState(sdkCtx, core.ModelId, core.ProfileVersion)
	if !found {
		return nil, fmt.Errorf("frozen profile is unavailable")
	}
	snapshot := profile.ExecutionSnapshot

	samples := make([]types.VerifierSampleV1, 0, len(assignment.SelectedVerifiers))
	for index, selected := range assignment.SelectedVerifiers {
		commitKey, err := types.DeriveCommitKey(sdkCtx.ChainID(), core.TaskId, verifyRound, selected.OperatorAddress)
		if err != nil {
			return nil, fmt.Errorf("selected verifier %d: %w", index, err)
		}
		storeKey := types.NewCommitKey(commitKey[:])
		commit, ok, err := k.getCommitStateIfExists(ctx, storeKey)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		receipt, ok, err := k.getResultReceiptStateIfExists(ctx, storeKey)
		if err != nil {
			return nil, err
		}
		if !ok || receipt.AcceptedHeight == 0 || receipt.AcceptedHeight > cutoffHeight {
			continue
		}
		// The receipt must still be bound to the same slot and commit it was
		// accepted under; a mismatch is a broken invariant, not a skipped slot.
		if !bytes.Equal(receipt.CommitKey, commit.CommitKey) ||
			receipt.VerifyRound != verifyRound || receipt.VerifierOperatorAddress != selected.OperatorAddress ||
			receipt.SelectedVerifierIndex != uint32(index) {
			return nil, fmt.Errorf("selected verifier %d result receipt is not bound to its slot", index)
		}
		if err := k.validateMetricSummaryAgainstFrozenProfile(sdkCtx, core, taskAssignment, receipt.MetricSummary); err != nil {
			return nil, fmt.Errorf("selected verifier %d: %w", index, err)
		}
		summaryHash, err := types.MetricSummaryHash(receipt.MetricSummary)
		if err != nil {
			return nil, fmt.Errorf("selected verifier %d: %w", index, err)
		}
		if !bytes.Equal(summaryHash[:], receipt.MetricSummaryHash) {
			return nil, fmt.Errorf("selected verifier %d metric summary hash does not match its receipt", index)
		}
		verdict, err := types.JudgeMetricSample(receipt.MetricSummary, snapshot.VerificationProfile.Metrics, snapshot.VerificationThresholds)
		if err != nil {
			return nil, fmt.Errorf("selected verifier %d: %w", index, err)
		}
		// count_j = finite_count + missing_compared_count, checked and capped by the
		// budget's frozen max_output_tokens (§10.9 step 3).
		count, overflow := checkedHeightAdd(uint64(receipt.MetricSummary.FiniteCount), uint64(receipt.MetricSummary.MissingComparedCount))
		if overflow || count > budget.MaxOutputTokens {
			return nil, fmt.Errorf("selected verifier %d generated token count is out of range", index)
		}
		samples = append(samples, types.VerifierSampleV1{
			SelectedVerifierIndex:   uint32(index),
			VerifierOperatorAddress: selected.OperatorAddress,
			CommitKey:               append([]byte(nil), commit.CommitKey...),
			ResultReceiptDigest:     append([]byte(nil), receipt.ResultReceiptSigningDigest...),
			MetricSummaryHash:       append([]byte(nil), receipt.MetricSummaryHash...),
			SampleVerdict:           verdict,
			AcceptedHeight:          receipt.AcceptedHeight,
			GeneratedTokenCount:     count,
		})
	}
	return samples, nil
}
