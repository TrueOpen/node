package types

import (
	"bytes"
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// ValidateBridgeGenesis closes every Genesis gate that
// the bridge protocol place on the bridge's own
// rows. What it deliberately does *not* do is read
// the upstream Mailbox / HypToken / RemoteRouter objects: those live in the
// Hyperlane modules, are imported by their own InitGenesis, and are checked by
// Keeper.validateBridgeUpstreamGenesis once both sides are in the store. Keeping
// the pure document checks here means a malformed Genesis fails before any
// module writes anything.
func ValidateBridgeGenesis(chainID string, genesis BridgeGenesisV1, signers []ValidatorBridgeSignerState, params BridgeParamsV1) error {
	if genesis.SchemaVersion != 1 {
		return fmt.Errorf("bridge genesis schema_version must be 1")
	}
	routeID, err := USDCRouteID(chainID, genesis.Route)
	if err != nil {
		return fmt.Errorf("bridge route: %w", err)
	}
	if !bytes.Equal(genesis.Route.UsdcRouteId, routeID[:]) {
		return fmt.Errorf("bridge route usdc_route_id is not the hash of its own eleven frozen fields")
	}
	// The local identifiers must be real upstream HexAddresses or the guard's
	// comparison against the actual Mailbox/token can never succeed (DOC-012).
	if _, err := RequireHyperlaneID("local_mailbox_id", genesis.Route.LocalMailboxId); err != nil {
		return err
	}
	if _, err := RequireHyperlaneID("local_warp_token_id", genesis.Route.LocalWarpTokenId); err != nil {
		return err
	}
	if _, err := RequireHyperlaneID("local_ism_id", genesis.LocalIsmId); err != nil {
		return err
	}
	if _, err := RequireEVMAddress("confirmed_evm_ism_address", genesis.ConfirmedEvmIsmAddress); err != nil {
		return err
	}
	if err := requireBridgeGenesisHash("deployment_manifest_hash", genesis.DeploymentManifestHash); err != nil {
		return err
	}

	if err := validateBridgeSignerProjection(chainID, genesis, signers); err != nil {
		return err
	}
	switch genesis.Lifecycle {
	case BridgeLifecycleV1_BRIDGE_LIFECYCLE_V1_ACTIVE:
		if genesis.Cutover != nil {
			return fmt.Errorf("an ACTIVE bridge must not carry a cutover row")
		}
	case BridgeLifecycleV1_BRIDGE_LIFECYCLE_V1_PENDING_EVM_CONFIRMATION:
		// §4.3: the whole point of PENDING is that the bridge stays shut until the
		// EVM side is confirmed, so a thawed pending bridge is not a state the
		// import may produce.
		if !genesis.Frozen {
			return fmt.Errorf("a PENDING_EVM_CONFIRMATION bridge must be frozen")
		}
		if genesis.Cutover == nil {
			return fmt.Errorf("a PENDING_EVM_CONFIRMATION bridge must carry its cutover row")
		}
	default:
		return fmt.Errorf("bridge lifecycle must be ACTIVE or PENDING_EVM_CONFIRMATION")
	}
	if err := validateBridgeCutoverGenesis(genesis.Cutover, params); err != nil {
		return err
	}
	if err := validateBridgeLimitGenesis(genesis.Limits, genesis.PendingLimit, params); err != nil {
		return err
	}
	if err := validateBridgeSupplyGenesis(genesis.Supply); err != nil {
		return err
	}
	if err := validateBridgeBootstrapGenesis(chainID, genesis); err != nil {
		return err
	}
	return validateBridgeUsageGenesis(genesis.Usages)
}

func requireBridgeGenesisHash(field string, value []byte) error {
	_, err := requireBridgeHash32(field, value)
	return err
}

// validateBridgeSignerProjection is the Genesis half of I-BRIDGE-1: the frozen
// signer-set hash must be the hash of the Validator bridge signer table, and the
// confirmed EVM hash must equal it. §4.2 lets the bridge run only when all three
// views agree, and at Genesis the third view is the confirmed deployment.
func validateBridgeSignerProjection(chainID string, genesis BridgeGenesisV1, signers []ValidatorBridgeSignerState) error {
	if uint32(len(signers)) != genesis.SignerCount {
		return fmt.Errorf("bridge signer_count %d does not match the %d validator_bridge_signers rows", genesis.SignerCount, len(signers))
	}
	if err := RequireBridgeSignerCount(genesis.SignerCount); err != nil {
		return err
	}
	derived, err := BridgeThresholdV1(genesis.SignerCount)
	if err != nil {
		return err
	}
	if genesis.Threshold != derived {
		return fmt.Errorf("bridge threshold %d is not the derived ceil(2n/3) value %d", genesis.Threshold, derived)
	}
	projection, err := BridgeSignerProjection(chainID, signers)
	if err != nil {
		return err
	}
	if !bytes.Equal(genesis.LocalSignerSetHash, projection[:]) {
		return fmt.Errorf("local_signer_set_hash is not the hash of the validator_bridge_signers projection")
	}
	if !bytes.Equal(genesis.ConfirmedEvmSignerSetHash, projection[:]) {
		return fmt.Errorf("confirmed_evm_signer_set_hash does not equal the local projection (I-BRIDGE-1)")
	}
	return nil
}

// BridgeSignerProjection hashes the Validator bridge signer table the way
// §4.2 defines it: current signers only, ascending by raw address bytes, with
// the derived threshold. Every raw20 must be globally unique and every row must
// carry a PoP that recovers to its own signer, so a registration cannot smuggle
// in a key nobody controls.
func BridgeSignerProjection(chainID string, signers []ValidatorBridgeSignerState) ([32]byte, error) {
	raw := make([][]byte, 0, len(signers))
	seenOperators := make(map[string]struct{}, len(signers))
	seenSigners := make(map[string]struct{}, len(signers))
	for index, signer := range signers {
		if _, duplicate := seenOperators[signer.OperatorAddress]; duplicate {
			return [32]byte{}, fmt.Errorf("validator_bridge_signers has two rows for operator %s", signer.OperatorAddress)
		}
		seenOperators[signer.OperatorAddress] = struct{}{}
		address, err := RequireEVMAddress(fmt.Sprintf("validator_bridge_signers[%d].bridge_signer_address_raw20", index), signer.BridgeSignerAddressRaw20)
		if err != nil {
			return [32]byte{}, err
		}
		if _, duplicate := seenSigners[string(address)]; duplicate {
			return [32]byte{}, fmt.Errorf("bridge signer raw20 must be globally unique")
		}
		seenSigners[string(address)] = struct{}{}
		if signer.KeyVersion == 0 {
			return [32]byte{}, fmt.Errorf("validator_bridge_signers[%d].key_version must be greater than zero", index)
		}
		if err := VerifyBridgeSignerPoP(chainID, signer.OperatorAddress, address, signer.PopSignature, signer.KeyVersion); err != nil {
			return [32]byte{}, fmt.Errorf("validator_bridge_signers[%d]: %w", index, err)
		}
		raw = append(raw, address)
	}
	sortBridgeSigners(raw)
	count := uint32(len(raw))
	threshold, err := BridgeThresholdV1(count)
	if err != nil {
		return [32]byte{}, err
	}
	return BridgeSignerSetHash(chainID, threshold, count, raw)
}

// sortBridgeSigners orders by raw address bytes. Insertion sort keeps the
// comparison rule in one place and the sets are bounded by the validator count.
func sortBridgeSigners(signers [][]byte) {
	for i := 1; i < len(signers); i++ {
		for j := i; j > 0 && bytes.Compare(signers[j-1], signers[j]) > 0; j-- {
			signers[j-1], signers[j] = signers[j], signers[j-1]
		}
	}
}

func validateBridgeCutoverGenesis(cutover *BridgeCutoverGenesisV1, params BridgeParamsV1) error {
	if cutover == nil {
		return nil
	}
	if cutover.ProposalId == 0 || cutover.FreezeHeight == 0 {
		return fmt.Errorf("bridge cutover needs a proposal id and freeze height")
	}
	if _, err := RequireHyperlaneID("cutover next_local_ism_id", cutover.NextLocalIsmId); err != nil {
		return err
	}
	if err := requireBridgeGenesisHash("cutover next_signer_set_hash", cutover.NextSignerSetHash); err != nil {
		return err
	}
	if err := RequireBridgeSignerCount(cutover.NextSignerCount); err != nil {
		return err
	}
	derived, err := BridgeThresholdV1(cutover.NextSignerCount)
	if err != nil {
		return err
	}
	if cutover.NextThreshold != derived {
		return fmt.Errorf("cutover next_threshold %d is not the derived ceil(2n/3) value %d", cutover.NextThreshold, derived)
	}
	if err := requireBridgeGenesisHash("evm_ingress_pause_tx_hash", cutover.EvmIngressPauseTxHash); err != nil {
		return err
	}
	if cutover.EvmIngressPauseBlockNumber == 0 {
		return fmt.Errorf("evm_ingress_pause_block_number must be non-zero")
	}
	if err := requireBridgeGenesisHash("evm_ingress_pause_block_hash", cutover.EvmIngressPauseBlockHash); err != nil {
		return err
	}
	if count := cutover.GetInflightMessageCount(); cutover.XInflightMessageCount != nil {
		if params.MaxBridgeCutoverInflightMessages != 0 && count > params.MaxBridgeCutoverInflightMessages {
			return fmt.Errorf("cutover inflight_message_count %d exceeds max_bridge_cutover_inflight_messages %d",
				count, params.MaxBridgeCutoverInflightMessages)
		}
		if cutover.XInflightManifestHash == nil {
			return fmt.Errorf("cutover inflight_message_count without its manifest hash")
		}
	}
	if cutover.XInflightManifestHash != nil {
		if err := requireBridgeGenesisHash("inflight_manifest_hash", cutover.GetInflightManifestHash()); err != nil {
			return err
		}
		if cutover.XInflightMessageCount == nil {
			return fmt.Errorf("cutover inflight_manifest_hash without its message count")
		}
	}
	// §4.3: the EVM confirmation tail arrives as one group. A half-filled tail
	// would let unfreeze read a confirmation that was never actually approved.
	present := 0
	for _, set := range []bool{
		cutover.XConfirmedEvmIsmAddress != nil, cutover.XConfirmedEvmSignerSetHash != nil,
		cutover.XEvmConfirmationChainId != nil, cutover.XEvmConfirmationBlockNumber != nil,
		cutover.XEvmConfirmationBlockHash != nil,
	} {
		if set {
			present++
		}
	}
	if present != 0 && present != 5 {
		return fmt.Errorf("bridge cutover EVM confirmation fields must be all present or all absent")
	}
	if present == 5 {
		if _, err := RequireEVMAddress("confirmed_evm_ism_address", cutover.GetConfirmedEvmIsmAddress()); err != nil {
			return err
		}
		if err := requireBridgeGenesisHash("confirmed_evm_signer_set_hash", cutover.GetConfirmedEvmSignerSetHash()); err != nil {
			return err
		}
		if cutover.GetEvmConfirmationChainId() == 0 || cutover.GetEvmConfirmationBlockNumber() == 0 {
			return fmt.Errorf("EVM confirmation chain id and block number must be non-zero")
		}
		if err := requireBridgeGenesisHash("evm_confirmation_block_hash", cutover.GetEvmConfirmationBlockHash()); err != nil {
			return err
		}
	}
	return nil
}

func validateBridgeLimitGenesis(limits BridgeLimitGenesisV1, pending *BridgePendingLimitGenesisV1, params BridgeParamsV1) error {
	hardMax, err := shared.ParseAmount(limits.BridgeLimitHardMax)
	if err != nil {
		return fmt.Errorf("bridge_limit_hard_max: %w", err)
	}
	paramHardMax, err := shared.ParseAmount(params.BridgeLimitHardMax)
	if err != nil {
		return fmt.Errorf("params bridge_limit_hard_max: %w", err)
	}
	if hardMax == 0 || hardMax != paramHardMax {
		return fmt.Errorf("bridge_limit_hard_max must be non-zero and equal to the registered param")
	}
	if err := requireBridgeLimitPair("active", limits.InboundLimitPerEpoch, limits.OutboundLimitPerEpoch, hardMax); err != nil {
		return err
	}
	if pending == nil {
		return nil
	}
	if pending.ProposalId == 0 {
		return fmt.Errorf("pending bridge limit needs its proposal id")
	}
	if pending.EffectiveEpoch <= limits.EffectiveEpoch {
		return fmt.Errorf("pending bridge limit must take effect after the active one")
	}
	return requireBridgeLimitPair("pending", pending.InboundLimitPerEpoch, pending.OutboundLimitPerEpoch, hardMax)
}

// requireBridgeLimitPair enforces §7.2: a limit is a positive Amount bounded by
// the hard max. Zero is explicitly not "unlimited" — the limit exists to bound
// the loss when the signer threshold is stolen, so an unbounded limit would
// remove the only cap on that loss.
func requireBridgeLimitPair(label string, inbound, outbound shared.Amount, hardMax uint64) error {
	for name, value := range map[string]shared.Amount{
		label + " inbound_limit_per_epoch":  inbound,
		label + " outbound_limit_per_epoch": outbound,
	} {
		parsed, err := shared.ParseAmount(value)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if parsed == 0 {
			return fmt.Errorf("%s must be positive; 0 does not mean unlimited", name)
		}
		if parsed > hardMax {
			return fmt.Errorf("%s %d exceeds bridge_limit_hard_max %d", name, parsed, hardMax)
		}
	}
	return nil
}

func validateBridgeSupplyGenesis(supply BridgeSupplyGenesisV1) error {
	for name, value := range map[string]shared.Amount{
		"genesis_allocated":        supply.GenesisAllocated,
		"cumulative_bridge_minted": supply.CumulativeBridgeMinted,
		"cumulative_bridge_burned": supply.CumulativeBridgeBurned,
	} {
		if _, err := shared.ParseAmount(value); err != nil {
			return fmt.Errorf("bridge supply %s: %w", name, err)
		}
	}
	genesisAllocated, _ := shared.ParseAmount(supply.GenesisAllocated)
	minted, _ := shared.ParseAmount(supply.CumulativeBridgeMinted)
	burned, _ := shared.ParseAmount(supply.CumulativeBridgeBurned)
	if burned > minted && burned-minted > genesisAllocated {
		return fmt.Errorf("cumulative_bridge_burned %d exceeds genesis_allocated %d plus cumulative_bridge_minted %d",
			burned, genesisAllocated, minted)
	}
	return nil
}

// validateBridgeBootstrapGenesis enforces §5.3's three shapes. The binding
// fields are all-or-nothing because a partially bound bootstrap would let an
// arbitrary first message take the fee exemption.
func validateBridgeBootstrapGenesis(chainID string, genesis BridgeGenesisV1) error {
	bootstrap := genesis.Bootstrap
	present := 0
	for _, set := range []bool{
		bootstrap.XMessageId != nil, bootstrap.XRouteId != nil, bootstrap.XRecipient != nil,
		bootstrap.Amount != nil, bootstrap.XFeePayer != nil, bootstrap.XMaxGas != nil,
	} {
		if set {
			present++
		}
	}
	switch bootstrap.Mode {
	case BridgeBootstrapModeV1_BRIDGE_BOOTSTRAP_MODE_V1_DISABLED:
		if present != 0 || bootstrap.XConsumedHeight != nil {
			return fmt.Errorf("a DISABLED bootstrap must carry no binding fields")
		}
		return nil
	case BridgeBootstrapModeV1_BRIDGE_BOOTSTRAP_MODE_V1_ARMED:
		if bootstrap.XConsumedHeight != nil {
			return fmt.Errorf("an ARMED bootstrap must not carry consumed_height")
		}
	case BridgeBootstrapModeV1_BRIDGE_BOOTSTRAP_MODE_V1_CONSUMED:
		if bootstrap.XConsumedHeight == nil || bootstrap.GetConsumedHeight() == 0 {
			return fmt.Errorf("a CONSUMED bootstrap must retain its consumed_height")
		}
	default:
		return fmt.Errorf("bridge bootstrap mode must be DISABLED, ARMED or CONSUMED")
	}
	if present != 6 {
		return fmt.Errorf("an %s bootstrap must carry all six binding fields", bootstrap.Mode)
	}
	if err := requireBridgeGenesisHash("bootstrap message_id", bootstrap.GetMessageId()); err != nil {
		return err
	}
	if !bytes.Equal(bootstrap.GetRouteId(), genesis.Route.UsdcRouteId) {
		return fmt.Errorf("bootstrap route_id must be the frozen canonical route")
	}
	if _, err := bridgeAddressBytes("bootstrap recipient", bootstrap.GetRecipient()); err != nil {
		return err
	}
	if _, err := bridgeAddressBytes("bootstrap fee_payer", bootstrap.GetFeePayer()); err != nil {
		return err
	}
	amount, err := shared.ParseAmount(*bootstrap.Amount)
	if err != nil {
		return fmt.Errorf("bootstrap amount: %w", err)
	}
	if amount == 0 {
		return fmt.Errorf("bootstrap amount must be greater than zero")
	}
	if bootstrap.GetMaxGas() == 0 {
		return fmt.Errorf("bootstrap max_gas must be greater than zero")
	}
	_ = chainID
	return nil
}

// validateBridgeUsageGenesis mirrors §6.6a: a fresh Genesis carries no usage
// rows at all, and an export may only carry closed rows that each name their own
// prune epoch. A current, still-open epoch is not exportable state because the
// epoch it belongs to is decided by the importing chain's own clock.
func validateBridgeUsageGenesis(usages []BridgeEpochUsageGenesisV1) error {
	seen := make(map[uint64]struct{}, len(usages))
	for index, usage := range usages {
		if _, duplicate := seen[usage.RewardEpoch]; duplicate {
			return fmt.Errorf("bridge usage epoch %d appears twice", usage.RewardEpoch)
		}
		seen[usage.RewardEpoch] = struct{}{}
		for name, value := range map[string]shared.Amount{
			"inbound_used": usage.InboundUsed, "outbound_used": usage.OutboundUsed,
		} {
			if _, err := shared.ParseAmount(value); err != nil {
				return fmt.Errorf("bridge usage[%d] %s: %w", index, name, err)
			}
		}
		if !usage.Closed {
			return fmt.Errorf("bridge usage[%d] is still open; only closed epochs are exportable", index)
		}
		if usage.XPruneEpoch == nil || usage.GetPruneEpoch() <= usage.RewardEpoch {
			return fmt.Errorf("bridge usage[%d] must name a prune epoch after its own", index)
		}
	}
	return nil
}
