package bridge

import (
	"bytes"

	sdk "github.com/cosmos/cosmos-sdk/types"

	coretypes "github.com/bcp-innovations/hyperlane-cosmos/x/core/types"

	hubkeeper "github.com/TrueOpen/node/x/hub/keeper"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// BootstrapFeeExemption implements the bridge protocol,
// and nothing more.
//
// A shared testnet or mainnet starts with zero business_denom supply while
// business_denom is also the only fee denom, so the very first inbound transfer
// has no one who can pay for it. Genesis therefore binds exactly one message —
// by id, route, recipient, amount, fee payer and gas cap — that may be submitted
// with a zero fee. Every other gate still runs: signature, sequence, gas meter,
// ISM, and the whole inbound guard.
//
// §7.4 and §9 are explicit that this must never become a standing privilege, so
// the match is all-or-nothing and the ARMED -> CONSUMED flip (performed by the
// guard, in the same transition as the mint it paid for) is one-way.
type BootstrapFeeExemption struct {
	hub hubkeeper.Keeper
}

func NewBootstrapFeeExemption(hub hubkeeper.Keeper) BootstrapFeeExemption {
	return BootstrapFeeExemption{hub: hub}
}

// IsExempt reports whether this transaction is the one Genesis-bound bootstrap
// message. A near miss — a second message, a different signer, too much gas, a
// non-zero fee — is simply not exempt and is charged the ordinary fee; it is
// never an error, because an ordinary transaction that happens to touch the same
// route must keep working.
func (b BootstrapFeeExemption) IsExempt(ctx sdk.Context, tx sdk.Tx) bool {
	feeTx, ok := tx.(sdk.FeeTx)
	if !ok {
		return false
	}
	// "a Tx carries exactly one Msg": a bundled transaction could smuggle
	// arbitrary free execution alongside the bootstrap message.
	msgs := tx.GetMsgs()
	if len(msgs) != 1 {
		return false
	}
	process, ok := msgs[0].(*coretypes.MsgProcessMessage)
	if !ok {
		return false
	}
	if !feeTx.GetFee().IsZero() {
		return false
	}
	bootstrap, err := b.hub.BridgeBootstrap.Get(ctx)
	if err != nil || bootstrap.Mode != hubtypes.BridgeBootstrapModeV1_BRIDGE_BOOTSTRAP_MODE_V1_ARMED {
		return false
	}
	if gas := feeTx.GetGas(); gas == 0 || gas > bootstrap.GetMaxGas() {
		return false
	}
	if bootstrap.GetFeePayer() != feePayerAddress(feeTx) {
		return false
	}
	route, err := b.hub.BridgeRoute.Get(ctx)
	if err != nil {
		return false
	}
	transfer, err := parseBridgeTransfer(process, route)
	if err != nil {
		return false
	}
	amount, err := shared.ParseAmount(*bootstrap.Amount)
	if err != nil {
		return false
	}
	return hubkeeper.BridgeBootstrapMatches(bootstrap, route, transfer.MessageID, transfer.Counterpart, amount) &&
		bytes.Equal(bootstrap.GetRouteId(), route.UsdcRouteId)
}

// feePayerAddress is the account the fee would be taken from: an explicit
// granter when one is set, otherwise the first signer. §5.3 binds the exemption
// to that account, not merely to whoever relayed the message.
func feePayerAddress(feeTx sdk.FeeTx) string {
	if granter := feeTx.FeeGranter(); len(granter) != 0 {
		return sdk.AccAddress(granter).String()
	}
	payer := feeTx.FeePayer()
	if len(payer) == 0 {
		return ""
	}
	return sdk.AccAddress(payer).String()
}

// AnteHandle clears the validator min-gas-price floor for the single
// Genesis-bound bootstrap transaction, and does nothing else.
//
// That floor is the only thing standing between a zero-fee transaction and
// inclusion: the SDK's fee check enforces min gas prices in CheckTx only, and
// deducting a zero fee is already a no-op. Clearing it for exactly this
// transaction is therefore the whole exemption — no fee-checking logic is
// reimplemented here, so every other transaction keeps the unmodified rule and
// there is no second copy of it to drift.
func (b BootstrapFeeExemption) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
	if b.IsExempt(ctx, tx) {
		ctx = ctx.WithMinGasPrices(sdk.DecCoins{})
	}
	return next(ctx, tx, simulate)
}
