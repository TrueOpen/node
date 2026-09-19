package keeper_test

import (
	"bytes"
	"testing"

	dcrsecp256k1 "github.com/decred/dcrd/dcrec/secp256k1/v4"
	dcrecdsa "github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

const bridgeTestDenom = "hyperlane/0x4444444444444444444444444444444444444444"

// bridgeTestSigner builds one Validator bridge signer row with a real PoP, so
// the guard's I-BRIDGE-1 recomputation exercises signature recovery rather than
// a placeholder.
func bridgeTestSigner(t *testing.T, chainID, operator string, seed byte, keyVersion uint64) types.ValidatorBridgeSignerState {
	t.Helper()
	privateKey := dcrsecp256k1.PrivKeyFromBytes(bytes.Repeat([]byte{seed}, 32))
	address := shared.EVMAddressFromSecp256k1PublicKey(privateKey.PubKey())
	digest, err := types.BridgeSignerPoPDigest(chainID, operator, address[:], keyVersion)
	require.NoError(t, err)
	compact := dcrecdsa.SignCompact(privateKey, digest[:], false)
	signature := make([]byte, 65)
	copy(signature, compact[1:])
	signature[64] = compact[0]
	return types.ValidatorBridgeSignerState{
		OperatorAddress: operator, BridgeSignerAddressRaw20: address[:],
		KeyVersion: keyVersion, PopSignature: signature, RegisteredHeight: 1,
	}
}

// installBridge writes a minimal but fully consistent ACTIVE bridge: four
// signers (the §4.1 floor), a projection-matching signer set, positive limits
// and zero supply.
func installBridge(t *testing.T, f *fixture, inboundLimit, outboundLimit uint64) {
	t.Helper()
	chainID := sdk.UnwrapSDKContext(f.ctx).ChainID()
	require.NoError(t, f.keeper.Params.Set(f.ctx, types.DefaultHubParams()))

	signers := make([]types.ValidatorBridgeSignerState, 0, 4)
	for i := 0; i < 4; i++ {
		operator := hubAddress(t, byte(0xb0+i))
		signer := bridgeTestSigner(t, chainID, operator, byte(0x21+i), 1)
		signers = append(signers, signer)
		require.NoError(t, f.keeper.ValidatorBridgeSigner.Set(f.ctx, operator, signer))
	}
	projection, err := types.BridgeSignerProjection(chainID, signers)
	require.NoError(t, err)

	route := types.BridgeRouteState{
		HyperlaneLocalDomain: 424243, OriginDomain: 1,
		OriginTokenAddress:      bytes.Repeat([]byte{0x01}, 20),
		OriginWarpRouterAddress: bytes.Repeat([]byte{0x02}, 20),
		OriginMailboxAddress:    bytes.Repeat([]byte{0x03}, 20),
		OriginDecimals:          6, BusinessDenom: bridgeTestDenom,
		LocalMailboxId:   bytes.Repeat([]byte{0x04}, 32),
		LocalWarpTokenId: bytes.Repeat([]byte{0x05}, 32),
		LocalOriginDenom: bridgeTestDenom,
	}
	routeID, err := types.USDCRouteID(chainID, types.BridgeRouteV1{
		HyperlaneLocalDomain: route.HyperlaneLocalDomain, OriginDomain: route.OriginDomain,
		OriginTokenAddress: route.OriginTokenAddress, OriginWarpRouterAddress: route.OriginWarpRouterAddress,
		OriginMailboxAddress: route.OriginMailboxAddress, OriginDecimals: route.OriginDecimals,
		BusinessDenom: route.BusinessDenom, LocalMailboxId: route.LocalMailboxId,
		LocalWarpTokenId: route.LocalWarpTokenId, LocalOriginDenom: route.LocalOriginDenom,
	})
	require.NoError(t, err)
	route.UsdcRouteId = routeID[:]
	require.NoError(t, f.keeper.BridgeRoute.Set(f.ctx, route))

	require.NoError(t, f.keeper.BridgeSignerSet.Set(f.ctx, types.BridgeSignerSetState{
		LocalIsmId: bytes.Repeat([]byte{0x06}, 32), LocalSignerSetHash: projection[:],
		ConfirmedEvmIsmAddress: bytes.Repeat([]byte{0x07}, 20), ConfirmedEvmSignerSetHash: projection[:],
		Threshold: 3, SignerCount: 4, UpdatedHeight: 1,
	}))
	require.NoError(t, f.keeper.BridgeControl.Set(f.ctx, types.BridgeControlState{
		Lifecycle: types.BridgeLifecycleV1_BRIDGE_LIFECYCLE_V1_ACTIVE, Frozen: false,
		DeploymentManifestHash: bytes.Repeat([]byte{0x08}, 32), UpdatedHeight: 1,
	}))
	require.NoError(t, f.keeper.BridgeLimit.Set(f.ctx, types.BridgeLimitState{
		BridgeLimitHardMax:   shared.NewAmount(1_000_000_000),
		InboundLimitPerEpoch: shared.NewAmount(inboundLimit), OutboundLimitPerEpoch: shared.NewAmount(outboundLimit),
		EffectiveEpoch: 0,
	}))
	require.NoError(t, f.keeper.BridgeSupply.Set(f.ctx, types.BridgeSupplyState{
		GenesisAllocated: shared.NewAmount(0), CumulativeBridgeMinted: shared.NewAmount(0),
		CumulativeBridgeBurned: shared.NewAmount(0),
	}))
	require.NoError(t, f.keeper.BridgeBootstrap.Set(f.ctx, types.BridgeBootstrapState{
		Mode: types.BridgeBootstrapModeV1_BRIDGE_BOOTSTRAP_MODE_V1_DISABLED,
	}))
	attachMatchingBridgeUpstream(t, f)
}

func attachMatchingBridgeUpstream(t *testing.T, f *fixture) {
	t.Helper()
	route, err := f.keeper.BridgeRoute.Get(f.ctx)
	require.NoError(t, err)
	signerSet, err := f.keeper.BridgeSignerSet.Get(f.ctx)
	require.NoError(t, err)
	gov := sdk.AccAddress(authtypes.NewModuleAddress(types.GovModuleName)).String()
	_ = f.keeper.WithBridgeUpstream(matchingBridgeUpstream{
		mailbox: types.BridgeUpstreamMailbox{
			LocalDomain: route.HyperlaneLocalDomain,
			DefaultIsm:  signerSet.LocalIsmId,
			Owner:       gov,
		},
		token: types.BridgeUpstreamToken{
			Synthetic: true, OriginMailbox: route.LocalMailboxId,
			OriginDenom: route.BusinessDenom, Owner: gov,
		},
		router: types.BridgeUpstreamRemoteRouter{
			DestinationDomain: route.OriginDomain,
			ReceiverContract:  route.OriginWarpRouterAddress,
		},
	})
}

// runBridgeTransfer mirrors the production bracket exactly: the guard refuses
// before the ledger moves, the ledger then moves, and only then is the delta
// asserted and recorded. Tests that pre-credited the bank and called a single
// combined entry point would hide the ordering the guard depends on.
func runBridgeTransfer(t *testing.T, f *fixture, transfer keeper.BridgeTransferContext) error {
	t.Helper()
	route, err := f.keeper.PrepareBridgeTransfer(f.ctx, transfer)
	if err != nil {
		return err
	}
	before := f.keeper.BusinessDenomSupply(f.ctx, route.BusinessDenom)
	if transfer.Direction == keeper.BridgeInbound {
		f.bank.add(bridgeHolder, transfer.Denom, transfer.Amount)
	} else {
		f.bank.burn(bridgeHolder, transfer.Denom, transfer.Amount)
	}
	return f.keeper.SettleBridgeTransfer(f.ctx, route, transfer, before)
}

const bridgeHolder = "bridge-holder"

func bridgeTransfer(direction keeper.BridgeDirection, amount uint64, id byte) keeper.BridgeTransferContext {
	return keeper.BridgeTransferContext{
		Direction: direction, Denom: bridgeTestDenom, Amount: amount,
		MessageID: bytes.Repeat([]byte{id}, 32), Counterpart: "trueopen1recipient",
	}
}

// The guard must move the epoch usage and the cumulative supply counter together
// with the ledger, and I-BRIDGE-2 must hold against the bank afterwards.
func TestBridgeInboundChargesUsageSupplyAndInvariant(t *testing.T) {
	f := initFixture(t)
	installBridge(t, f, 1_000, 1_000)

	require.NoError(t, runBridgeTransfer(t, f, bridgeTransfer(keeper.BridgeInbound, 400, 0x11)))

	epoch, err := f.keeper.BridgeEpochUsage.Get(f.ctx, 0)
	require.NoError(t, err)
	used, err := shared.ParseAmount(epoch.InboundUsed)
	require.NoError(t, err)
	require.Equal(t, uint64(400), used)

	supply, err := f.keeper.BridgeSupply.Get(f.ctx)
	require.NoError(t, err)
	minted, err := shared.ParseAmount(supply.CumulativeBridgeMinted)
	require.NoError(t, err)
	require.Equal(t, uint64(400), minted)

	require.NoError(t, f.keeper.EnsureBridgeSupplyInvariant(f.ctx))
}

// §7.2: an over-limit transfer is rejected whole. Nothing may be partially
// charged, because a partial mint would leave the two sides of the bridge
// describing different amounts.
func TestBridgeRejectsOverLimitTransferWhole(t *testing.T) {
	f := initFixture(t)
	installBridge(t, f, 500, 500)

	require.NoError(t, runBridgeTransfer(t, f, bridgeTransfer(keeper.BridgeInbound, 300, 0x11)))

	err := runBridgeTransfer(t, f, bridgeTransfer(keeper.BridgeInbound, 300, 0x12))
	require.ErrorContains(t, err, "rejected whole")

	usage, err2 := f.keeper.BridgeEpochUsage.Get(f.ctx, 0)
	require.NoError(t, err2)
	used, err2 := shared.ParseAmount(usage.InboundUsed)
	require.NoError(t, err2)
	require.Equal(t, uint64(300), used, "a refused transfer must leave usage untouched")

	supply, err2 := f.keeper.BridgeSupply.Get(f.ctx)
	require.NoError(t, err2)
	minted, err2 := shared.ParseAmount(supply.CumulativeBridgeMinted)
	require.NoError(t, err2)
	require.Equal(t, uint64(300), minted, "a refused transfer must not accrue supply")

	// Exactly at the limit is allowed; one above is not.
	require.NoError(t, runBridgeTransfer(t, f, bridgeTransfer(keeper.BridgeInbound, 200, 0x13)))
	require.ErrorContains(t, runBridgeTransfer(t, f, bridgeTransfer(keeper.BridgeInbound, 1, 0x14)), "rejected whole")
}

// §5.1: only business_denom crosses the bridge. Module Minter permission is not
// denom-scoped, so this check is the closed set.
func TestBridgeRefusesForeignDenom(t *testing.T) {
	f := initFixture(t)
	installBridge(t, f, 1_000, 1_000)

	transfer := bridgeTransfer(keeper.BridgeInbound, 10, 0x11)
	transfer.Denom = "uatom"
	require.ErrorContains(t, runBridgeTransfer(t, f, transfer), "may only mint or burn")
}

// §7.1 and §7.3: a frozen or non-ACTIVE bridge refuses both directions, and the
// refusal is total rather than direction-specific.
func TestBridgeRefusesWhenFrozenOrNotActive(t *testing.T) {
	f := initFixture(t)
	installBridge(t, f, 1_000, 1_000)

	control, err := f.keeper.BridgeControl.Get(f.ctx)
	require.NoError(t, err)
	control.Frozen = true
	require.NoError(t, f.keeper.BridgeControl.Set(f.ctx, control))
	require.ErrorContains(t, runBridgeTransfer(t, f, bridgeTransfer(keeper.BridgeInbound, 1, 0x11)), "frozen")
	require.ErrorContains(t, runBridgeTransfer(t, f, bridgeTransfer(keeper.BridgeOutbound, 1, 0x12)), "frozen")

	control.Frozen = false
	control.Lifecycle = types.BridgeLifecycleV1_BRIDGE_LIFECYCLE_V1_PENDING_EVM_CONFIRMATION
	require.NoError(t, f.keeper.BridgeControl.Set(f.ctx, control))
	require.ErrorContains(t, runBridgeTransfer(t, f, bridgeTransfer(keeper.BridgeInbound, 1, 0x13)), "not ACTIVE")
}

// I-BRIDGE-1: the guard recomputes the signer projection instead of trusting the
// cached hash, so a Validator bridge signer change that never reached the ISM
// freezes the bridge rather than being silently accepted.
func TestBridgeFreezesWhenSignerProjectionDrifts(t *testing.T) {
	f := initFixture(t)
	installBridge(t, f, 1_000, 1_000)

	extra := hubAddress(t, 0xc9)
	require.NoError(t, f.keeper.ValidatorBridgeSigner.Set(f.ctx, extra,
		bridgeTestSigner(t, sdk.UnwrapSDKContext(f.ctx).ChainID(), extra, 0x31, 1)))

	require.ErrorContains(t, runBridgeTransfer(t, f, bridgeTransfer(keeper.BridgeInbound, 1, 0x11)), "I-BRIDGE-1")
}

func TestBridgeLiveUpstreamDriftBlocksTransferQueryAndUnfreeze(t *testing.T) {
	f := initFixture(t)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(1))
	installBridge(t, f, 1_000, 1_000)
	route, err := f.keeper.BridgeRoute.Get(f.ctx)
	require.NoError(t, err)
	gov := sdk.AccAddress(authtypes.NewModuleAddress(types.GovModuleName)).String()
	_ = f.keeper.WithBridgeUpstream(matchingBridgeUpstream{
		mailbox: types.BridgeUpstreamMailbox{
			LocalDomain: route.HyperlaneLocalDomain,
			DefaultIsm:  bytes.Repeat([]byte{0xff}, 32),
			Owner:       gov,
		},
		token: types.BridgeUpstreamToken{
			Synthetic: true, OriginMailbox: route.LocalMailboxId,
			OriginDenom: route.BusinessDenom, Owner: gov,
		},
		router: types.BridgeUpstreamRemoteRouter{
			DestinationDomain: route.OriginDomain,
			ReceiverContract:  route.OriginWarpRouterAddress,
		},
	})

	_, err = f.keeper.PrepareBridgeTransfer(f.ctx, bridgeTransfer(keeper.BridgeInbound, 1, 0x11))
	require.ErrorContains(t, err, "default ISM")

	query := keeper.NewQueryServerImpl(f.keeper)
	_, err = query.BridgeStatus(f.ctx, &types.QueryBridgeStatusRequest{})
	require.Equal(t, codes.FailedPrecondition, grpcstatus.Code(err))

	control, err := f.keeper.BridgeControl.Get(f.ctx)
	require.NoError(t, err)
	control.Frozen = true
	require.NoError(t, f.keeper.BridgeControl.Set(f.ctx, control))
	err = f.keeper.ExecuteSetBridgeFreezeV1(f.ctx, keeper.AcceptedGovernanceActionContext{
		AuthorityAddress: sdk.AccAddress(f.keeper.GetAuthority()).String(),
		ProposalID:       7,
		Accepted:         true,
	}, types.SetBridgeFreezeV1{
		ProposalId: 7, Frozen: false,
		ExpectedLifecycle: types.BridgeLifecycleV1_BRIDGE_LIFECYCLE_V1_ACTIVE,
	})
	require.ErrorContains(t, err, "live Hyperlane state")
}

