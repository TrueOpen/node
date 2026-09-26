package keeper

import (
	"bytes"
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

// The private message mirrors every public field number and wire type except
// winner_worker, which changes from optional string to optional bytes.
func (k Keeper) taskTerminalSummaryToStore(state types.TaskTerminalSummaryState) (internaltypes.TaskTerminalSummaryStoreState, error) {
	encoded, err := state.Marshal()
	if err != nil {
		return internaltypes.TaskTerminalSummaryStoreState{}, err
	}
	var stored internaltypes.TaskTerminalSummaryStoreState
	if err := stored.Unmarshal(encoded); err != nil {
		return internaltypes.TaskTerminalSummaryStoreState{}, err
	}
	if state.XWinnerWorker != nil {
		address, _, err := k.canonicalAddress("terminal summary winner Worker", state.GetWinnerWorker())
		if err != nil {
			return internaltypes.TaskTerminalSummaryStoreState{}, err
		}
		stored.XWinnerWorker = &internaltypes.TaskTerminalSummaryStoreState_WinnerWorker{WinnerWorker: append([]byte(nil), address...)}
	}
	return stored, nil
}

func (k Keeper) ProjectTaskTerminalSummaryStore(stored internaltypes.TaskTerminalSummaryStoreState) (types.TaskTerminalSummaryState, error) {
	var address string
	if stored.XWinnerWorker != nil {
		var err error
		address, err = k.sessionAddressFromStore("terminal summary winner Worker", stored.GetWinnerWorker())
		if err != nil {
			return types.TaskTerminalSummaryState{}, err
		}
		stored.XWinnerWorker = &internaltypes.TaskTerminalSummaryStoreState_WinnerWorker{WinnerWorker: []byte("placeholder")}
	}
	encoded, err := stored.Marshal()
	if err != nil {
		return types.TaskTerminalSummaryState{}, err
	}
	var state types.TaskTerminalSummaryState
	if err := state.Unmarshal(encoded); err != nil {
		return types.TaskTerminalSummaryState{}, err
	}
	if stored.XWinnerWorker != nil {
		state.XWinnerWorker = &types.TaskTerminalSummaryState_WinnerWorker{WinnerWorker: address}
	}
	return state, nil
}

func (k Keeper) ReadTaskTerminalSummary(ctx context.Context, key types.TaskKey) (types.TaskTerminalSummaryState, error) {
	stored, err := k.TaskTerminalSummary.Get(ctx, key)
	if err != nil {
		return types.TaskTerminalSummaryState{}, err
	}
	if !bytes.Equal(key, stored.TaskId) {
		return types.TaskTerminalSummaryState{}, fmt.Errorf("%w: task terminal summary key/value mismatch", types.ErrInvariantBroken)
	}
	return k.ProjectTaskTerminalSummaryStore(stored)
}

func (k Keeper) WriteTaskTerminalSummary(ctx context.Context, key types.TaskKey, state types.TaskTerminalSummaryState) error {
	if !bytes.Equal(key, state.TaskId) {
		return fmt.Errorf("task terminal summary key/value mismatch")
	}
	stored, err := k.taskTerminalSummaryToStore(state)
	if err != nil {
		return err
	}
	return k.TaskTerminalSummary.Set(ctx, key, stored)
}

func (k Keeper) exportTaskTerminalSummaries(ctx context.Context) ([]types.TaskTerminalSummaryState, error) {
	iter, err := k.TaskTerminalSummary.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.TaskTerminalSummaryState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(entry.Key, entry.Value.TaskId) {
			return nil, fmt.Errorf("task terminal summary key/value mismatch")
		}
		state, err := k.ProjectTaskTerminalSummaryStore(entry.Value)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}
