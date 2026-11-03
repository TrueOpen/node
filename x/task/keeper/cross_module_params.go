package keeper

import (
	"context"
	"fmt"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// GetTaskSafetyWindows exposes the task-owned windows required by Hub
// governance and cross-module import checks for verifier clocks and slashability.
func (k Keeper) GetTaskSafetyWindows(ctx context.Context) (hubtypes.TaskSafetyWindows, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return hubtypes.TaskSafetyWindows{}, err
	}
	windows, err := taskSafetyWindowsFromParams(params)
	if err != nil {
		return windows, err
	}
	// Governance visits parameter versions, never every live Task or Session.
	// A referenced old body may still supply a not-yet-opened verifier clock.
	err = k.ParamsHistory.Walk(ctx, nil, func(version uint64, row types.TaskParamsHistoryState) (bool, error) {
		refs, err := k.paramsVersionRefCount(ctx, version)
		if err != nil || refs == 0 {
			return false, err
		}
		old, err := taskSafetyWindowsFromParams(row.Params)
		if err == nil {
			windows = mergeTaskSafetyWindows(windows, old)
		}
		return false, err
	})
	return windows, err
}

func taskSafetyWindowsFromParams(params types.TaskParamsV1) (hubtypes.TaskSafetyWindows, error) {
	p, d := params.Challenge, params.Dispatch
	challenge, overflow := shared.CheckedSumUint64(p.CandidateWindowDelayBlocks, p.HandraiseWindowBlocks, p.AssignmentDelayBlocks, p.CommitWindowBlocks, p.RevealWindowBlocks)
	if overflow {
		return hubtypes.TaskSafetyWindows{}, fmt.Errorf("challenge safety window overflows")
	}
	dispatch, overflow := shared.CheckedSumUint64(d.DispatchChallengeWindowBlocks, d.DispatchChallengeResponseBlocks)
	if overflow {
		return hubtypes.TaskSafetyWindows{}, fmt.Errorf("dispatch safety window overflows")
	}
	claim, overflow := shared.CheckedSumUint64(d.RecordDeadlineBlocks, d.DispatchCompensationClaimWindowBlocks, d.DispatchCompensationRebuttalBlocks)
	if overflow || params.Deadlines.VerifyOpenDeadlineBlocks <= params.Deadlines.SelfRescueMarginBlocks {
		return hubtypes.TaskSafetyWindows{}, fmt.Errorf("task safety clock is invalid")
	}
	takeoverSpan, overflow := shared.CheckedMulUint64(d.RecordDeadlineBlocks, 2)
	if overflow {
		return hubtypes.TaskSafetyWindows{}, fmt.Errorf("takeover safety window overflows")
	}
	notice, overflow := shared.CheckedAddUint64(max(d.MaxOrderLifetimeBlocks, takeoverSpan), 1)
	if overflow {
		return hubtypes.TaskSafetyWindows{}, fmt.Errorf("BuilderSet notice safety window overflows")
	}
	return hubtypes.TaskSafetyWindows{
		ChallengeResolveWindowBlocks: max(challenge, dispatch, claim),
		EvidenceResponseWindowBlocks: params.Evidence.VerifierValueEvidenceWindowBlocks,
		VerifyOpenDeadlineBlocks:     params.Deadlines.VerifyOpenDeadlineBlocks,
		SelfRescueMarginBlocks:       params.Deadlines.SelfRescueMarginBlocks,
		MinimumBuilderSetLeadBlocks:  notice,
	}, nil
}

func mergeTaskSafetyWindows(left, right hubtypes.TaskSafetyWindows) hubtypes.TaskSafetyWindows {
	left.ChallengeResolveWindowBlocks = max(left.ChallengeResolveWindowBlocks, right.ChallengeResolveWindowBlocks)
	left.EvidenceResponseWindowBlocks = max(left.EvidenceResponseWindowBlocks, right.EvidenceResponseWindowBlocks)
	left.MinimumBuilderSetLeadBlocks = max(left.MinimumBuilderSetLeadBlocks, right.MinimumBuilderSetLeadBlocks)
	if right.VerifyOpenDeadlineBlocks-right.SelfRescueMarginBlocks < left.VerifyOpenDeadlineBlocks-left.SelfRescueMarginBlocks {
		left.VerifyOpenDeadlineBlocks, left.SelfRescueMarginBlocks = right.VerifyOpenDeadlineBlocks, right.SelfRescueMarginBlocks
	}
	return left
}

var _ hubtypes.TaskSafetyWindowProvider = Keeper{}

// GetWorkerCandidateWeightPolicy supplies the policy effective at an epoch reference height.
// Historical queries and challenges read the header instead of calling this adapter.
func (k Keeper) GetWorkerCandidateWeightPolicy(ctx context.Context, referenceHeight uint64) (shared.WorkerCandidateWeightPolicy, error) {
	params, err := k.paramsAtHeight(ctx, referenceHeight)
	if err != nil {
		return shared.WorkerCandidateWeightPolicy{}, err
	}
	policy := shared.WorkerCandidateWeightPolicy{
		StakeWeightPPM:       params.Weights.WorkerStakeWeightPpm,
		PerformanceWeightPPM: params.Weights.WorkerPerformanceWeightPpm,
	}
	return policy, policy.Validate()
}

var _ hubtypes.WorkerCandidateWeightProvider = Keeper{}