// §4.1: below four signers the bridge does not open at all.
func TestBridgeRefusesBelowFourSigners(t *testing.T) {
	f := initFixture(t)
	installBridge(t, f, 1_000, 1_000)

	signerSet, err := f.keeper.BridgeSignerSet.Get(f.ctx)
	require.NoError(t, err)
	signerSet.SignerCount = 3
	signerSet.Threshold = 2
	require.NoError(t, f.keeper.BridgeSignerSet.Set(f.ctx, signerSet))

	require.ErrorContains(t, runBridgeTransfer(t, f, bridgeTransfer(keeper.BridgeInbound, 1, 0x11)), "at least 4 signers")
}

// §5.2: the chain may never burn more than it minted, and the counters are the
// authority rather than the current balance.
func TestBridgeOutboundCannotBurnMoreThanMinted(t *testing.T) {
	f := initFixture(t)
	installBridge(t, f, 1_000, 1_000)

	require.NoError(t, runBridgeTransfer(t, f, bridgeTransfer(keeper.BridgeInbound, 100, 0x11)))
	require.NoError(t, runBridgeTransfer(t, f, bridgeTransfer(keeper.BridgeOutbound, 100, 0x12)))

	// With the vouchers already burned there is no supply left, so the ledger
	// bracket refuses first. The cumulative counters carry the same rule for the
	// case where a balance exists but was never bridged in.
	require.ErrorContains(t, runBridgeTransfer(t, f, bridgeTransfer(keeper.BridgeOutbound, 1, 0x13)), "exceeds the")

	f.bank.add(bridgeHolder, bridgeTestDenom, 1)
	transfer := bridgeTransfer(keeper.BridgeOutbound, 1, 0x14)
	route, err := f.keeper.PrepareBridgeTransfer(f.ctx, transfer)
	require.NoError(t, err)
	before := f.keeper.BusinessDenomSupply(f.ctx, route.BusinessDenom)
	f.bank.burn(bridgeHolder, bridgeTestDenom, 1)
	require.ErrorContains(t, f.keeper.SettleBridgeTransfer(f.ctx, route, transfer, before), "would burn")
}

