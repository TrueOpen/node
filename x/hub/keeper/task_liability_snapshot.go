package keeper

import (
	"bytes"
	"context"
	"fmt"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// GetTaskLiabilityReservation exposes only the immutable slash basis needed by
// the Task round owner. Hub remains the sole owner of the reservation row.
func (k Keeper) GetTaskLiabilityReservation(
	ctx context.Context,
	taskID []byte,
	duty shared.Duty,
	operatorAddress string,
) (types.TaskLiabilityReservationSnapshot, error) {
	if len(taskID) != shared.Hash32KeySize || bytes.Equal(taskID, make([]byte, shared.Hash32KeySize)) {
		return types.TaskLiabilityReservationSnapshot{}, fmt.Errorf("task_id must be a non-zero Hash32")
	}
	if duty != shared.DutyWorker && duty != shared.DutyVerifier {
		return types.TaskLiabilityReservationSnapshot{}, fmt.Errorf("duty must be WORKER or VERIFIER")
	}
	_, operator, err := k.requireCanonicalAddress("operator_address", operatorAddress)
	if err != nil {
		return types.TaskLiabilityReservationSnapshot{}, err
	}
	row, err := k.ReadTaskLiabilityValue(ctx, types.NewTaskLiabilityReservationKey(taskID, duty, operator))
	if err != nil {
		return types.TaskLiabilityReservationSnapshot{}, err
	}
	if !bytes.Equal(row.TaskId, taskID) || row.OperatorAddress != operator || row.Duty != duty {
		return types.TaskLiabilityReservationSnapshot{}, fmt.Errorf("task liability reservation scope mismatch")
	}
	return types.TaskLiabilityReservationSnapshot{
		TaskID: append([]byte(nil), row.TaskId...), OperatorAddress: row.OperatorAddress,
		Duty: row.Duty, ReservedAmount: row.ReservedAmount,
		Reserved: row.Status == types.TaskLiabilityStatusReserved,
	}, nil
}
