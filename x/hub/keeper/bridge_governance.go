package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// The five bridge actions below are x/gov internal actions: the API contract
// §9.6c gives them no Tx route, no AutoCLI entry and no signer field, so each
// takes the trusted execution locator instead of an authority argument. Every
// one recomputes its own action digest and compares the caller's expected_*
// fields against live state, which is how replay is expressed without a stored
// digest.

// ExecuteSetBridgeFreezeV1 is the only way the bridge freezes or thaws (§7.1).
// Unfreezing is not a state flip: §4.3 requires the EVM side to be confirmed and
// every invariant to hold first, and only then is the cutover row retired.
func (k Keeper) ExecuteSetBridgeFreezeV1(
	ctx context.Context,
	execution AcceptedGovernanceActionContext,
	action types.SetBridgeFreezeV1,
) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if err := k.requireAcceptedBridgeAction(execution, action.ProposalId); err != nil {
		return err
	}
	if _, err := types.SetBridgeFreezeActionDigest(sdkCtx.ChainID(), action); err != nil {
		return err
	}
	control, err := k.BridgeControl.Get(ctx)
	if err != nil {
		return fmt.Errorf("bridge control state is unavailable: %w", err)
	}
	if control.Lifecycle != action.ExpectedLifecycle {
		return fmt.Errorf("bridge lifecycle is %s, not the expected %s", control.Lifecycle, action.ExpectedLifecycle)
	}
	if control.Frozen == action.Frozen {
		// Exact replay: the state already says what the action asks for, so it
		// writes nothing and emits nothing rather than re-emitting code 122.
		return nil
	}
	height, err := currentBridgeHeight(ctx)
	if err != nil {
		return err
	}
	previous := control.Frozen
	if !action.Frozen {
		if err := k.completeBridgeCutoverOnUnfreeze(ctx); err != nil {
			return err
		}
		control.Lifecycle = types.BridgeLifecycleV1_BRIDGE_LIFECYCLE_V1_ACTIVE
	}
	control.Frozen = action.Frozen
	control.UpdatedHeight = height
	if err := k.BridgeControl.Set(ctx, control); err != nil {
		return err
	}
	mustEmitHubEvent(ctx, &types.EventBridgeFreezeChanged{
		ProposalId: action.ProposalId, OldFrozen: previous, NewFrozen: control.Frozen, Lifecycle: control.Lifecycle,
	})
	return nil
}

// completeBridgeCutoverOnUnfreeze is §4.3's closing gate. Thawing a bridge that
// is mid-cutover requires the EVM confirmation to be present, the in-flight
// manifest to be closed, and both signer views to agree again; only then is the
// cutover row deleted and the confirmed identity promoted.
func (k Keeper) completeBridgeCutoverOnUnfreeze(ctx context.Context) error {
	cutover, err := k.BridgeCutover.Get(ctx)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			// No cutover in progress: an ordinary emergency unfreeze.
			return k.requireBridgeInvariantsForUnfreeze(ctx)
		}
		return err
	}
	if cutover.XConfirmedEvmIsmAddress == nil {
		return fmt.Errorf("cannot unfreeze: the EVM side of cutover %d is not confirmed", cutover.ProposalId)
	}
	if cutover.XInflightManifestHash == nil || cutover.XInflightMessageCount == nil {
		return fmt.Errorf("cannot unfreeze: cutover %d has no closed in-flight manifest", cutover.ProposalId)
	}
	signerSet, err := k.BridgeSignerSet.Get(ctx)
	if err != nil {
		return err
	}
	signerSet.LocalIsmId = append([]byte(nil), cutover.NextLocalIsmId...)
	signerSet.LocalSignerSetHash = append([]byte(nil), cutover.NextSignerSetHash...)
	signerSet.ConfirmedEvmIsmAddress = append([]byte(nil), cutover.GetConfirmedEvmIsmAddress()...)
	signerSet.ConfirmedEvmSignerSetHash = append([]byte(nil), cutover.GetConfirmedEvmSignerSetHash()...)
	signerSet.Threshold = cutover.NextThreshold
	signerSet.SignerCount = cutover.NextSignerCount
	height, err := currentBridgeHeight(ctx)
	if err != nil {
		return err
	}
	signerSet.UpdatedHeight = height
	if err := k.BridgeSignerSet.Set(ctx, signerSet); err != nil {
		return err
	}
	if err := k.requireBridgeInvariantsForUnfreeze(ctx); err != nil {
		return err
	}
	return k.BridgeCutover.Remove(ctx)
}

