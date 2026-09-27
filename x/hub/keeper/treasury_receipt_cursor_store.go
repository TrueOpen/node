package keeper

import (
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (k Keeper) treasurySpendReceiptToStore(state types.TreasurySpendReceiptState) (internaltypes.TreasurySpendReceiptStoreState, error) {
	address, _, err := k.requireCanonicalAddress("treasury spend recipient", state.Recipient)
	if err != nil {
		return internaltypes.TreasurySpendReceiptStoreState{}, err
	}
	if _, err := shared.ParseAmount(state.Amount); err != nil {
		return internaltypes.TreasurySpendReceiptStoreState{}, err
	}
	return internaltypes.TreasurySpendReceiptStoreState{
		ProposalId: state.ProposalId, ItemIndex: state.ItemIndex,
		ExecutedHeight: state.ExecutedHeight, PruneHeight: state.PruneHeight,
		ActionDigest:         append([]byte(nil), state.ActionDigest...),
		TreasuryVersionAfter: state.TreasuryVersionAfter,
		Recipient:            append([]byte(nil), address...),
		AmountAtomicUnits:    state.Amount.AtomicUnits,
		RewardEpoch:          state.RewardEpoch, PurposeCode: int32(state.PurposeCode),
	}, nil
}

func (k Keeper) treasurySpendReceiptStorePublicProjection(stored internaltypes.TreasurySpendReceiptStoreState) (types.TreasurySpendReceiptState, error) {
	if len(stored.Recipient) != accountAddressBytes {
		return types.TreasurySpendReceiptState{}, fmt.Errorf("stored treasury spend recipient must be %d bytes", accountAddressBytes)
	}
	address, err := k.addressCodec.BytesToString(stored.Recipient)
	if err != nil {
		return types.TreasurySpendReceiptState{}, fmt.Errorf("encode stored treasury spend recipient: %w", err)
	}
	amount := shared.Amount{AtomicUnits: stored.AmountAtomicUnits}
	if _, err := shared.ParseAmount(amount); err != nil {
		return types.TreasurySpendReceiptState{}, fmt.Errorf("stored treasury spend amount: %w", err)
	}
	return types.TreasurySpendReceiptState{
		ProposalId: stored.ProposalId, ItemIndex: stored.ItemIndex,
		ExecutedHeight: stored.ExecutedHeight, PruneHeight: stored.PruneHeight,
		ActionDigest:         append([]byte(nil), stored.ActionDigest...),
		TreasuryVersionAfter: stored.TreasuryVersionAfter,
		Recipient:            address, Amount: amount, RewardEpoch: stored.RewardEpoch,
		PurposeCode: types.TreasuryPurposeCode(stored.PurposeCode),
	}, nil
}

func (k Keeper) getTreasurySpendReceipt(ctx context.Context, key types.TreasurySpendReceiptKeyPair) (types.TreasurySpendReceiptState, error) {
	stored, err := k.TreasurySpendReceipt.Get(ctx, key)
	if err != nil {
		return types.TreasurySpendReceiptState{}, err
	}
	if stored.ProposalId != key.K1() || stored.ItemIndex != key.K2() {
		return types.TreasurySpendReceiptState{}, fmt.Errorf("stored treasury spend receipt key mismatch")
	}
	return k.treasurySpendReceiptStorePublicProjection(stored)
}

func (k Keeper) storeTreasurySpendReceipt(ctx context.Context, key types.TreasurySpendReceiptKeyPair, state types.TreasurySpendReceiptState) error {
	stored, err := k.treasurySpendReceiptToStore(state)
	if err != nil {
		return err
	}
	if stored.ProposalId != key.K1() || stored.ItemIndex != key.K2() {
		return fmt.Errorf("treasury spend receipt key mismatch")
	}
	return k.TreasurySpendReceipt.Set(ctx, key, stored)
}

func (k Keeper) exportTreasurySpendReceipts(ctx context.Context) ([]types.TreasurySpendReceiptState, error) {
	iter, err := k.TreasurySpendReceipt.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.TreasurySpendReceiptState{}
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		stored, err := iter.Value()
		if err != nil {
			return nil, err
		}
		if stored.ProposalId != key.K1() || stored.ItemIndex != key.K2() {
			return nil, fmt.Errorf("stored treasury spend receipt key mismatch")
		}
		state, err := k.treasurySpendReceiptStorePublicProjection(stored)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}

func (k Keeper) treasuryCleanupCursorToStore(state types.TreasurySpendEpochCleanupCursorState) (internaltypes.TreasurySpendEpochCleanupCursorStoreState, error) {
	stored := internaltypes.TreasurySpendEpochCleanupCursorStoreState{
		RewardEpoch: state.RewardEpoch, CleanupHeight: state.CleanupHeight,
		VisitedCount: state.VisitedCount, DeletedCount: state.DeletedCount,
	}
	if value, ok := state.XLastRecipientAddress.(*types.TreasurySpendEpochCleanupCursorState_LastRecipientAddress); ok {
		address, _, err := k.requireCanonicalAddress("treasury cleanup cursor address", value.LastRecipientAddress)
		if err != nil {
			return internaltypes.TreasurySpendEpochCleanupCursorStoreState{}, err
		}
		stored.LastRecipientAddress = append([]byte(nil), address...)
		stored.HasLastRecipientAddress = true
	} else if state.XLastRecipientAddress != nil {
		return internaltypes.TreasurySpendEpochCleanupCursorStoreState{}, fmt.Errorf("unknown treasury cleanup cursor address")
	}
	return stored, nil
}

func (k Keeper) treasuryCleanupCursorStorePublicProjection(stored internaltypes.TreasurySpendEpochCleanupCursorStoreState) (types.TreasurySpendEpochCleanupCursorState, error) {
	state := types.TreasurySpendEpochCleanupCursorState{
		RewardEpoch: stored.RewardEpoch, CleanupHeight: stored.CleanupHeight,
		VisitedCount: stored.VisitedCount, DeletedCount: stored.DeletedCount,
	}
	if stored.HasLastRecipientAddress {
		if len(stored.LastRecipientAddress) != accountAddressBytes {
			return types.TreasurySpendEpochCleanupCursorState{}, fmt.Errorf("stored treasury cleanup cursor address must be %d bytes", accountAddressBytes)
		}
		address, err := k.addressCodec.BytesToString(stored.LastRecipientAddress)
		if err != nil {
			return types.TreasurySpendEpochCleanupCursorState{}, fmt.Errorf("encode stored treasury cleanup cursor address: %w", err)
		}
		state.XLastRecipientAddress = &types.TreasurySpendEpochCleanupCursorState_LastRecipientAddress{LastRecipientAddress: address}
	} else if len(stored.LastRecipientAddress) != 0 {
		return types.TreasurySpendEpochCleanupCursorState{}, fmt.Errorf("stored treasury cleanup cursor has unmarked address")
	}
	return state, nil
}

func (k Keeper) getTreasuryCleanupCursor(ctx context.Context, epoch uint64) (types.TreasurySpendEpochCleanupCursorState, error) {
	stored, err := k.TreasurySpendEpochCleanupCursor.Get(ctx, epoch)
	if err != nil {
		return types.TreasurySpendEpochCleanupCursorState{}, err
	}
	if stored.RewardEpoch != epoch {
		return types.TreasurySpendEpochCleanupCursorState{}, fmt.Errorf("stored treasury cleanup cursor key/epoch mismatch")
	}
	return k.treasuryCleanupCursorStorePublicProjection(stored)
}

func (k Keeper) storeTreasuryCleanupCursor(ctx context.Context, state types.TreasurySpendEpochCleanupCursorState) error {
	stored, err := k.treasuryCleanupCursorToStore(state)
	if err != nil {
		return err
	}
	return k.TreasurySpendEpochCleanupCursor.Set(ctx, state.RewardEpoch, stored)
}

func (k Keeper) exportTreasuryCleanupCursors(ctx context.Context) ([]types.TreasurySpendEpochCleanupCursorState, error) {
	iter, err := k.TreasurySpendEpochCleanupCursor.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.TreasurySpendEpochCleanupCursorState{}
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		stored, err := iter.Value()
		if err != nil {
			return nil, err
		}
		if stored.RewardEpoch != key {
			return nil, fmt.Errorf("stored treasury cleanup cursor key/epoch mismatch")
		}
		state, err := k.treasuryCleanupCursorStorePublicProjection(stored)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}
