package keeper_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/keeper"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

// §16.2 QueryTask / QueryTaskStage / QueryTaskAssignment: TaskCoreState is the
// only primary of the six sub-statuses, and while the draw is still pending the
// three winner fields must be *absent* rather than defaulted.
func TestQueryTaskProjectsTaskCoreAndHidesPendingWinner(t *testing.T) {
	f := initFixture(t)
	genesis := taskGenesisV1(t, genesisChainID(f))
	genesis.TaskCores[0].TaskPhase = tasktypes.TaskPhase_TASK_PHASE_WORKER_ASSIGNMENT_PENDING
	genesis.TaskCores[0].AssignmentStatus = tasktypes.AssignmentStatus_ASSIGNMENT_STATUS_RANDOMNESS_PENDING
	genesis.TaskAssignments[0].WinnerWorker = ""
	genesis.TaskAssignments[0].WinnerDrawDigest = nil
	genesis.TaskAssignments[0].WinnerConfirmHeight = 0
	genesis.TaskAssignments[0].InferDeadlineHeight = 0
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))

	server := keeper.NewQueryServerImpl(f.keeper)
	taskID := genesis.TaskCores[0].TaskId

	task, err := server.Task(f.ctx, &tasktypes.QueryTaskRequest{TaskId: taskID})
	require.NoError(t, err)
	active := task.Task.GetActive()
	require.NotNil(t, active)
	require.Equal(t, tasktypes.TaskPhase_TASK_PHASE_WORKER_ASSIGNMENT_PENDING, active.Core.TaskPhase)
	require.Equal(t, genesis.TaskCores[0].AcceptedTaskHash, active.Core.AcceptedTaskHash)
	require.NotNil(t, active.Assignment)
	require.Equal(t, genesis.TaskAssignments[0].GenerationParamsDigest, active.Assignment.GenerationParamsDigest)
	require.Nil(t, active.Assignment.XWinnerWorker, "a pending draw must not project a winner")
	require.Nil(t, active.Assignment.XWinnerDrawDigest)
	require.Nil(t, active.Assignment.XWinnerConfirmHeight)

	stage, err := server.TaskStage(f.ctx, &tasktypes.QueryTaskStageRequest{TaskId: taskID})
	require.NoError(t, err)
	require.Equal(t, tasktypes.TaskPhase_TASK_PHASE_WORKER_ASSIGNMENT_PENDING, stage.Stage.TaskPhase)
	// §5.9 value 9 WORKER_ASSIGNMENT is the assignment randomness expiry.
	require.Equal(t,
		tasktypes.DeadlineKindV1_DEADLINE_KIND_V1_WORKER_ASSIGNMENT,
		stage.Stage.GetNextDeadlineKind())
	require.Equal(t, genesis.TaskAssignments[0].AssignmentRandomnessHeight, stage.Stage.GetNextDeadlineHeight())

	assignment, err := server.TaskAssignment(f.ctx, &tasktypes.QueryTaskAssignmentRequest{TaskId: taskID})
	require.NoError(t, err)
	require.Equal(t, genesis.TaskAssignments[0].GenerationParamsDigest, assignment.Assignment.GenerationParamsDigest)

	// §16.1: a malformed selector is InvalidArgument, never an empty result.
	_, err = server.Task(f.ctx, &tasktypes.QueryTaskRequest{TaskId: []byte{0x01}})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = server.Task(f.ctx, &tasktypes.QueryTaskRequest{TaskId: repeatByte(0xfe)})
	require.Equal(t, codes.NotFound, status.Code(err))
}

func TestQueryTaskAndVerifierAssignmentUseTheSingleV1Round(t *testing.T) {
	f := initFixture(t)
	genesis := taskGenesisV1(t, genesisChainID(f))
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))

	core := genesis.TaskCores[0]
	core.TaskPhase = tasktypes.TaskPhase_TASK_PHASE_VERIFIER_ASSIGNED
	core.VerificationStatus = tasktypes.VerificationStatus_VERIFICATION_STATUS_VERIFIER_ASSIGNED
	require.NoError(t, f.keeper.TaskCore.Set(f.ctx, taskKeyOf(core.TaskId), core))
	assignment := verifierAssignmentForQuery(t, genesisChainID(f), core.TaskId, []string{genesisVerifier, genesisVerifier2, genesisVerifier3})
	require.NoError(t, f.keeper.WriteVerifierAssignment(
		f.ctx, tasktypes.NewVerifyRoundKey(taskKeyOf(core.TaskId), tasktypes.VerifyRoundV1), assignment,
	))
	require.NoError(t, f.keeper.VerifierCandidateWindow.Set(
		f.ctx, tasktypes.NewVerifyRoundKey(taskKeyOf(core.TaskId), tasktypes.VerifyRoundV1),
		verifierWindowForAssignment(assignment),
	))

	server := keeper.NewQueryServerImpl(f.keeper)
	task, err := server.Task(f.ctx, &tasktypes.QueryTaskRequest{TaskId: core.TaskId})
	require.NoError(t, err)
	require.Equal(t, assignment, *task.Task.GetActive().Round1VerifierAssignment)
	got, err := server.VerifierAssignment(f.ctx, &tasktypes.QueryVerifierAssignmentRequest{TaskId: core.TaskId, VerifyRound: tasktypes.VerifyRoundV1})
	require.NoError(t, err)
	require.Equal(t, assignment, got.Assignment)
	stage, err := server.TaskStage(f.ctx, &tasktypes.QueryTaskStageRequest{TaskId: core.TaskId})
	require.NoError(t, err)
	require.Equal(t, tasktypes.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_COMMIT, stage.Stage.GetNextDeadlineKind())
	require.Equal(t, assignment.CommitDeadlineHeight, stage.Stage.GetNextDeadlineHeight())

	assignmentKey := tasktypes.NewVerifyRoundKey(taskKeyOf(core.TaskId), tasktypes.VerifyRoundV1)
	stored, err := f.keeper.VerifierAssignment.Get(f.ctx, assignmentKey)
	require.NoError(t, err)
	stored.VerifyRound = 2
	require.NoError(t, f.keeper.VerifierAssignment.Set(f.ctx, assignmentKey, stored))
	_, err = server.VerifierAssignment(f.ctx, &tasktypes.QueryVerifierAssignmentRequest{TaskId: core.TaskId, VerifyRound: tasktypes.VerifyRoundV1})
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestQueryTaskProjectsVerifyFailedBeforeAndAfterVerifierAssignment(t *testing.T) {
	f := initFixture(t)
	genesis := taskGenesisV1(t, genesisChainID(f))
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	taskID := genesis.TaskCores[0].TaskId
	taskKey := taskKeyOf(taskID)
	core := genesis.TaskCores[0]
	core.TaskPhase = tasktypes.TaskPhase_TASK_PHASE_FAILED
	core.VerificationStatus = tasktypes.VerificationStatus_VERIFICATION_STATUS_VERIFY_FAILED
	core.SettlementStatus = tasktypes.SettlementStatus_SETTLEMENT_STATUS_REFUNDED
	require.NoError(t, f.keeper.TaskCore.Set(f.ctx, taskKey, core))
	server := keeper.NewQueryServerImpl(f.keeper)

	preAssignment, err := server.Task(f.ctx, &tasktypes.QueryTaskRequest{TaskId: taskID})
	require.NoError(t, err)
	require.NotNil(t, preAssignment.Task.GetActive())
	require.Nil(t, preAssignment.Task.GetActive().Round1VerifierAssignment,
		"verify-open failure is valid before a VerifierAssignment exists")

	core.TaskPhase = tasktypes.TaskPhase_TASK_PHASE_VERIFIER_ASSIGNED
	core.VerificationStatus = tasktypes.VerificationStatus_VERIFICATION_STATUS_VERIFIER_ASSIGNED
	require.NoError(t, f.keeper.TaskCore.Set(f.ctx, taskKey, core))
	_, err = server.Task(f.ctx, &tasktypes.QueryTaskRequest{TaskId: taskID})
	require.Equal(t, codes.Internal, status.Code(err),
		"a status that is unambiguously post-assignment must still fail closed")

	assignment := verifierAssignmentForQuery(
		t, genesisChainID(f), taskID, []string{genesisVerifier, genesisVerifier2, genesisVerifier3},
	)
	require.NoError(t, f.keeper.WriteVerifierAssignment(
		f.ctx, tasktypes.NewVerifyRoundKey(taskKey, tasktypes.VerifyRoundV1), assignment,
	))
	require.NoError(t, f.keeper.VerifierCandidateWindow.Set(
		f.ctx, tasktypes.NewVerifyRoundKey(taskKey, tasktypes.VerifyRoundV1),
		verifierWindowForAssignment(assignment),
	))
	core.TaskPhase = tasktypes.TaskPhase_TASK_PHASE_FAILED
	core.VerificationStatus = tasktypes.VerificationStatus_VERIFICATION_STATUS_VERIFY_FAILED
	require.NoError(t, f.keeper.TaskCore.Set(f.ctx, taskKey, core))
	postAssignment, err := server.Task(f.ctx, &tasktypes.QueryTaskRequest{TaskId: taskID})
	require.NoError(t, err)
	require.Equal(t, assignment, *postAssignment.Task.GetActive().Round1VerifierAssignment)
}

