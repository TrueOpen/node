package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"strings"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

type DeclareModelSupportResult struct {
	Capability types.ProfileCapabilityState
	Support    types.ModelSupportState
	Status     shared.MutationStatusV1
}

func (k Keeper) DeclareModelSupport(
	ctx context.Context,
	operatorAddress, modelID string,
	profileVersion uint32,
	inferenceCapability, verificationCapability bool,
	height uint64,
) (DeclareModelSupportResult, error) {
	_, operatorAddress, err := k.requireCanonicalAddress("operator_address", operatorAddress)
	if err != nil {
		return DeclareModelSupportResult{}, err
	}
	modelID = strings.TrimSpace(modelID)
	if err := types.ValidateModelID(modelID); err != nil {
		return DeclareModelSupportResult{}, err
	}
	if profileVersion == 0 || (!inferenceCapability && !verificationCapability) {
		return DeclareModelSupportResult{}, fmt.Errorf("profile_version and at least one capability are required")
	}
	if height == 0 {
		return DeclareModelSupportResult{}, fmt.Errorf("height must be greater than 0")
	}

	params, err := k.Params.Get(ctx)
	if err != nil {
		return DeclareModelSupportResult{}, err
	}
	currentEpoch := epochForHeight(height, params.Epoch.EpochLengthBlocks)
	profile, err := k.requireSupportScope(ctx, operatorAddress, modelID, profileVersion, currentEpoch)
	if err != nil {
		return DeclareModelSupportResult{}, err
	}

	capability, capabilityExists, err := k.loadProfileCapability(ctx, operatorAddress, modelID, profileVersion)
	if err != nil {
		return DeclareModelSupportResult{}, err
	}
	oldSupport, supportExists, err := k.loadModelSupport(ctx, operatorAddress, modelID, profileVersion)
	if err != nil {
		return DeclareModelSupportResult{}, err
	}
	if capabilityExists {
		if err := capability.Validate(); err != nil {
			return DeclareModelSupportResult{}, err
		}
	}
	if supportExists {
		if err := oldSupport.Validate(); err != nil {
			return DeclareModelSupportResult{}, err
		}
	}
	// The operator cap counts stored support rows, not capability rows. A legacy
	// orphan capability must not make a new support row look like an update and
	// bypass max_supported_profiles_per_operator (B-5).
	if !supportExists {
		count, err := k.countOperatorModelSupports(ctx, operatorAddress, uint64(params.Support.MaxSupportedProfilesPerOperator)+1)
		if err != nil {
			return DeclareModelSupportResult{}, err
		}
		if count >= uint64(params.Support.MaxSupportedProfilesPerOperator) {
			return DeclareModelSupportResult{}, fmt.Errorf("operator support profiles must not exceed %d", params.Support.MaxSupportedProfilesPerOperator)
		}
	}
	if !capabilityExists {
		capability = types.ProfileCapabilityState{
			OperatorAddress: operatorAddress,
			ModelId:         modelID,
			ProfileVersion:  profileVersion,
		}
	}
	// FirstActivationDuty remains on the retained support row after deactivation
	// and Genesis requires it to be declared by the paired capability. Refusing a
	// narrowing declaration here prevents runtime from writing state that its own
	// Genesis validator rejects (B-23). Capability expansion and changes before
	// first activation remain allowed.
	if supportExists {
		switch oldSupport.FirstActivationDuty {
		case shared.DutyWorker:
			if !inferenceCapability {
				return DeclareModelSupportResult{}, fmt.Errorf("inference capability cannot be removed after WORKER support activation")
			}
		case shared.DutyVerifier:
			if !verificationCapability {
				return DeclareModelSupportResult{}, fmt.Errorf("verification capability cannot be removed after VERIFIER support activation")
			}
		}
	}
	capabilityChanged := !capabilityExists ||
		capability.InferenceCapability != inferenceCapability ||
		capability.VerificationCapability != verificationCapability
	supportLive := supportExists && oldSupport.DeclaredSupport && currentEpoch < oldSupport.SupportFreshUntilEpoch
	if supportLive && !capabilityChanged {
		return DeclareModelSupportResult{
			Capability: capability,
			Support:    oldSupport,
			Status:     shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP,
		}, nil
	}
	freshUntil := oldSupport.SupportFreshUntilEpoch
	if !supportLive {
		freshUntil, err = checkedAdd(currentEpoch, uint64(params.Support.SupportWindowEpochs))
		if err != nil {
			return DeclareModelSupportResult{}, err
		}
	}
	if capabilityChanged {
		if capability.CapabilityVersion == math.MaxUint64 {
			return DeclareModelSupportResult{}, fmt.Errorf("profile capability version overflow")
		}
		capability.CapabilityVersion++
		capability.InferenceCapability = inferenceCapability
		capability.VerificationCapability = verificationCapability
	}
	if err := capability.Validate(); err != nil {
		return DeclareModelSupportResult{}, err
	}

	newSupport := oldSupport
	if !supportExists {
		newSupport = types.ModelSupportState{
			OperatorAddress: operatorAddress,
			ModelId:         modelID,
			ProfileVersion:  profileVersion,
			ActivationKind:  types.ModelSupportActivationNone,
		}
	}
	if newSupport.SupportVersion == math.MaxUint64 {
		return DeclareModelSupportResult{}, fmt.Errorf("model support version overflow")
	}
	newSupport.DeclaredSupport = true
	if !supportLive {
		newSupport.SupportFreshUntilEpoch = freshUntil
		newSupport.LastRefreshHeight = height
		newSupport.LastRefreshTaskId = nil
	}
	newSupport.SupportVersion++
	newSupport.EligibleSupportStakeSnapshot = 0
	newSupport.ActiveSupportStakeSnapshot = 0
	eligibleWeight, eligible, err := k.deriveSupportWeight(ctx, profile, capability, newSupport, currentEpoch)
	if err != nil {
		return DeclareModelSupportResult{}, err
	}
	if eligible {
		newSupport.EligibleSupportStakeSnapshot = eligibleWeight
		if newSupport.ActivationKind != types.ModelSupportActivationNone {
			newSupport.SupportActive = true
			newSupport.ActiveSupportStakeSnapshot = eligibleWeight
		} else {
			newSupport.SupportActive = false
		}
	} else {
		newSupport.SupportActive = false
	}
	if err := newSupport.Validate(); err != nil {
		return DeclareModelSupportResult{}, err
	}
	if err := k.applyModelSupportMutation(ctx, optionalSupport(oldSupport, supportExists), newSupport, capability, currentEpoch, height); err != nil {
		return DeclareModelSupportResult{}, err
	}
	if err := k.syncLiveServiceBondStatus(ctx, operatorAddress); err != nil {
		return DeclareModelSupportResult{}, err
	}
	return DeclareModelSupportResult{Capability: capability, Support: newSupport, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED}, nil
}

