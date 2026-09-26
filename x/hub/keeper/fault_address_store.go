package keeper

import (
	"bytes"
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (k Keeper) builderFaultToStore(state types.BuilderFaultState) (internaltypes.BuilderFaultStoreState, error) {
	address, err := k.accountAddressToStore("Builder fault address", state.BuilderAddress, false)
	if err != nil {
		return internaltypes.BuilderFaultStoreState{}, err
	}
	return internaltypes.BuilderFaultStoreState{
		BuilderAddress:          address,
		FaultId:                 append([]byte(nil), state.FaultId...),
		FaultKind:               int32(state.FaultKind),
		FaultStatus:             int32(state.FaultStatus),
		FaultHeight:             state.FaultHeight,
		EvidenceId:              append([]byte(nil), state.EvidenceId...),
		CanonicalEvidenceDigest: append([]byte(nil), state.CanonicalEvidenceDigest...),
		ScopeId:                 append([]byte(nil), state.ScopeId...),
		PruneHeight:             state.PruneHeight,
		FrozenSlashBps:          state.FrozenSlashBps,
	}, nil
}

func (k Keeper) ProjectBuilderFaultStore(stored internaltypes.BuilderFaultStoreState) (types.BuilderFaultState, error) {
	address, err := k.accountAddressFromStore("Builder fault address", stored.BuilderAddress, false)
	if err != nil {
		return types.BuilderFaultState{}, err
	}
	return types.BuilderFaultState{
		BuilderAddress:          address,
		FaultId:                 append([]byte(nil), stored.FaultId...),
		FaultKind:               types.BuilderFaultKind(stored.FaultKind),
		FaultStatus:             types.BuilderFaultStatus(stored.FaultStatus),
		FaultHeight:             stored.FaultHeight,
		EvidenceId:              append([]byte(nil), stored.EvidenceId...),
		CanonicalEvidenceDigest: append([]byte(nil), stored.CanonicalEvidenceDigest...),
		ScopeId:                 append([]byte(nil), stored.ScopeId...),
		PruneHeight:             stored.PruneHeight,
		FrozenSlashBps:          stored.FrozenSlashBps,
	}, nil
}

func builderFaultKeyMatches(key types.BuilderFaultKeyPair, state types.BuilderFaultState) bool {
	return key.K1() == state.BuilderAddress && bytes.Equal(key.K2(), state.FaultId)
}

func (k Keeper) ReadBuilderFaultValue(ctx context.Context, key types.BuilderFaultKeyPair) (types.BuilderFaultState, error) {
	stored, err := k.BuilderFault.Get(ctx, key)
	if err != nil {
		return types.BuilderFaultState{}, err
	}
	state, err := k.ProjectBuilderFaultStore(stored)
	if err != nil {
		return types.BuilderFaultState{}, err
	}
	if !builderFaultKeyMatches(key, state) {
		return types.BuilderFaultState{}, fmt.Errorf("Builder fault key/value mismatch")
	}
	return state, nil
}

func (k Keeper) WriteBuilderFaultValue(ctx context.Context, key types.BuilderFaultKeyPair, state types.BuilderFaultState) error {
	if !builderFaultKeyMatches(key, state) {
		return fmt.Errorf("Builder fault key/value mismatch")
	}
	stored, err := k.builderFaultToStore(state)
	if err != nil {
		return err
	}
	return k.BuilderFault.Set(ctx, key, stored)
}

func (k Keeper) exportBuilderFaults(ctx context.Context) ([]types.BuilderFaultState, error) {
	iter, err := k.BuilderFault.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.BuilderFaultState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		state, err := k.ProjectBuilderFaultStore(entry.Value)
		if err != nil {
			return nil, err
		}
		if !builderFaultKeyMatches(entry.Key, state) {
			return nil, fmt.Errorf("Builder fault key/value mismatch")
		}
		states = append(states, state)
	}
	return states, nil
}

func (k Keeper) roleFaultToStore(state types.RoleFaultState) (internaltypes.RoleFaultStoreState, error) {
	address, err := k.accountAddressToStore("role fault operator", state.OperatorAddress, false)
	if err != nil {
		return internaltypes.RoleFaultStoreState{}, err
	}
	stored := internaltypes.RoleFaultStoreState{
		FaultId:              append([]byte(nil), state.FaultId...),
		TaskId:               append([]byte(nil), state.TaskId...),
		OperatorAddress:      address,
		Duty:                 int32(state.Duty),
		FaultClass:           int32(state.FaultClass),
		ClassificationSource: int32(state.ClassificationSource),
		EvidenceDigest:       append([]byte(nil), state.EvidenceDigest...),
		JailDelta:            state.JailDelta,
		RecordedHeight:       state.RecordedHeight,
		Status:               int32(state.Status),
	}
	if state.XSlashSummaryId != nil {
		stored.XSlashSummaryId = &internaltypes.RoleFaultStoreState_SlashSummaryId{SlashSummaryId: append([]byte(nil), state.GetSlashSummaryId()...)}
	}
	return stored, nil
}

func (k Keeper) ProjectRoleFaultStore(stored internaltypes.RoleFaultStoreState) (types.RoleFaultState, error) {
	address, err := k.accountAddressFromStore("role fault operator", stored.OperatorAddress, false)
	if err != nil {
		return types.RoleFaultState{}, err
	}
	state := types.RoleFaultState{
		FaultId:              append([]byte(nil), stored.FaultId...),
		TaskId:               append([]byte(nil), stored.TaskId...),
		OperatorAddress:      address,
		Duty:                 shared.Duty(stored.Duty),
		FaultClass:           types.FaultKind(stored.FaultClass),
		ClassificationSource: shared.FailureClassificationSource(stored.ClassificationSource),
		EvidenceDigest:       append([]byte(nil), stored.EvidenceDigest...),
		JailDelta:            stored.JailDelta,
		RecordedHeight:       stored.RecordedHeight,
		Status:               types.RoleFaultStatus(stored.Status),
	}
	if stored.XSlashSummaryId != nil {
		state.XSlashSummaryId = &types.RoleFaultState_SlashSummaryId{SlashSummaryId: append([]byte(nil), stored.GetSlashSummaryId()...)}
	}
	return state, nil
}

func (k Keeper) ReadRoleFaultValue(ctx context.Context, key shared.Hash32Key) (types.RoleFaultState, error) {
	stored, err := k.RoleFault.Get(ctx, key)
	if err != nil {
		return types.RoleFaultState{}, err
	}
	if !bytes.Equal(key, stored.FaultId) {
		return types.RoleFaultState{}, fmt.Errorf("role fault key/value mismatch")
	}
	return k.ProjectRoleFaultStore(stored)
}

func (k Keeper) WriteRoleFaultValue(ctx context.Context, key shared.Hash32Key, state types.RoleFaultState) error {
	if !bytes.Equal(key, state.FaultId) {
		return fmt.Errorf("role fault key/value mismatch")
	}
	stored, err := k.roleFaultToStore(state)
	if err != nil {
		return err
	}
	return k.RoleFault.Set(ctx, key, stored)
}

func (k Keeper) exportRoleFaults(ctx context.Context) ([]types.RoleFaultState, error) {
	iter, err := k.RoleFault.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.RoleFaultState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(entry.Key, entry.Value.FaultId) {
			return nil, fmt.Errorf("role fault key/value mismatch")
		}
		state, err := k.ProjectRoleFaultStore(entry.Value)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}
