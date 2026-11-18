package types_test

import (
	"math"
	"testing"

	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
)

func TestProofBindingHeightsCannotLosePresence(t *testing.T) {
	current := secp256k1.GenPrivKey().PubKey().Bytes()
	previous := secp256k1.GenPrivKey().PubKey().Bytes()
	for _, height := range []uint64{0, uint64(math.MaxInt64) + 1} {
		require.Error(t, types.ValidateRevokedServiceKeyBinding(types.ServiceKeyStatusRevoked, height, true))
		require.Error(t, types.ValidatePreviousServiceKeyBinding(2, current, previous, 1, height))
	}
	require.NoError(t, types.ValidateRevokedServiceKeyBinding(types.ServiceKeyStatusRevoked, 0, false), "terminal revocation has no explicit cutoff")
	require.NoError(t, types.ValidateRevokedServiceKeyBinding(types.ServiceKeyStatusRevoked, 1, true))
	require.NoError(t, types.ValidatePreviousServiceKeyBinding(2, current, previous, 1, 1))
}
