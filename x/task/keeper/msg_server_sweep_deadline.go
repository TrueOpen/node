package keeper

import (
	"context"
	"math"
	"sort"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// deadlineKindPriority is the *only* copy of the keeper_api_contract.md §5.9 table
// (lines 970-984). It backs the single same-height ordering rule
// `deadline_height, kind_priority, primary_id` from §5.9 line 964, which both
// MsgSweepDeadline and EndBlock use. Value 0 (UNSPECIFIED) has no priority and
// is rejected before any state read.
//
//	| value | kind              | locator                        | kind_priority |
//	|     1 | TASK_FINALITY     | TaskDeadlineLocator            |            30 |
//	|     2 | TASK_SETTLEMENT   | TaskDeadlineLocator            |            40 |
//	|     4 | VERIFY_ROUND_CLOSE| TaskRoundDeadlineLocator       |            20 |
//	|     5 | VERIFY_REVEAL     | TaskDeadlineLocator            |            50 |
//	|     6 | VERIFY_COMMIT     | TaskDeadlineLocator            |            60 |
//	|     7 | VERIFY_OPEN       | TaskDeadlineLocator            |            70 |
//	|     8 | WORKER_INFER      | TaskDeadlineLocator            |            80 |
//	|     9 | WORKER_ASSIGNMENT | TaskDeadlineLocator            |            90 |
//	|    10 | SESSION_LIFECYCLE | SessionLifecycleLocator        |           100 |
//	|    11 | VERIFY_FINAL      | TaskDeadlineLocator            |            45 |
//	|    12 | CHALLENGE_CLOSE   | ChallengeDeadlineLocator       |            25 |
//	|    13 | EVIDENCE_CLEANUP  | TaskDeadlineLocator            |           110 |
var deadlineKindPriority = map[types.DeadlineKindV1]uint32{
	types.DeadlineKindV1_DEADLINE_KIND_V1_TASK_FINALITY:          30,
	types.DeadlineKindV1_DEADLINE_KIND_V1_TASK_SETTLEMENT:        40,
	types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_ROUND_CLOSE:     20,
	types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_REVEAL:          50,
	types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_COMMIT:          60,
	types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_OPEN:            70,
	types.DeadlineKindV1_DEADLINE_KIND_V1_WORKER_INFER:           80,
	types.DeadlineKindV1_DEADLINE_KIND_V1_WORKER_ASSIGNMENT:      90,
	types.DeadlineKindV1_DEADLINE_KIND_V1_SESSION_LIFECYCLE:      100,
	types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_FINAL:           45,
	types.DeadlineKindV1_DEADLINE_KIND_V1_CHALLENGE_WINDOW_CLOSE: 25,
	types.DeadlineKindV1_DEADLINE_KIND_V1_EVIDENCE_CLEANUP:       110,
}

// DeadlineKindPriority resolves the frozen §5.9 kind_priority. Unknown values
// are rejected (§5.9 line 968: "an unknown value is rejected before any state is
// read").
func DeadlineKindPriority(kind types.DeadlineKindV1) (uint32, error) {
	priority, ok := deadlineKindPriority[kind]
	if !ok {
		return 0, errorsmod.Wrapf(types.ErrInvalidTaskStatus, "unknown deadline kind %d", int32(kind))
	}
	return priority, nil
}

func endBlockDeadlinePriority(kind types.DeadlineKindV1) endBlockQueuePriority {
	priority, err := DeadlineKindPriority(kind)
	if err != nil {
		panic(err)
	}
	return endBlockQueuePriority(priority)
}

// deadlineWorkItem is a same-height sweep candidate.
type deadlineWorkItem struct {
	DeadlineHeight uint64
	Kind           types.DeadlineKindV1
	// PrimaryID is the canonical store key of the primary object (task_id,
	// challenge_id, request_id or session_id) — the §5.9 `primary_id` tiebreak.
	PrimaryID string
}

// SortDeadlineWorkItems implements the single §5.9 line 964 ordering rule
// `deadline_height, kind_priority, primary_id`. It is deliberately the only
// comparator in this module: any second ordering would let a proposer pick a
// different sweep order than EndBlock.
func SortDeadlineWorkItems(items []deadlineWorkItem) error {
	priorities := make(map[types.DeadlineKindV1]uint32, len(items))
	for _, item := range items {
		priority, err := DeadlineKindPriority(item.Kind)
		if err != nil {
			return err
		}
		priorities[item.Kind] = priority
	}
	sort.SliceStable(items, func(i, j int) bool {
		left, right := items[i], items[j]
		if left.DeadlineHeight != right.DeadlineHeight {
			return left.DeadlineHeight < right.DeadlineHeight
		}
		if priorities[left.Kind] != priorities[right.Kind] {
			return priorities[left.Kind] < priorities[right.Kind]
		}
		return left.PrimaryID < right.PrimaryID
	})
	return nil
}

// SweepDeadline is the single public failure/expiry entry point (§9.4, §10.14).
// V1 registers no MsgFailSettle and no second MsgSessionSweep.
func (m msgServer) SweepDeadline(ctx context.Context, req *types.MsgSweepDeadline) (*types.MsgSweepDeadlineResponse, error) {
	if req == nil || req.Locator == nil {
		return nil, errorsmod.Wrap(types.ErrInvalidTaskStatus, "locator is required")
	}
	// submitter_address is the Cosmos signer / gas payer only. §5.9 line 964: it
	// never enters the locator digest or any business state.
	if _, _, err := m.k.canonicalAddress("submitter_address", req.SubmitterAddress); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidUserAddress, err.Error())
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	currentHeight, err := currentBlockHeight(ctx)
	if err != nil {
		return nil, err
	}
	cacheCtx, commit := sdkCtx.CacheContext()
	response, err := m.sweepDeadline(sdk.WrapSDKContext(cacheCtx), req, currentHeight)
	if err != nil {
		return nil, err
	}
	commit()
	return response, nil
}

