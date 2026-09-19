package types_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestIsBlockHeightV1BoundsTheInt64Domain(t *testing.T) {
	require.True(t, shared.IsBlockHeightV1(0))
	require.True(t, shared.IsBlockHeightV1(math.MaxInt64))
	require.False(t, shared.IsBlockHeightV1(math.MaxInt64+1))
	require.False(t, shared.IsBlockHeightV1(math.MaxUint64))
}

// CheckedHeightAddV1 must reject the whole [MaxInt64+1, MaxUint64] band, which
// CheckedAddUint64 accepts because it only reports a uint64 wrap. That band is
// representable but unusable: it cannot be converted to the int64 the consensus
// engine consumes, and a deadline placed there can never be swept.
func TestCheckedHeightAddV1RejectsAboveInt64WithoutWrapping(t *testing.T) {
	for _, tc := range []struct {
		name     string
		left     uint64
		right    uint64
		want     uint64
		overflow bool
	}{
		{name: "zero", left: 0, right: 0, want: 0},
		{name: "ordinary", left: 88019, right: 10000, want: 98019},
		{name: "exactly max", left: math.MaxInt64 - 1, right: 1, want: math.MaxInt64},
		{name: "one above max", left: math.MaxInt64, right: 1, overflow: true},
		{name: "timeout blocks at max", left: 88019, right: math.MaxInt64, overflow: true},
		{name: "wraps uint64", left: math.MaxUint64, right: 2, overflow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, overflow := shared.CheckedHeightAddV1(tc.left, tc.right)
			require.Equal(t, tc.overflow, overflow)
			if !tc.overflow {
				require.Equal(t, tc.want, value)
			}
		})
	}
}

// The uint64-only helper stays available for byte counters and counters, so the
// two must not be collapsed: it accepts exactly the band the height domain bans.
func TestCheckedAddUint64StillAcceptsTheAboveInt64Band(t *testing.T) {
	value, overflow := shared.CheckedAddUint64(math.MaxInt64, 1)
	require.False(t, overflow)
	require.Equal(t, uint64(math.MaxInt64)+1, value)

	_, overflow = shared.CheckedHeightAddV1(math.MaxInt64, 1)
	require.True(t, overflow, "the height domain must reject what uint64 accepts")
}
