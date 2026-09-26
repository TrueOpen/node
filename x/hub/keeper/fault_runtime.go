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

// Jail and tombstone are operator-global and live only on ServiceBondState
// (`jail_count` / `normal_action_count_since_jail` / `status`), per
// the data-structure contract and the API contract ("no
// second TombstoneState is read"). The previous duty-scoped Jail/Tombstone
// collections were a
// second source of truth for the same facts and are no longer read or written
// here. Duty attribution survives in RoleFaultState.duty and in the event 51
// payload.
//
// The two thresholds are parameters, not constants: the old hardcoded
// JailClearThreshold=1000 / MaxJailCountBeforeTombstone=3 shadowed
// params.Service.JailClearNormalActionCount and
// params.Service.TombstoneJailCountThreshold, so governance could not move
// them.

// ServiceJailSnapshot is a read-only projection of the operator-global jail
// counters. It is derived from ServiceBondState on every read; nothing stores
// it. Duty is echoed back from the caller for attribution only.
type ServiceJailSnapshot struct {
	OperatorAddress string
	Duty            string
	JailCount       uint64
	ClearCounter    uint64
	Tombstoned      bool
}

type roleFaultApplyResult struct {
	Created    bool
	Jail       ServiceJailSnapshot
	Tombstoned bool
	Slash      uint64
	Unfilled   uint64
	// State is the authoritative RoleFaultState this call left in the Store,
	// including the jail_delta Hub itself decided. Task needs it because §6.6's
	// fault_summary_hash frames jail_delta and status, so the caller cannot
	// reconstruct the committed vector from the fact it submitted.
	State types.RoleFaultState
}

func (k Keeper) recordRoleFaultState(ctx context.Context, state types.RoleFaultState, key shared.Hash32Key, sessionID string) (bool, error) {
	if existing, err := k.ReadRoleFaultValue(ctx, key); err == nil {
		if !sameRoleFault(existing, state) {
			return false, fmt.Errorf("role fault %s replay facts differ", hexRef(key))
		}
		return false, nil
	} else if !errors.Is(err, collections.ErrNotFound) {
		return false, err
	}
	if err := k.WriteRoleFaultValue(ctx, key, state); err != nil {
		return false, err
	}
	// The by-task direction is written in the same transition as the primary so
	// the §6.6 fault vector for a task is always readable in bounded work; the
	// prune path deletes both together.
	if err := k.RoleFaultByTaskIndex.Set(ctx, types.NewRoleFaultByTaskKey(state.TaskId, key)); err != nil {
		return false, err
	}
	if err := k.scheduleRoleFaultPrune(ctx, state); err != nil {
		return false, err
	}
	emitFaultRecordedEvent(ctx, state, sessionID)
	return true, nil
}

// ApplyTaskRoleFault is the production Task-to-Hub fault boundary. Task owns
// the classification evidence and frozen consequence parameter; Hub atomically
// closes the liability, moves slash custody and advances jail/tombstone state.
//
// It returns the applied RoleFaultState because Hub is the only side that knows
// jail_delta and status, and §6.6's fault_summary_hash - which Task must close
// into settlement_facts_hash - frames both. Returning the row is what lets Task
// commit the fault vector without ever recomputing Hub's decisions.
func (k Keeper) ApplyTaskRoleFault(ctx context.Context, fact types.TaskRoleFaultFact) (types.RoleFaultState, error) {
	if len(fact.SessionID) != 32 || bytes.Equal(fact.SessionID, make([]byte, 32)) ||
		len(fact.TaskID) != 32 || bytes.Equal(fact.TaskID, make([]byte, 32)) ||
		len(fact.EvidenceDigest) != 32 || bytes.Equal(fact.EvidenceDigest, make([]byte, 32)) ||
		!types.IsValidDuty(fact.Duty) || strings.TrimSpace(fact.FaultType) == "" ||
		uint64(fact.SlashBps) > types.ObjectiveForgerySlashDenom || fact.Height == 0 {
		return types.RoleFaultState{}, fmt.Errorf("task role fault fact is incomplete or invalid")
	}
	if err := validateRoleFaultClassificationSource(fact.FaultType, fact.ClassificationSource); err != nil {
		return types.RoleFaultState{}, err
	}
	role, err := roleFromDuty(fact.Duty)
	if err != nil {
		return types.RoleFaultState{}, err
	}
	result, err := k.applyRoleFaultAtomic(
		ctx, fact.OperatorAddress, role, fact.FaultType,
		hex.EncodeToString(fact.SessionID), hex.EncodeToString(fact.TaskID), hex.EncodeToString(fact.EvidenceDigest),
		fact.ClassificationSource, fact.SlashBps, fact.Height,
	)
	if err != nil {
		return types.RoleFaultState{}, err
	}
	if err := result.State.Validate(); err != nil {
		return types.RoleFaultState{}, fmt.Errorf("applied role fault is not canonical: %w", err)
	}
	return result.State, nil
}

