package keeper

import (
	"bytes"
	"context"

	"cosmossdk.io/collections"
	collectionscodec "cosmossdk.io/collections/codec"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

type taskQueryCaps struct {
	pageLimit     uint32
	pageTokenSize uint32
	responseSize  uint64
}

// queryServer embeds the generated forward-compatibility server, while the
// descriptor coverage test requires every RPC registered by this binary to
// resolve to a concrete handler.
type queryServer struct {
	types.UnimplementedQueryServer
	k Keeper
}

// NewQueryServerImpl returns the Task Query service implementation.
func NewQueryServerImpl(k Keeper) types.QueryServer {
	return &queryServer{k: k}
}

var _ types.QueryServer = (*queryServer)(nil)

// resolveQueryPage validates a §16.1 page request: `limit=0` uses the default,
// non-zero above the cap is rejected instead of silently clamped, and an
// over-long token is rejected.
//
// RPC-specific handlers additionally decode PageTokenV1 and bind its RPC,
// selector, primary key and first-page height. This shared envelope gate remains
// responsible only for the uniform limit and encoded-token byte caps.
func (q *queryServer) queryCaps(ctx context.Context) (taskQueryCaps, error) {
	params := q.k.hubKeeper.GetHubParams(sdkContextFrom(ctx))
	caps := taskQueryCaps{
		pageLimit:     params.MaxQueryPageLimit,
		pageTokenSize: params.MaxQueryPageTokenBytes,
		responseSize:  params.MaxQueryResponseBytes,
	}
	if caps.pageLimit == 0 || caps.pageTokenSize == 0 || caps.responseSize == 0 {
		return taskQueryCaps{}, status.Error(codes.Internal, "Hub query caps are missing or invalid")
	}
	return caps, nil
}

func resolveQueryPage(page shared.QueryPageRequestV1, caps taskQueryCaps) (uint32, []byte, error) {
	limit := page.Limit
	if limit == 0 {
		limit = caps.pageLimit
	}
	if limit > caps.pageLimit {
		return 0, nil, status.Errorf(codes.InvalidArgument, "pagination limit must be <= %d", caps.pageLimit)
	}
	if uint32(len(page.PageToken)) > caps.pageTokenSize {
		return 0, nil, status.Errorf(codes.InvalidArgument, "page_token must be <= %d bytes", caps.pageTokenSize)
	}
	return limit, page.PageToken, nil
}

// decodeStringPairQueryPageToken opens a §16.1 page token whose primary key is
// an (address, Hash32) pair — SessionByOwnerIndex and the two RoleActiveTask
// indexes. The second component is types.Hash32Key since X-16, so the returned
// cursor is the raw 32 bytes and feeds a range bound directly.
//
// The three rejections below are not redundant. Decode catches a token that is
// not a well-formed key at all and, because Hash32KeyCodec is fail-closed on
// width, one whose Hash32 half is the wrong length. The re-encode comparison
// catches a token that decodes but was not produced by this codec — a
// non-minimal length prefix on the address half decodes to the same pair yet is
// a different byte string, and accepting it would let one logical cursor have
// several encodings. The K1 comparison pins the token to the selector the caller
// actually asked about, so a token minted for one address cannot page another's
// rows.
//
// The hex round-trip that used to sit here ("does K2 re-encode to itself?") is
// gone with the string keys: canonicality of a raw 32-byte component is exactly
// its width, and the codec has already enforced that.
func (q *queryServer) decodeStringPairQueryPageToken(
	ctx context.Context,
	encoded []byte,
	rpcMethod string,
	expectedFirst string,
	keyCodec collectionscodec.KeyCodec[collections.Pair[string, types.Hash32Key]],
	selectorFields ...[]byte,
) (uint64, types.Hash32Key, []byte, []byte, error) {
	sdkCtx := sdkContextFrom(ctx)
	if sdkCtx.BlockHeight() < 0 {
		return 0, nil, nil, nil, status.Error(codes.Internal, "query block height is negative")
	}
	queryHeight := uint64(sdkCtx.BlockHeight())
	rpcDigest, selectorDigest, err := shared.QueryPageDigestsV1(sdkCtx.ChainID(), rpcMethod, selectorFields...)
	if err != nil {
		return 0, nil, nil, nil, status.Error(codes.Internal, err.Error())
	}
	if len(encoded) == 0 {
		return queryHeight, nil, rpcDigest, selectorDigest, nil
	}

	lastPrimaryKey, err := shared.DecodePageTokenV1(encoded, rpcDigest, selectorDigest, queryHeight)
	if err != nil {
		return 0, nil, nil, nil, status.Error(codes.InvalidArgument, err.Error())
	}

	read, key, err := keyCodec.Decode(lastPrimaryKey)
	if err != nil || read != len(lastPrimaryKey) || key.K1() != expectedFirst || len(key.K2()) != types.Hash32Len {
		return 0, nil, nil, nil, status.Error(codes.InvalidArgument, "page_token has a non-canonical primary key")
	}
	reencoded, err := encodeStringPairPrimaryKey(keyCodec, key)
	if err != nil || !bytes.Equal(reencoded, lastPrimaryKey) {
		return 0, nil, nil, nil, status.Error(codes.InvalidArgument, "page_token primary key is not canonically encoded")
	}
	return queryHeight, key.K2(), rpcDigest, selectorDigest, nil
}

func encodeStringPairPrimaryKey(
	keyCodec collectionscodec.KeyCodec[collections.Pair[string, types.Hash32Key]],
	key collections.Pair[string, types.Hash32Key],
) ([]byte, error) {
	encoded := make([]byte, keyCodec.Size(key))
	written, err := keyCodec.Encode(encoded, key)
	if err != nil {
		return nil, err
	}
	return encoded[:written], nil
}

func encodeStringPairQueryPageToken(rpcDigest, selectorDigest, lastPrimaryKey []byte, queryHeight uint64) ([]byte, error) {
	return shared.EncodePageTokenV1(rpcDigest, selectorDigest, lastPrimaryKey, queryHeight)
}

// requireQueryHash32 is the §16.1 selector gate: a malformed ID is
// InvalidArgument, never an empty page. It is the query-side twin of
// taskStoreKey / sessionStoreKey — same width check, different status code,
// because a bad selector is the caller's fault rather than a broken invariant.
func requireQueryHash32(name string, value []byte) (types.Hash32Key, error) {
	if len(value) != types.Hash32Len {
		return nil, status.Errorf(codes.InvalidArgument, "%s must be exactly %d bytes", name, types.Hash32Len)
	}
	return value, nil
}

func (q *queryServer) requireCanonicalAddress(name, value string) (string, error) {
	_, canonical, err := q.k.canonicalAddress(name, value)
	if err != nil {
		return "", status.Errorf(codes.InvalidArgument, "%s: %s", name, err.Error())
	}
	return canonical, nil
}

func (q *queryServer) Params(ctx context.Context, req *types.QueryTaskParamsRequest) (*types.QueryTaskParamsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	params, err := q.k.Params.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	meta, err := q.k.GetTaskParamsMeta(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &types.QueryTaskParamsResponse{Params: params, Meta: meta}, nil
}
