package keeper

import (
	"bytes"
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) verifierAssignmentToStore(state types.VerifierAssignmentState) (internaltypes.VerifierAssignmentStoreState, error) {
	var selected []*internaltypes.SelectedVerifierStoreV1
	if state.SelectedVerifiers != nil {
		selected = make([]*internaltypes.SelectedVerifierStoreV1, len(state.SelectedVerifiers))
		for i, verifier := range state.SelectedVerifiers {
			address, _, err := k.canonicalAddress("selected verifier operator", verifier.OperatorAddress)
			if err != nil {
				return internaltypes.VerifierAssignmentStoreState{}, err
			}
			selected[i] = &internaltypes.SelectedVerifierStoreV1{
				OperatorAddress: append([]byte(nil), address...),
				Slot:            verifier.Slot,
				SlotVersion:     verifier.SlotVersion,
			}
		}
	}
	return internaltypes.VerifierAssignmentStoreState{
		TaskId:                      append([]byte(nil), state.TaskId...),
		VerifyRound:                 state.VerifyRound,
		OpenVerifyHeight:            state.OpenVerifyHeight,
		VerifierCandidateWindowHash: append([]byte(nil), state.VerifierCandidateWindowHash...),
		VerifierLegalSetHash:        append([]byte(nil), state.VerifierLegalSetHash...),
		SelectedVerifiersHash:       append([]byte(nil), state.SelectedVerifiersHash...),
		SelectionRandomnessHeight:   state.SelectionRandomnessHeight,
		SelectionRandomnessBeacon:   append([]byte(nil), state.SelectionRandomnessBeacon...),
		SelectedVerifiers:           selected,
		SelectedVerifierCount:       state.SelectedVerifierCount,
		VerifierHandraiseCount:      state.VerifierHandraiseCount,
		CommitDeadlineHeight:        state.CommitDeadlineHeight,
		RevealDeadlineHeight:        state.RevealDeadlineHeight,
		VerifyDeadlineHeight:        state.VerifyDeadlineHeight,
	}, nil
}

func (k Keeper) ProjectVerifierAssignmentStore(stored internaltypes.VerifierAssignmentStoreState) (types.VerifierAssignmentState, error) {
	var selected []types.SelectedVerifierV1
	if stored.SelectedVerifiers != nil {
		selected = make([]types.SelectedVerifierV1, len(stored.SelectedVerifiers))
		for i, verifier := range stored.SelectedVerifiers {
			if verifier == nil {
				return types.VerifierAssignmentState{}, fmt.Errorf("stored selected verifier %d is nil", i)
			}
			address, err := k.sessionAddressFromStore("selected verifier operator", verifier.OperatorAddress)
			if err != nil {
				return types.VerifierAssignmentState{}, err
			}
			selected[i] = types.SelectedVerifierV1{
				OperatorAddress: address,
				Slot:            verifier.Slot,
				SlotVersion:     verifier.SlotVersion,
			}
		}
	}
	return types.VerifierAssignmentState{
		TaskId:                      append([]byte(nil), stored.TaskId...),
		VerifyRound:                 stored.VerifyRound,
		OpenVerifyHeight:            stored.OpenVerifyHeight,
		VerifierCandidateWindowHash: append([]byte(nil), stored.VerifierCandidateWindowHash...),
		VerifierLegalSetHash:        append([]byte(nil), stored.VerifierLegalSetHash...),
		SelectedVerifiersHash:       append([]byte(nil), stored.SelectedVerifiersHash...),
		SelectionRandomnessHeight:   stored.SelectionRandomnessHeight,
		SelectionRandomnessBeacon:   append([]byte(nil), stored.SelectionRandomnessBeacon...),
		SelectedVerifiers:           selected,
		SelectedVerifierCount:       stored.SelectedVerifierCount,
		VerifierHandraiseCount:      stored.VerifierHandraiseCount,
		CommitDeadlineHeight:        stored.CommitDeadlineHeight,
		RevealDeadlineHeight:        stored.RevealDeadlineHeight,
		VerifyDeadlineHeight:        stored.VerifyDeadlineHeight,
	}, nil
}

func verifierAssignmentKeyMatches(key types.VerifyRoundKey, taskID []byte, round uint32) bool {
	return bytes.Equal(key.K1(), taskID) && key.K2() == round
}

func (k Keeper) ReadVerifierAssignment(ctx context.Context, key types.VerifyRoundKey) (types.VerifierAssignmentState, error) {
	stored, err := k.VerifierAssignment.Get(ctx, key)
	if err != nil {
		return types.VerifierAssignmentState{}, err
	}
	if !verifierAssignmentKeyMatches(key, stored.TaskId, stored.VerifyRound) {
		return types.VerifierAssignmentState{}, fmt.Errorf("verifier assignment key/value mismatch")
	}
	return k.ProjectVerifierAssignmentStore(stored)
}

func (k Keeper) WriteVerifierAssignment(ctx context.Context, key types.VerifyRoundKey, state types.VerifierAssignmentState) error {
	if !verifierAssignmentKeyMatches(key, state.TaskId, state.VerifyRound) {
		return fmt.Errorf("verifier assignment key/value mismatch")
	}
	stored, err := k.verifierAssignmentToStore(state)
	if err != nil {
		return err
	}
	return k.VerifierAssignment.Set(ctx, key, stored)
}

func (k Keeper) exportVerifierAssignments(ctx context.Context) ([]types.VerifierAssignmentState, error) {
	iter, err := k.VerifierAssignment.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.VerifierAssignmentState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		if !verifierAssignmentKeyMatches(entry.Key, entry.Value.TaskId, entry.Value.VerifyRound) {
			return nil, fmt.Errorf("verifier assignment key/value mismatch")
		}
		state, err := k.ProjectVerifierAssignmentStore(entry.Value)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}