// ApplyWorkerObjectiveEvidence is the sole Task-to-Hub kernel for accepted
// output equivocation. The active responsibility proves the retained Worker key
// generation; attribution is fixed and cannot be selected by the caller.
func (k Keeper) ApplyWorkerObjectiveEvidence(
	ctx context.Context,
	fact shared.WorkerObjectiveEvidenceFactV1,
) (types.RoleFaultState, error) {
	if len(fact.SessionId) != shared.Hash32KeySize || bytes.Equal(fact.SessionId, make([]byte, shared.Hash32KeySize)) ||
		len(fact.TaskId) != shared.Hash32KeySize || bytes.Equal(fact.TaskId, make([]byte, shared.Hash32KeySize)) ||
		len(fact.EvidenceDigest) != shared.Hash32KeySize || bytes.Equal(fact.EvidenceDigest, make([]byte, shared.Hash32KeySize)) ||
		fact.FrozenSlashBps > uint32(types.ObjectiveForgerySlashDenom) {
		return types.RoleFaultState{}, fmt.Errorf("worker objective evidence fact is invalid")
	}
	sessionID, taskID := hex.EncodeToString(fact.SessionId), hex.EncodeToString(fact.TaskId)
	if _, err := k.GetWorkerEvidenceProofKey(ctx, sessionID, taskID, fact.WorkerOperatorAddress); err != nil {
		return types.RoleFaultState{}, err
	}
	height := sdkWrappedContextHeight(ctx)
	if height == 0 {
		return types.RoleFaultState{}, fmt.Errorf("worker objective evidence requires a positive block height")
	}
	result, err := k.applyRoleFaultAtomic(
		ctx, fact.WorkerOperatorAddress, types.ServiceBondRoleWorker, types.FaultTypeObjectiveForgery,
		sessionID, taskID, hex.EncodeToString(fact.EvidenceDigest),
		shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_OBJECTIVE_EVIDENCE,
		fact.FrozenSlashBps, height,
	)
	if err != nil {
		return types.RoleFaultState{}, err
	}
	return result.State, nil
}

func roleFromDuty(duty shared.Duty) (string, error) {
	switch duty {
	case shared.Duty_DUTY_WORKER:
		return types.ServiceBondRoleWorker, nil
	case shared.Duty_DUTY_VERIFIER:
		return types.ServiceBondRoleVerifier, nil
	default:
		return "", fmt.Errorf("role fault duty is invalid")
	}
}

func (k Keeper) applyRoleFaultAtomic(ctx context.Context, operatorAddress, role, faultType, sessionID, taskID, evidenceDigest string, classificationSource shared.FailureClassificationSource, slashBps uint32, height uint64) (roleFaultApplyResult, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	result, err := k.applyRoleFault(sdk.WrapSDKContext(cacheCtx), operatorAddress, role, faultType, sessionID, taskID, evidenceDigest, classificationSource, slashBps, height)
	if err != nil {
		return roleFaultApplyResult{}, err
	}
	commit()
	return result, nil
}

