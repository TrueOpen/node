package keeper

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (q queryServer) CurrentCandidatePool(ctx context.Context, req *types.QueryCurrentCandidatePoolRequest) (*types.QueryCurrentCandidatePoolResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	current, err := q.k.CurrentCandidatePool.Get(ctx)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "current candidate pool not ready")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "load current candidate pool")
	}
	snapshot, err := q.k.loadCandidatePoolSnapshot(ctx, current.SnapshotId)
	if err != nil || snapshot.Status != candidateSnapshotActive || snapshot.Epoch != current.Epoch || !equalCandidateBytes(snapshot.PoolHash, current.PoolHash) {
		return nil, status.Error(codes.Internal, "current candidate pool pointer is invalid")
	}
	height := candidatePoolContextHeight(ctx)
	if height < snapshot.EffectiveHeight || height >= snapshot.ExpiresHeight {
		return nil, status.Error(codes.NotFound, "current candidate pool is not effective")
	}
	return &types.QueryCurrentCandidatePoolResponse{Snapshot: candidateSnapshotView(snapshot)}, nil
}

func (q queryServer) CandidatePoolSnapshot(ctx context.Context, req *types.QueryCandidatePoolSnapshotRequest) (*types.QueryCandidatePoolSnapshotResponse, error) {
	key, err := candidateQuerySnapshotKey(req)
	if err != nil {
		return nil, err
	}
	snapshot, err := q.k.loadCandidatePoolSnapshot(ctx, key)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "candidate pool snapshot not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "candidate pool snapshot is invalid")
	}
	return &types.QueryCandidatePoolSnapshotResponse{Snapshot: candidateSnapshotView(snapshot)}, nil
}

func (q queryServer) CandidatePoolMember(ctx context.Context, req *types.QueryCandidatePoolMemberRequest) (*types.QueryCandidatePoolMemberResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	key, err := candidateRawSnapshotKey(req.SnapshotId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	snapshot, err := q.k.loadCandidatePoolSnapshot(ctx, key)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "candidate pool snapshot not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "candidate pool snapshot is invalid")
	}
	if snapshot.Status == candidateSnapshotPruned {
		return nil, status.Error(codes.FailedPrecondition, "candidate pool body has been pruned")
	}
	if req.CandidateSlot >= snapshot.SlotCapacity {
		return nil, status.Error(codes.InvalidArgument, "candidate_slot exceeds snapshot capacity")
	}
	member, err := q.k.loadCandidatePoolMember(ctx, snapshot, req.CandidateSlot)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "candidate pool member not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "candidate pool member is invalid")
	}
	return &types.QueryCandidatePoolMemberResponse{Member: candidateMemberView(member)}, nil
}

func (q queryServer) CandidatePoolMembers(ctx context.Context, req *types.QueryCandidatePoolMembersRequest) (*types.QueryCandidatePoolMembersResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	key, err := candidateRawSnapshotKey(req.SnapshotId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	snapshot, err := q.k.loadCandidatePoolSnapshot(ctx, key)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "candidate pool snapshot not found")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "candidate pool snapshot is invalid")
	}
	if snapshot.Status == candidateSnapshotPruned {
		return nil, status.Error(codes.FailedPrecondition, "candidate pool body has been pruned")
	}
	params, err := q.k.Params.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "params unavailable")
	}
	limit := req.Page.Limit
	if limit == 0 {
		limit = params.CandidatePool.CandidatePoolQueryDefaultLimit
	}
	if limit == 0 || limit > params.CandidatePool.CandidatePoolQueryHardLimit || (params.QueryEvent.MaxQueryPageLimit != 0 && limit > params.QueryEvent.MaxQueryPageLimit) {
		return nil, status.Error(codes.InvalidArgument, "invalid candidate pool query limit")
	}
	if uint32(len(req.Page.PageToken)) > params.QueryEvent.MaxQueryPageTokenBytes {
		return nil, status.Error(codes.InvalidArgument, "candidate member page token exceeds configured maximum")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	queryHeight, rpcDigest, selectorDigest, err := queryPageDigests(sdkCtx, candidatePoolMembersRPC, req.SnapshotId)
	if err != nil {
		return nil, err
	}
	lastSlot, hasToken, err := decodeCandidateMemberPageToken(req.Page.PageToken, rpcDigest, selectorDigest, queryHeight, snapshot.SlotCapacity)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	iter, err := q.k.CandidatePoolMember.Iterate(ctx, collections.NewPrefixedPairRange[uint64, uint32](snapshot.Epoch))
	if err != nil {
		return nil, status.Error(codes.Internal, "candidate member store unavailable")
	}
	defer iter.Close()
	members := make([]types.CandidatePoolMemberViewV1, 0, limit)
	responseBytes := uint64(0)
	more := false
	for ; iter.Valid(); iter.Next() {
		row, err := iter.Value()
		if err != nil {
			return nil, status.Error(codes.Internal, "candidate member store is invalid")
		}
		if hasToken && row.Slot <= lastSlot {
			continue
		}
		if uint32(len(members)) >= limit {
			more = true
			break
		}
		valid, err := q.k.loadCandidatePoolMember(ctx, snapshot, row.Slot)
		if err != nil {
			return nil, status.Error(codes.Internal, "candidate member/binding join is invalid")
		}
		view := candidateMemberView(valid)
		rowBytes := uint64(view.Size() + 8)
		if params.QueryEvent.MaxQueryResponseBytes != 0 && responseBytes+rowBytes > params.QueryEvent.MaxQueryResponseBytes {
			if len(members) == 0 {
				return nil, status.Error(codes.ResourceExhausted, "one candidate member exceeds query response byte cap")
			}
			more = true
			break
		}
		members = append(members, view)
		responseBytes += rowBytes
	}
	var next []byte
	if more && len(members) != 0 {
		next, err = shared.EncodePageTokenV1(rpcDigest, selectorDigest, uint32CursorKey(members[len(members)-1].CandidateSlot), queryHeight)
		if err != nil {
			return nil, status.Error(codes.Internal, "could not encode candidate member page token")
		}
	}
	return &types.QueryCandidatePoolMembersResponse{
		Members: members, Page: shared.QueryPageResponseV1{NextPageToken: next},
	}, nil
}