// requireBridgeInvariantsForUnfreeze re-reads the authoritative rows rather than
// trusting what the cutover claimed: §4.3 only permits ACTIVE once I-BRIDGE-1
// and the supply identity both hold against live state.
func (k Keeper) requireBridgeInvariantsForUnfreeze(ctx context.Context) error {
	signerSet, err := k.BridgeSignerSet.Get(ctx)
	if err != nil {
		return err
	}
	if err := types.RequireBridgeSignerCount(signerSet.SignerCount); err != nil {
		return err
	}
	projection, err := k.BridgeSignerProjectionHash(ctx)
	if err != nil {
		return err
	}
	if !bytes.Equal(signerSet.LocalSignerSetHash, projection[:]) ||
		!bytes.Equal(signerSet.ConfirmedEvmSignerSetHash, projection[:]) {
		return fmt.Errorf("cannot unfreeze: I-BRIDGE-1 does not hold")
	}
	if err := k.ValidateBridgeUpstream(ctx); err != nil {
		return fmt.Errorf("cannot unfreeze: live Hyperlane state is not canonical: %w", err)
	}
	return k.EnsureBridgeSupplyInvariant(ctx)
}

// ExecuteSetBridgeLimitV1 schedules the next epoch's limits (§7.2). It never
// writes the active row: limits change at an epoch boundary so the window a
// transfer is measured against cannot move underneath it.
func (k Keeper) ExecuteSetBridgeLimitV1(
	ctx context.Context,
	execution AcceptedGovernanceActionContext,
	action types.SetBridgeLimitV1,
) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if err := k.requireAcceptedBridgeAction(execution, action.ProposalId); err != nil {
		return err
	}
	if _, err := types.SetBridgeLimitActionDigest(sdkCtx.ChainID(), action); err != nil {
		return err
	}
	limit, err := k.BridgeLimit.Get(ctx)
	if err != nil {
		return fmt.Errorf("bridge limit state is unavailable: %w", err)
	}
	if limit.InboundLimitPerEpoch != action.ExpectedInboundLimitPerEpoch ||
		limit.OutboundLimitPerEpoch != action.ExpectedOutboundLimitPerEpoch {
		return fmt.Errorf("bridge limits have moved since the proposal was written")
	}
	hardMax, err := shared.ParseAmount(limit.BridgeLimitHardMax)
	if err != nil {
		return err
	}
	if err := requireBridgeRuntimeLimitPair(action.NextInboundLimitPerEpoch, action.NextOutboundLimitPerEpoch, hardMax); err != nil {
		return err
	}
	epoch, err := k.currentBridgeEpoch(ctx)
	if err != nil {
		return err
	}
	effective := epoch + 1
	if existing, err := k.BridgePendingLimit.Get(ctx); err == nil {
		if existing.ProposalId == action.ProposalId &&
			existing.InboundLimitPerEpoch == action.NextInboundLimitPerEpoch &&
			existing.OutboundLimitPerEpoch == action.NextOutboundLimitPerEpoch {
			return nil
		}
		return fmt.Errorf("a different bridge limit change is already pending for epoch %d", existing.EffectiveEpoch)
	} else if !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	if err := k.BridgePendingLimit.Set(ctx, types.BridgePendingLimitState{
		ProposalId:            action.ProposalId,
		InboundLimitPerEpoch:  action.NextInboundLimitPerEpoch,
		OutboundLimitPerEpoch: action.NextOutboundLimitPerEpoch,
		EffectiveEpoch:        effective,
	}); err != nil {
		return err
	}
	mustEmitHubEvent(ctx, &types.EventBridgeLimitsScheduled{
		ProposalId: action.ProposalId, InboundLimit: action.NextInboundLimitPerEpoch,
		OutboundLimit: action.NextOutboundLimitPerEpoch, EffectiveEpoch: effective,
	})
	return nil
}

