package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/collections"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) initParamsVersionGenesis(ctx context.Context, genesis types.GenesisState, stateHeight uint64) error {
	if err := types.ValidateParamsVersionGenesis(genesis); err != nil {
		return err
	}
	if genesis.ParamsMeta.UpdatedHeight > stateHeight {
		return fmt.Errorf("parameter metadata is in the future")
	}
	for _, row := range genesis.ParamsHistory {
		if row.SupersededHeight > stateHeight {
			return fmt.Errorf("parameter history is in the future")
		}
		if err := k.ParamsHistory.Set(ctx, row.ParamsVersion, row); err != nil {
			return err
		}
		if err := k.ParamsHistoryByHeightIndex.Set(ctx, collections.Join(row.UpdatedHeight, row.ParamsVersion)); err != nil {
			return err
		}
	}
	bind := func(table collections.Map[types.Hash32Key, uint64], objects [][]byte, overrides []types.ParamsVersionBindingState) error {
		versions := make(map[string]uint64, len(overrides))
		for _, row := range overrides {
			versions[string(row.ObjectId)] = row.ParamsVersion
		}
		for _, object := range objects {
			version, exists := versions[string(object)]
			if !exists {
				version = genesis.ParamsMeta.ParamsVersion
			}
			if err := k.bindParamsVersion(ctx, table, object, version); err != nil {
				return err
			}
		}
		return nil
	}
	tasks, sessions := make([][]byte, len(genesis.TaskCores)), make([][]byte, len(genesis.Streams))
	for i, row := range genesis.TaskCores {
		tasks[i] = row.TaskId
	}
	for i, row := range genesis.Streams {
		sessions[i] = row.SessionId
	}
	if err := bind(k.TaskParamsVersion, tasks, genesis.TaskParamsVersionBindings); err != nil {
		return err
	}
	if err := bind(k.SessionParamsVersion, sessions, genesis.SessionParamsVersionBindings); err != nil {
		return err
	}
	for _, row := range genesis.ParamsHistory {
		if err := k.scheduleParamsHistoryPrune(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) exportParamsVersionGenesis(ctx context.Context, genesis *types.GenesisState) error {
	var err error
	genesis.ParamsHistory, err = collectMapValues(ctx, k.ParamsHistory)
	if err != nil {
		return err
	}
	export := func(table collections.Map[types.Hash32Key, uint64]) ([]types.ParamsVersionBindingState, error) {
		iter, err := table.Iterate(ctx, nil)
		if err != nil {
			return nil, err
		}
		defer iter.Close()
		var rows []types.ParamsVersionBindingState
		for ; iter.Valid(); iter.Next() {
			entry, err := iter.KeyValue()
			if err != nil {
				return nil, err
			}
			if entry.Value == genesis.ParamsMeta.ParamsVersion {
				continue
			}
			rows = append(rows, types.ParamsVersionBindingState{ObjectId: append([]byte(nil), entry.Key...), ParamsVersion: entry.Value})
		}
		return rows, nil
	}
	genesis.TaskParamsVersionBindings, err = export(k.TaskParamsVersion)
	if err != nil {
		return err
	}
	genesis.SessionParamsVersionBindings, err = export(k.SessionParamsVersion)
	return err
}
