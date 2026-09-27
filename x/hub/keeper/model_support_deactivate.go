package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// EnqueueModelSupportDeactivation starts one model-scoped, bounded sweep.
// The model status gates support immediately; the cursor converges stored rows.
// The caller is responsible for clearing any in-flight threshold recheck state
// for modelID first: this call assumes that is already done and does not
// itself resolve a conflicting recheck cursor.
func (k Keeper) EnqueueModelSupportDeactivation(ctx context.Context, modelID []byte) error {
	if len(modelID) != shared.Hash32KeySize {
		return fmt.Errorf("model support deactivation requires a raw Hash32 model_id")
	}
	if has, err := k.ModelSupportDeactivateCursor.Has(ctx, modelID); err != nil {
		return err
	} else if has {
		return fmt.Errorf("model support deactivation is already in progress")
	}
	if _, found, err := k.nextModelSupportOperator(ctx, modelID, ""); err != nil {
		return err
	} else if !found {
		// No ModelSupportState row exists for this model: nothing to converge,
		// so no cursor is created and the status write completes directly.
		return nil
	}
	return k.ModelSupportDeactivateCursor.Set(ctx, modelID, types.ModelSupportDeactivateCursorState{
		ModelId: append([]byte(nil), modelID...),
	})
}

// ProcessModelSupportDeactivations advances cursors in model ID order under
// both the dedicated per-block item cap and the shared EndBlock byte budget.
func (k Keeper) ProcessModelSupportDeactivations(ctx context.Context, height, visitedLimit, bytesLimit uint64) (uint64, uint64, error) {
	budget := newEndblockBudget(visitedLimit, bytesLimit)
	for !budget.exhausted() {
		modelID, cursor, found, err := k.firstModelSupportDeactivateCursor(ctx)
		if err != nil {
			visited, consumed := budget.result()
			return visited, consumed, err
		}
		if !found {
			break
		}
		done, err := k.advanceModelSupportDeactivateCursor(ctx, modelID, cursor, height, budget)
		if err != nil {
			visited, consumed := budget.result()
			return visited, consumed, err
		}
		if !done {
			break
		}
	}
	visited, consumed := budget.result()
	return visited, consumed, nil
}

func (k Keeper) firstModelSupportDeactivateCursor(ctx context.Context) ([]byte, types.ModelSupportDeactivateCursorState, bool, error) {
	iter, err := k.ModelSupportDeactivateCursor.Iterate(ctx, nil)
	if err != nil {
		return nil, types.ModelSupportDeactivateCursorState{}, false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return nil, types.ModelSupportDeactivateCursorState{}, false, nil
	}
	entry, err := iter.KeyValue()
	if err != nil {
		return nil, types.ModelSupportDeactivateCursorState{}, false, err
	}
	if !bytes.Equal(entry.Key, entry.Value.ModelId) {
		return nil, types.ModelSupportDeactivateCursorState{}, false, fmt.Errorf("model support deactivate cursor key mismatch")
	}
	return entry.Key, entry.Value, true, nil
}

func (k Keeper) advanceModelSupportDeactivateCursor(ctx context.Context, modelID []byte, cursor types.ModelSupportDeactivateCursorState, height uint64, budget *endblockBudget) (bool, error) {
	if len(modelID) != shared.Hash32KeySize || !bytes.Equal(modelID, cursor.ModelId) || cursor.DeactivatedCount > cursor.VisitedCount {
		return false, fmt.Errorf("model support deactivate cursor is non-canonical")
	}
	model, err := k.GetModel(ctx, modelID)
	if err != nil {
		return false, err
	}
	if model.Status != types.ModelStatusFrozen && model.Status != types.ModelStatusDelisted {
		return false, fmt.Errorf("model support deactivate cursor requires a frozen or delisted model")
	}
	for !budget.exhausted() {
		operator, found, err := k.nextModelSupportOperator(ctx, modelID, cursor.LastOperatorAddress)
		if err != nil {
			return false, err
		}
		if !found {
			if err := k.ModelSupportDeactivateCursor.Remove(ctx, modelID); err != nil {
				return false, err
			}
			budget.charge(cursor.Size())
			return true, nil
		}
		old, exists, err := k.loadModelSupport(ctx, operator, modelID)
		if err != nil {
			return false, err
		}
		rowBytes := 0
		if !exists {
			if err := k.ModelSupportByModelIndex.Remove(ctx, types.NewModelSupportByModelIndexKey(modelID, operator)); err != nil && !errors.Is(err, collections.ErrNotFound) {
				return false, err
			}
		} else {
			rowBytes = old.Size()
			if old.DeclaredSupport || old.SupportActive || old.ActiveSupportStakeSnapshot != 0 {
				updated, err := k.DeactivateModelSupport(ctx, operator, modelID, types.ModelSupportDeactivateFrozen, height)
				if err != nil {
					return false, err
				}
				rowBytes = max(rowBytes, updated.Size())
				cursor.DeactivatedCount, err = checkedAdd(cursor.DeactivatedCount, 1)
				if err != nil {
					return false, err
				}
			}
		}
		cursor.VisitedCount, err = checkedAdd(cursor.VisitedCount, 1)
		if err != nil {
			return false, err
		}
		cursor.LastOperatorAddress = operator
		if err := k.ModelSupportDeactivateCursor.Set(ctx, modelID, cursor); err != nil {
			return false, err
		}
		budget.charge(max(rowBytes, cursor.Size()))
	}
	return false, nil
}
