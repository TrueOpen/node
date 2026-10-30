package keeper

import (
	"bytes"
	"context"
	"errors"

	"cosmossdk.io/collections"
	"github.com/cosmos/gogoproto/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func dispatchQueryError(err error) error {
	if errors.Is(err, collections.ErrNotFound) {
		return status.Error(codes.NotFound, "dispatch primary not found")
	}
	return status.Error(codes.Internal, err.Error())
}

func (q *queryServer) checkDispatchQuerySize(ctx context.Context, response proto.Message) error {
	caps, err := q.queryCaps(ctx)
	if err != nil {
		return err
	}
	if uint64(proto.Size(response)) > caps.responseSize {
		return status.Error(codes.ResourceExhausted, "dispatch response exceeds the configured byte cap")
	}
	return nil
}

func (q *queryServer) DispatchRecord(ctx context.Context, req *types.QueryDispatchRecordRequest) (*types.QueryDispatchRecordResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	var taskID []byte
	switch selector := req.Selector.(type) {
	case *types.QueryDispatchRecordRequest_ByOrder:
		if selector.ByOrder == nil {
			return nil, status.Error(codes.InvalidArgument, "order selector is required")
		}
		id, err := types.DeriveTaskIDFromRawSession(selector.ByOrder.SessionId, selector.ByOrder.OrderSequence)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		taskID = id[:]
	case *types.QueryDispatchRecordRequest_DispatchRecordHash:
		hash, err := requireQueryHash32("dispatch_record_hash", selector.DispatchRecordHash)
		if err != nil {
			return nil, err
		}
		taskID, err = q.k.DispatchRecordByHash.Get(ctx, hash)
		if err != nil {
			return nil, dispatchQueryError(err)
		}
	default:
		return nil, status.Error(codes.InvalidArgument, "dispatch record selector is required")
	}
	row, err := q.k.readDispatchRecord(ctx, taskID)
	if err != nil {
		return nil, dispatchQueryError(err)
	}
	if selector, ok := req.Selector.(*types.QueryDispatchRecordRequest_DispatchRecordHash); ok && !bytes.Equal(selector.DispatchRecordHash, row.DispatchRecordHash) {
		return nil, status.Error(codes.Internal, "record hash index/primary mismatch")
	}
	response := &types.QueryDispatchRecordResponse{Record: &row}
	return response, q.checkDispatchQuerySize(ctx, response)
}

func (q *queryServer) DispatchCompensationClaim(ctx context.Context, req *types.QueryDispatchCompensationClaimRequest) (*types.QueryDispatchCompensationClaimResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	key, err := requireQueryHash32("dispatch_ticket_hash", req.DispatchTicketHash)
	if err != nil {
		return nil, err
	}
	row, err := q.k.readDispatchClaim(ctx, key)
	if err != nil {
		return nil, dispatchQueryError(err)
	}
	response := &types.QueryDispatchCompensationClaimResponse{Claim: &row}
	return response, q.checkDispatchQuerySize(ctx, response)
}

func (q *queryServer) DispatchWinnerIneligible(ctx context.Context, req *types.QueryDispatchWinnerIneligibleRequest) (*types.QueryDispatchWinnerIneligibleResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	key, err := requireQueryHash32("dispatch_ticket_hash", req.DispatchTicketHash)
	if err != nil {
		return nil, err
	}
	state, err := q.k.DispatchWinnerIneligible.Get(ctx, key)
	if err != nil {
		return nil, dispatchQueryError(err)
	}
	if !bytes.Equal(key, state.DispatchTicketHash) {
		return nil, status.Error(codes.Internal, "winner exclusion key/primary mismatch")
	}
	row, err := q.k.ProjectDispatchWinnerIneligibleStore(state)
	if err != nil {
		return nil, dispatchQueryError(err)
	}
	response := &types.QueryDispatchWinnerIneligibleResponse{Row: &row}
	return response, q.checkDispatchQuerySize(ctx, response)
}

func (q *queryServer) DispatchChallenge(ctx context.Context, req *types.QueryDispatchChallengeRequest) (*types.QueryDispatchChallengeResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	key, err := requireQueryHash32("challenge_id", req.ChallengeId)
	if err != nil {
		return nil, err
	}
	row, err := q.k.readDispatchChallenge(ctx, key)
	if err != nil {
		return nil, dispatchQueryError(err)
	}
	response := &types.QueryDispatchChallengeResponse{Row: &row}
	return response, q.checkDispatchQuerySize(ctx, response)
}

