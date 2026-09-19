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

// A handraise window that closes short of selected_verifier_count is something a
// live network does on its own — a two-cortex devnet where one node takes the
// Worker duty leaves exactly one Verifier. The task specification 04 §5 calls that
// a dispatch
// failure. Treating it as ErrInvariantBroken aborted FinalizeBlock and stopped
// consensus, so these tests pin the boundary: short = task failure, over the
// frozen cap = still an invariant.

func shortenVerifierUnion(t *testing.T, f *internalFixture, taskKey types.TaskKey) uint32 {
	t.Helper()
	stageKey := types.NewTaskStageKey(taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY)
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	require.Greater(t, params.Weights.SelectedVerifierCount, uint32(1),
		"the fixture cannot express a short window if one handraise already suffices")
	union, err := f.keeper.TaskStageHandraiseUnion.Get(f.ctx, stageKey)
	require.NoError(t, err)
	union.UnionCount = params.Weights.SelectedVerifierCount - 1
	require.NoError(t, f.keeper.TaskStageHandraiseUnion.Set(f.ctx, stageKey, union))
	return union.UnionCount
}

// stageVerifyOpenFailureReadyTask completes verifierFinalizeFixture into the
// exact row set writePreVerificationFailure needs for a VERIFY_OPEN sweep: the
// accepted infer receipt, the reserved budget it refunds, the assignment it
// reads the evidence-schema snapshot from, and the session/order rows the
// terminal transition closes. The devnet task this reproduces
// (9d213abf..., receipt at 65411) carried every one of them.
func stageVerifyOpenFailureReadyTask(t *testing.T, f *internalFixture, taskKey types.TaskKey) {
	t.Helper()
	core, err := f.keeper.TaskCore.Get(f.ctx, taskKey)
	require.NoError(t, err)
	core.UserAddress = sdk.AccAddress(bytes.Repeat([]byte{0xc1}, 20)).String()
	core.OrderSequence = 1
	core.AcceptedTaskHash = bytes.Repeat([]byte{0xc2}, types.Hash32Len)
	core.ModelId = "model-a"
	core.ProfileVersion = 1
	core.OrderValue = shared.NewAmount(800)
	core.EvidenceRetentionBlocksSnapshot = types.DefaultMaxEvidenceRetentionBlocks
	require.NoError(t, f.keeper.TaskCore.Set(f.ctx, taskKey, core))

	worker := sdk.AccAddress(bytes.Repeat([]byte{0xc3}, 20)).String()
	require.NoError(t, f.keeper.TaskAssignment.Set(f.ctx, taskKey, types.TaskAssignmentState{
		TaskId: core.TaskId, WinnerWorker: worker,
		EvidenceSchemaHash:           bytes.Repeat([]byte{0xc4}, types.Hash32Len),
		ProfileExecutionSnapshotHash: bytes.Repeat([]byte{0xc5}, types.Hash32Len),
		GenerationParamsDigest:       bytes.Repeat([]byte{0xc6}, types.Hash32Len),
	}))
	require.NoError(t, f.keeper.InferReceipt.Set(f.ctx, taskKey, types.InferReceiptState{
		TaskId: core.TaskId, WinnerWorker: worker, GeneratedTokenCount: 640,
		InferReceiptHash: bytes.Repeat([]byte{0xc7}, types.Hash32Len),
	}))
	require.NoError(t, f.keeper.TaskBudget.Set(f.ctx, taskKey, types.TaskBudgetState{
		TaskId: core.TaskId, FeeRuleVersion: 1,
		OriginalReservedAmount: shared.NewAmount(1000), ReservedAmount: shared.NewAmount(1000),
		TxFeeReserveRemaining: shared.NewAmount(100), GasReimbursedTotal: shared.NewAmount(0),
		BudgetStatus: types.TaskBudgetStatus_TASK_BUDGET_STATUS_RESERVED,
		PriceBid:     shared.NewAmount(1_000_000),
		WorkerMax:    shared.NewAmount(640), VerifyMax: shared.NewAmount(160),
		VerifyRatioBpsSnapshot: 2500, MaxOutputTokens: 640,
		MaintenanceRateBpsSnapshot: 500, FeePolicyVersionSnapshot: 1,
	}))

	// task_admission.go:276 files exactly one timeout bucket ref per admitted
	// task, and the terminal release refuses to run without it.
	require.NoError(t, f.keeper.TaskBucketRef.Set(f.ctx,
		types.NewTaskBucketRefKey(taskKey, int32(shared.BucketKind_BUCKET_KIND_TIMEOUT), hubtypes.DefaultParameterBucketKey),
		types.TaskBucketRefState{
			TaskId: core.TaskId, BucketKind: shared.BucketKind_BUCKET_KIND_TIMEOUT,
			BucketKey: hubtypes.DefaultParameterBucketKey, Version: 1, AcquiredHeight: 10,
		}))

	sessionKey, err := sessionStoreKey(core.SessionId)
	require.NoError(t, err)
	require.NoError(t, f.keeper.Stream.Set(f.ctx, sessionKey, types.StreamState{
		SessionId: core.SessionId, OwnerUserAddress: core.UserAddress,
		NextExpectedSequence: 2, LastActiveHeight: 10, OpenPendingCount: 1,
		Status: types.SessionStatus_SESSION_STATUS_ACTIVE,
	}))
	require.NoError(t, f.keeper.OrderSequence.Set(f.ctx,
		types.NewOrderSequenceStateKey(sessionKey, core.OrderSequence),
		types.OrderSequenceState{
			SessionId: core.SessionId, OrderSequence: core.OrderSequence,
			Status: types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_CONSUMED,
			TaskId: core.TaskId, ConsumedHeight: 10,
		}))
}

