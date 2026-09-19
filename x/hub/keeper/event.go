package keeper

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	proto "github.com/cosmos/gogoproto/proto"
)

// mustEmitHubEvent treats an unencodable event as a keeper invariant failure.
// Every caller passes a generated message containing only scalar fields.
func mustEmitHubEvent(ctx context.Context, event proto.Message) {
	if err := sdk.UnwrapSDKContext(ctx).EventManager().EmitTypedEvent(event); err != nil {
		panic(fmt.Errorf("emit typed hub event %T: %w", event, err))
	}
}
