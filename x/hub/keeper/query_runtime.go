package keeper

import (
	"bytes"
	"context"
	"errors"

	"cosmossdk.io/collections"
	collectionscodec "cosmossdk.io/collections/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// The eight paginated Hub RPCs, named locally for the call sites below and
// defined once in the shared package, where shared.QueryPageSelectorSchemaV1 has
// to enumerate the same closed set anyway. These are aliases, not a second
// spelling: a method literal that existed here alone could drift from the
// selector schema and from the TRUEOPEN_QUERY_SELECTOR_V1 registry row without
// anything disagreeing.
const (
	freezeSignalsRPC        = shared.QueryRPCHubFreezeSignalsV1
	emergencyFreezeVotesRPC = shared.QueryRPCHubEmergencyFreezeVotesV1
	modelsRPC               = shared.QueryRPCHubModelsV1
	buildersRPC             = shared.QueryRPCHubBuildersV1
	candidatePoolMembersRPC = shared.QueryRPCHubCandidatePoolMembersV1
	serviceUnbondingsRPC    = shared.QueryRPCHubServiceUnbondingsV1
)

// pageTokenBudget is the token-and-bytes half of one page of any §16.1 keyset
// Query: the caps it must respect and the scope its next-page token binds to.
// Every paginated Hub Query builds one, so the cap is enforced the same way on
// all of them.
type pageTokenBudget struct {
	params         types.HubParamsV2
	rpcDigest      []byte
	selectorDigest []byte
	queryHeight    uint64
}

func (b pageTokenBudget) responseByteCap() int {
	return int(b.params.QueryEvent.MaxQueryResponseBytes)
}

func (b pageTokenBudget) nextPageToken(lastPrimaryKey []byte) ([]byte, error) {
	next, err := encodeQueryPageToken(b.rpcDigest, b.selectorDigest, lastPrimaryKey, b.queryHeight)
	if err != nil || uint32(len(next)) > b.params.QueryEvent.MaxQueryPageTokenBytes {
		return nil, status.Error(codes.Internal, "could not encode a bounded page token")
	}
	return next, nil
}

// fitRowsWithToken returns how many of the page's rows fit together with the
// next-page token that names the last of them, plus that token.
//
// max_query_response_bytes bounds the encoded response, and the token is part of
// that response. A row loop can only measure rows, because whether a token is
// needed at all is not known until the walk stops - so a page whose rows exactly
// fill the cap encodes past it the moment the token is attached. Measuring that
// only at the end turned it into Internal: a hard failure on data that pages
// perfectly well one row shorter. Giving rows back instead keeps the cap a real
// bound while still guaranteeing forward progress, because the retained rows are
// a non-empty prefix of the page and the token names the last of them, so the
// next page resumes exactly after it.
//
// keyAt returns the canonical store key of row i, which the walk already
// decoded; sizeWithToken encodes the first `rows` rows plus the token.
func (b pageTokenBudget) fitRowsWithToken(
	rowCount int,
	keyAt func(int) []byte,
	sizeWithToken func(rows int, token []byte) int,
) (int, []byte, error) {
	for rows := rowCount; rows > 0; rows-- {
		token, err := b.nextPageToken(keyAt(rows - 1))
		if err != nil {
			return 0, nil, err
		}
		if sizeWithToken(rows, token) <= b.responseByteCap() {
			return rows, token, nil
		}
	}
	return 0, nil, status.Error(codes.ResourceExhausted, "the response byte cap cannot hold one row and its page token")
}

// registryPageScope is the bounded page window of a registry discovery Query -
// one that walks a whole string-keyed primary map and therefore has no request
// selector at all. Because the request carries nothing but `page`, the selector
// digest binds only the chain id and the RPC digest: there is no selector to
// hash, and inventing a placeholder input would let a token minted for one
// scope verify under another.
//
// The scope deliberately stops at the page window. Row validation and iteration
// stay explicit in each handler, because what counts as a consistent row differs
// per collection and a shared "walk and validate" helper would have to take a
// callback per difference anyway.
type registryPageScope struct {
	pageTokenBudget
	limit    uint32
	lastKey  string
	resuming bool
}

// newRegistryPageScope resolves the caps, limit and page position of one discovery
// page. canonicalKey re-checks the key decoded from the token against the same
// field rule the collection is written under, so a token cannot smuggle a key
// shape the store could never hold.
func (q queryServer) newRegistryPageScope(
	ctx context.Context,
	rpc string,
	page shared.QueryPageRequestV1,
	keyCodec collectionscodec.KeyCodec[string],
	canonicalKey func(string) error,
) (registryPageScope, error) {
	params, limit, err := q.queryPageParams(ctx, page)
	if err != nil {
		return registryPageScope{}, err
	}
	queryHeight, rpcDigest, selectorDigest, err := queryPageDigests(sdk.UnwrapSDKContext(ctx), rpc)
	if err != nil {
		return registryPageScope{}, err
	}
	scope := registryPageScope{
		pageTokenBudget: pageTokenBudget{
			params: params, rpcDigest: rpcDigest, selectorDigest: selectorDigest, queryHeight: queryHeight,
		},
		limit: limit,
	}
	if len(page.PageToken) == 0 {
		return scope, nil
	}
	lastKey, err := decodeQueryPageToken(page.PageToken, rpcDigest, selectorDigest, queryHeight, keyCodec)
	if err != nil {
		return registryPageScope{}, err
	}
	if canonicalKey(lastKey) != nil {
		return registryPageScope{}, status.Error(codes.InvalidArgument, "page_token has a non-canonical registry key")
	}
	scope.lastKey, scope.resuming = lastKey, true
	return scope, nil
}

// walkRange reads the primary map forward only. A resumed page starts strictly
// after the previous page's last key, so no row is returned twice and none is
// skipped; there is no offset and no whole-table pre-sort.
func (s registryPageScope) walkRange() *collections.Range[string] {
	if !s.resuming {
		return new(collections.Range[string])
	}
	return new(collections.Range[string]).StartExclusive(s.lastKey)
}

// pageKeysAt adapts the encoded keys a walk collected to fitRowsWithToken.
func pageKeysAt(keys [][]byte) func(int) []byte {
	return func(index int) []byte { return keys[index] }
}

func (q queryServer) Params(ctx context.Context, req *types.QueryHubParamsRequest) (*types.QueryHubParamsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	params, err := q.k.Params.Get(ctx)
	if err != nil && !errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.Internal, "internal error")
	}
	// meta is what makes MsgUpdateHubParams usable more than once: its
	// expected_version is optimistic concurrency against params_version, and this
	// query is the only way a client can learn the current value. Leaving it at
	// the zero value reports version 0 forever, so every update after the first
	// is rejected with ErrHubParamsVersionMismatch. task.v1.Query/Params
	// already answers with its own Meta; this is the same contract.
	meta, err := q.k.GetHubParamsMeta(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "internal error")
	}
	return &types.QueryHubParamsResponse{Params: params, Meta: meta}, nil
}

