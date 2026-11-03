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
	return internaltypes.DispatchCompensationClaimStoreState{DispatchTicketHash: bytes.Clone(row.DispatchTicketHash), TaskId: bytes.Clone(row.TaskId), SessionId: bytes.Clone(row.SessionId), OrderSequence: row.OrderSequence, BuilderAddress: builder, BuilderSetId: row.BuilderSetId, WorkerOperatorAddress: worker, Amount: amount, Status: int32(row.Status), ClaimedHeight: row.ClaimedHeight, RebuttalDeadlineHeight: row.RebuttalDeadlineHeight, PaidAmount: paid, UnfilledAmount: unfilled, RebuttedByOrEmpty: rebutter, ResolvedHeight: row.GetResolvedHeight(), ClaimWindowCloseHeight: row.ClaimWindowCloseHeight, EarliestSubmitHeight: row.EarliestSubmitHeight, BoundChangeHeight: row.BoundChangeHeight}, nil
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
	row = types.DispatchCompensationClaimState{DispatchTicketHash: bytes.Clone(state.DispatchTicketHash), TaskId: bytes.Clone(state.TaskId), SessionId: bytes.Clone(state.SessionId), OrderSequence: state.OrderSequence, BuilderAddress: builder, BuilderSetId: state.BuilderSetId, WorkerOperatorAddress: worker, Amount: shared.NewAmount(state.Amount), Status: types.DispatchCompensationClaimStatusV1(state.Status), ClaimedHeight: state.ClaimedHeight, RebuttalDeadlineHeight: state.RebuttalDeadlineHeight, PaidAmount: shared.NewAmount(state.PaidAmount), UnfilledAmount: shared.NewAmount(state.UnfilledAmount), ClaimWindowCloseHeight: state.ClaimWindowCloseHeight, EarliestSubmitHeight: state.EarliestSubmitHeight, BoundChangeHeight: state.BoundChangeHeight}
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

func (k Keeper) dispatchSessionParams(ctx context.Context, session []byte) (types.TaskParamsV1, error) {
	exists, err := k.Stream.Has(ctx, session)
	if err != nil {
		return types.TaskParamsV1{}, err
	}
	if exists {
		return k.ParamsForSession(ctx, session)
	}
	// Independent terminal receipts can outlive the Session and its binding.
	return k.Params.Get(ctx)
}

func (k Keeper) readDispatchClaimBySequence(ctx context.Context, session []byte, sequence uint64) (types.DispatchCompensationClaimState, error) {
	state, err := k.DispatchCompensationClaim.Get(ctx, types.NewOrderSequenceStateKey(session, sequence))
	if err != nil {
		return types.DispatchCompensationClaimState{}, err
	}
	if !bytes.Equal(state.SessionId, session) || state.OrderSequence != sequence {
		return types.DispatchCompensationClaimState{}, fmt.Errorf("claim key/sequence mismatch")
	}
	return k.ProjectDispatchClaimStore(state)
}

func (k Keeper) readDispatchClaim(ctx context.Context, ticket []byte) (types.DispatchCompensationClaimState, error) {
	encoded, err := k.DispatchClaimByTicket.Get(ctx, ticket)
	if err != nil {
		return types.DispatchCompensationClaimState{}, err
	}
	n, key, err := k.DispatchCompensationClaim.KeyCodec().Decode(encoded)
	if err != nil {
		return types.DispatchCompensationClaimState{}, fmt.Errorf("invalid claim ticket index key: %w", err)
	}
	if n != len(encoded) {
		return types.DispatchCompensationClaimState{}, fmt.Errorf("noncanonical claim ticket index key")
	}
	row, err := k.readDispatchClaimBySequence(ctx, key.K1(), key.K2())
	if err != nil {
		return row, fmt.Errorf("claim ticket index lost its primary: %w", err)
	}
	if !bytes.Equal(row.DispatchTicketHash, ticket) {
		return row, fmt.Errorf("claim ticket index/primary mismatch")
	}
	return row, nil
}