// RecordTaskSupportCompletion applies the support consequence of one fault-free
// task duty. The Task caller supplies only immutable task facts; Hub verifies the
// released liability and derives P30, support weight, lifecycle status and events.
// errSupportActivationNotApplicable marks a §10.9 *condition* that is simply not
// met, as opposed to a failure.
//
// §10.9 lists conditions 2-4 (declared support matches the task's model/profile,
// the assigned duty's capability is true, the support row is still fresh) as
// prerequisites for activation, and the function below already expresses two
// other conditions — a repeat of the same task, and an order_value under the P30
// cutoff — by returning nil. The remaining ones used to return an error instead,
// which made them fatal to the caller.
//
// That distinction matters because the only caller is Task settlement. Every one
// of these conditions can stop holding between assignment and settlement: a
// Cortex node can be tombstoned, revoke its service key, unstake, let its support
// window lapse, or have its model frozen. Treating that as an error meant one
// such node among the paid roles would abort the settlement transaction — and,
// since the settlement is deterministic, abort it again on every retry, leaving
// the task permanently unsettleable. Real corruption (a malformed fact, a missing
// RELEASED liability, an overflow, a store error) still propagates.
var errSupportActivationNotApplicable = errors.New("task support activation conditions are not met")

func (k Keeper) RecordTaskSupportCompletion(ctx context.Context, fact types.TaskSupportCompletionFact) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	if err := k.recordTaskSupportCompletion(cache, fact); err != nil {
		if errors.Is(err, errSupportActivationNotApplicable) {
			// Nothing is written: the cache is dropped rather than committed.
			return nil
		}
		return err
	}
	write()
	return nil
}

func (k Keeper) recordTaskSupportCompletion(ctx context.Context, fact types.TaskSupportCompletionFact) error {
	if len(fact.TaskID) != shared.Hash32KeySize || bytes.Equal(fact.TaskID, make([]byte, shared.Hash32KeySize)) ||
		fact.ProfileVersion == 0 || fact.OrderValue == 0 || fact.Height == 0 || !types.IsValidDuty(fact.Duty) ||
		!validRewardBucket(fact.RewardBucket) {
		return fmt.Errorf("task support completion fact is incomplete")
	}
	_, operatorAddress, err := k.requireCanonicalAddress("operator_address", fact.OperatorAddress)
	if err != nil {
		return err
	}
	modelID := strings.TrimSpace(fact.ModelID)
	if modelID != fact.ModelID {
		return fmt.Errorf("task support completion model_id is not canonical")
	}
	if err := types.ValidateModelID(modelID); err != nil {
		return err
	}
	liability, err := k.TaskLiabilityReservation.Get(
		ctx, types.NewTaskLiabilityReservationKey(fact.TaskID, fact.Duty, operatorAddress),
	)
	if err != nil {
		return fmt.Errorf("released task liability is required: %w", err)
	}
	if liability.Status != types.TaskLiabilityStatusReleased || !bytes.Equal(liability.TaskId, fact.TaskID) ||
		liability.OperatorAddress != operatorAddress || liability.Duty != fact.Duty {
		return fmt.Errorf("task support completion does not match a RELEASED liability")
	}

	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	currentEpoch := epochForHeight(fact.Height, params.Epoch.EpochLengthBlocks)
	profile, err := k.requireSupportScope(ctx, operatorAddress, modelID, fact.ProfileVersion, currentEpoch)
	if err != nil {
		// Scope covers "is this operator/model/profile still eligible to support at
		// all" — an activation condition, not a settlement failure.
		return fmt.Errorf("%w: %s", errSupportActivationNotApplicable, err.Error())
	}
	capability, err := k.GetProfileCapabilityState(ctx, operatorAddress, modelID, fact.ProfileVersion)
	if err != nil {
		return fmt.Errorf("%w: %s", errSupportActivationNotApplicable, err.Error())
	}
	if fact.Duty == shared.DutyWorker && !capability.InferenceCapability {
		return fmt.Errorf("%w: worker duty without inference capability", errSupportActivationNotApplicable)
	}
	if fact.Duty == shared.DutyVerifier && !capability.VerificationCapability {
		return fmt.Errorf("%w: verifier duty without verification capability", errSupportActivationNotApplicable)
	}
	oldSupport, err := k.GetModelSupportState(ctx, operatorAddress, modelID, fact.ProfileVersion)
	if err != nil {
		return fmt.Errorf("%w: %s", errSupportActivationNotApplicable, err.Error())
	}
	if !oldSupport.DeclaredSupport || oldSupport.SupportFreshUntilEpoch == 0 || currentEpoch >= oldSupport.SupportFreshUntilEpoch {
		return fmt.Errorf("%w: declared support is absent or stale", errSupportActivationNotApplicable)
	}
	if bytes.Equal(oldSupport.LastRefreshTaskId, fact.TaskID) {
		return nil
	}
	if oldSupport.LastRefreshHeight > fact.Height {
		return nil
	}
	if fact.Duty == shared.DutyWorker {
		if err := k.recordRewardCompetitionSample(ctx, fact, currentEpoch, params); err != nil {
			return err
		}
	}
	// Completion delivery is monotonic by finality height. Once a newer task (or
	// another task in the same block) refreshed the row, an older completion can
	// no longer change freshness or support_version. This closes A -> B -> A
	// replay without adding a second per-task receipt store.
	if oldSupport.LastRefreshHeight >= fact.Height {
		return nil
	}

	firstActivation := oldSupport.ActivationKind == types.ModelSupportActivationNone
	var p30CutoffEpoch uint64
	var p30Bootstrap bool
	if firstActivation && fact.Duty == shared.DutyWorker {
		var cutoff uint64
		p30CutoffEpoch, p30Bootstrap, cutoff, err = k.supportP30Cutoff(ctx, fact.RewardBucket, currentEpoch, params.Reward)
		if err != nil {
			return err
		}
		if fact.OrderValue < cutoff {
			return nil
		}
	}

	freshUntil, err := checkedAdd(currentEpoch, uint64(params.Support.SupportWindowEpochs))
	if err != nil {
		return err
	}
	newSupport := oldSupport
	if newSupport.SupportVersion == math.MaxUint64 {
		return fmt.Errorf("model support version overflow")
	}
	newSupport.SupportVersion++
	newSupport.SupportFreshUntilEpoch = freshUntil
	newSupport.LastRefreshHeight = fact.Height
	newSupport.LastRefreshTaskId = append([]byte(nil), fact.TaskID...)
	if firstActivation {
		newSupport.FirstActivationDuty = fact.Duty
		newSupport.FirstSupportTaskId = append([]byte(nil), fact.TaskID...)
		newSupport.FirstSupportOrderValue = fact.OrderValue
		if fact.Duty == shared.DutyWorker {
			newSupport.ActivationKind = types.ModelSupportActivationP30OrderValue
			if p30Bootstrap {
				newSupport.P30Source = &types.ModelSupportState_P30Bootstrap{P30Bootstrap: true}
			} else {
				newSupport.P30Source = &types.ModelSupportState_P30CutoffEpoch{P30CutoffEpoch: p30CutoffEpoch}
			}
		} else {
			newSupport.ActivationKind = types.ModelSupportActivationVerifierAssignedValid
			newSupport.P30Source = nil
		}
	}
	newSupport.SupportActive = false
	newSupport.ActiveSupportStakeSnapshot = 0
	newSupport.EligibleSupportStakeSnapshot = 0
	weight, eligible, err := k.deriveSupportWeight(ctx, profile, capability, newSupport, currentEpoch)
	if err != nil {
		return err
	}
	if eligible {
		newSupport.SupportActive = true
		newSupport.ActiveSupportStakeSnapshot = weight
		newSupport.EligibleSupportStakeSnapshot = weight
	} else {
		bond, err := k.GetServiceBondState(ctx, operatorAddress)
		if err != nil {
			return fmt.Errorf("%w: %s", errSupportActivationNotApplicable, err.Error())
		}
		if bond.Status != types.ServiceBondStatusJailed || bond.JailCount == 0 {
			// §10.9 step 6 branches on jail: still JAILED keeps the activation proof
			// with support_active=false, otherwise the row is written "according to
			// current eligibility".
			// An operator that is neither eligible nor jailed — slashed below
			// min_stake or unbonding since the task was assigned — is simply not
			// activating, and the deactivation itself is owned by
			// DeactivateModelSupport's single mutation entry point rather than by
			// this path. Erroring here would abort the settlement that paid it.
			return fmt.Errorf("%w: operator is neither eligible nor jailed", errSupportActivationNotApplicable)
		}
	}
	if err := newSupport.Validate(); err != nil {
		return err
	}
	if err := k.applyModelSupportMutation(ctx, &oldSupport, newSupport, capability, currentEpoch, fact.Height); err != nil {
		return err
	}
	if err := k.syncLiveServiceBondStatus(ctx, operatorAddress); err != nil {
		return err
	}
	if firstActivation {
		mustEmitHubEvent(ctx, &types.EventModelSupportActivated{
			Operator: operatorAddress, ModelId: modelID, ProfileVersion: fact.ProfileVersion,
			ActivationTaskId: append([]byte(nil), fact.TaskID...), ActivationDuty: fact.Duty,
			SupportVersion: newSupport.SupportVersion, ExpiryEpoch: newSupport.SupportFreshUntilEpoch,
		})
	}
	return nil
}