func (q queryServer) FreezeSignal(ctx context.Context, req *types.QueryFreezeSignalRequest) (*types.QueryFreezeSignalResponse, error) {
	if req == nil || len(req.FreezeSignalId) != 32 {
		return nil, status.Error(codes.InvalidArgument, "freeze_signal_id must be Hash32 bytes")
	}
	signal, err := q.k.FreezeSignalState.Get(ctx, req.FreezeSignalId)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "freeze signal not found")
	}
	if err != nil || !validFreezeSignalHeader(signal, req.FreezeSignalId) {
		return nil, status.Error(codes.Internal, "invalid freeze signal state")
	}
	response := &types.QueryFreezeSignalResponse{Signal: signal}
	if err := q.enforceFreezeResponseCap(ctx, response.Size()); err != nil {
		return nil, err
	}
	return response, nil
}

func (q queryServer) FreezeSignals(ctx context.Context, req *types.QueryFreezeSignalsRequest) (*types.QueryFreezeSignalsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	modelID, profileVersion, err := validateModelProfileQueryScope(req.ModelId, req.ProfileVersion)
	if err != nil || !isQueryableFreezeSignalStatus(req.Status) {
		return nil, status.Error(codes.InvalidArgument, "canonical profile scope and a retained freeze signal status are required")
	}
	if _, err := q.k.Profile.Get(ctx, types.NewProfileStateKey(modelID, profileVersion)); errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "profile not found")
	} else if err != nil {
		return nil, status.Error(codes.Internal, "profile unavailable")
	}
	params, limit, err := q.queryPageParams(ctx, req.Page)
	if err != nil {
		return nil, err
	}
	queryHeight, rpcDigest, selectorDigest, lastKey, err := q.decodeFreezeSignalPageToken(ctx, req)
	if err != nil {
		return nil, err
	}
	var rangeValue collections.Ranger[types.FreezeSignalByProfileKey] = collections.NewSuperPrefixedQuadRange3[string, uint32, int32, types.FreezeSignalWindowOrderKey](modelID, profileVersion, int32(req.Status))
	if lastKey != nil {
		// The trailing signal_id used to be spelled as an empty slice, which BytesKey
		// encoded to zero bytes; Hash32KeyCodec is fixed width and rejects it. The
		// smallest 32-byte value is the equivalent bound: the status component is
		// already one greater, so every excluded key is excluded either way.
		nextStatus := collections.Join4(modelID, profileVersion, int32(req.Status)+1, collections.Join(uint64(0), lowestFreezeSignalID()))
		rangeValue = new(collections.Range[types.FreezeSignalByProfileKey]).StartExclusive(*lastKey).EndExclusive(nextStatus)
	}
	iter, err := q.k.FreezeSignalByProfileIndex.Iterate(ctx, rangeValue)
	if err != nil {
		return nil, status.Error(codes.Internal, "freeze signal index unavailable")
	}
	defer iter.Close()
	budget := pageTokenBudget{params: params, rpcDigest: rpcDigest, selectorDigest: selectorDigest, queryHeight: queryHeight}
	response := &types.QueryFreezeSignalsResponse{Signals: []types.FreezeSignalState{}}
	var pageKeys [][]byte
	more := false
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, status.Error(codes.Internal, "freeze signal index key unavailable")
		}
		if uint32(len(response.Signals)) >= limit {
			more = true
			break
		}
		signalID := key.K4().K2()
		signal, err := q.k.FreezeSignalState.Get(ctx, signalID)
		if err != nil || !validFreezeSignalHeader(signal, signalID) || signal.ModelId != modelID || signal.ProfileVersion != profileVersion || signal.SignalStatus != req.Status || signal.RiskWindowEndHeight != key.K4().K1() {
			return nil, status.Error(codes.Internal, "freeze signal index disagrees with primary state")
		}
		candidate := append(response.Signals, signal)
		if (&types.QueryFreezeSignalsResponse{Signals: candidate}).Size() > budget.responseByteCap() {
			if len(response.Signals) == 0 {
				return nil, status.Error(codes.Internal, "one freeze signal exceeds the response byte cap")
			}
			more = true
			break
		}
		pageKey, err := encodeCollectionKey(q.k.FreezeSignalByProfileIndex.KeyCodec(), key)
		if err != nil {
			return nil, status.Error(codes.Internal, "could not encode freeze signal page key")
		}
		response.Signals, pageKeys = candidate, append(pageKeys, pageKey)
	}
	if !more {
		return response, nil
	}
	rows, next, err := budget.fitRowsWithToken(len(response.Signals), pageKeysAt(pageKeys),
		func(rows int, token []byte) int {
			return (&types.QueryFreezeSignalsResponse{
				Signals: response.Signals[:rows], Page: shared.QueryPageResponseV1{NextPageToken: token},
			}).Size()
		})
	if err != nil {
		return nil, err
	}
	response.Signals = response.Signals[:rows]
	response.Page = shared.QueryPageResponseV1{NextPageToken: next}
	return response, nil
}