func TestQueryTaskStageUsesTheCurrentVerifierMilestone(t *testing.T) {
	f := initFixture(t)
	genesis := taskGenesisV1(t, genesisChainID(f))
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	taskID := genesis.TaskCores[0].TaskId
	taskKey := taskKeyOf(taskID)
	window := tasktypes.VerifierCandidateWindowState{
		SchemaVersion: 1, TaskId: taskID, VerifyRound: tasktypes.VerifyRoundV1,
		EligibilityFrozenHeight: 10, WindowRandomnessHeight: 20,
		BuilderProposalCloseHeight: 21, HandraiseCloseHeight: 22,
		SelectionRandomnessHeight: 23, AssignmentDeadlineHeight: 24,
		VerifierWindowSourceHash: repeatByte(0x25), EligibilitySegmentCount: 1,
		EligibleCount: 3, WindowSize: 3,
		Status: tasktypes.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_SOURCE_FROZEN,
	}
	require.NoError(t, f.keeper.VerifierCandidateWindow.Set(
		f.ctx, tasktypes.NewVerifyRoundKey(taskKey, tasktypes.VerifyRoundV1), window,
	))
	core := genesis.TaskCores[0]
	core.TaskPhase = tasktypes.TaskPhase_TASK_PHASE_RECEIPT_COMMITTED
	server := keeper.NewQueryServerImpl(f.keeper)
	for _, testCase := range []struct {
		status tasktypes.VerificationStatus
		height uint64
	}{
		{tasktypes.VerificationStatus_VERIFICATION_STATUS_VERIFIER_WINDOW_PENDING, window.WindowRandomnessHeight},
		{tasktypes.VerificationStatus_VERIFICATION_STATUS_VERIFY_COLLECTION_OPEN, window.HandraiseCloseHeight},
		{tasktypes.VerificationStatus_VERIFICATION_STATUS_VERIFIER_SELECTION_PENDING, window.SelectionRandomnessHeight},
	} {
		core.VerificationStatus = testCase.status
		require.NoError(t, f.keeper.TaskCore.Set(f.ctx, taskKey, core))
		stage, err := server.TaskStage(f.ctx, &tasktypes.QueryTaskStageRequest{TaskId: taskID})
		require.NoError(t, err)
		require.Equal(t, tasktypes.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_OPEN, stage.Stage.GetNextDeadlineKind())
		require.Equal(t, testCase.height, stage.Stage.GetNextDeadlineHeight())
	}

	window.SelectionRandomnessHeight = window.HandraiseCloseHeight
	require.NoError(t, f.keeper.VerifierCandidateWindow.Set(
		f.ctx, tasktypes.NewVerifyRoundKey(taskKey, tasktypes.VerifyRoundV1), window,
	))
	_, err := server.TaskStage(f.ctx, &tasktypes.QueryTaskStageRequest{TaskId: taskID})
	require.Equal(t, codes.Internal, status.Code(err))
}

