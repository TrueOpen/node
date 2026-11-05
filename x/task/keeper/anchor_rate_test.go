package keeper

import (
	"math/bits"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/task/types"
)

func TestAnchorAuditRateFollowsBond(t *testing.T) {
	// D = 0: nothing to lose, always audited.
	require.Equal(t, uint32(10000), anchorAuditRateBps(1_000, 0, 2500, 10000, 500))
	// D = R: s = 50%.
	require.Equal(t, uint32(5000), anchorAuditRateBps(1_000, 4_000, 2500, 10000, 500))
	// D = 9R: s = 10%.
	require.Equal(t, uint32(1000), anchorAuditRateBps(1_000, 36_000, 2500, 10000, 500))
	// D = 99R: 1% but floored at 5%.
	require.Equal(t, uint32(500), anchorAuditRateBps(1_000, 396_000, 2500, 10000, 500))
	// margin k = 2x doubles the gain side: D = 2R -> s = 50%.
	require.Equal(t, uint32(5000), anchorAuditRateBps(1_000, 8_000, 2500, 20000, 500))
	// R = 0: nothing to gain, the floor.
	require.Equal(t, uint32(500), anchorAuditRateBps(0, 8_000, 2500, 10000, 500))
	// Rounds up: D = 2R -> 33.34%.
	require.Equal(t, uint32(3334), anchorAuditRateBps(1_000, 8_000, 2500, 10000, 500))
}

func TestAnchorCoinSelects(t *testing.T) {
	require.True(t, anchorCoinSelects(0, 1))
	require.True(t, anchorCoinSelects(^uint64(0), 10000))
	require.False(t, anchorCoinSelects(^uint64(0), 9999))
	// threshold at exactly rate/10000 of 2^64
	hi, _ := bits.Mul64(^uint64(0)/2, 10000)
	require.True(t, hi < 5000 || hi == 4999)
	require.True(t, anchorCoinSelects(^uint64(0)/2, 5000))
	require.False(t, anchorCoinSelects(^uint64(0)/2+1, 5000))
	// empirical frequency of a 25% rate over the coin space is near 25%
	hits := 0
	for i := uint64(0); i < 10000; i++ {
		if anchorCoinSelects(anchorAuditCoin("chain", []byte{byte(i), byte(i >> 8)}, []byte("beacon")), 2500) {
			hits++
		}
	}
	require.InDelta(t, 2500, hits, 150)
}

func TestAnchorDeterrentIsTheCheaperExit(t *testing.T) {
	v := types.VerificationParamsV1{AnchorRejectSlashBps: 2500, AnchorTimeoutSlashBps: 500}
	require.Equal(t, uint32(500), anchorDeterrentSlashBps(v), "a cheater would time out rather than be rejected")
	v.AnchorTimeoutSlashBps = 3000
	require.Equal(t, uint32(2500), anchorDeterrentSlashBps(v))
	// D = 1e6 effective bond, R = 1126 (devnet order): 25% would give 0.45%, the 5% timeout gives 2.2%.
	require.Equal(t, uint32(45), anchorAuditRateBps(1126, 1_000_000, 2500, 10000, 0))
	require.Equal(t, uint32(221), anchorAuditRateBps(1126, 1_000_000, 500, 10000, 0))
}
