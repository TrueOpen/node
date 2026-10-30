package keeper

import sdk "github.com/cosmos/cosmos-sdk/types"

// Frozen fixtures use the chain account prefix rather than the SDK default.
func init() {
	sdk.GetConfig().SetBech32PrefixForAccount("trueopen", "trueopenpub")
}
