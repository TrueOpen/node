package keeper

import (
	"bytes"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func feeRound(voted ...bool) types.AnchorPanelRoundState {
	round := types.AnchorPanelRoundState{ClosedHeight: 1}
	for index, cast := range voted {
		member := sdk.AccAddress(bytes.Repeat([]byte{byte(0x50 + len(round.Members) + index)}, 20)).String() + string(rune('a'+index))
		round.Members = append(round.Members, member)
		if cast {
			round.Votes = append(round.Votes, types.AnchorPanelVoteState{VerifierOperatorAddress: member, Verdict: votePass})
		}
	}
	return round
}

// Each paid round is worth one round fee, one seat per member: a voter is
// paid its seat less maintenance, the seat of a member that did not vote goes
// to the treasury, and a further round is paid only while the reserved budget
// covers a whole round fee.
func TestPanelPayoutsFollowSeatsVotesAndBudget(t *testing.T) {
	const fee, maintenanceBps = uint64(75), uint64(500)
	first := feeRound(true, true, false)
	second := feeRound(true, false, false)

	payouts, seat, net, maintenance, err := appendAnchorPanelPayouts(nil, []types.AnchorPanelRoundState{first, second}, fee, maintenanceBps, 285, 15, 0, 1_000)
	require.NoError(t, err)
	require.Equal(t, uint64(25), seat)
	require.Len(t, payouts, 3)
	for index, payout := range payouts {
		require.Equal(t, uint32(index), payout.SelectedVerifierIndex)
		require.Equal(t, shared.NewAmount(25), payout.Gross)
		require.Equal(t, shared.NewAmount(1), payout.Maintenance)
		require.Equal(t, shared.NewAmount(24), payout.Net)
	}
	require.Equal(t, first.Members[0], payouts[0].OperatorAddress)
	require.Equal(t, second.Members[0], payouts[2].OperatorAddress)
	require.Equal(t, uint64(285+3*24), net)
	require.Equal(t, uint64(15+3*1+3*25), maintenance, "three unvoted seats go to the treasury")

	// 300 already committed and 100 of gas: 500 covers one round fee, not two.
	payouts, _, net, maintenance, err = appendAnchorPanelPayouts(nil, []types.AnchorPanelRoundState{first, second}, fee, maintenanceBps, 285, 15, 100, 500)
	require.NoError(t, err)
	require.Len(t, payouts, 2, "the second round does not fit the budget and is not paid")
	require.Equal(t, uint64(285+2*24), net)
	require.Equal(t, uint64(15+2*1+25), maintenance)

	payouts, seat, _, _, err = appendAnchorPanelPayouts(nil, []types.AnchorPanelRoundState{first}, 0, maintenanceBps, 0, 0, 0, 1_000)
	require.NoError(t, err)
	require.Empty(t, payouts, "a task that is worth nothing pays no panel")
	require.Zero(t, seat)
}

// The rounds a task pays for: drawn and closed, the re-audit excluded; and
// only for a panel audit.
func TestPaidPanelRoundsLeaveOutUndrawnAndReauditRounds(t *testing.T) {
	drawn := feeRound(true, true, true)
	undrawn := types.AnchorPanelRoundState{ClosedHeight: 1}
	open := feeRound(true)
	open.ClosedHeight = 0
	reaudit := feeRound(true, true, true)
	reaudit.Kind = types.AnchorPanelRoundKindV1_ANCHOR_PANEL_ROUND_KIND_V1_REAUDIT
	audit := &types.AnchorAuditState{AuditMode: types.AnchorAuditModePanel, PanelRounds: []types.AnchorPanelRoundState{drawn, undrawn, open, reaudit}}
	require.Len(t, anchorPanelPaidRounds(audit), 1)
	audit.AuditMode = 0
	require.Empty(t, anchorPanelPaidRounds(audit))
	require.Empty(t, anchorPanelPaidRounds(nil))
}

// An automatic re-assignment is a second paid round: the members of both
// rounds that voted are paid, from what the user reserved.
func TestReassignedRoundIsPaidAsWell(t *testing.T) {
	f := newBatchFixture(t, 4_000, 9)
	f.openAndProveSampled(t, f.audit)
	f.reload(t)
	first := f.assign(t)
	voteAt := first.AssignmentHeight + 1
	f.castVotes(t, first, voteAt, votePass, voteReject, voteUnavailable)
	f.sweep(t, voteAt)
	second := f.assign(t)
	f.castVotes(t, second, second.AssignmentHeight+1, votePass, votePass)
	audit := f.reload(t)
	f.sweep(t, audit.PublicationEndHeight)
	settlement, err := f.settleNow(t, audit.PublicationEndHeight+1)
	require.NoError(t, err)
	f.requireAuditedPaid(t, settlement)
	require.Equal(t, uint32(5), settlement.VerifierPayoutCount)
	for _, member := range first.Members {
		require.Equal(t, batchSeatNet, f.credited(member), "every vote is paid, whatever its value")
	}
	require.Equal(t, batchSeatNet, f.credited(second.Members[1]))
	require.Zero(t, f.credited(second.Members[2]))
	// reserved = Worker gross + five seats + one unvoted seat + refund
	require.Equal(t, shared.NewAmount(f.reserved-batchGross-2*batchRoundFee), settlement.RefundAmount)
}

// The re-audit panel is paid from the fee its opener put up, when the task
// settles: a seat to each member that voted, the rest to the treasury. A fee
// that went back to the opener leaves nothing to pay.
func TestReauditPanelIsPaidFromTheOpenersFee(t *testing.T) {
	for _, overturn := range []bool{false, true} {
		f := newBatchFixture(t, 4_000, 9)
		f.openAndProveSampled(t, f.audit)
		f.reload(t)
		audit := f.publish(t, votePass)
		opener := sdk.AccAddress(bytes.Repeat([]byte{0xd1}, 20)).String()
		_, err := f.server.RequestAnchorReaudit(f.at(int64(audit.ProvisionalHeight+1)), f.reauditMsg(opener, batchRoundFee))
		require.NoError(t, err)
		reaudit := f.assign(t)
		verdict := votePass
		if overturn {
			verdict = voteReject
		}
		f.castVotes(t, reaudit, reaudit.AssignmentHeight+1, verdict, verdict)
		audit = f.reload(t)
		require.NotZero(t, audit.ResultHeight)
		_, err = f.settleNow(t, audit.ResultHeight+1)
		require.NoError(t, err)
		if overturn {
			require.Zero(t, f.credited(reaudit.Members[0]), "the fee went back to the opener")
			require.Zero(t, f.transfers(shared.TaskChallengeEffectModuleName, hubtypes.TreasuryModuleName))
			continue
		}
		require.Equal(t, batchSeat, f.credited(reaudit.Members[0]))
		require.Equal(t, batchSeat, f.credited(reaudit.Members[1]))
		require.Zero(t, f.credited(reaudit.Members[2]))
		require.Equal(t, int64(batchSeat), f.transfers(shared.TaskChallengeEffectModuleName, hubtypes.TreasuryModuleName), "the unvoted seat")
		require.Equal(t, int64(2*batchSeat), f.transfers(shared.TaskChallengeEffectModuleName, hubtypes.RewardsModuleName))
	}
}
