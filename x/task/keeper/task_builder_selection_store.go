package keeper

import (
	"bytes"
	"context"
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

const taskAccountAddressBytes = 20

func (k Keeper) taskBuilderSelectionToStore(state types.TaskBuilderSelectionState) (internaltypes.TaskBuilderSelectionStoreState, error) {
	builders := make([][]byte, len(state.SelectedTaskBuilders))
	for i, address := range state.SelectedTaskBuilders {
		raw, _, err := k.canonicalAddress("selected_task_builders", address)
		if err != nil {
			return internaltypes.TaskBuilderSelectionStoreState{}, err
		}
		builders[i] = append([]byte(nil), raw...)
	}
	return internaltypes.TaskBuilderSelectionStoreState{
		TaskId:                   append([]byte(nil), state.TaskId...),
		SessionAnchorBlockHash:   append([]byte(nil), state.SessionAnchorBlockHash...),
		BuilderSetId:             state.BuilderSetId,
		BuilderSetHash:           append([]byte(nil), state.BuilderSetHash...),
		SelectedTaskBuilders:     builders,
		SelectedTaskBuilderCount: state.SelectedTaskBuilderCount,
		SelectedTaskBuildersHash: append([]byte(nil), state.SelectedTaskBuildersHash...),
		CreatedHeight:            state.CreatedHeight, BodyStatus: int32(state.BodyStatus),
		BuilderSetRefReleased:        state.BuilderSetRefReleased,
		BuilderFaultSlashBpsSnapshot: state.BuilderFaultSlashBpsSnapshot,
	}, nil
}

// ProjectTaskBuilderSelection returns the public state without changing frozen member order.
func (k Keeper) ProjectTaskBuilderSelection(stored internaltypes.TaskBuilderSelectionStoreState) (types.TaskBuilderSelectionState, error) {
	builders := make([]string, len(stored.SelectedTaskBuilders))
	for i, raw := range stored.SelectedTaskBuilders {
		if len(raw) != taskAccountAddressBytes {
			return types.TaskBuilderSelectionState{}, fmt.Errorf("stored selected Builder address must be %d bytes", taskAccountAddressBytes)
		}
		address, err := k.addressCodec.BytesToString(raw)
		if err != nil {
			return types.TaskBuilderSelectionState{}, fmt.Errorf("encode stored selected Builder address: %w", err)
		}
		builders[i] = address
	}
	return types.TaskBuilderSelectionState{
		TaskId:                       append([]byte(nil), stored.TaskId...),
		SessionAnchorBlockHash:       append([]byte(nil), stored.SessionAnchorBlockHash...),
		BuilderSetId:                 stored.BuilderSetId,
		BuilderSetHash:               append([]byte(nil), stored.BuilderSetHash...),
		SelectedTaskBuilders:         builders,
		SelectedTaskBuilderCount:     stored.SelectedTaskBuilderCount,
		SelectedTaskBuildersHash:     append([]byte(nil), stored.SelectedTaskBuildersHash...),
		CreatedHeight:                stored.CreatedHeight,
		BodyStatus:                   shared.StoredBodyStatus(stored.BodyStatus),
		BuilderSetRefReleased:        stored.BuilderSetRefReleased,
		BuilderFaultSlashBpsSnapshot: stored.BuilderFaultSlashBpsSnapshot,
	}, nil
}

func (k Keeper) GetTaskBuilderSelection(ctx context.Context, key types.TaskKey) (types.TaskBuilderSelectionState, error) {
	stored, err := k.TaskBuilderSelection.Get(ctx, key)
	if err != nil {
		return types.TaskBuilderSelectionState{}, err
	}
	if !bytes.Equal(stored.TaskId, key) {
		return types.TaskBuilderSelectionState{}, fmt.Errorf("stored Task Builder selection key/task mismatch")
	}
	return k.ProjectTaskBuilderSelection(stored)
}

func (k Keeper) StoreTaskBuilderSelection(ctx context.Context, key types.TaskKey, state types.TaskBuilderSelectionState) error {
	if !bytes.Equal(state.TaskId, key) {
		return fmt.Errorf("Task Builder selection key/task mismatch")
	}
	stored, err := k.taskBuilderSelectionToStore(state)
	if err != nil {
		return err
	}
	return k.TaskBuilderSelection.Set(ctx, key, stored)
}

func (k Keeper) exportTaskBuilderSelections(ctx context.Context) ([]types.TaskBuilderSelectionState, error) {
	iter, err := k.TaskBuilderSelection.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.TaskBuilderSelectionState{}
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		stored, err := iter.Value()
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(stored.TaskId, key) {
			return nil, fmt.Errorf("stored Task Builder selection key/task mismatch")
		}
		state, err := k.ProjectTaskBuilderSelection(stored)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}
