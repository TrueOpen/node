package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// ClaimDispatchCompensation authenticates the signed ticket and acceptance, but
// deliberately does not re-authorize an old order against today's Session key.
func (m msgServer) ClaimDispatchCompensation(ctx context.Context, req *types.MsgClaimDispatchCompensation) (*types.MsgClaimDispatchCompensationResponse, error) {
	if req == nil || req.Order == nil || req.Ticket == nil || req.Accept == nil || req.Accept.Member == nil {
		return nil, fmt.Errorf("order, ticket and acceptance are required")
	}
	worker := req.Accept.Member.OperatorAddress
	if err := m.k.RequireCurrentServiceSubmitter(ctx, shared.ParticipantTypeCortexNode, worker, req.SubmitterAddress); err != nil {
		return nil, err
	}
	params, err := m.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if uint64(req.Size()) > params.Dispatch.MaxDispatchRecordBytes {
		return nil, fmt.Errorf("compensation proof exceeds byte cap")
	}
	ticketHash, err := dispatchMaterialDigest("task.v1.DispatchTicketV1", req.Ticket)
	if err != nil {
		return nil, err
	}
	if previous, err := m.k.readDispatchClaim(ctx, ticketHash[:]); err == nil {
		if previous.WorkerOperatorAddress != worker || previous.BuilderAddress != req.Ticket.BuilderAddress || !bytes.Equal(previous.TaskId, req.Ticket.TaskId) {
			return nil, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_CONFLICT, fmt.Errorf("claim identity conflicts with its first receipt"))
		}
		return claimDispatchResponse(previous, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP), nil
	} else if !errors.Is(err, collections.ErrNotFound) {
		return nil, err
	}
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return nil, err
	}
	row, err := m.k.prepareDispatchClaim(ctx, req, ticketHash[:], params, height)
	if err != nil {
		return nil, err
	}
	cacheCtx, commit := sdk.UnwrapSDKContext(ctx).CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	if err := m.k.writeDispatchClaim(cache, row); err != nil {
		return nil, err
	}
	if err := emitTypedEvent(cache, &types.EventDispatchCompensationClaimed{DispatchTicketHash: row.DispatchTicketHash, TaskId: row.TaskId, Builder: row.BuilderAddress, Worker: row.WorkerOperatorAddress, Amount: row.Amount, RebuttalDeadlineHeight: row.RebuttalDeadlineHeight}); err != nil {
		return nil, err
	}
	commit()
	return claimDispatchResponse(row, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED), nil
}

func claimDispatchResponse(row types.DispatchCompensationClaimState, status shared.MutationStatusV1) *types.MsgClaimDispatchCompensationResponse {
	return &types.MsgClaimDispatchCompensationResponse{DispatchTicketHash: bytes.Clone(row.DispatchTicketHash), Amount: row.Amount, RebuttalDeadlineHeight: row.RebuttalDeadlineHeight, Status: status}
}

func (k Keeper) prepareDispatchClaim(ctx context.Context, req *types.MsgClaimDispatchCompensation, ticketHash []byte, params types.TaskParamsV1, height uint64) (types.DispatchCompensationClaimState, error) {
	var row types.DispatchCompensationClaimState
	ticket, accept, order := req.Ticket, req.Accept, req.Order.Order
	taskID, taskHash, err := dispatchOrderIdentity(order)
	if err != nil {
		return row, err
	}
	if !bytes.Equal(taskID[:], ticket.TaskId) || !bytes.Equal(taskHash[:], ticket.TaskHash) || !bytes.Equal(order.SessionId, ticket.SessionId) || order.OrderSequence != ticket.OrderSequence || !bytes.Equal(accept.TaskId, ticket.TaskId) || !bytes.Equal(accept.TaskHash, ticket.TaskHash) || !bytes.Equal(accept.DispatchTicketHash, ticketHash) || !sameCandidateMember(ticket.Target, accept.Member) {
		return row, invalidDispatchTicket("claim order, ticket and acceptance scopes differ")
	}
	if _, err := k.verifyDispatchMaterial(ctx, "task.v1.DispatchTicketV1", ticket, &order.EarliestSubmitHeight); err != nil {
		return row, invalidDispatchTicket(err.Error())
	}
	if _, err := k.verifyDispatchMaterial(ctx, "task.v1.DispatchAcceptV1", accept, &order.EarliestSubmitHeight); err != nil {
		return row, invalidDispatchTicket(err.Error())
	}
	deadline, closeHeight, err := dispatchClaimWindow(order.EarliestSubmitHeight, ticket.BoundChangeHeight, params.Dispatch)
	if err != nil {
		return row, err
	}
	if height <= deadline || height > closeHeight {
		return row, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_COMPENSATION_NOT_CLAIMABLE, fmt.Errorf("claim is outside its signed ticket window"))
	}
	excluded, pruned, err := k.dispatchClaimExcluded(ctx, ticket.TaskId, ticketHash)
	if err != nil {
		return row, err
	}
	if pruned {
		return row, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_COMPENSATION_RECORD_PRUNED, fmt.Errorf("record exclusion facts have been pruned"))
	}
	if excluded {
		return row, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_COMPENSATION_NOT_CLAIMABLE, fmt.Errorf("recorded or ineligible winner cannot claim"))
	}
	profile, found := k.hubKeeper.GetProfileState(sdkContextFrom(ctx), order.ModelId, order.ProfileVersion)
	if !found {
		return row, fmt.Errorf("claim profile is unavailable")
	}
	var amount shared.Amount
	for _, tier := range params.Dispatch.PrefillCompensationByModelTier {
		if tier != nil && tier.ResourceTier == profile.ResourceTier {
			amount = tier.Amount
			break
		}
	}
	if value, err := shared.ParseAmount(amount); err != nil || value == 0 {
		return row, fmt.Errorf("claim profile tier has no compensation policy")
	}
	rebuttal, overflow := shared.CheckedHeightAddV1(height, params.Dispatch.DispatchCompensationRebuttalBlocks)
	if overflow {
		return row, fmt.Errorf("claim rebuttal height overflows")
	}
	row = types.DispatchCompensationClaimState{DispatchTicketHash: bytes.Clone(ticketHash), TaskId: taskID[:], SessionId: bytes.Clone(order.SessionId), OrderSequence: order.OrderSequence, BuilderAddress: ticket.BuilderAddress, BuilderSetId: order.BuilderSetId, WorkerOperatorAddress: ticket.Target.OperatorAddress, Amount: amount, Status: types.DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_PENDING_REBUTTAL, ClaimedHeight: height, RebuttalDeadlineHeight: rebuttal, PaidAmount: shared.NewAmount(0), UnfilledAmount: shared.NewAmount(0), ClaimWindowCloseHeight: closeHeight, EarliestSubmitHeight: order.EarliestSubmitHeight}
	return row, types.ValidateDispatchClaim(row)
}

