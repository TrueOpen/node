package app

// Submission-time mixed-proposal guard tests
// (docs/governance_dualmode_design.md §6).

import (
	"testing"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	v1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	protov2 "google.golang.org/protobuf/proto"

	"github.com/stretchr/testify/require"
)

// stubTx is the minimum sdk.Tx needed to drive an ante decorator.
type stubTx struct{ msgs []sdk.Msg }

func (t stubTx) GetMsgs() []sdk.Msg { return t.msgs }

func (t stubTx) GetMsgsV2() ([]protov2.Message, error) { return nil, nil }

func submitProposalTx(typeURLs ...string) stubTx {
	msgs := make([]*codectypes.Any, 0, len(typeURLs))
	for _, url := range typeURLs {
		msgs = append(msgs, &codectypes.Any{TypeUrl: url})
	}
	return stubTx{msgs: []sdk.Msg{&v1.MsgSubmitProposal{Messages: msgs}}}
}

func TestHasMixedGovDomains(t *testing.T) {
	mk := func(urls ...string) []*codectypes.Any {
		out := make([]*codectypes.Any, 0, len(urls))
		for _, u := range urls {
			out = append(out, &codectypes.Any{TypeUrl: u})
		}
		return out
	}
	require.False(t, HasMixedGovDomains(nil))
	require.False(t, HasMixedGovDomains(mk(standardMsgURL)))
	require.False(t, HasMixedGovDomains(mk(standardMsgURL, "/cosmos.gov.v1.MsgUpdateParams")))
	require.False(t, HasMixedGovDomains(mk(builderMsgURL)))
	require.False(t, HasMixedGovDomains(mk(builderMsgURL, profileMsgURL)))
	require.True(t, HasMixedGovDomains(mk(standardMsgURL, builderMsgURL)))
	require.True(t, HasMixedGovDomains(mk(builderMsgURL, standardMsgURL)))
	// nil entries must not be counted as a standard-domain message.
	require.False(t, HasMixedGovDomains([]*codectypes.Any{nil, {TypeUrl: builderMsgURL}}))
}

func TestProposalDomainDecoratorRejectsMixedProposal(t *testing.T) {
	d := NewProposalDomainDecorator()

	require.NoError(t, d.ValidateTx(submitProposalTx(standardMsgURL)), "pure standard proposal must pass")
	require.NoError(t, d.ValidateTx(submitProposalTx(builderMsgURL, profileMsgURL)), "pure builder proposal must pass")
	require.NoError(t, d.ValidateTx(stubTx{}), "tx without a proposal must pass")
	require.NoError(t, d.ValidateTx(nil), "nil tx must not panic")

	err := d.ValidateTx(submitProposalTx(standardMsgURL, builderMsgURL))
	require.Error(t, err)
	require.ErrorContains(t, err, "mixes builder-domain and standard-domain messages")
}

func TestProposalDomainDecoratorCallsNextWhenValid(t *testing.T) {
	d := NewProposalDomainDecorator()
	called := false
	next := func(ctx sdk.Context, tx sdk.Tx, simulate bool) (sdk.Context, error) {
		called = true
		return ctx, nil
	}

	_, err := d.AnteHandle(sdk.Context{}, submitProposalTx(builderMsgURL), false, next)
	require.NoError(t, err)
	require.True(t, called, "valid tx must continue down the ante chain")

	called = false
	_, err = d.AnteHandle(sdk.Context{}, submitProposalTx(standardMsgURL, builderMsgURL), false, next)
	require.Error(t, err)
	require.False(t, called, "rejected tx must not reach the rest of the ante chain")
}

// TestProposalDomainGuardIsWiredIntoApp proves the decorator is installed in the
// real app's ante chain, not merely defined. A mixed proposal must be rejected
// with the domain error before any other ante stage runs.
func TestProposalDomainGuardIsWiredIntoApp(t *testing.T) {
	app, _ := bootAppForGovTally(t)
	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: 2})

	ante := app.App.BaseApp.AnteHandler()
	require.NotNil(t, ante, "app must have an ante handler")

	_, err := ante(ctx, submitProposalTx(standardMsgURL, builderMsgURL), false)
	require.Error(t, err)
	require.ErrorContains(t, err, "mixes builder-domain and standard-domain messages",
		"the mixed-proposal guard must run inside the app ante chain")
}
