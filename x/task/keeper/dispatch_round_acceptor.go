package keeper

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	shared "github.com/TrueOpen/node/x/shared/types"
	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

const dispatchRoundScopeKeySize = types.Hash32Len + taskAccountAddressBytes + 3*8 + 4

func (k Keeper) dispatchRoundAcceptorKey(row types.DispatchRoundAcceptorState) ([]byte, error) {
	if err := types.ValidateDispatchRoundAcceptor(row); err != nil {
		return nil, err
	}
	builder, err := k.sessionAddressToStore("round Builder", row.BuilderAddress)
	if err != nil {
		return nil, err
	}
	if len(builder) != taskAccountAddressBytes {
		return nil, fmt.Errorf("round Builder must be raw20")
	}
	key := make([]byte, dispatchRoundScopeKeySize)
	n := copy(key, row.SessionId)
	binary.BigEndian.PutUint64(key[n:], row.OrderSequence)
	n += 8
	n += copy(key[n:], builder)
	binary.BigEndian.PutUint64(key[n:], row.BoundChangeHeight)
	n += 8
	binary.BigEndian.PutUint64(key[n:], row.VrfEpoch)
	n += 8
	binary.BigEndian.PutUint32(key[n:], row.DispatchRound)
	return key, nil
}

func (k Keeper) projectDispatchRoundAcceptor(key []byte, stored internaltypes.DispatchRoundAcceptorStoreState) (types.DispatchRoundAcceptorState, error) {
	var row types.DispatchRoundAcceptorState
	if len(key) != dispatchRoundScopeKeySize {
		return row, fmt.Errorf("invalid dispatch round scope key")
	}
	row.SessionId = bytes.Clone(key[:types.Hash32Len])
	n := types.Hash32Len
	row.OrderSequence = binary.BigEndian.Uint64(key[n:])
	n += 8
	var err error
	row.BuilderAddress, err = k.sessionAddressFromStore("round Builder", key[n:n+taskAccountAddressBytes])
	n += taskAccountAddressBytes
	if err != nil {
		return row, err
	}
	row.BoundChangeHeight = binary.BigEndian.Uint64(key[n:])
	n += 8
	row.VrfEpoch = binary.BigEndian.Uint64(key[n:])
	n += 8
	row.DispatchRound = binary.BigEndian.Uint32(key[n:])
	row.DispatchTicketHash = bytes.Clone(stored.DispatchTicketHash)
	row.RegisteredHeight = stored.RegisteredHeight
	return row, types.ValidateDispatchRoundAcceptor(row)
}

func (k Keeper) writeDispatchRoundAcceptor(ctx context.Context, row types.DispatchRoundAcceptorState) error {
	key, err := k.dispatchRoundAcceptorKey(row)
	if err != nil {
		return err
	}
	if _, err := k.DispatchRoundAcceptor.Get(ctx, key); err == nil {
		return nil
	} else if !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	params, err := k.dispatchSessionParams(ctx, row.SessionId)
	if err != nil {
		return err
	}
	prune, overflow := shared.CheckedHeightAddV1(row.RegisteredHeight, params.Dispatch.DispatchRecordRetentionBlocks)
	if overflow {
		return fmt.Errorf("round acceptor retention overflows")
	}
	if err := k.DispatchRoundAcceptor.Set(ctx, key, internaltypes.DispatchRoundAcceptorStoreState{DispatchTicketHash: bytes.Clone(row.DispatchTicketHash), RegisteredHeight: row.RegisteredHeight}); err != nil {
		return err
	}
	return k.DispatchRoundAcceptorPrune.Set(ctx, collections.Join(prune, key))
}

func (k Keeper) registerDispatchRoundAcceptors(ctx context.Context, record *types.DispatchRecordV1, height uint64) error {
	for _, round := range record.Rounds {
		for _, attempt := range round.Attempts {
			if attempt.Outcome != types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_ACCEPTED && attempt.Outcome != types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_VOIDED {
				continue
			}
			row := types.DispatchRoundAcceptorState{SessionId: record.SessionId, OrderSequence: record.OrderSequence, BuilderAddress: record.BuilderAddress, BoundChangeHeight: record.BoundChangeHeight, VrfEpoch: record.VrfEpoch, DispatchRound: round.DispatchRound, DispatchTicketHash: attempt.DispatchTicketHash, RegisteredHeight: height}
			if err := k.writeDispatchRoundAcceptor(ctx, row); err != nil {
				return err
			}
		}
	}
	return nil
}

func (k Keeper) dispatchRoundClaimable(ctx context.Context, ticket *types.DispatchTicketV1, hash []byte, height uint64) (bool, error) {
	key, err := k.dispatchRoundAcceptorKey(types.DispatchRoundAcceptorState{SessionId: ticket.SessionId, OrderSequence: ticket.OrderSequence, BuilderAddress: ticket.BuilderAddress, BoundChangeHeight: ticket.BoundChangeHeight, VrfEpoch: ticket.VrfEpoch, DispatchRound: ticket.DispatchRound, DispatchTicketHash: hash, RegisteredHeight: height})
	if err != nil {
		return false, err
	}
	stored, err := k.DispatchRoundAcceptor.Get(ctx, key)
	if errors.Is(err, collections.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := k.projectDispatchRoundAcceptor(key, stored); err != nil {
		return false, err
	}
	return bytes.Equal(stored.DispatchTicketHash, hash), nil
}
