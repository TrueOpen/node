package keeper

import (
	"bytes"
	"context"
	"fmt"

	"cosmossdk.io/collections"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// EnsureParamsVersionInvariant checks object bindings, historical bodies and
// all derived counts/time indexes without accepting mirrored Genesis counters.
func (k Keeper) EnsureParamsVersionInvariant(ctx context.Context) error {
	var genesis types.GenesisState
	var err error
	genesis.Params, err = k.Params.Get(ctx)
	if err != nil {
		return err
	}
	genesis.ParamsMeta, err = k.GetTaskParamsMeta(ctx)
	if err != nil {
		return err
	}
	if genesis.ParamsMeta.ParamsVersion != 0 {
		hash, err := types.TaskParamsHashV1(sdkChainID(ctx), genesis.ParamsMeta.ParamsVersion, genesis.Params)
		if err != nil {
			return err
		}
		if !bytes.Equal(hash, genesis.ParamsMeta.ParamsHash) {
			return fmt.Errorf("current parameter metadata hash disagrees with its body")
		}
	}
	tasks, sessions := map[string]bool{}, map[string]bool{}
	if err = k.TaskCore.Walk(ctx, nil, func(id types.TaskKey, row types.TaskCoreState) (bool, error) {
		tasks[string(id)] = true
		genesis.TaskCores = append(genesis.TaskCores, types.TaskCoreState{TaskId: id})
		return false, nil
	}); err != nil {
		return err
	}
	iter, err := k.Stream.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; iter.Valid(); iter.Next() {
		id, err := iter.Key()
		if err != nil {
			iter.Close()
			return err
		}
		sessions[string(id)] = true
		genesis.Streams = append(genesis.Streams, types.StreamState{SessionId: id})
	}
	if err = iter.Close(); err != nil {
		return err
	}
	if err = k.exportParamsVersionGenesis(ctx, &genesis); err != nil {
		return err
	}
	if err = types.ValidateParamsVersionGenesis(genesis); err != nil {
		return err
	}
	history := map[uint64]types.TaskParamsHistoryState{}
	if err = k.ParamsHistory.Walk(ctx, nil, func(version uint64, row types.TaskParamsHistoryState) (bool, error) {
		if version != row.ParamsVersion {
			return false, fmt.Errorf("parameter history key/version mismatch")
		}
		history[version] = row
		return false, nil
	}); err != nil {
		return err
	}
	counts := map[uint64]uint64{}
	check := func(table collections.Map[types.Hash32Key, uint64], objects map[string]bool) error {
		returnErr := table.Walk(ctx, nil, func(id types.Hash32Key, version uint64) (bool, error) {
			if !objects[string(id)] {
				return false, fmt.Errorf("parameter binding has no live object")
			}
			delete(objects, string(id))
			if version != genesis.ParamsMeta.ParamsVersion {
				if _, exists := history[version]; !exists {
					return false, fmt.Errorf("parameter binding has no history body")
				}
			}
			next, overflow := shared.CheckedAddUint64(counts[version], 1)
			if overflow {
				return false, fmt.Errorf("parameter invariant reference count overflows")
			}
			counts[version] = next
			return false, nil
		})
		if returnErr != nil {
			return returnErr
		}
		if len(objects) != 0 {
			return fmt.Errorf("live object has no private parameter version binding")
		}
		return nil
	}
	if err = check(k.TaskParamsVersion, tasks); err != nil {
		return err
	}
	if err = check(k.SessionParamsVersion, sessions); err != nil {
		return err
	}
	if err = k.ParamsVersionRefCount.Walk(ctx, nil, func(version, count uint64) (bool, error) {
		if count == 0 || counts[version] != count {
			return false, fmt.Errorf("parameter reference count differs from bindings")
		}
		delete(counts, version)
		return false, nil
	}); err != nil {
		return err
	}
	if len(counts) != 0 {
		return fmt.Errorf("parameter bindings have no derived reference count")
	}
	byHeight, prune := map[string]bool{}, map[string]bool{}
	for version, row := range history {
		key, err := indexKeyBytes(k.ParamsHistoryByHeightIndex.KeyCodec(), collections.Join(row.UpdatedHeight, version))
		if err != nil {
			return err
		}
		byHeight[key] = true
		count, err := k.paramsVersionRefCount(ctx, version)
		if err != nil {
			return err
		}
		if count == 0 {
			floor, err := k.paramsHistoryPruneFloor(ctx, row.SupersededHeight)
			if err != nil {
				return err
			}
			key, err := indexKeyBytes(k.ParamsHistoryPruneIndex.KeyCodec(), collections.Join(floor, version))
			if err != nil {
				return err
			}
			prune[key] = true
		}
	}
	if err = checkDerivedKeySet(ctx, k.ParamsHistoryByHeightIndex, byHeight); err != nil {
		return err
	}
	return checkDerivedKeySet(ctx, k.ParamsHistoryPruneIndex, prune)
}
