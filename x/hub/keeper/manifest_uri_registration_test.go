package keeper_test

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
)

type manifestURICases struct {
	MaxBytes uint32 `json:"max_manifest_uri_bytes"`
	Accepted []struct {
		URI string `json:"uri"`
	} `json:"accepted"`
	Rejected []struct {
		URI    string `json:"uri"`
		Reason string `json:"reason"`
	} `json:"rejected"`
}

// TestRegisterModelProfileManifestURI drives every accepted and rejected form of
// wire testdata/v1/hub/manifest_uri_v1.json through MsgRegisterModelProfile: an
// accepted URI is stored byte for byte and returned by Query/Profile, a
// rejected one fails the registration before any fee moves.
func TestRegisterModelProfileManifestURI(t *testing.T) {
	raw, err := os.ReadFile("../types/testdata/manifest_uri_v1.json")
	require.NoError(t, err)
	var cases manifestURICases
	require.NoError(t, json.Unmarshal(raw, &cases))

	f := initFixture(t)
	const businessDenom = "uusdc"
	const modelFee = uint64(2_000_001)
	genesis := types.DefaultGenesis()
	genesis.Params.Phase0.BusinessDenom = businessDenom
	genesis.Params.Treasury.ModelProfileRegistrationFee = types.AmountFromUint64(modelFee)
	require.Equal(t, cases.MaxBytes, genesis.Params.Model.MaxManifestUriBytes)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	const chainID = "manifest-uri-chain"
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithChainID(chainID).WithBlockHeight(25))
	registrant := hubIdentity(t, 172)
	registerHubTestAccount(t, f, registrant)
	server := keeper.NewMsgServerImpl(f.keeper)
	query := keeper.NewQueryServerImpl(f.keeper)

	register := func(label, uri string) (*types.MsgRegisterModelProfileResponse, error) {
		profile := testModelProfileProjection(chainID, registrant.Address, label, 1, testServiceBondMinInitial)
		profile.MinStake.Denom = businessDenom
		profile.RegistrationFee = sdk.NewCoin(businessDenom, sdkmath.NewIntFromUint64(modelFee))
		profile.ManifestUri = uri
		return server.RegisterModelProfile(f.ctx, signedModelProfileRequest(t, f, registrant, profile))
	}

	f.bank.add(registrant.Address, businessDenom, modelFee)
	for index, c := range cases.Rejected {
		_, err := register(fmt.Sprintf("model-rejected-%d", index), c.URI)
		require.ErrorContains(t, err, "manifest_uri is invalid", "rejected %q (%s)", c.URI, c.Reason)
		require.Equal(t, modelFee, f.bank.balance(registrant.Address, businessDenom), "no fee moves for %q", c.URI)
	}

	for index, c := range cases.Accepted {
		label := fmt.Sprintf("model-accepted-%d", index)
		if index > 0 {
			f.bank.add(registrant.Address, businessDenom, modelFee)
		}
		response, err := register(label, c.URI)
		require.NoError(t, err, "accepted %q", c.URI)

		modelID := testModelID(chainID, registrant.Address, label)
		stored, err := query.Profile(f.ctx, &types.QueryProfileRequest{ModelId: modelID, ProfileVersion: 1})
		require.NoError(t, err)
		require.Equal(t, c.URI, stored.Profile.ManifestUri)

		// The stored registration digest binds this exact URI.
		expected := testModelProfileProjection(chainID, registrant.Address, label, 1, testServiceBondMinInitial)
		expected.MinStake.Denom = businessDenom
		expected.RegistrationFee = sdk.NewCoin(businessDenom, sdkmath.NewIntFromUint64(modelFee))
		expected.ManifestUri = c.URI
		digest, _, err := types.ModelRegistrationDigest(chainID, registrant.Address, expected)
		require.NoError(t, err)
		require.Equal(t, digest, stored.Profile.RegistrationDigest)
		require.Equal(t, digest, response.RegistrationDigest)
	}
}

// TestRegisterModelProfileManifestURIUsesParam checks that the length cap is
// read from ModelParamsV1.max_manifest_uri_bytes.
func TestRegisterModelProfileManifestURIUsesParam(t *testing.T) {
	f := initFixture(t)
	const businessDenom = "uusdc"
	const modelFee = uint64(2_000_001)
	const uri = "https://models.trueopen.example/m.json"
	genesis := types.DefaultGenesis()
	genesis.Params.Phase0.BusinessDenom = businessDenom
	genesis.Params.Treasury.ModelProfileRegistrationFee = types.AmountFromUint64(modelFee)
	genesis.Params.Model.MaxManifestUriBytes = uint32(len(uri)) - 1
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	const chainID = "manifest-uri-param-chain"
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithChainID(chainID).WithBlockHeight(25))
	registrant := hubIdentity(t, 173)
	registerHubTestAccount(t, f, registrant)
	f.bank.add(registrant.Address, businessDenom, modelFee)
	server := keeper.NewMsgServerImpl(f.keeper)

	profile := testModelProfileProjection(chainID, registrant.Address, "model-param", 1, testServiceBondMinInitial)
	profile.MinStake.Denom = businessDenom
	profile.RegistrationFee = sdk.NewCoin(businessDenom, sdkmath.NewIntFromUint64(modelFee))
	profile.ManifestUri = uri
	_, err := server.RegisterModelProfile(f.ctx, signedModelProfileRequest(t, f, registrant, profile))
	require.ErrorContains(t, err, "manifest_uri is invalid")
}
