package app

import (
	"bytes"
	"testing"
	"time"

	"cosmossdk.io/collections"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	govv1 "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
)

// The keeper side of ReplaceBuilderSetV1 is covered in x/hub/keeper. What was
// not covered is the glue that decides whether the keeper is called at all:
// HandleGovernanceContent resolves an execution context first, and that
// resolution has one path which returns success without doing anything.
//
// That path exists for a reason. x/gov preflights legacy content in a throwaway
// cache before the proposal has an ID, so a lookup that misses there must not be
// an error. The risk is that the same silence covers a real execution: a
// proposal that passes its vote, reports PASSED, and changes nothing. These
// tests pin which case is which, because the two are indistinguishable from the
// proposal status alone.
// A content object complete enough to pass ValidateBasic, which x/gov runs at
// submission. The members only have to be well-formed and strictly ascending by
// address bytes here; whether they are admitted builders is the keeper's
// question, and these tests stop before the keeper.
func replaceBuilderSetContent(proposalID uint64) *hubtypes.ReplaceBuilderSetV1 {
	members := make([]string, 0, 3)
	for i := byte(1); i <= 3; i++ {
		raw := bytes.Repeat([]byte{i}, 20)
		members = append(members, sdk.AccAddress(raw).String())
	}
	return &hubtypes.ReplaceBuilderSetV1{
		ProposalId:             proposalID,
		ExpectedCurrentVersion: 1,
		ExpectedCurrentSetHash: bytes.Repeat([]byte{0xab}, 32),
		NextBuilderSetId:       "wiring-probe",
		Members:                members,
		EffectiveHeight:        100_000,
	}
}

// TestGovernanceContentPreflightIsSilent is the documented case: no proposal
// carries this ID yet, so resolution misses and the handler must return nil
// rather than an error, or x/gov could not preflight legacy content at all.
func TestGovernanceContentPreflightIsSilent(t *testing.T) {
	app := bootAppMinimal(t)
	ctx := app.NewUncachedContext(false, cmtproto.Header{Height: 1, Time: time.Unix(1_700_000_000, 0).UTC()})

	runtime := &governanceRuntime{gov: app.GovKeeper, hub: app.HubKeeper}

	_, found, err := runtime.acceptedExecutionContext(ctx, 4242, replaceBuilderSetContent(4242))
	require.NoError(t, err, "a preflight miss must not be an error")
	require.False(t, found, "no proposal 4242 exists, so there is nothing to execute against")
}

// TestGovernanceContentRefusesAProposalThatIsNotAtItsBoundary is the half that
// keeps the silence narrow: once a proposal with that ID does exist, every way
// of not being executable is an error, so a real action can never be skipped
// quietly. Without this, a mismatch between the ID a proposer writes into the
// action and the ID x/gov assigns would read as success.
func TestGovernanceContentRefusesAProposalThatIsNotAtItsBoundary(t *testing.T) {
	app := bootAppMinimal(t)
	ctx := app.NewUncachedContext(false, cmtproto.Header{Height: 1, Time: time.Unix(1_700_000_000, 0).UTC()})

	content := replaceBuilderSetContent(1)
	msg, err := govv1.NewLegacyContent(content, app.GovKeeper.GetAuthority())
	require.NoError(t, err)

	proposer := sdk.AccAddress([]byte("proposer____________"))
	proposal, err := app.GovKeeper.SubmitProposal(ctx, []sdk.Msg{msg}, "",
		"probe", "probe", proposer, false)
	require.NoError(t, err)
	require.EqualValues(t, 1, proposal.Id, "the fixture assumes this is the first proposal")

	runtime := &governanceRuntime{gov: app.GovKeeper, hub: app.HubKeeper}

	// Still in its deposit period: it exists, but it is nowhere near execution.
	_, _, err = runtime.acceptedExecutionContext(ctx, 1, content)
	require.Error(t, err, "a proposal short of its execution boundary must be refused, not skipped")
	require.Contains(t, err.Error(), "not at its accepted execution boundary")

	// Voting, but the period has not closed yet.
	end := ctx.BlockTime().Add(time.Hour)
	proposal.Status = govv1.ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD
	proposal.VotingEndTime = &end
	require.NoError(t, app.GovKeeper.SetProposal(ctx, proposal))

	_, _, err = runtime.acceptedExecutionContext(ctx, 1, content)
	require.Error(t, err, "a proposal whose voting period is still open must be refused")

	// The period has closed, but x/gov has not dequeued it, so this is not the
	// execution boundary either.
	past := ctx.BlockTime().Add(-time.Hour)
	proposal.VotingEndTime = &past
	require.NoError(t, app.GovKeeper.SetProposal(ctx, proposal))
	require.NoError(t, app.GovKeeper.ActiveProposalsQueue.Set(ctx, collections.Join(past, uint64(1)), uint64(1)))

	_, _, err = runtime.acceptedExecutionContext(ctx, 1, content)
	require.Error(t, err, "a proposal still queued has not entered accepted execution")
	require.Contains(t, err.Error(), "has not entered accepted execution")
}

