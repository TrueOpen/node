package keeper

import (
	"bytes"
	"context"
	"errors"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// processExpiredAssignmentRandomness is the WORKER_ASSIGNMENT (§10.2) sweep.
// Every visited index row charges the budget, including rows whose beacon is not
// published yet (§4.5 line 709: "when the beacon is missing, stay pending and
// consume this block's visited budget").
func (k Keeper) processExpiredAssignmentRandomness(ctx context.Context, currentHeight, maxPerBlock uint64) (uint64, error) {
	return k.sweepExpiredTaskDeadlines(ctx, k.AssignmentRandomnessIndex, currentHeight, maxPerBlock,
		func(ctx context.Context, taskID types.TaskKey, deadline uint64) (deadlineSweepOutcome, uint64, error) {
			return k.finalizeAssignmentRandomness(ctx, taskID, deadline, currentHeight)
		})
}

type assignmentWinnerComputation struct {
	Core       types.TaskCoreState
	Assignment types.TaskAssignmentState
	Candidate  types.TaskCandidateFactState
	Beacon     []byte
	Digest     [32]byte
	Counter    uint64
}

// finalizeAssignmentRandomness is the single §10.2 executor shared by EndBlock
// and MsgSweepDeadline(TaskDeadlineLocator{WORKER_ASSIGNMENT}).
func (k Keeper) finalizeAssignmentRandomness(ctx context.Context, taskID types.TaskKey, deadline, currentHeight uint64) (deadlineSweepOutcome, uint64, error) {
	core, found, rowBytes, err := k.loadSweepTaskCore(ctx, taskID)
	if err != nil || !found {
		return deadlineSweepStale, rowBytes, err
	}
	if core.TaskPhase == types.TaskPhase_TASK_PHASE_WORKER_ASSIGNMENT_PENDING &&
		core.AssignmentStatus == types.AssignmentStatus_ASSIGNMENT_STATUS_NONE {
		assignment, err := k.ReadTaskAssignment(ctx, taskID)
		if err != nil || assignment.AssignmentRandomnessHeight != deadline {
			return deadlineSweepStale, rowBytes, err
		}
		union, err := k.TaskStageHandraiseUnion.Get(ctx, types.NewTaskStageKey(taskID, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK))
		if err != nil {
			return deadlineSweepPending, rowBytes, err
		}
		if err := k.finalizeOpenTaskCandidateUnion(ctx, taskID, union.WindowCloseHeight); err != nil {
			return deadlineSweepPending, rowBytes, err
		}
		core, err = k.TaskCore.Get(ctx, taskID)
		if err != nil {
			return deadlineSweepPending, rowBytes, err
		}
	}
	if core.TaskPhase != types.TaskPhase_TASK_PHASE_WORKER_ASSIGNMENT_PENDING ||
		core.AssignmentStatus != types.AssignmentStatus_ASSIGNMENT_STATUS_RANDOMNESS_PENDING {
		return deadlineSweepStale, rowBytes, nil
	}
	assignment, err := k.ReadTaskAssignment(ctx, taskID)
	if err != nil {
		if errIsNotFound(err) {
			return deadlineSweepStale, rowBytes, nil
		}
		return deadlineSweepStale, rowBytes, err
	}
	if assignment.AssignmentRandomnessHeight != deadline {
		return deadlineSweepStale, rowBytes, nil
	}

	params, err := k.Params.Get(ctx)
	if err != nil {
		return deadlineSweepPending, rowBytes, err
	}
	computation, err := k.computeAssignmentWinner(
		ctx, taskID, deadline, uint64(params.Weights.MaxWeightedDrawAttemptsPerSelection),
	)
	switch {
	case err == nil:
	case errors.Is(err, types.ErrBeaconNotFound):
		// §4.5: stay pending, but the visited budget is already charged by the
		// caller. The index row survives so the next block retries.
		return deadlineSweepPending, rowBytes, nil
	case errors.Is(err, errEmptyAssignmentLegalSet):
		// §10.1 line 2652: an empty / unusable legal set is a deterministic
		// order failure, not an invariant error.
		if err := k.failAssignmentRandomness(ctx, core, assignment, currentHeight,
			types.AssignmentFailureReason_ASSIGNMENT_FAILURE_REASON_EMPTY_LEGAL_SET); err != nil {
			return deadlineSweepAdvanced, rowBytes, err
		}
		if err := removeDeadlineIndex(ctx, k.AssignmentRandomnessIndex, taskID, deadline); err != nil {
			return deadlineSweepAdvanced, rowBytes, err
		}
		return deadlineSweepAdvanced, rowBytes, nil
	default:
		// §4.5: attempts exhausted or a broken commitment is an invariant error
		// and the task stays pending. It is *not* swallowed as "recoverable".
		return deadlineSweepPending, rowBytes, err
	}

	if err := k.commitAssignmentWinner(ctx, computation, currentHeight, params.Weights.CandidateWeightPpmMax); err != nil {
		return deadlineSweepAdvanced, rowBytes, err
	}
	if err := removeDeadlineIndex(ctx, k.AssignmentRandomnessIndex, taskID, deadline); err != nil {
		return deadlineSweepAdvanced, rowBytes, err
	}
	return deadlineSweepAdvanced, rowBytes, nil
}

var errEmptyAssignmentLegalSet = errors.New("assignment legal set cannot select a worker")

// computeAssignmentWinner is the complete deterministic read/compute portion. It
// writes nothing.
func (k Keeper) computeAssignmentWinner(
	ctx context.Context, taskID types.TaskKey, randomnessHeight, maxAttempts uint64,
) (assignmentWinnerComputation, error) {
	if maxAttempts == 0 {
		return assignmentWinnerComputation{}, errorsmod.Wrap(types.ErrInvariantBroken, "weighted draw attempt limit must be positive")
	}
	core, err := k.TaskCore.Get(ctx, types.NewTaskKey(taskID))
	if err != nil {
		return assignmentWinnerComputation{}, err
	}
	assignment, err := k.ReadTaskAssignment(ctx, types.NewTaskKey(taskID))
	if err != nil {
		return assignmentWinnerComputation{}, err
	}
	if core.AssignmentStatus != types.AssignmentStatus_ASSIGNMENT_STATUS_RANDOMNESS_PENDING ||
		core.TaskPhase != types.TaskPhase_TASK_PHASE_WORKER_ASSIGNMENT_PENDING ||
		assignment.AssignmentRandomnessHeight != randomnessHeight ||
		!bytes.Equal(core.TaskId, assignment.TaskId) ||
		len(assignment.GenerationParamsDigest) != types.Hash32Len {
		return assignmentWinnerComputation{}, errorsmod.Wrap(types.ErrInvariantBroken, "assignment randomness state mismatch")
	}
	header, err := k.AssignmentCandidateSet.Get(ctx, types.NewTaskKey(taskID))
	if err != nil {
		if errIsNotFound(err) {
			return assignmentWinnerComputation{}, errEmptyAssignmentLegalSet
		}
		return assignmentWinnerComputation{}, err
	}
	if !bytes.Equal(header.TaskId, core.TaskId) ||
		!bytes.Equal(header.AssignmentCandidateSetHash, assignment.AssignmentCandidateSetHash) {
		return assignmentWinnerComputation{}, errorsmod.Wrap(types.ErrInvariantBroken, "assignment candidate commitment mismatch")
	}
	if header.CandidateCount == 0 {
		return assignmentWinnerComputation{}, errEmptyAssignmentLegalSet
	}
	facts, err := k.loadWorkerCandidateFacts(ctx, taskID, header.CandidateCount)
	if err != nil {
		return assignmentWinnerComputation{}, err
	}
	if !shared.IsBlockHeightV1(randomnessHeight) {
		return assignmentWinnerComputation{}, errorsmod.Wrap(types.ErrInvariantBroken, "assignment randomness height exceeds int64")
	}
	beacon, ok := k.hubKeeper.GetBeaconForDomain(
		sdk.UnwrapSDKContext(ctx), shared.DomainWeightedDrawV1, int64(randomnessHeight),
	)
	if !ok || len(beacon.Randomness) != types.Hash32Len {
		return assignmentWinnerComputation{}, types.ErrBeaconNotFound
	}
	result, err := drawWeightedCandidate(
		sdk.UnwrapSDKContext(ctx).ChainID(), workerAssignmentPurposeV1,
		core.TaskId, assignment.AssignmentCandidateSetHash, randomnessHeight,
		beacon.Randomness, 0, maxAttempts, facts,
	)
	if err != nil {
		return assignmentWinnerComputation{}, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	digest, err := winnerDrawDigest(
		sdk.UnwrapSDKContext(ctx).ChainID(), core.TaskId,
		assignment.AssignmentCandidateSetHash, randomnessHeight, beacon.Randomness,
		result.AcceptedCounter, result.Fact,
	)
	if err != nil {
		return assignmentWinnerComputation{}, err
	}
	return assignmentWinnerComputation{
		Core: core, Assignment: assignment, Candidate: result.Fact,
		Beacon: append([]byte(nil), beacon.Randomness...), Digest: digest,
		Counter: result.AcceptedCounter,
	}, nil
}

// commitAssignmentWinner performs the §10.2 atomic write set.
func (k Keeper) commitAssignmentWinner(
	ctx context.Context,
	computation assignmentWinnerComputation,
	currentHeight uint64,
	candidateWeightPpmMax uint32,
) error {
	taskKey, err := taskStoreKey(computation.Core.TaskId)
	if err != nil {
		return err
	}
	// §10.2 lines 2798-2809: the liability reservation must succeed *before*
	// WORKER_ASSIGNED is written.
	if err := k.reserveWinnerTaskLiability(ctx, computation, currentHeight, candidateWeightPpmMax); err != nil {
		// §10.2 line 2808: a failed precheck goes down the deterministic
		// ASSIGN_TIMEOUT / refund path; it never redraws.
		return k.failAssignmentRandomness(ctx, computation.Core, computation.Assignment, currentHeight,
			types.AssignmentFailureReason_ASSIGNMENT_FAILURE_REASON_WINNER_LIABILITY_UNAVAILABLE)
	}

	if computation.Assignment.InferTimeoutBlocks == 0 {
		return errorsmod.Wrap(types.ErrInvariantBroken, "assignment has no frozen infer timeout")
	}
	inferDeadline, overflow := checkedHeightAdd(currentHeight, computation.Assignment.InferTimeoutBlocks)
	if overflow {
		return errorsmod.Wrap(types.ErrInvariantBroken, "infer deadline height overflow")
	}

	assignment := computation.Assignment
	assignment.WinnerWorker = computation.Candidate.OperatorAddress
	assignment.WinnerDrawDigest = computation.Digest[:]
	assignment.WinnerConfirmHeight = currentHeight
	assignment.InferDeadlineHeight = inferDeadline
	if err := k.WriteTaskAssignment(ctx, taskKey, assignment); err != nil {
		return err
	}

	core := computation.Core
	core.TaskPhase = types.TaskPhase_TASK_PHASE_WORKER_ASSIGNED
	core.AssignmentStatus = types.AssignmentStatus_ASSIGNMENT_STATUS_WORKER_ASSIGNED
	core.UpdatedHeight = currentHeight
	if err := k.TaskCore.Set(ctx, taskKey, core); err != nil {
		return err
	}

	if err := addDeadlineIndex(ctx, k.InferDeadlineIndex, taskKey, inferDeadline); err != nil {
		return err
	}
	if err := k.AddWorkerActiveTaskIndex(ctx, assignment.WinnerWorker, core.TaskId); err != nil {
		return err
	}

	reserved, err := k.reservedBudgetAmount(ctx, taskKey)
	if err != nil {
		return err
	}
	return emitTypedEvent(ctx, &types.EventWorkerAssignmentFinalized{
		SessionId:                  append([]byte(nil), core.SessionId...),
		TaskId:                     append([]byte(nil), core.TaskId...),
		WinnerWorker:               assignment.WinnerWorker,
		AssignmentCandidateSetHash: append([]byte(nil), assignment.AssignmentCandidateSetHash...),
		RandomnessHeight:           assignment.AssignmentRandomnessHeight,
		ReservedAmount:             reserved,
		InferDeadline:              inferDeadline,
	})
}

// reserveWinnerTaskLiability reserves from the accepted-time frozen fact; live
// stake or support changes cannot reinterpret the committed legal set.
func (k Keeper) reserveWinnerTaskLiability(
	ctx context.Context,
	computation assignmentWinnerComputation,
	currentHeight uint64,
	candidateWeightPpmMax uint32,
) error {
	if candidateWeightPpmMax == 0 || computation.Candidate.CandidateWeight == 0 || computation.Candidate.CandidateWeight > candidateWeightPpmMax {
		return errorsmod.Wrapf(types.ErrInvariantBroken,
			"frozen candidate weight %d is outside the registered interval", computation.Candidate.CandidateWeight)
	}
	requiredLiability, err := shared.ParseAmount(computation.Candidate.RequiredTaskLiabilitySnapshot)
	if err != nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, "frozen required task liability is invalid")
	}
	activeBond, err := shared.ParseAmount(computation.Candidate.ActiveBondSnapshot)
	if err != nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, "frozen active bond is invalid")
	}
	availableBond, err := shared.ParseAmount(computation.Candidate.AvailableBondSnapshot)
	if err != nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, "frozen available bond is invalid")
	}
	minStake, err := shared.ParseAmount(computation.Candidate.MinStakeSnapshot)
	if err != nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, "frozen minimum stake is invalid")
	}
	orderValue, err := shared.ParseAmount(computation.Core.OrderValue)
	if err != nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, "task order value is invalid")
	}
	_, err = k.hubKeeper.ReserveTaskLiabilityFromFrozenFact(ctx, hubtypes.FrozenFactLiabilityRequest{
		TaskID: computation.Core.TaskId, CandidatePoolSnapshotID: computation.Assignment.CandidatePoolSnapshotId,
		OperatorAddress: computation.Candidate.OperatorAddress, Duty: computation.Candidate.Duty,
		OrderValue: orderValue,
		Slot:       computation.Candidate.Slot, SlotVersion: computation.Candidate.SlotVersion,
		RequiredTaskLiability: requiredLiability, ActiveBondSnapshot: activeBond, AvailableBondSnapshot: availableBond,
		MinStakeSnapshot: minStake, BondVersionSnapshot: computation.Candidate.BondVersionSnapshot,
		CapabilityVersionSnapshot: computation.Candidate.CapabilityVersionSnapshot,
		SupportVersionSnapshot:    computation.Candidate.SupportVersionSnapshot,
		ModelID:                   computation.Core.ModelId, ProfileVersion: computation.Core.ProfileVersion, Height: currentHeight,
	})
	return err
}

