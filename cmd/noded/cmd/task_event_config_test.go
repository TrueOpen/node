package cmd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	nodeapp "github.com/TrueOpen/node/app"
	"github.com/TrueOpen/node/app/taskevents"
)

func TestInitAppConfigIncludesDisabledTaskEventService(t *testing.T) {
	templateText, rawConfig := initAppConfig()
	require.Contains(t, templateText, "[task-event-grpc]")
	require.Contains(t, templateText, "protocol-events-enabled")
	require.Contains(t, templateText, "max-concurrent-replays")
	require.NotNil(t, rawConfig)

	defaults := taskevents.DefaultConfig()
	require.False(t, defaults.Enabled)
	require.False(t, defaults.ProtocolEventsEnabled)
	require.True(t, strings.Contains(templateText, "enabled = {{ .TaskEventGRPC.Enabled }}"))
}

func TestInitAppConfigIncludesBeaconRoleDefault(t *testing.T) {
	templateText, rawConfig := initAppConfig()
	require.Contains(t, templateText, "[beacon]")
	require.Contains(t, templateText, "vrf-key-required = {{ .Beacon.VrfKeyRequired }}")
	require.True(t, nodeapp.DefaultBeaconNodeConfig().VrfKeyRequired)
	require.NotNil(t, rawConfig)
}