// TestGovernanceContentRequiresTheActionToBeInTheProposal closes the last way an
// action could execute without having been voted on: the content handed to the
// handler must be one of the messages the proposal actually carries.
func TestGovernanceContentRequiresTheActionToBeInTheProposal(t *testing.T) {
	app := bootAppMinimal(t)
	ctx := app.NewUncachedContext(false, cmtproto.Header{Height: 1, Time: time.Unix(1_700_000_000, 0).UTC()})

	carried := replaceBuilderSetContent(1)
	msg, err := govv1.NewLegacyContent(carried, app.GovKeeper.GetAuthority())
	require.NoError(t, err)

	proposer := sdk.AccAddress([]byte("proposer____________"))
	proposal, err := app.GovKeeper.SubmitProposal(ctx, []sdk.Msg{msg}, "",
		"probe", "probe", proposer, false)
	require.NoError(t, err)

	past := ctx.BlockTime().Add(-time.Hour)
	proposal.Status = govv1.ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD
	proposal.VotingEndTime = &past
	require.NoError(t, app.GovKeeper.SetProposal(ctx, proposal))

	runtime := &governanceRuntime{gov: app.GovKeeper, hub: app.HubKeeper}

	// The action the proposal carries resolves.
	execution, found, err := runtime.acceptedExecutionContext(ctx, proposal.Id, carried)
	require.NoError(t, err)
	require.True(t, found, "the action this proposal carries must resolve to an execution context")
	require.True(t, execution.Accepted)
	require.EqualValues(t, proposal.Id, execution.ProposalID)

	// A different action naming the same proposal does not.
	forged := replaceBuilderSetContent(proposal.Id)
	forged.NextBuilderSetId = "not-in-the-proposal"
	_, _, err = runtime.acceptedExecutionContext(ctx, proposal.Id, forged)
	require.Error(t, err, "an action the proposal does not carry must be refused")
	require.Contains(t, err.Error(), "does not contain the executing TrueOpen action")
}

// TestHandleGovernanceContentReachesTheKeeper is the decisive one. The two
// tests above cover the resolver in isolation; this drives the whole handler
// the way x/gov's EndBlocker does, with a proposal in exactly the state the SDK
// leaves it in at execution: still VOTING_PERIOD, voting end in the past, and
// already removed from the active queue.
//
// The members are not admitted builders, so the keeper must refuse. What is
// being asserted is that the refusal happens at all: a nil here would mean the
// action was skipped and the proposal would report PASSED having changed
// nothing, which is indistinguishable from success on the proposal status.
func TestHandleGovernanceContentReachesTheKeeper(t *testing.T) {
	app := bootAppMinimal(t)
	ctx := app.NewUncachedContext(false, cmtproto.Header{Height: 1, Time: time.Unix(1_700_000_000, 0).UTC()})

	content := replaceBuilderSetContent(1)
	msg, err := govv1.NewLegacyContent(content, app.GovKeeper.GetAuthority())
	require.NoError(t, err)

	proposer := sdk.AccAddress([]byte("proposer____________"))
	proposal, err := app.GovKeeper.SubmitProposal(ctx, []sdk.Msg{msg}, "",
		"probe", "probe", proposer, false)
	require.NoError(t, err)

	past := ctx.BlockTime().Add(-time.Hour)
	proposal.Status = govv1.ProposalStatus_PROPOSAL_STATUS_VOTING_PERIOD
	proposal.VotingEndTime = &past
	require.NoError(t, app.GovKeeper.SetProposal(ctx, proposal))
	content.ProposalId = proposal.Id

	runtime := &governanceRuntime{gov: app.GovKeeper, hub: app.HubKeeper}

	err = runtime.HandleGovernanceContent(ctx, content)
	require.Error(t, err,
		"the keeper must be reached and refuse these members; a nil return means the action was skipped while the proposal still reports PASSED")
}
