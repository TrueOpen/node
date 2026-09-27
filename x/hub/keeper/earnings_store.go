package keeper

import (
	"bytes"
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

const accountAddressBytes = 20

// earningsStateToStore is the only public-to-private Earnings projection.
func (k Keeper) earningsStateToStore(state types.EarningsState) (internaltypes.EarningsStoreState, error) {
	if err := state.Validate(); err != nil {
		return internaltypes.EarningsStoreState{}, err
	}
	address, _, err := k.requireCanonicalAddress("earnings address", state.Address)
	if err != nil {
		return internaltypes.EarningsStoreState{}, err
	}
	return internaltypes.EarningsStoreState{
		Address:                append([]byte(nil), address...),
		ClaimableTaskFee:       state.ClaimableTaskFee.AtomicUnits,
		ClaimableServiceReward: state.ClaimableServiceReward.AtomicUnits,
		ClaimableBuilderReward: state.ClaimableBuilderReward.AtomicUnits,
		ClaimableAmount:        state.ClaimableAmount.AtomicUnits,
		EarningsVersion:        state.EarningsVersion,
		LastUpdatedHeight:      state.LastUpdatedHeight,
	}, nil
}

// earningsStorePublicProjection is the only private-to-public Earnings projection.
func (k Keeper) earningsStorePublicProjection(stored internaltypes.EarningsStoreState) (types.EarningsState, error) {
	if len(stored.Address) != accountAddressBytes {
		return types.EarningsState{}, fmt.Errorf("stored earnings address must be %d bytes", accountAddressBytes)
	}
	address, err := k.addressCodec.BytesToString(stored.Address)
	if err != nil {
		return types.EarningsState{}, fmt.Errorf("encode stored earnings address: %w", err)
	}
	state := types.EarningsState{
		Address:                address,
		ClaimableTaskFee:       shared.Amount{AtomicUnits: stored.ClaimableTaskFee},
		ClaimableServiceReward: shared.Amount{AtomicUnits: stored.ClaimableServiceReward},
		ClaimableBuilderReward: shared.Amount{AtomicUnits: stored.ClaimableBuilderReward},
		ClaimableAmount:        shared.Amount{AtomicUnits: stored.ClaimableAmount},
		EarningsVersion:        stored.EarningsVersion,
		LastUpdatedHeight:      stored.LastUpdatedHeight,
	}
	if err := state.Validate(); err != nil {
		return types.EarningsState{}, fmt.Errorf("stored earnings state: %w", err)
	}
	return state, nil
}

func (k Keeper) getEarnings(ctx context.Context, address string) (types.EarningsState, error) {
	key, canonical, err := k.requireCanonicalAddress("earnings address", address)
	if err != nil {
		return types.EarningsState{}, err
	}
	stored, err := k.Earnings.Get(ctx, canonical)
	if err != nil {
		return types.EarningsState{}, err
	}
	if !bytes.Equal(key, stored.Address) {
		return types.EarningsState{}, fmt.Errorf("stored earnings key/address mismatch")
	}
	return k.earningsStorePublicProjection(stored)
}

func (k Keeper) setEarnings(ctx context.Context, state types.EarningsState) error {
	stored, err := k.earningsStateToStore(state)
	if err != nil {
		return err
	}
	return k.Earnings.Set(ctx, state.Address, stored)
}

func (k Keeper) removeEarnings(ctx context.Context, address string) error {
	_, canonical, err := k.requireCanonicalAddress("earnings address", address)
	if err != nil {
		return err
	}
	return k.Earnings.Remove(ctx, canonical)
}

func (k Keeper) exportEarningsGenesis(ctx context.Context) ([]types.EarningsState, error) {
	iter, err := k.Earnings.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.EarningsState{}
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		stored, err := iter.Value()
		if err != nil {
			return nil, err
		}
		state, err := k.earningsStorePublicProjection(stored)
		if err != nil {
			return nil, err
		}
		if key != state.Address {
			return nil, fmt.Errorf("stored earnings key/address mismatch")
		}
		states = append(states, state)
	}
	return states, nil
}
