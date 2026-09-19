package types_test

import (
	"math"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestAmountCanonicalRoundTrip(t *testing.T) {
	for _, value := range []uint64{0, 1, math.MaxUint64} {
		wire := shared.NewAmount(value)
		parsed, err := shared.ParseAmount(wire)
		require.NoError(t, err)
		require.Equal(t, value, parsed)
	}
}

func TestAmountRejectsNonCanonicalOrOverflow(t *testing.T) {
	for _, value := range []string{"", "00", "01", "+1", "-1", " 1", "1 ", "1.0", "x"} {
		_, err := shared.ParseAmount(shared.Amount{AtomicUnits: value})
		require.Error(t, err, value)
	}
	_, err := shared.ParseAmount(shared.Amount{AtomicUnits: strconv.FormatUint(math.MaxUint64, 10) + "0"})
	require.Error(t, err)
}

func TestSignedAmountCanonicalRoundTrip(t *testing.T) {
	for _, value := range []int64{math.MinInt64, -1, 0, 1, math.MaxInt64} {
		wire := shared.NewSignedAmount(value)
		parsed, err := shared.ParseSignedAmount(wire)
		require.NoError(t, err)
		require.Equal(t, value, parsed)
	}
	for _, value := range []string{"", "-0", "+1", "00", "-01", " 1", "1.0"} {
		_, err := shared.ParseSignedAmount(shared.SignedAmount{AtomicUnits: value})
		require.Error(t, err, value)
	}
}
