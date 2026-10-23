package app

import (
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	"github.com/spf13/cast"
)

const SessionKeyActivationHeightOption = "session-key-rules.activation-height"

// SessionKeyNodeConfig is a coordinated consensus deployment setting. Every
// validator and replay node must retain the same first-rules height.
type SessionKeyNodeConfig struct {
	ActivationHeight uint64 `mapstructure:"activation-height"`
}

func SessionKeyConfigFromAppOptions(opts servertypes.AppOptions) (SessionKeyNodeConfig, error) {
	var cfg SessionKeyNodeConfig
	if opts == nil || opts.Get(SessionKeyActivationHeightOption) == nil {
		return cfg, nil
	}
	height, err := cast.ToUint64E(opts.Get(SessionKeyActivationHeightOption))
	if err != nil || !shared.IsBlockHeightV1(height) {
		return cfg, fmt.Errorf("invalid %s", SessionKeyActivationHeightOption)
	}
	cfg.ActivationHeight = height
	return cfg, nil
}
