package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// BridgeStatus is the §7.5 read-only join. Every field comes from the same query
// height, and the three invariants are *recomputed* rather than read back from a
// cached flag: a status that reported "ok" from a stored boolean would be exactly
// the reassurance an operator must not be given.
//
// §7.5 also forbids dressing a missing object up as a healthy zero value, so a
// chain without a bridge answers FailedPrecondition instead of an empty view.
func (q queryServer) BridgeStatus(ctx context.Context, req *types.QueryBridgeStatusRequest) (*types.QueryBridgeStatusResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request must not be nil")
	}
	route, err := q.k.BridgeRoute.Get(ctx)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, status.Error(codes.FailedPrecondition, "this chain has no canonical USDC bridge")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	signerSet, err := q.k.BridgeSignerSet.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "bridge signer set is unavailable")
	}
	control, err := q.k.BridgeControl.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "bridge control state is unavailable")
	}
	limit, err := q.k.BridgeLimit.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "bridge limit state is unavailable")
	}
	supply, err := q.k.BridgeSupply.Get(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "bridge supply state is unavailable")
	}
	bootstrap, err := q.k.ReadBridgeBootstrapValue(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, "bridge bootstrap state is unavailable")
	}
	if err := q.k.ValidateBridgeUpstream(ctx); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	epoch, err := q.k.currentBridgeEpoch(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	view := types.BridgeStatusViewV1{
		UsdcRouteId: append([]byte(nil), route.UsdcRouteId...),
		Lifecycle:   control.Lifecycle, Frozen: control.Frozen,
		HyperlaneLocalDomain: route.HyperlaneLocalDomain, OriginDomain: route.OriginDomain,
		OriginTokenAddress:      append([]byte(nil), route.OriginTokenAddress...),
		OriginWarpRouterAddress: append([]byte(nil), route.OriginWarpRouterAddress...),
		OriginMailboxAddress:    append([]byte(nil), route.OriginMailboxAddress...),
		OriginDecimals:          route.OriginDecimals, BusinessDenom: route.BusinessDenom,
		LocalMailboxId:            append([]byte(nil), route.LocalMailboxId...),
		LocalWarpTokenId:          append([]byte(nil), route.LocalWarpTokenId...),
		LocalOriginDenom:          route.LocalOriginDenom,
		LocalIsmId:                append([]byte(nil), signerSet.LocalIsmId...),
		LocalSignerSetHash:        append([]byte(nil), signerSet.LocalSignerSetHash...),
		ConfirmedEvmIsmAddress:    append([]byte(nil), signerSet.ConfirmedEvmIsmAddress...),
		ConfirmedEvmSignerSetHash: append([]byte(nil), signerSet.ConfirmedEvmSignerSetHash...),
		Threshold:                 signerSet.Threshold, SignerCount: signerSet.SignerCount,
		DeploymentManifestHash:      append([]byte(nil), control.DeploymentManifestHash...),
		ActiveInboundLimitPerEpoch:  limit.InboundLimitPerEpoch,
		ActiveOutboundLimitPerEpoch: limit.OutboundLimitPerEpoch,
		ActiveLimitEffectiveEpoch:   limit.EffectiveEpoch,
		CurrentRewardEpoch:          epoch,
		CurrentInboundUsed:          shared.NewAmount(0),
		CurrentOutboundUsed:         shared.NewAmount(0),
		GenesisAllocated:            supply.GenesisAllocated,
		CumulativeBridgeMinted:      supply.CumulativeBridgeMinted,
		CumulativeBridgeBurned:      supply.CumulativeBridgeBurned,
		BootstrapMode:               bootstrap.Mode,
	}
	if usage, err := q.k.BridgeEpochUsage.Get(ctx, epoch); err == nil {
		view.CurrentInboundUsed = usage.InboundUsed
		view.CurrentOutboundUsed = usage.OutboundUsed
	} else if !errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.Internal, err.Error())
	}

	bankSupply := q.k.bankKeeper.GetSupply(sdk.UnwrapSDKContext(ctx), route.BusinessDenom)
	if !bankSupply.Amount.IsUint64() {
		return nil, status.Error(codes.Internal, "business denom supply is outside uint64")
	}
	view.CurrentBankSupply = shared.NewAmount(bankSupply.Amount.Uint64())

	if pending, err := q.k.BridgePendingLimit.Get(ctx); err == nil {
		inbound, outbound := pending.InboundLimitPerEpoch, pending.OutboundLimitPerEpoch
		view.PendingInboundLimitPerEpoch = &inbound
		view.PendingOutboundLimitPerEpoch = &outbound
		view.XPendingLimitEffectiveEpoch = &types.BridgeStatusViewV1_PendingLimitEffectiveEpoch{
			PendingLimitEffectiveEpoch: pending.EffectiveEpoch,
		}
	} else if !errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if bootstrap.XMessageId != nil {
		amount := *bootstrap.Amount
		view.XBootstrapMessageId = &types.BridgeStatusViewV1_BootstrapMessageId{BootstrapMessageId: append([]byte(nil), bootstrap.GetMessageId()...)}
		view.XBootstrapRouteId = &types.BridgeStatusViewV1_BootstrapRouteId{BootstrapRouteId: append([]byte(nil), bootstrap.GetRouteId()...)}
		view.XBootstrapRecipient = &types.BridgeStatusViewV1_BootstrapRecipient{BootstrapRecipient: bootstrap.GetRecipient()}
		view.BootstrapAmount = &amount
		view.XBootstrapFeePayer = &types.BridgeStatusViewV1_BootstrapFeePayer{BootstrapFeePayer: bootstrap.GetFeePayer()}
		view.XBootstrapMaxGas = &types.BridgeStatusViewV1_BootstrapMaxGas{BootstrapMaxGas: bootstrap.GetMaxGas()}
	}
	if bootstrap.XConsumedHeight != nil {
		view.XBootstrapConsumedHeight = &types.BridgeStatusViewV1_BootstrapConsumedHeight{
			BootstrapConsumedHeight: bootstrap.GetConsumedHeight(),
		}
	}
	if cutover, err := q.k.BridgeCutover.Get(ctx); err == nil {
		view.XCutoverProposalId = &types.BridgeStatusViewV1_CutoverProposalId{CutoverProposalId: cutover.ProposalId}
		if cutover.XInflightManifestHash != nil {
			view.XInflightManifestHash = &types.BridgeStatusViewV1_InflightManifestHash{
				InflightManifestHash: append([]byte(nil), cutover.GetInflightManifestHash()...),
			}
			view.XInflightMessageCount = &types.BridgeStatusViewV1_InflightMessageCount{
				InflightMessageCount: cutover.GetInflightMessageCount(),
			}
		}
		if cutover.XEvmConfirmationBlockNumber != nil {
			view.XEvmConfirmationBlockNumber = &types.BridgeStatusViewV1_EvmConfirmationBlockNumber{
				EvmConfirmationBlockNumber: cutover.GetEvmConfirmationBlockNumber(),
			}
			view.XEvmConfirmationBlockHash = &types.BridgeStatusViewV1_EvmConfirmationBlockHash{
				EvmConfirmationBlockHash: append([]byte(nil), cutover.GetEvmConfirmationBlockHash()...),
			}
		}
	} else if !errors.Is(err, collections.ErrNotFound) {
		return nil, status.Error(codes.Internal, err.Error())
	}

	view.InvariantBridge_1Ok = q.k.bridgeSignerInvariantHolds(ctx, signerSet)
	view.InvariantBridge_2Ok = q.k.EnsureBridgeSupplyInvariant(ctx) == nil
	// I-BRIDGE-3 is "outside localnet, genesis_allocated must be 0". The query
	// reports the checkable half — whether any supply was allocated outside the
	// bridge — and leaves the network-class judgement to the reader.
	genesisAllocated, parseErr := shared.ParseAmount(supply.GenesisAllocated)
	view.InvariantBridge_3Ok = parseErr == nil && genesisAllocated == 0

	return &types.QueryBridgeStatusResponse{Status: view}, nil
}

// bridgeSignerInvariantHolds recomputes I-BRIDGE-1 without failing the query: a
// broken invariant is a reportable fact here, not a query error.
func (k Keeper) bridgeSignerInvariantHolds(ctx context.Context, signerSet types.BridgeSignerSetState) bool {
	if types.RequireBridgeSignerCount(signerSet.SignerCount) != nil {
		return false
	}
	projection, err := k.BridgeSignerProjectionHash(ctx)
	if err != nil {
		return false
	}
	return string(signerSet.LocalSignerSetHash) == string(projection[:]) &&
		string(signerSet.ConfirmedEvmSignerSetHash) == string(projection[:])
}
