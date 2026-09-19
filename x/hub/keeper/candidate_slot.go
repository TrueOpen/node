package keeper

import (
	"context"
	"errors"
	"fmt"
	"math"

	"cosmossdk.io/collections"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (k Keeper) syncCandidateSlotMembership(ctx context.Context, operatorAddress string, height uint64) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	epoch := epochForHeight(height, normalizedEpochLengthBlocks(params))
	return k.syncCandidateSlotMembershipForEpoch(ctx, operatorAddress, height, epoch)
}

func (k Keeper) syncCandidateSlotMembershipForEpoch(ctx context.Context, operatorAddress string, height, eligibilityEpoch uint64) error {
	operatorBytes, operatorAddress, err := k.requireCanonicalAddress("operator_address", operatorAddress)
	if err != nil {
		return err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	eligible, err := k.isGlobalCandidateMember(ctx, operatorAddress, eligibilityEpoch)
	if err != nil {
		return err
	}
	reverse, reverseErr := k.OperatorCandidateSlot.Get(ctx, operatorAddress)
	if reverseErr == nil {
		current, getErr := k.CandidateSlotCurrent.Get(ctx, reverse.Slot)
		if getErr != nil {
			return getErr
		}
		if current.OperatorAddress != operatorAddress || current.SlotVersion != reverse.SlotVersion || current.Slot != reverse.Slot {
			return fmt.Errorf("operator candidate slot reverse index mismatch")
		}
		if eligible {
			if current.Status == candidateSlotAllocated {
				return nil
			}
			if current.Status != candidateSlotRetiring {
				return fmt.Errorf("operator reverse index points to a FREE slot")
			}
			current.Status = candidateSlotAllocated
			current.RetiredHeight = 0
			if err := k.CandidateSlotCurrent.Set(ctx, current.Slot, current); err != nil {
				return err
			}
			return k.noteCandidateMembershipChange(ctx, current.Slot, height)
		}
		if current.Status == candidateSlotRetiring {
			return k.tryReleaseCandidateSlot(ctx, current, height)
		}
		if current.Status != candidateSlotAllocated {
			return fmt.Errorf("allocated operator has invalid candidate slot status")
		}
		current.Status = candidateSlotRetiring
		current.RetiredHeight = height
		if err := k.CandidateSlotCurrent.Set(ctx, current.Slot, current); err != nil {
			return err
		}
		if err := k.noteCandidateMembershipChange(ctx, current.Slot, height); err != nil {
			return err
		}
		return k.tryReleaseCandidateSlot(ctx, current, height)
	}
	if !errors.Is(reverseErr, collections.ErrNotFound) {
		return reverseErr
	}
	if !eligible {
		return nil
	}
	return k.allocateCandidateSlot(ctx, operatorAddress, operatorBytes, eligibilityEpoch, height, params.CandidatePool.CandidateSlotHardCapacity)
}

// retryCandidateSlotRelease re-runs the §3.3 release predicate for whatever slot
// the operator still holds, without touching membership or bumping
// candidate_source_revision.
//
// §3.3 line 225 names four clearing points that must each re-run the *same*
// release judgment inside the same transaction: active_task_refs reaching zero,
// the operator's last ActiveLiability turning terminal, a pending
// fault/slash/unbonding hold being lifted, and §3.4's snapshot_ref_count
// reaching zero. Points 1 and 4 live in ReleaseCandidateSlotTaskRef and
// removeCandidateSnapshotMember. Points 2 and 3 call this helper: those paths do
// NOT change global membership (Ruling 29), so they must not go through
// syncCandidateSlotMembership — but a RETIRING slot may only have been blocked
// by the condition they just cleared, and "just write that the counter reached zero
// and wait for the next exit request or a standalone full-table scan" is exactly
// the forbidden shape.
func (k Keeper) retryCandidateSlotRelease(ctx context.Context, operatorAddress string, height uint64) error {
	_, operatorAddress, err := k.requireCanonicalAddress("operator_address", operatorAddress)
	if err != nil {
		return err
	}
	reverse, err := k.OperatorCandidateSlot.Get(ctx, operatorAddress)
	if errors.Is(err, collections.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	current, err := k.CandidateSlotCurrent.Get(ctx, reverse.Slot)
	if err != nil {
		return err
	}
	if current.Slot != reverse.Slot || current.SlotVersion != reverse.SlotVersion || current.OperatorAddress != operatorAddress {
		return fmt.Errorf("operator candidate slot reverse index mismatch")
	}
	if current.Status != candidateSlotRetiring {
		return nil
	}
	return k.tryReleaseCandidateSlot(ctx, current, height)
}

// syncCandidateSlotMembershipOnBondExit is the Ruling 29 conditional keep for the
// two ServiceBond exit paths. A partial unstake or a top-up leaves all five §3.3
// predicate items unchanged, so it must not touch the slot or bump
// candidate_source_revision (§3.4 line 231). Only entering or completing full
// exit — the fifth predicate item — may.
func (k Keeper) syncCandidateSlotMembershipOnBondExit(ctx context.Context, operatorAddress string, bond types.ServiceBondState, height uint64) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	epoch := epochForHeight(height, normalizedEpochLengthBlocks(params))
	if candidateBondChangeRequiresMembershipSync(bond, epoch) {
		return k.syncCandidateSlotMembership(ctx, operatorAddress, height)
	}
	return nil
}

func candidateBondChangeRequiresMembershipSync(bond types.ServiceBondState, epoch uint64) bool {
	switch bond.Status {
	case types.ServiceBondStatusUnbonding, types.ServiceBondStatusExited, types.ServiceBondStatusTombstoned:
		return true
	}
	return types.EffectiveActiveBond(bond, epoch) == 0
}

func (k Keeper) isGlobalCandidateMember(ctx context.Context, operatorAddress string, currentEpoch uint64) (bool, error) {
	if _, err := k.GetCortexNodeState(ctx, operatorAddress); err != nil {
		return false, nil
	}
	if _, err := k.GetCurrentServiceKey(ctx, shared.ParticipantTypeCortexNode, operatorAddress); err != nil {
		return false, nil
	}
	bond, err := k.GetServiceBondState(ctx, operatorAddress)
	if err != nil {
		return false, nil
	}
	switch bond.Status {
	case types.ServiceBondStatusRegistered, types.ServiceBondStatusActive, types.ServiceBondStatusJailed:
		return types.EffectiveActiveBond(bond, currentEpoch) > 0, nil
	case types.ServiceBondStatusUnbonding, types.ServiceBondStatusExited, types.ServiceBondStatusTombstoned:
		return false, nil
	default:
		return false, fmt.Errorf("operator has invalid service bond status")
	}
}

func (k Keeper) allocateCandidateSlot(ctx context.Context, operatorAddress string, operatorBytes []byte, epoch, height uint64, capacity uint32) error {
	for candidate := uint64(0); candidate < uint64(capacity); candidate++ {
		slot := uint32(candidate)
		current, err := k.CandidateSlotCurrent.Get(ctx, slot)
		if errors.Is(err, collections.ErrNotFound) {
			return k.bindCandidateSlot(ctx, slot, 1, operatorAddress, operatorBytes, epoch, height)
		}
		if err != nil {
			return err
		}
		if current.Slot != slot {
			return fmt.Errorf("candidate slot state does not match key")
		}
		if current.Status != candidateSlotFree || current.SlotVersion == math.MaxUint64 {
			continue
		}
		return k.bindCandidateSlot(ctx, slot, current.SlotVersion+1, operatorAddress, operatorBytes, epoch, height)
	}
	return k.markCandidateCapacityFailure(ctx, epoch, height)
}

func (k Keeper) bindCandidateSlot(ctx context.Context, slot uint32, slotVersion uint64, operatorAddress string, operatorBytes []byte, epoch, height uint64) error {
	bindingHash, err := types.CandidateSlotBindingHash(slot, slotVersion, operatorBytes, epoch)
	if err != nil {
		return err
	}
	bindingKey := types.NewCandidateSlotBindingKey(slot, slotVersion)
	if has, err := k.CandidateSlotBinding.Has(ctx, bindingKey); err != nil {
		return err
	} else if has {
		return fmt.Errorf("candidate slot binding already exists")
	}
	current := types.CandidateSlotCurrentState{
		SchemaVersion: 1, Slot: slot, SlotVersion: slotVersion, OperatorAddress: operatorAddress,
		Status: candidateSlotAllocated, AllocatedEpoch: epoch,
	}
	binding := types.CandidateSlotBindingState{
		Slot: slot, SlotVersion: slotVersion, OperatorAddress: operatorAddress,
		AllocatedEpoch: epoch, BindingHash: bindingHash,
	}
	reverse := types.OperatorCandidateSlotState{OperatorAddress: operatorAddress, Slot: slot, SlotVersion: slotVersion}
	if err := k.CandidateSlotBinding.Set(ctx, bindingKey, binding); err != nil {
		return err
	}
	if err := k.CandidateSlotCurrent.Set(ctx, slot, current); err != nil {
		return err
	}
	if err := k.OperatorCandidateSlot.Set(ctx, operatorAddress, reverse); err != nil {
		return err
	}
	return k.noteCandidateMembershipChange(ctx, slot, height)
}

func (k Keeper) noteCandidateMembershipChange(ctx context.Context, slot uint32, height uint64) error {
	status, err := k.CandidatePoolBuildStatus.Get(ctx)
	if errors.Is(err, collections.ErrNotFound) {
		status = types.CandidatePoolBuildStatusState{Status: types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_IDLE}
	} else if err != nil {
		return err
	}
	if status.SourceRevision == math.MaxUint64 {
		return fmt.Errorf("candidate source revision overflow")
	}
	status.SourceRevision++
	status.UpdatedHeight = height
	if status.Status == types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_FAILED_CAPACITY {
		status.Status = types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_IDLE
		status.FailureReason = types.CandidatePoolBuildFailureReason_CANDIDATE_POOL_BUILD_FAILURE_REASON_UNSPECIFIED
	}
	if status.Status == types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_BUILDING {
		cursor, getErr := k.CandidatePoolBuildCursor.Get(ctx, status.TargetEpoch)
		if getErr != nil {
			return getErr
		}
		params, getErr := k.Params.Get(ctx)
		if getErr != nil {
			return getErr
		}
		segmentBits := uint64(params.CandidatePool.CandidateBitmapSegmentBytes) * 8
		segment := uint32(uint64(slot) / segmentBits)
		setCandidateDirtySegment(cursor.DirtySegments, segment)
		if segment <= cursor.NextSegment {
			cursor.NextSegment = segment
			cursor.NextCleanupKey = nil
		}
		if err := k.CandidatePoolBuildCursor.Set(ctx, cursor.TargetEpoch, cursor); err != nil {
			return err
		}
	}
	return k.CandidatePoolBuildStatus.Set(ctx, status)
}

func (k Keeper) markCandidateCapacityFailure(ctx context.Context, epoch, height uint64) error {
	status, err := k.CandidatePoolBuildStatus.Get(ctx)
	if errors.Is(err, collections.ErrNotFound) {
		status = types.CandidatePoolBuildStatusState{}
	} else if err != nil {
		return err
	}
	if epoch == math.MaxUint64 {
		return fmt.Errorf("candidate epoch overflow")
	}
	status.TargetEpoch = epoch + 1
	status.Status = types.CandidatePoolBuildStatus_CANDIDATE_POOL_BUILD_STATUS_FAILED_CAPACITY
	status.UpdatedHeight = height
	status.FailureReason = types.CandidatePoolBuildFailureReason_CANDIDATE_POOL_BUILD_FAILURE_REASON_CAPACITY_EXHAUSTED
	if cursor, getErr := k.CandidatePoolBuildCursor.Get(ctx, status.TargetEpoch); getErr == nil {
		cursor.Status = types.CandidatePoolBuildCursorStatus_CANDIDATE_POOL_BUILD_CURSOR_STATUS_CLEANING_FAILED_DRAFT
		cursor.FailureReason = status.FailureReason
		if err := k.CandidatePoolBuildCursor.Set(ctx, cursor.TargetEpoch, cursor); err != nil {
			return err
		}
	} else if !errors.Is(getErr, collections.ErrNotFound) {
		return getErr
	}
	return k.CandidatePoolBuildStatus.Set(ctx, status)
}

func (k Keeper) tryReleaseCandidateSlot(ctx context.Context, current types.CandidateSlotCurrentState, height uint64) error {
	if current.Status != candidateSlotRetiring || current.ActiveTaskRefs != 0 {
		return nil
	}
	bindingKey := types.NewCandidateSlotBindingKey(current.Slot, current.SlotVersion)
	binding, err := k.CandidateSlotBinding.Get(ctx, bindingKey)
	if err != nil {
		return err
	}
	if binding.SnapshotRefCount != 0 || binding.ReleasedHeight != 0 {
		return nil
	}
	active, err := k.operatorHasActiveLiability(ctx, current.OperatorAddress)
	if err != nil || active {
		return err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	currentEpoch := epochForHeight(height, normalizedEpochLengthBlocks(params))
	pruneEpoch := currentEpoch + uint64(params.CandidatePool.CandidateSlotBindingRetentionEpochs)
	if pruneEpoch < currentEpoch {
		return fmt.Errorf("candidate binding prune epoch overflow")
	}
	binding.ReleasedHeight = height
	if err := k.CandidateSlotBinding.Set(ctx, bindingKey, binding); err != nil {
		return err
	}
	if err := k.CandidateSlotBindingPruneIndex.Set(ctx, types.NewCandidateSlotBindingPruneIndexKey(pruneEpoch, current.Slot, current.SlotVersion)); err != nil {
		return err
	}
	if err := k.OperatorCandidateSlot.Remove(ctx, current.OperatorAddress); err != nil {
		return err
	}
	current.OperatorAddress = ""
	current.Status = candidateSlotFree
	if err := k.CandidateSlotCurrent.Set(ctx, current.Slot, current); err != nil {
		return err
	}
	return k.noteCandidateMembershipChange(ctx, current.Slot, height)
}

func (k Keeper) operatorHasActiveLiability(ctx context.Context, operatorAddress string) (bool, error) {
	iter, err := k.ActiveLiabilityByOperatorIndex.Iterate(ctx, collections.NewPrefixedTripleRange[string, shared.Hash32Key, int32](operatorAddress))
	if err != nil {
		return false, err
	}
	defer iter.Close()
	return iter.Valid(), nil
}
