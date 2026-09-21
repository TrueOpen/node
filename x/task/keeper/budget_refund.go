package keeper

import (
	"context"

	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) releaseTaskBudgetRefund(ctx context.Context, owner string, refundAmount uint64) error {
	if refundAmount == 0 {
		return nil
	}
	ownerBytes, err := k.addressCodec.StringToBytes(owner)
	if err != nil {
		return errorsmod.Wrap(types.ErrInvalidUserAddress, err.Error())
	}
	denom := k.hubKeeper.GetHubParams(sdk.UnwrapSDKContext(ctx)).BusinessDenom
	if denom == "" {
		return errorsmod.Wrap(types.ErrInvariantBroken, "business denom is unavailable")
	}
	coins := sdk.NewCoins(sdk.NewCoin(denom, sdkmath.NewIntFromUint64(refundAmount)))
	if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, shared.TaskEscrowModuleName, sdk.AccAddress(ownerBytes), coins); err != nil {
		return errorsmod.Wrap(types.ErrInsufficientEscrow, err.Error())
	}
	return nil
}
