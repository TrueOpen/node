package keeper

import (
	"context"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
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
	// The provider therefore contributes no unregistered K-BLOCK window.
	return hubtypes.TaskSafetyWindows{}
}

var _ hubtypes.TaskSafetyWindowProvider = Keeper{}
