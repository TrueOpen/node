package keeper

import (
	"fmt"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

type recoveryWindowHub struct{ *dispatchTestHub }

func (h recoveryWindowHub) GetHubParams(ctx sdk.Context) hubtypes.HubParamsSnapshot {
	params := h.dispatchTestHub.GetHubParams(ctx)
	params.BuilderSetUpdateLeadBlocks = 8400
	return params
}

func TestLongRecoveryWindowsDoNotDelayRecordingOrTheNextOrder(t *testing.T) {
	for _, elapsed := range []uint64{0, 600, 601} {
		t.Run(fmt.Sprint(elapsed), func(t *testing.T) {
			f := newDispatchFixture(t)
			f.keeper.hubKeeper = recoveryWindowHub{f.hub}
			updateDispatchTestParams(t, f, func(p *types.TaskParamsV1) {
				p.Dispatch.MaxOrderLifetimeBlocks, p.Dispatch.RecordDeadlineBlocks = 7200, 600
				p.Dispatch.DispatchCompensationClaimWindowBlocks, p.Dispatch.DispatchCompensationRebuttalBlocks = 7200, 1800
				p.Dispatch.DispatchChallengeWindowBlocks, p.Dispatch.DispatchChallengeResponseBlocks = 7200, 1800
				p.Session.SessionWithdrawDelayBlocks, p.Session.SessionChangeDelayBlocks = 720, 720
				p.Evidence.MaxEvidenceRetentionBlocks = 14400
			})
			server := NewMsgServerImpl(f.keeper)
			created, err := server.CreateSession(f.ctx, &types.MsgCreateSession{SignerAddress: f.owner, InitialBuilder: f.hub.builders[0], InitialDeposit: shared.NewAmount(dispatchTestPrefund)})
			require.NoError(t, err)
			f.sessionID = created.SessionId
			start := sdk.UnwrapSDKContext(f.ctx).BlockHeight()
			entry := f.entry(t, f.signedOrder(t, 0, f.ownerKey), f.hub.builders[0])
			f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(start + int64(elapsed)))
			if elapsed != 0 {
				f.hub.pool.Status = hubtypes.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_EXPIRED
			}
			result, err := server.RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[0], Entries: []*types.DispatchRecordEntryV1{dispatchEntryItem(entry)}})
			require.NoError(t, err)
			if elapsed > 600 {
				require.Equal(t, types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_RECORD_LATE, result.Results[0].RejectCode)
				return
			}
			require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, result.Results[0].Status)
			record, err := f.keeper.readDispatchRecord(f.ctx, entry.Record.TaskId)
			require.NoError(t, err)
			require.Equal(t, uint64(start)+elapsed, record.RecordedHeight)
			if elapsed == 0 {
				second := f.entry(t, f.signedOrder(t, 1, f.ownerKey), f.hub.builders[0])
				result, err := server.RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[0], Entries: []*types.DispatchRecordEntryV1{dispatchEntryItem(second)}})
				require.NoError(t, err)
				require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, result.Results[0].Status)
				stream, err := f.keeper.ReadStream(f.ctx, f.sessionID)
				require.NoError(t, err)
				require.Equal(t, uint64(2), stream.NextExpectedSequence)
				require.Equal(t, uint32(2), stream.OpenPendingCount, "prior finality must not gate a later healthy order")
				require.Equal(t, start, sdk.UnwrapSDKContext(f.ctx).BlockHeight(), "successful recording must not wait for the recovery window")
			}
		})
	}
}
