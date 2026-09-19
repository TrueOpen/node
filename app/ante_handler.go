package app

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/auth/ante"

	appbridge "github.com/TrueOpen/node/app/bridge"
)

// installBridgeAnteHandler keeps the SDK account, fee, gas and replay gates,
// replaces only its permissive signature verification with TrueOpen's strict
// two-path verifier, then appends the bridge guards.
func (app *App) installBridgeAnteHandler() error {
	if app.txConfig.SignModeHandler() == nil || app.legacyAmino == nil {
		return fmt.Errorf("account ante handler: signing codecs are required")
	}
	stockSignatureChecks := ante.NewSigVerificationDecorator(app.AuthKeeper, app.txConfig.SignModeHandler())
	pathGate := newSignaturePathDecorator(app.HubKeeper)
	strictVerifier := newSignatureVerificationDecorator(
		app.AuthKeeper, app.HubKeeper, app.txConfig.SignModeHandler(), app.legacyAmino, stockSignatureChecks,
	)
	stock := sdk.ChainAnteDecorators(
		ante.NewSetUpContextDecorator(),
		ante.NewValidateBasicDecorator(),
		pathGate,
		ante.NewTxTimeoutHeightDecorator(),
		ante.NewValidateMemoDecorator(app.AuthKeeper),
		ante.NewConsumeGasForTxSizeDecorator(app.AuthKeeper),
		ante.NewDeductFeeDecorator(app.AuthKeeper, app.BankKeeper, nil, nil),
		ante.NewSetPubKeyDecorator(app.AuthKeeper),
		ante.NewValidateSigCountDecorator(app.AuthKeeper),
		ante.NewSigGasConsumeDecorator(app.AuthKeeper, trueopenSignatureGasConsumer),
		strictVerifier,
		ante.NewIncrementSequenceDecorator(app.AuthKeeper),
	)
	bootstrap := appbridge.NewBootstrapFeeExemption(app.HubKeeper)
	stakingGuard := appbridge.NewStakingExitGuard(app.StakingKeeper)
	decorator := appbridge.NewBridgeDecorator(app.HubKeeper)
	terminal := func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) { return ctx, nil }
	app.SetAnteHandler(func(ctx sdk.Context, tx sdk.Tx, simulate bool) (sdk.Context, error) {
		// BootstrapFeeExemption only relaxes the min-gas-price floor and writes
		// nothing, so it runs first, before the stock fee decorator reads it.
		ctx, err := bootstrap.AnteHandle(ctx, tx, simulate, stock)
		if err != nil {
			return ctx, err
		}
		// The exit guard only rejects and writes nothing, so it goes ahead of the
		// writing decorator: a transaction it turns away never leaves bridge
		// intents behind in the transient store.
		ctx, err = stakingGuard.AnteHandle(ctx, tx, simulate, func(ctx sdk.Context, _ sdk.Tx, _ bool) (sdk.Context, error) {
			return ctx, nil
		})
		if err != nil {
			return ctx, err
		}
		// BridgeDecorator writes the per-message intents, so it runs last: by then
		// SetUpContextDecorator has installed the real gas meter and those writes
		// are metered like any other. Running it first would give an attacker
		// unmetered store writes in the ante phase.
		return decorator.AnteHandle(ctx, tx, simulate, terminal)
	})
	return nil
}
