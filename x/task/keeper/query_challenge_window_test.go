package keeper

import (
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func openChallengeQueryFixture(t *testing.T) (*verificationFixture, types.VerifierCandidateWindowState) {
	t.Helper()
	f := newVerificationFixture(t)
	core, err := f.keeper.TaskCore.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	core.AcceptedTaskHash = bytes32(0x81)
	core.TaskPhase = types.TaskPhase_TASK_PHASE_REVEALING
	core.VerificationStatus = types.VerificationStatus_VERIFICATION_STATUS_VERIFICATION_PASSED
	core.EffectiveVerifyRound = 1
	require.NoError(t, f.keeper.TaskCore.Set(f.ctx, f.taskKey, core))
	closed := types.VerificationRoundState{TaskId: f.taskID, VerifyRound: 1, RoundId: bytes32(0x82), GeneratedTokenCount: 1,
		XClosedHeight:   &types.VerificationRoundState_ClosedHeight{ClosedHeight: 10},
		XVerdict:        &types.VerificationRoundState_Verdict{Verdict: types.TaskVerdict_TASK_VERDICT_PASS},
		XRoundFactsHash: &types.VerificationRoundState_RoundFactsHash{RoundFactsHash: bytes32(0x83)}}
	require.NoError(t, f.keeper.WriteVerificationRound(f.ctx, types.NewVerifyRoundKey(f.taskKey, 1), closed))
	require.NoError(t, f.keeper.TaskRoundSummary.Set(f.ctx, f.taskKey, types.TaskRoundSummaryState{TaskId: f.taskID, MaxClosedRound: 1,
		Round1FactsHashOrZero32: closed.GetRoundFactsHash(), XChallengeOpenHeight: &types.TaskRoundSummaryState_ChallengeOpenHeight{ChallengeOpenHeight: 10},
		XChallengeCloseHeight: &types.TaskRoundSummaryState_ChallengeCloseHeight{ChallengeCloseHeight: 100}}))
	require.NoError(t, f.keeper.TaskBudget.Set(f.ctx, f.taskKey, types.TaskBudgetState{TaskId: f.taskID, PriceBid: shared.NewAmount(1_000_000), BudgetStatus: types.TaskBudgetStatus_TASK_BUDGET_STATUS_RESERVED}))
	require.NoError(t, f.keeper.WriteInferReceipt(f.ctx, f.taskKey, types.InferReceiptState{TaskId: f.taskID, WinnerWorker: f.operators[0], InferReceiptHash: bytes32(0x84)}))
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	params.Challenge.ChallengeOpenBond = shared.NewAmount(100)
	require.NoError(t, f.keeper.Params.Set(f.ctx, params))
	bank := dispatchTestBank{store: f.keeper.storeService}
	f.keeper.bankKeeper = bank
	require.NoError(t, bank.setBalance(f.ctx, sdk.MustAccAddressFromBech32(f.operators[0]), 1000))
	_, err = NewMsgServerImpl(f.keeper).OpenChallengeRound(f.ctx, &types.MsgOpenChallengeRound{TaskId: f.taskID, OpenerAddress: f.operators[0], MaxTotalDebit: shared.NewAmount(1000)})
	require.NoError(t, err)
	window, err := f.keeper.VerifierCandidateWindow.Get(f.ctx, types.NewVerifyRoundKey(f.taskKey, 2))
	require.NoError(t, err)
	previous := window
	previous.VerifyRound = 1
	previous.EligibilityFrozenHeight, previous.WindowRandomnessHeight, previous.BuilderProposalCloseHeight = 1, 2, 3
	previous.HandraiseCloseHeight, previous.SelectionRandomnessHeight, previous.AssignmentDeadlineHeight = 4, 5, 6
	require.NoError(t, f.keeper.VerifierCandidateWindow.Set(f.ctx, types.NewVerifyRoundKey(f.taskKey, 1), previous))
	return f, window
}

func TestStageUsesOpenChallengeWindowBeforeEffectiveRoundChanges(t *testing.T) {
	f, window := openChallengeQueryFixture(t)
	for _, test := range []struct {
		status           types.VerificationStatus
		height, deadline uint64
	}{
		{types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_WINDOW_PENDING, window.EligibilityFrozenHeight, window.WindowRandomnessHeight},
		{types.VerificationStatus_VERIFICATION_STATUS_VERIFY_COLLECTION_OPEN, window.WindowRandomnessHeight, window.HandraiseCloseHeight},
		{types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_SELECTION_PENDING, window.HandraiseCloseHeight, window.SelectionRandomnessHeight},
		{types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_SELECTION_PENDING, window.SelectionRandomnessHeight, window.AssignmentDeadlineHeight},
	} {
		core, err := f.keeper.TaskCore.Get(f.ctx, f.taskKey)
		require.NoError(t, err)
		require.Equal(t, uint32(1), core.EffectiveVerifyRound)
		core.VerificationStatus = test.status
		require.NoError(t, f.keeper.TaskCore.Set(f.ctx, f.taskKey, core))
		ctx := sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(test.height)))
		response, err := NewQueryServerImpl(f.keeper).TaskStage(ctx, &types.QueryTaskStageRequest{TaskId: f.taskID})
		require.NoError(t, err)
		require.Equal(t, types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_OPEN, response.Stage.GetNextDeadlineKind())
		require.Equal(t, test.deadline, response.Stage.GetNextDeadlineHeight())
	}
}

