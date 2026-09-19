package app

import (
	"context"

	cmtrpc "github.com/cometbft/cometbft/rpc/client"
	gogogrpc "github.com/cosmos/gogoproto/grpc"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

// RegisterGRPCServerWithSkipCheckHeader adds the optional streaming service to
// the same gRPC server as the existing unary Cosmos SDK services.
func (app *App) RegisterGRPCServerWithSkipCheckHeader(server gogogrpc.Server, skipCheckHeader bool) {
	app.App.RegisterGRPCServerWithSkipCheckHeader(server, skipCheckHeader)
	if app.taskEventServer != nil {
		tasktypes.RegisterTaskEventServiceServer(server, app.taskEventServer)
		hubtypes.RegisterHubEventServiceServer(server, app.taskEventServer)
	}
}

func (app *App) StartTaskEventService(ctx context.Context, client cmtrpc.Client) error {
	if app.taskEventServer == nil {
		return nil
	}
	return app.taskEventServer.Run(ctx, client)
}

func (app *App) TaskEventServiceEnabled() bool {
	return app.taskEventServer != nil
}

// Close is idempotent as required by servertypes.Application.
func (app *App) Close() error {
	app.closeOnce.Do(func() {
		if app.taskEventServer != nil {
			app.taskEventServer.Close()
		}
		app.closeErr = app.App.Close()
	})
	return app.closeErr
}
