package keeper

import (
	"bytes"
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
)

func (k Keeper) accountAddressToStore(field, value string, optional bool) ([]byte, error) {
	if optional && value == "" {
		return nil, nil
	}
	raw, _, err := k.requireCanonicalAddress(field, value)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), raw...), nil
}

func (k Keeper) accountAddressFromStore(field string, raw []byte, optional bool) (string, error) {
	if optional && len(raw) == 0 {
		return "", nil
	}
	if len(raw) != accountAddressBytes {
		return "", fmt.Errorf("stored %s must be %d bytes", field, accountAddressBytes)
	}
	address, err := k.addressCodec.BytesToString(raw)
	if err != nil {
		return "", fmt.Errorf("encode stored %s: %w", field, err)
	}
	return address, nil
}

func (k Keeper) cortexNodeStateToStore(state types.CortexNodeState) (internaltypes.CortexNodeStoreState, error) {
	operator, err := k.accountAddressToStore("Cortex operator address", state.OperatorAddress, false)
	if err != nil {
		return internaltypes.CortexNodeStoreState{}, err
	}
	service, err := k.accountAddressToStore("Cortex service address", state.CurrentServiceAddress, true)
	if err != nil {
		return internaltypes.CortexNodeStoreState{}, err
	}
	return internaltypes.CortexNodeStoreState{
		SchemaVersion: state.SchemaVersion, OperatorAddress: operator,
		CurrentServiceAddress:     service,
		CurrentServicePubkey:      append([]byte(nil), state.CurrentServicePubkey...),
		ServiceAuthorizationNonce: state.ServiceAuthorizationNonce,
		ServiceKeyStatus:          int32(state.ServiceKeyStatus),
		CurrentDescriptorVersion:  state.CurrentDescriptorVersion,
		RegisteredHeight:          state.RegisteredHeight, UpdatedHeight: state.UpdatedHeight,
		ActiveTaskLiabilityCount:       state.ActiveTaskLiabilityCount,
		PendingStageDutyCount:          state.PendingStageDutyCount,
		PendingEvidenceSubmissionCount: state.PendingEvidenceSubmissionCount,
	}, nil
}

// ProjectCortexNodeStore is the public projection of a private Cortex row.
func (k Keeper) ProjectCortexNodeStore(stored internaltypes.CortexNodeStoreState) (types.CortexNodeState, error) {
	operator, err := k.accountAddressFromStore("Cortex operator address", stored.OperatorAddress, false)
	if err != nil {
		return types.CortexNodeState{}, err
	}
	service, err := k.accountAddressFromStore("Cortex service address", stored.CurrentServiceAddress, true)
	if err != nil {
		return types.CortexNodeState{}, err
	}
	return types.CortexNodeState{
		SchemaVersion: stored.SchemaVersion, OperatorAddress: operator,
		CurrentServiceAddress:     service,
		CurrentServicePubkey:      append([]byte(nil), stored.CurrentServicePubkey...),
		ServiceAuthorizationNonce: stored.ServiceAuthorizationNonce,
		ServiceKeyStatus:          types.ServiceKeyStatus(stored.ServiceKeyStatus),
		CurrentDescriptorVersion:  stored.CurrentDescriptorVersion,
		RegisteredHeight:          stored.RegisteredHeight, UpdatedHeight: stored.UpdatedHeight,
		ActiveTaskLiabilityCount:       stored.ActiveTaskLiabilityCount,
		PendingStageDutyCount:          stored.PendingStageDutyCount,
		PendingEvidenceSubmissionCount: stored.PendingEvidenceSubmissionCount,
	}, nil
}

func (k Keeper) ReadCortexNodeStore(ctx context.Context, address string) (types.CortexNodeState, error) {
	raw, canonical, err := k.requireCanonicalAddress("Cortex operator address", address)
	if err != nil {
		return types.CortexNodeState{}, err
	}
	stored, err := k.CortexNode.Get(ctx, canonical)
	if err != nil {
		return types.CortexNodeState{}, err
	}
	if !bytes.Equal(stored.OperatorAddress, raw) {
		return types.CortexNodeState{}, fmt.Errorf("stored Cortex key/address mismatch")
	}
	return k.ProjectCortexNodeStore(stored)
}

func (k Keeper) StoreCortexNode(ctx context.Context, address string, state types.CortexNodeState) error {
	stored, err := k.cortexNodeStateToStore(state)
	if err != nil {
		return err
	}
	_, canonical, err := k.requireCanonicalAddress("Cortex operator key", address)
	if err != nil || canonical != state.OperatorAddress {
		return fmt.Errorf("Cortex key/address mismatch")
	}
	return k.CortexNode.Set(ctx, canonical, stored)
}

func (k Keeper) exportCortexNodes(ctx context.Context) ([]types.CortexNodeState, error) {
	iter, err := k.CortexNode.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.CortexNodeState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		state, err := k.ProjectCortexNodeStore(entry.Value)
		if err != nil {
			return nil, err
		}
		if entry.Key != state.OperatorAddress {
			return nil, fmt.Errorf("stored Cortex key/address mismatch")
		}
		states = append(states, state)
	}
	return states, nil
}

