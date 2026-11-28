package keeper

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/task/types"
)

// The RPCs of batch settlement and
// the per-model brake. The wire contract carries them; this file answers each
// with a plain refusal until the change that implements it replaces the method.
// Nothing here reads or writes state, so a chain that never enables the panel
// mode behaves exactly as before.

var errAnchorPanelNotAvailable = status.Error(codes.Unimplemented, "this operation is not available on this chain version")

func (m msgServer) ReleaseAnchorBrake(context.Context, *types.MsgReleaseAnchorBrake) (*types.MsgReleaseAnchorBrakeResponse, error) {
	return nil, errAnchorPanelNotAvailable
}

func (q queryServer) AnchorBrake(context.Context, *types.QueryAnchorBrakeRequest) (*types.QueryAnchorBrakeResponse, error) {
	return nil, errAnchorPanelNotAvailable
}
