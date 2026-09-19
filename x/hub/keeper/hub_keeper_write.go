package keeper

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

var _ types.HubKeeper = Keeper{}

func (k Keeper) addTreasuryInflow(ctx context.Context, _ uint64, amount uint64, _ uint64) error {
	if amount == 0 {
		return nil
	}
	treasury, err := k.getOrInitTreasuryState(ctx)
	if err != nil {
		return err
	}
	balance, err := shared.ParseAmount(treasury.Balance)
	if err != nil {
		return fmt.Errorf("treasury balance: %w", err)
	}
	balance, err = checkedAdd(balance, amount)
	if err != nil {
		return err
	}
	treasury.Balance = shared.NewAmount(balance)
	treasury.TreasuryVersion, err = checkedAdd(treasury.TreasuryVersion, 1)
	if err != nil {
		return fmt.Errorf("treasury version overflow: %w", err)
	}
	return k.Treasury.Set(ctx, treasury)
}

// CreditGovernanceDepositResidual mirrors the bank move performed by the app
// governance guard into TreasuryState. It is not a public Msg entry.
func (k Keeper) CreditGovernanceDepositResidual(ctx context.Context, amount uint64) error {
	if amount == 0 {
		return fmt.Errorf("governance deposit residual must be positive")
	}
	height := sdkContextHeight(sdk.UnwrapSDKContext(ctx))
	if height == 0 {
		return fmt.Errorf("governance deposit residual requires a positive height")
	}
	return k.addTreasuryInflow(ctx, 0, amount, height)
}

func (k Keeper) getOrInitTreasuryState(ctx context.Context) (types.TreasuryState, error) {
	state, err := k.Treasury.Get(ctx)
	if err == nil {
		return state, nil
	}
	if !errors.Is(err, collections.ErrNotFound) {
		return types.TreasuryState{}, err
	}
	state = types.TreasuryState{Balance: shared.NewAmount(0)}
	if err := k.Treasury.Set(ctx, state); err != nil {
		return types.TreasuryState{}, err
	}
	return state, nil
}

func checkedAdd(a, b uint64) (uint64, error) {
	value, overflow := shared.CheckedAddUint64(a, b)
	if overflow {
		return 0, fmt.Errorf("uint64 overflow adding %d and %d", a, b)
	}
	return value, nil
}

func saturatingAdd(a, b uint64) uint64 {
	return shared.SaturatingAddUint64(a, b)
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func sdkContextHeight(ctx sdk.Context) uint64 {
	height := ctx.BlockHeight()
	if height < 0 {
		return 0
	}
	return uint64(height)
}

func hexRef(value []byte) string {
	return hex.EncodeToString(value)
}
