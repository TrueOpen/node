package keeper

import (
	"bytes"
	"context"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) planDispatchRecord(ctx context.Context, entry *types.DispatchEntryV1, authority hubtypes.DispatchBuilderAuthority, params types.TaskParamsV1, height uint64, identity [32]byte) (dispatchOrderAdmission, types.DispatchRecordState, hubtypes.FrozenFactLiabilityRequest, error) {
	var state types.DispatchRecordState
	var liability hubtypes.FrozenFactLiabilityRequest
	builder := authority.Binding.OperatorAddress
	admission, err := k.prepareDispatchOrder(ctx, *entry.SignedOrder, builder, params, height)
	if err != nil {
		return admission, state, liability, err
	}
	order, record, ticket, accept := admission.order, entry.Record, entry.WinnerTicket, entry.Accept
	if admission.stream.BoundBuilder != builder {
		return admission, state, liability, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_BUILDER_NOT_BOUND, fmt.Errorf("session is bound to another Builder"))
	}
	if record.BuilderAddress != builder || ticket.BuilderAddress != builder || !bytes.Equal(record.TaskId, admission.taskID[:]) || !bytes.Equal(record.TaskHash, admission.taskHash[:]) || !bytes.Equal(record.SessionId, order.SessionId) || record.OrderSequence != order.OrderSequence || !bytes.Equal(ticket.InputHash, order.InputHash) {
		return admission, state, liability, invalidDispatchTicket("record or ticket differs from the signed order")
	}
	recordHash, err := k.verifyDispatchMaterial(ctx, "task.v1.DispatchRecordV1", record, &order.EarliestSubmitHeight)
	if err != nil {
		return admission, state, liability, invalidDispatchTicket(err.Error())
	}
	ticketHash, err := k.verifyDispatchMaterial(ctx, "task.v1.DispatchTicketV1", ticket, &order.EarliestSubmitHeight)
	if err != nil {
		return admission, state, liability, invalidDispatchTicket(err.Error())
	}
	if err := validateDispatchRecordStructure(record, ticket, ticketHash, params.Dispatch.MaxSkips); err != nil {
		return admission, state, liability, err
	}
	if err := validateDispatchSticky(admission.stream, order, ticket, params.Dispatch); err != nil {
		return admission, state, liability, err
	}
	pool, err := k.validateDispatchVrf(ctx, admission, ticket)
	if err != nil {
		return admission, state, liability, err
	}
	if !bytes.Equal(accept.TaskId, admission.taskID[:]) || !bytes.Equal(accept.TaskHash, admission.taskHash[:]) || !bytes.Equal(accept.DispatchTicketHash, ticketHash[:]) || !sameCandidateMember(accept.Member, ticket.Target) {
		return admission, state, liability, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_ACCEPT_SIGNATURE_INVALID, fmt.Errorf("accept differs from the winner ticket"))
	}
	if _, err := k.verifyDispatchMaterial(ctx, "task.v1.DispatchAcceptV1", accept, &order.EarliestSubmitHeight); err != nil {
		return admission, state, liability, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_ACCEPT_SIGNATURE_INVALID, err)
	}
	liability, err = k.dispatchWinnerLiability(ctx, admission, ticket, pool, height)
	if err != nil {
		return admission, state, liability, err
	}
	deadline, overflow := shared.CheckedHeightAddV1(max(order.EarliestSubmitHeight, admission.stream.BoundChangeHeight), params.Dispatch.RecordDeadlineBlocks)
	if overflow {
		return admission, state, liability, invalidDispatchTicket("dispatch deadline overflows")
	}
	if height > deadline {
		return admission, state, liability, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_RECORD_LATE, fmt.Errorf("dispatch record is late"))
	}
	if err := k.requireUnusedDispatchSequence(ctx, admission); err != nil {
		return admission, state, liability, err
	}
	fee, err := shared.ParseAmount(order.MaxFee)
	if err != nil {
		return admission, state, liability, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_ORDER_AMOUNT_INVALID, err)
	}
	balance, err := shared.ParseAmount(admission.stream.Balance)
	if err != nil {
		return admission, state, liability, err
	}
	frozen, err := shared.ParseAmount(admission.stream.FrozenTotal)
	if err != nil {
		return admission, state, liability, err
	}
	if frozen > balance {
		return admission, state, liability, fmt.Errorf("session reservation exceeds its balance")
	}
	if fee > balance-frozen {
		return admission, state, liability, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_SESSION_BALANCE_INSUFFICIENT, fmt.Errorf("session available balance is below max_fee"))
	}
	inferDeadline, overflow := shared.CheckedHeightAddV1(height, admission.assignment.InferTimeoutBlocks)
	if overflow {
		return admission, state, liability, invalidDispatchTicket("inference deadline overflows")
	}
	closeHeight, overflow := shared.CheckedHeightAddV1(deadline, params.Dispatch.DispatchCompensationClaimWindowBlocks)
	if overflow {
		return admission, state, liability, invalidDispatchTicket("claim window close height overflows")
	}
	admission.assignment.DispatchRecordHash = recordHash[:]
	admission.assignment.DispatchTicketHash = ticketHash[:]
	admission.assignment.WinnerWorker = ticket.Target.OperatorAddress
	admission.assignment.CandidatePoolSnapshotId = bytes.Clone(pool.SnapshotId)
	admission.assignment.CandidatePoolHash = bytes.Clone(pool.PoolHash)
	admission.assignment.InferDeadlineHeight = inferDeadline
	sticky := &types.DispatchStickySnapshotV1{AnchorSequence: admission.stream.StickyAnchorSequence, AnchorHeight: admission.stream.StickyAnchorHeight, RunCount: admission.stream.StickyRunCount}
	if admission.stream.XLastRecordWinner != nil {
		sticky.XLastRecordWinner = &types.DispatchStickySnapshotV1_LastRecordWinner{LastRecordWinner: admission.stream.GetLastRecordWinner()}
	}
	state = types.DispatchRecordState{TaskId: admission.taskID[:], DispatchRecordHash: recordHash[:], DispatchTicketHash: ticketHash[:], BuilderAddress: builder,
		ReferenceHeight: record.ReferenceHeight, CandidatePoolSnapshotId: bytes.Clone(pool.SnapshotId), VrfEpoch: record.VrfEpoch, EarliestSubmitHeight: order.EarliestSubmitHeight, ServiceAuthorizationNonce: record.ServiceAuthorizationNonce, BoundChangeHeight: record.BoundChangeHeight,
		VrfOutput: bytes.Clone(record.VrfOutput), VrfProof: bytes.Clone(record.VrfProof), RandomnessScheme: record.RandomnessScheme, Rounds: record.Rounds,
		WinnerRound: ticket.DispatchRound, StickyAnchorSequence: record.StickyAnchorSequence, StickyRunCount: record.StickyRunCount, StickyApplied: record.StickyApplied,
		StickySnapshot: sticky, Winner: ticket.Target, RecordedHeight: height, AcceptedEntryDigest: identity[:], ClaimWindowCloseHeight: closeHeight}
	return admission, state, liability, nil
}

