package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (k Keeper) RegisterBuilder(ctx context.Context, address string, height uint64) (types.BuilderState, error) {
	address, err := k.normalizeBuilderAddress(address)
	if err != nil {
		return types.BuilderState{}, err
	}
	if height == 0 {
		return types.BuilderState{}, fmt.Errorf("builder registration height must be positive")
	}
	if existing, err := k.GetBuilderState(ctx, address); err == nil {
		return existing, nil
	} else if !errors.Is(err, collections.ErrNotFound) {
		return types.BuilderState{}, err
	}
	return types.BuilderState{SchemaVersion: 1, BuilderAddress: address, RegisteredHeight: height}, nil
}

func (k Keeper) GetBuilder(ctx context.Context, address string) (types.BuilderState, error) {
	state, found, err := k.loadBuilder(ctx, address)
	if err != nil {
		return types.BuilderState{}, err
	}
	if !found {
		return types.BuilderState{}, fmt.Errorf("builder %s not found", address)
	}
	if err := state.Validate(); err != nil {
		return types.BuilderState{}, fmt.Errorf("builder %s is invalid: %w", address, err)
	}
	return state, nil
}

func (k Keeper) loadBuilder(ctx context.Context, address string) (types.BuilderState, bool, error) {
	canonical, err := k.normalizeBuilderAddress(address)
	if err != nil {
		return types.BuilderState{}, false, err
	}
	state, err := k.GetBuilderState(ctx, canonical)
	if errors.Is(err, collections.ErrNotFound) {
		return types.BuilderState{}, false, nil
	}
	return state, err == nil, err
}

func (k Keeper) normalizeBuilderAddress(address string) (string, error) {
	_, canonical, err := k.requireCanonicalAddress("builder_address", address)
	return canonical, err
}

func isRegisteredBuilderState(state types.BuilderState) bool {
	return state.Validate() == nil && state.CurrentServiceKeyStatus == types.ServiceKeyStatusActive
}

func (k Keeper) submitBuilderEvidenceFault(ctx context.Context, evidence canonicalBuilderEvidence, height uint64) (types.BuilderFaultState, types.BuilderState, bool, error) {
	if evidence.FrozenSlashBps != 0 {
		return types.BuilderFaultState{}, types.BuilderState{}, false, fmt.Errorf("Builder slash is disabled in Phase 0")
	}
	builder, err := k.GetBuilder(ctx, evidence.BuilderOperator)
	if err != nil {
		return types.BuilderFaultState{}, types.BuilderState{}, false, err
	}
	faultKind, err := types.BuilderFaultKindForEvidence(evidence.Kind)
	if err != nil {
		return types.BuilderFaultState{}, types.BuilderState{}, false, err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return types.BuilderFaultState{}, types.BuilderState{}, false, err
	}
	pruneHeight, overflow := checkedAddHubUint64(height, params.Builder.BuilderFaultRetentionBlocks)
	if overflow {
		return types.BuilderFaultState{}, types.BuilderState{}, false, fmt.Errorf("builder fault prune height overflows")
	}
	fault := types.BuilderFaultState{
		BuilderAddress: evidence.BuilderOperator,
		FaultId:        append([]byte(nil), evidence.FaultID...), FaultKind: faultKind,
		FaultStatus: types.BuilderFaultStatus_BUILDER_FAULT_STATUS_RECORDED,
		FaultHeight: height, EvidenceId: append([]byte(nil), evidence.EvidenceID...),
		CanonicalEvidenceDigest: append([]byte(nil), evidence.CanonicalDigest...),
		ScopeId:                 append([]byte(nil), evidence.ScopeID...), PruneHeight: pruneHeight,
		FrozenSlashBps: 0,
	}
	if err := fault.Validate(); err != nil {
		return types.BuilderFaultState{}, types.BuilderState{}, false, err
	}
	key := types.NewBuilderFaultKey(fault.BuilderAddress, fault.FaultId)
	if existing, err := k.ReadBuilderFaultValue(ctx, key); err == nil {
		if builderFaultMatchesEvidence(existing, evidence) {
			return existing, builder, false, nil
		}
		return types.BuilderFaultState{}, types.BuilderState{}, false, fmt.Errorf("builder fault replay conflicts with retained facts")
	} else if !errors.Is(err, collections.ErrNotFound) {
		return types.BuilderFaultState{}, types.BuilderState{}, false, err
	}
	if err := k.WriteBuilderFaultValue(ctx, key, fault); err != nil {
		return types.BuilderFaultState{}, types.BuilderState{}, false, err
	}
	if err := k.BuilderFaultPruneIndex.Set(ctx, types.NewBuilderFaultPruneKey(pruneHeight, fault.BuilderAddress, fault.FaultId)); err != nil {
		return types.BuilderFaultState{}, types.BuilderState{}, false, err
	}
	return fault, builder, true, nil
}

