package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// RebutDispatchCompensation accepts incomplete signed records; a completed
// winner is not required to prove that this ticket was rejected or withdrawn.
func (m msgServer) RebutDispatchCompensation(ctx context.Context, req *types.MsgRebutDispatchCompensation) (*types.MsgRebutDispatchCompensationResponse, error) {
	if req == nil || len(req.DispatchTicketHash) != types.Hash32Len || req.Record == nil {
		return nil, fmt.Errorf("ticket hash and signed record are required")
	}
	params, err := m.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if uint64(req.Size()) > params.Dispatch.MaxDispatchRecordBytes {
		return nil, fmt.Errorf("dispatch rebuttal exceeds byte cap")
	}
	row, err := m.k.readDispatchClaim(ctx, req.DispatchTicketHash)
	if err != nil {
		return nil, err
	}
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return nil, err
	}
	if row.Status != types.DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_PENDING_REBUTTAL || height > row.RebuttalDeadlineHeight {
		return nil, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_COMPENSATION_REBUTTAL_WINDOW_CLOSED, fmt.Errorf("claim is not within its rebuttal window"))
	}
	responder, err := m.k.authorizeDispatchResponse(ctx, row.BuilderAddress, row.BuilderSetId, req.ResponderAddress)
	if err != nil {
		return nil, err
	}
	attempt, err := m.k.verifyDispatchRebuttal(ctx, row, req.Record)
	if err != nil {
		return nil, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_COMPENSATION_REBUTTAL_INVALID, err)
	}
	var rejectDigest [32]byte
	if reject := req.GetReject(); reject != nil {
		if !bytes.Equal(reject.DispatchTicketHash, row.DispatchTicketHash) || !bytes.Equal(reject.TaskId, row.TaskId) || !bytes.Equal(reject.TaskHash, req.Record.TaskHash) || !sameCandidateMember(reject.Member, attempt.Member) || reject.Member.OperatorAddress != row.WorkerOperatorAddress {
			return nil, invalidDispatchTicket("reject does not match the claimed Worker and ticket")
		}
		rejectDigest, err = m.k.verifyDispatchMaterial(ctx, "task.v1.DispatchRejectV1", reject, &row.EarliestSubmitHeight)
		if err != nil {
			return nil, err
		}
		if attempt.Outcome == types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_REJECTED && !bytes.Equal(attempt.RejectDigestOrZero32, rejectDigest[:]) {
			return nil, invalidDispatchTicket("reject differs from the signed attempt digest")
		}
	}
	cacheCtx, commit := sdk.UnwrapSDKContext(ctx).CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	if req.GetReject() != nil {
		if _, err := m.k.hubKeeper.ApplyTaskRoleFault(cache, hubtypes.TaskRoleFaultFact{SessionID: row.SessionId, TaskID: row.TaskId, OperatorAddress: row.WorkerOperatorAddress, Duty: shared.DutyWorker, FaultType: hubtypes.FaultTypeWorkerDispatchContradiction, ClassificationSource: shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_OBJECTIVE_EVIDENCE, EvidenceDigest: rejectDigest[:], SlashBps: params.Dispatch.WorkerDispatchContradictionSlashBps, Height: height}); err != nil {
			return nil, err
		}
	}
	row.Status = types.DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_DISMISSED
	row.XRebuttedByOrEmpty = &types.DispatchCompensationClaimState_RebuttedByOrEmpty{RebuttedByOrEmpty: responder}
	row.XResolvedHeight = &types.DispatchCompensationClaimState_ResolvedHeight{ResolvedHeight: height}
	if err := m.k.writeDispatchClaim(cache, row); err != nil {
		return nil, err
	}
	if err := emitTypedEvent(cache, &types.EventDispatchCompensationRebutted{DispatchTicketHash: row.DispatchTicketHash, TaskId: row.TaskId, Builder: row.BuilderAddress, Worker: row.WorkerOperatorAddress, Responder: responder, AttemptOutcome: attempt.Outcome, WorkerFaultRecorded: req.GetReject() != nil}); err != nil {
		return nil, err
	}
	commit()
	return &types.MsgRebutDispatchCompensationResponse{DispatchTicketHash: bytes.Clone(row.DispatchTicketHash), ClaimStatus: row.Status, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED}, nil
}

