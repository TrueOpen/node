package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/wire/dispatch"
)

// scheduleCandidateBodyPrune preserves unrecorded material windows after expiry
// and late-recorded proof windows after the last Task reference closes. The
// reverse height cancels pending work in O(1) and is rebuilt at Genesis. Its
// absence on an unreferenced EXPIRED body means physical pruning has begun.
func (k Keeper) scheduleCandidateBodyPrune(ctx context.Context, snapshot types.CandidatePoolSnapshotState, height uint64) error {
	interval, err := dispatch.ServiceKeyRotationInterval(shared.DispatchRotationWindowHardLimits())
	if err != nil {
		return err
	}
	due, overflow := shared.CheckedHeightAddV1(max(height, snapshot.ExpiresHeight), interval)
	if overflow {
		return fmt.Errorf("candidate body proof retention overflows")
	}
	if err := k.cancelCandidateBodyPrune(ctx, snapshot.SnapshotId); err != nil {
		return err
	}
	if err := k.CandidatePoolBodyPruneHeight.Set(ctx, snapshot.SnapshotId, due); err != nil {
		return err
	}
	return k.CandidatePoolPruneIndex.Set(ctx, types.NewCandidatePoolPruneIndexKey(due, snapshot.SnapshotId, types.CandidatePoolPrunePhase_CANDIDATE_POOL_PRUNE_PHASE_BODY))
}

func (k Keeper) cancelCandidateBodyPrune(ctx context.Context, id []byte) error {
	due, err := k.CandidatePoolBodyPruneHeight.Get(ctx, id)
	if errors.Is(err, collections.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := k.CandidatePoolPruneIndex.Remove(ctx, types.NewCandidatePoolPruneIndexKey(due, id, types.CandidatePoolPrunePhase_CANDIDATE_POOL_PRUNE_PHASE_BODY)); err != nil {
		return err
	}
	return k.CandidatePoolBodyPruneHeight.Remove(ctx, id)
}