func (q queryServer) EmergencyFreezeVotes(ctx context.Context, req *types.QueryEmergencyFreezeVotesRequest) (*types.QueryEmergencyFreezeVotesResponse, error) {
	if req == nil || len(req.FreezeSignalId) != 32 {
		return nil, status.Error(codes.InvalidArgument, "freeze_signal_id must be Hash32 bytes")
	}
	if signal, err := q.k.FreezeSignalState.Get(ctx, req.FreezeSignalId); errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "freeze signal not found")
	} else if err != nil || !validFreezeSignalHeader(signal, req.FreezeSignalId) {
		return nil, status.Error(codes.Internal, "invalid freeze signal state")
	}
	params, limit, err := q.queryPageParams(ctx, req.Page)
	if err != nil {
		return nil, err
	}
	queryHeight, rpcDigest, selectorDigest, lastKey, err := q.decodeEmergencyFreezeVotePageToken(ctx, req)
	if err != nil {
		return nil, err
	}
	rangeValue := collections.NewPrefixedPairRange[[]byte, []byte](req.FreezeSignalId)
	if lastKey != nil {
		rangeValue.StartExclusive(lastKey.K2())
	}
	iter, err := q.k.EmergencyFreezeVoteState.Iterate(ctx, rangeValue)
	if err != nil {
		return nil, status.Error(codes.Internal, "emergency freeze vote store unavailable")
	}
	defer iter.Close()
	budget := pageTokenBudget{params: params, rpcDigest: rpcDigest, selectorDigest: selectorDigest, queryHeight: queryHeight}
	response := &types.QueryEmergencyFreezeVotesResponse{Votes: []types.EmergencyFreezeVoteState{}}
	var pageKeys [][]byte
	more := false
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, status.Error(codes.Internal, "emergency freeze vote key unavailable")
		}
		if uint32(len(response.Votes)) >= limit {
			more = true
			break
		}
		vote, err := iter.Value()
		if err != nil || !bytes.Equal(vote.FreezeSignalId, key.K1()) || !bytes.Equal(vote.ValidatorConsensusAddress, key.K2()) || vote.VotingPowerSnapshot == 0 || (vote.Vote != types.EmergencyFreezeVote_EMERGENCY_FREEZE_VOTE_ACCEPT && vote.Vote != types.EmergencyFreezeVote_EMERGENCY_FREEZE_VOTE_REJECT) {
			return nil, status.Error(codes.Internal, "emergency freeze vote key disagrees with primary state")
		}
		candidate := append(response.Votes, vote)
		if (&types.QueryEmergencyFreezeVotesResponse{Votes: candidate}).Size() > budget.responseByteCap() {
			if len(response.Votes) == 0 {
				return nil, status.Error(codes.Internal, "one emergency freeze vote exceeds the response byte cap")
			}
			more = true
			break
		}
		pageKey, err := encodeCollectionKey(q.k.EmergencyFreezeVoteState.KeyCodec(), key)
		if err != nil {
			return nil, status.Error(codes.Internal, "could not encode emergency freeze vote page key")
		}
		response.Votes, pageKeys = candidate, append(pageKeys, pageKey)
	}
	if !more {
		return response, nil
	}
	rows, next, err := budget.fitRowsWithToken(len(response.Votes), pageKeysAt(pageKeys),
		func(rows int, token []byte) int {
			return (&types.QueryEmergencyFreezeVotesResponse{
				Votes: response.Votes[:rows], Page: shared.QueryPageResponseV1{NextPageToken: token},
			}).Size()
		})
	if err != nil {
		return nil, err
	}
	response.Votes = response.Votes[:rows]
	response.Page = shared.QueryPageResponseV1{NextPageToken: next}
	return response, nil
}