// sweepDeadline routes the frozen four-branch DeadlineLocatorV1 oneof. Every
// branch calls exactly the same internal executor EndBlock calls.
func (m msgServer) sweepDeadline(ctx context.Context, req *types.MsgSweepDeadline, currentHeight uint64) (*types.MsgSweepDeadlineResponse, error) {
	switch locator := req.Locator.GetLocator().(type) {
	// 1. TaskDeadlineLocator
	case *types.DeadlineLocatorV1_Task:
		if locator.Task == nil {
			return nil, errorsmod.Wrap(types.ErrInvalidTaskStatus, "task locator is required")
		}
		return m.k.sweepTaskDeadline(ctx, locator.Task, currentHeight)

	case *types.DeadlineLocatorV1_TaskRound:
		if locator.TaskRound == nil || len(locator.TaskRound.TaskId) != types.Hash32Len ||
			!isPhase0VerifyRound(locator.TaskRound.VerifyRound) ||
			locator.TaskRound.DeadlineKind != types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_ROUND_CLOSE {
			return nil, errorsmod.Wrap(types.ErrInvalidTaskStatus, "task-round locator is invalid")
		}
		taskKey := types.NewTaskKey(locator.TaskRound.TaskId)
		assignment, err := m.k.VerifierAssignment.Get(ctx, types.NewVerifyRoundKey(taskKey, locator.TaskRound.VerifyRound))
		if err != nil {
			return nil, err
		}
		visited, advanced, err := m.k.sweepSpecificTaskDeadline(ctx, m.k.VerifyDeadlineIndex, taskKey,
			assignment.VerifyDeadlineHeight, currentHeight,
			func(ctx context.Context, taskID types.TaskKey, deadline uint64) (deadlineSweepOutcome, uint64, error) {
				return m.k.handleExpiredVerifyDeadline(ctx, taskID, deadline, currentHeight)
			})
		if err != nil {
			return nil, err
		}
		return sweepDeadlineResponse(visited, advanced), nil

	// 4. SessionLifecycleLocator — §10.0b1 line 2332: shares the session
	// lifecycle function with EndBlock.
	case *types.DeadlineLocatorV1_SessionLifecycle:
		if locator.SessionLifecycle == nil {
			return nil, errorsmod.Wrap(types.ErrInvalidTaskStatus, "session_lifecycle locator is required")
		}
		return m.k.sweepSessionLifecycleLocator(ctx, locator.SessionLifecycle, currentHeight)

	default:
		return nil, errorsmod.Wrap(types.ErrInvalidTaskStatus, "exactly one deadline locator branch is required")
	}
}

