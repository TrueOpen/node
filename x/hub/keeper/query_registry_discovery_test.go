package keeper_test

import (
	"bytes"
	"sort"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// Models and Builders are the only enumeration surfaces of the two Hub
// registries, so these tests pin what a client may rely on: canonical key
// order, a token that is honoured only inside its own RPC/chain/height scope,
// and a broken row failing the page rather than being skipped.

func TestQueryModelsWalksRegistryInCanonicalKeyOrder(t *testing.T) {
	f, queries := newRegistryDiscoveryFixture(t)
	// Seeded out of order on purpose: the walk must return key order, not
	// insertion order.
	for _, modelID := range []string{"model-c", "model-a", "model-b0", "model-b"} {
		putModelListState(t, f, modelListState(modelID, types.ModelStatusActive, hubAddress(t, 240), 1))
	}

	response, err := queries.Models(f.ctx, &types.QueryModelsRequest{})
	require.NoError(t, err)
	require.Empty(t, response.Page.NextPageToken)
	require.Equal(t, []string{"model-a", "model-b", "model-b0", "model-c"}, modelIDsOf(response.Models))
}

func TestQueryBuildersWalksRegistryInCanonicalKeyOrder(t *testing.T) {
	f, queries := newRegistryDiscoveryFixture(t)
	seeded := seedBuilderRegistry(t, f, 201, 202, 203)

	response, err := queries.Builders(f.ctx, &types.QueryBuildersRequest{})
	require.NoError(t, err)
	require.Empty(t, response.Page.NextPageToken)
	require.Equal(t, sortedStrings(seeded), builderAddressesOf(response.Builders))
}

// An empty registry is an empty list with an empty token, not NotFound: the
// registry itself always exists, so NotFound would be indistinguishable from a
// bad selector on a query that has no selector to get wrong.
func TestRegistryDiscoveryQueriesReturnEmptyPageForEmptyRegistry(t *testing.T) {
	f, queries := newRegistryDiscoveryFixture(t)

	modelResponse, err := queries.Models(f.ctx, &types.QueryModelsRequest{})
	require.NoError(t, err)
	require.Empty(t, modelResponse.Models)
	require.Empty(t, modelResponse.Page.NextPageToken)

	builderResponse, err := queries.Builders(f.ctx, &types.QueryBuildersRequest{})
	require.NoError(t, err)
	require.Empty(t, builderResponse.Builders)
	require.Empty(t, builderResponse.Page.NextPageToken)
}

func TestRegistryDiscoveryQueriesRejectNilRequest(t *testing.T) {
	f, queries := newRegistryDiscoveryFixture(t)

	_, err := queries.Models(f.ctx, nil)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = queries.Builders(f.ctx, nil)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// Paging must cover the registry exactly once: every row appears, in order, and
// the final page carries no token even though it is full.
func TestQueryModelsPagesEveryRowExactlyOnce(t *testing.T) {
	f, queries := newRegistryDiscoveryFixture(t)
	want := []string{"model-a", "model-b", "model-c", "model-d", "model-e", "model-f"}
	for _, modelID := range want {
		putModelListState(t, f, modelListState(modelID, types.ModelStatusActive, hubAddress(t, 240), 1))
	}

	var got []string
	var token []byte
	for page := 0; ; page++ {
		require.Less(t, page, len(want), "paging did not terminate")
		response, err := queries.Models(f.ctx, &types.QueryModelsRequest{
			Page: shared.QueryPageRequestV1{Limit: 2, PageToken: token},
		})
		require.NoError(t, err)
		require.LessOrEqual(t, len(response.Models), 2)
		got = append(got, modelIDsOf(response.Models)...)
		token = response.Page.NextPageToken
		if len(token) == 0 {
			break
		}
	}
	require.Equal(t, want, got)
}

func TestQueryBuildersPagesEveryRowExactlyOnce(t *testing.T) {
	f, queries := newRegistryDiscoveryFixture(t)
	want := sortedStrings(seedBuilderRegistry(t, f, 211, 212, 213, 214, 215))

	var got []string
	var token []byte
	for page := 0; ; page++ {
		require.Less(t, page, len(want), "paging did not terminate")
		response, err := queries.Builders(f.ctx, &types.QueryBuildersRequest{
			Page: shared.QueryPageRequestV1{Limit: 2, PageToken: token},
		})
		require.NoError(t, err)
		require.LessOrEqual(t, len(response.Builders), 2)
		got = append(got, builderAddressesOf(response.Builders)...)
		token = response.Page.NextPageToken
		if len(token) == 0 {
			break
		}
	}
	require.Equal(t, want, got)
}

// A page whose limit exactly consumes the registry must not mint a token: the
// server has not seen a next row, and a client that trusted the token would
// make one wasted request per walk.
func TestRegistryDiscoveryLastFullPageReturnsNoToken(t *testing.T) {
	f, queries := newRegistryDiscoveryFixture(t)
	putModelListState(t, f, modelListState("model-a", types.ModelStatusActive, hubAddress(t, 240), 1))
	putModelListState(t, f, modelListState("model-b", types.ModelStatusActive, hubAddress(t, 240), 1))

	response, err := queries.Models(f.ctx, &types.QueryModelsRequest{Page: shared.QueryPageRequestV1{Limit: 2}})
	require.NoError(t, err)
	require.Len(t, response.Models, 2)
	require.Empty(t, response.Page.NextPageToken)
}

func TestRegistryDiscoveryLimitBoundaries(t *testing.T) {
	f, queries := newRegistryDiscoveryFixture(t)
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	maxLimit := params.QueryEvent.MaxQueryPageLimit
	require.NotZero(t, maxLimit)
	putModelListState(t, f, modelListState("model-a", types.ModelStatusActive, hubAddress(t, 240), 1))
	seedBuilderRegistry(t, f, 231)

	// limit = 0 means max_query_page_limit, not "no rows".
	models, err := queries.Models(f.ctx, &types.QueryModelsRequest{Page: shared.QueryPageRequestV1{Limit: 0}})
	require.NoError(t, err)
	require.Len(t, models.Models, 1)
	builders, err := queries.Builders(f.ctx, &types.QueryBuildersRequest{Page: shared.QueryPageRequestV1{Limit: 0}})
	require.NoError(t, err)
	require.Len(t, builders.Builders, 1)

	// The cap itself is accepted.
	_, err = queries.Models(f.ctx, &types.QueryModelsRequest{Page: shared.QueryPageRequestV1{Limit: maxLimit}})
	require.NoError(t, err)
	_, err = queries.Builders(f.ctx, &types.QueryBuildersRequest{Page: shared.QueryPageRequestV1{Limit: maxLimit}})
	require.NoError(t, err)

	// One past the cap is rejected, never silently clamped.
	_, err = queries.Models(f.ctx, &types.QueryModelsRequest{Page: shared.QueryPageRequestV1{Limit: maxLimit + 1}})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = queries.Builders(f.ctx, &types.QueryBuildersRequest{Page: shared.QueryPageRequestV1{Limit: maxLimit + 1}})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestRegistryDiscoveryRejectsOversizedAndMalformedTokens(t *testing.T) {
	f, queries := newRegistryDiscoveryFixture(t)
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	oversized := bytes.Repeat([]byte{0x01}, int(params.QueryEvent.MaxQueryPageTokenBytes)+1)

	for name, token := range map[string][]byte{
		"oversized":    oversized,
		"not-protobuf": {0xff, 0xff, 0xff, 0xff},
		"foreign-digests": mustMarshalPageToken(t, &shared.PageTokenV1{
			RpcMethodDigest: bytes.Repeat([]byte{0x01}, 32),
			SelectorDigest:  bytes.Repeat([]byte{0x02}, 32),
			LastPrimaryKey:  []byte("model-a"),
		}),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := queries.Models(f.ctx, &types.QueryModelsRequest{Page: shared.QueryPageRequestV1{PageToken: token}})
			require.Equal(t, codes.InvalidArgument, status.Code(err))
			_, err = queries.Builders(f.ctx, &types.QueryBuildersRequest{Page: shared.QueryPageRequestV1{PageToken: token}})
			require.Equal(t, codes.InvalidArgument, status.Code(err))
		})
	}
}

// A token binds to one RPC, one chain and one query height. Accepting it
// elsewhere would let a caller resume a walk against state it never read.
func TestRegistryDiscoveryTokensAreScopedToRPCChainAndHeight(t *testing.T) {
	f, queries := newRegistryDiscoveryFixture(t)
	for _, modelID := range []string{"model-a", "model-b"} {
		putModelListState(t, f, modelListState(modelID, types.ModelStatusActive, hubAddress(t, 240), 1))
	}
	seedBuilderRegistry(t, f, 241, 242)

	modelPage, err := queries.Models(f.ctx, &types.QueryModelsRequest{Page: shared.QueryPageRequestV1{Limit: 1}})
	require.NoError(t, err)
	require.NotEmpty(t, modelPage.Page.NextPageToken)
	builderPage, err := queries.Builders(f.ctx, &types.QueryBuildersRequest{Page: shared.QueryPageRequestV1{Limit: 1}})
	require.NoError(t, err)
	require.NotEmpty(t, builderPage.Page.NextPageToken)

	t.Run("cross-rpc", func(t *testing.T) {
		_, err := queries.Builders(f.ctx, &types.QueryBuildersRequest{
			Page: shared.QueryPageRequestV1{PageToken: modelPage.Page.NextPageToken},
		})
		require.Equal(t, codes.InvalidArgument, status.Code(err))
		_, err = queries.Models(f.ctx, &types.QueryModelsRequest{
			Page: shared.QueryPageRequestV1{PageToken: builderPage.Page.NextPageToken},
		})
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("cross-chain", func(t *testing.T) {
		otherChain := sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithChainID("trueopen-hub-other"))
		_, err := queries.Models(otherChain, &types.QueryModelsRequest{
			Page: shared.QueryPageRequestV1{PageToken: modelPage.Page.NextPageToken},
		})
		require.Equal(t, codes.InvalidArgument, status.Code(err))
		_, err = queries.Builders(otherChain, &types.QueryBuildersRequest{
			Page: shared.QueryPageRequestV1{PageToken: builderPage.Page.NextPageToken},
		})
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("cross-height", func(t *testing.T) {
		sdkCtx := sdk.UnwrapSDKContext(f.ctx)
		laterHeight := sdk.WrapSDKContext(sdkCtx.WithBlockHeight(sdkCtx.BlockHeight() + 1))
		_, err := queries.Models(laterHeight, &types.QueryModelsRequest{
			Page: shared.QueryPageRequestV1{PageToken: modelPage.Page.NextPageToken},
		})
		require.Equal(t, codes.InvalidArgument, status.Code(err))
		_, err = queries.Builders(laterHeight, &types.QueryBuildersRequest{
			Page: shared.QueryPageRequestV1{PageToken: builderPage.Page.NextPageToken},
		})
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

// A token whose last_primary_key could never be a store key is rejected before
// the walk, so a caller cannot use the token as a free-form range selector.
func TestRegistryDiscoveryRejectsNonCanonicalTokenKeys(t *testing.T) {
	f, queries := newRegistryDiscoveryFixture(t)
	sdkCtx := sdk.UnwrapSDKContext(f.ctx)

	_, err := queries.Models(f.ctx, &types.QueryModelsRequest{Page: shared.QueryPageRequestV1{
		PageToken: mustDiscoveryPageToken(t, sdkCtx, "/hub.v1.Query/Models", []byte("Model/Bad")),
	}})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = queries.Builders(f.ctx, &types.QueryBuildersRequest{Page: shared.QueryPageRequestV1{
		PageToken: mustDiscoveryPageToken(t, sdkCtx, "/hub.v1.Query/Builders", []byte("not-a-bech32-address")),
	}})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

// A single row wider than the response cap means the write-side bound or the
// encoding invariant is already broken. Returning half a row or an empty page
// would hide that, so the walk fails.
func TestRegistryDiscoveryRejectsRowWiderThanResponseCap(t *testing.T) {
	f, queries := newRegistryDiscoveryFixture(t)
	putModelListState(t, f, modelListState("model-a", types.ModelStatusActive, hubAddress(t, 240), 1))
	seedBuilderRegistry(t, f, 251)
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	params.QueryEvent.MaxQueryResponseBytes = 1
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))

	_, err = queries.Models(f.ctx, &types.QueryModelsRequest{})
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
	_, err = queries.Builders(f.ctx, &types.QueryBuildersRequest{})
	require.Equal(t, codes.ResourceExhausted, status.Code(err))
}

// The byte cap bounds the whole serialized response, page token included. The
// walk must give a row back rather than encode past the cap or fail the page,
// and it must still make forward progress.
func TestRegistryDiscoveryStopsAtResponseByteCapAndKeepsPaging(t *testing.T) {
	f, queries := newRegistryDiscoveryFixture(t)
	want := []string{"model-a", "model-b", "model-c", "model-d", "model-e", "model-f"}
	for _, modelID := range want {
		putModelListState(t, f, modelListState(modelID, types.ModelStatusActive, hubAddress(t, 240), 1))
	}
	full, err := queries.Models(f.ctx, &types.QueryModelsRequest{})
	require.NoError(t, err)
	require.Equal(t, want, modelIDsOf(full.Models))

	// One byte above four rows: the row loop admits four, its token then does not
	// fit, so a correct handler returns fewer rows with a usable token.
	byteCap := (&types.QueryModelsResponse{Models: full.Models[:4]}).Size() + 1
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	params.QueryEvent.MaxQueryResponseBytes = uint64(byteCap)
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))

	var got []string
	var token []byte
	for page := 0; ; page++ {
		require.Less(t, page, len(want), "paging did not terminate")
		response, err := queries.Models(f.ctx, &types.QueryModelsRequest{
			Page: shared.QueryPageRequestV1{PageToken: token},
		})
		require.NoError(t, err)
		require.NotEmpty(t, response.Models, "every page must make forward progress")
		require.LessOrEqual(t, response.Size(), byteCap, "the cap must bound the response including its token")
		if page == 0 {
			require.Less(t, len(response.Models), 4, "the first page must give a row back to fit its token")
		}
		got = append(got, modelIDsOf(response.Models)...)
		token = response.Page.NextPageToken
		if len(token) == 0 {
			break
		}
	}
	require.Equal(t, want, got)
}

// A row whose body disagrees with its key, or whose invariants no longer hold,
// is an Internal failure of the whole page. Skipping it would publish a
// registry view that silently omits rows.
func TestQueryModelsFailsOnCorruptRow(t *testing.T) {
	f, queries := newRegistryDiscoveryFixture(t)
	corrupt := modelListState("model-a", types.ModelStatusActive, hubAddress(t, 240), 1)
	corrupt.ModelId = "model-b"
	require.NoError(t, f.keeper.Model.Set(f.ctx, "model-a", corrupt))

	_, err := queries.Models(f.ctx, &types.QueryModelsRequest{})
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestQueryBuildersFailsOnCorruptRow(t *testing.T) {
	for name, corrupt := range map[string]func(*types.BuilderState){
		"key disagrees with body": func(b *types.BuilderState) { b.BuilderAddress = hubAddress(t, 99) },
		"unset schema version":    func(b *types.BuilderState) { b.SchemaVersion = 0 },
		"unset service key status": func(b *types.BuilderState) {
			b.CurrentServiceKeyStatus = types.ServiceKeyStatus_SERVICE_KEY_STATUS_UNSPECIFIED
		},
		"pubkey does not derive service address": func(b *types.BuilderState) {
			b.CurrentServicePubkey = mustHubIdentity(199).PubKey
		},
	} {
		t.Run(name, func(t *testing.T) {
			f, queries := newRegistryDiscoveryFixture(t)
			address := hubAddress(t, 61)
			state := builderRegistryState(t, address, 61)
			corrupt(&state)
			require.NoError(t, f.keeper.Builder.Set(f.ctx, address, state))

			_, err := queries.Builders(f.ctx, &types.QueryBuildersRequest{})
			require.Equal(t, codes.Internal, status.Code(err))
		})
	}
}

// A revoked service key is a live registry row: MsgRevokeServiceKey keeps the
// binding and only flips the status, so the walk must still return it.
func TestQueryBuildersReturnsRevokedServiceKeyRows(t *testing.T) {
	f, queries := newRegistryDiscoveryFixture(t)
	address := hubAddress(t, 62)
	state := builderRegistryState(t, address, 62)
	state.CurrentServiceKeyStatus = types.ServiceKeyStatus_SERVICE_KEY_STATUS_REVOKED
	require.NoError(t, f.keeper.Builder.Set(f.ctx, address, state))

	response, err := queries.Builders(f.ctx, &types.QueryBuildersRequest{})
	require.NoError(t, err)
	require.Equal(t, []string{address}, builderAddressesOf(response.Builders))
}

func newRegistryDiscoveryFixture(t *testing.T) (*fixture, types.QueryServer) {
	t.Helper()
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	return f, keeper.NewQueryServerImpl(f.keeper)
}

func seedBuilderRegistry(t *testing.T, f *fixture, fills ...byte) []string {
	t.Helper()
	addresses := make([]string, 0, len(fills))
	for _, fill := range fills {
		address := hubAddress(t, fill)
		require.NoError(t, f.keeper.Builder.Set(f.ctx, address, builderRegistryState(t, address, fill)))
		addresses = append(addresses, address)
	}
	return addresses
}

func builderRegistryState(t *testing.T, address string, fill byte) types.BuilderState {
	t.Helper()
	identity := mustHubIdentity(fill)
	return types.BuilderState{
		SchemaVersion: 1, BuilderAddress: address,
		CurrentServiceAddress: identity.Address, CurrentServicePubkey: identity.PubKey,
		ServiceAuthorizationNonce: 1, CurrentServiceKeyStatus: types.ServiceKeyStatusActive,
		RegisteredHeight: 1, CurrentDescriptorVersion: 1,
	}
}

// mustDiscoveryPageToken mints the token a no-selector discovery Query would
// accept, so a test can vary last_primary_key alone.
func mustDiscoveryPageToken(t *testing.T, ctx sdk.Context, rpc string, lastPrimaryKey []byte) []byte {
	t.Helper()
	rpcDigest := shared.CanonicalHashBytes(shared.MustDomain(shared.DomainQueryRPCV1), []byte(rpc))
	selectorDigest := shared.CanonicalHashBytes(
		shared.MustDomain(shared.DomainQuerySelectorV1), []byte(ctx.ChainID()), rpcDigest,
	)
	token, err := shared.EncodePageTokenV1(rpcDigest, selectorDigest, lastPrimaryKey, uint64(ctx.BlockHeight()))
	require.NoError(t, err)
	return token
}

func mustMarshalPageToken(t *testing.T, token *shared.PageTokenV1) []byte {
	t.Helper()
	raw, err := proto.Marshal(token)
	require.NoError(t, err)
	return raw
}

func modelIDsOf(models []types.ModelState) []string {
	out := make([]string, 0, len(models))
	for _, model := range models {
		out = append(out, model.ModelId)
	}
	return out
}

func builderAddressesOf(builders []types.BuilderState) []string {
	out := make([]string, 0, len(builders))
	for _, builder := range builders {
		out = append(out, builder.BuilderAddress)
	}
	return out
}

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}
