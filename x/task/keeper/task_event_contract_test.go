package keeper

import (
	"bytes"
	"context"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	proto "github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// Three event-contract tests were deleted by the earlier schema stub
// pass, because the emitters they pinned are gone with their handlers.
//
//  1. TestEmitOpenVerifyAcceptedEvent pinned EventOpenVerifyAccepted: the CSV
//     formal_verifier_set is split into a repeated field and all four heights
//     (open_verify / sample_seed_ready / commit_deadline / verify_deadline) are
//     carried. Both the event and MsgOpenVerify are removed from the frozen wire;
//     Verification owns EventVerifierHandraisesAccepted / EventVerifierAssignmentFinalized
//     and must pin the replacement heights there.
//  2. TestEmitInferReceiptAcceptedEvent pinned EventInferReceiptAccepted field by
//     field against the stored InferReceiptState. The event kept its name but changed
//     shape (worker / infer_receipt_hash / output_hash /
//     verifier_window_randomness_height, all Hash32 raw bytes; session_id/task_id are
//     bytes; receipt_mode and receipt_height are gone). Re-pin it once
//     SubmitInferReceipt exists and once it decides where
//     verifier_window_randomness_height is frozen.
//  3. The former reveal event test targeted deleted fields. Verification runtime
//     tests now pin the registered EventRevealPhaseStarted and
//     EventCommitDeadlineClosed payloads for both reveal triggers.
//
// TestEmitWorkerAssignmentFinalizedEvent below replaces the deleted
// TestEmitAssignmentFinalizedEvent: EventAssignmentFinalized became
// EventWorkerAssignmentFinalized (§5.11 code 11) and TaskAssignment became
// TaskAssignmentState keyed by task_id alone with winner_worker.

func TestEmitWorkerAssignmentFinalizedEvent(t *testing.T) {
	f := initInternalFixture(t)
	sessionID := bytes.Repeat([]byte{0x01}, types.Hash32Len)
	taskID := bytes.Repeat([]byte{0x02}, types.Hash32Len)
	legalSetHash := bytes.Repeat([]byte{0x03}, types.Hash32Len)

	want := &types.EventWorkerAssignmentFinalized{
		SessionId:                  sessionID,
		TaskId:                     taskID,
		WinnerWorker:               "worker-assignment",
		AssignmentCandidateSetHash: legalSetHash,
		RandomnessHeight:           101,
		ReservedAmount:             shared.NewAmount(1234),
		InferDeadline:              121,
	}
	require.NoError(t, emitTypedEvent(f.ctx, want))
	require.Equal(t, want, lastTypedEvent(t, f.ctx, want))
}

// §5.11 code 20 `deadline_swept` carries the §5.9 DeadlineKindV1 numbers and the
// optional session_id / task_id oneof branches, never a second deadline enum.
func TestEmitDeadlineSweptEventCarriesFrozenKind(t *testing.T) {
	f := initInternalFixture(t)
	core := types.TaskCoreState{
		SessionId: bytes.Repeat([]byte{0x04}, types.Hash32Len),
		TaskId:    bytes.Repeat([]byte{0x05}, types.Hash32Len),
	}
	require.NoError(t, emitDeadlineSweptEvent(f.ctx, core,
		types.DeadlineKindV1_DEADLINE_KIND_V1_WORKER_INFER,
		types.DeadlineTransitionCode_DEADLINE_TRANSITION_CODE_WORKER_TIMEOUT,
		types.TaskFailureClass_TASK_FAILURE_CLASS_OBJECTIVE_FAULT))

	want := &types.EventDeadlineSwept{
		XSessionId:     &types.EventDeadlineSwept_SessionId{SessionId: core.SessionId},
		XTaskId:        &types.EventDeadlineSwept_TaskId{TaskId: core.TaskId},
		DeadlineKind:   types.DeadlineKindV1_DEADLINE_KIND_V1_WORKER_INFER,
		TransitionCode: types.DeadlineTransitionCode_DEADLINE_TRANSITION_CODE_WORKER_TIMEOUT,
		FailureClass:   types.TaskFailureClass_TASK_FAILURE_CLASS_OBJECTIVE_FAULT,
	}
	require.Equal(t, want, lastTypedEvent(t, f.ctx, want))
}

func lastTypedEvent(t *testing.T, ctx context.Context, want proto.Message) proto.Message {
	t.Helper()
	event := lastEventOfType(t, sdk.UnwrapSDKContext(ctx).EventManager().Events(), proto.MessageName(want))
	got, err := sdk.ParseTypedEvent(sdk.Events{event}.ToABCIEvents()[0])
	require.NoError(t, err)
	return got
}

func lastEventOfType(t *testing.T, events sdk.Events, eventType string) sdk.Event {
	t.Helper()
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == eventType {
			return events[i]
		}
	}
	t.Fatalf("event %q was not emitted", eventType)
	return sdk.Event{}
}