// sweepTaskDeadline runs a single task-scoped deadline kind through the same
// bounded executor EndBlock uses, with a visited budget of one item.
func (k Keeper) sweepTaskDeadline(ctx context.Context, locator *types.TaskDeadlineLocator, currentHeight uint64) (*types.MsgSweepDeadlineResponse, error) {
	if _, err := DeadlineKindPriority(locator.DeadlineKind); err != nil {
		return nil, err
	}
	if len(locator.TaskId) != types.Hash32Len {
		return nil, errorsmod.Wrap(types.ErrInvalidTaskID, "task_id must be 32 bytes")
	}
	taskKey, err := taskStoreKey(locator.TaskId)
	if err != nil {
		return nil, err
	}
	if has, err := k.TaskCore.Has(ctx, taskKey); err != nil {
		return nil, err
	} else if !has {
		return nil, errorsmod.Wrapf(types.ErrTaskNotFound, "task %s", hex32(taskKey))
	}

	var visited, advanced uint64
	switch locator.DeadlineKind {
	case types.DeadlineKindV1_DEADLINE_KIND_V1_WORKER_ASSIGNMENT:
		assignment, loadErr := k.TaskAssignment.Get(ctx, taskKey)
		if loadErr != nil {
			return nil, loadErr
		}
		visited, advanced, err = k.sweepSpecificTaskDeadline(
			ctx, k.AssignmentRandomnessIndex, taskKey, assignment.AssignmentRandomnessHeight, currentHeight,
			func(ctx context.Context, taskID types.TaskKey, deadline uint64) (deadlineSweepOutcome, uint64, error) {
				return k.finalizeAssignmentRandomness(ctx, taskID, deadline, currentHeight)
			},
		)
	case types.DeadlineKindV1_DEADLINE_KIND_V1_WORKER_INFER:
		assignment, loadErr := k.TaskAssignment.Get(ctx, taskKey)
		if loadErr != nil {
			return nil, loadErr
		}
		visited, advanced, err = k.sweepSpecificTaskDeadline(
			ctx, k.InferDeadlineIndex, taskKey, assignment.InferDeadlineHeight, currentHeight,
			func(ctx context.Context, taskID types.TaskKey, deadline uint64) (deadlineSweepOutcome, uint64, error) {
				return k.handleExpiredInferDeadline(ctx, taskID, deadline, currentHeight)
			},
		)
	case types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_OPEN:
		// The locator carries only (task_id, kind) by design — §10.14's table
		// assigns VERIFY_OPEN to TaskDeadlineLocator. The height is not missing,
		// it is derived: handleExpiredVerifyOpenDeadline already picks the round
		// off TaskRoundSummary and reads assignment_deadline_height from the
		// VerifierCandidateWindow row, so the dispatcher resolves it exactly the
		// same way instead of reading the height-first KeySet head.
		window, loadErr := k.verifyOpenWindowForSweep(ctx, taskKey)
		if loadErr != nil {
			return nil, loadErr
		}
		visited, advanced, err = k.sweepSpecificTaskDeadline(
			ctx, k.VerifyOpenDeadlineIndex, taskKey, window.AssignmentDeadlineHeight, currentHeight,
			func(ctx context.Context, taskID types.TaskKey, deadline uint64) (deadlineSweepOutcome, uint64, error) {
				return k.handleExpiredVerifyOpenDeadline(ctx, taskID, deadline, currentHeight)
			},
		)
	case types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_COMMIT:
		// Same shape: activeVerifierAssignment finds the one round that is still
		// open, which is the round the locator does not need to name.
		_, assignment, loadErr := k.activeVerifierAssignment(ctx, taskKey)
		if loadErr != nil {
			return nil, errorsmod.Wrap(types.ErrInvalidTaskStatus, loadErr.Error())
		}
		visited, advanced, err = k.sweepSpecificTaskDeadline(
			ctx, k.CommitDeadlineIndex, taskKey, assignment.CommitDeadlineHeight, currentHeight,
			func(ctx context.Context, taskID types.TaskKey, deadline uint64) (deadlineSweepOutcome, uint64, error) {
				return k.handleExpiredCommitDeadline(ctx, taskID, deadline, currentHeight)
			},
		)
	case types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_REVEAL:
		_, assignment, loadErr := k.activeVerifierAssignment(ctx, taskKey)
		if loadErr != nil {
			return nil, errorsmod.Wrap(types.ErrInvalidTaskStatus, loadErr.Error())
		}
		visited, advanced, err = k.sweepSpecificTaskDeadline(
			ctx, k.RevealDeadlineIndex, taskKey, assignment.RevealDeadlineHeight, currentHeight,
			func(ctx context.Context, taskID types.TaskKey, deadline uint64) (deadlineSweepOutcome, uint64, error) {
				return k.handleExpiredRevealDeadline(ctx, taskID, deadline, currentHeight)
			},
		)
	case types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_FINAL:
		assignment, loadErr := k.VerifierAssignment.Get(ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1))
		if loadErr != nil {
			return nil, loadErr
		}
		visited, advanced, err = k.sweepSpecificTaskDeadline(
			ctx, k.VerifyDeadlineIndex, taskKey, assignment.VerifyDeadlineHeight, currentHeight,
			func(ctx context.Context, taskID types.TaskKey, deadline uint64) (deadlineSweepOutcome, uint64, error) {
				return k.handleExpiredVerifyDeadline(ctx, taskID, deadline, currentHeight)
			},
		)
	case types.DeadlineKindV1_DEADLINE_KIND_V1_TASK_FINALITY:
		return nil, errorsmod.Wrap(types.ErrInvalidTaskStatus, "TASK_FINALITY has no independent Phase 0 transition")
	case types.DeadlineKindV1_DEADLINE_KIND_V1_EVIDENCE_CLEANUP:
		// §10.14's table: EVIDENCE_CLEANUP "hits the §10.16 EvidenceCleanupIndex and
		// shares the same bounded cursor executor with EndBlock". The cleanup height is
		// the same
		// derivation ApplySettlementPlan used when it scheduled the row —
		// task_finality_height + max_evidence_retention_blocks — so the locator
		// does not need to carry it.
		core, loadErr := k.TaskCore.Get(ctx, taskKey)
		if loadErr != nil {
			return nil, loadErr
		}
		if core.XTaskFinalityHeight == nil {
			return nil, errorsmod.Wrap(types.ErrInvalidTaskStatus, "task has no finality height to schedule cleanup from")
		}
		params, loadErr := k.Params.Get(ctx)
		if loadErr != nil {
			return nil, loadErr
		}
		cleanupHeight, overflow := checkedHeightAdd(core.GetTaskFinalityHeight(), params.Evidence.MaxEvidenceRetentionBlocks)
		if overflow {
			return nil, errorsmod.Wrap(types.ErrInvariantBroken, "evidence cleanup height overflows")
		}
		visited, advanced, err = k.sweepSpecificTaskDeadline(
			ctx, k.EvidenceCleanupIndex, taskKey, cleanupHeight, currentHeight,
			func(ctx context.Context, taskID types.TaskKey, deadline uint64) (deadlineSweepOutcome, uint64, error) {
				// The shared bounded executor, one task at a time.
				progress, cleanupErr := k.runTaskCleanup(ctx, core.SessionId, taskID, deadline,
					uint64(params.Cleanup.MaxTaskCleanupItemsPerBlock))
				if cleanupErr != nil {
					return deadlineSweepPending, 0, cleanupErr
				}
				if progress.processed == 0 {
					// Pending, not stale. runTaskCleanup owns this index's rows —
					// it removes the row when the task is fully cleaned and
					// re-schedules a retry row otherwise (task_cleanup.go:33/69/72)
					// — so the generic sweep must never retire one on its behalf.
					// deadlineSweepStale would do exactly that, and the EndBlock
					// executor makes the same choice by simply breaking out.
					return deadlineSweepPending, taskDeadlineConservativeBytesV1, nil
				}
				return deadlineSweepAdvanced, taskDeadlineConservativeBytesV1, nil
			},
		)
	case types.DeadlineKindV1_DEADLINE_KIND_V1_TASK_SETTLEMENT:
		summary, loadErr := k.TaskRoundSummary.Get(ctx, taskKey)
		if loadErr != nil || summary.XRoundsClosedHeight == nil {
			return nil, errorsmod.Wrap(types.ErrInvalidTaskStatus, "task rounds are not finalized")
		}
		params, loadErr := k.Params.Get(ctx)
		if loadErr != nil {
			return nil, loadErr
		}
		deadline, deadlineErr := settlementDeadlineHeight(summary.GetRoundsClosedHeight(), params)
		if deadlineErr != nil {
			return nil, deadlineErr
		}
		visited, advanced, err = k.sweepSpecificTaskDeadline(ctx, k.SettlementDeadlineIndex, taskKey, deadline, currentHeight,
			func(ctx context.Context, taskID types.TaskKey, deadline uint64) (deadlineSweepOutcome, uint64, error) {
				applied, err := k.settleTaskFromDeadline(ctx, taskID, deadline, currentHeight)
				if err != nil {
					return deadlineSweepPending, 0, err
				}
				if applied {
					if err := removeDeadlineIndex(ctx, k.SettlementDeadlineIndex, taskID, deadline); err != nil {
						return deadlineSweepPending, 0, err
					}
					return deadlineSweepAdvanced, taskDeadlineConservativeBytesV1, nil
				}
				return deadlineSweepStale, taskDeadlineConservativeBytesV1, nil
			})
	case types.DeadlineKindV1_DEADLINE_KIND_V1_CHALLENGE_WINDOW_CLOSE:
		summary, loadErr := k.TaskRoundSummary.Get(ctx, taskKey)
		if loadErr != nil || summary.XChallengeCloseHeight == nil {
			return nil, errorsmod.Wrap(types.ErrInvalidTaskStatus, "challenge window is unavailable")
		}
		deadline := summary.GetChallengeCloseHeight()
		visited, advanced, err = k.sweepSpecificTaskDeadline(ctx, k.ChallengeWindowCloseIndex, taskKey, deadline, currentHeight,
			func(ctx context.Context, taskID types.TaskKey, deadline uint64) (deadlineSweepOutcome, uint64, error) {
				if err := k.finalizeTaskRounds(ctx, taskID); err != nil {
					return deadlineSweepPending, 0, err
				}
				if err := removeDeadlineIndex(ctx, k.ChallengeWindowCloseIndex, taskID, deadline); err != nil {
					return deadlineSweepPending, 0, err
				}
				return deadlineSweepAdvanced, taskDeadlineConservativeBytesV1, nil
			})
	default:
		return nil, errorsmod.Wrap(types.ErrInvalidTaskStatus, "deadline kind is not routed by TaskDeadlineLocator")
	}

	if err != nil {
		return nil, err
	}
	if visited == 0 {
		// §10.14 rule 5: nothing due for this kind at this height.
		return nil, errorsmod.Wrapf(types.ErrInvalidTaskStatus, "no due %s row at height %d", locator.DeadlineKind, currentHeight)
	}
	return sweepDeadlineResponse(visited, advanced), nil
}

