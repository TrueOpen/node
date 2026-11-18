package keeper

import (
	"bytes"
	"context"
	"fmt"

	"cosmossdk.io/collections"
	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (k Keeper) exportDispatchFaults(ctx context.Context) ([]types.DispatchFaultState, error) {
	var rows []types.DispatchFaultState
	err := k.DispatchFault.Walk(ctx, nil, func(key types.DispatchFaultKeyPair, state internaltypes.DispatchFaultStoreState) (bool, error) {
		row, err := k.projectDispatchFault(state)
		if err != nil {
			return false, err
		}
		if row.BuilderAddress != key.K1() || !bytes.Equal(row.FaultId, key.K2()) {
			return false, fmt.Errorf("dispatch fault key/primary mismatch")
		}
		rows = append(rows, row)
		return false, nil
	})
	return rows, err
}

// RestoreDispatchFaultPruneIndex runs after Task parameters are imported. Hub
// owns the receipts, while their retention is Task-owned; no fallback or second
// persisted parameter can reconstruct that authority during Hub's earlier import.
func (k Keeper) RestoreDispatchFaultPruneIndex(ctx context.Context, retention uint64) error {
	if retention == 0 {
		return fmt.Errorf("dispatch fault retention must be positive")
	}
	return k.DispatchFault.Walk(ctx, nil, func(key types.DispatchFaultKeyPair, state internaltypes.DispatchFaultStoreState) (bool, error) {
		row, err := k.projectDispatchFault(state)
		if err != nil {
			return false, err
		}
		if row.BuilderAddress != key.K1() || !bytes.Equal(row.FaultId, key.K2()) {
			return false, fmt.Errorf("dispatch fault key/primary mismatch")
		}
		prune, err := dispatchFaultPruneHeight(row.FaultHeight, retention)
		if err != nil {
			return false, err
		}
		return false, k.DispatchFaultPruneIndex.Set(ctx, collections.Join3(prune, row.BuilderAddress, bytes.Clone(row.FaultId)))
	})
}

// ProcessDispatchFaultPrunes shares the Hub visited/bytes allowance. Receipts
// have no live consumers after their independently retained challenge closes.
func (k Keeper) ProcessDispatchFaultPrunes(ctx context.Context, height, limit, maxBytes uint64) (uint64, uint64, error) {
	budget := newEndblockBudget(limit, maxBytes)
	iter, err := k.DispatchFaultPruneIndex.Iterate(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	var due []types.DispatchFaultPruneKeyTriple
	for ; iter.Valid() && uint64(len(due)) < limit; iter.Next() {
		key, err := iter.Key()
		if err != nil {
			iter.Close()
			return 0, 0, err
		}
		if key.K1() > height {
			break
		}
		due = append(due, key)
	}
	if err := iter.Close(); err != nil {
		return 0, 0, err
	}
	for _, key := range due {
		if budget.exhausted() {
			break
		}
		state, err := k.DispatchFault.Get(ctx, collections.Join(key.K2(), key.K3()))
		if err != nil {
			return budget.visited, budget.bytes, err
		}
		if !bytes.Equal(state.FaultId, key.K3()) || state.FaultHeight >= key.K1() {
			return budget.visited, budget.bytes, fmt.Errorf("dispatch fault prune index/primary mismatch")
		}
		budget.charge(state.Size())
		if err := k.DispatchFault.Remove(ctx, collections.Join(key.K2(), key.K3())); err != nil {
			return budget.visited, budget.bytes, err
		}
		if err := k.DispatchFaultPruneIndex.Remove(ctx, key); err != nil {
			return budget.visited, budget.bytes, err
		}
	}
	return budget.visited, budget.bytes, nil
}

func dispatchFaultPruneHeight(height, retention uint64) (uint64, error) {
	prune, overflow := shared.CheckedHeightAddV1(height, retention)
	if overflow {
		return 0, fmt.Errorf("dispatch fault prune height overflows")
	}
	return prune, nil
}

// EnsureDispatchFaultPruneInvariant audits the derived queue after both modules
// have restored genesis. The scheduled height is immutable across governance.
func (k Keeper) EnsureDispatchFaultPruneInvariant(ctx context.Context) error {
	seen := make(map[string]struct{})
	if err := k.DispatchFaultPruneIndex.Walk(ctx, nil, func(key types.DispatchFaultPruneKeyTriple) (bool, error) {
		row, err := k.DispatchFault.Get(ctx, collections.Join(key.K2(), key.K3()))
		if err != nil {
			return false, fmt.Errorf("dispatch fault prune has no primary: %w", err)
		}
		if key.K1() <= row.FaultHeight || !shared.IsBlockHeightV1(key.K1()) || !bytes.Equal(row.FaultId, key.K3()) {
			return false, fmt.Errorf("dispatch fault prune index/primary mismatch")
		}
		id := key.K2() + string(key.K3())
		if _, duplicate := seen[id]; duplicate {
			return false, fmt.Errorf("dispatch fault has duplicate prune schedules")
		}
		seen[id] = struct{}{}
		return false, nil
	}); err != nil {
		return err
	}
	return k.DispatchFault.Walk(ctx, nil, func(key types.DispatchFaultKeyPair, row internaltypes.DispatchFaultStoreState) (bool, error) {
		projected, err := k.projectDispatchFault(row)
		if err != nil {
			return false, err
		}
		if projected.BuilderAddress != key.K1() || !bytes.Equal(projected.FaultId, key.K2()) {
			return false, fmt.Errorf("dispatch fault key/primary mismatch")
		}
		if _, found := seen[key.K1()+string(key.K2())]; !found {
			return false, fmt.Errorf("dispatch fault has no prune schedule")
		}
		return false, nil
	})
}