func dispatchOrderIdentity(order types.TaskOrderV3) ([32]byte, [32]byte, error) {
	id, err := types.DeriveTaskIDFromRawSession(order.SessionId, order.OrderSequence)
	if err != nil {
		return [32]byte{}, [32]byte{}, err
	}
	hash, err := types.TaskOrderHash(order)
	return id, hash, err
}

func dispatchClaimWindow(earliest, bound uint64, params types.DispatchParamsV1) (uint64, uint64, error) {
	deadline, overflow := shared.CheckedHeightAddV1(max(earliest, bound), params.RecordDeadlineBlocks)
	if overflow {
		return 0, 0, fmt.Errorf("dispatch record deadline overflows")
	}
	closeHeight, overflow := shared.CheckedHeightAddV1(deadline, params.DispatchCompensationClaimWindowBlocks)
	if overflow {
		return 0, 0, fmt.Errorf("dispatch claim window overflows")
	}
	return deadline, closeHeight, nil
}

// dispatchClaimExcluded uses retained primaries, never a missing record as proof
// that a task was unrecorded. It is shared by registration and delayed payment.
func (k Keeper) dispatchClaimExcluded(ctx context.Context, taskID, ticket []byte) (excluded, pruned bool, err error) {
	if found, err := k.DispatchWinnerIneligible.Has(ctx, ticket); err != nil || found {
		return found, false, err
	}
	record, err := k.readDispatchRecord(ctx, taskID)
	if err == nil {
		return bytes.Equal(record.DispatchTicketHash, ticket), false, nil
	}
	if !errors.Is(err, collections.ErrNotFound) {
		return false, false, err
	}
	terminal, err := k.TaskTerminalSummary.Has(ctx, taskID)
	if err != nil {
		return false, false, err
	}
	if terminal {
		return false, true, nil
	}
	core, err := k.TaskCore.Has(ctx, taskID)
	if err != nil {
		return false, false, err
	}
	if core {
		return false, false, fmt.Errorf("accepted task is missing its retained dispatch record")
	}
	return false, false, nil
}

func (k Keeper) matureDispatchClaim(ctx context.Context, ticket []byte, deadline, height uint64) error {
	row, err := k.readDispatchClaim(ctx, ticket)
	if errors.Is(err, collections.ErrNotFound) {
		return fmt.Errorf("claim deadline points at a missing primary")
	}
	if err != nil {
		return err
	}
	if row.Status != types.DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_PENDING_REBUTTAL || row.RebuttalDeadlineHeight != deadline {
		return k.DispatchClaimRebuttal.Remove(ctx, collections.Join(deadline, ticket))
	}
	if height <= deadline {
		return nil
	}
	excluded, pruned, err := k.dispatchClaimExcluded(ctx, row.TaskId, ticket)
	if err != nil {
		return err
	}
	row.XResolvedHeight = &types.DispatchCompensationClaimState_ResolvedHeight{ResolvedHeight: height}
	if excluded || pruned {
		row.Status = types.DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_DISMISSED
		if err := k.writeDispatchClaim(ctx, row); err != nil {
			return err
		}
		return emitTypedEvent(ctx, &types.EventDispatchCompensationRebutted{DispatchTicketHash: ticket, TaskId: row.TaskId, Builder: row.BuilderAddress, Worker: row.WorkerOperatorAddress, AttemptOutcome: types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_ACCEPTED})
	}
	amount, err := shared.ParseAmount(row.Amount)
	if err != nil {
		return err
	}
	paid, err := k.hubKeeper.PayDispatchCompensation(ctx, row.BuilderAddress, row.WorkerOperatorAddress, row.SessionId, row.TaskId, amount, height)
	if err != nil {
		return err
	}
	if paid > amount {
		return fmt.Errorf("compensation payment exceeds requested amount")
	}
	row.Status = types.DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_PAID
	row.PaidAmount, row.UnfilledAmount = shared.NewAmount(paid), shared.NewAmount(amount-paid)
	if err := k.writeDispatchClaim(ctx, row); err != nil {
		return err
	}
	return emitTypedEvent(ctx, &types.EventDispatchCompensationPaid{DispatchTicketHash: ticket, TaskId: row.TaskId, Builder: row.BuilderAddress, Worker: row.WorkerOperatorAddress, Amount: row.Amount, PaidAmount: row.PaidAmount, UnfilledAmount: row.UnfilledAmount})
}
