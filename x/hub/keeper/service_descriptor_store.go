package keeper

import (
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (k Keeper) serviceDescriptorToStore(state types.ServiceDescriptorState) (internaltypes.ServiceDescriptorStoreState, error) {
	address, err := k.accountAddressToStore("service descriptor operator address", state.OperatorAddress, false)
	if err != nil {
		return internaltypes.ServiceDescriptorStoreState{}, err
	}
	endpoints := make([]*internaltypes.ServiceEndpointStoreV1, len(state.Endpoints))
	for i := range state.Endpoints {
		endpoint := state.Endpoints[i]
		stored := &internaltypes.ServiceEndpointStoreV1{
			EndpointKind: int32(endpoint.EndpointKind), Uri: endpoint.Uri,
			ProtocolVersion: endpoint.ProtocolVersion,
		}
		if value, ok := endpoint.XTlsPubkeyHash.(*types.ServiceEndpointV1_TlsPubkeyHash); ok {
			if value == nil {
				return internaltypes.ServiceDescriptorStoreState{}, fmt.Errorf("service endpoint has nil TLS hash")
			}
			stored.TlsPubkeyHash = append([]byte(nil), value.TlsPubkeyHash...)
			stored.HasTlsPubkeyHash = true
		} else if endpoint.XTlsPubkeyHash != nil {
			return internaltypes.ServiceDescriptorStoreState{}, fmt.Errorf("service endpoint has unknown TLS hash field")
		}
		endpoints[i] = stored
	}
	return internaltypes.ServiceDescriptorStoreState{
		ParticipantType: int32(state.ParticipantType), OperatorAddress: address,
		DescriptorVersion: state.DescriptorVersion, EndpointCount: state.EndpointCount,
		Endpoints: endpoints, DescriptorHash: append([]byte(nil), state.DescriptorHash...),
		UpdatedHeight: state.UpdatedHeight,
	}, nil
}

// ProjectServiceDescriptorStore returns the public descriptor without changing endpoint order.
func (k Keeper) ProjectServiceDescriptorStore(stored internaltypes.ServiceDescriptorStoreState) (types.ServiceDescriptorState, error) {
	address, err := k.accountAddressFromStore("service descriptor operator address", stored.OperatorAddress, false)
	if err != nil {
		return types.ServiceDescriptorState{}, err
	}
	endpoints := make([]types.ServiceEndpointV1, len(stored.Endpoints))
	for i, endpoint := range stored.Endpoints {
		if endpoint == nil {
			return types.ServiceDescriptorState{}, fmt.Errorf("stored service descriptor has nil endpoint")
		}
		projected := types.ServiceEndpointV1{
			EndpointKind: types.ServiceEndpointKind(endpoint.EndpointKind), Uri: endpoint.Uri,
			ProtocolVersion: endpoint.ProtocolVersion,
		}
		if endpoint.HasTlsPubkeyHash {
			projected.XTlsPubkeyHash = &types.ServiceEndpointV1_TlsPubkeyHash{TlsPubkeyHash: append([]byte(nil), endpoint.TlsPubkeyHash...)}
		} else if len(endpoint.TlsPubkeyHash) != 0 {
			return types.ServiceDescriptorState{}, fmt.Errorf("stored service endpoint has unmarked TLS hash")
		}
		endpoints[i] = projected
	}
	return types.ServiceDescriptorState{
		ParticipantType: shared.ParticipantType(stored.ParticipantType), OperatorAddress: address,
		DescriptorVersion: stored.DescriptorVersion, EndpointCount: stored.EndpointCount,
		Endpoints: endpoints, DescriptorHash: append([]byte(nil), stored.DescriptorHash...),
		UpdatedHeight: stored.UpdatedHeight,
	}, nil
}

func (k Keeper) GetServiceDescriptor(ctx context.Context, key types.ParticipantKeyPair) (types.ServiceDescriptorState, error) {
	stored, err := k.ServiceDescriptor.Get(ctx, key)
	if err != nil {
		return types.ServiceDescriptorState{}, err
	}
	state, err := k.ProjectServiceDescriptorStore(stored)
	if err != nil {
		return types.ServiceDescriptorState{}, err
	}
	if int32(state.ParticipantType) != key.K1() || state.OperatorAddress != key.K2() {
		return types.ServiceDescriptorState{}, fmt.Errorf("stored service descriptor key/address mismatch")
	}
	return state, nil
}

func (k Keeper) StoreServiceDescriptor(ctx context.Context, key types.ParticipantKeyPair, state types.ServiceDescriptorState) error {
	stored, err := k.serviceDescriptorToStore(state)
	if err != nil {
		return err
	}
	if stored.ParticipantType != key.K1() || state.OperatorAddress != key.K2() {
		return fmt.Errorf("service descriptor key/address mismatch")
	}
	return k.ServiceDescriptor.Set(ctx, key, stored)
}

func (k Keeper) exportServiceDescriptors(ctx context.Context) ([]types.ServiceDescriptorState, error) {
	iter, err := k.ServiceDescriptor.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.ServiceDescriptorState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		state, err := k.ProjectServiceDescriptorStore(entry.Value)
		if err != nil {
			return nil, err
		}
		if int32(state.ParticipantType) != entry.Key.K1() || state.OperatorAddress != entry.Key.K2() {
			return nil, fmt.Errorf("stored service descriptor key/address mismatch")
		}
		states = append(states, state)
	}
	return states, nil
}
