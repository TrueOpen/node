package taskevents

import (
	"testing"

	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	"github.com/stretchr/testify/require"
)

func TestConfigFromAppOptionsDefaultsDisabled(t *testing.T) {
	cfg, err := ConfigFromAppOptions(simtestutil.AppOptionsMap{})
	require.NoError(t, err)
	require.False(t, cfg.Enabled)
	require.False(t, cfg.ProtocolEventsEnabled)
	require.Equal(t, defaultSubscriberBufferSize, cfg.SubscriberBufferSize)
	require.Equal(t, defaultMaxSubscribers, cfg.MaxSubscribers)
	require.Equal(t, uint64(defaultMaxReplayBlocks), cfg.MaxReplayBlocks)
	require.Equal(t, defaultMaxConcurrentReplays, cfg.MaxConcurrentReplays)
}

func TestConfigFromAppOptionsOverridesAndValidates(t *testing.T) {
	opts := simtestutil.AppOptionsMap{
		"task-event-grpc.enabled":                 true,
		"task-event-grpc.protocol-events-enabled": true,
		"task-event-grpc.subscriber-buffer-size":  "32",
		"task-event-grpc.max-subscribers":         20,
		"task-event-grpc.max-replay-blocks":       uint64(50),
		"task-event-grpc.max-concurrent-replays":  3,
	}
	cfg, err := ConfigFromAppOptions(opts)
	require.NoError(t, err)
	require.True(t, cfg.Enabled)
	require.True(t, cfg.ProtocolEventsEnabled)
	require.Equal(t, 32, cfg.SubscriberBufferSize)
	require.Equal(t, 20, cfg.MaxSubscribers)
	require.Equal(t, uint64(50), cfg.MaxReplayBlocks)
	require.Equal(t, 3, cfg.MaxConcurrentReplays)

	opts["task-event-grpc.enabled"] = false
	_, err = ConfigFromAppOptions(opts)
	require.ErrorContains(t, err, "requires enabled=true")
}

func TestEnabledConfigRejectsNonPositiveLimits(t *testing.T) {
	opts := simtestutil.AppOptionsMap{
		"task-event-grpc.enabled":                true,
		"task-event-grpc.subscriber-buffer-size": 0,
	}
	_, err := ConfigFromAppOptions(opts)
	require.ErrorContains(t, err, "subscriber-buffer-size")
}

func TestConfigRejectsTaskEventsWhenStandardGRPCIsDisabled(t *testing.T) {
	opts := simtestutil.AppOptionsMap{
		"task-event-grpc.enabled": true,
		"grpc.enable":             false,
	}
	_, err := ConfigFromAppOptions(opts)
	require.ErrorContains(t, err, "requires grpc.enable=true")
}