func (k Keeper) supportP30Cutoff(
	ctx context.Context,
	bucket types.RewardBucket,
	settlementEpoch uint64,
	params types.RewardParamsV1,
) (uint64, bool, uint64, error) {
	pointer, err := k.RewardP30CutoffPointer.Get(ctx, uint64(bucket))
	if errors.Is(err, collections.ErrNotFound) {
		cutoff, parseErr := shared.ParseAmount(params.P30BootstrapOrderValueFloor)
		if parseErr != nil || cutoff == 0 {
			return 0, false, 0, fmt.Errorf("p30 bootstrap cutoff is invalid")
		}
		return 0, true, cutoff, nil
	}
	if err != nil {
		return 0, false, 0, err
	}
	if pointer.RewardBucket != bucket || pointer.CutoffEpoch >= settlementEpoch {
		return 0, false, 0, fmt.Errorf("latest closed P30 pointer is outside the settlement history")
	}
	cutoff, err := shared.ParseAmount(pointer.P30Cutoff)
	if err != nil || cutoff == 0 {
		return 0, false, 0, fmt.Errorf("latest closed P30 pointer cutoff is invalid")
	}
	return pointer.CutoffEpoch, false, cutoff, nil
}

func (k Keeper) DeactivateModelSupport(ctx context.Context, operatorAddress, modelID string, profileVersion uint32, reason string, height uint64) (types.ModelSupportState, error) {
	state, err := k.deactivateModelSupport(ctx, operatorAddress, modelID, profileVersion, reason, height)
	if err != nil {
		return types.ModelSupportState{}, err
	}
	if err := k.syncLiveServiceBondStatus(ctx, state.OperatorAddress); err != nil {
		return types.ModelSupportState{}, err
	}
	return state, nil
}

func (k Keeper) deactivateModelSupport(ctx context.Context, operatorAddress, modelID string, profileVersion uint32, reason string, height uint64) (types.ModelSupportState, error) {
	oldSupport, err := k.GetModelSupportState(ctx, operatorAddress, modelID, profileVersion)
	if err != nil {
		return types.ModelSupportState{}, err
	}
	if !oldSupport.DeclaredSupport && !oldSupport.SupportActive && oldSupport.ActiveSupportStakeSnapshot == 0 && oldSupport.EligibleSupportStakeSnapshot == 0 {
		return oldSupport, nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return types.ModelSupportState{}, err
	}
	currentEpoch := epochForHeight(height, params.Epoch.EpochLengthBlocks)
	capability, err := k.GetProfileCapabilityState(ctx, operatorAddress, modelID, profileVersion)
	if err != nil {
		return types.ModelSupportState{}, err
	}
	newSupport := oldSupport
	if newSupport.SupportVersion == math.MaxUint64 {
		return types.ModelSupportState{}, fmt.Errorf("model support version overflow")
	}
	newSupport.SupportVersion++
	newSupport.SupportActive = false
	newSupport.ActiveSupportStakeSnapshot = 0
	newSupport.EligibleSupportStakeSnapshot = 0
	// Deactivation ends the operator's standing declaration regardless of the
	// reason, including SUPPORT_EXPIRED. Keeping declared_support set on an
	// expired row would let refreshModelSupport (which only requires
	// declared_support && activation_kind != NONE) revive a lapsed declaration
	// from a single daily confirmation, without the operator re-running
	// MsgDeclareModelSupport. Freshness is the whole point of the expiry sweep,
	// so the declaration must be re-established explicitly.
	newSupport.DeclaredSupport = false
	newSupport.SupportFreshUntilEpoch = 0
	if err := newSupport.Validate(); err != nil {
		return types.ModelSupportState{}, err
	}
	// The retention/prune index entry is written by WriteModelSupportIndexes
	// (the single index writer) because the deactivated row no longer qualifies
	// for the expiry index. Writing it here as well would duplicate the rule.
	if err := k.applyModelSupportMutation(ctx, &oldSupport, newSupport, capability, currentEpoch, height); err != nil {
		return types.ModelSupportState{}, err
	}
	return newSupport, nil
}

