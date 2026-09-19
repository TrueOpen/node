package app

import (
	"fmt"

	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	"github.com/spf13/cast"
)

const BeaconVrfKeyRequiredOption = "beacon.vrf-key-required"

// BeaconNodeConfig controls node-local beacon startup checks. It never changes
// proposal validation; committed BeaconParams remain the sole consensus policy.
type BeaconNodeConfig struct {
	VrfKeyRequired bool `mapstructure:"vrf-key-required"`
}

func DefaultBeaconNodeConfig() BeaconNodeConfig {
	return BeaconNodeConfig{VrfKeyRequired: true}
}

func BeaconNodeConfigFromAppOptions(opts servertypes.AppOptions) (BeaconNodeConfig, error) {
	cfg := DefaultBeaconNodeConfig()
	if opts == nil {
		return cfg, nil
	}
	value := opts.Get(BeaconVrfKeyRequiredOption)
	if value == nil {
		return cfg, nil
	}
	required, err := cast.ToBoolE(value)
	if err != nil {
		return BeaconNodeConfig{}, fmt.Errorf("parse %s: %w", BeaconVrfKeyRequiredOption, err)
	}
	cfg.VrfKeyRequired = required
	return cfg, nil
}
