// Package bridge holds the TrueOpen-owned wiring that turns the pinned upstream
// Hyperlane modules into the single canonical USDC path
// (cross_chain_asset_bridge_protocol.md §3.2). It contains no copy of Hyperlane
// proto, state, handler or ISM verification logic: everything here either
// constrains what the upstream modules may do, or observes what they did.
package bridge

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	coretypes "github.com/bcp-innovations/hyperlane-cosmos/x/core/types"
	warptypes "github.com/bcp-innovations/hyperlane-cosmos/x/warp/types"

	hubkeeper "github.com/TrueOpen/node/x/hub/keeper"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

// BankKeeper is the subset of the real bank keeper the Hyperlane modules and
// this guard need.
type BankKeeper interface {
	SendCoinsFromAccountToModule(ctx context.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error
	SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error
	BurnCoins(ctx context.Context, moduleName string, amt sdk.Coins) error
	MintCoins(ctx context.Context, moduleName string, amt sdk.Coins) error
	GetSupply(ctx context.Context, denom string) sdk.Coin
}

// GuardedBankKeeper is the only bank keeper the Hyperlane modules ever see.
//
// cross_chain_asset_bridge_protocol.md §5.1 makes the warp module the sole
// legal source of business_denom mint and burn, and notes that Cosmos module
// permissions are not denom-scoped — a module holding Minter may mint
// anything. Interposing here
// turns that open permission into a closed set, and makes the mint/burn call the
// place where the §7.3 guard, the §7.2 epoch limit and the §5.2 conservation
// identity are enforced, all inside the upstream handler's own context so a
// rejection leaves zero writes.
//
// Placing the guard at the ledger rather than around the Msg handler is the one
// intentional divergence from §3.2's decorator sketch: it is strictly harder to
// bypass, because no upstream code path can reach business_denom without passing
// through MintCoins/BurnCoins.
type GuardedBankKeeper struct {
	inner BankKeeper
	hub   hubkeeper.Keeper
}

func NewGuardedBankKeeper(inner BankKeeper, hub hubkeeper.Keeper) GuardedBankKeeper {
	return GuardedBankKeeper{inner: inner, hub: hub}
}

var (
	_ warptypes.BankKeeper = GuardedBankKeeper{}
	_ coretypes.BankKeeper = GuardedBankKeeper{}
)

func (g GuardedBankKeeper) SendCoinsFromAccountToModule(ctx context.Context, sender sdk.AccAddress, recipientModule string, amt sdk.Coins) error {
	return g.inner.SendCoinsFromAccountToModule(ctx, sender, recipientModule, amt)
}

func (g GuardedBankKeeper) SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipient sdk.AccAddress, amt sdk.Coins) error {
	return g.inner.SendCoinsFromModuleToAccount(ctx, senderModule, recipient, amt)
}

func (g GuardedBankKeeper) GetSupply(ctx context.Context, denom string) sdk.Coin {
	return g.inner.GetSupply(ctx, denom)
}

// MintCoins is the only inbound path. The guard runs before the ledger moves so
// a refused transfer mints nothing at all, rather than minting and rolling back.
func (g GuardedBankKeeper) MintCoins(ctx context.Context, moduleName string, amt sdk.Coins) error {
	return g.bracket(ctx, hubkeeper.BridgeInbound, moduleName, amt, g.inner.MintCoins)
}

// BurnCoins is the only outbound path and the only legal burn of
// business_denom anywhere on the chain: §5.1 routes every business-side
// forfeiture to the treasury instead.
func (g GuardedBankKeeper) BurnCoins(ctx context.Context, moduleName string, amt sdk.Coins) error {
	return g.bracket(ctx, hubkeeper.BridgeOutbound, moduleName, amt, g.inner.BurnCoins)
}

// bracket runs the §3.2 order around one ledger call: guard, then the upstream
// move, then the supply-delta assertion and the accounting.
//
// The supply is read on both sides rather than assumed, because the assertion is
// the only thing that ties this module's counters to what the ledger actually
// did. Every refusal happens in the first step, so a rejected transfer never
// mints and rolls back, which §7.3 forbids outright.
func (g GuardedBankKeeper) bracket(
	ctx context.Context,
	direction hubkeeper.BridgeDirection,
	moduleName string,
	amt sdk.Coins,
	move func(context.Context, string, sdk.Coins) error,
) error {
	transfer, route, err := g.authorize(ctx, direction, moduleName, amt)
	if err != nil {
		return err
	}
	before := g.hub.BusinessDenomSupply(ctx, route.BusinessDenom)
	if err := move(ctx, moduleName, amt); err != nil {
		return err
	}
	if direction == hubkeeper.BridgeOutbound {
		return g.hub.RecordPendingOutboundBridgeTransfer(ctx, route, transfer, before)
	}
	return g.hub.SettleBridgeTransfer(ctx, route, transfer, before)
}

func (g GuardedBankKeeper) authorize(
	ctx context.Context,
	direction hubkeeper.BridgeDirection,
	moduleName string,
	amt sdk.Coins,
) (hubkeeper.BridgeTransferContext, hubtypes.BridgeRouteState, error) {
	var (
		none    hubkeeper.BridgeTransferContext
		noRoute hubtypes.BridgeRouteState
	)
	if moduleName != warptypes.ModuleName {
		return none, noRoute, fmt.Errorf("only the %s module may mint or burn through the bridge, not %s", warptypes.ModuleName, moduleName)
	}
	// One coin per transfer. A multi-coin mint would make "the amount" ambiguous
	// for the limit and the conservation identity, and the upstream synthetic
	// path never produces one.
	if len(amt) != 1 {
		return none, noRoute, fmt.Errorf("bridge %s must move exactly one coin, got %d", direction, len(amt))
	}
	coin := amt[0]
	if !coin.Amount.IsUint64() {
		return none, noRoute, fmt.Errorf("bridge %s amount is outside uint64", direction)
	}
	// The message-level facts were recorded at the decorated Msg boundary; the
	// ledger call itself cannot see the Hyperlane message id or the EVM-side
	// recipient.
	transfer, ok := g.hub.TakeBridgeTransferIntent(ctx)
	if !ok {
		// A mint or burn that did not come from a decorated bridge message has no
		// message-level facts behind it, so it cannot be authorised at all.
		return none, noRoute, fmt.Errorf("bridge %s outside a decorated bridge message is not permitted", direction)
	}
	transfer.Direction = direction
	transfer.Denom = coin.Denom
	transfer.Amount = coin.Amount.Uint64()
	route, err := g.hub.PrepareBridgeTransfer(ctx, transfer)
	if err != nil {
		return none, noRoute, err
	}
	return transfer, route, nil
}