func (k Keeper) writeDispatchClaim(ctx context.Context, row types.DispatchCompensationClaimState) error {
	state, err := k.dispatchClaimToStore(row)
	if err != nil {
		return err
	}
	key := types.NewOrderSequenceStateKey(row.SessionId, row.OrderSequence)
	encoded, err := indexKeyBytes(k.DispatchCompensationClaim.KeyCodec(), key)
	if err != nil {
		return err
	}
	if indexed, err := k.DispatchClaimByTicket.Get(ctx, row.DispatchTicketHash); err == nil {
		if !bytes.Equal(indexed, []byte(encoded)) {
			return fmt.Errorf("ticket already indexes another claim sequence")
		}
	} else if !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	old, err := k.DispatchCompensationClaim.Get(ctx, key)
	exists := err == nil
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	wasPending := exists && old.Status == int32(types.DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_PENDING_REBUTTAL)
	if exists {
		if bytes.Equal(old.DispatchTicketHash, state.DispatchTicketHash) {
			if !wasPending {
				return fmt.Errorf("terminal compensation claim is immutable")
			}
			if state.EarliestSubmitHeight != old.EarliestSubmitHeight || state.BoundChangeHeight != old.BoundChangeHeight ||
				!bytes.Equal(state.TaskId, old.TaskId) || !bytes.Equal(state.BuilderAddress, old.BuilderAddress) || !bytes.Equal(state.WorkerOperatorAddress, old.WorkerOperatorAddress) ||
				state.BuilderSetId != old.BuilderSetId || state.Amount != old.Amount || state.ClaimedHeight != old.ClaimedHeight || state.RebuttalDeadlineHeight != old.RebuttalDeadlineHeight || state.ClaimWindowCloseHeight != old.ClaimWindowCloseHeight {
				return fmt.Errorf("compensation claim original facts are immutable")
			}
		} else if old.Status != int32(types.DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_DISMISSED) || row.Status != types.DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_PENDING_REBUTTAL {
			return fmt.Errorf("only a dismissed sequence claim may be replaced")
		}
		if wasPending {
			if err := k.DispatchClaimRebuttal.Remove(ctx, collections.Join(old.RebuttalDeadlineHeight, old.DispatchTicketHash)); err != nil {
				return err
			}
			if err := k.DispatchOpenClaimByBuilder.Remove(ctx, collections.Join(old.BuilderAddress, old.DispatchTicketHash)); err != nil {
				return err
			}
		} else {
			prune, err := k.dispatchClaimPruneHeight(ctx, old.SessionId, old.ResolvedHeight, old.ClaimWindowCloseHeight)
			if err != nil {
				return err
			}
			if err := k.DispatchClaimPrune.Remove(ctx, collections.Join(prune, old.DispatchTicketHash)); err != nil {
				return err
			}
		}
		if err := k.DispatchClaimByTicket.Remove(ctx, old.DispatchTicketHash); err != nil {
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
		prune, err := k.dispatchClaimPruneHeight(ctx, row.SessionId, row.GetResolvedHeight(), row.ClaimWindowCloseHeight)
		if err != nil {
			return err
		}
		if err := k.DispatchClaimPrune.Set(ctx, collections.Join(prune, row.DispatchTicketHash)); err != nil {
			return err
		}
	}
	if err := k.adjustDispatchPendingCount(ctx, row.TaskId, wasPending, row.Status == types.DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_PENDING_REBUTTAL); err != nil {
		return err
	}
	if err := k.DispatchClaimByTicket.Set(ctx, row.DispatchTicketHash, []byte(encoded)); err != nil {
		return err
	}
	return k.DispatchCompensationClaim.Set(ctx, key, state)
}

func (k Keeper) markDispatchCompensationPaid(ctx context.Context, row types.DispatchCompensationClaimState) error {
	key := types.NewOrderSequenceStateKey(row.SessionId, row.OrderSequence)
	sequence, err := k.OrderSequence.Get(ctx, key)
	if err != nil {
		if !errors.Is(err, collections.ErrNotFound) {
			return err
		}
		sequence = types.OrderSequenceState{SessionId: bytes.Clone(row.SessionId), OrderSequence: row.OrderSequence, Status: types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_UNUSED}
	}
	if sequence.CompensationPaid {
		return fmt.Errorf("sequence compensation was already paid")
	}
	sequence.CompensationPaid = true
	return k.OrderSequence.Set(ctx, key, sequence)
}

// Replacement is possible only while the Session exists, so its fixed policy
// reconstructs the exact terminal schedule without a duplicate stored clock.
func (k Keeper) dispatchClaimPruneHeight(ctx context.Context, session []byte, resolved, window uint64) (uint64, error) {
	params, err := k.dispatchSessionParams(ctx, session)
	if err != nil {
		return 0, err
	}
	height, overflow := shared.CheckedHeightAddV1(resolved, params.Dispatch.DispatchRecordRetentionBlocks)
	if overflow {
		return 0, fmt.Errorf("claim retention height overflows")
	}
	return max(height, window), nil
}
