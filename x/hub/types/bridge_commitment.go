package types

import (
	"bytes"
	"fmt"

	"github.com/cosmos/cosmos-sdk/types/bech32"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// cross_chain_asset_bridge_protocol.md §3.1 pins the two release literals the deployment manifest
// must carry verbatim. They are consensus strings inside
// TRUEOPEN_BRIDGE_DEPLOYMENT_MANIFEST_V1, not documentation: a manifest naming a
// different upstream or SDK is a different bridge and must not hash the same.
const (
	HyperlaneSourceReleaseV1 = "bcp-innovations/hyperlane-cosmos@v1.1.0"
	NodeCosmosSDKReleaseV1   = "github.com/cosmos/cosmos-sdk@v0.53.6"
)

const (
	// EVMAddressLen is the raw width of every EVM-side address in the route and
	// the manifest, and of a bridge signer identity (§2.1, §4.1).
	EVMAddressLen = shared.EVMAddressBytes
	// HyperlaneIDLen is the raw width of an upstream HexAddress — the Mailbox,
	// Synthetic token and ISM identifiers. util.HEX_ADDRESS_LENGTH is 32; see
	// RequireHyperlaneID for why this is not the 20 the prose claims.
	HyperlaneIDLen = 32
	// BridgeSignerPoPSignatureLen is the R||S||V PoP transport width (§4.1).
	BridgeSignerPoPSignatureLen = shared.RecoverableSecp256k1SignatureBytes
	// MinBridgeSignerCount is the §4.1 floor: below four signers Phase 0 does not
	// open the bridge at all, whatever the threshold formula would return.
	MinBridgeSignerCount = 4
)

// BridgeThresholdV1 is the §4.2 threshold ceil(2n/3), fixed as the integer
// expression (2n+2)/3 so every implementation lands on the same value without a
// float or a rounding mode. The n >= 4 floor is deliberately *not* enforced
// here: §10.2 publishes the formula as a total function and the admission gates
// (Genesis, cutover, guard) own the floor, so a caller cannot accidentally get a
// threshold for a set the bridge must refuse to run on.
func BridgeThresholdV1(signerCount uint32) (uint32, error) {
	if signerCount == 0 {
		return 0, fmt.Errorf("bridge signer set must not be empty")
	}
	return uint32((2*uint64(signerCount) + 2) / 3), nil
}

// RequireBridgeSignerCount is the §4.1 admission floor that BridgeThresholdV1
// leaves to its callers.
func RequireBridgeSignerCount(signerCount uint32) error {
	if signerCount < MinBridgeSignerCount {
		return fmt.Errorf("bridge requires at least %d signers, got %d", MinBridgeSignerCount, signerCount)
	}
	return nil
}

func requireBytesLen(field string, value []byte, want int) ([]byte, error) {
	if len(value) != want {
		return nil, fmt.Errorf("%s must be exactly %d bytes, got %d", field, want, len(value))
	}
	return append([]byte(nil), value...), nil
}

// RequireEVMAddress accepts exactly 20 raw bytes. Per §4.1 these values are EVM
// identities owned by upstream fields; they are never decoded as TrueOpen accounts
// and never cross the bech32 boundary in the other direction.
func RequireEVMAddress(field string, value []byte) ([]byte, error) {
	return requireBytesLen(field, value, EVMAddressLen)
}

// RequireHyperlaneID accepts exactly one raw upstream HexAddress.
//
// cross_chain_asset_bridge_protocol.md §2.1 rows 9-10 and
// keeper_data_structure_contract.md §6.6a describe these as "the raw 20 bytes of
// the upstream HexAddress", but util.HEX_ADDRESS_LENGTH in the pinned
// hyperlane-cosmos@v1.1.0 is 32, and §10.1a's own published manifest vector
// encodes local_ism_id as 32 bytes. A 20-byte identifier could never equal the
// upstream Mailbox, token or ISM ID the guard compares against, so the stated
// width is the stale part and "raw upstream HexAddress" is the binding intent.
// Registered as DOC-012.
func RequireHyperlaneID(field string, value []byte) ([]byte, error) {
	return requireBytesLen(field, value, HyperlaneIDLen)
}

func requireBridgeHash32(field string, value []byte) ([]byte, error) {
	return requireBytesLen(field, value, shared.Hash32KeySize)
}

func requireBridgeText(field, value string) ([]byte, error) {
	if value == "" {
		return nil, fmt.Errorf("%s must not be empty", field)
	}
	return []byte(value), nil
}

// bridgeAddressBytes is the §1.4 rule-4 address encoding: every Address field in
// a bridge preimage contributes its canonical codec bytes, never its bech32
// text. The §10.1a manifest vector encodes local_owner as its raw 20 bytes,
// which is what this reproduces.
func bridgeAddressBytes(field, value string) ([]byte, error) {
	if value == "" {
		return nil, fmt.Errorf("%s must not be empty", field)
	}
	hrp, raw, err := bech32.DecodeAndConvert(value)
	if err != nil {
		return nil, fmt.Errorf("%s is not a decodable Bech32 address: %w", field, err)
	}
	reencoded, err := bech32.ConvertAndEncode(hrp, raw)
	if err != nil || reencoded != value {
		return nil, fmt.Errorf("%s must be the canonical Bech32 encoding of its address bytes", field)
	}
	return raw, nil
}

// canonicalBridgeRouteFields returns the ten identity fields of §2.1 in their
// frozen order, already validated. usdc_route_id is not among them: it is what
// they derive.
func canonicalBridgeRouteFields(route BridgeRouteV1) ([][]byte, error) {
	if route.HyperlaneLocalDomain == 0 {
		return nil, fmt.Errorf("hyperlane_local_domain must be non-zero")
	}
	if route.OriginDomain == 0 || route.OriginDomain == route.HyperlaneLocalDomain {
		return nil, fmt.Errorf("origin_domain must be non-zero and differ from hyperlane_local_domain")
	}
	if route.OriginDecimals == 0 {
		return nil, fmt.Errorf("origin_decimals must be greater than zero")
	}
	originToken, err := RequireEVMAddress("origin_token_address", route.OriginTokenAddress)
	if err != nil {
		return nil, err
	}
	originRouter, err := RequireEVMAddress("origin_warp_router_address", route.OriginWarpRouterAddress)
	if err != nil {
		return nil, err
	}
	originMailbox, err := RequireEVMAddress("origin_mailbox_address", route.OriginMailboxAddress)
	if err != nil {
		return nil, err
	}
	businessDenom, err := requireBridgeText("business_denom", route.BusinessDenom)
	if err != nil {
		return nil, err
	}
	// §2.1 row 11: local_origin_denom is the upstream HypToken.origin_denom and
	// must be byte-equal to business_denom. Two spellings of the same asset are
	// two assets as far as the conservation identity is concerned.
	if route.LocalOriginDenom != route.BusinessDenom {
		return nil, fmt.Errorf("local_origin_denom must be byte-equal to business_denom")
	}
	if len(route.LocalMailboxId) == 0 || len(route.LocalWarpTokenId) == 0 {
		return nil, fmt.Errorf("local_mailbox_id and local_warp_token_id must not be empty")
	}
	return [][]byte{
		[]byte(nil), // placeholder replaced by the caller's chain_id position
		shared.Uint32BE(route.HyperlaneLocalDomain),
		shared.Uint32BE(route.OriginDomain),
		originToken, originRouter, originMailbox,
		shared.Uint32BE(route.OriginDecimals),
		businessDenom,
		append([]byte(nil), route.LocalMailboxId...),
		append([]byte(nil), route.LocalWarpTokenId...),
		[]byte(route.LocalOriginDenom),
	}, nil
}

// USDCRouteID derives the §2.1 route identity. The eleven fields are the whole
// definition of "which asset is business_denom": any one of them changing is a
// different route and therefore a chain upgrade, never a governance edit.
//
// The local Hyperlane identifiers are length-checked only for non-emptiness
// here so the published §10.1 golden — which uses 20-byte placeholders and is
// explicitly not any real network's values — still reproduces byte for byte.
// Genesis applies the real upstream width through RequireHyperlaneID.
func USDCRouteID(chainID string, route BridgeRouteV1) ([32]byte, error) {
	chain, err := requireBridgeText("chain_id", chainID)
	if err != nil {
		return [32]byte{}, err
	}
	fields, err := canonicalBridgeRouteFields(route)
	if err != nil {
		return [32]byte{}, err
	}
	fields[0] = chain
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainUSDCRouteV1)).Raw(fields...).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

