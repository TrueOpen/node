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

func (q queryServer) Model(ctx context.Context, req *types.QueryModelRequest) (*types.QueryModelResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	if err := types.ValidateModelID(req.ModelId); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	model, err := q.k.Model.Get(ctx, req.ModelId)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "model not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	if model.ModelId != req.ModelId || model.Validate() != nil {
		return nil, status.Error(codes.Internal, "invalid model state")
	}
	return &types.QueryModelResponse{Model: model}, nil
}

// Models pages the model primary map in canonical store key byte order. It is
// the registry's only enumeration surface: Query/Model needs a model_id the
// caller already holds, so a client with no out-of-band list had no way to
// discover one.
//
// V1 accepts no status selector. ModelState carries a status but there is no
// status index, so filtering would have to visit - and pay for - rows it never
// returns, which makes the work behind one bounded page unbounded. A typed
// discovery index has to land before a filtered variant can.
func (q queryServer) Models(ctx context.Context, req *types.QueryModelsRequest) (*types.QueryModelsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	scope, err := q.newRegistryPageScope(ctx, modelsRPC, req.Page, q.k.Model.KeyCodec(), types.ValidateModelID)
	if err != nil {
		return nil, err
	}
	iter, err := q.k.Model.Iterate(ctx, scope.walkRange())
	if err != nil {
		return nil, status.Error(codes.Internal, "model store unavailable")
	}
	defer iter.Close()

	response := &types.QueryModelsResponse{Models: []types.ModelState{}}
	var pageKeys [][]byte
	more := false
	for ; iter.Valid(); iter.Next() {
		// Stop before decoding the row we will not return: reaching the limit
		// with the iterator still valid is exactly what proves a next page
		// exists, and only then may a token be minted.
		if uint32(len(response.Models)) >= scope.limit {
			more = true
			break
		}
		key, err := iter.Key()
		if err != nil {
			return nil, status.Error(codes.Internal, "model key unavailable")
		}
		model, err := iter.Value()
		if err != nil {
			return nil, status.Error(codes.Internal, "model row unavailable")
		}
		if model.ModelId != key || model.Validate() != nil {
			return nil, status.Error(codes.Internal, "model key disagrees with primary state")
		}
		candidate := append(response.Models, model)
		if (&types.QueryModelsResponse{Models: candidate}).Size() > scope.responseByteCap() {
			if len(response.Models) == 0 {
				return nil, status.Error(codes.ResourceExhausted, "one model exceeds the response byte cap")
			}
			more = true
			break
		}
		pageKey, err := encodeCollectionKey(q.k.Model.KeyCodec(), key)
		if err != nil {
			return nil, status.Error(codes.Internal, "could not encode model page key")
		}
		response.Models, pageKeys = candidate, append(pageKeys, pageKey)
	}
	if !more {
		return response, nil
	}
	rows, next, err := scope.fitRowsWithToken(len(response.Models), pageKeysAt(pageKeys),
		func(rows int, token []byte) int {
			return (&types.QueryModelsResponse{
				Models: response.Models[:rows], Page: shared.QueryPageResponseV1{NextPageToken: token},
			}).Size()
		})
	if err != nil {
		return nil, err
	}
	response.Models = response.Models[:rows]
	response.Page = shared.QueryPageResponseV1{NextPageToken: next}
	return response, nil
}

func (q queryServer) Profile(ctx context.Context, req *types.QueryProfileRequest) (*types.QueryProfileResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	modelID, profileVersion, err := validateModelProfileQueryScope(req.ModelId, req.ProfileVersion)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	profile, err := q.k.Profile.Get(ctx, types.NewProfileStateKey(modelID, profileVersion))
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "profile not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	if profile.ModelId != modelID || profile.ProfileVersion != profileVersion || q.k.validateStoredProfile(ctx, profile) != nil {
		return nil, status.Error(codes.Internal, "invalid profile state")
	}
	return &types.QueryProfileResponse{Profile: profile}, nil
}
