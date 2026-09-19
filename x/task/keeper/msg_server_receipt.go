package keeper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// SubmitInferReceipt validates every frozen receipt boundary and derives the
// complete verifier-window source without writing partial state.
func (m msgServer) SubmitInferReceipt(ctx context.Context, req *types.MsgSubmitInferReceipt) (*types.MsgSubmitInferReceiptResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "infer receipt request is required")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return nil, err
	}
	receipt := req.Receipt
	if receipt.SchemaVersion != types.InferReceiptSchemaVersionV2 || receipt.ChainId != sdkCtx.ChainID() ||
		len(receipt.TaskId) != types.Hash32Len || len(receipt.TaskHash) != types.Hash32Len ||
		len(receipt.GenerationParamsDigest) != types.Hash32Len || len(receipt.OutputHash) != types.Hash32Len ||
		receipt.OutputLeafCount == 0 || receipt.ExpiryHeight == 0 ||
		len(receipt.ServiceSignature) != 64 {
		return nil, status.Error(codes.InvalidArgument, "infer receipt envelope is invalid")
	}
	workerBytes, worker, err := m.k.canonicalAddress("worker_operator_address", receipt.WorkerOperatorAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if _, _, err := m.k.canonicalAddress("submitter_address", req.SubmitterAddress); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if uint64(len(receipt.RequiredEvidenceCommitments)) > uint64(types.MaxInferEvidenceCommitmentsLimit) {
		return nil, status.Error(codes.InvalidArgument, "required evidence commitment count exceeds the protocol hard limit")
	}
	commitmentBytes := uint64(4) // canonical uint32 count prefix
	for index, item := range receipt.RequiredEvidenceCommitments {
		frame, err := types.CanonicalEvidenceCommitmentFrameV1(item)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "required_evidence_commitments[%d]: %v", index, err)
		}
		nextBytes, overflow := checkedAddUint64(commitmentBytes, uint64(len(frame)))
		if overflow || nextBytes > types.MaxInferReceiptCommitmentBytesLimit {
			return nil, status.Error(codes.InvalidArgument, "required evidence commitment bytes exceed the protocol hard limit")
		}
		commitmentBytes = nextBytes
	}
	evidenceHash, err := types.EvidenceCommitmentsHash(receipt.RequiredEvidenceCommitments)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	signingDigest, err := inferReceiptSigningDigest(receipt)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	taskKey := types.NewTaskKey(receipt.TaskId)
	existing, found, err := m.k.getInferReceiptIfExists(ctx, taskKey)
	if err != nil {
		return nil, err
	}
	if found {
		signatureDigest := sha256.Sum256(receipt.ServiceSignature)
		if !inferReceiptReplayMatches(existing, receipt, evidenceHash[:], signingDigest[:], signatureDigest[:]) {
			return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "conflicting infer receipt replay")
		}
		window, err := m.k.VerifierCandidateWindow.Get(ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1))
		if err != nil || !bytes.Equal(window.TaskId, receipt.TaskId) || window.VerifyRound != types.VerifyRoundV1 ||
			!bytes.Equal(window.InferReceiptHash, existing.InferReceiptHash) || window.AssignmentDeadlineHeight == 0 {
			return nil, errorsmod.Wrap(types.ErrInvariantBroken, "retained infer receipt has no matching verifier window")
		}
		return &types.MsgSubmitInferReceiptResponse{
			TaskId: receipt.TaskId, InferReceiptHash: signingDigest[:],
			VerifyOpenDeadlineHeight: window.AssignmentDeadlineHeight,
			Status:                   shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP,
		}, nil
	}
	if err := m.k.requireInferReceiptSubmitter(ctx, taskKey, receipt.TaskId, worker, req.SubmitterAddress); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidSignature, err.Error())
	}
	params, err := m.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if uint64(len(receipt.RequiredEvidenceCommitments)) > uint64(params.Evidence.MaxInferEvidenceCommitmentsPerReceipt) {
		return nil, status.Error(codes.InvalidArgument, "required evidence commitment count exceeds the registered limit")
	}
	if commitmentBytes > params.Evidence.MaxInferReceiptCommitmentBytes {
		return nil, status.Error(codes.InvalidArgument, "required evidence commitment bytes exceed the registered limit")
	}
	if receipt.OutputLeafCount > params.Evidence.MaxOutputMmrLeaves {
		return nil, status.Error(codes.InvalidArgument, "output_leaf_count exceeds the registered limit")
	}
	if height > receipt.ExpiryHeight {
		return nil, status.Error(codes.InvalidArgument, "infer receipt credential has expired")
	}
	core, err := m.k.TaskCore.Get(ctx, taskKey)
	if err != nil || !bytes.Equal(core.TaskId, receipt.TaskId) || !bytes.Equal(core.AcceptedTaskHash, receipt.TaskHash) ||
		core.TaskPhase != types.TaskPhase_TASK_PHASE_WORKER_ASSIGNED || core.ReceiptStatus != types.ReceiptStatus_RECEIPT_STATUS_NONE {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "infer receipt task scope does not match")
	}
	assignment, err := m.k.TaskAssignment.Get(ctx, taskKey)
	if err != nil || assignment.WinnerWorker != receipt.WorkerOperatorAddress ||
		!bytes.Equal(assignment.GenerationParamsDigest, receipt.GenerationParamsDigest) ||
		len(assignment.CandidatePoolSnapshotId) != types.Hash32Len || len(assignment.CandidatePoolHash) != types.Hash32Len ||
		len(assignment.ProfileExecutionSnapshotHash) != types.Hash32Len || assignment.CandidatePoolRefReleased ||
		assignment.InferDeadlineHeight == 0 || height > assignment.InferDeadlineHeight {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "infer receipt does not match the timely winning assignment")
	}
	budget, err := m.k.TaskBudget.Get(ctx, taskKey)
	if err != nil || budget.MaxOutputTokens == 0 || receipt.GeneratedTokenCount > budget.MaxOutputTokens {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "generated_token_count exceeds the frozen Task limit")
	}
	if err := m.k.requireCurrentCortexAuthorization(ctx, sdkCtx, workerBytes, worker, receipt.ServiceAuthorizationNonce); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidSignature, err.Error())
	}
	if err := m.k.hubKeeper.VerifyCurrentCortexServiceDigest(ctx, worker, receipt.ServiceSignature, signingDigest[:], height); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidSignature, err.Error())
	}
	profile, found := m.k.hubKeeper.GetProfileState(sdkCtx, core.ModelId, core.ProfileVersion)
	if !found || len(profile.ExecutionSnapshotHash) != types.Hash32Len ||
		!bytes.Equal(profile.ExecutionSnapshotHash, assignment.ProfileExecutionSnapshotHash) {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "locked Profile execution snapshot is unavailable")
	}
	if err := validateLockedProfileEvidenceCommitments(core, assignment, profile, receipt.RequiredEvidenceCommitments); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	hubParams := m.k.hubKeeper.GetHubParams(sdkCtx)
	clock, err := freezeVerifierWindowClock(height, verifierWindowClockParams{
		DeltaWBlocks:                hubParams.DeltaWBlocks,
		BuilderProposalWindowBlocks: hubParams.OpenVerifyBuilderProposalWindowBlocks,
		SelfRescueMarginBlocks:      params.Deadlines.SelfRescueMarginBlocks,
		VerifyOpenDeadlineBlocks:    params.Deadlines.VerifyOpenDeadlineBlocks,
	})
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	slotCapacity, segmentBytes, candidates, err := m.k.deriveVerifierEligibilityCandidates(
		ctx, core, assignment, hubParams,
	)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	windowSize, err := verifierWindowSize(
		uint32(len(candidates)), params.Weights.VerifierCandidateRatioPpm,
		params.Weights.VerifierCandidateWindowMin, params.Weights.VerifierCandidateWindowMax,
	)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	header := types.VerifierCandidateWindowState{
		SchemaVersion: 1, TaskId: append([]byte(nil), receipt.TaskId...), VerifyRound: types.VerifyRoundV1,
		InferReceiptHash:        append([]byte(nil), signingDigest[:]...),
		CandidatePoolSnapshotId: append([]byte(nil), assignment.CandidatePoolSnapshotId...),
		CandidatePoolHash:       append([]byte(nil), assignment.CandidatePoolHash...), EligibilityFrozenHeight: height,
		WindowRandomnessHeight:     clock.WindowRandomnessHeight,
		BuilderProposalCloseHeight: clock.BuilderProposalCloseHeight,
		HandraiseCloseHeight:       clock.HandraiseCloseHeight,
		SelectionRandomnessHeight:  clock.SelectionRandomnessHeight,
		AssignmentDeadlineHeight:   clock.AssignmentDeadlineHeight, WindowSize: windowSize,
	}
	header, segments, err := freezeVerifierEligibilitySource(sdkCtx.ChainID(), header, slotCapacity, segmentBytes, candidates)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	windowKey := types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1)
	if exists, err := m.k.VerifierCandidateWindow.Has(cache, windowKey); err != nil || exists {
		if err != nil {
			return nil, err
		}
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "verifier candidate window already exists without a receipt")
	}
	buildKey := types.NewVerifyRoundIndexKey(header.WindowRandomnessHeight, taskKey, types.VerifyRoundV1)
	closeKey := types.NewVerifyRoundIndexKey(header.HandraiseCloseHeight, taskKey, types.VerifyRoundV1)
	deadlineKey := types.NewDeadlineIndexKey(header.AssignmentDeadlineHeight, taskKey)
	if exists, err := m.k.VerifierWindowBuildIndex.Has(cache, buildKey); err != nil || exists {
		if err != nil {
			return nil, err
		}
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "verifier window build index already exists without a receipt")
	}
	if exists, err := m.k.VerifierHandraiseCloseIndex.Has(cache, closeKey); err != nil || exists {
		if err != nil {
			return nil, err
		}
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "verifier handraise close index already exists without a receipt")
	}
	if exists, err := m.k.VerifyOpenDeadlineIndex.Has(cache, deadlineKey); err != nil || exists {
		if err != nil {
			return nil, err
		}
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "verify-open deadline index already exists without a receipt")
	}
	signatureDigest := sha256.Sum256(receipt.ServiceSignature)
	receiptState := types.InferReceiptState{
		TaskId: append([]byte(nil), receipt.TaskId...), WinnerWorker: worker,
		InferReceiptHash:       append([]byte(nil), signingDigest[:]...),
		GenerationParamsDigest: append([]byte(nil), receipt.GenerationParamsDigest...),
		OutputHash:             append([]byte(nil), receipt.OutputHash...), OutputSizeBytes: receipt.OutputSizeBytes,
		GeneratedTokenCount:         receipt.GeneratedTokenCount,
		EvidenceCommitmentsHash:     append([]byte(nil), evidenceHash[:]...),
		RequiredEvidenceCommitments: cloneEvidenceCommitments(receipt.RequiredEvidenceCommitments),
		EvidenceCommitmentCount:     uint32(len(receipt.RequiredEvidenceCommitments)),
		InferReceiptSigningDigest:   append([]byte(nil), signingDigest[:]...),
		SignatureDigest:             append([]byte(nil), signatureDigest[:]...), ExpiryHeight: receipt.ExpiryHeight,
		ReceiptHeight: height, OutputLeafCount: receipt.OutputLeafCount,
	}
	core.TaskPhase = types.TaskPhase_TASK_PHASE_RECEIPT_COMMITTED
	core.ReceiptStatus = types.ReceiptStatus_RECEIPT_STATUS_RECEIPT_ACCEPTED
	core.VerificationStatus = types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_WINDOW_PENDING
	core.UpdatedHeight = height
	if err := m.k.InferReceipt.Set(cache, taskKey, receiptState); err != nil {
		return nil, err
	}
	if err := m.k.VerifierCandidateWindow.Set(cache, windowKey, header); err != nil {
		return nil, err
	}
	for _, segment := range segments {
		if bitmapIsZero(segment.Bitmap) {
			continue
		}
		segmentKey := types.NewVerifierEligibilitySegmentKey(taskKey, types.VerifyRoundV1, segment.SegmentIndex)
		if exists, err := m.k.VerifierCandidateEligibilitySegment.Has(cache, segmentKey); err != nil || exists {
			if err != nil {
				return nil, err
			}
			return nil, errorsmod.Wrap(types.ErrInvariantBroken, "verifier eligibility segment already exists without a receipt")
		}
		if err := m.k.VerifierCandidateEligibilitySegment.Set(cache, segmentKey, segment); err != nil {
			return nil, err
		}
	}
	if err := m.k.TaskCore.Set(cache, taskKey, core); err != nil {
		return nil, err
	}
	if err := m.k.reserveWorkerOutputEvidenceResponsibility(cache, core, worker, height); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	if err := removeDeadlineIndex(cache, m.k.InferDeadlineIndex, taskKey, assignment.InferDeadlineHeight); err != nil {
		return nil, err
	}
	if err := m.k.VerifierWindowBuildIndex.Set(cache, buildKey); err != nil {
		return nil, err
	}
	if err := m.k.VerifierHandraiseCloseIndex.Set(cache, closeKey); err != nil {
		return nil, err
	}
	if err := m.k.VerifyOpenDeadlineIndex.Set(cache, deadlineKey); err != nil {
		return nil, err
	}
	// The Hub's beacon consumer ref is still a string identity (A-15b), so the
	// task is rendered at the boundary instead of the raw key being handed over.
	if err := m.k.hubKeeper.AcquireBeaconConsumerRef(
		cache, header.WindowRandomnessHeight, hubtypes.BeaconConsumerKindVerifierWindow, hex32(taskKey),
	); err != nil {
		return nil, err
	}
	if err := m.k.hubKeeper.AcquireBeaconConsumerRef(
		cache, header.SelectionRandomnessHeight, hubtypes.BeaconConsumerKindVerifierSelection, hex32(taskKey),
	); err != nil {
		return nil, err
	}
	selection, err := m.k.TaskBuilderSelection.Get(cache, taskKey)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "Task Builder selection is unavailable at verifier-window freeze")
	}
	if err := m.k.reserveBuilderStageResponsibilities(
		cache, core, selection, serviceKeyResponsibilityOpenVerifyBuilder, height,
	); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	if err := emitTypedEvent(cache, &types.EventInferReceiptAccepted{
		SessionId: append([]byte(nil), core.SessionId...), TaskId: append([]byte(nil), receipt.TaskId...),
		Worker: worker, InferReceiptHash: append([]byte(nil), signingDigest[:]...),
		OutputHash:                     append([]byte(nil), receipt.OutputHash...),
		GeneratedTokenCount:            receipt.GeneratedTokenCount,
		VerifierWindowRandomnessHeight: header.WindowRandomnessHeight,
		OutputLeafCount:                receipt.OutputLeafCount,
	}); err != nil {
		return nil, err
	}
	if err := m.k.recordTaskGasReimbursementIntent(cache, receipt.TaskId, 0,
		types.TaskReimbursementKindV1_TASK_REIMBURSEMENT_KIND_V1_INFER_RECEIPT); err != nil {
		return nil, err
	}
	write()
	return &types.MsgSubmitInferReceiptResponse{
		TaskId: append([]byte(nil), receipt.TaskId...), InferReceiptHash: append([]byte(nil), signingDigest[:]...),
		VerifyOpenDeadlineHeight: header.AssignmentDeadlineHeight,
		Status:                   shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED,
	}, nil
}

