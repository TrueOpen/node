package module

import (
	"testing"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/TrueOpen/node/x/task/keeper"
	"github.com/TrueOpen/node/x/task/types"
)

type recordingServiceRegistrar struct {
	services map[string]*grpc.ServiceDesc
}

func TestRegisterLegacyAminoCodecIncludesWorkerEvidence(t *testing.T) {
	cdc := codec.NewLegacyAmino()
	(AppModule{}).RegisterLegacyAminoCodec(cdc)
	encoded, err := cdc.MarshalJSON(&types.MsgSubmitWorkerEvidence{})
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"type":"trueopen/x/task/MsgSubmitWorkerEvidence"`)
}

type recordingInvariantRegistry struct{ routes map[string]sdk.Invariant }

func (r *recordingInvariantRegistry) RegisterRoute(moduleName, route string, invariant sdk.Invariant) {
	if r.routes == nil {
		r.routes = make(map[string]sdk.Invariant)
	}
	r.routes[moduleName+"/"+route] = invariant
}

func TestRegisterInvariantsExposesEveryKeeperCheck(t *testing.T) {
	registry := &recordingInvariantRegistry{}
	appModule := NewAppModule(nil, keeper.Keeper{})
	appModule.RegisterInvariants(registry)
	require.Len(t, registry.routes, len((keeper.Keeper{}).InvariantChecks()))
}

func TestConsensusVersionRequiresFreshV4Store(t *testing.T) {
	require.Equal(t, uint64(3), (AppModule{}).ConsensusVersion())
}

func (r *recordingServiceRegistrar) RegisterService(desc *grpc.ServiceDesc, _ interface{}) {
	if r.services == nil {
		r.services = make(map[string]*grpc.ServiceDesc)
	}
	r.services[desc.ServiceName] = desc
}

func TestRegisterServicesExposesTaskMsgAndQuery(t *testing.T) {
	registrar := &recordingServiceRegistrar{}
	appModule := NewAppModule(nil, keeper.Keeper{})

	require.NoError(t, appModule.RegisterServices(registrar))
	require.Contains(t, registrar.services, "task.v1.Msg")
	require.Contains(t, registrar.services, "task.v1.Query")
	require.NotEmpty(t, registrar.services["task.v1.Msg"].Methods)
	require.NotEmpty(t, registrar.services["task.v1.Query"].Methods)
	methods := make(map[string]struct{})
	for _, method := range registrar.services["task.v1.Msg"].Methods {
		methods[method.MethodName] = struct{}{}
	}
	require.Contains(t, methods, "SubmitBuilderEvidence")
}
