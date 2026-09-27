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

// BridgeDirection names which half of the guard a transfer is on. Keeping one
// enum rather than two near-identical code paths is what makes the inbound and
// outbound limit, usage and supply rules provably the same rules.
type BridgeDirection uint8

const (
	BridgeInbound BridgeDirection = iota + 1
	BridgeOutbound
)

func (d BridgeDirection) String() string {
	if d == BridgeInbound {
		return "inbound"
	}
	return "outbound"
}

// BridgeTransferContext is everything the guard needs about one transfer that
// the mint/burn choke point can observe. message_id and the counterparty are
// carried for the event only; no protocol decision is taken on them, because
// message uniqueness is the upstream delivered marker's job (§6.1) and TrueOpen
// must not build a second de-duplication table.
type BridgeTransferContext struct {
	Direction BridgeDirection
	Denom     string
	Amount    uint64
	MessageID []byte
	// Counterpart is the TrueOpen-side party: the mint recipient inbound, the
	// burning sender outbound.
	Counterpart string
	// DestinationRecipient is the raw EVM-side recipient of an outbound
	// transfer. It is upstream-owned bytes and is never decoded as a TrueOpen
	// account (§4.1's explicit 0x exception).
	DestinationRecipient []byte
}

// PrepareBridgeTransfer is the pre-ledger half of the §3.2 guard: everything
// that can refuse a transfer runs here, before any coin moves.
//
// §7.3 is explicit that a refusal must be zero-write and must not mint first and
// roll back, so the denom closed set, the freeze/lifecycle state, I-BRIDGE-1 and
// the epoch limit are all decided before the upstream handler touches the
// ledger. Nothing in this half writes state.
func (k Keeper) PrepareBridgeTransfer(ctx context.Context, transfer BridgeTransferContext) (types.BridgeRouteState, error) {
	var zero types.BridgeRouteState
	if transfer.Amount == 0 {
		return zero, fmt.Errorf("bridge %s amount must be greater than zero", transfer.Direction)
	}
	route, err := k.BridgeRoute.Get(ctx)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return zero, fmt.Errorf("bridge is not configured on this chain")
		}
		return zero, err
	}
	// §5.1: only business_denom moves through the bridge. Cosmos module
	// permissions are not denom-scoped, so this is the denom closed set the
	// contract requires the handler layer to apply.
	if transfer.Denom != route.BusinessDenom {
		return zero, fmt.Errorf("the bridge may only mint or burn %s, not %s", route.BusinessDenom, transfer.Denom)
	}
	if err := k.requireBridgeOperable(ctx); err != nil {
		return zero, err
	}
	epoch, err := k.currentBridgeEpoch(ctx)
	if err != nil {
		return zero, err
	}
	if _, err := k.checkBridgeEpochUsage(ctx, epoch, transfer); err != nil {
		return zero, err
	}
	return route, nil
}

// SettleBridgeTransfer is the post-ledger half: it asserts that the ledger moved
// exactly what the message authorised, and only then records it.
//
// The delta assertion is the point of the split. The guard authorises an amount;
// the upstream handler is what actually mints or burns. Comparing the two is
// what makes cumulative_bridge_minted a statement about the ledger rather than
// about this module's own bookkeeping, and it is why those counters are
// trustworthy enough for I-BRIDGE-2 to be recomputed from them.
func (k Keeper) SettleBridgeTransfer(
	ctx context.Context,
	route types.BridgeRouteState,
	transfer BridgeTransferContext,
	supplyBefore uint64,
) error {
	if err := k.requireBridgeSupplyDelta(ctx, route, transfer, supplyBefore); err != nil {
		return err
	}
	return k.finishBridgeTransfer(ctx, route, transfer, true)
}

func (k Keeper) requireBridgeSupplyDelta(
	ctx context.Context,
	route types.BridgeRouteState,
	transfer BridgeTransferContext,
	supplyBefore uint64,
) error {
	actual := k.businessDenomSupply(ctx, route.BusinessDenom)
	expected, overflow := checkedBridgeAdd(supplyBefore, transfer.Amount)
	if transfer.Direction == BridgeOutbound {
		if supplyBefore < transfer.Amount {
			return fmt.Errorf("bridge outbound of %d exceeds the %s supply of %d",
				transfer.Amount, route.BusinessDenom, supplyBefore)
		}
		expected, overflow = supplyBefore-transfer.Amount, false
	}
	if overflow || actual != expected {
		return fmt.Errorf("bridge %s moved the %s supply from %d to %d, not to the authorised %d",
			transfer.Direction, route.BusinessDenom, supplyBefore, actual, expected)
	}
	return nil
}

