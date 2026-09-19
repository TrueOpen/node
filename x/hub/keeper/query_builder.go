package keeper

import (
	"bytes"
	"context"
	"errors"
	"strings"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (q queryServer) BuilderSet(ctx context.Context, req *types.QueryBuilderSetRequest) (*types.QueryBuilderSetResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	var set types.BuilderSetState
	var err error
	switch selector := req.Selector.(type) {
	case *types.QueryBuilderSetRequest_Height:
		if selector.Height == 0 {
			return nil, status.Error(codes.InvalidArgument, "height must be greater than zero")
		}
		set, err = q.k.GetBuilderSetForHeight(sdkCtx, selector.Height)
	case *types.QueryBuilderSetRequest_BuilderSetId:
		if selector.BuilderSetId == "" || selector.BuilderSetId != strings.TrimSpace(selector.BuilderSetId) {
			return nil, status.Error(codes.InvalidArgument, "builder_set_id is not canonical")
		}
		set, err = q.k.GetBuilderSetByID(sdkCtx, selector.BuilderSetId)
	default:
		return nil, status.Error(codes.InvalidArgument, "exactly one builder set selector is required")
	}
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "builder set not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "builder set unavailable")
	}
	view := types.BuilderSetViewV1{
		BuilderSetVersion: set.BuilderSetVersion, BuilderSetId: set.BuilderSetId,
		BuilderSetHash: append([]byte(nil), set.BuilderSetHash...), EffectiveHeight: set.EffectiveHeight,
		ActiveBuilders: append([]string(nil), set.ActiveBuilders...), ActiveBuilderCount: set.ActiveBuilderCount,
		BodyStatus: set.BodyStatus,
	}
	if set.GetXPrunedHeight() != nil {
		view.XPrunedHeight = &types.BuilderSetViewV1_PrunedHeight{PrunedHeight: set.GetPrunedHeight()}
	}
	return &types.QueryBuilderSetResponse{Set: view}, nil
}

func (q queryServer) Builder(ctx context.Context, req *types.QueryBuilderRequest) (*types.QueryBuilderResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	_, address, err := q.k.requireCanonicalAddress("builder_address", req.BuilderAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	builder, err := q.k.Builder.Get(ctx, address)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "builder not found")
	}
	if err != nil || builder.BuilderAddress != address || builder.Validate() != nil {
		return nil, status.Error(codes.Internal, "builder state is invalid")
	}
	return &types.QueryBuilderResponse{Builder: builder}, nil
}

func (q queryServer) Builders(ctx context.Context, req *types.QueryBuildersRequest) (*types.QueryBuildersResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	scope, err := q.newRegistryPageScope(ctx, buildersRPC, req.Page, q.k.Builder.KeyCodec(), q.canonicalBuilderKey)
	if err != nil {
		return nil, err
	}
	iter, err := q.k.Builder.Iterate(ctx, scope.walkRange())
	if err != nil {
		return nil, status.Error(codes.Internal, "builder store unavailable")
	}
	defer iter.Close()
	response := &types.QueryBuildersResponse{Builders: []types.BuilderState{}}
	var pageKeys [][]byte
	more := false
	for ; iter.Valid(); iter.Next() {
		if uint32(len(response.Builders)) >= scope.limit {
			more = true
			break
		}
		key, err := iter.Key()
		if err != nil {
			return nil, status.Error(codes.Internal, "builder key unavailable")
		}
		builder, err := iter.Value()
		if err != nil || !q.validBuilderRegistryRow(builder, key) {
			return nil, status.Error(codes.Internal, "builder key disagrees with primary state")
		}
		candidate := append(response.Builders, builder)
		if (&types.QueryBuildersResponse{Builders: candidate}).Size() > scope.responseByteCap() {
			if len(response.Builders) == 0 {
				return nil, status.Error(codes.ResourceExhausted, "one builder exceeds the response byte cap")
			}
			more = true
			break
		}
		pageKey, err := encodeCollectionKey(q.k.Builder.KeyCodec(), key)
		if err != nil {
			return nil, status.Error(codes.Internal, "could not encode builder page key")
		}
		response.Builders, pageKeys = candidate, append(pageKeys, pageKey)
	}
	if !more {
		return response, nil
	}
	rows, next, err := scope.fitRowsWithToken(len(response.Builders), pageKeysAt(pageKeys), func(rows int, token []byte) int {
		return (&types.QueryBuildersResponse{Builders: response.Builders[:rows], Page: shared.QueryPageResponseV1{NextPageToken: token}}).Size()
	})
	if err != nil {
		return nil, err
	}
	response.Builders = response.Builders[:rows]
	response.Page = shared.QueryPageResponseV1{NextPageToken: next}
	return response, nil
}

func (q queryServer) canonicalBuilderKey(key string) error {
	_, canonical, err := q.k.requireCanonicalAddress("builder_address", key)
	if err != nil {
		return err
	}
	if canonical != key {
		return errors.New("builder_address is not canonically encoded")
	}
	return nil
}

func (q queryServer) validBuilderRegistryRow(builder types.BuilderState, key string) bool {
	return builder.BuilderAddress == key && q.canonicalBuilderKey(key) == nil && builder.Validate() == nil
}

func (q queryServer) Fault(ctx context.Context, req *types.QueryFaultRequest) (*types.QueryFaultResponse, error) {
	if req == nil || len(req.FaultId) != shared.Hash32KeySize {
		return nil, status.Error(codes.InvalidArgument, "fault_id must be a 32-byte hash")
	}
	fault, err := q.k.RoleFault.Get(ctx, types.NewRoleFaultKey(req.FaultId))
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "fault not found")
	}
	if err != nil || !bytes.Equal(fault.FaultId, req.FaultId) || fault.Validate() != nil {
		return nil, status.Error(codes.Internal, "fault state is invalid")
	}
	if _, canonical, err := q.k.requireCanonicalAddress("fault operator_address", fault.OperatorAddress); err != nil || canonical != fault.OperatorAddress {
		return nil, status.Error(codes.Internal, "fault operator_address is invalid")
	}
	response := &types.QueryFaultResponse{Fault: fault}
	if len(fault.GetSlashSummaryId()) != 0 {
		summary, err := q.k.SlashSummary.Get(ctx, types.NewSlashSummaryKey(types.SlashSourceKind_SLASH_SOURCE_KIND_ROLE_FAULT, fault.FaultId, 0))
		if err != nil || summary.Validate() != nil || !bytes.Equal(summary.SlashSummaryId, fault.GetSlashSummaryId()) ||
			!bytes.Equal(summary.SourceId, fault.FaultId) || !bytes.Equal(summary.GetTaskId(), fault.TaskId) ||
			summary.OperatorAddress != fault.OperatorAddress || summary.Duty != fault.Duty {
			return nil, status.Error(codes.Internal, "fault slash summary is unavailable")
		}
		response.SlashSummary = &summary
	}
	return response, nil
}