func (k Keeper) applyRoleFault(ctx context.Context, roleAddress, role, faultType, sessionID, taskID, evidenceDigest string, classificationSource shared.FailureClassificationSource, slashBps uint32, height uint64) (roleFaultApplyResult, error) {
	_, roleAddress, err := k.requireCanonicalAddress("operator_address", roleAddress)
	if err != nil {
		return roleFaultApplyResult{}, err
	}
	role = normalizeServiceRole(role)
	faultType = strings.TrimSpace(faultType)
	evidenceDigest = strings.TrimSpace(evidenceDigest)
	duty, err := dutyFromRole(role)
	if err != nil || faultType == "" || height == 0 {
		return roleFaultApplyResult{}, errors.New("operator_address, duty, and fault_type are required")
	}
	prospective, faultKey, err := k.buildRoleFaultState(ctx, roleAddress, role, faultType, sessionID, taskID, evidenceDigest, classificationSource, height)
	if err != nil {
		return roleFaultApplyResult{}, err
	}
	if existing, err := k.ReadRoleFaultValue(ctx, faultKey); err == nil {
		if !sameRoleFaultIdentity(existing, prospective) || existing.Status != prospective.Status {
			return roleFaultApplyResult{}, fmt.Errorf("role fault %s replay facts differ", hexRef(faultKey))
		}
		var slash ApplyServiceSlashResult
		if len(existing.GetSlashSummaryId()) != 0 {
			if !bytes.Equal(existing.GetSlashSummaryId(), existing.FaultId) {
				return roleFaultApplyResult{}, fmt.Errorf("role fault %s replay slash summary is invalid", hexRef(faultKey))
			}
			summary, summaryErr := k.ReadSlashSummaryValue(ctx, types.NewSlashSummaryKey(
				types.SlashSourceKind_SLASH_SOURCE_KIND_ROLE_FAULT, faultKey, 0,
			))
			if summaryErr != nil {
				return roleFaultApplyResult{}, summaryErr
			}
			if summary.Validate() != nil || !bytes.Equal(summary.SourceId, existing.FaultId) ||
				!bytes.Equal(summary.GetTaskId(), existing.TaskId) || summary.OperatorAddress != roleAddress || summary.Duty != duty {
				return roleFaultApplyResult{}, fmt.Errorf("role fault %s replay slash summary differs", hexRef(faultKey))
			}
			slash, summaryErr = slashSummaryResult(summary)
			if summaryErr != nil {
				return roleFaultApplyResult{}, summaryErr
			}
		}
		jail, err := k.GetJail(ctx, roleAddress, role)
		return roleFaultApplyResult{Jail: jail, Slash: slash.Applied, Unfilled: slash.Unfilled, State: existing}, err
	} else if !errors.Is(err, collections.ErrNotFound) {
		return roleFaultApplyResult{}, err
	}
	var slash ApplyServiceSlashResult
	var slashable bool
	if strings.TrimSpace(sessionID) != "" && strings.TrimSpace(taskID) != "" {
		canonicalTaskID, idErr := parseTaskLiabilityID(taskID)
		if idErr != nil {
			return roleFaultApplyResult{}, idErr
		}
		reservation, reservationErr := k.ReadTaskLiabilityValue(ctx, types.NewTaskLiabilityReservationKey(canonicalTaskID, duty, roleAddress))
		if reservationErr != nil {
			return roleFaultApplyResult{}, reservationErr
		}
		requested := types.SlashByFraction(reservation.ReservedAmount, uint64(slashBps), types.ObjectiveForgerySlashDenom)
		slashable = requested > 0
		if classificationSource == shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_OBJECTIVE_EVIDENCE {
			if duty != shared.DutyWorker || faultType != types.FaultTypeObjectiveForgery {
				return roleFaultApplyResult{}, fmt.Errorf("worker objective evidence has invalid attribution")
			}
			switch reservation.Status {
			case types.TaskLiabilityStatusReserved:
				slash, err = k.closeTaskLiabilityReservationInCache(
					ctx, canonicalTaskID, roleAddress, duty, types.TaskLiabilityStatusSlashed, requested, height,
					newRoleFaultSlashMetadata(prospective),
				)
			case types.TaskLiabilityStatusReleased:
				if requested == 0 {
					break
				}
				metadata := newRoleFaultSlashMetadata(prospective)
				slash, err = k.applyServiceSlashInCache(ctx, ApplyServiceSlashRequest{
					OperatorAddress: roleAddress, Duty: duty, TaskID: canonicalTaskID,
					Requested: requested, Height: height,
					SummaryID: append([]byte(nil), metadata.SummaryID...), SourceKind: metadata.SourceKind,
					SourceID: append([]byte(nil), metadata.SourceID...), EffectIndex: metadata.EffectIndex,
					Destination: metadata.Destination,
				})
			default:
				return roleFaultApplyResult{}, fmt.Errorf("worker objective evidence liability is not slashable")
			}
		} else {
			// The reservation close and the debit are one transition for faults
			// classified before Task finality.
			slash, err = k.closeTaskLiabilityReservationInCache(
				ctx, canonicalTaskID, roleAddress, duty, types.TaskLiabilityStatusSlashed, requested, height,
				newRoleFaultSlashMetadata(prospective),
			)
		}
	} else {
		return roleFaultApplyResult{}, fmt.Errorf("role fault requires canonical session_id and task_id")
	}
	if err != nil {
		return roleFaultApplyResult{}, err
	}
	if slashable {
		prospective.XSlashSummaryId = &types.RoleFaultState_SlashSummaryId{
			SlashSummaryId: append([]byte(nil), prospective.FaultId...),
		}
	}
	jailBefore, err := k.GetJail(ctx, roleAddress, role)
	if err != nil {
		return roleFaultApplyResult{}, err
	}
	created, err := k.recordRoleFaultState(ctx, prospective, faultKey, sessionID)
	if err != nil {
		return roleFaultApplyResult{}, err
	}
	result := roleFaultApplyResult{Created: created, Slash: slash.Applied, Unfilled: slash.Unfilled, State: prospective}
	if !created {
		jail, err := k.GetJail(ctx, roleAddress, role)
		result.Jail = jail
		return result, err
	}
	if slash.Applied > 0 || slash.Unfilled > 0 {
		emitRoleSlashedEvent(ctx, duty, roleAddress, prospective.FaultId, slash, faultType)
	}
	if shouldJailForFault(faultType) {
		jail, tombstoned, err := k.incJail(ctx, roleAddress, role, height, eventLifecycleReasonForFault(faultType))
		if err != nil {
			return roleFaultApplyResult{}, err
		}
		result.Jail = jail
		result.Tombstoned = tombstoned
		if jail.JailCount > jailBefore.JailCount {
			prospective.JailDelta = 1
			if err := k.WriteRoleFaultValue(ctx, faultKey, prospective); err != nil {
				return roleFaultApplyResult{}, err
			}
			// The returned row has to be the one now in the Store: Task frames
			// jail_delta into fault_summary_hash, so handing back the pre-jail copy
			// would commit a vector the Store disagrees with.
			result.State = prospective
		}
	}
	if shouldTombstoneForFault(faultType) && slashable {
		if err := k.SetTombstone(ctx, roleAddress, height, faultType); err != nil {
			return roleFaultApplyResult{}, err
		}
		result.Tombstoned = true
	}
	return result, nil
}

