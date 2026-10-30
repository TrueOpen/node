package keeper_test

import (
	"fmt"
	"testing"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func reserveWorkerEvidenceProofForTest(t *testing.T, f *fixture, operator string, sessionID, taskID []byte) {
	t.Helper()
	id, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainServiceKeyResponsibilityIDV1)).Raw(shared.EnumBE(uint32(shared.ParticipantType_PARTICIPANT_TYPE_CORTEX)), shared.EnumBE(uint32(hubtypes.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_WORKER_OUTPUT_EVIDENCE)), sessionID, taskID, taskID, sdk.MustAccAddressFromBech32(operator)).Sum()
	require.NoError(t, err)
	require.NoError(t, f.keeper.ReserveServiceKeyResponsibility(f.ctx, hubtypes.ServiceKeyResponsibilityState{ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, OperatorAddress: operator, ResponsibilityId: id, ResponsibilityKind: hubtypes.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_WORKER_OUTPUT_EVIDENCE, SessionId: sessionID, TaskId: taskID, CreatedHeight: 2}))
}

func TestValueAndWorkerEvidenceShareOneObjectiveFaultInEitherOrder(t *testing.T) {
	for _, valueFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(valueFirst), func(t *testing.T) {
			f, operator, request := seedJailAdmissionLiabilityFixture(t, 9)
			request.Duty = shared.DutyWorker
			liability, err := f.keeper.ReserveTaskLiabilityFromFrozenFact(f.ctx, request)
			require.NoError(t, err)
			session := hubHashBytes("merged-objective-session")
			reserveWorkerEvidenceProofForTest(t, f, operator.Address, session, request.TaskID)
			f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(10))
			before, err := f.keeper.GetServiceBondState(f.ctx, operator.Address)
			require.NoError(t, err)
			value := shared.VerifierValueEvidenceFactV1{SessionId: session, TaskId: request.TaskID, VerifyRound: 1, TargetOperatorAddress: operator.Address, FaultedDuty: shared.DutyWorker, FaultClass: shared.FaultKind_FAULT_KIND_WORKER_VALUES_INVALID, EvidenceDigest: hubHashBytes("merged-value-proof"), FrozenSlashBps: 300}
			objective := shared.WorkerObjectiveEvidenceFactV1{SessionId: session, TaskId: request.TaskID, WorkerOperatorAddress: operator.Address, EvidenceDigest: hubHashBytes("merged-equivocation-proof"), FrozenSlashBps: 300}
			var first, second hubtypes.RoleFaultState
			if valueFirst {
				first, err = f.keeper.RecordVerifierValueEvidence(f.ctx, value)
				require.NoError(t, err)
				second, err = f.keeper.ApplyWorkerObjectiveEvidence(f.ctx, objective)
			} else {
				first, err = f.keeper.ApplyWorkerObjectiveEvidence(f.ctx, objective)
				require.NoError(t, err)
				second, err = f.keeper.RecordVerifierValueEvidence(f.ctx, value)
			}
			require.NoError(t, err)
			require.Equal(t, first.FaultId, second.FaultId)
			require.Equal(t, first.EvidenceDigest, second.EvidenceDigest)
			_, err = f.keeper.ApplyVerifierValueEvidenceFault(f.ctx, value, first.FaultId)
			require.NoError(t, err)
			_, err = f.keeper.ApplyWorkerObjectiveEvidence(f.ctx, objective)
			require.NoError(t, err)
			faults, err := f.keeper.RoleFaultsForTask(f.ctx, request.TaskID)
			require.NoError(t, err)
			require.Len(t, faults, 1)
			after, err := f.keeper.GetServiceBondState(f.ctx, operator.Address)
			require.NoError(t, err)
			expected := hubtypes.SlashByFraction(liability.ReservedAmount, 300, hubtypes.ObjectiveForgerySlashDenom)
			require.Equal(t, before.ActiveBond-expected, after.ActiveBond)
			require.Equal(t, expected, f.bank.moduleBalance(hubtypes.TreasuryModuleName))
			closed, err := f.keeper.GetTaskLiabilityReservation(f.ctx, request.TaskID, shared.DutyWorker, operator.Address)
			require.NoError(t, err)
			require.Equal(t, hubtypes.TaskLiabilityStatusSlashed, closed.Status)
		})
	}
}
