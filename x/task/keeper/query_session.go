package keeper

import (
	"bytes"
	"context"
	"errors"

	"cosmossdk.io/collections"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// Session is `QuerySession`: the retained active/idle/closed row. Once the
// history has been collapsed the row is gone and the caller must ask for the
// terminal summary instead.
func (q *queryServer) Session(ctx context.Context, req *types.QuerySessionRequest) (*types.QuerySessionResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	sessionKey, err := requireQueryHash32("session_id", req.SessionId)
	if err != nil {
		return nil, err
	}
	stream, err := q.k.ReadStream(ctx, sessionKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "session not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !bytes.Equal(stream.SessionId, req.SessionId) {
		return nil, status.Error(codes.Internal, "stream primary key does not match session_id")
	}
	return &types.QuerySessionResponse{Session: stream}, nil
}

// SessionNonce is `QuerySessionNonce`: a legal address with no state
// returns 0; a malformed address is InvalidArgument, never an empty state.
func (q *queryServer) SessionNonce(ctx context.Context, req *types.QuerySessionNonceRequest) (*types.QuerySessionNonceResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	owner, err := q.requireCanonicalAddress("user_address", req.UserAddress)
	if err != nil {
		return nil, err
	}
	state, err := q.k.ReadSessionNonce(ctx, owner)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return &types.QuerySessionNonceResponse{}, nil
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QuerySessionNonceResponse{NextSessionNonce: state.NextSessionNonce}, nil
}

// SessionsByOwner is `QuerySessionsByOwner`: it only walks the ACTIVE/IDLE
// owner index in session_id ascending order. CLOSED rows are forbidden in
// that index, so history is never enumerated here.
func (q *queryServer) SessionsByOwner(ctx context.Context, req *types.QuerySessionsByOwnerRequest) (*types.QuerySessionsByOwnerResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	owner, err := q.requireCanonicalAddress("user_address", req.UserAddress)
	if err != nil {
		return nil, err
	}
	caps, err := q.queryCaps(ctx)
	if err != nil {
		return nil, err
	}
	limit, token, err := resolveQueryPage(req.Page, caps)
	if err != nil {
		return nil, err
	}
	ownerBytes, err := q.k.addressCodec.StringToBytes(owner)
	if err != nil {
		return nil, status.Error(codes.Internal, "canonical user address cannot be decoded")
	}
	queryHeight, lastSessionKey, rpcDigest, selectorDigest, err := q.decodeAddressPairQueryPageToken(
		ctx, token, shared.QueryRPCTaskSessionsByOwnerV1, ownerBytes, q.k.SessionByOwnerIndex.KeyCodec(), ownerBytes,
	)
	if err != nil {
		return nil, err
	}

	// The address prefix is raw codec bytes. The terminal Hash32 cursor remains
	// a fixed 32-byte component and advances only within this owner prefix.
	rng := collections.NewPrefixedPairRange[types.AddrKey, types.Hash32Key](ownerBytes)
	if len(lastSessionKey) != 0 {
		rng = rng.StartExclusive(lastSessionKey)
	}
	iter, err := q.k.SessionByOwnerIndex.Iterate(ctx, rng)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	defer iter.Close()

	response := &types.QuerySessionsByOwnerResponse{Sessions: []types.StreamState{}}
	responseBytes := 0
	var lastReturnedSessionKey types.SessionKey
	for ; iter.Valid(); iter.Next() {
		if uint32(len(response.Sessions)) >= limit {
			pageToken, err := q.encodeSessionsByOwnerPageToken(ownerBytes, lastReturnedSessionKey, rpcDigest, selectorDigest, queryHeight)
			if err != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
			if uint32(len(pageToken)) > caps.pageTokenSize {
				return nil, status.Error(codes.Internal, "sessions by owner page token exceeds the configured byte cap")
			}
			response.Page = shared.QueryPageResponseV1{NextPageToken: pageToken}
			return response, nil
		}
		key, err := iter.Key()
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		stream, err := q.k.ReadStream(ctx, key.K2())
		if err != nil {
			// A stale index row inside a page is an invariant break.
			return nil, status.Errorf(codes.Internal, "session_by_owner index points at missing stream %s", hex32(key.K2()))
		}
		if stream.Status == types.SessionStatus_SESSION_STATUS_CLOSED {
			return nil, status.Errorf(codes.Internal, "session_by_owner index retains CLOSED session %s", hex32(key.K2()))
		}
		rowBytes := stream.Size()
		if uint64(responseBytes+rowBytes) > caps.responseSize {
			if len(response.Sessions) == 0 {
				return nil, status.Error(codes.Internal, "single session row exceeds the response byte cap")
			}
			pageToken, err := q.encodeSessionsByOwnerPageToken(ownerBytes, lastReturnedSessionKey, rpcDigest, selectorDigest, queryHeight)
			if err != nil {
				return nil, status.Error(codes.Internal, err.Error())
			}
			if uint32(len(pageToken)) > caps.pageTokenSize {
				return nil, status.Error(codes.Internal, "sessions by owner page token exceeds the configured byte cap")
			}
			response.Page = shared.QueryPageResponseV1{NextPageToken: pageToken}
			return response, nil
		}
		responseBytes += rowBytes
		response.Sessions = append(response.Sessions, stream)
		lastReturnedSessionKey = key.K2()
	}
	return response, nil
}

func (q *queryServer) encodeSessionsByOwnerPageToken(
	owner types.AddrKey,
	sessionKey types.SessionKey,
	rpcDigest, selectorDigest []byte,
	queryHeight uint64,
) ([]byte, error) {
	primaryKey, err := encodeAddressPairPrimaryKey(q.k.SessionByOwnerIndex.KeyCodec(), types.NewSessionByOwnerKey(owner, sessionKey))
	if err != nil {
		return nil, err
	}
	return encodeAddressPairQueryPageToken(rpcDigest, selectorDigest, primaryKey, queryHeight)
}

// SessionTerminalSummary is `QuerySessionTerminalSummary`.
func (q *queryServer) SessionTerminalSummary(ctx context.Context, req *types.QuerySessionTerminalSummaryRequest) (*types.QuerySessionTerminalSummaryResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	sessionKey, err := requireQueryHash32("session_id", req.SessionId)
	if err != nil {
		return nil, err
	}
	summary, err := q.k.ReadSessionTerminalSummary(ctx, sessionKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "session terminal summary not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QuerySessionTerminalSummaryResponse{Summary: summary}, nil
}

// OrderSequence is `QueryOrderSequence`: only inside the detail retention
// window. After the history is folded into `sequence_root` the row is NotFound;
// faking a membership proof out of the root is forbidden.
func (q *queryServer) OrderSequence(ctx context.Context, req *types.QueryOrderSequenceRequest) (*types.QueryOrderSequenceResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	sessionKey, err := requireQueryHash32("session_id", req.SessionId)
	if err != nil {
		return nil, err
	}
	sequence, err := q.k.OrderSequence.Get(ctx, types.NewOrderSequenceStateKey(sessionKey, req.OrderSequence))
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "order sequence not found")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryOrderSequenceResponse{Sequence: sequence}, nil
}
