package keeper

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

const (
	maxWorkerLiabilitiesPerTaskV1 = 1
	// A Task retains round-1 liabilities through finality and may reserve a
	// disjoint five-member challenge set at the same time.
	maxVerifierLiabilitiesPerTaskV1 = 8
	maxTaskLiabilitiesPerTaskV1     = maxWorkerLiabilitiesPerTaskV1 + maxVerifierLiabilitiesPerTaskV1
)

func taskLiabilityEconomics(params types.ServiceParamsV1) (slashBps uint32, minLiability uint64, orderCoverageBps uint32, err error) {
	if params.ObjectiveForgerySlashBps == 0 || uint64(params.ObjectiveForgerySlashBps) > types.ObjectiveForgerySlashDenom {
		return 0, 0, 0, fmt.Errorf("objective_forgery_slash_bps is invalid")
	}
	minLiability, err = shared.ParseAmount(params.MinTaskLiability)
	if err != nil || minLiability == 0 {
		return 0, 0, 0, fmt.Errorf("min_task_liability is invalid")
	}
	if params.TaskLiabilityOrderCoverageBps == 0 || params.TaskLiabilityOrderCoverageBps > uint32(types.ObjectiveForgerySlashDenom) {
		return 0, 0, 0, fmt.Errorf("task_liability_order_coverage_bps is invalid")
	}
	return params.ObjectiveForgerySlashBps, minLiability, params.TaskLiabilityOrderCoverageBps, nil
}

// parseTaskLiabilityID is the single text boundary for the three task-liability
// keyspaces. The HubKeeper methods that reach them (ReleaseTaskLiabilities,
// DeleteOneClosedTaskLiability) still take task_id as lower-hex, like their
// three siblings on that interface, so the decode happens exactly once here and
// only raw bytes travel past it. It used to return the canonical hex as well,
// because that text *was* the store key; nothing needs it now.
func parseTaskLiabilityID(taskID string) (shared.Hash32Key, error) {
	canonical, err := types.ValidateSHA256Hex("task_id", taskID)
	if err != nil {
		return nil, err
	}
	raw, err := hex.DecodeString(canonical)
	if err != nil {
		return nil, fmt.Errorf("decode task_id: %w", err)
	}
	return raw, nil
}

func parseTaskLiabilityDuty(duty string) (shared.Duty, error) {
	switch duty {
	case types.ServiceBondRoleWorker:
		return shared.DutyWorker, nil
	case types.ServiceBondRoleVerifier:
		return shared.DutyVerifier, nil
	default:
		return shared.Duty_DUTY_UNSPECIFIED, fmt.Errorf("duty must be WORKER or VERIFIER")
	}
}

// ReserveTaskLiabilityFromFrozenFact is the frozen assignment preflight. It
// revalidates a Task-committed candidate fact against current hard-invalid Hub
// state and writes its exact CandidatePool slot ownership atomically.
func (k Keeper) ReserveTaskLiabilityFromFrozenFact(
	ctx context.Context,
	req types.FrozenFactLiabilityRequest,
) (types.TaskLiabilityReservationState, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	reservation, err := k.reserveTaskLiabilityFromFrozenFact(sdk.WrapSDKContext(cacheCtx), req)
	if err != nil {
		return types.TaskLiabilityReservationState{}, err
	}
	write()
	return reservation, nil
}

