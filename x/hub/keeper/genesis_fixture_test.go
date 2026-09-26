package keeper_test

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestModelProfileGenesisRebuildsCanonicalRegistration(t *testing.T) {
	f := initFixture(t)
	genesis := hubGenesisWithIndexes()
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	bond := genesis.ServiceBonds[0]
	storedBond, err := f.keeper.ServiceBond.Get(f.ctx, bond.OperatorAddress)
	require.NoError(t, err)
	operatorBytes, err := sdk.AccAddressFromBech32(bond.OperatorAddress)
	require.NoError(t, err)
	require.Equal(t, []byte(operatorBytes), storedBond.OperatorAddress)
	unbonding := genesis.ServiceUnbondings[0]
	storedUnbonding, err := f.keeper.Unbonding.Get(f.ctx, types.NewUnbondingKey(unbonding.OperatorAddress, shared.Hash32Key(unbonding.UnbondingId)))
	require.NoError(t, err)
	require.Equal(t, []byte(operatorBytes), storedUnbonding.OperatorAddress)
	liability := genesis.TaskLiabilityReservations[0]
	liabilityKey := types.NewTaskLiabilityReservationKey(shared.Hash32Key(liability.TaskId), liability.Duty, liability.OperatorAddress)
	storedLiability, err := f.keeper.TaskLiabilityReservation.Get(f.ctx, liabilityKey)
	require.NoError(t, err)
	require.Equal(t, []byte(operatorBytes), storedLiability.OperatorAddress)
	responsibility := genesis.ServiceKeyResponsibilities[0]
	responsibilityKey := types.NewServiceKeyResponsibilityKey(responsibility.ParticipantType, responsibility.OperatorAddress, shared.Hash32Key(responsibility.ResponsibilityId))
	storedResponsibility, err := f.keeper.ServiceKeyResponsibility.Get(f.ctx, responsibilityKey)
	require.NoError(t, err)
	require.Equal(t, []byte(operatorBytes), storedResponsibility.OperatorAddress)

	receipt, err := f.keeper.RegistrationReceipt.Get(f.ctx, genesis.Profiles[0].RegistrationDigest)
	require.NoError(t, err)
	require.Equal(t, genesis.Profiles[0].ModelId, receipt.ModelId)
	require.Equal(t, genesis.Profiles[0].ProfileVersion, receipt.ProfileVersion)

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	restarted := initFixture(t)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))

	t.Run("registration digest", func(t *testing.T) {
		invalid := hubGenesisWithIndexes()
		invalid.Profiles[0].RegistrationDigest[0] ^= 0x01
		rejected := initFixture(t)
		require.ErrorContains(t, rejected.keeper.InitGenesis(rejected.ctx, *invalid), "canonical preimage")
	})

	t.Run("global price guard", func(t *testing.T) {
		invalid := hubGenesisWithIndexes()
		invalid.Params.Price.PriceHardMax = invalid.Profiles[0].PricingProfile.InitialOutputPrice - 1
		rejected := initFixture(t)
		require.ErrorContains(t, rejected.keeper.InitGenesis(rejected.ctx, *invalid), "global price guards")
	})
}

func testServiceDescriptor(participantType shared.ParticipantType, operatorAddress, uri string, height uint64) types.ServiceDescriptorState {
	kind := types.ServiceEndpointKind_SERVICE_ENDPOINT_KIND_OBJECT_GATEWAY_HTTPS
	if participantType == shared.ParticipantType_PARTICIPANT_TYPE_BUILDER {
		kind = types.ServiceEndpointKind_SERVICE_ENDPOINT_KIND_NEXUS_GRPC
	}
	state := types.ServiceDescriptorState{
		ParticipantType: participantType, OperatorAddress: operatorAddress,
		DescriptorVersion: 1, EndpointCount: 1,
		Endpoints:     []types.ServiceEndpointV1{{EndpointKind: kind, Uri: uri, ProtocolVersion: "v1"}},
		UpdatedHeight: height,
	}
	operatorBytes := sdk.MustAccAddressFromBech32(operatorAddress)
	descriptorHash, err := types.CanonicalServiceDescriptorHash(
		participantType, operatorBytes, state.DescriptorVersion, state.Endpoints, types.DefaultHubParams().Service,
	)
	if err != nil {
		panic(err)
	}
	state.DescriptorHash = descriptorHash
	return state
}

func hubGenesisWithIndexes() *types.GenesisState {
	return hubGenesisWithIndexesForChain(testHubChainID)
}