// CanonicalBridgeRouteFrameV1 is the eleven-field nested frame the deployment
// manifest embeds: the derived usdc_route_id followed by the ten identity
// fields. The route id is included rather than recomputed by the consumer so a
// manifest cannot quietly describe one route and commit to another.
func CanonicalBridgeRouteFrameV1(chainID string, route BridgeRouteV1) (shared.CanonicalFrameV1, error) {
	routeID, err := USDCRouteID(chainID, route)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	if len(route.UsdcRouteId) != 0 && !bytes.Equal(route.UsdcRouteId, routeID[:]) {
		return shared.CanonicalFrameV1{}, fmt.Errorf("usdc_route_id does not match its own eleven frozen fields")
	}
	fields, err := canonicalBridgeRouteFields(route)
	if err != nil {
		return shared.CanonicalFrameV1{}, err
	}
	fields[0] = routeID[:]
	return shared.FlatCanonicalFrameV1(fields...), nil
}

// BridgeSignerSetHash is the I-BRIDGE-1 projection (§4.2). The guard recomputes
// it from upstream Mailbox/default-ISM rows and from the Validator bridge signer
// table and requires both — plus the confirmed EVM hash — to agree, so the
// ordering rule below is what makes those three independently-derived sets
// comparable at all.
func BridgeSignerSetHash(chainID string, threshold, signerCount uint32, signers [][]byte) ([32]byte, error) {
	chain, err := requireBridgeText("chain_id", chainID)
	if err != nil {
		return [32]byte{}, err
	}
	if signerCount != uint32(len(signers)) {
		return [32]byte{}, fmt.Errorf("bridge signer count %d does not match the %d signers given", signerCount, len(signers))
	}
	derived, err := BridgeThresholdV1(signerCount)
	if err != nil {
		return [32]byte{}, err
	}
	if threshold != derived {
		return [32]byte{}, fmt.Errorf("bridge threshold %d is not the derived ceil(2n/3) value %d", threshold, derived)
	}
	elements := make([]shared.CanonicalFieldV1, 0, len(signers))
	for index, signer := range signers {
		raw, err := RequireEVMAddress(fmt.Sprintf("bridge_signer_address_raw20[%d]", index), signer)
		if err != nil {
			return [32]byte{}, err
		}
		if index != 0 && bytes.Compare(signers[index-1], raw) >= 0 {
			return [32]byte{}, fmt.Errorf("bridge signers must ascend strictly by raw address bytes")
		}
		elements = append(elements, shared.RawCanonicalFieldV1(raw))
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainBridgeSignerSetV1)).
		Raw(chain, shared.Uint32BE(threshold), shared.Uint32BE(signerCount)).
		Nested(shared.CanonicalRepeatedFieldsV1(elements)).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