func validateLockedProfileEvidenceCommitments(
	core types.TaskCoreState,
	assignment types.TaskAssignmentState,
	profile hubtypes.ProfileStateSnapshot,
	commitments []types.EvidenceCommitmentV1,
) error {
	if profile.ModelID != core.ModelId || profile.ProfileVersion != core.ProfileVersion {
		return fmt.Errorf("locked Profile scope does not match the Task")
	}
	verification := profile.ExecutionSnapshot.VerificationProfile
	if !bytes.Equal(verification.EvidenceSchemaHash, assignment.EvidenceSchemaHash) {
		return fmt.Errorf("locked Profile evidence_schema_hash does not match the assignment")
	}
	projection := shared.ModelProfileProjection{
		ModelId: core.ModelId, ProfileVersion: core.ProfileVersion,
		TokenizerHash:       profile.ExecutionSnapshot.TokenizerHash,
		RequiredTopK:        profile.ExecutionSnapshot.RequiredTopK,
		GenerationType:      profile.ExecutionSnapshot.GenerationType,
		VerificationProfile: verification,
		BatchVerification:   profile.ExecutionSnapshot.BatchVerification,
		SchemaHash:          profile.ExecutionSnapshot.SchemaHash,
	}
	recomputed, err := hubtypes.EvidenceSchemaHash(projection)
	if err != nil || !bytes.Equal(recomputed, verification.EvidenceSchemaHash) {
		return fmt.Errorf("locked Profile evidence_schema does not match its hash")
	}
	if err := shared.ValidateEvidenceSchemaV1(verification.EvidenceSchema); err != nil {
		return fmt.Errorf("locked Profile evidence_schema is invalid: %w", err)
	}
	requirements := verification.EvidenceSchema.RequiredInferEvidence
	if len(commitments) != len(requirements) {
		return fmt.Errorf("required_evidence_commitments must exactly match the locked Profile requirement count")
	}
	for index, requirement := range requirements {
		item := commitments[index]
		if item.EvidenceKind != requirement.EvidenceKind {
			return fmt.Errorf("required_evidence_commitments[%d] kind does not match the locked Profile", index)
		}
		if requirement.CommitmentSchemaVersion != types.WorkerValueCommitmentSchemaVersionV2 ||
			requirement.EvidenceKind != shared.EvidenceKind_EVIDENCE_KIND_WORKER_VALUE_OPENING {
			return fmt.Errorf("required_evidence_commitments[%d] uses an unsupported commitment schema", index)
		}
		if item.EncodedSizeBytes == 0 || item.EncodedSizeBytes > requirement.MaxEncodedSizeBytes {
			return fmt.Errorf("required_evidence_commitments[%d] encoded_size_bytes exceeds the locked Profile limit", index)
		}
	}
	return nil
}