// IncJail raises the operator-global jail_count by one. duty is used only for
// event attribution; the counter itself is not duty-scoped (Ruling 11).
func (k Keeper) IncJail(ctx context.Context, operatorAddress, duty string, height uint64) (ServiceJailSnapshot, bool, error) {
	return k.incJail(ctx, operatorAddress, duty, height, types.EventLifecycleReason_EVENT_LIFECYCLE_REASON_OBJECTIVE_FAULT)
}

func (k Keeper) incJail(ctx context.Context, operatorAddress, duty string, height uint64, reason types.EventLifecycleReason) (ServiceJailSnapshot, bool, error) {
	_, operatorAddress, err := k.requireCanonicalAddress("operator_address", operatorAddress)
	if err != nil {
		return ServiceJailSnapshot{}, false, err
	}
	duty = normalizeServiceRole(duty)
	dutyValue, err := dutyFromRole(duty)
	if err != nil {
		return ServiceJailSnapshot{}, false, errors.New("operator_address and duty are required")
	}
	if height == 0 {
		return ServiceJailSnapshot{}, false, errors.New("jail height must be > 0")
	}
	bond, err := k.GetServiceBondState(ctx, operatorAddress)
	if err != nil {
		return ServiceJailSnapshot{}, false, err
	}
	if bond.Status == types.ServiceBondStatusTombstoned {
		return serviceJailSnapshot(bond, duty), false, nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return ServiceJailSnapshot{}, false, err
	}
	threshold := params.Service.TombstoneJailCountThreshold
	if threshold == 0 {
		return ServiceJailSnapshot{}, false, fmt.Errorf("tombstone_jail_count_threshold must be > 0")
	}
	if bond.JailCount >= threshold {
		return serviceJailSnapshot(bond, duty), true, nil
	}
	oldJailCount := bond.JailCount
	bond.JailCount, err = checkedAddUint32(bond.JailCount, 1)
	if err != nil {
		return ServiceJailSnapshot{}, false, fmt.Errorf("jail_count overflow: %w", err)
	}
	bond.NormalActionCountSinceJail = 0
	tombstoned := bond.JailCount >= threshold
	switch {
	case tombstoned:
		bond.Status = types.ServiceBondStatusTombstoned
	case types.IsLiveServiceBondStatus(bond.Status) || bond.Status == types.ServiceBondStatusJailed:
		bond.Status = types.ServiceBondStatusJailed
	}
	if err := bond.Validate(); err != nil {
		return ServiceJailSnapshot{}, false, err
	}
	if err := k.WriteServiceBondValue(ctx, types.NewServiceBondKey(operatorAddress), bond); err != nil {
		return ServiceJailSnapshot{}, false, err
	}
	// the data-structure contract lists jail and tombstone together, both as a path
	// that invalidates support and as a mutation that must go through the single
	// applyModelSupportMutation entry point: the support aggregates and the
	// ModelSupportByProfileIndex / ModelSupportExpiryIndex rows have to be updated
	// in the same transaction, with the already-counted support debited exactly
	// once. Doing this only on the tombstone branch left jail_count > 0 rows whose
	// SupportVoteWeight is no longer eligible while support_active and both stake
	// snapshots were still set, i.e. exactly the shape validateSupportGenesis
	// rejects — any chain that ever jailed an operator exported a genesis it could
	// not re-import.
	//
	// The operator->profiles direction is capped by
	// params.Support.MaxSupportedProfilesPerOperator (§6.1), so unlike the
	// profile->operators fan-out that P0-3 made asynchronous this scan is bounded
	// and safe to run inside the jailing transaction.
	//
	// Only the terminal branch ends the declarations. Running the full
	// deactivation on a plain jail as well is what made the first jail permanent:
	// it wiped declared_support, the joint filter of
	// the candidate selection contract admits candidates on a declared
	// support, and requireSupportScope refuses to re-declare while JAILED — so
	// §10.0c's jail_clear_normal_action_count normal actions could never be
	// performed and jail_count could never come back down. The graduated
	// candidate_jail_factor (the parameter table[hard boundary]: 1 -> 500000,
	// 2 -> 250000) is the
	// penalty the ladder is supposed to apply; exclusion belongs to the tombstone
	// step alone. suspendDeclaredSupportsForJail still discharges the §6.1 genesis
	// invariant that motivated deactivating here in the first place.
	//
	// reason: §9.6b registers no JAILED lifecycle reason and none may be invented
	// here. TOMBSTONED is the closest existing value — it is the same
	// operator-global fault escalation and the terminal step of the very counter
	// being raised here. It is also inert: DeactivateModelSupport ignores reason
	// (no state field and no event field carries it), so the choice is documentary
	// only.
	if tombstoned {
		if err := k.deactivateDeclaredSupports(ctx, operatorAddress, types.ModelSupportDeactivateTombstoned, height); err != nil {
			return ServiceJailSnapshot{}, false, err
		}
	} else if err := k.suspendDeclaredSupportsForJail(ctx, operatorAddress, height); err != nil {
		return ServiceJailSnapshot{}, false, err
	}
	if tombstoned {
		if err := k.invalidateCandidatePoolsForOperator(ctx, operatorAddress, height); err != nil {
			return ServiceJailSnapshot{}, false, err
		}
		if err := k.removeCortexIdentityOnTerminalBond(ctx, operatorAddress); err != nil {
			return ServiceJailSnapshot{}, false, err
		}
	}
	mustEmitHubEvent(ctx, &types.EventRoleJailed{
		Duty: dutyValue, Operator: operatorAddress,
		OldJailCount: oldJailCount, NewJailCount: bond.JailCount, Reason: reason,
	})
	return serviceJailSnapshot(bond, duty), tombstoned, nil
}