func requireBridgeRuntimeLimitPair(inbound, outbound shared.Amount, hardMax uint64) error {
	for name, value := range map[string]shared.Amount{
		"next_inbound_limit_per_epoch": inbound, "next_outbound_limit_per_epoch": outbound,
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

// ActivateDueBridgeLimit promotes a pending limit at the epoch boundary and then
// opens that epoch's usage window. §6.6a fixes this order: a transfer in the new
// epoch must be measured against the new limit, never the old one.
func (k Keeper) ActivateDueBridgeLimit(ctx context.Context, epoch uint64) error {
	pending, err := k.BridgePendingLimit.Get(ctx)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil
		}
		return err
	}
	if pending.EffectiveEpoch > epoch {
		return nil
	}
	limit, err := k.BridgeLimit.Get(ctx)
	if err != nil {
		return err
	}
	limit.InboundLimitPerEpoch = pending.InboundLimitPerEpoch
	limit.OutboundLimitPerEpoch = pending.OutboundLimitPerEpoch
	limit.EffectiveEpoch = pending.EffectiveEpoch
	if err := k.BridgeLimit.Set(ctx, limit); err != nil {
		return err
	}
	if err := k.BridgePendingLimit.Remove(ctx); err != nil {
		return err
	}
	mustEmitHubEvent(ctx, &types.EventBridgeLimitsActivated{
		InboundLimit: limit.InboundLimitPerEpoch, OutboundLimit: limit.OutboundLimitPerEpoch,
		EffectiveEpoch: limit.EffectiveEpoch,
	})
	return nil
}

// CloseBridgeUsageEpoch seals a finished usage window and schedules its prune.
// A closed window is never reopened, so a late transfer cannot be charged to an
// epoch whose budget was already accounted for.
func (k Keeper) CloseBridgeUsageEpoch(ctx context.Context, epoch uint64, retentionEpochs uint32) error {
	usage, err := k.BridgeEpochUsage.Get(ctx, epoch)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil
		}
		return err
	}
	if usage.Closed {
		return nil
	}
	if retentionEpochs == 0 {
		return fmt.Errorf("bridge_usage_retention_epochs must be positive")
	}
	usage.Closed = true
	usage.PruneEpoch = epoch + uint64(retentionEpochs)
	if err := k.BridgeEpochUsage.Set(ctx, epoch, usage); err != nil {
		return err
	}
	return k.BridgeEpochUsagePrune.Set(ctx, types.NewBridgeEpochUsagePruneKey(usage.PruneEpoch, epoch))
}

