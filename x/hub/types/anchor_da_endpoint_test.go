package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A Cortex operator publishes where it serves anchor audit material from as an
// ANCHOR_DA endpoint. The Task module uses it as a prefix and validators fetch
// from it, so it is held to the strict location rules.
func TestServiceDescriptorAnchorDAEndpoint(t *testing.T) {
	params := DefaultHubParams().Service
	endpoint := func(uri string) []ServiceEndpointV1 {
		return []ServiceEndpointV1{{EndpointKind: ServiceEndpointKind_SERVICE_ENDPOINT_KIND_ANCHOR_DA, Uri: uri, ProtocolVersion: "v1"}}
	}
	for _, uri := range []string{"http://203.0.113.7:50132/da/", "https://da.example/", "https://da.example:8443/a/"} {
		_, err := CanonicalServiceDescriptorEndpointFields(endpoint(uri), params)
		require.NoError(t, err, uri)
	}
	for _, uri := range []string{
		"https://da.example", "https://da.example/da", "grpc://da.example/", "http://a:99999/", "http://a:0/",
		"https://da.example/da/?x=1", "https://da.example/" + string(make([]byte, 300)),
	} {
		_, err := CanonicalServiceDescriptorEndpointFields(endpoint(uri), params)
		require.Error(t, err, uri)
	}

	// It sorts after the existing kinds and is unique like them.
	both := []ServiceEndpointV1{
		{EndpointKind: ServiceEndpointKind_SERVICE_ENDPOINT_KIND_HEALTH_HTTPS, Uri: "https://w.example/health", ProtocolVersion: "v1"},
		{EndpointKind: ServiceEndpointKind_SERVICE_ENDPOINT_KIND_ANCHOR_DA, Uri: "https://da.example/", ProtocolVersion: "v1"},
	}
	_, err := CanonicalServiceDescriptorEndpointFields(both, params)
	require.NoError(t, err)
	require.Equal(t, "https://da.example/", AnchorDAEndpointURI(ServiceDescriptorState{Endpoints: both}))
	require.Empty(t, AnchorDAEndpointURI(ServiceDescriptorState{Endpoints: both[:1]}))

	// The other kinds keep their rules: a health endpoint needs no trailing slash.
	_, err = CanonicalServiceDescriptorEndpointFields(both[:1], params)
	require.NoError(t, err)
}
