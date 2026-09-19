package app

import (
	"crypto/sha256"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	hubkeeper "github.com/TrueOpen/node/x/hub/keeper"
)

// taskGasPostHandler records reimbursements only after all messages succeeded.
// Keeper handlers leave block-scoped intents for first-APPLIED eligible items;
// a transaction shape outside the single-Task-Msg contract simply receives no
// reimbursement and otherwise keeps its normal execution result.
func (app *App) taskGasPostHandler(
	ctx sdk.Context,
	tx sdk.Tx,
	simulate bool,
	success bool,
) (sdk.Context, error) {
	if !success {
		return ctx, nil
	}
	// genutil executes fixed genesis transactions before TrueOpen module params are
	// initialized. They are not runtime transactions and cannot be reimbursed.
	if ctx.BlockHeight() == 0 {
		return ctx, nil
	}
	dispatches, err := app.bridgeUpstream.OutboundDispatches(sdk.WrapSDKContext(ctx), tx.GetMsgs())
	if err != nil {
		return ctx, fmt.Errorf("finalize outbound bridge transfer: %w", err)
	}
	if len(dispatches) != 0 {
		finalized := make([]hubkeeper.BridgeTransferContext, len(dispatches))
		for index, dispatch := range dispatches {
			finalized[index] = hubkeeper.BridgeTransferContext{
				Direction: hubkeeper.BridgeOutbound, MessageID: dispatch.MessageID,
				Counterpart: dispatch.Sender, DestinationRecipient: dispatch.DestinationRecipient,
				Denom: dispatch.Denom, Amount: dispatch.Amount,
			}
		}
		if err := app.HubKeeper.FinalizePendingOutboundBridgeTransfers(sdk.WrapSDKContext(ctx), finalized); err != nil {
			return ctx, fmt.Errorf("finalize outbound bridge transfer: %w", err)
		}
	}
	if simulate || len(tx.GetMsgs()) != 1 {
		return ctx, nil
	}
	feeTx, ok := tx.(sdk.FeeTx)
	if !ok || len(ctx.TxBytes()) == 0 {
		return ctx, nil
	}
	payer := feeTx.FeeGranter()
	if len(payer) == 0 {
		payer = feeTx.FeePayer()
	}
	if len(payer) == 0 {
		return ctx, fmt.Errorf("task gas reimbursement fee payer is empty")
	}
	denom := app.HubKeeper.GetHubParams(ctx).BusinessDenom
	if denom == "" {
		return ctx, fmt.Errorf("task gas reimbursement business denom is unavailable")
	}
	actualFee := feeTx.GetFee().AmountOf(denom)
	if actualFee.IsNegative() || !actualFee.IsUint64() {
		return ctx, fmt.Errorf("task gas reimbursement fee is outside uint64")
	}
	txHash := sha256.Sum256(ctx.TxBytes())
	if err := app.TaskKeeper.ApplyTaskGasReimbursementIntents(
		sdk.WrapSDKContext(ctx), txHash[:], sdk.AccAddress(payer).String(),
		ctx.GasMeter().GasConsumed(), actualFee.Uint64(),
	); err != nil {
		return ctx, err
	}
	return ctx, nil
}
