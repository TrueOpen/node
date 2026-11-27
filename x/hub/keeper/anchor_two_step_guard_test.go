package keeper_test

import (
	"context"
	"testing"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// twoStepGate is the Task-side fact the registration guard reads. It also
// satisfies the role-fault gate, the dependency the Task keeper is wired in as.
type twoStepGate struct{ configured bool }

func (twoStepGate) RoleFaultConsumersClosed(context.Context, []byte, []byte, []byte) (bool, error) {
	return true, nil
}

func (g twoStepGate) AnchorTwoStepSamplingConfigured(context.Context) (bool, error) {
	return g.configured, nil
}

// plainGate is a role-fault gate that cannot answer for the Task parameters.
type plainGate struct{}

func (plainGate) RoleFaultConsumersClosed(context.Context, []byte, []byte, []byte) (bool, error) {
	return true, nil
}

func anchorProfile(t *testing.T, chainID, proposer, label string, anchorVersion uint32) shared.ModelProfileProjection {
	t.Helper()
	profile := testModelProfileProjection(chainID, proposer, label, 1, testServiceBondMinInitial)
	profile.VerificationProfile.EvidenceSchema.RequiredInferEvidence = append(profile.VerificationProfile.EvidenceSchema.RequiredInferEvidence,
		shared.InferEvidenceRequirementV1{
			EvidenceKind:            shared.EvidenceKind_EVIDENCE_KIND_WORKER_ANCHOR_OPENING,
			CommitmentSchemaVersion: anchorVersion, MaxEncodedSizeBytes: 16 << 20,
		})
	profile.VerificationProfile.EvidenceSchemaHash = nil
	hash, err := types.EvidenceSchemaHash(profile)
	require.NoError(t, err)
	profile.VerificationProfile.EvidenceSchemaHash = hash
	return profile
}

// A Profile version that asks for two-step sampling (anchor commitment schema
// version 2) is admitted only on a chain that can run it: the VRF beacon is
// required at this height and the Task parameters of the flow are set. A
// version-1 Profile is admitted as before on any chain.
func TestRegisterTwoStepProfileNeedsVRFAndParameters(t *testing.T) {
	const chainID, denom = "two-step-chain", "uusdc"
	for name, tc := range map[string]struct {
		vrfRequiredFrom uint64
		gate            types.RoleFaultConsumerGate
		anchorVersion   uint32
		wantErr         string
	}{
		"version 1 on a chain without VRF and without parameters": {0, plainGate{}, shared.WorkerAnchorCommitmentSchemaVersionV1, ""},
		"version 2 without the VRF requirement":                   {0, twoStepGate{configured: true}, shared.WorkerAnchorCommitmentSchemaVersionV2, "vrf_required_from_height"},
		"version 2 before the VRF requirement is reached":         {26, twoStepGate{configured: true}, shared.WorkerAnchorCommitmentSchemaVersionV2, "vrf_required_from_height"},
		"version 2 without the Task parameters":                   {20, twoStepGate{configured: false}, shared.WorkerAnchorCommitmentSchemaVersionV2, "anchor_list_window_blocks"},
		"version 2 when the Task parameters cannot be read":       {20, plainGate{}, shared.WorkerAnchorCommitmentSchemaVersionV2, "cannot be read"},
		"version 2 with VRF required and the parameters set":      {20, twoStepGate{configured: true}, shared.WorkerAnchorCommitmentSchemaVersionV2, ""},
		"version 2 at exactly the VRF requirement height":         {25, twoStepGate{configured: true}, shared.WorkerAnchorCommitmentSchemaVersionV2, ""},
		"an unknown anchor schema version":                        {20, twoStepGate{configured: true}, 3, "anchor commitment V1 or V2"},
	} {
		t.Run(name, func(t *testing.T) {
			f := initFixture(t)
			genesis := types.DefaultGenesis()
			genesis.Params.Phase0.BusinessDenom = denom
			require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
			params, err := f.keeper.Params.Get(f.ctx)
			require.NoError(t, err)
			params.Beacon.VrfRequiredFromHeight = tc.vrfRequiredFrom
			require.NoError(t, f.keeper.Params.Set(f.ctx, params))
			registrant := hubIdentity(t, 180)
			registerHubTestAccount(t, f, registrant)
			fee := types.ModelRegistrationFeeMinMicroUSDC
			f.bank.add(registrant.Address, denom, 10*fee)
			f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithChainID(chainID).WithBlockHeight(25))
			server := keeper.NewMsgServerImpl(f.keeper, types.MsgServerDependencies{RoleFaultConsumerGate: tc.gate})

			profile := anchorProfile(t, chainID, registrant.Address, "two-step-model", tc.anchorVersion)
			profile.MinStake.Denom = denom
			profile.RegistrationFee = sdk.NewCoin(denom, sdkmath.NewIntFromUint64(fee))
			before := f.bank.balance(registrant.Address, denom)
			request := signedModelProfileRequest(t, f, registrant, profile)
			response, err := server.RegisterModelProfile(f.ctx, request)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				require.Equal(t, before, f.bank.balance(registrant.Address, denom), "a refused registration moves no fee")
				_, err := f.keeper.Profile.Get(f.ctx, types.NewProfileStateKey(profile.ModelId, profile.ProfileVersion))
				require.Error(t, err, "a refused registration writes no Profile")
				return
			}
			require.NoError(t, err)
			require.False(t, response.IdempotentReplay)

			// An accepted registration stays reachable when the chain's
			// parameters change afterwards: the replay answers before the guard.
			params.Beacon.VrfRequiredFromHeight = 0
			require.NoError(t, f.keeper.Params.Set(f.ctx, params))
			replay, err := keeper.NewMsgServerImpl(f.keeper, types.MsgServerDependencies{RoleFaultConsumerGate: plainGate{}}).RegisterModelProfile(f.ctx, request)
			require.NoError(t, err)
			require.True(t, replay.IdempotentReplay)
		})
	}
}