func (k Keeper) finishBridgeTransfer(ctx context.Context, route types.BridgeRouteState, transfer BridgeTransferContext, checkInvariant bool) error {
	epoch, err := k.currentBridgeEpoch(ctx)
	if err != nil {
		return err
	}
	if err := k.chargeBridgeEpochUsage(ctx, epoch, transfer); err != nil {
		return err
	}
	minted, burned, err := k.accrueBridgeSupply(ctx, transfer)
	if err != nil {
		return err
	}
	// I-BRIDGE-2 is recomputed against the bank on every transfer, so a drift is
	// caught by the transfer that caused it rather than at the next export.
	if checkInvariant {
		if err := k.EnsureBridgeSupplyInvariant(ctx); err != nil {
			return err
		}
	}
	return k.emitBridgeTransferEvent(ctx, route, transfer, epoch, minted, burned)
}

// RecordPendingOutboundBridgeTransfer verifies the irreversible burn now, but
// defers event/accounting until upstream dispatch has produced its real ID.
func (k Keeper) RecordPendingOutboundBridgeTransfer(
	ctx context.Context,
	route types.BridgeRouteState,
	transfer BridgeTransferContext,
	supplyBefore uint64,
) error {
	if err := k.requireBridgeSupplyDelta(ctx, route, transfer, supplyBefore); err != nil {
		return err
	}
	return k.PushPendingOutboundBridgeTransfer(ctx, transfer)
}

// FinalizePendingOutboundBridgeTransfers binds successful burns to IDs that the
// application has verified in the upstream Mailbox message set. BaseApp keeps
// these writes in the same CacheContext as burn and dispatch.
func (k Keeper) FinalizePendingOutboundBridgeTransfers(ctx context.Context, dispatched []BridgeTransferContext) error {
	pending, err := k.pendingOutboundBridgeTransfers(ctx)
	if err != nil {
		return err
	}
	if len(pending) != len(dispatched) {
		return fmt.Errorf("outbound dispatch count %d does not match pending burn count %d", len(dispatched), len(pending))
	}
	if len(pending) == 0 {
		return nil
	}
	route, err := k.BridgeRoute.Get(ctx)
	if err != nil {
		return err
	}
	for index := range pending {
		if len(dispatched[index].MessageID) != shared.Hash32KeySize || bytes.Equal(dispatched[index].MessageID, make([]byte, shared.Hash32KeySize)) {
			return fmt.Errorf("outbound dispatch %d returned an invalid message_id", index)
		}
		if pending[index].Counterpart != dispatched[index].Counterpart ||
			pending[index].Denom != dispatched[index].Denom || pending[index].Amount != dispatched[index].Amount ||
			!bytes.Equal(pending[index].DestinationRecipient, dispatched[index].DestinationRecipient) {
			return fmt.Errorf("outbound dispatch %d does not match its guarded burn", index)
		}
		pending[index].MessageID = append([]byte(nil), dispatched[index].MessageID...)
		if err := k.finishBridgeTransfer(ctx, route, pending[index], false); err != nil {
			return err
		}
	}
	return k.EnsureBridgeSupplyInvariant(ctx)
}

// BusinessDenomSupply is the authoritative bank total the guard brackets every
// transfer with.
func (k Keeper) BusinessDenomSupply(ctx context.Context, denom string) uint64 {
	return k.businessDenomSupply(ctx, denom)
}

func (k Keeper) businessDenomSupply(ctx context.Context, denom string) uint64 {
	coin := k.bankKeeper.GetSupply(sdk.UnwrapSDKContext(ctx), denom)
	if !coin.Amount.IsUint64() {
		return 0
	}
	return coin.Amount.Uint64()
}

