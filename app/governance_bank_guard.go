package app

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"

	hubkeeper "github.com/TrueOpen/node/x/hub/keeper"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

// GovernedGovBankKeeper turns every SDK governance deposit burn into the
// protocol-mandated Treasury residual. x/gov still decides when a deposit is
// forfeited; only the destination changes, and USDC is never destroyed.
type GovernedGovBankKeeper struct {
	bankkeeper.Keeper
	hub hubkeeper.Keeper
}

func ProvideGovernedGovBankKeeper(bank bankkeeper.Keeper, hub hubkeeper.Keeper) GovernedGovBankKeeper {
	return GovernedGovBankKeeper{Keeper: bank, hub: hub}
}

func (k GovernedGovBankKeeper) BurnCoins(ctx context.Context, moduleName string, amount sdk.Coins) error {
	if moduleName != govtypes.ModuleName {
		return fmt.Errorf("governance bank guard cannot burn for module %s", moduleName)
	}
	if len(amount) != 1 {
		return fmt.Errorf("governance deposit residual must contain exactly one coin")
	}
	params := k.hub.GetHubParams(sdk.UnwrapSDKContext(ctx))
	if amount[0].Denom != params.BusinessDenom || !amount[0].Amount.IsUint64() || !amount[0].Amount.IsPositive() {
		return fmt.Errorf("governance deposit residual must be positive %s", params.BusinessDenom)
	}
	if err := k.Keeper.SendCoinsFromModuleToModule(ctx, govtypes.ModuleName, hubtypes.TreasuryModuleName, amount); err != nil {
		return err
	}
	return k.hub.CreditGovernanceDepositResidual(ctx, amount[0].Amount.Uint64())
}

var _ govtypes.BankKeeper = GovernedGovBankKeeper{}
