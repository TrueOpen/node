package bridge

import (
	"context"
	"fmt"
	"strings"

	coretypes "github.com/bcp-innovations/hyperlane-cosmos/x/core/types"
	warptypes "github.com/bcp-innovations/hyperlane-cosmos/x/warp/types"
)

// ProcessMessageTypeURL and RemoteTransferTypeURL are the entire public Msg
// surface of the bridge (the bridge protocol). They are
// the upstream type URLs verbatim: Node registers no wrapper Msg, so an SDK or
// relayer built against the pinned release talks to this chain unchanged.
const (
	ProcessMessageTypeURL = "/hyperlane.core.v1.MsgProcessMessage"
	RemoteTransferTypeURL = "/hyperlane.warp.v1.MsgRemoteTransfer"
)

// hyperlaneServicePrefixes are the four upstream proto packages §3.1 enables.
// Everything under them is Hyperlane's public Msg surface, and everything in
// that surface except the two URLs above is closed in Phase 0.
var hyperlaneServicePrefixes = []string{
	"/hyperlane.core.v1.",
	"/hyperlane.core.interchain_security.v1.",
	"/hyperlane.core.post_dispatch.v1.",
	"/hyperlane.warp.v1.",
}

// MsgClosedSet is the §3.2 / §9 closed set, installed as the BaseApp circuit
// breaker so it is consulted for every message before its handler runs.
//
// The create / set / enroll / unroll / ownership / ISM / validator-announce
// messages are not merely discouraged: reaching MsgSetMailbox, MsgSetToken or
// MsgEnrollRemoteRouter from an ordinary transaction would let a signer move the
// route out from under usdc_route_id and I-BRIDGE-1 without a chain upgrade.
// They remain reachable only through Genesis and the §4.3/§7 governance actions.
type MsgClosedSet struct {
	// inner is consulted for non-Hyperlane messages so installing this breaker
	// does not silently disable another module's own circuit.
	inner CircuitBreaker
}

// CircuitBreaker mirrors baseapp's interface without importing it, so this type
// stays testable on its own.
type CircuitBreaker interface {
	IsAllowed(ctx context.Context, typeURL string) (bool, error)
}

func NewMsgClosedSet(inner CircuitBreaker) MsgClosedSet {
	return MsgClosedSet{inner: inner}
}

// IsAllowed returns false for every Hyperlane message outside the two-URL public
// set. A closed message therefore has no reachable route at all, which is what
// "register no ordinary Tx route" means in a chain that mounts the upstream
// modules for their state, Genesis and Query services.
func (c MsgClosedSet) IsAllowed(ctx context.Context, typeURL string) (bool, error) {
	if !IsHyperlaneMsg(typeURL) {
		if c.inner == nil {
			return true, nil
		}
		return c.inner.IsAllowed(ctx, typeURL)
	}
	switch typeURL {
	case ProcessMessageTypeURL, RemoteTransferTypeURL:
		return true, nil
	default:
		return false, fmt.Errorf(
			"%s is not part of the Phase 0 bridge public Msg set; route, ISM, token and ownership changes are governance-only",
			typeURL)
	}
}

// IsHyperlaneMsg reports whether a type URL belongs to one of the four enabled
// upstream packages.
func IsHyperlaneMsg(typeURL string) bool {
	for _, prefix := range hyperlaneServicePrefixes {
		if strings.HasPrefix(typeURL, prefix) {
			return true
		}
	}
	return false
}

// assert the two constants still name real upstream messages. If the pinned
// release renamed either, this fails to compile rather than silently leaving the
// bridge with no reachable entry point.
var (
	_ = coretypes.MsgProcessMessage{}
	_ = warptypes.MsgRemoteTransfer{}
)
