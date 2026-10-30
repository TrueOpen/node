package cmd

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	slashingtypes "github.com/cosmos/cosmos-sdk/x/slashing/types"
	proto "github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

type genesisSeedTestIdentity struct {
	address   string
	pubKeyHex string
}

func newGenesisSeedTestDescriptor(kind, uri, protocolVersion string) *genesisSeedServiceDescriptor {
	return &genesisSeedServiceDescriptor{Endpoints: []genesisSeedServiceEndpoint{{
		EndpointKind: kind, URI: uri, ProtocolVersion: protocolVersion,
	}}}
}

// genesisSeedTestServiceBondMinInitial mirrors params.Service.ServiceBondMinInitial.
// The duplicated keeper.MinServiceBond constant (500_000) was deleted, making
// params the only floor, so seeded bonds and profile min_stake must use this value.
var genesisSeedTestServiceBondMinInitial = func() uint64 {
	value, err := hubtypes.AmountToUint64(hubtypes.DefaultHubParams().Service.ServiceBondMinInitial, false)
	if err != nil {
		panic(err)
	}
	return value
}()

func TestReadGenesisSeedRejectsRemovedCortexNodeID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seed.json")
	require.NoError(t, os.WriteFile(path, []byte(`{
  "version": 1,
  "cortex_nodes": [{
    "cortex_node_id": "removed",
    "operator_address": "trueopen1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq3w3xs0"
  }]
}`), 0o600))

	_, err := readGenesisSeed(path)
	require.ErrorContains(t, err, "unknown field \"cortex_node_id\"")
}

func TestReadGenesisSeedRejectsLegacyDescriptorFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seed.json")
	require.NoError(t, os.WriteFile(path, []byte(`{
  "version": 1,
  "builders": [{
    "descriptor_uri": "https://builder.example/.well-known/trueopen-builder.json"
  }]
}`), 0o600))

	_, err := readGenesisSeed(path)
	require.ErrorContains(t, err, "unknown field \"descriptor_uri\"")
}