func (k Keeper) builderEvidenceReplay(ctx context.Context, evidence canonicalBuilderEvidence) (types.BuilderFaultState, bool, error) {
	fault, err := k.ReadBuilderFaultValue(ctx, types.NewBuilderFaultKey(evidence.BuilderOperator, evidence.FaultID))
	if errors.Is(err, collections.ErrNotFound) {
		return types.BuilderFaultState{}, false, nil
	}
	if err != nil {
		return types.BuilderFaultState{}, false, err
	}
	if !builderFaultMatchesEvidence(fault, evidence) {
		return types.BuilderFaultState{}, false, fmt.Errorf("builder evidence replay conflicts with retained fault")
	}
	return fault, true, nil
}

func builderFaultMatchesEvidence(fault types.BuilderFaultState, evidence canonicalBuilderEvidence) bool {
	kind, err := types.BuilderFaultKindForEvidence(evidence.Kind)
	return err == nil && fault.BuilderAddress == evidence.BuilderOperator && fault.FaultKind == kind &&
		fault.FaultStatus == types.BuilderFaultStatus_BUILDER_FAULT_STATUS_RECORDED && fault.FrozenSlashBps == evidence.FrozenSlashBps &&
		bytes.Equal(fault.FaultId, evidence.FaultID) && bytes.Equal(fault.EvidenceId, evidence.EvidenceID) &&
		bytes.Equal(fault.CanonicalEvidenceDigest, evidence.CanonicalDigest) && bytes.Equal(fault.ScopeId, evidence.ScopeID)
}

func BuilderSetMembersHash(builderAddressBytes [][]byte) ([]byte, error) {
	if len(builderAddressBytes) == 0 || uint64(len(builderAddressBytes)) > math.MaxUint32 {
		return nil, fmt.Errorf("builder set members must have a uint32-sized non-empty count")
	}
	memberFrames := make([]shared.CanonicalFrameV1, len(builderAddressBytes))
	for rank, address := range builderAddressBytes {
		if len(address) == 0 {
			return nil, fmt.Errorf("builder set member %d has an empty address", rank)
		}
		memberFrames[rank] = shared.FlatCanonicalFrameV1(shared.Uint32BE(uint32(rank)), address)
	}
	repeatedMembers := shared.CanonicalRepeatedFramesV1(memberFrames)
	if err := repeatedMembers.Err(); err != nil {
		return nil, fmt.Errorf("builder set members: %w", err)
	}
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainBuilderSetMembersV1)).
		Raw(shared.Uint32BE(uint32(len(builderAddressBytes)))).Nested(repeatedMembers).Sum()
}

func BuilderSetHash(chainID string, version uint64, builderSetID string, effectiveHeight uint64, activeBuilderCount uint32, membersHash []byte) ([]byte, error) {
	if strings.TrimSpace(chainID) == "" || strings.TrimSpace(chainID) != chainID || version == 0 ||
		strings.TrimSpace(builderSetID) == "" || strings.TrimSpace(builderSetID) != builderSetID || effectiveHeight == 0 || activeBuilderCount == 0 {
		return nil, fmt.Errorf("builder set hash scope is invalid")
	}
	if len(membersHash) != shared.Hash32KeySize {
		return nil, fmt.Errorf("builder_set_members_hash must be Hash32")
	}
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainBuilderSetV1)).Raw(
		[]byte(chainID), shared.Uint64BE(version), []byte(builderSetID), shared.Uint64BE(effectiveHeight),
		shared.Uint32BE(activeBuilderCount), membersHash,
	).Sum()
}

