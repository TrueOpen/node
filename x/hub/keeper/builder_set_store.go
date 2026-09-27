package keeper

import (
	"bytes"
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (k Keeper) builderAddressesToStore(addresses []string) ([][]byte, error) {
	result := make([][]byte, len(addresses))
	for i, address := range addresses {
		raw, _, err := k.requireCanonicalAddress("builder set member", address)
		if err != nil {
			return nil, err
		}
		if i != 0 && bytes.Compare(result[i-1], raw) >= 0 {
			return nil, fmt.Errorf("builder set members must be strictly sorted and unique")
		}
		result[i] = append([]byte(nil), raw...)
	}
	return result, nil
}

func (k Keeper) builderAddressesToPublic(addresses [][]byte) ([]string, error) {
	result := make([]string, len(addresses))
	for i, raw := range addresses {
		if len(raw) != accountAddressBytes {
			return nil, fmt.Errorf("stored builder set member must be %d bytes", accountAddressBytes)
		}
		if i != 0 && bytes.Compare(addresses[i-1], raw) >= 0 {
			return nil, fmt.Errorf("stored builder set members are not strictly sorted")
		}
		address, err := k.addressCodec.BytesToString(raw)
		if err != nil {
			return nil, fmt.Errorf("encode stored builder set member: %w", err)
		}
		result[i] = address
	}
	return result, nil
}

func (k Keeper) builderSetStateToStore(state types.BuilderSetState) (internaltypes.BuilderSetStoreState, error) {
	members, err := k.builderAddressesToStore(state.ActiveBuilders)
	if err != nil {
		return internaltypes.BuilderSetStoreState{}, err
	}
	stored := internaltypes.BuilderSetStoreState{
		BuilderSetVersion: state.BuilderSetVersion, BuilderSetId: state.BuilderSetId,
		BuilderSetHash:        append([]byte(nil), state.BuilderSetHash...),
		BuilderSetMembersHash: append([]byte(nil), state.BuilderSetMembersHash...),
		EffectiveHeight:       state.EffectiveHeight, ActiveBuilders: members,
		ActiveBuilderCount: state.ActiveBuilderCount, TaskRefCount: state.TaskRefCount,
		BodyStatus: int32(state.BodyStatus),
	}
	if value, ok := state.XSupersededHeight.(*types.BuilderSetState_SupersededHeight); ok {
		stored.HasSupersededHeight, stored.SupersededHeight = true, value.SupersededHeight
	} else if state.XSupersededHeight != nil {
		return internaltypes.BuilderSetStoreState{}, fmt.Errorf("unknown superseded height field")
	}
	if value, ok := state.XSourceProposalId.(*types.BuilderSetState_SourceProposalId); ok {
		stored.HasSourceProposalId, stored.SourceProposalId = true, value.SourceProposalId
	} else if state.XSourceProposalId != nil {
		return internaltypes.BuilderSetStoreState{}, fmt.Errorf("unknown source proposal field")
	}
	if value, ok := state.XPrunedHeight.(*types.BuilderSetState_PrunedHeight); ok {
		stored.HasPrunedHeight, stored.PrunedHeight = true, value.PrunedHeight
	} else if state.XPrunedHeight != nil {
		return internaltypes.BuilderSetStoreState{}, fmt.Errorf("unknown pruned height field")
	}
	return stored, nil
}

func (k Keeper) builderSetStorePublicProjection(stored internaltypes.BuilderSetStoreState) (types.BuilderSetState, error) {
	members, err := k.builderAddressesToPublic(stored.ActiveBuilders)
	if err != nil {
		return types.BuilderSetState{}, err
	}
	status := shared.StoredBodyStatus(stored.BodyStatus)
	if status != shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE && status != shared.StoredBodyStatus_STORED_BODY_STATUS_PRUNED {
		return types.BuilderSetState{}, fmt.Errorf("stored builder set has invalid body status")
	}
	state := types.BuilderSetState{
		BuilderSetVersion: stored.BuilderSetVersion, BuilderSetId: stored.BuilderSetId,
		BuilderSetHash:        append([]byte(nil), stored.BuilderSetHash...),
		BuilderSetMembersHash: append([]byte(nil), stored.BuilderSetMembersHash...),
		EffectiveHeight:       stored.EffectiveHeight, ActiveBuilders: members,
		ActiveBuilderCount: stored.ActiveBuilderCount, TaskRefCount: stored.TaskRefCount,
		BodyStatus: status,
	}
	if stored.HasSupersededHeight {
		state.XSupersededHeight = &types.BuilderSetState_SupersededHeight{SupersededHeight: stored.SupersededHeight}
	} else if stored.SupersededHeight != 0 {
		return types.BuilderSetState{}, fmt.Errorf("stored builder set has unmarked superseded height")
	}
	if stored.HasSourceProposalId {
		state.XSourceProposalId = &types.BuilderSetState_SourceProposalId{SourceProposalId: stored.SourceProposalId}
	} else if stored.SourceProposalId != 0 {
		return types.BuilderSetState{}, fmt.Errorf("stored builder set has unmarked source proposal")
	}
	if stored.HasPrunedHeight {
		state.XPrunedHeight = &types.BuilderSetState_PrunedHeight{PrunedHeight: stored.PrunedHeight}
	} else if stored.PrunedHeight != 0 {
		return types.BuilderSetState{}, fmt.Errorf("stored builder set has unmarked pruned height")
	}
	return state, nil
}

func (k Keeper) GetBuilderSet(ctx context.Context, version uint64) (types.BuilderSetState, error) {
	stored, err := k.BuilderSet.Get(ctx, version)
	if err != nil {
		return types.BuilderSetState{}, err
	}
	if stored.BuilderSetVersion != version {
		return types.BuilderSetState{}, fmt.Errorf("stored builder set key/version mismatch")
	}
	return k.builderSetStorePublicProjection(stored)
}

func (k Keeper) StoreBuilderSet(ctx context.Context, state types.BuilderSetState) error {
	stored, err := k.builderSetStateToStore(state)
	if err != nil {
		return err
	}
	return k.BuilderSet.Set(ctx, state.BuilderSetVersion, stored)
}

func (k Keeper) exportBuilderSets(ctx context.Context) ([]types.BuilderSetState, error) {
	iter, err := k.BuilderSet.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.BuilderSetState{}
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		stored, err := iter.Value()
		if err != nil {
			return nil, err
		}
		if stored.BuilderSetVersion != key {
			return nil, fmt.Errorf("stored builder set key/version mismatch")
		}
		state, err := k.builderSetStorePublicProjection(stored)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}

func (k Keeper) pendingBuilderSetStateToStore(state types.BuilderSetPendingReplacementState) (internaltypes.BuilderSetPendingReplacementStoreState, error) {
	members, err := k.builderAddressesToStore(state.NextActiveBuilders)
	if err != nil {
		return internaltypes.BuilderSetPendingReplacementStoreState{}, err
	}
	return internaltypes.BuilderSetPendingReplacementStoreState{
		ProposalId: state.ProposalId, ActionDigest: append([]byte(nil), state.ActionDigest...),
		ExpectedCurrentVersion: state.ExpectedCurrentVersion,
		ExpectedCurrentSetHash: append([]byte(nil), state.ExpectedCurrentSetHash...),
		NextBuilderSetVersion:  state.NextBuilderSetVersion, NextBuilderSetId: state.NextBuilderSetId,
		NextBuilderSetHash:        append([]byte(nil), state.NextBuilderSetHash...),
		NextBuilderSetMembersHash: append([]byte(nil), state.NextBuilderSetMembersHash...),
		NextActiveBuilders:        members, NextActiveBuilderCount: state.NextActiveBuilderCount,
		AcceptedHeight: state.AcceptedHeight, EffectiveHeight: state.EffectiveHeight,
	}, nil
}

func (k Keeper) pendingBuilderSetStorePublicProjection(stored internaltypes.BuilderSetPendingReplacementStoreState) (types.BuilderSetPendingReplacementState, error) {
	members, err := k.builderAddressesToPublic(stored.NextActiveBuilders)
	if err != nil {
		return types.BuilderSetPendingReplacementState{}, err
	}
	return types.BuilderSetPendingReplacementState{
		ProposalId: stored.ProposalId, ActionDigest: append([]byte(nil), stored.ActionDigest...),
		ExpectedCurrentVersion: stored.ExpectedCurrentVersion,
		ExpectedCurrentSetHash: append([]byte(nil), stored.ExpectedCurrentSetHash...),
		NextBuilderSetVersion:  stored.NextBuilderSetVersion, NextBuilderSetId: stored.NextBuilderSetId,
		NextBuilderSetHash:        append([]byte(nil), stored.NextBuilderSetHash...),
		NextBuilderSetMembersHash: append([]byte(nil), stored.NextBuilderSetMembersHash...),
		NextActiveBuilders:        members, NextActiveBuilderCount: stored.NextActiveBuilderCount,
		AcceptedHeight: stored.AcceptedHeight, EffectiveHeight: stored.EffectiveHeight,
	}, nil
}

func (k Keeper) GetPendingBuilderSetReplacement(ctx context.Context) (types.BuilderSetPendingReplacementState, error) {
	stored, err := k.PendingBuilderSetReplacement.Get(ctx)
	if err != nil {
		return types.BuilderSetPendingReplacementState{}, err
	}
	return k.pendingBuilderSetStorePublicProjection(stored)
}

func (k Keeper) StorePendingBuilderSetReplacement(ctx context.Context, state types.BuilderSetPendingReplacementState) error {
	stored, err := k.pendingBuilderSetStateToStore(state)
	if err != nil {
		return err
	}
	return k.PendingBuilderSetReplacement.Set(ctx, stored)
}
