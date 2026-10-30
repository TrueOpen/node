package keeper

import (
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func TestInferReceiptMustBindAcceptedDispatchTicket(t *testing.T) {
	for _, wrong := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted ticket and replay", true: "different signed attempt"}[wrong], func(t *testing.T) {
			f := newVerificationFixture(t)
			core, err := f.keeper.TaskCore.Get(f.ctx, f.taskID)
			require.NoError(t, err)
			core.AcceptedTaskHash = bytes32(0x71)
			core.TaskPhase = types.TaskPhase_TASK_PHASE_INFER_PENDING
			core.ReceiptStatus = types.ReceiptStatus_RECEIPT_STATUS_NONE
			require.NoError(t, f.keeper.TaskCore.Set(f.ctx, f.taskID, core))
			assignment, err := f.keeper.ReadTaskAssignment(f.ctx, f.taskID)
			require.NoError(t, err)
			assignment.WinnerWorker = f.operators[0]
			assignment.DispatchTicketHash = bytes32(0x72)
			assignment.InferDeadlineHeight = 100
			require.NoError(t, f.keeper.WriteTaskAssignment(f.ctx, f.taskID, assignment))
			require.NoError(t, f.keeper.TaskBudget.Set(f.ctx, f.taskID, types.TaskBudgetState{TaskId: f.taskID, MaxOutputTokens: 10}))
			f.setTaskBuilders(t, f.operators[1], f.operators[2])
			receipt := types.InferReceiptV3{SchemaVersion: 3, ChainId: sdkChainID(f.ctx), TaskId: f.taskID, TaskHash: core.AcceptedTaskHash, WorkerOperatorAddress: assignment.WinnerWorker, ServiceAuthorizationNonce: 7, GenerationParamsDigest: f.genDigest, OutputHash: bytes32(0x73), OutputSizeBytes: 4, RequiredEvidenceCommitments: []types.EvidenceCommitmentV1{{EvidenceKind: shared.EvidenceKind_EVIDENCE_KIND_WORKER_VALUE_OPENING, EvidenceHashOrRoot: bytes32(0x74), EncodedSizeBytes: 1}, {EvidenceKind: shared.EvidenceKind_EVIDENCE_KIND_WORKER_TOKEN_OPENING, EvidenceHashOrRoot: bytes32(0x75), EncodedSizeBytes: 1}}, ExpiryHeight: 100, GeneratedTokenCount: 2, OutputLeafCount: 2, OutputKeyCommitment: make([]byte, 32), WorkerTokenKeyCommitment: make([]byte, 32), WorkerValueKeyCommitment: make([]byte, 32), CiphertextOutputRoot: make([]byte, 32), DispatchTicketHash: assignment.DispatchTicketHash, ServiceSignature: make([]byte, 64)}
			if wrong {
				receipt.DispatchTicketHash = bytes32(0x76)
			}
			request := &types.MsgSubmitInferReceipt{Receipt: receipt, SubmitterAddress: assignment.WinnerWorker}
			response, err := f.server.SubmitInferReceipt(f.ctx, request)
			if wrong {
				code, ok := dispatchRejectionCode(err)
				require.True(t, ok, "%v", err)
				require.Equal(t, types.DispatchRejectCodeV1_DISPATCH_REJECT_CODE_V1_TICKET_INVALID, code)
				found, readErr := f.keeper.InferReceipt.Has(f.ctx, f.taskID)
				require.NoError(t, readErr)
				require.False(t, found)
				return
			}
			require.NoError(t, err)
			require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Status)
			replay, err := f.server.SubmitInferReceipt(f.ctx, request)
			require.NoError(t, err)
			require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, replay.Status)
			request.Receipt.DispatchTicketHash = bytes32(0x76)
			_, err = f.server.SubmitInferReceipt(f.ctx, request)
			require.Error(t, err, "a changed attempt cannot replay another ticket's receipt")
		})
	}
}
