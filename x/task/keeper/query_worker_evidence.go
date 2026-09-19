package keeper

import (
	"bytes"
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/task/types"
)

func (q *queryServer) WorkerEvidence(ctx context.Context, req *types.QueryWorkerEvidenceRequest) (*types.QueryWorkerEvidenceResponse, error) {
	if req == nil || len(req.TaskId) != types.Hash32Len {
		return nil, status.Error(codes.InvalidArgument, "task_id must be Hash32")
	}
	_, worker, err := q.k.canonicalAddress("worker_operator_address", req.WorkerOperatorAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	kind := types.WorkerEvidenceKindV1_WORKER_EVIDENCE_KIND_V1_OUTPUT_CHUNK_EQUIVOCATION
	state, err := q.k.WorkerEvidenceReceipt.Get(ctx, types.NewWorkerEvidenceReceiptKey(types.NewTaskKey(req.TaskId), worker, kind, req.Seq))
	if err != nil {
		if errIsNotFound(err) {
			return nil, status.Error(codes.NotFound, "Worker evidence receipt not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	if state.SchemaVersion != types.WorkerEvidenceSchemaVersionV1 || !bytes.Equal(state.TaskId, req.TaskId) ||
		state.WorkerOperatorAddress != worker || state.EvidenceKind != kind || state.Seq != req.Seq ||
		len(state.AcceptedInferReceiptHash) != types.Hash32Len || len(state.EvidenceDigest) != types.Hash32Len ||
		len(state.FaultId) != types.Hash32Len || state.AcceptedHeight == 0 {
		return nil, status.Error(codes.Internal, "Worker evidence receipt is non-canonical")
	}
	return &types.QueryWorkerEvidenceResponse{Receipt: state}, nil
}
