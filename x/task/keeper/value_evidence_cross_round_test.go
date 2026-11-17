package keeper

import (
	"bytes"
	"testing"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func TestValueEvidenceAcrossRoundsRetainsOneOriginalFault(t *testing.T) {
	for _, objectiveFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "value fault", true: "earlier equivocation"}[objectiveFirst], func(t *testing.T) {
			for _, order := range [][]uint32{{1, 2}, {2, 1}} {
				f, hub, original := submitCrossRoundWorkerEvidence(t, objectiveFirst, order...)
				rows, err := f.keeper.firstValueEvidenceReceipts(f.ctx, f.taskID)
				require.NoError(t, err)
				require.Len(t, rows, 1, "one merged fault must have one settlement consequence across rounds")
				require.Equal(t, order[0], rows[0].VerifyRound)
				fault := hub.faults[string(original)]
				_, err = taskFaultSummaryHash(sdkChainID(f.ctx), f.taskID, []taskRoleFaultRecord{{faultID: fault.FaultId, operatorAddress: fault.OperatorAddress, duty: fault.Duty, faultClass: fault.FaultClass, classificationSource: fault.ClassificationSource, evidenceDigest: fault.EvidenceDigest, status: fault.Status}})
				require.NoError(t, err)
				require.NoError(t, f.keeper.EnsureValueEvidenceInvariant(f.ctx))
				require.True(t, hubtypes.IsMergedValueEvidenceFault(fault))
			}
		})
	}
}

func submitCrossRoundWorkerEvidence(t *testing.T, objectiveFirst bool, order ...uint32) (*verificationFixture, *valueEvidenceTestHub, []byte) {
	t.Helper()
	f, hub, evidence := valueEvidenceFixture(t, true)
	assignment, err := f.keeper.ReadTaskAssignment(f.ctx, f.taskID)
	require.NoError(t, err)
	second := f.assignment
	second.VerifyRound = types.ChallengeVerifyRoundV1
	require.NoError(t, f.keeper.WriteVerifierAssignment(f.ctx, types.NewVerifyRoundKey(f.taskID, 2), second))
	core, err := f.keeper.TaskCore.Get(f.ctx, f.taskID)
	require.NoError(t, err)
	require.NoError(t, f.keeper.WriteVerificationRound(f.ctx, types.NewVerifyRoundKey(f.taskID, 2), types.VerificationRoundState{TaskId: f.taskID, TaskHash: core.AcceptedTaskHash, VerifyRound: 2, RoundId: bytes32(0x91), RoundOpenHeight: 1, RoundCloseDeadlineHeight: 200, GeneratedTokenCount: 2}))
	var original []byte
	if len(order) == 0 {
		order = []uint32{1, 2}
	}
	for index, round := range order {
		proof := *evidence[0]
		proof.VerifyRound = round
		proof.EvidenceKind = types.VerifierValueEvidenceKindV1_VERIFIER_VALUE_EVIDENCE_KIND_V1_WORKER_VALUES_INVALID
		proof.TargetOperatorAddress = assignment.WinnerWorker
		opening := *proof.Positions[0]
		opening.VerifierValueLeaf, opening.VerifierValuePath = nil, nil
		opening.MetricLeaf, opening.MetricPath = nil, nil
		proof.Positions = []*types.VerifierValuePositionOpeningV1{&opening}
		f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(int64(10 + index)))
		raw, err := proof.Marshal()
		require.NoError(t, err)
		response, err := f.server.SubmitVerifierValueEvidence(f.ctx, &types.MsgSubmitVerifierValueEvidence{EvidenceBytes: raw, SubmitterAddress: f.operators[2]})
		require.NoError(t, err)
		require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Status)
		if index == 0 {
			original = bytes.Clone(response.FaultId)
			if objectiveFirst {
				row := hub.faults[string(original)]
				row.FaultClass = shared.FaultKind_FAULT_KIND_EQUIVOCATION
				row.EvidenceDigest = bytes32(0x72)
				row.RecordedHeight--
				hub.faults[string(original)] = row
			}
		}
		require.Equal(t, original, response.FaultId)
	}
	return f, hub, original
}

func TestValueEvidenceInvariantChecksOriginalAcrossRoundsAndCleanup(t *testing.T) {
	f, _, _ := submitCrossRoundWorkerEvidence(t, false)
	require.NoError(t, f.keeper.EnsureValueEvidenceInvariant(f.ctx))
	assignment, err := f.keeper.ReadTaskAssignment(f.ctx, f.taskID)
	require.NoError(t, err)
	target, err := f.keeper.sessionAddressToStore("Worker", assignment.WinnerWorker)
	require.NoError(t, err)
	require.NoError(t, f.keeper.ValueEvidenceReceipt.Remove(f.ctx, types.NewValueEvidenceReceiptKey(f.taskID, 1, target, 0)))
	require.ErrorContains(t, f.keeper.EnsureValueEvidenceInvariant(f.ctx), "no original receipt")
	require.NoError(t, f.keeper.TaskCleanupCursor.Set(f.ctx, f.taskID, types.TaskCleanupCursorState{TaskId: f.taskID, Phase: types.TaskCleanupPhase_TASK_CLEANUP_PHASE_ROUND_STATE_AND_EFFECTS}))
	require.NoError(t, f.keeper.EnsureValueEvidenceInvariant(f.ctx), "bounded cleanup may have consumed the first receipt")
}