// suspendModelSupportForJail clears the active half of a support row while
// leaving the declaration standing. It is the non-terminal counterpart of
// DeactivateModelSupport, for jail rather than tombstone.
//
// §6.1 requires jail to run through applyModelSupportMutation for a concrete
// reason: a row left at support_active=true with jail_count > 0 is exactly the
// shape validateSupportGenesis rejects, so a chain that ever jailed an operator
// exported a genesis it could not re-import. Zeroing support_active and both
// stake snapshots is what discharges that invariant.
//
// Clearing declared_support is not required by it, and doing so as well is what
// made jail permanent. The joint filter of
// candidate_selection_and_performance_score.md §4 admits a candidate on a declared
// support, so wiping the declaration removed the operator from every candidate
// pool, and requireSupportScope refuses to re-declare while the bond is JAILED.
// keeper_api_contract.md §10.0c then only decrements jail_count after
// jail_clear_normal_action_count normal actions the operator can no longer
// perform. Keeping the declaration leaves the Worker path open — task_candidate_fact
// tests declared_support alone — at the reduced candidate_jail_factor, which is
// the graduated penalty parameter_table.md registers as the [hard boundary].
//
// Recovery stays gated rather than free: refreshModelSupport restores
// support_active only through requireSupportScope (which rejects a JAILED bond)
// and deriveSupportWeight (SupportVoteWeight excludes jail_count != 0), so the
// active half comes back only once the ladder has actually been walked to 0.
// Freshness is deliberately preserved too: the expiry sweep still owns it, and a
// row that lapses while jailed is deactivated in full by that sweep, as before.
func (k Keeper) suspendModelSupportForJail(ctx context.Context, operatorAddress, modelID string, profileVersion uint32, height uint64) error {
	oldSupport, err := k.GetModelSupportState(ctx, operatorAddress, modelID, profileVersion)
	if err != nil {
		return err
	}
	if !oldSupport.SupportActive && oldSupport.ActiveSupportStakeSnapshot == 0 && oldSupport.EligibleSupportStakeSnapshot == 0 {
		return nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	capability, err := k.GetProfileCapabilityState(ctx, operatorAddress, modelID, profileVersion)
	if err != nil {
		return err
	}
	newSupport := oldSupport
	if newSupport.SupportVersion == math.MaxUint64 {
		return fmt.Errorf("model support version overflow")
	}
	newSupport.SupportVersion++
	newSupport.SupportActive = false
	newSupport.ActiveSupportStakeSnapshot = 0
	newSupport.EligibleSupportStakeSnapshot = 0
	if err := newSupport.Validate(); err != nil {
		return err
	}
	return k.applyModelSupportMutation(
		ctx, &oldSupport, newSupport, capability,
		epochForHeight(height, params.Epoch.EpochLengthBlocks), height,
	)
}

// restoreSupportsAfterJailClear is suspendModelSupportForJail's mirror, run when
// AdvanceJailClearCounter walks jail_count back to 0.
//
// It is not optional bookkeeping. SupportVoteWeight returns eligible=false while
// IsLiveServiceBondStatus is false or jail_count != 0, so a suspended row's zero
// snapshots agree with the recomputation for exactly as long as the jail lasts.
// The moment the counter clears, the same row recomputes to a non-zero weight,
// and genesis.go's "eligible_support_stake_snapshot does not match recomputed
// support vote weight" check rejects it — the export/import symmetry that
// motivated zeroing the snapshots at jail time breaks at the other end unless
// they are put back.
//
// The reactivation rule is DeclareModelSupport's, not a new one: a row that was
// activated before (activation_kind != NONE) becomes active again, and one that
// never earned activation stays declared-and-eligible until a completed task
// promotes it. So this restores what jail suspended and nothing more.
//
// The scan is bounded by params.Support.MaxSupportedProfilesPerOperator (§6.1),
// the same bound the jail-time scan relies on, and it only runs on the rare
// transition to jail_count == 0.
func (k Keeper) restoreSupportsAfterJailClear(ctx context.Context, operatorAddress string, height uint64) (bool, error) {
	supports, err := k.collectDeclaredSupports(ctx, operatorAddress, func(types.ProfileState) bool { return true })
	if err != nil || len(supports) == 0 {
		return false, err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return false, err
	}
	currentEpoch := epochForHeight(height, params.Epoch.EpochLengthBlocks)
	anyActive := false
	for _, oldSupport := range supports {
		newSupport, _, err := k.reweightModelSupport(ctx, oldSupport, currentEpoch, height)
		if err != nil {
			return false, err
		}
		anyActive = anyActive || newSupport.SupportActive
	}
	return anyActive, nil
}

// EnqueueSupportDeactivation records exactly one bounded support-deactivation
// work item for (model_id, profile_version). It replaces the former inline
// operator fan-out in the MsgSetProfileStatus / MsgSetModelStatus / freeze
// transactions (P0-3): the operator dimension of
// ModelSupportByProfileIndex(model_id, profile_version, operator) has no cap
// whatsoever, while the operator->profiles direction is bounded by
// max_supported_profiles_per_operator. Doing the scan inside a Tx therefore made
// block gas a function of an unbounded supporter set.
//
// Safety: freezing takes effect immediately and does not depend on this cursor.
// deriveSupportWeight returns eligible=false as soon as the parent model or the
// profile leaves REGISTERED/ACTIVE (see the isParentModelOpenForProfile /
// isProfileOpenForOrders guard in deriveSupportWeight), so no frozen profile can
// admit, refresh or re-weight a supporter after the status write lands. The
// per-row work the cursor performs later is purely bookkeeping: it zeroes the
// ModelSupportState snapshots so the ProfileState aggregates
// (active_supporter_count / active_support_stake / eligible_support_stake) match
// the rows again. That convergence is safe to defer across blocks.
//
// exact replay: the cursor key is (model_id, profile_version), so resubmitting
// the same Msg never creates a second cursor and never rewinds an in-flight one.
func (k Keeper) EnqueueSupportDeactivation(ctx context.Context, modelID string, profileVersion uint32, reason string, height uint64) error {
	modelID = strings.TrimSpace(modelID)
	if err := types.ValidateModelID(modelID); err != nil {
		return err
	}
	if profileVersion == 0 {
		return fmt.Errorf("profile_version is required")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || reason == types.ModelSupportDeactivateExpired {
		return fmt.Errorf("support deactivation cursor reason %q is not a governance/freeze reason", reason)
	}
	cursorKey := types.NewProfileStateKey(modelID, profileVersion)
	if _, err := k.SupportDeactivateCursor.Get(ctx, cursorKey); err == nil {
		return nil
	} else if !errors.Is(err, collections.ErrNotFound) {
		return err
	}
	return k.SupportDeactivateCursor.Set(ctx, cursorKey, types.SupportDeactivateCursorState{
		ModelId:        modelID,
		ProfileVersion: profileVersion,
		Reason:         reason,
		EnqueuedHeight: height,
	})
}

// EnqueueModelSupportDeactivation fans one model-wide status change out to one
// cursor per registered profile. The profile dimension is bounded by
// types.MaxProfilesPerModel, so this enumeration is safe inside a Tx; only the
// operator dimension underneath each profile is unbounded and therefore async.
func (k Keeper) EnqueueModelSupportDeactivation(ctx context.Context, modelID, reason string, height uint64) error {
	modelID = strings.TrimSpace(modelID)
	if err := types.ValidateModelID(modelID); err != nil {
		return err
	}
	versions, err := k.modelProfileVersions(ctx, modelID)
	if err != nil {
		return err
	}
	for _, version := range versions {
		if err := k.EnqueueSupportDeactivation(ctx, modelID, version, reason, height); err != nil {
			return err
		}
	}
	return nil
}

// modelProfileVersions collects every registered profile version of one model.
// The iterator is fully drained and closed before the caller mutates anything, so
// no write happens under an open iterator. Growth is bounded by
// types.MaxProfilesPerModel, which RegisterModelProfileState already enforces.
func (k Keeper) modelProfileVersions(ctx context.Context, modelID string) ([]uint32, error) {
	iter, err := k.Profile.Iterate(ctx, collections.NewPrefixedPairRange[string, string](modelID))
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	versions := make([]uint32, 0, types.MaxProfilesPerModel)
	for ; iter.Valid(); iter.Next() {
		profile, err := iter.Value()
		if err != nil {
			return nil, err
		}
		if profile.ProfileVersion == 0 {
			return nil, fmt.Errorf("invalid stored profile version 0 for model %s", modelID)
		}
		if uint32(len(versions)) >= types.MaxProfilesPerModel {
			return nil, fmt.Errorf("model %s profiles must not exceed %d", modelID, types.MaxProfilesPerModel)
		}
		versions = append(versions, profile.ProfileVersion)
	}
	return versions, nil
}

// nextProfileSupportOperator resumes the operator scan of one profile from
// lastOperatorAddress (exclusive). An empty lastOperatorAddress starts at the
// first indexed operator. Returning one key per call keeps the caller in charge
// of the visited-item and serialized-bytes budgets.
func (k Keeper) nextProfileSupportOperator(ctx context.Context, modelID string, profileVersion uint32, lastOperatorAddress string) (string, bool, error) {
	rng := new(collections.Range[types.ModelSupportByProfileIndexKeyTriple]).
		Prefix(collections.TripleSuperPrefix[string, uint32, string](modelID, profileVersion))
	if lastOperatorAddress != "" {
		rng = rng.StartExclusive(types.NewModelSupportByProfileIndexKey(modelID, profileVersion, lastOperatorAddress))
	}
	iter, err := k.ModelSupportByProfileIndex.Iterate(ctx, rng)
	if err != nil {
		return "", false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return "", false, nil
	}
	key, err := iter.Key()
	if err != nil {
		return "", false, err
	}
	return key.K3(), true, nil
}

// WriteModelSupportIndexes is the single writer for every ModelSupportState side
// index (P1-11). Genesis import and every runtime mutation must go through it so
// a rebuilt store is identical to a runtime-built one:
//
//   - ByProfile/ByOperator are written unconditionally. They are the reverse
//     lookups used by countOperatorModelSupports (which enforces
//     max_supported_profiles_per_operator) and collectAndDeactivateSupports, so
//     a row that exists but is invisible to them would still consume an operator
//     slot while never being swept.
//   - ExpiryIndex only accepts rows that are still fresh. Indexing an already
//     expired freshness epoch just burns ProcessExpiredModelSupports budget on a
//     stale row every block.
//   - Every other row (undeclared or already expired) goes to the prune index so
//     it has a bounded cleanup path instead of living forever.
func (k Keeper) WriteModelSupportIndexes(ctx context.Context, state types.ModelSupportState, currentEpoch, retentionEpochs uint64) error {
	if err := k.ModelSupportByProfileIndex.Set(ctx, types.NewModelSupportByProfileIndexKey(state.ModelId, state.ProfileVersion, state.OperatorAddress)); err != nil {
		return err
	}
	if err := k.ModelSupportByOperatorIndex.Set(ctx, types.NewModelSupportByOperatorIndexKey(state.OperatorAddress, state.ModelId, state.ProfileVersion)); err != nil {
		return err
	}
	if state.DeclaredSupport && state.SupportFreshUntilEpoch > currentEpoch {
		return k.ModelSupportExpiryIndex.Set(ctx, types.NewModelSupportExpiryIndexKey(state.SupportFreshUntilEpoch, state.OperatorAddress, state.ModelId, state.ProfileVersion))
	}
	pruneEpoch, err := checkedAdd(currentEpoch, retentionEpochs)
	if err != nil {
		return err
	}
	return k.ModelSupportPruneIndex.Set(ctx, types.NewModelSupportPruneIndexKey(pruneEpoch, state.OperatorAddress, state.ModelId, state.ProfileVersion))
}

func (k Keeper) refreshModelSupport(ctx context.Context, operatorAddress, modelID string, profileVersion uint32, epoch, height uint64) (types.ModelSupportState, bool, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return types.ModelSupportState{}, false, err
	}
	profile, err := k.requireSupportScope(ctx, operatorAddress, modelID, profileVersion, epoch)
	if err != nil {
		return types.ModelSupportState{}, false, err
	}
	capability, err := k.GetProfileCapabilityState(ctx, operatorAddress, modelID, profileVersion)
	if err != nil {
		return types.ModelSupportState{}, false, err
	}
	oldSupport, err := k.GetModelSupportState(ctx, operatorAddress, modelID, profileVersion)
	if err != nil {
		return types.ModelSupportState{}, false, err
	}
	if !oldSupport.DeclaredSupport || oldSupport.ActivationKind == types.ModelSupportActivationNone {
		return types.ModelSupportState{}, false, fmt.Errorf("support must be declared and activated before daily refresh")
	}
	freshUntil, err := checkedAdd(epoch, uint64(params.Support.SupportWindowEpochs))
	if err != nil {
		return types.ModelSupportState{}, false, err
	}
	if oldSupport.SupportActive && oldSupport.SupportFreshUntilEpoch == freshUntil {
		return oldSupport, false, nil
	}
	newSupport := oldSupport
	if newSupport.SupportVersion == math.MaxUint64 {
		return types.ModelSupportState{}, false, fmt.Errorf("model support version overflow")
	}
	newSupport.SupportVersion++
	newSupport.SupportFreshUntilEpoch = freshUntil
	newSupport.LastRefreshHeight = height
	newSupport.ActiveSupportStakeSnapshot = 0
	newSupport.EligibleSupportStakeSnapshot = 0
	weight, eligible, err := k.deriveSupportWeight(ctx, profile, capability, newSupport, epoch)
	if err != nil {
		return types.ModelSupportState{}, false, err
	}
	if eligible {
		newSupport.SupportActive = true
		newSupport.ActiveSupportStakeSnapshot = weight
		newSupport.EligibleSupportStakeSnapshot = weight
	} else {
		bond, err := k.GetServiceBondState(ctx, operatorAddress)
		if err != nil {
			return types.ModelSupportState{}, false, err
		}
		if bond.Status != types.ServiceBondStatusJailed || bond.JailCount == 0 {
			return types.ModelSupportState{}, false, fmt.Errorf("operator is not currently eligible for profile support")
		}
		newSupport.SupportActive = false
	}
	if err := newSupport.Validate(); err != nil {
		return types.ModelSupportState{}, false, err
	}
	if err := k.applyModelSupportMutation(ctx, &oldSupport, newSupport, capability, epoch, height); err != nil {
		return types.ModelSupportState{}, false, err
	}
	if err := k.syncLiveServiceBondStatus(ctx, operatorAddress); err != nil {
		return types.ModelSupportState{}, false, err
	}
	return newSupport, true, nil
}

func (k Keeper) applyModelSupportMutation(
	ctx context.Context,
	oldSupport *types.ModelSupportState,
	newSupport types.ModelSupportState,
	capability types.ProfileCapabilityState,
	currentEpoch, height uint64,
) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	profile, err := k.GetProfile(ctx, newSupport.ModelId, newSupport.ProfileVersion)
	if err != nil {
		return err
	}
	oldStatus := modelSupportStatus(oldSupport, currentEpoch)
	if oldSupport != nil {
		if profile.EligibleSupportStake < oldSupport.EligibleSupportStakeSnapshot || profile.ActiveSupportStake < oldSupport.ActiveSupportStakeSnapshot {
			return fmt.Errorf("profile support aggregate underflow")
		}
		profile.EligibleSupportStake -= oldSupport.EligibleSupportStakeSnapshot
		profile.ActiveSupportStake -= oldSupport.ActiveSupportStakeSnapshot
		if oldSupport.ActiveSupportStakeSnapshot > 0 {
			if profile.ActiveSupporterCount == 0 {
				return fmt.Errorf("profile active supporter count underflow")
			}
			profile.ActiveSupporterCount--
		}
		if oldSupport.SupportFreshUntilEpoch > 0 {
			oldExpiry := types.NewModelSupportExpiryIndexKey(oldSupport.SupportFreshUntilEpoch, oldSupport.OperatorAddress, oldSupport.ModelId, oldSupport.ProfileVersion)
			if err := k.ModelSupportExpiryIndex.Remove(ctx, oldExpiry); err != nil && !errors.Is(err, collections.ErrNotFound) {
				return err
			}
		}
	}
	if math.MaxUint64-profile.EligibleSupportStake < newSupport.EligibleSupportStakeSnapshot || math.MaxUint64-profile.ActiveSupportStake < newSupport.ActiveSupportStakeSnapshot {
		return fmt.Errorf("profile support aggregate overflow")
	}
	profile.EligibleSupportStake += newSupport.EligibleSupportStakeSnapshot
	profile.ActiveSupportStake += newSupport.ActiveSupportStakeSnapshot
	if newSupport.ActiveSupportStakeSnapshot > 0 {
		if profile.ActiveSupporterCount == math.MaxUint32 {
			return fmt.Errorf("profile active supporter count overflow")
		}
		profile.ActiveSupporterCount++
	}
	if err := k.deriveProfileAndModelStatus(ctx, &profile, height); err != nil {
		return err
	}
	if err := capability.Validate(); err != nil {
		return err
	}
	if err := newSupport.Validate(); err != nil {
		return err
	}
	key := types.NewModelSupportKey(newSupport.OperatorAddress, newSupport.ModelId, newSupport.ProfileVersion)
	if err := k.ProfileCapability.Set(ctx, types.NewProfileCapabilityKey(newSupport.OperatorAddress, newSupport.ModelId, newSupport.ProfileVersion), capability); err != nil {
		return err
	}
	if err := k.ModelSupport.Set(ctx, key, newSupport); err != nil {
		return err
	}
	if err := k.WriteModelSupportIndexes(ctx, newSupport, currentEpoch, uint64(params.Support.ModelSupportRowRetentionEpochs)); err != nil {
		return err
	}
	newStatus := modelSupportStatus(&newSupport, currentEpoch)
	mustEmitHubEvent(ctx, &types.EventModelSupportUpdated{
		Operator: newSupport.OperatorAddress, ModelId: newSupport.ModelId, ProfileVersion: newSupport.ProfileVersion,
		SupportVersion: newSupport.SupportVersion, OldStatus: oldStatus, NewStatus: newStatus,
		ExpiryEpoch: newSupport.SupportFreshUntilEpoch,
	})
	return nil
}