// AdvanceJailClearCounter records one completed protocol-assigned duty without
// a fault. §10.0c clears one jail_count every
// params.Service.JailClearNormalActionCount such actions. The only production
// caller is the RELEASED branch of closeTaskLiabilityReservation; the SLASHED
// branch must never reach here.
//
// height is threaded rather than read off the ambient sdk context because
// clearing the last jail_count re-snapshots the operator's suspended supports,
// which stamps profile updated_height — the same reason incJail and SetTombstone
// take it explicitly.
func (k Keeper) advanceJailClearCounter(ctx context.Context, operatorAddress string, duty shared.Duty, height uint64) (ServiceJailSnapshot, error) {
	_, operatorAddress, err := k.requireCanonicalAddress("operator_address", operatorAddress)
	if err != nil {
		return ServiceJailSnapshot{}, err
	}
	role, err := roleFromDuty(duty)
	if err != nil {
		return ServiceJailSnapshot{}, err
	}
	if height == 0 {
		return ServiceJailSnapshot{}, fmt.Errorf("jail clear height must be > 0")
	}
	bond, err := k.GetServiceBondState(ctx, operatorAddress)
	if err != nil {
		return ServiceJailSnapshot{}, err
	}
	if bond.Status == types.ServiceBondStatusTombstoned {
		return serviceJailSnapshot(bond, role), nil
	}
	if bond.JailCount == 0 {
		// Nothing to clear; do not let the counter grow without a jail so a
		// long-lived healthy operator cannot bank clears in advance.
		if bond.NormalActionCountSinceJail != 0 {
			return ServiceJailSnapshot{}, fmt.Errorf("unjailled service bond retains a normal-action counter")
		}
		return serviceJailSnapshot(bond, role), nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return ServiceJailSnapshot{}, err
	}
	threshold := params.Service.JailClearNormalActionCount
	if threshold == 0 {
		return ServiceJailSnapshot{}, fmt.Errorf("jail_clear_normal_action_count must be > 0")
	}
	oldJailCount := bond.JailCount
	bond.NormalActionCountSinceJail, err = checkedAddUint32(bond.NormalActionCountSinceJail, 1)
	if err != nil {
		return ServiceJailSnapshot{}, fmt.Errorf("normal_action_count_since_jail overflow: %w", err)
	}
	normalActionCount := bond.NormalActionCountSinceJail
	decremented := false
	if normalActionCount >= threshold {
		bond.NormalActionCountSinceJail = 0
		bond.JailCount--
		decremented = true
	}
	cleared := bond.JailCount == 0 && bond.Status == types.ServiceBondStatusJailed
	if cleared {
		// Leaving jail restores the live status that register/top-up produce.
		// Promotion to ACTIVE stays owned by support activation.
		bond.Status = types.ServiceBondStatusRegistered
		applyPostSlashBondStatus(&bond)
	}
	if err := bond.Validate(); err != nil {
		return ServiceJailSnapshot{}, err
	}
	if err := k.WriteServiceBondValue(ctx, types.NewServiceBondKey(operatorAddress), bond); err != nil {
		return ServiceJailSnapshot{}, err
	}
	// The bond write above is what makes SupportVoteWeight eligible again, so the
	// suspended snapshots have to be recomputed against it — in this transaction,
	// or the operator's exported genesis no longer re-imports. See
	// restoreSupportsAfterJailClear.
	if cleared && types.IsLiveServiceBondStatus(bond.Status) {
		anyActive, err := k.restoreSupportsAfterJailClear(ctx, operatorAddress, height)
		if err != nil {
			return ServiceJailSnapshot{}, err
		}
		if anyActive {
			bond.Status = types.ServiceBondStatusActive
		} else {
			bond.Status = types.ServiceBondStatusRegistered
		}
		if err := bond.Validate(); err != nil {
			return ServiceJailSnapshot{}, err
		}
		if err := k.WriteServiceBondValue(ctx, types.NewServiceBondKey(operatorAddress), bond); err != nil {
			return ServiceJailSnapshot{}, err
		}
	}
	if bond.Status == types.ServiceBondStatusExited {
		if err := k.removeCortexIdentityOnTerminalBond(ctx, operatorAddress); err != nil {
			return ServiceJailSnapshot{}, err
		}
	}
	if decremented {
		mustEmitHubEvent(ctx, &types.EventServiceJailRecovered{
			Operator: operatorAddress, OldJailCount: oldJailCount, NewJailCount: bond.JailCount,
			TriggerDuty: duty, NormalActionCount: normalActionCount, BondStatus: bond.Status,
		})
	}
	return serviceJailSnapshot(bond, role), nil
}

