package keeper_test

import (
	"bytes"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// bridgeGenesisFixture builds a consistent bridge section: four signers with
// real PoPs (the §4.1 floor), a signer-set hash that is the projection of those
// rows, and limits inside the registered hard max.
func bridgeGenesisFixture(t *testing.T, chainID string) (types.BridgeGenesisV1, []types.ValidatorBridgeSignerState) {
	t.Helper()

	signers := make([]types.ValidatorBridgeSignerState, 0, 4)
	for i := 0; i < 4; i++ {
		operator := hubAddress(t, byte(0xb0+i))
		signers = append(signers, bridgeTestSigner(t, chainID, operator, byte(0x21+i), 1))
	}
	projection, err := types.BridgeSignerProjection(chainID, signers)
	require.NoError(t, err)

	route := types.BridgeRouteV1{
		HyperlaneLocalDomain:    424243,
		OriginDomain:            1,
		OriginTokenAddress:      bytes.Repeat([]byte{0x01}, 20),
		OriginWarpRouterAddress: bytes.Repeat([]byte{0x02}, 20),
		OriginMailboxAddress:    bytes.Repeat([]byte{0x03}, 20),
		OriginDecimals:          6,
		BusinessDenom:           types.DefaultHubParams().Phase0.BusinessDenom,
		LocalMailboxId:          bytes.Repeat([]byte{0x04}, 32),
		LocalWarpTokenId:        bytes.Repeat([]byte{0x05}, 32),
		LocalOriginDenom:        types.DefaultHubParams().Phase0.BusinessDenom,
	}
	routeID, err := types.USDCRouteID(chainID, route)
	require.NoError(t, err)
	route.UsdcRouteId = routeID[:]

	return types.BridgeGenesisV1{
		SchemaVersion:             1,
		Route:                     route,
		LocalIsmId:                bytes.Repeat([]byte{0x06}, 32),
		LocalSignerSetHash:        projection[:],
		ConfirmedEvmIsmAddress:    bytes.Repeat([]byte{0x07}, 20),
		ConfirmedEvmSignerSetHash: projection[:],
		Threshold:                 3,
		SignerCount:               4,
		Lifecycle:                 types.BridgeLifecycleV1_BRIDGE_LIFECYCLE_V1_ACTIVE,
		Frozen:                    false,
		DeploymentManifestHash:    bytes.Repeat([]byte{0x08}, 32),
		Limits: types.BridgeLimitGenesisV1{
			BridgeLimitHardMax:    types.DefaultHubParams().Bridge.BridgeLimitHardMax,
			InboundLimitPerEpoch:  shared.NewAmount(1_000),
			OutboundLimitPerEpoch: shared.NewAmount(2_000),
			EffectiveEpoch:        0,
		},
		Supply: types.BridgeSupplyGenesisV1{
			GenesisAllocated:       shared.NewAmount(0),
			CumulativeBridgeMinted: shared.NewAmount(0),
			CumulativeBridgeBurned: shared.NewAmount(0),
		},
		Bootstrap: types.BridgeBootstrapGenesisV1{
			Mode: types.BridgeBootstrapModeV1_BRIDGE_BOOTSTRAP_MODE_V1_DISABLED,
		},
	}, signers
}

func hubGenesisWithBridge(t *testing.T, chainID string) *types.GenesisState {
	t.Helper()
	genesis := hubGenesisWithIndexes()
	bridge, signers := bridgeGenesisFixture(t, chainID)
	genesis.Bridge = bridge
	genesis.ValidatorBridgeSigners = signers
	return genesis
}

func TestBridgeGenesisBindsCanonicalDenomAndInitialSupply(t *testing.T) {
	chainID := sdk.UnwrapSDKContext(initFixture(t).ctx).ChainID()

	wrongDenom := hubGenesisWithBridge(t, chainID)
	wrongDenom.Bridge.Route.BusinessDenom = "hyperlane/wrong"
	wrongDenom.Bridge.Route.LocalOriginDenom = "hyperlane/wrong"
	routeID, err := types.USDCRouteID(chainID, wrongDenom.Bridge.Route)
	require.NoError(t, err)
	wrongDenom.Bridge.Route.UsdcRouteId = routeID[:]
	f := initFixture(t)
	require.ErrorContains(t, f.keeper.InitGenesis(f.ctx, *wrongDenom), "does not equal Phase0Params business_denom")

	mismatchedSupply := hubGenesisWithBridge(t, chainID)
	mismatchedSupply.Bridge.Supply.GenesisAllocated = shared.NewAmount(10)
	f = initFixture(t)
	require.ErrorContains(t, f.keeper.InitGenesis(f.ctx, *mismatchedSupply), "bridge genesis supply invariant")

	validExport := hubGenesisWithBridge(t, chainID)
	validExport.Bridge.Supply.GenesisAllocated = shared.NewAmount(100)
	validExport.Bridge.Supply.CumulativeBridgeMinted = shared.NewAmount(20)
	validExport.Bridge.Supply.CumulativeBridgeBurned = shared.NewAmount(110)
	f = initFixture(t)
	f.bank.add(bridgeHolder, validExport.Bridge.Route.BusinessDenom, 10)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *validExport))
}

