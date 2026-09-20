package app

// Submission-time validation for dual-mode governance
// (docs/governance_dualmode_design.md §6).
//
// The tally already refuses to be fooled by a mixed proposal: strictest-wins
// means any builder-domain message keeps the whole proposal builder-gated. This
// decorator is the UX half — it rejects the mix at submission instead of letting
// a proposal sit through a voting period with a non-obvious electorate.
//
// Why an ante decorator rather than a gov hook: x/gov's hook slot is already
// claimed by depinject (InvokeSetHooks installs a MultiGovHooks during wiring,
// and SetHooks panics if called twice), so a second SetHooks from the app is not
// possible. Ante is also the natural place for "reject this tx on admission".
//
// The check is a pure function of the tx contents, so CheckTx and DeliverTx
// reach the same verdict on every node.
//
// NOTE: x/authz is not wired on this chain, so a MsgSubmitProposal cannot arrive
// nested inside a MsgExec. If authz is ever added, this scan must recurse into
// nested messages.

import (
	errorsmod "cosmossdk.io/errors"

	sdk "github.com/cosmos/cosmos-sdk/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	v1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
)

// ProposalDomainDecorator rejects governance proposals that mix builder-domain
// and standard-domain messages.
type ProposalDomainDecorator struct{}

// NewProposalDomainDecorator returns the submission-time mixed-proposal guard.
func NewProposalDomainDecorator() ProposalDomainDecorator {
	return ProposalDomainDecorator{}
}

// ValidateTx returns an error if any MsgSubmitProposal in the tx mixes domains.
func (ProposalDomainDecorator) ValidateTx(tx sdk.Tx) error {
	if tx == nil {
		return nil
	}
	for _, msg := range tx.GetMsgs() {
		submit, ok := msg.(*v1.MsgSubmitProposal)
		if !ok {
			continue
		}
		if HasMixedGovDomains(submit.Messages) {
			return errorsmod.Wrap(govtypes.ErrInvalidProposalMsg,
				"proposal mixes builder-domain and standard-domain messages; "+
					"builder-gated actions (model/profile status) must be submitted "+
					"as their own proposal")
		}
	}
	return nil
}

// AnteHandle implements the ante decorator contract.
func (d ProposalDomainDecorator) AnteHandle(ctx sdk.Context, tx sdk.Tx, simulate bool, next sdk.AnteHandler) (sdk.Context, error) {
	if err := d.ValidateTx(tx); err != nil {
		return ctx, err
	}
	return next(ctx, tx, simulate)
}
