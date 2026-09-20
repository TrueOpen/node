package bridge

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strings"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/bcp-innovations/hyperlane-cosmos/util"
	coretypes "github.com/bcp-innovations/hyperlane-cosmos/x/core/types"
	warptypes "github.com/bcp-innovations/hyperlane-cosmos/x/warp/types"

	hubkeeper "github.com/TrueOpen/node/x/hub/keeper"
	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

// BridgeDecorator is the decorated Msg boundary of
// the bridge protocol. It runs
// before any message executes and does two things the ledger hook cannot:
//
//   - refuses every Hyperlane message outside the public two-URL set, so a
//     closed message is rejected before it consumes execution gas;
//   - parses each bridge message and records the facts the ledger hook needs —
//     the Hyperlane message id and the counterparty — in message order.
//
// Message fields that the ledger hook cannot observe are bound to the frozen
// canonical route here. Freeze, lifecycle, I-BRIDGE-1, the epoch limit and the
// conservation identity stay at the ledger hook, where they cannot be bypassed.
type BridgeDecorator struct {
	hub hubkeeper.Keeper
}

func NewBridgeDecorator(hub hubkeeper.Keeper) BridgeDecorator {
	return BridgeDecorator{hub: hub}
}

func (d BridgeDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
	recorded := false
	for _, msg := range tx.GetMsgs() {
		typeURL := sdk.MsgTypeURL(msg)
		if !IsHyperlaneMsg(typeURL) {
			continue
		}
		if typeURL != ProcessMessageTypeURL && typeURL != RemoteTransferTypeURL {
			return ctx, fmt.Errorf(
				"%s is not part of the Phase 0 bridge public Msg set; route, ISM, token and ownership changes are governance-only",
				typeURL)
		}
		route, err := d.hub.BridgeRoute.Get(ctx)
		if err != nil {
			return ctx, fmt.Errorf("canonical bridge route is unavailable: %w", err)
		}
		transfer, err := parseBridgeTransfer(msg, route)
		if err != nil {
			return ctx, err
		}
		// CheckTx and simulation validate the canonical route but must not leave
		// block-scoped intents behind: only delivered execution reaches the ledger
		// hook.
		if ctx.IsCheckTx() || ctx.IsReCheckTx() || simulate {
			continue
		}
		if !recorded {
			// Clear anything a previous transaction in this block left behind; see
			// Keeper.ResetBridgeTransferIntents for why that is possible at all.
			if err := d.hub.ResetBridgeTransferIntents(ctx); err != nil {
				return ctx, err
			}
			recorded = true
		}
		if err := d.hub.PushBridgeTransferIntent(ctx, transfer); err != nil {
			return ctx, err
		}
	}
	return next(ctx, tx, simulate)
}

