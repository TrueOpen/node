package app

// Governance domain classification tests.
//
// The classification does not choose an electorate — Phase 0 has exactly one,
// and the tally is x/gov's default (see app/gov_domain.go for the protocol
// citations). Its only consumer is the submission-time guard in
// app/gov_proposal_ante.go, which is also the only place any of this is
// observable end-to-end; TestProposalDomainGuardIsWiredIntoApp covers that.

import (
	"testing"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	v1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	"github.com/stretchr/testify/require"
)

func TestGovDomainOfProposalClassifiesByContent(t *testing.T) {
	cases := []struct {
		name string
		urls []string
		want GovDomain
	}{
		{"empty proposal is standard", nil, GovDomainStandard},
		{"params only", []string{standardMsgURL}, GovDomainStandard},
		{"unknown msg is standard", []string{"/cosmos.bank.v1beta1.MsgSend"}, GovDomainStandard},
		{"set model status", []string{builderMsgURL}, GovDomainBuilder},
		{"set profile status", []string{profileMsgURL}, GovDomainBuilder},
		{"multiple builder msgs", []string{builderMsgURL, profileMsgURL}, GovDomainBuilder},
		{"mixed builder+standard", []string{builderMsgURL, standardMsgURL}, GovDomainBuilder},
		{"mixed standard+builder reversed", []string{standardMsgURL, builderMsgURL}, GovDomainBuilder},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, GovDomainOfTypeURLs(tc.urls))
			require.Equal(t, tc.want, GovDomainOfProposal(proposalWith(1, tc.urls...).Messages))
		})
	}
}

func TestGovDomainOfProposalSkipsNilMessages(t *testing.T) {
	msgs := []*codectypes.Any{nil, {TypeUrl: builderMsgURL}, nil}
	require.Equal(t, GovDomainBuilder, GovDomainOfProposal(msgs))
	require.Equal(t, GovDomainStandard, GovDomainOfProposal([]*codectypes.Any{nil}))
}

// TestBuilderDomainProposalIsTalliedLikeAnyOther pins the Phase 0 outcome that
// matters operationally: a model status proposal must be able to PASS on
// validator stake. Builders hold no bond in Phase 0 (governance_protocol.md §2:104), so a
// builder-weighted tally would total zero weight and no model could ever be
// frozen or delisted.
func TestBuilderDomainProposalIsTalliedLikeAnyOther(t *testing.T) {
	app, voter := bootAppForGovTally(t)
	ctx := app.NewContextLegacy(true, cmtproto.Header{Height: 2})

	for _, tc := range []struct {
		name string
		prop v1.Proposal
	}{
		{"standard", proposalWith(430, standardMsgURL)},
		{"builder", proposalWith(431, builderMsgURL)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			castVote(t, app, ctx, tc.prop.Id, voter, v1.OptionYes)
			passes, _, tally, err := app.GovKeeper.Tally(ctx, tc.prop)
			require.NoError(t, err)
			require.True(t, passes, "both domains are decided by the same electorate in Phase 0")
			require.NotEqual(t, "0", tally.YesCount)
		})
	}
}
