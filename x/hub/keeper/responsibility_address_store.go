package keeper

import (
	"bytes"
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (k Keeper) taskLiabilityToStore(state types.TaskLiabilityReservationState) (internaltypes.TaskLiabilityStoreState, error) {
	address, err := k.accountAddressToStore("task liability operator", state.OperatorAddress, false)
	if err != nil {
		return internaltypes.TaskLiabilityStoreState{}, err
	}
	return internaltypes.TaskLiabilityStoreState{
		SchemaVersion:           state.SchemaVersion,
		TaskId:                  append([]byte(nil), state.TaskId...),
		OperatorAddress:         address,
		Duty:                    int32(state.Duty),
		BondVersion:             state.BondVersion,
		CapabilityVersion:       state.CapabilityVersion,
		ReservedAmount:          state.ReservedAmount,
		Status:                  int32(state.Status),
		CandidatePoolSnapshotId: append([]byte(nil), state.CandidatePoolSnapshotId...),
		Slot:                    state.Slot,
		SlotVersion:             state.SlotVersion,
	}, nil
}

func (k Keeper) ProjectTaskLiabilityStore(stored internaltypes.TaskLiabilityStoreState) (types.TaskLiabilityReservationState, error) {
	address, err := k.accountAddressFromStore("task liability operator", stored.OperatorAddress, false)
	if err != nil {
		return types.TaskLiabilityReservationState{}, err
	}
	return types.TaskLiabilityReservationState{
		SchemaVersion:           stored.SchemaVersion,
		TaskId:                  append([]byte(nil), stored.TaskId...),
		OperatorAddress:         address,
		Duty:                    shared.Duty(stored.Duty),
		BondVersion:             stored.BondVersion,
		CapabilityVersion:       stored.CapabilityVersion,
		ReservedAmount:          stored.ReservedAmount,
		Status:                  types.LiabilityStatus(stored.Status),
		CandidatePoolSnapshotId: append([]byte(nil), stored.CandidatePoolSnapshotId...),
		Slot:                    stored.Slot,
		SlotVersion:             stored.SlotVersion,
	}, nil
}

func liabilityKeyMatches(key types.TaskLiabilityReservationKeyTriple, state types.TaskLiabilityReservationState) bool {
	return bytes.Equal(key.K1(), state.TaskId) && key.K2() == int32(state.Duty) && key.K3() == state.OperatorAddress
}

func (k Keeper) ReadTaskLiabilityValue(ctx context.Context, key types.TaskLiabilityReservationKeyTriple) (types.TaskLiabilityReservationState, error) {
	stored, err := k.TaskLiabilityReservation.Get(ctx, key)
	if err != nil {
		return types.TaskLiabilityReservationState{}, err
	}
	state, err := k.ProjectTaskLiabilityStore(stored)
	if err != nil {
		return types.TaskLiabilityReservationState{}, err
	}
	if !liabilityKeyMatches(key, state) {
		return types.TaskLiabilityReservationState{}, fmt.Errorf("task liability key/value mismatch")
	}
	return state, nil
}

func (k Keeper) WriteTaskLiabilityValue(ctx context.Context, key types.TaskLiabilityReservationKeyTriple, state types.TaskLiabilityReservationState) error {
	if !liabilityKeyMatches(key, state) {
		return fmt.Errorf("task liability key/value mismatch")
	}
	stored, err := k.taskLiabilityToStore(state)
	if err != nil {
		return err
	}
	return k.TaskLiabilityReservation.Set(ctx, key, stored)
}

func (k Keeper) exportTaskLiabilities(ctx context.Context) ([]types.TaskLiabilityReservationState, error) {
	iter, err := k.TaskLiabilityReservation.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.TaskLiabilityReservationState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		state, err := k.ProjectTaskLiabilityStore(entry.Value)
		if err != nil {
			return nil, err
		}
		if !liabilityKeyMatches(entry.Key, state) {
			return nil, fmt.Errorf("task liability key/value mismatch")
		}
		states = append(states, state)
	}
	return states, nil
}

