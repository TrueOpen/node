package keeper

import (
	"fmt"
	"testing"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func TestDispatchRecordingAfterClaimPreservesMaturityAndGenesis(t *testing.T) {
	for _, paidFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(paidFirst), func(t *testing.T) {
			f := newDispatchFixture(t)
			entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
			f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(150))
			server := NewMsgServerImpl(f.keeper)
			claim, err := server.ClaimDispatchCompensation(f.ctx, &types.MsgClaimDispatchCompensation{Order: entry.SignedOrder, Ticket: entry.WinnerTicket, Accept: entry.Accept, SubmitterAddress: f.worker})
			require.NoError(t, err)
			mature := func() {
				t.Helper()
				height := claim.RebuttalDeadlineHeight + 1
				f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(height)))
				queue := f.keeper.newDispatchDeadlineQueue(dispatchClaimMaturity)
				_, found, err := queue.head(f.ctx, height)
				require.NoError(t, err)
				require.True(t, found)
				_, err = queue.run(f.ctx, height, 1, 1<<20)
				require.NoError(t, err)
			}
			if paidFirst {
				mature()
				f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(182))
			}
			params, err := f.keeper.Params.Get(f.ctx)
			require.NoError(t, err)
			params.Dispatch.RecordDeadlineBlocks = 60
			params.Session.SessionChangeDelayBlocks = 62
			params.Session.SessionWithdrawDelayBlocks = 62
			_, err = server.UpdateTaskParams(f.ctx, &types.MsgUpdateTaskParams{Authority: sdk.AccAddress(f.keeper.authority).String(), Params: params})
			require.NoError(t, err)
			f.record(t, entry)
			require.NoError(t, f.keeper.EnsureDispatchInvariant(f.ctx))
			genesis := types.DefaultGenesis()
			require.NoError(t, f.keeper.exportDispatchGenesis(f.ctx, genesis))
			require.NoError(t, genesis.Validate())
			restored := newDispatchFixture(t)
			restored.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(restored.ctx).WithBlockHeight(sdk.UnwrapSDKContext(f.ctx).BlockHeight()))
			require.NoError(t, restored.keeper.initDispatchGenesis(restored.ctx, *genesis, uint64(sdk.UnwrapSDKContext(f.ctx).BlockHeight())))
			if !paidFirst {
				mature()
			}
			row, err := f.keeper.readDispatchClaim(f.ctx, claim.DispatchTicketHash)
			require.NoError(t, err)
			if paidFirst {
				require.Equal(t, types.DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_PAID, row.Status)
				require.Equal(t, shared.NewAmount(1000), row.PaidAmount)
				for _, invalidHeight := range []uint64{182, 183} {
					bad := genesis.DispatchCompensationClaims[0]
					bad.XResolvedHeight = &types.DispatchCompensationClaimState_ResolvedHeight{ResolvedHeight: invalidHeight}
					genesis.DispatchCompensationClaims[0] = bad
					require.ErrorContains(t, genesis.Validate(), "paid at or after winner exclusion")
				}
			} else {
				require.Equal(t, types.DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_DISMISSED, row.Status)
				require.Equal(t, shared.NewAmount(0), row.PaidAmount)
			}
			require.NoError(t, f.keeper.EnsureDispatchInvariant(f.ctx))
		})
	}
}