func TestBridgeOutboundMayBurnGenesisAllocatedSupply(t *testing.T) {
	f := initFixture(t)
	installBridge(t, f, 1_000, 1_000)
	f.bank.add(bridgeHolder, bridgeTestDenom, 100)
	require.NoError(t, f.keeper.BridgeSupply.Set(f.ctx, types.BridgeSupplyState{
		GenesisAllocated:       shared.NewAmount(100),
		CumulativeBridgeMinted: shared.NewAmount(0),
		CumulativeBridgeBurned: shared.NewAmount(0),
	}))

	require.NoError(t, runBridgeTransfer(t, f, bridgeTransfer(keeper.BridgeOutbound, 60, 0x21)))
	require.NoError(t, f.keeper.EnsureBridgeSupplyInvariant(f.ctx))
	supply, err := f.keeper.BridgeSupply.Get(f.ctx)
	require.NoError(t, err)
	burned, err := shared.ParseAmount(supply.CumulativeBridgeBurned)
	require.NoError(t, err)
	require.Equal(t, uint64(60), burned)
}

func TestConfirmBridgeCutoverRejectsPartialReplayMatches(t *testing.T) {
	f := initFixture(t)
	installBridge(t, f, 1_000, 1_000)
	signerSet, err := f.keeper.BridgeSignerSet.Get(f.ctx)
	require.NoError(t, err)
	control, err := f.keeper.BridgeControl.Get(f.ctx)
	require.NoError(t, err)
	control.Lifecycle = types.BridgeLifecycleV1_BRIDGE_LIFECYCLE_V1_PENDING_EVM_CONFIRMATION
	control.Frozen = true
	require.NoError(t, f.keeper.BridgeControl.Set(f.ctx, control))
	require.NoError(t, f.keeper.BridgeCutover.Set(f.ctx, types.BridgeCutoverState{
		ProposalId: 10, NextLocalIsmId: signerSet.LocalIsmId,
		NextSignerSetHash: signerSet.LocalSignerSetHash,
		NextThreshold:     signerSet.Threshold, NextSignerCount: signerSet.SignerCount,
		EvmIngressPauseTxHash: bytes.Repeat([]byte{0x31}, 32), EvmIngressPauseBlockNumber: 20,
		EvmIngressPauseBlockHash: bytes.Repeat([]byte{0x32}, 32), FreezeHeight: 1,
	}))
	execution := keeper.AcceptedGovernanceActionContext{
		AuthorityAddress: sdk.AccAddress(f.keeper.GetAuthority()).String(),
		ProposalID:       11,
		Accepted:         true,
	}
	action := types.ConfirmBridgeCutoverV1{
		ProposalId: 11, CutoverProposalId: 10,
		ExpectedLocalIsmId: signerSet.LocalIsmId, ExpectedSignerSetHash: signerSet.LocalSignerSetHash,
		EvmIsmAddress: bytes.Repeat([]byte{0x41}, 20), EvmSignerSetHash: signerSet.LocalSignerSetHash,
		EvmChainId: 1, EvmBlockNumber: 30, EvmBlockHash: bytes.Repeat([]byte{0x42}, 32),
		DeploymentManifestHash: control.DeploymentManifestHash,
		InflightManifestHash:   bytes.Repeat([]byte{0x43}, 32), InflightMessageCount: 2,
	}
	require.NoError(t, f.keeper.ExecuteConfirmBridgeCutoverV1(f.ctx, execution, action))
	require.NoError(t, f.keeper.ExecuteConfirmBridgeCutoverV1(f.ctx, execution, action), "exact replay must be a noop")

	mutations := []func(*types.ConfirmBridgeCutoverV1){
		func(a *types.ConfirmBridgeCutoverV1) { a.EvmChainId++ },
		func(a *types.ConfirmBridgeCutoverV1) { a.EvmBlockHash[0] ^= 0xff },
		func(a *types.ConfirmBridgeCutoverV1) { a.InflightManifestHash[0] ^= 0xff },
		func(a *types.ConfirmBridgeCutoverV1) { a.InflightMessageCount++ },
	}
	for _, mutate := range mutations {
		conflict := action
		conflict.EvmBlockHash = append([]byte(nil), action.EvmBlockHash...)
		conflict.InflightManifestHash = append([]byte(nil), action.InflightManifestHash...)
		mutate(&conflict)
		require.ErrorContains(t, f.keeper.ExecuteConfirmBridgeCutoverV1(f.ctx, execution, conflict), "different EVM confirmation")
	}
}

