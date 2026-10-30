package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	storetypes "cosmossdk.io/store/types"
	"github.com/TrueOpen/wire/dispatch"
	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

type dispatchChallengeHandler func(context.Context, types.TaskParamsV1, uint64) (types.DispatchChallengeState, shared.MutationStatusV1, error)

// runDispatchChallenge shares cache ownership and charges a bounded child gas
// meter back to its parent on success, rejection and out-of-gas alike.
func (k Keeper) runDispatchChallenge(ctx context.Context, taskID []byte, kind shared.DispatchChallengeKindV1, size int, handle dispatchChallengeHandler) (row types.DispatchChallengeState, mutation shared.MutationStatusV1, err error) {
	params, err := k.Params.Get(ctx)
	if len(taskID) != 0 {
		params, err = k.ParamsForTask(ctx, taskID)
	}
	if err != nil {
		return row, mutation, err
	}
	maximum, overflow := checkedAddUint64(params.Dispatch.MaxDispatchRecordBytes, 2*params.Dispatch.MaxDispatchRosterBytes)
	if overflow || uint64(size) > maximum {
		return row, mutation, fmt.Errorf("dispatch challenge exceeds byte cap")
	}
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return row, mutation, err
	}
	parent := sdk.UnwrapSDKContext(ctx).GasMeter()
	cap := parent.GasRemaining()
	if kind != shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_DOUBLE_DISPATCH {
		found := false
		for _, policy := range params.Dispatch.DispatchChallengeGasCapByKind {
			if policy != nil && policy.Kind == kind {
				cap = min(cap, policy.GasCap)
				found = cap != 0
				break
			}
		}
		if !found {
			return row, mutation, fmt.Errorf("dispatch challenge gas policy is unavailable")
		}
	}
	meter := storetypes.NewGasMeter(cap)
	defer func() {
		parent.ConsumeGas(meter.GasConsumedToLimit(), "dispatch challenge")
		if recovered := recover(); recovered != nil {
			if _, ok := recovered.(storetypes.ErrorOutOfGas); ok {
				err = fmt.Errorf("dispatch challenge gas cap exceeded")
				mutation = shared.MutationStatusV1_MUTATION_STATUS_V1_UNSPECIFIED
				return
			}
			panic(recovered)
		}
	}()
	cacheCtx, commit := sdk.UnwrapSDKContext(ctx).WithGasMeter(meter).CacheContext()
	row, mutation, err = handle(sdk.WrapSDKContext(cacheCtx), params, height)
	if err == nil {
		commit()
	}
	return row, mutation, err
}

func (k Keeper) recordedDispatchChallenge(ctx context.Context, taskID []byte, challenger string, kind shared.DispatchChallengeKindV1, params types.TaskParamsV1, height uint64) (types.DispatchRecordState, types.TaskCoreState, types.DispatchChallengeState, error) {
	var record types.DispatchRecordState
	var core types.TaskCoreState
	var row types.DispatchChallengeState
	if len(taskID) != types.Hash32Len {
		return record, core, row, fmt.Errorf("task_id must be raw32")
	}
	if _, _, err := k.canonicalAddress("challenger_address", challenger); err != nil {
		return record, core, row, err
	}
	record, err := k.readDispatchRecord(ctx, taskID)
	if err != nil {
		return record, core, row, err
	}
	closeHeight, overflow := checkedHeightAdd(record.RecordedHeight, params.Dispatch.DispatchChallengeWindowBlocks)
	if overflow || height > closeHeight {
		return record, core, row, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_CHALLENGE_WINDOW_CLOSED, fmt.Errorf("dispatch challenge window is closed"))
	}
	core, err = k.TaskCore.Get(ctx, taskID)
	if err != nil {
		return record, core, row, err
	}
	row = types.DispatchChallengeState{TaskId: bytes.Clone(taskID), Kind: kind, ChallengerAddress: challenger, BuilderAddress: record.BuilderAddress, XDispatchRecordHash: &types.DispatchChallengeState_DispatchRecordHash{DispatchRecordHash: bytes.Clone(record.DispatchRecordHash)}, OpenedHeight: height}
	return record, core, row, nil
}

func dispatchChallengeIdentity(chain string, row types.DispatchChallengeState) ([32]byte, error) {
	scope := dispatch.ChallengeScope{Kind: uint32(row.Kind), ChallengerAddress: row.ChallengerAddress, DispatchRound: row.GetDispatchRound(), AttemptRank: row.GetAttemptRank(), VictimOperator: row.GetVictimOperatorAddress()}
	copy(scope.TaskID[:], row.TaskId)
	if row.Kind == shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_DOUBLE_DISPATCH {
		scope.BuilderAddress = row.BuilderAddress
		copy(scope.DispatchRecordHash[:], row.GetDispatchRecordHash())
	}
	return dispatch.ChallengeID(chain, scope)
}

func (k Keeper) identifyDispatchChallenge(ctx context.Context, row *types.DispatchChallengeState) error {
	id, err := dispatchChallengeIdentity(sdkContextFrom(ctx).ChainID(), *row)
	if err != nil {
		return err
	}
	row.ChallengeId = id[:]
	key, err := k.dispatchUpheldScopeKey(*row)
	if err != nil {
		return err
	}
	if exists, err := k.DispatchUpheldScope.Has(ctx, key); err != nil {
		return err
	} else if exists {
		return rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_CHALLENGE_DUPLICATE, fmt.Errorf("objective dispatch scope already upheld"))
	}
	return nil
}

