package keeper_test

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

type taskExternalAuthKeeper struct{}

func (taskExternalAuthKeeper) GetAccount(context.Context, sdk.AccAddress) sdk.AccountI { return nil }

type taskExternalBankKeeper struct{}

func (taskExternalBankKeeper) GetBalance(context.Context, sdk.AccAddress, string) sdk.Coin {
	return sdk.Coin{}
}
func (taskExternalBankKeeper) SendCoinsFromAccountToModule(context.Context, sdk.AccAddress, string, sdk.Coins) error {
	return nil
}
func (taskExternalBankKeeper) SendCoinsFromModuleToAccount(context.Context, string, sdk.AccAddress, sdk.Coins) error {
	return nil
}

func (taskExternalBankKeeper) SendCoinsFromModuleToModule(context.Context, string, string, sdk.Coins) error {
	return nil
}
