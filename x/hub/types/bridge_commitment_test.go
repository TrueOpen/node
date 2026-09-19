package types_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/cosmos/cosmos-sdk/types/bech32"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// The two vectors below are the published conformance values of
// monorepo cross_chain_asset_bridge_protocol.md §10.1 and §10.1a. They
// are the only authority for these preimages: Wire v0.3 ships no testdata file
// for the bridge domains (DOC-013), so the doc's own golden hex is
// what Node reproduces rather than a digest computed here.
const (
	goldenBridgeChainID = "trueopen-golden-1"
	goldenUSDCRouteID   = "e33c90dd889124dbb52a982a29f9f9caaa3392aa0f39fb8bf3e8c61bc2d67aa4"
	goldenManifestHash  = "ca69aebc92d79d79030521cd06af1380f8dc350e882d37c5c2d29df9442590c3"
	goldenBusinessDenom = "hyperlane/0x4444444444444444444444444444444444444444"
)

func bridgeHex(t *testing.T, value string) []byte {
	t.Helper()
	raw, err := hex.DecodeString(value)
	require.NoError(t, err)
	return raw
}

func bridgeRepeat(t *testing.T, pair string, count int) []byte {
	t.Helper()
	out := make([]byte, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, bridgeHex(t, pair)...)
	}
	return out
}

func bridgeAddress(t *testing.T, raw []byte) string {
	t.Helper()
	encoded, err := bech32.ConvertAndEncode("trueopen", raw)
	require.NoError(t, err)
	return encoded
}

// goldenRoute is §10.1 verbatim. Its local_mailbox_id / local_warp_token_id are
// 20-byte placeholders, which is why USDCRouteID length-checks only the EVM-side
// fields: the published vector must reproduce even though a real network carries
// 32-byte upstream HexAddresses there.
func goldenRoute(t *testing.T) hubtypes.BridgeRouteV1 {
	t.Helper()
	return hubtypes.BridgeRouteV1{
		HyperlaneLocalDomain:    424243,
		OriginDomain:            1,
		OriginTokenAddress:      bridgeHex(t, "d0417b64b14d1ae817c92c92c655e03bcda1fd92"),
		OriginWarpRouterAddress: bridgeRepeat(t, "11", 20),
		OriginMailboxAddress:    bridgeRepeat(t, "22", 20),
		OriginDecimals:          6,
		BusinessDenom:           goldenBusinessDenom,
		LocalMailboxId:          bridgeRepeat(t, "33", 20),
		LocalWarpTokenId:        bridgeRepeat(t, "44", 20),
		LocalOriginDenom:        goldenBusinessDenom,
	}
}

func TestUSDCRouteIDMatchesTheProtocolGoldenVector(t *testing.T) {
	got, err := hubtypes.USDCRouteID(goldenBridgeChainID, goldenRoute(t))
	require.NoError(t, err)
	require.Equal(t, goldenUSDCRouteID, hex.EncodeToString(got[:]))
}

// §10.3 lists the route mutations that must be rejected. Each one either changes
// the identity or is refused outright; none may quietly keep the old id.
func TestUSDCRouteIDRejectsTheProtocolNegativeCases(t *testing.T) {
	base, err := hubtypes.USDCRouteID(goldenBridgeChainID, goldenRoute(t))
	require.NoError(t, err)

	t.Run("origin_domain equal to the local domain", func(t *testing.T) {
		route := goldenRoute(t)
		route.OriginDomain = route.HyperlaneLocalDomain
		_, err := hubtypes.USDCRouteID(goldenBridgeChainID, route)
		require.ErrorContains(t, err, "origin_domain")
	})
	t.Run("zero origin_decimals", func(t *testing.T) {
		route := goldenRoute(t)
		route.OriginDecimals = 0
		_, err := hubtypes.USDCRouteID(goldenBridgeChainID, route)
		require.ErrorContains(t, err, "origin_decimals")
	})
	t.Run("local_origin_denom drifting from business_denom", func(t *testing.T) {
		route := goldenRoute(t)
		route.LocalOriginDenom = goldenBusinessDenom + "x"
		_, err := hubtypes.USDCRouteID(goldenBridgeChainID, route)
		require.ErrorContains(t, err, "local_origin_denom")
	})
	t.Run("a different business_denom is a different route", func(t *testing.T) {
		route := goldenRoute(t)
		route.BusinessDenom = "hyperlane/0x5555555555555555555555555555555555555555"
		route.LocalOriginDenom = route.BusinessDenom
		got, err := hubtypes.USDCRouteID(goldenBridgeChainID, route)
		require.NoError(t, err)
		require.NotEqual(t, base, got)
	})
	t.Run("chain_id is part of the identity", func(t *testing.T) {
		got, err := hubtypes.USDCRouteID("trueopen-golden-2", goldenRoute(t))
		require.NoError(t, err)
		require.NotEqual(t, base, got)
	})
	t.Run("every EVM address width is exact", func(t *testing.T) {
		route := goldenRoute(t)
		route.OriginTokenAddress = route.OriginTokenAddress[:19]
		_, err := hubtypes.USDCRouteID(goldenBridgeChainID, route)
		require.ErrorContains(t, err, "origin_token_address")
	})
}

