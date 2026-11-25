package keeper_test

import (
	"bytes"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// GetAnchorDAEndpoint reads the ANCHOR_DA endpoint of a Cortex operator's
// current descriptor and nothing else.
func TestGetAnchorDAEndpoint(t *testing.T) {
	f := initFixture(t)
	operator := sdk.AccAddress(bytes.Repeat([]byte{0x41}, 20)).String()
	other := sdk.AccAddress(bytes.Repeat([]byte{0x42}, 20)).String()
	require.Empty(t, f.keeper.GetAnchorDAEndpoint(f.ctx, operator), "no descriptor")

	store := func(participant shared.ParticipantType, address string, endpoints ...types.ServiceEndpointV1) {
		require.NoError(t, f.keeper.StoreServiceDescriptor(f.ctx, types.NewParticipantKey(participant, address), types.ServiceDescriptorState{
			ParticipantType: participant, OperatorAddress: address, DescriptorVersion: 1,
			EndpointCount: uint32(len(endpoints)), Endpoints: endpoints, DescriptorHash: bytes.Repeat([]byte{0x07}, 32), UpdatedHeight: 1,
		}))
	}
	health := types.ServiceEndpointV1{EndpointKind: types.ServiceEndpointKind_SERVICE_ENDPOINT_KIND_HEALTH_HTTPS, Uri: "https://w.example/health", ProtocolVersion: "v1"}
	da := types.ServiceEndpointV1{EndpointKind: types.ServiceEndpointKind_SERVICE_ENDPOINT_KIND_ANCHOR_DA, Uri: "http://203.0.113.7:50132/da/", ProtocolVersion: "v1"}

	store(shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, other, health)
	require.Empty(t, f.keeper.GetAnchorDAEndpoint(f.ctx, other), "a descriptor without the endpoint")
	store(shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, operator, health, da)
	require.Equal(t, "http://203.0.113.7:50132/da/", f.keeper.GetAnchorDAEndpoint(f.ctx, operator))
	require.Empty(t, f.keeper.GetAnchorDAEndpoint(f.ctx, other))
}