// §16.2 next_deadline_kind/height, and issue 156: EndBlock consumes a queue row
// as soon as current_height >= deadline_height, and handleExpiredCommitDeadline,
// handleExpiredRevealDeadline and rescheduleVerifyOpenDeadline each move that row
// without touching any status. The stage view must therefore never name a height
// the sweeper has already consumed - past a commit or reveal deadline the only row
// still queued for the task is VERIFY_FINAL, and past the last VERIFY_OPEN mark the
// retry height is recorded in no store row at all, so absent is the only honest
// answer. Naming the consumed height instead reads as a permanently stuck task.
func TestQueryTaskStageNeverNamesAConsumedDeadline(t *testing.T) {
	f := initFixture(t)
	genesis := taskGenesisV1(t, genesisChainID(f))
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))

	core := genesis.TaskCores[0]
	core.TaskPhase = tasktypes.TaskPhase_TASK_PHASE_RECEIPT_COMMITTED
	taskKey := taskKeyOf(core.TaskId)
	roundKey := tasktypes.NewVerifyRoundKey(taskKey, tasktypes.VerifyRoundV1)
	assignment := verifierAssignmentForQuery(
		t, genesisChainID(f), core.TaskId, []string{genesisVerifier, genesisVerifier2, genesisVerifier3},
	)
	require.NoError(t, f.keeper.WriteVerifierAssignment(f.ctx, roundKey, assignment))
	require.NoError(t, f.keeper.VerifierCandidateWindow.Set(f.ctx, roundKey, verifierWindowForAssignment(assignment)))

	server := keeper.NewQueryServerImpl(f.keeper)
	stageAt := func(t *testing.T, verificationStatus tasktypes.VerificationStatus, height uint64) tasktypes.TaskStageViewV1 {
		t.Helper()
		core.VerificationStatus = verificationStatus
		require.NoError(t, f.keeper.TaskCore.Set(f.ctx, taskKey, core))
		ctx := sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(height)))
		stage, err := server.TaskStage(ctx, &tasktypes.QueryTaskStageRequest{TaskId: core.TaskId})
		require.NoError(t, err)
		return stage.Stage
	}

	for _, testCase := range []struct {
		name   string
		status tasktypes.VerificationStatus
		height uint64
		kind   tasktypes.DeadlineKindV1
		want   uint64
	}{
		{
			"commit deadline still queued", tasktypes.VerificationStatus_VERIFICATION_STATUS_COMMITTING,
			assignment.CommitDeadlineHeight - 1,
			tasktypes.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_COMMIT, assignment.CommitDeadlineHeight,
		},
		{
			"commit deadline consumed by this block", tasktypes.VerificationStatus_VERIFICATION_STATUS_COMMITTING,
			assignment.CommitDeadlineHeight,
			tasktypes.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_FINAL, assignment.VerifyDeadlineHeight,
		},
		{
			"reveal deadline still queued", tasktypes.VerificationStatus_VERIFICATION_STATUS_REVEALING,
			assignment.RevealDeadlineHeight - 1,
			tasktypes.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_REVEAL, assignment.RevealDeadlineHeight,
		},
		{
			"reveal deadline consumed by this block", tasktypes.VerificationStatus_VERIFICATION_STATUS_REVEALING,
			assignment.RevealDeadlineHeight,
			tasktypes.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_FINAL, assignment.VerifyDeadlineHeight,
		},
		{
			"reveal deadline long overdue", tasktypes.VerificationStatus_VERIFICATION_STATUS_REVEALING,
			assignment.VerifyDeadlineHeight - 1,
			tasktypes.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_FINAL, assignment.VerifyDeadlineHeight,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			stage := stageAt(t, testCase.status, testCase.height)
			require.Equal(t, testCase.kind, stage.GetNextDeadlineKind())
			require.Equal(t, testCase.want, stage.GetNextDeadlineHeight())
		})
	}

	// Past assignment_deadline_height the VERIFY_OPEN row has been re-filed by
	// rescheduleVerifyOpenDeadline under current_height + verifyDeadlineRetryBlocksV1,
	// a height no store row records, so both fields must be absent.
	window := tasktypes.VerifierCandidateWindowState{
		SchemaVersion: 1, TaskId: core.TaskId, VerifyRound: tasktypes.VerifyRoundV1,
		EligibilityFrozenHeight: 10, WindowRandomnessHeight: 20,
		BuilderProposalCloseHeight: 21, HandraiseCloseHeight: 22,
		SelectionRandomnessHeight: 23, AssignmentDeadlineHeight: 24,
		VerifierWindowSourceHash: repeatByte(0x25), EligibilitySegmentCount: 1,
		EligibleCount: 3, WindowSize: 3,
		Status: tasktypes.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_SOURCE_FROZEN,
	}
	require.NoError(t, f.keeper.VerifierCandidateWindow.Set(f.ctx, roundKey, window))
	pending := stageAt(
		t, tasktypes.VerificationStatus_VERIFICATION_STATUS_VERIFIER_SELECTION_PENDING,
		window.AssignmentDeadlineHeight,
	)
	require.Nil(t, pending.XNextDeadlineKind, "a consumed VERIFY_OPEN row must not name a height")
	require.Nil(t, pending.XNextDeadlineHeight)

	// The same status one block earlier still has assignment_deadline_height queued,
	// so absence above is the sweep, not a blanket hole in the VERIFY_OPEN branch.
	queued := stageAt(
		t, tasktypes.VerificationStatus_VERIFICATION_STATUS_VERIFIER_SELECTION_PENDING,
		window.AssignmentDeadlineHeight-1,
	)
	require.Equal(t, tasktypes.DeadlineKindV1_DEADLINE_KIND_V1_VERIFY_OPEN, queued.GetNextDeadlineKind())
	require.Equal(t, window.AssignmentDeadlineHeight, queued.GetNextDeadlineHeight())
}

// QueryTaskStage projects the three Task-level deadlines that follow verifier
// collection from their authoritative state, including proto3 presence fields.
func TestQueryTaskStageProjectsPostVerificationDeadlines(t *testing.T) {
	f := initFixture(t)
	genesis := taskGenesisV1(t, genesisChainID(f))
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	taskID := genesis.TaskCores[0].TaskId
	taskKey := taskKeyOf(taskID)
	server := keeper.NewQueryServerImpl(f.keeper)
	core := genesis.TaskCores[0]

	const challengeOpenHeight = uint64(50)
	const challengeCloseHeight = uint64(80)
	round := tasktypes.VerificationRoundState{
		TaskId: taskID, VerifyRound: tasktypes.VerifyRoundV1,
		XClosedHeight: &tasktypes.VerificationRoundState_ClosedHeight{ClosedHeight: challengeOpenHeight},
		XVerdict:      &tasktypes.VerificationRoundState_Verdict{Verdict: tasktypes.TaskVerdict_TASK_VERDICT_PASS},
	}
	summary := tasktypes.TaskRoundSummaryState{
		TaskId: taskID, MaxClosedRound: tasktypes.VerifyRoundV1,
		XChallengeOpenHeight:  &tasktypes.TaskRoundSummaryState_ChallengeOpenHeight{ChallengeOpenHeight: challengeOpenHeight},
		XChallengeCloseHeight: &tasktypes.TaskRoundSummaryState_ChallengeCloseHeight{ChallengeCloseHeight: challengeCloseHeight},
	}
	require.NoError(t, f.keeper.WriteVerificationRound(
		f.ctx, tasktypes.NewVerifyRoundKey(taskKey, tasktypes.VerifyRoundV1), round,
	))
	require.NoError(t, f.keeper.TaskRoundSummary.Set(f.ctx, taskKey, summary))

	queryStage := func(t *testing.T) tasktypes.TaskStageViewV1 {
		t.Helper()
		require.NoError(t, f.keeper.TaskCore.Set(f.ctx, taskKey, core))
		response, err := server.TaskStage(f.ctx, &tasktypes.QueryTaskStageRequest{TaskId: taskID})
		require.NoError(t, err)
		return response.Stage
	}
	core.TaskPhase = tasktypes.TaskPhase_TASK_PHASE_REVEALING
	for _, testCase := range []struct {
		name    string
		verdict tasktypes.TaskVerdict
		status  tasktypes.VerificationStatus
	}{
		{"pass", tasktypes.TaskVerdict_TASK_VERDICT_PASS, tasktypes.VerificationStatus_VERIFICATION_STATUS_VERIFICATION_PASSED},
		{"fail", tasktypes.TaskVerdict_TASK_VERDICT_FAIL, tasktypes.VerificationStatus_VERIFICATION_STATUS_VERIFY_FAILED},
	} {
		t.Run("challenge window after "+testCase.name, func(t *testing.T) {
			round.XVerdict = &tasktypes.VerificationRoundState_Verdict{Verdict: testCase.verdict}
			require.NoError(t, f.keeper.WriteVerificationRound(
				f.ctx, tasktypes.NewVerifyRoundKey(taskKey, tasktypes.VerifyRoundV1), round,
			))
			core.VerificationStatus = testCase.status
			stage := queryStage(t)
			require.Equal(t, tasktypes.DeadlineKindV1_DEADLINE_KIND_V1_CHALLENGE_WINDOW_CLOSE, stage.GetNextDeadlineKind())
			require.Equal(t, challengeCloseHeight, stage.GetNextDeadlineHeight())
		})
	}

	summary.XRoundsClosedHeight = &tasktypes.TaskRoundSummaryState_RoundsClosedHeight{
		RoundsClosedHeight: challengeCloseHeight,
	}
	require.NoError(t, f.keeper.TaskRoundSummary.Set(f.ctx, taskKey, summary))
	core.TaskPhase = tasktypes.TaskPhase_TASK_PHASE_SETTLING
	settlement := queryStage(t)
	require.Equal(t, tasktypes.DeadlineKindV1_DEADLINE_KIND_V1_TASK_SETTLEMENT, settlement.GetNextDeadlineKind())
	require.Equal(t,
		challengeCloseHeight+genesis.Params.Deadlines.SettleMarginBlocks,
		settlement.GetNextDeadlineHeight(),
	)

	// Cleanup removes detail rows before TaskCore, so the terminal projection must
	// not depend on TaskAssignment still being present.
	require.NoError(t, f.keeper.TaskAssignment.Remove(f.ctx, taskKey))
	const finalityHeight = uint64(100)
	core.FinalityStatus = shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL
	core.XTaskFinalityHeight = &tasktypes.TaskCoreState_TaskFinalityHeight{TaskFinalityHeight: finalityHeight}
	for _, phase := range []tasktypes.TaskPhase{
		tasktypes.TaskPhase_TASK_PHASE_SETTLED,
		tasktypes.TaskPhase_TASK_PHASE_FAILED,
	} {
		core.TaskPhase = phase
		cleanup := queryStage(t)
		require.Equal(t, tasktypes.DeadlineKindV1_DEADLINE_KIND_V1_EVIDENCE_CLEANUP, cleanup.GetNextDeadlineKind())
		require.Equal(t, finalityHeight+core.EvidenceRetentionBlocksSnapshot, cleanup.GetNextDeadlineHeight())
	}

	core.XTaskFinalityHeight = nil
	require.NoError(t, f.keeper.TaskCore.Set(f.ctx, taskKey, core))
	_, err := server.TaskStage(f.ctx, &tasktypes.QueryTaskStageRequest{TaskId: taskID})
	require.Equal(t, codes.Internal, status.Code(err), "a terminal phase must not default a missing finality height")
}