// GetJail projects the operator-global jail counters. duty is echoed for
// attribution only; there is no duty-scoped jail state.
func (k Keeper) GetJail(ctx context.Context, operatorAddress, duty string) (ServiceJailSnapshot, error) {
	duty = normalizeServiceRole(duty)
	bond, exists, err := k.loadServiceBond(ctx, operatorAddress)
	if err != nil {
		return ServiceJailSnapshot{}, err
	}
	if !exists {
		return ServiceJailSnapshot{OperatorAddress: strings.TrimSpace(operatorAddress), Duty: duty}, nil
	}
	return serviceJailSnapshot(bond, duty), nil
}

func serviceJailSnapshot(bond types.ServiceBondState, duty string) ServiceJailSnapshot {
	return ServiceJailSnapshot{
		OperatorAddress: bond.OperatorAddress,
		Duty:            duty,
		JailCount:       uint64(bond.JailCount),
		ClearCounter:    uint64(bond.NormalActionCountSinceJail),
		Tombstoned:      bond.Status == types.ServiceBondStatusTombstoned,
	}
}

// IsJailEjected reports the pool-ejection end of the jail ladder, i.e.
// the parameter table's "jail_count >= 3 leaves the pool" and the hard filter of
// the candidate selection contract's "jail_count has not reached the
// pool-ejection threshold".
// It deliberately does NOT report jail_count 1/2: those are demotions carried by
// candidate_jail_factor (500000/250000 ppm), not exclusions, and treating them as
// exclusions strands the recovery path — the API contract only
// decrements
// jail_count on the normal actions an excluded operator can never perform.
//
// incJail already writes TOMBSTONED when the count reaches the threshold, so the
// count test only matters when the two can disagree: a genesis import, or a
// threshold lowered under rows that were jailed at the old one. GetNodeJailStatus
// resolves the same disagreement the same way.
func (k Keeper) IsJailEjected(ctx context.Context, operatorAddress string) (bool, error) {
	bond, exists, err := k.loadServiceBond(ctx, operatorAddress)
	if err != nil || !exists {
		return false, err
	}
	if bond.Status == types.ServiceBondStatusTombstoned {
		return true, nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return false, err
	}
	threshold := params.Service.TombstoneJailCountThreshold
	return threshold != 0 && bond.JailCount >= threshold, nil
}

func (k Keeper) SetTombstone(ctx context.Context, roleAddress string, height uint64, reason string) error {
	_, roleAddress, err := k.requireCanonicalAddress("operator_address", roleAddress)
	if err != nil {
		return err
	}
	if height == 0 {
		return fmt.Errorf("tombstone height must be > 0")
	}
	if err := k.deactivateDeclaredSupports(ctx, roleAddress, types.ModelSupportDeactivateTombstoned, height); err != nil {
		return err
	}
	bond, exists, err := k.loadServiceBond(ctx, roleAddress)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("service bond %s not found", roleAddress)
	}
	if bond.Status != types.ServiceBondStatusTombstoned {
		bond.Status = types.ServiceBondStatusTombstoned
		if err := bond.Validate(); err != nil {
			return err
		}
		if err := k.WriteServiceBondValue(ctx, types.NewServiceBondKey(roleAddress), bond); err != nil {
			return err
		}
	}
	if err := k.invalidateCandidatePoolsForOperator(ctx, roleAddress, height); err != nil {
		return err
	}
	return k.removeCortexIdentityOnTerminalBond(ctx, roleAddress)
}

func (k Keeper) IsTombstoned(ctx context.Context, roleAddress string) (bool, error) {
	bond, exists, err := k.loadServiceBond(ctx, roleAddress)
	if err != nil || !exists {
		return false, err
	}
	return bond.Status == types.ServiceBondStatusTombstoned, nil
}

