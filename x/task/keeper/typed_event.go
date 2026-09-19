package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
)

func emitTypedEvent(ctx context.Context, event proto.Message) error {
	return sdk.UnwrapSDKContext(ctx).EventManager().EmitTypedEvent(event)
}