func cloneEvidenceCommitments(items []types.EvidenceCommitmentV1) []types.EvidenceCommitmentV1 {
	cloned := make([]types.EvidenceCommitmentV1, len(items))
	for index, item := range items {
		cloned[index] = types.EvidenceCommitmentV1{
			EvidenceKind: item.EvidenceKind, EvidenceHashOrRoot: append([]byte(nil), item.EvidenceHashOrRoot...),
			EncodedSizeBytes: item.EncodedSizeBytes,
		}
	}
	return cloned
}

func bitmapIsZero(bitmap []byte) bool {
	for _, value := range bitmap {
		if value != 0 {
			return false
		}
	}
	return true
}

func (k Keeper) requireInferReceiptSubmitter(ctx context.Context, taskKey types.TaskKey, taskID []byte, worker, submitter string) error {
	if err := k.RequireCurrentServiceSubmitter(ctx, shared.ParticipantTypeCortexNode, worker, submitter); err == nil {
		return nil
	}
	selection, err := k.loadActiveTaskBuilderSelection(ctx, taskKey, taskID)
	if err != nil {
		return errorsmod.Wrap(types.ErrInvalidAssignment, "active Task Builder selection is unavailable")
	}
	for _, builder := range selection.SelectedTaskBuilders {
		if err := k.RequireCurrentServiceSubmitter(ctx, shared.ParticipantTypeBuilder, builder, submitter); err == nil {
			return nil
		}
	}
	return errorsmod.Wrap(types.ErrInvalidAssignment, "submitter is neither the winner Worker nor a selected Task Builder current service")
}

