package keeper

import (
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (k Keeper) bridgeBootstrapToStore(state types.BridgeBootstrapState) (internaltypes.BridgeBootstrapStoreState, error) {
	stored := internaltypes.BridgeBootstrapStoreState{Mode: int32(state.Mode)}
	if state.XMessageId != nil {
		stored.HasMessageId = true
		stored.MessageId = append([]byte(nil), state.GetMessageId()...)
	}
	if state.XRouteId != nil {
		stored.HasRouteId = true
		stored.RouteId = append([]byte(nil), state.GetRouteId()...)
	}
	if state.XRecipient != nil {
		address, err := k.accountAddressToStore("bridge bootstrap recipient", state.GetRecipient(), false)
		if err != nil {
			return internaltypes.BridgeBootstrapStoreState{}, err
		}
		stored.HasRecipient = true
		stored.Recipient = address
	}
	if state.Amount != nil {
		stored.HasAmount = true
		stored.AmountAtomicUnits = state.Amount.AtomicUnits
	}
	if state.XFeePayer != nil {
		address, err := k.accountAddressToStore("bridge bootstrap fee payer", state.GetFeePayer(), false)
		if err != nil {
			return internaltypes.BridgeBootstrapStoreState{}, err
		}
		stored.HasFeePayer = true
		stored.FeePayer = address
	}
	if state.XMaxGas != nil {
		stored.HasMaxGas = true
		stored.MaxGas = state.GetMaxGas()
	}
	if state.XConsumedHeight != nil {
		stored.HasConsumedHeight = true
		stored.ConsumedHeight = state.GetConsumedHeight()
	}
	return stored, nil
}

func (k Keeper) ProjectBridgeBootstrapStore(stored internaltypes.BridgeBootstrapStoreState) (types.BridgeBootstrapState, error) {
	if !stored.HasMessageId && len(stored.MessageId) != 0 || !stored.HasRouteId && len(stored.RouteId) != 0 ||
		!stored.HasRecipient && len(stored.Recipient) != 0 || !stored.HasAmount && stored.AmountAtomicUnits != "" ||
		!stored.HasFeePayer && len(stored.FeePayer) != 0 || !stored.HasMaxGas && stored.MaxGas != 0 ||
		!stored.HasConsumedHeight && stored.ConsumedHeight != 0 {
		return types.BridgeBootstrapState{}, fmt.Errorf("stored bridge bootstrap has an absent field body")
	}
	if _, ok := types.BridgeBootstrapModeV1_name[stored.Mode]; !ok {
		return types.BridgeBootstrapState{}, fmt.Errorf("stored bridge bootstrap mode is invalid")
	}
	state := types.BridgeBootstrapState{Mode: types.BridgeBootstrapModeV1(stored.Mode)}
	if stored.HasMessageId {
		state.XMessageId = &types.BridgeBootstrapState_MessageId{MessageId: append([]byte(nil), stored.MessageId...)}
	}
	if stored.HasRouteId {
		state.XRouteId = &types.BridgeBootstrapState_RouteId{RouteId: append([]byte(nil), stored.RouteId...)}
	}
	if stored.HasRecipient {
		address, err := k.accountAddressFromStore("bridge bootstrap recipient", stored.Recipient, false)
		if err != nil {
			return types.BridgeBootstrapState{}, err
		}
		state.XRecipient = &types.BridgeBootstrapState_Recipient{Recipient: address}
	}
	if stored.HasAmount {
		amount := shared.Amount{AtomicUnits: stored.AmountAtomicUnits}
		if _, err := shared.ParseAmount(amount); err != nil {
			return types.BridgeBootstrapState{}, fmt.Errorf("stored bridge bootstrap amount: %w", err)
		}
		state.Amount = &amount
	}
	if stored.HasFeePayer {
		address, err := k.accountAddressFromStore("bridge bootstrap fee payer", stored.FeePayer, false)
		if err != nil {
			return types.BridgeBootstrapState{}, err
		}
		state.XFeePayer = &types.BridgeBootstrapState_FeePayer{FeePayer: address}
	}
	if stored.HasMaxGas {
		state.XMaxGas = &types.BridgeBootstrapState_MaxGas{MaxGas: stored.MaxGas}
	}
	if stored.HasConsumedHeight {
		state.XConsumedHeight = &types.BridgeBootstrapState_ConsumedHeight{ConsumedHeight: stored.ConsumedHeight}
	}
	return state, nil
}

func (k Keeper) ReadBridgeBootstrapValue(ctx context.Context) (types.BridgeBootstrapState, error) {
	stored, err := k.BridgeBootstrap.Get(ctx)
	if err != nil {
		return types.BridgeBootstrapState{}, err
	}
	return k.ProjectBridgeBootstrapStore(stored)
}

func (k Keeper) WriteBridgeBootstrapValue(ctx context.Context, state types.BridgeBootstrapState) error {
	stored, err := k.bridgeBootstrapToStore(state)
	if err != nil {
		return err
	}
	return k.BridgeBootstrap.Set(ctx, stored)
}
