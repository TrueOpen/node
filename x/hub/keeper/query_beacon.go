package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	"github.com/TrueOpen/node/x/hub/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Beacon queries one BeaconState for randomness audit. It exposes source_tag,
// proof digest/proposer metadata, and verified so indexers can distinguish dev-only
// placeholder history from production proposer-VRF history.
func (q queryServer) Beacon(ctx context.Context, req *types.QueryBeaconRequest) (*types.QueryBeaconResponse, error) {
	if req == nil || req.Height == 0 {
		return nil, types.ErrInvalidBeacon
	}
	state, err := q.k.GetBeaconAtHeight(ctx, req.Height)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, status.Errorf(codes.NotFound, "beacon height %d is outside the retained window", req.Height)
		}
		return nil, err
	}
	return &types.QueryBeaconResponse{Beacon: state}, nil
}