func TestApplyGenesisSeedBuildsRunnableStateAndIsIdempotent(t *testing.T) {
	interfaceRegistry := codectypes.NewInterfaceRegistry()
	authtypes.RegisterInterfaces(interfaceRegistry)
	cdc := codec.NewProtoCodec(interfaceRegistry)
	builders := []genesisSeedTestIdentity{
		newGenesisSeedTestIdentity(),
		newGenesisSeedTestIdentity(),
		newGenesisSeedTestIdentity(),
	}
	builderServices := []genesisSeedTestIdentity{
		newGenesisSeedTestIdentity(),
		newGenesisSeedTestIdentity(),
		newGenesisSeedTestIdentity(),
	}
	cortexNodes := []genesisSeedTestIdentity{
		newGenesisSeedTestIdentity(),
		newGenesisSeedTestIdentity(),
		newGenesisSeedTestIdentity(),
	}
	cortexOperators := []genesisSeedTestIdentity{
		newGenesisSeedTestIdentity(),
		newGenesisSeedTestIdentity(),
		newGenesisSeedTestIdentity(),
	}
	const accountBalance = uint64(10_000_000)
	seed := genesisSeed{
		Version:           genesisSeedVersion,
		AccountBalance:    accountBalance,
		SupportUntilEpoch: 10_000,
		HubParams:         json.RawMessage(`{"epoch":{"epoch_length_blocks":"604800"}}`),
		TaskParams:        json.RawMessage(`{"deadlines":{"self_rescue_margin_blocks":"17"}}`),
	}
	seed.Models = []genesisSeedModel{newGenesisSeedTestModel(cortexOperators[0].address)}
	for i, identity := range builders {
		seed.Builders = append(seed.Builders, genesisSeedBuilder{
			Address: identity.address, ServiceAddress: builderServices[i].address,
			ServicePubKey: builderServices[i].pubKeyHex,
			Descriptor: newGenesisSeedTestDescriptor(
				"SERVICE_ENDPOINT_KIND_NEXUS_GRPC", "https://builders.example/"+identity.address, "trueopen-nexus-ingress-v1",
			),
		})
	}
	for i, identity := range cortexNodes {
		seed.CortexNodes = append(seed.CortexNodes, genesisSeedCortex{
			OperatorAddress: cortexOperators[i].address,
			ServiceAddress:  identity.address, ServicePubKey: identity.pubKeyHex, Bond: 2_000_000,
			Descriptor: newGenesisSeedTestDescriptor(
				"SERVICE_ENDPOINT_KIND_OBJECT_GATEWAY_HTTPS", "https://cortex.example/"+identity.address, "trueopen-object-gateway-v1",
			),
			SupportedModels: []genesisSeedModelSupport{{
				ModelRef: "trueopen/golden-model", FirstSupportProfileVersion: 1, Active: true,
			}},
		})
	}
	require.NoError(t, validateGenesisSeed(seed))

	bank := banktypes.DefaultGenesisState()
	expectedAddresses := make([]string, 0, 12)
	for _, identities := range [][]genesisSeedTestIdentity{builders, builderServices, cortexNodes, cortexOperators} {
		for _, identity := range identities {
			expectedAddresses = append(expectedAddresses, identity.address)
		}
	}
	rawGenesis := makeGenesisSeedDocument(
		t, cdc, hubtypes.DefaultGenesis(), bank, authtypes.DefaultGenesisState(),
	)

	updated, err := applyGenesisSeed(cdc, rawGenesis, seed)
	require.NoError(t, err)
	seededHub, seededBank, seededAuth := decodeGenesisSeedDocument(t, cdc, updated)
	seededTask := decodeGenesisSeedTask(t, cdc, updated)
	seededGov, seededSlashing := decodeSDKGenesisParams(t, cdc, updated)
	require.Equal(t, uint64(604_800), seededHub.Params.Epoch.EpochLengthBlocks)
	require.Equal(t, uint64(17), seededTask.Params.Deadlines.SelfRescueMarginBlocks)
	require.Equal(t, sdk.NewCoins(sdk.NewInt64Coin("uusdc", 10_000_000)), sdk.Coins(seededGov.Params.MinDeposit))
	require.Equal(t, sdk.NewCoins(sdk.NewInt64Coin("uusdc", 50_000_000)), sdk.Coins(seededGov.Params.ExpeditedMinDeposit))
	require.False(t, seededGov.Params.BurnVoteQuorum)
	require.False(t, seededGov.Params.BurnProposalDepositPrevote)
	require.True(t, seededGov.Params.BurnVoteVeto)
	require.True(t, seededSlashing.Params.SlashFractionDoubleSign.IsZero())
	require.True(t, seededSlashing.Params.SlashFractionDowntime.IsZero())
	require.Equal(t, int64(5000), seededSlashing.Params.SignedBlocksWindow)
	assertGenesisSeedState(
		t, seededHub, seededBank, seededAuth, builders[0].address,
		cortexOperators[0].address, expectedAddresses, accountBalance,
	)

	// An exported/migrated genesis may already contain every configured row.
	// Reapplying the same file must not register or bond anything twice.
	reapplied, err := applyGenesisSeed(cdc, updated, seed)
	require.NoError(t, err)
	reappliedHub, reappliedBank, reappliedAuth := decodeGenesisSeedDocument(t, cdc, reapplied)
	assertGenesisSeedState(
		t, reappliedHub, reappliedBank, reappliedAuth, builders[0].address,
		cortexOperators[0].address, expectedAddresses, accountBalance,
	)
	require.Equal(t, seededHub, reappliedHub)
	require.Equal(t, seededBank, reappliedBank)
	require.Equal(t, seededAuth, reappliedAuth)
	require.Len(t, seededHub.Profiles, 1)
	require.Equal(t, seed.Models[0].Profile.ManifestURI, seededHub.Profiles[0].ManifestUri)

	// A seeded profile goes through the same manifest_uri syntax check as
	// MsgRegisterModelProfile.
	seed.Models[0].Profile.ManifestURI = "http://models.trueopen.example/m.json"
	_, err = applyGenesisSeed(cdc, rawGenesis, seed)
	require.ErrorContains(t, err, "manifest_uri")
}

func TestGenesisSeedLeavesUnlistedSectionsEmpty(t *testing.T) {
	interfaceRegistry := codectypes.NewInterfaceRegistry()
	authtypes.RegisterInterfaces(interfaceRegistry)
	cdc := codec.NewProtoCodec(interfaceRegistry)
	raw := makeGenesisSeedDocument(
		t, cdc, hubtypes.DefaultGenesis(), banktypes.DefaultGenesisState(), authtypes.DefaultGenesisState(),
	)
	updated, err := applyGenesisSeed(cdc, raw, genesisSeed{Version: genesisSeedVersion})
	require.NoError(t, err)
	hub, bank, auth := decodeGenesisSeedDocument(t, cdc, updated)
	require.Empty(t, hub.Builders)
	require.Empty(t, hub.CortexNodes)
	require.Empty(t, hub.Models)
	require.Empty(t, bank.Balances)
	require.Empty(t, auth.Accounts)
}

func TestGenesisSeedFaucetAccountIsFundedIndependentlyOfAccountBalance(t *testing.T) {
	identity := newGenesisSeedTestIdentity()
	interfaceRegistry := codectypes.NewInterfaceRegistry()
	authtypes.RegisterInterfaces(interfaceRegistry)
	cdc := codec.NewProtoCodec(interfaceRegistry)
	raw := makeGenesisSeedDocument(
		t, cdc, hubtypes.DefaultGenesis(), banktypes.DefaultGenesisState(), authtypes.DefaultGenesisState(),
	)
	const faucetBalance = uint64(1_000_000_000_000)
	seed := genesisSeed{
		Version:        genesisSeedVersion,
		AccountBalance: 1,
		FaucetAccounts: []genesisSeedFaucetAccount{
			{Address: identity.address, Balance: faucetBalance},
		},
	}

	updated, err := applyGenesisSeed(cdc, raw, seed)
	require.NoError(t, err)
	_, bank, _ := decodeGenesisSeedDocument(t, cdc, updated)
	require.Equal(t, faucetBalance, genesisBalanceAmount(bank, identity.address))
}

