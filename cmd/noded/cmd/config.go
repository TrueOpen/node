package cmd

import (
	cmtcfg "github.com/cometbft/cometbft/config"
	serverconfig "github.com/cosmos/cosmos-sdk/server/config"

	nodeapp "github.com/TrueOpen/node/app"
	"github.com/TrueOpen/node/app/taskevents"
)

// initCometBFTConfig helps to override default CometBFT Config values.
// return cmtcfg.DefaultConfig if no custom configuration is required for the application.
func initCometBFTConfig() *cmtcfg.Config {
	cfg := cmtcfg.DefaultConfig()

	// these values put a higher strain on node memory
	// cfg.P2P.MaxNumInboundPeers = 100
	// cfg.P2P.MaxNumOutboundPeers = 40

	return cfg
}

// initAppConfig helps to override default appConfig template and configs.
// return "", nil if no custom configuration is required for the application.
func initAppConfig() (string, interface{}) {
	// The following code snippet is just for reference.
	type CustomAppConfig struct {
		serverconfig.Config `mapstructure:",squash"`
		TaskEventGRPC       taskevents.Config        `mapstructure:"task-event-grpc"`
		Beacon              nodeapp.BeaconNodeConfig `mapstructure:"beacon"`
	}

	// Optionally allow the chain developer to overwrite the SDK's default
	// server config.
	srvCfg := serverconfig.DefaultConfig()
	// The SDK's default minimum gas price is set to "" (empty value) inside
	// app.toml. If left empty by validators, the node will halt on startup.
	// However, the chain developer can set a default app.toml value for their
	// validators here.
	//
	// In summary:
	// - if you leave srvCfg.MinGasPrices = "", all validators MUST tweak their
	//   own app.toml config,
	// - if you set srvCfg.MinGasPrices non-empty, validators CAN tweak their
	//   own app.toml to override, or use this default value.
	//
	// In tests, we set the min gas prices to 0.
	// srvCfg.MinGasPrices = "0stake"

	customAppConfig := CustomAppConfig{
		Config:        *srvCfg,
		TaskEventGRPC: taskevents.DefaultConfig(),
		Beacon:        nodeapp.DefaultBeaconNodeConfig(),
	}

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
	// Edit the default template file
	//
	// customAppTemplate := serverconfig.DefaultConfigTemplate + `
	// [wasm]
	// # This is the maximum sdk gas (wasm and storage) that we allow for any x/wasm "smart" queries
	// query_gas_limit = 300000
	// # This is the number of wasm vm instances we keep cached in memory for speed-up
	// # Warning: this is currently unstable and may lead to crashes, best to keep for 0 unless testing locally
	// lru_size = 0`

	return customAppTemplate, customAppConfig
}
