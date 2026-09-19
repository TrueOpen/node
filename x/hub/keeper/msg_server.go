package keeper

import "github.com/TrueOpen/node/x/hub/types"

type msgServer struct {
	// The generated embedding preserves forward source compatibility. A
	// descriptor-based contract test requires every registered RPC to have a
	// concrete keeper method.
	types.UnimplementedMsgServer
	k                   Keeper
	freezeTaskValidator types.FreezeSignalTaskValidator
	validatorSnapshots  types.ValidatorSnapshotProvider
	taskSafetyWindows   types.TaskSafetyWindowProvider
}

// NewMsgServerImpl returns the Hub Msg service implementation.
//
// The embedded types.UnimplementedMsgServer declares its fallbacks on
// *UnimplementedMsgServer, so only *msgServer satisfies types.MsgServer.
func NewMsgServerImpl(k Keeper, dependencies ...types.MsgServerDependencies) types.MsgServer {
	server := &msgServer{k: k}
	if len(dependencies) > 0 {
		server.k = k.WithRuntimeDependencies(dependencies[0].FreezeTaskValidator, dependencies[0].ValidatorSnapshotProvider, dependencies[0].RoleFaultConsumerGate)
		server.freezeTaskValidator = dependencies[0].FreezeTaskValidator
		server.validatorSnapshots = dependencies[0].ValidatorSnapshotProvider
		server.taskSafetyWindows = dependencies[0].TaskSafetyWindowProvider
	}
	return server
}

var _ types.MsgServer = (*msgServer)(nil)