func (k Keeper) reserveTaskLiabilityFromFrozenFact(
	ctx context.Context,
	req types.FrozenFactLiabilityRequest,
) (types.TaskLiabilityReservationState, error) {
	taskID, err := parseFrozenLiabilityRequest(req)
	if err != nil {
		return types.TaskLiabilityReservationState{}, err
	}
	snapshotKey, err := candidateHashKey(req.CandidatePoolSnapshotID)
	if err != nil {
		return types.TaskLiabilityReservationState{}, fmt.Errorf("candidate_pool_snapshot_id: %w", err)
	}
	_, operator, err := k.requireCanonicalAddress("operator_address", req.OperatorAddress)
	if err != nil {
		return types.TaskLiabilityReservationState{}, err
	}

	reservationKey := types.NewTaskLiabilityReservationKey(taskID, req.Duty, operator)
	existing, err := k.ReadTaskLiabilityValue(ctx, reservationKey)
	if err == nil {
		if existing.Status != types.TaskLiabilityStatusReserved ||
			!bytes.Equal(existing.TaskId, taskID) || existing.OperatorAddress != operator || existing.Duty != req.Duty ||
			existing.BondVersion != req.BondVersionSnapshot || existing.CapabilityVersion != req.CapabilityVersionSnapshot ||
			existing.ReservedAmount != req.RequiredTaskLiability ||
			!bytes.Equal(existing.CandidatePoolSnapshotId, req.CandidatePoolSnapshotID) ||
			existing.Slot != req.Slot || existing.SlotVersion != req.SlotVersion {
			return types.TaskLiabilityReservationState{}, fmt.Errorf("task liability already exists with incompatible frozen facts")
		}
		if err := existing.Validate(); err != nil {
			return types.TaskLiabilityReservationState{}, err
		}
		if err := k.requireActiveTaskLiabilityIndexes(ctx, taskID, existing); err != nil {
			return types.TaskLiabilityReservationState{}, err
		}
		if err := k.requireTaskLiabilitySlotOwnership(ctx, taskID, existing); err != nil {
			return types.TaskLiabilityReservationState{}, err
		}
		return existing, nil
	}
	if !errors.Is(err, collections.ErrNotFound) {
		return types.TaskLiabilityReservationState{}, err
	}

	poolRef, err := k.CandidatePoolTaskRef.Get(ctx, types.NewCandidatePoolTaskRefKey(taskID, snapshotKey))
	if err != nil || poolRef.Status != types.CandidatePoolTaskRefStatus_CANDIDATE_POOL_TASK_REF_STATUS_ACQUIRED ||
		!bytes.Equal(poolRef.TaskId, taskID) || !bytes.Equal(poolRef.SnapshotId, req.CandidatePoolSnapshotID) {
		return types.TaskLiabilityReservationState{}, fmt.Errorf("candidate pool task reference is unavailable")
	}
	member, binding, err := k.ResolveCandidatePoolMember(ctx, req.CandidatePoolSnapshotID, req.Slot)
	if err != nil || member.Slot != req.Slot || member.SlotVersion != req.SlotVersion || member.OperatorAddress != operator ||
		binding.Slot != req.Slot || binding.SlotVersion != req.SlotVersion || binding.OperatorAddress != operator ||
		!bytes.Equal(member.BindingHash, binding.BindingHash) {
		return types.TaskLiabilityReservationState{}, fmt.Errorf("frozen candidate member binding is unavailable")
	}
	current, err := k.ReadCandidateSlotCurrent(ctx, req.Slot)
	if err != nil || current.Status != candidateSlotAllocated || current.SlotVersion != req.SlotVersion || current.OperatorAddress != operator {
		return types.TaskLiabilityReservationState{}, fmt.Errorf("candidate slot is no longer allocated to the frozen operator")
	}
	if current.ActiveTaskRefs == ^uint32(0) {
		return types.TaskLiabilityReservationState{}, fmt.Errorf("candidate slot active_task_refs overflow")
	}

	oppositeDuty := shared.DutyWorker
	if req.Duty == shared.DutyWorker {
		oppositeDuty = shared.DutyVerifier
	}
	if _, err := k.ReadTaskLiabilityValue(ctx, types.NewTaskLiabilityReservationKey(taskID, oppositeDuty, operator)); err == nil {
		return types.TaskLiabilityReservationState{}, fmt.Errorf("same cortex node cannot reserve both WORKER and VERIFIER liability for one task")
	} else if !errors.Is(err, collections.ErrNotFound) {
		return types.TaskLiabilityReservationState{}, err
	}
	if err := k.ensureTaskLiabilityCapacity(ctx, taskID, req.Duty); err != nil {
		return types.TaskLiabilityReservationState{}, err
	}

	node, err := k.GetCortexNodeState(ctx, operator)
	if err != nil {
		return types.TaskLiabilityReservationState{}, err
	}
	if ejected, err := k.IsJailEjected(ctx, operator); err != nil {
		return types.TaskLiabilityReservationState{}, err
	} else if ejected {
		return types.TaskLiabilityReservationState{}, fmt.Errorf("cortex node has reached the jail ejection threshold")
	}
	if tombstoned, err := k.IsTombstoned(ctx, operator); err != nil {
		return types.TaskLiabilityReservationState{}, err
	} else if tombstoned {
		return types.TaskLiabilityReservationState{}, fmt.Errorf("cortex node is tombstoned")
	}
	if _, err := k.GetCurrentServiceKey(ctx, shared.ParticipantTypeCortexNode, operator); err != nil {
		return types.TaskLiabilityReservationState{}, err
	}

	bond, err := k.GetServiceBondState(ctx, operator)
	if err != nil {
		return types.TaskLiabilityReservationState{}, err
	}
	epochLength, err := k.epochLengthBlocks(ctx)
	if err != nil {
		return types.TaskLiabilityReservationState{}, err
	}
	currentEpoch := epochForHeight(req.Height, epochLength)
	effectiveBond := types.EffectiveActiveBond(bond, currentEpoch)
	availableBond, err := types.AvailableBond(bond, currentEpoch)
	if err != nil {
		return types.TaskLiabilityReservationState{}, err
	}
	// JAILED reserves like a live bond: a jailed candidate the Task side admitted
	// at a reduced jail factor still has to take the duty, and the RELEASED branch
	// below is the only caller that advances the jail-clear counter. Tombstone is
	// already rejected above.
	if !types.IsCandidateEligibleBondStatus(bond.Status) || bond.PendingUnbondingTotal >= effectiveBond {
		return types.TaskLiabilityReservationState{}, fmt.Errorf("service bond is not reservable")
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return types.TaskLiabilityReservationState{}, err
	}
	slashBps, minLiability, orderCoverageBps, err := taskLiabilityEconomics(params.Service)
	if err != nil {
		return types.TaskLiabilityReservationState{}, err
	}
	requiredLiability, err := types.RequiredTaskLiability(
		req.ActiveBondSnapshot, req.OrderValue, orderCoverageBps, slashBps, minLiability,
	)
	if err != nil {
		return types.TaskLiabilityReservationState{}, err
	}
	// available_bond is checked for coverage, never for equality with the frozen
	// snapshot. §B.1.3 only states the inequality "available_bond >=
	// required_task_liability", and available_bond = effective_active_bond -
	// reserved_liability moves on every reservation *and* every release by any
	// other duty of the same operator, while bond_version deliberately does not
	// change for either. Demanding equality therefore made concurrent duties
	// structurally impossible: an operator that took or finished a second task
	// between the candidate-fact freeze and this reservation failed here, and the
	// verifier assignment path escalated that into a FinalizeBlock error that
	// halted consensus. The identity facts (bond_version, the epoch-effective
	// active bond and the required-liability derivation) stay pinned.
	if bond.BondVersion != req.BondVersionSnapshot || effectiveBond != req.ActiveBondSnapshot ||
		requiredLiability != req.RequiredTaskLiability ||
		availableBond < req.RequiredTaskLiability {
		return types.TaskLiabilityReservationState{}, fmt.Errorf("current service bond does not match or cover the frozen liability fact")
	}
	profile, err := k.GetProfile(ctx, req.ModelID, req.ProfileVersion)
	if err != nil || RequiredServiceBondForProfile(profile) != req.MinStakeSnapshot {
		return types.TaskLiabilityReservationState{}, fmt.Errorf("profile min_stake does not match the frozen liability fact")
	}
	frozenCapabilityVersion, err := k.validateTaskLiabilityCapability(
		ctx, operator, req.Duty, req.ModelID, req.ProfileVersion,
		req.CapabilityVersionSnapshot, req.SupportVersionSnapshot, currentEpoch, effectiveBond, bond.Status,
	)
	if err != nil {
		return types.TaskLiabilityReservationState{}, err
	}
	types.NormalizeEffectiveActiveBond(&bond, currentEpoch)
	bond.ReservedLiability, err = checkedAdd(bond.ReservedLiability, req.RequiredTaskLiability)
	if err != nil {
		return types.TaskLiabilityReservationState{}, fmt.Errorf("reserved liability overflow: %w", err)
	}
	node.ActiveTaskLiabilityCount, err = checkedAddUint32(node.ActiveTaskLiabilityCount, 1)
	if err != nil {
		return types.TaskLiabilityReservationState{}, fmt.Errorf("active task liability count overflow: %w", err)
	}
	reservation := types.TaskLiabilityReservationState{
		SchemaVersion: 1, TaskId: append([]byte(nil), taskID...), OperatorAddress: operator, Duty: req.Duty,
		BondVersion: req.BondVersionSnapshot, CapabilityVersion: frozenCapabilityVersion,
		ReservedAmount: req.RequiredTaskLiability, Status: types.TaskLiabilityStatusReserved,
		CandidatePoolSnapshotId: append([]byte(nil), req.CandidatePoolSnapshotID...),
		Slot:                    req.Slot, SlotVersion: req.SlotVersion,
	}
	if err := reservation.Validate(); err != nil {
		return types.TaskLiabilityReservationState{}, err
	}
	if err := bond.Validate(); err != nil {
		return types.TaskLiabilityReservationState{}, err
	}
	if err := k.assertTaskLiabilityIndexesAbsent(ctx, taskID, reservation); err != nil {
		return types.TaskLiabilityReservationState{}, err
	}
	if err := k.ReserveCandidateSlotTaskRef(ctx, req.CandidatePoolSnapshotID, req.Slot, req.SlotVersion, taskID); err != nil {
		return types.TaskLiabilityReservationState{}, err
	}
	if err := k.WriteServiceBondValue(ctx, types.NewServiceBondKey(operator), bond); err != nil {
		return types.TaskLiabilityReservationState{}, err
	}
	if err := k.StoreCortexNode(ctx, operator, node); err != nil {
		return types.TaskLiabilityReservationState{}, err
	}
	if err := k.WriteTaskLiabilityValue(ctx, reservationKey, reservation); err != nil {
		return types.TaskLiabilityReservationState{}, err
	}
	if err := k.ActiveLiabilityByOperatorIndex.Set(ctx, types.NewActiveLiabilityByOperatorKey(operator, taskID, req.Duty)); err != nil {
		return types.TaskLiabilityReservationState{}, err
	}
	if err := k.TaskLiabilityByTaskIndex.Set(ctx, types.NewTaskLiabilityByTaskKey(taskID, req.Duty, operator)); err != nil {
		return types.TaskLiabilityReservationState{}, err
	}
	return reservation, nil
}

