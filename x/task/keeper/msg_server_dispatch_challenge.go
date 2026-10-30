package keeper

import (
	"bytes"
	"context"
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
	"github.com/TrueOpen/wire/dispatch"
)

func (m msgServer) ChallengeDispatchSkip(ctx context.Context, req *types.MsgChallengeDispatchSkip) (*types.MsgChallengeDispatchSkipResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("dispatch skip challenge is required")
	}
	row, mutation, err := m.k.runDispatchChallenge(ctx, req.TaskId, shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_SKIP, req.Size(), func(ctx context.Context, params types.TaskParamsV1, height uint64) (types.DispatchChallengeState, shared.MutationStatusV1, error) {
		record, _, row, err := m.k.recordedDispatchChallenge(ctx, req.TaskId, req.ChallengerAddress, shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_SKIP, params, height)
		if err != nil {
			return row, 0, err
		}
		attempt, err := dispatchAttemptByRank(record, req.AttemptRank)
		if err != nil || attempt.Outcome != types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_REJECTED {
			return row, 0, invalidDispatchChallengeProof("only a retained REJECTED rank may be challenged")
		}
		row.XAttemptRank = &types.DispatchChallengeState_AttemptRank{AttemptRank: req.AttemptRank}
		row.XChallengedTicketHash = &types.DispatchChallengeState_ChallengedTicketHash{ChallengedTicketHash: bytes.Clone(attempt.DispatchTicketHash)}
		if err := m.k.identifyDispatchChallenge(ctx, &row); err != nil {
			return row, 0, err
		}
		return m.k.openDispatchChallenge(ctx, row, params, height)
	})
	if err != nil {
		return nil, err
	}
	return &types.MsgChallengeDispatchSkipResponse{Challenge: &row, Status: mutation}, nil
}

func (m msgServer) ReportDispatchRosterUnavailable(ctx context.Context, req *types.MsgReportDispatchRosterUnavailable) (*types.MsgReportDispatchRosterUnavailableResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("dispatch roster report is required")
	}
	row, mutation, err := m.k.runDispatchChallenge(ctx, req.TaskId, shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_ROSTER_UNAVAILABLE, req.Size(), func(ctx context.Context, params types.TaskParamsV1, height uint64) (types.DispatchChallengeState, shared.MutationStatusV1, error) {
		record, _, row, err := m.k.recordedDispatchChallenge(ctx, req.TaskId, req.ReporterAddress, shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_ROSTER_UNAVAILABLE, params, height)
		if err != nil {
			return row, 0, err
		}
		if _, err := dispatchRound(record, req.DispatchRound); err != nil {
			return row, 0, invalidDispatchChallengeProof(err.Error())
		}
		row.XDispatchRound = &types.DispatchChallengeState_DispatchRound{DispatchRound: req.DispatchRound}
		if err := m.k.identifyDispatchChallenge(ctx, &row); err != nil {
			return row, 0, err
		}
		return m.k.openDispatchChallenge(ctx, row, params, height)
	})
	if err != nil {
		return nil, err
	}
	return &types.MsgReportDispatchRosterUnavailableResponse{Challenge: &row, Status: mutation}, nil
}

func dispatchAttemptByRank(record types.DispatchRecordState, rank uint32) (*types.DispatchAttemptV1, error) {
	for _, round := range record.Rounds {
		for _, attempt := range round.Attempts {
			if attempt.Rank == rank {
				return attempt, nil
			}
		}
	}
	return nil, fmt.Errorf("attempt rank is absent")
}

func invalidDispatchChallengeProof(reason string) error {
	return rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_CHALLENGE_PROOF_INVALID, fmt.Errorf("%s", reason))
}