func goldenManifest(t *testing.T) hubtypes.BridgeDeploymentManifestV1 {
	t.Helper()
	route := goldenRoute(t)
	routeID, err := hubtypes.USDCRouteID(goldenBridgeChainID, route)
	require.NoError(t, err)
	route.UsdcRouteId = routeID[:]
	owner := bridgeHex(t, "1a642f0e3c3af545e7acbd38b07251b3990914f1")
	return hubtypes.BridgeDeploymentManifestV1{
		SchemaVersion:          1,
		Route:                  route,
		OriginEip155ChainId:    11155111,
		HyperlaneSourceRelease: hubtypes.HyperlaneSourceReleaseV1,
		NodeCosmosSdkRelease:   hubtypes.NodeCosmosSDKReleaseV1,
		LocalIsmId:             bridgeRepeat(t, "55", 32),
		EvmIsmAddress:          bridgeRepeat(t, "66", 20),
		LocalOwner:             bridgeAddress(t, owner),
		EvmOwnerOrProxyAdmin:   bridgeRepeat(t, "77", 20),
		FinalitySource:         hubtypes.BridgeOriginFinalitySourceV1_BRIDGE_ORIGIN_FINALITY_SOURCE_V1_FINALIZED_RPC,
		FinalityParameter:      0,
		LocalSignerSetHash:     bridgeRepeat(t, "88", 32),
		EvmSignerSetHash:       bridgeRepeat(t, "99", 32),
		Agents: []hubtypes.BridgeAgentDeploymentV1{{
			OperatorAddress:          bridgeAddress(t, owner),
			BridgeSignerAddressRaw20: bridgeRepeat(t, "aa", 20),
			StorageLocation:          "kms://trueopen-golden/bridge/1",
		}},
		Relayers:                          []string{bridgeAddress(t, bridgeHex(t, "c0c1c2c3c4c5c6c7c8c9cacbcccdcecfd0d1d2d3"))},
		EvmMailboxCodeHash:                bridgeRepeat(t, "bb", 32),
		EvmWarpRouterCodeHash:             bridgeRepeat(t, "cc", 32),
		EvmIsmCodeHash:                    bridgeRepeat(t, "dd", 32),
		EvmDeploymentTxHash:               bridgeRepeat(t, "ee", 32),
		EvmDeploymentBlockNumber:          123456,
		EvmDeploymentBlockHash:            bridgeRepeat(t, "ff", 32),
		EvmIngressPauseController:         bridgeRepeat(t, "12", 20),
		EvmIngressPauseControllerCodeHash: bridgeRepeat(t, "13", 32),
	}
}

func TestBridgeDeploymentManifestHashMatchesTheProtocolGoldenVector(t *testing.T) {
	got, err := hubtypes.BridgeDeploymentManifestHash(goldenBridgeChainID, goldenManifest(t))
	require.NoError(t, err)
	require.Equal(t, goldenManifestHash, hex.EncodeToString(got[:]))
}