// §16.2 QueryTaskBuilders: the three body states are frozen. ACTIVE must carry
// exactly the frozen member list, PRUNED must keep every header field with an
// empty member array (and is NOT NotFound), and an ACTIVE row with no resident
// members is an invariant break, never an empty page.
func TestQueryTaskBuildersTriStateSemantics(t *testing.T) {
	f := initFixture(t)
	genesis := taskGenesisV1(t, genesisChainID(f))
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))

	server := keeper.NewQueryServerImpl(f.keeper)
	selection := genesis.TaskBuilderSelections[0]
	taskKey := taskKeyOf(selection.TaskId)

	active, err := server.TaskBuilders(f.ctx, &tasktypes.QueryTaskBuildersRequest{TaskId: selection.TaskId})
	require.NoError(t, err)
	require.Equal(t, shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE, active.Selection.BodyStatus)
	require.Equal(t, selection.SelectedTaskBuilders, active.Selection.SelectedTaskBuilders)
	require.Equal(t, selection.SelectedTaskBuildersHash, active.Selection.SelectedTaskBuildersHash)

	// PRUNED: members empty, everything else retained.
	pruned := selection
	pruned.SelectedTaskBuilders = nil
	pruned.BodyStatus = shared.StoredBodyStatus_STORED_BODY_STATUS_PRUNED
	require.NoError(t, f.keeper.StoreTaskBuilderSelection(f.ctx, taskKey, pruned))
	response, err := server.TaskBuilders(f.ctx, &tasktypes.QueryTaskBuildersRequest{TaskId: selection.TaskId})
	require.NoError(t, err)
	require.Empty(t, response.Selection.SelectedTaskBuilders)
	require.Equal(t, selection.SelectedTaskBuilderCount, response.Selection.SelectedTaskBuilderCount)
	require.Equal(t, selection.SelectedTaskBuildersHash, response.Selection.SelectedTaskBuildersHash)
	require.Equal(t, selection.BuilderSetId, response.Selection.BuilderSetId)
	require.Equal(t, selection.BuilderSetHash, response.Selection.BuilderSetHash)
	require.Equal(t, selection.SessionAnchorBlockHash, response.Selection.SessionAnchorBlockHash)
	require.Equal(t, selection.CreatedHeight, response.Selection.CreatedHeight)

	// ACTIVE with an empty member array is Internal, not an empty page.
	broken := selection
	broken.SelectedTaskBuilders = nil
	broken.BodyStatus = shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE
	require.NoError(t, f.keeper.StoreTaskBuilderSelection(f.ctx, taskKey, broken))
	_, err = server.TaskBuilders(f.ctx, &tasktypes.QueryTaskBuildersRequest{TaskId: selection.TaskId})
	require.Equal(t, codes.Internal, status.Code(err))

	// A body whose hash cannot be recomputed from its members is Internal.
	drifted := selection
	drifted.SelectedTaskBuildersHash = repeatByte(0x0f)
	require.NoError(t, f.keeper.StoreTaskBuilderSelection(f.ctx, taskKey, drifted))
	_, err = server.TaskBuilders(f.ctx, &tasktypes.QueryTaskBuildersRequest{TaskId: selection.TaskId})
	require.Equal(t, codes.Internal, status.Code(err))

	// A missing row is NotFound.
	_, err = server.TaskBuilders(f.ctx, &tasktypes.QueryTaskBuildersRequest{TaskId: repeatByte(0xfd)})
	require.Equal(t, codes.NotFound, status.Code(err))
}

// §16.2 QuerySessionsByOwner only walks the ACTIVE/IDLE owner index; §7 line 2011
// forbids CLOSED rows there, and QueryOrderSequence is detail-retention only.
func TestQuerySessionSurface(t *testing.T) {
	f := initFixture(t)
	genesis := taskGenesisV1(t, genesisChainID(f))
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))

	server := keeper.NewQueryServerImpl(f.keeper)
	sessionID := genesis.Streams[0].SessionId

	sessions, err := server.SessionsByOwner(f.ctx, &tasktypes.QuerySessionsByOwnerRequest{UserAddress: genesisUser})
	require.NoError(t, err)
	require.Len(t, sessions.Sessions, 1)
	require.Empty(t, sessions.Page.NextPageToken)

	nonce, err := server.SessionNonce(f.ctx, &tasktypes.QuerySessionNonceRequest{UserAddress: genesisUser})
	require.NoError(t, err)
	require.Equal(t, uint64(1), nonce.NextSessionNonce)

	sequence, err := server.OrderSequence(f.ctx, &tasktypes.QueryOrderSequenceRequest{SessionId: sessionID, OrderSequence: 1})
	require.NoError(t, err)
	require.Equal(t, tasktypes.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_CONSUMED, sequence.Sequence.Status)

	_, err = server.OrderSequence(f.ctx, &tasktypes.QueryOrderSequenceRequest{SessionId: sessionID, OrderSequence: 99})
	require.Equal(t, codes.NotFound, status.Code(err))

	// A collapsed session has no terminal summary yet.
	_, err = server.SessionTerminalSummary(f.ctx, &tasktypes.QuerySessionTerminalSummaryRequest{SessionId: sessionID})
	require.Equal(t, codes.NotFound, status.Code(err))
}

