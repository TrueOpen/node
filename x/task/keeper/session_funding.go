package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) sessionOwner(ctx context.Context, sessionID []byte, signer string) (types.StreamState, []byte, error) {
	key, err := sessionStoreKey(sessionID)
	if err != nil {
		return types.StreamState{}, nil, err
	}
	stream, err := k.ReadStream(ctx, key)
	if err != nil {
		return types.StreamState{}, nil, err
	}
	owner, canonical, err := k.canonicalAddress("signer_address", signer)
	if err != nil || canonical != stream.OwnerUserAddress {
		return stream, owner, errorsmod.Wrap(sdkerrors.ErrUnauthorized, "signer must be the session owner")
	}
	if stream.Status != types.SessionStatus_SESSION_STATUS_ACTIVE && stream.Status != types.SessionStatus_SESSION_STATUS_IDLE {
		return types.StreamState{}, nil, errorsmod.Wrap(types.ErrInvalidSessionID, "session is closed")
	}
	return stream, owner, nil
}

func (k Keeper) sessionFundingOwner(ctx context.Context, sessionID []byte, signer string) (types.StreamState, []byte, error) {
	stream, owner, err := k.sessionOwner(ctx, sessionID, signer)
	if errors.Is(err, sdkerrors.ErrUnauthorized) && len(owner) != 0 && bytes.Equal(owner, stream.GetSessionKey()) {
		return stream, owner, types.ErrSessionKeyCannotFund
	}
	return stream, owner, err
}

func (k Keeper) sessionCoins(ctx context.Context, amount uint64) (sdk.Coins, error) {
	denom := k.hubKeeper.GetHubParams(sdk.UnwrapSDKContext(ctx)).BusinessDenom
	if denom == "" || amount == 0 {
		return nil, fmt.Errorf("session transfer requires a business denomination and positive amount")
	}
	return sdk.NewCoins(sdk.NewCoin(denom, sdkmath.NewIntFromUint64(amount))), nil
}

func (k Keeper) depositSessionFunds(ctx context.Context, owner []byte, stream *types.StreamState, amount shared.Amount) error {
	deposit, err := shared.ParseAmount(amount)
	if err != nil || deposit == 0 {
		return fmt.Errorf("session deposit must be a canonical positive amount")
	}
	balance, err := shared.ParseAmount(stream.Balance)
	if err != nil {
		return err
	}
	next, overflow := shared.CheckedAddUint64(balance, deposit)
	if overflow {
		return fmt.Errorf("session balance overflow")
	}
	coins, err := k.sessionCoins(ctx, deposit)
	if err != nil {
		return err
	}
	if err := k.bankKeeper.SendCoinsFromAccountToModule(ctx, sdk.AccAddress(owner), shared.TaskEscrowModuleName, coins); err != nil {
		return err
	}
	stream.Balance = shared.NewAmount(next)
	return nil
}

func (k Keeper) validateInitialSessionFunding(ctx context.Context, owner []byte, req *types.MsgCreateSession) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	amount, err := shared.ParseAmount(req.InitialDeposit)
	minimum, minErr := shared.ParseAmount(params.Session.SessionMinPrefund)
	if err != nil || minErr != nil || amount == 0 || minimum == 0 || amount < minimum {
		return fmt.Errorf("initial_deposit must meet session_min_prefund")
	}
	if _, _, err := k.canonicalAddress("initial_builder", req.InitialBuilder); err != nil {
		return err
	}
	_, err = k.hubKeeper.GetBuilderDispatchAuthority(ctx, req.InitialBuilder)
	if err != nil {
		return fmt.Errorf("initial Builder has no active service binding: %w", err)
	}
	set, err := k.hubKeeper.GetBuilderSetForHeight(sdk.UnwrapSDKContext(ctx), uint64(sdk.UnwrapSDKContext(ctx).BlockHeight()))
	if err != nil {
		return err
	}
	member := false
	for _, builder := range set.ActiveBuilders {
		member = member || builder == req.InitialBuilder
	}
	if !member {
		return fmt.Errorf("initial Builder is outside the current BuilderSet")
	}
	if params.Session.MaxActiveSessionsPerAccount == 0 {
		return fmt.Errorf("max_active_sessions_per_account must be positive")
	}
	iter, err := k.SessionByOwnerIndex.Iterate(ctx, collections.NewPrefixedPairRange[types.AddrKey, types.Hash32Key](owner))
	if err != nil {
		return err
	}
	defer iter.Close()
	var count uint32
	for ; iter.Valid(); iter.Next() {
		count++
		if count >= params.Session.MaxActiveSessionsPerAccount {
			return fmt.Errorf("max_active_sessions_per_account reached")
		}
	}
	return nil
}