// BridgeSignerPoPDigest is the §4.1 proof-of-possession preimage. key_version is
// inside it so a rotation cannot replay the previous version's signature.
func BridgeSignerPoPDigest(chainID, operatorAddress string, signerRaw20 []byte, keyVersion uint64) ([32]byte, error) {
	chain, err := requireBridgeText("chain_id", chainID)
	if err != nil {
		return [32]byte{}, err
	}
	operator, err := bridgeAddressBytes("operator_address", operatorAddress)
	if err != nil {
		return [32]byte{}, err
	}
	signer, err := RequireEVMAddress("bridge_signer_address_raw20", signerRaw20)
	if err != nil {
		return [32]byte{}, err
	}
	if keyVersion == 0 {
		return [32]byte{}, fmt.Errorf("bridge signer key_version must be greater than zero")
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainBridgeSignerPoPV1)).Raw(
		chain, operator, signer, shared.Uint64BE(keyVersion),
	).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

// VerifyBridgeSignerPoP proves the registered bridge key is usable by the party
// registering it. §4.1 is explicit that it grants no TrueOpen account authority:
// the recovered value is compared to the declared EVM identity and discarded.
func VerifyBridgeSignerPoP(chainID, operatorAddress string, signerRaw20, popSignature []byte, keyVersion uint64) error {
	digest, err := BridgeSignerPoPDigest(chainID, operatorAddress, signerRaw20, keyVersion)
	if err != nil {
		return err
	}
	recovered, err := shared.RecoverSecp256k1Signer(digest[:], popSignature)
	if err != nil {
		return fmt.Errorf("bridge signer PoP: %w", err)
	}
	if !bytes.Equal(recovered.Address, signerRaw20) {
		return fmt.Errorf("bridge signer PoP recovers a different address than bridge_signer_address_raw20")
	}
	return nil
}