// Verification queries resolve their authoritative stores. Missing rows are
// NotFound, including cleanup progress when no active Task exists.
func TestVerificationQueryHandlersDistinguishMissingRowsFromMissingContract(t *testing.T) {
	f := initFixture(t)
	server := keeper.NewQueryServerImpl(f.keeper)
	taskID := repeatByte(0x21)
	require.Equal(t, uint32(1), tasktypes.VerifyRoundV1)

	_, err := server.InferReceipt(f.ctx, &tasktypes.QueryInferReceiptRequest{TaskId: taskID})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = server.VerifierCandidateWindow(f.ctx, &tasktypes.QueryVerifierCandidateWindowRequest{TaskId: taskID, VerifyRound: tasktypes.VerifyRoundV1})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = server.VerifierCandidateWindow(f.ctx, &tasktypes.QueryVerifierCandidateWindowRequest{TaskId: taskID, VerifyRound: 0})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = server.VerifyCommit(f.ctx, &tasktypes.QueryVerifyCommitRequest{TaskId: taskID, VerifyRound: 0, VerifierOperatorAddress: genesisWorker})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = server.ResultReceipt(f.ctx, &tasktypes.QueryResultReceiptRequest{TaskId: taskID, VerifyRound: 0, VerifierOperatorAddress: genesisWorker})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = server.DataUnavailableReports(f.ctx, &tasktypes.QueryDataUnavailableReportsRequest{TaskId: taskID, VerifyRound: 0})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = server.BuilderDataUnavailable(f.ctx, &tasktypes.QueryBuilderDataUnavailableRequest{TaskId: taskID, VerifyRound: 0, BuilderOperatorAddress: genesisWorker})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	_, err = server.VerifierCandidateWindow(f.ctx, &tasktypes.QueryVerifierCandidateWindowRequest{TaskId: taskID, VerifyRound: 2})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = server.VerifierAssignment(f.ctx, &tasktypes.QueryVerifierAssignmentRequest{TaskId: taskID, VerifyRound: tasktypes.VerifyRoundV1})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = server.EvidenceCleanup(f.ctx, &tasktypes.QueryEvidenceCleanupRequest{TaskId: taskID})
	require.Equal(t, codes.NotFound, status.Code(err))

	// Selector validation runs before any authoritative state lookup.
	_, err = server.InferReceipt(f.ctx, &tasktypes.QueryInferReceiptRequest{TaskId: []byte{0x01}})
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestEvidenceCleanupProjectsAuthoritativeLifecycleStates(t *testing.T) {
	f := initFixture(t)
	genesis := taskGenesisV1(t, genesisChainID(f))
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	taskID := genesis.TaskCores[0].TaskId
	server := keeper.NewQueryServerImpl(f.keeper)

	response, err := server.EvidenceCleanup(f.ctx, &tasktypes.QueryEvidenceCleanupRequest{TaskId: taskID})
	require.NoError(t, err)
	require.Equal(t, tasktypes.TaskCleanupStatus_TASK_CLEANUP_STATUS_NOT_SCHEDULED, response.Cleanup.Status)
	require.Nil(t, response.Cleanup.XPhase)
	require.Zero(t, response.Cleanup.VisitedCount)
	require.Zero(t, response.Cleanup.DeletedCount)

	core := genesis.TaskCores[0]
	for _, phase := range []tasktypes.TaskPhase{
		tasktypes.TaskPhase_TASK_PHASE_SETTLING,
		tasktypes.TaskPhase_TASK_PHASE_SETTLED,
		tasktypes.TaskPhase_TASK_PHASE_FAILED,
	} {
		core.TaskPhase = phase
		require.NoError(t, f.keeper.TaskCore.Set(f.ctx, taskKeyOf(taskID), core))
		response, err = server.EvidenceCleanup(f.ctx, &tasktypes.QueryEvidenceCleanupRequest{TaskId: taskID})
		require.NoError(t, err)
		require.Equal(t, tasktypes.TaskCleanupStatus_TASK_CLEANUP_STATUS_NOT_SCHEDULED, response.Cleanup.Status)
	}

	taskKey := taskKeyOf(taskID)
	cursor := tasktypes.TaskCleanupCursorState{
		TaskId: taskID, Phase: tasktypes.TaskCleanupPhase_TASK_CLEANUP_PHASE_PROPOSALS,
		VisitedCount: 7, DeletedCount: 3,
	}
	require.NoError(t, f.keeper.TaskCleanupCursor.Set(f.ctx, taskKey, cursor))
	response, err = server.EvidenceCleanup(f.ctx, &tasktypes.QueryEvidenceCleanupRequest{TaskId: taskID})
	require.NoError(t, err)
	require.Equal(t, tasktypes.TaskCleanupStatus_TASK_CLEANUP_STATUS_RUNNING, response.Cleanup.Status)
	require.Equal(t, tasktypes.TaskCleanupPhase_TASK_CLEANUP_PHASE_PROPOSALS, response.Cleanup.GetPhase())
	require.Equal(t, uint64(7), response.Cleanup.VisitedCount)
	require.Equal(t, uint64(3), response.Cleanup.DeletedCount)

	require.NoError(t, f.keeper.TaskCleanupCursor.Remove(f.ctx, taskKey))
	require.NoError(t, f.keeper.TaskCore.Remove(f.ctx, taskKey))
	require.NoError(t, f.keeper.WriteTaskTerminalSummary(f.ctx, taskKey, tasktypes.TaskTerminalSummaryState{
		TaskId: taskID, SummaryHash: repeatByte(0x5a),
	}))
	response, err = server.EvidenceCleanup(f.ctx, &tasktypes.QueryEvidenceCleanupRequest{TaskId: taskID})
	require.NoError(t, err)
	require.Equal(t, tasktypes.TaskCleanupStatus_TASK_CLEANUP_STATUS_COMPACTED, response.Cleanup.Status)
}

func TestVerificationQueriesProjectAuthoritativeRows(t *testing.T) {
	f := initFixture(t)
	genesis := taskGenesisV1(t, genesisChainID(f))
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	taskID := genesis.TaskCores[0].TaskId
	taskKey := taskKeyOf(taskID)
	verifier := genesisWorker
	const verifyRound = tasktypes.VerifyRoundV1

	receipt := tasktypes.InferReceiptState{TaskId: taskID, WinnerWorker: genesisWorker, InferReceiptHash: repeatByte(0x31)}
	require.NoError(t, f.keeper.WriteInferReceipt(f.ctx, taskKey, receipt))
	window := tasktypes.VerifierCandidateWindowState{
		SchemaVersion: 1, TaskId: taskID, VerifyRound: verifyRound,
		VerifierWindowSourceHash: repeatByte(0x32), WindowSize: 1,
		XVerifierWindowHash: &tasktypes.VerifierCandidateWindowState_VerifierWindowHash{VerifierWindowHash: repeatByte(0x33)},
		Status:              tasktypes.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY,
	}
	require.NoError(t, f.keeper.VerifierCandidateWindow.Set(f.ctx, tasktypes.NewVerifyRoundKey(taskKey, verifyRound), window))
	member := tasktypes.VerifierCandidateWindowMemberState{
		SchemaVersion: 1, TaskId: taskID, VerifyRound: verifyRound,
		RankIndex: 0, Slot: 7, SlotVersion: 2, OperatorAddress: verifier, VerifierWindowRank: repeatByte(0x34),
	}
	require.NoError(t, f.keeper.WriteVerifierWindowMember(f.ctx, tasktypes.NewVerifierWindowMemberKey(taskKey, verifyRound, 0), member))

	commitKey, err := tasktypes.DeriveCommitKey(genesisChainID(f), taskID, verifyRound, verifier)
	require.NoError(t, err)
	commitStoreKey := tasktypes.NewCommitKey(commitKey[:])
	commit := tasktypes.CommitState{
		CommitKey: commitKey[:], TaskId: taskID, VerifyRound: verifyRound,
		VerifierOperatorAddress: verifier, Status: tasktypes.CommitStatusV1_COMMIT_STATUS_V1_ACCEPTED,
	}
	require.NoError(t, f.keeper.WriteCommit(f.ctx, commitStoreKey, commit))
	result := tasktypes.ResultReceiptState{
		CommitKey: commitKey[:], TaskId: taskID, VerifyRound: verifyRound,
		VerifierOperatorAddress: verifier, MetricRoot: repeatByte(0x35),
	}
	require.NoError(t, f.keeper.WriteResultReceipt(f.ctx, commitStoreKey, result))

	server := keeper.NewQueryServerImpl(f.keeper)
	gotReceipt, err := server.InferReceipt(f.ctx, &tasktypes.QueryInferReceiptRequest{TaskId: taskID})
	require.NoError(t, err)
	require.Equal(t, receipt.InferReceiptHash, gotReceipt.Receipt.InferReceiptHash)
	gotWindow, err := server.VerifierCandidateWindow(f.ctx, &tasktypes.QueryVerifierCandidateWindowRequest{TaskId: taskID, VerifyRound: verifyRound})
	require.NoError(t, err)
	require.Equal(t, []tasktypes.VerifierCandidateWindowMemberState{member}, gotWindow.Members)
	gotCommit, err := server.VerifyCommit(f.ctx, &tasktypes.QueryVerifyCommitRequest{TaskId: taskID, VerifyRound: verifyRound, VerifierOperatorAddress: verifier})
	require.NoError(t, err)
	require.Equal(t, commitKey[:], gotCommit.Commit.CommitKey)
	gotResult, err := server.ResultReceipt(f.ctx, &tasktypes.QueryResultReceiptRequest{TaskId: taskID, VerifyRound: verifyRound, VerifierOperatorAddress: verifier})
	require.NoError(t, err)
	require.Equal(t, result.MetricRoot, gotResult.Receipt.MetricRoot)

	window.Status = tasktypes.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_SOURCE_FROZEN
	window.XVerifierWindowHash = nil
	require.NoError(t, f.keeper.VerifierCandidateWindow.Set(f.ctx, tasktypes.NewVerifyRoundKey(taskKey, verifyRound), window))
	_, err = server.VerifierCandidateWindow(f.ctx, &tasktypes.QueryVerifierCandidateWindowRequest{TaskId: taskID, VerifyRound: verifyRound})
	require.Equal(t, codes.FailedPrecondition, status.Code(err))

}

func TestTaskFailureClassQueryRejectsDerivedFlagDrift(t *testing.T) {
	f := initFixture(t)
	genesis := taskGenesisV1(t, genesisChainID(f))
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	taskID := genesis.TaskCores[0].TaskId
	taskKey := taskKeyOf(taskID)
	server := keeper.NewQueryServerImpl(f.keeper)

	// v0.3 keys TaskFailureClass by (task_id, verify_round).
	failureKey := tasktypes.NewVerifyRoundKey(taskKey, tasktypes.VerifyRoundV1)
	failure := tasktypes.TaskFailureClassState{
		TaskId: taskID, VerifyRound: tasktypes.VerifyRoundV1,
		FailureClass:         tasktypes.TaskFailureClass_TASK_FAILURE_CLASS_OBJECTIVE_FAULT,
		FreezeSignalEligible: true,
	}
	require.NoError(t, f.keeper.TaskFailureClass.Set(f.ctx, failureKey, failure))
	response, err := server.TaskFailureClass(f.ctx, &tasktypes.QueryTaskFailureClassRequest{TaskId: taskID, VerifyRound: tasktypes.VerifyRoundV1})
	require.NoError(t, err)
	require.True(t, response.Failure.FreezeSignalEligible)

	failure.VerifyRound = 0
	require.NoError(t, f.keeper.TaskFailureClass.Set(f.ctx, failureKey, failure))
	_, err = server.TaskFailureClass(f.ctx, &tasktypes.QueryTaskFailureClassRequest{TaskId: taskID, VerifyRound: tasktypes.VerifyRoundV1})
	require.Equal(t, codes.Internal, status.Code(err))

	failure.VerifyRound = tasktypes.VerifyRoundV1
	failure.FreezeSignalEligible = false
	require.NoError(t, f.keeper.TaskFailureClass.Set(f.ctx, failureKey, failure))
	_, err = server.TaskFailureClass(f.ctx, &tasktypes.QueryTaskFailureClassRequest{TaskId: taskID, VerifyRound: tasktypes.VerifyRoundV1})
	require.Equal(t, codes.Internal, status.Code(err))

	failure.FailureClass = tasktypes.TaskFailureClass_TASK_FAILURE_CLASS_UNSPECIFIED
	require.NoError(t, f.keeper.TaskFailureClass.Set(f.ctx, failureKey, failure))
	_, err = server.TaskFailureClass(f.ctx, &tasktypes.QueryTaskFailureClassRequest{TaskId: taskID, VerifyRound: tasktypes.VerifyRoundV1})
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestVerifierCandidateWindowQueryAcceptsBothPrunedHeaderShapes(t *testing.T) {
	f := initFixture(t)
	genesis := taskGenesisV1(t, genesisChainID(f))
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	taskID := genesis.TaskCores[0].TaskId
	key := tasktypes.NewVerifyRoundKey(taskKeyOf(taskID), tasktypes.VerifyRoundV1)
	server := keeper.NewQueryServerImpl(f.keeper)

	window := tasktypes.VerifierCandidateWindowState{
		SchemaVersion: 1, TaskId: taskID, VerifyRound: tasktypes.VerifyRoundV1,
		InferReceiptHash: repeatByte(0x80), CandidatePoolSnapshotId: repeatByte(0x84),
		CandidatePoolHash: repeatByte(0x85), VerifierWindowSourceHash: repeatByte(0x81),
		EligibilityFrozenHeight: 10, WindowRandomnessHeight: 20,
		BuilderProposalCloseHeight: 21, HandraiseCloseHeight: 22,
		SelectionRandomnessHeight: 23, AssignmentDeadlineHeight: 24,
		EligibilitySegmentCount: 1, EligibleCount: 3, WindowSize: 3,
		Status: tasktypes.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_PRUNED,
	}
	require.NoError(t, f.keeper.VerifierCandidateWindow.Set(f.ctx, key, window))
	response, err := server.VerifierCandidateWindow(f.ctx, &tasktypes.QueryVerifierCandidateWindowRequest{TaskId: taskID, VerifyRound: tasktypes.VerifyRoundV1})
	require.NoError(t, err)
	require.Empty(t, response.Members)
	require.Empty(t, response.Window.GetVerifierWindowHash())

	window.XWindowRandomnessBeacon = &tasktypes.VerifierCandidateWindowState_WindowRandomnessBeacon{WindowRandomnessBeacon: repeatByte(0x82)}
	window.XVerifierWindowHash = &tasktypes.VerifierCandidateWindowState_VerifierWindowHash{VerifierWindowHash: repeatByte(0x83)}
	window.GeneratedHeight = window.WindowRandomnessHeight
	require.NoError(t, f.keeper.VerifierCandidateWindow.Set(f.ctx, key, window))
	response, err = server.VerifierCandidateWindow(f.ctx, &tasktypes.QueryVerifierCandidateWindowRequest{TaskId: taskID, VerifyRound: tasktypes.VerifyRoundV1})
	require.NoError(t, err)
	require.Empty(t, response.Members)
	require.Equal(t, repeatByte(0x83), response.Window.GetVerifierWindowHash())

	window.XWindowRandomnessBeacon = nil
	require.NoError(t, f.keeper.VerifierCandidateWindow.Set(f.ctx, key, window))
	_, err = server.VerifierCandidateWindow(f.ctx, &tasktypes.QueryVerifierCandidateWindowRequest{TaskId: taskID, VerifyRound: tasktypes.VerifyRoundV1})
	require.Equal(t, codes.Internal, status.Code(err))
}

func TestDataUnavailableReportsUsesCanonicalBoundPageTokens(t *testing.T) {
	f := initFixture(t)
	genesis := taskGenesisV1(t, genesisChainID(f))
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))
	taskID := genesis.TaskCores[0].TaskId
	taskKey := taskKeyOf(taskID)
	selectedOrder := []string{genesisVerifier3, genesisVerifier, genesisVerifier2}
	assignment := verifierAssignmentForQuery(t, genesisChainID(f), taskID, selectedOrder)
	require.NoError(t, f.keeper.WriteVerifierAssignment(
		f.ctx, tasktypes.NewVerifyRoundKey(taskKey, tasktypes.VerifyRoundV1), assignment,
	))
	require.NoError(t, f.keeper.VerifierCandidateWindow.Set(
		f.ctx, tasktypes.NewVerifyRoundKey(taskKey, tasktypes.VerifyRoundV1),
		verifierWindowForAssignment(assignment),
	))
	for i, operator := range selectedOrder {
		attempt := keeper.DataUnavailableReportAttempt{
			TaskID: taskID, VerifyRound: tasktypes.VerifyRoundV1,
			VerifierOperatorAddress: operator, UnavailableTaskBuilderBitmap: []byte{byte(i + 1)},
			ServiceAuthorizationNonceSnapshot: uint64(i + 1),
		}
		digest, err := keeper.DataUnavailableReportDigest(genesisChainID(f), attempt)
		require.NoError(t, err)
		report := tasktypes.DataUnavailableReportState{
			TaskId: taskID, VerifyRound: tasktypes.VerifyRoundV1, VerifierOperatorAddress: operator,
			UnavailableTaskBuilderBitmap:      attempt.UnavailableTaskBuilderBitmap,
			ServiceAuthorizationNonceSnapshot: attempt.ServiceAuthorizationNonceSnapshot,
			ReportHeight:                      uint64(46 + i), ReportDigest: digest,
		}
		require.NoError(t, f.keeper.WriteDataUnavailableReport(
			f.ctx, tasktypes.NewVerifyActorKey(taskKey, tasktypes.VerifyRoundV1, operator), report,
		))
	}

	server := keeper.NewQueryServerImpl(f.keeper)
	req := &tasktypes.QueryDataUnavailableReportsRequest{
		TaskId: taskID, VerifyRound: tasktypes.VerifyRoundV1,
		Page: shared.QueryPageRequestV1{Limit: 1},
	}
	first, err := server.DataUnavailableReports(f.ctx, req)
	require.NoError(t, err)
	require.Equal(t, selectedOrder[:1], []string{first.Reports[0].VerifierOperatorAddress})
	require.NotEmpty(t, first.Page.NextPageToken)

	var token shared.PageTokenV1
	require.NoError(t, proto.Unmarshal(first.Page.NextPageToken, &token))
	expectedRPC := shared.CanonicalHashBytes(shared.MustDomain(shared.DomainQueryRPCV1), []byte("/task.v1.Query/DataUnavailableReports"))
	expectedSelector := shared.CanonicalHashBytes(
		shared.MustDomain(shared.DomainQuerySelectorV1), []byte(genesisChainID(f)), expectedRPC,
		taskID, shared.Uint32BE(tasktypes.VerifyRoundV1),
	)
	// External anchors. The two comparisons below only prove the token agrees with
	// digests this test derived itself, and a reordered preimage applied to both
	// sides keeps that agreement. The frozen hex is what a token minted by an
	// earlier binary actually carries, so it is the part that has to be defended.
	require.Equal(t, "b18a0901f429262f4f746ec4059acbb0baa739c3ae182613e4fcf517cef64c63",
		hex.EncodeToString(expectedRPC),
		"TRUEOPEN_QUERY_RPC_V1 over /task.v1.Query/DataUnavailableReports is a frozen consensus preimage; moving this constant is a consensus change and must be re-checked against the §1.4 domain registry")
	require.Equal(t, "9d71a4585a13eee5d436b5c974c06924f74a3af28de66176d4c27ec434813461",
		hex.EncodeToString(expectedSelector),
		"TRUEOPEN_QUERY_SELECTOR_V1 is a frozen consensus preimage; moving this constant is a consensus change and must be re-checked against the §1.4 domain registry")
	require.Equal(t, expectedRPC, token.RpcMethodDigest)
	require.Equal(t, expectedSelector, token.SelectorDigest)
	require.Equal(t, uint64(sdk.UnwrapSDKContext(f.ctx).BlockHeight()), token.QueryHeight)
	require.NotEmpty(t, token.LastPrimaryKey)

	req.Page.PageToken = first.Page.NextPageToken
	second, err := server.DataUnavailableReports(f.ctx, req)
	require.NoError(t, err)
	require.Equal(t, selectedOrder[1], second.Reports[0].VerifierOperatorAddress)

	bad := token
	bad.SelectorDigest = repeatByte(0x91)
	req.Page.PageToken, err = proto.Marshal(&bad)
	require.NoError(t, err)
	_, err = server.DataUnavailableReports(f.ctx, req)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	bad = token
	bad.QueryHeight++
	req.Page.PageToken, err = proto.Marshal(&bad)
	require.NoError(t, err)
	_, err = server.DataUnavailableReports(f.ctx, req)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	bad = token
	bad.RpcMethodDigest = shared.CanonicalHashBytes(
		shared.MustDomain(shared.DomainQueryRPCV1), []byte("/task.v1.Query/SessionsByOwner"),
	)
	req.Page.PageToken, err = proto.Marshal(&bad)
	require.NoError(t, err)
	_, err = server.DataUnavailableReports(f.ctx, req)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	nonCanonical := append(bytes.Clone(first.Page.NextPageToken), 0x28, 0x01)
	req.Page.PageToken = nonCanonical
	_, err = server.DataUnavailableReports(f.ctx, req)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	firstKey := tasktypes.NewVerifyActorKey(taskKey, tasktypes.VerifyRoundV1, selectedOrder[0])
	drifted, err := f.keeper.ReadDataUnavailableReport(f.ctx, firstKey)
	require.NoError(t, err)
	drifted.ReportDigest = repeatByte(0x92)
	require.NoError(t, f.keeper.WriteDataUnavailableReport(f.ctx, firstKey, drifted))
	req.Page.PageToken = nil
	_, err = server.DataUnavailableReports(f.ctx, req)
	require.Equal(t, codes.Internal, status.Code(err))
}