// §10.1a: "changing the outer chain_id, any one of the 23 manifest fields, the
// order of agents/relayers or the nested framing must all change the digest". The
// mutations below are the ones a wrong
// implementation is most likely to get away with.
func TestBridgeDeploymentManifestHashReadsEveryField(t *testing.T) {
	base, err := hubtypes.BridgeDeploymentManifestHash(goldenBridgeChainID, goldenManifest(t))
	require.NoError(t, err)

	for name, mutate := range map[string]func(*hubtypes.BridgeDeploymentManifestV1){
		"origin_eip155_chain_id": func(m *hubtypes.BridgeDeploymentManifestV1) { m.OriginEip155ChainId++ },
		"local_ism_id":           func(m *hubtypes.BridgeDeploymentManifestV1) { m.LocalIsmId[0] ^= 0xff },
		"evm_ism_address":        func(m *hubtypes.BridgeDeploymentManifestV1) { m.EvmIsmAddress[0] ^= 0xff },
		"local_signer_set_hash":  func(m *hubtypes.BridgeDeploymentManifestV1) { m.LocalSignerSetHash[0] ^= 0xff },
		"evm_signer_set_hash":    func(m *hubtypes.BridgeDeploymentManifestV1) { m.EvmSignerSetHash[0] ^= 0xff },
		"agent storage_location": func(m *hubtypes.BridgeDeploymentManifestV1) { m.Agents[0].StorageLocation += "/2" },
		"agent bridge signer":    func(m *hubtypes.BridgeDeploymentManifestV1) { m.Agents[0].BridgeSignerAddressRaw20[0] ^= 0xff },
		"deployment block number": func(m *hubtypes.BridgeDeploymentManifestV1) {
			m.EvmDeploymentBlockNumber++
		},
		"pause controller": func(m *hubtypes.BridgeDeploymentManifestV1) { m.EvmIngressPauseController[0] ^= 0xff },
		"route field":      func(m *hubtypes.BridgeDeploymentManifestV1) { m.Route.UsdcRouteId = nil; m.Route.OriginDecimals = 8 },
	} {
		t.Run(name, func(t *testing.T) {
			manifest := goldenManifest(t)
			mutate(&manifest)
			got, err := hubtypes.BridgeDeploymentManifestHash(goldenBridgeChainID, manifest)
			require.NoError(t, err)
			require.NotEqual(t, base, got)
		})
	}

	// A manifest carries its route id, so moving it to another chain means
	// rebuilding that id too; hashing the golden manifest under a different
	// chain_id is refused outright rather than silently producing a digest for a
	// manifest whose route id belongs to a different chain.
	t.Run("chain_id is refused when the route id was derived elsewhere", func(t *testing.T) {
		_, err := hubtypes.BridgeDeploymentManifestHash("trueopen-golden-2", goldenManifest(t))
		require.ErrorContains(t, err, "usdc_route_id does not match")
	})

	t.Run("a consistent manifest for another chain hashes differently", func(t *testing.T) {
		manifest := goldenManifest(t)
		routeID, err := hubtypes.USDCRouteID("trueopen-golden-2", manifest.Route)
		require.NoError(t, err)
		manifest.Route.UsdcRouteId = routeID[:]
		got, err := hubtypes.BridgeDeploymentManifestHash("trueopen-golden-2", manifest)
		require.NoError(t, err)
		require.NotEqual(t, base, got)
	})
}

// The release literals are consensus content, not prose: a manifest built
// against a different upstream or SDK must not be hashable at all.
func TestBridgeDeploymentManifestRejectsNonCanonicalReleaseLiterals(t *testing.T) {
	manifest := goldenManifest(t)
	manifest.HyperlaneSourceRelease = "bcp-innovations/hyperlane-cosmos@v1.2.0"
	_, err := hubtypes.BridgeDeploymentManifestHash(goldenBridgeChainID, manifest)
	require.ErrorContains(t, err, "hyperlane_source_release")

	manifest = goldenManifest(t)
	manifest.NodeCosmosSdkRelease = "github.com/cosmos/cosmos-sdk@v0.50.12"
	_, err = hubtypes.BridgeDeploymentManifestHash(goldenBridgeChainID, manifest)
	require.ErrorContains(t, err, "node_cosmos_sdk_release")
}

func TestBridgeDeploymentManifestRejectsUnorderedAgentsAndRelayers(t *testing.T) {
	low := bridgeHex(t, "1a642f0e3c3af545e7acbd38b07251b3990914f1")
	high := bridgeRepeat(t, "fe", 20)

	manifest := goldenManifest(t)
	manifest.Agents = []hubtypes.BridgeAgentDeploymentV1{
		{OperatorAddress: bridgeAddress(t, high), BridgeSignerAddressRaw20: bridgeRepeat(t, "aa", 20), StorageLocation: "kms://b"},
		{OperatorAddress: bridgeAddress(t, low), BridgeSignerAddressRaw20: bridgeRepeat(t, "ab", 20), StorageLocation: "kms://a"},
	}
	_, err := hubtypes.BridgeDeploymentManifestHash(goldenBridgeChainID, manifest)
	require.ErrorContains(t, err, "agents must ascend")

	manifest = goldenManifest(t)
	manifest.Relayers = []string{bridgeAddress(t, high), bridgeAddress(t, low)}
	_, err = hubtypes.BridgeDeploymentManifestHash(goldenBridgeChainID, manifest)
	require.ErrorContains(t, err, "relayers must ascend")

	manifest = goldenManifest(t)
	manifest.Relayers = []string{bridgeAddress(t, low), bridgeAddress(t, low)}
	_, err = hubtypes.BridgeDeploymentManifestHash(goldenBridgeChainID, manifest)
	require.ErrorContains(t, err, "relayers must ascend")
}

