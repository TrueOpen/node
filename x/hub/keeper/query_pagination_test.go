package keeper

import (
	"testing"

	"github.com/cosmos/cosmos-sdk/types/query"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestValidateHubPageRequestLimit(t *testing.T) {
	require.NoError(t, validateHubPageRequest(nil))
	require.NoError(t, validateHubPageRequest(&query.PageRequest{}))
	require.NoError(t, validateHubPageRequest(&query.PageRequest{Limit: maxHubQueryPageLimit}))
	err := validateHubPageRequest(&query.PageRequest{Limit: maxHubQueryPageLimit + 1})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	err = validateHubPageRequest(&query.PageRequest{Offset: maxHubQueryPageLimit + 1})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	err = validateHubPageRequest(&query.PageRequest{CountTotal: true})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestHubQueryScopesRejectNonCanonicalIdentifiers(t *testing.T) {
	require.Error(t, validateTaskScope(" session-1", "task-1"))
	require.Error(t, validateTaskScope("session-1", "task-1 "))
	_, _, err := validateModelProfileQueryScope(" model-a", 1)
	require.Error(t, err)
	_, _, err = validateModelProfileQueryScope("model-a", 0)
	require.Error(t, err)
}
