package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (q queryServer) Earnings(ctx context.Context, req *types.QueryEarningsRequest) (*types.QueryEarningsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	_, address, err := q.k.requireCanonicalAddress("address", req.Address)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	earnings, err := q.k.getEarnings(ctx, address)
	if errors.Is(err, collections.ErrNotFound) {
		return &types.QueryEarningsResponse{Earnings: emptyEarnings(address)}, nil
	}
	if err != nil || earnings.Validate() != nil {
		return nil, status.Error(codes.Internal, "earnings state is invalid")
	}
	return &types.QueryEarningsResponse{Earnings: earnings}, nil
}

func (q queryServer) Treasury(ctx context.Context, req *types.QueryTreasuryRequest) (*types.QueryTreasuryResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "invalid request")
	}
	treasury, err := q.k.Treasury.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "treasury state unavailable")
	}
	logical, err := shared.ParseAmount(treasury.Balance)
	if err != nil {
		return nil, status.Error(codes.Internal, "treasury balance is invalid")
	}
	params, err := q.k.Params.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "params unavailable")
	}
	bankBalance := q.k.bankKeeper.GetBalance(ctx, authtypes.NewModuleAddress(types.TreasuryModuleName), params.Phase0.BusinessDenom)
	if bankBalance.Denom != params.Phase0.BusinessDenom || !bankBalance.Amount.IsUint64() || bankBalance.Amount.Uint64() != logical {
		return nil, status.Error(codes.Internal, "treasury state does not match module account balance")
	}
	response := &types.QueryTreasuryResponse{Treasury: treasury}
	if response.Size() > int(params.QueryEvent.MaxQueryResponseBytes) {
		return nil, status.Error(codes.Internal, "treasury response exceeds the configured byte cap")
	}
	return response, nil
}
