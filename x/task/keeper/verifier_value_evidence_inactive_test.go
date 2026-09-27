package keeper

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/task/types"
)

func TestVerifierValueEvidenceFailsClosedBeforeActivation(t *testing.T) {
	msg := msgServer{}
	_, err := msg.SubmitVerifierValueEvidence(context.Background(), &types.MsgSubmitVerifierValueEvidence{EvidenceBytes: []byte{0xff}})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.ErrorContains(t, err, "ERR_NOT_ACTIVATED")

	query := queryServer{}
	_, err = query.VerifierValueEvidence(context.Background(), &types.QueryVerifierValueEvidenceRequest{})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))
	require.ErrorContains(t, err, "ERR_NOT_ACTIVATED")
}
