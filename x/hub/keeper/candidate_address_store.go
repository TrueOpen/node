package keeper

import (
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
)

func (k Keeper) candidateSlotCurrentToStore(state types.CandidateSlotCurrentState) (internaltypes.CandidateSlotCurrentStoreState, error) {
	address, err := k.accountAddressToStore("candidate slot operator", state.OperatorAddress, true)
	if err != nil {
		return internaltypes.CandidateSlotCurrentStoreState{}, err
	}
	return internaltypes.CandidateSlotCurrentStoreState{
		SchemaVersion: state.SchemaVersion, Slot: state.Slot, SlotVersion: state.SlotVersion,
		OperatorAddress: address, Status: int32(state.Status), AllocatedEpoch: state.AllocatedEpoch,
		RetiredHeight: state.RetiredHeight, ActiveTaskRefs: state.ActiveTaskRefs,
	}, nil
}

func (k Keeper) ProjectCandidateSlotCurrentStore(stored internaltypes.CandidateSlotCurrentStoreState) (types.CandidateSlotCurrentState, error) {
	address, err := k.accountAddressFromStore("candidate slot operator", stored.OperatorAddress, true)
	if err != nil {
		return types.CandidateSlotCurrentState{}, err
	}
	return types.CandidateSlotCurrentState{
		SchemaVersion: stored.SchemaVersion, Slot: stored.Slot, SlotVersion: stored.SlotVersion,
		OperatorAddress: address, Status: types.CandidateSlotStatus(stored.Status),
		AllocatedEpoch: stored.AllocatedEpoch, RetiredHeight: stored.RetiredHeight,
		ActiveTaskRefs: stored.ActiveTaskRefs,
	}, nil
}

func (k Keeper) ReadCandidateSlotCurrent(ctx context.Context, slot uint32) (types.CandidateSlotCurrentState, error) {
	stored, err := k.CandidateSlotCurrent.Get(ctx, slot)
	if err != nil {
		return types.CandidateSlotCurrentState{}, err
	}
	if stored.Slot != slot {
		return types.CandidateSlotCurrentState{}, fmt.Errorf("candidate slot key mismatch")
	}
	return k.ProjectCandidateSlotCurrentStore(stored)
}

func (k Keeper) WriteCandidateSlotCurrent(ctx context.Context, slot uint32, state types.CandidateSlotCurrentState) error {
	stored, err := k.candidateSlotCurrentToStore(state)
	if err != nil {
		return err
	}
	if stored.Slot != slot {
		return fmt.Errorf("candidate slot key mismatch")
	}
	return k.CandidateSlotCurrent.Set(ctx, slot, stored)
}

func (k Keeper) exportCandidateSlotCurrents(ctx context.Context) ([]types.CandidateSlotCurrentState, error) {
	iter, err := k.CandidateSlotCurrent.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.CandidateSlotCurrentState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		if entry.Key != entry.Value.Slot {
			return nil, fmt.Errorf("candidate slot key mismatch")
		}
		state, err := k.ProjectCandidateSlotCurrentStore(entry.Value)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}

func (k Keeper) candidateSlotBindingToStore(state types.CandidateSlotBindingState) (internaltypes.CandidateSlotBindingStoreState, error) {
	address, err := k.accountAddressToStore("candidate binding operator", state.OperatorAddress, false)
	if err != nil {
		return internaltypes.CandidateSlotBindingStoreState{}, err
	}
	return internaltypes.CandidateSlotBindingStoreState{
		Slot: state.Slot, SlotVersion: state.SlotVersion, OperatorAddress: address,
		AllocatedEpoch: state.AllocatedEpoch, ReleasedHeight: state.ReleasedHeight,
		SnapshotRefCount: state.SnapshotRefCount, BindingHash: append([]byte(nil), state.BindingHash...),
	}, nil
}

func (k Keeper) ProjectCandidateSlotBindingStore(stored internaltypes.CandidateSlotBindingStoreState) (types.CandidateSlotBindingState, error) {
	address, err := k.accountAddressFromStore("candidate binding operator", stored.OperatorAddress, false)
	if err != nil {
		return types.CandidateSlotBindingState{}, err
	}
	return types.CandidateSlotBindingState{
		Slot: stored.Slot, SlotVersion: stored.SlotVersion, OperatorAddress: address,
		AllocatedEpoch: stored.AllocatedEpoch, ReleasedHeight: stored.ReleasedHeight,
		SnapshotRefCount: stored.SnapshotRefCount, BindingHash: append([]byte(nil), stored.BindingHash...),
	}, nil
}

func (k Keeper) ReadCandidateSlotBinding(ctx context.Context, key types.CandidateSlotBindingKeyPair) (types.CandidateSlotBindingState, error) {
	stored, err := k.CandidateSlotBinding.Get(ctx, key)
	if err != nil {
		return types.CandidateSlotBindingState{}, err
	}
	if stored.Slot != key.K1() || stored.SlotVersion != key.K2() {
		return types.CandidateSlotBindingState{}, fmt.Errorf("candidate binding key mismatch")
	}
	return k.ProjectCandidateSlotBindingStore(stored)
}

func (k Keeper) WriteCandidateSlotBinding(ctx context.Context, key types.CandidateSlotBindingKeyPair, state types.CandidateSlotBindingState) error {
	stored, err := k.candidateSlotBindingToStore(state)
	if err != nil {
		return err
	}
	if stored.Slot != key.K1() || stored.SlotVersion != key.K2() {
		return fmt.Errorf("candidate binding key mismatch")
	}
	return k.CandidateSlotBinding.Set(ctx, key, stored)
}