func TestGenesisSeedRejectsFaucetAccountWithZeroBalance(t *testing.T) {
	identity := newGenesisSeedTestIdentity()
	interfaceRegistry := codectypes.NewInterfaceRegistry()
	authtypes.RegisterInterfaces(interfaceRegistry)
	cdc := codec.NewProtoCodec(interfaceRegistry)
	raw := makeGenesisSeedDocument(
		t, cdc, hubtypes.DefaultGenesis(), banktypes.DefaultGenesisState(), authtypes.DefaultGenesisState(),
	)
	seed := genesisSeed{
		Version: genesisSeedVersion,
		FaucetAccounts: []genesisSeedFaucetAccount{
			{Address: identity.address, Balance: 0},
		},
	}

	_, err := applyGenesisSeed(cdc, raw, seed)
	require.Error(t, err)
}

func TestGenesisSeedCortexDescriptorIsOptionalButAtomic(t *testing.T) {
	node := newGenesisSeedTestIdentity()
	operator := newGenesisSeedTestIdentity()
	seed := genesisSeed{
		AccountBalance: 10_000_000,
		CortexNodes: []genesisSeedCortex{{
			OperatorAddress: operator.address,
			ServiceAddress:  node.address, ServicePubKey: node.pubKeyHex,
			Bond: genesisSeedTestServiceBondMinInitial,
		}},
	}
	require.NoError(t, validateGenesisSeed(seed))
	hub := hubtypes.DefaultGenesis()
	require.NoError(t, appendGenesisCortexNode(hub, seed.CortexNodes[0], 7))
	require.Zero(t, hub.CortexNodes[0].CurrentDescriptorVersion)
	require.Empty(t, hub.ServiceDescriptors)

	seed.CortexNodes[0].Descriptor = &genesisSeedServiceDescriptor{}
	require.ErrorContains(t, validateGenesisSeed(seed), "descriptor endpoint count must be between 1")

	seed.CortexNodes[0].Descriptor = newGenesisSeedTestDescriptor(
		"SERVICE_ENDPOINT_KIND_OBJECT_GATEWAY_HTTPS", "https://cortex.example", "trueopen-object-gateway-v1",
	)
	require.NoError(t, validateGenesisSeed(seed))
	hub = hubtypes.DefaultGenesis()
	require.NoError(t, appendGenesisCortexNode(hub, seed.CortexNodes[0], 7))
	require.Equal(t, uint64(1), hub.CortexNodes[0].CurrentDescriptorVersion)
	require.Len(t, hub.ServiceDescriptors, 1)
	descriptor := hub.ServiceDescriptors[0]
	require.Equal(t, uint64(1), descriptor.DescriptorVersion)
	require.Equal(t, uint32(1), descriptor.EndpointCount)
	require.Len(t, descriptor.Endpoints, 1)
	require.Equal(t, seed.CortexNodes[0].Descriptor.Endpoints[0].URI, descriptor.Endpoints[0].Uri)
	require.Equal(t, shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, descriptor.ParticipantType)
	require.Equal(t, operator.address, descriptor.OperatorAddress)
	operatorBytes, err := sdk.AccAddressFromBech32(operator.address)
	require.NoError(t, err)
	expectedHash, err := hubtypes.CanonicalServiceDescriptorHash(
		descriptor.ParticipantType, operatorBytes, descriptor.DescriptorVersion,
		descriptor.Endpoints, hub.Params.Service,
	)
	require.NoError(t, err)
	require.Equal(t, expectedHash, descriptor.DescriptorHash)
	require.NoError(t, hub.Validate())
}

