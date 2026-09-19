package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sort"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

type preVerificationFailureResult struct {
	Core         types.TaskCoreState
	Settlement   types.TaskSettlementState
	RefundAmount shared.Amount
	Applied      bool
}

// writePreVerificationFailure is the single funded terminal transition before a
// verification round exists. It writes a round=0 settlement, preserves accepted
// gas reimbursements, refunds the remainder, closes all non-evidence
// responsibilities, and publishes finality in one cache transaction.
func (k Keeper) writePreVerificationFailure(
	ctx context.Context,
	core types.TaskCoreState,
	currentHeight uint64,
	kind types.DeadlineKindV1,
	failureClass types.TaskFailureClass,
) (preVerificationFailureResult, error) {
	taskKey, err := taskStoreKey(core.TaskId)
	if err != nil {
		return preVerificationFailureResult{}, err
	}
	if existing, err := k.TaskSettlement.Get(ctx, taskKey); err == nil {
		storedCore, coreErr := k.TaskCore.Get(ctx, taskKey)
		if coreErr != nil || storedCore.FinalityStatus != shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL ||
			storedCore.EffectiveVerifyRound != 0 {
			return preVerificationFailureResult{}, fmt.Errorf("pre-verification settlement replay conflicts with task authority")
		}
		return preVerificationFailureResult{
			Core: storedCore, Settlement: existing,
			RefundAmount: existing.RefundAmount, Applied: false,
		}, nil
	} else if !errors.Is(err, collections.ErrNotFound) {
		return preVerificationFailureResult{}, err
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	result, err := k.applyPreVerificationFailure(cache, core, currentHeight, kind, failureClass)
	if err != nil {
		return preVerificationFailureResult{}, err
	}
	write()
	result.Applied = true
	return result, nil
}

func (k Keeper) applyPreVerificationFailure(
	ctx context.Context,
	core types.TaskCoreState,
	currentHeight uint64,
	kind types.DeadlineKindV1,
	failureClass types.TaskFailureClass,
) (preVerificationFailureResult, error) {
	taskKey := types.NewTaskKey(core.TaskId)
	assignment, err := k.TaskAssignment.Get(ctx, taskKey)
	if err != nil {
		return preVerificationFailureResult{}, err
	}
	budget, err := k.TaskBudget.Get(ctx, taskKey)
	if err != nil || budget.BudgetStatus != types.TaskBudgetStatus_TASK_BUDGET_STATUS_RESERVED {
		return preVerificationFailureResult{}, fmt.Errorf("pre-verification task budget is not reserved")
	}
	selection, err := k.TaskBuilderSelection.Get(ctx, taskKey)
	if err != nil || len(selection.SelectedTaskBuilders) == 0 {
		return preVerificationFailureResult{}, fmt.Errorf("pre-verification Task Builder selection is unavailable")
	}
	if currentHeight == 0 {
		return preVerificationFailureResult{}, fmt.Errorf("pre-verification finality height must be positive")
	}

	verdict, err := preVerificationVerdict(kind)
	if err != nil {
		return preVerificationFailureResult{}, err
	}
	inferRef := make([]byte, types.Hash32Len)
	generatedTokens := uint64(0)
	worker := ""
	billHash := make([]byte, types.Hash32Len)
	if kind == types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_OPEN {
		receipt, err := k.InferReceipt.Get(ctx, taskKey)
		if err != nil || len(receipt.InferReceiptHash) != types.Hash32Len {
			return preVerificationFailureResult{}, fmt.Errorf("verify-open failure has no canonical infer receipt")
		}
		inferRef = append([]byte(nil), receipt.InferReceiptHash...)
		generatedTokens = receipt.GeneratedTokenCount
		worker = receipt.WinnerWorker
		bill := types.TaskSettlementBillV1{
			WorkerOperatorAddress: worker, InferReceiptRef: inferRef,
			FeeRuleVersion:      budget.FeeRuleVersion,
			GeneratedTokenCount: generatedTokens, WorkUnit: generatedTokens,
		}
		hash, err := types.SettlementBillHash(bill)
		if err != nil {
			return preVerificationFailureResult{}, err
		}
		billHash = hash[:]
	}

	zero32 := make([]byte, types.Hash32Len)
	roundSummary := types.TaskRoundSummaryState{
		TaskId: core.TaskId, MaxClosedRound: 0, OpenRoundCount: 0, EffectiveVerifyRound: 0,
		XRoundsClosedHeight: &types.TaskRoundSummaryState_RoundsClosedHeight{
			RoundsClosedHeight: currentHeight,
		},
		XSettlementFactsCutoffHeight: &types.TaskRoundSummaryState_SettlementFactsCutoffHeight{
			SettlementFactsCutoffHeight: currentHeight,
		},
		Round1FactsHashOrZero32:  append([]byte(nil), zero32...),
		Round2FactsHashOrZero32:  append([]byte(nil), zero32...),
		Round2EffectRootOrZero32: append([]byte(nil), zero32...),
	}
	roundSummaryHash, err := types.TaskRoundSummaryHash(
		sdk.UnwrapSDKContext(ctx).ChainID(), roundSummary,
	)
	if err != nil {
		return preVerificationFailureResult{}, err
	}
	roundSummary.XTaskRoundSummaryHash = &types.TaskRoundSummaryState_TaskRoundSummaryHash{
		TaskRoundSummaryHash: roundSummaryHash[:],
	}

	settlementID, err := types.TaskSettlementID(
		sdk.UnwrapSDKContext(ctx).ChainID(), core.TaskId, core.AcceptedTaskHash,
		0, currentHeight, currentHeight,
	)
	if err != nil {
		return preVerificationFailureResult{}, err
	}
	source := shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_DEADLINE
	evidenceDigest, err := preVerificationEvidenceDigest(
		sdk.UnwrapSDKContext(ctx).ChainID(), core.TaskId, failureClass, source,
		currentHeight, inferRef, settlementID[:],
	)
	if err != nil {
		return preVerificationFailureResult{}, err
	}
	faults := []hubtypes.RoleFaultState{}
	if kind == types.DeadlineKindV1_DEADLINE_KIND_V1_WORKER_INFER {
		if assignment.WinnerWorker == "" {
			return preVerificationFailureResult{}, fmt.Errorf("worker timeout has no assigned Worker")
		}
		fault, err := k.hubKeeper.ApplyTaskRoleFault(ctx, hubtypes.TaskRoleFaultFact{
			SessionID: append([]byte(nil), core.SessionId...), TaskID: append([]byte(nil), core.TaskId...),
			OperatorAddress: assignment.WinnerWorker, Duty: shared.Duty_DUTY_WORKER,
			FaultType: hubtypes.FaultTypeWorkerInferTimeout, ClassificationSource: source,
			EvidenceDigest: evidenceDigest, SlashBps: assignment.WorkerInferTimeoutSlashBps,
			Height: currentHeight,
		})
		if err != nil {
			return preVerificationFailureResult{}, err
		}
		if !bytes.Equal(fault.TaskId, core.TaskId) || !bytes.Equal(fault.EvidenceDigest, evidenceDigest) {
			return preVerificationFailureResult{}, fmt.Errorf("Hub returned a non-canonical worker timeout fault")
		}
		faults = append(faults, fault)
	}
	faultSummaryHash, err := preVerificationFaultSummaryHash(
		sdk.UnwrapSDKContext(ctx).ChainID(), core.TaskId, faults,
	)
	if err != nil {
		return preVerificationFailureResult{}, err
	}

	facts := types.SettlementFactsV1{
		TaskId: append([]byte(nil), core.TaskId...), VerifyRound: 0,
		SettlementHeight: currentHeight, SettlementFactsCutoffHeight: currentHeight,
		SettlementDutyBuilderOperator: selection.SelectedTaskBuilders[0],
		ConsensusClusterHash:          append([]byte(nil), zero32...),
		Verdict:                       verdict, FailureClass: failureClass,
		FaultSummaryHash: faultSummaryHash, InferReceiptRef: inferRef,
		ResultReceiptRefsHash: append([]byte(nil), zero32...),
	}
	factsHash, err := types.SettlementFactsHash(
		sdk.UnwrapSDKContext(ctx).ChainID(), settlementID[:], facts,
	)
	if err != nil {
		return preVerificationFailureResult{}, err
	}
	reimbursements, err := k.collectTaskGasReimbursements(ctx, taskKey)
	if err != nil {
		return preVerificationFailureResult{}, err
	}
	gasHash, err := types.GasReimbursementsHash(
		sdk.UnwrapSDKContext(ctx).ChainID(), core.TaskId, reimbursements,
	)
	if err != nil {
		return preVerificationFailureResult{}, err
	}
	reserved, err := shared.ParseAmount(budget.ReservedAmount)
	if err != nil {
		return preVerificationFailureResult{}, err
	}
	gasTotal := uint64(0)
	for _, item := range reimbursements {
		amount, err := shared.ParseAmount(item.ReimbursedAmount)
		if err != nil {
			return preVerificationFailureResult{}, err
		}
		gasTotal, err = addPreVerificationAmount(gasTotal, amount)
		if err != nil {
			return preVerificationFailureResult{}, err
		}
	}
	if gasTotal > reserved {
		return preVerificationFailureResult{}, fmt.Errorf("gas reimbursements exceed reserved task budget")
	}
	refund := reserved - gasTotal
	plan := types.SettlementPlanV1{
		TaskId: append([]byte(nil), core.TaskId...), SettlementId: settlementID[:],
		FeeRuleVersion: budget.FeeRuleVersion, EffectiveVerifyRound: 0,
		GeneratedTokenCount: generatedTokens, WorkUnit: generatedTokens,
		WorkerGross: shared.NewAmount(0), WorkerMaintenance: shared.NewAmount(0),
		WorkerNet: shared.NewAmount(0), VerifierSlotGross: shared.NewAmount(0),
		GasReimbursements: reimbursements, GasReimbursementsHash: gasHash[:],
		MaintenanceFee: shared.NewAmount(0), RefundAmount: shared.NewAmount(refund),
		OriginalReservedAmount: budget.OriginalReservedAmount,
		SettlementFactsHash:    factsHash[:], TaskRoundSummaryHash: roundSummaryHash[:],
		SettlementBillHash: billHash,
	}
	planHash, err := types.SettlementPlanHash(sdk.UnwrapSDKContext(ctx).ChainID(), plan)
	if err != nil {
		return preVerificationFailureResult{}, err
	}
	paidRolesHash, err := types.PaidRolesHash(
		sdk.UnwrapSDKContext(ctx).ChainID(), core.TaskId, settlementID[:], nil,
	)
	if err != nil {
		return preVerificationFailureResult{}, err
	}

	settlement := types.TaskSettlementState{
		TaskId: append([]byte(nil), core.TaskId...), SettlementId: settlementID[:],
		FeeRuleVersion: budget.FeeRuleVersion, EffectiveVerifyRound: 0,
		Verdict: verdict, FailureClass: failureClass,
		SettlementFactsCutoffHeight: currentHeight, SettlementHeight: currentHeight,
		TaskFinalityHeight: currentHeight, GeneratedTokenCount: generatedTokens, WorkUnit: generatedTokens,
		InferReceiptRefOrZero32: inferRef,
		WorkerGross:             shared.NewAmount(0), WorkerMaintenance: shared.NewAmount(0), WorkerNet: shared.NewAmount(0),
		VerifierSlotGross: shared.NewAmount(0), GasReimbursementCount: uint32(len(reimbursements)),
		GasReimbursementsHash: gasHash[:], MaintenanceFee: shared.NewAmount(0),
		RefundAmount: shared.NewAmount(refund), OriginalReservedAmount: budget.OriginalReservedAmount,
		SettlementFactsHash: factsHash[:], TaskRoundSummaryHash: roundSummaryHash[:],
		SettlementBillHashOrZero32: billHash, SettlementPlanHash: planHash[:],
		GasReimbursedTotal: shared.NewAmount(gasTotal),
	}
	if worker != "" {
		settlement.XWorkerOperatorAddress = &types.TaskSettlementState_WorkerOperatorAddress{
			WorkerOperatorAddress: worker,
		}
	}
	if err := k.TaskRoundSummary.Set(ctx, taskKey, roundSummary); err != nil {
		return preVerificationFailureResult{}, err
	}
	if err := k.TaskSettlement.Set(ctx, taskKey, settlement); err != nil {
		return preVerificationFailureResult{}, err
	}
	if err := k.SettlementFactsRetained.Set(ctx, taskKey, types.SettlementFactsRetainedState{
		TaskId: append([]byte(nil), core.TaskId...), TaskHash: append([]byte(nil), core.AcceptedTaskHash...),
		SettlementId: settlementID[:], SettlementFactsHash: factsHash[:],
		SettlementFactsCutoffHeight: currentHeight, SettlementHeight: currentHeight,
		SettlementDutyBuilderOperator: selection.SelectedTaskBuilders[0],
		ConsensusClusterHash:          append([]byte(nil), zero32...), RecomputedVerdict: verdict,
		FailureClass: failureClass, FaultSummaryHash: faultSummaryHash,
		PaidRolesHash: paidRolesHash[:], InferReceiptRefOrZero32: inferRef,
		ResultReceiptRefsHash: append([]byte(nil), zero32...), EffectiveVerifyRound: 0,
		FeeRuleVersion: budget.FeeRuleVersion, GeneratedTokenCount: generatedTokens,
		WorkUnit: generatedTokens, SettlementBillHashOrZero32: billHash,
		TaskRoundSummaryHash:         roundSummaryHash[:],
		EvidenceSchemaHash:           append([]byte(nil), assignment.EvidenceSchemaHash...),
		JudgmentFunctionVersion:      assignment.JudgmentFunctionVersion,
		CanonicalEncodingVersion:     assignment.CanonicalEncodingVersion,
		ProfileExecutionSnapshotHash: append([]byte(nil), assignment.ProfileExecutionSnapshotHash...),
		GenerationParamsDigest:       append([]byte(nil), assignment.GenerationParamsDigest...),
		MetricAggregateProofVersion:  assignment.MetricAggregateProofVersion,
	}); err != nil {
		return preVerificationFailureResult{}, err
	}

	credits, err := settlementEarningsCredits(settlementInputs{Core: core, Assignment: assignment}, plan)
	if err != nil {
		return preVerificationFailureResult{}, err
	}
	if len(credits) != 0 {
		if err := k.hubKeeper.CreditTaskSettlementEarnings(
			ctx, core.SessionId, core.TaskId, credits, currentHeight,
		); err != nil {
			return preVerificationFailureResult{}, err
		}
	}
	if err := k.sendFromTaskEscrow(ctx, hubtypes.RewardsModuleName, gasTotal); err != nil {
		return preVerificationFailureResult{}, err
	}
	if refund != 0 {
		if err := k.releaseTaskBudgetRefund(ctx, core.UserAddress, refund); err != nil {
			return preVerificationFailureResult{}, err
		}
	}
	budget.ReservedAmount = shared.NewAmount(0)
	budget.TxFeeReserveRemaining = shared.NewAmount(0)
	budget.BudgetStatus = types.TaskBudgetStatus_TASK_BUDGET_STATUS_FINALIZED
	if err := k.TaskBudget.Set(ctx, taskKey, budget); err != nil {
		return preVerificationFailureResult{}, err
	}
	if err := k.closeStreamPendingTask(ctx, core.SessionId); err != nil {
		return preVerificationFailureResult{}, err
	}
	if err := k.finalizeOrderSequenceState(ctx, core.TaskId,
		types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_REFUNDED); err != nil {
		return preVerificationFailureResult{}, err
	}
	if err := k.releaseFailedTaskResponsibilities(ctx, core, currentHeight); err != nil {
		return preVerificationFailureResult{}, err
	}
	if err := k.RemoveRoleActiveTaskIndexes(ctx, core.TaskId); err != nil {
		return preVerificationFailureResult{}, err
	}

	eligible, known := types.FreezeSignalEligibilityForTaskFailureClass(failureClass)
	if !known {
		return preVerificationFailureResult{}, fmt.Errorf("pre-verification failure class is not registered")
	}
	hubParams := k.hubKeeper.GetHubParams(sdk.UnwrapSDKContext(ctx))
	pruneHeight, overflow := checkedHeightAdd(
		currentHeight, hubParams.FreezeFailureIndexRetentionBlocks,
	)
	if overflow {
		return preVerificationFailureResult{}, fmt.Errorf("task failure prune height overflow")
	}
	failure := types.TaskFailureClassState{
		TaskId: append([]byte(nil), core.TaskId...), VerifyRound: types.VerifyRoundV1,
		ModelId: core.ModelId, ProfileVersion: core.ProfileVersion,
		XSettlementId: &types.TaskFailureClassState_SettlementId{SettlementId: settlementID[:]},
		FailureClass:  failureClass, FreezeSignalEligible: eligible,
		ClassificationSource: source, ClassifiedHeight: currentHeight,
		// Classification precedes the settlement-facts write inside this one
		// transaction, so both the row and its digest use ZERO32 at this slot.
		InferReceiptHashOrZero32: inferRef, SettlementFactsHashOrZero32: append([]byte(nil), zero32...),
		SettlementIdOrZero32: settlementID[:],
		XTaskFinalityHeight: &types.TaskFailureClassState_TaskFinalityHeight{
			TaskFinalityHeight: currentHeight,
		},
		XPruneHeight:   &types.TaskFailureClassState_PruneHeight{PruneHeight: pruneHeight},
		EvidenceDigest: evidenceDigest,
	}
	if err := k.TaskFailureClass.Set(ctx,
		types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1), failure); err != nil {
		return preVerificationFailureResult{}, err
	}
	if failureClass != types.TaskFailureClass_TASK_FAILURE_CLASS_NONE {
		if err := k.TaskFailureClassByProfileWindowIndex.Set(ctx,
			types.NewTaskFailureClassByProfileWindowKey(
				core.ModelId, core.ProfileVersion, currentHeight, failureClass, taskKey,
			)); err != nil {
			return preVerificationFailureResult{}, err
		}
	}
	if err := k.TaskFailureClassWindowPruneIndex.Set(ctx,
		types.NewTaskFailureClassPruneKey(pruneHeight, taskKey, types.VerifyRoundV1)); err != nil {
		return preVerificationFailureResult{}, err
	}

	core.TaskPhase = types.TaskPhase_TASK_PHASE_FAILED
	// §10.7 pairs the two halves as `task -> VERIFY_FAILED / REFUNDED`: a task that
	// fails and has its budget returned carries settlement_status REFUNDED, not
	// FINALIZED. This function is definitionally that path — it already declares
	// ORDER_SEQUENCE_STATUS_REFUNDED a few lines above — so writing FINALIZED here
	// contradicted its own order-sequence state and left SETTLEMENT_STATUS_REFUNDED
	// with no producer anywhere on the chain.
	core.SettlementStatus = types.SettlementStatus_SETTLEMENT_STATUS_REFUNDED
	core.FinalityStatus = shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL
	core.EffectiveVerifyRound = 0
	core.XTaskFinalityHeight = &types.TaskCoreState_TaskFinalityHeight{
		TaskFinalityHeight: currentHeight,
	}
	core.UpdatedHeight = currentHeight
	if err := k.TaskCore.Set(ctx, taskKey, core); err != nil {
		return preVerificationFailureResult{}, err
	}
	cleanupHeight, overflow := checkedHeightAdd(
		currentHeight, core.EvidenceRetentionBlocksSnapshot,
	)
	if overflow {
		return preVerificationFailureResult{}, fmt.Errorf("evidence cleanup height overflow")
	}
	if err := addDeadlineIndex(ctx, k.EvidenceCleanupIndex, taskKey, cleanupHeight); err != nil {
		return preVerificationFailureResult{}, err
	}
	if err := emitTypedEvent(ctx, &types.EventTaskFailureClassUpdated{
		SessionId: append([]byte(nil), core.SessionId...), TaskId: append([]byte(nil), core.TaskId...),
		VerifyRound: types.VerifyRoundV1,
		XSettlementIdOrEmpty: &types.EventTaskFailureClassUpdated_SettlementIdOrEmpty{
			SettlementIdOrEmpty: settlementID[:],
		},
		FailureClass: failureClass, ClassificationSource: source,
		EvidenceDigest: evidenceDigest, FreezeSignalEligible: eligible,
	}); err != nil {
		return preVerificationFailureResult{}, err
	}
	switch kind {
	case types.DeadlineKindV1_DEADLINE_KIND_V1_WORKER_INFER:
		if err := emitTypedEvent(ctx, &types.EventWorkerTimeout{
			SessionId: core.SessionId, TaskId: core.TaskId, Worker: assignment.WinnerWorker,
			FailureClass: failureClass, RefundAmount: shared.NewAmount(refund),
		}); err != nil {
			return preVerificationFailureResult{}, err
		}
	case types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_OPEN:
		if err := emitTypedEvent(ctx, &types.EventVerifyOpenTimeout{
			SessionId: core.SessionId, TaskId: core.TaskId, VerifyRound: types.VerifyRoundV1,
			FailureClass: failureClass, RefundAmount: shared.NewAmount(refund),
		}); err != nil {
			return preVerificationFailureResult{}, err
		}
	}
	return preVerificationFailureResult{
		Core: core, Settlement: settlement, RefundAmount: shared.NewAmount(refund),
	}, nil
}

