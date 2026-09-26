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
// Ruling 16 deleted the duplicated keeper.MinServiceBond constant (500_000) and made
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
			SupportedProfiles: []genesisSeedProfileSupport{{
				ModelID: "local-inference-model", ProfileVersion: 1, Active: true,
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
	require.Equal(t, "hf-ad410b3157d13dbfb8263e92914cfe5a75868ce68fd722d2f73c75ff8cc7378b", model.Profile.ModelID)
	require.Equal(t, uint32(1), model.Profile.ProfileVersion)
	require.Equal(t, []string{"TEXT_GENERATION", "CHAT"}, model.Profile.TaskTypes)
	require.Equal(t, "uusdc", model.Profile.MinStake.Denom)
	require.Equal(t, uint64(1_000_000), model.Profile.MinStake.Amount)
	require.Equal(t, "uusdc", model.Profile.RegistrationFee.Denom)
	require.Equal(t, uint64(1_000_000), model.Profile.RegistrationFee.Amount)
	require.Equal(t, seed.CortexNodes[0].OperatorAddress, model.ProposerAddress)
	operators := make(map[string]struct{}, len(seed.CortexNodes))
	serviceAddresses := make(map[string]struct{}, len(seed.CortexNodes))
	for _, cortex := range seed.CortexNodes {
		operators[cortex.OperatorAddress] = struct{}{}
		serviceAddresses[cortex.ServiceAddress] = struct{}{}
		require.Nil(t, cortex.Descriptor)
		require.Equal(t, []genesisSeedProfileSupport{{ModelID: model.Profile.ModelID, ProfileVersion: 1, Active: true}}, cortex.SupportedProfiles)
	}
	require.Len(t, operators, 4)
	require.Len(t, serviceAddresses, 4)
	interfaceRegistry := codectypes.NewInterfaceRegistry()
	cdc := codec.NewProtoCodec(interfaceRegistry)
	hubParams := hubtypes.DefaultHubParams()
	require.NoError(t, applyHubParamsOverride(cdc, &hubParams, seed.HubParams))
	// 14400 blocks is four hours under FAST_BLOCKS. The seed previously carried
	// 60_480_000 - seven hundred days - which meant the epoch counter never
	// advanced on a localnet, and a freshly registered cortex could never join.
	// StakeService sets effective_active_bond to zero and effective_bond_epoch to
	// current_epoch + ServiceBondEffectiveEpochDelay, and candidate eligibility
	// reads effective_active_bond, so without a rollover a new operator stays at
	// zero bond forever however much it staked.
	//
	// Shorter would onboard faster - the wait is uniform on [0, one epoch] - but
	// the epoch-denominated retentions are all 30 epochs (builder set, candidate
	// pool header, slot binding, vrf key history, daily support, support row,
	// reward audit). Four hours puts those at five days, which outlives a
	// debugging session; an hour would put them at thirty.
	require.Equal(t, uint64(14_400), hubParams.Epoch.EpochLengthBlocks)
	require.Equal(t, uint64(5), hubParams.Epoch.DeltaWBlocks)
	require.Equal(t, uint64(5), hubParams.Builder.AssignmentBuilderProposalWindowBlocks)
	require.Equal(t, uint64(5), hubParams.Builder.OpenVerifyBuilderProposalWindowBlocks)
	// Freshness is epoch-based now; daily_support_window_blocks is gone.
	//
	// A rolling epoch also switches support expiry on, and support_fresh_until is
	// current_epoch + support_window_epochs. At the protocol value of 2 that is
	// eight hours, after which every support row goes stale and the network stops
	// admitting tasks unless something keeps calling MsgBatchConfirmModelSupport.
	// Trading "a new operator can never join" for "every operator drops out
	// overnight" is not a trade a debugging network wants, so the seed widens the
	// window to 30 days.
	//
	// This one is a governance parameter rather than genesis-only, so narrowing it
	// back to exercise the expiry path costs a proposal rather than a fresh
	// genesis - both behaviours are reachable on the same chain.
	require.Equal(t, uint32(180), hubParams.Support.SupportWindowEpochs)
	require.Equal(t, uint32(1024), hubParams.Reward.MaxRewardEpochItemsPerBlock)
	require.Equal(t, uint32(32), hubParams.Support.MaxSupportExpiryItemsPerBlock)
	requireGenesisSeedListsAllParams(t, cdc, &hubParams, seed.HubParams)
	taskParams := tasktypes.DefaultTaskParams()
	require.NoError(t, applyTaskParamsOverride(cdc, &taskParams, seed.TaskParams))
	// Localnet runs with FAST_BLOCKS at roughly one second per block, where the
	// former 8-block commit and reveal windows were under two seconds of wall
	// clock. A cortex paused on a breakpoint missed them every time, and Ruling 4
	// makes a Verifier miss an immediate jail, so debugging the node reliably
	// jailed it.
	//
	// Every fault x/task can raise is a missed deadline — worker_infer_
	// timeout, verifier_miss, commit_no_result — so wall clock on a breakpoint is
	// the whole exposure. The windows are not interchangeable, though, and the
	// budget belongs on the ones that are free:
	//
	//   - commit_window is a pure upper bound. verification_runtime.go opens the
	//     reveal phase on REVEAL_PHASE_TRIGGER_ALL_COMMITS the moment the last
	//     commit lands, so 100000 blocks — a day under FAST_BLOCKS — costs a
	//     healthy task nothing. It is also where a breakpoint actually lands,
	//     since committing is what runs verification.
	//   - verify_open_deadline is the one window where a longer value is strictly
	//     worse, and it used to be justified here as "an upper bound, widening it
	//     buys more handraise retries". It buys none. freezeVerifierWindowClock
	//     freezes the whole verifier clock off the receipt height, and selection
	//     runs once, at handraise_close + delta_w. If fewer than
	//     selected_verifier_count cortexes raised a hand by then, verifier_finalize
	//     _runtime.go drops the queue key outright — "the union cannot grow after
	//     the close height" — and the task just sits there until
	//     receipt + verify_open_deadline finally fails it as
	//     TASK_FAILURE_CLASS_INSUFFICIENT_VERIFIER and refunds.
	//
	//     So verify_open_deadline is not tolerance, it is how long a task that has
	//     ALREADY failed takes to say so. At 150000 that was 41 hours of a hung
	//     task per missed handraise, on a chain whose whole point is that a
	//     breakpoint is cheap. 300 blocks is five minutes to the refund and still
	//     twelve times the receipt+45 height selection actually needs, which is
	//     ample against a deadline-sweep backlog (verifyDeadlineRetryBlocksV1 is 1,
	//     so a capped sweep retries on the very next block).
	//
	//     The tolerance that a missed handraise actually needs goes into
	//     self_rescue_margin_blocks instead — see the selection bound below.
	//   - reveal_window used to be treated here as the one window that is NOT an
	//     upper bound, on the grounds that settlement refuses any height <= the
	//     reveal deadline. That branch does not exist: loadSettlementInputs gates
	//     settlement on the challenge close height
	//     (msg_server_settlement.go: "round 1 challenge window is still open"),
	//     and the remote devnet confirmed it — round 1 closed at 16457 with a
	//     reveal deadline of 16575, yet no MsgSettleTask could succeed before
	//     18257 = 16457 + challenge_open_window_blocks. reveal_window is an upper
	//     bound exactly like commit_window: applyVerifyResult closes the round the
	//     moment the last reveal lands, so a healthy task never waits it out.
	//
	//     What it actually buys is tolerance. planVerifyResult hard-rejects a
	//     reveal past the deadline ("reveal deadline has passed"), and
	//     handleExpiredRevealDeadline does not close the round — it only moves the
	//     index to verify_deadline_height. So a verifier that overruns by one
	//     block can never close the round, and the task hangs until
	//     open_verify_height + collection_window. At 120 blocks that turned a
	//     two-minute overrun into a day of waiting, which is the exact opposite of
	//     what a debug chain wants. Matching commit_window at 100000 lets the slow
	//     verifier still reveal and close the round immediately.
	//
	//     The one coupling that remains is authorizeSettlementSubmitter: below the
	//     reveal deadline, settlementBuilderSchedule returns rank 0 and is not yet
	//     permissionless, so only Builder rank 0 may submit MsgSettleTask. That is
	//     the frozen rank duty doing its job, and it costs nothing: if rank 0 stays
	//     silent, processExpiredSettlementDeadlines settles the task anyway at
	//     rounds_closed_height + settle_margin_blocks.
	//
	// collection_window must cover commit + reveal + settle_margin, and the whole
	// chain has to stay under session_close_ttl_blocks, which is why collection is
	// 200200 and the TTL had to move with it. ValidateSessionCloseHorizon is
	// asserted directly below: it runs in InitGenesis rather than in Validate, so
	// without that assertion a bad horizon here would only surface as a panic
	// inside the lifecycle integration tests. These windows are the debug chain's
	// only; DefaultTaskParams is untouched.
	require.Equal(t, uint64(300), taskParams.Deadlines.VerifyOpenDeadlineBlocks)
	require.Equal(t, uint64(100000), taskParams.Deadlines.CommitWindowBlocks)
	require.Equal(t, uint64(100000), taskParams.Deadlines.RevealWindowBlocks)
	require.Equal(t, uint64(200200), taskParams.Deadlines.CollectionWindowBlocks)
	require.Equal(t, uint64(20), taskParams.Deadlines.SettleMarginBlocks)
	require.Equal(t, uint64(30), taskParams.Deadlines.SelfRescueMarginBlocks)
	require.NoError(t, tasktypes.ValidateVerifyOpenClock(
		taskParams.Deadlines,
		hubParams.Epoch.DeltaWBlocks,
		hubParams.Builder.OpenVerifyBuilderProposalWindowBlocks,
	))
	require.NoError(t, tasktypes.ValidateSessionCloseHorizon(
		taskParams, hubtypes.ProfileChallengeOpenWindowMaxBlocks,
	), "widening a deadline window without moving session_close_ttl_blocks would panic InitGenesis")
	workerSelectionBlocks := hubParams.Builder.AssignmentBuilderProposalWindowBlocks + hubParams.Epoch.DeltaWBlocks
	verifierSelectionBlocks := 2*hubParams.Epoch.DeltaWBlocks +
		hubParams.Builder.OpenVerifyBuilderProposalWindowBlocks +
		taskParams.Deadlines.SelfRescueMarginBlocks
	// These two spans are the only mandatory waits on the happy path besides the
	// challenge window: both selections run once, at a height frozen off the
	// receipt, so every block added here is a block added to every task that
	// succeeds. The exchange rate for self_rescue_margin_blocks is exactly 1:1.
	//
	// It is still worth paying. The verifier handraise window is the one place in
	// the lifecycle with no second chance at all — miss it and the task is dead,
	// per the "union cannot grow" comment quoted above — while commit and reveal
	// each tolerate a full day. 10 blocks of margin made that window 25 blocks,
	// twenty-five seconds under FAST_BLOCKS, for the one deadline a cortex on a
	// breakpoint cannot recover from. 30 makes it 45, and the whole selection
	// budget 55 blocks, under a minute, against a challenge window of 300.
	//
	// The bound below was 36 with the rationale "three minutes at five seconds per
	// block". FAST_BLOCKS is one second per block, so it was really enforcing
	// thirty-six seconds. 60 is the same intent restated in the units the debug
	// chain actually runs at: selection must stay inside a minute.
	require.LessOrEqual(t, workerSelectionBlocks+verifierSelectionBlocks, uint64(60),
		"Worker and Verifier selection are mandatory waits on every successful task and must stay inside a minute at FAST_BLOCKS")
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
	require.Len(t, hub.ProfileCapabilities, 4)
	require.Len(t, hub.ModelSupports, 4)
	require.Len(t, hub.DailySupports, 4)
	require.Len(t, hub.Models, 1)
	require.Len(t, hub.Profiles, 1)
	require.Equal(t, hubtypes.ModelStatusActive, hub.Models[0].Status)
	require.Equal(t, hubtypes.ModelStatusActive, hub.Profiles[0].Status)
	require.Equal(t, []shared.TaskType{
		shared.TaskType_TASK_TYPE_TEXT_GENERATION,
		shared.TaskType_TASK_TYPE_CHAT,
	}, hub.Profiles[0].TaskTypes)
	require.Equal(t, uint32(4), hub.Profiles[0].ActiveSupporterCount)
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
			MinStake: genesisSeedCoin{Amount: genesisSeedTestServiceBondMinInitial, Denom: hubtypes.DefaultBusinessDenom},
			ModelID:  "local-inference-model", PricingProfile: genesisSeedPricingProfile{
				InitialOutputPrice: 1_000, MinOrderValue: 1_000, VerifyRatioBps: 1_000,
			},
			ProfileVersion: 1, RegistrationFee: genesisSeedCoin{Amount: 1_000_000, Denom: hubtypes.DefaultBusinessDenom},
			RequiredTopK: 20, ResourceTier: 1, RuntimeClass: "CAUSAL_LM_PREFILL_LOGPROBS_V1",
			SchemaHash: nonZeroHash, TaskTypes: []string{"TEXT_GENERATION", "CHAT"}, TokenizerHash: nonZeroHash,
			VerificationProfile: genesisSeedVerificationProfile{
				CanonicalEncodingVersion: "CANONICAL_OUTPUT_TEXT_V1",
				EvidenceSchema: genesisSeedEvidenceSchema{
					SchemaVersion: 1,
					RequiredInferEvidence: []genesisSeedEvidenceRequirement{{
						EvidenceKind: "WORKER_VALUE_OPENING", CommitmentSchemaVersion: 2, MaxEncodedSizeBytes: 1 << 30,
					}},
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
	require.Equal(t, hubtypes.ModelStatusActive, hub.Profiles[0].Status)
	require.Equal(t, hubtypes.ModelRegistrationFeeMinMicroUSDC, hub.Models[0].RegistrationFeePaid)
	// TreasuryState is (balance, treasury_version) now;
	// total_collected and the per-epoch treasury ledger both left the wire
	// with TreasuryEpochState. The genesis seed credits the registration fee
	// into balance, so that is what this pins.
	require.Equal(t, shared.NewAmount(hubtypes.ModelRegistrationFeeMinMicroUSDC), hub.Treasury.Balance)
	require.Len(t, hub.ProfileCapabilities, 3)
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
