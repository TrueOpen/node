package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/TrueOpen/node/x/hub/types"
)

// bridgeUpstreamHolder is shared by every value copy derived from one Keeper.
// Depinject creates those copies before the Hyperlane keepers are available, so
// installing the adapter on the AppModule copy must also make it visible to the
// app, ante and guarded-bank copies created from the same keeper.
type bridgeUpstreamHolder struct {
	upstream types.BridgeUpstream
}

// WithBridgeUpstream returns the module-owned Keeper copy that can read the
// Hyperlane objects. The dependency is installed by app wiring rather than taken
// in NewKeeper for the same reason the freeze and validator adapters are: the
// Task module holds a dependency-free HubKeeper copy and must not drag the
// upstream modules in behind it.
func (k Keeper) WithBridgeUpstream(upstream types.BridgeUpstream) Keeper {
	if k.bridgeUpstream == nil {
		k.bridgeUpstream = &bridgeUpstreamHolder{}
	}
	k.bridgeUpstream.upstream = upstream
	return k
}

// ValidateBridgeUpstream re-derives the §2.1 / §3.3 / §3.4 / §4.4 agreement
// between the frozen route and the live upstream objects.
//
// It is a no-op when the chain has no bridge, and when no adapter is installed
// it reports that rather than passing: a check that silently does nothing is
// worse than one that is absent, because it reads like coverage.
func (k Keeper) ValidateBridgeUpstream(ctx context.Context) error {
	route, err := k.BridgeRoute.Get(ctx)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil
		}
		return err
	}
	if k.bridgeUpstream == nil || k.bridgeUpstream.upstream == nil {
		return fmt.Errorf("bridge upstream adapter is not installed, so the route cannot be checked against the Hyperlane objects")
	}
	signerSet, err := k.BridgeSignerSet.Get(ctx)
	if err != nil {
		return err
	}
	authority, err := k.addressCodec.BytesToString(k.authority)
	if err != nil {
		return err
	}
	if expected, err := k.addressCodec.BytesToString(authtypes.NewModuleAddress(types.GovModuleName)); err == nil && expected != authority {
		// The keeper authority is what x/gov actually executes as; §4.4 requires
		// the upstream owners to be that same account.
		authority = expected
	}
	return types.ValidateBridgeUpstream(ctx, k.bridgeUpstream.upstream, route, signerSet.LocalIsmId, authority)
}
