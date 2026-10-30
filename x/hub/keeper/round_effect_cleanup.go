package keeper

import (
	"bytes"
	"context"
	"fmt"

	"cosmossdk.io/collections"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// DeleteOneRoundSlashSummary retires one custody receipt after Task has closed
// finality and its evidence retention window. The round prefix avoids a global
// scan or a second task index for this derived audit data.
func (k Keeper) DeleteOneRoundSlashSummary(ctx context.Context, taskID, roundID []byte) (bool, error) {
	if len(taskID) != 32 || len(roundID) != 32 {
		return false, fmt.Errorf("round slash cleanup requires raw32 scope")
	}
	rangeKey := collections.NewSuperPrefixedTripleRange[int32, shared.Hash32Key, uint32](int32(types.SlashSourceKind_SLASH_SOURCE_KIND_CHALLENGE_EFFECT), roundID)
	iter, err := k.SlashSummary.Iterate(ctx, rangeKey)
	if err != nil {
		return false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return false, nil
	}
	entry, err := iter.KeyValue()
	if err != nil {
		return false, err
	}
	row, err := k.ProjectSlashSummaryStore(entry.Value)
	if err != nil {
		return false, err
	}
	if !bytes.Equal(row.GetTaskId(), taskID) || !bytes.Equal(row.SourceId, roundID) {
		return false, fmt.Errorf("round slash cleanup scope mismatch")
	}
	return true, k.SlashSummary.Remove(ctx, entry.Key)
}

// HasTaskRoleFaults keeps the task's terminal authority until Hub's independently
// budgeted fault retention runner has consumed it.
func (k Keeper) HasTaskRoleFaults(ctx context.Context, taskID []byte) (bool, error) {
	iter, err := k.RoleFaultByTaskIndex.Iterate(ctx, collections.NewPrefixedPairRange[shared.Hash32Key, shared.Hash32Key](taskID))
	if err != nil {
		return false, err
	}
	defer iter.Close()
	return iter.Valid(), nil
}
