package keeper

import (
	"fmt"
	"testing"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func updateDispatchTestParams(t *testing.T, f *dispatchFixture, change func(*types.TaskParamsV1)) {
	t.Helper()
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	change(&params)
	_, err = NewMsgServerImpl(f.keeper).UpdateTaskParams(f.ctx, &types.MsgUpdateTaskParams{Authority: sdk.AccAddress(f.keeper.authority).String(), Params: params})
	require.NoError(t, err)
}

func TestSessionRecordingAndTakeoverKeepTheirOriginalClock(t *testing.T) {
	for _, original := range []uint64{20, 60} {
		t.Run(fmt.Sprint(original), func(t *testing.T) {
			for _, takeover := range []bool{false, true} {
				f := newDispatchFixtureAtHeight(t, 121, func(p *types.TaskParamsV1) {
					p.Dispatch.RecordDeadlineBlocks = original
					p.Session.SessionChangeDelayBlocks, p.Session.SessionWithdrawDelayBlocks = original+2, original+2
				})
				entry := f.entry(t, f.signedOrder(t, 0, f.ownerKey), f.hub.builders[0])
				updateDispatchTestParams(t, f, func(p *types.TaskParamsV1) {
					p.Dispatch.RecordDeadlineBlocks = 80 - original
					p.Session.SessionChangeDelayBlocks, p.Session.SessionWithdrawDelayBlocks = 82-original, 82-original
				})
				if takeover {
					dispatchFixtureHeight(f, 130)
					response, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[1], Entries: []*types.DispatchRecordEntryV1{{Entry: &types.DispatchRecordEntryV1_Takeover{Takeover: &types.TakeoverEntryV1{SignedOrder: entry.SignedOrder}}}}})
					require.NoError(t, err)
					require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Results[0].Status)
					stream, err := f.keeper.ReadStream(f.ctx, f.sessionID)
					require.NoError(t, err)
					require.Equal(t, uint64(130)+original, stream.GetPendingTakeover().ActivationHeight)
				} else {
					dispatchFixtureHeight(f, 150)
					response, err := NewMsgServerImpl(f.keeper).RecordDispatch(f.ctx, &types.MsgRecordDispatch{SubmitterAddress: f.hub.builders[0], Entries: []*types.DispatchRecordEntryV1{dispatchEntryItem(entry)}})
					require.NoError(t, err)
					if original == 20 {
						require.Equal(t, types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_RECORD_LATE, response.Results[0].RejectCode)
					} else {
						require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Results[0].Status)
						record, err := f.keeper.readDispatchRecord(f.ctx, entry.Record.TaskId)
						require.NoError(t, err)
						require.Equal(t, uint64(123+60+60), record.ClaimWindowCloseHeight)
					}
				}
			}
		})
	}
}

func TestUnrecordedDoubleDispatchKeepsSessionChallengeClock(t *testing.T) {
	f := newDispatchFixture(t)
	entry := f.entry(t, f.signedOrder(t, 0, f.ownerKey), f.hub.builders[0])
	second := *entry.Record.Rounds[0].Attempts[0]
	second.Rank, second.DispatchTicketHash = 1, bytes32(0x94)
	entry.Record.Rounds[0].Attempts = append(entry.Record.Rounds[0].Attempts, &second)
	signDispatchTestMaterial(t, dispatchRecordMessageName, entry.Record, f.hub.keys[entry.Record.BuilderAddress], func(s []byte) { entry.Record.BuilderServiceSignature = s })
	updateDispatchTestParams(t, f, func(p *types.TaskParamsV1) {
		p.Dispatch.RecordDeadlineBlocks, p.Dispatch.DispatchChallengeWindowBlocks = 60, 120
		p.Session.SessionChangeDelayBlocks, p.Session.SessionWithdrawDelayBlocks = 62, 62
	})
	dispatchFixtureHeight(f, 204)
	_, err := NewMsgServerImpl(f.keeper).ChallengeDispatchDoubleDispatch(f.ctx, &types.MsgChallengeDispatchDoubleDispatch{Order: entry.SignedOrder, Record: entry.Record, ChallengerAddress: f.owner})
	code, business := dispatchRejectionCode(err)
	require.True(t, business)
	require.Equal(t, types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_CHALLENGE_WINDOW_CLOSED, code)
}
