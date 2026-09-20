package types

import (
	"bytes"
	"context"
	"fmt"
)

// BridgeUpstream is the whole of what TrueOpen reads from the Hyperlane modules.
//
// the bridge protocol forbids mirroring upstream proto, state
// or handlers here,
// so this interface deliberately exposes only the few frozen facts §2.1, §3.3,
// §3.4 and §4.4 require the guard to compare against — never the objects
// themselves. The app supplies the adapter; x/hub never imports Hyperlane.
type BridgeUpstream interface {
	// MailboxFacts returns the local domain, the configured default ISM and the
	// owner of the Mailbox the route names.
	MailboxFacts(ctx context.Context, mailboxID []byte) (BridgeUpstreamMailbox, error)
	// TokenFacts returns the Synthetic token's shape. ism_id is returned as
	// present/absent because §3.4 turns on exactly that distinction.
	TokenFacts(ctx context.Context, tokenID []byte) (BridgeUpstreamToken, error)
	// RemoteRouters returns every enrolled router of the token, so the guard can
	// require exactly one and reject a token that also speaks to another domain.
	RemoteRouters(ctx context.Context, tokenID []byte) ([]BridgeUpstreamRemoteRouter, error)
}

type BridgeUpstreamMailbox struct {
	LocalDomain uint32
	DefaultIsm  []byte
	Owner       string
}

type BridgeUpstreamToken struct {
	// SyntheticTokenType reports whether the token is HYP_TOKEN_TYPE_SYNTHETIC.
	// The raw upstream enum is not surfaced: §3.3 admits exactly one value and
	// re-exporting the rest would invite a second interpretation of it.
	Synthetic     bool
	OriginMailbox []byte
	OriginDenom   string
	Owner         string
	HasIsmID      bool
}

type BridgeUpstreamRemoteRouter struct {
	DestinationDomain uint32
	ReceiverContract  []byte
}

// ValidateBridgeUpstream is the §2.1 / §3.3 / §3.4 / §4.4 cross-check that the
// route actually describes the live upstream objects. Genesis runs it once both
// modules have imported, and the operator-facing query reports it.
//
// Every one of these conditions is a silent-failure mode if left unchecked: a
// token carrying its own ism_id would make an ISM cutover a no-op; a second
// enrolled router would give the same voucher a second origin; an owner that is
// not x/gov would put the route outside governance entirely.
func ValidateBridgeUpstream(ctx context.Context, upstream BridgeUpstream, route BridgeRouteState, localIsmID []byte, govAuthority string) error {
	if upstream == nil {
		return fmt.Errorf("bridge upstream adapter is not installed")
	}
	mailbox, err := upstream.MailboxFacts(ctx, route.LocalMailboxId)
	if err != nil {
		return fmt.Errorf("local mailbox: %w", err)
	}
	if mailbox.LocalDomain != route.HyperlaneLocalDomain {
		return fmt.Errorf("mailbox local domain %d does not match the frozen route domain %d",
			mailbox.LocalDomain, route.HyperlaneLocalDomain)
	}
	if !bytes.Equal(mailbox.DefaultIsm, localIsmID) {
		return fmt.Errorf("mailbox default ISM does not match the committed local ISM")
	}
	if mailbox.Owner != govAuthority {
		return fmt.Errorf("mailbox owner %s is not the x/gov module account", mailbox.Owner)
	}

	token, err := upstream.TokenFacts(ctx, route.LocalWarpTokenId)
	if err != nil {
		return fmt.Errorf("local warp token: %w", err)
	}
	if !token.Synthetic {
		return fmt.Errorf("the USDC warp token must be SYNTHETIC: a Collateral token here would collateralise the same asset on both sides")
	}
	if token.OriginDenom != route.LocalOriginDenom || token.OriginDenom != route.BusinessDenom {
		return fmt.Errorf("warp token origin_denom %q is not the frozen business_denom", token.OriginDenom)
	}
	if !bytes.Equal(token.OriginMailbox, route.LocalMailboxId) {
		return fmt.Errorf("warp token points at a different mailbox than the frozen route")
	}
	if token.Owner != govAuthority {
		return fmt.Errorf("warp token owner %s is not the x/gov module account", token.Owner)
	}
	// §3.4: a per-token ISM would shadow the Mailbox default, so a cutover that
	// repoints the default would look applied while the old signers still verify.
	if token.HasIsmID {
		return fmt.Errorf("the USDC warp token must have no ism_id; it must use the mailbox default ISM")
	}

	routers, err := upstream.RemoteRouters(ctx, route.LocalWarpTokenId)
	if err != nil {
		return fmt.Errorf("remote routers: %w", err)
	}
	if len(routers) != 1 {
		return fmt.Errorf("the USDC warp token must have exactly one enrolled remote router, found %d", len(routers))
	}
	if routers[0].DestinationDomain != route.OriginDomain {
		return fmt.Errorf("the enrolled remote router points at domain %d, not the frozen origin domain %d",
			routers[0].DestinationDomain, route.OriginDomain)
	}
	if !bytes.Equal(routers[0].ReceiverContract, route.OriginWarpRouterAddress) {
		return fmt.Errorf("the enrolled remote router contract is not the frozen origin warp router")
	}
	return nil
}