func (m msgServer) ChallengeDispatchDraw(ctx context.Context, req *types.MsgChallengeDispatchDraw) (*types.MsgChallengeDispatchDrawResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("dispatch draw challenge is required")
	}
	row, mutation, err := m.k.runDispatchChallenge(ctx, req.TaskId, shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_DRAW, req.Size(), func(ctx context.Context, params types.TaskParamsV1, height uint64) (types.DispatchChallengeState, shared.MutationStatusV1, error) {
		record, core, row, err := m.k.recordedDispatchChallenge(ctx, req.TaskId, req.ChallengerAddress, shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_DRAW, params, height)
		if err != nil {
			return row, 0, err
		}
		row.XDispatchRound = &types.DispatchChallengeState_DispatchRound{DispatchRound: req.DispatchRound}
		if err := m.k.identifyDispatchChallenge(ctx, &row); err != nil {
			return row, 0, err
		}
		roster, first, pool, frozen, err := m.k.dispatchChallengeDrawInputs(ctx, record, core, req.DispatchRound, req.Roster, req.GetFirstRoundRoster(), params)
		if err != nil {
			return row, 0, err
		}
		order, sticky, err := dispatchExpectedOrder(sdkContextFrom(ctx).ChainID(), record, first, pool, frozen, core.OrderSequence)
		if err != nil {
			return row, 0, err
		}
		wrong := dispatchDrawIsWrong(record, core.OrderSequence, req.DispatchRound, roster, pool, order, sticky)
		if !wrong {
			return row, 0, invalidDispatchChallengeProof("committed draw follows the frozen pool order")
		}
		return m.k.upholdDispatchChallenge(ctx, row, core.SessionId, 0, params, height)
	})
	if err != nil {
		return nil, err
	}
	return &types.MsgChallengeDispatchDrawResponse{Challenge: &row, Status: mutation}, nil
}

func (k Keeper) dispatchChallengeDrawInputs(ctx context.Context, record types.DispatchRecordState, core types.TaskCoreState, number uint32, roster, first *types.DispatchRosterV1, params types.TaskParamsV1) ([]dispatch.RosterMember, []dispatch.RosterMember, []dispatch.RosterMember, types.TaskParamsV1, error) {
	var frozen types.TaskParamsV1
	members, err := k.committedDispatchRoster(ctx, record, number, roster, params.Dispatch)
	if err != nil {
		return nil, nil, nil, frozen, invalidDispatchChallengeProof(err.Error())
	}
	firstMembers := members
	if number != 0 {
		firstMembers, err = k.committedDispatchRoster(ctx, record, 0, first, params.Dispatch)
		if err != nil {
			return nil, nil, nil, frozen, invalidDispatchChallengeProof(err.Error())
		}
	} else if first != nil {
		if _, err := k.committedDispatchRoster(ctx, record, 0, first, params.Dispatch); err != nil {
			return nil, nil, nil, frozen, invalidDispatchChallengeProof(err.Error())
		}
	}
	pool, err := k.frozenDispatchPool(ctx, record, core)
	if err != nil {
		return nil, nil, nil, frozen, err
	}
	frozen, err = k.ParamsForTask(ctx, core.TaskId)
	if err != nil {
		return nil, nil, nil, frozen, err
	}
	return members, firstMembers, pool, frozen, nil
}