func preVerificationVerdict(kind types.DeadlineKindV1) (types.TaskVerdict, error) {
	switch kind {
	case types.DeadlineKindV1_DEADLINE_KIND_V1_WORKER_ASSIGNMENT:
		return types.TaskVerdict_TASK_VERDICT_ASSIGN_TIMEOUT, nil
	case types.DeadlineKindV1_DEADLINE_KIND_V1_WORKER_INFER:
		return types.TaskVerdict_TASK_VERDICT_WORKER_TIMEOUT, nil
	case types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_OPEN:
		return types.TaskVerdict_TASK_VERDICT_VERIFY_UNAVAILABLE, nil
	default:
		return types.TaskVerdict_TASK_VERDICT_UNSPECIFIED,
			fmt.Errorf("deadline kind does not use the pre-verification terminal path")
	}
}

func preVerificationEvidenceDigest(
	chainID string,
	taskID []byte,
	failureClass types.TaskFailureClass,
	source shared.FailureClassificationSource,
	height uint64,
	inferReceiptHash, settlementID []byte,
) ([]byte, error) {
	if len(taskID) != types.Hash32Len || len(inferReceiptHash) != types.Hash32Len ||
		len(settlementID) != types.Hash32Len {
		return nil, fmt.Errorf("classification evidence preimage contains a non-Hash32 field")
	}
	return shared.NewCanonicalHashBuilderV1(
		shared.MustDomain(shared.DomainClassificationEvidenceDigestV1),
	).Raw(
		[]byte(chainID), taskID, shared.Uint32BE(types.VerifyRoundV1),
		shared.EnumBE(uint32(failureClass)), shared.EnumBE(uint32(source)),
		shared.Uint64BE(height), inferReceiptHash,
		make([]byte, types.Hash32Len), settlementID,
		shared.Uint32BE(0), shared.Uint32BE(0), shared.Uint32BE(0),
	).Sum()
}