// PruneDueBridgeUsage visits at most limit index rows per call and never scans
// the whole table. A stale index row whose usage is gone still counts as visited
// so the cursor always advances (§6.6a).
func (k Keeper) PruneDueBridgeUsage(ctx context.Context, currentEpoch uint64, limit uint32) (uint32, error) {
	if limit == 0 {
		return 0, nil
	}
	iter, err := k.BridgeEpochUsagePrune.Iterate(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer iter.Close()
	type due struct{ pruneEpoch, rewardEpoch uint64 }
	pending := make([]due, 0, limit)
	for ; iter.Valid() && uint32(len(pending)) < limit; iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return 0, err
		}
		if key.K1() > currentEpoch {
			// The index ascends by due epoch, so the first not-yet-due row ends
			// the sweep.
			break
		}
		pending = append(pending, due{pruneEpoch: key.K1(), rewardEpoch: key.K2()})
	}
	visited := uint32(0)
	for _, row := range pending {
		visited++
		usage, err := k.BridgeEpochUsage.Get(ctx, row.rewardEpoch)
		switch {
		case err == nil:
			// The current window is never pruned, however old its index row says
			// it is.
			if !usage.Closed || row.rewardEpoch == currentEpoch {
				continue
			}
			if err := k.BridgeEpochUsage.Remove(ctx, row.rewardEpoch); err != nil {
				return visited, err
			}
		case errors.Is(err, collections.ErrNotFound):
		default:
			return visited, err
		}
		if err := k.BridgeEpochUsagePrune.Remove(ctx, types.NewBridgeEpochUsagePruneKey(row.pruneEpoch, row.rewardEpoch)); err != nil {
			return visited, err
		}
	}
	return visited, nil
}

// ExecuteRotateBridgeSignerV1 advances one operator's bridge key by exactly one
// version (§4.3). It never touches the operator's consensus identity or power,
// and it must run inside the same proposal and transaction as
// BeginBridgeCutoverV1 — the caller enforces that pairing, because only the
// proposal executor can see the whole item group.
func (k Keeper) ExecuteRotateBridgeSignerV1(
	ctx context.Context,
	execution AcceptedGovernanceActionContext,
	action types.RotateBridgeSignerV1,
) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if err := k.requireAcceptedBridgeAction(execution, action.ProposalId); err != nil {
		return err
	}
	if _, err := types.RotateBridgeSignerActionDigest(sdkCtx.ChainID(), action); err != nil {
		return err
	}
	current, err := k.ValidatorBridgeSigner.Get(ctx, action.TargetOperator)
	if err != nil {
		return fmt.Errorf("operator %s has no registered bridge signer", action.TargetOperator)
	}
	if current.KeyVersion != action.ExpectedKeyVersion {
		return fmt.Errorf("bridge signer key_version is %d, not the expected %d", current.KeyVersion, action.ExpectedKeyVersion)
	}
	next := current.KeyVersion + 1
	if next <= current.KeyVersion {
		return fmt.Errorf("bridge signer key_version overflows")
	}
	if err := types.VerifyBridgeSignerPoP(
		sdkCtx.ChainID(), action.TargetOperator,
		action.NextBridgeSignerAddressRaw20, action.NextBridgeSignerPopSignature, next,
	); err != nil {
		return err
	}
	// §4.1: current signer raw20 is globally unique. Rotating onto an address
	// another operator already holds would make the ISM set ambiguous.
	if err := k.requireUnusedBridgeSigner(ctx, action.TargetOperator, action.NextBridgeSignerAddressRaw20); err != nil {
		return err
	}
	height, err := currentBridgeHeight(ctx)
	if err != nil {
		return err
	}
	previous := append([]byte(nil), current.BridgeSignerAddressRaw20...)
	current.BridgeSignerAddressRaw20 = append([]byte(nil), action.NextBridgeSignerAddressRaw20...)
	current.PopSignature = append([]byte(nil), action.NextBridgeSignerPopSignature...)
	current.KeyVersion = next
	current.RegisteredHeight = height
	if err := k.ValidatorBridgeSigner.Set(ctx, action.TargetOperator, current); err != nil {
		return err
	}
	mustEmitHubEvent(ctx, &types.EventBridgeSignerRotated{
		Operator: action.TargetOperator, OldSigner: previous,
		NewSigner:  append([]byte(nil), current.BridgeSignerAddressRaw20...),
		KeyVersion: next, ProposalId: action.ProposalId,
	})
	return nil
}

