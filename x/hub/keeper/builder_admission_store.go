package keeper

import (
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
)

func (k Keeper) builderAdmissionStateToStore(state types.BuilderAdmissionState) (internaltypes.BuilderAdmissionStoreState, error) {
	address, _, err := k.requireCanonicalAddress("builder admission address", state.BuilderAddress)
	if err != nil {
		return internaltypes.BuilderAdmissionStoreState{}, err
	}
	if state.Status != types.BuilderStatus_BUILDER_STATUS_ADMITTED && state.Status != types.BuilderStatus_BUILDER_STATUS_REVOKED {
		return internaltypes.BuilderAdmissionStoreState{}, fmt.Errorf("builder admission has invalid status")
	}
	if state.CurrentBuilderSetVersion == 0 || state.UpdatedHeight == 0 {
		return internaltypes.BuilderAdmissionStoreState{}, fmt.Errorf("builder admission has incomplete version metadata")
	}
	stored := internaltypes.BuilderAdmissionStoreState{
		BuilderAddress:           append([]byte(nil), address...),
		Status:                   int32(state.Status),
		CurrentBuilderSetVersion: state.CurrentBuilderSetVersion,
		UpdatedHeight:            state.UpdatedHeight,
	}
	switch source := state.XSourceProposalId.(type) {
	case nil:
	case *types.BuilderAdmissionState_SourceProposalId:
		if source == nil {
			return internaltypes.BuilderAdmissionStoreState{}, fmt.Errorf("builder admission has nil proposal identifier")
		}
		stored.HasSourceProposalId = true
		stored.SourceProposalId = source.SourceProposalId
	default:
		return internaltypes.BuilderAdmissionStoreState{}, fmt.Errorf("builder admission has unknown proposal identifier")
	}
	return stored, nil
}

func (k Keeper) builderAdmissionStorePublicProjection(stored internaltypes.BuilderAdmissionStoreState) (types.BuilderAdmissionState, error) {
	if len(stored.BuilderAddress) != accountAddressBytes {
		return types.BuilderAdmissionState{}, fmt.Errorf("stored builder admission address must be %d bytes", accountAddressBytes)
	}
	address, err := k.addressCodec.BytesToString(stored.BuilderAddress)
	if err != nil {
		return types.BuilderAdmissionState{}, fmt.Errorf("encode stored builder admission address: %w", err)
	}
	status := types.BuilderStatus(stored.Status)
	if status != types.BuilderStatus_BUILDER_STATUS_ADMITTED && status != types.BuilderStatus_BUILDER_STATUS_REVOKED {
		return types.BuilderAdmissionState{}, fmt.Errorf("stored builder admission has invalid status")
	}
	if stored.CurrentBuilderSetVersion == 0 || stored.UpdatedHeight == 0 {
		return types.BuilderAdmissionState{}, fmt.Errorf("stored builder admission has incomplete version metadata")
	}
	state := types.BuilderAdmissionState{
		BuilderAddress:           address,
		Status:                   status,
		CurrentBuilderSetVersion: stored.CurrentBuilderSetVersion,
		UpdatedHeight:            stored.UpdatedHeight,
	}
	if stored.HasSourceProposalId {
		state.XSourceProposalId = &types.BuilderAdmissionState_SourceProposalId{SourceProposalId: stored.SourceProposalId}
	} else if stored.SourceProposalId != 0 {
		return types.BuilderAdmissionState{}, fmt.Errorf("stored builder admission has unmarked proposal identifier")
	}
	return state, nil
}

func (k Keeper) storeBuilderAdmission(ctx context.Context, state types.BuilderAdmissionState) error {
	stored, err := k.builderAdmissionStateToStore(state)
	if err != nil {
		return err
	}
	return k.BuilderAdmission.Set(ctx, state.BuilderAddress, stored)
}

func (k Keeper) exportBuilderAdmissionGenesis(ctx context.Context) ([]types.BuilderAdmissionState, error) {
	iter, err := k.BuilderAdmission.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.BuilderAdmissionState{}
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		stored, err := iter.Value()
		if err != nil {
			return nil, err
		}
		state, err := k.builderAdmissionStorePublicProjection(stored)
		if err != nil {
			return nil, err
		}
		if key != state.BuilderAddress {
			return nil, fmt.Errorf("stored builder admission key/address mismatch")
		}
		states = append(states, state)
	}
	return states, nil
}
