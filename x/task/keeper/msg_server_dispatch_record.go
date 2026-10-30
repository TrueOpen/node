package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"github.com/TrueOpen/wire/dispatch"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// RecordDispatch commits each entry independently, while a storage/invariant error
// aborts the enclosing message. The cache of a rejected entry is always discarded.
func (m msgServer) RecordDispatch(ctx context.Context, req *types.MsgRecordDispatch) (*types.MsgRecordDispatchResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "dispatch record request is required")
	}
	params, err := m.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if len(req.Entries) == 0 || uint64(len(req.Entries)) > uint64(params.Dispatch.MaxDispatchEntriesPerRecord) || uint64(req.Size()) > params.Dispatch.MaxDispatchRecordBytes {
		return nil, status.Error(codes.InvalidArgument, "dispatch batch exceeds its item or byte limit")
	}
	if _, _, err := m.k.canonicalAddress("submitter_address", req.SubmitterAddress); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return nil, err
	}
	authority, err := m.k.dispatchSubmitterAuthority(ctx, req.SubmitterAddress, height)
	if err != nil {
		return nil, status.Error(codes.PermissionDenied, err.Error())
	}
	minimum, err := shared.ParseAmount(params.Dispatch.BuilderCompensationAccountMinBalance)
	if err != nil {
		return nil, err
	}
	if authority.CompensationBalance < minimum {
		return nil, status.Error(codes.FailedPrecondition, types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_BUILDER_COMPENSATION_BELOW_MIN.String())
	}
	batchCtx, commitBatch := sdk.UnwrapSDKContext(ctx).CacheContext()
	results := make([]types.BatchItemResultV1, len(req.Entries))
	rejectedSequences := make(map[string]uint64)
	for i, entry := range req.Entries {
		if entry == nil {
			return nil, status.Error(codes.InvalidArgument, "dispatch batch contains a nil entry")
		}
		itemCtx, commitItem := batchCtx.CacheContext()
		var identity [32]byte
		var mutation shared.MutationStatusV1
		var order *types.TaskOrderV3
		switch branch := entry.Entry.(type) {
		case *types.DispatchRecordEntryV1_Dispatch:
			identity, err = dispatchEntryIdentity(batchCtx.ChainID(), branch.Dispatch)
			if err != nil {
				return nil, status.Error(codes.InvalidArgument, err.Error())
			}
			order = &branch.Dispatch.SignedOrder.Order
			if rejected, ok := rejectedSequences[string(order.SessionId)]; ok && order.OrderSequence > rejected {
				err = rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_CONFLICT, errDispatchBatchDependency)
			} else {
				mutation, err = m.k.admitDispatchItem(sdk.WrapSDKContext(itemCtx), branch.Dispatch, authority, params, height, identity, uint32(i))
			}
		case *types.DispatchRecordEntryV1_Takeover:
			if branch.Takeover == nil || branch.Takeover.SignedOrder == nil {
				return nil, status.Error(codes.InvalidArgument, "takeover entry needs a signed order")
			}
			order = &branch.Takeover.SignedOrder.Order
			if rejected, ok := rejectedSequences[string(order.SessionId)]; ok && order.OrderSequence > rejected {
				identity, _, err = m.k.admitTakeoverItem(sdk.WrapSDKContext(itemCtx), branch.Takeover, authority, params, height)
				if _, business := dispatchRejectionCode(err); err == nil || business {
					err = rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_CONFLICT, errDispatchBatchDependency)
				}
			} else {
				identity, mutation, err = m.k.admitTakeoverItem(sdk.WrapSDKContext(itemCtx), branch.Takeover, authority, params, height)
			}
		default:
			return nil, status.Error(codes.InvalidArgument, "dispatch entry must select dispatch or takeover")
		}
		result := types.BatchItemResultV1{ItemIndex: uint32(i), ObjectId: bytes.Clone(identity[:]), Status: mutation}
		if err != nil {
			var ineligible *dispatchWinnerIneligibleError
			if errors.As(err, &ineligible) {
				branch, ok := entry.Entry.(*types.DispatchRecordEntryV1_Dispatch)
				if !ok {
					return nil, fmt.Errorf("winner ineligibility outside a dispatch entry")
				}
				if err := m.k.recordDispatchWinnerIneligible(sdk.WrapSDKContext(batchCtx), branch.Dispatch, ineligible.reason, params.Dispatch, height); err != nil {
					return nil, err
				}
			}
			code, business := dispatchRejectionCode(err)
			if !business {
				return nil, err
			}
			result.Status = shared.MutationStatusV1_MUTATION_STATUS_V1_REJECTED
			result.RejectCode = code
			stream, readErr := m.k.ReadStream(sdk.WrapSDKContext(batchCtx), types.NewSessionStoreKey(order.SessionId))
			if readErr != nil && !errors.Is(readErr, collections.ErrNotFound) {
				return nil, readErr
			}
			if readErr == nil && stream.NextExpectedSequence == order.OrderSequence {
				if first, ok := rejectedSequences[string(order.SessionId)]; !ok || order.OrderSequence < first {
					rejectedSequences[string(order.SessionId)] = order.OrderSequence
				}
			}
		} else {
			commitItem()
		}
		results[i] = result
	}
	digest, err := taskBatchDigest(batchCtx.ChainID(), shared.BatchResultKindDispatchRecord, results)
	if err != nil {
		return nil, err
	}
	response := &types.MsgRecordDispatchResponse{Results: make([]*types.BatchItemResultV1, len(results)), BatchDigest: digest}
	for i := range results {
		response.Results[i] = &results[i]
	}
	commitBatch()
	return response, nil
}