// keeper_data_structure_contract.md §6.6a requires every bridge row and the upstream module
// state to survive one export/import round trip with the same app hash and the
// same next-block behaviour. This is the module half of that: the exported
// document must be byte-identical to what was imported, and must re-import into
// a fresh store.
func TestBridgeGenesisSurvivesExportImportRoundTrip(t *testing.T) {
	f := initFixture(t)
	chainID := sdk.UnwrapSDKContext(f.ctx).ChainID()
	genesis := hubGenesisWithBridge(t, chainID)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	attachMatchingBridgeUpstream(t, f)

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(&genesis.Bridge, &exported.Bridge),
		"the exported bridge section must equal the imported one")
	require.Len(t, exported.ValidatorBridgeSigners, len(genesis.ValidatorBridgeSigners))

	restarted := initFixture(t)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	reexported, err := restarted.keeper.ExportGenesis(restarted.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(&exported.Bridge, &reexported.Bridge),
		"a second round trip must be a fixed point")
}

// §5.2 is explicit that export must not mistake the current supply for a new
// genesis allocation. A chain that bridged funds in and then exports must carry
// the three counters verbatim, so the importing chain still knows none of its
// supply was allocated outside the bridge.
func TestBridgeGenesisExportKeepsCumulativeCountersVerbatim(t *testing.T) {
	f := initFixture(t)
	chainID := sdk.UnwrapSDKContext(f.ctx).ChainID()
	genesis := hubGenesisWithBridge(t, chainID)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	attachMatchingBridgeUpstream(t, f)

	transfer := bridgeTransfer(keeper.BridgeInbound, 400, 0x11)
	transfer.Denom = genesis.Bridge.Route.BusinessDenom
	require.NoError(t, runBridgeTransfer(t, f, transfer))

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	minted, err := shared.ParseAmount(exported.Bridge.Supply.CumulativeBridgeMinted)
	require.NoError(t, err)
	require.Equal(t, uint64(400), minted)
	allocated, err := shared.ParseAmount(exported.Bridge.Supply.GenesisAllocated)
	require.NoError(t, err)
	require.Zero(t, allocated, "bridged supply must never be reclassified as a genesis allocation")

	// The current, still-open usage window belongs to this chain's clock and is
	// not exportable state (§6.6a).
	require.Empty(t, exported.Bridge.Usages)
}

// A document whose signer-set hash does not match its own signer rows would let
// a chain start with the bridge already out of step with its ValidatorSet, which
// I-BRIDGE-1 exists to make impossible.
func TestBridgeGenesisRejectsAnInconsistentSignerProjection(t *testing.T) {
	f := initFixture(t)
	chainID := sdk.UnwrapSDKContext(f.ctx).ChainID()

	genesis := hubGenesisWithBridge(t, chainID)
	genesis.Bridge.LocalSignerSetHash = bytes.Repeat([]byte{0x99}, 32)
	require.ErrorContains(t, f.keeper.InitGenesis(f.ctx, *genesis), "local_signer_set_hash")

	genesis = hubGenesisWithBridge(t, chainID)
	genesis.Bridge.ConfirmedEvmSignerSetHash = bytes.Repeat([]byte{0x99}, 32)
	require.ErrorContains(t, f.keeper.InitGenesis(f.ctx, *genesis), "I-BRIDGE-1")

	genesis = hubGenesisWithBridge(t, chainID)
	genesis.ValidatorBridgeSigners = genesis.ValidatorBridgeSigners[:3]
	genesis.Bridge.SignerCount = 3
	genesis.Bridge.Threshold = 2
	require.ErrorContains(t, f.keeper.InitGenesis(f.ctx, *genesis), "at least 4 signers")

	// A PoP that recovers to someone else proves nothing about the declared key.
	genesis = hubGenesisWithBridge(t, chainID)
	genesis.ValidatorBridgeSigners[0].BridgeSignerAddressRaw20 = bytes.Repeat([]byte{0x5a}, 20)
	require.ErrorContains(t, f.keeper.InitGenesis(f.ctx, *genesis), "PoP")
}