func verifierAssignmentForQuery(t *testing.T, chainID string, taskID []byte, operators []string) tasktypes.VerifierAssignmentState {
	t.Helper()
	selected := make([]tasktypes.SelectedVerifierV1, len(operators))
	for i, operator := range operators {
		selected[i] = tasktypes.SelectedVerifierV1{OperatorAddress: operator, Slot: uint32(i), SlotVersion: 1}
	}
	legalSetHash := repeatByte(0xa1)
	selectionBeacon := repeatByte(0xa2)
	selectedHash, err := keeper.SelectedVerifierRefsHash(
		chainID, taskID, tasktypes.VerifyRoundV1, legalSetHash, 40, selectionBeacon, selected,
	)
	require.NoError(t, err)
	return tasktypes.VerifierAssignmentState{
		TaskId: taskID, VerifyRound: tasktypes.VerifyRoundV1,
		SelectedVerifiers: selected, SelectedVerifierCount: uint32(len(selected)),
		VerifierCandidateWindowHash: repeatByte(0xa0), VerifierLegalSetHash: legalSetHash,
		SelectedVerifiersHash: selectedHash, SelectionRandomnessHeight: 40,
		SelectionRandomnessBeacon: selectionBeacon, OpenVerifyHeight: 45,
		CommitDeadlineHeight: 50, RevealDeadlineHeight: 60, VerifyDeadlineHeight: 70,
	}
}