func TestLocalnetGenesisSeedContainsQueriedBuilders(t *testing.T) {
	seed, err := readGenesisSeed(filepath.Join("..", "..", "..", "config", "localnet_genesis_seed.json"))
	require.NoError(t, err)
	require.Equal(t, uint64(100_000_000_000_000), seed.AccountBalance)
	require.True(t, seed.NodeConfig.TaskEventGRPC.Enabled)
	require.True(t, seed.NodeConfig.TaskEventGRPC.ProtocolEventsEnabled)
	require.NotEmpty(t, seed.HubParams)
	require.NotEmpty(t, seed.TaskParams)
	require.Len(t, seed.Builders, 3)
	require.Len(t, seed.CortexNodes, 4)
	require.Len(t, seed.Models, 1)
	expectedBuilderEndpoints := []struct {
		address       string
		servicePubKey string
		baseURL       string
		tlsPubkeyHash string
	}{
		{
			"trueopen1zxpnl7qm588xhfr4vnh6sunjarl0t83lzhseks",
			"03f43ccf6784a7fc3d6fd06a263eb8fd0e887fd9226715110f3b7fbe1a58da92dc",
			"https://217.15.167.12:8080",
			"1c174d14264b6c0491d28e9717023421e33e197fe35430971c7597525eca99ec",
		},
		{
			"trueopen1s7xj7hqja40mxgksz8ec3g4rf30ahxddlpd832",
			"0288d96e81bdec3c067747a429d957cef66127f4f2afb4b41d512cb2b3e992994f",
			"https://217.15.167.12:8081",
			"5c0347bfe6cd88a1923f98ca6e71b18bc894ff755e3978ba76c383776df7e517",
		},
		{
			"trueopen1668vt9fpa97h65c76kskhah0ep6kk39zsv4y05",
			"02ef48091c8c932ac9b6684603d5c65a0abf23f4a3b3fbed136f3947479d32ebff",
			"https://217.15.167.12:8082",
			"3635c48b05a5552aacd9bba561c83e56de991c6dfc197a9a9542c40dc2d7d43f",
		},
	}
	for i, builder := range seed.Builders {
		require.NotNil(t, builder.Descriptor)
		expected := expectedBuilderEndpoints[i]
		require.Equal(t, expected.address, builder.Address)
		require.Equal(t, expected.address, builder.ServiceAddress)
		require.Equal(t, expected.servicePubKey, builder.ServicePubKey)
		require.Equal(t, []genesisSeedServiceEndpoint{
			{
				EndpointKind: "SERVICE_ENDPOINT_KIND_NEXUS_GRPC", URI: expected.baseURL,
				ProtocolVersion: "v1", TLSPubkeyHash: expected.tlsPubkeyHash,
			},
			{
				EndpointKind: "SERVICE_ENDPOINT_KIND_OBJECT_GATEWAY_HTTPS", URI: expected.baseURL,
				ProtocolVersion: "v1", TLSPubkeyHash: expected.tlsPubkeyHash,
			},
			{
				EndpointKind: "SERVICE_ENDPOINT_KIND_HEALTH_HTTPS", URI: expected.baseURL + "/healthz",
				ProtocolVersion: "v1", TLSPubkeyHash: expected.tlsPubkeyHash,
			},
		}, builder.Descriptor.Endpoints)
	}
	model := seed.Models[0]
	require.Equal(t, "HUGGINGFACE", model.Profile.Source.Provider)
	require.Equal(t, "Qwen/Qwen3-8B", model.Profile.Source.RepoId)
	require.Equal(t, uint32(1), model.Profile.ProfileVersion)
	require.Equal(t, []string{"TEXT_GENERATION", "CHAT"}, model.Profile.TaskTypes)
	require.Equal(t, "uusdc", model.Profile.MinStake.Denom)
	require.Equal(t, uint64(1_000_000), model.Profile.MinStake.Amount)
	require.Equal(t, "uusdc", model.Profile.RegistrationFee.Denom)
	require.Equal(t, uint64(1_000_000), model.Profile.RegistrationFee.Amount)
	require.Equal(t, seed.CortexNodes[0].OperatorAddress, model.ProposerAddress)
	// The dev model profile is the published canonical projection of the dev
	// chain, so its hash, challenge window, bootstrap timeouts and pass limits
	// are pinned to it; a seed edit that drifts from it no longer matches the
	// registry manifest.
	require.Equal(t, "0xa25b2d5b0d7cb32f6f42bca7ccce6175ace8bd12b3b92ec4c88022bb4bdcb023", model.Profile.ManifestHash)
	require.Equal(t, uint64(100), model.Profile.ChallengeOpenWindow)
	require.Equal(t, uint32(20), model.Profile.TimeoutBootstrapProfile.CommitTimeoutBootstrapBlocks)
	require.Equal(t, uint32(100), model.Profile.TimeoutBootstrapProfile.InferTimeoutBootstrapBlocks)
	require.Equal(t, uint32(50), model.Profile.TimeoutBootstrapProfile.VerifyTimeoutBootstrapBlocks)
	require.Equal(t, uint32(100000), model.Profile.VerificationThresholds.PassAbsLogprobDiffP95Max)
	require.Equal(t, uint32(200000), model.Profile.VerificationThresholds.PassAbsLogprobDiffP99Max)
	operators := make(map[string]struct{}, len(seed.CortexNodes))
	serviceAddresses := make(map[string]struct{}, len(seed.CortexNodes))
	for _, cortex := range seed.CortexNodes {
		operators[cortex.OperatorAddress] = struct{}{}
		serviceAddresses[cortex.ServiceAddress] = struct{}{}
		require.Nil(t, cortex.Descriptor)
		require.Equal(t, []genesisSeedModelSupport{{ModelRef: model.Profile.Source.RepoId, FirstSupportProfileVersion: 1, Active: true}}, cortex.SupportedModels)
	}
	require.Len(t, operators, 4)
	require.Len(t, serviceAddresses, 4)
	interfaceRegistry := codectypes.NewInterfaceRegistry()
	cdc := codec.NewProtoCodec(interfaceRegistry)
	hubParams := hubtypes.DefaultHubParams()
	require.NoError(t, applyHubParamsOverride(cdc, &hubParams, seed.HubParams))
	// Short epochs and windows exercise Phase 1 rollover and delayed actions.
	require.Equal(t, uint64(120), hubParams.Epoch.EpochLengthBlocks)
	require.Equal(t, uint64(1), hubParams.Epoch.DeltaWBlocks)
	require.Equal(t, uint64(10), hubParams.Builder.OpenVerifyBuilderProposalWindowBlocks)
	require.Equal(t, uint32(180), hubParams.Support.SupportWindowEpochs)
	require.Equal(t, uint32(1024), hubParams.Reward.MaxRewardEpochItemsPerBlock)
	require.Equal(t, uint32(32), hubParams.Support.MaxSupportExpiryItemsPerBlock)
	requireGenesisSeedListsAllParams(t, cdc, &hubParams, seed.HubParams)
	taskParams := tasktypes.DefaultTaskParams()
	require.NoError(t, applyTaskParamsOverride(cdc, &taskParams, seed.TaskParams))
	require.Equal(t, uint64(60), taskParams.Deadlines.VerifyOpenDeadlineBlocks)
	require.Equal(t, uint64(20), taskParams.Deadlines.CommitWindowBlocks)
	require.Equal(t, uint64(10), taskParams.Deadlines.RevealWindowBlocks)
	require.Equal(t, uint64(60), taskParams.Deadlines.CollectionWindowBlocks)
	require.Equal(t, uint64(10), taskParams.Deadlines.SettleMarginBlocks)
	require.Equal(t, uint64(10), taskParams.Deadlines.SelfRescueMarginBlocks)
	require.NoError(t, tasktypes.ValidateVerifyOpenClock(taskParams.Deadlines, hubParams.Epoch.DeltaWBlocks, hubParams.Builder.OpenVerifyBuilderProposalWindowBlocks))
	require.NoError(t, tasktypes.ValidateSessionCloseHorizon(taskParams, hubtypes.ProfileChallengeOpenWindowMaxBlocks))
	require.Less(t, 2*hubParams.Epoch.DeltaWBlocks+hubParams.Builder.OpenVerifyBuilderProposalWindowBlocks+taskParams.Deadlines.SelfRescueMarginBlocks, taskParams.Deadlines.VerifyOpenDeadlineBlocks)
	require.Equal(t, uint32(16), taskParams.Generation.StopSequenceMaxItems)
	requireGenesisSeedListsAllParams(t, cdc, &taskParams, seed.TaskParams)
}