func shouldJailForFault(faultType string) bool {
	switch strings.TrimSpace(faultType) {
	case types.FaultTypeVerifierMiss, types.FaultTypeCommitNoResult, types.FaultTypeResultNoSettleReveal, types.FaultTypeValueOutlier, types.FaultTypeCommitRevealMismatch, types.FaultTypeWorkerRevealTimeout, types.FaultTypeObjectiveForgery, types.FaultTypeRoundDivergence:
		return true
	default:
		return false
	}
}

func shouldTombstoneForFault(faultType string) bool {
	return strings.TrimSpace(faultType) == types.FaultTypeObjectiveForgery
}

func eventLifecycleReasonForFault(faultType string) types.EventLifecycleReason {
	switch strings.TrimSpace(faultType) {
	case types.FaultTypeObjectiveForgery, types.FaultTypeCommitRevealMismatch, types.FaultTypeValueOutlier, types.FaultTypeRoundDivergence:
		return types.EventLifecycleReason_EVENT_LIFECYCLE_REASON_OBJECTIVE_FAULT
	default:
		return types.EventLifecycleReason_EVENT_LIFECYCLE_REASON_TIMEOUT
	}
}

// emitFaultRecordedEvent is §5.11 code 50. session_id is optional Hash32, so a
// non-hash debug session scope is reported as absent rather than coerced.
func emitFaultRecordedEvent(ctx context.Context, state types.RoleFaultState, sessionID string) {
	event := &types.EventFaultRecorded{
		Duty:      state.Duty,
		Operator:  state.OperatorAddress,
		FaultId:   append([]byte(nil), state.FaultId...),
		FaultKind: state.FaultClass,
	}
	if raw, err := decodePayloadHash32("session_id", sessionID); err == nil {
		event.XSessionIdOrEmpty = &types.EventFaultRecorded_SessionIdOrEmpty{SessionIdOrEmpty: raw}
	}
	if len(state.TaskId) == 32 {
		event.XTaskIdOrEmpty = &types.EventFaultRecorded_TaskIdOrEmpty{
			TaskIdOrEmpty: append([]byte(nil), state.TaskId...),
		}
	}
	mustEmitHubEvent(ctx, event)
}

// emitRoleSlashedEvent is §5.11 code 52.
func emitRoleSlashedEvent(ctx context.Context, duty shared.Duty, operatorAddress string, faultID []byte, slash ApplyServiceSlashResult, faultType string) {
	mustEmitHubEvent(ctx, &types.EventRoleSlashed{
		Duty:           duty,
		Operator:       operatorAddress,
		FaultId:        append([]byte(nil), faultID...),
		AppliedAmount:  shared.NewAmount(slash.Applied),
		UnfilledAmount: shared.NewAmount(slash.Unfilled),
		Reason:         eventLifecycleReasonForFault(faultType),
		BondVersion:    slash.BondVersion,
	})
}

func (k Keeper) buildRoleFaultState(ctx context.Context, operatorAddress, role, faultType, sessionID, taskID, evidenceDigestHex string, classificationSource shared.FailureClassificationSource, height uint64) (types.RoleFaultState, shared.Hash32Key, error) {
	operatorBytes, operatorAddress, err := k.requireCanonicalAddress("operator_address", operatorAddress)
	if err != nil {
		return types.RoleFaultState{}, nil, err
	}
	duty, err := dutyFromRole(role)
	if err != nil {
		return types.RoleFaultState{}, nil, err
	}
	taskBytes, err := decodePayloadHash32("task_id", taskID)
	if err != nil {
		return types.RoleFaultState{}, nil, err
	}
	faultClass, _, err := classifyRoleFaultType(faultType)
	if err != nil {
		return types.RoleFaultState{}, nil, err
	}
	if err := validateRoleFaultClassificationSource(faultType, classificationSource); err != nil {
		return types.RoleFaultState{}, nil, err
	}
	if height == 0 {
		return types.RoleFaultState{}, nil, errors.New("recorded height must be greater than 0")
	}
	// This is a consumer of the Task classification digest, never a second
	// producer. The Task finality gate and app startup invariant both prove the
	// exact task/digest relationship before this receipt can be pruned.
	evidenceDigest, err := decodePayloadHash32("evidence_digest", evidenceDigestHex)
	if err != nil {
		return types.RoleFaultState{}, nil, err
	}
	faultID, err := roleFaultID(
		sdk.UnwrapSDKContext(ctx).ChainID(), taskBytes, operatorBytes, duty, faultClass, evidenceDigest,
	)
	if err != nil {
		return types.RoleFaultState{}, nil, err
	}
	state := types.RoleFaultState{
		FaultId: append([]byte(nil), faultID...), TaskId: append([]byte(nil), taskBytes...),
		OperatorAddress: operatorAddress, Duty: duty, FaultClass: faultClass,
		ClassificationSource: classificationSource, EvidenceDigest: evidenceDigest, RecordedHeight: height,
		Status: types.RoleFaultStatus_ROLE_FAULT_STATUS_CONFIRMED,
	}
	if err := state.Validate(); err != nil {
		return types.RoleFaultState{}, nil, err
	}
	return state, faultID, nil
}

