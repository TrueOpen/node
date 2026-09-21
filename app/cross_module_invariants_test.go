package app

import (
	"bytes"
	"encoding/hex"
	"testing"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func TestCrossModuleReferencesAcceptDefaultGenesis(t *testing.T) {
	application := bootAppMinimal(t)
	ctx := application.NewContextLegacy(true, cmtproto.Header{Height: application.LastBlockHeight() + 1})
	require.NoError(t, application.EnsureCrossModuleReferences(ctx))
}

func TestCrossModuleReferencesRequireExactRoleFaultEvidenceScope(t *testing.T) {
	application := bootAppMinimal(t)
	ctx := application.NewContextLegacy(true, cmtproto.Header{Height: application.LastBlockHeight() + 1})
	taskID := bytes.Repeat([]byte{0x61}, 32)
	evidenceDigest := bytes.Repeat([]byte{0x62}, 32)
	faultID := bytes.Repeat([]byte{0x63}, 32)
	operator := sdk.AccAddress(bytes.Repeat([]byte{0x64}, 20)).String()
	taskKey := tasktypes.NewTaskKey(taskID)
	// v0.3 keys TaskFailureClass by (task_id, verify_round).
	failureKey := tasktypes.NewVerifyRoundKey(taskKey, tasktypes.VerifyRoundV1)

	require.NoError(t, application.TaskKeeper.TaskFailureClass.Set(ctx, failureKey, tasktypes.TaskFailureClassState{
		TaskId: taskID, VerifyRound: tasktypes.VerifyRoundV1,
		FailureClass:         tasktypes.TaskFailureClass_TASK_FAILURE_CLASS_METRIC_THRESHOLD_BREACH,
		FreezeSignalEligible: true,
		ClassificationSource: shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_SETTLEMENT,
		ClassifiedHeight:     1, EvidenceDigest: evidenceDigest,
	}))
	fault := hubtypes.RoleFaultState{
		FaultId: faultID, TaskId: taskID, OperatorAddress: operator,
		Duty: shared.DutyWorker, FaultClass: hubtypes.FaultKind_FAULT_KIND_INVALID_RESULT,
		ClassificationSource: shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_SETTLEMENT,
		EvidenceDigest:       evidenceDigest, RecordedHeight: 1,
		Status: hubtypes.RoleFaultStatus_ROLE_FAULT_STATUS_CONFIRMED,
	}
	require.NoError(t, application.HubKeeper.RoleFault.Set(ctx, faultID, fault))
	require.NoError(t, application.EnsureCrossModuleReferences(ctx))

	fault.EvidenceDigest = bytes.Repeat([]byte{0x65}, 32)
	require.NoError(t, application.HubKeeper.RoleFault.Set(ctx, faultID, fault))
	require.ErrorContains(t, application.EnsureCrossModuleReferences(ctx), "evidence_digest disagrees")

	// classification_source is the other half of the same reference. A Hub that
	// re-derives it from the fault type instead of consuming the Task verdict
	// produces exactly this shape: a matching digest under a disagreeing source,
	// which must fail before it can reach an export.
	fault.EvidenceDigest = evidenceDigest
	fault.ClassificationSource = shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_DEADLINE
	require.NoError(t, application.HubKeeper.RoleFault.Set(ctx, faultID, fault))
	require.ErrorContains(t, application.EnsureCrossModuleReferences(ctx), "evidence_digest disagrees")
}

func TestBuilderDutyResponsibilitiesFollowTaskOwnedLifecycle(t *testing.T) {
	application := bootAppMinimal(t)
	ctx := application.NewContextLegacy(true, cmtproto.Header{Height: application.LastBlockHeight() + 1})
	ctx = ctx.WithChainID("builder-duty-invariant")
	taskID := bytes.Repeat([]byte{0x81}, tasktypes.Hash32Len)
	sessionID := bytes.Repeat([]byte{0x82}, tasktypes.Hash32Len)
	// taskKey is the Task-side store key (raw 32 bytes since X-16). taskHex is the
	// SAME id in the spelling Hub uses, and it is load-bearing twice over: it is a
	// hash preimage component of responsibility_id (serviceKeyResponsibilityID hashes
	// the hex text, so feeding it raw bytes would derive a different id and the Hub
	// rows below would stop matching what the invariant recomputes), and it is the
	// value of ServiceKeyResponsibilityState.task_id, a string proto field.
	taskKey := tasktypes.NewTaskKey(taskID)
	taskHex := hex.EncodeToString(taskID)
	builders := []string{
		sdk.AccAddress(bytes.Repeat([]byte{0x83}, 20)).String(),
		sdk.AccAddress(bytes.Repeat([]byte{0x84}, 20)).String(),
		sdk.AccAddress(bytes.Repeat([]byte{0x85}, 20)).String(),
	}
	builderSetHash := bytes.Repeat([]byte{0x86}, tasktypes.Hash32Len)
	selectedHash, err := tasktypes.SelectedTaskBuildersHash(
		ctx.ChainID(), taskID, "builder-set-v1", builderSetHash, builders,
	)
	require.NoError(t, err)
	// An external anchor, not a recomputation. Everything else this test says about
	// the two commitments below is derived the same way the production code derives
	// them, so a reordered preimage stays green the moment the derivation here is
	// edited to agree. A frozen hex digest cannot be re-derived from the test, so
	// that same edit has to move a constant whose provenance is the contract.
	require.Equal(t, "9152865c416c8632dcd9756f2161c6348319ca073dcc73f478f9b7a49140e188",
		hex.EncodeToString(selectedHash),
		"TRUEOPEN_SELECTED_TASK_BUILDERS_V1 is a frozen consensus preimage; moving this constant is a consensus change and must be re-checked against the data-structure contract §6.5 and the §1.4 domain registry")
	require.NoError(t, application.TaskKeeper.TaskCore.Set(ctx, taskKey, tasktypes.TaskCoreState{
		TaskId: taskID, SessionId: sessionID,
		TaskPhase: tasktypes.TaskPhase_TASK_PHASE_RECEIPT_COMMITTED,
	}))
	require.NoError(t, application.TaskKeeper.TaskBuilderSelection.Set(ctx, taskKey, tasktypes.TaskBuilderSelectionState{
		TaskId: taskID, BuilderSetId: "builder-set-v1", BuilderSetHash: builderSetHash,
		SelectedTaskBuilders: builders, SelectedTaskBuilderCount: uint32(len(builders)),
		SelectedTaskBuildersHash: selectedHash, BodyStatus: shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE,
	}))
	require.NoError(t, application.TaskKeeper.InferReceipt.Set(ctx, taskKey, tasktypes.InferReceiptState{
		TaskId: taskID, ReceiptHeight: 10,
	}))
	require.ErrorContains(t, application.ensureBuilderDutyResponsibilities(ctx), "missing 3 Hub responsibilities")

	setResponsibilities := func(kind hubtypes.ServiceKeyResponsibilityKind, height uint64) [][]byte {
		written := make([][]byte, 0, len(builders))
		for _, builder := range builders {
			responsibilityID := shared.CanonicalHashBytes(
				shared.MustDomain(shared.DomainServiceKeyResponsibilityIDV1),
				shared.EnumBE(uint32(shared.ParticipantType_PARTICIPANT_TYPE_BUILDER)),
				shared.EnumBE(uint32(kind)), []byte(hex.EncodeToString(sessionID)), []byte(taskHex), nil, []byte(builder),
			)
			require.NoError(t, application.HubKeeper.ServiceKeyResponsibility.Set(ctx,
				hubtypes.NewServiceKeyResponsibilityKey(shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, builder, responsibilityID),
				hubtypes.ServiceKeyResponsibilityState{
					ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_BUILDER,
					OperatorAddress: builder, ResponsibilityId: responsibilityID, ResponsibilityKind: kind,
					SessionId: hex.EncodeToString(sessionID), TaskId: taskHex, CreatedHeight: height,
				},
			))
			written = append(written, responsibilityID)
		}
		return written
	}
	removeResponsibilities := func(kind hubtypes.ServiceKeyResponsibilityKind) {
		for _, builder := range builders {
			responsibilityID := shared.CanonicalHashBytes(
				shared.MustDomain(shared.DomainServiceKeyResponsibilityIDV1),
				shared.EnumBE(uint32(shared.ParticipantType_PARTICIPANT_TYPE_BUILDER)),
				shared.EnumBE(uint32(kind)), []byte(hex.EncodeToString(sessionID)), []byte(taskHex), nil, []byte(builder),
			)
			require.NoError(t, application.HubKeeper.ServiceKeyResponsibility.Remove(ctx,
				hubtypes.NewServiceKeyResponsibilityKey(shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, builder, responsibilityID),
			))
		}
	}

	openVerifyIDs := setResponsibilities(hubtypes.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_OPEN_VERIFY_BUILDER, 10)
	require.Equal(t, "4cbbda58e9112bb22b2b6de78cbc7e88c87dc8f3513b612c8e18c9e6a21c50d6",
		hex.EncodeToString(openVerifyIDs[0]),
		"TRUEOPEN_SERVICE_KEY_RESPONSIBILITY_ID_V1 is a frozen consensus preimage; moving this constant is a consensus change and must be re-checked against the §1.4 domain registry")
	require.NoError(t, application.ensureBuilderDutyResponsibilities(ctx))
	require.NoError(t, application.TaskKeeper.VerifierAssignment.Set(
		ctx, tasktypes.NewVerifyRoundKey(taskKey, tasktypes.VerifyRoundV1),
		tasktypes.VerifierAssignmentState{TaskId: taskID, VerifyRound: tasktypes.VerifyRoundV1, OpenVerifyHeight: 20},
	))
	require.ErrorContains(t, application.ensureBuilderDutyResponsibilities(ctx), "no exact Task-owned authority")
	removeResponsibilities(hubtypes.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_OPEN_VERIFY_BUILDER)
	setResponsibilities(hubtypes.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_SETTLE_BUILDER, 20)
	require.NoError(t, application.ensureBuilderDutyResponsibilities(ctx))

	selection, err := application.TaskKeeper.TaskBuilderSelection.Get(ctx, taskKey)
	require.NoError(t, err)
	selection.BuilderSetRefReleased = true
	require.NoError(t, application.TaskKeeper.TaskBuilderSelection.Set(ctx, taskKey, selection))
	require.ErrorContains(t, application.ensureBuilderDutyResponsibilities(ctx), "no exact Task-owned authority")
	removeResponsibilities(hubtypes.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_SETTLE_BUILDER)
	require.NoError(t, application.ensureBuilderDutyResponsibilities(ctx))
}

func TestBusObjectiveEvidenceResponsibilitiesFollowRetainedTaskSelection(t *testing.T) {
	application := bootAppMinimal(t)
	ctx := application.NewContextLegacy(true, cmtproto.Header{Height: application.LastBlockHeight() + 1})
	ctx = ctx.WithChainID("bus-objective-evidence-invariant")
	taskID := bytes.Repeat([]byte{0x91}, tasktypes.Hash32Len)
	sessionID := bytes.Repeat([]byte{0x92}, tasktypes.Hash32Len)
	taskKey := tasktypes.NewTaskKey(taskID)
	builder := sdk.AccAddress(bytes.Repeat([]byte{0x93}, 20)).String()
	builderSetHash := bytes.Repeat([]byte{0x94}, tasktypes.Hash32Len)
	selectedHash, err := tasktypes.SelectedTaskBuildersHash(ctx.ChainID(), taskID, "1", builderSetHash, []string{builder})
	require.NoError(t, err)
	require.NoError(t, application.TaskKeeper.TaskCore.Set(ctx, taskKey, tasktypes.TaskCoreState{
		TaskId: taskID, SessionId: sessionID,
		TaskPhase: tasktypes.TaskPhase_TASK_PHASE_WORKER_ASSIGNMENT_PENDING,
	}))
	selection := tasktypes.TaskBuilderSelectionState{
		TaskId: taskID, BuilderSetId: "1", BuilderSetHash: builderSetHash,
		SelectedTaskBuilders: []string{builder}, SelectedTaskBuilderCount: 1,
		SelectedTaskBuildersHash: selectedHash, CreatedHeight: 7,
		BodyStatus: shared.StoredBodyStatus_STORED_BODY_STATUS_ACTIVE,
	}
	require.NoError(t, application.TaskKeeper.TaskBuilderSelection.Set(ctx, taskKey, selection))
	builderState := hubtypes.BuilderState{
		BuilderAddress: builder, ServiceAuthorizationNonce: 9,
		CurrentServiceKeyStatus: hubtypes.ServiceKeyStatusActive,
	}
	require.NoError(t, application.HubKeeper.Builder.Set(ctx, builder, builderState))
	require.ErrorContains(t, application.ensureBusObjectiveEvidenceResponsibilities(ctx), "missing 1 BUS")

	locator := shared.BusObjectiveEvidenceResponsibilityV1{
		SchemaVersion: 1, BuilderOperator: builder, SessionId: sessionID, TaskId: taskID,
		ServiceAuthorizationNonce: builderState.ServiceAuthorizationNonce,
	}
	responsibilityID, err := application.HubKeeper.BusObjectiveEvidenceResponsibilityID(locator)
	require.NoError(t, err)
	state := hubtypes.ServiceKeyResponsibilityState{
		ParticipantType: shared.ParticipantType_PARTICIPANT_TYPE_BUILDER,
		OperatorAddress: builder, ResponsibilityId: responsibilityID,
		ResponsibilityKind: hubtypes.ServiceKeyResponsibilityKind_SERVICE_KEY_RESPONSIBILITY_KIND_BUS_OBJECTIVE_EVIDENCE,
		SessionId:          hex.EncodeToString(sessionID), TaskId: hex.EncodeToString(taskID),
		CreatedHeight: selection.CreatedHeight, ServiceAuthorizationNonce: builderState.ServiceAuthorizationNonce,
	}
	primaryKey := hubtypes.NewServiceKeyResponsibilityKey(
		shared.ParticipantType_PARTICIPANT_TYPE_BUILDER, builder, responsibilityID,
	)
	indexKey := hubtypes.NewServiceKeyResponsibilityByTaskKey(
		state.SessionId, state.TaskId, state.ParticipantType, state.OperatorAddress, state.ResponsibilityId,
	)
	require.NoError(t, application.HubKeeper.ServiceKeyResponsibility.Set(ctx, primaryKey, state))
	require.ErrorContains(t, application.ensureBusObjectiveEvidenceResponsibilities(ctx), "missing its ByTask index")
	require.NoError(t, application.HubKeeper.ServiceKeyResponsibilityByTaskIndex.Set(ctx, indexKey))
	builderState.PendingEvidenceSubmissionCount = 1
	require.NoError(t, application.HubKeeper.Builder.Set(ctx, builder, builderState))
	require.NoError(t, application.ensureBusObjectiveEvidenceResponsibilities(ctx))

	state.ServiceAuthorizationNonce++
	require.NoError(t, application.HubKeeper.ServiceKeyResponsibility.Set(ctx, primaryKey, state))
	require.ErrorContains(t, application.ensureBusObjectiveEvidenceResponsibilities(ctx), "no exact Task-owned authority")
	state.ServiceAuthorizationNonce--
	require.NoError(t, application.HubKeeper.ServiceKeyResponsibility.Set(ctx, primaryKey, state))

	selection.SelectedTaskBuilders = nil
	selection.BodyStatus = shared.StoredBodyStatus_STORED_BODY_STATUS_PRUNED
	require.NoError(t, application.TaskKeeper.TaskBuilderSelection.Set(ctx, taskKey, selection))
	require.ErrorContains(t, application.ensureBusObjectiveEvidenceResponsibilities(ctx), "no exact Task-owned authority")
	require.NoError(t, application.HubKeeper.ServiceKeyResponsibility.Remove(ctx, primaryKey))
	require.NoError(t, application.HubKeeper.ServiceKeyResponsibilityByTaskIndex.Remove(ctx, indexKey))
	builderState.PendingEvidenceSubmissionCount = 0
	require.NoError(t, application.HubKeeper.Builder.Set(ctx, builder, builderState))
	require.NoError(t, application.ensureBusObjectiveEvidenceResponsibilities(ctx))
}

func TestCrossModuleReferencesRejectOrphanTaskRetentionReferences(t *testing.T) {
	// The store key and the row's own two ids are the same raw Hash32 now, so a
	// mismatch is checkable directly. Nothing exercised that branch before this
	// batch -- the case below seeds a key naming a different task than the row.
	t.Run("candidate pool key mismatch", func(t *testing.T) {
		application := bootAppMinimal(t)
		ctx := application.NewContextLegacy(true, cmtproto.Header{Height: application.LastBlockHeight() + 1})
		taskID := bytes.Repeat([]byte{0x51}, 32)
		snapshotID := bytes.Repeat([]byte{0x52}, 32)
		require.NoError(t, application.HubKeeper.CandidatePoolTaskRef.Set(ctx,
			hubtypes.NewCandidatePoolTaskRefKey(bytes.Repeat([]byte{0x50}, 32), snapshotID),
			hubtypes.CandidatePoolTaskRefState{TaskId: taskID, SnapshotId: snapshotID, AcquiredHeight: 1,
				Status: hubtypes.CandidatePoolTaskRefStatus_CANDIDATE_POOL_TASK_REF_STATUS_ACQUIRED},
		))
		require.ErrorContains(t, application.EnsureCrossModuleReferences(ctx), "candidate pool task reference is non-canonical")
	})

	t.Run("candidate pool", func(t *testing.T) {
		application := bootAppMinimal(t)
		ctx := application.NewContextLegacy(true, cmtproto.Header{Height: application.LastBlockHeight() + 1})
		taskID := bytes.Repeat([]byte{0x51}, 32)
		snapshotID := bytes.Repeat([]byte{0x52}, 32)
		require.NoError(t, application.HubKeeper.CandidatePoolTaskRef.Set(ctx,
			hubtypes.NewCandidatePoolTaskRefKey(taskID, snapshotID),
			hubtypes.CandidatePoolTaskRefState{TaskId: taskID, SnapshotId: snapshotID, AcquiredHeight: 1,
				Status: hubtypes.CandidatePoolTaskRefStatus_CANDIDATE_POOL_TASK_REF_STATUS_ACQUIRED},
		))
		require.ErrorContains(t, application.EnsureCrossModuleReferences(ctx), "no matching Task assignment")
	})

	t.Run("Task assignment", func(t *testing.T) {
		application := bootAppMinimal(t)
		ctx := application.NewContextLegacy(true, cmtproto.Header{Height: application.LastBlockHeight() + 1})
		taskID := bytes.Repeat([]byte{0x55}, 32)
		taskKey := tasktypes.NewTaskKey(taskID)
		require.NoError(t, application.TaskKeeper.TaskAssignment.Set(ctx, taskKey, tasktypes.TaskAssignmentState{
			TaskId: taskID, CandidatePoolSnapshotId: bytes.Repeat([]byte{0x56}, 32),
		}))
		require.ErrorContains(t, application.EnsureCrossModuleReferences(ctx), "has no matching candidate pool reference")
	})

	// Same shape as the candidate pool case above: the key's task_id and the row's
	// task_id are the same raw Hash32 now, and nothing exercised a disagreement
	// between them before this batch.
	t.Run("BuilderSet key mismatch", func(t *testing.T) {
		application := bootAppMinimal(t)
		ctx := application.NewContextLegacy(true, cmtproto.Header{Height: application.LastBlockHeight() + 1})
		taskID := bytes.Repeat([]byte{0x61}, 32)
		require.NoError(t, application.HubKeeper.BuilderSetTaskRef.Set(ctx,
			hubtypes.NewBuilderSetTaskRefKey(bytes.Repeat([]byte{0x60}, 32), 1),
			hubtypes.BuilderSetTaskRefState{TaskId: taskID, BuilderSetVersion: 1, AcquiredHeight: 1},
		))
		require.ErrorContains(t, application.EnsureCrossModuleReferences(ctx), "BuilderSet task reference is non-canonical")
	})

	t.Run("BuilderSet", func(t *testing.T) {
		application := bootAppMinimal(t)
		ctx := application.NewContextLegacy(true, cmtproto.Header{Height: application.LastBlockHeight() + 1})
		taskID := bytes.Repeat([]byte{0x61}, 32)
		require.NoError(t, application.HubKeeper.BuilderSetTaskRef.Set(ctx,
			hubtypes.NewBuilderSetTaskRefKey(taskID, 1),
			hubtypes.BuilderSetTaskRefState{TaskId: taskID, BuilderSetVersion: 1, AcquiredHeight: 1},
		))
		require.ErrorContains(t, application.EnsureCrossModuleReferences(ctx), "no matching Task selection")
	})

	t.Run("Task BuilderSet selection", func(t *testing.T) {
		application := bootAppMinimal(t)
		ctx := application.NewContextLegacy(true, cmtproto.Header{Height: application.LastBlockHeight() + 1})
		taskID := bytes.Repeat([]byte{0x65}, 32)
		taskKey := tasktypes.NewTaskKey(taskID)
		require.NoError(t, application.TaskKeeper.TaskCore.Set(ctx, taskKey, tasktypes.TaskCoreState{
			TaskId: taskID, TaskPhase: tasktypes.TaskPhase_TASK_PHASE_WORKER_ASSIGNED,
		}))
		require.NoError(t, application.TaskKeeper.TaskBuilderSelection.Set(ctx, taskKey, tasktypes.TaskBuilderSelectionState{
			TaskId: taskID, BuilderSetId: "1", BuilderSetHash: bytes.Repeat([]byte{0x66}, 32),
		}))
		require.ErrorContains(t, application.EnsureCrossModuleReferences(ctx), "has no matching BuilderSet reference")
	})

	t.Run("parameter bucket count", func(t *testing.T) {
		application := bootAppMinimal(t)
		ctx := application.NewContextLegacy(true, cmtproto.Header{Height: application.LastBlockHeight() + 1})
		key := hubtypes.NewParameterBucketVersionKey(shared.BucketKind_BUCKET_KIND_TIMEOUT, hubtypes.DefaultParameterBucketKey, 1)
		version, err := application.HubKeeper.ParameterBucketVersion.Get(ctx, key)
		require.NoError(t, err)
		version.TaskRefCount = 1
		require.NoError(t, application.HubKeeper.ParameterBucketVersion.Set(ctx, key, version))
		require.ErrorContains(t, application.EnsureCrossModuleReferences(ctx), "Task refs=0")
	})

	t.Run("Task bucket orphan", func(t *testing.T) {
		application := bootAppMinimal(t)
		ctx := application.NewContextLegacy(true, cmtproto.Header{Height: application.LastBlockHeight() + 1})
		taskID := bytes.Repeat([]byte{0x71}, 32)
		taskKey := tasktypes.NewTaskKey(taskID)
		require.NoError(t, application.TaskKeeper.TaskBucketRef.Set(ctx,
			tasktypes.NewTaskBucketRefKey(taskKey, int32(shared.BucketKind_BUCKET_KIND_TIMEOUT), hubtypes.DefaultParameterBucketKey),
			tasktypes.TaskBucketRefState{TaskId: taskID, BucketKind: shared.BucketKind_BUCKET_KIND_TIMEOUT,
				BucketKey: hubtypes.DefaultParameterBucketKey, Version: 1, AcquiredHeight: 1},
		))
		require.ErrorContains(t, application.EnsureCrossModuleReferences(ctx), "points at a missing task")
	})

	// A bucket ref is released by keeper.releaseTaskAdmissionRefs, and the only
	// path that reaches it -- finalizeTerminalTask -- runs off a sweep that
	// requires the task to be SETTLED or FAILED first. A terminal task therefore
	// holds these rows legitimately for the whole finality delay, and rejecting
	// that phase outright halted startup on state the runtime is built to
	// produce. What must not survive is a ref whose admission refs are already
	// marked released, since that call removes the rows and sets the flag
	// together.
	t.Run("Task bucket ref across the terminal-to-final window", func(t *testing.T) {
		application := bootAppMinimal(t)
		ctx := application.NewContextLegacy(true, cmtproto.Header{Height: application.LastBlockHeight() + 1})
		taskID := bytes.Repeat([]byte{0x73}, 32)
		snapshotID := bytes.Repeat([]byte{0x74}, 32)
		taskKey := tasktypes.NewTaskKey(taskID)

		require.NoError(t, application.TaskKeeper.TaskCore.Set(ctx, taskKey, tasktypes.TaskCoreState{
			TaskId: taskID, TaskPhase: tasktypes.TaskPhase_TASK_PHASE_SETTLED,
		}))
		assignment := tasktypes.TaskAssignmentState{TaskId: taskID, CandidatePoolSnapshotId: snapshotID}
		require.NoError(t, application.TaskKeeper.TaskAssignment.Set(ctx, taskKey, assignment))
		require.NoError(t, application.HubKeeper.CandidatePoolTaskRef.Set(ctx,
			hubtypes.NewCandidatePoolTaskRefKey(taskID, snapshotID),
			hubtypes.CandidatePoolTaskRefState{TaskId: taskID, SnapshotId: snapshotID, AcquiredHeight: 1,
				Status: hubtypes.CandidatePoolTaskRefStatus_CANDIDATE_POOL_TASK_REF_STATUS_ACQUIRED},
		))
		bucketKey := tasktypes.NewTaskBucketRefKey(taskKey, int32(shared.BucketKind_BUCKET_KIND_TIMEOUT), hubtypes.DefaultParameterBucketKey)
		require.NoError(t, application.TaskKeeper.TaskBucketRef.Set(ctx, bucketKey,
			tasktypes.TaskBucketRefState{TaskId: taskID, BucketKind: shared.BucketKind_BUCKET_KIND_TIMEOUT,
				BucketKey: hubtypes.DefaultParameterBucketKey, Version: 1, AcquiredHeight: 1},
		))
		versionKey := hubtypes.NewParameterBucketVersionKey(shared.BucketKind_BUCKET_KIND_TIMEOUT, hubtypes.DefaultParameterBucketKey, 1)
		version, err := application.HubKeeper.ParameterBucketVersion.Get(ctx, versionKey)
		require.NoError(t, err)
		version.TaskRefCount = 1
		require.NoError(t, application.HubKeeper.ParameterBucketVersion.Set(ctx, versionKey, version))

		require.NoError(t, application.EnsureCrossModuleReferences(ctx),
			"a SETTLED task awaiting the finality sweep still owns its admission refs")

		// Once the release is recorded, the rows must be gone with it.
		assignment.CandidatePoolRefReleased = true
		require.NoError(t, application.TaskKeeper.TaskAssignment.Set(ctx, taskKey, assignment))
		require.NoError(t, application.HubKeeper.CandidatePoolTaskRef.Remove(ctx,
			hubtypes.NewCandidatePoolTaskRefKey(taskID, snapshotID)))
		require.ErrorContains(t, application.EnsureCrossModuleReferences(ctx),
			"outlives its released admission refs")
	})
}

func TestCrossModuleReferencesRejectOrphanSettlementAndLiability(t *testing.T) {

	t.Run("liability", func(t *testing.T) {
		application := bootAppMinimal(t)
		ctx := application.NewContextLegacy(true, cmtproto.Header{Height: application.LastBlockHeight() + 1})
		taskID := bytes.Repeat([]byte{0x41}, 32)
		operator := sdk.AccAddress(bytes.Repeat([]byte{0x42}, 20)).String()
		require.NoError(t, application.HubKeeper.TaskLiabilityReservation.Set(ctx,
			hubtypes.NewTaskLiabilityReservationKey(taskID, shared.DutyWorker, operator),
			hubtypes.TaskLiabilityReservationState{
				SchemaVersion: 1, TaskId: taskID, OperatorAddress: operator, Duty: shared.DutyWorker,
				BondVersion: 1, CapabilityVersion: 1, ReservedAmount: 1,
				Status: hubtypes.TaskLiabilityStatusReserved,
			},
		))
		require.ErrorContains(t, application.EnsureCrossModuleReferences(ctx), "references missing task")
	})
}