func parseFrozenLiabilityRequest(req types.FrozenFactLiabilityRequest) (shared.Hash32Key, error) {
	if len(req.TaskID) != 32 || len(req.CandidatePoolSnapshotID) != 32 || req.OrderValue == 0 || req.SlotVersion == 0 ||
		req.RequiredTaskLiability == 0 || req.ActiveBondSnapshot == 0 || req.AvailableBondSnapshot < req.RequiredTaskLiability ||
		req.MinStakeSnapshot == 0 || req.BondVersionSnapshot == 0 || req.CapabilityVersionSnapshot == 0 || req.SupportVersionSnapshot == 0 ||
		req.ActiveBondSnapshot < req.MinStakeSnapshot ||
		req.ModelID == "" || req.ModelID != strings.TrimSpace(req.ModelID) || req.ProfileVersion == 0 || req.Height == 0 ||
		(req.Duty != shared.DutyWorker && req.Duty != shared.DutyVerifier) {
		return nil, fmt.Errorf("frozen task liability request is incomplete")
	}
	if err := types.ValidateModelID(req.ModelID); err != nil {
		return nil, err
	}
	return append(shared.Hash32Key(nil), req.TaskID...), nil
}

func (k Keeper) validateTaskLiabilityCapability(
	ctx context.Context,
	operatorAddress string,
	duty shared.Duty,
	modelID string,
	profileVersion uint32,
	expectedCapabilityVersion, expectedSupportVersion, currentEpoch, effectiveBond uint64,
	bondStatus types.ServiceBondStatus,
) (uint64, error) {
	model, err := k.GetModel(ctx, modelID)
	if err != nil || (model.Status != types.ModelStatusRegistered && model.Status != types.ModelStatusActive) {
		return 0, fmt.Errorf("model is not accepting task liability")
	}
	profile, err := k.GetProfile(ctx, modelID, profileVersion)
	if err != nil || (profile.Status != types.ModelStatusRegistered && profile.Status != types.ModelStatusActive) {
		return 0, fmt.Errorf("profile is not accepting task liability")
	}
	if effectiveBond < RequiredServiceBondForProfile(profile) {
		return 0, fmt.Errorf("effective active bond is below profile min_stake")
	}
	capability, err := k.GetProfileCapabilityState(ctx, operatorAddress, modelID, profileVersion)
	if err != nil {
		return 0, fmt.Errorf("profile capability is required: %w", err)
	}
	support, err := k.GetModelSupportState(ctx, operatorAddress, modelID, profileVersion)
	if err != nil {
		return 0, fmt.Errorf("model support is required: %w", err)
	}
	if capability.OperatorAddress != operatorAddress || capability.ModelId != modelID || capability.ProfileVersion != profileVersion ||
		support.OperatorAddress != operatorAddress || support.ModelId != modelID || support.ProfileVersion != profileVersion {
		return 0, fmt.Errorf("capability or support state does not match its store key")
	}
	if err := capability.Validate(); err != nil {
		return 0, fmt.Errorf("invalid profile capability: %w", err)
	}
	if capability.CapabilityVersion != expectedCapabilityVersion {
		return 0, fmt.Errorf("task liability capability version does not match the frozen fact")
	}
	if duty == shared.DutyWorker && !capability.InferenceCapability {
		return 0, fmt.Errorf("worker liability requires inference capability")
	}
	if duty == shared.DutyVerifier && !capability.VerificationCapability {
		return 0, fmt.Errorf("verifier liability requires verification capability")
	}
	if err := support.Validate(); err != nil {
		return 0, fmt.Errorf("invalid model support: %w", err)
	}
	// The accepted candidate version is an audit floor, not a CAS token. Contract
	// §10.1 explicitly allows jail suspension, daily refresh and bond reweight to
	// advance support_version before liability reservation; capability_version
	// remains exact because changing either capability boolean changes duty
	// eligibility itself.
	if !support.DeclaredSupport || support.SupportVersion < expectedSupportVersion {
		return 0, fmt.Errorf("task liability support is not currently declared or its version moved backwards")
	}
	if support.SupportFreshUntilEpoch == 0 || currentEpoch >= support.SupportFreshUntilEpoch {
		return 0, fmt.Errorf("task liability model support is stale")
	}
	if duty == shared.DutyWorker && profile.Status == types.ModelStatusActive && !support.SupportActive && bondStatus != types.ServiceBondStatusJailed {
		return 0, fmt.Errorf("active profile requires active model support")
	}
	return capability.CapabilityVersion, nil
}