func verifierWindowForAssignment(assignment tasktypes.VerifierAssignmentState) tasktypes.VerifierCandidateWindowState {
	return tasktypes.VerifierCandidateWindowState{
		SchemaVersion: 1, TaskId: assignment.TaskId, VerifyRound: assignment.VerifyRound,
		VerifierWindowSourceHash: repeatByte(0xb0), EligibilitySegmentCount: 1,
		EligibleCount: uint32(len(assignment.SelectedVerifiers)), WindowSize: uint32(len(assignment.SelectedVerifiers)),
		WindowRandomnessHeight:    assignment.SelectionRandomnessHeight - 10,
		SelectionRandomnessHeight: assignment.SelectionRandomnessHeight,
		AssignmentDeadlineHeight:  assignment.OpenVerifyHeight + 1,
		XWindowRandomnessBeacon: &tasktypes.VerifierCandidateWindowState_WindowRandomnessBeacon{
			WindowRandomnessBeacon: repeatByte(0xb1),
		},
		XVerifierWindowHash: &tasktypes.VerifierCandidateWindowState_VerifierWindowHash{
			VerifierWindowHash: assignment.VerifierCandidateWindowHash,
		},
		Status:          tasktypes.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY,
		GeneratedHeight: assignment.SelectionRandomnessHeight - 10,
	}
}