func TestGenesisSeedRejectsProtocolEventsWithoutTaskEventService(t *testing.T) {
	seed := genesisSeed{NodeConfig: genesisSeedNodeConfig{TaskEventGRPC: genesisSeedTaskEventGRPCConfig{
		ProtocolEventsEnabled: true,
	}}}
	require.ErrorContains(t, validateGenesisSeed(seed), "requires enabled=true")
}

func TestLocalnetGenesisSeedAppliesRunnableModelSupportState(t *testing.T) {
	seed, err := readGenesisSeed(filepath.Join("..", "..", "..", "config", "localnet_genesis_seed.json"))
	require.NoError(t, err)
	interfaceRegistry := codectypes.NewInterfaceRegistry()
	authtypes.RegisterInterfaces(interfaceRegistry)
	cdc := codec.NewProtoCodec(interfaceRegistry)
	raw := makeGenesisSeedDocument(
		t, cdc, hubtypes.DefaultGenesis(), banktypes.DefaultGenesisState(), authtypes.DefaultGenesisState(),
	)

	updated, err := applyGenesisSeed(cdc, raw, seed)
	require.NoError(t, err)
	hub, bank, auth := decodeGenesisSeedDocument(t, cdc, updated)
	require.NoError(t, hub.Validate())
	require.NoError(t, bank.Validate())
	require.NoError(t, authtypes.ValidateGenesis(auth))
	require.Len(t, hub.Builders, 3)
	require.Len(t, hub.CortexNodes, 4)
	require.Len(t, hub.ServiceBonds, 4)
	require.Len(t, hub.ServiceDescriptors, 3)
	for i, builder := range seed.Builders {
		descriptor := hub.ServiceDescriptors[i]
		require.Equal(t, builder.Address, descriptor.OperatorAddress)
		expectedEndpoints, err := genesisServiceEndpoints(builder.Descriptor)
		require.NoError(t, err)
		require.Equal(t, expectedEndpoints, descriptor.Endpoints)
		operatorBytes, err := sdk.AccAddressFromBech32(builder.Address)
		require.NoError(t, err)
		expectedHash, err := hubtypes.CanonicalServiceDescriptorHash(
			shared.ParticipantType_PARTICIPANT_TYPE_BUILDER,
			operatorBytes,
			descriptor.DescriptorVersion,
			expectedEndpoints,
			hub.Params.Service,
		)
		require.NoError(t, err)
		require.Equal(t, expectedHash, descriptor.DescriptorHash)
	}
	for _, node := range hub.CortexNodes {
		require.Zero(t, node.CurrentDescriptorVersion)
	}
	require.Len(t, hub.ModelCapabilities, 4)
	require.Len(t, hub.ModelSupports, 4)
	require.Len(t, hub.DailySupports, 4)
	require.Len(t, hub.Models, 1)
	require.Len(t, hub.Profiles, 1)
	require.Equal(t, hubtypes.ModelStatusActive, hub.Models[0].Status)
	require.Equal(t, hubtypes.ModelStatusRegistered, hub.Profiles[0].Status)
	require.Equal(t, []shared.TaskType{
		shared.TaskType_TASK_TYPE_TEXT_GENERATION,
		shared.TaskType_TASK_TYPE_CHAT,
	}, hub.Profiles[0].TaskTypes)
	require.Equal(t, uint32(4), hub.Models[0].ActiveSupporterCount)
	for _, support := range hub.ModelSupports {
		require.True(t, support.DeclaredSupport)
		require.True(t, support.SupportActive)
		require.Equal(t, seed.SupportUntilEpoch, support.SupportFreshUntilEpoch)
	}
	// TreasuryState is (balance, treasury_version) now;
	// total_collected and the per-epoch treasury ledger both left the wire
	// with TreasuryEpochState. The genesis seed credits the registration fee
	// into balance, so that is what this pins.
	require.Equal(t, shared.NewAmount(hubtypes.ModelRegistrationFeeMinMicroUSDC), hub.Treasury.Balance)
	require.Equal(t, hubtypes.ModelRegistrationFeeMinMicroUSDC, genesisBalanceAmount(
		bank,
		authtypes.NewModuleAddress(hubtypes.TreasuryModuleName).String(),
	))
	require.NotEmpty(t, seed.FaucetAccounts)
	for _, faucet := range seed.FaucetAccounts {
		require.Equal(t, faucet.Balance, genesisBalanceAmount(bank, faucet.Address))
	}
}