func candidateSnapshotView(snapshot types.CandidatePoolSnapshotState) types.CandidatePoolSnapshotViewV1 {
	view := types.CandidatePoolSnapshotViewV1{
		Epoch: snapshot.Epoch, SnapshotId: append([]byte(nil), snapshot.SnapshotId...), PoolHash: append([]byte(nil), snapshot.PoolHash...),
		Status: snapshot.Status, SlotCapacity: snapshot.SlotCapacity, ActiveCount: snapshot.ActiveCount,
		ActiveBitmapHash: append([]byte(nil), snapshot.ActiveBitmapHash...), MemberSetHash: append([]byte(nil), snapshot.MemberSetHash...),
		EffectiveHeight: snapshot.EffectiveHeight, ExpiresHeight: snapshot.ExpiresHeight,
	}
	if snapshot.PublishedHeight != 0 {
		view.XPublishedHeight = &types.CandidatePoolSnapshotViewV1_PublishedHeight{PublishedHeight: snapshot.PublishedHeight}
	}
	if snapshot.Status == candidateSnapshotPruned {
		view.XPrunedHeight = &types.CandidatePoolSnapshotViewV1_PrunedHeight{PrunedHeight: snapshot.PrunedHeight}
	}
	return view
}

func candidateMemberView(member types.CandidatePoolMemberState) types.CandidatePoolMemberViewV1 {
	return types.CandidatePoolMemberViewV1{
		CandidateSlot: member.Slot, SlotVersion: member.SlotVersion,
		OperatorAddress: member.OperatorAddress, BindingHash: append([]byte(nil), member.BindingHash...),
	}
}

func candidateQuerySnapshotKey(req *types.QueryCandidatePoolSnapshotRequest) (shared.Hash32Key, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	key, err := candidateRawSnapshotKey(req.SnapshotId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return key, nil
}

// candidateRawSnapshotKey now lives up to its name: the request already carries
// snapshot_id as bytes, and the store key is those same bytes, so the hex hop
// this used to make between the two is gone.
func candidateRawSnapshotKey(raw []byte) (shared.Hash32Key, error) {
	if len(raw) != shared.Hash32KeySize {
		return nil, fmt.Errorf("snapshot_id must contain exactly 32 raw bytes")
	}
	return raw, nil
}

func decodeCandidateMemberPageToken(token, rpcDigest, selectorDigest []byte, queryHeight uint64, capacity uint32) (uint32, bool, error) {
	if len(token) == 0 {
		return 0, false, nil
	}
	primaryKey, err := shared.DecodePageTokenV1(token, rpcDigest, selectorDigest, queryHeight)
	if err != nil || len(primaryKey) != 4 {
		return 0, false, fmt.Errorf("candidate member page token must be u32_be(last_slot)")
	}
	last := binary.BigEndian.Uint32(primaryKey)
	if last >= capacity {
		return 0, false, fmt.Errorf("candidate member page token exceeds snapshot capacity")
	}
	return last, true, nil
}
