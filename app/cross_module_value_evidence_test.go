package app

import (
	"bytes"
	"testing"

	"cosmossdk.io/collections"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func TestValueReceiptReferencesKeepFirstAcceptanceAcrossPositionOrderAndCleanup(t *testing.T) {
	application := bootAppMinimal(t)
	ctx := application.NewContextLegacy(true, cmtproto.Header{Height: 10})
	hash := func(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }
	raw := bytes.Repeat([]byte{4}, 20)
	operator := sdk.AccAddress(raw).String()
	fault := hubtypes.RoleFaultState{FaultId: hash(1), TaskId: hash(2), OperatorAddress: operator, Duty: shared.DutyVerifier, FaultClass: shared.FaultKind_FAULT_KIND_VERIFIER_VALUE_MISMATCH, ClassificationSource: shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_OBJECTIVE_EVIDENCE, EvidenceDigest: hash(3), RecordedHeight: 5, Status: hubtypes.RoleFaultStatus_ROLE_FAULT_STATUS_CONFIRMED}
	require.NoError(t, application.HubKeeper.WriteRoleFaultValue(ctx, fault.FaultId, fault))
	key := tasktypes.NewValueEvidenceReceiptKey(fault.TaskId, 1, raw, 9)
	row, err := application.TaskKeeper.ValueEvidenceReceipt.Get(ctx, key)
	require.ErrorIs(t, err, collections.ErrNotFound)
	row.SchemaVersion, row.TaskId, row.VerifyRound, row.TargetOperatorAddress, row.Position = 1, fault.TaskId, 1, raw, 9
	row.EvidenceKind, row.FaultedDuty, row.EvidenceDigest, row.FaultId, row.AcceptedHeight = int32(tasktypes.VerifierValueEvidenceKindV1_VERIFIER_VALUE_EVIDENCE_KIND_V1_VERIFIER_VALUE_MISMATCH), int32(shared.DutyVerifier), fault.EvidenceDigest, fault.FaultId, 5
	require.NoError(t, application.TaskKeeper.ValueEvidenceReceipt.Set(ctx, key, row))
	secondKey := tasktypes.NewValueEvidenceReceiptKey(fault.TaskId, 1, raw, 0)
	row.Position, row.EvidenceDigest, row.AcceptedHeight = 0, hash(5), 6
	require.NoError(t, application.TaskKeeper.ValueEvidenceReceipt.Set(ctx, secondKey, row))
	require.NoError(t, application.ensureValueEvidenceFaultReferences(ctx))
	fault.OperatorAddress = sdk.AccAddress(bytes.Repeat([]byte{7}, 20)).String()
	require.NoError(t, application.HubKeeper.WriteRoleFaultValue(ctx, fault.FaultId, fault))
	require.ErrorContains(t, application.ensureValueEvidenceFaultReferences(ctx), "attribution")
	fault.OperatorAddress = operator
	require.NoError(t, application.HubKeeper.WriteRoleFaultValue(ctx, fault.FaultId, fault))
	require.NoError(t, application.TaskKeeper.ValueEvidenceReceipt.Remove(ctx, key))
	require.ErrorContains(t, application.ensureValueEvidenceFaultReferences(ctx), "first receipt")
	require.NoError(t, application.TaskKeeper.TaskCleanupCursor.Set(ctx, fault.TaskId, tasktypes.TaskCleanupCursorState{TaskId: fault.TaskId}))
	require.NoError(t, application.ensureValueEvidenceFaultReferences(ctx))
}

func TestRoundEffectAndHubCustodyRequireBothReferences(t *testing.T) {
	application := bootAppMinimal(t)
	ctx := application.NewContextLegacy(true, cmtproto.Header{Height: 10})
	hash := func(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }
	taskID, roundID := hash(11), hash(12)
	operator := sdk.AccAddress(bytes.Repeat([]byte{13}, 20)).String()
	round := tasktypes.VerificationRoundState{TaskId: taskID, VerifyRound: 2, RoundId: roundID}
	require.NoError(t, application.TaskKeeper.WriteVerificationRound(ctx, tasktypes.NewVerifyRoundKey(taskID, 2), round))
	effect := shared.RoundEconomicEffectV1{EffectIndex: 1, EffectKind: shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_SERVICE_SLASH, XSourceAddress: &shared.RoundEconomicEffectV1_SourceAddress{SourceAddress: operator}, RequestedAmount: shared.NewAmount(10), AppliedAmount: shared.NewAmount(10), UnfilledAmount: shared.NewAmount(0), Status: shared.RoundEconomicEffectStatusV1_ROUND_ECONOMIC_EFFECT_STATUS_V1_APPLIED}
	effectKey := tasktypes.NewRoundEconomicEffectKey(taskID, 2, 1)
	require.NoError(t, application.TaskKeeper.RoundEconomicEffect.Set(ctx, effectKey, tasktypes.RoundEconomicEffectState{TaskId: taskID, VerifyRound: 2, Effect: effect}))
	require.ErrorContains(t, application.ensureRoundSlashReferences(ctx), "no Hub slash summary")
	zero := shared.NewAmount(0)
	summary := hubtypes.SlashSummaryState{SlashSummaryId: roundID, SourceKind: hubtypes.SlashSourceKind_SLASH_SOURCE_KIND_CHALLENGE_EFFECT, SourceId: roundID, EffectIndex: 1, OperatorAddress: operator, Duty: shared.DutyWorker, XTaskId: &hubtypes.SlashSummaryState_TaskId{TaskId: taskID}, RequestedAmount: shared.NewAmount(10), PendingTaskEarningsDebit: zero, ActiveBondDebit: shared.NewAmount(10), UnbondingDebit: zero, ClaimableEarningsDebit: zero, AppliedAmount: shared.NewAmount(10), UnfilledAmount: zero, Destination: hubtypes.SlashDestination_SLASH_DESTINATION_CHALLENGE_EFFECT_POOL, AppliedHeight: 10, BondVersion: 1}
	key := hubtypes.NewSlashSummaryKey(summary.SourceKind, roundID, 1)
	require.NoError(t, application.HubKeeper.WriteSlashSummaryValue(ctx, key, summary))
	require.NoError(t, application.ensureRoundSlashReferences(ctx))
	effect.AppliedAmount = shared.NewAmount(9)
	require.NoError(t, application.TaskKeeper.RoundEconomicEffect.Set(ctx, effectKey, tasktypes.RoundEconomicEffectState{TaskId: taskID, VerifyRound: 2, Effect: effect}))
	require.ErrorContains(t, application.ensureRoundSlashReferences(ctx), "custody")
	require.NoError(t, application.TaskKeeper.RoundEconomicEffect.Remove(ctx, effectKey))
	require.ErrorContains(t, application.ensureRoundSlashReferences(ctx), "no applied Task effect")
}

func TestLiveValueFaultRequiresReceiptUntilCleanupStarts(t *testing.T) {
	application := bootAppMinimal(t)
	ctx := application.NewContextLegacy(true, cmtproto.Header{Height: 10})
	id, task := bytes.Repeat([]byte{21}, 32), bytes.Repeat([]byte{22}, 32)
	fault := hubtypes.RoleFaultState{FaultId: id, TaskId: task, OperatorAddress: sdk.AccAddress(bytes.Repeat([]byte{23}, 20)).String(), Duty: shared.DutyWorker, FaultClass: shared.FaultKind_FAULT_KIND_WORKER_VALUES_INVALID, ClassificationSource: shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_OBJECTIVE_EVIDENCE, EvidenceDigest: bytes.Repeat([]byte{24}, 32), RecordedHeight: 5, Status: hubtypes.RoleFaultStatus_ROLE_FAULT_STATUS_CONFIRMED}
	require.NoError(t, application.HubKeeper.WriteRoleFaultValue(ctx, id, fault))
	require.NoError(t, application.TaskKeeper.TaskCore.Set(ctx, task, tasktypes.TaskCoreState{TaskId: task}))
	require.ErrorContains(t, application.ensureValueEvidenceFaultReferences(ctx), "live value fault has no matching receipt")
	require.NoError(t, application.TaskKeeper.TaskCleanupCursor.Set(ctx, task, tasktypes.TaskCleanupCursorState{TaskId: task}))
	require.NoError(t, application.ensureValueEvidenceFaultReferences(ctx))
	require.NoError(t, application.TaskKeeper.TaskCleanupCursor.Remove(ctx, task))
	require.NoError(t, application.TaskKeeper.TaskCore.Remove(ctx, task))
	require.NoError(t, application.ensureValueEvidenceFaultReferences(ctx), "archived faults outlive compacted receipts")
}
