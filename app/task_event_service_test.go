package app

import (
	"testing"

	"cosmossdk.io/log"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

const taskEventServiceName = "task.v1.TaskEventService"
const hubEventServiceName = "hub.v1.HubEventService"

func TestTaskEventGRPCRegistrationFollowsSwitch(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		db := dbm.NewMemDB()
		application := New(log.NewNopLogger(), db, nil, true, smokeAppOptions(), baseapp.SetChainID(SimAppChainID))
		server := grpc.NewServer()
		application.RegisterGRPCServerWithSkipCheckHeader(server, false)
		services := server.GetServiceInfo()
		_, found := services[taskEventServiceName]
		require.False(t, found)
		_, found = services[hubEventServiceName]
		require.False(t, found)
		require.NoError(t, application.Close())
	})

	t.Run("enabled", func(t *testing.T) {
		db := dbm.NewMemDB()
		opts := smokeAppOptions()
		opts["task-event-grpc.enabled"] = true
		application := New(log.NewNopLogger(), db, nil, true, opts, baseapp.SetChainID(SimAppChainID))
		server := grpc.NewServer()
		application.RegisterGRPCServerWithSkipCheckHeader(server, false)
		services := server.GetServiceInfo()
		_, found := services[taskEventServiceName]
		require.True(t, found)
		_, found = services[hubEventServiceName]
		require.True(t, found)
		require.NoError(t, application.Close())
		require.NoError(t, application.Close())
	})
}
