package keeper_test

import (
	"context"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
)

type matchingBridgeUpstream struct {
	mailbox types.BridgeUpstreamMailbox
	token   types.BridgeUpstreamToken
	router  types.BridgeUpstreamRemoteRouter
}

func (u matchingBridgeUpstream) MailboxFacts(context.Context, []byte) (types.BridgeUpstreamMailbox, error) {
	return u.mailbox, nil
}

func (u matchingBridgeUpstream) TokenFacts(context.Context, []byte) (types.BridgeUpstreamToken, error) {
	return u.token, nil
}

func (u matchingBridgeUpstream) RemoteRouters(context.Context, []byte) ([]types.BridgeUpstreamRemoteRouter, error) {
	return []types.BridgeUpstreamRemoteRouter{u.router}, nil
}

func TestBridgeUpstreamInjectionIsVisibleToEarlierKeeperCopies(t *testing.T) {
	f := initFixture(t)
	installBridge(t, f, 1_000, 1_000)
	original := f.keeper
	route, err := original.BridgeRoute.Get(f.ctx)
	require.NoError(t, err)
	signerSet, err := original.BridgeSignerSet.Get(f.ctx)
	require.NoError(t, err)
	gov := sdk.AccAddress(authtypes.NewModuleAddress(types.GovModuleName)).String()

	_ = f.keeper.WithBridgeUpstream(matchingBridgeUpstream{
		mailbox: types.BridgeUpstreamMailbox{
			LocalDomain: route.HyperlaneLocalDomain, DefaultIsm: signerSet.LocalIsmId, Owner: gov,
		},
		token: types.BridgeUpstreamToken{
			Synthetic: true, OriginMailbox: route.LocalMailboxId, OriginDenom: route.BusinessDenom, Owner: gov,
		},
		router: types.BridgeUpstreamRemoteRouter{
			DestinationDomain: route.OriginDomain, ReceiverContract: route.OriginWarpRouterAddress,
		},
	})

	require.NoError(t, original.ValidateBridgeUpstream(f.ctx))
}