func (k Keeper) dispatchWinnerLiability(ctx context.Context, admission dispatchOrderAdmission, ticket *types.DispatchTicketV1, pool hubtypes.CandidatePoolSnapshotState, height uint64) (hubtypes.FrozenFactLiabilityRequest, error) {
	var empty hubtypes.FrozenFactLiabilityRequest
	target := ticket.Target
	if target == nil {
		return empty, invalidDispatchTicket("winner member is absent")
	}
	member, binding, err := k.hubKeeper.ResolveCandidatePoolMember(ctx, pool.SnapshotId, target.Slot)
	if err != nil || member.SlotVersion != target.SlotVersion || member.OperatorAddress != target.OperatorAddress || binding.SlotVersion != target.SlotVersion || binding.OperatorAddress != target.OperatorAddress {
		return empty, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_WINNER_NOT_ELIGIBLE, fmt.Errorf("winner is outside the referenced pool"))
	}
	inputs := member.WeightInputs
	if inputs == nil {
		return empty, fmt.Errorf("candidate snapshot weight inputs are missing")
	}
	var profile *hubtypes.CandidateProfileWeightInputV1
	for _, input := range inputs.Profiles {
		if input != nil && input.ProfileVersion == admission.order.ProfileVersion && bytes.Equal(input.ModelId, admission.order.ModelId) {
			profile = input
			break
		}
	}
	weight, err := hubtypes.FrozenWorkerCandidateWeight(pool, inputs, profile)
	if err != nil {
		return empty, err
	}
	if weight == 0 {
		return empty, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_WINNER_NOT_ELIGIBLE, fmt.Errorf("winner has no eligible frozen profile weight"))
	}
	current, err := k.hubKeeper.GetCurrentServiceKey(ctx, shared.ParticipantTypeCortexNode, target.OperatorAddress)
	if err != nil {
		return empty, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_WINNER_NOT_ELIGIBLE, err)
	}
	if current.Status != hubtypes.ServiceKeyStatusActive {
		return empty, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_WINNER_NOT_ELIGIBLE, fmt.Errorf("winner current service key is not active"))
	}
	raw, _, err := k.canonicalAddress("winner_operator", target.OperatorAddress)
	if err != nil {
		return empty, invalidDispatchTicket(err.Error())
	}
	orderValue, err := shared.ParseAmount(admission.core.OrderValue)
	if err != nil {
		return empty, err
	}
	bond, found := k.hubKeeper.GetServiceBond(sdkContextFrom(ctx), sdk.AccAddress(raw), orderValue)
	if !found {
		return empty, fmt.Errorf("winner service bond authority is unavailable")
	}
	var reason types.DispatchWinnerIneligibleReasonV1
	switch {
	case bond.Status == hubtypes.ServiceBondStatusTombstoned:
		reason = types.DispatchWinnerIneligibleReasonV1_DISPATCH_WINNER_INELIGIBLE_REASON_V1_TOMBSTONED
	case bond.Status == hubtypes.ServiceBondStatusUnbonding:
		reason = types.DispatchWinnerIneligibleReasonV1_DISPATCH_WINNER_INELIGIBLE_REASON_V1_UNBONDING
	case bond.EffectiveActiveBond < admission.profile.MinStake:
		reason = types.DispatchWinnerIneligibleReasonV1_DISPATCH_WINNER_INELIGIBLE_REASON_V1_BOND_INSUFFICIENT
	case bond.RequiredTaskLiability == 0 || bond.AvailableBond < bond.RequiredTaskLiability:
		reason = types.DispatchWinnerIneligibleReasonV1_DISPATCH_WINNER_INELIGIBLE_REASON_V1_LIABILITY_INSUFFICIENT
	}
	if reason != types.DispatchWinnerIneligibleReasonV1_DISPATCH_WINNER_INELIGIBLE_REASON_V1_UNSPECIFIED {
		return empty, &dispatchWinnerIneligibleError{reason: reason}
	}
	if !hubtypes.IsCandidateEligibleBondStatus(bond.Status) {
		return empty, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_WINNER_NOT_ELIGIBLE, fmt.Errorf("winner bond cannot cover this task"))
	}
	capability, capable := k.hubKeeper.GetModelCapability(sdkContextFrom(ctx), sdk.AccAddress(raw), admission.order.ModelId)
	support, supported := k.hubKeeper.GetModelSupport(sdkContextFrom(ctx), sdk.AccAddress(raw), admission.order.ModelId)
	epochLength := k.hubKeeper.GetHubParams(sdkContextFrom(ctx)).EpochLengthBlocks
	if epochLength == 0 {
		return empty, fmt.Errorf("winner support epoch length is unavailable")
	}
	if !capable || !supported || !capability.InferenceCapability || capability.CapabilityVersion == 0 || !candidateModelSupportAllowed(k.hubKeeper.GetModelStatus(sdkContextFrom(ctx), admission.order.ModelId), support, bond) || support.SupportVersion == 0 || support.SupportFreshUntilEpoch == 0 || height/epochLength >= support.SupportFreshUntilEpoch {
		return empty, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_WINNER_NOT_ELIGIBLE, fmt.Errorf("winner liability audit versions are unavailable"))
	}
	return hubtypes.FrozenFactLiabilityRequest{TaskID: admission.taskID[:], CandidatePoolSnapshotID: pool.SnapshotId, OperatorAddress: target.OperatorAddress, Duty: shared.DutyWorker,
		OrderValue: orderValue, Slot: target.Slot, SlotVersion: target.SlotVersion, RequiredTaskLiability: bond.RequiredTaskLiability, ActiveBondSnapshot: bond.EffectiveActiveBond,
		AvailableBondSnapshot: bond.AvailableBond, MinStakeSnapshot: admission.profile.MinStake, BondVersionSnapshot: bond.BondVersion,
		CapabilityVersionSnapshot: capability.CapabilityVersion, SupportVersionSnapshot: support.SupportVersion, ModelID: admission.order.ModelId, ProfileVersion: admission.order.ProfileVersion, Height: height}, nil
}

