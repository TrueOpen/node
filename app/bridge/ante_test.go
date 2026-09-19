package bridge

import (
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/bcp-innovations/hyperlane-cosmos/util"
	coretypes "github.com/bcp-innovations/hyperlane-cosmos/x/core/types"
	warptypes "github.com/bcp-innovations/hyperlane-cosmos/x/warp/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

func TestRequireCanonicalInboundRouteRejectsEveryAlternatePath(t *testing.T) {
	mailbox := util.CreateMockHexAddress("mailbox", 1)
	token := util.CreateMockHexAddress("token", 2)
	router := make([]byte, 20)
	for i := range router {
		router[i] = byte(i + 1)
	}
	var sender util.HexAddress
	copy(sender[len(sender)-len(router):], router)

	route := hubtypes.BridgeRouteState{
		HyperlaneLocalDomain:    1000,
		OriginDomain:            2000,
		OriginWarpRouterAddress: router,
		LocalMailboxId:          mailbox.Bytes(),
		LocalWarpTokenId:        token.Bytes(),
	}
	msg := coretypes.MsgProcessMessage{MailboxId: mailbox}
	parsed := util.HyperlaneMessage{
		Origin: 2000, Destination: 1000, Sender: sender, Recipient: token,
	}
	require.NoError(t, requireCanonicalInboundRoute(&msg, parsed, route))

	tests := map[string]func(*coretypes.MsgProcessMessage, *util.HyperlaneMessage, *hubtypes.BridgeRouteState){
		"mailbox": func(msg *coretypes.MsgProcessMessage, _ *util.HyperlaneMessage, _ *hubtypes.BridgeRouteState) {
			msg.MailboxId = util.CreateMockHexAddress("other-mailbox", 3)
		},
		"origin domain": func(_ *coretypes.MsgProcessMessage, parsed *util.HyperlaneMessage, _ *hubtypes.BridgeRouteState) {
			parsed.Origin++
		},
		"destination domain": func(_ *coretypes.MsgProcessMessage, parsed *util.HyperlaneMessage, _ *hubtypes.BridgeRouteState) {
			parsed.Destination++
		},
		"token": func(_ *coretypes.MsgProcessMessage, parsed *util.HyperlaneMessage, _ *hubtypes.BridgeRouteState) {
			parsed.Recipient = util.CreateMockHexAddress("other-token", 4)
		},
		"origin router": func(_ *coretypes.MsgProcessMessage, parsed *util.HyperlaneMessage, _ *hubtypes.BridgeRouteState) {
			parsed.Sender[len(parsed.Sender)-1]++
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			candidateMsg, candidateParsed, candidateRoute := msg, parsed, route
			mutate(&candidateMsg, &candidateParsed, &candidateRoute)
			require.Error(t, requireCanonicalInboundRoute(&candidateMsg, candidateParsed, candidateRoute))
		})
	}
}

func TestParseCanonicalOutboundTransfer(t *testing.T) {
	token := util.CreateMockHexAddress("token", 2)
	recipient := util.CreateMockHexAddress("recipient", 3)
	sender := sdk.AccAddress(bytesOf(0x44, 20)).String()
	route := hubtypes.BridgeRouteState{LocalWarpTokenId: token.Bytes(), OriginDomain: 2000}
	msg := &warptypes.MsgRemoteTransfer{
		Sender: sender, TokenId: token, DestinationDomain: route.OriginDomain,
		Recipient: recipient, Amount: math.NewInt(7),
	}

	transfer, err := parseBridgeTransfer(msg, route)
	require.NoError(t, err)
	require.Equal(t, sender, transfer.Counterpart)
	require.Equal(t, recipient.Bytes(), transfer.DestinationRecipient)
	require.Empty(t, transfer.MessageID)

	msg.DestinationDomain++
	_, err = parseBridgeTransfer(msg, route)
	require.Error(t, err)
}

func bytesOf(value byte, count int) []byte {
	out := make([]byte, count)
	for index := range out {
		out[index] = value
	}
	return out
}
