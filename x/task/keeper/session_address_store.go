package keeper

import (
	"bytes"
	"context"
	"fmt"

	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) sessionAddressToStore(field, value string) ([]byte, error) {
	raw, _, err := k.canonicalAddress(field, value)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), raw...), nil
}

func (k Keeper) sessionAddressFromStore(field string, raw []byte) (string, error) {
	if len(raw) != taskAccountAddressBytes {
		return "", fmt.Errorf("stored %s must be %d bytes", field, taskAccountAddressBytes)
	}
	address, err := k.addressCodec.BytesToString(raw)
	if err != nil {
		return "", fmt.Errorf("encode stored %s: %w", field, err)
	}
	return address, nil
}

func (k Keeper) sessionNonceToStore(state types.SessionNonceState) (internaltypes.SessionNonceStoreState, error) {
	address, err := k.sessionAddressToStore("session nonce user", state.UserAddress)
	if err != nil {
		return internaltypes.SessionNonceStoreState{}, err
	}
	return internaltypes.SessionNonceStoreState{UserAddress: address, NextSessionNonce: state.NextSessionNonce}, nil
}

func (k Keeper) ProjectSessionNonceStore(stored internaltypes.SessionNonceStoreState) (types.SessionNonceState, error) {
	address, err := k.sessionAddressFromStore("session nonce user", stored.UserAddress)
	if err != nil {
		return types.SessionNonceState{}, err
	}
	return types.SessionNonceState{UserAddress: address, NextSessionNonce: stored.NextSessionNonce}, nil
}

func (k Keeper) ReadSessionNonce(ctx context.Context, user string) (types.SessionNonceState, error) {
	stored, err := k.SessionNonce.Get(ctx, user)
	if err != nil {
		return types.SessionNonceState{}, err
	}
	state, err := k.ProjectSessionNonceStore(stored)
	if err != nil {
		return types.SessionNonceState{}, err
	}
	if state.UserAddress != user {
		return types.SessionNonceState{}, fmt.Errorf("session nonce key/address mismatch")
	}
	return state, nil
}

func (k Keeper) WriteSessionNonce(ctx context.Context, user string, state types.SessionNonceState) error {
	stored, err := k.sessionNonceToStore(state)
	if err != nil {
		return err
	}
	if state.UserAddress != user {
		return fmt.Errorf("session nonce key/address mismatch")
	}
	return k.SessionNonce.Set(ctx, user, stored)
}

func (k Keeper) exportSessionNonces(ctx context.Context) ([]types.SessionNonceState, error) {
	iter, err := k.SessionNonce.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.SessionNonceState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		state, err := k.ProjectSessionNonceStore(entry.Value)
		if err != nil {
			return nil, err
		}
		if entry.Key != state.UserAddress {
			return nil, fmt.Errorf("session nonce key/address mismatch")
		}
		states = append(states, state)
	}
	return states, nil
}

func setUniqueSessionNonce(ctx context.Context, k Keeper, state types.SessionNonceState) error {
	has, err := k.SessionNonce.Has(ctx, state.UserAddress)
	if err != nil {
		return err
	}
	if has {
		return fmt.Errorf("duplicate session nonce")
	}
	return k.WriteSessionNonce(ctx, state.UserAddress, state)
}

func (k Keeper) streamToStore(state types.StreamState) (internaltypes.StreamStoreState, error) {
	address, err := k.sessionAddressToStore("stream owner", state.OwnerUserAddress)
	if err != nil {
		return internaltypes.StreamStoreState{}, err
	}
	return internaltypes.StreamStoreState{
		SessionId: append([]byte(nil), state.SessionId...), OwnerUserAddress: address,
		NextExpectedSequence: state.NextExpectedSequence, LastActiveHeight: state.LastActiveHeight,
		OpenPendingCount: state.OpenPendingCount, Status: int32(state.Status),
	}, nil
}

func (k Keeper) ProjectStreamStore(stored internaltypes.StreamStoreState) (types.StreamState, error) {
	address, err := k.sessionAddressFromStore("stream owner", stored.OwnerUserAddress)
	if err != nil {
		return types.StreamState{}, err
	}
	return types.StreamState{
		SessionId: append([]byte(nil), stored.SessionId...), OwnerUserAddress: address,
		NextExpectedSequence: stored.NextExpectedSequence, LastActiveHeight: stored.LastActiveHeight,
		OpenPendingCount: stored.OpenPendingCount, Status: types.SessionStatus(stored.Status),
	}, nil
}

func (k Keeper) ReadStream(ctx context.Context, key types.SessionKey) (types.StreamState, error) {
	stored, err := k.Stream.Get(ctx, key)
	if err != nil {
		return types.StreamState{}, err
	}
	if !bytes.Equal(stored.SessionId, key) {
		return types.StreamState{}, fmt.Errorf("stream key/session mismatch")
	}
	return k.ProjectStreamStore(stored)
}