func (k Keeper) dispatchSubmitterAuthority(ctx context.Context, submitter string, height uint64) (hubtypes.DispatchBuilderAuthority, error) {
	set, err := k.hubKeeper.GetBuilderSetForHeight(sdkContextFrom(ctx), height)
	if err != nil {
		return hubtypes.DispatchBuilderAuthority{}, err
	}
	for _, operator := range set.ActiveBuilders {
		if err := k.RequireCurrentServiceSubmitter(ctx, shared.ParticipantTypeBuilder, operator, submitter); err == nil {
			return k.hubKeeper.GetBuilderDispatchAuthority(ctx, operator)
		}
	}
	return hubtypes.DispatchBuilderAuthority{}, fmt.Errorf("submitter is not a current BuilderSet service")
}

func (k Keeper) admitDispatchItem(ctx context.Context, entry *types.DispatchEntryV1, authority hubtypes.DispatchBuilderAuthority, params types.TaskParamsV1, height uint64, identity [32]byte, itemIndex uint32) (shared.MutationStatusV1, error) {
	order := entry.SignedOrder.Order
	taskID, err := types.DeriveTaskIDFromRawSession(order.SessionId, order.OrderSequence)
	if err != nil {
		return 0, err
	}
	stored, err := k.DispatchRecord.Get(ctx, taskID[:])
	if err == nil {
		if bytes.Equal(stored.AcceptedEntryDigest, identity[:]) {
			return shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, nil
		}
		return 0, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_CONFLICT, fmt.Errorf("a different dispatch entry was accepted for this task"))
	}
	if !errors.Is(err, collections.ErrNotFound) {
		return 0, err
	}
	if exists, err := k.TaskCore.Has(ctx, taskID[:]); err != nil {
		return 0, err
	} else if exists {
		return 0, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_CONFLICT, fmt.Errorf("task already exists"))
	}
	if exists, err := k.TaskTerminalSummary.Has(ctx, taskID[:]); err != nil {
		return 0, err
	} else if exists {
		return 0, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_CONFLICT, fmt.Errorf("task is already terminal"))
	}
	admission, state, liability, err := k.planDispatchRecord(ctx, entry, authority, params, height, identity)
	if err != nil {
		return 0, err
	}
	if err := k.applyDispatchRecord(ctx, admission, state, liability, params, itemIndex); err != nil {
		return 0, err
	}
	return shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, nil
}