func (k Keeper) applyDispatchRecord(ctx context.Context, admission dispatchOrderAdmission, state types.DispatchRecordState, liability hubtypes.FrozenFactLiabilityRequest, params types.TaskParamsV1, itemIndex uint32) error {
	height, taskKey := state.RecordedHeight, types.NewTaskKey(admission.taskID[:])
	created, err := k.hubKeeper.AcquireCandidatePoolTaskRef(ctx, admission.taskID[:], state.CandidatePoolSnapshotId, height)
	if err != nil {
		return err
	}
	if !created {
		return fmt.Errorf("new dispatch already has a candidate-pool reference")
	}
	if _, err := k.hubKeeper.ReserveTaskLiabilityFromFrozenFact(ctx, liability); err != nil {
		return rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_LIABILITY_RESERVATION_FAILED, err)
	}
	created, err = k.hubKeeper.AcquireBuilderSetTaskRef(ctx, admission.taskID[:], admission.selection.BuilderSetId, admission.selection.BuilderSetHash, height)
	if err != nil {
		return err
	}
	if !created {
		return fmt.Errorf("new dispatch already has a BuilderSet reference")
	}
	if err := k.hubKeeper.AcquireParameterBucketTaskRef(ctx, shared.BucketKind_BUCKET_KIND_TIMEOUT, hubtypes.DefaultParameterBucketKey, admission.order.TimeoutBucketVersion, height); err != nil {
		return err
	}
	if err := k.TaskBucketRef.Set(ctx, types.NewTaskBucketRefKey(taskKey, int32(shared.BucketKind_BUCKET_KIND_TIMEOUT), hubtypes.DefaultParameterBucketKey), types.TaskBucketRefState{TaskId: admission.taskID[:], BucketKind: shared.BucketKind_BUCKET_KIND_TIMEOUT, BucketKey: hubtypes.DefaultParameterBucketKey, Version: admission.order.TimeoutBucketVersion, AcquiredHeight: height}); err != nil {
		return err
	}
	if err := k.TaskCore.Set(ctx, taskKey, admission.core); err != nil {
		return err
	}
	if err := k.PinTaskParams(ctx, taskKey); err != nil {
		return err
	}
	if err := k.WriteTaskAssignment(ctx, taskKey, admission.assignment); err != nil {
		return err
	}
	if err := k.StoreTaskBuilderSelection(ctx, taskKey, admission.selection); err != nil {
		return err
	}
	if err := k.acquireTaskBuilderEvidenceResponsibilities(ctx, admission.core, admission.selection); err != nil {
		return err
	}
	if err := k.TaskBudget.Set(ctx, taskKey, admission.budget); err != nil {
		return err
	}
	if err := k.consumeOrderSequence(ctx, admission.order.SessionId, admission.order.OrderSequence, admission.taskID[:]); err != nil {
		return err
	}
	stream, err := k.ReadStream(ctx, admission.order.SessionId)
	if err != nil {
		return err
	}
	frozen, err := shared.ParseAmount(stream.FrozenTotal)
	if err != nil {
		return err
	}
	fee, err := shared.ParseAmount(admission.order.MaxFee)
	if err != nil {
		return err
	}
	nextFrozen, overflow := checkedAddUint64(frozen, fee)
	if overflow {
		return fmt.Errorf("session frozen funds overflow")
	}
	stream.FrozenTotal = shared.NewAmount(nextFrozen)
	stream.LastRecordHeight = height
	if admission.delegated {
		stream.SessionKeySpent = shared.NewAmount(admission.nextSpent)
	}
	if state.StickyAnchorSequence == admission.order.OrderSequence && state.StickyRunCount == 1 {
		stream.StickyAnchorSequence = admission.order.OrderSequence
		stream.StickyAnchorHeight = height
	}
	stream.StickyRunCount = state.StickyRunCount
	stream.XLastRecordWinner = &types.StreamState_LastRecordWinner{LastRecordWinner: state.Winner.OperatorAddress}
	if err := k.WriteStream(ctx, admission.order.SessionId, stream); err != nil {
		return err
	}
	if err := k.writeDispatchRecord(ctx, state); err != nil {
		return err
	}
	prune, overflow := shared.CheckedHeightAddV1(height, params.Dispatch.DispatchRecordRetentionBlocks)
	if overflow {
		return fmt.Errorf("dispatch record retention height overflows")
	}
	if err := k.DispatchRecordPrune.Set(ctx, collections.Join(prune, admission.taskID[:])); err != nil {
		return err
	}
	if err := addDeadlineIndex(ctx, k.InferDeadlineIndex, taskKey, admission.assignment.InferDeadlineHeight); err != nil {
		return err
	}
	if err := k.AddWorkerActiveTaskIndex(ctx, state.Winner.OperatorAddress, admission.taskID[:]); err != nil {
		return err
	}
	if err := k.recordTaskGasReimbursementIntent(ctx, admission.taskID[:], itemIndex, types.TaskReimbursementKindV1_TASK_REIMBURSEMENT_KIND_V1_DISPATCH_RECORD); err != nil {
		return err
	}
	return emitTypedEvent(ctx, &types.EventDispatchRecorded{SessionId: admission.order.SessionId, TaskId: admission.taskID[:], OrderSequence: admission.order.OrderSequence, Builder: state.BuilderAddress, WinnerWorker: state.Winner.OperatorAddress, DispatchRecordHash: state.DispatchRecordHash, DispatchTicketHash: state.DispatchTicketHash, ReferenceHeight: state.ReferenceHeight, ReservedAmount: admission.order.MaxFee, InferDeadline: admission.assignment.InferDeadlineHeight})
}
