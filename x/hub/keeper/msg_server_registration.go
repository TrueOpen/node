package keeper

import (
	"context"
	"strings"

	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (m msgServer) RegisterModelProfile(ctx context.Context, req *types.MsgRegisterModelProfile) (*types.MsgRegisterModelProfileResponse, error) {
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrInvalidModel, "nil request")
	}
	proposerBytes, proposer, err := m.k.requireCanonicalAddress("proposer_address", req.ProposerAddress)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Profile.ModelId) != req.Profile.ModelId || req.Profile.ModelId == "" {
		return nil, errorsmod.Wrap(types.ErrInvalidModel, "canonical model_id is required")
	}
	if err := types.ValidateModelID(req.Profile.ModelId); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidModel, err.Error())
	}
	params, err := m.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	minStake, err := modelRegistrationCoinAmount("min_stake", req.Profile.MinStake, params.Phase0.BusinessDenom)
	if err != nil {
		return nil, err
	}
	fee, err := modelRegistrationCoinAmount("registration_fee", req.Profile.RegistrationFee, params.Phase0.BusinessDenom)
	if err != nil {
		return nil, err
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	digest, _, err := types.ModelRegistrationDigest(sdkCtx.ChainID(), proposer, req.Profile)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidModel, err.Error())
	}
	// The min_stake-vs-service_bond_min_initial clamp deliberately lives inside
	// RegisterModelProfileState (past its receipt lookup), not here. Running it up
	// front makes the outcome of a replay depend on the *current* governance floor:
	// once service_bond_min_initial is raised above an already-accepted profile's
	// min_stake, resubmitting the identical Msg errors instead of returning the
	// receipt it was accepted with, so a rebroadcast becomes a hard failure and the
	// receipt stops being reachable. Idempotency has to be decided before any
	// current-parameter check; genuinely new registrations still hit the same clamp
	// one call deeper.
	cacheCtx, commit := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	_, profile, _, replay, err := m.k.RegisterModelProfileState(cache, proposer, req.Profile, digest, minStake, fee, sdkContextHeight(cacheCtx))
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidModel, err.Error())
	}
	if replay {
		return registrationResponse(profile, digest, true), nil
	}
	coins := sdk.NewCoins(sdk.NewCoin(params.Phase0.BusinessDenom, sdkmath.NewIntFromUint64(fee)))
	if err := m.k.bankKeeper.SendCoinsFromAccountToModule(cache, sdk.AccAddress(proposerBytes), types.TreasuryModuleName, coins); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidModel, err.Error())
	}
	epochLength, err := m.k.epochLengthBlocks(cache)
	if err != nil {
		return nil, err
	}
	if err := m.k.addTreasuryInflow(cache, epochForHeight(profile.CreatedHeight, epochLength), fee, profile.CreatedHeight); err != nil {
		return nil, err
	}
	mustEmitHubEvent(cache, &types.EventModelProfileRegistered{
		ModelId: profile.ModelId, ProfileVersion: profile.ProfileVersion,
		ManifestHash: append([]byte(nil), profile.ManifestHash...), Proposer: proposer,
		RegistrationFeeAmount: shared.NewAmount(fee),
	})
	commit()
	return registrationResponse(profile, digest, false), nil
}

func modelRegistrationCoinAmount(name string, coin sdk.Coin, businessDenom string) (uint64, error) {
	if !coin.IsValid() || coin.Denom != businessDenom || !coin.Amount.IsUint64() {
		return 0, errorsmod.Wrapf(types.ErrInvalidModel, "%s must be a valid business-denom Coin with a u64 amount", name)
	}
	amount := coin.Amount.Uint64()
	if amount == 0 {
		return 0, errorsmod.Wrapf(types.ErrInvalidModel, "%s amount must be positive", name)
	}
	return amount, nil
}

func registrationResponse(profile types.ProfileState, digest []byte, replay bool) *types.MsgRegisterModelProfileResponse {
	return &types.MsgRegisterModelProfileResponse{
		ModelId: profile.ModelId, ProfileVersion: profile.ProfileVersion,
		RegistrationDigest: append([]byte(nil), digest...),
		IdempotentReplay:   replay, RegisteredHeight: profile.CreatedHeight,
		RegistrationFeePaid: shared.NewAmount(profile.RegistrationFeePaid),
	}
}