// verifierAssignmentForQueryRound reuses the round 1 fixture values and re-derives
// only what the round takes part in, so a round 2 row differs from a round 1 row
// exactly where the protocol says it should.
func verifierAssignmentForQueryRound(
	t *testing.T, chainID string, taskID []byte, operators []string, round uint32,
) tasktypes.VerifierAssignmentState {
	t.Helper()
	assignment := verifierAssignmentForQuery(t, chainID, taskID, operators)
	assignment.VerifyRound = round
	selectedHash, err := keeper.SelectedVerifierRefsHash(
		chainID, taskID, round, assignment.VerifierLegalSetHash,
		assignment.SelectionRandomnessHeight, assignment.SelectionRandomnessBeacon,
		assignment.SelectedVerifiers,
	)
	require.NoError(t, err)
	assignment.SelectedVerifiersHash = selectedHash
	return assignment
}

// TestQueryTaskServesRoundSummaryAndRound2Assignment covers the two
// TaskActiveBundleV1 members that QueryTask used to leave empty whatever the
// stored state said, which left a caller unable to read the challenge window
// close height or the round 2 verifier set from the composite query at all.
//
// Both are optional in the bundle, so the test pins presence and absence: the
// same request must report them missing while no row exists and report the
// stored row once one does.
func TestQueryTaskServesRoundSummaryAndRound2Assignment(t *testing.T) {
	f := initFixture(t)
	genesis := taskGenesisV1(t, genesisChainID(f))
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))

	core := genesis.TaskCores[0]
	core.TaskPhase = tasktypes.TaskPhase_TASK_PHASE_VERIFIER_ASSIGNED
	core.VerificationStatus = tasktypes.VerificationStatus_VERIFICATION_STATUS_VERIFIER_ASSIGNED
	require.NoError(t, f.keeper.TaskCore.Set(f.ctx, taskKeyOf(core.TaskId), core))
	taskKey := taskKeyOf(core.TaskId)
	verifiers := []string{genesisVerifier, genesisVerifier2, genesisVerifier3}
	round1 := verifierAssignmentForQuery(t, genesisChainID(f), core.TaskId, verifiers)
	require.NoError(t, f.keeper.WriteVerifierAssignment(
		f.ctx, tasktypes.NewVerifyRoundKey(taskKey, tasktypes.VerifyRoundV1), round1,
	))
	require.NoError(t, f.keeper.VerifierCandidateWindow.Set(
		f.ctx, tasktypes.NewVerifyRoundKey(taskKey, tasktypes.VerifyRoundV1),
		verifierWindowForAssignment(round1),
	))

	server := keeper.NewQueryServerImpl(f.keeper)
	task, err := server.Task(f.ctx, &tasktypes.QueryTaskRequest{TaskId: core.TaskId})
	require.NoError(t, err)
	require.Nil(t, task.Task.GetActive().RoundSummary,
		"a task with no round summary row must report the member absent rather than a zero value")
	require.Nil(t, task.Task.GetActive().Round2VerifierAssignment,
		"a task that was never challenged has no round 2 row and must report the member absent")

	summary := tasktypes.TaskRoundSummaryState{
		TaskId: core.TaskId, MaxClosedRound: tasktypes.VerifyRoundV1, OpenRoundCount: 1,
		EffectiveVerifyRound: tasktypes.ChallengeVerifyRoundV1,
		XChallengeOpenHeight: &tasktypes.TaskRoundSummaryState_ChallengeOpenHeight{
			ChallengeOpenHeight: 80,
		},
		XChallengeCloseHeight: &tasktypes.TaskRoundSummaryState_ChallengeCloseHeight{
			ChallengeCloseHeight: 120,
		},
	}
	require.NoError(t, f.keeper.TaskRoundSummary.Set(f.ctx, taskKey, summary))
	round2 := verifierAssignmentForQueryRound(
		t, genesisChainID(f), core.TaskId, verifiers, tasktypes.ChallengeVerifyRoundV1,
	)
	require.NoError(t, f.keeper.WriteVerifierAssignment(
		f.ctx, tasktypes.NewVerifyRoundKey(taskKey, tasktypes.ChallengeVerifyRoundV1), round2,
	))
	require.NoError(t, f.keeper.VerifierCandidateWindow.Set(
		f.ctx, tasktypes.NewVerifyRoundKey(taskKey, tasktypes.ChallengeVerifyRoundV1),
		verifierWindowForAssignment(round2),
	))

	task, err = server.Task(f.ctx, &tasktypes.QueryTaskRequest{TaskId: core.TaskId})
	require.NoError(t, err)
	active := task.Task.GetActive()
	require.NotNil(t, active.RoundSummary)
	require.Equal(t, summary, *active.RoundSummary)
	require.Equal(t, uint64(120), active.RoundSummary.GetChallengeCloseHeight(),
		"the challenge close height is the value a client reads to know the window is open")
	require.NotNil(t, active.Round2VerifierAssignment)
	require.Equal(t, round2, *active.Round2VerifierAssignment)
	require.Equal(t, tasktypes.ChallengeVerifyRoundV1, active.Round2VerifierAssignment.VerifyRound)
	require.Equal(t, round1, *active.Round1VerifierAssignment,
		"filling round 2 must not disturb the round 1 member")
}