// sweepSessionLifecycleLocator implements the frozen ByIDV1 / BatchV1 oneof.
func (k Keeper) sweepSessionLifecycleLocator(ctx context.Context, locator *types.SessionLifecycleLocator, currentHeight uint64) (*types.MsgSweepDeadlineResponse, error) {
	switch target := locator.GetTarget().(type) {
	case *types.SessionLifecycleLocator_ById:
		if target.ById == nil || len(target.ById.Id) != types.Hash32Len {
			return nil, errorsmod.Wrap(types.ErrInvalidSessionID, "session_id must be 32 bytes")
		}
		sessionKey, err := sessionStoreKey(target.ById.Id)
		if err != nil {
			return nil, err
		}
		if has, err := k.Stream.Has(ctx, sessionKey); err != nil {
			return nil, err
		} else if !has {
			return nil, errorsmod.Wrapf(types.ErrInvalidSessionID, "session %s", hex32(sessionKey))
		}
		sweep, err := k.SweepSessionLifecycleByID(ctx, sessionKey, currentHeight)
		if err != nil {
			return nil, err
		}
		if sweep.SweptCount == 0 {
			return nil, errorsmod.Wrapf(types.ErrInvalidTaskStatus, "session %s has no due lifecycle row", hex32(sessionKey))
		}
		return sweepDeadlineResponse(sweep.SweptCount, sweep.MarkedIdleCount+sweep.ClosedCount), nil

	case *types.SessionLifecycleLocator_Batch:
		if target.Batch == nil || target.Batch.MaxItems == 0 {
			return nil, errorsmod.Wrap(types.ErrInvalidSessionID, "batch max_items must be positive")
		}
		sweep, err := k.SweepExpiredSessionLifecycle(ctx, currentHeight, uint64(target.Batch.MaxItems))
		if err != nil {
			return nil, err
		}
		return sweepDeadlineResponse(sweep.SweptCount, sweep.MarkedIdleCount+sweep.ClosedCount), nil

	default:
		return nil, errorsmod.Wrap(types.ErrInvalidSessionID, "exactly one session lifecycle target is required")
	}
}

func sweepDeadlineResponse(visited, advanced uint64) *types.MsgSweepDeadlineResponse {
	status := shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED
	if advanced == 0 {
		status = shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP
	}
	return &types.MsgSweepDeadlineResponse{
		Visited:  clampUint32(visited),
		Advanced: clampUint32(advanced),
		Status:   status,
	}
}

func clampUint32(value uint64) uint32 {
	if value > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(value)
}
