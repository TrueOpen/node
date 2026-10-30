package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	internaltypes "github.com/TrueOpen/node/x/hub/internal/types"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// GetBuilderCompensationAccount returns a zero account for a registered Builder
// that has never been funded. Reading it never creates a ledger row.
func (k Keeper) GetBuilderCompensationAccount(ctx context.Context, builder string) (types.BuilderCompensationAccountState, error) {
	raw, builder, err := k.requireCanonicalAddress("Builder", builder)
	if err != nil {
		return types.BuilderCompensationAccountState{}, err
	}
	if _, err := k.GetBuilderState(ctx, builder); err != nil {
		return types.BuilderCompensationAccountState{}, err
	}
	stored, err := k.BuilderCompensationAccount.Get(ctx, builder)
	if errors.Is(err, collections.ErrNotFound) {
		return types.BuilderCompensationAccountState{BuilderAddress: builder, Balance: shared.NewAmount(0), TotalFunded: shared.NewAmount(0), TotalPaid: shared.NewAmount(0), TotalPenalized: shared.NewAmount(0)}, nil
	}
	if err != nil {
		return types.BuilderCompensationAccountState{}, err
	}
	if !bytes.Equal(stored.BuilderAddress, raw) {
		return types.BuilderCompensationAccountState{}, fmt.Errorf("compensation account key/address mismatch")
	}
	state := types.BuilderCompensationAccountState{BuilderAddress: builder, Balance: shared.NewAmount(stored.Balance), TotalFunded: shared.NewAmount(stored.TotalFunded), TotalPaid: shared.NewAmount(stored.TotalPaid), TotalPenalized: shared.NewAmount(stored.TotalPenalized)}
	if err := state.Validate(); err != nil {
		return types.BuilderCompensationAccountState{}, err
	}
	return state, nil
}

// WriteBuilderCompensationAccount is the single compact storage projection.
func (k Keeper) WriteBuilderCompensationAccount(ctx context.Context, state types.BuilderCompensationAccountState) error {
	if err := state.Validate(); err != nil {
		return err
	}
	address, err := k.accountAddressToStore("compensation Builder", state.BuilderAddress, false)
	if err != nil {
		return err
	}
	stored := internaltypes.BuilderCompensationAccountStoreState{BuilderAddress: address}
	for _, amount := range []struct {
		value  shared.Amount
		target *uint64
	}{{state.Balance, &stored.Balance}, {state.TotalFunded, &stored.TotalFunded}, {state.TotalPaid, &stored.TotalPaid}, {state.TotalPenalized, &stored.TotalPenalized}} {
		value, err := shared.ParseAmount(amount.value)
		if err != nil {
			return err
		}
		*amount.target = value
	}
	return k.BuilderCompensationAccount.Set(ctx, state.BuilderAddress, stored)
}

func (m msgServer) FundBuilderCompensation(ctx context.Context, req *types.MsgFundBuilderCompensation) (*types.MsgFundBuilderCompensationResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}
	signer, _, err := m.k.requireCanonicalAddress("signer_address", req.SignerAddress)
	if err != nil {
		return nil, err
	}
	amount, err := shared.ParseAmount(req.Amount)
	if err != nil || amount == 0 {
		return nil, fmt.Errorf("compensation funding amount must be canonical and positive")
	}
	cacheCtx, commit := sdk.UnwrapSDKContext(ctx).CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	state, err := m.k.GetBuilderCompensationAccount(cache, req.BuilderAddress)
	if err != nil {
		return nil, err
	}
	balance, err := shared.ParseAmount(state.Balance)
	if err != nil {
		return nil, err
	}
	funded, err := shared.ParseAmount(state.TotalFunded)
	if err != nil {
		return nil, err
	}
	balance, err = checkedAdd(balance, amount)
	if err != nil {
		return nil, err
	}
	funded, err = checkedAdd(funded, amount)
	if err != nil {
		return nil, err
	}
	coins, err := m.k.hubCoins(cache, amount)
	if err != nil {
		return nil, err
	}
	if err := m.k.bankKeeper.SendCoinsFromAccountToModule(cache, sdk.AccAddress(signer), types.BuilderCompensationModuleName, coins); err != nil {
		return nil, err
	}
	state.Balance, state.TotalFunded = shared.NewAmount(balance), shared.NewAmount(funded)
	if err := m.k.WriteBuilderCompensationAccount(cache, state); err != nil {
		return nil, err
	}
	mustEmitHubEvent(cache, &types.EventBuilderCompensationFunded{Builder: state.BuilderAddress, Amount: req.Amount, Balance: state.Balance})
	commit()
	return &types.MsgFundBuilderCompensationResponse{Balance: state.Balance, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED}, nil
}

func (q queryServer) BuilderCompensationAccount(ctx context.Context, req *types.QueryBuilderCompensationAccountRequest) (*types.QueryBuilderCompensationAccountResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if _, _, err := q.k.requireCanonicalAddress("Builder", req.BuilderAddress); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	state, err := q.k.GetBuilderCompensationAccount(ctx, req.BuilderAddress)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.NotFound, "Builder is not registered")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "compensation account unavailable")
	}
	return &types.QueryBuilderCompensationAccountResponse{Account: &state}, nil
}

// EnsureBuilderCompensationInvariant compares custody to the only ledger.
func (k Keeper) EnsureBuilderCompensationInvariant(ctx context.Context) error {
	iter, err := k.BuilderCompensationAccount.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer iter.Close()
	var total uint64
	for ; iter.Valid(); iter.Next() {
		builder, err := iter.Key()
		if err != nil {
			return err
		}
		state, err := k.GetBuilderCompensationAccount(ctx, builder)
		if err != nil {
			return err
		}
		balance, err := shared.ParseAmount(state.Balance)
		if err != nil {
			return err
		}
		total, err = checkedAdd(total, balance)
		if err != nil {
			return err
		}
	}
	return k.ensureInvariantModuleBalance(ctx, types.BuilderCompensationModuleName, total, "Builder compensation")
}

func (k Keeper) exportBuilderCompensationAccounts(ctx context.Context) ([]types.BuilderCompensationAccountState, error) {
	iter, err := k.BuilderCompensationAccount.Iterate(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	rows := []types.BuilderCompensationAccountState{}
	for ; iter.Valid(); iter.Next() {
		builder, err := iter.Key()
		if err != nil {
			return nil, err
		}
		state, err := k.GetBuilderCompensationAccount(ctx, builder)
		if err != nil {
			return nil, err
		}
		rows = append(rows, state)
	}
	return rows, nil
}