func (k Keeper) requireTaskLiabilitySlotOwnership(
	ctx context.Context,
	taskID shared.Hash32Key,
	reservation types.TaskLiabilityReservationState,
) error {
	snapshotKey, err := candidateHashKey(reservation.CandidatePoolSnapshotId)
	if err != nil {
		return fmt.Errorf("task liability candidate_pool_snapshot_id: %w", err)
	}
	// Both keyspaces are raw Hash32 now, so the single taskID serves the liability
	// key and the CandidatePoolTaskRef component; the split this function used to
	// carry (raw for one, hex for the other) is gone.
	ref, err := k.CandidatePoolTaskRef.Get(ctx, types.NewCandidatePoolTaskRefKey(taskID, snapshotKey))
	if err != nil || ref.Status != types.CandidatePoolTaskRefStatus_CANDIDATE_POOL_TASK_REF_STATUS_ACQUIRED ||
		!bytes.Equal(ref.TaskId, taskID) || !bytes.Equal(ref.SnapshotId, reservation.CandidatePoolSnapshotId) {
		return fmt.Errorf("task liability candidate pool task reference is unavailable")
	}
	member, binding, err := k.ResolveCandidatePoolMember(ctx, reservation.CandidatePoolSnapshotId, reservation.Slot)
	if err != nil || member.SlotVersion != reservation.SlotVersion || member.OperatorAddress != reservation.OperatorAddress ||
		binding.SlotVersion != reservation.SlotVersion || binding.OperatorAddress != reservation.OperatorAddress ||
		!bytes.Equal(member.BindingHash, binding.BindingHash) {
		return fmt.Errorf("task liability candidate slot ownership does not match the frozen snapshot")
	}
	current, err := k.ReadCandidateSlotCurrent(ctx, reservation.Slot)
	if err != nil || current.SlotVersion != reservation.SlotVersion || current.OperatorAddress != reservation.OperatorAddress ||
		current.ActiveTaskRefs == 0 {
		return fmt.Errorf("task liability candidate slot ownership is not active")
	}
	return nil
}

