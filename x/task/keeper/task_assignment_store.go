package keeper

import (
	"bytes"
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

// The private message mirrors public field numbers and wire types except
// winner_worker, which changes from string to bytes and may be empty.
func (k Keeper) taskAssignmentToStore(state types.TaskAssignmentState) (internaltypes.TaskAssignmentStoreState, error) {
	encoded, err := state.Marshal()
	if err != nil {
		return internaltypes.TaskAssignmentStoreState{}, err
	}
	var stored internaltypes.TaskAssignmentStoreState
	if err := stored.Unmarshal(encoded); err != nil {
		return internaltypes.TaskAssignmentStoreState{}, err
	}
	if state.WinnerWorker != "" {
		address, _, err := k.canonicalAddress("assignment winner Worker", state.WinnerWorker)
		if err != nil {
			return internaltypes.TaskAssignmentStoreState{}, err
		}
		stored.WinnerWorker = append([]byte(nil), address...)
	}
	return stored, nil
}

func (k Keeper) ProjectTaskAssignmentStore(stored internaltypes.TaskAssignmentStoreState) (types.TaskAssignmentState, error) {
	var address string
	if len(stored.WinnerWorker) != 0 {
		var err error
		address, err = k.sessionAddressFromStore("assignment winner Worker", stored.WinnerWorker)
		if err != nil {
			return types.TaskAssignmentState{}, err
		}
		stored.WinnerWorker = []byte("placeholder")
	}
	encoded, err := stored.Marshal()
	if err != nil {
		return types.TaskAssignmentState{}, err
	}
	var state types.TaskAssignmentState
	if err := state.Unmarshal(encoded); err != nil {
		return types.TaskAssignmentState{}, err
	}
	state.WinnerWorker = address
	return state, nil
}

func (k Keeper) ReadTaskAssignment(ctx context.Context, key types.TaskKey) (types.TaskAssignmentState, error) {
	stored, err := k.TaskAssignment.Get(ctx, key)
	if err != nil {
		return types.TaskAssignmentState{}, err
	}
	if !bytes.Equal(key, stored.TaskId) {
		return types.TaskAssignmentState{}, fmt.Errorf("task assignment key/value mismatch")
	}
	return k.ProjectTaskAssignmentStore(stored)
}

func (k Keeper) WriteTaskAssignment(ctx context.Context, key types.TaskKey, state types.TaskAssignmentState) error {
	if !bytes.Equal(key, state.TaskId) {
		return fmt.Errorf("task assignment key/value mismatch")
	}
	stored, err := k.taskAssignmentToStore(state)
	if err != nil {
		return err
	}
	return k.TaskAssignment.Set(ctx, key, stored)
}

func (k Keeper) exportTaskAssignments(ctx context.Context) ([]types.TaskAssignmentState, error) {
	iter, err := k.TaskAssignment.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.TaskAssignmentState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(entry.Key, entry.Value.TaskId) {
			return nil, fmt.Errorf("task assignment key/value mismatch")
		}
		state, err := k.ProjectTaskAssignmentStore(entry.Value)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}
