package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/hub/types"
)

// WorkerQuota reports a Worker's order quota for a settlement period: the
// quota at the current effective bond, what the period's tasks have reserved
// and settled, and what is left. enforced is false when no coefficient or no
// settlement period is configured; the pause flags mirror the bond.
func (q queryServer) WorkerQuota(ctx context.Context, req *types.QueryWorkerQuotaRequest) (*types.QueryWorkerQuotaResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	_, operatorAddress, err := q.k.requireCanonicalAddress("operator_address", req.OperatorAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	answer, err := q.k.workerQuotaAnswer(ctx, operatorAddress, req.Period)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "service bond not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "worker quota unavailable")
	}
	return answer, nil
}