// requireBridgeOperable is the §7.3 fail-closed set that does not depend on the
// amount: lifecycle, freeze and the I-BRIDGE-1 signer agreement. §4.1's n >= 4
// floor is folded in because a set that small must not move funds at all.
func (k Keeper) requireBridgeOperable(ctx context.Context) error {
	control, err := k.BridgeControl.Get(ctx)
	if err != nil {
		return fmt.Errorf("bridge control state is unavailable: %w", err)
	}
	if control.Frozen {
		return fmt.Errorf("bridge is frozen")
	}
	if control.Lifecycle != types.BridgeLifecycleV1_BRIDGE_LIFECYCLE_V1_ACTIVE {
		return fmt.Errorf("bridge is %s, not ACTIVE", control.Lifecycle)
	}
	signerSet, err := k.BridgeSignerSet.Get(ctx)
	if err != nil {
		return fmt.Errorf("bridge signer set state is unavailable: %w", err)
	}
	if err := types.RequireBridgeSignerCount(signerSet.SignerCount); err != nil {
		return err
	}
	derived, err := types.BridgeThresholdV1(signerSet.SignerCount)
	if err != nil {
		return err
	}
	if signerSet.Threshold != derived {
		return fmt.Errorf("bridge threshold %d is not the derived value %d", signerSet.Threshold, derived)
	}
	// I-BRIDGE-1: the projection is recomputed from the Validator bridge signer
	// rows rather than read back from the cached hash, so a ValidatorSet change
	// that never reached the ISM freezes the bridge instead of being trusted.
	projection, err := k.BridgeSignerProjectionHash(ctx)
	if err != nil {
		return err
	}
	if !bytes.Equal(signerSet.LocalSignerSetHash, projection[:]) {
		return fmt.Errorf("I-BRIDGE-1 broken: the Validator bridge signer projection no longer matches local_signer_set_hash")
	}
	if !bytes.Equal(signerSet.ConfirmedEvmSignerSetHash, projection[:]) {
		return fmt.Errorf("I-BRIDGE-1 broken: the confirmed EVM signer set no longer matches the local projection")
	}
	// The cached TrueOpen rows are only two of the three I-BRIDGE-1 views. Read the
	// live Hyperlane objects on every transfer so a changed Mailbox/default ISM,
	// token, router or owner stops funds before the ledger is touched.
	if err := k.ValidateBridgeUpstream(ctx); err != nil {
		return fmt.Errorf("bridge upstream state is not canonical: %w", err)
	}
	return nil
}

// BridgeSignerProjectionHash recomputes §4.2's projection from the current
// Validator bridge signer table.
func (k Keeper) BridgeSignerProjectionHash(ctx context.Context) ([32]byte, error) {
	signers, err := k.exportValidatorBridgeSigners(ctx)
	if err != nil {
		return [32]byte{}, err
	}
	return types.BridgeSignerProjection(sdk.UnwrapSDKContext(ctx).ChainID(), signers)
}

func (k Keeper) currentBridgeEpoch(ctx context.Context) (uint64, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if sdkCtx.BlockHeight() < 0 {
		return 0, fmt.Errorf("bridge epoch requires a non-negative height")
	}
	// The epoch length is read, not defaulted. Substituting DefaultEpochLengthBlocks
	// on a read failure would place the transfer in a usage window computed from a
	// length no other consumer uses, so the §7.2 limit would be charged against the
	// wrong epoch while EndBlocker rolled the real one.
	epochLength, err := k.epochLengthBlocks(ctx)
	if err != nil {
		return 0, err
	}
	return epochForHeight(uint64(sdkCtx.BlockHeight()), epochLength), nil
}

// chargeBridgeEpochUsage applies §7.2. The whole transfer is rejected when it
// would cross the limit: partial execution is explicitly forbidden, because a
// half-filled inbound would mint less than the message says and break the
// two-sided accounting the conservation identity depends on.
func (k Keeper) chargeBridgeEpochUsage(ctx context.Context, epoch uint64, transfer BridgeTransferContext) error {
	usage, err := k.checkBridgeEpochUsage(ctx, epoch, transfer)
	if err != nil {
		return err
	}
	return k.BridgeEpochUsage.Set(ctx, epoch, usage)
}