func dispatchDrawIsWrong(record types.DispatchRecordState, sequence uint64, number uint32, roster, pool []dispatch.RosterMember, order []uint32, sticky bool) bool {
	if record.StickyApplied != sticky {
		return true
	}
	if sticky {
		first := record.Rounds[0].Attempts[0]
		if first.Rank != 0 || first.Member.OperatorAddress != record.StickySnapshot.GetLastRecordWinner() {
			return true
		}
	}
	continuation := sticky && record.Winner.OperatorAddress == record.StickySnapshot.GetLastRecordWinner()
	if continuation {
		if record.StickyAnchorSequence != record.StickySnapshot.AnchorSequence || record.StickyRunCount != record.StickySnapshot.RunCount+1 {
			return true
		}
	} else if record.StickyAnchorSequence != sequence || record.StickyRunCount != 1 {
		return true
	}
	rosterSlots := make(map[uint32]bool, len(roster))
	for _, member := range roster {
		expected, found := dispatchPoolMember(pool, member.Slot)
		if !found || !dispatchRosterMemberEqual(member, expected) {
			return true
		}
		rosterSlots[member.Slot] = true
	}
	round, err := dispatchRound(record, number)
	if err != nil || len(round.Attempts) == 0 {
		return true
	}
	seen := make(map[uint32]bool, len(round.Attempts))
	for _, attempt := range round.Attempts {
		if uint64(attempt.Rank) >= uint64(len(order)) || order[attempt.Rank] != attempt.Member.Slot || !rosterSlots[attempt.Member.Slot] {
			return true
		}
		expected, found := dispatchPoolMember(pool, attempt.Member.Slot)
		if !found || attempt.Member.SlotVersion != expected.SlotVersion || attempt.Member.OperatorAddress != expected.OperatorAddress {
			return true
		}
		seen[attempt.Rank] = true
	}
	lower := uint64(0)
	if number != 0 {
		previous, err := dispatchRound(record, number-1)
		if err != nil || len(previous.Attempts) == 0 {
			return true
		}
		lower = uint64(previous.Attempts[len(previous.Attempts)-1].Rank) + 1
	}
	upper := uint64(round.Attempts[len(round.Attempts)-1].Rank)
	for rank := lower; rank <= upper; rank++ {
		if rosterSlots[order[rank]] && !seen[uint32(rank)] {
			return true
		}
	}
	return false
}

func (m msgServer) ChallengeDispatchOmission(ctx context.Context, req *types.MsgChallengeDispatchOmission) (*types.MsgChallengeDispatchOmissionResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("dispatch omission challenge is required")
	}
	row, mutation, err := m.k.runDispatchChallenge(ctx, req.TaskId, shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_OMISSION, req.Size(), func(ctx context.Context, params types.TaskParamsV1, height uint64) (types.DispatchChallengeState, shared.MutationStatusV1, error) {
		record, core, row, err := m.k.recordedDispatchChallenge(ctx, req.TaskId, req.ChallengerAddress, shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_OMISSION, params, height)
		if err != nil {
			return row, 0, err
		}
		roster, first, pool, frozen, err := m.k.dispatchChallengeDrawInputs(ctx, record, core, req.DispatchRound, req.Roster, req.GetFirstRoundRoster(), params)
		if err != nil {
			return row, 0, err
		}
		round, err := dispatchRound(record, req.DispatchRound)
		if err != nil {
			return row, 0, err
		}
		if err := m.k.verifyOmissionRoot(ctx, record, round, req.HandraiseRoot, req.GetBoundHandraiseRoot()); err != nil {
			return row, 0, invalidDispatchChallengeProof(err.Error())
		}
		handraise, err := m.k.omissionVictim(ctx, record, req.HandraiseRoot, req.InclusionProof, core)
		if err != nil {
			return row, 0, invalidDispatchChallengeProof(err.Error())
		}
		victim, found := dispatchPoolMember(pool, handraise.Member.Slot)
		if !found || victim.SlotVersion != handraise.Member.SlotVersion || victim.OperatorAddress != handraise.Member.OperatorAddress {
			return row, 0, invalidDispatchChallengeProof("omitted handraise is outside the frozen profile pool")
		}
		if err := verifyRosterAbsence(roster, victim, req.NonInclusionProof); err != nil {
			return row, 0, invalidDispatchChallengeProof(err.Error())
		}
		row.XDispatchRound = &types.DispatchChallengeState_DispatchRound{DispatchRound: req.DispatchRound}
		row.XVictimOperatorAddress = &types.DispatchChallengeState_VictimOperatorAddress{VictimOperatorAddress: victim.OperatorAddress}
		if err := m.k.identifyDispatchChallenge(ctx, &row); err != nil {
			return row, 0, err
		}
		order, _, err := dispatchExpectedOrder(sdkContextFrom(ctx).ChainID(), record, first, pool, frozen, core.OrderSequence)
		if err != nil {
			return row, 0, err
		}
		var compensation uint64
		lastRank := round.Attempts[len(round.Attempts)-1].Rank
		for rank, slot := range order {
			if slot == victim.Slot && uint64(rank) < uint64(lastRank) {
				budget, err := m.k.TaskBudget.Get(ctx, core.TaskId)
				if err != nil {
					return row, 0, err
				}
				compensation, err = shared.ParseAmount(budget.WorkerMax)
				if err != nil {
					return row, 0, err
				}
				break
			}
		}
		return m.k.upholdDispatchChallenge(ctx, row, core.SessionId, compensation, params, height)
	})
	if err != nil {
		return nil, err
	}
	return &types.MsgChallengeDispatchOmissionResponse{Challenge: &row, Status: mutation}, nil
}