func (k Keeper) validateBuilderSetState(ctx context.Context, state types.BuilderSetState, expectedVersion uint64) error {
	if state.BuilderSetVersion == 0 || state.BuilderSetVersion != expectedVersion || state.BuilderSetId == "" ||
		state.BuilderSetId != strings.TrimSpace(state.BuilderSetId) || state.EffectiveHeight == 0 || state.ActiveBuilderCount == 0 ||
		len(state.BuilderSetHash) != shared.Hash32KeySize || len(state.BuilderSetMembersHash) != shared.Hash32KeySize {
		return fmt.Errorf("builder set header is invalid")
	}
	switch state.BodyStatus {
	case shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE:
		if state.GetXSupersededHeight() == nil && state.GetXPrunedHeight() != nil {
			return fmt.Errorf("current builder set cannot be pruned")
		}
		if len(state.ActiveBuilders) == 0 || uint32(len(state.ActiveBuilders)) != state.ActiveBuilderCount || state.GetXPrunedHeight() != nil {
			return fmt.Errorf("active builder set body is invalid")
		}
	case shared.StoredBodyStatus_STORED_BODY_STATUS_PRUNED:
		if len(state.ActiveBuilders) != 0 || state.GetXPrunedHeight() == nil || state.TaskRefCount != 0 || state.GetXSupersededHeight() == nil {
			return fmt.Errorf("pruned builder set header is invalid")
		}
		return nil
	default:
		return fmt.Errorf("builder set body_status is invalid")
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	if state.ActiveBuilderCount < params.Builder.BuildersPerTask || state.ActiveBuilderCount > params.Builder.BuilderSetCap {
		return fmt.Errorf("builder set member count is outside configured bounds")
	}
	addresses := make([][]byte, len(state.ActiveBuilders))
	for i, address := range state.ActiveBuilders {
		raw, canonical, err := k.requireCanonicalAddress("builder set member", address)
		if err != nil || canonical != address || (i != 0 && bytes.Compare(addresses[i-1], raw) >= 0) {
			return fmt.Errorf("builder set members must be unique and strictly sorted by address bytes")
		}
		addresses[i] = raw
	}
	membersHash, err := BuilderSetMembersHash(addresses)
	if err != nil || !bytes.Equal(membersHash, state.BuilderSetMembersHash) {
		return fmt.Errorf("builder set members hash mismatch")
	}
	setHash, err := BuilderSetHash(sdk.UnwrapSDKContext(ctx).ChainID(), state.BuilderSetVersion, state.BuilderSetId, state.EffectiveHeight, state.ActiveBuilderCount, membersHash)
	if err != nil || !bytes.Equal(setHash, state.BuilderSetHash) {
		return fmt.Errorf("builder set hash mismatch")
	}
	return nil
}

func (k Keeper) GetBuilderSetByID(ctx sdk.Context, builderSetID string) (types.BuilderSetState, error) {
	version, err := k.BuilderSetByIDIndex.Get(ctx, builderSetID)
	if err != nil {
		return types.BuilderSetState{}, err
	}
	state, err := k.GetBuilderSet(ctx, version)
	if err != nil {
		return types.BuilderSetState{}, err
	}
	if state.BuilderSetId != builderSetID || k.validateBuilderSetState(ctx, state, version) != nil {
		return types.BuilderSetState{}, fmt.Errorf("builder set ID index is inconsistent")
	}
	return state, nil
}

func (k Keeper) GetBuilderSetForHeight(ctx sdk.Context, height uint64) (types.BuilderSetState, error) {
	if height == 0 {
		return types.BuilderSetState{}, fmt.Errorf("builder set height must be positive")
	}
	rng := (&collections.Range[types.BuilderSetByHeightKey]{}).
		EndInclusive(types.NewBuilderSetByHeightKey(height, math.MaxUint64)).Descending()
	iter, err := k.BuilderSetByHeightIndex.Iterate(ctx, rng)
	if err != nil {
		return types.BuilderSetState{}, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return types.BuilderSetState{}, collections.ErrNotFound
	}
	entry, err := iter.KeyValue()
	if err != nil {
		return types.BuilderSetState{}, err
	}
	state, err := k.GetBuilderSet(ctx, entry.Key.K2())
	if err != nil {
		return types.BuilderSetState{}, err
	}
	if entry.Value != state.BuilderSetId || entry.Key.K1() != state.EffectiveHeight || k.validateBuilderSetState(ctx, state, state.BuilderSetVersion) != nil {
		return types.BuilderSetState{}, fmt.Errorf("builder set height index is inconsistent")
	}
	return state, nil
}

func sdkWrappedContextHeight(ctx context.Context) uint64 {
	return sdkContextHeight(sdk.UnwrapSDKContext(ctx))
}