// roleFaultID is the TRUEOPEN_ROLE_FAULT_V1 identity of one recorded role fault.
//
// It is a free function taking already-decoded values so a golden vector can call it
// without a keeper: buildRoleFaultState above spends most of its body turning
// Bech32, role names and hex text into these six values, and none of that is
// consensus. The preimage is. Two of the six are adjacent EnumBE fields - duty and
// fault_class - and two more are opaque byte strings; while this call was inline
// nothing in the tree could see either pair swap.
func roleFaultID(
	chainID string, taskID, operatorAddress []byte,
	duty shared.Duty, faultClass types.FaultKind, evidenceDigest []byte,
) ([]byte, error) {
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainRoleFaultV1)).Raw(
		[]byte(chainID),
		taskID,
		operatorAddress,
		shared.EnumBE(uint32(duty)),
		shared.EnumBE(uint32(faultClass)),
		evidenceDigest,
	).Sum()
}

// decodePayloadHash32 is the one gate a hex identifier passes through on its way
// from a shared payload into a stored Hash32 field. It rejects uppercase and
// short forms by round-tripping rather than by only checking the length, so a
// payload cannot smuggle two spellings of the same digest into one store key.
// Named for the payload rather than for the fault path because the custody
// rows now decode through it too; it dies with the wire PR that types those
// payload fields as `bytes`.
func decodePayloadHash32(name, value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	raw, err := hex.DecodeString(value)
	if err != nil || len(raw) != 32 || hex.EncodeToString(raw) != value || bytes.Equal(raw, make([]byte, 32)) {
		return nil, fmt.Errorf("%s must be lowercase non-zero 32-byte hex", name)
	}
	return raw, nil
}

func classifyRoleFaultType(faultType string) (types.FaultKind, shared.FailureClassificationSource, error) {
	switch strings.TrimSpace(faultType) {
	case types.FaultTypeWorkerInferTimeout, types.FaultTypeVerifierMiss, types.FaultTypeCommitNoResult,
		types.FaultTypeResultNoSettleReveal, types.FaultTypeWorkerRevealTimeout:
		return types.FaultKind_FAULT_KIND_TIMEOUT, shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_DEADLINE, nil
	case types.FaultTypeValueOutlier:
		return types.FaultKind_FAULT_KIND_INVALID_RESULT, shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_SETTLEMENT, nil
	case types.FaultTypeCommitRevealMismatch:
		return types.FaultKind_FAULT_KIND_EQUIVOCATION, shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_SETTLEMENT, nil
	case types.FaultTypeObjectiveForgery:
		return types.FaultKind_FAULT_KIND_EQUIVOCATION, shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_VERIFICATION_ROUND, nil
	case types.FaultTypeRoundDivergence:
		// Wire v0.3 has no distinct ROUND_DIVERGENCE FaultKind; the frozen
		// invalid-result value is the only compatible role-fault carrier.
		return types.FaultKind_FAULT_KIND_INVALID_RESULT, shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_VERIFICATION_ROUND, nil
	default:
		return types.FaultKind_FAULT_KIND_UNSPECIFIED, shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_UNSPECIFIED, fmt.Errorf("unsupported fault_type %q", faultType)
	}
}

func validateRoleFaultClassificationSource(faultType string, source shared.FailureClassificationSource) error {
	_, defaultSource, err := classifyRoleFaultType(faultType)
	if err != nil {
		return err
	}
	if source == defaultSource {
		return nil
	}
	// A verifier deadline omission can be discovered either by the deadline
	// sweep or while a Tx-driven settlement freezes its facts.
	switch strings.TrimSpace(faultType) {
	case types.FaultTypeObjectiveForgery:
		if source == shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_OBJECTIVE_EVIDENCE {
			return nil
		}
	case types.FaultTypeVerifierMiss, types.FaultTypeCommitNoResult:
		if source == shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_SETTLEMENT {
			return nil
		}
	}
	return fmt.Errorf("fault_type %q cannot use classification_source %s", faultType, source.String())
}

func sameRoleFaultIdentity(left, right types.RoleFaultState) bool {
	return bytes.Equal(left.FaultId, right.FaultId) && bytes.Equal(left.TaskId, right.TaskId) &&
		left.OperatorAddress == right.OperatorAddress && left.Duty == right.Duty &&
		left.FaultClass == right.FaultClass && left.ClassificationSource == right.ClassificationSource &&
		bytes.Equal(left.EvidenceDigest, right.EvidenceDigest)
}

func sameRoleFault(left, right types.RoleFaultState) bool {
	return sameRoleFaultIdentity(left, right) && left.JailDelta == right.JailDelta &&
		bytes.Equal(left.GetSlashSummaryId(), right.GetSlashSummaryId()) &&
		left.RecordedHeight == right.RecordedHeight && left.Status == right.Status
}