func (k Keeper) exportCandidateSlotBindings(ctx context.Context) ([]types.CandidateSlotBindingState, error) {
	iter, err := k.CandidateSlotBinding.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.CandidateSlotBindingState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		if entry.Value.Slot != entry.Key.K1() || entry.Value.SlotVersion != entry.Key.K2() {
			return nil, fmt.Errorf("candidate binding key mismatch")
		}
		state, err := k.ProjectCandidateSlotBindingStore(entry.Value)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}

func (k Keeper) operatorCandidateSlotToStore(state types.OperatorCandidateSlotState) (internaltypes.OperatorCandidateSlotStoreState, error) {
	address, err := k.accountAddressToStore("candidate reverse-index operator", state.OperatorAddress, false)
	if err != nil {
		return internaltypes.OperatorCandidateSlotStoreState{}, err
	}
	return internaltypes.OperatorCandidateSlotStoreState{
		OperatorAddress: address, Slot: state.Slot, SlotVersion: state.SlotVersion,
	}, nil
}

func (k Keeper) ProjectOperatorCandidateSlotStore(stored internaltypes.OperatorCandidateSlotStoreState) (types.OperatorCandidateSlotState, error) {
	address, err := k.accountAddressFromStore("candidate reverse-index operator", stored.OperatorAddress, false)
	if err != nil {
		return types.OperatorCandidateSlotState{}, err
	}
	return types.OperatorCandidateSlotState{OperatorAddress: address, Slot: stored.Slot, SlotVersion: stored.SlotVersion}, nil
}

func (k Keeper) ReadOperatorCandidateSlot(ctx context.Context, operator string) (types.OperatorCandidateSlotState, error) {
	stored, err := k.OperatorCandidateSlot.Get(ctx, operator)
	if err != nil {
		return types.OperatorCandidateSlotState{}, err
	}
	state, err := k.ProjectOperatorCandidateSlotStore(stored)
	if err != nil {
		return types.OperatorCandidateSlotState{}, err
	}
	if state.OperatorAddress != operator {
		return types.OperatorCandidateSlotState{}, fmt.Errorf("candidate reverse-index key/address mismatch")
	}
	return state, nil
}

func (k Keeper) WriteOperatorCandidateSlot(ctx context.Context, operator string, state types.OperatorCandidateSlotState) error {
	stored, err := k.operatorCandidateSlotToStore(state)
	if err != nil {
		return err
	}
	if state.OperatorAddress != operator {
		return fmt.Errorf("candidate reverse-index key/address mismatch")
	}
	return k.OperatorCandidateSlot.Set(ctx, operator, stored)
}

func (k Keeper) candidatePoolMemberToStore(state types.CandidatePoolMemberState) (internaltypes.CandidatePoolMemberStoreState, error) {
	address, err := k.accountAddressToStore("candidate pool member operator", state.OperatorAddress, false)
	if err != nil {
		return internaltypes.CandidatePoolMemberStoreState{}, err
	}
	return internaltypes.CandidatePoolMemberStoreState{
		Epoch: state.Epoch, Slot: state.Slot, SlotVersion: state.SlotVersion,
		OperatorAddress: address, BindingHash: append([]byte(nil), state.BindingHash...),
	}, nil
}

func (k Keeper) ProjectCandidatePoolMemberStore(stored internaltypes.CandidatePoolMemberStoreState) (types.CandidatePoolMemberState, error) {
	address, err := k.accountAddressFromStore("candidate pool member operator", stored.OperatorAddress, false)
	if err != nil {
		return types.CandidatePoolMemberState{}, err
	}
	return types.CandidatePoolMemberState{
		Epoch: stored.Epoch, Slot: stored.Slot, SlotVersion: stored.SlotVersion,
		OperatorAddress: address, BindingHash: append([]byte(nil), stored.BindingHash...),
	}, nil
}

func (k Keeper) ReadCandidatePoolMember(ctx context.Context, key types.CandidatePoolMemberKeyPair) (types.CandidatePoolMemberState, error) {
	stored, err := k.CandidatePoolMember.Get(ctx, key)
	if err != nil {
		return types.CandidatePoolMemberState{}, err
	}
	if stored.Epoch != key.K1() || stored.Slot != key.K2() {
		return types.CandidatePoolMemberState{}, fmt.Errorf("candidate pool member key mismatch")
	}
	return k.ProjectCandidatePoolMemberStore(stored)
}

func (k Keeper) WriteCandidatePoolMember(ctx context.Context, key types.CandidatePoolMemberKeyPair, state types.CandidatePoolMemberState) error {
	stored, err := k.candidatePoolMemberToStore(state)
	if err != nil {
		return err
	}
	if stored.Epoch != key.K1() || stored.Slot != key.K2() {
		return fmt.Errorf("candidate pool member key mismatch")
	}
	return k.CandidatePoolMember.Set(ctx, key, stored)
}

func (k Keeper) exportCandidatePoolMembers(ctx context.Context) ([]types.CandidatePoolMemberState, error) {
	iter, err := k.CandidatePoolMember.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.CandidatePoolMemberState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		if entry.Value.Epoch != entry.Key.K1() || entry.Value.Slot != entry.Key.K2() {
			return nil, fmt.Errorf("candidate pool member key mismatch")
		}
		state, err := k.ProjectCandidatePoolMemberStore(entry.Value)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}