func (q queryServer) queryPageParams(ctx context.Context, page shared.QueryPageRequestV1) (types.HubParamsV2, uint32, error) {
	params, err := q.k.Params.Get(ctx)
	if err != nil {
		return types.HubParamsV2{}, 0, status.Error(codes.Internal, "params unavailable")
	}
	limit := page.Limit
	if limit == 0 {
		limit = params.QueryEvent.MaxQueryPageLimit
	}
	if params.QueryEvent.MaxQueryPageLimit == 0 || limit == 0 || limit > params.QueryEvent.MaxQueryPageLimit {
		return types.HubParamsV2{}, 0, status.Error(codes.InvalidArgument, "page limit exceeds the configured maximum")
	}
	if uint32(len(page.PageToken)) > params.QueryEvent.MaxQueryPageTokenBytes {
		return types.HubParamsV2{}, 0, status.Error(codes.InvalidArgument, "page_token exceeds the configured maximum")
	}
	if params.QueryEvent.MaxQueryResponseBytes == 0 {
		return types.HubParamsV2{}, 0, status.Error(codes.Internal, "query response byte cap is zero")
	}
	return params, limit, nil
}

func (q queryServer) enforceFreezeResponseCap(ctx context.Context, size int) error {
	params, err := q.k.Params.Get(ctx)
	if err != nil || params.QueryEvent.MaxQueryResponseBytes == 0 {
		return status.Error(codes.Internal, "query response byte cap unavailable")
	}
	if size > int(params.QueryEvent.MaxQueryResponseBytes) {
		return status.Error(codes.Internal, "freeze response exceeds the configured byte cap")
	}
	return nil
}

