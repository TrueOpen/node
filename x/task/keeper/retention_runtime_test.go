package keeper

import (
	"testing"

	"cosmossdk.io/collections"

	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func TestRoleFaultConsumerGateRequiresFinalityAndExactEvidence(t *testing.T) {
	f := initInternalFixture(t)
	taskID := bytes32(0x31)
	taskKey := types.NewTaskKey(taskID)
	evidence := bytes32(0x32)
	settlementID := bytes32(0x33)

	require.NoError(t, f.keeper.TaskCore.Set(f.ctx, taskKey, types.TaskCoreState{
		TaskId: taskID, FinalityStatus: shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL,
		XTaskFinalityHeight: &types.TaskCoreState_TaskFinalityHeight{TaskFinalityHeight: 20},
	}))
	require.NoError(t, f.keeper.TaskSettlement.Set(f.ctx, taskKey, types.TaskSettlementState{
		TaskId: taskID, SettlementId: settlementID, TaskFinalityHeight: 20,
	}))
	require.NoError(t, f.keeper.TaskRoundSummary.Set(f.ctx, taskKey, types.TaskRoundSummaryState{
		TaskId:              taskID,
		XRoundsClosedHeight: &types.TaskRoundSummaryState_RoundsClosedHeight{RoundsClosedHeight: 20},
		XSettlementFactsCutoffHeight: &types.TaskRoundSummaryState_SettlementFactsCutoffHeight{
			SettlementFactsCutoffHeight: 20,
		},
	}))
	require.NoError(t, f.keeper.TaskFailureClass.Set(
		f.ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1),
		types.TaskFailureClassState{
			TaskId: taskID, VerifyRound: types.VerifyRoundV1,
			EvidenceDigest: evidence,
			XTaskFinalityHeight: &types.TaskFailureClassState_TaskFinalityHeight{
				TaskFinalityHeight: 20,
			},
		},
	))

	closed, err := f.keeper.RoleFaultConsumersClosed(f.ctx, taskID, evidence)
	require.NoError(t, err)
	require.True(t, closed)
	closed, err = f.keeper.RoleFaultConsumersClosed(f.ctx, taskID, bytes32(0x34))
	require.NoError(t, err)
	require.False(t, closed)
}

func TestFreezeFailureScanPagesCanonicalProfileIndex(t *testing.T) {
	f := initInternalFixture(t)
	modelID := "model-freeze"
	const profileVersion = uint32(3)
	rows := []struct {
		taskID   []byte
		height   uint64
		class    types.TaskFailureClass
		eligible bool
	}{
		{bytes32(0x41), 10, types.TaskFailureClass_TASK_FAILURE_CLASS_INSUFFICIENT_VERIFIER, false},
		{bytes32(0x42), 11, types.TaskFailureClass_TASK_FAILURE_CLASS_OBJECTIVE_FAULT, true},
	}
	for _, row := range rows {
		state := types.TaskFailureClassState{
			TaskId: row.taskID, VerifyRound: types.VerifyRoundV1,
			ModelId: modelID, ProfileVersion: profileVersion,
			FailureClass: row.class, FreezeSignalEligible: row.eligible,
			InferReceiptHashOrZero32:    bytes32(0x50),
			SettlementFactsHashOrZero32: bytes32(0x51),
			SettlementIdOrZero32:        bytes32(0x52),
			EvidenceDigest:              bytes32(byte(row.height)),
			XTaskFinalityHeight: &types.TaskFailureClassState_TaskFinalityHeight{
				TaskFinalityHeight: row.height,
			},
		}
		key := types.NewVerifyRoundKey(types.NewTaskKey(row.taskID), types.VerifyRoundV1)
		require.NoError(t, f.keeper.TaskFailureClass.Set(f.ctx, key, state))
		require.NoError(t, f.keeper.TaskFailureClassByProfileWindowIndex.Set(
			f.ctx, types.NewTaskFailureClassByProfileWindowKey(
				modelID, profileVersion, row.height, row.class, row.taskID,
			),
		))
	}

	first, err := f.keeper.ScanFreezeSignalFailures(f.ctx, hubFreezeRequest(
		modelID, profileVersion, 10, 11, nil, 1,
	))
	require.NoError(t, err)
	require.Equal(t, uint32(1), first.Visited)
	require.False(t, first.Done)
	require.False(t, first.Failures[0].IncludedInRoot)
	require.NotEmpty(t, first.LastIndexKey)

	second, err := f.keeper.ScanFreezeSignalFailures(f.ctx, hubFreezeRequest(
		modelID, profileVersion, 10, 11, first.LastIndexKey, 1,
	))
	require.NoError(t, err)
	require.Equal(t, uint32(1), second.Visited)
	require.True(t, second.Done)
	require.True(t, second.Failures[0].IncludedInRoot)
	require.Equal(t, rows[1].taskID, second.Failures[0].TaskID)
}

