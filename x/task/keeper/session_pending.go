package keeper

import (
	"bytes"
	"context"
	"fmt"

	"cosmossdk.io/collections"

	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) voidPendingTakeover(ctx context.Context, stream *types.StreamState, reason types.SessionTakeoverVoidReasonV1) error {
	pending := stream.GetPendingTakeover()
	if pending == nil {
		return nil
	}
	if pending.ActivationHeight == 0 {
		return fmt.Errorf("pending takeover is missing its activation height")
	}
	if err := k.SessionTakeoverIndex.Remove(ctx, collections.Join(pending.ActivationHeight, stream.SessionId)); err != nil {
		return err
	}
	stream.XPendingTakeover = nil
	return emitTypedEvent(ctx, &types.EventSessionTakeoverVoided{SessionId: bytes.Clone(stream.SessionId), CandidateBuilder: pending.CandidateBuilder, OrderSequence: pending.OrderSequence, Reason: reason})
}
