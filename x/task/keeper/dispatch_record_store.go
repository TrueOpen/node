package keeper

import (
	"bytes"
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) dispatchMemberToStore(member *types.CandidateMemberRefV1) (*internaltypes.CandidateMemberStoreV1, error) {
	if member == nil || len(member.CandidatePoolSnapshotId) != types.Hash32Len || member.SlotVersion == 0 {
		return nil, fmt.Errorf("dispatch member identity is invalid")
	}
	operator, err := k.sessionAddressToStore("dispatch member", member.OperatorAddress)
	if err != nil {
		return nil, err
	}
	return &internaltypes.CandidateMemberStoreV1{CandidatePoolSnapshotId: bytes.Clone(member.CandidatePoolSnapshotId), Slot: member.Slot, SlotVersion: member.SlotVersion, OperatorAddress: operator}, nil
}

func (k Keeper) dispatchMemberFromStore(member *internaltypes.CandidateMemberStoreV1) (*types.CandidateMemberRefV1, error) {
	if member == nil || len(member.CandidatePoolSnapshotId) != types.Hash32Len || member.SlotVersion == 0 {
		return nil, fmt.Errorf("stored dispatch member identity is invalid")
	}
	operator, err := k.sessionAddressFromStore("dispatch member", member.OperatorAddress)
	if err != nil {
		return nil, err
	}
	return &types.CandidateMemberRefV1{CandidatePoolSnapshotId: bytes.Clone(member.CandidatePoolSnapshotId), Slot: member.Slot, SlotVersion: member.SlotVersion, OperatorAddress: operator}, nil
}

func (k Keeper) dispatchRecordToStore(state types.DispatchRecordState) (internaltypes.DispatchRecordStoreState, error) {
	var empty internaltypes.DispatchRecordStoreState
	if err := types.ValidateDispatchRecord(state); err != nil {
		return empty, err
	}
	for _, hash := range [][]byte{state.TaskId, state.DispatchRecordHash, state.DispatchTicketHash, state.CandidatePoolSnapshotId, state.VrfOutput, state.AcceptedEntryDigest} {
		if len(hash) != types.Hash32Len {
			return empty, fmt.Errorf("dispatch record hash must be raw32")
		}
	}
	if state.RecordedHeight == 0 || state.ClaimWindowCloseHeight < state.RecordedHeight {
		return empty, fmt.Errorf("dispatch record heights are invalid")
	}
	builder, err := k.sessionAddressToStore("dispatch Builder", state.BuilderAddress)
	if err != nil {
		return empty, err
	}
	winner, err := k.dispatchMemberToStore(state.Winner)
	if err != nil {
		return empty, err
	}
	stored := internaltypes.DispatchRecordStoreState{
		TaskId: bytes.Clone(state.TaskId), DispatchRecordHash: bytes.Clone(state.DispatchRecordHash), DispatchTicketHash: bytes.Clone(state.DispatchTicketHash),
		BuilderAddress: builder, ReferenceHeight: state.ReferenceHeight, CandidatePoolSnapshotId: bytes.Clone(state.CandidatePoolSnapshotId),
		VrfEpoch: state.VrfEpoch, EarliestSubmitHeight: state.EarliestSubmitHeight, VrfOutput: bytes.Clone(state.VrfOutput), VrfProof: bytes.Clone(state.VrfProof),
		RandomnessScheme: int32(state.RandomnessScheme), WinnerRound: state.WinnerRound, StickyAnchorSequence: state.StickyAnchorSequence,
		StickyRunCount: state.StickyRunCount, StickyApplied: state.StickyApplied, Winner: winner, RecordedHeight: state.RecordedHeight,
		AcceptedEntryDigest: bytes.Clone(state.AcceptedEntryDigest), ClaimWindowCloseHeight: state.ClaimWindowCloseHeight, ServiceAuthorizationNonce: state.ServiceAuthorizationNonce, BoundChangeHeight: state.BoundChangeHeight,
	}
	if state.StickySnapshot == nil {
		return empty, fmt.Errorf("dispatch record lacks its pre-admission sticky snapshot")
	}
	sticky := state.StickySnapshot
	stored.StickySnapshot = &internaltypes.DispatchStickySnapshotStoreV1{AnchorSequence: sticky.AnchorSequence, AnchorHeight: sticky.AnchorHeight, RunCount: sticky.RunCount}
	if sticky.XLastRecordWinner != nil {
		stored.StickySnapshot.LastRecordWinner, err = k.sessionAddressToStore("sticky winner", sticky.GetLastRecordWinner())
		if err != nil {
			return empty, err
		}
	}
	for _, round := range state.Rounds {
		if round == nil {
			return empty, fmt.Errorf("dispatch round is nil")
		}
		row := &internaltypes.DispatchRoundStoreV1{DispatchRound: round.DispatchRound, HandraiseRootDigest: bytes.Clone(round.HandraiseRootDigest), RosterHash: bytes.Clone(round.RosterHash)}
		for _, attempt := range round.Attempts {
			if attempt == nil {
				return empty, fmt.Errorf("dispatch attempt is nil")
			}
			member, err := k.dispatchMemberToStore(attempt.Member)
			if err != nil {
				return empty, err
			}
			row.Attempts = append(row.Attempts, &internaltypes.DispatchAttemptStoreV1{DispatchTicketHash: bytes.Clone(attempt.DispatchTicketHash), Rank: attempt.Rank, Member: member, Outcome: int32(attempt.Outcome), RejectDigestOrZero32: bytes.Clone(attempt.RejectDigestOrZero32), TimeoutTierOrZero: attempt.TimeoutTierOrZero})
		}
		stored.Rounds = append(stored.Rounds, row)
	}
	return stored, nil
}

