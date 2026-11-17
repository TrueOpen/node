package keeper

import (
	"fmt"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDispatchGovernanceCannotReopenAClaimedRecordingWindow(t *testing.T) {
	for _, paidFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(paidFirst), func(t *testing.T) {
			f := newDispatchFixture(t)
			entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
			claim := dispatchFixtureClaim(t, f, entry, 150)
			if paidFirst {
				runDispatchFixtureQueue(t, f, dispatchClaimMaturity, claim.RebuttalDeadlineHeight+1)
			}
			params, err := f.keeper.Params.Get(f.ctx)
			require.NoError(t, err)
			params.Dispatch.RecordDeadlineBlocks = 60
			params.Session.SessionChangeDelayBlocks = 62
			params.Session.SessionWithdrawDelayBlocks = 62
			_, err = NewMsgServerImpl(f.keeper).UpdateTaskParams(f.ctx, &types.MsgUpdateTaskParams{Authority: sdk.AccAddress(f.keeper.authority).String(), Params: params})
			require.NoError(t, err)
			frozen, err := f.keeper.ParamsForSession(f.ctx, f.sessionID)
			require.NoError(t, err)
			require.Equal(t, uint64(20), frozen.Dispatch.RecordDeadlineBlocks)
			response, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[0], Entries: []*types.DispatchRecordEntryV1{dispatchEntryItem(entry)}})
			require.NoError(t, err)
			require.Equal(t, types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_RECORD_LATE, response.Results[0].RejectCode)
			accepted, err := f.keeper.TaskCore.Has(f.ctx, entry.Record.TaskId)
			require.NoError(t, err)
			require.False(t, accepted)
			if !paidFirst {
				runDispatchFixtureQueue(t, f, dispatchClaimMaturity, claim.RebuttalDeadlineHeight+1)
			}
			row, err := f.keeper.readDispatchClaim(f.ctx, claim.DispatchTicketHash)
			require.NoError(t, err)
			require.Equal(t, types.DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_PAID, row.Status)
			require.Equal(t, shared.NewAmount(1000), row.PaidAmount)
			require.NoError(t, f.keeper.EnsureDispatchInvariant(f.ctx))
			genesis, err := f.keeper.ExportGenesis(f.ctx)
			require.NoError(t, err)
			require.NoError(t, genesis.Validate())
			fresh := initInternalFixtureWithHub(t, f.hub)
			require.NoError(t, fresh.keeper.Params.Remove(fresh.ctx))
			fresh.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(fresh.ctx).WithChainID("dispatch-test").WithBlockHeight(sdk.UnwrapSDKContext(f.ctx).BlockHeight() + 1))
			require.NoError(t, fresh.keeper.InitGenesis(fresh.ctx, *genesis))
			restored, err := fresh.keeper.ParamsForSession(fresh.ctx, f.sessionID)
			require.NoError(t, err)
			require.Equal(t, frozen, restored)
		})
	}
}
