package keeper_test

import (
	"context"
	"fmt"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/TrueOpen/node/x/hub/types"
)

type hubMockBankKeeper struct {
	balances                 map[string]map[string]uint64
	sendModuleToAccountError error
}

type hubMockAuthKeeper struct {
	accounts map[string]sdk.AccountI
}

func newHubMockAuthKeeper() *hubMockAuthKeeper {
	return &hubMockAuthKeeper{accounts: map[string]sdk.AccountI{}}
}

func (a *hubMockAuthKeeper) GetAccount(_ context.Context, address sdk.AccAddress) sdk.AccountI {
	return a.accounts[address.String()]
}

func newHubMockBankKeeper() *hubMockBankKeeper {
	return &hubMockBankKeeper{balances: map[string]map[string]uint64{}}
}

func (b *hubMockBankKeeper) GetBalance(_ context.Context, addr sdk.AccAddress, denom string) sdk.Coin {
	return sdk.NewCoin(denom, sdkmath.NewIntFromUint64(b.balance(addr.String(), denom)))
}

// GetSupply sums the tracked balances so the bridge invariant sees the same
// total the mock actually holds rather than a separately maintained number that
// could agree with the counters while the balances do not.
func (b *hubMockBankKeeper) GetSupply(_ context.Context, denom string) sdk.Coin {
	total := uint64(0)
	for _, byDenom := range b.balances {
		total += byDenom[denom]
	}
	return sdk.NewCoin(denom, sdkmath.NewIntFromUint64(total))
}

func (b *hubMockBankKeeper) SendCoinsFromAccountToModule(_ context.Context, from sdk.AccAddress, moduleName string, amt sdk.Coins) error {
	return b.transfer(from.String(), authtypes.NewModuleAddress(moduleName).String(), amt)
}

func (b *hubMockBankKeeper) SendCoinsFromModuleToAccount(_ context.Context, moduleName string, to sdk.AccAddress, amt sdk.Coins) error {
	if b.sendModuleToAccountError != nil {
		return b.sendModuleToAccountError
	}
	return b.transfer(authtypes.NewModuleAddress(moduleName).String(), to.String(), amt)
}

func (b *hubMockBankKeeper) SendCoinsFromModuleToModule(_ context.Context, fromModule, toModule string, amt sdk.Coins) error {
	return b.transfer(authtypes.NewModuleAddress(fromModule).String(), authtypes.NewModuleAddress(toModule).String(), amt)
}

func (b *hubMockBankKeeper) seedModule(moduleName string, amount uint64) {
	b.add(authtypes.NewModuleAddress(moduleName).String(), types.DefaultBusinessDenom, amount)
}

func (b *hubMockBankKeeper) seedAccount(address string, amount uint64) {
	b.add(address, types.DefaultBusinessDenom, amount)
}

func (b *hubMockBankKeeper) moduleBalance(moduleName string) uint64 {
	return b.balance(authtypes.NewModuleAddress(moduleName).String(), types.DefaultBusinessDenom)
}

func (b *hubMockBankKeeper) accountBalance(address string) uint64 {
	return b.balance(address, types.DefaultBusinessDenom)
}

func (b *hubMockBankKeeper) transfer(from, to string, coins sdk.Coins) error {
	for _, coin := range coins {
		if !coin.Amount.IsUint64() {
			return fmt.Errorf("coin amount must fit uint64")
		}
		if b.balance(from, coin.Denom) < coin.Amount.Uint64() {
			return fmt.Errorf("insufficient funds in %s", from)
		}
	}
	for _, coin := range coins {
		amount := coin.Amount.Uint64()
		b.balances[from][coin.Denom] -= amount
		b.add(to, coin.Denom, amount)
	}
	return nil
}

func (b *hubMockBankKeeper) add(address, denom string, amount uint64) {
	if b.balances[address] == nil {
		b.balances[address] = map[string]uint64{}
	}
	b.balances[address][denom] += amount
}

// burn removes tracked supply so a test can model the bridge destroying
// business_denom before asking the guard to account for it.
func (b *hubMockBankKeeper) burn(address, denom string, amount uint64) {
	if b.balances[address] == nil {
		return
	}
	if b.balances[address][denom] < amount {
		b.balances[address][denom] = 0
		return
	}
	b.balances[address][denom] -= amount
}

func (b *hubMockBankKeeper) balance(address, denom string) uint64 {
	if b.balances[address] == nil {
		b.balances[address] = map[string]uint64{}
	}
	return b.balances[address][denom]
}