func inferReceiptReplayMatches(
	state types.InferReceiptState,
	receipt types.InferReceiptV2,
	evidenceHash, signingDigest, signatureDigest []byte,
) bool {
	if !bytes.Equal(state.TaskId, receipt.TaskId) || state.WinnerWorker != receipt.WorkerOperatorAddress ||
		!bytes.Equal(state.InferReceiptHash, signingDigest) || !bytes.Equal(state.GenerationParamsDigest, receipt.GenerationParamsDigest) ||
		!bytes.Equal(state.OutputHash, receipt.OutputHash) || state.OutputSizeBytes != receipt.OutputSizeBytes ||
		!bytes.Equal(state.EvidenceCommitmentsHash, evidenceHash) || state.EvidenceCommitmentCount != uint32(len(receipt.RequiredEvidenceCommitments)) ||
		!bytes.Equal(state.InferReceiptSigningDigest, signingDigest) || !bytes.Equal(state.SignatureDigest, signatureDigest) ||
		state.ExpiryHeight != receipt.ExpiryHeight || state.GeneratedTokenCount != receipt.GeneratedTokenCount ||
		state.OutputLeafCount != receipt.OutputLeafCount {
		return false
	}
	if len(state.RequiredEvidenceCommitments) == 0 {
		return true
	}
	if len(state.RequiredEvidenceCommitments) != len(receipt.RequiredEvidenceCommitments) {
		return false
	}
	for index := range state.RequiredEvidenceCommitments {
		stored := state.RequiredEvidenceCommitments[index]
		requested := receipt.RequiredEvidenceCommitments[index]
		if stored.EvidenceKind != requested.EvidenceKind || stored.EncodedSizeBytes != requested.EncodedSizeBytes ||
			!bytes.Equal(stored.EvidenceHashOrRoot, requested.EvidenceHashOrRoot) {
			return false
		}
	}
	return true
}

func (k Keeper) getInferReceiptIfExists(ctx context.Context, taskKey types.TaskKey) (types.InferReceiptState, bool, error) {
	state, err := k.InferReceipt.Get(ctx, taskKey)
	if err != nil {
		if errIsNotFound(err) {
			return types.InferReceiptState{}, false, nil
		}
		return types.InferReceiptState{}, false, err
	}
	return state, true, nil
}

// inferReceiptSigningDigest is the only Keeper entry point to the frozen
// TRUEOPEN_INFER_RECEIPT_V2 preimage.
func inferReceiptSigningDigest(receipt types.InferReceiptV2) ([32]byte, error) {
	return types.InferReceiptSigningDigest(receipt)
}