func TestBridgeDeploymentManifestBindsFinalityPolicyToItsParameter(t *testing.T) {
	manifest := goldenManifest(t)
	manifest.FinalityParameter = 1
	_, err := hubtypes.BridgeDeploymentManifestHash(goldenBridgeChainID, manifest)
	require.ErrorContains(t, err, "must be 0 for FINALIZED_RPC")

	manifest = goldenManifest(t)
	manifest.FinalitySource = hubtypes.BridgeOriginFinalitySourceV1_BRIDGE_ORIGIN_FINALITY_SOURCE_V1_FINALIZED_CHECKPOINT
	manifest.FinalityParameter = 0
	_, err = hubtypes.BridgeDeploymentManifestHash(goldenBridgeChainID, manifest)
	require.ErrorContains(t, err, "positive approved checkpoint policy version")

	manifest = goldenManifest(t)
	manifest.FinalitySource = hubtypes.BridgeOriginFinalitySourceV1_BRIDGE_ORIGIN_FINALITY_SOURCE_V1_UNSPECIFIED
	_, err = hubtypes.BridgeDeploymentManifestHash(goldenBridgeChainID, manifest)
	require.ErrorContains(t, err, "finality_source")
}

// §10.2 publishes the threshold table; an off-by-one here changes how many
// stolen bridge keys it takes to mint USDC out of nothing.
func TestBridgeThresholdMatchesTheProtocolTable(t *testing.T) {
	for _, tc := range []struct{ n, want uint32 }{
		{n: 4, want: 3}, {n: 5, want: 4}, {n: 7, want: 5}, {n: 10, want: 7}, {n: 15, want: 10},
		{n: 1, want: 1}, {n: 2, want: 2}, {n: 3, want: 2}, {n: 6, want: 4},
	} {
		got, err := hubtypes.BridgeThresholdV1(tc.n)
		require.NoError(t, err)
		require.Equalf(t, tc.want, got, "n=%d", tc.n)
	}
	_, err := hubtypes.BridgeThresholdV1(0)
	require.Error(t, err)

	// The formula is total, but §4.1 refuses to run a bridge below four signers.
	require.Error(t, hubtypes.RequireBridgeSignerCount(3))
	require.NoError(t, hubtypes.RequireBridgeSignerCount(4))
}

func TestBridgeSignerSetHashBindsOrderThresholdAndCount(t *testing.T) {
	signers := [][]byte{
		bridgeRepeat(t, "11", 20), bridgeRepeat(t, "22", 20),
		bridgeRepeat(t, "33", 20), bridgeRepeat(t, "44", 20),
	}
	base, err := hubtypes.BridgeSignerSetHash(goldenBridgeChainID, 3, 4, signers)
	require.NoError(t, err)

	// A descending or duplicated set must not hash at all: I-BRIDGE-1 compares
	// three independently derived sets, which only works under one total order.
	reversed := [][]byte{signers[3], signers[2], signers[1], signers[0]}
	_, err = hubtypes.BridgeSignerSetHash(goldenBridgeChainID, 3, 4, reversed)
	require.ErrorContains(t, err, "ascend strictly")

	duplicated := [][]byte{signers[0], signers[0], signers[2], signers[3]}
	_, err = hubtypes.BridgeSignerSetHash(goldenBridgeChainID, 3, 4, duplicated)
	require.ErrorContains(t, err, "ascend strictly")

	// The threshold is derived, never free.
	_, err = hubtypes.BridgeSignerSetHash(goldenBridgeChainID, 2, 4, signers)
	require.ErrorContains(t, err, "derived")

	_, err = hubtypes.BridgeSignerSetHash(goldenBridgeChainID, 3, 5, signers)
	require.ErrorContains(t, err, "does not match")

	five := append(append([][]byte(nil), signers...), bridgeRepeat(t, "55", 20))
	other, err := hubtypes.BridgeSignerSetHash(goldenBridgeChainID, 4, 5, five)
	require.NoError(t, err)
	require.NotEqual(t, base, other)
}

