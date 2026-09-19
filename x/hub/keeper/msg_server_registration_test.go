package keeper_test

import (
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	proto "github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestRegisterModelProfileIsAtomicSignedAndReplaySafe(t *testing.T) {
	f := initFixture(t)
	const (
		businessDenom = "uusdc"
		modelFee      = uint64(2_000_001)
		updateFee     = uint64(1_000_001)
	)
	genesis := types.DefaultGenesis()
	genesis.Params.Phase0.BusinessDenom = businessDenom
	genesis.Params.Treasury.ModelProfileRegistrationFee = types.AmountFromUint64(modelFee)
	genesis.Params.Treasury.ProfileVersionUpdateFee = types.AmountFromUint64(updateFee)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	registrant := hubIdentity(t, 170)
	registerHubTestAccount(t, f, registrant)
	f.bank.add(registrant.Address, businessDenom, modelFee+updateFee)
	treasuryAddress := authtypes.NewModuleAddress(types.TreasuryModuleName).String()
	f.ctx = sdk.WrapSDKContext(
		sdk.UnwrapSDKContext(f.ctx).WithChainID("registration-chain").WithBlockHeight(25),
	)
	server := keeper.NewMsgServerImpl(f.keeper)

	profile1 := testModelProfileProjection("model-registration", 1, testServiceBondMinInitial)
	profile1.MinStake.Denom = businessDenom
	profile1.RegistrationFee = sdk.NewCoin(businessDenom, sdkmath.NewIntFromUint64(modelFee))
	request1 := signedModelProfileRequest(t, f, registrant, profile1)
	response1, err := server.RegisterModelProfile(f.ctx, request1)
	require.NoError(t, err)
	require.False(t, response1.IdempotentReplay)
	require.Equal(t, uint32(1), response1.ProfileVersion)
	require.Equal(t, updateFee, f.bank.balance(registrant.Address, businessDenom))
	require.Equal(t, modelFee, f.bank.balance(treasuryAddress, businessDenom))
	require.True(t, hasEventType(sdk.UnwrapSDKContext(f.ctx).EventManager().Events(), proto.MessageName(&types.EventModelProfileRegistered{})))

	replay, err := server.RegisterModelProfile(f.ctx, request1)
	require.NoError(t, err)
	require.True(t, replay.IdempotentReplay)
	require.Equal(t, updateFee, f.bank.balance(registrant.Address, businessDenom))
	require.Equal(t, modelFee, f.bank.balance(treasuryAddress, businessDenom))

	profileKey := types.NewProfileStateKey(profile1.ModelId, profile1.ProfileVersion)
	storedProfile, err := f.keeper.Profile.Get(f.ctx, profileKey)
	require.NoError(t, err)
	storedProfile.MinStake++
	require.NoError(t, f.keeper.Profile.Set(f.ctx, profileKey, storedProfile))
	_, err = server.RegisterModelProfile(f.ctx, request1)
	require.ErrorContains(t, err, "registration receipt projection mismatch")
	storedProfile.MinStake--
	require.NoError(t, f.keeper.Profile.Set(f.ctx, profileKey, storedProfile))

	// §1.3 rule 2: MsgRegisterModelProfile no longer carries a same-account
	// detached registrant_signature; the Cosmos Tx signer is the only
	// authorization. A request whose proposer has no funded account is rejected
	// before any state or fee movement.
	unfunded := hubIdentity(t, 171)
	registerHubTestAccount(t, f, unfunded)
	invalid := &types.MsgRegisterModelProfile{
		ProposerAddress: unfunded.Address,
		Profile:         testModelProfileProjection("model-registration", 2, testServiceBondMinInitial),
	}
	invalid.Profile.MinStake.Denom = businessDenom
	invalid.Profile.RegistrationFee = sdk.NewCoin(businessDenom, sdkmath.NewIntFromUint64(updateFee))
	_, err = server.RegisterModelProfile(f.ctx, invalid)
	require.Error(t, err)
	require.Equal(t, updateFee, f.bank.balance(registrant.Address, businessDenom))

	profile2 := testModelProfileProjection("model-registration", 2, testServiceBondMinInitial)
	profile2.MinStake.Denom = businessDenom
	profile2.RegistrationFee = sdk.NewCoin(businessDenom, sdkmath.NewIntFromUint64(updateFee))
	response2, err := server.RegisterModelProfile(f.ctx, signedModelProfileRequest(t, f, registrant, profile2))
	require.NoError(t, err)
	require.False(t, response2.IdempotentReplay)
	require.Equal(t, uint32(2), response2.ProfileVersion)
	require.Zero(t, f.bank.balance(registrant.Address, businessDenom))
	require.Equal(t, modelFee+updateFee, f.bank.balance(treasuryAddress, businessDenom))

	model, err := f.keeper.GetModel(f.ctx, "model-registration")
	require.NoError(t, err)
	require.Equal(t, uint32(2), model.LatestProfileVersion)
	require.Equal(t, modelFee+updateFee, model.RegistrationFeePaid)
	profile, err := f.keeper.GetProfile(f.ctx, "model-registration", 2)
	require.NoError(t, err)
	require.Equal(t, types.ModelStatusRegistered, profile.Status)
	require.Equal(t, updateFee, profile.RegistrationFeePaid)
	require.Equal(t, registrant.Address, profile.ProposerAddress)

}

