package types

import (
	"context"

	"cosmossdk.io/core/address"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

// StakingKeeper is the consensus validator set that anchor audit votes are
// weighed against (ADR-0040 v4): a vote counts with the validator's last
// recorded power, and a verdict is final at two thirds of the last total power.
type StakingKeeper interface {
	GetValidator(ctx context.Context, addr sdk.ValAddress) (stakingtypes.Validator, error)
	GetLastValidatorPower(ctx context.Context, operator sdk.ValAddress) (int64, error)
	GetLastTotalPower(ctx context.Context) (math.Int, error)
	ValidatorAddressCodec() address.Codec
}

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