func (q queryServer) decodeFreezeSignalPageToken(ctx context.Context, req *types.QueryFreezeSignalsRequest) (uint64, []byte, []byte, *types.FreezeSignalByProfileKey, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	queryHeight, rpcDigest, selectorDigest, err := queryPageDigests(sdkCtx, freezeSignalsRPC, []byte(req.ModelId), shared.Uint32BE(req.ProfileVersion), shared.EnumBE(uint32(req.Status)))
	if err != nil || len(req.Page.PageToken) == 0 {
		return queryHeight, rpcDigest, selectorDigest, nil, err
	}
	key, err := decodeQueryPageToken(req.Page.PageToken, rpcDigest, selectorDigest, queryHeight, q.k.FreezeSignalByProfileIndex.KeyCodec())
	if err != nil || key.K1() != req.ModelId || key.K2() != req.ProfileVersion || key.K3() != int32(req.Status) || len(key.K4().K2()) != 32 {
		return 0, nil, nil, nil, status.Error(codes.InvalidArgument, "page_token has a non-canonical freeze signal key")
	}
	return queryHeight, rpcDigest, selectorDigest, &key, nil
}

func (q queryServer) decodeEmergencyFreezeVotePageToken(ctx context.Context, req *types.QueryEmergencyFreezeVotesRequest) (uint64, []byte, []byte, *types.EmergencyFreezeVoteKey, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	queryHeight, rpcDigest, selectorDigest, err := queryPageDigests(sdkCtx, emergencyFreezeVotesRPC, req.FreezeSignalId)
	if err != nil || len(req.Page.PageToken) == 0 {
		return queryHeight, rpcDigest, selectorDigest, nil, err
	}
	key, err := decodeQueryPageToken(req.Page.PageToken, rpcDigest, selectorDigest, queryHeight, q.k.EmergencyFreezeVoteState.KeyCodec())
	if err != nil || !bytes.Equal(key.K1(), req.FreezeSignalId) || len(key.K2()) == 0 {
		return 0, nil, nil, nil, status.Error(codes.InvalidArgument, "page_token has a non-canonical emergency freeze vote key")
	}
	return queryHeight, rpcDigest, selectorDigest, &key, nil
}