// hubGenesisWithIndexesForChain builds the document for one specific chain.
// registration_digest binds chain_id, so a fixture that runs the keeper under a
// different chain id has to build its genesis for that same chain or the row can
// never match its own canonical preimage.
func hubGenesisWithIndexesForChain(chainID string) *types.GenesisState {
	genesis := types.DefaultGenesis()
	serviceIdentity := mustHubIdentity(101)
	provider := serviceIdentity.Address
	proposer := mustHubIdentity(103).Address
	const minStake = uint64(1_000)
	const activeBond = uint64(1_000_000)
	supportWeight := minStake * uint64(genesis.Params.Support.ActiveSupportStakeCapMultiplier)

	genesis.Models = []types.ModelState{{
		ModelId: "model-a", ProposerAddress: proposer,
		Status: types.ModelStatusActive, StatusSource: types.ModelStatusSourceAutoProfile,
		LatestProfileVersion: 1, ActiveProfileCount: 1,
		RegistrationFeePaid: types.ModelRegistrationFeeMinMicroUSDC,
		CreatedHeight:       1, UpdatedHeight: 1,
	}}
	profile := mustTestProfileState("model-a", proposer, 1, minStake, 1)
	projection := testModelProfileProjection(profile.ModelId, profile.ProfileVersion, profile.MinStake)
	digest, _, err := types.ModelRegistrationDigest(chainID, proposer, projection)
	if err != nil {
		panic(err)
	}
	profile.RegistrationDigest = digest
	profile.ActiveSupporterCount = 1
	profile.ActiveSupportStake = supportWeight
	profile.EligibleSupportStake = supportWeight
	genesis.Profiles = []types.ProfileState{profile}
	genesis.CortexNodes = []types.CortexNodeState{{
		SchemaVersion: 1, OperatorAddress: provider,
		CurrentServiceAddress: provider, CurrentServicePubkey: serviceIdentity.PubKey,
		ServiceAuthorizationNonce: 1, ServiceKeyStatus: types.ServiceKeyStatusActive,
		CurrentDescriptorVersion: 1, RegisteredHeight: 1, UpdatedHeight: 1,
		ActiveTaskLiabilityCount: 0,
	}}
	genesis.ServiceDescriptors = []types.ServiceDescriptorState{
		testServiceDescriptor(shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, provider, "https://cortex.invalid/genesis", 1),
	}
	genesis.ServiceKeyResponsibilities = []types.ServiceKeyResponsibilityState{{
		ParticipantType:    shared.ParticipantType_PARTICIPANT_TYPE_CORTEX,
		OperatorAddress:    provider,
		ResponsibilityId:   hubHashBytes("challenge-verifier-genesis"),
		ResponsibilityKind: types.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_CHALLENGE_VERIFIER,
		SessionId:          "session-genesis",
		TaskId:             "task-genesis",
		CreatedHeight:      2,
		// The exported row carries the generation that acquired it, which for this
		// provider is the nonce its registration minted.
		ServiceAuthorizationNonce: 1,
	}}
	genesis.ServiceBonds = []types.ServiceBondState{{
		OperatorAddress: provider, ActiveBond: activeBond, EffectiveActiveBond: activeBond,
		ReservedLiability: 0, PendingUnbondingTotal: 100,
		Status: types.ServiceBondStatusActive, BondVersion: 1,
		LastStakeHeight: 1,
	}}
	genesis.ServiceUnbondings = []types.UnbondingState{{
		UnbondingId:     hubHashBytes("unbonding1"),
		OperatorAddress: provider,
		Amount:          100,
		RequestHeight:   30,
		MatureHeight:    40,
		Status:          types.UnbondingStatusOpen,
	}}
	genesis.TaskLiabilityReservations = []types.TaskLiabilityReservationState{{
		SchemaVersion: 1, TaskId: hubHashBytes("task-liability-genesis"),
		OperatorAddress: provider, Duty: shared.DutyWorker,
		BondVersion: 1, CapabilityVersion: 1, ReservedAmount: 15_000,
		Status:                  types.TaskLiabilityStatusReleased,
		CandidatePoolSnapshotId: hubHashBytes("task-liability-genesis-snapshot"), Slot: 0, SlotVersion: 1,
	}}
	genesis.ProfileCapabilities = []types.ProfileCapabilityState{{
		OperatorAddress: provider, ModelId: "model-a", ProfileVersion: 1,
		InferenceCapability: true, VerificationCapability: true,
		CapabilityVersion: 1,
	}}
	genesis.ModelSupports = []types.ModelSupportState{{
		OperatorAddress:              provider,
		ModelId:                      "model-a",
		ProfileVersion:               1,
		DeclaredSupport:              true,
		SupportActive:                true,
		ActivationKind:               types.ModelSupportActivationVerifierAssignedValid,
		FirstActivationDuty:          shared.DutyVerifier,
		FirstSupportTaskId:           hubHashBytes("first-support-task"),
		SupportFreshUntilEpoch:       2,
		ActiveSupportStakeSnapshot:   supportWeight,
		EligibleSupportStakeSnapshot: supportWeight,
		SupportVersion:               1,
	}}
	genesis.DailySupports = []types.DailySupportState{{
		Epoch:                 0,
		OperatorAddress:       provider,
		SupportedProfilesHash: hubHashBytes("profiles"),
		SignatureDigest:       hubHashBytes("signature"),
		AcceptedHeight:        1,
	}}
	return genesis
}
