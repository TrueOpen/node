package bridge

import (
	"bytes"
	"context"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/bcp-innovations/hyperlane-cosmos/util"
	corekeeper "github.com/bcp-innovations/hyperlane-cosmos/x/core/keeper"
	coretypes "github.com/bcp-innovations/hyperlane-cosmos/x/core/types"
	warpkeeper "github.com/bcp-innovations/hyperlane-cosmos/x/warp/keeper"
	warptypes "github.com/bcp-innovations/hyperlane-cosmos/x/warp/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

// UpstreamAdapter is the only place TrueOpen reads Hyperlane state. It projects the
// few frozen facts the route guard compares against and nothing else, so
// x/hub keeps no Hyperlane import and no copy of upstream state
// (cross_chain_asset_bridge_protocol.md §3.2).
type UpstreamAdapter struct {
	core *corekeeper.Keeper
	warp warpkeeper.Keeper
}

type OutboundDispatch struct {
	MessageID            []byte
	Sender               string
	DestinationRecipient []byte
	Denom                string
	Amount               uint64
}

// OutboundMessageIDs reproduces the bytes the pinned Mailbox has already
// dispatched and accepts them only when their IDs exist in its authoritative
// Messages set. It runs after a successful MsgRemoteTransfer handler, never in
// ante or before burn, and therefore cannot invent or predict a dispatch ID.
func (a UpstreamAdapter) OutboundDispatches(ctx context.Context, msgs []sdk.Msg) ([]OutboundDispatch, error) {
	transfers := make([]*warptypes.MsgRemoteTransfer, 0)
	for _, msg := range msgs {
		if transfer, ok := msg.(*warptypes.MsgRemoteTransfer); ok {
			transfers = append(transfers, transfer)
		}
	}
	if len(transfers) == 0 {
		return nil, nil
	}
	if a.core == nil {
		return nil, fmt.Errorf("Hyperlane core keeper is unavailable")
	}

	firstToken, err := a.warp.HypTokens.Get(ctx, transfers[0].TokenId.GetInternalId())
	if err != nil {
		return nil, fmt.Errorf("outbound token: %w", err)
	}
	mailboxID := firstToken.OriginMailbox
	mailbox, err := a.core.GetMailbox(sdk.UnwrapSDKContext(ctx), mailboxID)
	if err != nil {
		return nil, fmt.Errorf("outbound mailbox: %w", err)
	}
	if uint64(mailbox.MessageSent) < uint64(len(transfers)) {
		return nil, fmt.Errorf("outbound mailbox nonce is below this transaction's dispatch count")
	}
	nonce := mailbox.MessageSent - uint32(len(transfers))
	dispatches := make([]OutboundDispatch, 0, len(transfers))
	zero := make([]byte, util.HEX_ADDRESS_LENGTH)
	for index, transfer := range transfers {
		token, err := a.warp.HypTokens.Get(ctx, transfer.TokenId.GetInternalId())
		if err != nil {
			return nil, fmt.Errorf("outbound transfer %d token: %w", index, err)
		}
		if token.OriginMailbox != mailboxID {
			return nil, fmt.Errorf("outbound transfer %d uses a different mailbox", index)
		}
		router, err := a.warp.EnrolledRouters.Get(ctx, collections.Join(token.Id.GetInternalId(), transfer.DestinationDomain))
		if err != nil {
			return nil, fmt.Errorf("outbound transfer %d router: %w", index, err)
		}
		receiver, err := util.DecodeHexAddress(router.ReceiverContract)
		if err != nil {
			return nil, fmt.Errorf("outbound transfer %d receiver: %w", index, err)
		}
		payload, err := warptypes.NewWarpPayload(transfer.Recipient.Bytes(), *transfer.Amount.BigInt())
		if err != nil {
			return nil, fmt.Errorf("outbound transfer %d payload: %w", index, err)
		}
		dispatched := util.HyperlaneMessage{
			Version: coretypes.MESSAGE_VERSION, Nonce: nonce + uint32(index),
			Origin: mailbox.LocalDomain, Sender: token.Id,
			Destination: router.ReceiverDomain, Recipient: receiver, Body: payload.Bytes(),
		}
		id := dispatched.Id()
		if bytes.Equal(id.Bytes(), zero) {
			return nil, fmt.Errorf("outbound transfer %d produced a zero message_id", index)
		}
		has, err := a.core.Messages.Has(ctx, collections.Join(mailboxID.GetInternalId(), id.Bytes()))
		if err != nil {
			return nil, fmt.Errorf("outbound transfer %d dispatch lookup: %w", index, err)
		}
		if !has {
			return nil, fmt.Errorf("outbound transfer %d message_id is absent from the upstream Mailbox", index)
		}
		if !transfer.Amount.IsUint64() {
			return nil, fmt.Errorf("outbound transfer %d amount is outside uint64", index)
		}
		dispatches = append(dispatches, OutboundDispatch{
			MessageID: append([]byte(nil), id.Bytes()...), Sender: transfer.Sender,
			DestinationRecipient: append([]byte(nil), transfer.Recipient.Bytes()...),
			Denom:                token.OriginDenom, Amount: transfer.Amount.Uint64(),
		})
	}
	return dispatches, nil
}

func NewUpstreamAdapter(core *corekeeper.Keeper, warp warpkeeper.Keeper) UpstreamAdapter {
	return UpstreamAdapter{core: core, warp: warp}
}

var _ hubtypes.BridgeUpstream = UpstreamAdapter{}

func (a UpstreamAdapter) MailboxFacts(ctx context.Context, mailboxID []byte) (hubtypes.BridgeUpstreamMailbox, error) {
	id, err := hexAddressFromBytes(mailboxID)
	if err != nil {
		return hubtypes.BridgeUpstreamMailbox{}, err
	}
	mailbox, err := a.core.GetMailbox(sdk.UnwrapSDKContext(ctx), id)
	if err != nil {
		return hubtypes.BridgeUpstreamMailbox{}, err
	}
	return hubtypes.BridgeUpstreamMailbox{
		LocalDomain: mailbox.LocalDomain,
		DefaultIsm:  mailbox.DefaultIsm.Bytes(),
		Owner:       mailbox.Owner,
	}, nil
}

func (a UpstreamAdapter) TokenFacts(ctx context.Context, tokenID []byte) (hubtypes.BridgeUpstreamToken, error) {
	id, err := hexAddressFromBytes(tokenID)
	if err != nil {
		return hubtypes.BridgeUpstreamToken{}, err
	}
	token, err := a.warp.HypTokens.Get(ctx, id.GetInternalId())
	if err != nil {
		return hubtypes.BridgeUpstreamToken{}, err
	}
	return hubtypes.BridgeUpstreamToken{
		Synthetic:     token.TokenType == warptypes.HYP_TOKEN_TYPE_SYNTHETIC,
		OriginMailbox: token.OriginMailbox.Bytes(),
		OriginDenom:   token.OriginDenom,
		Owner:         token.Owner,
		HasIsmID:      token.IsmId != nil,
	}, nil
}

func (a UpstreamAdapter) RemoteRouters(ctx context.Context, tokenID []byte) ([]hubtypes.BridgeUpstreamRemoteRouter, error) {
	id, err := hexAddressFromBytes(tokenID)
	if err != nil {
		return nil, err
	}
	// The enrolled-router map is keyed (token internal id, destination domain);
	// ranging the token's own prefix is what lets the caller require exactly one
	// and reject a token that also speaks to a second domain.
	out := make([]hubtypes.BridgeUpstreamRemoteRouter, 0, 1)
	iter, err := a.warp.EnrolledRouters.Iterate(ctx,
		collections.NewPrefixedPairRange[uint64, uint32](id.GetInternalId()))
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		router, err := iter.Value()
		if err != nil {
			return nil, err
		}
		contract, err := util.DecodeEthHex(router.ReceiverContract)
		if err != nil {
			return nil, fmt.Errorf("remote router contract %q: %w", router.ReceiverContract, err)
		}
		out = append(out, hubtypes.BridgeUpstreamRemoteRouter{
			DestinationDomain: router.ReceiverDomain,
			ReceiverContract:  contract,
		})
	}
	return out, nil
}

// hexAddressFromBytes converts a stored raw upstream identifier back into the
// HexAddress the upstream keepers key on. The width is the upstream constant,
// not the 20 the prose states; see DOC-012.
func hexAddressFromBytes(raw []byte) (util.HexAddress, error) {
	if len(raw) != util.HEX_ADDRESS_LENGTH {
		return util.HexAddress{}, fmt.Errorf("hyperlane identifier must be exactly %d bytes, got %d", util.HEX_ADDRESS_LENGTH, len(raw))
	}
	var id util.HexAddress
	copy(id[:], raw)
	return id, nil
}
