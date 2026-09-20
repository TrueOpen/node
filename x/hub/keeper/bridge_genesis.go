package keeper

import (
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// initBridgeGenesis writes the TrueOpen-owned bridge rows. The upstream Hyperlane
// objects the route points at are imported by their own modules; the cross-check
// that they actually match this route runs in ValidateBridgeUpstream, after both
// sides are in the store.
func (k Keeper) initBridgeGenesis(ctx context.Context, genState types.GenesisState) error {
	chainID := sdk.UnwrapSDKContext(ctx).ChainID()
	genesis := genState.Bridge
	// schema_version 0 is the one shape that means "this chain has no bridge":
	// the whole section is absent. Phase 0 production Genesis must configure one
	// because business_denom can only originate at the bridge, but that is #163's
	// acceptance gate — here a chain without the section simply writes no bridge
	// rows rather than failing a document that never claimed to have a bridge.
	if genesis.SchemaVersion == 0 && len(genesis.Route.UsdcRouteId) == 0 {
		if len(genState.ValidatorBridgeSigners) != 0 {
			return fmt.Errorf("bridge genesis: validator_bridge_signers present without a bridge section")
		}
		return nil
	}
	if genesis.Route.BusinessDenom != genState.Params.Phase0.BusinessDenom {
		return fmt.Errorf("bridge genesis: route business_denom %q does not equal Phase0Params business_denom %q",
			genesis.Route.BusinessDenom, genState.Params.Phase0.BusinessDenom)
	}
	if err := types.ValidateBridgeGenesis(chainID, genesis, genState.ValidatorBridgeSigners, genState.Params.Bridge); err != nil {
		return fmt.Errorf("bridge genesis: %w", err)
	}
	for _, signer := range genState.ValidatorBridgeSigners {
		if err := k.ValidatorBridgeSigner.Set(ctx, signer.OperatorAddress, signer); err != nil {
			return err
		}
	}
	if err := k.BridgeRoute.Set(ctx, types.BridgeRouteState{
		UsdcRouteId:             append([]byte(nil), genesis.Route.UsdcRouteId...),
		HyperlaneLocalDomain:    genesis.Route.HyperlaneLocalDomain,
		OriginDomain:            genesis.Route.OriginDomain,
		OriginTokenAddress:      append([]byte(nil), genesis.Route.OriginTokenAddress...),
		OriginWarpRouterAddress: append([]byte(nil), genesis.Route.OriginWarpRouterAddress...),
		OriginMailboxAddress:    append([]byte(nil), genesis.Route.OriginMailboxAddress...),
		OriginDecimals:          genesis.Route.OriginDecimals,
		BusinessDenom:           genesis.Route.BusinessDenom,
		LocalMailboxId:          append([]byte(nil), genesis.Route.LocalMailboxId...),
		LocalWarpTokenId:        append([]byte(nil), genesis.Route.LocalWarpTokenId...),
		LocalOriginDenom:        genesis.Route.LocalOriginDenom,
	}); err != nil {
		return err
	}
	if err := k.BridgeSignerSet.Set(ctx, types.BridgeSignerSetState{
		LocalIsmId:                append([]byte(nil), genesis.LocalIsmId...),
		LocalSignerSetHash:        append([]byte(nil), genesis.LocalSignerSetHash...),
		ConfirmedEvmIsmAddress:    append([]byte(nil), genesis.ConfirmedEvmIsmAddress...),
		ConfirmedEvmSignerSetHash: append([]byte(nil), genesis.ConfirmedEvmSignerSetHash...),
		Threshold:                 genesis.Threshold,
		SignerCount:               genesis.SignerCount,
	}); err != nil {
		return err
	}
	if err := k.BridgeControl.Set(ctx, types.BridgeControlState{
		Lifecycle:              genesis.Lifecycle,
		Frozen:                 genesis.Frozen,
		DeploymentManifestHash: append([]byte(nil), genesis.DeploymentManifestHash...),
	}); err != nil {
		return err
	}
	if genesis.Cutover != nil {
		if err := k.BridgeCutover.Set(ctx, bridgeCutoverStateFromGenesis(*genesis.Cutover)); err != nil {
			return err
		}
	}
	if err := k.BridgeLimit.Set(ctx, types.BridgeLimitState{
		BridgeLimitHardMax:    genesis.Limits.BridgeLimitHardMax,
		InboundLimitPerEpoch:  genesis.Limits.InboundLimitPerEpoch,
		OutboundLimitPerEpoch: genesis.Limits.OutboundLimitPerEpoch,
		EffectiveEpoch:        genesis.Limits.EffectiveEpoch,
	}); err != nil {
		return err
	}
	if genesis.PendingLimit != nil {
		if err := k.BridgePendingLimit.Set(ctx, types.BridgePendingLimitState{
			ProposalId:            genesis.PendingLimit.ProposalId,
			InboundLimitPerEpoch:  genesis.PendingLimit.InboundLimitPerEpoch,
			OutboundLimitPerEpoch: genesis.PendingLimit.OutboundLimitPerEpoch,
			EffectiveEpoch:        genesis.PendingLimit.EffectiveEpoch,
		}); err != nil {
			return err
		}
	}
	if err := k.BridgeSupply.Set(ctx, types.BridgeSupplyState{
		GenesisAllocated:       genesis.Supply.GenesisAllocated,
		CumulativeBridgeMinted: genesis.Supply.CumulativeBridgeMinted,
		CumulativeBridgeBurned: genesis.Supply.CumulativeBridgeBurned,
	}); err != nil {
		return err
	}
	if err := k.BridgeBootstrap.Set(ctx, bridgeBootstrapStateFromGenesis(genesis.Bootstrap)); err != nil {
		return err
	}
	for _, usage := range genesis.Usages {
		state := types.BridgeEpochUsageState{
			RewardEpoch:  usage.RewardEpoch,
			InboundUsed:  usage.InboundUsed,
			OutboundUsed: usage.OutboundUsed,
			Closed:       usage.Closed,
			PruneEpoch:   usage.GetPruneEpoch(),
		}
		if err := k.BridgeEpochUsage.Set(ctx, usage.RewardEpoch, state); err != nil {
			return err
		}
		// The prune index is derived, so it is rebuilt here rather than imported;
		// an exported document that disagreed with its own usage rows could
		// otherwise resurrect a schedule the rows no longer justify.
		if err := k.BridgeEpochUsagePrune.Set(ctx, types.NewBridgeEpochUsagePruneKey(state.PruneEpoch, state.RewardEpoch)); err != nil {
			return err
		}
	}
	if err := k.EnsureBridgeSupplyInvariant(ctx); err != nil {
		return fmt.Errorf("bridge genesis supply invariant: %w", err)
	}
	return nil
}

func bridgeCutoverStateFromGenesis(cutover types.BridgeCutoverGenesisV1) types.BridgeCutoverState {
	state := types.BridgeCutoverState{
		ProposalId:                 cutover.ProposalId,
		NextLocalIsmId:             append([]byte(nil), cutover.NextLocalIsmId...),
		NextSignerSetHash:          append([]byte(nil), cutover.NextSignerSetHash...),
		NextThreshold:              cutover.NextThreshold,
		NextSignerCount:            cutover.NextSignerCount,
		EvmIngressPauseTxHash:      append([]byte(nil), cutover.EvmIngressPauseTxHash...),
		EvmIngressPauseBlockNumber: cutover.EvmIngressPauseBlockNumber,
		EvmIngressPauseBlockHash:   append([]byte(nil), cutover.EvmIngressPauseBlockHash...),
		FreezeHeight:               cutover.FreezeHeight,
	}
	if cutover.XLastDeliveredInboundMessageId != nil {
		state.XLastDeliveredInboundMessageId = &types.BridgeCutoverState_LastDeliveredInboundMessageId{
			LastDeliveredInboundMessageId: append([]byte(nil), cutover.GetLastDeliveredInboundMessageId()...),
		}
	}
	if cutover.XInflightManifestHash != nil {
		state.XInflightManifestHash = &types.BridgeCutoverState_InflightManifestHash{
			InflightManifestHash: append([]byte(nil), cutover.GetInflightManifestHash()...),
		}
	}
	if cutover.XInflightMessageCount != nil {
		state.XInflightMessageCount = &types.BridgeCutoverState_InflightMessageCount{
			InflightMessageCount: cutover.GetInflightMessageCount(),
		}
	}
	if cutover.XConfirmedEvmIsmAddress != nil {
		state.XConfirmedEvmIsmAddress = &types.BridgeCutoverState_ConfirmedEvmIsmAddress{
			ConfirmedEvmIsmAddress: append([]byte(nil), cutover.GetConfirmedEvmIsmAddress()...),
		}
		state.XConfirmedEvmSignerSetHash = &types.BridgeCutoverState_ConfirmedEvmSignerSetHash{
			ConfirmedEvmSignerSetHash: append([]byte(nil), cutover.GetConfirmedEvmSignerSetHash()...),
		}
		state.XEvmConfirmationChainId = &types.BridgeCutoverState_EvmConfirmationChainId{
			EvmConfirmationChainId: cutover.GetEvmConfirmationChainId(),
		}
		state.XEvmConfirmationBlockNumber = &types.BridgeCutoverState_EvmConfirmationBlockNumber{
			EvmConfirmationBlockNumber: cutover.GetEvmConfirmationBlockNumber(),
		}
		state.XEvmConfirmationBlockHash = &types.BridgeCutoverState_EvmConfirmationBlockHash{
			EvmConfirmationBlockHash: append([]byte(nil), cutover.GetEvmConfirmationBlockHash()...),
		}
	}
	return state
}

func bridgeCutoverGenesisFromState(cutover types.BridgeCutoverState) *types.BridgeCutoverGenesisV1 {
	genesis := &types.BridgeCutoverGenesisV1{
		ProposalId:                 cutover.ProposalId,
		NextLocalIsmId:             append([]byte(nil), cutover.NextLocalIsmId...),
		NextSignerSetHash:          append([]byte(nil), cutover.NextSignerSetHash...),
		NextThreshold:              cutover.NextThreshold,
		NextSignerCount:            cutover.NextSignerCount,
		EvmIngressPauseTxHash:      append([]byte(nil), cutover.EvmIngressPauseTxHash...),
		EvmIngressPauseBlockNumber: cutover.EvmIngressPauseBlockNumber,
		EvmIngressPauseBlockHash:   append([]byte(nil), cutover.EvmIngressPauseBlockHash...),
		FreezeHeight:               cutover.FreezeHeight,
	}
	if cutover.XLastDeliveredInboundMessageId != nil {
		genesis.XLastDeliveredInboundMessageId = &types.BridgeCutoverGenesisV1_LastDeliveredInboundMessageId{
			LastDeliveredInboundMessageId: append([]byte(nil), cutover.GetLastDeliveredInboundMessageId()...),
		}
	}
	if cutover.XInflightManifestHash != nil {
		genesis.XInflightManifestHash = &types.BridgeCutoverGenesisV1_InflightManifestHash{
			InflightManifestHash: append([]byte(nil), cutover.GetInflightManifestHash()...),
		}
	}
	if cutover.XInflightMessageCount != nil {
		genesis.XInflightMessageCount = &types.BridgeCutoverGenesisV1_InflightMessageCount{
			InflightMessageCount: cutover.GetInflightMessageCount(),
		}
	}
	if cutover.XConfirmedEvmIsmAddress != nil {
		genesis.XConfirmedEvmIsmAddress = &types.BridgeCutoverGenesisV1_ConfirmedEvmIsmAddress{
			ConfirmedEvmIsmAddress: append([]byte(nil), cutover.GetConfirmedEvmIsmAddress()...),
		}
		genesis.XConfirmedEvmSignerSetHash = &types.BridgeCutoverGenesisV1_ConfirmedEvmSignerSetHash{
			ConfirmedEvmSignerSetHash: append([]byte(nil), cutover.GetConfirmedEvmSignerSetHash()...),
		}
		genesis.XEvmConfirmationChainId = &types.BridgeCutoverGenesisV1_EvmConfirmationChainId{
			EvmConfirmationChainId: cutover.GetEvmConfirmationChainId(),
		}
		genesis.XEvmConfirmationBlockNumber = &types.BridgeCutoverGenesisV1_EvmConfirmationBlockNumber{
			EvmConfirmationBlockNumber: cutover.GetEvmConfirmationBlockNumber(),
		}
		genesis.XEvmConfirmationBlockHash = &types.BridgeCutoverGenesisV1_EvmConfirmationBlockHash{
			EvmConfirmationBlockHash: append([]byte(nil), cutover.GetEvmConfirmationBlockHash()...),
		}
	}
	return genesis
}

func bridgeBootstrapStateFromGenesis(bootstrap types.BridgeBootstrapGenesisV1) types.BridgeBootstrapState {
	state := types.BridgeBootstrapState{Mode: bootstrap.Mode}
	if bootstrap.XMessageId != nil {
		state.XMessageId = &types.BridgeBootstrapState_MessageId{MessageId: append([]byte(nil), bootstrap.GetMessageId()...)}
		state.XRouteId = &types.BridgeBootstrapState_RouteId{RouteId: append([]byte(nil), bootstrap.GetRouteId()...)}
		state.XRecipient = &types.BridgeBootstrapState_Recipient{Recipient: bootstrap.GetRecipient()}
		amount := *bootstrap.Amount
		state.Amount = &amount
		state.XFeePayer = &types.BridgeBootstrapState_FeePayer{FeePayer: bootstrap.GetFeePayer()}
		state.XMaxGas = &types.BridgeBootstrapState_MaxGas{MaxGas: bootstrap.GetMaxGas()}
	}
	if bootstrap.XConsumedHeight != nil {
		state.XConsumedHeight = &types.BridgeBootstrapState_ConsumedHeight{ConsumedHeight: bootstrap.GetConsumedHeight()}
	}
	return state
}

func bridgeBootstrapGenesisFromState(bootstrap types.BridgeBootstrapState) types.BridgeBootstrapGenesisV1 {
	genesis := types.BridgeBootstrapGenesisV1{Mode: bootstrap.Mode}
	if bootstrap.XMessageId != nil {
		genesis.XMessageId = &types.BridgeBootstrapGenesisV1_MessageId{MessageId: append([]byte(nil), bootstrap.GetMessageId()...)}
		genesis.XRouteId = &types.BridgeBootstrapGenesisV1_RouteId{RouteId: append([]byte(nil), bootstrap.GetRouteId()...)}
		genesis.XRecipient = &types.BridgeBootstrapGenesisV1_Recipient{Recipient: bootstrap.GetRecipient()}
		amount := *bootstrap.Amount
		genesis.Amount = &amount
		genesis.XFeePayer = &types.BridgeBootstrapGenesisV1_FeePayer{FeePayer: bootstrap.GetFeePayer()}
		genesis.XMaxGas = &types.BridgeBootstrapGenesisV1_MaxGas{MaxGas: bootstrap.GetMaxGas()}
	}
	if bootstrap.XConsumedHeight != nil {
		genesis.XConsumedHeight = &types.BridgeBootstrapGenesisV1_ConsumedHeight{ConsumedHeight: bootstrap.GetConsumedHeight()}
	}
	return genesis
}

// exportBridgeGenesis reproduces the imported document. §5.2 is explicit that
// the three cumulative supply counters travel verbatim: re-deriving
// genesis_allocated from the current supply would silently reclassify every
// bridged voucher as a genesis allocation and make I-BRIDGE-3 pass on a chain
// where it should fail.
func (k Keeper) exportBridgeGenesis(ctx context.Context, genesis *types.GenesisState) error {
	route, err := k.BridgeRoute.Get(ctx)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			// A chain without a bridge exports the default empty section rather
			// than a half-built one.
			return nil
		}
		return err
	}
	signerSet, err := k.BridgeSignerSet.Get(ctx)
	if err != nil {
		return err
	}
	control, err := k.BridgeControl.Get(ctx)
	if err != nil {
		return err
	}
	limit, err := k.BridgeLimit.Get(ctx)
	if err != nil {
		return err
	}
	supply, err := k.BridgeSupply.Get(ctx)
	if err != nil {
		return err
	}
	bootstrap, err := k.BridgeBootstrap.Get(ctx)
	if err != nil {
		return err
	}
	genesis.Bridge = types.BridgeGenesisV1{
		SchemaVersion: 1,
		Route: types.BridgeRouteV1{
			UsdcRouteId:             append([]byte(nil), route.UsdcRouteId...),
			HyperlaneLocalDomain:    route.HyperlaneLocalDomain,
			OriginDomain:            route.OriginDomain,
			OriginTokenAddress:      append([]byte(nil), route.OriginTokenAddress...),
			OriginWarpRouterAddress: append([]byte(nil), route.OriginWarpRouterAddress...),
			OriginMailboxAddress:    append([]byte(nil), route.OriginMailboxAddress...),
			OriginDecimals:          route.OriginDecimals,
			BusinessDenom:           route.BusinessDenom,
			LocalMailboxId:          append([]byte(nil), route.LocalMailboxId...),
			LocalWarpTokenId:        append([]byte(nil), route.LocalWarpTokenId...),
			LocalOriginDenom:        route.LocalOriginDenom,
		},
		LocalIsmId:                append([]byte(nil), signerSet.LocalIsmId...),
		LocalSignerSetHash:        append([]byte(nil), signerSet.LocalSignerSetHash...),
		ConfirmedEvmIsmAddress:    append([]byte(nil), signerSet.ConfirmedEvmIsmAddress...),
		ConfirmedEvmSignerSetHash: append([]byte(nil), signerSet.ConfirmedEvmSignerSetHash...),
		Threshold:                 signerSet.Threshold,
		SignerCount:               signerSet.SignerCount,
		Lifecycle:                 control.Lifecycle,
		Frozen:                    control.Frozen,
		DeploymentManifestHash:    append([]byte(nil), control.DeploymentManifestHash...),
		Limits:                    types.BridgeLimitGenesisV1(limit),
		Supply:                    types.BridgeSupplyGenesisV1(supply),
		Bootstrap:                 bridgeBootstrapGenesisFromState(bootstrap),
	}
	if pending, err := k.BridgePendingLimit.Get(ctx); err == nil {
		genesis.Bridge.PendingLimit = &types.BridgePendingLimitGenesisV1{
			ProposalId:            pending.ProposalId,
			InboundLimitPerEpoch:  pending.InboundLimitPerEpoch,
			OutboundLimitPerEpoch: pending.OutboundLimitPerEpoch,
			EffectiveEpoch:        pending.EffectiveEpoch,
		}
	} else if !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	if cutover, err := k.BridgeCutover.Get(ctx); err == nil {
		genesis.Bridge.Cutover = bridgeCutoverGenesisFromState(cutover)
	} else if !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	genesis.ValidatorBridgeSigners, err = collectMapValues[string, types.ValidatorBridgeSignerState](ctx, k.ValidatorBridgeSigner)
	if err != nil {
		return err
	}
	usages, err := collectMapValues[uint64, types.BridgeEpochUsageState](ctx, k.BridgeEpochUsage)
	if err != nil {
		return err
	}
	genesis.Bridge.Usages = make([]types.BridgeEpochUsageGenesisV1, 0, len(usages))
	for _, usage := range usages {
		// §6.6a: only closed epochs are exportable. The current window belongs to
		// the exporting chain's clock, so carrying it would import a usage figure
		// for an epoch the new chain has not reached.
		if !usage.Closed {
			continue
		}
		genesis.Bridge.Usages = append(genesis.Bridge.Usages, types.BridgeEpochUsageGenesisV1{
			RewardEpoch:  usage.RewardEpoch,
			InboundUsed:  usage.InboundUsed,
			OutboundUsed: usage.OutboundUsed,
			Closed:       true,
			XPruneEpoch:  &types.BridgeEpochUsageGenesisV1_PruneEpoch{PruneEpoch: usage.PruneEpoch},
		})
	}
	return nil
}