func (m msgServer) ChallengeDispatchDoubleDispatch(ctx context.Context, req *types.MsgChallengeDispatchDoubleDispatch) (*types.MsgChallengeDispatchDoubleDispatchResponse, error) {
	if req == nil || req.Record == nil || req.Order == nil {
		return nil, fmt.Errorf("signed record and order are required")
	}
	var paramsTask []byte
	recorded, err := m.k.DispatchRecord.Has(ctx, req.Record.TaskId)
	if err != nil {
		return nil, err
	}
	if recorded {
		paramsTask = req.Record.TaskId
	}
	row, mutation, err := m.k.runDispatchChallenge(ctx, paramsTask, shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_DOUBLE_DISPATCH, req.Size(), func(ctx context.Context, params types.TaskParamsV1, height uint64) (types.DispatchChallengeState, shared.MutationStatusV1, error) {
		var row types.DispatchChallengeState
		if _, _, err := m.k.canonicalAddress("challenger_address", req.ChallengerAddress); err != nil {
			return row, 0, err
		}
		id, hash, err := dispatchOrderIdentity(req.Order.Order)
		if err != nil {
			return row, 0, err
		}
		record := req.Record
		if !bytes.Equal(record.TaskId, id[:]) || !bytes.Equal(record.TaskHash, hash[:]) || !bytes.Equal(record.SessionId, req.Order.Order.SessionId) || record.OrderSequence != req.Order.Order.OrderSequence {
			return row, 0, invalidDispatchChallengeProof("double dispatch order/record scope mismatch")
		}
		digest, err := m.k.verifyDispatchMaterial(ctx, "task.v1.DispatchRecordV1", record, &req.Order.Order.EarliestSubmitHeight)
		if err != nil {
			return row, 0, err
		}
		deadline, overflow := checkedHeightAdd(max(req.Order.Order.EarliestSubmitHeight, record.BoundChangeHeight), params.Dispatch.RecordDeadlineBlocks)
		if overflow {
			return row, 0, fmt.Errorf("double dispatch deadline overflows")
		}
		closeHeight, overflow := checkedHeightAdd(deadline, params.Dispatch.DispatchChallengeWindowBlocks)
		if overflow || height > closeHeight {
			return row, 0, rejectDispatch(types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_CHALLENGE_WINDOW_CLOSED, fmt.Errorf("double dispatch window is closed"))
		}
		attempts, err := signedDispatchAttempts(record)
		if err != nil {
			return row, 0, invalidDispatchChallengeProof(err.Error())
		}
		accepted := 0
		for _, attempt := range attempts {
			if attempt.Outcome == types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_ACCEPTED {
				accepted++
			}
		}
		if accepted < 2 {
			return row, 0, invalidDispatchChallengeProof("signed record does not contain two acceptances")
		}
		row = types.DispatchChallengeState{TaskId: id[:], Kind: shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_DOUBLE_DISPATCH, ChallengerAddress: req.ChallengerAddress, BuilderAddress: record.BuilderAddress, XDispatchRecordHash: &types.DispatchChallengeState_DispatchRecordHash{DispatchRecordHash: digest[:]}, OpenedHeight: height}
		if err := m.k.identifyDispatchChallenge(ctx, &row); err != nil {
			return row, 0, err
		}
		return m.k.upholdDispatchChallenge(ctx, row, record.SessionId, 0, params, height)
	})
	if err != nil {
		return nil, err
	}
	return &types.MsgChallengeDispatchDoubleDispatchResponse{Challenge: &row, Status: mutation}, nil
}
