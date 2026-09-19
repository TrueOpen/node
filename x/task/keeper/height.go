package keeper

import shared "github.com/TrueOpen/node/x/shared/types"

func checkedHeightAdd(left, right uint64) (uint64, bool) {
	return shared.CheckedHeightAddV1(left, right)
}

func checkedAddUint64(left, right uint64) (uint64, bool) {
	return shared.CheckedAddUint64(left, right)
}
