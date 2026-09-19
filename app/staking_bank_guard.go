package app

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// GovernedStakingBankKeeper prevents a completed Validator exit from producing
// transferable ubond. Ordinary staking mutations are closed at ante; therefore
// a pool-to-account ubond undelegation is the maturity leg of BurnBondV1 and is
// burned directly from the staking pool.
type GovernedStakingBankKeeper struct {
	bankkeeper.Keeper
}

func ProvideGovernedStakingBankKeeper(bank bankkeeper.Keeper) GovernedStakingBankKeeper {
	return GovernedStakingBankKeeper{Keeper: bank}
}

func (k GovernedStakingBankKeeper) UndelegateCoinsFromModuleToAccount(
	ctx context.Context,
	senderModule string,
	recipient sdk.AccAddress,
	amount sdk.Coins,
) error {
	if senderModule == stakingtypes.BondedPoolName || senderModule == stakingtypes.NotBondedPoolName {
		if len(amount) != 1 || amount[0].Denom != sdk.DefaultBondDenom || !amount[0].Amount.IsPositive() {
			return fmt.Errorf("governed validator exit must return one positive %s coin", sdk.DefaultBondDenom)
		}
		return k.Keeper.BurnCoins(ctx, senderModule, amount)
	}
	return k.Keeper.UndelegateCoinsFromModuleToAccount(ctx, senderModule, recipient, amount)
}

var _ stakingtypes.BankKeeper = GovernedStakingBankKeeper{}
