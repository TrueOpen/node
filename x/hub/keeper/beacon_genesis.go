package keeper

import (
	"context"
	"sort"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
)

// initBeaconGenesis restores immutable checkpoints and derives all mutable
// lifecycle state from the retained beacon rows.
func (k Keeper) initBeaconGenesis(ctx context.Context, genState *types.GenesisState) error {
	closed := make(map[uint64]struct{}, len(genState.BeaconCheckpoints))
	for _, checkpoint := range genState.BeaconCheckpoints {
		if err := k.BeaconCheckpoint.Set(ctx, checkpoint.CheckpointIndex, checkpoint); err != nil {
			return err
		}
		closed[checkpoint.CheckpointIndex] = struct{}{}
	}
	rows := append([]types.BeaconState(nil), genState.Beacons...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Height < rows[j].Height })
	for _, state := range rows {
		stored, err := beaconStateToStore(state)
		if err != nil {
			return err
		}
		if err := k.Beacon.Set(ctx, stored.Height, stored); err != nil {
			return err
		}
		if err := k.BeaconPruneIndex.Set(ctx, types.NewBeaconPruneKey(
			state.Height+genState.Params.Beacon.BeaconRetentionBlocks, state.Height,
		)); err != nil {
			return err
		}
		index := (state.Height - 1) / genState.Params.Beacon.BeaconCheckpointIntervalBlocks
		if _, isClosed := closed[index]; !isClosed {
			if err := k.advanceBeaconCheckpoint(ctx, stored, genState.Params.Beacon.BeaconCheckpointIntervalBlocks); err != nil {
				return err
			}
		}
	}
	return nil
}

func (k Keeper) exportBeaconGenesis(ctx context.Context, genesis *types.GenesisState) error {
	stored, err := collectMapValues[uint64, internaltypes.BeaconStoreState](ctx, k.Beacon)
	if err != nil {
		return err
	}
	genesis.Beacons = make([]types.BeaconState, len(stored))
	for index := range stored {
		genesis.Beacons[index], err = beaconStoreToState(stored[index])
		if err != nil {
			return err
		}
	}
	genesis.BeaconCheckpoints, err = collectMapValues[uint64, types.BeaconCheckpointState](ctx, k.BeaconCheckpoint)
	return err
}
