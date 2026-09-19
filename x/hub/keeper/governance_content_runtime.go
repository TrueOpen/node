package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
	govv1beta1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1beta1"
)

// GovernanceContentRuntime is the application-owned adapter that joins the
// stock x/gov proposal store, staking and Hyperlane capabilities to Hub's
// internal typed actions. The action messages remain governance Content and
// never become public transaction messages.
type GovernanceContentRuntime interface {
	HandleGovernanceContent(sdk.Context, govv1beta1.Content) error
	ValidateSubmittedGovernanceProposal(context.Context, uint64) error
}
