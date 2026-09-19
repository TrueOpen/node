package taskevents

import (
	"fmt"

	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	"github.com/spf13/cast"
)

const (
	configPrefix = "task-event-grpc."

	defaultSubscriberBufferSize = 128
	defaultMaxSubscribers       = 500
	defaultMaxReplayBlocks      = 10_000
	defaultMaxConcurrentReplays = 8
)

// Config controls the optional, node-local task event gRPC service.
type Config struct {
	Enabled               bool   `mapstructure:"enabled"`
	ProtocolEventsEnabled bool   `mapstructure:"protocol-events-enabled"`
	SubscriberBufferSize  int    `mapstructure:"subscriber-buffer-size"`
	MaxSubscribers        int    `mapstructure:"max-subscribers"`
	MaxReplayBlocks       uint64 `mapstructure:"max-replay-blocks"`
	MaxConcurrentReplays  int    `mapstructure:"max-concurrent-replays"`
}

func DefaultConfig() Config {
	return Config{
		SubscriberBufferSize: defaultSubscriberBufferSize,
		MaxSubscribers:       defaultMaxSubscribers,
		MaxReplayBlocks:      defaultMaxReplayBlocks,
		MaxConcurrentReplays: defaultMaxConcurrentReplays,
	}
}

func ConfigFromAppOptions(opts servertypes.AppOptions) (Config, error) {
	cfg := DefaultConfig()
	if opts == nil {
		return cfg, nil
	}

	var err error
	if value := opts.Get(configPrefix + "enabled"); value != nil {
		cfg.Enabled, err = cast.ToBoolE(value)
		if err != nil {
			return Config{}, fmt.Errorf("parse %senabled: %w", configPrefix, err)
		}
	}
	if value := opts.Get(configPrefix + "protocol-events-enabled"); value != nil {
		cfg.ProtocolEventsEnabled, err = cast.ToBoolE(value)
		if err != nil {
			return Config{}, fmt.Errorf("parse %sprotocol-events-enabled: %w", configPrefix, err)
		}
	}
	if err := readPositiveIntOption(opts, configPrefix+"subscriber-buffer-size", &cfg.SubscriberBufferSize); err != nil {
		return Config{}, err
	}
	if err := readPositiveIntOption(opts, configPrefix+"max-subscribers", &cfg.MaxSubscribers); err != nil {
		return Config{}, err
	}
	if value := opts.Get(configPrefix + "max-replay-blocks"); value != nil {
		cfg.MaxReplayBlocks, err = cast.ToUint64E(value)
		if err != nil {
			return Config{}, fmt.Errorf("parse %smax-replay-blocks: %w", configPrefix, err)
		}
	}
	if err := readPositiveIntOption(opts, configPrefix+"max-concurrent-replays", &cfg.MaxConcurrentReplays); err != nil {
		return Config{}, err
	}
	if cfg.Enabled {
		if value := opts.Get("grpc.enable"); value != nil {
			grpcEnabled, parseErr := cast.ToBoolE(value)
			if parseErr != nil {
				return Config{}, fmt.Errorf("parse grpc.enable: %w", parseErr)
			}
			if !grpcEnabled {
				return Config{}, fmt.Errorf("task-event-grpc enabled requires grpc.enable=true")
			}
		}
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.ProtocolEventsEnabled && !c.Enabled {
		return fmt.Errorf("task-event-grpc protocol-events-enabled requires enabled=true")
	}
	if !c.Enabled {
		return nil
	}
	if c.SubscriberBufferSize <= 0 {
		return fmt.Errorf("task-event-grpc subscriber-buffer-size must be positive")
	}
	if c.MaxSubscribers <= 0 {
		return fmt.Errorf("task-event-grpc max-subscribers must be positive")
	}
	if c.MaxReplayBlocks == 0 {
		return fmt.Errorf("task-event-grpc max-replay-blocks must be positive")
	}
	if c.MaxConcurrentReplays <= 0 {
		return fmt.Errorf("task-event-grpc max-concurrent-replays must be positive")
	}
	return nil
}

func readPositiveIntOption(opts servertypes.AppOptions, key string, target *int) error {
	value := opts.Get(key)
	if value == nil {
		return nil
	}
	parsed, err := cast.ToIntE(value)
	if err != nil {
		return fmt.Errorf("parse %s: %w", key, err)
	}
	*target = parsed
	return nil
}