// I-BRIDGE-2 is recomputed against the bank on every transfer, so a supply that
// moved outside the bridge is caught by the next transfer rather than at export.
func TestBridgeDetectsSupplyDriftOutsideTheBridge(t *testing.T) {
	f := initFixture(t)
	installBridge(t, f, 1_000, 1_000)

	f.bank.add("rogue", bridgeTestDenom, 7)
	require.ErrorContains(t, runBridgeTransfer(t, f, bridgeTransfer(keeper.BridgeInbound, 1, 0x11)), "I-BRIDGE-2")
}

// The delta assertion is what ties the counters to the ledger. A guard that
// authorised an amount the ledger never moved — an upstream path that silently
// minted nothing, or minted something else — must be caught here rather than
// recorded as if it had happened.
func TestBridgeSettleRejectsALedgerThatDidNotMoveTheAuthorisedAmount(t *testing.T) {
	f := initFixture(t)
	installBridge(t, f, 1_000, 1_000)
	transfer := bridgeTransfer(keeper.BridgeInbound, 400, 0x11)

	route, err := f.keeper.PrepareBridgeTransfer(f.ctx, transfer)
	require.NoError(t, err)
	before := f.keeper.BusinessDenomSupply(f.ctx, route.BusinessDenom)

	// The ledger did nothing at all.
	require.ErrorContains(t, f.keeper.SettleBridgeTransfer(f.ctx, route, transfer, before),
		"not to the authorised")

	// The ledger moved a different amount than the message authorised.
	f.bank.add(bridgeHolder, bridgeTestDenom, 399)
	require.ErrorContains(t, f.keeper.SettleBridgeTransfer(f.ctx, route, transfer, before),
		"not to the authorised")

	supply, err := f.keeper.BridgeSupply.Get(f.ctx)
	require.NoError(t, err)
	minted, err := shared.ParseAmount(supply.CumulativeBridgeMinted)
	require.NoError(t, err)
	require.Zero(t, minted, "a mismatched ledger move must not accrue supply")
}