func TestGenesisSeedParameterOverridesRejectUnknownFields(t *testing.T) {
	cdc := codec.NewProtoCodec(codectypes.NewInterfaceRegistry())
	params := hubtypes.DefaultHubParams()
	err := applyHubParamsOverride(cdc, &params, json.RawMessage(`{"unknown_parameter":1}`))
	require.Error(t, err)
}

func TestGenesisSeedCommandIsRegistered(t *testing.T) {
	root := NewRootCmd()
	command, _, err := root.Find([]string{"genesis", "apply-seed"})
	require.NoError(t, err)
	require.Equal(t, "apply-seed", command.Name())
}

func newGenesisSeedTestIdentity() genesisSeedTestIdentity {
	privateKey := secp256k1.GenPrivKey()
	publicKey := privateKey.PubKey().(*secp256k1.PubKey)
	return genesisSeedTestIdentity{
		address:   sdk.AccAddress(hubtypes.ServicePubKeyAddress(publicKey)).String(),
		pubKeyHex: hex.EncodeToString(publicKey.Bytes()),
	}
}

func newGenesisSeedTestModel(proposer string) genesisSeedModel {
	const nonZeroHash = "0x1111111111111111111111111111111111111111111111111111111111111111"
	return genesisSeedModel{
		ProposerAddress: proposer,
		Profile: genesisSeedProfile{
			ChallengeOpenWindow: hubtypes.ProfileChallengeOpenWindowMinBlocks,
			GenerationType:      "SAMPLED", ManifestHash: nonZeroHash,
			ManifestURI: "https://models.trueopen.example/manifests/golden-model/v1.json",
			MinStake:    genesisSeedCoin{Amount: genesisSeedTestServiceBondMinInitial, Denom: hubtypes.DefaultBusinessDenom},
			Source: shared.SourceRefV1{
				Provider: "HUGGINGFACE", RepoId: "trueopen/golden-model", RepoType: "model",
				ResolverVersion: "HF_RESOLVER_V1", Revision: "0123456789abcdef0123456789abcdef01234567",
				SourceUri: "hf://trueopen/golden-model@0123456789abcdef0123456789abcdef01234567",
			},
			PricingProfile: genesisSeedPricingProfile{
				InitialOutputPrice: 1_000, MinOrderValue: 1_000, VerifyRatioBps: 1_000,
			},
			ProfileVersion: 1, RegistrationFee: genesisSeedCoin{Amount: 1_000_000, Denom: hubtypes.DefaultBusinessDenom},
			RequiredTopK: 20, ResourceTier: 1, RuntimeClass: "CAUSAL_LM_PREFILL_LOGPROBS_V1",
			SchemaHash: nonZeroHash, TaskTypes: []string{"TEXT_GENERATION", "CHAT"}, TokenizerHash: nonZeroHash,
			VerificationProfile: genesisSeedVerificationProfile{
				CanonicalEncodingVersion: "CANONICAL_OUTPUT_TEXT_V1",
				EvidenceSchema: genesisSeedEvidenceSchema{
					SchemaVersion: 1,
					RequiredInferEvidence: []genesisSeedEvidenceRequirement{
						{EvidenceKind: "WORKER_VALUE_OPENING", CommitmentSchemaVersion: 3, MaxEncodedSizeBytes: 1 << 30},
						{EvidenceKind: "WORKER_TOKEN_OPENING", CommitmentSchemaVersion: 1, MaxEncodedSizeBytes: 1 << 26},
					},
				},
				JudgmentFunctionVersion: "PREFILL_GENERATED_TOKEN_METRICS_V1", MetricAggregateProofVersion: "PREFILL_METRIC_AGGREGATE_PROOF_V1",
				Metrics:             genesisSeedMetricSpec{CompareLogprobDiff: true, ComparedTopK: 20, NumericScale: "FP_1E6"},
				RequireFinishReason: true, RequireOutputTokenIDs: true, TokenScope: "ALL_GENERATED_OUTPUT_TOKENS",
				VerificationMode: "SINGLE_SAMPLE", VerificationProfileID: 1,
			},
		},
	}
}

