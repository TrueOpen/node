package types

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

// HubKeeper is the only dependency task may hold for hub state access.
type HubKeeper = hubtypes.HubKeeper

// AuthKeeper resolves account public keys for user and coordinator signatures.
type AuthKeeper interface {
	GetAccount(context.Context, sdk.AccAddress) sdk.AccountI
}

// BankKeeper is the Task escrow custody boundary.
type BankKeeper interface {
	GetBalance(context.Context, sdk.AccAddress, string) sdk.Coin
	SendCoinsFromAccountToModule(context.Context, sdk.AccAddress, string, sdk.Coins) error
	SendCoinsFromModuleToAccount(context.Context, string, sdk.AccAddress, sdk.Coins) error
	SendCoinsFromModuleToModule(context.Context, string, string, sdk.Coins) error
}