func (k Keeper) requireUnusedBridgeSigner(ctx context.Context, operator string, signer []byte) error {
	iter, err := k.ValidatorBridgeSigner.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return err
		}
		if key == operator {
			continue
		}
		row, err := iter.Value()
		if err != nil {
			return err
		}
		if bytes.Equal(row.BridgeSignerAddressRaw20, signer) {
			return fmt.Errorf("bridge signer raw20 is already registered to %s", key)
		}
	}
	return nil
}

// ExecuteBeginBridgeCutoverV1 performs the TrueOpen-atomic half of §4.3: it freezes
// the bridge, records the next signer set and the EVM pause evidence, and moves
// the lifecycle to PENDING_EVM_CONFIRMATION. It deliberately does not claim the
// two sides switched atomically — the EVM ISM cannot change in this transaction,
// which is exactly why the bridge stays frozen until ConfirmBridgeCutoverV1.
func (k Keeper) ExecuteBeginBridgeCutoverV1(
	ctx context.Context,
	execution AcceptedGovernanceActionContext,
	action types.BeginBridgeCutoverV1,
	nextLocalIsmID []byte,
) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if err := k.requireAcceptedBridgeAction(execution, action.ProposalId); err != nil {
		return err
	}
	if _, err := types.BeginBridgeCutoverActionDigest(sdkCtx.ChainID(), action); err != nil {
		return err
	}
	if _, err := types.RequireHyperlaneID("next_local_ism_id", nextLocalIsmID); err != nil {
		return err
	}
	control, err := k.BridgeControl.Get(ctx)
	if err != nil {
		return fmt.Errorf("bridge control state is unavailable: %w", err)
	}
	if control.Lifecycle != types.BridgeLifecycleV1_BRIDGE_LIFECYCLE_V1_ACTIVE {
		return fmt.Errorf("a cutover may only begin from ACTIVE, not %s", control.Lifecycle)
	}
	if _, err := k.BridgeCutover.Get(ctx); err == nil {
		return fmt.Errorf("a cutover is already in progress")
	} else if !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	if _, err := k.BridgePendingLimit.Get(ctx); err == nil {
		return fmt.Errorf("a cutover may not begin while a limit change is pending")
	} else if !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	signerSet, err := k.BridgeSignerSet.Get(ctx)
	if err != nil {
		return err
	}
	if !bytes.Equal(signerSet.LocalIsmId, action.ExpectedCurrentIsmId) ||
		!bytes.Equal(signerSet.LocalSignerSetHash, action.ExpectedCurrentSignerSetHash) {
		return fmt.Errorf("the current ISM or signer set has moved since the proposal was written")
	}
	// §4.3: next_signer_set must be exactly the projection of the Validator
	// bridge signer table *after* the rotations in this same transaction, which
	// is why this is recomputed here rather than trusted from the action.
	projection, err := k.BridgeSignerProjectionHash(ctx)
	if err != nil {
		return err
	}
	nextHash, err := types.BridgeSignerSetHash(sdkCtx.ChainID(), action.NextThreshold, uint32(len(action.NextSignerSet)), action.NextSignerSet)
	if err != nil {
		return err
	}
	if !bytes.Equal(projection[:], nextHash[:]) {
		return fmt.Errorf("next_signer_set does not equal the post-rotation Validator bridge signer projection")
	}
	height, err := currentBridgeHeight(ctx)
	if err != nil {
		return err
	}
	cutover := types.BridgeCutoverState{
		ProposalId:                 action.ProposalId,
		NextLocalIsmId:             append([]byte(nil), nextLocalIsmID...),
		NextSignerSetHash:          nextHash[:],
		NextThreshold:              action.NextThreshold,
		NextSignerCount:            uint32(len(action.NextSignerSet)),
		EvmIngressPauseTxHash:      append([]byte(nil), action.EvmIngressPauseTxHash...),
		EvmIngressPauseBlockNumber: action.EvmIngressPauseBlockNumber,
		EvmIngressPauseBlockHash:   append([]byte(nil), action.EvmIngressPauseBlockHash...),
		FreezeHeight:               height,
	}
	if action.XLastDeliveredInboundMessageId != nil {
		cutover.XLastDeliveredInboundMessageId = &types.BridgeCutoverState_LastDeliveredInboundMessageId{
			LastDeliveredInboundMessageId: append([]byte(nil), action.GetLastDeliveredInboundMessageId()...),
		}
	}
	if err := k.BridgeCutover.Set(ctx, cutover); err != nil {
		return err
	}
	control.Lifecycle = types.BridgeLifecycleV1_BRIDGE_LIFECYCLE_V1_PENDING_EVM_CONFIRMATION
	control.Frozen = true
	control.UpdatedHeight = height
	if err := k.BridgeControl.Set(ctx, control); err != nil {
		return err
	}
	mustEmitHubEvent(ctx, &types.EventBridgeCutoverBegun{
		ProposalId: action.ProposalId, LocalIsmId: cutover.NextLocalIsmId,
		NextSignerSetHash: cutover.NextSignerSetHash, Threshold: cutover.NextThreshold,
		FreezeHeight: height, EvmIngressPauseBlockNumber: action.EvmIngressPauseBlockNumber,
		EvmIngressPauseBlockHash: append([]byte(nil), action.EvmIngressPauseBlockHash...),
	})
	return nil
}

