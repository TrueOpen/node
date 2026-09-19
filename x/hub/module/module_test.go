package module

import (
	"context"
	"testing"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
)

type recordingServiceRegistrar struct {
	services map[string]*grpc.ServiceDesc
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
	appModule := NewAppModule(nil, keeper.Keeper{}, types.MsgServerDependencies{})
	appModule.RegisterInvariants(registry)
	require.Len(t, registry.routes, len((keeper.Keeper{}).InvariantChecks()))
}

func TestConsensusVersionRequiresFreshV5Store(t *testing.T) {
	require.Equal(t, uint64(2), (AppModule{}).ConsensusVersion())
}

func TestRegisterLegacyAminoCodecUsesFrozenModelLifecycleNames(t *testing.T) {
	cdc := codec.NewLegacyAmino()
	(AppModule{}).RegisterLegacyAminoCodec(cdc)

	for _, testCase := range []struct {
		message sdk.Msg
		name    string
	}{
		{&types.MsgRegisterModelProfile{}, "trueopen/x/hub/MsgRegisterModelProfile"},
		{&types.MsgSetModelStatus{}, "trueopen/x/hub/MsgSetModelStatus"},
		{&types.MsgSetProfileStatus{}, "trueopen/x/hub/MsgSetProfileStatus"},
	} {
		encoded, err := cdc.MarshalJSON(testCase.message)
		require.NoError(t, err)
		require.Contains(t, string(encoded), `"type":"`+testCase.name+`"`)
	}
}

func (r *recordingServiceRegistrar) RegisterService(desc *grpc.ServiceDesc, _ interface{}) {
	if r.services == nil {
		r.services = make(map[string]*grpc.ServiceDesc)
	}
	r.services[desc.ServiceName] = desc
}

type freezeTaskValidatorStub struct{}

func (freezeTaskValidatorStub) ScanFreezeSignalFailures(context.Context, types.FreezeSignalFailureScanRequest) (types.FreezeSignalFailureScanResult, error) {
	return types.FreezeSignalFailureScanResult{Done: true}, nil
}

type validatorSnapshotProviderStub struct{}

func (validatorSnapshotProviderStub) HistoricalEntries(context.Context) (uint32, error) {
	return 10_000, nil
}

func (validatorSnapshotProviderStub) CaptureValidatorSetSnapshot(sdk.Context, uint64) (types.ValidatorSnapshot, error) {
	return types.ValidatorSnapshot{}, nil
}

func (validatorSnapshotProviderStub) GetValidatorSnapshotMemberBySigner(sdk.Context, types.ValidatorSnapshot, string) (types.ValidatorSnapshotMember, bool, error) {
	return types.ValidatorSnapshotMember{}, false, nil
}

type taskSafetyWindowProviderStub struct{}

func (taskSafetyWindowProviderStub) GetTaskSafetyWindows(context.Context) (types.TaskSafetyWindows, error) {
	return types.TaskSafetyWindows{}, nil
}

type roleFaultConsumerGateStub struct{}

func (roleFaultConsumerGateStub) RoleFaultConsumersClosed(context.Context, []byte, []byte) (bool, error) {
	return true, nil
}

func TestRegisterServicesRequiresAllCrossModuleDependencies(t *testing.T) {
	registrar := &recordingServiceRegistrar{}
	appModule := NewAppModule(nil, keeper.Keeper{}, types.MsgServerDependencies{})

	err := appModule.RegisterServices(registrar)
	require.Error(t, err)
	require.Empty(t, registrar.services)
}

func TestRegisterServicesExposesActiveMsgAndQueryRoutes(t *testing.T) {
	registrar := &recordingServiceRegistrar{}
	appModule := NewAppModule(nil, keeper.Keeper{}, types.MsgServerDependencies{
		FreezeTaskValidator:       freezeTaskValidatorStub{},
		ValidatorSnapshotProvider: validatorSnapshotProviderStub{},
		TaskSafetyWindowProvider:  taskSafetyWindowProviderStub{},
		RoleFaultConsumerGate:     roleFaultConsumerGateStub{},
	})

	require.NoError(t, appModule.RegisterServices(registrar))
	require.Contains(t, registrar.services, "hub.v1.Msg")
	require.Contains(t, registrar.services, "hub.v1.Query")

	methods := make(map[string]struct{})
	for _, method := range registrar.services["hub.v1.Msg"].Methods {
		methods[method.MethodName] = struct{}{}
	}
	require.NotContains(t, methods, "SubmitBuilderEvidence")
	require.Contains(t, methods, "RunBuilderTerm")
	require.Contains(t, methods, "RegisterModelProfile")
	require.NotContains(t, methods, "RegisterModel")
	require.NotContains(t, methods, "RegisterProfile")
	require.NotContains(t, methods, "SubmitCapabilityProof")
	require.NotContains(t, methods, "ProcessUnifiedDeadline")
	require.Contains(t, methods, "RegisterBuilder")
	require.Contains(t, methods, "StakeService")
	require.Contains(t, methods, "BatchConfirmModelSupport")
	require.Contains(t, methods, "ClaimEarnings")

	var registerProfileFields []string
	for _, option := range appModule.AutoCLIOptions().Tx.RpcCommandOptions {
		if option.RpcMethod != "RegisterModelProfile" {
			continue
		}
		for _, arg := range option.PositionalArgs {
			registerProfileFields = append(registerProfileFields, arg.ProtoField)
		}
	}
	require.NotEmpty(t, registerProfileFields)
	require.Equal(t, []string{"proposer_address"}, registerProfileFields)
}
