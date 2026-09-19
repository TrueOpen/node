package keeper

import (
	"bytes"
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (k Keeper) prepareParameterBucketGenesis(ctx context.Context, genesis types.GenesisState) (types.GenesisState, error) {
	chainID := sdk.UnwrapSDKContext(ctx).ChainID()
	for i := range genesis.ParameterBucketVersions {
		state := genesis.ParameterBucketVersions[i]
		if err := types.ValidateParameterBucketVersionStructure(state, genesis.Params.Bucket); err != nil {
			return types.GenesisState{}, fmt.Errorf("parameter bucket version: %w", err)
		}
		hash, err := types.ParameterBucketContentHash(chainID, state)
		if err != nil {
			return types.GenesisState{}, err
		}
		if len(state.ContentHash) != 0 && !bytes.Equal(state.ContentHash, hash) {
			return types.GenesisState{}, fmt.Errorf("parameter bucket version content_hash mismatch")
		}
		state.ContentHash = hash
		genesis.ParameterBucketVersions[i] = state
	}
	versions := make(map[parameterBucketVersionIdentity]types.ParameterBucketVersionState, len(genesis.ParameterBucketVersions))
	for _, state := range genesis.ParameterBucketVersions {
		versions[newParameterBucketVersionIdentity(state.BucketKind, state.BucketKey, state.Version)] = state
	}
	for i := range genesis.ParameterBucketCurrentPointers {
		pointer := genesis.ParameterBucketCurrentPointers[i]
		body, exists := versions[newParameterBucketVersionIdentity(pointer.BucketKind, pointer.BucketKey, pointer.CurrentVersion)]
		if !exists {
			return types.GenesisState{}, fmt.Errorf("parameter bucket current pointer references a missing version")
		}
		if len(pointer.CurrentContentHash) != 0 && !bytes.Equal(pointer.CurrentContentHash, body.ContentHash) {
			return types.GenesisState{}, fmt.Errorf("parameter bucket current pointer hash mismatch")
		}
		pointer.CurrentContentHash = append([]byte(nil), body.ContentHash...)
		genesis.ParameterBucketCurrentPointers[i] = pointer
	}
	for i := range genesis.ParameterBucketPendingPointers {
		pointer := genesis.ParameterBucketPendingPointers[i]
		body, exists := versions[newParameterBucketVersionIdentity(pointer.BucketKind, pointer.BucketKey, pointer.PendingVersion)]
		if !exists {
			return types.GenesisState{}, fmt.Errorf("parameter bucket pending pointer references a missing version")
		}
		if len(pointer.PendingContentHash) != 0 && !bytes.Equal(pointer.PendingContentHash, body.ContentHash) {
			return types.GenesisState{}, fmt.Errorf("parameter bucket pending pointer hash mismatch")
		}
		pointer.PendingContentHash = append([]byte(nil), body.ContentHash...)
		genesis.ParameterBucketPendingPointers[i] = pointer
	}
	return genesis, nil
}

func (k Keeper) initParameterBucketGenesis(ctx context.Context, genesis types.GenesisState) error {
	current := make(map[parameterBucketVersionIdentity]struct{}, len(genesis.ParameterBucketCurrentPointers))
	pending := make(map[parameterBucketVersionIdentity]struct{}, len(genesis.ParameterBucketPendingPointers))
	for _, state := range genesis.ParameterBucketVersions {
		if err := k.ParameterBucketVersion.Set(ctx, types.NewParameterBucketVersionKey(state.BucketKind, state.BucketKey, state.Version), state); err != nil {
			return err
		}
	}
	for _, pointer := range genesis.ParameterBucketCurrentPointers {
		if err := k.ParameterBucketCurrentPointer.Set(ctx, types.NewParameterBucketPointerKey(pointer.BucketKind, pointer.BucketKey), pointer); err != nil {
			return err
		}
		current[newParameterBucketVersionIdentity(pointer.BucketKind, pointer.BucketKey, pointer.CurrentVersion)] = struct{}{}
	}
	for _, pointer := range genesis.ParameterBucketPendingPointers {
		if err := k.ParameterBucketPendingPointer.Set(ctx, types.NewParameterBucketPointerKey(pointer.BucketKind, pointer.BucketKey), pointer); err != nil {
			return err
		}
		if err := k.ParameterBucketEffectiveIndex.Set(ctx, types.NewParameterBucketEffectiveIndexKey(pointer.EffectiveHeight, pointer.BucketKind, pointer.BucketKey, pointer.PendingVersion)); err != nil {
			return err
		}
		pending[newParameterBucketVersionIdentity(pointer.BucketKind, pointer.BucketKey, pointer.PendingVersion)] = struct{}{}
	}
	height := uint64(0)
	if blockHeight := sdk.UnwrapSDKContext(ctx).BlockHeight(); blockHeight > 0 {
		height = uint64(blockHeight)
	}
	for _, state := range genesis.ParameterBucketVersions {
		key := newParameterBucketVersionIdentity(state.BucketKind, state.BucketKey, state.Version)
		if _, exists := current[key]; exists {
			continue
		}
		if _, exists := pending[key]; exists {
			continue
		}
		if state.TaskRefCount == 0 {
			if err := k.scheduleParameterBucketPruneIfEligible(ctx, state, height); err != nil {
				return err
			}
		}
	}
	return nil
}

func (k Keeper) EnsureParameterBucketInvariant(ctx context.Context) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	chainID := sdk.UnwrapSDKContext(ctx).ChainID()
	versions := make(map[parameterBucketVersionIdentity]types.ParameterBucketVersionState)
	versionIter, err := k.ParameterBucketVersion.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; versionIter.Valid(); versionIter.Next() {
		key, err := versionIter.Key()
		if err != nil {
			versionIter.Close()
			return err
		}
		state, err := versionIter.Value()
		if err != nil {
			versionIter.Close()
			return err
		}
		if key.K1() != int32(state.BucketKind) || key.K2() != state.BucketKey || key.K3() != state.Version {
			versionIter.Close()
			return fmt.Errorf("parameter bucket version primary key mismatch")
		}
		if chainID == "" {
			if err := types.ValidateParameterBucketVersionStructure(state, params.Bucket); err != nil || len(state.ContentHash) != 32 {
				versionIter.Close()
				if err != nil {
					return err
				}
				return fmt.Errorf("parameter bucket content_hash must be Hash32")
			}
		} else if err := types.ValidateParameterBucketVersion(state, params.Bucket, chainID); err != nil {
			versionIter.Close()
			return err
		}
		versions[newParameterBucketVersionIdentity(state.BucketKind, state.BucketKey, state.Version)] = state
	}
	versionIter.Close()
	current := make(map[parameterBucketVersionIdentity]struct{})
	currentIter, err := k.ParameterBucketCurrentPointer.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; currentIter.Valid(); currentIter.Next() {
		key, err := currentIter.Key()
		if err != nil {
			currentIter.Close()
			return err
		}
		pointer, err := currentIter.Value()
		if err != nil {
			currentIter.Close()
			return err
		}
		if key.K1() != int32(pointer.BucketKind) || key.K2() != pointer.BucketKey {
			currentIter.Close()
			return fmt.Errorf("parameter bucket current pointer key mismatch")
		}
		versionKey := newParameterBucketVersionIdentity(pointer.BucketKind, pointer.BucketKey, pointer.CurrentVersion)
		body, exists := versions[versionKey]
		if !exists || !bytes.Equal(pointer.CurrentContentHash, body.ContentHash) {
			currentIter.Close()
			return fmt.Errorf("parameter bucket current pointer body mismatch")
		}
		current[versionKey] = struct{}{}
	}
	currentIter.Close()
	pending := make(map[parameterBucketVersionIdentity]types.ParameterBucketPendingPointerState)
	pendingIter, err := k.ParameterBucketPendingPointer.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; pendingIter.Valid(); pendingIter.Next() {
		key, err := pendingIter.Key()
		if err != nil {
			pendingIter.Close()
			return err
		}
		pointer, err := pendingIter.Value()
		if err != nil {
			pendingIter.Close()
			return err
		}
		if key.K1() != int32(pointer.BucketKind) || key.K2() != pointer.BucketKey {
			pendingIter.Close()
			return fmt.Errorf("parameter bucket pending pointer key mismatch")
		}
		versionKey := newParameterBucketVersionIdentity(pointer.BucketKind, pointer.BucketKey, pointer.PendingVersion)
		body, exists := versions[versionKey]
		if !exists || body.EffectiveHeight != pointer.EffectiveHeight || !bytes.Equal(pointer.PendingContentHash, body.ContentHash) {
			pendingIter.Close()
			return fmt.Errorf("parameter bucket pending pointer body mismatch")
		}
		indexKey := types.NewParameterBucketEffectiveIndexKey(pointer.EffectiveHeight, pointer.BucketKind, pointer.BucketKey, pointer.PendingVersion)
		has, err := k.ParameterBucketEffectiveIndex.Has(ctx, indexKey)
		if err != nil || !has {
			pendingIter.Close()
			return fmt.Errorf("parameter bucket pending pointer has no effective index")
		}
		pending[versionKey] = pointer
	}
	pendingIter.Close()
	if err := k.ensureParameterBucketEffectiveIndexInvariant(ctx, pending); err != nil {
		return err
	}
	return k.ensureParameterBucketPruneIndexInvariant(ctx, versions, current, pending)
}

