package types

import (
	"math"
	"math/bits"
)

// MaxBlockHeightV1 is the inclusive upper bound of the usable block height
// domain. keeper_api_contract.md §3 models Height as uint64, but the consensus
// engine both
// publishes and consumes heights as int64, so any height above MaxInt64 is
// representable in the protocol types yet unusable everywhere it matters: it
// cannot be converted for a beacon lookup, a deadline sweep can never reach it,
// and an int64 Query consumer cannot even decode it. Producers of a height must
// therefore treat leaving this range exactly like a uint64 wrap.
const MaxBlockHeightV1 = uint64(math.MaxInt64)

// IsBlockHeightV1 reports whether value is inside the usable height domain.
func IsBlockHeightV1(value uint64) bool {
	return value <= MaxBlockHeightV1
}

// CheckedHeightAddV1 adds two height-domain values. Unlike CheckedAddUint64 it
// reports overflow as soon as the sum leaves [0, MaxBlockHeightV1], not only
// when it wraps uint64, because a sum above MaxInt64 is already unusable as a
// height. Every derived deadline height must go through this helper.
func CheckedHeightAddV1(left, right uint64) (uint64, bool) {
	value, overflow := CheckedAddUint64(left, right)
	if overflow || !IsBlockHeightV1(value) {
		return value, true
	}
	return value, false
}

func CheckedAddUint64(left, right uint64) (uint64, bool) {
	value, carry := bits.Add64(left, right, 0)
	return value, carry != 0
}

func CheckedMulUint64(left, right uint64) (uint64, bool) {
	high, low := bits.Mul64(left, right)
	return low, high != 0
}

func CheckedAddUint32(left, right uint32) (uint32, bool) {
	value := uint64(left) + uint64(right)
	return uint32(value), value > math.MaxUint32
}

func SaturatingAddUint64(left, right uint64) uint64 {
	value, overflow := CheckedAddUint64(left, right)
	if overflow {
		return math.MaxUint64
	}
	return value
}

func SaturatingMulUint64(left, right uint64) uint64 {
	value, overflow := CheckedMulUint64(left, right)
	if overflow {
		return math.MaxUint64
	}
	return value
}