func (k Keeper) openDispatchChallenge(ctx context.Context, row types.DispatchChallengeState, params types.TaskParamsV1, height uint64) (types.DispatchChallengeState, shared.MutationStatusV1, error) {
	if existing, err := k.readDispatchChallenge(ctx, row.ChallengeId); err == nil {
		return existing, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, nil
	} else if !errors.Is(err, collections.ErrNotFound) {
		return row, 0, err
	}
	deadline, overflow := checkedHeightAdd(height, params.Dispatch.DispatchChallengeResponseBlocks)
	if overflow {
		return row, 0, fmt.Errorf("dispatch challenge response height overflows")
	}
	row.XProofDeadlineHeight = &types.DispatchChallengeState_ProofDeadlineHeight{ProofDeadlineHeight: deadline}
	row.Status = types.DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_AWAITING_PROOF
	if row.Kind == shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_ROSTER_UNAVAILABLE {
		row.Status = types.DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_AWAITING_ROSTER
	}
	if err := k.writeDispatchChallenge(ctx, row); err != nil {
		return row, 0, err
	}
	if err := emitDispatchChallengeOpened(ctx, row); err != nil {
		return row, 0, err
	}
	return row, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, nil
}

func emitDispatchChallengeOpened(ctx context.Context, row types.DispatchChallengeState) error {
	return emitTypedEvent(ctx, &types.EventDispatchChallengeOpened{ChallengeId: row.ChallengeId, TaskId: row.TaskId, Kind: row.Kind, Challenger: row.ChallengerAddress, ProofDeadlineHeightOrZero: row.GetProofDeadlineHeight()})
}

func emitDispatchChallengeResolved(ctx context.Context, row types.DispatchChallengeState) error {
	return emitTypedEvent(ctx, &types.EventDispatchChallengeResolved{ChallengeId: row.ChallengeId, TaskId: row.TaskId, Kind: row.Kind, Outcome: row.Status})
}

func (k Keeper) upholdDispatchChallenge(ctx context.Context, row types.DispatchChallengeState, sessionID []byte, compensation uint64, params types.TaskParamsV1, height uint64) (types.DispatchChallengeState, shared.MutationStatusV1, error) {
	key, err := k.dispatchUpheldScopeKey(row)
	if err != nil {
		return row, 0, err
	}
	if exists, err := k.DispatchUpheldScope.Has(ctx, key); err != nil {
		return row, 0, err
	} else if exists {
		return row, 0, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_CHALLENGE_DUPLICATE, fmt.Errorf("objective dispatch scope already upheld"))
	}
	var penalty uint64
	found := false
	for _, policy := range params.Dispatch.DispatchFaultPenaltyByKind {
		if policy != nil && policy.Kind == row.Kind {
			penalty, err = shared.ParseAmount(policy.Amount)
			found = true
			break
		}
	}
	if err != nil || !found {
		return row, 0, fmt.Errorf("dispatch fault penalty policy is unavailable")
	}
	if row.Status == types.DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_UNSPECIFIED {
		if err := emitDispatchChallengeOpened(ctx, row); err != nil {
			return row, 0, err
		}
	}
	fault := hubtypes.DispatchFaultState{BuilderAddress: row.BuilderAddress, FaultId: bytes.Clone(row.ChallengeId), Kind: row.Kind, TaskId: bytes.Clone(row.TaskId), DispatchRecordHash: bytes.Clone(row.GetDispatchRecordHash()), ChallengedTicketHashOrZero32: make([]byte, 32), PenaltyAmount: shared.NewAmount(0), UnfilledAmount: shared.NewAmount(0), CompensatedAmount: shared.NewAmount(0), FaultHeight: height}
	if row.XChallengedTicketHash != nil {
		fault.ChallengedTicketHashOrZero32 = bytes.Clone(row.GetChallengedTicketHash())
	}
	if compensation != 0 {
		fault.XCompensatedWorker = &hubtypes.DispatchFaultState_CompensatedWorker{CompensatedWorker: row.GetVictimOperatorAddress()}
	}
	fault, applied, err := k.hubKeeper.ApplyDispatchFault(ctx, hubtypes.DispatchFaultRequest{Fault: fault, SessionID: sessionID, Penalty: penalty, Compensation: compensation, RetentionBlocks: params.Dispatch.DispatchRecordRetentionBlocks})
	if err != nil {
		return row, 0, err
	}
	if !applied {
		return row, 0, fmt.Errorf("dispatch fault receipt exists without its upheld challenge")
	}
	row.Status = types.DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_UPHELD
	row.XFaultId = &types.DispatchChallengeState_FaultId{FaultId: bytes.Clone(row.ChallengeId)}
	row.XResolvedHeight = &types.DispatchChallengeState_ResolvedHeight{ResolvedHeight: height}
	if err := k.writeDispatchChallenge(ctx, row); err != nil {
		return row, 0, err
	}
	event := &types.EventBuilderDispatchFaultRecorded{Builder: fault.BuilderAddress, FaultId: fault.FaultId, Kind: fault.Kind, TaskId: fault.TaskId, PenaltyAmount: fault.PenaltyAmount, UnfilledAmount: fault.UnfilledAmount, CompensatedAmount: fault.CompensatedAmount}
	if worker := fault.GetCompensatedWorker(); worker != "" {
		event.XCompensatedWorkerOrEmpty = &types.EventBuilderDispatchFaultRecorded_CompensatedWorkerOrEmpty{CompensatedWorkerOrEmpty: worker}
	}
	if err := emitTypedEvent(ctx, event); err != nil {
		return row, 0, err
	}
	return row, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, emitDispatchChallengeResolved(ctx, row)
}