// failAssignmentRandomness is the §10.2 "winner unavailable" deterministic path.
func (k Keeper) failAssignmentRandomness(
	ctx context.Context,
	core types.TaskCoreState,
	assignment types.TaskAssignmentState,
	currentHeight uint64,
	reason types.AssignmentFailureReason,
) error {
	taskKey, err := taskStoreKey(core.TaskId)
	if err != nil {
		return err
	}
	assignment.AssignmentFailReason = reason
	if err := k.WriteTaskAssignment(ctx, taskKey, assignment); err != nil {
		return err
	}
	// The §10.2 "winner cannot serve" failure path sets assignment_status =
	// ASSIGN_TIMEOUT. The
	// infer and verify-open timeouts already set their own sub-state before the
	// shared terminal writer; leaving this one at RANDOMNESS_PENDING made the row
	// say "final, but assignment still pending" and lost the distinction between
	// an assignment failure and a worker timeout.
	core.AssignmentStatus = types.AssignmentStatus_ASSIGNMENT_STATUS_ASSIGN_TIMEOUT
	result, err := k.writePreVerificationFailure(
		ctx, core, currentHeight,
		types.DeadlineKindV1_DEADLINE_KIND_V1_WORKER_ASSIGNMENT,
		types.TaskFailureClass_TASK_FAILURE_CLASS_NONE,
	)
	if err != nil {
		return err
	}
	if !result.Applied {
		return nil
	}
	return emitTypedEvent(ctx, &types.EventAssignmentFailed{
		SessionId:    append([]byte(nil), result.Core.SessionId...),
		TaskId:       append([]byte(nil), result.Core.TaskId...),
		Reason:       reason,
		RefundAmount: result.RefundAmount,
	})
}