// TestRegisterModelProfileReplayStaysIdempotentAfterBondFloorRaise pins the order
// of the idempotency check and the governance min_stake clamp. A profile accepted
// under the old service_bond_min_initial keeps its min_stake forever, so once
// governance raises the floor above it the clamp can never pass again. Running that
// clamp in front of the receipt lookup therefore turned every rebroadcast of an
// already-accepted registration into a hard error: the fee had been taken and the
// profile written, but the confirming receipt became unreachable. Idempotency has to
// be decided before any current-parameter check.
func TestRegisterModelProfileReplayStaysIdempotentAfterBondFloorRaise(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
	registrant := hubIdentity(t, 172)
	registerHubTestAccount(t, f, registrant)
	f.bank.seedAccount(registrant.Address, types.ModelRegistrationFeeMinMicroUSDC)
	f.ctx = sdk.WrapSDKContext(
		sdk.UnwrapSDKContext(f.ctx).WithChainID("registration-chain").WithBlockHeight(31),
	)
	server := keeper.NewMsgServerImpl(f.keeper)

	profile := testModelProfileProjection("model-bond-floor-replay", 1, testServiceBondMinInitial)
	request := signedModelProfileRequest(t, f, registrant, profile)
	first, err := server.RegisterModelProfile(f.ctx, request)
	require.NoError(t, err)
	require.False(t, first.IdempotentReplay)
	require.Equal(t, types.ModelRegistrationFeeMinMicroUSDC, f.bank.moduleBalance(types.TreasuryModuleName))

	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	params.Service.ServiceBondMinInitial = types.AmountFromUint64(testServiceBondMinInitial + 1)
	params.Treasury.ModelProfileRegistrationFee = types.AmountFromUint64(types.ModelRegistrationFeeMinMicroUSDC + 1)
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))

	replay, err := server.RegisterModelProfile(f.ctx, request)
	require.NoError(t, err)
	require.True(t, replay.IdempotentReplay)
	require.Equal(t, first.ModelId, replay.ModelId)
	require.Equal(t, first.ProfileVersion, replay.ProfileVersion)
	require.Equal(t, first.RegistrationDigest, replay.RegistrationDigest)
	require.Equal(t, first.RegisteredHeight, replay.RegisteredHeight)
	require.Equal(t, first.RegistrationFeePaid, replay.RegistrationFeePaid)
	// The replay must stay free: it is a receipt lookup, not a second registration.
	require.Equal(t, types.ModelRegistrationFeeMinMicroUSDC, f.bank.moduleBalance(types.TreasuryModuleName))
	require.Zero(t, f.bank.accountBalance(registrant.Address))

	// The clamp is unchanged for a genuinely new registration: it simply runs one
	// call deeper, past the receipt lookup.
	fresh := testModelProfileProjection("model-bond-floor-fresh", 1, testServiceBondMinInitial)
	fresh.RegistrationFee = sdk.NewCoin(types.DefaultBusinessDenom, sdkmath.NewIntFromUint64(types.ModelRegistrationFeeMinMicroUSDC+1))
	_, err = server.RegisterModelProfile(f.ctx, signedModelProfileRequest(t, f, registrant, fresh))
	require.ErrorContains(t, err, "below service bond floor")
	hasFresh, err := f.keeper.Model.Has(f.ctx, "model-bond-floor-fresh")
	require.NoError(t, err)
	require.False(t, hasFresh)
}

func registerHubTestAccount(t *testing.T, f *fixture, identity hubTestIdentity) {
	t.Helper()
	account := authtypes.NewBaseAccountWithAddress(sdk.MustAccAddressFromBech32(identity.Address))
	require.NoError(t, account.SetPubKey(identity.PrivKey.PubKey()))
	f.auth.accounts[identity.Address] = account
}

func signedModelProfileRequest(t *testing.T, f *fixture, identity hubTestIdentity, profile shared.ModelProfileProjection) *types.MsgRegisterModelProfile {
	t.Helper()
	return &types.MsgRegisterModelProfile{
		ProposerAddress: identity.Address,
		Profile:         profile,
	}
}

func hasEventType(events sdk.Events, eventType string) bool {
	for _, event := range events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}