// parseBridgeTransfer reads the message-level facts out of one bridge message
// and binds its upstream identifiers to the frozen canonical route.
// It re-uses the upstream parsers rather than reimplementing the wire format:
// §3.2 forbids a second copy of Hyperlane's message handling, and a divergent
// parser here would be exactly that.
func parseBridgeTransfer(msg sdk.Msg, route hubtypes.BridgeRouteState) (hubkeeper.BridgeTransferContext, error) {
	switch typed := msg.(type) {
	case *coretypes.MsgProcessMessage:
		raw, err := decodeHyperlaneHex(typed.Message)
		if err != nil {
			return hubkeeper.BridgeTransferContext{}, fmt.Errorf("process message body: %w", err)
		}
		parsed, err := util.ParseHyperlaneMessage(raw)
		if err != nil {
			return hubkeeper.BridgeTransferContext{}, fmt.Errorf("process message: %w", err)
		}
		if err := requireCanonicalInboundRoute(typed, parsed, route); err != nil {
			return hubkeeper.BridgeTransferContext{}, err
		}
		payload, err := warptypes.ParseWarpPayload(parsed.Body)
		if err != nil {
			return hubkeeper.BridgeTransferContext{}, fmt.Errorf("warp payload: %w", err)
		}
		recipient, err := sdk.Bech32ifyAddressBytes(sdk.GetConfig().GetBech32AccountAddrPrefix(), payload.GetCosmosAccount())
		if err != nil {
			return hubkeeper.BridgeTransferContext{}, fmt.Errorf("warp recipient: %w", err)
		}
		id := parsed.Id()
		return hubkeeper.BridgeTransferContext{
			Direction: hubkeeper.BridgeInbound,
			MessageID: append([]byte(nil), id[:]...),
			// The recipient is the account the mint lands on; it is the only
			// TrueOpen-side identity an inbound message carries.
			Counterpart: recipient,
		}, nil
	case *warptypes.MsgRemoteTransfer:
		if !bytes.Equal(typed.TokenId.Bytes(), route.LocalWarpTokenId) {
			return hubkeeper.BridgeTransferContext{}, fmt.Errorf("remote transfer token is not the canonical bridge token")
		}
		if typed.DestinationDomain != route.OriginDomain {
			return hubkeeper.BridgeTransferContext{}, fmt.Errorf("remote transfer destination is not the canonical origin domain")
		}
		if typed.Recipient.IsZeroAddress() {
			return hubkeeper.BridgeTransferContext{}, fmt.Errorf("remote transfer recipient must be non-zero")
		}
		if !typed.Amount.IsPositive() || !typed.Amount.IsUint64() {
			return hubkeeper.BridgeTransferContext{}, fmt.Errorf("remote transfer amount must be a positive uint64")
		}
		sender, err := sdk.AccAddressFromBech32(typed.Sender)
		if err != nil || sender.String() != typed.Sender {
			return hubkeeper.BridgeTransferContext{}, fmt.Errorf("remote transfer sender is not a canonical TrueOpen address")
		}
		return hubkeeper.BridgeTransferContext{
			Direction:            hubkeeper.BridgeOutbound,
			Counterpart:          typed.Sender,
			DestinationRecipient: append([]byte(nil), typed.Recipient.Bytes()...),
		}, nil
	default:
		return hubkeeper.BridgeTransferContext{}, fmt.Errorf("unexpected bridge message %T", msg)
	}
}

// requireCanonicalInboundRoute binds the public ProcessMessage URL to the one
// route Genesis froze. Without this check another upstream token could leave an
// unused intent ahead of the canonical mint, or mint the business denom under a
// second token identity supplied through upstream Genesis.
func requireCanonicalInboundRoute(msg *coretypes.MsgProcessMessage, parsed util.HyperlaneMessage, route hubtypes.BridgeRouteState) error {
	if !bytes.Equal(msg.MailboxId.Bytes(), route.LocalMailboxId) {
		return fmt.Errorf("process message mailbox is not the canonical bridge mailbox")
	}
	if parsed.Destination != route.HyperlaneLocalDomain || parsed.Origin != route.OriginDomain {
		return fmt.Errorf("process message domains do not match the canonical bridge route")
	}
	if !bytes.Equal(parsed.Recipient.Bytes(), route.LocalWarpTokenId) {
		return fmt.Errorf("process message recipient is not the canonical bridge token")
	}
	if !hyperlaneSenderMatchesEVMAddress(parsed.Sender, route.OriginWarpRouterAddress) {
		return fmt.Errorf("process message sender is not the canonical origin warp router")
	}
	return nil
}

func hyperlaneSenderMatchesEVMAddress(sender util.HexAddress, evmAddress []byte) bool {
	if len(evmAddress) != hubtypes.EVMAddressLen {
		return false
	}
	raw := sender.Bytes()
	return bytes.Equal(raw[:len(raw)-hubtypes.EVMAddressLen], make([]byte, len(raw)-hubtypes.EVMAddressLen)) &&
		bytes.Equal(raw[len(raw)-hubtypes.EVMAddressLen:], evmAddress)
}

// decodeHyperlaneHex accepts the upstream transport form of a message or its
// metadata: lowercase hex, optionally 0x-prefixed.
func decodeHyperlaneHex(value string) ([]byte, error) {
	trimmed := strings.TrimPrefix(value, "0x")
	raw, err := hex.DecodeString(trimmed)
	if err != nil {
		return nil, err
	}
	return raw, nil
}
