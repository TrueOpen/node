package keeper

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/task/types"
)

// The RPCs of late submission, batch settlement and
// the per-model brake. The wire contract carries them; this file answers each
// with a plain refusal until the change that implements it replaces the method.
// Nothing here reads or writes state, so a chain that never enables the panel
// mode behaves exactly as before.

var errAnchorPanelNotAvailable = status.Error(codes.Unimplemented, "this operation is not available on this chain version")

func (m msgServer) ReleaseAnchorBrake(context.Context, *types.MsgReleaseAnchorBrake) (*types.MsgReleaseAnchorBrakeResponse, error) {
	return nil, errAnchorPanelNotAvailable
}

func (q queryServer) WorkerBatch(context.Context, *types.QueryWorkerBatchRequest) (*types.QueryWorkerBatchResponse, error) {
	return nil, errAnchorPanelNotAvailable
}

func (q queryServer) WorkerBatches(context.Context, *types.QueryWorkerBatchesRequest) (*types.QueryWorkerBatchesResponse, error) {
	return nil, errAnchorPanelNotAvailable
}

func (q queryServer) WorkerBatchTasks(context.Context, *types.QueryWorkerBatchTasksRequest) (*types.QueryWorkerBatchTasksResponse, error) {
	return nil, errAnchorPanelNotAvailable
}

func (q queryServer) VerifierAuditStanding(context.Context, *types.QueryVerifierAuditStandingRequest) (*types.QueryVerifierAuditStandingResponse, error) {
	return nil, errAnchorPanelNotAvailable
}

func (q queryServer) AnchorBrake(context.Context, *types.QueryAnchorBrakeRequest) (*types.QueryAnchorBrakeResponse, error) {
	return nil, errAnchorPanelNotAvailable
}

func (q queryServer) WorkerLateSubmissions(context.Context, *types.QueryWorkerLateSubmissionsRequest) (*types.QueryWorkerLateSubmissionsResponse, error) {
	return nil, errAnchorPanelNotAvailable
}