// queryPageDigests adds the query height to the (TRUEOPEN_QUERY_RPC_V1,
// TRUEOPEN_QUERY_SELECTOR_V1) pair that shared.QueryPageDigestsV1 produces for every
// paginated Query in the repository.
//
// The two digests themselves used to be assembled here, and the Task module had
// two more copies of the same twelve lines. They now come from the one shared
// producer, which also checks the RPC and its selector arity against
// shared.QueryPageSelectorSchemaV1; the bytes are unchanged, and the frozen
// per-RPC token vectors are what says so.
func queryPageDigests(ctx sdk.Context, rpc string, selectors ...[]byte) (uint64, []byte, []byte, error) {
	if ctx.BlockHeight() < 0 {
		return 0, nil, nil, status.Error(codes.Internal, "query block height is negative")
	}
	rpcDigest, selectorDigest, err := shared.QueryPageDigestsV1(ctx.ChainID(), rpc, selectors...)
	if err != nil {
		return 0, nil, nil, status.Error(codes.Internal, err.Error())
	}
	return uint64(ctx.BlockHeight()), rpcDigest, selectorDigest, nil
}

func decodeQueryPageToken[K any](encoded, rpcDigest, selectorDigest []byte, queryHeight uint64, codec collectionscodec.KeyCodec[K]) (K, error) {
	var zero K
	lastPrimaryKey, err := shared.DecodePageTokenV1(encoded, rpcDigest, selectorDigest, queryHeight)
	if err != nil {
		return zero, status.Error(codes.InvalidArgument, err.Error())
	}
	read, key, err := codec.Decode(lastPrimaryKey)
	if err != nil || read != len(lastPrimaryKey) {
		return zero, status.Error(codes.InvalidArgument, "page_token primary key is invalid")
	}
	reencoded, err := encodeCollectionKey(codec, key)
	if err != nil || !bytes.Equal(reencoded, lastPrimaryKey) {
		return zero, status.Error(codes.InvalidArgument, "page_token primary key is not canonically encoded")
	}
	return key, nil
}

func encodeQueryPageToken(rpcDigest, selectorDigest, lastPrimaryKey []byte, queryHeight uint64) ([]byte, error) {
	if len(lastPrimaryKey) == 0 {
		return nil, errors.New("pagination made no forward progress")
	}
	return shared.EncodePageTokenV1(rpcDigest, selectorDigest, lastPrimaryKey, queryHeight)
}

func encodeCollectionKey[K any](codec collectionscodec.KeyCodec[K], key K) ([]byte, error) {
	encoded := make([]byte, codec.Size(key))
	written, err := codec.Encode(encoded, key)
	if err != nil {
		return nil, err
	}
	return encoded[:written], nil
}

func isQueryableFreezeSignalStatus(value types.FreezeSignalStatus) bool {
	return value == types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_OPEN || value == types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_ACCEPTED || value == types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_REJECTED || value == types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_EXPIRED
}

func validFreezeSignalHeader(signal types.FreezeSignalState, key []byte) bool {
	if len(key) != 32 || !bytes.Equal(signal.FreezeSignalId, key) || !isQueryableFreezeSignalStatus(signal.SignalStatus) || signal.ModelId == "" || signal.ProfileVersion == 0 || len(signal.IncludedFailureTaskRefsHash) != 32 || len(signal.ValidatorSetHash) != 32 || signal.TotalVotingPowerSnapshot == 0 {
		return false
	}
	return signal.AcceptedVotingPower <= signal.TotalVotingPowerSnapshot && signal.RejectedVotingPower <= signal.TotalVotingPowerSnapshot-signal.AcceptedVotingPower
}

// lowestFreezeSignalID is the smallest value the fixed-width freeze_signal_id
// component can take, used where a range bound previously used an empty slice.
func lowestFreezeSignalID() shared.Hash32Key {
	return make(shared.Hash32Key, shared.Hash32KeySize)
}