func makeGenesisSeedDocument(
	t *testing.T,
	cdc codec.Codec,
	hub *hubtypes.GenesisState,
	bank *banktypes.GenesisState,
	auth *authtypes.GenesisState,
) []byte {
	t.Helper()
	hubJSON, err := cdc.MarshalJSON(hub)
	require.NoError(t, err)
	bankJSON, err := cdc.MarshalJSON(bank)
	require.NoError(t, err)
	authJSON, err := cdc.MarshalJSON(auth)
	require.NoError(t, err)
	appStateJSON, err := json.Marshal(map[string]json.RawMessage{
		hubtypes.ModuleName:      hubJSON,
		banktypes.ModuleName:     bankJSON,
		authtypes.ModuleName:     authJSON,
		tasktypes.ModuleName:     mustGenesisSeedJSON(t, cdc, tasktypes.DefaultGenesis()),
		govtypes.ModuleName:      mustGenesisSeedJSON(t, cdc, govv1.DefaultGenesisState()),
		slashingtypes.ModuleName: mustGenesisSeedJSON(t, cdc, slashingtypes.DefaultGenesisState()),
	})
	require.NoError(t, err)
	raw, err := json.Marshal(map[string]json.RawMessage{
		"app_state":      appStateJSON,
		"chain_id":       json.RawMessage(`"trueopen-genesis-seed-test"`),
		"initial_height": json.RawMessage(`"77"`),
	})
	require.NoError(t, err)
	return raw
}

func decodeGenesisSeedTask(t *testing.T, cdc codec.Codec, raw []byte) tasktypes.GenesisState {
	t.Helper()
	var document map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &document))
	var appState map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(document["app_state"], &appState))
	var task tasktypes.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(appState[tasktypes.ModuleName], &task))
	return task
}

func decodeSDKGenesisParams(
	t *testing.T,
	cdc codec.Codec,
	raw []byte,
) (govv1.GenesisState, slashingtypes.GenesisState) {
	t.Helper()
	var document map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &document))
	var appState map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(document["app_state"], &appState))
	var governance govv1.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(appState[govtypes.ModuleName], &governance))
	var slashing slashingtypes.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(appState[slashingtypes.ModuleName], &slashing))
	return governance, slashing
}

func mustGenesisSeedJSON(t *testing.T, cdc codec.Codec, message proto.Message) json.RawMessage {
	t.Helper()
	raw, err := cdc.MarshalJSON(message)
	require.NoError(t, err)
	return raw
}

func requireGenesisSeedListsAllParams(t *testing.T, cdc codec.Codec, params proto.Message, configured json.RawMessage) {
	t.Helper()
	encoded, err := cdc.MarshalJSON(params)
	require.NoError(t, err)
	var schemaFields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(encoded, &schemaFields))
	var configuredFields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(configured, &configuredFields))
	require.ElementsMatch(t, mapKeys(schemaFields), mapKeys(configuredFields))
}

func mapKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func decodeGenesisSeedDocument(
	t *testing.T,
	cdc codec.Codec,
	raw []byte,
) (hubtypes.GenesisState, banktypes.GenesisState, authtypes.GenesisState) {
	t.Helper()
	var document map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &document))
	var appState map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(document["app_state"], &appState))
	var hub hubtypes.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(appState[hubtypes.ModuleName], &hub))
	var bank banktypes.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(appState[banktypes.ModuleName], &bank))
	var auth authtypes.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(appState[authtypes.ModuleName], &auth))
	return hub, bank, auth
}

