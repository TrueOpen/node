package bridge_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/app/bridge"
)

// §3.2 names exactly two callable upstream messages, and §9 lists the rest as
// NOT_SUPPORTED with no callable entry point. The closed set is what makes that
// true on a chain that mounts the upstream modules whole, so the two allowed
// URLs and the refused ones are both asserted here rather than assumed.
func TestMsgClosedSetAllowsOnlyTheTwoPublicBridgeMessages(t *testing.T) {
	closed := bridge.NewMsgClosedSet(nil)

	for _, allowed := range []string{
		bridge.ProcessMessageTypeURL,
		bridge.RemoteTransferTypeURL,
	} {
		ok, err := closed.IsAllowed(context.Background(), allowed)
		require.NoError(t, err)
		require.True(t, ok, "%s is the public bridge surface", allowed)
	}

	// Reaching any of these from an ordinary transaction would move the route,
	// the ISM or the token out from under usdc_route_id and I-BRIDGE-1 without a
	// chain upgrade (§10.3).
	for _, refused := range []string{
		"/hyperlane.core.v1.MsgCreateMailbox",
		"/hyperlane.core.v1.MsgSetMailbox",
		"/hyperlane.warp.v1.MsgCreateSyntheticToken",
		"/hyperlane.warp.v1.MsgCreateCollateralToken",
		"/hyperlane.warp.v1.MsgSetToken",
		"/hyperlane.warp.v1.MsgEnrollRemoteRouter",
		"/hyperlane.warp.v1.MsgUnrollRemoteRouter",
		"/hyperlane.core.interchain_security.v1.MsgCreateMessageIdMultisigIsm",
		"/hyperlane.core.interchain_security.v1.MsgAnnounceValidator",
		"/hyperlane.core.interchain_security.v1.MsgUpdateRoutingIsmOwner",
		"/hyperlane.core.post_dispatch.v1.MsgCreateMerkleTreeHook",
	} {
		ok, err := closed.IsAllowed(context.Background(), refused)
		require.False(t, ok, "%s must have no callable route", refused)
		require.ErrorContains(t, err, "governance-only")
	}
}

// The breaker must not quietly disable anything outside the bridge; a chain that
// mounts it keeps every other module's messages exactly as they were.
func TestMsgClosedSetLeavesNonBridgeMessagesAlone(t *testing.T) {
	closed := bridge.NewMsgClosedSet(nil)
	for _, other := range []string{
		"/cosmos.bank.v1beta1.MsgSend",
		"/task.v1.MsgSettleTask",
		"/hub.v1.MsgClaimEarnings",
		"/hyperlanexyz.other.v1.MsgSomething",
	} {
		ok, err := closed.IsAllowed(context.Background(), other)
		require.NoError(t, err)
		require.True(t, ok, "%s is not a bridge message", other)
	}
}

// An inner breaker stays authoritative for non-bridge messages, so installing
// the bridge set cannot silently override another module's circuit.
func TestMsgClosedSetDelegatesNonBridgeDecisions(t *testing.T) {
	closed := bridge.NewMsgClosedSet(denyAll{})

	ok, err := closed.IsAllowed(context.Background(), "/cosmos.bank.v1beta1.MsgSend")
	require.NoError(t, err)
	require.False(t, ok, "the inner breaker still decides non-bridge messages")

	// The bridge decision is the bridge's own and is not delegated.
	ok, err = closed.IsAllowed(context.Background(), bridge.ProcessMessageTypeURL)
	require.NoError(t, err)
	require.True(t, ok)
}

type denyAll struct{}

func (denyAll) IsAllowed(context.Context, string) (bool, error) { return false, nil }

func TestIsHyperlaneMsgCoversTheFourEnabledPackages(t *testing.T) {
	for _, typeURL := range []string{
		"/hyperlane.core.v1.MsgProcessMessage",
		"/hyperlane.core.interchain_security.v1.MsgCreateNoopIsm",
		"/hyperlane.core.post_dispatch.v1.MsgCreateIgp",
		"/hyperlane.warp.v1.MsgRemoteTransfer",
	} {
		require.True(t, bridge.IsHyperlaneMsg(typeURL), typeURL)
	}
	// A package that merely starts with the same letters is not the bridge.
	require.False(t, bridge.IsHyperlaneMsg("/hyperlane.other.v1.MsgX"))
	require.False(t, bridge.IsHyperlaneMsg("/cosmos.bank.v1beta1.MsgSend"))
}