// EnsureBridgeSupplyInvariant recomputes I-BRIDGE-2 and I-BRIDGE-3 from
// authoritative state: the bank's actual business_denom supply against the three
// persisted counters. Every successful bridge handler re-runs it, and so does
// export, so a drift can never be carried across a restart.
func (k Keeper) EnsureBridgeSupplyInvariant(ctx context.Context) error {
	route, err := k.BridgeRoute.Get(ctx)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil
		}
		return err
	}
	supply, err := k.BridgeSupply.Get(ctx)
	if err != nil {
		return err
	}
	genesisAllocated, err := shared.ParseAmount(supply.GenesisAllocated)
	if err != nil {
		return fmt.Errorf("genesis_allocated: %w", err)
	}
	minted, err := shared.ParseAmount(supply.CumulativeBridgeMinted)
	if err != nil {
		return fmt.Errorf("cumulative_bridge_minted: %w", err)
	}
	burned, err := shared.ParseAmount(supply.CumulativeBridgeBurned)
	if err != nil {
		return fmt.Errorf("cumulative_bridge_burned: %w", err)
	}
	expected, err := expectedBridgeSupply(genesisAllocated, minted, burned)
	if err != nil {
		return fmt.Errorf("I-BRIDGE-2 broken: %w", err)
	}
	actual := k.bankKeeper.GetSupply(sdk.UnwrapSDKContext(ctx), route.BusinessDenom)
	if !actual.Amount.IsUint64() || actual.Amount.Uint64() != expected {
		return fmt.Errorf("I-BRIDGE-2 broken: bank supply of %s is %s but minted-burned+genesis is %d",
			route.BusinessDenom, actual.Amount, expected)
	}
	return nil
}

func expectedBridgeSupply(genesisAllocated, minted, burned uint64) (uint64, error) {
	if burned > genesisAllocated {
		bridgedBurn := burned - genesisAllocated
		if bridgedBurn > minted {
			return 0, fmt.Errorf("burned %d exceeds %d genesis allocated plus %d minted", burned, genesisAllocated, minted)
		}
		return minted - bridgedBurn, nil
	}
	expected, overflow := checkedBridgeAdd(genesisAllocated-burned, minted)
	if overflow {
		return 0, fmt.Errorf("expected supply overflows")
	}
	return expected, nil
}

func checkedBridgeAdd(left, right uint64) (uint64, bool) {
	sum := left + right
	return sum, sum < left
}
