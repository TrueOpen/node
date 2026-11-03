package keeper

import (
	"testing"

	"github.com/TrueOpen/node/x/task/types"
	"github.com/stretchr/testify/require"
)

func TestCompactedSessionCannotReopenDoubleDispatchUnderCurrentPolicy(t *testing.T) {
	f := newDispatchFixtureAtHeight(t, 121, func(p *types.TaskParamsV1) {
		p.Dispatch.DispatchChallengeWindowBlocks = 1
		p.Session.SessionOrderRetentionBlocks = 110
	})
	entry := f.entry(t, f.signedOrder(t, 0, f.ownerKey), f.hub.builders[0])
	second := *entry.Record.Rounds[0].Attempts[0]
	second.Rank, second.DispatchTicketHash = 1, bytes32(0x94)
	entry.Record.Rounds[0].Attempts = append(entry.Record.Rounds[0].Attempts, &second)
	signDispatchTestMaterial(t, dispatchRecordMessageName, entry.Record, f.hub.keys[entry.Record.BuilderAddress], func(s []byte) { entry.Record.BuilderServiceSignature = s })
	dispatchFixtureHeight(f, 124)
	pending := requestTestWithdrawal(t, f, true, 0)
	updateDispatchTestParams(t, f, func(p *types.TaskParamsV1) {
		p.Dispatch.RecordDeadlineBlocks, p.Dispatch.DispatchChallengeWindowBlocks = 60, 120
		p.Session.SessionChangeDelayBlocks, p.Session.SessionWithdrawDelayBlocks = 62, 62
		p.Session.SessionOrderRetentionBlocks = 210
	})
	dispatchFixtureHeight(f, pending.PendingWithdrawEffectiveHeight)
	changed, err := f.keeper.applySessionPendingRow(f.ctx, sessionPendingWithdraw, sessionPendingRow{height: pending.PendingWithdrawEffectiveHeight, sessionID: f.sessionID}, pending.PendingWithdrawEffectiveHeight)
	require.NoError(t, err)
	require.True(t, changed)
	height := pending.PendingWithdrawEffectiveHeight + 110
	dispatchFixtureHeight(f, height)
	result, err := f.keeper.SweepSessionHistoryPrune(f.ctx, height, 4)
	require.NoError(t, err)
	require.Equal(t, uint64(1), result.CompactedCount)
	_, err = NewMsgServerImpl(f.keeper).ChallengeDispatchDoubleDispatch(f.ctx, &types.MsgChallengeDispatchDoubleDispatch{Order: entry.SignedOrder, Record: entry.Record, ChallengerAddress: f.owner})
	code, business := dispatchRejectionCode(err)
	require.True(t, business)
	require.Equal(t, types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_CHALLENGE_WINDOW_CLOSED, code)
}
