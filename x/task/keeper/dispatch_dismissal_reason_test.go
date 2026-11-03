package keeper

import (
	"testing"

	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
	"github.com/stretchr/testify/require"
)

func TestAutomaticCompensationDismissalDoesNotInventAnAcceptance(t *testing.T) {
	for _, pruned := range []bool{false, true} {
		f := newDispatchFixture(t)
		entry := f.entry(t, f.signedOrder(t, 0, f.ownerKey), f.hub.builders[0])
		claim := dispatchFixtureClaim(t, f, entry, 144)
		want := types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_COMPENSATION_NOT_CLAIMABLE
		if pruned {
			// Exercise the defensive retained-header path; normal cleanup waits
			// for pending claims and cannot create this shape.
			require.NoError(t, f.keeper.TaskTerminalSummary.Set(f.ctx, claim.TaskId, internaltypes.TaskTerminalSummaryStoreState{TaskId: claim.TaskId}))
			want = types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_COMPENSATION_RECORD_PRUNED
		} else {
			zero := uint64(0)
			f.hub.availableBond = &zero
			response, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[0], Entries: []*types.DispatchRecordEntryV1{dispatchEntryItem(entry)}})
			require.NoError(t, err)
			require.Equal(t, types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_WINNER_NOT_ELIGIBLE, response.Results[0].RejectCode)
		}
		runDispatchFixtureQueue(t, f, dispatchClaimMaturity, claim.RebuttalDeadlineHeight+1)
		event := lastTypedEvent(t, f.ctx, &types.EventDispatchCompensationRebutted{}).(*types.EventDispatchCompensationRebutted)
		require.Equal(t, types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_UNSPECIFIED, event.AttemptOutcome)
		require.Equal(t, want, event.GetDismissalReason())
		require.NotNil(t, event.XDismissalReason)
	}
}
