package app

import (
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	"github.com/stretchr/testify/require"
	"math"
	"strings"
	"testing"
)

func TestSessionKeyActivationConfiguration(t *testing.T) {
	cfg, err := SessionKeyConfigFromAppOptions(nil)
	require.NoError(t, err)
	require.Zero(t, cfg.ActivationHeight)
	cfg, err = SessionKeyConfigFromAppOptions(simtestutil.AppOptionsMap{SessionKeyActivationHeightOption: "101"})
	require.NoError(t, err)
	require.Equal(t, uint64(101), cfg.ActivationHeight)
	for _, bad := range []any{-1, "invalid", uint64(math.MaxInt64) + 1} {
		_, err := SessionKeyConfigFromAppOptions(simtestutil.AppOptionsMap{SessionKeyActivationHeightOption: bad})
		require.Error(t, err)
	}
}

// The app.toml section must not be named like the CreateSession AutoCLI flag
// "--session-key": viper would hand that flag the whole section as its default
// and every create-session invocation would fail to bind.
func TestSessionKeyConfigSectionDoesNotShadowCreateSessionFlag(t *testing.T) {
	section, _, found := strings.Cut(SessionKeyActivationHeightOption, ".")
	require.True(t, found)
	require.NotEqual(t, "session-key", section)
}