func assertGenesisSeedState(
	t *testing.T,
	hub hubtypes.GenesisState,
	bank banktypes.GenesisState,
	auth authtypes.GenesisState,
	firstBuilder string,
	firstCortexOperator string,
	expectedAddresses []string,
	accountBalance uint64,
) {
	t.Helper()
	require.Len(t, hub.Builders, 3)
	require.Equal(t, uint64(77), hub.Builders[0].RegisteredHeight)
	require.Len(t, hub.CortexNodes, 3)
	require.Len(t, hub.ServiceBonds, 3)
	require.Len(t, hub.CandidateSlotCurrents, 3)
	require.Len(t, hub.CandidateSlotBindings, 3)
	require.Equal(t, hubtypes.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_IDLE, hub.CandidatePoolBuildStatus.Status)
	require.Equal(t, uint64(3), hub.CandidatePoolBuildStatus.SourceRevision)
	for index, current := range hub.CandidateSlotCurrents {
		require.Equal(t, uint32(index), current.Slot)
		require.Equal(t, uint64(1), current.SlotVersion)
		require.Equal(t, hubtypes.CandidateSlotStatus_CANDIDATE_SLOT_STATUS_ALLOCATED, current.Status)
		require.Equal(t, current.OperatorAddress, hub.CandidateSlotBindings[index].OperatorAddress)
		require.Equal(t, current.Slot, hub.CandidateSlotBindings[index].Slot)
		require.Equal(t, current.SlotVersion, hub.CandidateSlotBindings[index].SlotVersion)
	}
	require.Len(t, hub.Models, 1)
	require.Equal(t, uint64(77), hub.Models[0].CreatedHeight)
	require.Equal(t, hubtypes.ModelStatusActive, hub.Models[0].Status)
	require.Len(t, hub.Profiles, 1)
	require.Equal(t, hubtypes.ModelStatusRegistered, hub.Profiles[0].Status)
	require.Equal(t, hubtypes.ModelRegistrationFeeMinMicroUSDC, hub.Models[0].RegistrationFeePaid)
	// TreasuryState is (balance, treasury_version) now;
	// total_collected and the per-epoch treasury ledger both left the wire
	// with TreasuryEpochState. The genesis seed credits the registration fee
	// into balance, so that is what this pins.
	require.Equal(t, shared.NewAmount(hubtypes.ModelRegistrationFeeMinMicroUSDC), hub.Treasury.Balance)
	require.Len(t, hub.ModelCapabilities, 3)
	require.Len(t, hub.ModelSupports, 3)
	require.Len(t, hub.DailySupports, 3)
	// The standalone ServiceKeyBinding collection is gone: the current online key
	// lives inline on CortexNodeState / BuilderState.
	for _, node := range hub.CortexNodes {
		require.NotEmpty(t, node.OperatorAddress)
		require.NotEmpty(t, node.CurrentServiceAddress)
		require.NotEmpty(t, node.CurrentServicePubkey)
		require.Equal(t, uint64(1), node.ServiceAuthorizationNonce)
		require.Equal(t, hubtypes.ServiceKeyStatusActive, node.ServiceKeyStatus)
		require.Equal(t, uint64(77), node.UpdatedHeight)
	}
	for _, builder := range hub.Builders {
		require.NotEmpty(t, builder.BuilderAddress)
		require.NotEmpty(t, builder.CurrentServiceAddress)
		require.NotEmpty(t, builder.CurrentServicePubkey)
		require.Equal(t, uint64(1), builder.ServiceAuthorizationNonce)
		require.Equal(t, hubtypes.ServiceKeyStatusActive, builder.CurrentServiceKeyStatus)
	}
	require.Len(t, hub.ServiceDescriptors, 6)
	for _, node := range hub.CortexNodes {
		require.Equal(t, uint64(1), node.CurrentDescriptorVersion)
	}
	require.NoError(t, hub.Validate())
	require.NoError(t, bank.Validate())
	require.NoError(t, authtypes.ValidateGenesis(auth))
	require.Equal(t, uint64(6_000_000), genesisBalanceAmount(
		bank,
		authtypes.NewModuleAddress(hubtypes.ServiceBondModuleName).String(),
	))
	require.Equal(t, accountBalance, genesisBalanceAmount(bank, firstBuilder))
	require.Equal(
		t,
		accountBalance-2_000_000-hubtypes.ModelRegistrationFeeMinMicroUSDC,
		genesisBalanceAmount(bank, firstCortexOperator),
	)
	require.Equal(t, hubtypes.ModelRegistrationFeeMinMicroUSDC, genesisBalanceAmount(
		bank,
		authtypes.NewModuleAddress(hubtypes.TreasuryModuleName).String(),
	))
	require.Equal(
		t,
		uint64(len(expectedAddresses))*accountBalance,
		bank.Supply.AmountOf(hubtypes.DefaultBusinessDenom).Uint64(),
	)
	accounts, err := authtypes.UnpackAccounts(auth.Accounts)
	require.NoError(t, err)
	actualAddresses := make(map[string]bool, len(accounts))
	for _, account := range accounts {
		actualAddresses[account.GetAddress().String()] = true
	}
	for _, address := range expectedAddresses {
		require.Truef(t, actualAddresses[address], "missing auth account %s", address)
		require.Positive(t, genesisBalanceAmount(bank, address))
	}
}

func genesisBalanceAmount(state banktypes.GenesisState, address string) uint64 {
	for _, balance := range state.Balances {
		if balance.Address == address {
			return balance.Coins.AmountOf(hubtypes.DefaultBusinessDenom).Uint64()
		}
	}
	return 0
}