// The fallback deadline is the only key left once the handraise window closes
// short, so it has to actually retire the task. On devnet it did not: the task
// reached the deadline in VERIFY_COLLECTION_OPEN — the status
// materializeVerifierWindow writes delta_w blocks after the receipt and that
// nothing ever writes back — while the sweep only accepted
// VERIFIER_WINDOW_PENDING. The row was dropped as stale, the task kept its
// RECEIPT_COMMITTED phase forever and the order value stayed locked.
func TestExpiredVerifyOpenDeadlineFailsACollectionOpenTask(t *testing.T) {
	f, taskKey, window, _, _ := verifierFinalizeFixture(t)
	stageVerifyOpenFailureReadyTask(t, f, taskKey)
	deadlineKey := types.NewDeadlineIndexKey(window.AssignmentDeadlineHeight, taskKey)
	require.NoError(t, f.keeper.VerifyOpenDeadlineIndex.Set(f.ctx, deadlineKey))
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).
		WithBlockHeight(int64(window.AssignmentDeadlineHeight)))

	visited, err := f.keeper.processExpiredVerifyOpenDeadlines(
		f.ctx, window.AssignmentDeadlineHeight, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)

	has, err := f.keeper.VerifyOpenDeadlineIndex.Has(f.ctx, deadlineKey)
	require.NoError(t, err)
	require.False(t, has, "the swept deadline key must not stay pending")

	core, err := f.keeper.TaskCore.Get(f.ctx, taskKey)
	require.NoError(t, err)
	require.Equal(t, types.TaskPhase_TASK_PHASE_FAILED, core.TaskPhase,
		"dropping the last deadline key without a transition strands the task forever")
	require.Equal(t, types.VerificationStatus_VERIFICATION_STATUS_VERIFY_FAILED, core.VerificationStatus)
	require.Equal(t, types.SettlementStatus_SETTLEMENT_STATUS_REFUNDED, core.SettlementStatus)

	failure, err := f.keeper.TaskFailureClass.Get(f.ctx,
		types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1))
	require.NoError(t, err)
	require.Equal(t, types.TaskFailureClass_TASK_FAILURE_CLASS_INSUFFICIENT_VERIFIER,
		failure.FailureClass)

	budget, err := f.keeper.TaskBudget.Get(f.ctx, taskKey)
	require.NoError(t, err)
	require.Equal(t, types.TaskBudgetStatus_TASK_BUDGET_STATUS_FINALIZED, budget.BudgetStatus)
	require.Equal(t, shared.NewAmount(0), budget.ReservedAmount, "the order value must be released")
}

// The other half of the boundary. Once a verifier assignment exists the task
// belongs to normal settlement, so a residual verify-open row really is stale
// and must still be dropped without touching the task.
func TestExpiredVerifyOpenDeadlineLeavesAnAssignedTaskToSettlement(t *testing.T) {
	f, taskKey, window, _, _ := verifierFinalizeFixture(t)
	stageVerifyOpenFailureReadyTask(t, f, taskKey)
	core, err := f.keeper.TaskCore.Get(f.ctx, taskKey)
	require.NoError(t, err)
	core.TaskPhase = types.TaskPhase_TASK_PHASE_VERIFIER_ASSIGNED
	core.VerificationStatus = types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_ASSIGNED
	require.NoError(t, f.keeper.TaskCore.Set(f.ctx, taskKey, core))

	deadlineKey := types.NewDeadlineIndexKey(window.AssignmentDeadlineHeight, taskKey)
	require.NoError(t, f.keeper.VerifyOpenDeadlineIndex.Set(f.ctx, deadlineKey))
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).
		WithBlockHeight(int64(window.AssignmentDeadlineHeight)))

	visited, err := f.keeper.processExpiredVerifyOpenDeadlines(
		f.ctx, window.AssignmentDeadlineHeight, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)

	core, err = f.keeper.TaskCore.Get(f.ctx, taskKey)
	require.NoError(t, err)
	require.Equal(t, types.TaskPhase_TASK_PHASE_VERIFIER_ASSIGNED, core.TaskPhase,
		"an assigned task must never be failed by the verify-open fallback")
	has, err := f.keeper.TaskSettlement.Has(f.ctx, taskKey)
	require.NoError(t, err)
	require.False(t, has)
}

