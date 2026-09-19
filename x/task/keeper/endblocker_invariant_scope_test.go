package keeper

import (
	"testing"

	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	"github.com/TrueOpen/node/x/task/types"
)

// Three EndBlocker sweeps refuse to retire a handraise-close row unless a later
// verify-open deadline is still scheduled, and raise ErrInvariantBroken when it
// is not — endblocker.go's retireVerifierRowToFallbackDeadline, the SOURCE_FROZEN
// branch, and the empty-union branch. Halting FinalizeBlock is only defensible
// there because the height half of that guard, AssignmentDeadlineHeight <=
// HandraiseCloseHeight, cannot be produced by any accepted parameter set. Issue
// #153 is what happens when a runtime-normal condition raises an invariant
// instead, so the classification these three depend on is worth pinning.
//
// Two independent checks establish it, and both must keep holding:
//   - ValidateVerifyOpenClock, enforced at genesis.go and on session open, makes
//     verify_open_deadline_blocks exceed 2*delta_w + builder window + self-rescue
//     margin — which is exactly assignment_due > selection > handraise_close.
//   - freezeVerifierWindowClock re-derives the whole clock per window and refuses
//     a non-increasing one, so a window that violates the relation is never
//     stored for a sweep to find.
func TestValidatedClockKeepsHandraiseCloseInvariantsUnreachable(t *testing.T) {
	hub := hubtypes.DefaultHubParams()
	task := types.DefaultTaskParams()
	const receiptHeight = uint64(1_000)

	cases := map[string]verifierWindowClockParams{
		// Drift guard: the shipped defaults must satisfy the relation.
		"defaults": {
			DeltaWBlocks:                hub.Epoch.DeltaWBlocks,
			BuilderProposalWindowBlocks: hub.Builder.OpenVerifyBuilderProposalWindowBlocks,
			SelfRescueMarginBlocks:      task.Deadlines.SelfRescueMarginBlocks,
			VerifyOpenDeadlineBlocks:    task.Deadlines.VerifyOpenDeadlineBlocks,
		},
		// The localnet debug chain, whose widened windows are what let a cortex sit
		// on a breakpoint without missing its duty.
		"localnet": {
			DeltaWBlocks: 5, BuilderProposalWindowBlocks: 5,
			SelfRescueMarginBlocks: 10, VerifyOpenDeadlineBlocks: 300,
		},
		// The tightest parameters ValidateVerifyOpenClock accepts: one block of
		// separation between selection and the assignment deadline.
		"minimum accepted separation": {
			DeltaWBlocks: 5, BuilderProposalWindowBlocks: 5,
			SelfRescueMarginBlocks: 10, VerifyOpenDeadlineBlocks: 2*5 + 5 + 10 + 1,
		},
	}

	for name, clockParams := range cases {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, types.ValidateVerifyOpenClock(
				types.TaskDeadlineParamsV1{
					VerifyOpenDeadlineBlocks: clockParams.VerifyOpenDeadlineBlocks,
					SelfRescueMarginBlocks:   clockParams.SelfRescueMarginBlocks,
				},
				clockParams.DeltaWBlocks,
				clockParams.BuilderProposalWindowBlocks,
			), "case parameters must be ones the chain would accept")

			clock, err := freezeVerifierWindowClock(receiptHeight, clockParams)
			require.NoError(t, err)
			require.Greater(t, clock.AssignmentDeadlineHeight, clock.HandraiseCloseHeight,
				"the guard's height half must stay unreachable for a stored window")
		})
	}
}

// The other direction: parameters that would make the guard reachable are
// refused before a window is ever stored, and refused with an ordinary error
// rather than an invariant — so misconfiguration fails the receipt, not the
// block.
func TestUnvalidatedClockIsRefusedBeforeAnyWindowIsStored(t *testing.T) {
	clockParams := verifierWindowClockParams{
		DeltaWBlocks: 5, BuilderProposalWindowBlocks: 5,
		SelfRescueMarginBlocks: 10, VerifyOpenDeadlineBlocks: 2*5 + 5 + 10,
	}
	require.Error(t, types.ValidateVerifyOpenClock(
		types.TaskDeadlineParamsV1{
			VerifyOpenDeadlineBlocks: clockParams.VerifyOpenDeadlineBlocks,
			SelfRescueMarginBlocks:   clockParams.SelfRescueMarginBlocks,
		},
		clockParams.DeltaWBlocks,
		clockParams.BuilderProposalWindowBlocks,
	), "these parameters must not pass validation")

	_, err := freezeVerifierWindowClock(1_000, clockParams)
	require.Error(t, err, "freezing must refuse the clock instead of storing it")
	require.NotErrorIs(t, err, types.ErrInvariantBroken,
		"bad parameters are a rejected receipt, not corrupt storage")
}