func (k Keeper) admitTakeoverItem(ctx context.Context, entry *types.TakeoverEntryV1, authority hubtypes.DispatchBuilderAuthority, params types.TaskParamsV1, height uint64) ([32]byte, shared.MutationStatusV1, error) {
	var zero [32]byte
	if entry == nil || entry.SignedOrder == nil {
		return zero, 0, fmt.Errorf("takeover entry needs a signed order")
	}
	order := entry.SignedOrder.Order
	taskHash, err := types.TaskOrderHash(order)
	if err != nil {
		return zero, 0, err
	}
	stream, err := k.ReadStream(ctx, order.SessionId)
	if errors.Is(err, collections.ErrNotFound) {
		// The entry digest requires the authoritative binding generation. A
		// missing Session cannot produce a nonzero canonical batch object ID.
		return zero, 0, types.ErrInvalidSessionID.Wrap("takeover Session is unavailable")
	}
	if err != nil {
		return zero, 0, err
	}
	identity, err := dispatch.TakeoverEntryDigest(sdkContextFrom(ctx).ChainID(), [32]byte(order.SessionId), order.OrderSequence, taskHash, authority.Binding.OperatorAddress, stream.BoundChangeHeight)
	if err != nil {
		return zero, 0, err
	}
	if previous, err := k.TakeoverEntryReceipt.Get(ctx, identity[:]); err == nil {
		if !bytes.Equal(previous.TakeoverEntryDigest, identity[:]) {
			return identity, 0, fmt.Errorf("takeover receipt key/digest mismatch")
		}
		return identity, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, nil
	} else if !errors.Is(err, collections.ErrNotFound) {
		return identity, 0, err
	}
	admission, err := k.prepareDispatchOrder(ctx, *entry.SignedOrder, authority.Binding.OperatorAddress, params, height)
	if err != nil {
		return identity, 0, err
	}
	if err := k.requireUnusedDispatchSequence(ctx, admission); err != nil {
		if code, business := dispatchRejectionCode(err); business && code == types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_SEQUENCE_CONSUMED {
			return identity, 0, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_TAKEOVER_SEQUENCE_CONSUMED, err)
		}
		return identity, 0, err
	}
	if stream.BoundBuilder == authority.Binding.OperatorAddress {
		return identity, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, nil
	}
	if stream.GetPendingTakeover() != nil {
		return identity, 0, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_TAKEOVER_PENDING, fmt.Errorf("session already has a pending takeover"))
	}
	if stream.PendingWithdrawKind == types.SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_CLOSE {
		return identity, 0, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_SESSION_INVALID, fmt.Errorf("session has a pending CLOSE"))
	}
	activation, overflow := shared.CheckedHeightAddV1(height, params.Dispatch.RecordDeadlineBlocks)
	if overflow {
		return identity, 0, fmt.Errorf("takeover activation height overflows")
	}
	pending := &types.PendingTakeoverV1{CandidateBuilder: authority.Binding.OperatorAddress, OrderSequence: order.OrderSequence, OrderTaskHash: taskHash[:], RegisteredHeight: height, ActivationHeight: activation}
	previous := stream
	stream.XPendingTakeover = &types.StreamState_PendingTakeover{PendingTakeover: pending}
	stream.LastActiveHeight = height
	if err := k.replaceStreamState(ctx, previous, stream); err != nil {
		return identity, 0, err
	}
	if err := k.SessionTakeoverIndex.Set(ctx, collections.Join(activation, order.SessionId)); err != nil {
		return identity, 0, err
	}
	receipt := types.TakeoverEntryReceiptState{TakeoverEntryDigest: identity[:], SessionId: bytes.Clone(order.SessionId), OrderSequence: order.OrderSequence, OrderTaskHash: taskHash[:], CandidateOperator: authority.Binding.OperatorAddress, BoundChangeHeightAtRegistration: stream.BoundChangeHeight, RegisteredHeight: height, ActivationHeight: activation, OrderExpireHeight: order.OrderExpireHeight}
	if err := k.writeTakeoverEntryReceipt(ctx, receipt, params.Dispatch.DispatchRecordRetentionBlocks); err != nil {
		return identity, 0, err
	}
	if err := emitTypedEvent(ctx, &types.EventSessionTakeoverRegistered{SessionId: order.SessionId, BoundBuilder: stream.BoundBuilder, CandidateBuilder: pending.CandidateBuilder, OrderSequence: order.OrderSequence, RegisteredHeight: height, ActivationHeight: activation}); err != nil {
		return identity, 0, err
	}
	return identity, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, nil
}