func (m *msgServer) DepositSession(ctx context.Context, req *types.MsgDepositSession) (*types.MsgDepositSessionResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}
	cacheCtx, commit := sdk.UnwrapSDKContext(ctx).CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	stream, owner, err := m.k.sessionFundingOwner(cache, req.SessionId, req.SignerAddress)
	if err != nil {
		return nil, err
	}
	if stream.PendingWithdrawKind == types.SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_CLOSE {
		return nil, types.ErrSessionWithdrawPending
	}
	previous := stream
	if err := m.k.depositSessionFunds(cache, owner, &stream, req.Amount); err != nil {
		return nil, err
	}
	height, err := currentBlockHeight(cache)
	if err != nil {
		return nil, err
	}
	stream.LastActiveHeight = height
	stream.Status = types.SessionStatus_SESSION_STATUS_ACTIVE
	if err := m.k.replaceStreamState(cache, previous, stream); err != nil {
		return nil, err
	}
	if err := emitTypedEvent(cache, &types.EventSessionDeposited{SessionId: bytes.Clone(req.SessionId), Owner: stream.OwnerUserAddress, Amount: req.Amount, Balance: stream.Balance}); err != nil {
		return nil, err
	}
	commit()
	return &types.MsgDepositSessionResponse{Balance: stream.Balance, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED}, nil
}

func (m *msgServer) WithdrawSession(ctx context.Context, req *types.MsgWithdrawSession) (*types.MsgWithdrawSessionResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}
	cacheCtx, commit := sdk.UnwrapSDKContext(ctx).CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	stream, _, err := m.k.sessionFundingOwner(cache, req.SessionId, req.SignerAddress)
	if err != nil {
		return nil, err
	}
	params, err := m.k.Params.Get(cache)
	if err != nil {
		return nil, err
	}
	height, err := currentBlockHeight(cache)
	if err != nil {
		return nil, err
	}
	var kind types.SessionWithdrawKindV1
	amount := shared.NewAmount(0)
	switch action := req.Action.(type) {
	case *types.MsgWithdrawSession_Amount:
		if action == nil || action.Amount == nil {
			return nil, fmt.Errorf("withdrawal amount is required")
		}
		kind, amount = types.SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_AMOUNT, action.Amount.Amount
	case *types.MsgWithdrawSession_Close:
		if action == nil || action.Close == nil {
			return nil, fmt.Errorf("CLOSE action is required")
		}
		kind = types.SessionWithdrawKindV1_SESSION_WITHDRAW_KIND_V1_CLOSE
	default:
		return nil, fmt.Errorf("exactly one withdrawal action is required")
	}
	previous := stream
	if err := m.k.requestSessionWithdrawal(cache, &stream, kind, amount, height, params.Session.SessionWithdrawDelayBlocks); err != nil {
		return nil, err
	}
	if err := m.k.replaceStreamState(cache, previous, stream); err != nil {
		return nil, err
	}
	commit()
	return &types.MsgWithdrawSessionResponse{EffectiveHeight: stream.PendingWithdrawEffectiveHeight, PendingAmount: stream.PendingWithdrawAmount, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED}, nil
}