// ExecuteConfirmBridgeCutoverV1 records the EVM side and closes the in-flight
// manifest (§4.3). It fills the cutover's optional tail and nothing else: the
// bridge stays frozen until a separate SetBridgeFreezeV1(false) re-checks every
// invariant, so confirmation alone can never reopen the channel.
func (k Keeper) ExecuteConfirmBridgeCutoverV1(
	ctx context.Context,
	execution AcceptedGovernanceActionContext,
	action types.ConfirmBridgeCutoverV1,
) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if err := k.requireAcceptedBridgeAction(execution, action.ProposalId); err != nil {
		return err
	}
	if _, err := types.ConfirmBridgeCutoverActionDigest(sdkCtx.ChainID(), action); err != nil {
		return err
	}
	cutover, err := k.BridgeCutover.Get(ctx)
	if err != nil {
		return fmt.Errorf("no cutover is awaiting EVM confirmation")
	}
	if cutover.ProposalId != action.CutoverProposalId {
		return fmt.Errorf("cutover in progress is proposal %d, not %d", cutover.ProposalId, action.CutoverProposalId)
	}
	if !bytes.Equal(cutover.NextLocalIsmId, action.ExpectedLocalIsmId) ||
		!bytes.Equal(cutover.NextSignerSetHash, action.ExpectedSignerSetHash) {
		return fmt.Errorf("the confirmation does not describe the cutover in progress")
	}
	// I-BRIDGE-1 requires all three views to agree, so the EVM signer set hash
	// the deployment reports must equal the one TrueOpen already committed to.
	if !bytes.Equal(action.EvmSignerSetHash, cutover.NextSignerSetHash) {
		return fmt.Errorf("the confirmed EVM signer set does not equal the local next signer set")
	}
	control, err := k.BridgeControl.Get(ctx)
	if err != nil {
		return err
	}
	if !bytes.Equal(control.DeploymentManifestHash, action.DeploymentManifestHash) {
		return fmt.Errorf("the confirmation references a different deployment manifest")
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	if cap := params.Bridge.MaxBridgeCutoverInflightMessages; cap != 0 && action.InflightMessageCount > cap {
		return fmt.Errorf("in-flight manifest lists %d messages, above the registered cap %d", action.InflightMessageCount, cap)
	}
	if cutover.XConfirmedEvmIsmAddress != nil {
		// Exact replay of the same confirmation is a NOOP; a different one is a
		// conflict, because the cutover may only be confirmed once.
		if bytes.Equal(cutover.GetConfirmedEvmIsmAddress(), action.EvmIsmAddress) &&
			bytes.Equal(cutover.GetConfirmedEvmSignerSetHash(), action.EvmSignerSetHash) &&
			cutover.GetEvmConfirmationChainId() == action.EvmChainId &&
			cutover.GetEvmConfirmationBlockNumber() == action.EvmBlockNumber &&
			bytes.Equal(cutover.GetEvmConfirmationBlockHash(), action.EvmBlockHash) &&
			bytes.Equal(cutover.GetInflightManifestHash(), action.InflightManifestHash) &&
			cutover.GetInflightMessageCount() == action.InflightMessageCount {
			return nil
		}
		return fmt.Errorf("cutover %d already carries a different EVM confirmation", cutover.ProposalId)
	}
	cutover.XConfirmedEvmIsmAddress = &types.BridgeCutoverState_ConfirmedEvmIsmAddress{
		ConfirmedEvmIsmAddress: append([]byte(nil), action.EvmIsmAddress...),
	}
	cutover.XConfirmedEvmSignerSetHash = &types.BridgeCutoverState_ConfirmedEvmSignerSetHash{
		ConfirmedEvmSignerSetHash: append([]byte(nil), action.EvmSignerSetHash...),
	}
	cutover.XEvmConfirmationChainId = &types.BridgeCutoverState_EvmConfirmationChainId{EvmConfirmationChainId: action.EvmChainId}
	cutover.XEvmConfirmationBlockNumber = &types.BridgeCutoverState_EvmConfirmationBlockNumber{EvmConfirmationBlockNumber: action.EvmBlockNumber}
	cutover.XEvmConfirmationBlockHash = &types.BridgeCutoverState_EvmConfirmationBlockHash{
		EvmConfirmationBlockHash: append([]byte(nil), action.EvmBlockHash...),
	}
	cutover.XInflightManifestHash = &types.BridgeCutoverState_InflightManifestHash{
		InflightManifestHash: append([]byte(nil), action.InflightManifestHash...),
	}
	cutover.XInflightMessageCount = &types.BridgeCutoverState_InflightMessageCount{InflightMessageCount: action.InflightMessageCount}
	if err := k.BridgeCutover.Set(ctx, cutover); err != nil {
		return err
	}
	mustEmitHubEvent(ctx, &types.EventBridgeCutoverConfirmed{
		ProposalId: action.ProposalId, EvmIsmAddress: append([]byte(nil), action.EvmIsmAddress...),
		EvmSignerSetHash: append([]byte(nil), action.EvmSignerSetHash...),
		EvmBlockNumber:   action.EvmBlockNumber, EvmBlockHash: append([]byte(nil), action.EvmBlockHash...),
		DeploymentManifestHash: append([]byte(nil), action.DeploymentManifestHash...),
		InflightManifestHash:   append([]byte(nil), action.InflightManifestHash...),
		InflightMessageCount:   action.InflightMessageCount,
	})
	return nil
}

// requireAcceptedBridgeAction rejects anything that did not arrive through an
// accepted x/gov item. The locator is compared against the typed action's own
// proposal_id so a caller cannot execute one proposal's action under another's
// authority.
func (k Keeper) requireAcceptedBridgeAction(execution AcceptedGovernanceActionContext, proposalID uint64) error {
	if !execution.Accepted {
		return fmt.Errorf("bridge governance action requires an accepted proposal")
	}
	if execution.ProposalID == 0 || execution.ProposalID != proposalID {
		return fmt.Errorf("bridge action proposal_id %d does not match the execution locator %d", proposalID, execution.ProposalID)
	}
	authority, _, err := k.requireCanonicalAddress("governance authority", execution.AuthorityAddress)
	if err != nil {
		return err
	}
	if !bytes.Equal(authority, k.authority) {
		return fmt.Errorf("bridge governance authority mismatch")
	}
	return nil
}
