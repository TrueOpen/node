package keeper

import (
	"bytes"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func TestDispatchClaimAuthenticatesRebutsAndPaysAtomically(t *testing.T) {
	for _, rebut := range []bool{false, true} {
		t.Run(map[bool]string{false: "payment rollback and replay", true: "incomplete timeout rebuttal"}[rebut], func(t *testing.T) {
			f := newDispatchFixture(t)
			entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
			request := &types.MsgClaimDispatchCompensation{Order: entry.SignedOrder, Ticket: entry.WinnerTicket, Accept: entry.Accept, SubmitterAddress: f.worker}
			server := NewMsgServerImpl(f.keeper)
			_, err := server.ClaimDispatchCompensation(f.ctx, request)
			require.Error(t, err, "record deadline is inclusive")
			f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(144))
			entry.Accept.ServiceSignature[0] ^= 1
			_, err = server.ClaimDispatchCompensation(f.ctx, request)
			require.Error(t, err)
			found, err := f.keeper.DispatchCompensationClaim.Has(f.ctx, entry.Accept.DispatchTicketHash)
			require.NoError(t, err)
			require.False(t, found)
			entry.Accept.ServiceSignature[0] ^= 1
			created, err := server.ClaimDispatchCompensation(f.ctx, request)
			require.NoError(t, err)
			require.Equal(t, shared.NewAmount(1000), created.Amount)
			if rebut {
				attempt := entry.Record.Rounds[0].Attempts[0]
				attempt.Outcome = types.DispatchAttemptOutcomeV1_DISPATCH_ATTEMPT_OUTCOME_V1_TIMEOUT
				attempt.TimeoutTierOrZero = 1
				signDispatchTestMaterial(t, "task.v1.DispatchRecordV1", entry.Record, f.hub.keys[entry.Record.BuilderAddress], func(s []byte) { entry.Record.BuilderServiceSignature = s })
				_, err = server.RebutDispatchCompensation(f.ctx, &types.MsgRebutDispatchCompensation{DispatchTicketHash: created.DispatchTicketHash, Record: entry.Record, ResponderAddress: f.hub.builders[1]})
				require.NoError(t, err, "a group Builder may return a signed incomplete record")
				row, err := f.keeper.readDispatchClaim(f.ctx, created.DispatchTicketHash)
				require.NoError(t, err)
				require.Equal(t, types.DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_DISMISSED, row.Status)
				require.Equal(t, entry.SignedOrder.Order.EarliestSubmitHeight, row.EarliestSubmitHeight)
			} else {
				height := created.RebuttalDeadlineHeight + 1
				f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(height)))
				queue := f.keeper.newDispatchDeadlineQueue(dispatchClaimMaturity)
				_, found, err := queue.head(f.ctx, height)
				require.NoError(t, err)
				require.True(t, found)
				f.hub.faultFailure = true
				_, err = queue.run(f.ctx, height, 1, 1<<20)
				require.ErrorContains(t, err, "custody failure")
				row, err := f.keeper.readDispatchClaim(f.ctx, created.DispatchTicketHash)
				require.NoError(t, err)
				require.Equal(t, types.DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_PENDING_REBUTTAL, row.Status)
				f.hub.faultFailure = false
				_, err = queue.run(f.ctx, height, 1, 1<<20)
				require.NoError(t, err)
				row, err = f.keeper.readDispatchClaim(f.ctx, created.DispatchTicketHash)
				require.NoError(t, err)
				require.Equal(t, shared.NewAmount(1000), row.PaidAmount)
				require.Equal(t, types.DispatchCompensationClaimStatusV1_DISPATCH_COMPENSATION_CLAIM_STATUS_V1_PAID, row.Status)
				require.Equal(t, entry.SignedOrder.Order.EarliestSubmitHeight, row.EarliestSubmitHeight)
			}
			events := len(sdk.UnwrapSDKContext(f.ctx).EventManager().Events())
			again, err := server.ClaimDispatchCompensation(f.ctx, request)
			require.NoError(t, err)
			require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, again.Status)
			require.Len(t, sdk.UnwrapSDKContext(f.ctx).EventManager().Events(), events)
		})
	}
}

