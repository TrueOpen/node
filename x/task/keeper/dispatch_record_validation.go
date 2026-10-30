package keeper

import (
	"bytes"
	"context"
	"fmt"
	"math"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	"github.com/TrueOpen/wire/dispatch"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func invalidDispatchTicket(reason string) error {
	return rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_TICKET_INVALID, fmt.Errorf("%s", reason))
}

func dispatchEntryIdentity(chainID string, entry *types.DispatchEntryV1) ([32]byte, error) {
	if entry == nil || entry.SignedOrder == nil || entry.Record == nil || entry.WinnerTicket == nil || entry.Accept == nil {
		return [32]byte{}, fmt.Errorf("dispatch entry needs order, record, winner ticket and accept")
	}
	hash, err := types.TaskOrderHash(entry.SignedOrder.Order)
	if err != nil {
		return [32]byte{}, err
	}
	id, err := types.DeriveTaskIDFromRawSession(entry.SignedOrder.Order.SessionId, entry.SignedOrder.Order.OrderSequence)
	if err != nil {
		return [32]byte{}, err
	}
	record, err := dispatchMaterialDigest("task.v1.DispatchRecordV1", entry.Record)
	if err != nil {
		return [32]byte{}, err
	}
	accept, err := dispatchMaterialDigest("task.v1.DispatchAcceptV1", entry.Accept)
	if err != nil {
		return [32]byte{}, err
	}
	return dispatch.DispatchEntryDigest(chainID, id, hash, record, accept), nil
}

func validateDispatchRecordStructure(record *types.DispatchRecordV1, ticket *types.DispatchTicketV1, ticketHash [32]byte, maxSkips uint32) error {
	if record == nil || ticket == nil || record.BuilderAddress != ticket.BuilderAddress || record.ReferenceHeight != ticket.ReferenceHeight ||
		!bytes.Equal(record.TaskId, ticket.TaskId) || !bytes.Equal(record.TaskHash, ticket.TaskHash) || !bytes.Equal(record.SessionId, ticket.SessionId) || record.OrderSequence != ticket.OrderSequence ||
		!bytes.Equal(record.CandidatePoolSnapshotId, ticket.CandidatePoolSnapshotId) || record.VrfEpoch != ticket.VrfEpoch || !bytes.Equal(record.VrfOutput, ticket.VrfOutput) || !bytes.Equal(record.VrfProof, ticket.VrfProof) ||
		record.RandomnessScheme != ticket.RandomnessScheme || record.BoundChangeHeight != ticket.BoundChangeHeight || record.StickyAnchorSequence != ticket.StickyAnchorSequence || record.StickyRunCount != ticket.StickyRunCount || !bytes.Equal(record.DispatchTicketHash, ticketHash[:]) {
		return invalidDispatchTicket("record and winner ticket scope differ")
	}
	if len(record.Rounds) == 0 {
		return invalidDispatchTicket("record has no dispatch rounds")
	}
	var previous uint32
	haveRank := false
	skips := uint64(0)
	seenTickets := make(map[string]struct{})
	for i, round := range record.Rounds {
		if round == nil || uint64(i) > math.MaxUint32 || round.DispatchRound != uint32(i) || len(round.Attempts) == 0 || len(round.RosterHash) != types.Hash32Len || len(round.HandraiseRootDigest) != types.Hash32Len {
			return invalidDispatchTicket("dispatch rounds are incomplete or not sequential")
		}
		for j, attempt := range round.Attempts {
			if attempt == nil || attempt.Member == nil || len(attempt.DispatchTicketHash) != types.Hash32Len || len(attempt.RejectDigestOrZero32) != types.Hash32Len || !bytes.Equal(attempt.Member.CandidatePoolSnapshotId, record.CandidatePoolSnapshotId) || (haveRank && attempt.Rank <= previous) {
				return invalidDispatchTicket("dispatch attempt identity or rank is invalid")
			}
			if _, duplicate := seenTickets[string(attempt.DispatchTicketHash)]; duplicate {
				return invalidDispatchTicket("dispatch attempts repeat a ticket hash")
			}
			seenTickets[string(attempt.DispatchTicketHash)] = struct{}{}
			previous, haveRank = attempt.Rank, true
			last := i == len(record.Rounds)-1 && j == len(round.Attempts)-1
			switch attempt.Outcome {
			case types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_ACCEPTED:
				if !last || ticket.DispatchRound != round.DispatchRound || ticket.Rank != attempt.Rank || !sameCandidateMember(attempt.Member, ticket.Target) || !bytes.Equal(attempt.DispatchTicketHash, ticketHash[:]) || !bytes.Equal(ticket.RosterHash, round.RosterHash) || !bytes.Equal(ticket.HandraiseRootDigest, round.HandraiseRootDigest) {
					return invalidDispatchTicket("the sole accepted attempt must be the record's winner")
				}
			case types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_REJECTED:
				if last || bytes.Equal(attempt.RejectDigestOrZero32, make([]byte, types.Hash32Len)) {
					return invalidDispatchTicket("rejected attempt lacks a reject digest")
				}
				skips++
			case types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_TIMEOUT:
				if last || attempt.TimeoutTierOrZero == 0 {
					return invalidDispatchTicket("timeout attempt is incomplete")
				}
				skips++
			case types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_VOIDED, types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_REVOKED:
				if last {
					return invalidDispatchTicket("record does not end in an accepted attempt")
				}
			default:
				return invalidDispatchTicket("dispatch attempt outcome is invalid")
			}
			if attempt.Outcome != types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_REJECTED && !bytes.Equal(attempt.RejectDigestOrZero32, make([]byte, types.Hash32Len)) {
				return invalidDispatchTicket("non-rejected attempt has a reject digest")
			}
			if attempt.Outcome != types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_TIMEOUT && attempt.TimeoutTierOrZero != 0 {
				return invalidDispatchTicket("non-timeout attempt has a timeout tier")
			}
		}
	}
	if skips > uint64(maxSkips) {
		return rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_SKIP_LIMIT_EXCEEDED, fmt.Errorf("dispatch skip limit exceeded"))
	}
	return nil
}

