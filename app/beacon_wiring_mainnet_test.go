//go:build mainnet

package app

import (
	"testing"

	"cosmossdk.io/log"
	"github.com/stretchr/testify/require"
)

type startupVrfSigner struct{}

func (startupVrfSigner) Prove([]byte) ([]byte, []byte, error) { return nil, nil, nil }
func (startupVrfSigner) VrfPubKey() ([]byte, error)           { return make([]byte, 32), nil }

func TestMainnetBeaconStartupAssertionRespectsNodeRole(t *testing.T) {
	logger := log.NewNopLogger()
	require.Panics(t, func() {
		assertBeaconStartupConfig(logger, t.TempDir(), nil, true)
	})
	require.NotPanics(t, func() {
		assertBeaconStartupConfig(logger, t.TempDir(), nil, false)
	})
	require.NotPanics(t, func() {
		assertBeaconStartupConfig(logger, t.TempDir(), startupVrfSigner{}, true)
	})
}