func TestRosterChallengeRecoversOrMaturesOnceAndKeepsFrozenWindow(t *testing.T) {
	for _, respond := range []bool{false, true} {
		t.Run(map[bool]string{false: "timeout fault and objective dedup", true: "group response"}[respond], func(t *testing.T) {
			f := newDispatchFixture(t)
			entry := f.entry(t, f.signedOrder(t, 0, f.delegateKey), f.hub.builders[0])
			roster := f.committedRoster(t, entry, 0)
			f.record(t, entry)
			server := NewMsgServerImpl(f.keeper)
			// Updating live parameters must not shorten an accepted Task's window.
			params, err := f.keeper.Params.Get(f.ctx)
			require.NoError(t, err)
			params.Dispatch.DispatchChallengeWindowBlocks = 1
			_, err = server.UpdateTaskParams(f.ctx, &types.MsgUpdateTaskParams{Authority: sdk.AccAddress(f.keeper.authority).String(), Params: params})
			require.NoError(t, err)
			f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(130))
			opened, err := server.ReportDispatchRosterUnavailable(f.ctx, &types.MsgReportDispatchRosterUnavailable{TaskId: entry.Record.TaskId, DispatchRound: 0, ReporterAddress: f.owner})
			require.NoError(t, err)
			if respond {
				_, err = server.RespondDispatchChallenge(f.ctx, &types.MsgRespondDispatchChallenge{ChallengeId: opened.Challenge.ChallengeId, ResponderAddress: f.hub.builders[1], Response: &types.MsgRespondDispatchChallenge_Roster{Roster: roster}})
				require.NoError(t, err)
			} else {
				height := opened.Challenge.GetProofDeadlineHeight() + 1
				f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(height)))
				q := f.keeper.newDispatchDeadlineQueue(dispatchChallengeMaturity)
				_, found, err := q.head(f.ctx, height)
				require.NoError(t, err)
				require.True(t, found)
				f.hub.faultFailure = true
				_, err = q.run(f.ctx, height, 1, 1<<20)
				require.ErrorContains(t, err, "custody failure")
				row, err := f.keeper.readDispatchChallenge(f.ctx, opened.Challenge.ChallengeId)
				require.NoError(t, err)
				require.True(t, dispatchChallengePending(row.Status))
				f.hub.faultFailure = false
				_, err = q.run(f.ctx, height, 1, 1<<20)
				require.NoError(t, err)
				row, err = f.keeper.readDispatchChallenge(f.ctx, opened.Challenge.ChallengeId)
				require.NoError(t, err)
				require.Equal(t, types.DispatchChallengeStatusV1_DISPATCH_CHALLENGE_STATUS_V1_UPHELD, row.Status)
				require.Equal(t, opened.Challenge.ChallengeId, row.GetFaultId())
				_, err = server.ReportDispatchRosterUnavailable(f.ctx, &types.MsgReportDispatchRosterUnavailable{TaskId: entry.Record.TaskId, DispatchRound: 0, ReporterAddress: f.worker})
				require.Error(t, err)
			}
			row, err := f.keeper.readDispatchChallenge(f.ctx, opened.Challenge.ChallengeId)
			require.NoError(t, err)
			require.False(t, dispatchChallengePending(row.Status))
			pending, err := f.keeper.pendingDispatchCount(f.ctx, entry.Record.TaskId)
			require.NoError(t, err)
			require.Zero(t, pending)
			query := &queryServer{k: f.keeper}
			listed, err := query.DispatchChallengesByBuilder(f.ctx, &types.QueryDispatchChallengesByBuilderRequest{BuilderAddress: f.hub.builders[0]})
			require.NoError(t, err)
			require.Empty(t, listed.Rows)
			f.hub.queryMaxBytes = 1
			_, err = query.DispatchChallengesByBuilder(f.ctx, &types.QueryDispatchChallengesByBuilderRequest{BuilderAddress: f.hub.builders[0]})
			require.Equal(t, codes.ResourceExhausted, status.Code(err))
			require.NoError(t, f.keeper.EnsureDispatchInvariant(f.ctx))
		})
	}
}

func TestSessionDelegateCannotManageCustody(t *testing.T) {
	f := newDispatchFixture(t)
	stream, err := f.keeper.ReadStream(f.ctx, f.sessionID)
	require.NoError(t, err)
	delegate, err := f.keeper.addressCodec.BytesToString(stream.GetSessionKey())
	require.NoError(t, err)
	_, err = NewMsgServerImpl(f.keeper).DepositSession(f.ctx, &types.MsgDepositSession{SessionId: f.sessionID, SignerAddress: delegate, Amount: shared.NewAmount(1)})
	require.ErrorIs(t, err, types.ErrSessionKeyCannotFund)
	after, err := f.keeper.ReadStream(f.ctx, f.sessionID)
	require.NoError(t, err)
	require.Equal(t, stream, after)
	require.True(t, bytes.Equal(stream.GetSessionKey(), after.GetSessionKey()))
}
