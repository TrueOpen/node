package app

// gRPC query smoke through the real ABCI query router.
//
// The other the Keeper query integration tests call NewQueryServerImpl
// directly, which is the fastest way to assert handler behaviour but
// bypasses the layer a live gRPC client (and, transitively, the
// proto-annotation-driven REST gateway) actually hits: the baseapp
// grpcQueryRouter, the InterfaceRegistry, and the protobuf binary
// (un)marshal of request/response over the wire.
//
// This file drives app.Query(&abci.RequestQuery{Path: "/hub.v1.Query/..."})
// so a regression in the module's RegisterServices wiring, the query
// route registration, or the codec surfaces here rather than only in a
// deployed node.
//
// The REST gateway is not exercised as a live HTTP server (that needs a
// bound port and is not deterministic in CI); its routes are generated
// from the same google.api.http proto annotations over these same
// handlers and are covered structurally by the OpenAPI sync (the OpenAPI sync).

import (
	"context"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

// TestGRPCQueryParamsThroughRouter proves the Params query is reachable
// through the real gRPC query router and that the protobuf request /
// response round-trips over the ABCI query boundary.
func TestGRPCQueryParamsThroughRouter(t *testing.T) {
	app := bootAppMinimal(t)

	reqBytes, err := app.AppCodec().Marshal(&hubtypes.QueryHubParamsRequest{})
	require.NoError(t, err)

	resp, err := app.Query(context.Background(), &abci.RequestQuery{
		Path: "/hub.v1.Query/Params",
		Data: reqBytes,
	})
	require.NoError(t, err)
	require.Equalf(t, uint32(0), resp.Code, "gRPC Params query must succeed; log=%s", resp.Log)

	var out hubtypes.QueryHubParamsResponse
	require.NoError(t, app.AppCodec().Unmarshal(resp.Value, &out))
	require.Equal(t, hubtypes.DefaultHubParams(), out.Params,
		"params returned through the gRPC router must equal genesis defaults")
}

// TestGRPCQueryUnknownRowReturnsErrorCode proves the router maps a
// handler NotFound into a non-zero ABCI response code (rather than a
// zero-code empty body). This locks the error contract a gRPC/REST
// client relies on to distinguish "no such row" from "empty".
//
// This used to drive /hub.v1.Query/ChallengeEffectPool.
// K-BLOCK-03/04 removed the whole public challenge query surface
// (QueryChallengeEffectPoolRequest and the ChallengeEconomicEffect ledger are
// gone), so the router contract is now pinned on Model, which has the same
// NotFound / InvalidArgument shape. Re-point at the challenge query if a public
// challenge read surface is ever registered.
func TestGRPCQueryUnknownRowReturnsErrorCode(t *testing.T) {
	app := bootAppMinimal(t)

	reqBytes, err := app.AppCodec().Marshal(&hubtypes.QueryModelRequest{
		ModelId: "does-not-exist",
	})
	require.NoError(t, err)

	resp, err := app.Query(context.Background(), &abci.RequestQuery{
		Path: "/hub.v1.Query/Model",
		Data: reqBytes,
	})
	// The gRPC error is carried in-band via a non-zero Code, not as a Go
	// error from Query itself.
	require.NoError(t, err)
	require.NotEqualf(t, uint32(0), resp.Code,
		"unknown model must map to a non-zero ABCI code; log=%s", resp.Log)
}

// TestGRPCQueryInvalidArgumentReturnsErrorCode proves an empty required
// field is rejected with a non-zero code through the router, matching
// the InvalidArgument contract the direct-handler tests assert.
func TestGRPCQueryInvalidArgumentReturnsErrorCode(t *testing.T) {
	app := bootAppMinimal(t)

	reqBytes, err := app.AppCodec().Marshal(&hubtypes.QueryModelRequest{
		ModelId: "",
	})
	require.NoError(t, err)

	resp, err := app.Query(context.Background(), &abci.RequestQuery{
		Path: "/hub.v1.Query/Model",
		Data: reqBytes,
	})
	require.NoError(t, err)
	require.NotEqualf(t, uint32(0), resp.Code,
		"empty model_id must map to a non-zero ABCI code; log=%s", resp.Log)
}