func (k Keeper) serviceKeyResponsibilityToStore(state types.ServiceKeyResponsibilityState) (internaltypes.ServiceKeyResponsibilityStoreState, error) {
	address, err := k.accountAddressToStore("service key responsibility operator", state.OperatorAddress, false)
	if err != nil {
		return internaltypes.ServiceKeyResponsibilityStoreState{}, err
	}
	return internaltypes.ServiceKeyResponsibilityStoreState{
		ParticipantType:           int32(state.ParticipantType),
		OperatorAddress:           address,
		ResponsibilityId:          append([]byte(nil), state.ResponsibilityId...),
		ResponsibilityKind:        int32(state.ResponsibilityKind),
		SessionId:                 state.SessionId,
		TaskId:                    state.TaskId,
		CreatedHeight:             state.CreatedHeight,
		ServiceAuthorizationNonce: state.ServiceAuthorizationNonce,
	}, nil
}

func (k Keeper) ProjectServiceKeyResponsibilityStore(stored internaltypes.ServiceKeyResponsibilityStoreState) (types.ServiceKeyResponsibilityState, error) {
	address, err := k.accountAddressFromStore("service key responsibility operator", stored.OperatorAddress, false)
	if err != nil {
		return types.ServiceKeyResponsibilityState{}, err
	}
	return types.ServiceKeyResponsibilityState{
		ParticipantType:           shared.ParticipantType(stored.ParticipantType),
		OperatorAddress:           address,
		ResponsibilityId:          append([]byte(nil), stored.ResponsibilityId...),
		ResponsibilityKind:        types.ServiceKeyResponsibilityKind(stored.ResponsibilityKind),
		SessionId:                 stored.SessionId,
		TaskId:                    stored.TaskId,
		CreatedHeight:             stored.CreatedHeight,
		ServiceAuthorizationNonce: stored.ServiceAuthorizationNonce,
	}, nil
}

func serviceKeyResponsibilityKeyMatches(key types.ServiceKeyResponsibilityKeyTriple, state types.ServiceKeyResponsibilityState) bool {
	return key.K1() == int32(state.ParticipantType) && key.K2() == state.OperatorAddress && bytes.Equal(key.K3(), state.ResponsibilityId)
}

func (k Keeper) ReadServiceKeyResponsibilityValue(ctx context.Context, key types.ServiceKeyResponsibilityKeyTriple) (types.ServiceKeyResponsibilityState, error) {
	stored, err := k.ServiceKeyResponsibility.Get(ctx, key)
	if err != nil {
		return types.ServiceKeyResponsibilityState{}, err
	}
	state, err := k.ProjectServiceKeyResponsibilityStore(stored)
	if err != nil {
		return types.ServiceKeyResponsibilityState{}, err
	}
	if !serviceKeyResponsibilityKeyMatches(key, state) {
		return types.ServiceKeyResponsibilityState{}, fmt.Errorf("service key responsibility key/value mismatch")
	}
	return state, nil
}

func (k Keeper) WriteServiceKeyResponsibilityValue(ctx context.Context, key types.ServiceKeyResponsibilityKeyTriple, state types.ServiceKeyResponsibilityState) error {
	if !serviceKeyResponsibilityKeyMatches(key, state) {
		return fmt.Errorf("service key responsibility key/value mismatch")
	}
	stored, err := k.serviceKeyResponsibilityToStore(state)
	if err != nil {
		return err
	}
	return k.ServiceKeyResponsibility.Set(ctx, key, stored)
}

func (k Keeper) exportServiceKeyResponsibilities(ctx context.Context) ([]types.ServiceKeyResponsibilityState, error) {
	iter, err := k.ServiceKeyResponsibility.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.ServiceKeyResponsibilityState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		state, err := k.ProjectServiceKeyResponsibilityStore(entry.Value)
		if err != nil {
			return nil, err
		}
		if !serviceKeyResponsibilityKeyMatches(entry.Key, state) {
			return nil, fmt.Errorf("service key responsibility key/value mismatch")
		}
		states = append(states, state)
	}
	return states, nil
}
