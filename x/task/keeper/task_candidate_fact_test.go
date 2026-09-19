package keeper

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkerCandidateWeightUsesNeutralPerformance(t *testing.T) {
	weight, err := workerCandidateWeightPpm(3_000_000, 1_000_000, 1_000_000, 1_000_000)
	require.NoError(t, err)
	require.Equal(t, uint32(1_000_000), weight)
	weight, err = workerCandidateWeightPpm(1_000_000, 1_000_000, 1_000_000, 500_000)
	require.NoError(t, err)
	require.Equal(t, uint32(399_999), weight)
}

func TestWorkerCandidateWeightDoesNotOverflowAtUint64Scale(t *testing.T) {
	weight, err := workerCandidateWeightPpm(math.MaxUint64, math.MaxUint64, 1_000_000, 1_000_000)
	require.NoError(t, err)
	require.Equal(t, uint32(799_999), weight)
}

func TestCandidateJailFactorPpm(t *testing.T) {
	require.Equal(t, uint32(1_000_000), candidateJailFactorPpm(0))
	require.Equal(t, uint32(500_000), candidateJailFactorPpm(1))
	require.Equal(t, uint32(250_000), candidateJailFactorPpm(2))
	require.Zero(t, candidateJailFactorPpm(3))
}
