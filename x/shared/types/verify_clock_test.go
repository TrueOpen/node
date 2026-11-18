package types

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckedWindowSumsRejectIntermediateWrap(t *testing.T) {
	value, overflow := CheckedSumUint64(1, math.MaxUint64-1)
	require.False(t, overflow)
	require.Equal(t, uint64(math.MaxUint64), value)
	_, overflow = CheckedSumUint64(1, math.MaxUint64, 1)
	require.True(t, overflow)
	require.Error(t, ValidateVerifyOpenClock(math.MaxUint64, 1, math.MaxUint64, 1))
}