func (k Keeper) WriteStream(ctx context.Context, key types.SessionKey, state types.StreamState) error {
	if !bytes.Equal(state.SessionId, key) {
		return fmt.Errorf("stream key/session mismatch")
	}
	stored, err := k.streamToStore(state)
	if err != nil {
		return err
	}
	return k.Stream.Set(ctx, key, stored)
}

func (k Keeper) exportStreams(ctx context.Context) ([]types.StreamState, error) {
	iter, err := k.Stream.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.StreamState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(entry.Key, entry.Value.SessionId) {
			return nil, fmt.Errorf("stream key/session mismatch")
		}
		state, err := k.ProjectStreamStore(entry.Value)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}

func setUniqueStream(ctx context.Context, k Keeper, key types.SessionKey, state types.StreamState) error {
	has, err := k.Stream.Has(ctx, key)
	if err != nil {
		return err
	}
	if has {
		return fmt.Errorf("duplicate stream")
	}
	return k.WriteStream(ctx, key, state)
}

func (k Keeper) terminalSummaryToStore(state types.SessionTerminalSummaryState) (internaltypes.SessionTerminalSummaryStoreState, error) {
	address, err := k.sessionAddressToStore("terminal session owner", state.OwnerUserAddress)
	if err != nil {
		return internaltypes.SessionTerminalSummaryStoreState{}, err
	}
	return internaltypes.SessionTerminalSummaryStoreState{
		SessionId: append([]byte(nil), state.SessionId...), OwnerUserAddress: address,
		FinalNextExpectedSequence: state.FinalNextExpectedSequence,
		SequenceCount:             state.SequenceCount, SequenceRoot: append([]byte(nil), state.SequenceRoot...),
		ConsumedCount: state.ConsumedCount, CancelledCount: state.CancelledCount,
		RefundedCount: state.RefundedCount, SettledCount: state.SettledCount,
		ClosedHeight: state.ClosedHeight, CompactedHeight: state.CompactedHeight,
	}, nil
}

func (k Keeper) ProjectTerminalSummaryStore(stored internaltypes.SessionTerminalSummaryStoreState) (types.SessionTerminalSummaryState, error) {
	address, err := k.sessionAddressFromStore("terminal session owner", stored.OwnerUserAddress)
	if err != nil {
		return types.SessionTerminalSummaryState{}, err
	}
	return types.SessionTerminalSummaryState{
		SessionId: append([]byte(nil), stored.SessionId...), OwnerUserAddress: address,
		FinalNextExpectedSequence: stored.FinalNextExpectedSequence,
		SequenceCount:             stored.SequenceCount, SequenceRoot: append([]byte(nil), stored.SequenceRoot...),
		ConsumedCount: stored.ConsumedCount, CancelledCount: stored.CancelledCount,
		RefundedCount: stored.RefundedCount, SettledCount: stored.SettledCount,
		ClosedHeight: stored.ClosedHeight, CompactedHeight: stored.CompactedHeight,
	}, nil
}

func (k Keeper) ReadSessionTerminalSummary(ctx context.Context, key types.SessionKey) (types.SessionTerminalSummaryState, error) {
	stored, err := k.SessionTerminalSummary.Get(ctx, key)
	if err != nil {
		return types.SessionTerminalSummaryState{}, err
	}
	if !bytes.Equal(stored.SessionId, key) {
		return types.SessionTerminalSummaryState{}, fmt.Errorf("terminal summary key/session mismatch")
	}
	return k.ProjectTerminalSummaryStore(stored)
}

func (k Keeper) WriteSessionTerminalSummary(ctx context.Context, key types.SessionKey, state types.SessionTerminalSummaryState) error {
	if !bytes.Equal(state.SessionId, key) {
		return fmt.Errorf("terminal summary key/session mismatch")
	}
	stored, err := k.terminalSummaryToStore(state)
	if err != nil {
		return err
	}
	return k.SessionTerminalSummary.Set(ctx, key, stored)
}

func (k Keeper) exportSessionTerminalSummaries(ctx context.Context) ([]types.SessionTerminalSummaryState, error) {
	iter, err := k.SessionTerminalSummary.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	states := []types.SessionTerminalSummaryState{}
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(entry.Key, entry.Value.SessionId) {
			return nil, fmt.Errorf("terminal summary key/session mismatch")
		}
		state, err := k.ProjectTerminalSummaryStore(entry.Value)
		if err != nil {
			return nil, err
		}
		states = append(states, state)
	}
	return states, nil
}

func setUniqueSessionTerminalSummary(ctx context.Context, k Keeper, key types.SessionKey, state types.SessionTerminalSummaryState) error {
	has, err := k.SessionTerminalSummary.Has(ctx, key)
	if err != nil {
		return err
	}
	if has {
		return fmt.Errorf("duplicate session terminal summary")
	}
	return k.WriteSessionTerminalSummary(ctx, key, state)
}