func (k Keeper) ensureTaskLiabilityCapacity(ctx context.Context, taskID shared.Hash32Key, newDuty shared.Duty) error {
	iter, err := k.TaskLiabilityByTaskIndex.Iterate(ctx, collections.NewPrefixedTripleRange[shared.Hash32Key, int32, string](taskID))
	if err != nil {
		return err
	}
	defer iter.Close()
	var workers, verifiers uint32
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return err
		}
		switch shared.Duty(key.K2()) {
		case shared.DutyWorker:
			workers++
		case shared.DutyVerifier:
			verifiers++
		default:
			return fmt.Errorf("task liability index has invalid duty")
		}
		if workers > maxWorkerLiabilitiesPerTaskV1 || verifiers > maxVerifierLiabilitiesPerTaskV1 ||
			workers+verifiers > maxTaskLiabilitiesPerTaskV1 {
			return fmt.Errorf("task liability count exceeds V1 worker/verifier hard bounds")
		}
	}
	switch newDuty {
	case shared.DutyWorker:
		workers++
	case shared.DutyVerifier:
		verifiers++
	default:
		return fmt.Errorf("task liability duty is invalid")
	}
	if workers > maxWorkerLiabilitiesPerTaskV1 || verifiers > maxVerifierLiabilitiesPerTaskV1 ||
		workers+verifiers > maxTaskLiabilitiesPerTaskV1 {
		return fmt.Errorf("task liability count exceeds V1 worker/verifier hard bounds")
	}
	return nil
}

