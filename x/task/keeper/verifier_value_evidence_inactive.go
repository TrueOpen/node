package keeper

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/task/types"
)

func (msgServer) SubmitVerifierValueEvidence(context.Context, *types.MsgSubmitVerifierValueEvidence) (*types.MsgSubmitVerifierValueEvidenceResponse, error) {
	return nil, status.Error(codes.FailedPrecondition, "ERR_NOT_ACTIVATED")
}

func (queryServer) VerifierValueEvidence(context.Context, *types.QueryVerifierValueEvidenceRequest) (*types.QueryVerifierValueEvidenceResponse, error) {
	return nil, status.Error(codes.FailedPrecondition, "ERR_NOT_ACTIVATED")
}
