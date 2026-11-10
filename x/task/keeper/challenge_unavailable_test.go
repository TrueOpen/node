package keeper

import (
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// A challenge round that closes UNAVAILABLE never had an assignment, so closing
// the value-evidence window must resolve its funding without replaying one.
func TestValueEvidenceCloseResolvesUnavailableChallengeRound(t *testing.T) {
	f, _ := openChallengeQueryFixture(t)
	key := types.NewVerifyRoundKey(f.taskKey, types.ChallengeVerifyRoundV1)
	round, err := f.keeper.ReadVerificationRound(f.ctx, key)
	require.NoError(t, err)
	require.NoError(t, f.keeper.extendValueEvidenceWindow(f.ctx, f.taskID, 50))
	const closeHeight = uint64(60)
	require.NoError(t, f.keeper.closeUnavailableChallengeRound(f.ctx, f.taskKey, closeHeight))

	summary, err := f.keeper.TaskRoundSummary.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.Equal(t, shared.RoundOutcomeV1_ROUND_OUTCOME_V1_UNAVAILABLE, summary.GetRound2Outcome())
	closed, err := f.keeper.ReadVerificationRound(f.ctx, key)
	require.NoError(t, err)
	require.Nil(t, closed.XRoundEffectPlanRoot, "effects wait for the evidence window")
	require.Equal(t, round.TaskId, closed.TaskId)

	deadline := summary.GetVerifierValueEvidenceCloseHeight()
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(deadline)))
	require.NoError(t, f.keeper.closeValueEvidenceWindow(f.ctx, f.taskKey, deadline, deadline))

	frozen, err := f.keeper.ReadVerificationRound(f.ctx, key)
	require.NoError(t, err)
	require.NotNil(t, frozen.XRoundEffectPlanRoot, "funding effects must be frozen after the window closes")
}
