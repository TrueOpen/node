package keeper

import (
	"bytes"
	"context"
	"fmt"

	"cosmossdk.io/collections"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (k Keeper) initRoleFaultGenesis(ctx context.Context, states []types.RoleFaultState, retention uint64) error {
	for _, state := range states {
		faultID := shared.Hash32Key(state.FaultId)
		if err := k.RoleFault.Set(ctx, types.NewRoleFaultKey(faultID), state); err != nil {
			return err
		}
		pruneHeight, err := checkedAdd(state.RecordedHeight, retention)
		if retention == 0 || err != nil {
			return fmt.Errorf("role fault %s prune height overflows", hexRef(faultID))
		}
		if err := k.RoleFaultPruneIndex.Set(ctx, types.NewRoleFaultPruneKey(pruneHeight, faultID)); err != nil {
			return err
		}
		// RoleFaultByTaskIndex is derived, so it is rebuilt here instead of being
		// carried on the wire: an exported index could disagree with the primaries
		// it indexes, and a disagreement in this direction would silently change a
		// task's §6.6 fault vector.
		if err := k.RoleFaultByTaskIndex.Set(ctx, types.NewRoleFaultByTaskKey(state.TaskId, faultID)); err != nil {
			return err
		}
	}
	if err := k.EnsureRoleFaultByTaskIndexInvariant(ctx); err != nil {
		return err
	}
	return k.validateRoleFaultPruneIndex(ctx, retention, true)
}

// EnsureRoleFaultByTaskIndexInvariant proves both directions of the derived
// by-task index. It runs on import after the rebuild, on export before the
// primaries are collected, and in the App invariant registry, so a runner that
// ever wrote one side without the other is caught immediately rather than by a
// wrong fault_summary_hash months later.
func (k Keeper) EnsureRoleFaultByTaskIndexInvariant(ctx context.Context) error {
	// Both key components are raw Hash32 now, so the dedupe set uses a fixed-width
	// array; the hex that used to be the key survives only in the error texts.
	indexed := make(map[[shared.Hash32KeySize]byte]struct{})
	faultKey := func(raw shared.Hash32Key) [shared.Hash32KeySize]byte {
		var key [shared.Hash32KeySize]byte
		copy(key[:], raw)
		return key
	}
	rows, err := k.RoleFaultByTaskIndex.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer rows.Close()
	for ; rows.Valid(); rows.Next() {
		key, err := rows.Key()
		if err != nil {
			return err
		}
		state, err := k.RoleFault.Get(ctx, key.K2())
		if err != nil {
			return fmt.Errorf("role fault by task %s/%s has no primary", hexRef(key.K1()), hexRef(key.K2()))
		}
		if !bytes.Equal(state.TaskId, key.K1()) || !bytes.Equal(state.FaultId, key.K2()) {
			return fmt.Errorf("role fault by task %s/%s disagrees with primary", hexRef(key.K1()), hexRef(key.K2()))
		}
		if _, duplicate := indexed[faultKey(key.K2())]; duplicate {
			return fmt.Errorf("role fault %s is indexed under more than one task", hexRef(key.K2()))
		}
		indexed[faultKey(key.K2())] = struct{}{}
	}

	primaries, err := k.RoleFault.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer primaries.Close()
	for ; primaries.Valid(); primaries.Next() {
		key, err := primaries.Key()
		if err != nil {
			return err
		}
		if _, exists := indexed[faultKey(key)]; !exists {
			return fmt.Errorf("role fault %s has no by-task index", hexRef(key))
		}
	}
	return nil
}

// RoleFaultsForTask reads one task's §6.6 fault vector in bounded work through
// the derived by-task index. The rows come back in fault_id ascending order
// because the index is keyed (task_id, fault_id) and both components are now raw
// Hash32; memcmp order is the same order the lower-hex key produced.
func (k Keeper) RoleFaultsForTask(ctx context.Context, taskID []byte) ([]types.RoleFaultState, error) {
	iter, err := k.RoleFaultByTaskIndex.Iterate(ctx, collections.NewPrefixedPairRange[shared.Hash32Key, shared.Hash32Key](taskID))
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	faults := make([]types.RoleFaultState, 0, 4)
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		state, err := k.RoleFault.Get(ctx, key.K2())
		if err != nil {
			return nil, fmt.Errorf("role fault by task %s/%s has no primary", hexRef(key.K1()), hexRef(key.K2()))
		}
		if !bytes.Equal(state.TaskId, taskID) || !bytes.Equal(state.FaultId, key.K2()) {
			return nil, fmt.Errorf("role fault by task %s/%s disagrees with primary", hexRef(key.K1()), hexRef(key.K2()))
		}
		faults = append(faults, state)
	}
	return faults, nil
}

// validateRoleFaultPruneIndex proves both directions. InitGenesis requires the
// exact derived height; export also accepts a strictly later retry height that
// was written after a terminal consumer gate returned false.
func (k Keeper) validateRoleFaultPruneIndex(ctx context.Context, retention uint64, exact bool) error {
	indexed := make(map[[shared.Hash32KeySize]byte]uint64)
	pruneKey := func(raw shared.Hash32Key) [shared.Hash32KeySize]byte {
		var key [shared.Hash32KeySize]byte
		copy(key[:], raw)
		return key
	}
	rows, err := k.RoleFaultPruneIndex.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer rows.Close()
	for ; rows.Valid(); rows.Next() {
		key, err := rows.Key()
		if err != nil {
			return err
		}
		if prior, duplicate := indexed[pruneKey(key.K2())]; duplicate {
			return fmt.Errorf("role fault %s has duplicate prune heights %d and %d", hexRef(key.K2()), prior, key.K1())
		}
		state, err := k.RoleFault.Get(ctx, key.K2())
		if err != nil {
			return fmt.Errorf("role fault prune %d/%s has no primary", key.K1(), hexRef(key.K2()))
		}
		earliest, addErr := checkedAdd(state.RecordedHeight, retention)
		if addErr != nil || !bytes.Equal(state.FaultId, key.K2()) || key.K1() < earliest || exact && key.K1() != earliest {
			return fmt.Errorf("role fault prune %d/%s disagrees with primary", key.K1(), hexRef(key.K2()))
		}
		indexed[pruneKey(key.K2())] = key.K1()
	}

	primaries, err := k.RoleFault.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer primaries.Close()
	for ; primaries.Valid(); primaries.Next() {
		entry, err := primaries.KeyValue()
		if err != nil {
			return err
		}
		if !bytes.Equal(entry.Value.FaultId, entry.Key) {
			return fmt.Errorf("role fault key %s disagrees with primary id", hexRef(entry.Key))
		}
		if _, exists := indexed[pruneKey(entry.Key)]; !exists {
			return fmt.Errorf("role fault %s has no prune index", hexRef(entry.Key))
		}
	}
	return nil
}