// ProjectDispatchRecordStore renders raw operator identities only at the public boundary.
func (k Keeper) ProjectDispatchRecordStore(stored internaltypes.DispatchRecordStoreState) (types.DispatchRecordState, error) {
	builder, err := k.sessionAddressFromStore("dispatch Builder", stored.BuilderAddress)
	if err != nil {
		return types.DispatchRecordState{}, err
	}
	winner, err := k.dispatchMemberFromStore(stored.Winner)
	if err != nil {
		return types.DispatchRecordState{}, err
	}
	state := types.DispatchRecordState{
		TaskId: bytes.Clone(stored.TaskId), DispatchRecordHash: bytes.Clone(stored.DispatchRecordHash), DispatchTicketHash: bytes.Clone(stored.DispatchTicketHash),
		BuilderAddress: builder, ReferenceHeight: stored.ReferenceHeight, CandidatePoolSnapshotId: bytes.Clone(stored.CandidatePoolSnapshotId),
		VrfEpoch: stored.VrfEpoch, EarliestSubmitHeight: stored.EarliestSubmitHeight, VrfOutput: bytes.Clone(stored.VrfOutput), VrfProof: bytes.Clone(stored.VrfProof),
		RandomnessScheme: types.DispatchRandomnessSchemeV1(stored.RandomnessScheme), WinnerRound: stored.WinnerRound, StickyAnchorSequence: stored.StickyAnchorSequence,
		StickyRunCount: stored.StickyRunCount, StickyApplied: stored.StickyApplied, Winner: winner, RecordedHeight: stored.RecordedHeight,
		AcceptedEntryDigest: bytes.Clone(stored.AcceptedEntryDigest), ClaimWindowCloseHeight: stored.ClaimWindowCloseHeight, ServiceAuthorizationNonce: stored.ServiceAuthorizationNonce, BoundChangeHeight: stored.BoundChangeHeight,
	}
	if stored.StickySnapshot == nil {
		return types.DispatchRecordState{}, fmt.Errorf("stored dispatch sticky snapshot is nil")
	}
	sticky := stored.StickySnapshot
	state.StickySnapshot = &types.DispatchStickySnapshotV1{AnchorSequence: sticky.AnchorSequence, AnchorHeight: sticky.AnchorHeight, RunCount: sticky.RunCount}
	if len(sticky.LastRecordWinner) != 0 {
		operator, err := k.sessionAddressFromStore("sticky winner", sticky.LastRecordWinner)
		if err != nil {
			return types.DispatchRecordState{}, err
		}
		state.StickySnapshot.XLastRecordWinner = &types.DispatchStickySnapshotV1_LastRecordWinner{LastRecordWinner: operator}
	}
	for _, round := range stored.Rounds {
		if round == nil {
			return types.DispatchRecordState{}, fmt.Errorf("stored dispatch round is nil")
		}
		row := &types.DispatchRoundV1{DispatchRound: round.DispatchRound, HandraiseRootDigest: bytes.Clone(round.HandraiseRootDigest), RosterHash: bytes.Clone(round.RosterHash)}
		for _, attempt := range round.Attempts {
			if attempt == nil {
				return types.DispatchRecordState{}, fmt.Errorf("stored dispatch attempt is nil")
			}
			member, err := k.dispatchMemberFromStore(attempt.Member)
			if err != nil {
				return types.DispatchRecordState{}, err
			}
			row.Attempts = append(row.Attempts, &types.DispatchAttemptV1{DispatchTicketHash: bytes.Clone(attempt.DispatchTicketHash), Rank: attempt.Rank, Member: member, Outcome: types.DispatchAttemptOutcomeV1(attempt.Outcome), RejectDigestOrZero32: bytes.Clone(attempt.RejectDigestOrZero32), TimeoutTierOrZero: attempt.TimeoutTierOrZero})
		}
		state.Rounds = append(state.Rounds, row)
	}
	if _, err := k.dispatchRecordToStore(state); err != nil {
		return types.DispatchRecordState{}, err
	}
	return state, nil
}

func (k Keeper) readDispatchRecord(ctx context.Context, taskID []byte) (types.DispatchRecordState, error) {
	key, err := taskStoreKey(taskID)
	if err != nil {
		return types.DispatchRecordState{}, err
	}
	stored, err := k.DispatchRecord.Get(ctx, key)
	if err != nil {
		return types.DispatchRecordState{}, err
	}
	if !bytes.Equal(stored.TaskId, key) {
		return types.DispatchRecordState{}, fmt.Errorf("dispatch record key/task mismatch")
	}
	return k.ProjectDispatchRecordStore(stored)
}

func (k Keeper) writeDispatchRecord(ctx context.Context, state types.DispatchRecordState) error {
	stored, err := k.dispatchRecordToStore(state)
	if err != nil {
		return err
	}
	if err := k.DispatchRecord.Set(ctx, state.TaskId, stored); err != nil {
		return err
	}
	return k.DispatchRecordByHash.Set(ctx, state.DispatchRecordHash, bytes.Clone(state.TaskId))
}
