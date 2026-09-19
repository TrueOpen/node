package keeper

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
)

func TestRuntimeStateValidatorsRejectPathUnsafeModelID(t *testing.T) {
	const invalidModelID = "hf/model"

	require.ErrorContains(t, validateCanonicalSupportedProfiles([]types.ProfileKeyV1{{
		ModelId: invalidModelID, ProfileVersion: 1,
	}}, 1), "model_id")
}

// Restore RewardCompetition/eligible/P30 model-id assertions when
// their frozen states and runtime validators replace the removed legacy types.
