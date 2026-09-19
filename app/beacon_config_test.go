package app

import (
	"testing"

	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	"github.com/stretchr/testify/require"
)

func TestBeaconNodeConfigFromAppOptions(t *testing.T) {
	cfg, err := BeaconNodeConfigFromAppOptions(nil)
	require.NoError(t, err)
	require.True(t, cfg.VrfKeyRequired)

	opts := simtestutil.AppOptionsMap{BeaconVrfKeyRequiredOption: false}
	cfg, err = BeaconNodeConfigFromAppOptions(opts)
	require.NoError(t, err)
	require.False(t, cfg.VrfKeyRequired)

	_, err = BeaconNodeConfigFromAppOptions(simtestutil.AppOptionsMap{BeaconVrfKeyRequiredOption: "invalid"})
	require.ErrorContains(t, err, BeaconVrfKeyRequiredOption)
}