func (k Keeper) builderStateToStore(state types.BuilderState) (internaltypes.BuilderStoreState, error) {
	operator, err := k.accountAddressToStore("Builder operator address", state.BuilderAddress, false)
	if err != nil {
		return internaltypes.BuilderStoreState{}, err
	}
	service, err := k.accountAddressToStore("Builder service address", state.CurrentServiceAddress, true)
	if err != nil {
		return internaltypes.BuilderStoreState{}, err
	}
	return internaltypes.BuilderStoreState{
		SchemaVersion: state.SchemaVersion, BuilderAddress: operator,
		CurrentServiceAddress:          service,
		CurrentServicePubkey:           append([]byte(nil), state.CurrentServicePubkey...),
		CurrentServiceKeyStatus:        int32(state.CurrentServiceKeyStatus),
		ServiceAuthorizationNonce:      state.ServiceAuthorizationNonce,
		CurrentDescriptorVersion:       state.CurrentDescriptorVersion,
		RegisteredHeight:               state.RegisteredHeight,
		ActiveTaskLiabilityCount:       state.ActiveTaskLiabilityCount,
		PendingStageDutyCount:          state.PendingStageDutyCount,
		PendingEvidenceSubmissionCount: state.PendingEvidenceSubmissionCount,
	}, nil
}

// ProjectBuilderStore is the public projection of a private Builder row.
func (k Keeper) ProjectBuilderStore(stored internaltypes.BuilderStoreState) (types.BuilderState, error) {
	operator, err := k.accountAddressFromStore("Builder operator address", stored.BuilderAddress, false)
	if err != nil {
		return types.BuilderState{}, err
	}
	service, err := k.accountAddressFromStore("Builder service address", stored.CurrentServiceAddress, true)
	if err != nil {
		return types.BuilderState{}, err
	}
	return types.BuilderState{
		SchemaVersion: stored.SchemaVersion, BuilderAddress: operator,
		CurrentServiceAddress:          service,
		CurrentServicePubkey:           append([]byte(nil), stored.CurrentServicePubkey...),
		CurrentServiceKeyStatus:        types.ServiceKeyStatus(stored.CurrentServiceKeyStatus),
		ServiceAuthorizationNonce:      stored.ServiceAuthorizationNonce,
		CurrentDescriptorVersion:       stored.CurrentDescriptorVersion,
		RegisteredHeight:               stored.RegisteredHeight,
		ActiveTaskLiabilityCount:       stored.ActiveTaskLiabilityCount,
		PendingStageDutyCount:          stored.PendingStageDutyCount,
		PendingEvidenceSubmissionCount: stored.PendingEvidenceSubmissionCount,
	}, nil
}

func (k Keeper) GetBuilderState(ctx context.Context, address string) (types.BuilderState, error) {
	raw, canonical, err := k.requireCanonicalAddress("Builder operator address", address)
	if err != nil {
		return types.BuilderState{}, err
	}
	stored, err := k.Builder.Get(ctx, canonical)
	if err != nil {
		return types.BuilderState{}, err
	}
	if !bytes.Equal(stored.BuilderAddress, raw) {
		return types.BuilderState{}, fmt.Errorf("stored Builder key/address mismatch")
	}
	return k.ProjectBuilderStore(stored)
}

func (k Keeper) StoreBuilder(ctx context.Context, address string, state types.BuilderState) error {
	stored, err := k.builderStateToStore(state)
	if err != nil {
		return err
	}
	_, canonical, err := k.requireCanonicalAddress("Builder operator key", address)
	if err != nil || canonical != state.BuilderAddress {
		return fmt.Errorf("Builder key/address mismatch")
	}
	return k.Builder.Set(ctx, canonical, stored)
}

func (k Keeper) exportBuilders(ctx context.Context) ([]types.BuilderState, error) {
	iter, err := k.Builder.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.BuilderState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		state, err := k.ProjectBuilderStore(entry.Value)
		if err != nil {
			return nil, err
		}
		if entry.Key != state.BuilderAddress {
			return nil, fmt.Errorf("stored Builder key/address mismatch")
		}
		states = append(states, state)
	}
	return states, nil
}

func (k Keeper) serviceAddressIndexToStore(state types.CurrentServiceAddressIndexState) (internaltypes.CurrentServiceAddressIndexStoreState, error) {
	address, err := k.accountAddressToStore("service index operator address", state.OperatorAddress, false)
	if err != nil {
		return internaltypes.CurrentServiceAddressIndexStoreState{}, err
	}
	return internaltypes.CurrentServiceAddressIndexStoreState{
		OperatorAddress: address, ServiceAuthorizationNonce: state.ServiceAuthorizationNonce,
	}, nil
}

// ProjectCurrentServiceAddressIndexStore projects a private reverse-index value.
func (k Keeper) ProjectCurrentServiceAddressIndexStore(stored internaltypes.CurrentServiceAddressIndexStoreState) (types.CurrentServiceAddressIndexState, error) {
	address, err := k.accountAddressFromStore("service index operator address", stored.OperatorAddress, false)
	if err != nil {
		return types.CurrentServiceAddressIndexState{}, err
	}
	return types.CurrentServiceAddressIndexState{OperatorAddress: address, ServiceAuthorizationNonce: stored.ServiceAuthorizationNonce}, nil
}

func (k Keeper) GetCurrentServiceAddressIndex(ctx context.Context, key types.CurrentServiceAddressIndexKeyPair) (types.CurrentServiceAddressIndexState, error) {
	stored, err := k.CurrentServiceAddressIndex.Get(ctx, key)
	if err != nil {
		return types.CurrentServiceAddressIndexState{}, err
	}
	return k.ProjectCurrentServiceAddressIndexStore(stored)
}

func (k Keeper) StoreCurrentServiceAddressIndex(ctx context.Context, key types.CurrentServiceAddressIndexKeyPair, state types.CurrentServiceAddressIndexState) error {
	stored, err := k.serviceAddressIndexToStore(state)
	if err != nil {
		return err
	}
	return k.CurrentServiceAddressIndex.Set(ctx, key, stored)
}