func (k Keeper) authorizeDispatchResponse(ctx context.Context, builder, builderSetID, signer string) (string, error) {
	if _, _, err := k.canonicalAddress("responder_address", signer); err != nil {
		return "", err
	}
	current, err := k.hubKeeper.GetCurrentServiceKey(ctx, shared.ParticipantTypeBuilder, builder)
	if err == nil && current.Status == hubtypes.ServiceKeyStatusActive && current.ServiceAddress == signer {
		return builder, nil
	}
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return "", err
	}
	allowed := []string{builder}
	if builderSetID != "" {
		set, err := k.hubKeeper.GetBuilderSetByID(sdkContextFrom(ctx), builderSetID)
		if err != nil {
			return "", err
		}
		if set.BodyStatus != shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE || len(set.ActiveBuilders) != int(set.ActiveBuilderCount) {
			return "", fmt.Errorf("response BuilderSet body is unavailable")
		}
		allowed = set.ActiveBuilders
	}
	for _, operator := range allowed {
		binding, err := k.hubKeeper.GetCurrentServiceKey(ctx, shared.ParticipantTypeBuilder, operator)
		if errors.Is(err, collections.ErrNotFound) {
			continue
		}
		if err != nil {
			return "", err
		}
		if binding.Status == hubtypes.ServiceKeyStatusActive && binding.ServiceAddress == signer {
			return operator, nil
		}
	}
	return "", sdkerrors.ErrUnauthorized.Wrap("responder is not an authorized current Builder service")
}

func signedDispatchAttempts(record *types.DispatchRecordV1) ([]*types.DispatchAttemptV1, error) {
	var attempts []*types.DispatchAttemptV1
	var previous uint32
	for _, round := range record.Rounds {
		if round == nil {
			return nil, fmt.Errorf("dispatch record has a nil round")
		}
		for _, attempt := range round.Attempts {
			if attempt == nil || attempt.Member == nil || len(attempt.DispatchTicketHash) != types.Hash32Len || len(attempt.RejectDigestOrZero32) != types.Hash32Len || len(attempts) != 0 && attempt.Rank <= previous {
				return nil, fmt.Errorf("dispatch attempts are invalid or ranks are not strictly increasing")
			}
			if attempt.Outcome < types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_ACCEPTED || attempt.Outcome > types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_REVOKED {
				return nil, fmt.Errorf("dispatch attempt outcome is unknown")
			}
			previous = attempt.Rank
			attempts = append(attempts, attempt)
		}
	}
	return attempts, nil
}

func (k Keeper) verifyDispatchRebuttal(ctx context.Context, claim types.DispatchCompensationClaimState, record *types.DispatchRecordV1) (*types.DispatchAttemptV1, error) {
	if record.BuilderAddress != claim.BuilderAddress || !bytes.Equal(record.TaskId, claim.TaskId) || !bytes.Equal(record.SessionId, claim.SessionId) || record.OrderSequence != claim.OrderSequence {
		return nil, fmt.Errorf("signed rebuttal scope differs from the claim")
	}
	// The claim binds the original order even if a replacement ticket was
	// recorded under the same TaskID, or no record was accepted at all.
	if _, err := k.verifyDispatchMaterial(ctx, "task.v1.DispatchRecordV1", record, &claim.EarliestSubmitHeight); err != nil {
		return nil, err
	}
	// The ticket-based window was frozen at claim creation. Its first deadline
	// bounds this old record even if a later Session takeover changed live binding.
	attempts, err := signedDispatchAttempts(record)
	if err != nil {
		return nil, err
	}
	var found *types.DispatchAttemptV1
	for _, attempt := range attempts {
		if !bytes.Equal(attempt.DispatchTicketHash, claim.DispatchTicketHash) {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("claimed ticket appears twice")
		}
		found = attempt
	}
	if found == nil || found.Member.OperatorAddress != claim.WorkerOperatorAddress {
		return nil, fmt.Errorf("claimed Worker/ticket is absent from record")
	}
	switch found.Outcome {
	case types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_REJECTED, types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_TIMEOUT, types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_REVOKED:
		return found, nil
	default:
		return nil, fmt.Errorf("accepted or voided ticket remains compensable")
	}
}
