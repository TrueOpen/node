package keeper

import "github.com/TrueOpen/node/x/task/types"

type msgServer struct {
	// Generated fallbacks remain only for surfaces whose authoritative state or
	// public contract is still blocked; ACTIVE verification methods are implemented by
	// the concrete handlers in this package.
	types.UnimplementedMsgServer
	k Keeper
}

// NewMsgServerImpl returns the Task Msg service implementation.
func NewMsgServerImpl(k Keeper) types.MsgServer {
	return &msgServer{k: k}
}

var _ types.MsgServer = (*msgServer)(nil)
