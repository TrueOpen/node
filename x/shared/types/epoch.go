package types

import (
	"fmt"
	"math"
)

// EpochForHeight uses the V1 half-open epoch convention. A zero length is
// invalid params; returning epoch zero keeps diagnostic callers total while
// params validation prevents it from reaching consensus execution.
func EpochForHeight(height, epochLength uint64) uint64 {
	if epochLength == 0 {
		return 0
	}
	return height / epochLength
}

func EpochHeightRange(epoch, epochLength uint64) (start, end uint64, err error) {
	if epochLength == 0 || epoch > math.MaxUint64/epochLength {
		return 0, 0, fmt.Errorf("epoch height overflow")
	}
	start = epoch * epochLength
	if start > math.MaxUint64-(epochLength-1) {
		return 0, 0, fmt.Errorf("epoch height overflow")
	}
	return start, start + epochLength - 1, nil
}
