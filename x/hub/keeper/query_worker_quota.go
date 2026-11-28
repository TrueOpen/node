package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/hub/types"
)

// WorkerQuota reports a Worker's order quota for a settlement period. No quota
// is enforced by this chain version, so the answer carries the bond's pause
// flags and enforced = false.
func (q queryServer) WorkerQuota(ctx context.Context, req *types.QueryWorkerQuotaRequest) (*types.QueryWorkerQuotaResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	_, operatorAddress, err := q.k.requireCanonicalAddress("operator_address", req.OperatorAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	bond, err := q.k.ReadServiceBondValue(ctx, operatorAddress)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "service bond not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "service bond unavailable")
	}
	return &types.QueryWorkerQuotaResponse{Period: req.Period, DispatchPaused: bond.DispatchPaused, PauseReasons: bond.PauseReasons}, nil
}