// BridgeDeploymentManifestHash is the §8 manifest commitment: a required nested
// frame over fields 1..23 under chain_id. Mainnet approval and every cutover
// reference this digest, so the release literals, the agent/relayer ordering and
// every address/hash width are checked before hashing rather than after.
func BridgeDeploymentManifestHash(chainID string, manifest BridgeDeploymentManifestV1) ([32]byte, error) {
	chain, err := requireBridgeText("chain_id", chainID)
	if err != nil {
		return [32]byte{}, err
	}
	frame, err := canonicalBridgeDeploymentManifestFrame(chainID, manifest)
	if err != nil {
		return [32]byte{}, err
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainBridgeDeploymentManifestV1)).
		Raw(chain).Nested(frame).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

func canonicalBridgeDeploymentManifestFrame(chainID string, manifest BridgeDeploymentManifestV1) (shared.CanonicalFrameV1, error) {
	var zero shared.CanonicalFrameV1
	if manifest.SchemaVersion != 1 {
		return zero, fmt.Errorf("deployment manifest schema_version must be 1")
	}
	route, err := CanonicalBridgeRouteFrameV1(chainID, manifest.Route)
	if err != nil {
		return zero, err
	}
	if manifest.OriginEip155ChainId == 0 {
		return zero, fmt.Errorf("origin_eip155_chain_id must be non-zero")
	}
	if manifest.HyperlaneSourceRelease != HyperlaneSourceReleaseV1 {
		return zero, fmt.Errorf("hyperlane_source_release must be exactly %q", HyperlaneSourceReleaseV1)
	}
	if manifest.NodeCosmosSdkRelease != NodeCosmosSDKReleaseV1 {
		return zero, fmt.Errorf("node_cosmos_sdk_release must be exactly %q", NodeCosmosSDKReleaseV1)
	}
	localIsm, err := RequireHyperlaneID("local_ism_id", manifest.LocalIsmId)
	if err != nil {
		return zero, err
	}
	evmIsm, err := RequireEVMAddress("evm_ism_address", manifest.EvmIsmAddress)
	if err != nil {
		return zero, err
	}
	localOwner, err := bridgeAddressBytes("local_owner", manifest.LocalOwner)
	if err != nil {
		return zero, err
	}
	evmOwner, err := RequireEVMAddress("evm_owner_or_proxy_admin", manifest.EvmOwnerOrProxyAdmin)
	if err != nil {
		return zero, err
	}
	if err := requireBridgeFinality(manifest.FinalitySource, manifest.FinalityParameter); err != nil {
		return zero, err
	}
	localSignerSet, err := requireBridgeHash32("local_signer_set_hash", manifest.LocalSignerSetHash)
	if err != nil {
		return zero, err
	}
	evmSignerSet, err := requireBridgeHash32("evm_signer_set_hash", manifest.EvmSignerSetHash)
	if err != nil {
		return zero, err
	}
	agents, err := canonicalBridgeAgentsFrame(manifest.Agents)
	if err != nil {
		return zero, err
	}
	relayers, err := canonicalBridgeRelayersFrame(manifest.Relayers)
	if err != nil {
		return zero, err
	}
	hashes := make([][]byte, 0, 6)
	for _, field := range []struct {
		name  string
		value []byte
	}{
		{"evm_mailbox_code_hash", manifest.EvmMailboxCodeHash},
		{"evm_warp_router_code_hash", manifest.EvmWarpRouterCodeHash},
		{"evm_ism_code_hash", manifest.EvmIsmCodeHash},
		{"evm_deployment_tx_hash", manifest.EvmDeploymentTxHash},
	} {
		value, err := requireBridgeHash32(field.name, field.value)
		if err != nil {
			return zero, err
		}
		hashes = append(hashes, value)
	}
	if manifest.EvmDeploymentBlockNumber == 0 {
		return zero, fmt.Errorf("evm_deployment_block_number must be non-zero")
	}
	deploymentBlockHash, err := requireBridgeHash32("evm_deployment_block_hash", manifest.EvmDeploymentBlockHash)
	if err != nil {
		return zero, err
	}
	pauseController, err := RequireEVMAddress("evm_ingress_pause_controller", manifest.EvmIngressPauseController)
	if err != nil {
		return zero, err
	}
	pauseControllerCode, err := requireBridgeHash32("evm_ingress_pause_controller_code_hash", manifest.EvmIngressPauseControllerCodeHash)
	if err != nil {
		return zero, err
	}
	return shared.NewCanonicalFrameBuilderV1().
		Raw(shared.Uint32BE(manifest.SchemaVersion)).
		Nested(route).
		Raw(
			shared.Uint64BE(manifest.OriginEip155ChainId),
			[]byte(manifest.HyperlaneSourceRelease),
			[]byte(manifest.NodeCosmosSdkRelease),
			localIsm, evmIsm, localOwner, evmOwner,
			shared.EnumBE(uint32(manifest.FinalitySource)),
			shared.Uint64BE(manifest.FinalityParameter),
			localSignerSet, evmSignerSet,
		).
		Nested(agents, relayers).
		Raw(
			hashes[0], hashes[1], hashes[2], hashes[3],
			shared.Uint64BE(manifest.EvmDeploymentBlockNumber),
			deploymentBlockHash, pauseController, pauseControllerCode,
		).Build(), nil
}