// §2.1: the route id is the hash of its own eleven fields, so a document that
// carries a stale id after editing the route must not start.
func TestBridgeGenesisRejectsARouteThatDoesNotHashToItsOwnID(t *testing.T) {
	f := initFixture(t)
	chainID := sdk.UnwrapSDKContext(f.ctx).ChainID()

	genesis := hubGenesisWithBridge(t, chainID)
	genesis.Bridge.Route.OriginDecimals = 8
	require.ErrorContains(t, f.keeper.InitGenesis(f.ctx, *genesis), "usdc_route_id")

	// The upstream identifiers must be real HexAddresses or the guard could never
	// match them against the live Mailbox and token (DOC-012).
	genesis = hubGenesisWithBridge(t, chainID)
	genesis.Bridge.Route.LocalMailboxId = bytes.Repeat([]byte{0x04}, 20)
	routeID, err := types.USDCRouteID(chainID, genesis.Bridge.Route)
	require.NoError(t, err)
	genesis.Bridge.Route.UsdcRouteId = routeID[:]
	require.ErrorContains(t, f.keeper.InitGenesis(f.ctx, *genesis), "local_mailbox_id")
}

// §7.2: a limit of zero does not mean "unlimited"; it would remove the only cap
// on what a stolen signer threshold can mint.
func TestBridgeGenesisRejectsZeroAndOversizedLimits(t *testing.T) {
	f := initFixture(t)
	chainID := sdk.UnwrapSDKContext(f.ctx).ChainID()

	genesis := hubGenesisWithBridge(t, chainID)
	genesis.Bridge.Limits.InboundLimitPerEpoch = shared.NewAmount(0)
	require.ErrorContains(t, f.keeper.InitGenesis(f.ctx, *genesis), "0 does not mean unlimited")

	genesis = hubGenesisWithBridge(t, chainID)
	hardMax, err := shared.ParseAmount(genesis.Bridge.Limits.BridgeLimitHardMax)
	require.NoError(t, err)
	genesis.Bridge.Limits.OutboundLimitPerEpoch = shared.NewAmount(hardMax + 1)
	require.ErrorContains(t, f.keeper.InitGenesis(f.ctx, *genesis), "exceeds bridge_limit_hard_max")
}

// §5.3: the bootstrap binding fields are all-or-nothing, and CONSUMED keeps its
// height. A partially bound ARMED bootstrap would let an arbitrary first message
// take the fee exemption.
func TestBridgeGenesisRejectsAPartiallyBoundBootstrap(t *testing.T) {
	f := initFixture(t)
	chainID := sdk.UnwrapSDKContext(f.ctx).ChainID()

	genesis := hubGenesisWithBridge(t, chainID)
	genesis.Bridge.Bootstrap = types.BridgeBootstrapGenesisV1{
		Mode: types.BridgeBootstrapModeV1_BRIDGE_BOOTSTRAP_MODE_V1_ARMED,
		XMessageId: &types.BridgeBootstrapGenesisV1_MessageId{
			MessageId: bytes.Repeat([]byte{0x77}, 32),
		},
	}
	require.ErrorContains(t, f.keeper.InitGenesis(f.ctx, *genesis), "all six binding fields")

	genesis = hubGenesisWithBridge(t, chainID)
	genesis.Bridge.Bootstrap = types.BridgeBootstrapGenesisV1{
		Mode:            types.BridgeBootstrapModeV1_BRIDGE_BOOTSTRAP_MODE_V1_DISABLED,
		XConsumedHeight: &types.BridgeBootstrapGenesisV1_ConsumedHeight{ConsumedHeight: 5},
	}
	require.ErrorContains(t, f.keeper.InitGenesis(f.ctx, *genesis), "DISABLED bootstrap must carry no binding fields")
}

// A chain with no bridge section imports cleanly and exports nothing, so every
// existing fixture and a bridge-free devnet keep working.
func TestHubGenesisWithoutABridgeSectionIsAccepted(t *testing.T) {
	f := initFixture(t)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *hubGenesisWithIndexes()))

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.Zero(t, exported.Bridge.SchemaVersion)
	require.Empty(t, exported.ValidatorBridgeSigners)

	// Signers without a bridge would describe an ISM projection nothing consumes.
	genesis := hubGenesisWithIndexes()
	_, signers := bridgeGenesisFixture(t, sdk.UnwrapSDKContext(f.ctx).ChainID())
	genesis.ValidatorBridgeSigners = signers
	restarted := initFixture(t)
	require.ErrorContains(t, restarted.keeper.InitGenesis(restarted.ctx, *genesis),
		"validator_bridge_signers present without a bridge section")
}