func TestShortVerifierWindowIsATaskFailureNotAnInvariantBreach(t *testing.T) {
	f, taskKey, window, _, _ := verifierFinalizeFixture(t)
	shortenVerifierUnion(t, f, taskKey)

	_, _, _, err := f.keeper.finalizeVerifierLegalSetAtHeight(f.ctx, taskKey, window, 20, 3)
	require.ErrorIs(t, err, ErrInsufficientVerifierHandraises)
	require.NotErrorIs(t, err, types.ErrInvariantBroken,
		"a short window must never be able to abort FinalizeBlock")
}

func TestShortVerifierWindowRetiresItsCloseKeyAndKeepsTheChainProducing(t *testing.T) {
	f, taskKey, window, _, _ := verifierFinalizeFixture(t)
	shortenVerifierUnion(t, f, taskKey)

	// The frozen verify-open assignment deadline is the terminal key that fails
	// the task as TASK_FAILURE_CLASS_INSUFFICIENT_VERIFIER and refunds it.
	require.NoError(t, f.keeper.VerifyOpenDeadlineIndex.Set(f.ctx,
		types.NewDeadlineIndexKey(window.AssignmentDeadlineHeight, taskKey)))
	closeKey := types.NewVerifyRoundIndexKey(window.HandraiseCloseHeight, taskKey, types.VerifyRoundV1)
	require.NoError(t, f.keeper.VerifierHandraiseCloseIndex.Set(f.ctx, closeKey))

	usage, err := f.keeper.processVerifierHandraiseCloseIndexWithBudget(
		f.ctx, window.HandraiseCloseHeight, 1, maxDeadlineSweepBytesPerBlockV1)
	require.NoError(t, err, "the close sweep must not return an error to EndBlock")
	require.Equal(t, uint64(1), usage.visited)

	has, err := f.keeper.VerifierHandraiseCloseIndex.Has(f.ctx, closeKey)
	require.NoError(t, err)
	require.False(t, has,
		"a retired close key must not stay pending and hold the head of the height-ordered sweep")
}

// Without a later verify-open deadline nothing would ever retire the task, so
// that really is a store inconsistency and must stay an invariant.
func TestShortVerifierWindowWithoutAFallbackDeadlineStaysAnInvariant(t *testing.T) {
	f, taskKey, window, _, _ := verifierFinalizeFixture(t)
	shortenVerifierUnion(t, f, taskKey)
	closeKey := types.NewVerifyRoundIndexKey(window.HandraiseCloseHeight, taskKey, types.VerifyRoundV1)
	require.NoError(t, f.keeper.VerifierHandraiseCloseIndex.Set(f.ctx, closeKey))

	_, err := f.keeper.processVerifierHandraiseCloseIndexWithBudget(
		f.ctx, window.HandraiseCloseHeight, 1, maxDeadlineSweepBytesPerBlockV1)
	require.ErrorIs(t, err, types.ErrInvariantBroken)
}

// The shared executor reports a short window as "no work done" rather than as a
// caller fault, so a public caller does not see a network-shaped condition as
// its own error.
func TestRunVerifierRoundReportsAShortWindowAsNoWork(t *testing.T) {
	f, taskKey, window, _, _ := verifierFinalizeFixture(t)
	shortenVerifierUnion(t, f, taskKey)
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(window.HandraiseCloseHeight)))

	result, err := f.keeper.RunVerifierRound(f.ctx, window.TaskId, types.VerifyRoundV1, window.HandraiseCloseHeight, 3)
	require.NoError(t, err)
	require.False(t, result.LegalSetFinalized)
	require.False(t, result.AssignmentCreated)

	hasCursor, err := f.keeper.TaskCandidateFinalizeCursor.Has(f.ctx,
		types.NewTaskStageKey(taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY))
	require.NoError(t, err)
	require.False(t, hasCursor, "a short window must not leave a half-built legal set behind")
}

// The upper bound is the other half of the boundary: the handraise path caps
// entrants at max_candidate_union_members_per_stage, so a stored union above it
// contradicts the code that wrote it.
func TestOversizedVerifierUnionStaysAnInvariant(t *testing.T) {
	f, taskKey, window, _, _ := verifierFinalizeFixture(t)
	stageKey := types.NewTaskStageKey(taskKey, types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY)
	params, err := f.keeper.Params.Get(f.ctx)
	require.NoError(t, err)
	union, err := f.keeper.TaskStageHandraiseUnion.Get(f.ctx, stageKey)
	require.NoError(t, err)
	union.UnionCount = params.Proposals.MaxCandidateUnionMembersPerStage + 1
	require.NoError(t, f.keeper.TaskStageHandraiseUnion.Set(f.ctx, stageKey, union))

	_, _, _, err = f.keeper.finalizeVerifierLegalSetAtHeight(f.ctx, taskKey, window, 20, 3)
	require.ErrorIs(t, err, types.ErrInvariantBroken)
	require.NotErrorIs(t, err, ErrInsufficientVerifierHandraises)
}