// ReleaseTaskLiabilities is the CloseAllTaskLiabilitiesOnce entry of §B.1.3:
// it walks TaskLiabilityByTaskIndex in stable order under the hard bound and
// closes every reservation through the single-row helper.
func (k Keeper) ReleaseTaskLiabilities(ctx context.Context, sessionID, taskID string, height uint64) error {
	if sessionID == "" || sessionID != strings.TrimSpace(sessionID) || height == 0 {
		return fmt.Errorf("canonical session_id and positive release height are required")
	}
	taskKey, err := parseTaskLiabilityID(taskID)
	if err != nil {
		return err
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	iter, err := k.TaskLiabilityByTaskIndex.Iterate(cache, collections.NewPrefixedTripleRange[shared.Hash32Key, int32, string](taskKey))
	if err != nil {
		return err
	}
	keys := make([]types.TaskLiabilityByTaskKey, 0, maxTaskLiabilitiesPerTaskV1)
	var workers, verifiers uint32
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			iter.Close()
			return err
		}
		keys = append(keys, key)
		switch shared.Duty(key.K2()) {
		case shared.DutyWorker:
			workers++
		case shared.DutyVerifier:
			verifiers++
		default:
			iter.Close()
			return fmt.Errorf("task liability index has invalid duty")
		}
		if workers > maxWorkerLiabilitiesPerTaskV1 || verifiers > maxVerifierLiabilitiesPerTaskV1 || len(keys) > maxTaskLiabilitiesPerTaskV1 {
			iter.Close()
			return fmt.Errorf("task liability count exceeds V1 worker/verifier hard bounds")
		}
	}
	if err := iter.Close(); err != nil {
		return err
	}
	for _, key := range keys {
		if _, err := k.closeTaskLiabilityReservationInCache(cache, taskKey, key.K3(), shared.Duty(key.K2()), types.TaskLiabilityStatusReleased, 0, height, nil); err != nil {
			return err
		}
	}
	write()
	return nil
}

// DeleteOneClosedTaskLiability removes one retained terminal liability receipt.
// It scans at most the V1 per-task hard bound before deleting anything, so a
// malformed or still-reserved row cannot be hidden behind an earlier terminal
// row. Cleanup calls this repeatedly and charges one visited item per deletion.
func (k Keeper) DeleteOneClosedTaskLiability(ctx context.Context, taskID string) (bool, error) {
	taskKey, err := parseTaskLiabilityID(taskID)
	if err != nil {
		return false, err
	}
	iter, err := k.TaskLiabilityReservation.Iterate(ctx, collections.NewPrefixedTripleRange[shared.Hash32Key, int32, string](taskKey))
	if err != nil {
		return false, err
	}
	keys := make([]types.TaskLiabilityReservationKeyTriple, 0, maxTaskLiabilitiesPerTaskV1)
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			iter.Close()
			return false, err
		}
		if len(keys) == maxTaskLiabilitiesPerTaskV1 {
			iter.Close()
			return false, fmt.Errorf("task liability count exceeds V1 hard bound")
		}
		reservation, err := k.ProjectTaskLiabilityStore(entry.Value)
		if err != nil {
			iter.Close()
			return false, err
		}
		if !bytes.Equal(reservation.TaskId, taskKey) ||
			reservation.Duty != shared.Duty(entry.Key.K2()) || reservation.OperatorAddress != entry.Key.K3() {
			iter.Close()
			return false, fmt.Errorf("task liability state does not match its store key")
		}
		if err := reservation.Validate(); err != nil {
			iter.Close()
			return false, fmt.Errorf("invalid task liability reservation: %w", err)
		}
		if reservation.Status == types.TaskLiabilityStatusReserved {
			iter.Close()
			return false, fmt.Errorf("cannot delete a reserved task liability")
		}
		if reservation.Status != types.TaskLiabilityStatusReleased && reservation.Status != types.TaskLiabilityStatusSlashed {
			iter.Close()
			return false, fmt.Errorf("task liability has invalid terminal status")
		}
		if err := k.assertTaskLiabilityIndexesAbsent(ctx, taskKey, reservation); err != nil {
			iter.Close()
			return false, err
		}
		keys = append(keys, entry.Key)
	}
	if err := iter.Close(); err != nil {
		return false, err
	}
	if len(keys) == 0 {
		return false, nil
	}
	if err := k.TaskLiabilityReservation.Remove(ctx, keys[0]); err != nil {
		return false, err
	}
	return true, nil
}

