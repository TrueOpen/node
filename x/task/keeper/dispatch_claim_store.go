package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	shared "github.com/TrueOpen/node/x/shared/types"
	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) dispatchClaimToStore(row types.DispatchCompensationClaimState) (internaltypes.DispatchCompensationClaimStoreState, error) {
	var state internaltypes.DispatchCompensationClaimStoreState
	if err := types.ValidateDispatchClaim(row); err != nil {
		return state, err
	}
	builder, err := k.sessionAddressToStore("claim Builder", row.BuilderAddress)
	if err != nil {
		return state, err
	}
	worker, err := k.sessionAddressToStore("claim Worker", row.WorkerOperatorAddress)
	if err != nil {
		return state, err
	}
	var rebutter []byte
	if address := row.GetRebuttedByOrEmpty(); address != "" {
		rebutter, err = k.sessionAddressToStore("claim rebutter", address)
		if err != nil {
			return state, err
		}
	}
	amount, _ := shared.ParseAmount(row.Amount)
	paid, _ := shared.ParseAmount(row.PaidAmount)
	unfilled, _ := shared.ParseAmount(row.UnfilledAmount)
	return internaltypes.DispatchCompensationClaimStoreState{DispatchTicketHash: bytes.Clone(row.DispatchTicketHash), TaskId: bytes.Clone(row.TaskId), SessionId: bytes.Clone(row.SessionId), OrderSequence: row.OrderSequence, BuilderAddress: builder, BuilderSetId: row.BuilderSetId, WorkerOperatorAddress: worker, Amount: amount, Status: int32(row.Status), ClaimedHeight: row.ClaimedHeight, RebuttalDeadlineHeight: row.RebuttalDeadlineHeight, PaidAmount: paid, UnfilledAmount: unfilled, RebuttedByOrEmpty: rebutter, ResolvedHeight: row.GetResolvedHeight(), ClaimWindowCloseHeight: row.ClaimWindowCloseHeight, EarliestSubmitHeight: row.EarliestSubmitHeight}, nil
}

// ProjectDispatchClaimStore restores the public projection from the sole compact primary.
func (k Keeper) ProjectDispatchClaimStore(state internaltypes.DispatchCompensationClaimStoreState) (types.DispatchCompensationClaimState, error) {
	var row types.DispatchCompensationClaimState
	builder, err := k.sessionAddressFromStore("claim Builder", state.BuilderAddress)
	if err != nil {
		return row, err
	}
	worker, err := k.sessionAddressFromStore("claim Worker", state.WorkerOperatorAddress)
	if err != nil {
		return row, err
	}
	row = types.DispatchCompensationClaimState{DispatchTicketHash: bytes.Clone(state.DispatchTicketHash), TaskId: bytes.Clone(state.TaskId), SessionId: bytes.Clone(state.SessionId), OrderSequence: state.OrderSequence, BuilderAddress: builder, BuilderSetId: state.BuilderSetId, WorkerOperatorAddress: worker, Amount: shared.NewAmount(state.Amount), Status: types.DispatchCompensationClaimStatusV1(state.Status), ClaimedHeight: state.ClaimedHeight, RebuttalDeadlineHeight: state.RebuttalDeadlineHeight, PaidAmount: shared.NewAmount(state.PaidAmount), UnfilledAmount: shared.NewAmount(state.UnfilledAmount), ClaimWindowCloseHeight: state.ClaimWindowCloseHeight, EarliestSubmitHeight: state.EarliestSubmitHeight}
	if len(state.RebuttedByOrEmpty) != 0 {
		address, err := k.sessionAddressFromStore("claim rebutter", state.RebuttedByOrEmpty)
		if err != nil {
			return row, err
		}
		row.XRebuttedByOrEmpty = &types.DispatchCompensationClaimState_RebuttedByOrEmpty{RebuttedByOrEmpty: address}
	}
	if state.ResolvedHeight != 0 {
		row.XResolvedHeight = &types.DispatchCompensationClaimState_ResolvedHeight{ResolvedHeight: state.ResolvedHeight}
	}
	return row, types.ValidateDispatchClaim(row)
}

func (k Keeper) readDispatchClaim(ctx context.Context, ticket []byte) (types.DispatchCompensationClaimState, error) {
	state, err := k.DispatchCompensationClaim.Get(ctx, ticket)
	if err != nil {
		return types.DispatchCompensationClaimState{}, err
	}
	if !bytes.Equal(state.DispatchTicketHash, ticket) {
		return types.DispatchCompensationClaimState{}, fmt.Errorf("claim key/ticket mismatch")
	}
	return k.ProjectDispatchClaimStore(state)
}

func (k Keeper) writeDispatchClaim(ctx context.Context, row types.DispatchCompensationClaimState) error {
	state, err := k.dispatchClaimToStore(row)
	if err != nil {
		return err
	}
	old, err := k.DispatchCompensationClaim.Get(ctx, row.DispatchTicketHash)
	wasPending := err == nil
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	if err == nil {
		if state.EarliestSubmitHeight != old.EarliestSubmitHeight {
			return fmt.Errorf("compensation claim order height is immutable")
		}
		if old.Status != int32(types.DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_PENDING_REBUTTAL) {
			return fmt.Errorf("terminal compensation claim is immutable")
		}
		if err := k.DispatchClaimRebuttal.Remove(ctx, collections.Join(old.RebuttalDeadlineHeight, old.DispatchTicketHash)); err != nil {
			return err
		}
		if err := k.DispatchOpenClaimByBuilder.Remove(ctx, collections.Join(old.BuilderAddress, old.DispatchTicketHash)); err != nil {
			return err
		}
	}
	if row.Status == types.DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_PENDING_REBUTTAL {
		if err := k.DispatchClaimRebuttal.Set(ctx, collections.Join(row.RebuttalDeadlineHeight, row.DispatchTicketHash)); err != nil {
			return err
		}
		if err := k.DispatchOpenClaimByBuilder.Set(ctx, collections.Join(state.BuilderAddress, row.DispatchTicketHash)); err != nil {
			return err
		}
	} else {
		params, err := k.Params.Get(ctx)
		if err != nil {
			return err
		}
		prune, overflow := shared.CheckedHeightAddV1(row.GetResolvedHeight(), params.Dispatch.DispatchRecordRetentionBlocks)
		if overflow {
			return fmt.Errorf("claim retention height overflows")
		}
		prune = max(prune, row.ClaimWindowCloseHeight)
		if err := k.DispatchClaimPrune.Set(ctx, collections.Join(prune, row.DispatchTicketHash)); err != nil {
			return err
		}
	}
	if err := k.adjustDispatchPendingCount(ctx, row.TaskId, wasPending, row.Status == types.DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_PENDING_REBUTTAL); err != nil {
		return err
	}
	return k.DispatchCompensationClaim.Set(ctx, row.DispatchTicketHash, state)
}
