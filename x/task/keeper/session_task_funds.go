package keeper

import (
	"context"
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// finalizeSessionTaskFunds releases one budget's reservation and charges only its
// actual spend. Refunds remain in the session's existing escrow custody.
func (k Keeper) finalizeSessionTaskFunds(ctx context.Context, sessionID []byte, reservedAmount shared.Amount, refund uint64) error {
	reserved, err := shared.ParseAmount(reservedAmount)
	if err != nil || refund > reserved {
		return fmt.Errorf("invalid finalized task reservation or refund")
	}
	stream, err := k.ReadStream(ctx, types.NewSessionStoreKey(sessionID))
	if err != nil {
		return err
	}
	balance, err := shared.ParseAmount(stream.Balance)
	if err != nil {
		return err
	}
	frozen, err := shared.ParseAmount(stream.FrozenTotal)
	if err != nil || reserved > frozen || frozen > balance {
		return fmt.Errorf("session funds do not cover the finalized task reservation")
	}
	stream.Balance = shared.NewAmount(balance - (reserved - refund))
	stream.FrozenTotal = shared.NewAmount(frozen - reserved)
	return k.WriteStream(ctx, types.NewSessionStoreKey(sessionID), stream)
}