// §7.3: a refusal is zero-write. The pre-ledger half must decide every rejection
// so nothing is ever minted and then rolled back.
func TestBridgePrepareRefusesBeforeTouchingState(t *testing.T) {
	f := initFixture(t)
	installBridge(t, f, 100, 100)

	_, err := f.keeper.PrepareBridgeTransfer(f.ctx, bridgeTransfer(keeper.BridgeInbound, 101, 0x11))
	require.ErrorContains(t, err, "rejected whole")

	_, err = f.keeper.BridgeEpochUsage.Get(f.ctx, 0)
	require.Error(t, err, "a refused transfer must not even open a usage window")

	supply, err := f.keeper.BridgeSupply.Get(f.ctx)
	require.NoError(t, err)
	minted, err := shared.ParseAmount(supply.CumulativeBridgeMinted)
	require.NoError(t, err)
	require.Zero(t, minted)
}

// A failed bridge transaction commits its ante writes but rolls back the cursor,
// so without an explicit reset the next bridge transaction would consume the
// dead intent and attribute someone else's message id and recipient to its own
// transfer. The reset is what makes the queue safe to reuse within a block.
func TestBridgeTransferIntentsDoNotLeakBetweenTransactions(t *testing.T) {
	f := initFixture(t)

	stale := bridgeTransfer(keeper.BridgeInbound, 10, 0xaa)
	stale.Counterpart = "trueopen1stale"
	require.NoError(t, f.keeper.PushBridgeTransferIntent(f.ctx, stale))

	// The next transaction carrying a bridge message starts from a clean queue.
	require.NoError(t, f.keeper.ResetBridgeTransferIntents(f.ctx))
	fresh := bridgeTransfer(keeper.BridgeInbound, 20, 0xbb)
	fresh.Counterpart = "trueopen1fresh"
	require.NoError(t, f.keeper.PushBridgeTransferIntent(f.ctx, fresh))

	taken, ok := f.keeper.TakeBridgeTransferIntent(f.ctx)
	require.True(t, ok)
	require.Equal(t, "trueopen1fresh", taken.Counterpart, "the stale intent must not be consumed")
	require.Equal(t, fresh.MessageID, taken.MessageID)

	_, ok = f.keeper.TakeBridgeTransferIntent(f.ctx)
	require.False(t, ok, "the queue holds exactly what this transaction recorded")
}

// A mint or burn with no decorated bridge message behind it has no message-level
// facts and must not be authorised at all (§5.1).
func TestBridgeTakeIntentRefusesWhenTheQueueIsEmpty(t *testing.T) {
	f := initFixture(t)
	_, ok := f.keeper.TakeBridgeTransferIntent(f.ctx)
	require.False(t, ok)
}