func hubFreezeRequest(modelID string, profileVersion uint32, start, end uint64, last []byte, limit uint32) hubtypes.FreezeSignalFailureScanRequest {
	return hubtypes.FreezeSignalFailureScanRequest{
		ModelID: modelID, ProfileVersion: profileVersion,
		RiskWindowStartHeight: start, RiskWindowEndHeight: end,
		LastIndexKey: last, Limit: limit,
	}
}

func TestTaskCleanupProposalPhaseDeletesOneRowPerVisit(t *testing.T) {
	f := initInternalFixture(t)
	taskID := bytes32(0x61)
	taskKey := types.NewTaskKey(taskID)
	stage := types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_TASK
	for _, marker := range []byte{0x62, 0x63} {
		digest := bytes32(marker)
		require.NoError(t, f.keeper.BuilderStageProposal.Set(
			f.ctx, types.NewBuilderStageProposalKey(taskKey, stage, digest),
			types.BuilderStageProposalState{
				TaskId: taskID, Stage: stage, ProposalDigest: digest,
			},
		))
	}
	cursor := types.TaskCleanupCursorState{
		TaskId: taskID, Phase: types.TaskCleanupPhase_TASK_CLEANUP_PHASE_PROPOSALS,
	}
	step, err := f.keeper.runTaskCleanupStep(f.ctx, taskKey, &cursor, 100, 100)
	require.NoError(t, err)
	require.Equal(t, uint64(1), step.visited)
	require.Equal(t, uint64(1), step.deleted)
	require.Equal(t, types.TaskCleanupPhase_TASK_CLEANUP_PHASE_PROPOSALS, cursor.Phase)

	iter, err := f.keeper.BuilderStageProposal.Iterate(
		f.ctx, collections.NewPrefixedTripleRange[types.Hash32Key, int32, types.Hash32Key](taskKey),
	)
	require.NoError(t, err)
	defer iter.Close()
	count := 0
	for ; iter.Valid(); iter.Next() {
		count++
	}
	require.Equal(t, 1, count)
}

func TestTaskTerminalSummaryHashCommitsEveryField(t *testing.T) {
	f := initInternalFixture(t)
	hash := bytes32(0x71)
	state := types.TaskTerminalSummaryState{
		TaskId: hash, SessionId: hash, TaskHash: hash,
		TerminalPhase:    types.TaskPhase_TASK_PHASE_FAILED,
		Verdict:          types.TaskVerdict_TASK_VERDICT_VERIFY_UNAVAILABLE,
		FailureClass:     types.TaskFailureClass_TASK_FAILURE_CLASS_INSUFFICIENT_VERIFIER,
		SettlementStatus: types.SettlementStatus_SETTLEMENT_STATUS_FINALIZED,
		FinalityStatus:   shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL,
		ModelId:          "model-a", ProfileVersion: 1,
		TaskType:                shared.TaskType_TASK_TYPE_TEXT_GENERATION,
		CandidatePoolSnapshotId: hash, CandidatePoolHash: hash,
		AssignmentCandidateSetHash: hash, CandidatePoolRefReleased: true,
		Round1SelectedVerifiersHashOrZero32: hash,
		Round2SelectedVerifiersHashOrZero32: hash,
		BuilderSetVersion:                   1, BuilderSetId: "set-a", BuilderSetHash: hash,
		SelectedTaskBuildersHash: hash, ProfileExecutionSnapshotHash: hash,
		GenerationParamsDigest: hash, InferReceiptRefOrZero32: hash,
		SettlementId: hash, SettlementFactsHash: hash, SettlementPlanHash: hash,
		SettlementBillHashOrZero32: hash, TaskRoundSummaryHash: hash,
		Round1FactsHashOrZero32: hash, Round2FactsHashOrZero32: hash,
		RoundEffectRoot: hash, Round2FundingResolutionHashOrZero32: hash,
		FaultSummaryHash: hash, CreatedHeight: 1, CompactedHeight: 2,
	}
	base, err := f.keeper.taskTerminalSummaryHash(f.ctx, state)
	require.NoError(t, err)
	state.CompactedHeight++
	changed, err := f.keeper.taskTerminalSummaryHash(f.ctx, state)
	require.NoError(t, err)
	require.NotEqual(t, base, changed)
}