func (k Keeper) reweightModelSupport(
	ctx context.Context,
	oldSupport types.ModelSupportState,
	currentEpoch, height uint64,
) (types.ModelSupportState, bool, error) {
	profile, err := k.GetProfile(ctx, oldSupport.ModelId, oldSupport.ProfileVersion)
	if err != nil {
		return types.ModelSupportState{}, false, err
	}
	capability, err := k.GetProfileCapabilityState(ctx, oldSupport.OperatorAddress, oldSupport.ModelId, oldSupport.ProfileVersion)
	if err != nil {
		return types.ModelSupportState{}, false, err
	}
	newSupport := oldSupport
	newSupport.SupportActive = false
	newSupport.ActiveSupportStakeSnapshot = 0
	newSupport.EligibleSupportStakeSnapshot = 0
	weight, eligible, err := k.deriveSupportWeight(ctx, profile, capability, newSupport, currentEpoch)
	if err != nil {
		return types.ModelSupportState{}, false, err
	}
	if eligible {
		newSupport.EligibleSupportStakeSnapshot = weight
		if newSupport.ActivationKind != types.ModelSupportActivationNone {
			newSupport.SupportActive = true
			newSupport.ActiveSupportStakeSnapshot = weight
		}
	}
	if newSupport.SupportActive == oldSupport.SupportActive &&
		newSupport.ActiveSupportStakeSnapshot == oldSupport.ActiveSupportStakeSnapshot &&
		newSupport.EligibleSupportStakeSnapshot == oldSupport.EligibleSupportStakeSnapshot {
		return oldSupport, false, nil
	}
	if newSupport.SupportVersion == math.MaxUint64 {
		return types.ModelSupportState{}, false, fmt.Errorf("model support version overflow")
	}
	newSupport.SupportVersion++
	if err := newSupport.Validate(); err != nil {
		return types.ModelSupportState{}, false, err
	}
	if err := k.applyModelSupportMutation(ctx, &oldSupport, newSupport, capability, currentEpoch, height); err != nil {
		return types.ModelSupportState{}, false, err
	}
	return newSupport, true, nil
}

