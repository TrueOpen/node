package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

type ClaimEarningsResult struct {
	Address      string
	ClaimedAt    uint64
	Amount       uint64
	State        types.EarningsState
	MaturedItems uint64
	Mutated      bool
}

func (k Keeper) CreditTaskSettlementEarnings(ctx context.Context, sessionID, taskID []byte, credits []types.TaskEarningsCredit, height uint64) error {
	if len(sessionID) != shared.Hash32KeySize || len(taskID) != shared.Hash32KeySize || height == 0 {
		return fmt.Errorf("task earnings credit requires Hash32 session/task IDs and a positive height")
	}
	canonical := make([]types.TaskEarningsCredit, len(credits))
	var previousAddress []byte
	for i, credit := range credits {
		addressBytes, address, err := k.requireCanonicalAddress("earnings beneficiary", credit.Beneficiary)
		if err != nil {
			return err
		}
		value, err := shared.ParseAmount(credit.Amount)
		if err != nil || value == 0 {
			return fmt.Errorf("task earnings credit %d must have a positive canonical amount", i)
		}
		if i != 0 && bytes.Compare(previousAddress, addressBytes) >= 0 {
			return fmt.Errorf("task earnings credits must be unique and strictly sorted by beneficiary address bytes")
		}
		previousAddress = append(previousAddress[:0], addressBytes...)
		canonical[i] = types.TaskEarningsCredit{Beneficiary: address, Amount: shared.NewAmount(value)}
	}
	for _, credit := range canonical {
		if err := k.creditTaskEarnings(ctx, sessionID, taskID, credit, height); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) creditTaskEarnings(ctx context.Context, sessionID, taskID []byte, credit types.TaskEarningsCredit, height uint64) error {
	value, _ := shared.ParseAmount(credit.Amount)
	address := credit.Beneficiary
	state, err := k.getOrInitEarnings(ctx, address)
	if err != nil {
		return err
	}
	taskFee, err := shared.ParseAmount(state.ClaimableTaskFee)
	if err != nil {
		return err
	}
	claimable, err := shared.ParseAmount(state.ClaimableAmount)
	if err != nil {
		return err
	}
	taskFee, err = checkedAdd(taskFee, value)
	if err != nil {
		return err
	}
	claimable, err = checkedAdd(claimable, value)
	if err != nil {
		return err
	}
	state.ClaimableTaskFee = shared.NewAmount(taskFee)
	state.ClaimableAmount = shared.NewAmount(claimable)
	state.EarningsVersion, err = checkedAdd(state.EarningsVersion, 1)
	if err != nil {
		return fmt.Errorf("earnings_version overflow for %s", address)
	}
	state.LastUpdatedHeight = height
	if err := state.Validate(); err != nil {
		return err
	}
	if err := k.setEarnings(ctx, state); err != nil {
		return err
	}
	mustEmitHubEvent(ctx, &types.EventEarningsAccrued{
		Beneficiary: address, Amount: shared.NewAmount(value), RewardClass: types.RewardClass_REWARD_CLASS_TASK_FEE,
		XSessionIdOrEmpty: &types.EventEarningsAccrued_SessionIdOrEmpty{SessionIdOrEmpty: append([]byte(nil), sessionID...)},
		XTaskIdOrEmpty:    &types.EventEarningsAccrued_TaskIdOrEmpty{TaskIdOrEmpty: append([]byte(nil), taskID...)},
		EarningsVersion:   state.EarningsVersion,
	})
	return nil
}

func (k Keeper) CreditTaskMaintenance(ctx context.Context, settlementID []byte, amount shared.Amount, height uint64) error {
	value, err := shared.ParseAmount(amount)
	if len(settlementID) != shared.Hash32KeySize || err != nil || value == 0 || height == 0 {
		return fmt.Errorf("task maintenance credit requires a positive canonical amount and height")
	}
	treasury, err := k.Treasury.Get(ctx)
	if err != nil {
		return err
	}
	balance, err := shared.ParseAmount(treasury.Balance)
	if err != nil {
		return err
	}
	balance, err = checkedAdd(balance, value)
	if err != nil {
		return err
	}
	treasury.TreasuryVersion, err = checkedAdd(treasury.TreasuryVersion, 1)
	if err != nil {
		return fmt.Errorf("treasury_version overflow")
	}
	treasury.Balance = shared.NewAmount(balance)
	if err := k.Treasury.Set(ctx, treasury); err != nil {
		return err
	}
	mustEmitHubEvent(ctx, &types.EventTreasuryCollected{
		Amount: shared.NewAmount(value), SourceKind: types.TreasurySourceKind_TREASURY_SOURCE_KIND_TASK_FEE,
		SourceId: append([]byte(nil), settlementID...), TreasuryVersion: treasury.TreasuryVersion,
	})
	return nil
}

func (k Keeper) ClaimEarnings(ctx context.Context, address string, claimClass types.ClaimClassV1) (ClaimEarningsResult, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	result, err := k.claimEarnings(sdk.WrapSDKContext(cacheCtx), address, claimClass)
	if err != nil {
		return ClaimEarningsResult{}, err
	}
	commit()
	return result, nil
}

func (k Keeper) claimEarnings(ctx context.Context, address string, claimClass types.ClaimClassV1) (ClaimEarningsResult, error) {
	addressBytes, canonical, err := k.requireCanonicalAddress("earnings address", address)
	if err != nil {
		return ClaimEarningsResult{}, err
	}
	if claimClass != types.ClaimClassV1_CLAIM_CLASS_V1_ALL && claimClass != types.ClaimClassV1_CLAIM_CLASS_V1_TASK_FEE {
		// §16.1: a Phase 0 disabled path is a typed FailedPrecondition, not an
		// untyped internal error, so a client can tell "not yet" from "broken".
		return ClaimEarningsResult{}, status.Error(codes.FailedPrecondition, "FEATURE_DISABLED")
	}
	state, err := k.getEarnings(ctx, canonical)
	if errors.Is(err, collections.ErrNotFound) {
		return ClaimEarningsResult{Address: canonical, State: emptyEarnings(canonical)}, nil
	}
	if err != nil {
		return ClaimEarningsResult{}, err
	}
	if err := state.Validate(); err != nil {
		return ClaimEarningsResult{}, err
	}
	amount, err := shared.ParseAmount(state.ClaimableTaskFee)
	if err != nil {
		return ClaimEarningsResult{}, err
	}
	if amount == 0 {
		return ClaimEarningsResult{Address: canonical, State: state}, nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return ClaimEarningsResult{}, err
	}
	minimum, err := shared.ParseAmount(params.Reward.MinClaimAmount)
	if err != nil {
		return ClaimEarningsResult{}, err
	}
	if amount < minimum {
		// §14 table: `0 < claimable < min_claim_amount -> FailedPrecondition, zero
		// writes`.
		// The dust threshold is a state precondition, not a malformed request: the
		// same message succeeds unchanged once the ledger crosses the floor, and
		// the enclosing cache context is what supplies the zero-write half.
		return ClaimEarningsResult{}, status.Errorf(codes.FailedPrecondition,
			"claimable earnings %d is below the %d minimum claim amount", amount, minimum)
	}
	if err := k.requireModuleBalance(ctx, types.RewardsModuleName, amount); err != nil {
		return ClaimEarningsResult{}, err
	}
	if err := k.sendModuleToAccount(ctx, types.RewardsModuleName, sdk.AccAddress(addressBytes), amount); err != nil {
		return ClaimEarningsResult{}, err
	}
	nextVersion, err := checkedAdd(state.EarningsVersion, 1)
	if err != nil {
		return ClaimEarningsResult{}, fmt.Errorf("earnings_version overflow for %s", canonical)
	}
	height := sdkWrappedContextHeight(ctx)
	// §10.0b1 claims one class: zero claimable_task_fee and recompute
	// claimable_amount. Removing the whole row would silently discard the
	// service and builder sub-ledgers, and EnsureRewardsEarningsInvariant would
	// then find trueopen_rewards holding coins no claimable row accounts for.
	state.ClaimableTaskFee = shared.NewAmount(0)
	remaining, err := shared.ParseAmount(state.ClaimableServiceReward)
	if err != nil {
		return ClaimEarningsResult{}, err
	}
	builderRemaining, err := shared.ParseAmount(state.ClaimableBuilderReward)
	if err != nil {
		return ClaimEarningsResult{}, err
	}
	total, overflow := checkedBridgeAdd(remaining, builderRemaining)
	if overflow {
		return ClaimEarningsResult{}, fmt.Errorf("claimable_amount overflow for %s", canonical)
	}
	state.ClaimableAmount = shared.NewAmount(total)
	state.EarningsVersion = nextVersion
	state.LastUpdatedHeight = height
	if err := k.storeOrRemoveEmptyEarnings(ctx, state); err != nil {
		return ClaimEarningsResult{}, err
	}
	mustEmitHubEvent(ctx, &types.EventEarningsClaimed{
		Beneficiary: canonical, Amount: shared.NewAmount(amount), RewardClass: types.RewardClass_REWARD_CLASS_TASK_FEE,
		EarningsVersion: nextVersion,
	})
	// The response reports what is actually left, not an assumed empty ledger:
	// claiming the task-fee class does not touch the service or builder classes.
	return ClaimEarningsResult{
		Address: canonical, ClaimedAt: height, Amount: amount, State: state, Mutated: true,
	}, nil
}

func (k Keeper) getOrInitEarnings(ctx context.Context, address string) (types.EarningsState, error) {
	state, err := k.getEarnings(ctx, address)
	if errors.Is(err, collections.ErrNotFound) {
		return emptyEarnings(address), nil
	}
	return state, err
}

func emptyEarnings(address string) types.EarningsState {
	return types.EarningsState{
		Address: address, ClaimableTaskFee: shared.NewAmount(0), ClaimableServiceReward: shared.NewAmount(0),
		ClaimableBuilderReward: shared.NewAmount(0), ClaimableAmount: shared.NewAmount(0),
	}
}

func (k Keeper) storeOrRemoveEmptyEarnings(ctx context.Context, state types.EarningsState) error {
	taskFee, err := shared.ParseAmount(state.ClaimableTaskFee)
	if err != nil {
		return err
	}
	serviceReward, err := shared.ParseAmount(state.ClaimableServiceReward)
	if err != nil {
		return err
	}
	builderReward, err := shared.ParseAmount(state.ClaimableBuilderReward)
	if err != nil {
		return err
	}
	if taskFee == 0 && serviceReward == 0 && builderReward == 0 {
		return k.removeEarnings(ctx, state.Address)
	}
	if err := state.Validate(); err != nil {
		return err
	}
	return k.setEarnings(ctx, state)
}

func (k Keeper) requireModuleBalance(ctx context.Context, moduleName string, amount uint64) error {
	if amount == 0 {
		return nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	balance := k.bankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(moduleName), params.Phase0.BusinessDenom)
	if !balance.Amount.IsUint64() || balance.Amount.Uint64() < amount {
		return fmt.Errorf("module account %s balance below required amount %d", moduleName, amount)
	}
	return nil
}

func (k Keeper) sendModuleToModule(ctx context.Context, fromModule, toModule string, amount uint64) error {
	if amount == 0 {
		return nil
	}
	coins, err := k.hubCoins(ctx, amount)
	if err != nil {
		return err
	}
	return k.bankKeeper.SendCoinsFromModuleToModule(ctx, fromModule, toModule, coins)
}

func (k Keeper) sendModuleToAccount(ctx context.Context, fromModule string, to sdk.AccAddress, amount uint64) error {
	coins, err := k.hubCoins(ctx, amount)
	if err != nil {
		return err
	}
	return k.bankKeeper.SendCoinsFromModuleToAccount(ctx, fromModule, to, coins)
}

func (k Keeper) hubCoins(ctx context.Context, amount uint64) (sdk.Coins, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if amount == 0 {
		return nil, fmt.Errorf("coin amount must be greater than zero")
	}
	return sdk.NewCoins(sdk.NewCoin(params.Phase0.BusinessDenom, sdkmath.NewIntFromUint64(amount))), nil
}