func TestPrunedChallengeWindowRetainsItsRoundSpecificClock(t *testing.T) {
	f, window := openChallengeQueryFixture(t)
	require.Equal(t, window.BuilderProposalCloseHeight, window.HandraiseCloseHeight)
	window.Status = types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_PRUNED
	require.NoError(t, f.keeper.VerifierCandidateWindow.Set(f.ctx, types.NewVerifyRoundKey(f.taskKey, 2), window))
	response, err := NewQueryServerImpl(f.keeper).VerifierCandidateWindow(f.ctx, &types.QueryVerifierCandidateWindowRequest{TaskId: f.taskID, VerifyRound: 2})
	require.NoError(t, err)
	require.Equal(t, window, response.Window)
	require.Empty(t, response.Members)
	window.VerifyRound = 1
	require.NoError(t, f.keeper.VerifierCandidateWindow.Set(f.ctx, types.NewVerifyRoundKey(f.taskKey, 1), window))
	_, err = NewQueryServerImpl(f.keeper).VerifierCandidateWindow(f.ctx, &types.QueryVerifierCandidateWindowRequest{TaskId: f.taskID, VerifyRound: 1})
	require.Equal(t, codes.Internal, status.Code(err), "round one must retain its separate proposal interval")
}

func TestStageAndAssignmentUseTheOpenChallengeRoundClock(t *testing.T) {
	f, window := openChallengeQueryFixture(t)
	round, err := f.keeper.ReadVerificationRound(f.ctx, types.NewVerifyRoundKey(f.taskKey, 2))
	require.NoError(t, err)
	assignment := f.assignment
	assignment.VerifyRound = 2
	assignment.OpenVerifyHeight = window.SelectionRandomnessHeight
	assignment.SelectionRandomnessHeight = window.SelectionRandomnessHeight
	assignment.CommitDeadlineHeight = window.AssignmentDeadlineHeight
	assignment.VerifyDeadlineHeight = round.RoundCloseDeadlineHeight
	assignment.RevealDeadlineHeight = round.RoundCloseDeadlineHeight
	assignment.VerifierCandidateWindowHash = bytes32(0xa1)
	assignment.VerifierLegalSetHash = bytes32(0xa2)
	assignment.SelectionRandomnessBeacon = bytes32(0xa3)
	for i := range assignment.SelectedVerifiers {
		assignment.SelectedVerifiers[i].OperatorAddress = sdk.AccAddress(bytes32(byte(0xb1 + i))[:20]).String()
	}
	assignment.SelectedVerifiersHash, err = SelectedVerifierRefsHash(sdkChainID(f.ctx), f.taskID, 2, assignment.VerifierLegalSetHash, assignment.SelectionRandomnessHeight, assignment.SelectionRandomnessBeacon, assignment.SelectedVerifiers)
	require.NoError(t, err)
	require.NoError(t, f.keeper.WriteVerifierAssignment(f.ctx, types.NewVerifyRoundKey(f.taskKey, 2), assignment))
	window.Status = types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY
	window.XVerifierWindowHash = &types.VerifierCandidateWindowState_VerifierWindowHash{VerifierWindowHash: assignment.VerifierCandidateWindowHash}
	require.NoError(t, f.keeper.VerifierCandidateWindow.Set(f.ctx, types.NewVerifyRoundKey(f.taskKey, 2), window))
	q := NewQueryServerImpl(f.keeper)
	got, err := q.VerifierAssignment(f.ctx, &types.QueryVerifierAssignmentRequest{TaskId: f.taskID, VerifyRound: 2})
	require.NoError(t, err, "round two reveal and terminal deadlines intentionally coincide")
	require.Equal(t, assignment, got.Assignment)
	for _, test := range []struct {
		status   types.VerificationStatus
		height   uint64
		kind     types.DeadlineKindV1
		deadline uint64
	}{
		{types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_ASSIGNED, assignment.OpenVerifyHeight, types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_COMMIT, assignment.CommitDeadlineHeight},
		{types.VerificationStatus_VERIFICATION_STATUS_COMMITTING, assignment.CommitDeadlineHeight, types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_FINAL, assignment.VerifyDeadlineHeight},
		{types.VerificationStatus_VERIFICATION_STATUS_REVEALING, assignment.RevealDeadlineHeight - 1, types.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_REVEAL, assignment.RevealDeadlineHeight},
		{types.VerificationStatus_VERIFICATION_STATUS_REVEALING, assignment.RevealDeadlineHeight, types.DeadlineKindV1_DEADLINE_KIND_V1_UNSPECIFIED, 0},
	} {
		core, err := f.keeper.TaskCore.Get(f.ctx, f.taskKey)
		require.NoError(t, err)
		require.Equal(t, uint32(1), core.EffectiveVerifyRound)
		core.VerificationStatus = test.status
		require.NoError(t, f.keeper.TaskCore.Set(f.ctx, f.taskKey, core))
		ctx := sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(test.height)))
		response, err := q.TaskStage(ctx, &types.QueryTaskStageRequest{TaskId: f.taskID})
		require.NoError(t, err)
		require.Equal(t, test.kind, response.Stage.GetNextDeadlineKind())
		require.Equal(t, test.deadline, response.Stage.GetNextDeadlineHeight())
	}
	assignment.RevealDeadlineHeight++
	require.NoError(t, f.keeper.WriteVerifierAssignment(f.ctx, types.NewVerifyRoundKey(f.taskKey, 2), assignment))
	_, err = q.VerifierAssignment(f.ctx, &types.QueryVerifierAssignmentRequest{TaskId: f.taskID, VerifyRound: 2})
	require.Equal(t, codes.Internal, status.Code(err))
}