// closeTaskLiabilityReservationInCache owns the checked liability decrement,
// active-index removal, terminal write, and unified slash transition. Callers
// already own the transaction cache so later failures roll back the full fault
// or challenge transition.
func (k Keeper) closeTaskLiabilityReservationInCache(
	ctx context.Context,
	taskID shared.Hash32Key,
	operatorAddress string,
	duty shared.Duty,
	outcome types.LiabilityStatus,
	requestedSlash, height uint64,
	slashMetadata *applyServiceSlashMetadata,
) (ApplyServiceSlashResult, error) {
	if outcome != types.TaskLiabilityStatusReleased && outcome != types.TaskLiabilityStatusSlashed {
		return ApplyServiceSlashResult{}, fmt.Errorf("task liability outcome must be RELEASED or SLASHED")
	}
	if height == 0 {
		return ApplyServiceSlashResult{}, fmt.Errorf("task liability close height must be > 0")
	}
	key := types.NewTaskLiabilityReservationKey(taskID, duty, operatorAddress)
	reservation, err := k.ReadTaskLiabilityValue(ctx, key)
	if err != nil {
		return ApplyServiceSlashResult{}, fmt.Errorf("task liability reservation not found: %w", err)
	}
	if reservation.Status == outcome {
		return ApplyServiceSlashResult{}, nil
	}
	if reservation.Status != types.TaskLiabilityStatusReserved {
		return ApplyServiceSlashResult{}, fmt.Errorf("task liability already closed with %s", reservation.Status.String())
	}
	// taskID is the store key itself now, so this is a direct key/value identity
	// check rather than a re-decode of the text the key used to be.
	if !bytes.Equal(reservation.TaskId, taskID) || reservation.OperatorAddress != operatorAddress || reservation.Duty != duty {
		return ApplyServiceSlashResult{}, fmt.Errorf("task liability state does not match its store key")
	}
	if err := reservation.Validate(); err != nil {
		return ApplyServiceSlashResult{}, fmt.Errorf("invalid task liability reservation: %w", err)
	}
	if err := k.requireActiveTaskLiabilityIndexes(ctx, taskID, reservation); err != nil {
		return ApplyServiceSlashResult{}, err
	}
	if err := k.requireTaskLiabilitySlotOwnership(ctx, taskID, reservation); err != nil {
		return ApplyServiceSlashResult{}, err
	}
	bond, err := k.GetServiceBondState(ctx, operatorAddress)
	if err != nil {
		return ApplyServiceSlashResult{}, err
	}
	if bond.ReservedLiability < reservation.ReservedAmount {
		return ApplyServiceSlashResult{}, fmt.Errorf("task liability exceeds node reserved liability")
	}
	// A terminal bond owns no cortex identity. removeCortexIdentityOnTerminalBond
	// deletes the row the moment a bond reaches TOMBSTONED or EXITED — the jail
	// ladder does it mid-flight, without waiting for that operator's RESERVED task
	// liabilities to close — and the genesis cross-check does not merely tolerate
	// the resulting pairing, it *requires* it ("terminal service bond %s retains a
	// cortex identity"). Demanding the row back here is what turned every later
	// deadline sweep over such a liability into an EndBlocker error, hence a
	// FinalizeBlock error, hence a deterministic chain halt that also reproduced on
	// every restart via handshake replay (localnet @ 115588).
	//
	// Nothing this function needs is lost. The money lives on the bond, the task
	// ref lives on the candidate slot, and both are still closed below. Only
	// active_task_liability_count lives on the identity, and a retired identity has
	// no counter left to keep consistent. A *live* bond with no identity remains a
	// broken invariant and is still rejected.
	node, identityLive, err := k.getCortexNodeStateIfPresent(ctx, operatorAddress)
	if err != nil {
		return ApplyServiceSlashResult{}, err
	}
	switch {
	case identityLive:
		if node.ActiveTaskLiabilityCount == 0 {
			return ApplyServiceSlashResult{}, fmt.Errorf("active task liability count underflow for %s", operatorAddress)
		}
	case bond.Status != types.ServiceBondStatusTombstoned && bond.Status != types.ServiceBondStatusExited:
		return ApplyServiceSlashResult{}, fmt.Errorf("cortex node %s not found", operatorAddress)
	}
	requested := uint64(0)
	if outcome == types.TaskLiabilityStatusSlashed {
		requested = minUint64(requestedSlash, reservation.ReservedAmount)
	}

	bond.ReservedLiability -= reservation.ReservedAmount
	applyPostSlashBondStatus(&bond)
	if identityLive {
		node.ActiveTaskLiabilityCount--
	}
	reservation.Status = outcome
	if err := bond.Validate(); err != nil {
		return ApplyServiceSlashResult{}, err
	}
	if err := reservation.Validate(); err != nil {
		return ApplyServiceSlashResult{}, err
	}
	if err := k.ReleaseCandidateSlotTaskRef(ctx, reservation.Slot, reservation.SlotVersion, height); err != nil {
		return ApplyServiceSlashResult{}, err
	}
	if err := k.WriteServiceBondValue(ctx, types.NewServiceBondKey(operatorAddress), bond); err != nil {
		return ApplyServiceSlashResult{}, err
	}
	// Never resurrect a retired identity: writing the row back would restore an
	// operator the terminal transition already deleted and break the genesis
	// pairing this branch exists to respect.
	if identityLive {
		if err := k.StoreCortexNode(ctx, operatorAddress, node); err != nil {
			return ApplyServiceSlashResult{}, err
		}
	}
	if err := k.WriteTaskLiabilityValue(ctx, key, reservation); err != nil {
		return ApplyServiceSlashResult{}, err
	}
	if err := k.ActiveLiabilityByOperatorIndex.Remove(ctx, types.NewActiveLiabilityByOperatorKey(operatorAddress, taskID, duty)); err != nil {
		return ApplyServiceSlashResult{}, err
	}
	if err := k.TaskLiabilityByTaskIndex.Remove(ctx, types.NewTaskLiabilityByTaskKey(taskID, duty, operatorAddress)); err != nil {
		return ApplyServiceSlashResult{}, err
	}
	// Ruling 29: no pool invalidation here — closing a reservation does not change
	// global membership. But this is §3.3 line 225 clearing point 2: the
	// ActiveLiabilityByOperatorIndex row just went away, so a RETIRING slot that
	// was blocked only by it must complete its release in this same transaction.
	if err := k.retryCandidateSlotRelease(ctx, operatorAddress, height); err != nil {
		return ApplyServiceSlashResult{}, err
	}
	if identityLive && (bond.Status == types.ServiceBondStatusTombstoned || bond.Status == types.ServiceBondStatusExited) {
		if err := k.removeCortexIdentityOnTerminalBond(ctx, operatorAddress); err != nil {
			return ApplyServiceSlashResult{}, err
		}
	}

	if outcome == types.TaskLiabilityStatusReleased {
		// This is the only authoritative Hub-side fact for "the operator fully
		// discharged one protocol-assigned duty without a fault", which §10.0c
		// makes the jail-clear trigger. The SLASHED branch must never advance it.
		if _, err := k.advanceJailClearCounter(ctx, operatorAddress, duty, height); err != nil {
			return ApplyServiceSlashResult{}, err
		}
		return ApplyServiceSlashResult{}, nil
	}
	if requested == 0 {
		// A classified zero-bps fault still consumes the liability and must not
		// advance the healthy-duty jail-clear counter, but it has no monetary
		// effect and therefore no SlashSummary.
		return ApplyServiceSlashResult{BondVersion: bond.BondVersion}, nil
	}
	if slashMetadata == nil {
		return ApplyServiceSlashResult{}, fmt.Errorf("task liability slash metadata is required")
	}

	result, err := k.applyServiceSlashInCache(ctx, ApplyServiceSlashRequest{
		OperatorAddress: operatorAddress,
		Duty:            duty,
		TaskID:          taskID,
		Requested:       requested,
		Height:          height,
		SummaryID:       append([]byte(nil), slashMetadata.SummaryID...),
		SourceKind:      slashMetadata.SourceKind,
		SourceID:        append([]byte(nil), slashMetadata.SourceID...),
		EffectIndex:     slashMetadata.EffectIndex,
		Destination:     slashMetadata.Destination,
	})
	if err != nil {
		return ApplyServiceSlashResult{}, err
	}
	return result, nil
}

