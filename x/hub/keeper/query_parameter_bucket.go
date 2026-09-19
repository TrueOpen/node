package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (q queryServer) TimeoutBucket(ctx context.Context, req *types.QueryTimeoutBucketRequest) (*types.QueryTimeoutBucketResponse, error) {
	state, currentVersion, pendingVersion, err := q.parameterBucket(ctx, shared.BucketKind_BUCKET_KIND_TIMEOUT, reqBucketKeyTimeout(req), reqVersionTimeout(req))
	if err != nil {
		return nil, err
	}
	response := &types.QueryTimeoutBucketResponse{
		Bucket: parameterBucketView(state), CurrentVersion: currentVersion,
	}
	if pendingVersion != nil {
		response.XPendingVersion = &types.QueryTimeoutBucketResponse_PendingVersion{PendingVersion: *pendingVersion}
	}
	return response, nil
}

func reqBucketKeyTimeout(req *types.QueryTimeoutBucketRequest) string {
	if req == nil {
		return ""
	}
	return req.BucketKey
}

func reqVersionTimeout(req *types.QueryTimeoutBucketRequest) *uint64 {
	if req == nil {
		return nil
	}
	version, ok := req.GetXVersion().(*types.QueryTimeoutBucketRequest_Version)
	if !ok {
		return nil
	}
	value := version.Version
	return &value
}

func (q queryServer) parameterBucket(ctx context.Context, kind shared.BucketKind, bucketKey string, version *uint64) (types.ParameterBucketVersionState, uint64, *uint64, error) {
	if err := types.ValidateParameterBucketKey(bucketKey); err != nil {
		return types.ParameterBucketVersionState{}, 0, nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if version != nil && *version == 0 {
		return types.ParameterBucketVersionState{}, 0, nil, status.Error(codes.InvalidArgument, "version must be greater than zero")
	}
	pointerKey := types.NewParameterBucketPointerKey(kind, bucketKey)
	current, err := q.k.ParameterBucketCurrentPointer.Get(ctx, pointerKey)
	if errors.Is(err, collections.ErrNotFound) {
		return types.ParameterBucketVersionState{}, 0, nil, status.Error(codes.NotFound, "parameter bucket not found")
	}
	if err != nil {
		return types.ParameterBucketVersionState{}, 0, nil, status.Error(codes.Internal, "internal error")
	}
	selectedVersion := current.CurrentVersion
	if version != nil {
		selectedVersion = *version
	}
	state, err := q.k.GetParameterBucketVersion(ctx, kind, bucketKey, selectedVersion)
	if errors.Is(err, collections.ErrNotFound) {
		return types.ParameterBucketVersionState{}, 0, nil, status.Error(codes.NotFound, "parameter bucket version not found")
	}
	if err != nil {
		return types.ParameterBucketVersionState{}, 0, nil, status.Error(codes.Internal, "internal error")
	}
	var pendingVersion *uint64
	pending, err := q.k.ParameterBucketPendingPointer.Get(ctx, pointerKey)
	if err == nil {
		value := pending.PendingVersion
		pendingVersion = &value
	} else if !errors.Is(err, collections.ErrNotFound) {
		return types.ParameterBucketVersionState{}, 0, nil, status.Error(codes.Internal, "internal error")
	}
	return state, current.CurrentVersion, pendingVersion, nil
}

func parameterBucketView(state types.ParameterBucketVersionState) types.ParameterBucketVersionViewV1 {
	view := types.ParameterBucketVersionViewV1{
		BucketKind: state.BucketKind, BucketKey: state.BucketKey, Version: state.Version,
		SchemaVersion: state.SchemaVersion, EffectiveHeight: state.EffectiveHeight,
		BucketHash: append([]byte(nil), state.ContentHash...), TimeoutEntries: state.TimeoutEntries,
		EntryCount: state.EntryCount, EncodedSizeBytes: state.EncodedSizeBytes,
	}
	return view
}
