package keeper

import (
	"bytes"
	"context"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// ReportDataUnavailable records the immutable pre-deadline report. Deadline
// close remains a separate consumer that invalidates reports followed by a
// timely accepted commit and performs aggregate fault accounting.
func (m msgServer) ReportDataUnavailable(ctx context.Context, req *types.MsgReportDataUnavailable) (*types.MsgReportDataUnavailableResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if len(req.TaskId) != types.Hash32Len || bytes.Equal(req.TaskId, make([]byte, types.Hash32Len)) {
		return nil, status.Errorf(codes.InvalidArgument, "task_id must be a non-zero %d-byte hash", types.Hash32Len)
	}
	if !isPhase0VerifyRound(req.VerifyRound) {
		return nil, status.Error(codes.InvalidArgument, "verify_round is unsupported")
	}
	if len(req.UnavailableTaskBuilderBitmap) == 0 {
		return nil, status.Error(codes.InvalidArgument, "unavailable_task_builder_bitmap must be non-empty")
	}
	if _, _, err := m.k.canonicalAddress("submitter_address", req.SubmitterAddress); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	height, err := currentBlockHeight(cache)
	if err != nil {
		return nil, err
	}
	taskKey := types.NewTaskKey(req.TaskId)
	core, err := m.k.TaskCore.Get(cache, taskKey)
	if err != nil || !bytes.Equal(core.TaskId, req.TaskId) {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "task core unavailable")
	}
	assignment, err := m.k.ReadVerifierAssignment(cache, types.NewVerifyRoundKey(taskKey, req.VerifyRound))
	if err != nil || validateVerifierAssignmentScope(assignment, req.TaskId, req.VerifyRound) != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "verifier assignment unavailable")
	}
	if err := m.k.validateActiveVerifierStage(cache, taskKey, core, req.VerifyRound,
		types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_ASSIGNED,
		types.VerificationStatus_VERIFICATION_STATUS_COMMITTING); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "task is not accepting data unavailable reports")
	}
	selection, err := m.k.GetTaskBuilderSelection(cache, taskKey)
	if err != nil || selection.BodyStatus != shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE ||
		selection.SelectedTaskBuilderCount == 0 || selection.SelectedTaskBuilderCount != uint32(len(selection.SelectedTaskBuilders)) {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "active Task Builder selection unavailable")
	}
	if _, err := DecodeDataUnavailableBuilderBitmap(req.UnavailableTaskBuilderBitmap, selection.SelectedTaskBuilderCount); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	verifier, nonce, err := m.k.resolveDataUnavailableSubmitter(cache, cacheCtx, assignment, req.SubmitterAddress)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidSignature, err.Error())
	}
	attempt := DataUnavailableReportAttempt{
		TaskID: append([]byte(nil), req.TaskId...), VerifyRound: req.VerifyRound,
		VerifierOperatorAddress: verifier, UnavailableTaskBuilderBitmap: append([]byte(nil), req.UnavailableTaskBuilderBitmap...),
		ServiceAuthorizationNonceSnapshot: nonce,
	}
	key := types.NewVerifyActorKey(taskKey, req.VerifyRound, verifier)
	existing, found, err := m.k.getDataUnavailableReportIfExists(cache, key)
	if err != nil {
		return nil, err
	}
	if found {
		decision, digest, err := ClassifyDataUnavailableReportReplay(&existing, attempt)
		if err != nil {
			return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, err.Error())
		}
		if decision != DataUnavailableReplayDecisionNoop {
			return nil, errorsmod.Wrap(types.ErrInvariantBroken, "unexpected data unavailable replay decision")
		}
		return &types.MsgReportDataUnavailableResponse{ReportDigest: digest, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP}, nil
	}
	if assignment.CommitDeadlineHeight == 0 || height > assignment.CommitDeadlineHeight {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "commit deadline has passed")
	}
	commitKey, err := types.DeriveCommitKey(sdkCtx.ChainID(), req.TaskId, req.VerifyRound, verifier)
	if err != nil {
		return nil, err
	}
	if _, found, err := m.k.getCommitStateIfExists(cache, types.NewCommitKey(commitKey[:])); err != nil {
		return nil, err
	} else if found {
		return nil, errorsmod.Wrap(types.ErrInvalidOpenVerify, "accepted verifier commit already proves data delivery")
	}
	reportDigest, err := DataUnavailableReportDigest(sdkCtx.ChainID(), attempt)
	if err != nil {
		return nil, err
	}
	bitmapHash, err := DataUnavailableBitmapHash(sdkCtx.ChainID(), req.TaskId, req.VerifyRound, selection.SelectedTaskBuilderCount, req.UnavailableTaskBuilderBitmap)
	if err != nil {
		return nil, err
	}
	state := types.DataUnavailableReportState{
		TaskId: attempt.TaskID, VerifyRound: attempt.VerifyRound, VerifierOperatorAddress: verifier,
		UnavailableTaskBuilderBitmap:      attempt.UnavailableTaskBuilderBitmap,
		ServiceAuthorizationNonceSnapshot: nonce, ReportHeight: height, ReportDigest: reportDigest,
	}
	if err := m.k.WriteDataUnavailableReport(cache, key, state); err != nil {
		return nil, err
	}
	if err := emitTypedEvent(cache, &types.EventDataUnavailableReported{
		SessionId: core.SessionId, TaskId: req.TaskId, VerifyRound: req.VerifyRound, Verifier: verifier,
		UnavailableBuilderBitmapHash: bitmapHash, ReportDigest: reportDigest,
	}); err != nil {
		return nil, err
	}
	if err := m.k.recordTaskGasReimbursementIntent(cache, req.TaskId, 0,
		types.TaskReimbursementKindV1_TASK_REIMBURSEMENT_KIND_V1_DATA_UNAVAILABLE_REPORT); err != nil {
		return nil, err
	}
	write()
	return &types.MsgReportDataUnavailableResponse{ReportDigest: reportDigest, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED}, nil
}

func (k Keeper) resolveDataUnavailableSubmitter(
	ctx context.Context,
	sdkCtx sdk.Context,
	assignment types.VerifierAssignmentState,
	submitter string,
) (string, uint64, error) {
	for _, selected := range assignment.SelectedVerifiers {
		if err := k.RequireCurrentServiceSubmitter(ctx, shared.ParticipantTypeCortexNode, selected.OperatorAddress, submitter); err != nil {
			continue
		}
		operatorBytes, operator, err := k.canonicalAddress("selected verifier operator", selected.OperatorAddress)
		if err != nil {
			return "", 0, err
		}
		node, found := k.hubKeeper.GetCortexNode(sdkCtx, sdk.AccAddress(operatorBytes))
		if !found || node.OperatorAddress != operator || node.ServiceKeyStatus != hubtypes.ServiceKeyStatusActive || node.ServiceAuthorizationNonce == 0 {
			return "", 0, errorsmod.Wrap(types.ErrInvariantBroken, "selected verifier current service binding is unavailable")
		}
		return operator, node.ServiceAuthorizationNonce, nil
	}
	return "", 0, errorsmod.Wrap(types.ErrInvalidSignature, "submitter is not a selected verifier current service")
}

func (k Keeper) getDataUnavailableReportIfExists(ctx context.Context, key types.VerifyActorKey) (types.DataUnavailableReportState, bool, error) {
	state, err := k.ReadDataUnavailableReport(ctx, key)
	if err != nil {
		if errIsNotFound(err) {
			return types.DataUnavailableReportState{}, false, nil
		}
		return types.DataUnavailableReportState{}, false, err
	}
	return state, true, nil
}