func (k Keeper) reservedBudgetAmount(ctx context.Context, taskKey types.TaskBudgetKey) (shared.Amount, error) {
	budget, err := k.TaskBudget.Get(ctx, taskKey)
	if err != nil {
		if errIsNotFound(err) {
			return shared.NewAmount(0), nil
		}
		return shared.NewAmount(0), err
	}
	return budget.ReservedAmount, nil
}

// loadWorkerCandidateFacts reads the immutable OPEN_TASK frozen facts in slot
// ascending order. The commitment count is the hard bound.
func (k Keeper) loadWorkerCandidateFacts(ctx context.Context, taskID types.TaskKey, expected uint32) ([]types.TaskCandidateFactState, error) {
	iter, err := k.TaskCandidateFact.Iterate(ctx, collections.NewSuperPrefixedTripleRange[types.Hash32Key, int32, uint32](
		taskID, int32(types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK),
	))
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	facts := make([]types.TaskCandidateFactState, 0, expected)
	for ; iter.Valid(); iter.Next() {
		stored, err := iter.Value()
		if err != nil {
			return nil, err
		}
		fact, err := k.ProjectTaskCandidateFactStore(stored)
		if err != nil {
			return nil, err
		}
		facts = append(facts, fact)
		if uint32(len(facts)) > expected {
			return nil, errorsmod.Wrap(types.ErrInvariantBroken, "worker candidate facts exceed the committed count")
		}
	}
	if uint32(len(facts)) != expected {
		return nil, errorsmod.Wrapf(types.ErrInvariantBroken,
			"worker candidate fact count %d does not match commitment %d", len(facts), expected)
	}
	return facts, nil
}