// requireBridgeFinality enforces the §8 pairing: FINALIZED_RPC carries no
// parameter, a checkpoint policy must name a positive approved version, and no
// other source is admissible. §6.3 puts the whole security ceiling of the bridge
// on every agent running the identical policy, so an unspecified source is not a
// default — it is an unanswerable question.
func requireBridgeFinality(source BridgeOriginFinalitySourceV1, parameter uint64) error {
	switch source {
	case BridgeOriginFinalitySourceV1_BRIDGE_ORIGIN_FINALITY_SOURCE_V1_FINALIZED_RPC:
		if parameter != 0 {
			return fmt.Errorf("finality_parameter must be 0 for FINALIZED_RPC")
		}
	case BridgeOriginFinalitySourceV1_BRIDGE_ORIGIN_FINALITY_SOURCE_V1_FINALIZED_CHECKPOINT:
		if parameter == 0 {
			return fmt.Errorf("finality_parameter must name a positive approved checkpoint policy version")
		}
	default:
		return fmt.Errorf("finality_source must be FINALIZED_RPC or FINALIZED_CHECKPOINT")
	}
	return nil
}

func canonicalBridgeAgentsFrame(agents []BridgeAgentDeploymentV1) (shared.CanonicalFrameV1, error) {
	var zero shared.CanonicalFrameV1
	if len(agents) == 0 {
		return zero, fmt.Errorf("deployment manifest must list at least one agent")
	}
	frames := make([]shared.CanonicalFrameV1, 0, len(agents))
	var previous []byte
	for index, agent := range agents {
		operator, err := bridgeAddressBytes(fmt.Sprintf("agents[%d].operator_address", index), agent.OperatorAddress)
		if err != nil {
			return zero, err
		}
		if index != 0 && bytes.Compare(previous, operator) >= 0 {
			return zero, fmt.Errorf("deployment manifest agents must ascend strictly by operator raw bytes")
		}
		previous = operator
		signer, err := RequireEVMAddress(fmt.Sprintf("agents[%d].bridge_signer_address_raw20", index), agent.BridgeSignerAddressRaw20)
		if err != nil {
			return zero, err
		}
		storage, err := requireBridgeText(fmt.Sprintf("agents[%d].storage_location", index), agent.StorageLocation)
		if err != nil {
			return zero, err
		}
		frames = append(frames, shared.FlatCanonicalFrameV1(operator, signer, storage))
	}
	return shared.CanonicalRepeatedFramesV1(frames), nil
}

