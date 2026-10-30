package types

import (
	"math"
	"math/big"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCandidateWeightPPMMatchesFullPrecisionArithmetic(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	for i := 0; i < 2000; i++ {
		bond, minimum := rng.Uint64()|1, rng.Uint64()|1
		if i == 0 {
			bond, minimum = math.MaxUint64, math.MaxUint64
		}
		performance := uint32(rng.Intn(1_000_000) + 1)
		jail := CandidateJailFactorPPM(uint64(i % 3))
		stake := uint32(rng.Intn(1_000_001))
		score := new(big.Int).SetUint64(bond)
		score.Mul(score, big.NewInt(1_000_000))
		denominator := new(big.Int).SetUint64(minimum)
		denominator.Mul(denominator, big.NewInt(3))
		score.Div(score, denominator)
		if score.Cmp(big.NewInt(1_000_000)) > 0 {
			score.SetInt64(1_000_000)
		}
		score.Mul(score, new(big.Int).SetUint64(uint64(stake)))
		score.Add(score, new(big.Int).SetUint64(uint64(performance)*uint64(1_000_000-stake)))
		score.Div(score, big.NewInt(1_000_000))
		score.Mul(score, new(big.Int).SetUint64(uint64(jail)))
		score.Div(score, big.NewInt(1_000_000))
		got, err := CandidateWeightPPM(bond, minimum, performance, jail, stake, 1_000_000-stake, 1_000_000)
		if score.Sign() == 0 {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, uint32(score.Uint64()), got)
	}
}

func TestWorkerCandidateWeightPolicyBoundaries(t *testing.T) {
	for _, stake := range []uint32{0, 300_000, 1_000_000} {
		require.NoError(t, (WorkerCandidateWeightPolicy{stake, 1_000_000 - stake}).Validate())
	}
	for _, p := range []WorkerCandidateWeightPolicy{{}, {math.MaxUint32, 1_000_001}, {1, 1_000_000}} {
		require.Error(t, p.Validate())
	}
	for count, want := range []uint32{1_000_000, 500_000, 250_000, 0} {
		require.Equal(t, want, CandidateJailFactorPPM(uint64(count)))
	}
	require.Zero(t, CandidateJailFactorPPM(math.MaxUint64))
	for _, bad := range []struct {
		bond, minimum                           uint64
		perf, jail, stake, performance, maximum uint32
	}{
		{0, 1, 1_000_000, 1_000_000, 300_000, 700_000, 1_000_000},
		{1, 0, 1_000_000, 1_000_000, 300_000, 700_000, 1_000_000},
		{1, 1, 1_000_001, 1_000_000, 300_000, 700_000, 1_000_000},
		{1, 1, 1_000_000, 0, 300_000, 700_000, 1_000_000},
		{1, 1, 1_000_000, 1_000_000, 300_000, 700_001, 1_000_000},
		{1, 1, 1_000_000, 1_000_000, 300_000, 700_000, 1},
	} {
		_, err := CandidateWeightPPM(bad.bond, bad.minimum, bad.perf, bad.jail, bad.stake, bad.performance, bad.maximum)
		require.Error(t, err)
	}
}
