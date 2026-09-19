package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
)

func TestHubCanonicalSigningBytesRejectFieldBoundaryCollision(t *testing.T) {
	left := types.CanonicalDailySupportConfirmationSigningBytes(
		"chain|provider", []byte("address"), 1, 1, 100,
		[]types.ProfileKeyV1{{ModelId: "model", ProfileVersion: 1}},
	)
	right := types.CanonicalDailySupportConfirmationSigningBytes(
		"chain", []byte("provider|address"), 1, 1, 100,
		[]types.ProfileKeyV1{{ModelId: "model", ProfileVersion: 1}},
	)
	require.NotEqual(t, left, right)
	checked, err := types.CanonicalDailySupportConfirmationSigningBytesV1(
		"chain|provider", []byte("address"), 1, 1, 100,
		[]types.ProfileKeyV1{{ModelId: "model", ProfileVersion: 1}},
	)
	require.NoError(t, err)
	require.Equal(t, left, checked)
	profiles, err := types.CanonicalSupportedProfilesHashV1(
		[]types.ProfileKeyV1{{ModelId: "model", ProfileVersion: 1}},
	)
	require.NoError(t, err)
	require.Equal(t, types.CanonicalSupportedProfilesHash(
		[]types.ProfileKeyV1{{ModelId: "model", ProfileVersion: 1}},
	), profiles)
}
