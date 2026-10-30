package keeper

import (
	"bytes"
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

type dispatchOrderAdmission struct {
	order      types.TaskOrderV3
	taskID     [32]byte
	taskHash   [32]byte
	stream     types.StreamState
	profile    hubtypes.ProfileStateSnapshot
	builderSet hubtypes.BuilderSetState
	core       types.TaskCoreState
	assignment types.TaskAssignmentState
	selection  types.TaskBuilderSelectionState
	budget     types.TaskBudgetState
	delegated  bool
	nextSpent  uint64
}

// prepareDispatchOrder validates user authority and derives every Task-owned fact
// without writes. Takeover shares user authorization, without creating Task facts.
func (k Keeper) prepareDispatchOrder(ctx context.Context, signed types.SignedOrderV2, builder string, params types.TaskParamsV1, height uint64) (dispatchOrderAdmission, error) {
	result, err := k.authorizeSessionOrder(ctx, signed, height)
	if err != nil {
		return result, err
	}
	order, taskID, taskHash, stream := result.order, result.taskID, result.taskHash, result.stream
	owner, delegated, nextSpent := stream.OwnerUserAddress, result.delegated, result.nextSpent
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	hubParams := k.hubKeeper.GetHubParams(sdkCtx)
	if height < order.EarliestSubmitHeight {
		return result, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_TICKET_INVALID, fmt.Errorf("order admission is before earliest_submit_height"))
	}
	set, err := k.hubKeeper.GetBuilderSetForHeight(sdkCtx, height)
	if err != nil {
		return result, err
	}
	if set.BuilderSetId != order.BuilderSetId || !bytes.Equal(set.BuilderSetHash, order.BuilderSetHash) || set.BodyStatus != shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE {
		return result, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_BUILDER_SET_CHANGED, fmt.Errorf("signed order does not bind the current BuilderSet"))
	}
	builders, err := orderedTaskBuilders(set, builder)
	if err != nil {
		return result, err
	}
	profile, found := k.hubKeeper.GetProfileState(sdkCtx, order.ModelId, order.ProfileVersion)
	if !found || !isParentModelOpenForProfile(k.hubKeeper.GetModelStatus(sdkCtx, order.ModelId)) ||
		(profile.Status != hubtypes.ModelStatusRegistered && profile.Status != hubtypes.ModelStatusActive) || !profileAllowsTaskType(profile.TaskTypes, order.TaskType) {
		return result, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_MODEL_PROFILE_UNAVAILABLE, fmt.Errorf("model profile is unavailable for this task type"))
	}
	if err := validateExecutionProfile(profile); err != nil {
		return result, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_VERIFICATION_PROFILE_UNSUPPORTED, err)
	}
	if order.GenerationParams.MaxOutputTokens > params.Generation.MaxOutputTokens {
		return result, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_ORDER_AMOUNT_INVALID, fmt.Errorf("max_output_tokens exceeds the registered Task limit"))
	}
	generationDigest, err := types.GenerationParamsDigest(order.ModelId, order.ProfileVersion, order.TaskType, order.OutputBudgetBucket, order.GenerationParams, params.Generation)
	if err != nil {
		return result, err
	}
	workerMax, verifyMax, orderValue, err := types.TaskOrderCosts(order, profile.PricingProfile.VerifyRatioBps)
	if err != nil {
		return result, err
	}
	orderValueNumber, err := shared.ParseAmount(orderValue)
	if err != nil {
		return result, err
	}
	if orderValueNumber < profile.PricingProfile.MinOrderValue {
		return result, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_ORDER_AMOUNT_INVALID, fmt.Errorf("order_value is below the profile minimum"))
	}
	bucket, err := k.hubKeeper.GetParameterBucketVersion(ctx, shared.BucketKind_BUCKET_KIND_TIMEOUT, hubtypes.DefaultParameterBucketKey, order.TimeoutBucketVersion)
	if err != nil || bucket.Version != order.TimeoutBucketVersion || bucket.EffectiveHeight > height {
		return result, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_TICKET_INVALID, fmt.Errorf("timeout bucket version is unavailable"))
	}
	inferTimeout, found := timeoutForWorkUnits(bucket.TimeoutEntries.Entries, order.GenerationParams.MaxOutputTokens)
	if !found {
		return result, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_TICKET_INVALID, fmt.Errorf("timeout bucket has no entry for the requested work"))
	}
	selectedHash, err := types.SelectedTaskBuildersHash(sdkCtx.ChainID(), taskID[:], set.BuilderSetId, set.BuilderSetHash, builders)
	if err != nil {
		return result, err
	}
	openingHash, err := types.AcceptedTaskOrderOpeningHash(order, generationDigest)
	if err != nil {
		return result, err
	}
	if params.Fee.FeeRuleVersion != types.TaskFeeRuleVersionV2 {
		return result, fmt.Errorf("unsupported Task fee rule version")
	}
	result = dispatchOrderAdmission{
		order: order, taskID: taskID, taskHash: taskHash, stream: stream, profile: profile, builderSet: set, delegated: delegated, nextSpent: nextSpent,
		core: types.TaskCoreState{
			TaskId: taskID[:], UserAddress: owner, SessionId: bytes.Clone(order.SessionId), OrderSequence: order.OrderSequence,
			AcceptedTaskHash: taskHash[:], AcceptedInputHash: bytes.Clone(order.InputHash), AcceptedOrderOpeningHash: openingHash[:],
			ModelId: bytes.Clone(order.ModelId), ProfileVersion: order.ProfileVersion, TaskType: order.TaskType, OrderValue: orderValue,
			TaskPhase: types.TaskPhase_TASK_PHASE_INFER_PENDING, AssignmentStatus: types.AssignmentStatus_ASSIGNMENT_STATUS_WORKER_RECORDED,
			ReceiptStatus: types.ReceiptStatus_RECEIPT_STATUS_NONE, VerificationStatus: types.VerificationStatus_VERIFICATION_STATUS_NONE,
			SettlementStatus: types.SettlementStatus_SETTLEMENT_STATUS_NONE, FinalityStatus: shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_PENDING,
			CreatedHeight: height, UpdatedHeight: height, EvidenceRetentionBlocksSnapshot: params.Evidence.MaxEvidenceRetentionBlocks,
			ObjectiveForgerySlashBpsSnapshot: hubParams.ObjectiveForgerySlashBps,
		},
		assignment: types.TaskAssignmentState{
			TaskId: taskID[:], RecordHeight: height, WinnerConfirmHeight: height,
			MinStakeSnapshot: shared.NewAmount(profile.MinStake), ProfileExecutionSnapshotHash: bytes.Clone(profile.ExecutionSnapshotHash),
			JudgmentFunctionVersion:  profile.ExecutionSnapshot.VerificationProfile.JudgmentFunctionVersion,
			EvidenceSchemaHash:       bytes.Clone(profile.ExecutionSnapshot.VerificationProfile.EvidenceSchemaHash),
			CanonicalEncodingVersion: profile.ExecutionSnapshot.VerificationProfile.CanonicalEncodingVersion,
			GenerationParamsDigest:   bytes.Clone(generationDigest), MetricAggregateProofVersion: profile.ExecutionSnapshot.VerificationProfile.MetricAggregateProofVersion,
			InferTimeoutBlocks: inferTimeout, ChallengeOpenWindowBlocksSnapshot: profile.ChallengeOpenWindowBlocks,
			WorkerInferTimeoutSlashBps: params.Verification.WorkerInferTimeoutSlashBps, ResultRevealMissingSlashBps: params.Verification.ResultRevealMissingSlashBps,
		},
		selection: types.TaskBuilderSelectionState{
			TaskId: taskID[:], BuilderSetId: set.BuilderSetId, BuilderSetHash: bytes.Clone(set.BuilderSetHash),
			SelectedTaskBuilders: builders, SelectedTaskBuilderCount: uint32(len(builders)), SelectedTaskBuildersHash: selectedHash,
			CreatedHeight: height, BodyStatus: shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE, BuilderFaultSlashBpsSnapshot: params.Verification.BuilderFaultSlashBps,
		},
		budget: types.TaskBudgetState{
			TaskId: taskID[:], FeeRuleVersion: params.Fee.FeeRuleVersion, OriginalReservedAmount: order.MaxFee, ReservedAmount: order.MaxFee,
			TxFeeReserveRemaining: order.TxFeeReserve, PriceBid: order.PriceBid, WorkerMax: workerMax, VerifyMax: verifyMax,
			VerifyRatioBpsSnapshot: profile.PricingProfile.VerifyRatioBps, MaxOutputTokens: order.GenerationParams.MaxOutputTokens,
			MaintenanceRateBpsSnapshot: params.Fee.MaintenanceRateBps, FeePolicyVersionSnapshot: params.Fee.FeePolicyVersion,
			MaxReimbursableFeePerGasSnapshot: types.FeePerGasRateV1{Numerator: params.Fee.MaxReimbursableFeePerGasNumerator, Denominator: params.Fee.MaxReimbursableFeePerGasDenominator},
			MaxReimbursementPerTxSnapshot:    params.Fee.MaxReimbursementPerTx, MaxReimbursementPerTaskSnapshot: params.Fee.MaxReimbursementPerTask,
			GasReimbursedTotal: shared.NewAmount(0), BudgetStatus: types.TaskBudgetStatus_TASK_BUDGET_STATUS_RESERVED,
		},
	}
	return result, nil
}