// The PoP proves the registered bridge key is usable and nothing else. The
// digest binds key_version so a rotation cannot replay the previous proof.
func TestBridgeSignerPoPRoundTripsAndBindsKeyVersion(t *testing.T) {
	operator := bridgeAddress(t, bridgeRepeat(t, "a1", 20))
	signer, signature := bridgeSignerPoPFixture(t, goldenBridgeChainID, operator, 1)

	require.NoError(t, hubtypes.VerifyBridgeSignerPoP(goldenBridgeChainID, operator, signer, signature, 1))

	require.Error(t, hubtypes.VerifyBridgeSignerPoP(goldenBridgeChainID, operator, signer, signature, 2),
		"a PoP for key_version 1 must not authorise version 2")
	require.Error(t, hubtypes.VerifyBridgeSignerPoP("trueopen-golden-2", operator, signer, signature, 1),
		"a PoP from another chain must not authorise this one")

	other := bridgeAddress(t, bridgeRepeat(t, "a2", 20))
	require.Error(t, hubtypes.VerifyBridgeSignerPoP(goldenBridgeChainID, other, signer, signature, 1),
		"a PoP bound to one operator must not authorise another")

	wrongSigner := append([]byte(nil), signer...)
	wrongSigner[0] ^= 0xff
	require.Error(t, hubtypes.VerifyBridgeSignerPoP(goldenBridgeChainID, operator, wrongSigner, signature, 1))

	short := append([]byte(nil), signature[:64]...)
	require.Error(t, hubtypes.VerifyBridgeSignerPoP(goldenBridgeChainID, operator, signer, short, 1))
}

func TestBridgeSignerPoPRejectsHighSAndBadRecoveryID(t *testing.T) {
	operator := bridgeAddress(t, bridgeRepeat(t, "a1", 20))
	signer, signature := bridgeSignerPoPFixture(t, goldenBridgeChainID, operator, 1)

	badV := append([]byte(nil), signature...)
	badV[64] = 29
	require.ErrorContains(t, hubtypes.VerifyBridgeSignerPoP(goldenBridgeChainID, operator, signer, badV, 1), "V must be 27 or 28")

	highS := append([]byte(nil), signature...)
	negateSecp256k1S(highS[32:64])
	err := hubtypes.VerifyBridgeSignerPoP(goldenBridgeChainID, operator, signer, highS, 1)
	require.Error(t, err, "a malleated high-S signature must be refused before recovery")
	require.Contains(t, err.Error(), "low-S")
}

func TestBridgeInflightManifestRequiresOutboundAscendingItems(t *testing.T) {
	item := func(id byte, disposition hubtypes.BridgeInflightDispositionV1) hubtypes.BridgeInflightItemV1 {
		return hubtypes.BridgeInflightItemV1{
			MessageId:    bytes.Repeat([]byte{id}, 32),
			Direction:    hubtypes.BridgeDirectionV1_BRIDGE_DIRECTION_V1_OUTBOUND,
			Amount:       shared.NewAmount(10),
			OriginTxHash: bytes.Repeat([]byte{id ^ 0x0f}, 32),
			Disposition:  disposition,
		}
	}
	delivered := hubtypes.BridgeInflightDispositionV1_BRIDGE_INFLIGHT_DISPOSITION_V1_DELIVERED_OLD_ISM
	base, err := hubtypes.BridgeInflightManifestHash(goldenBridgeChainID, 7, 900,
		[]hubtypes.BridgeInflightItemV1{item(0x11, delivered), item(0x22, delivered)})
	require.NoError(t, err)

	descending, err := hubtypes.BridgeInflightManifestHash(goldenBridgeChainID, 7, 900,
		[]hubtypes.BridgeInflightItemV1{item(0x22, delivered), item(0x11, delivered)})
	require.ErrorContains(t, err, "ascend strictly")
	require.Zero(t, descending)

	inbound := item(0x33, delivered)
	inbound.Direction = hubtypes.BridgeDirectionV1_BRIDGE_DIRECTION_V1_INBOUND
	_, err = hubtypes.BridgeInflightManifestHash(goldenBridgeChainID, 7, 900, []hubtypes.BridgeInflightItemV1{inbound})
	require.ErrorContains(t, err, "must be OUTBOUND")

	// The freeze height is the stable cutoff the manifest is cut at, so it is
	// part of the commitment rather than metadata.
	moved, err := hubtypes.BridgeInflightManifestHash(goldenBridgeChainID, 7, 901,
		[]hubtypes.BridgeInflightItemV1{item(0x11, delivered), item(0x22, delivered)})
	require.NoError(t, err)
	require.NotEqual(t, base, moved)
}
