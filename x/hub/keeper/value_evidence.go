package keeper

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// RecordVerifierValueEvidence records accepted attribution without spending
// collateral. Task final settlement owns the single merged economic effect.
func (k Keeper) RecordVerifierValueEvidence(ctx context.Context, fact shared.VerifierValueEvidenceFactV1) (types.RoleFaultState, error) {
	if err := validateValueEvidenceFact(fact); err != nil {
		return types.RoleFaultState{}, err
	}
	operator, canonical, err := k.requireCanonicalAddress("value evidence target", fact.TargetOperatorAddress)
	if err != nil {
		return types.RoleFaultState{}, err
	}
	id, err := roleFaultID(sdk.UnwrapSDKContext(ctx).ChainID(), fact.TaskId, operator, fact.FaultedDuty, fact.FaultClass, fact.EvidenceDigest)
	if err != nil {
		return types.RoleFaultState{}, err
	}
	if existing, err := k.requireValueEvidenceFault(ctx, fact.TaskId, canonical, fact.FaultedDuty, fact.EvidenceDigest, id); err == nil {
		return existing, nil
	} else if !errors.Is(err, collections.ErrNotFound) {
		return types.RoleFaultState{}, err
	}
	row := types.RoleFaultState{FaultId: id, TaskId: bytes.Clone(fact.TaskId), OperatorAddress: canonical, Duty: fact.FaultedDuty, FaultClass: fact.FaultClass, ClassificationSource: shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_OBJECTIVE_EVIDENCE, EvidenceDigest: bytes.Clone(fact.EvidenceDigest), RecordedHeight: sdkWrappedContextHeight(ctx), Status: types.RoleFaultStatus_ROLE_FAULT_STATUS_CONFIRMED}
	if err = row.Validate(); err != nil {
		return types.RoleFaultState{}, err
	}
	_, err = k.recordRoleFaultState(ctx, row, id, hex.EncodeToString(fact.SessionId))
	return row, err
}

func validateValueEvidenceFact(fact shared.VerifierValueEvidenceFactV1) error {
	if len(fact.SessionId) != 32 || len(fact.TaskId) != 32 || len(fact.EvidenceDigest) != 32 || fact.VerifyRound == 0 || fact.VerifyRound > 2 || fact.FrozenSlashBps > 10_000 || (fact.FaultedDuty != shared.DutyVerifier || fact.FaultClass != shared.FaultKind_FAULT_KIND_VERIFIER_VALUE_MISMATCH) && (fact.FaultedDuty != shared.DutyWorker || fact.FaultClass != shared.FaultKind_FAULT_KIND_WORKER_VALUES_INVALID) {
		return fmt.Errorf("invalid verifier value evidence fact")
	}
	return nil
}

func (k Keeper) requireValueEvidenceFault(ctx context.Context, taskID []byte, operator string, duty shared.Duty, digest, faultID []byte) (types.RoleFaultState, error) {
	row, err := k.ReadRoleFaultValue(ctx, faultID)
	if err != nil {
		return row, err
	}
	if !bytes.Equal(row.TaskId, taskID) || row.OperatorAddress != operator || row.Duty != duty || !bytes.Equal(row.EvidenceDigest, digest) || row.ClassificationSource != shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_OBJECTIVE_EVIDENCE || row.FaultClass != shared.FaultKind_FAULT_KIND_VERIFIER_VALUE_MISMATCH && row.FaultClass != shared.FaultKind_FAULT_KIND_WORKER_VALUES_INVALID {
		return row, fmt.Errorf("value evidence fault attribution mismatch")
	}
	return row, nil
}

func (k Keeper) finishValueEvidenceFault(ctx context.Context, row types.RoleFaultState, summaryID []byte, height uint64) (types.RoleFaultState, error) {
	if len(summaryID) != 0 {
		row.XSlashSummaryId = &types.RoleFaultState_SlashSummaryId{SlashSummaryId: bytes.Clone(summaryID)}
	}
	role, err := roleFromDuty(row.Duty)
	if err != nil {
		return row, err
	}
	before, err := k.GetJail(ctx, row.OperatorAddress, role)
	if err != nil {
		return row, err
	}
	after, _, err := k.incJail(ctx, row.OperatorAddress, role, height, types.EventLifecycleReason_EVENT_LIFECYCLE_REASON_OBJECTIVE_FAULT)
	if err != nil {
		return row, err
	}
	if after.JailCount > before.JailCount {
		row.JailDelta = 1
	}
	return row, k.WriteRoleFaultValue(ctx, row.FaultId, row)
}

// ApplyVerifierValueEvidenceFault spends a previously recorded fault at final
// settlement when no challenge-effect plan owns the merged consequence.
func (k Keeper) ApplyVerifierValueEvidenceFault(ctx context.Context, fact shared.VerifierValueEvidenceFactV1, faultID []byte) (types.RoleFaultState, error) {
	if err := validateValueEvidenceFact(fact); err != nil {
		return types.RoleFaultState{}, err
	}
	cacheCtx, write := sdk.UnwrapSDKContext(ctx).CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	row, err := k.requireValueEvidenceFault(cache, fact.TaskId, fact.TargetOperatorAddress, fact.FaultedDuty, fact.EvidenceDigest, faultID)
	if err != nil {
		return row, err
	}
	if len(row.GetSlashSummaryId()) != 0 {
		return row, nil
	}
	reservation, err := k.ReadTaskLiabilityValue(cache, types.NewTaskLiabilityReservationKey(fact.TaskId, fact.FaultedDuty, fact.TargetOperatorAddress))
	if err != nil {
		return row, err
	}
	if reservation.Status == types.TaskLiabilityStatusSlashed {
		return row, nil
	}
	if reservation.Status != types.TaskLiabilityStatusReserved || fact.FrozenSlashBps > 10_000 {
		return row, fmt.Errorf("value evidence liability is not reserved")
	}
	requested := types.SlashByFraction(reservation.ReservedAmount, uint64(fact.FrozenSlashBps), types.ObjectiveForgerySlashDenom)
	if _, err = k.closeTaskLiabilityReservationInCache(cache, fact.TaskId, fact.TargetOperatorAddress, fact.FaultedDuty, types.TaskLiabilityStatusSlashed, requested, sdkWrappedContextHeight(cache), newRoleFaultSlashMetadata(row)); err != nil {
		return row, err
	}
	var summary []byte
	if requested != 0 {
		summary = row.FaultId
	}
	row, err = k.finishValueEvidenceFault(cache, row, summary, sdkWrappedContextHeight(cache))
	if err != nil {
		return row, err
	}
	write()
	return row, nil
}