func orderedTaskBuilders(set hubtypes.BuilderSetState, dispatchBuilder string) ([]string, error) {
	if set.ActiveBuilderCount == 0 || uint32(len(set.ActiveBuilders)) != set.ActiveBuilderCount {
		return nil, fmt.Errorf("BuilderSet membership is invalid")
	}
	first := -1
	seen := make(map[string]bool, len(set.ActiveBuilders))
	for i, operator := range set.ActiveBuilders {
		if _, err := types.CanonicalOperatorAddressBytes("BuilderSet operator", operator); err != nil {
			return nil, err
		}
		if seen[operator] {
			return nil, fmt.Errorf("BuilderSet contains a duplicate operator")
		}
		seen[operator] = true
		if operator == dispatchBuilder {
			first = i
		}
	}
	if first < 0 {
		return nil, fmt.Errorf("dispatch Builder is outside the current BuilderSet")
	}
	builders := make([]string, 0, len(set.ActiveBuilders))
	builders = append(builders, set.ActiveBuilders[first:]...)
	builders = append(builders, set.ActiveBuilders[:first]...)
	return builders, nil
}

func profileAllowsTaskType(allowed []shared.TaskType, taskType shared.TaskType) bool {
	for _, candidate := range allowed {
		if candidate == taskType {
			return taskType == shared.TaskType_TASK_TYPE_TEXT_GENERATION || taskType == shared.TaskType_TASK_TYPE_CHAT
		}
	}
	return false
}

func validateExecutionProfile(profile hubtypes.ProfileStateSnapshot) error {
	verification := profile.ExecutionSnapshot.VerificationProfile
	batch := profile.ExecutionSnapshot.BatchVerification
	if len(profile.ExecutionSnapshotHash) != types.Hash32Len || len(verification.EvidenceSchemaHash) != types.Hash32Len ||
		profile.ExecutionSnapshot.RuntimeClass != shared.RuntimeClassV1 ||
		verification.JudgmentFunctionVersion != shared.JudgmentFunctionVersionV1 ||
		verification.CanonicalEncodingVersion != shared.CanonicalEncodingVersionV1 ||
		verification.MetricAggregateProofVersion != shared.MetricAggregateProofVersionV1 ||
		verification.VerificationMode != shared.VerificationMode_VERIFICATION_MODE_SINGLE_SAMPLE ||
		verification.TokenScope != shared.TokenScope_TOKEN_SCOPE_ALL_GENERATED_OUTPUT_TOKENS || batch.Enabled {
		return fmt.Errorf("verification profile is unsupported by the Task executor")
	}
	return nil
}
