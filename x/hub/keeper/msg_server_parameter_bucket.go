package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (m msgServer) UpdateTimeoutBucket(ctx context.Context, req *types.MsgUpdateTimeoutBucket) (*types.MsgUpdateTimeoutBucketResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("nil parameter bucket request")
	}
	if err := m.k.assertAuthority(req.Authority); err != nil {
		return nil, err
	}
	state, status, err := m.commitParameterBucketUpdate(ctx, req.Update)
	if err != nil {
		return nil, err
	}
	return &types.MsgUpdateTimeoutBucketResponse{
		BucketKey: state.BucketKey, NewVersion: state.Version, EffectiveHeight: state.EffectiveHeight,
		BucketHash: append([]byte(nil), state.ContentHash...), Status: status,
	}, nil
}

func (m msgServer) commitParameterBucketUpdate(
	ctx context.Context,
	update types.TimeoutBucketUpdateV1,
) (types.ParameterBucketVersionState, shared.MutationStatusV1, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if sdkCtx.BlockHeight() < 0 {
		return types.ParameterBucketVersionState{}, 0, fmt.Errorf("negative block height")
	}
	currentHeight := uint64(sdkCtx.BlockHeight())
	if update.EffectiveHeight < currentHeight {
		return types.ParameterBucketVersionState{}, 0, fmt.Errorf("effective_height cannot be earlier than the current height")
	}
	if update.ExpectedCurrentVersion == math.MaxUint64 {
		return types.ParameterBucketVersionState{}, 0, fmt.Errorf("parameter bucket version overflows uint64")
	}
	newVersion := update.ExpectedCurrentVersion + 1
	params, err := m.k.Params.Get(ctx)
	if err != nil {
		return types.ParameterBucketVersionState{}, 0, err
	}
	state := types.ParameterBucketVersionState{
		BucketKind: shared.BucketKind_BUCKET_KIND_TIMEOUT,
		BucketKey:  update.BucketKey, Version: newVersion,
		SchemaVersion: types.ParameterBucketSchemaVersionV1, EffectiveHeight: update.EffectiveHeight,
		TimeoutEntries: types.TimeoutBucketEntriesV1{Entries: append([]types.TimeoutBucketEntryV1(nil), update.Entries...)},
		EntryCount:     uint32(len(update.Entries)), CreatedHeight: currentHeight,
	}
	_, state.EncodedSizeBytes, err = types.CanonicalTimeoutBucketEntries(state.TimeoutEntries.Entries)
	if err != nil {
		return types.ParameterBucketVersionState{}, 0, err
	}
	state.ContentHash, err = types.ParameterBucketContentHash(sdkCtx.ChainID(), state)
	if err != nil {
		return types.ParameterBucketVersionState{}, 0, err
	}
	if err := types.ValidateParameterBucketVersion(state, params.Bucket, sdkCtx.ChainID()); err != nil {
		return types.ParameterBucketVersionState{}, 0, err
	}
	versionKey := types.NewParameterBucketVersionKey(state.BucketKind, state.BucketKey, newVersion)
	existing, err := m.k.GetParameterBucketVersion(ctx, state.BucketKind, state.BucketKey, newVersion)
	if err == nil {
		if sameParameterBucketContent(existing, state) {
			return existing, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, nil
		}
		return types.ParameterBucketVersionState{}, 0, fmt.Errorf("parameter bucket version already exists with different content")
	}
	if !errors.Is(err, collections.ErrNotFound) {
		return types.ParameterBucketVersionState{}, 0, err
	}
	pointerKey := types.NewParameterBucketPointerKey(state.BucketKind, state.BucketKey)
	current, err := m.k.ParameterBucketCurrentPointer.Get(ctx, pointerKey)
	if err != nil {
		return types.ParameterBucketVersionState{}, 0, err
	}
	if current.BucketKind != state.BucketKind || current.BucketKey != state.BucketKey || current.CurrentVersion != update.ExpectedCurrentVersion {
		return types.ParameterBucketVersionState{}, 0, fmt.Errorf("expected_current_version does not match the current pointer")
	}
	if _, err := m.k.ParameterBucketPendingPointer.Get(ctx, pointerKey); err == nil {
		return types.ParameterBucketVersionState{}, 0, fmt.Errorf("parameter bucket already has a pending version")
	} else if !errors.Is(err, collections.ErrNotFound) {
		return types.ParameterBucketVersionState{}, 0, err
	}
	cache, commit := sdkCtx.CacheContext()
	if err := m.k.ParameterBucketVersion.Set(cache, versionKey, state); err != nil {
		return types.ParameterBucketVersionState{}, 0, err
	}
	pending := types.ParameterBucketPendingPointerState{
		BucketKind: state.BucketKind, BucketKey: state.BucketKey, PendingVersion: newVersion,
		EffectiveHeight: update.EffectiveHeight, PendingContentHash: append([]byte(nil), state.ContentHash...),
	}
	if err := m.k.ParameterBucketPendingPointer.Set(cache, pointerKey, pending); err != nil {
		return types.ParameterBucketVersionState{}, 0, err
	}
	if err := m.k.ParameterBucketEffectiveIndex.Set(cache, types.NewParameterBucketEffectiveIndexKey(update.EffectiveHeight, state.BucketKind, state.BucketKey, newVersion)); err != nil {
		return types.ParameterBucketVersionState{}, 0, err
	}
	mustEmitHubEvent(cache, &types.EventParameterBucketUpdated{
		BucketKind: state.BucketKind, BucketKey: state.BucketKey, OldVersion: update.ExpectedCurrentVersion,
		NewVersion: newVersion, EffectiveHeight: update.EffectiveHeight, BucketHash: append([]byte(nil), state.ContentHash...),
	})
	commit()
	return state, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, nil
}

func sameParameterBucketContent(left, right types.ParameterBucketVersionState) bool {
	return left.BucketKind == right.BucketKind && left.BucketKey == right.BucketKey && left.Version == right.Version &&
		left.SchemaVersion == right.SchemaVersion && left.EffectiveHeight == right.EffectiveHeight &&
		left.EntryCount == right.EntryCount && left.EncodedSizeBytes == right.EncodedSizeBytes &&
		bytes.Equal(left.ContentHash, right.ContentHash)
}
