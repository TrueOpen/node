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

// ProcessModelSupportRechecks advances the model-local threshold recheck under
// both the support-specific item cap and the shared EndBlock budget.
func (k Keeper) ProcessModelSupportRechecks(ctx context.Context, height, visitedLimit, bytesLimit uint64) (uint64, uint64, error) {
	budget := newEndblockBudget(visitedLimit, bytesLimit)
	for !budget.exhausted() {
		modelID, cursor, found, err := k.firstModelSupportRecheckCursor(ctx)
		if err != nil {
			visited, consumed := budget.result()
			return visited, consumed, err
		}
		if found {
			done, err := k.advanceModelSupportRecheckCursor(ctx, modelID, cursor, height, budget)
			if err != nil {
				visited, consumed := budget.result()
				return visited, consumed, err
			}
			if !done {
				break
			}
			continue
		}
		indexKey, due, err := k.firstDueModelSupportRecheck(ctx, height)
		if err != nil {
			visited, consumed := budget.result()
			return visited, consumed, err
		}
		if !due {
			break
		}
		if deactivating, err := k.ModelSupportDeactivateCursor.Has(ctx, indexKey.K2()); err != nil {
			visited, consumed := budget.result()
			return visited, consumed, err
		} else if deactivating {
			break
		}
		model, exists, err := k.loadModel(ctx, indexKey.K2())
		if err != nil {
			visited, consumed := budget.result()
			return visited, consumed, err
		}
		if !exists {
			if err := k.ModelSupportRecheckIndex.Remove(ctx, indexKey); err != nil {
				visited, consumed := budget.result()
				return visited, consumed, err
			}
			budget.charge(0)
			continue
		}
		if model.PendingEffectiveHeight != 0 && model.PendingEffectiveHeight != indexKey.K1() {
			if err := k.ModelSupportRecheckIndex.Remove(ctx, indexKey); err != nil {
				visited, consumed := budget.result()
				return visited, consumed, err
			}
			budget.charge(model.Size())
			continue
		}
		lowered := model.PendingEffectiveHeight == 0
		if !lowered {
			model.SupportMinStake = model.PendingSupportMinStake
			model.PendingSupportMinStake = 0
			model.PendingEffectiveHeight = 0
			model.UpdatedHeight = height
			if err := k.setModelState(ctx, model); err != nil {
				visited, consumed := budget.result()
				return visited, consumed, err
			}
		}
		if err := k.ModelSupportRecheckIndex.Remove(ctx, indexKey); err != nil {
			visited, consumed := budget.result()
			return visited, consumed, err
		}
		cursor = types.ModelSupportRecheckCursorState{
			ModelId: append([]byte(nil), model.ModelId...), EffectiveHeight: indexKey.K1(),
			MinStakeLowered: lowered,
		}
		if err := k.ModelSupportRecheckCursor.Set(ctx, model.ModelId, cursor); err != nil {
			visited, consumed := budget.result()
			return visited, consumed, err
		}
		budget.charge(max(model.Size(), cursor.Size()))
	}
	visited, consumed := budget.result()
	return visited, consumed, nil
}

func (k Keeper) firstModelSupportRecheckCursor(ctx context.Context) ([]byte, types.ModelSupportRecheckCursorState, bool, error) {
	iter, err := k.ModelSupportRecheckCursor.Iterate(ctx, nil)
	if err != nil {
		return nil, types.ModelSupportRecheckCursorState{}, false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return nil, types.ModelSupportRecheckCursorState{}, false, nil
	}
	entry, err := iter.KeyValue()
	if err != nil {
		return nil, types.ModelSupportRecheckCursorState{}, false, err
	}
	if !bytes.Equal(entry.Key, entry.Value.ModelId) {
		return nil, types.ModelSupportRecheckCursorState{}, false, fmt.Errorf("model support recheck cursor key mismatch")
	}
	return entry.Key, entry.Value, true, nil
}

func (k Keeper) firstDueModelSupportRecheck(ctx context.Context, height uint64) (types.ModelSupportRecheckIndexKeyPair, bool, error) {
	iter, err := k.ModelSupportRecheckIndex.Iterate(ctx, nil)
	if err != nil {
		return types.ModelSupportRecheckIndexKeyPair{}, false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return types.ModelSupportRecheckIndexKeyPair{}, false, nil
	}
	key, err := iter.Key()
	return key, err == nil && key.K1() <= height, err
}

func (k Keeper) advanceModelSupportRecheckCursor(ctx context.Context, modelID []byte, cursor types.ModelSupportRecheckCursorState, height uint64, budget *endblockBudget) (bool, error) {
	if len(modelID) != shared.Hash32KeySize || !bytes.Equal(modelID, cursor.ModelId) || cursor.EffectiveHeight == 0 {
		return false, fmt.Errorf("model support recheck cursor is non-canonical")
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return false, err
	}
	epoch := epochForHeight(height, params.Epoch.EpochLengthBlocks)
	for !budget.exhausted() {
		operator, found, err := k.nextModelSupportOperator(ctx, modelID, cursor.LastOperatorAddress)
		if err != nil {
			return false, err
		}
		if !found {
			model, err := k.GetModel(ctx, modelID)
			if err != nil {
				return false, err
			}
			if err := k.deriveModelStatus(ctx, &model, height); err != nil {
				return false, err
			}
			if err := k.ModelSupportRecheckCursor.Remove(ctx, modelID); err != nil {
				return false, err
			}
			budget.charge(cursor.Size())
			return true, nil
		}
		state, exists, err := k.loadModelSupport(ctx, operator, modelID)
		if err != nil {
			return false, err
		}
		rowBytes := 0
		if !exists {
			if err := k.ModelSupportByModelIndex.Remove(ctx, types.NewModelSupportByModelIndexKey(modelID, operator)); err != nil && !errors.Is(err, collections.ErrNotFound) {
				return false, err
			}
		} else {
			rowBytes = state.Size()
			if state.DeclaredSupport {
				reason := types.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_BOND_BELOW_MIN
				if !cursor.MinStakeLowered {
					reason = types.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_MIN_STAKE_RAISED
				}
				updated, changed, err := k.reweightModelSupport(ctx, state, epoch, height, reason)
				if err != nil {
					return false, err
				}
				if changed {
					rowBytes = max(rowBytes, updated.Size())
					if state.SupportActive && !updated.SupportActive {
						cursor.SuspendedCount, err = checkedAdd(cursor.SuspendedCount, 1)
					}
					if !state.SupportActive && updated.SupportActive {
						cursor.RestoredCount, err = checkedAdd(cursor.RestoredCount, 1)
					}
					if err != nil {
						return false, err
					}
				}
			}
		}
		cursor.VisitedCount, err = checkedAdd(cursor.VisitedCount, 1)
		if err != nil {
			return false, err
		}
		cursor.LastOperatorAddress = operator
		if err := k.ModelSupportRecheckCursor.Set(ctx, modelID, cursor); err != nil {
			return false, err
		}
		budget.charge(max(rowBytes, cursor.Size()))
	}
	return false, nil
}
