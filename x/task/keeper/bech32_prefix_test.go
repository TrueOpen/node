package keeper

import (
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/wire/bus"
)

// The keeper builds its address codec from the global SDK config, and every
// frozen operator address in this package's testdata is bech32 "trueopen". wire
// gates Bus and evidence operators on that exact HRP, so a test binary left on
// the SDK default would exercise a codec production never uses and would fail
// those gates for the wrong reason. The prefix is taken from wire rather than
// spelled out again so the two can never drift.
func init() {
	sdk.GetConfig().SetBech32PrefixForAccount(
		bus.OperatorAddressHRPV1, bus.OperatorAddressHRPV1+"pub",
	)
}
