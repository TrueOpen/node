package keeper

import (
	"bytes"
	"context"
	"fmt"

	"cosmossdk.io/collections"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// releaseTaskAdmissionRefs releases every retained Hub body exactly once and
// removes the Task-owned parameter-bucket receipts in the same cache context.
func (k Keeper) releaseTaskAdmissionRefs(ctx context.Context, core types.TaskCoreState, height uint64) error {
	taskKey, err := taskStoreKey(core.TaskId)
	if err != nil {
		return err
	}
	assignment, err := k.TaskAssignment.Get(ctx, taskKey)
	if err != nil || !bytes.Equal(assignment.TaskId, core.TaskId) {
		return fmt.Errorf("terminal Task assignment is unavailable for admission-ref release")
	}
	selection, err := k.TaskBuilderSelection.Get(ctx, taskKey)
	if err != nil || !bytes.Equal(selection.TaskId, core.TaskId) {
		return fmt.Errorf("terminal Task Builder selection is unavailable for admission-ref release")
	}
	if assignment.CandidatePoolRefReleased != selection.BuilderSetRefReleased {
		return fmt.Errorf("terminal Task admission refs have a partial release state")
	}
	refsAlreadyReleased := assignment.CandidatePoolRefReleased
	if !assignment.CandidatePoolRefReleased {
		released, err := k.hubKeeper.ReleaseCandidatePoolTaskRef(ctx, core.TaskId, assignment.CandidatePoolSnapshotId, height)
		if err != nil {
			return err
		}
		if !released {
			return fmt.Errorf("candidate pool Task ref is missing before release")
		}
		assignment.CandidatePoolRefReleased = true
		if err := k.TaskAssignment.Set(ctx, taskKey, assignment); err != nil {
			return err
		}
	}

	if !selection.BuilderSetRefReleased {
		released, err := k.hubKeeper.ReleaseBuilderSetTaskRef(
			ctx, core.TaskId, selection.BuilderSetId, selection.BuilderSetHash, height,
		)
		if err != nil {
			return err
		}
		if !released {
			return fmt.Errorf("BuilderSet Task ref is missing before release")
		}
		selection.BuilderSetRefReleased = true
		if err := k.TaskBuilderSelection.Set(ctx, taskKey, selection); err != nil {
			return err
		}
	}

	// Still an exact match on task_id. A prefixed range bound is the non-terminal
	// encoding of K1 alone, and Hash32KeyCodec writes exactly 32 bytes with no
	// terminator; because every K1 here is that same width, no other task_id can
	// share those bytes as a prefix. That fixed width is what replaces the
	// delimiting role StringKey's NUL used to play, for this range and for every
	// other Hash32-prefixed range in the module.
	iter, err := k.TaskBucketRef.Iterate(ctx,
		collections.NewPrefixedTripleRange[types.Hash32Key, int32, string](taskKey))
	if err != nil {
		return err
	}
	defer iter.Close()
	seenKinds := make(map[shared.BucketKind]struct{}, 1)
	keys := make([]types.TaskBucketRefKeyTriple, 0, 1)
	refs := make([]types.TaskBucketRefState, 0, 1)
	for ; iter.Valid(); iter.Next() {
		if len(refs) == 1 {
			return fmt.Errorf("Task retains more than the one registered parameter bucket ref")
		}
		key, err := iter.Key()
		if err != nil {
			return err
		}
		state, err := iter.Value()
		if err != nil {
			return err
		}
		if !bytes.Equal(key.K1(), taskKey) || key.K2() != int32(state.BucketKind) || key.K3() != state.BucketKey ||
			!bytes.Equal(state.TaskId, core.TaskId) || !hubtypes.IsParameterBucketKind(state.BucketKind) ||
			state.BucketKey != hubtypes.DefaultParameterBucketKey || state.Version == 0 || state.AcquiredHeight == 0 {
			return fmt.Errorf("terminal Task parameter bucket ref is non-canonical")
		}
		if _, duplicate := seenKinds[state.BucketKind]; duplicate {
			return fmt.Errorf("terminal Task retains duplicate parameter bucket kind %s", state.BucketKind.String())
		}
		seenKinds[state.BucketKind] = struct{}{}
		keys = append(keys, key)
		refs = append(refs, state)
	}
	if refsAlreadyReleased {
		if len(refs) != 0 {
			return fmt.Errorf("released terminal Task still retains parameter bucket refs")
		}
		return nil
	}
	if len(refs) != 1 || refs[0].BucketKind != shared.BucketKind_BUCKET_KIND_TIMEOUT {
		return fmt.Errorf("terminal Task must retain exactly one timeout bucket ref before release")
	}
	for index, state := range refs {
		if err := k.hubKeeper.ReleaseParameterBucketTaskRef(
			ctx, state.BucketKind, state.BucketKey, state.Version, height,
		); err != nil {
			return err
		}
		if err := k.TaskBucketRef.Remove(ctx, keys[index]); err != nil {
			return err
		}
	}
	return nil
}