func (k Keeper) ensureParameterBucketEffectiveIndexInvariant(ctx context.Context, pending map[parameterBucketVersionIdentity]types.ParameterBucketPendingPointerState) error {
	iter, err := k.ParameterBucketEffectiveIndex.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return err
		}
		versionKey := newParameterBucketVersionIdentity(shared.BucketKind(key.K2()), key.K3(), key.K4())
		pointer, exists := pending[versionKey]
		if !exists || pointer.EffectiveHeight != key.K1() {
			return fmt.Errorf("parameter bucket effective index has no matching pending pointer")
		}
	}
	return nil
}

func (k Keeper) ensureParameterBucketPruneIndexInvariant(
	ctx context.Context,
	versions map[parameterBucketVersionIdentity]types.ParameterBucketVersionState,
	current map[parameterBucketVersionIdentity]struct{},
	pending map[parameterBucketVersionIdentity]types.ParameterBucketPendingPointerState,
) error {
	indexed := make(map[parameterBucketVersionIdentity]struct{})
	iter, err := k.ParameterBucketPruneIndex.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			iter.Close()
			return err
		}
		versionKey := newParameterBucketVersionIdentity(shared.BucketKind(key.K2()), key.K3(), key.K4())
		state, exists := versions[versionKey]
		if !exists || state.TaskRefCount != 0 {
			iter.Close()
			return fmt.Errorf("parameter bucket prune index references an ineligible version")
		}
		if _, exists := current[versionKey]; exists {
			iter.Close()
			return fmt.Errorf("current parameter bucket version is prune indexed")
		}
		if _, exists := pending[versionKey]; exists {
			iter.Close()
			return fmt.Errorf("pending parameter bucket version is prune indexed")
		}
		if _, duplicate := indexed[versionKey]; duplicate {
			iter.Close()
			return fmt.Errorf("parameter bucket version has duplicate prune indexes")
		}
		indexed[versionKey] = struct{}{}
	}
	iter.Close()
	for key, state := range versions {
		_, isCurrent := current[key]
		_, isPending := pending[key]
		_, hasIndex := indexed[key]
		eligible := !isCurrent && !isPending && state.TaskRefCount == 0
		if eligible != hasIndex {
			return fmt.Errorf("parameter bucket prune index predicate mismatch")
		}
	}
	return nil
}

type parameterBucketVersionIdentity struct {
	bucketKind shared.BucketKind
	bucketKey  string
	version    uint64
}

func newParameterBucketVersionIdentity(kind shared.BucketKind, bucketKey string, version uint64) parameterBucketVersionIdentity {
	return parameterBucketVersionIdentity{bucketKind: kind, bucketKey: bucketKey, version: version}
}