func (k Keeper) assertTaskLiabilityIndexesAbsent(ctx context.Context, taskID shared.Hash32Key, reservation types.TaskLiabilityReservationState) error {
	activeExists, err := k.ActiveLiabilityByOperatorIndex.Has(ctx, types.NewActiveLiabilityByOperatorKey(
		reservation.OperatorAddress, taskID, reservation.Duty,
	))
	if err != nil {
		return err
	}
	byTaskExists, err := k.TaskLiabilityByTaskIndex.Has(ctx, types.NewTaskLiabilityByTaskKey(
		taskID, reservation.Duty, reservation.OperatorAddress,
	))
	if err != nil {
		return err
	}
	if activeExists || byTaskExists {
		return fmt.Errorf("task liability index exists without primary reservation")
	}
	return nil
}

func (k Keeper) requireActiveTaskLiabilityIndexes(ctx context.Context, taskID shared.Hash32Key, reservation types.TaskLiabilityReservationState) error {
	activeExists, err := k.ActiveLiabilityByOperatorIndex.Has(ctx, types.NewActiveLiabilityByOperatorKey(
		reservation.OperatorAddress, taskID, reservation.Duty,
	))
	if err != nil {
		return err
	}
	byTaskExists, err := k.TaskLiabilityByTaskIndex.Has(ctx, types.NewTaskLiabilityByTaskKey(
		taskID, reservation.Duty, reservation.OperatorAddress,
	))
	if err != nil {
		return err
	}
	if !activeExists || !byTaskExists {
		return fmt.Errorf("reserved task liability is missing an active index")
	}
	return nil
}
