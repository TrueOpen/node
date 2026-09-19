package keeper

import (
	"fmt"
	"strings"

	"github.com/cosmos/cosmos-sdk/types/query"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/hub/types"
)

const (
	defaultHubQueryPageLimit = uint64(100)
	maxHubQueryPageLimit     = uint64(1000)
)

type queryServer struct {
	// The generated embedding preserves forward source compatibility. A
	// descriptor-based contract test requires every registered RPC to have a
	// concrete keeper method.
	types.UnimplementedQueryServer
	k Keeper
}

// NewQueryServerImpl returns the Hub Query service implementation.
//
// The embedded types.UnimplementedQueryServer declares its fallbacks on
// *UnimplementedQueryServer, so only *queryServer satisfies types.QueryServer.
func NewQueryServerImpl(k Keeper) types.QueryServer {
	return &queryServer{k: k}
}

var _ types.QueryServer = (*queryServer)(nil)

func validateHubPageRequest(page *query.PageRequest) error {
	if page == nil {
		return nil
	}
	if page.Limit > maxHubQueryPageLimit {
		return status.Errorf(codes.InvalidArgument, "pagination limit must be <= %d", maxHubQueryPageLimit)
	}
	if page.Offset > maxHubQueryPageLimit {
		return status.Errorf(codes.InvalidArgument, "pagination offset must be <= %d; use next_key for deep pages", maxHubQueryPageLimit)
	}
	if page.CountTotal {
		return status.Error(codes.InvalidArgument, "count_total is not supported on bounded keeper queries")
	}
	if page.Offset > 0 && len(page.Key) > 0 {
		return status.Error(codes.InvalidArgument, "pagination key and offset cannot be combined")
	}
	return nil
}

func validateTaskScope(sessionID, taskID string) error {
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(taskID) == "" || strings.TrimSpace(sessionID) != sessionID || strings.TrimSpace(taskID) != taskID {
		return fmt.Errorf("session_id and task_id are required")
	}
	return nil
}

func validateModelProfileQueryScope(modelID string, profileVersion uint32) (string, uint32, error) {
	if profileVersion == 0 {
		return "", 0, fmt.Errorf("model_id and profile_version are required")
	}
	if err := types.ValidateModelID(modelID); err != nil {
		return "", 0, err
	}
	return modelID, profileVersion, nil
}
