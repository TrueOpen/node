// Package dispatchpool supplies a real Hub pool for cross-module keeper tests.
package dispatchpool

import (
	"cosmossdk.io/core/address"
	corestore "cosmossdk.io/core/store"
	"github.com/cosmos/cosmos-sdk/codec"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
)

type unusedAuth struct{ types.AuthKeeper }
type unusedBank struct{ types.BankKeeper }

// NewKeeper shares the caller's SDK stores. Pool operations do not use auth or
// bank; an unexpected dependency call fails instead of silently mocking it.
func NewKeeper(store corestore.KVStoreService, transient corestore.TransientStoreService, cdc codec.Codec, addresses address.Codec, authority []byte) keeper.Keeper {
	return keeper.NewKeeper(store, transient, cdc, addresses, authority, unusedAuth{}, unusedBank{})
}