func canonicalBridgeRelayersFrame(relayers []string) (shared.CanonicalFrameV1, error) {
	var zero shared.CanonicalFrameV1
	if len(relayers) == 0 {
		return zero, fmt.Errorf("deployment manifest must list at least one relayer")
	}
	elements := make([]shared.CanonicalFieldV1, 0, len(relayers))
	var previous []byte
	for index, relayer := range relayers {
		raw, err := bridgeAddressBytes(fmt.Sprintf("relayers[%d]", index), relayer)
		if err != nil {
			return zero, err
		}
		if index != 0 && bytes.Compare(previous, raw) >= 0 {
			return zero, fmt.Errorf("deployment manifest relayers must ascend strictly by address raw bytes")
		}
		previous = raw
		elements = append(elements, shared.RawCanonicalFieldV1(raw))
	}
	return shared.CanonicalRepeatedFieldsV1(elements), nil
}

// BridgeInflightManifestHash commits the outbound queue a cutover has to drain
// before it may be confirmed (§4.3). The manifest is only well-formed after the
// freeze height, when canonical inbound is already zero, so every item must be
// OUTBOUND; confirm-time additionally requires every disposition to be
// DELIVERED_OLD_ISM, which is the caller's gate rather than a hashing rule.
func BridgeInflightManifestHash(
	chainID string,
	cutoverProposalID, freezeHeight uint64,
	items []BridgeInflightItemV1,
) ([32]byte, error) {
	chain, err := requireBridgeText("chain_id", chainID)
	if err != nil {
		return [32]byte{}, err
	}
	if cutoverProposalID == 0 || freezeHeight == 0 {
		return [32]byte{}, fmt.Errorf("inflight manifest needs a positive cutover proposal id and freeze height")
	}
	frames := make([]shared.CanonicalFrameV1, 0, len(items))
	var previous []byte
	for index, item := range items {
		messageID, err := requireBridgeHash32(fmt.Sprintf("inflight[%d].message_id", index), item.MessageId)
		if err != nil {
			return [32]byte{}, err
		}
		if index != 0 && bytes.Compare(previous, messageID) >= 0 {
			return [32]byte{}, fmt.Errorf("inflight manifest items must ascend strictly by message_id")
		}
		previous = messageID
		if item.Direction != BridgeDirectionV1_BRIDGE_DIRECTION_V1_OUTBOUND {
			return [32]byte{}, fmt.Errorf("inflight[%d] must be OUTBOUND: the manifest is cut after inbound is already drained", index)
		}
		amount, err := shared.CanonicalAmountFrameV1(item.Amount)
		if err != nil {
			return [32]byte{}, fmt.Errorf("inflight[%d].amount: %w", index, err)
		}
		originTx, err := requireBridgeHash32(fmt.Sprintf("inflight[%d].origin_tx_hash", index), item.OriginTxHash)
		if err != nil {
			return [32]byte{}, err
		}
		destination := shared.OptionalAbsentCanonicalFieldV1()
		if raw := item.GetDestinationTxHash(); item.XDestinationTxHash != nil {
			value, err := requireBridgeHash32(fmt.Sprintf("inflight[%d].destination_tx_hash", index), raw)
			if err != nil {
				return [32]byte{}, err
			}
			destination = shared.OptionalPresentCanonicalFieldV1(value)
		}
		if item.Disposition == BridgeInflightDispositionV1_BRIDGE_INFLIGHT_DISPOSITION_V1_UNSPECIFIED {
			return [32]byte{}, fmt.Errorf("inflight[%d].disposition must be explicit", index)
		}
		frames = append(frames, shared.NewCanonicalFrameBuilderV1().
			Raw(messageID, shared.EnumBE(uint32(item.Direction))).
			Nested(amount).
			Raw(originTx).
			Field(destination).
			Raw(shared.EnumBE(uint32(item.Disposition))).Build())
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainBridgeInflightManifestV1)).
		Raw(chain, shared.Uint64BE(cutoverProposalID), shared.Uint64BE(freezeHeight), shared.Uint32BE(uint32(len(items)))).
		Nested(shared.CanonicalRepeatedFramesV1(frames)).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

// MaxBridgeIntentFieldBytes bounds one field of the block-scoped bridge
// transfer intent. Addresses and message ids are far smaller; the bound exists
// so a malformed transient entry is rejected rather than allocated.
const MaxBridgeIntentFieldBytes = 256