func (q *queryServer) DispatchChallengesByBuilder(ctx context.Context, req *types.QueryDispatchChallengesByBuilderRequest) (*types.QueryDispatchChallengesByBuilderResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	rows, page, err := q.openDispatchPage(ctx, req.BuilderAddress, req.Page, shared.QueryRPCTaskDispatchChallengesByBuilderV1, q.k.DispatchOpenChallengeByBuilder, func(id []byte) (proto.Message, error) {
		row, err := q.k.readDispatchChallenge(ctx, id)
		if err != nil {
			return nil, err
		}
		if row.BuilderAddress != req.BuilderAddress || !dispatchChallengePending(row.Status) {
			return nil, status.Error(codes.Internal, "open challenge index/primary mismatch")
		}
		return &row, nil
	})
	if err != nil {
		return nil, err
	}
	response := &types.QueryDispatchChallengesByBuilderResponse{Rows: make([]*types.DispatchChallengeState, len(rows)), Page: page}
	for i, row := range rows {
		response.Rows[i] = row.(*types.DispatchChallengeState)
	}
	return response, q.checkDispatchQuerySize(ctx, response)
}

func (q *queryServer) DispatchCompensationClaimsByBuilder(ctx context.Context, req *types.QueryDispatchCompensationClaimsByBuilderRequest) (*types.QueryDispatchCompensationClaimsByBuilderResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	rows, page, err := q.openDispatchPage(ctx, req.BuilderAddress, req.Page, shared.QueryRPCTaskDispatchClaimsByBuilderV1, q.k.DispatchOpenClaimByBuilder, func(id []byte) (proto.Message, error) {
		row, err := q.k.readDispatchClaim(ctx, id)
		if err != nil {
			return nil, err
		}
		if row.BuilderAddress != req.BuilderAddress || row.Status != types.DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_PENDING_REBUTTAL {
			return nil, status.Error(codes.Internal, "open claim index/primary mismatch")
		}
		return &row, nil
	})
	if err != nil {
		return nil, err
	}
	response := &types.QueryDispatchCompensationClaimsByBuilderResponse{Claims: make([]*types.DispatchCompensationClaimState, len(rows)), Page: page}
	for i, row := range rows {
		response.Claims[i] = row.(*types.DispatchCompensationClaimState)
	}
	return response, q.checkDispatchQuerySize(ctx, response)
}

// openDispatchPage shares the raw20 prefix scan, height-pinned cursor and exact
// protobuf envelope accounting of the two open-only recovery queries.
func (q *queryServer) openDispatchPage(ctx context.Context, builder string, request shared.QueryPageRequestV1, rpc string, index collections.KeySet[types.RoleActiveTaskKey], load func([]byte) (proto.Message, error)) ([]proto.Message, shared.QueryPageResponseV1, error) {
	var page shared.QueryPageResponseV1
	operator, _, err := q.k.canonicalAddress("builder_address", builder)
	if err != nil {
		return nil, page, status.Error(codes.InvalidArgument, err.Error())
	}
	caps, err := q.queryCaps(ctx)
	if err != nil {
		return nil, page, err
	}
	limit, encoded, err := resolveQueryPage(request, caps)
	if err != nil {
		return nil, page, err
	}
	height, last, rpcDigest, selectorDigest, err := q.decodeAddressPairQueryPageToken(ctx, encoded, rpc, operator, index.KeyCodec(), operator)
	if err != nil {
		return nil, page, err
	}
	rangeKey := collections.NewPrefixedPairRange[types.AddrKey, types.Hash32Key](operator)
	if len(last) != 0 {
		rangeKey = rangeKey.StartExclusive(last)
	}
	iter, err := index.Iterate(ctx, rangeKey)
	if err != nil {
		return nil, page, dispatchQueryError(err)
	}
	defer iter.Close()
	var rows []proto.Message
	var ids [][]byte
	bytesUsed := 0
	more := false
	for ; iter.Valid(); iter.Next() {
		if uint32(len(rows)) >= limit {
			more = true
			break
		}
		key, err := iter.Key()
		if err != nil {
			return nil, page, dispatchQueryError(err)
		}
		row, err := load(key.K2())
		if err != nil {
			return nil, page, status.Error(codes.Internal, err.Error())
		}
		cost := protowire.SizeTag(1) + protowire.SizeBytes(proto.Size(row))
		if uint64(bytesUsed+cost+2) > caps.responseSize {
			more = true
			break
		}
		rows, ids = append(rows, row), append(ids, bytes.Clone(key.K2()))
		bytesUsed += cost
	}
	for more {
		if len(rows) == 0 {
			return nil, page, status.Error(codes.ResourceExhausted, "single open dispatch row exceeds the response byte cap")
		}
		token, err := encodeRoleActiveTasksPageToken(index, operator, ids[len(ids)-1], rpcDigest, selectorDigest, height)
		if err != nil {
			return nil, page, status.Error(codes.Internal, err.Error())
		}
		if uint32(len(token)) > caps.pageTokenSize {
			return nil, page, status.Error(codes.Internal, "dispatch page token exceeds its configured cap")
		}
		page.NextPageToken = token
		if uint64(bytesUsed+protowire.SizeTag(2)+protowire.SizeBytes(page.Size())) <= caps.responseSize {
			break
		}
		bytesUsed -= protowire.SizeTag(1) + protowire.SizeBytes(proto.Size(rows[len(rows)-1]))
		rows, ids = rows[:len(rows)-1], ids[:len(ids)-1]
	}
	return rows, page, nil
}
