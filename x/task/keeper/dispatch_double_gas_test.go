package keeper

import (
	"context"
	"testing"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func TestDoubleDispatchGasCapRollsBackAndChargesTheParent(t *testing.T) {
	f := newDispatchFixture(t)
	const limit = uint64(1000)
	updateDispatchTestParams(t, f, func(p *types.TaskParamsV1) {
		for _, policy := range p.Dispatch.DispatchChallengeGasCapByKind {
			if policy.Kind == shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_DOUBLE_DISPATCH {
				policy.GasCap = limit
			}
		}
	})
	parent := sdk.UnwrapSDKContext(f.ctx).GasMeter()
	before := parent.GasConsumed()
	_, _, err := f.keeper.runDispatchChallenge(f.ctx, nil, shared.DispatchChallengeKindV1_DISPATCH_CHALLENGE_KIND_V1_DOUBLE_DISPATCH, 0, func(ctx context.Context, _ types.TaskParamsV1, _ uint64) (types.DispatchChallengeState, shared.MutationStatusV1, error) {
		require.NoError(t, f.keeper.OrderSequence.Set(ctx, types.NewOrderSequenceStateKey(f.sessionID, 0), types.OrderSequenceState{SessionId: f.sessionID, Status: types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_UNUSED, CompensationPaid: true}))
		sdk.UnwrapSDKContext(ctx).GasMeter().ConsumeGas(limit+1, "double-dispatch regression")
		return types.DispatchChallengeState{}, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, nil
	})
	require.ErrorContains(t, err, "gas cap exceeded")
	present, err := f.keeper.OrderSequence.Has(f.ctx, types.NewOrderSequenceStateKey(f.sessionID, 0))
	require.NoError(t, err)
	require.False(t, present)
	require.GreaterOrEqual(t, parent.GasConsumed()-before, limit)
}
