package keeper

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

type workerProposalScope struct {
	ref             types.ExistingTaskRefV1
	signed          *types.SignedOrderV2
	isFallback      bool
	existingAtStart bool
}

func resolveWorkerProposalScope(scope types.WorkerHandraiseScopeV1) (workerProposalScope, error) {
	switch branch := scope.Scope.(type) {
	case *types.WorkerHandraiseScopeV1_ExistingTask:
		if branch.ExistingTask == nil || len(branch.ExistingTask.TaskId) != types.Hash32Len || len(branch.ExistingTask.TaskHash) != types.Hash32Len {
			return workerProposalScope{}, fmt.Errorf("existing_task scope needs a Hash32 task_id and task_hash")
		}
		return workerProposalScope{ref: *branch.ExistingTask}, nil
	case *types.WorkerHandraiseScopeV1_SignedOrder:
		if branch.SignedOrder == nil {
			return workerProposalScope{}, fmt.Errorf("signed_order scope is empty")
		}
		taskHash, err := types.TaskOrderHash(branch.SignedOrder.Order)
		if err != nil {
			return workerProposalScope{}, fmt.Errorf("signed order: %w", err)
		}
		taskID, err := types.DeriveTaskIDFromRawSession(branch.SignedOrder.Order.SessionId, branch.SignedOrder.Order.OrderSequence)
		if err != nil {
			return workerProposalScope{}, err
		}
		return workerProposalScope{
			ref:    types.ExistingTaskRefV1{TaskId: taskID[:], TaskHash: taskHash[:]},
			signed: branch.SignedOrder,
		}, nil
	default:
		return workerProposalScope{}, fmt.Errorf("scope must carry exactly one of signed_order or existing_task")
	}
}

