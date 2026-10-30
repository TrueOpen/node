package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
	"github.com/TrueOpen/wire/dispatch"
)

func (m msgServer) RespondDispatchChallenge(ctx context.Context, req *types.MsgRespondDispatchChallenge) (*types.MsgRespondDispatchChallengeResponse, error) {
	if req == nil || len(req.ChallengeId) != types.Hash32Len {
		return nil, fmt.Errorf("challenge_id must be raw32")
	}
	previous, err := m.k.readDispatchChallenge(ctx, req.ChallengeId)
	if err != nil {
		return nil, err
	}
	row, mutation, err := m.k.runDispatchChallenge(ctx, previous.TaskId, previous.Kind, req.Size(), func(ctx context.Context, params types.TaskParamsV1, height uint64) (types.DispatchChallengeState, shared.MutationStatusV1, error) {
		row, err := m.k.readDispatchChallenge(ctx, req.ChallengeId)
		if err != nil {
			return row, 0, err
		}
		if !dispatchChallengePending(row.Status) || height > row.GetProofDeadlineHeight() {
			return row, 0, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_CHALLENGE_WINDOW_CLOSED, fmt.Errorf("dispatch challenge response window is closed"))
		}
		record, err := m.k.readDispatchRecord(ctx, row.TaskId)
		if err != nil {
			return row, 0, err
		}
		if !bytes.Equal(record.DispatchRecordHash, row.GetDispatchRecordHash()) {
			return row, 0, fmt.Errorf("challenge retained record identity mismatch")
		}
		if row.Kind == shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_SKIP {
			if _, err := m.k.authorizeDispatchResponse(ctx, row.BuilderAddress, "", req.ResponderAddress); err != nil {
				return row, 0, err
			}
			reject := req.GetReject()
			attempt, err := dispatchAttemptByRank(record, row.GetAttemptRank())
			if err != nil {
				return row, 0, err
			}
			core, err := m.k.TaskCore.Get(ctx, row.TaskId)
			if err != nil {
				return row, 0, err
			}
			if reject == nil || !bytes.Equal(reject.TaskId, row.TaskId) || !bytes.Equal(reject.TaskHash, core.AcceptedTaskHash) || !bytes.Equal(reject.DispatchTicketHash, row.GetChallengedTicketHash()) || !sameCandidateMember(reject.Member, attempt.Member) {
				return row, 0, invalidDispatchChallengeProof("reject response scope mismatch")
			}
			digest, err := m.k.verifyDispatchMaterial(ctx, "task.v1.DispatchRejectV1", reject, &record.EarliestSubmitHeight)
			if err != nil {
				return row, 0, err
			}
			if !bytes.Equal(digest[:], attempt.RejectDigestOrZero32) {
				return row, 0, invalidDispatchChallengeProof("reject response digest differs from the committed skip")
			}
		} else {
			selection, err := m.k.GetTaskBuilderSelection(ctx, row.TaskId)
			if err != nil {
				return row, 0, err
			}
			if _, err := m.k.authorizeDispatchResponse(ctx, row.BuilderAddress, selection.BuilderSetId, req.ResponderAddress); err != nil {
				return row, 0, err
			}
			if _, err := m.k.committedDispatchRoster(ctx, record, row.GetDispatchRound(), req.GetRoster(), params.Dispatch); err != nil {
				return row, 0, invalidDispatchChallengeProof(err.Error())
			}
		}
		row.Status = types.DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_DISMISSED
		row.XResolvedHeight = &types.DispatchChallengeState_ResolvedHeight{ResolvedHeight: height}
		if err := m.k.writeDispatchChallenge(ctx, row); err != nil {
			return row, 0, err
		}
		return row, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, emitDispatchChallengeResolved(ctx, row)
	})
	if err != nil {
		return nil, err
	}
	return &types.MsgRespondDispatchChallengeResponse{Challenge: &row, Status: mutation}, nil
}

func (k Keeper) matureDispatchChallenge(ctx context.Context, id []byte, deadline, height uint64) error {
	row, err := k.readDispatchChallenge(ctx, id)
	if err != nil {
		return err
	}
	if !dispatchChallengePending(row.Status) || row.GetProofDeadlineHeight() != deadline {
		return k.DispatchChallengeResponse.Remove(ctx, collections.Join(deadline, id))
	}
	if height <= deadline {
		return nil
	}
	key, err := k.dispatchUpheldScopeKey(row)
	if err != nil {
		return err
	}
	if _, err := k.DispatchUpheldScope.Get(ctx, key); err == nil {
		row.Status = types.DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_EXPIRED
		row.XResolvedHeight = &types.DispatchChallengeState_ResolvedHeight{ResolvedHeight: height}
		if err := k.writeDispatchChallenge(ctx, row); err != nil {
			return err
		}
		return emitDispatchChallengeResolved(ctx, row)
	} else if !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	if row.Kind == shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_SKIP {
		record, err := k.readDispatchRecord(ctx, row.TaskId)
		if err != nil {
			return err
		}
		attempt, err := dispatchAttemptByRank(record, row.GetAttemptRank())
		if err != nil {
			return err
		}
		// A missing authoritative proof key is an invariant failure, not evidence
		// that an honest Builder withheld the signed rejection.
		if _, err := k.dispatchProofKey(ctx, dispatch.ServiceProofSigner{ParticipantType: dispatch.ProofParticipantCortex, OperatorAddress: attempt.Member.OperatorAddress}); err != nil {
			return err
		}
	}
	core, err := k.TaskCore.Get(ctx, row.TaskId)
	if err != nil {
		return err
	}
	params, err := k.ParamsForTask(ctx, row.TaskId)
	if err != nil {
		return err
	}
	_, _, err = k.upholdDispatchChallenge(ctx, row, core.SessionId, 0, params, height)
	return err
}