func (k Keeper) validateDispatchVrf(ctx context.Context, admission dispatchOrderAdmission, ticket *types.DispatchTicketV1) (hubtypes.CandidatePoolSnapshotState, error) {
	var empty hubtypes.CandidatePoolSnapshotState
	length := k.hubKeeper.GetHubParams(sdkContextFrom(ctx)).EpochLengthBlocks
	if length == 0 || !shared.IsBlockHeightV1(admission.order.EarliestSubmitHeight) {
		return empty, fmt.Errorf("dispatch epoch parameters are invalid")
	}
	epoch := admission.order.EarliestSubmitHeight / length
	if ticket.VrfEpoch != epoch {
		return empty, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_VRF_EPOCH_STALE, fmt.Errorf("ticket epoch differs from the signed order"))
	}
	reference := epoch * length
	if ticket.ReferenceHeight != reference {
		return empty, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_REFERENCE_HEIGHT_MISMATCH, fmt.Errorf("ticket must reference the exact epoch start"))
	}
	if sdkContextFrom(ctx).BlockHeight() <= int64(reference) {
		return empty, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_VRF_INVALID, fmt.Errorf("epoch-start beacon must already be committed"))
	}
	beacon, found := k.hubKeeper.GetBeaconForDomain(sdkContextFrom(ctx), "TRUEOPEN_DISPATCH_VRF_INPUT_V1", int64(reference))
	seed, err := dispatch.ResolveEpochSeed(admission.order.EarliestSubmitHeight, length, uint64(max(beacon.Height, 0)), beacon.Randomness, found && beacon.SourceTag == hubtypes.BeaconSourceProposerVRFV1 && len(beacon.ProofDigest) == types.Hash32Len)
	if err != nil {
		return empty, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_VRF_INVALID, err)
	}
	input := &types.DispatchVrfInputV1{ChainId: sdkContextFrom(ctx).ChainID(), Epoch: seed.Epoch, EpochSeed: seed.Randomness[:], SessionId: admission.order.SessionId, OrderSequence: admission.order.OrderSequence}
	alpha, err := dispatchMaterialDigest("task.v1.DispatchVrfInputV1", input)
	if err != nil {
		return empty, err
	}
	key, err := k.hubKeeper.GetBuilderVrfKeyForEpoch(ctx, ticket.BuilderAddress, epoch)
	if err != nil {
		return empty, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_BUILDER_VRF_KEY_MISSING, err)
	}
	if ticket.RandomnessScheme != types.DispatchRandomnessSchemeV1_DISPATCH_RANDOMNESS_SCHEME_V1_BUILDER_VRF {
		return empty, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_VRF_INVALID, fmt.Errorf("unsupported dispatch randomness scheme"))
	}
	if err := dispatch.VerifyDispatchVRF(key, alpha[:], ticket.VrfProof, ticket.VrfOutput); err != nil {
		return empty, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_VRF_INVALID, err)
	}
	pool, found := k.hubKeeper.GetCandidatePoolSnapshot(sdkContextFrom(ctx), ticket.CandidatePoolSnapshotId)
	if !found || pool.Epoch != epoch || pool.Status == hubtypes.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_PRUNED || pool.PublishedHeight == 0 {
		return empty, invalidDispatchTicket("referenced epoch pool is unavailable")
	}
	return pool, nil
}

func validateDispatchSticky(stream types.StreamState, order types.TaskOrderV3, ticket *types.DispatchTicketV1, params types.DispatchParamsV1) error {
	if ticket.BoundChangeHeight != stream.BoundChangeHeight {
		return rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_BOUND_CHANGE_HEIGHT_MISMATCH, fmt.Errorf("ticket binding generation differs from the session"))
	}
	if ticket.StickyAnchorSequence == order.OrderSequence && ticket.StickyRunCount == 1 {
		return nil
	}
	if stream.XLastRecordWinner != nil && ticket.Target != nil && ticket.Target.OperatorAddress == stream.GetLastRecordWinner() && ticket.StickyAnchorSequence == stream.StickyAnchorSequence && stream.StickyRunCount < math.MaxUint32 && ticket.StickyRunCount == stream.StickyRunCount+1 && ticket.StickyRunCount <= params.StickyMaxOrders && dispatchStickyWithinWindow(order.EarliestSubmitHeight, stream.StickyAnchorHeight, params.StickyWindowBlocks) {
		return nil
	}
	return rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_STICKY_MISMATCH, fmt.Errorf("ticket sticky commitment differs from the session"))
}

func dispatchStickyWithinWindow(earliest, anchor, window uint64) bool {
	elapsed := uint64(0)
	if earliest > anchor {
		elapsed = earliest - anchor
	}
	return elapsed <= window
}