func preVerificationFaultSummaryHash(
	chainID string,
	taskID []byte,
	faults []hubtypes.RoleFaultState,
) ([]byte, error) {
	sort.Slice(faults, func(i, j int) bool {
		return bytes.Compare(faults[i].FaultId, faults[j].FaultId) < 0
	})
	frames := make([]shared.CanonicalFrameV1, len(faults))
	for i, fault := range faults {
		if len(fault.FaultId) != types.Hash32Len || len(fault.EvidenceDigest) != types.Hash32Len {
			return nil, fmt.Errorf("role fault commitment contains a non-Hash32 field")
		}
		if i != 0 && bytes.Equal(faults[i-1].FaultId, fault.FaultId) {
			return nil, fmt.Errorf("role fault commitment contains duplicate fault_id")
		}
		operator, err := types.CanonicalOperatorAddressBytes("fault operator", fault.OperatorAddress)
		if err != nil {
			return nil, err
		}
		frames[i] = shared.NewCanonicalFrameBuilderV1().Raw(
			fault.FaultId, operator, shared.EnumBE(uint32(fault.Duty)),
			shared.EnumBE(uint32(fault.FaultClass)),
			shared.EnumBE(uint32(fault.ClassificationSource)),
			fault.EvidenceDigest, shared.Uint32BE(fault.JailDelta),
			shared.EnumBE(uint32(fault.Status)),
		).Build()
		if err := frames[i].Err(); err != nil {
			return nil, err
		}
	}
	return shared.NewCanonicalHashBuilderV1(
		shared.MustDomain(shared.DomainFaultSummaryV1),
	).Raw(
		[]byte(chainID), taskID, shared.Uint32BE(uint32(len(frames))),
	).Nested(shared.CanonicalRepeatedFramesV1(frames)).Sum()
}

func addPreVerificationAmount(left, right uint64) (uint64, error) {
	value, overflow := checkedAddUint64(left, right)
	if overflow {
		return 0, fmt.Errorf("pre-verification settlement amount overflow")
	}
	return value, nil
}
