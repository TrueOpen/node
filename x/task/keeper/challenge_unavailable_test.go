package keeper

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// A challenge round that closes UNAVAILABLE never had an assignment, so closing
// the value-evidence window must resolve its funding without replaying one. The
// bond-free case matters because every shipped seed sets challenge_open_bond to
// zero, which leaves the round with nothing to refund or slash.
func TestValueEvidenceCloseResolvesUnavailableChallengeRound(t *testing.T) {
	for _, bond := range []uint64{100, 0} {
		t.Run(fmt.Sprintf("bond_%d", bond), func(t *testing.T) {
			f, _ := openChallengeFixtureWithBond(t, bond)
			bank := dispatchTestBank{store: f.keeper.storeService}
			opener := sdk.MustAccAddressFromBech32(f.operators[0])
			key := types.NewVerifyRoundKey(f.taskKey, types.ChallengeVerifyRoundV1)
			funding, err := f.keeper.ReadRoundFunding(f.ctx, key)
			require.NoError(t, err)
			locked := bank.balance(f.ctx, opener)
			require.Less(t, locked, uint64(1000), "opening the round locked funds")
			require.NotZero(t, mustAmountValue(funding.VerifierBudget))

			require.NoError(t, f.keeper.extendValueEvidenceWindow(f.ctx, f.taskID, 50))
			require.NoError(t, f.keeper.closeUnavailableChallengeRound(f.ctx, f.taskKey, 60))
			summary, err := f.keeper.TaskRoundSummary.Get(f.ctx, f.taskKey)
			require.NoError(t, err)
			require.Equal(t, shared.RoundOutcomeV1_ROUND_OUTCOME_V1_UNAVAILABLE, summary.GetRound2Outcome())
			closed, err := f.keeper.ReadVerificationRound(f.ctx, key)
			require.NoError(t, err)
			require.Nil(t, closed.XRoundEffectPlanRoot, "effects wait for the evidence window")

			// The close handler used to fail here and defer its row; the retry at the
			// deferred height must now succeed instead of deferring it again.
			deadline := summary.GetVerifierValueEvidenceCloseHeight()
			require.NoError(t, f.keeper.deferFailedDeadlineRow(
				f.ctx, f.keeper.ValueEvidenceWindowCloseIndex, f.taskKey, deadline, deadline, errTestDeferred))
			retryHeight := deadline + deadlineSweepFailureRetryBlocksV1
			f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(retryHeight)))
			_, err = f.keeper.processValueEvidenceWindowCloses(f.ctx, retryHeight, 1)
			require.NoError(t, err)
			again, err := f.keeper.ValueEvidenceWindowCloseIndex.Has(f.ctx,
				types.NewDeadlineIndexKey(retryHeight+deadlineSweepFailureRetryBlocksV1, f.taskKey))
			require.NoError(t, err)
			require.False(t, again, "the retry must not be deferred again")

			frozen, err := f.keeper.ReadVerificationRound(f.ctx, key)
			require.NoError(t, err)
			require.NotNil(t, frozen.XRoundEffectPlanRoot, "funding effects must be frozen after the window closes")
			require.NotZero(t, frozen.RoundEffectCount, "a closed round never carries an empty plan")

			// Applying the plan returns every locked unit to the opener.
			_, done, err := f.keeper.applyRoundEconomicEffects(f.ctx, f.taskKey, types.ChallengeVerifyRoundV1, retryHeight, 16)
			require.NoError(t, err)
			require.True(t, done)
			require.Equal(t, uint64(1000), bank.balance(f.ctx, opener))
			resolved, err := f.keeper.ReadRoundFunding(f.ctx, key)
			require.NoError(t, err)
			require.NotNil(t, resolved.XFundingResolutionHash)
			require.Zero(t, mustAmountValue(resolved.VerifierBudgetRemaining))

			// Settlement replays the stored plan against its root.
			summary, err = f.keeper.TaskRoundSummary.Get(f.ctx, f.taskKey)
			require.NoError(t, err)
			disqualified, err := f.keeper.loadAppliedRoundDisqualifications(f.ctx, f.taskKey, summary)
			require.NoError(t, err)
			require.Empty(t, disqualified)
		})
	}
}
