package keeper_test

import (
	"bytes"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/keeper"
	"github.com/TrueOpen/node/x/hub/types"
)

// The dispatch pause is a set of reasons on the service bond; dispatch_paused
// is set exactly while any of them is, and the quota query reports both.
func TestWorkerDispatchPauseFollowsItsReasons(t *testing.T) {
	f, operator, _ := auditFixture(t, nil)
	late := types.WorkerPauseReasonLateSubmission
	const unbonding = uint32(2)
	quota := func() *types.QueryWorkerQuotaResponse {
		response, err := keeper.NewQueryServerImpl(f.keeper).WorkerQuota(f.ctx, &types.QueryWorkerQuotaRequest{OperatorAddress: operator})
		require.NoError(t, err)
		return response
	}
	require.False(t, quota().DispatchPaused)
	before, err := f.keeper.GetServiceBondState(f.ctx, operator)
	require.NoError(t, err)

	reasons, changed, err := f.keeper.SetWorkerDispatchPause(f.ctx, operator, late, true)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, late, reasons)
	require.True(t, quota().DispatchPaused)
	require.Equal(t, late, quota().PauseReasons)

	_, changed, err = f.keeper.SetWorkerDispatchPause(f.ctx, operator, late, true)
	require.NoError(t, err)
	require.False(t, changed, "setting a standing reason again changes nothing")

	reasons, changed, err = f.keeper.SetWorkerDispatchPause(f.ctx, operator, unbonding, true)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, late|unbonding, reasons)
	reasons, changed, err = f.keeper.SetWorkerDispatchPause(f.ctx, operator, late, false)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, unbonding, reasons)
	require.True(t, quota().DispatchPaused, "another reason still stands")
	reasons, _, err = f.keeper.SetWorkerDispatchPause(f.ctx, operator, unbonding, false)
	require.NoError(t, err)
	require.Zero(t, reasons)
	require.False(t, quota().DispatchPaused)

	after, err := f.keeper.GetServiceBondState(f.ctx, operator)
	require.NoError(t, err)
	require.Equal(t, before, after, "a pause that came and went leaves the bond as it was")
	require.NoError(t, f.keeper.EnsureServiceBondInvariant(f.ctx))

	_, _, err = f.keeper.SetWorkerDispatchPause(f.ctx, operator, 8, true)
	require.Error(t, err, "an undefined reason is refused")
	_, _, err = f.keeper.SetWorkerDispatchPause(f.ctx, operator, 0, true)
	require.Error(t, err)
	stranger := sdk.AccAddress(bytes.Repeat([]byte{0x6f}, 20)).String()
	reasons, changed, err = f.keeper.SetWorkerDispatchPause(f.ctx, stranger, late, true)
	require.NoError(t, err, "an operator without a bond has nothing to pause")
	require.False(t, changed)
	require.Zero(t, reasons)
}