func (k Keeper) reconcileSupportsAfterBondChange(
	ctx context.Context,
	operatorAddress string,
	activeBond, height uint64,
	belowMinReason string,
) error {
	supports, err := k.collectDeclaredSupports(ctx, operatorAddress, func(types.ProfileState) bool { return true })
	if err != nil {
		return err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	currentEpoch := epochForHeight(height, params.Epoch.EpochLengthBlocks)
	for _, support := range supports {
		profile, err := k.GetProfile(ctx, support.ModelId, support.ProfileVersion)
		if err != nil {
			return err
		}
		if activeBond < RequiredServiceBondForProfile(profile) {
			if _, err := k.deactivateModelSupport(
				ctx, support.OperatorAddress, support.ModelId, support.ProfileVersion, belowMinReason, height,
			); err != nil {
				return err
			}
			continue
		}
		if _, _, err := k.reweightModelSupport(ctx, support, currentEpoch, height); err != nil {
			return err
		}
	}
	return k.syncLiveServiceBondStatus(ctx, operatorAddress)
}

func (k Keeper) syncLiveServiceBondStatus(ctx context.Context, operatorAddress string) error {
	bond, exists, err := k.loadServiceBond(ctx, operatorAddress)
	if err != nil || !exists {
		return err
	}
	if !types.IsLiveServiceBondStatus(bond.Status) || bond.JailCount != 0 {
		return nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	iter, err := k.ModelSupportByOperatorIndex.Iterate(
		ctx, collections.NewPrefixedTripleRange[string, string, uint32](operatorAddress),
	)
	if err != nil {
		return err
	}
	defer iter.Close()
	anyActive := false
	var visited uint32
	for ; iter.Valid(); iter.Next() {
		if visited == params.Support.MaxSupportedProfilesPerOperator {
			return fmt.Errorf("operator support profiles exceed the configured bound")
		}
		visited++
		key, err := iter.Key()
		if err != nil {
			return err
		}
		support, err := k.ModelSupport.Get(ctx, types.NewModelSupportKey(key.K1(), key.K2(), key.K3()))
		if err != nil {
			return err
		}
		if support.SupportActive {
			anyActive = true
			break
		}
	}
	desired := types.ServiceBondStatusRegistered
	if anyActive {
		desired = types.ServiceBondStatusActive
	}
	if bond.Status == desired {
		return nil
	}
	bond.Status = desired
	if err := bond.Validate(); err != nil {
		return err
	}
	return k.ServiceBond.Set(ctx, types.NewServiceBondKey(operatorAddress), bond)
}

func (k Keeper) deriveProfileAndModelStatus(ctx context.Context, profile *types.ProfileState, height uint64) error {
	if profile == nil {
		return fmt.Errorf("profile is required")
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	oldProfileStatus := profile.Status
	if profile.StatusSource == types.ProfileStatusSourceAutoSupport {
		active, err := profileSupportThresholdMet(*profile, params.Support)
		if err != nil {
			return err
		}
		if active {
			profile.Status = types.ModelStatusActive
		} else {
			profile.Status = types.ModelStatusRegistered
		}
	}
	profile.UpdatedHeight = height
	if err := k.setProfileState(ctx, *profile); err != nil {
		return err
	}
	if oldProfileStatus != profile.Status {
		mustEmitHubEvent(ctx, &types.EventModelProfileStateChanged{
			ModelId: profile.ModelId, ProfileVersion: profile.ProfileVersion,
			OldStatus: oldProfileStatus, NewStatus: profile.Status, Source: profile.StatusSource,
		})
	}

	model, err := k.GetModel(ctx, profile.ModelId)
	if err != nil {
		return err
	}
	if oldProfileStatus != profile.Status {
		if oldProfileStatus == types.ModelStatusActive && profile.Status != types.ModelStatusActive {
			if model.ActiveProfileCount == 0 {
				return fmt.Errorf("model active profile count underflow")
			}
			model.ActiveProfileCount--
		}
		if oldProfileStatus != types.ModelStatusActive && profile.Status == types.ModelStatusActive {
			if model.ActiveProfileCount == math.MaxUint32 {
				return fmt.Errorf("model active profile count overflow")
			}
			model.ActiveProfileCount++
		}
	}
	return k.deriveModelStatus(ctx, &model, height)
}

// deriveModelStatus recomputes ModelState.status from active_profile_count and
// persists the row. It is the model half of deriveProfileAndModelStatus, split out
// so the governance unfreeze entry (setModelStatusWithSource) can re-run it on its
// own: a model-level unfreeze resets status_source to AUTO_PROFILE and therefore
// owes the same recomputation, but it has no profile in hand to drive the profile
// half with.
//
// The write is unconditional because the caller may have adjusted
// active_profile_count without changing the status; the event is not, because the
// convention here is to emit only when the status actually moved.
func (k Keeper) deriveModelStatus(ctx context.Context, model *types.ModelState, height uint64) error {
	if model == nil {
		return fmt.Errorf("model is required")
	}
	oldModelStatus := model.Status
	if model.StatusSource == types.ModelStatusSourceAutoProfile {
		if model.ActiveProfileCount > 0 {
			model.Status = types.ModelStatusActive
		} else {
			model.Status = types.ModelStatusRegistered
		}
	}
	model.UpdatedHeight = height
	if err := k.setModelState(ctx, *model); err != nil {
		return err
	}
	if oldModelStatus != model.Status {
		mustEmitHubEvent(ctx, &types.EventModelStateChanged{
			ModelId: model.ModelId, OldStatus: oldModelStatus, NewStatus: model.Status,
			Source: model.StatusSource, Reason: types.GovernanceReason_GOVERNANCE_REASON_UNSPECIFIED,
		})
	}
	return nil
}

func profileSupportThresholdMet(profile types.ProfileState, params types.SupportParamsV1) (bool, error) {
	if profile.ActiveSupporterCount < params.ActiveSupporterMinCount || profile.EligibleSupportStake == 0 {
		return false, nil
	}
	leftHi, leftLo := bits.Mul64(profile.ActiveSupportStake, uint64(params.ActiveSupportStakeRatioDenominator))
	rightHi, rightLo := bits.Mul64(profile.EligibleSupportStake, uint64(params.ActiveSupportStakeRatioNumerator))
	if leftHi != rightHi {
		return leftHi > rightHi, nil
	}
	return leftLo >= rightLo, nil
}

// deriveSupportWeight loads the authoritative rows and delegates the complete
// eligibility/weight predicate to the same pure function used by Genesis.
func (k Keeper) deriveSupportWeight(ctx context.Context, profile types.ProfileState, capability types.ProfileCapabilityState, support types.ModelSupportState, currentEpoch uint64) (uint64, bool, error) {
	model, exists, err := k.loadModel(ctx, profile.ModelId)
	if err != nil || !exists {
		return 0, false, err
	}
	node, err := k.GetCortexNodeState(ctx, support.OperatorAddress)
	if err != nil {
		return 0, false, err
	}
	bond, err := k.GetServiceBondState(ctx, support.OperatorAddress)
	if err != nil {
		return 0, false, err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, false, err
	}
	return types.SupportVoteWeight(types.SupportEligibilityInputs{
		Node: node, Bond: bond, Model: model, Profile: profile,
		Capability: capability, Support: support,
	}, currentEpoch, params.Support)
}

func (k Keeper) requireSupportScope(ctx context.Context, operatorAddress, modelID string, profileVersion uint32, currentEpoch uint64) (types.ProfileState, error) {
	node, err := k.GetCortexNodeState(ctx, operatorAddress)
	if err != nil {
		return types.ProfileState{}, err
	}
	if node.ServiceKeyStatus != types.ServiceKeyStatus_SERVICE_KEY_STATUS_ACTIVE {
		return types.ProfileState{}, fmt.Errorf("current Cortex service key is not active")
	}
	if tombstoned, err := k.IsTombstoned(ctx, operatorAddress); err != nil {
		return types.ProfileState{}, err
	} else if tombstoned {
		return types.ProfileState{}, fmt.Errorf("provider is tombstoned")
	}
	model, exists, err := k.loadModel(ctx, modelID)
	if err != nil {
		return types.ProfileState{}, err
	}
	if !exists || !isParentModelOpenForProfile(model.Status) {
		return types.ProfileState{}, fmt.Errorf("model is not open for support")
	}
	profile, exists, err := k.loadProfile(ctx, modelID, profileVersion)
	if err != nil {
		return types.ProfileState{}, err
	}
	if !exists || !isProfileOpenForOrders(profile.Status) {
		return types.ProfileState{}, fmt.Errorf("profile is not open for support")
	}
	bond, err := k.GetServiceBondState(ctx, operatorAddress)
	if err != nil {
		return types.ProfileState{}, err
	}
	// JAILED remains eligible for declaration and refresh, but both callers pass
	// through deriveSupportWeight, which keeps support_active and both snapshots
	// at zero while jail_count is non-zero. This preserves the declaration,
	// freshness and activation proof needed to accept reduced-weight recovery
	// duties without granting active support before the jail clears.
	//
	// Tombstone is unaffected — IsTombstoned above already rejected it, and
	// parameter_table.md keeps "after a tombstone the corresponding identity is
	// permanently refused re-entry" as the [hard boundary].
	if !types.IsCandidateEligibleBondStatus(bond.Status) {
		return types.ProfileState{}, fmt.Errorf("service bond status %s is not eligible", bond.Status)
	}
	if effectiveCandidateBond(bond, currentEpoch) < RequiredServiceBondForProfile(profile) {
		return types.ProfileState{}, fmt.Errorf("effective active bond is below profile min_stake")
	}
	return profile, nil
}

func (k Keeper) countOperatorModelSupports(ctx context.Context, operatorAddress string, stopAfter uint64) (uint64, error) {
	iter, err := k.ModelSupportByOperatorIndex.Iterate(ctx, collections.NewPrefixedTripleRange[string, string, uint32](operatorAddress))
	if err != nil {
		return 0, err
	}
	defer iter.Close()
	var count uint64
	for ; iter.Valid(); iter.Next() {
		count++
		if stopAfter > 0 && count >= stopAfter {
			break
		}
	}
	return count, nil
}

func (k Keeper) GetProfileCapabilityState(ctx context.Context, operatorAddress, modelID string, profileVersion uint32) (types.ProfileCapabilityState, error) {
	state, exists, err := k.loadProfileCapability(ctx, operatorAddress, modelID, profileVersion)
	if err != nil {
		return types.ProfileCapabilityState{}, err
	}
	if !exists {
		return types.ProfileCapabilityState{}, fmt.Errorf("profile capability not found")
	}
	return state, nil
}

func (k Keeper) GetModelSupportState(ctx context.Context, operatorAddress, modelID string, profileVersion uint32) (types.ModelSupportState, error) {
	state, exists, err := k.loadModelSupport(ctx, operatorAddress, modelID, profileVersion)
	if err != nil {
		return types.ModelSupportState{}, err
	}
	if !exists {
		return types.ModelSupportState{}, fmt.Errorf("model support not found")
	}
	return state, nil
}

func (k Keeper) loadProfileCapability(ctx context.Context, operatorAddress, modelID string, profileVersion uint32) (types.ProfileCapabilityState, bool, error) {
	state, err := k.ProfileCapability.Get(ctx, types.NewProfileCapabilityKey(strings.TrimSpace(operatorAddress), strings.TrimSpace(modelID), profileVersion))
	if errors.Is(err, collections.ErrNotFound) {
		return types.ProfileCapabilityState{}, false, nil
	}
	return state, err == nil, err
}

func (k Keeper) loadModelSupport(ctx context.Context, operatorAddress, modelID string, profileVersion uint32) (types.ModelSupportState, bool, error) {
	state, err := k.ModelSupport.Get(ctx, types.NewModelSupportKey(strings.TrimSpace(operatorAddress), strings.TrimSpace(modelID), profileVersion))
	if errors.Is(err, collections.ErrNotFound) {
		return types.ModelSupportState{}, false, nil
	}
	return state, err == nil, err
}

func modelSupportStatus(state *types.ModelSupportState, currentEpoch uint64) types.ModelSupportStatus {
	if state == nil || !state.DeclaredSupport {
		return types.ModelSupportStatus_MODEL_SUPPORT_STATUS_INACTIVE
	}
	if state.SupportActive && currentEpoch < state.SupportFreshUntilEpoch {
		return types.ModelSupportStatus_MODEL_SUPPORT_STATUS_ACTIVE
	}
	if currentEpoch >= state.SupportFreshUntilEpoch {
		return types.ModelSupportStatus_MODEL_SUPPORT_STATUS_STALE
	}
	return types.ModelSupportStatus_MODEL_SUPPORT_STATUS_DECLARED
}

func optionalSupport(state types.ModelSupportState, exists bool) *types.ModelSupportState {
	if !exists {
		return nil
	}
	return &state
}

func dutyFromRole(role string) (shared.Duty, error) {
	switch normalizeServiceRole(role) {
	case types.ServiceBondRoleWorker:
		return shared.Duty_DUTY_WORKER, nil
	case types.ServiceBondRoleVerifier:
		return shared.Duty_DUTY_VERIFIER, nil
	default:
		return shared.Duty_DUTY_UNSPECIFIED, fmt.Errorf("activation_duty must be WORKER or VERIFIER")
	}
}