// admitSignedWorkerOrder writes the first Task state inside the caller's cache
// transaction. Every external fact is validated before the first write.
func (k Keeper) admitSignedWorkerOrder(
	ctx context.Context,
	scope *workerProposalScope,
	firstHandraise types.WorkerHandraiseV1,
	params types.TaskParamsV1,
	currentHeight uint64,
) error {
	if scope == nil || scope.signed == nil {
		return fmt.Errorf("signed order is required")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	order := scope.signed.Order
	if order.ChainId != sdkCtx.ChainID() {
		return fmt.Errorf("signed order chain_id does not match this chain")
	}
	taskKey := types.NewTaskKey(scope.ref.TaskId)
	if exists, err := k.TaskCore.Has(ctx, taskKey); err != nil {
		return err
	} else if exists {
		core, err := k.TaskCore.Get(ctx, taskKey)
		if err != nil || !bytes.Equal(core.TaskId, scope.ref.TaskId) || !bytes.Equal(core.AcceptedTaskHash, scope.ref.TaskHash) ||
			!bytes.Equal(core.SessionId, order.SessionId) || core.OrderSequence != order.OrderSequence || core.UserAddress != order.UserAddress ||
			core.ModelId != order.ModelId || core.ProfileVersion != order.ProfileVersion || core.TaskType != order.TaskType ||
			!bytes.Equal(core.AcceptedInputHash, order.InputHash) {
			return fmt.Errorf("signed_order conflicts with the accepted Task")
		}
		scope.existingAtStart = true
		return nil
	}
	if currentHeight < order.EarliestSubmitHeight || currentHeight > order.OrderExpireHeight {
		return fmt.Errorf("signed order is outside its admission height range")
	}
	hubParams := k.hubKeeper.GetHubParams(sdkCtx)
	if hubParams.EVMChainID == 0 || hubParams.BusinessDenom == "" || hubParams.AssignmentBuilderProposalWindowBlocks == 0 {
		return fmt.Errorf("Hub admission parameters are unavailable")
	}
	windowClose, overflow := checkedHeightAdd(order.EarliestSubmitHeight, hubParams.AssignmentBuilderProposalWindowBlocks)
	if overflow || windowClose >= order.OrderExpireHeight {
		return fmt.Errorf("signed order does not leave a non-empty fallback window")
	}
	scope.isFallback = currentHeight > windowClose

	userBytes, user, err := k.canonicalAddress("user_address", order.UserAddress)
	if err != nil {
		return err
	}
	stream, err := k.Stream.Get(ctx, types.NewSessionKey(order.SessionId))
	if err != nil {
		if errIsNotFound(err) {
			return fmt.Errorf("session does not exist")
		}
		return err
	}
	if stream.OwnerUserAddress != user || stream.NextExpectedSequence != order.OrderSequence {
		return fmt.Errorf("signed order owner or sequence does not match the session")
	}
	account := k.authKeeper.GetAccount(ctx, sdk.AccAddress(userBytes))
	if account == nil || account.GetPubKey() == nil {
		return fmt.Errorf("user account public key is required before order admission")
	}
	if err := types.VerifySignedOrderV2EIP712WithAccountPublicKey(
		hubParams.EVMChainID, hubParams.BusinessDenom, *scope.signed, scope.ref.TaskHash, account.GetPubKey(),
	); err != nil {
		return fmt.Errorf("signed order authorization: %w", err)
	}

	if order.SessionAnchorHeight >= currentHeight || currentHeight-order.SessionAnchorHeight > params.Session.AnchorFreshnessWindowBlocks {
		return fmt.Errorf("session anchor is outside the freshness window")
	}
	anchorHash, err := k.hubKeeper.GetBlockAnchorHash(ctx, order.SessionAnchorHeight)
	if err != nil || !bytes.Equal(anchorHash, order.SessionAnchorBlockHash) {
		return fmt.Errorf("session anchor hash is unavailable or does not match")
	}
	builderSet, err := k.hubKeeper.GetBuilderSetForHeight(sdkCtx, order.SessionAnchorHeight)
	if err != nil || builderSet.BuilderSetId != order.BuilderSetId || !bytes.Equal(builderSet.BuilderSetHash, order.BuilderSetHash) ||
		builderSet.BodyStatus != shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE {
		return fmt.Errorf("signed order BuilderSet does not match the anchor height")
	}
	selectedBuilders, err := selectTaskBuilders(sdkCtx.ChainID(), scope.ref.TaskId, order.SessionAnchorBlockHash, builderSet, hubParams.BuildersPerTask)
	if err != nil {
		return err
	}
	profile, found := k.hubKeeper.GetProfileState(sdkCtx, order.ModelId, order.ProfileVersion)
	if !found || !isParentModelOpenForProfile(k.hubKeeper.GetModelStatus(sdkCtx, order.ModelId)) ||
		(profile.Status != hubtypes.ModelStatusRegistered && profile.Status != hubtypes.ModelStatusActive) ||
		!profileAllowsTaskType(profile.TaskTypes, order.TaskType) {
		return fmt.Errorf("model profile is unavailable for this task type")
	}
	if err := validatePhase0ExecutionProfile(profile); err != nil {
		return err
	}
	if order.GenerationParams.MaxOutputTokens > params.Generation.MaxOutputTokens {
		return fmt.Errorf("max_output_tokens exceeds the registered Task limit")
	}
	generationDigest, err := types.GenerationParamsDigest(
		order.ModelId, order.ProfileVersion, order.TaskType, order.OutputBudgetBucket, order.GenerationParams, params.Generation,
	)
	if err != nil {
		return fmt.Errorf("generation params: %w", err)
	}
	workerMax, verifyMax, orderValue, err := types.TaskOrderCosts(order, profile.PricingProfile.VerifyRatioBps)
	if err != nil {
		return err
	}
	orderValueNumber, _ := shared.ParseAmount(orderValue)
	if orderValueNumber < profile.PricingProfile.MinOrderValue {
		return fmt.Errorf("order_value is below the profile minimum")
	}

	bucket, err := k.hubKeeper.GetParameterBucketVersion(
		ctx, shared.BucketKind_BUCKET_KIND_TIMEOUT, hubtypes.DefaultParameterBucketKey, order.TimeoutBucketVersion,
	)
	if err != nil || bucket.Version != order.TimeoutBucketVersion || bucket.EffectiveHeight > currentHeight {
		return fmt.Errorf("timeout bucket version is unavailable")
	}
	inferTimeout, ok := timeoutForWorkUnits(bucket.TimeoutEntries.Entries, order.GenerationParams.MaxOutputTokens)
	if !ok {
		return fmt.Errorf("timeout bucket has no entry for the requested work")
	}

	pool, err := k.hubKeeper.CurrentActiveCandidatePool(ctx)
	if err != nil {
		return fmt.Errorf("current candidate pool: %w", err)
	}
	if pool.Status != hubtypes.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_ACTIVE ||
		len(pool.SnapshotId) != types.Hash32Len || len(pool.PoolHash) != types.Hash32Len ||
		!bytes.Equal(firstHandraise.Member.CandidatePoolSnapshotId, pool.SnapshotId) {
		return fmt.Errorf("current ACTIVE candidate pool is unavailable")
	}
	slotCapacity, segmentBytes, segmentCount, ok := k.hubKeeper.GetCandidatePoolLayout(ctx, pool.SnapshotId)
	if !ok || slotCapacity == 0 || segmentBytes == 0 || segmentCount == 0 {
		return fmt.Errorf("candidate pool bitmap layout is unavailable")
	}
	zeroSegments := make([]taskBitmapSegment, segmentCount)
	for i := range zeroSegments {
		zeroSegments[i] = taskBitmapSegment{Index: uint32(i), Bitmap: make([]byte, segmentBytes)}
	}
	zeroUnionHash, err := taskStageUnionBitmapHash(sdkCtx.ChainID(), scope.ref.TaskId,
		types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK, pool.SnapshotId,
		slotCapacity, segmentBytes, 0, zeroSegments)
	if err != nil {
		return err
	}
	selectedHash, err := types.SelectedTaskBuildersHash(
		sdkCtx.ChainID(), scope.ref.TaskId, builderSet.BuilderSetId, builderSet.BuilderSetHash, selectedBuilders,
	)
	if err != nil {
		return err
	}
	openingHash, err := types.AcceptedTaskOrderOpeningHash(order, generationDigest)
	if err != nil {
		return err
	}
	maxFee, _ := shared.ParseAmount(order.MaxFee)
	if k.bankKeeper.GetBalance(ctx, sdk.AccAddress(userBytes), hubParams.BusinessDenom).Amount.LT(sdkmath.NewIntFromUint64(maxFee)) {
		return fmt.Errorf("user balance is below max_fee")
	}
	if params.Fee.FeeRuleVersion != types.TaskFeeRuleVersionV2 {
		return fmt.Errorf("unsupported Task fee rule version")
	}

	core := types.TaskCoreState{
		TaskId: scope.ref.TaskId, UserAddress: user, SessionId: append([]byte(nil), order.SessionId...), OrderSequence: order.OrderSequence,
		AcceptedTaskHash: scope.ref.TaskHash, AcceptedInputHash: append([]byte(nil), order.InputHash...), AcceptedOrderOpeningHash: openingHash[:],
		ModelId: order.ModelId, ProfileVersion: order.ProfileVersion, TaskType: order.TaskType, OrderValue: orderValue,
		TaskPhase: types.TaskPhase_TASK_PHASE_WORKER_ASSIGNMENT_PENDING, AssignmentStatus: types.AssignmentStatus_ASSIGNMENT_STATUS_NONE,
		ReceiptStatus: types.ReceiptStatus_RECEIPT_STATUS_NONE, VerificationStatus: types.VerificationStatus_VERIFICATION_STATUS_NONE,
		SettlementStatus: types.SettlementStatus_SETTLEMENT_STATUS_NONE, FinalityStatus: shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_PENDING,
		CreatedHeight: currentHeight, UpdatedHeight: currentHeight,
		EvidenceRetentionBlocksSnapshot:  params.Evidence.MaxEvidenceRetentionBlocks,
		ObjectiveForgerySlashBpsSnapshot: hubParams.ObjectiveForgerySlashBps,
	}
	assignment := types.TaskAssignmentState{
		TaskId: scope.ref.TaskId, AssignmentRandomnessHeight: windowClose,
		CandidatePoolSnapshotId: append([]byte(nil), pool.SnapshotId...), CandidatePoolHash: append([]byte(nil), pool.PoolHash...),
		ProfileExecutionSnapshotHash: append([]byte(nil), profile.ExecutionSnapshotHash...),
		JudgmentFunctionVersion:      profile.ExecutionSnapshot.VerificationProfile.JudgmentFunctionVersion,
		EvidenceSchemaHash:           append([]byte(nil), profile.ExecutionSnapshot.VerificationProfile.EvidenceSchemaHash...),
		CanonicalEncodingVersion:     profile.ExecutionSnapshot.VerificationProfile.CanonicalEncodingVersion,
		GenerationParamsDigest:       append([]byte(nil), generationDigest...),
		MetricAggregateProofVersion:  profile.ExecutionSnapshot.VerificationProfile.MetricAggregateProofVersion,
		InferTimeoutBlocks:           inferTimeout, ChallengeOpenWindowBlocksSnapshot: profile.ChallengeOpenWindowBlocks,
		WorkerInferTimeoutSlashBps:  params.Verification.WorkerInferTimeoutSlashBps,
		ResultRevealMissingSlashBps: params.Verification.ResultRevealMissingSlashBps,
	}
	selection := types.TaskBuilderSelectionState{
		TaskId: scope.ref.TaskId, SessionAnchorBlockHash: append([]byte(nil), order.SessionAnchorBlockHash...),
		BuilderSetId: builderSet.BuilderSetId, BuilderSetHash: append([]byte(nil), builderSet.BuilderSetHash...),
		SelectedTaskBuilders: selectedBuilders, SelectedTaskBuilderCount: uint32(len(selectedBuilders)), SelectedTaskBuildersHash: selectedHash,
		CreatedHeight: currentHeight, BodyStatus: shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE,
		BuilderFaultSlashBpsSnapshot: params.Verification.BuilderFaultSlashBps,
	}
	union := types.TaskStageHandraiseUnionState{
		SchemaVersion: 1, TaskId: scope.ref.TaskId, Stage: types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK,
		CandidatePoolSnapshotId: append([]byte(nil), pool.SnapshotId...), CandidatePoolHash: append([]byte(nil), pool.PoolHash...),
		Status:            types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_OPEN,
		WindowCloseHeight: windowClose, UnionBitmapHash: zeroUnionHash[:],
	}
	budget := types.TaskBudgetState{
		TaskId: scope.ref.TaskId, FeeRuleVersion: params.Fee.FeeRuleVersion,
		OriginalReservedAmount: order.MaxFee, ReservedAmount: order.MaxFee, TxFeeReserveRemaining: order.TxFeeReserve,
		PriceBid: order.PriceBid, WorkerMax: workerMax, VerifyMax: verifyMax,
		VerifyRatioBpsSnapshot: profile.PricingProfile.VerifyRatioBps, MaxOutputTokens: order.GenerationParams.MaxOutputTokens,
		MaintenanceRateBpsSnapshot: params.Fee.MaintenanceRateBps, FeePolicyVersionSnapshot: params.Fee.FeePolicyVersion,
		MaxReimbursableFeePerGasSnapshot: types.FeePerGasRateV1{Numerator: params.Fee.MaxReimbursableFeePerGasNumerator, Denominator: params.Fee.MaxReimbursableFeePerGasDenominator},
		MaxReimbursementPerTxSnapshot:    params.Fee.MaxReimbursementPerTx, MaxReimbursementPerTaskSnapshot: params.Fee.MaxReimbursementPerTask,
		GasReimbursedTotal: shared.NewAmount(0), BudgetStatus: types.TaskBudgetStatus_TASK_BUDGET_STATUS_RESERVED,
	}

	if created, err := k.hubKeeper.AcquireCandidatePoolTaskRef(ctx, scope.ref.TaskId, pool.SnapshotId, currentHeight); err != nil {
		return fmt.Errorf("candidate pool Task ref acquisition failed: %w", err)
	} else if !created {
		return fmt.Errorf("candidate pool Task ref already exists")
	}
	if created, err := k.hubKeeper.AcquireBuilderSetTaskRef(ctx, scope.ref.TaskId, builderSet.BuilderSetId, builderSet.BuilderSetHash, currentHeight); err != nil {
		return fmt.Errorf("BuilderSet Task ref acquisition failed: %w", err)
	} else if !created {
		return fmt.Errorf("BuilderSet Task ref already exists")
	}
	if err := k.hubKeeper.AcquireParameterBucketTaskRef(ctx, shared.BucketKind_BUCKET_KIND_TIMEOUT, hubtypes.DefaultParameterBucketKey, bucket.Version, currentHeight); err != nil {
		return err
	}
	if err := k.TaskBucketRef.Set(ctx, types.NewTaskBucketRefKey(taskKey, int32(shared.BucketKind_BUCKET_KIND_TIMEOUT), hubtypes.DefaultParameterBucketKey), types.TaskBucketRefState{
		TaskId: scope.ref.TaskId, BucketKind: shared.BucketKind_BUCKET_KIND_TIMEOUT, BucketKey: hubtypes.DefaultParameterBucketKey,
		Version: bucket.Version, AcquiredHeight: currentHeight,
	}); err != nil {
		return err
	}
	if err := k.TaskCore.Set(ctx, taskKey, core); err != nil {
		return err
	}
	if err := k.TaskAssignment.Set(ctx, taskKey, assignment); err != nil {
		return err
	}
	if err := k.TaskBuilderSelection.Set(ctx, taskKey, selection); err != nil {
		return err
	}
	if err := k.acquireTaskBuilderEvidenceResponsibilities(ctx, core, selection); err != nil {
		return err
	}
	if err := k.TaskStageHandraiseUnion.Set(ctx, types.NewTaskStageKey(taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK), union); err != nil {
		return err
	}
	if err := k.TaskBudget.Set(ctx, taskKey, budget); err != nil {
		return err
	}
	if err := k.consumeOrderSequence(ctx, order.SessionId, order.OrderSequence, scope.ref.TaskId); err != nil {
		return err
	}
	if err := addDeadlineIndex(ctx, k.AssignmentRandomnessIndex, taskKey, windowClose); err != nil {
		return err
	}
	coins := sdk.NewCoins(sdk.NewCoin(hubParams.BusinessDenom, sdkmath.NewIntFromUint64(maxFee)))
	return k.bankKeeper.SendCoinsFromAccountToModule(ctx, sdk.AccAddress(userBytes), shared.TaskEscrowModuleName, coins)
}

func selectTaskBuilders(chainID string, taskID, anchorHash []byte, set hubtypes.BuilderSetState, count uint32) ([]string, error) {
	if count == 0 || uint32(len(set.ActiveBuilders)) != set.ActiveBuilderCount || count > set.ActiveBuilderCount {
		return nil, fmt.Errorf("BuilderSet cannot satisfy builders_per_task")
	}
	seed, err := types.TaskBuilderSeed(chainID, taskID, set.BuilderSetHash, anchorHash)
	if err != nil {
		return nil, err
	}
	type rankedBuilder struct {
		operator string
		raw      []byte
		rank     [32]byte
	}
	ranked := make([]rankedBuilder, len(set.ActiveBuilders))
	seen := make(map[string]struct{}, len(set.ActiveBuilders))
	for i, operator := range set.ActiveBuilders {
		raw, err := types.CanonicalOperatorAddressBytes("BuilderSet operator", operator)
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[operator]; duplicate {
			return nil, fmt.Errorf("BuilderSet contains a duplicate operator")
		}
		seen[operator] = struct{}{}
		rank, err := types.TaskBuilderRank(seed, operator)
		if err != nil {
			return nil, err
		}
		ranked[i] = rankedBuilder{operator: operator, raw: raw, rank: rank}
	}
	sort.Slice(ranked, func(i, j int) bool {
		if comparison := bytes.Compare(ranked[i].rank[:], ranked[j].rank[:]); comparison != 0 {
			return comparison < 0
		}
		return bytes.Compare(ranked[i].raw, ranked[j].raw) < 0
	})
	selected := make([]string, count)
	for i := range selected {
		selected[i] = ranked[i].operator
	}
	return selected, nil
}

func profileAllowsTaskType(allowed []shared.TaskType, taskType shared.TaskType) bool {
	for _, candidate := range allowed {
		if candidate == taskType {
			return taskType == shared.TaskType_TASK_TYPE_TEXT_GENERATION || taskType == shared.TaskType_TASK_TYPE_CHAT
		}
	}
	return false
}

func validatePhase0ExecutionProfile(profile hubtypes.ProfileStateSnapshot) error {
	verification := profile.ExecutionSnapshot.VerificationProfile
	batch := profile.ExecutionSnapshot.BatchVerification
	if len(profile.ExecutionSnapshotHash) != types.Hash32Len || len(verification.EvidenceSchemaHash) != types.Hash32Len ||
		profile.ExecutionSnapshot.RuntimeClass != shared.RuntimeClassV1 ||
		verification.JudgmentFunctionVersion != shared.JudgmentFunctionVersionV1 ||
		verification.CanonicalEncodingVersion != shared.CanonicalEncodingVersionV1 ||
		verification.MetricAggregateProofVersion != shared.MetricAggregateProofVersionV1 ||
		verification.VerificationMode != shared.VerificationMode_VERIFICATION_MODE_SINGLE_SAMPLE ||
		verification.TokenScope != shared.TokenScope_TOKEN_SCOPE_ALL_GENERATED_OUTPUT_TOKENS || batch.Enabled {
		return fmt.Errorf("verification profile is unsupported by the Phase 0 executor")
	}
	return nil
}
