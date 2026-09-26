package keeper_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestWorkerObjectiveEvidenceUsesRetainedResponsibilityAndSlashesOnce(t *testing.T) {
	f, identity, request := seedJailAdmissionLiabilityFixture(t, 10)
	request.Duty = shared.DutyWorker
	_, err := f.keeper.ReserveTaskLiabilityFromFrozenFact(f.ctx, request)
	require.NoError(t, err)
	sessionID := hubHashBytes("worker-evidence-session")
	sessionHex, taskHex := hex.EncodeToString(sessionID), hex.EncodeToString(request.TaskID)
	responsibilityID, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainServiceKeyResponsibilityIDV1)).Raw(
		shared.EnumBE(uint32(shared.ParticipantType_PARTICIPANT_TYPE_CORTEX)),
		shared.EnumBE(uint32(types.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_WORKER_OUTPUT_EVIDENCE)),
		[]byte(sessionHex), []byte(taskHex), []byte(taskHex), []byte(identity.Address),
	).Sum()
	require.NoError(t, err)
	require.NoError(t, f.keeper.ReserveServiceKeyResponsibility(f.ctx, types.ServiceKeyResponsibilityState{
		ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, OperatorAddress: identity.Address,
		ResponsibilityId:   responsibilityID,
		ResponsibilityKind: types.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_WORKER_OUTPUT_EVIDENCE,
		SessionId:          sessionHex, TaskId: taskHex, CreatedHeight: 2,
	}))
	require.NoError(t, f.keeper.ReleaseTaskLiabilities(f.ctx, sessionHex, taskHex, 3))

	proof, err := f.keeper.GetWorkerEvidenceProofKey(f.ctx, sessionHex, taskHex, identity.Address)
	require.NoError(t, err)
	require.Equal(t, uint64(1), proof.AuthorizationNonce)
	node, err := f.keeper.ReadCortexNodeStore(f.ctx, identity.Address)
	require.NoError(t, err)
	require.Equal(t, uint32(1), node.PendingEvidenceSubmissionCount)

	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(4))
	fact := shared.WorkerObjectiveEvidenceFactV1{
		SessionId: sessionID, TaskId: request.TaskID, WorkerOperatorAddress: identity.Address,
		EvidenceDigest: hubHashBytes("worker-output-equivocation"), FrozenSlashBps: 300,
	}
	before, err := f.keeper.ReadServiceBondValue(f.ctx, identity.Address)
	require.NoError(t, err)
	fault, err := f.keeper.ApplyWorkerObjectiveEvidence(f.ctx, fact)
	require.NoError(t, err)
	require.Equal(t, shared.DutyWorker, fault.Duty)
	require.Equal(t, types.FaultKind_FAULT_KIND_EQUIVOCATION, fault.FaultClass)
	require.Equal(t, shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_OBJECTIVE_EVIDENCE, fault.ClassificationSource)
	require.True(t, bytes.Equal(fact.EvidenceDigest, fault.EvidenceDigest))
	after, err := f.keeper.ReadServiceBondValue(f.ctx, identity.Address)
	require.NoError(t, err)
	require.Less(t, after.ActiveBond, before.ActiveBond)

	replayed, err := f.keeper.ApplyWorkerObjectiveEvidence(f.ctx, fact)
	require.NoError(t, err)
	require.Equal(t, fault, replayed)
	afterReplay, err := f.keeper.ReadServiceBondValue(f.ctx, identity.Address)
	require.NoError(t, err)
	require.Equal(t, after.ActiveBond, afterReplay.ActiveBond)

	require.NoError(t, f.keeper.ReleaseServiceKeyResponsibility(
		f.ctx, shared.ParticipantTypeCortexNode, identity.Address, hex.EncodeToString(responsibilityID),
	))
	_, err = f.keeper.ReadCortexNodeStore(f.ctx, identity.Address)
	require.ErrorIs(t, err, collections.ErrNotFound, "proof-only identity retires when its final responsibility closes")
}

func TestWorkerObjectiveEvidenceCanSlashBeforeTaskFinality(t *testing.T) {
	f, identity, request := seedJailAdmissionLiabilityFixture(t, 10)
	request.Duty = shared.DutyWorker
	_, err := f.keeper.ReserveTaskLiabilityFromFrozenFact(f.ctx, request)
	require.NoError(t, err)
	sessionID := hubHashBytes("active-worker-evidence-session")
	sessionHex, taskHex := hex.EncodeToString(sessionID), hex.EncodeToString(request.TaskID)
	responsibilityID, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainServiceKeyResponsibilityIDV1)).Raw(
		shared.EnumBE(uint32(shared.ParticipantType_PARTICIPANT_TYPE_CORTEX)),
		shared.EnumBE(uint32(types.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_WORKER_OUTPUT_EVIDENCE)),
		[]byte(sessionHex), []byte(taskHex), []byte(taskHex), []byte(identity.Address),
	).Sum()
	require.NoError(t, err)
	require.NoError(t, f.keeper.ReserveServiceKeyResponsibility(f.ctx, types.ServiceKeyResponsibilityState{
		ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_CORTEX, OperatorAddress: identity.Address,
		ResponsibilityId: responsibilityID, ResponsibilityKind: types.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_WORKER_OUTPUT_EVIDENCE,
		SessionId: sessionHex, TaskId: taskHex, CreatedHeight: 2,
	}))
	f.ctx = sdk.WrapSDKContext(sdk.UnwrapSDKContext(f.ctx).WithBlockHeight(3))
	fault, err := f.keeper.ApplyWorkerObjectiveEvidence(f.ctx, shared.WorkerObjectiveEvidenceFactV1{
		SessionId: sessionID, TaskId: request.TaskID, WorkerOperatorAddress: identity.Address,
		EvidenceDigest: hubHashBytes("active-worker-equivocation"), FrozenSlashBps: 300,
	})
	require.NoError(t, err)
	require.Equal(t, types.FaultKind_FAULT_KIND_EQUIVOCATION, fault.FaultClass)
	reservation, err := f.keeper.GetTaskLiabilityReservation(f.ctx, request.TaskID, shared.DutyWorker, identity.Address)
	require.NoError(t, err)
	require.False(t, reservation.Reserved)
}
