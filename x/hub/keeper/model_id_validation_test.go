package keeper

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelQueryRejectsTextInsteadOfHash32(t *testing.T) {
	require.ErrorContains(t, validateModelQueryID([]byte("hf/model")), "model_id")
}

// Restore RewardCompetition/eligible/P30 model-id assertions when
// their frozen states and runtime validators replace the removed legacy types.
