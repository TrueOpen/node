package keeper

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"sort"
	"strings"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (q queryServer) CortexNode(ctx context.Context, req *types.QueryCortexNodeRequest) (*types.QueryCortexNodeResponse, error) {
	operatorAddress, err := q.queryOperator(ctx, req)
	if err != nil {
		return nil, err
	}
	node, err := q.k.CortexNode.Get(ctx, operatorAddress)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "cortex node not found")
	}
	if err != nil || node.OperatorAddress != operatorAddress || node.Validate() != nil {
		return nil, status.Error(codes.Internal, "invalid cortex node state")
	}
	return &types.QueryCortexNodeResponse{Node: node}, nil
}

func (q queryServer) ServiceBond(ctx context.Context, req *types.QueryServiceBondRequest) (*types.QueryServiceBondResponse, error) {
	operatorAddress, err := q.queryOperator(ctx, req)
	if err != nil {
		return nil, err
	}
	bond, err := q.k.ServiceBond.Get(ctx, types.NewServiceBondKey(operatorAddress))
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "service bond not found")
	}
	if err != nil || bond.OperatorAddress != operatorAddress || bond.Validate() != nil {
		return nil, status.Error(codes.Internal, "invalid service bond state")
	}
	return &types.QueryServiceBondResponse{Bond: bond}, nil
}

func (q queryServer) ServiceUnbondings(ctx context.Context, req *types.QueryServiceUnbondingsRequest) (*types.QueryServiceUnbondingsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	operatorBytes, operatorAddress, err := q.k.requireCanonicalAddress("operator_address", req.OperatorAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if req.Status != types.UnbondingStatusOpen && req.Status != types.UnbondingStatusMature {
		return nil, status.Error(codes.InvalidArgument, "status must be OPEN or MATURE")
	}
	params, err := q.k.Params.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "params unavailable")
	}
	limit := req.Page.Limit
	if limit == 0 {
		limit = minUint32(100, params.QueryEvent.MaxQueryPageLimit)
	}
	if limit == 0 || limit > params.QueryEvent.MaxQueryPageLimit || uint32(len(req.Page.PageToken)) > params.QueryEvent.MaxQueryPageTokenBytes {
		return nil, status.Error(codes.InvalidArgument, "invalid page limit or token size")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	queryHeight, rpcDigest, selectorDigest, err := queryPageDigests(
		sdkCtx, serviceUnbondingsRPC, operatorBytes, shared.EnumBE(uint32(req.Status)),
	)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	lastKey, err := decodeUnbondingPageToken(req.Page.PageToken, rpcDigest, selectorDigest, queryHeight)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	rows, err := q.k.unbondingsForQuery(ctx, operatorAddress, req.Status)
	if err != nil {
		return nil, status.Error(codes.Internal, "unbonding index unavailable")
	}
	start := 0
	if len(lastKey) != 0 {
		start = sort.Search(len(rows), func(i int) bool { return bytes.Compare(unbondingPageKey(rows[i]), lastKey) > 0 })
	}
	end := start + int(limit)
	if end > len(rows) {
		end = len(rows)
	}
	entries := append([]types.UnbondingState(nil), rows[start:end]...)
	var next []byte
	if end < len(rows) && len(entries) > 0 {
		next, err = encodeUnbondingPageToken(rpcDigest, selectorDigest, unbondingPageKey(entries[len(entries)-1]), queryHeight)
		if err != nil {
			return nil, status.Error(codes.Internal, "could not encode page token")
		}
	}
	return &types.QueryServiceUnbondingsResponse{Entries: entries, Page: shared.QueryPageResponseV1{NextPageToken: next}}, nil
}

func (q queryServer) CurrentServiceKey(ctx context.Context, req *types.QueryCurrentServiceKeyRequest) (*types.QueryCurrentServiceKeyResponse, error) {
	if req == nil || requireParticipantType(req.ParticipantType) != nil {
		return nil, status.Error(codes.InvalidArgument, "participant_type must be CORTEX or BUILDER")
	}
	_, operatorAddress, err := q.k.requireCanonicalAddress("operator_address", req.OperatorAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	identity, err := q.k.loadParticipantIdentity(ctx, req.ParticipantType, operatorAddress)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "current service key not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "current service key unavailable")
	}
	view := types.CurrentServiceKeyViewV1{
		ParticipantType: identity.ParticipantType, OperatorAddress: identity.OperatorAddress,
		ServiceAddress: identity.ServiceAddress, ServicePubkey: append([]byte(nil), identity.ServicePubkey...),
		ServiceAuthorizationNonce: identity.AuthorizationNonce, CurrentDescriptorVersion: identity.CurrentDescriptorVersion,
	}
	if req.ParticipantType == shared.ParticipantType_PARTICIPANT_TYPE_CORTEX {
		view.ParticipantStatus = &types.CurrentServiceKeyViewV1_CortexServiceKeyStatus{CortexServiceKeyStatus: identity.ServiceKeyStatus}
	} else {
		view.ParticipantStatus = &types.CurrentServiceKeyViewV1_BuilderServiceKeyStatus{BuilderServiceKeyStatus: identity.ServiceKeyStatus}
	}
	return &types.QueryCurrentServiceKeyResponse{Binding: view}, nil
}