// checkBridgeEpochUsage returns the row the transfer would produce without
// writing it, so the pre-ledger half can refuse an over-limit transfer and the
// post-ledger half commits the very same arithmetic rather than a second copy
// of it.
func (k Keeper) checkBridgeEpochUsage(ctx context.Context, epoch uint64, transfer BridgeTransferContext) (types.BridgeEpochUsageState, error) {
	var zero types.BridgeEpochUsageState
	limit, err := k.BridgeLimit.Get(ctx)
	if err != nil {
		return zero, fmt.Errorf("bridge limit state is unavailable: %w", err)
	}
	cap, err := shared.ParseAmount(limit.InboundLimitPerEpoch)
	if err != nil {
		return zero, fmt.Errorf("inbound_limit_per_epoch: %w", err)
	}
	if transfer.Direction == BridgeOutbound {
		if cap, err = shared.ParseAmount(limit.OutboundLimitPerEpoch); err != nil {
			return zero, fmt.Errorf("outbound_limit_per_epoch: %w", err)
		}
	}
	usage, err := k.BridgeEpochUsage.Get(ctx, epoch)
	if err != nil {
		if !errors.Is(err, collections.ErrNotFound) {
			return zero, err
		}
		usage = types.BridgeEpochUsageState{
			RewardEpoch: epoch, InboundUsed: shared.NewAmount(0), OutboundUsed: shared.NewAmount(0),
		}
	}
	if usage.Closed {
		return zero, fmt.Errorf("bridge usage epoch %d is already closed", epoch)
	}
	field := &usage.InboundUsed
	if transfer.Direction == BridgeOutbound {
		field = &usage.OutboundUsed
	}
	used, err := shared.ParseAmount(*field)
	if err != nil {
		return zero, fmt.Errorf("bridge %s usage: %w", transfer.Direction, err)
	}
	next, overflow := checkedBridgeAdd(used, transfer.Amount)
	if overflow {
		return zero, fmt.Errorf("bridge %s usage overflows", transfer.Direction)
	}
	if next > cap {
		return zero, fmt.Errorf("bridge %s of %d would take epoch %d usage to %d, above the limit %d; the transfer is rejected whole",
			transfer.Direction, transfer.Amount, epoch, next, cap)
	}
	*field = shared.NewAmount(next)
	return usage, nil
}

// accrueBridgeSupply updates the two cumulative counters §5.2 persists. They are
// never re-derived from the current supply: that is what keeps genesis_allocated
// meaningful and makes I-BRIDGE-3 a checkable fact rather than a claim.
func (k Keeper) accrueBridgeSupply(ctx context.Context, transfer BridgeTransferContext) (minted, burned uint64, err error) {
	supply, err := k.BridgeSupply.Get(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("bridge supply state is unavailable: %w", err)
	}
	if minted, err = shared.ParseAmount(supply.CumulativeBridgeMinted); err != nil {
		return 0, 0, fmt.Errorf("cumulative_bridge_minted: %w", err)
	}
	if burned, err = shared.ParseAmount(supply.CumulativeBridgeBurned); err != nil {
		return 0, 0, fmt.Errorf("cumulative_bridge_burned: %w", err)
	}
	genesisAllocated, err := shared.ParseAmount(supply.GenesisAllocated)
	if err != nil {
		return 0, 0, fmt.Errorf("genesis_allocated: %w", err)
	}
	var overflow bool
	if transfer.Direction == BridgeInbound {
		if minted, overflow = checkedBridgeAdd(minted, transfer.Amount); overflow {
			return 0, 0, fmt.Errorf("cumulative_bridge_minted overflows")
		}
		supply.CumulativeBridgeMinted = shared.NewAmount(minted)
	} else {
		if burned, overflow = checkedBridgeAdd(burned, transfer.Amount); overflow {
			return 0, 0, fmt.Errorf("cumulative_bridge_burned overflows")
		}
		// Localnet may legitimately bridge out vouchers allocated at Genesis.
		// Compare without adding the two uint64 sources, which could overflow even
		// when the final supply after burns is representable.
		if burned > minted && burned-minted > genesisAllocated {
			return 0, 0, fmt.Errorf("bridge would burn %d against %d minted plus %d genesis allocated", burned, minted, genesisAllocated)
		}
		supply.CumulativeBridgeBurned = shared.NewAmount(burned)
	}
	if err := k.BridgeSupply.Set(ctx, supply); err != nil {
		return 0, 0, err
	}
	return minted, burned, nil
}

