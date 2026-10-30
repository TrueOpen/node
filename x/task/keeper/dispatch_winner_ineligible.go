package keeper

import (
	"bytes"
	"context"
	"fmt"

	"cosmossdk.io/collections"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

type dispatchWinnerIneligibleError struct {
	reason types.DispatchWinnerIneligibleReasonV1
}

func (e *dispatchWinnerIneligibleError) Error() string {
	return "authenticated dispatch winner is ineligible: " + e.reason.String()
}
func (e *dispatchWinnerIneligibleError) Unwrap() error {
	return rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_WINNER_NOT_ELIGIBLE, fmt.Errorf("winner self-ineligibility"))
}

// recordDispatchWinnerIneligible is the sole rejected-entry write exception.
// The entry cache has already been discarded; the batch cache owns both this
// receipt and the count-only fault, so a later fatal error still rolls them back.
func (k Keeper) recordDispatchWinnerIneligible(ctx context.Context, entry *types.DispatchEntryV1, reason types.DispatchWinnerIneligibleReasonV1, params types.DispatchParamsV1, height uint64) error {
	ticket, order := entry.WinnerTicket, entry.SignedOrder.Order
	hash, err := dispatchMaterialDigest("task.v1.DispatchTicketV1", ticket)
	if err != nil {
		return err
	}
	if old, err := k.DispatchWinnerIneligible.Get(ctx, hash[:]); err == nil {
		if !bytes.Equal(old.TaskId, ticket.TaskId) {
			return fmt.Errorf("ineligible winner receipt scope conflict")
		}
		return nil
	} else if !errIsNotFound(err) {
		return err
	}
	_, closeHeight, err := dispatchClaimWindow(order.EarliestSubmitHeight, ticket.BoundChangeHeight, params)
	if err != nil {
		return err
	}
	row := types.DispatchWinnerIneligibleState{DispatchTicketHash: hash[:], TaskId: bytes.Clone(ticket.TaskId), WorkerOperatorAddress: ticket.Target.OperatorAddress, Reason: reason, RejectedHeight: height, ClaimWindowCloseHeight: closeHeight}
	if err := k.writeDispatchWinnerIneligible(ctx, row); err != nil {
		return err
	}
	_, err = k.hubKeeper.ApplyTaskRoleFault(ctx, hubtypes.TaskRoleFaultFact{SessionID: order.SessionId, TaskID: ticket.TaskId, OperatorAddress: ticket.Target.OperatorAddress, Duty: shared.DutyWorker, FaultType: hubtypes.FaultTypeWorkerDispatchIneligible, ClassificationSource: shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_OBJECTIVE_EVIDENCE, EvidenceDigest: hash[:], SlashBps: 0, Height: height})
	return err
}

func (k Keeper) writeDispatchWinnerIneligible(ctx context.Context, row types.DispatchWinnerIneligibleState) error {
	if err := types.ValidateDispatchWinnerIneligible(row); err != nil {
		return err
	}
	worker, err := k.sessionAddressToStore("ineligible Worker", row.WorkerOperatorAddress)
	if err != nil {
		return err
	}
	if err := k.DispatchWinnerIneligible.Set(ctx, row.DispatchTicketHash, internaltypes.DispatchWinnerIneligibleStoreState{DispatchTicketHash: bytes.Clone(row.DispatchTicketHash), TaskId: bytes.Clone(row.TaskId), WorkerOperatorAddress: worker, Reason: int32(row.Reason), RejectedHeight: row.RejectedHeight, ClaimWindowCloseHeight: row.ClaimWindowCloseHeight}); err != nil {
		return err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	prune, overflow := shared.CheckedHeightAddV1(row.RejectedHeight, params.Dispatch.DispatchRecordRetentionBlocks)
	if overflow {
		return fmt.Errorf("winner exclusion retention overflows")
	}
	return k.DispatchWinnerIneligiblePrune.Set(ctx, collections.Join(max(prune, row.ClaimWindowCloseHeight), row.DispatchTicketHash))
}

// ProjectDispatchWinnerIneligibleStore restores the public exclusion receipt.
func (k Keeper) ProjectDispatchWinnerIneligibleStore(state internaltypes.DispatchWinnerIneligibleStoreState) (types.DispatchWinnerIneligibleState, error) {
	worker, err := k.sessionAddressFromStore("ineligible Worker", state.WorkerOperatorAddress)
	if err != nil {
		return types.DispatchWinnerIneligibleState{}, err
	}
	row := types.DispatchWinnerIneligibleState{DispatchTicketHash: bytes.Clone(state.DispatchTicketHash), TaskId: bytes.Clone(state.TaskId), WorkerOperatorAddress: worker, Reason: types.DispatchWinnerIneligibleReasonV1(state.Reason), RejectedHeight: state.RejectedHeight, ClaimWindowCloseHeight: state.ClaimWindowCloseHeight}
	return row, types.ValidateDispatchWinnerIneligible(row)
}