func (q queryServer) ServiceDescriptor(ctx context.Context, req *types.QueryServiceDescriptorRequest) (*types.QueryServiceDescriptorResponse, error) {
	if req == nil || requireParticipantType(req.ParticipantType) != nil {
		return nil, status.Error(codes.InvalidArgument, "participant_type must be CORTEX or BUILDER")
	}
	_, operatorAddress, err := q.k.requireCanonicalAddress("operator_address", req.OperatorAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	descriptor, err := q.k.ServiceDescriptor.Get(ctx, types.NewParticipantKey(req.ParticipantType, operatorAddress))
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "service descriptor not found")
	}
	if err != nil || descriptor.ParticipantType != req.ParticipantType || descriptor.OperatorAddress != operatorAddress || descriptor.Validate() != nil {
		return nil, status.Error(codes.Internal, "invalid service descriptor state")
	}
	return &types.QueryServiceDescriptorResponse{Descriptor_: descriptor}, nil
}

func (q queryServer) ServiceLifecycle(ctx context.Context, req *types.QueryServiceLifecycleRequest) (*types.QueryServiceLifecycleResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	_, operatorAddress, err := q.k.requireCanonicalAddress("operator_address", req.OperatorAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	bond, bondErr := q.k.ServiceBond.Get(ctx, operatorAddress)
	node, nodeErr := q.k.CortexNode.Get(ctx, operatorAddress)
	if errors.Is(bondErr, collections.ErrNotFound) && errors.Is(nodeErr, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "service lifecycle not found")
	}
	if bondErr != nil || bond.Validate() != nil || bond.OperatorAddress != operatorAddress {
		return nil, status.Error(codes.Internal, "invalid service lifecycle state")
	}
	view := types.ServiceLifecycleViewV1{Bond: bond}
	if errors.Is(nodeErr, collections.ErrNotFound) {
		if bond.Status != types.ServiceBondStatus_SERVICE_BOND_STATUS_EXITED &&
			bond.Status != types.ServiceBondStatus_SERVICE_BOND_STATUS_TOMBSTONED {
			return nil, status.Error(codes.Internal, "non-terminal service bond has no cortex node")
		}
		return &types.QueryServiceLifecycleResponse{Lifecycle: view}, nil
	}
	if nodeErr != nil || node.Validate() != nil || node.OperatorAddress != operatorAddress {
		return nil, status.Error(codes.Internal, "invalid service lifecycle state")
	}
	view.XNode = &types.ServiceLifecycleViewV1_Node{Node: &node}
	return &types.QueryServiceLifecycleResponse{Lifecycle: view}, nil
}

func (q queryServer) ProfileCapability(ctx context.Context, req *types.QueryProfileCapabilityRequest) (*types.QueryProfileCapabilityResponse, error) {
	operatorAddress, modelID, profileVersion, err := q.queryProviderProfile(ctx, req)
	if err != nil {
		return nil, err
	}
	capability, err := q.k.ProfileCapability.Get(ctx, types.NewProfileCapabilityKey(operatorAddress, modelID, profileVersion))
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "profile capability not found")
	}
	if err != nil || capability.Validate() != nil {
		return nil, status.Error(codes.Internal, "invalid profile capability state")
	}
	return &types.QueryProfileCapabilityResponse{Capability: capability}, nil
}

func (q queryServer) ModelSupport(ctx context.Context, req *types.QueryModelSupportRequest) (*types.QueryModelSupportResponse, error) {
	operatorAddress, modelID, profileVersion, err := q.queryProviderProfile(ctx, req)
	if err != nil {
		return nil, err
	}
	support, err := q.k.ModelSupport.Get(ctx, types.NewModelSupportKey(operatorAddress, modelID, profileVersion))
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "model support not found")
	}
	if err != nil || support.Validate() != nil {
		return nil, status.Error(codes.Internal, "invalid model support state")
	}
	return &types.QueryModelSupportResponse{Support: support}, nil
}

