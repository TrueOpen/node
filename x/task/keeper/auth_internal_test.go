package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

type taskInternalAuthKeeper struct{}

func (taskInternalAuthKeeper) GetAccount(context.Context, sdk.AccAddress) sdk.AccountI { return nil }

type taskInternalBankKeeper struct{}

func (taskInternalBankKeeper) GetBalance(context.Context, sdk.AccAddress, string) sdk.Coin {
	return sdk.Coin{}
}
func (taskInternalBankKeeper) SendCoinsFromAccountToModule(context.Context, sdk.AccAddress, string, sdk.Coins) error {
	return nil
}
func (taskInternalBankKeeper) SendCoinsFromModuleToAccount(context.Context, string, sdk.AccAddress, sdk.Coins) error {
	return nil
}

func (taskInternalBankKeeper) SendCoinsFromModuleToModule(context.Context, string, string, sdk.Coins) error {
	return nil
}
