package keeper

import (
	"context"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// GetTaskSafetyWindows exposes the task-owned windows required by Hub
// governance when validating the service-bond unbonding horizon.
func (k Keeper) GetTaskSafetyWindows(ctx context.Context) (hubtypes.TaskSafetyWindows, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return hubtypes.TaskSafetyWindows{}, err
	}
	return taskSafetyWindowsFromParams(params), nil
}

func taskSafetyWindowsFromParams(types.TaskParamsV1) hubtypes.TaskSafetyWindows {
	// Challenge windows are not part of the fresh ACTIVE TaskParamsV1 schema.
	// The provider therefore contributes no unregistered challenge window.
	return hubtypes.TaskSafetyWindows{}
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
