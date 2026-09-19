package types

import (
	"bytes"
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// The five bridge governance actions are x/gov internal actions: they have no
// Tx route, no AutoCLI entry and no signer field. Their digests are never
// persisted — keeper_api_contract.md §9.6c keys the bridge rows on proposal_id and
// has the Keeper recompute the digest inside the execution transaction, so
// replay protection is expressed by the expected_* fields rather than by a
// stored digest. These functions exist so the Keeper's recomputation and any
// auditor's reproduce the same bytes.

func MintBondActionDigest(chainID string, action MintBondV1) ([32]byte, error) {
	chain, err := requireBridgeText("chain_id", chainID)
	if err != nil {
		return [32]byte{}, err
	}
	if action.ProposalId == 0 {
		return [32]byte{}, fmt.Errorf("proposal_id must be non-zero")
	}
	operator, err := bridgeAddressBytes("target_operator", action.TargetOperator)
	if err != nil {
		return [32]byte{}, err
	}
	consensusKey, err := requireBytesLen("consensus_pubkey", action.ConsensusPubkey, 32)
	if err != nil {
		return [32]byte{}, err
	}
	disclosure, err := requireBridgeHash32("disclosure_digest", action.DisclosureDigest)
	if err != nil {
		return [32]byte{}, err
	}
	signer, err := RequireEVMAddress("bridge_signer_address_raw20", action.BridgeSignerAddressRaw20)
	if err != nil {
		return [32]byte{}, err
	}
	pop, err := requireBytesLen("bridge_signer_pop_signature", action.BridgeSignerPopSignature, BridgeSignerPoPSignatureLen)
	if err != nil {
		return [32]byte{}, err
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainMintBondV1)).Raw(
		chain, shared.Uint64BE(action.ProposalId), operator, consensusKey, disclosure, signer, pop,
	).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

func BurnBondActionDigest(chainID string, action BurnBondV1) ([32]byte, error) {
	chain, err := requireBridgeText("chain_id", chainID)
	if err != nil {
		return [32]byte{}, err
	}
	if action.ProposalId == 0 {
		return [32]byte{}, fmt.Errorf("proposal_id must be non-zero")
	}
	operator, err := bridgeAddressBytes("target_operator", action.TargetOperator)
	if err != nil {
		return [32]byte{}, err
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainBurnBondV1)).Raw(
		chain, shared.Uint64BE(action.ProposalId), operator,
	).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

// RotateBridgeSignerActionDigest commits the whole rotation including the PoP
// signature bytes. §1.3 rule 4 keeps signature bytes out of *business* digests;
// this is a governance action digest over the action's own literal content, and
// the action is exactly "install these bytes", so omitting them would let two
// different rotations share one digest.
func RotateBridgeSignerActionDigest(chainID string, action RotateBridgeSignerV1) ([32]byte, error) {
	chain, err := requireBridgeText("chain_id", chainID)
	if err != nil {
		return [32]byte{}, err
	}
	if action.ProposalId == 0 {
		return [32]byte{}, fmt.Errorf("proposal_id must be non-zero")
	}
	operator, err := bridgeAddressBytes("target_operator", action.TargetOperator)
	if err != nil {
		return [32]byte{}, err
	}
	if action.ExpectedKeyVersion == 0 {
		return [32]byte{}, fmt.Errorf("expected_key_version must be non-zero")
	}
	signer, err := RequireEVMAddress("next_bridge_signer_address_raw20", action.NextBridgeSignerAddressRaw20)
	if err != nil {
		return [32]byte{}, err
	}
	signature, err := requireBytesLen("next_bridge_signer_pop_signature", action.NextBridgeSignerPopSignature, BridgeSignerPoPSignatureLen)
	if err != nil {
		return [32]byte{}, err
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainRotateBridgeSignerV1)).Raw(
		chain, shared.Uint64BE(action.ProposalId), operator,
		shared.Uint64BE(action.ExpectedKeyVersion), signer, signature,
	).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

// BeginBridgeCutoverActionDigest expands next_signer_set into a count plus
// strictly ascending raw addresses and wraps threshold and reason; the digest
// order is deliberately not the message field order.
func BeginBridgeCutoverActionDigest(chainID string, action BeginBridgeCutoverV1) ([32]byte, error) {
	chain, err := requireBridgeText("chain_id", chainID)
	if err != nil {
		return [32]byte{}, err
	}
	if action.ProposalId == 0 {
		return [32]byte{}, fmt.Errorf("proposal_id must be non-zero")
	}
	currentIsm, err := RequireHyperlaneID("expected_current_ism_id", action.ExpectedCurrentIsmId)
	if err != nil {
		return [32]byte{}, err
	}
	currentSignerSet, err := requireBridgeHash32("expected_current_signer_set_hash", action.ExpectedCurrentSignerSetHash)
	if err != nil {
		return [32]byte{}, err
	}
	signers, err := canonicalBridgeSignerElements(action.NextSignerSet)
	if err != nil {
		return [32]byte{}, err
	}
	count := uint32(len(action.NextSignerSet))
	if err := RequireBridgeSignerCount(count); err != nil {
		return [32]byte{}, err
	}
	derived, err := BridgeThresholdV1(count)
	if err != nil {
		return [32]byte{}, err
	}
	if action.NextThreshold != derived {
		return [32]byte{}, fmt.Errorf("next_threshold %d is not the derived ceil(2n/3) value %d", action.NextThreshold, derived)
	}
	pauseTx, err := requireBridgeHash32("evm_ingress_pause_tx_hash", action.EvmIngressPauseTxHash)
	if err != nil {
		return [32]byte{}, err
	}
	if action.EvmIngressPauseBlockNumber == 0 {
		return [32]byte{}, fmt.Errorf("evm_ingress_pause_block_number must be non-zero")
	}
	pauseBlock, err := requireBridgeHash32("evm_ingress_pause_block_hash", action.EvmIngressPauseBlockHash)
	if err != nil {
		return [32]byte{}, err
	}
	lastDelivered := shared.OptionalAbsentCanonicalFieldV1()
	if action.XLastDeliveredInboundMessageId != nil {
		value, err := requireBridgeHash32("last_delivered_inbound_message_id", action.GetLastDeliveredInboundMessageId())
		if err != nil {
			return [32]byte{}, err
		}
		lastDelivered = shared.OptionalPresentCanonicalFieldV1(value)
	}
	if action.Reason == BridgeCutoverReasonV1_BRIDGE_CUTOVER_REASON_V1_UNSPECIFIED {
		return [32]byte{}, fmt.Errorf("cutover reason must be explicit")
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainBeginBridgeCutoverV1)).
		Raw(chain, shared.Uint64BE(action.ProposalId), currentIsm, currentSignerSet, shared.Uint32BE(count)).
		Nested(shared.CanonicalRepeatedFieldsV1(signers)).
		Raw(shared.Uint32BE(action.NextThreshold), pauseTx,
			shared.Uint64BE(action.EvmIngressPauseBlockNumber), pauseBlock).
		Field(lastDelivered).
		Raw(shared.EnumBE(uint32(action.Reason))).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

// ConfirmBridgeCutoverActionDigest carries evm_chain_id and evm_block_number as
// bare uint64; only inflight_message_count takes the uint32_be wrapper.
func ConfirmBridgeCutoverActionDigest(chainID string, action ConfirmBridgeCutoverV1) ([32]byte, error) {
	chain, err := requireBridgeText("chain_id", chainID)
	if err != nil {
		return [32]byte{}, err
	}
	if action.ProposalId == 0 || action.CutoverProposalId == 0 {
		return [32]byte{}, fmt.Errorf("proposal_id and cutover_proposal_id must be non-zero")
	}
	localIsm, err := RequireHyperlaneID("expected_local_ism_id", action.ExpectedLocalIsmId)
	if err != nil {
		return [32]byte{}, err
	}
	signerSet, err := requireBridgeHash32("expected_signer_set_hash", action.ExpectedSignerSetHash)
	if err != nil {
		return [32]byte{}, err
	}
	evmIsm, err := RequireEVMAddress("evm_ism_address", action.EvmIsmAddress)
	if err != nil {
		return [32]byte{}, err
	}
	evmSignerSet, err := requireBridgeHash32("evm_signer_set_hash", action.EvmSignerSetHash)
	if err != nil {
		return [32]byte{}, err
	}
	if action.EvmChainId == 0 || action.EvmBlockNumber == 0 {
		return [32]byte{}, fmt.Errorf("evm_chain_id and evm_block_number must be non-zero")
	}
	evmBlock, err := requireBridgeHash32("evm_block_hash", action.EvmBlockHash)
	if err != nil {
		return [32]byte{}, err
	}
	manifest, err := requireBridgeHash32("deployment_manifest_hash", action.DeploymentManifestHash)
	if err != nil {
		return [32]byte{}, err
	}
	inflight, err := requireBridgeHash32("inflight_manifest_hash", action.InflightManifestHash)
	if err != nil {
		return [32]byte{}, err
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainConfirmBridgeCutoverV1)).Raw(
		chain, shared.Uint64BE(action.ProposalId), shared.Uint64BE(action.CutoverProposalId),
		localIsm, signerSet, evmIsm, evmSignerSet,
		shared.Uint64BE(action.EvmChainId), shared.Uint64BE(action.EvmBlockNumber), evmBlock,
		manifest, inflight, shared.Uint32BE(action.InflightMessageCount),
	).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

// SetBridgeFreezeActionDigest keeps frozen as a bare bool byte; only
// expected_lifecycle is wrapped.
func SetBridgeFreezeActionDigest(chainID string, action SetBridgeFreezeV1) ([32]byte, error) {
	chain, err := requireBridgeText("chain_id", chainID)
	if err != nil {
		return [32]byte{}, err
	}
	if action.ProposalId == 0 {
		return [32]byte{}, fmt.Errorf("proposal_id must be non-zero")
	}
	if action.ExpectedLifecycle == BridgeLifecycleV1_BRIDGE_LIFECYCLE_V1_UNSPECIFIED {
		return [32]byte{}, fmt.Errorf("expected_lifecycle must be explicit")
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainSetBridgeFreezeV1)).Raw(
		chain, shared.Uint64BE(action.ProposalId), shared.BoolByte(action.Frozen),
		shared.EnumBE(uint32(action.ExpectedLifecycle)),
	).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

// SetBridgeLimitActionDigest carries all four limits as typed Amount frames with
// no integer wrapper.
func SetBridgeLimitActionDigest(chainID string, action SetBridgeLimitV1) ([32]byte, error) {
	chain, err := requireBridgeText("chain_id", chainID)
	if err != nil {
		return [32]byte{}, err
	}
	if action.ProposalId == 0 {
		return [32]byte{}, fmt.Errorf("proposal_id must be non-zero")
	}
	frames := make([]shared.CanonicalFrameV1, 0, 4)
	for _, limit := range []struct {
		name  string
		value shared.Amount
	}{
		{"expected_inbound_limit_per_epoch", action.ExpectedInboundLimitPerEpoch},
		{"expected_outbound_limit_per_epoch", action.ExpectedOutboundLimitPerEpoch},
		{"next_inbound_limit_per_epoch", action.NextInboundLimitPerEpoch},
		{"next_outbound_limit_per_epoch", action.NextOutboundLimitPerEpoch},
	} {
		frame, err := shared.CanonicalAmountFrameV1(limit.value)
		if err != nil {
			return [32]byte{}, fmt.Errorf("%s: %w", limit.name, err)
		}
		frames = append(frames, frame)
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainSetBridgeLimitV1)).
		Raw(chain, shared.Uint64BE(action.ProposalId)).
		Nested(frames...).Sum()
	if err != nil {
		return [32]byte{}, err
	}
	return [32]byte(digest), nil
}

// canonicalBridgeSignerElements validates the strictly ascending unique raw20
// ordering shared by the signer-set hash and the cutover action digest.
func canonicalBridgeSignerElements(signers [][]byte) ([]shared.CanonicalFieldV1, error) {
	elements := make([]shared.CanonicalFieldV1, 0, len(signers))
	for index, signer := range signers {
		raw, err := RequireEVMAddress(fmt.Sprintf("next_signer_set[%d]", index), signer)
		if err != nil {
			return nil, err
		}
		if index != 0 && bytes.Compare(signers[index-1], raw) >= 0 {
			return nil, fmt.Errorf("next_signer_set must ascend strictly by raw address bytes")
		}
		elements = append(elements, shared.RawCanonicalFieldV1(raw))
	}
	return elements, nil
}