func (k Keeper) emitBridgeTransferEvent(
	ctx context.Context,
	route types.BridgeRouteState,
	transfer BridgeTransferContext,
	epoch, minted, burned uint64,
) error {
	if transfer.Direction == BridgeInbound {
		bootstrapConsumed, err := k.consumeBridgeBootstrapIfBound(ctx, route, transfer)
		if err != nil {
			return err
		}
		mustEmitHubEvent(ctx, &types.EventBridgeInboundProcessed{
			MessageId: append([]byte(nil), transfer.MessageID...), UsdcRouteId: append([]byte(nil), route.UsdcRouteId...),
			Recipient: transfer.Counterpart, Amount: shared.NewAmount(transfer.Amount), RewardEpoch: epoch,
			CumulativeBridgeMinted: shared.NewAmount(minted), BootstrapConsumed: bootstrapConsumed,
		})
		return nil
	}
	mustEmitHubEvent(ctx, &types.EventBridgeOutboundDispatched{
		MessageId: append([]byte(nil), transfer.MessageID...), UsdcRouteId: append([]byte(nil), route.UsdcRouteId...),
		Sender: transfer.Counterpart, DestinationRecipient: append([]byte(nil), transfer.DestinationRecipient...),
		Amount: shared.NewAmount(transfer.Amount), RewardEpoch: epoch,
		CumulativeBridgeBurned: shared.NewAmount(burned),
	})
	return nil
}

// consumeBridgeBootstrapIfBound flips the one-shot ARMED bootstrap to CONSUMED
// in the same transition as the mint it paid for (§5.3). The transition is
// one-way: once CONSUMED, no later message can re-arm it, so the fee exemption
// cannot become a standing relayer privilege.
func (k Keeper) consumeBridgeBootstrapIfBound(ctx context.Context, route types.BridgeRouteState, transfer BridgeTransferContext) (bool, error) {
	bootstrap, err := k.ReadBridgeBootstrapValue(ctx)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	if bootstrap.Mode != types.BridgeBootstrapModeV1_BRIDGE_BOOTSTRAP_MODE_V1_ARMED {
		return false, nil
	}
	if !bridgeBootstrapMatches(bootstrap, route, transfer) {
		return false, nil
	}
	height, err := currentBridgeHeight(ctx)
	if err != nil {
		return false, err
	}
	bootstrap.Mode = types.BridgeBootstrapModeV1_BRIDGE_BOOTSTRAP_MODE_V1_CONSUMED
	bootstrap.XConsumedHeight = &types.BridgeBootstrapState_ConsumedHeight{ConsumedHeight: height}
	if err := k.WriteBridgeBootstrapValue(ctx, bootstrap); err != nil {
		return false, err
	}
	return true, nil
}

// BridgeBootstrapMatches reports whether one inbound transfer is exactly the
// Genesis-bound bootstrap message. Every binding field must match; §7.4 is
// explicit that a near-miss is charged the ordinary fee rather than being waved
// through.
func BridgeBootstrapMatches(bootstrap types.BridgeBootstrapState, route types.BridgeRouteState, messageID []byte, recipient string, amount uint64) bool {
	return bridgeBootstrapMatches(bootstrap, route, BridgeTransferContext{
		Direction: BridgeInbound, MessageID: messageID, Counterpart: recipient, Amount: amount,
	})
}

func bridgeBootstrapMatches(bootstrap types.BridgeBootstrapState, route types.BridgeRouteState, transfer BridgeTransferContext) bool {
	if bootstrap.Amount == nil || bootstrap.XMessageId == nil {
		return false
	}
	bound, err := shared.ParseAmount(*bootstrap.Amount)
	if err != nil {
		return false
	}
	return bytes.Equal(bootstrap.GetMessageId(), transfer.MessageID) &&
		bytes.Equal(bootstrap.GetRouteId(), route.UsdcRouteId) &&
		bootstrap.GetRecipient() == transfer.Counterpart &&
		bound == transfer.Amount
}

func currentBridgeHeight(ctx context.Context) (uint64, error) {
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	if height <= 0 {
		return 0, fmt.Errorf("bridge transition requires a positive height")
	}
	return uint64(height), nil
}
