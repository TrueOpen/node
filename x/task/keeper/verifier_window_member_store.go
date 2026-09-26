package keeper

import (
	"bytes"
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) verifierWindowMemberToStore(state types.VerifierCandidateWindowMemberState) (internaltypes.VerifierWindowMemberStoreState, error) {
	address, _, err := k.canonicalAddress("verifier window operator", state.OperatorAddress)
	if err != nil {
		return internaltypes.VerifierWindowMemberStoreState{}, err
	}
	return internaltypes.VerifierWindowMemberStoreState{
		SchemaVersion:      state.SchemaVersion,
		TaskId:             append([]byte(nil), state.TaskId...),
		VerifyRound:        state.VerifyRound,
		RankIndex:          state.RankIndex,
		Slot:               state.Slot,
		SlotVersion:        state.SlotVersion,
		OperatorAddress:    append([]byte(nil), address...),
		VerifierWindowRank: append([]byte(nil), state.VerifierWindowRank...),
	}, nil
}

func (k Keeper) ProjectVerifierWindowMemberStore(stored internaltypes.VerifierWindowMemberStoreState) (types.VerifierCandidateWindowMemberState, error) {
	address, err := k.sessionAddressFromStore("verifier window operator", stored.OperatorAddress)
	if err != nil {
		return types.VerifierCandidateWindowMemberState{}, err
	}
	return types.VerifierCandidateWindowMemberState{
		SchemaVersion:      stored.SchemaVersion,
		TaskId:             append([]byte(nil), stored.TaskId...),
		VerifyRound:        stored.VerifyRound,
		RankIndex:          stored.RankIndex,
		Slot:               stored.Slot,
		SlotVersion:        stored.SlotVersion,
		OperatorAddress:    address,
		VerifierWindowRank: append([]byte(nil), stored.VerifierWindowRank...),
	}, nil
}

func verifierWindowMemberKeyMatches(key types.VerifyRoundSegmentKey, taskID []byte, round, rank uint32) bool {
	return bytes.Equal(key.K1(), taskID) && key.K2() == round && key.K3() == rank
}

func (k Keeper) ReadVerifierWindowMember(ctx context.Context, key types.VerifyRoundSegmentKey) (types.VerifierCandidateWindowMemberState, error) {
	stored, err := k.VerifierCandidateWindowMember.Get(ctx, key)
	if err != nil {
		return types.VerifierCandidateWindowMemberState{}, err
	}
	if !verifierWindowMemberKeyMatches(key, stored.TaskId, stored.VerifyRound, stored.RankIndex) {
		return types.VerifierCandidateWindowMemberState{}, fmt.Errorf("verifier window member key/value mismatch")
	}
	return k.ProjectVerifierWindowMemberStore(stored)
}

func (k Keeper) WriteVerifierWindowMember(ctx context.Context, key types.VerifyRoundSegmentKey, state types.VerifierCandidateWindowMemberState) error {
	if !verifierWindowMemberKeyMatches(key, state.TaskId, state.VerifyRound, state.RankIndex) {
		return fmt.Errorf("verifier window member key/value mismatch")
	}
	stored, err := k.verifierWindowMemberToStore(state)
	if err != nil {
		return err
	}
	return k.VerifierCandidateWindowMember.Set(ctx, key, stored)
}

func (k Keeper) exportVerifierWindowMembers(ctx context.Context) ([]types.VerifierCandidateWindowMemberState, error) {
	iter, err := k.VerifierCandidateWindowMember.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.VerifierCandidateWindowMemberState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		if !verifierWindowMemberKeyMatches(entry.Key, entry.Value.TaskId, entry.Value.VerifyRound, entry.Value.RankIndex) {
			return nil, fmt.Errorf("verifier window member key/value mismatch")
		}
		state, err := k.ProjectVerifierWindowMemberStore(entry.Value)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}