func (q queryServer) DailySupport(ctx context.Context, req *types.QueryDailySupportRequest) (*types.QueryDailySupportResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	_, operatorAddress, err := q.k.requireCanonicalAddress("operator_address", req.OperatorAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	support, err := q.k.DailySupport.Get(ctx, types.NewDailySupportKey(req.Epoch, operatorAddress))
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "daily support not found")
	}
	if err != nil || support.Validate() != nil {
		return nil, status.Error(codes.Internal, "invalid daily support state")
	}
	return &types.QueryDailySupportResponse{Support: support}, nil
}

func (q queryServer) queryOperator(ctx context.Context, req interface{ GetOperatorAddress() string }) (string, error) {
	if req == nil {
		return "", status.Error(codes.InvalidArgument, "invalid request")
	}
	_, operatorAddress, err := q.k.requireCanonicalAddress("operator_address", req.GetOperatorAddress())
	if err != nil {
		return "", status.Error(codes.InvalidArgument, err.Error())
	}
	return operatorAddress, nil
}

func (q queryServer) queryProviderProfile(ctx context.Context, req interface {
	GetOperatorAddress() string
	GetModelId() string
	GetProfileVersion() uint32
}) (string, string, uint32, error) {
	if req == nil {
		return "", "", 0, status.Error(codes.InvalidArgument, "invalid request")
	}
	_, operatorAddress, err := q.k.requireCanonicalAddress("operator_address", req.GetOperatorAddress())
	if err != nil {
		return "", "", 0, status.Error(codes.InvalidArgument, err.Error())
	}
	modelID := req.GetModelId()
	if modelID == "" || modelID != strings.TrimSpace(modelID) || types.ValidateModelID(modelID) != nil || req.GetProfileVersion() == 0 {
		return "", "", 0, status.Error(codes.InvalidArgument, "canonical model_id and positive profile_version are required")
	}
	return operatorAddress, modelID, req.GetProfileVersion(), nil
}

func (k Keeper) unbondingsForQuery(ctx context.Context, operatorAddress string, filter types.UnbondingStatus) ([]types.UnbondingState, error) {
	iter, err := k.Unbonding.Iterate(ctx, collections.NewPrefixedPairRange[string, shared.Hash32Key](operatorAddress))
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	rows := make([]types.UnbondingState, 0)
	for ; iter.Valid(); iter.Next() {
		row, err := iter.Value()
		if err != nil {
			return nil, err
		}
		if row.Status == filter {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return bytes.Compare(unbondingPageKey(rows[i]), unbondingPageKey(rows[j])) < 0 })
	return rows, nil
}

func unbondingPageKey(state types.UnbondingState) []byte {
	key := make([]byte, 8+len(state.UnbondingId))
	binary.BigEndian.PutUint64(key[:8], state.MatureHeight)
	copy(key[8:], state.UnbondingId)
	return key
}

// The encoder and the decoder below both take the RPC digest the caller already
// obtained from queryPageDigests, rather than recomputing it.
//
// They used to call a local serviceUnbondingsRPCDigest helper, which made this
// file a second producer of TRUEOPEN_QUERY_RPC_V1 for a method queryPageDigests had
// already hashed in the same request - and the handler discarded that digest to
// make room for it. Two producers of one consensus digest is the wrong shape,
// even while they agree.
func encodeUnbondingPageToken(rpcDigest, selectorDigest, lastKey []byte, queryHeight uint64) ([]byte, error) {
	return shared.EncodePageTokenV1(rpcDigest, selectorDigest, lastKey, queryHeight)
}

func decodeUnbondingPageToken(encoded, rpcDigest, selectorDigest []byte, queryHeight uint64) ([]byte, error) {
	if len(encoded) == 0 {
		return nil, nil
	}
	lastPrimaryKey, err := shared.DecodePageTokenV1(encoded, rpcDigest, selectorDigest, queryHeight)
	if err != nil || len(lastPrimaryKey) != 40 {
		return nil, status.Error(codes.InvalidArgument, "page token does not match this query")
	}
	if binary.BigEndian.Uint64(lastPrimaryKey[:8]) == 0 || bytes.Equal(lastPrimaryKey[8:], make([]byte, 32)) {
		return nil, status.Error(codes.InvalidArgument, "page token primary key is not canonical")
	}
	return lastPrimaryKey, nil
}

func minUint32(left, right uint32) uint32 {
	if left < right {
		return left
	}
	return right
}
