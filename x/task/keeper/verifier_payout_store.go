package keeper

import (
	"bytes"
	"context"
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) verifierPayoutToStore(state types.VerifierPayoutState) (internaltypes.VerifierPayoutStoreState, error) {
	address, _, err := k.canonicalAddress("verifier payout operator", state.OperatorAddress)
	if err != nil {
		return internaltypes.VerifierPayoutStoreState{}, err
	}
	return internaltypes.VerifierPayoutStoreState{
		TaskId:                 append([]byte(nil), state.TaskId...),
		SettlementId:           append([]byte(nil), state.SettlementId...),
		OperatorAddress:        append([]byte(nil), address...),
		SelectedVerifierIndex:  state.SelectedVerifierIndex,
		GrossAtomicUnits:       state.Gross.AtomicUnits,
		MaintenanceAtomicUnits: state.Maintenance.AtomicUnits,
		NetAtomicUnits:         state.Net.AtomicUnits,
	}, nil
}

func (k Keeper) ProjectVerifierPayoutStore(stored internaltypes.VerifierPayoutStoreState) (types.VerifierPayoutState, error) {
	if len(stored.OperatorAddress) != taskAccountAddressBytes {
		return types.VerifierPayoutState{}, fmt.Errorf("stored verifier payout operator must be %d bytes", taskAccountAddressBytes)
	}
	for _, amount := range []struct{ name, atomicUnits string }{
		{"gross", stored.GrossAtomicUnits},
		{"maintenance", stored.MaintenanceAtomicUnits},
		{"net", stored.NetAtomicUnits},
	} {
		if _, err := shared.ParseAmount(shared.Amount{AtomicUnits: amount.atomicUnits}); err != nil {
			return types.VerifierPayoutState{}, fmt.Errorf("stored verifier payout %s: %w", amount.name, err)
		}
	}
	address, err := k.addressCodec.BytesToString(stored.OperatorAddress)
	if err != nil {
		return types.VerifierPayoutState{}, fmt.Errorf("encode stored verifier payout operator: %w", err)
	}
	return types.VerifierPayoutState{
		TaskId:                append([]byte(nil), stored.TaskId...),
		SettlementId:          append([]byte(nil), stored.SettlementId...),
		OperatorAddress:       address,
		SelectedVerifierIndex: stored.SelectedVerifierIndex,
		Gross:                 shared.Amount{AtomicUnits: stored.GrossAtomicUnits},
		Maintenance:           shared.Amount{AtomicUnits: stored.MaintenanceAtomicUnits},
		Net:                   shared.Amount{AtomicUnits: stored.NetAtomicUnits},
	}, nil
}

func (k Keeper) ReadVerifierPayout(ctx context.Context, key types.VerifierPayoutKey) (types.VerifierPayoutState, error) {
	stored, err := k.VerifierPayout.Get(ctx, key)
	if err != nil {
		return types.VerifierPayoutState{}, err
	}
	if !bytes.Equal(key.K1(), stored.TaskId) || key.K2() != stored.SelectedVerifierIndex {
		return types.VerifierPayoutState{}, fmt.Errorf("verifier payout key/value mismatch")
	}
	return k.ProjectVerifierPayoutStore(stored)
}

func (k Keeper) WriteVerifierPayout(ctx context.Context, key types.VerifierPayoutKey, state types.VerifierPayoutState) error {
	if !bytes.Equal(key.K1(), state.TaskId) || key.K2() != state.SelectedVerifierIndex {
		return fmt.Errorf("verifier payout key/value mismatch")
	}
	stored, err := k.verifierPayoutToStore(state)
	if err != nil {
		return err
	}
	return k.VerifierPayout.Set(ctx, key, stored)
}

func (k Keeper) exportVerifierPayouts(ctx context.Context) ([]types.VerifierPayoutState, error) {
	iter, err := k.VerifierPayout.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.VerifierPayoutState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(entry.Key.K1(), entry.Value.TaskId) || entry.Key.K2() != entry.Value.SelectedVerifierIndex {
			return nil, fmt.Errorf("verifier payout key/value mismatch")
		}
		state, err := k.ProjectVerifierPayoutStore(entry.Value)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}
