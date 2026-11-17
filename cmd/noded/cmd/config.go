package cmd

import (
	cmtcfg "github.com/cometbft/cometbft/config"
	serverconfig "github.com/cosmos/cosmos-sdk/server/config"

	nodeapp "github.com/TrueOpen/node/app"
	"github.com/TrueOpen/node/app/taskevents"
)

type appConfig struct {
	serverconfig.Config `mapstructure:",squash"`
	TaskEventGRPC       taskevents.Config        `mapstructure:"task-event-grpc"`
	Beacon              nodeapp.BeaconNodeConfig `mapstructure:"beacon"`
}

// Keep every genesis initializer on the same defaults and template shape.
func defaultAppConfig() appConfig {
	return appConfig{
		Config:        *serverconfig.DefaultConfig(),
		TaskEventGRPC: taskevents.DefaultConfig(),
		Beacon:        nodeapp.DefaultBeaconNodeConfig(),
	}
}

// initCometBFTConfig helps to override default CometBFT Config values.
// return cmtcfg.DefaultConfig if no custom configuration is required for the application.
func initCometBFTConfig() *cmtcfg.Config {
	cfg := cmtcfg.DefaultConfig()

	// these values put a higher strain on node memory
	// cfg.P2P.MaxNumInboundPeers = 100
	// cfg.P2P.MaxNumOutboundPeers = 40

	return cfg
}

// initAppConfig couples the custom template with its shared typed defaults.
func initAppConfig() (string, interface{}) {
	customAppConfig := defaultAppConfig()

	customAppTemplate := serverconfig.DefaultConfigTemplate + `

###############################################################################
###                         Task Event gRPC                                  ###
###############################################################################

[task-event-grpc]

# Enable committed task and role event server streams on the standard gRPC
# listener. This is intended for public RPC nodes and is disabled by default.
enabled = {{ .TaskEventGRPC.Enabled }}

# Enable the optional SubscribeProtocolEvents stream. Requires enabled=true.
protocol-events-enabled = {{ .TaskEventGRPC.ProtocolEventsEnabled }}

# Per-subscriber queue. Slow subscribers are disconnected instead of applying
# backpressure to block processing.
subscriber-buffer-size = {{ .TaskEventGRPC.SubscriberBufferSize }}

# Global concurrent stream limit for this node process.
max-subscribers = {{ .TaskEventGRPC.MaxSubscribers }}

# Maximum number of committed blocks scanned by one client replay request.
max-replay-blocks = {{ .TaskEventGRPC.MaxReplayBlocks }}

# Maximum number of historical replay scans running concurrently. New replay
# requests above this limit fail fast instead of multiplying local RPC load.
max-concurrent-replays = {{ .TaskEventGRPC.MaxConcurrentReplays }}

###############################################################################
###                         Beacon node role                                  ###
###############################################################################

[beacon]

# Mainnet builds fail fast without config/vrf_key.json when this is true.
# Validators keep the default true. RPC, seed, and other non-proposing nodes
# must set false; committed chain params still enforce every proposal.
vrf-key-required = {{ .Beacon.VrfKeyRequired }}

`

	return customAppTemplate, customAppConfig
}
