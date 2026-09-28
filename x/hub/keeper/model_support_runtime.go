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
	Capability types.ModelCapabilityState
	Support    types.ModelSupportState
	Status     shared.MutationStatusV1
}

func (k Keeper) DeclareModelSupport(
	ctx context.Context,
	operatorAddress string,
	modelID []byte,
	inferenceCapability, verificationCapability bool,
	height uint64,
) (DeclareModelSupportResult, error) {
	_, operatorAddress, err := k.requireCanonicalAddress("operator_address", operatorAddress)
	if err != nil {
		return DeclareModelSupportResult{}, err
	}
	if len(modelID) != shared.Hash32KeySize || (!inferenceCapability && !verificationCapability) {
		return DeclareModelSupportResult{}, fmt.Errorf("model_id and at least one capability are required")
	}
	if height == 0 {
		return DeclareModelSupportResult{}, fmt.Errorf("height must be greater than 0")
	}

	params, err := k.Params.Get(ctx)
	if err != nil {
		return DeclareModelSupportResult{}, err
	}
	currentEpoch := epochForHeight(height, params.Epoch.EpochLengthBlocks)
	model, err := k.requireSupportScope(ctx, operatorAddress, modelID, currentEpoch)
	if err != nil {
		return DeclareModelSupportResult{}, err
	}

	capability, capabilityExists, err := k.loadModelCapability(ctx, operatorAddress, modelID)
	if err != nil {
		return DeclareModelSupportResult{}, err
	}
	oldSupport, supportExists, err := k.loadModelSupport(ctx, operatorAddress, modelID)
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
	// bypass max_supported_models_per_operator.
	if !supportExists {
		count, err := k.countOperatorModelSupports(ctx, operatorAddress, uint64(params.Support.MaxSupportedModelsPerOperator)+1)
		if err != nil {
			return DeclareModelSupportResult{}, err
		}
		if count >= uint64(params.Support.MaxSupportedModelsPerOperator) {
			return DeclareModelSupportResult{}, fmt.Errorf("operator support models must not exceed %d", params.Support.MaxSupportedModelsPerOperator)
		}
	}
	if !capabilityExists {
		capability = types.ModelCapabilityState{
			OperatorAddress: operatorAddress,
			ModelId:         modelID,
		}
	}
	// FirstActivationDuty remains on the retained support row after deactivation
	// and Genesis requires it to be declared by the paired capability. Refusing a
	// narrowing declaration here prevents runtime from writing state that its own
	// Genesis validator rejects. Capability expansion and changes before
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
			return DeclareModelSupportResult{}, fmt.Errorf("model capability version overflow")
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
			ActivationKind:  types.ModelSupportActivationNone,
			SuspendReason:   types.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_NONE,
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
	newSupport.ActiveSupportStakeSnapshot = 0
	eligibleWeight, eligible, err := k.deriveSupportWeight(ctx, model, capability, newSupport, currentEpoch)
	if err != nil {
		return DeclareModelSupportResult{}, err
	}
	if eligible {
		if newSupport.ActivationKind != types.ModelSupportActivationNone {
			newSupport.SupportActive = true
			newSupport.ActiveSupportStakeSnapshot = eligibleWeight
			newSupport.SuspendReason = types.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_NONE
		} else {
			newSupport.SupportActive = false
		}
	} else {
		newSupport.SupportActive = false
		newSupport.SuspendReason = types.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_JAIL
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
// errSupportActivationNotApplicable marks an activation *condition* that is simply not
// met, as opposed to a failure.
//
// The activation rule lists conditions 2-4 (declared support matches the task's model/profile,
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
	modelID := fact.ModelID
	if len(modelID) != shared.Hash32KeySize {
		return fmt.Errorf("task support completion model_id must be raw Hash32")
	}
	liability, err := k.ReadTaskLiabilityValue(
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
	model, err := k.requireSupportScope(ctx, operatorAddress, modelID, currentEpoch)
	if err != nil {
		// Scope covers "is this operator/model/profile still eligible to support at
		// all" — an activation condition, not a settlement failure.
		return fmt.Errorf("%w: %s", errSupportActivationNotApplicable, err.Error())
	}
	capability, err := k.GetModelCapabilityState(ctx, operatorAddress, modelID)
	if err != nil {
		return fmt.Errorf("%w: %s", errSupportActivationNotApplicable, err.Error())
	}
	if fact.Duty == shared.DutyWorker && !capability.InferenceCapability {
		return fmt.Errorf("%w: worker duty without inference capability", errSupportActivationNotApplicable)
	}
	if fact.Duty == shared.DutyVerifier && !capability.VerificationCapability {
		return fmt.Errorf("%w: verifier duty without verification capability", errSupportActivationNotApplicable)
	}
	oldSupport, err := k.GetModelSupportState(ctx, operatorAddress, modelID)
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
		newSupport.FirstSupportProfileVersion = fact.ProfileVersion
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
	weight, eligible, err := k.deriveSupportWeight(ctx, model, capability, newSupport, currentEpoch)
	if err != nil {
		return err
	}
	if eligible {
		newSupport.SupportActive = true
		newSupport.ActiveSupportStakeSnapshot = weight
		newSupport.SuspendReason = types.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_NONE
	} else {
		bond, err := k.GetServiceBondState(ctx, operatorAddress)
		if err != nil {
			return fmt.Errorf("%w: %s", errSupportActivationNotApplicable, err.Error())
		}
		if bond.Status != types.ServiceBondStatusJailed || bond.JailCount == 0 {
			// Activation branches on jail: still JAILED keeps the activation proof
			// with support_active=false, otherwise the row is written according to
			// current eligibility.
			// An operator that is neither eligible nor jailed — slashed below
			// min_stake or unbonding since the task was assigned — is simply not
			// activating, and the deactivation itself is owned by
			// DeactivateModelSupport's single mutation entry point rather than by
			// this path. Erroring here would abort the settlement that paid it.
			return fmt.Errorf("%w: operator is neither eligible nor jailed", errSupportActivationNotApplicable)
		}
		newSupport.SuspendReason = types.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_JAIL
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
			Operator: operatorAddress, ModelId: modelID, FirstSupportProfileVersion: fact.ProfileVersion,
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

func (k Keeper) DeactivateModelSupport(ctx context.Context, operatorAddress string, modelID []byte, reason string, height uint64) (types.ModelSupportState, error) {
	state, err := k.deactivateModelSupport(ctx, operatorAddress, modelID, reason, height)
	if err != nil {
		return types.ModelSupportState{}, err
	}
	if err := k.syncLiveServiceBondStatus(ctx, state.OperatorAddress); err != nil {
		return types.ModelSupportState{}, err
	}
	return state, nil
}

func (k Keeper) deactivateModelSupport(ctx context.Context, operatorAddress string, modelID []byte, reason string, height uint64) (types.ModelSupportState, error) {
	oldSupport, err := k.GetModelSupportState(ctx, operatorAddress, modelID)
	if err != nil {
		return types.ModelSupportState{}, err
	}
	if !oldSupport.DeclaredSupport && !oldSupport.SupportActive && oldSupport.ActiveSupportStakeSnapshot == 0 {
		return oldSupport, nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return types.ModelSupportState{}, err
	}
	currentEpoch := epochForHeight(height, params.Epoch.EpochLengthBlocks)
	capability, err := k.GetModelCapabilityState(ctx, operatorAddress, modelID)
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
	newSupport.SuspendReason = types.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_NONE
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
// Jail must run through applyModelSupportMutation for a concrete
// reason: a row left at support_active=true with jail_count > 0 is exactly the
// shape validateSupportGenesis rejects, so a chain that ever jailed an operator
// exported a genesis it could not re-import. Zeroing support_active and both
// stake snapshots is what discharges that invariant.
//
// Clearing declared_support is not required by it, and doing so as well is what
// made jail permanent. The joint filter of
// the candidate selection contract admits a candidate on a declared
// support, so wiping the declaration removed the operator from every candidate
// pool, and requireSupportScope refuses to re-declare while the bond is JAILED.
// The protocol then only decrements jail_count after
// jail_clear_normal_action_count normal actions the operator can no longer
// perform. Keeping the declaration leaves the Worker path open — task_candidate_fact
// tests declared_support alone — at the reduced candidate_jail_factor, which is
// the graduated penalty the parameter tableregisters as the [hard boundary].
//
// Recovery stays gated rather than free: refreshModelSupport restores
// support_active only through requireSupportScope (which rejects a JAILED bond)
// and deriveSupportWeight (SupportVoteWeight excludes jail_count != 0), so the
// active half comes back only once the ladder has actually been walked to 0.
// Freshness is deliberately preserved too: the expiry sweep still owns it, and a
// row that lapses while jailed is deactivated in full by that sweep, as before.
func (k Keeper) suspendModelSupportForJail(ctx context.Context, operatorAddress string, modelID []byte, height uint64) error {
	oldSupport, err := k.GetModelSupportState(ctx, operatorAddress, modelID)
	if err != nil {
		return err
	}
	if !oldSupport.SupportActive && oldSupport.ActiveSupportStakeSnapshot == 0 && oldSupport.SuspendReason == types.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_JAIL {
		return nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	capability, err := k.GetModelCapabilityState(ctx, operatorAddress, modelID)
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
	newSupport.SuspendReason = types.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_JAIL
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
// The scan is bounded by params.Support.MaxSupportedProfilesPerOperator,
// the same bound the jail-time scan relies on, and it only runs on the rare
// transition to jail_count == 0.
func (k Keeper) restoreSupportsAfterJailClear(ctx context.Context, operatorAddress string, height uint64) (bool, error) {
	supports, err := k.collectDeclaredSupports(ctx, operatorAddress)
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
		newSupport, _, err := k.reweightModelSupport(ctx, oldSupport, currentEpoch, height, types.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_BOND_BELOW_MIN)
		if err != nil {
			return false, err
		}
		anyActive = anyActive || newSupport.SupportActive
	}
	return anyActive, nil
}

// nextModelSupportOperator resumes the model-scoped operator scan from
// lastOperatorAddress (exclusive). An empty lastOperatorAddress starts at the
// first indexed operator. Returning one key per call keeps the caller in charge
// of the visited-item and serialized-bytes budgets.
func (k Keeper) nextModelSupportOperator(ctx context.Context, modelID []byte, lastOperatorAddress string) (string, bool, error) {
	rng := new(collections.Range[types.ModelSupportByModelIndexKeyPair]).
		Prefix(collections.PairPrefix[shared.Hash32Key, string](modelID))
	if lastOperatorAddress != "" {
		rng = rng.StartExclusive(types.NewModelSupportByModelIndexKey(modelID, lastOperatorAddress))
	}
	iter, err := k.ModelSupportByModelIndex.Iterate(ctx, rng)
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
	return key.K2(), true, nil
}

// WriteModelSupportIndexes is the single writer for every ModelSupportState side
// index. Genesis import and every runtime mutation must go through it so
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
	if err := k.ModelSupportByModelIndex.Set(ctx, types.NewModelSupportByModelIndexKey(state.ModelId, state.OperatorAddress)); err != nil {
		return err
	}
	if err := k.ModelSupportByOperatorIndex.Set(ctx, types.NewModelSupportByOperatorIndexKey(state.OperatorAddress, state.ModelId)); err != nil {
		return err
	}
	if state.DeclaredSupport && state.SupportFreshUntilEpoch > currentEpoch {
		return k.ModelSupportExpiryIndex.Set(ctx, types.NewModelSupportExpiryIndexKey(state.SupportFreshUntilEpoch, state.OperatorAddress, state.ModelId))
	}
	pruneEpoch, err := checkedAdd(currentEpoch, retentionEpochs)
	if err != nil {
		return err
	}
	return k.ModelSupportPruneIndex.Set(ctx, types.NewModelSupportPruneIndexKey(pruneEpoch, state.OperatorAddress, state.ModelId))
}

// errSupportRefreshNotApplicable marks a daily-refresh precondition that one
// (operator, model, profile) item simply does not meet, as opposed to a failure.
// It is the daily-confirmation counterpart of errSupportActivationNotApplicable
// and exists for the same reason.
//
// MsgBatchConfirmModelSupport carries confirmations for many operators, each
// listing many profiles, and an operator signs its list before the batch is
// assembled and included. Every precondition marked with this sentinel can stop
// holding in between: governance can freeze or delist the profile or its parent
// model, the operator can rotate or revoke its service key, be tombstoned,
// unstake or be slashed below the profile's min_stake, or let a declaration that
// never earned an activation lapse. Treating any of those as a hard error aborted
// the whole transaction, so a single stale item discarded every other operator's
// confirmation in the same batch. That made a shared batch cheap to deny service
// to, and it required submitters to track activation and freeze state off-chain
// just to assemble a list that would be accepted at all.
//
// Skipping the item is safe because a skipped item is only denied an extension of
// its freshness window. Every return marked with this sentinel happens before any
// write, so a skip grants nothing, stores nothing and moves no aggregate: the
// support row keeps the freshness it already had and expires on its own schedule
// through the ordinary expiry sweep. Real failures - store errors, overflow,
// validation breaks - still propagate and still abort the batch.
var errSupportRefreshNotApplicable = errors.New("daily support refresh conditions are not met")

func (k Keeper) refreshModelSupport(ctx context.Context, operatorAddress string, modelID []byte, epoch, height uint64) (types.ModelSupportState, bool, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return types.ModelSupportState{}, false, err
	}
	model, err := k.requireSupportScope(ctx, operatorAddress, modelID, epoch)
	if err != nil {
		// Scope covers "is this operator/model/profile still eligible to support at
		// all", which is a condition rather than a batch failure.
		return types.ModelSupportState{}, false, fmt.Errorf("%w: %s", errSupportRefreshNotApplicable, err.Error())
	}
	capability, err := k.GetModelCapabilityState(ctx, operatorAddress, modelID)
	if err != nil {
		return types.ModelSupportState{}, false, fmt.Errorf("%w: %s", errSupportRefreshNotApplicable, err.Error())
	}
	oldSupport, err := k.GetModelSupportState(ctx, operatorAddress, modelID)
	if err != nil {
		return types.ModelSupportState{}, false, fmt.Errorf("%w: %s", errSupportRefreshNotApplicable, err.Error())
	}
	if !oldSupport.DeclaredSupport || oldSupport.ActivationKind == types.ModelSupportActivationNone {
		return types.ModelSupportState{}, false, fmt.Errorf(
			"%w: support must be declared and activated before daily refresh", errSupportRefreshNotApplicable,
		)
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
	weight, eligible, err := k.deriveSupportWeight(ctx, model, capability, newSupport, epoch)
	if err != nil {
		return types.ModelSupportState{}, false, err
	}
	if eligible {
		newSupport.SupportActive = true
		newSupport.ActiveSupportStakeSnapshot = weight
		newSupport.SuspendReason = types.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_NONE
	} else {
		bond, err := k.GetServiceBondState(ctx, operatorAddress)
		if err != nil {
			return types.ModelSupportState{}, false, err
		}
		if bond.Status != types.ServiceBondStatusJailed || bond.JailCount == 0 {
			return types.ModelSupportState{}, false, fmt.Errorf(
				"%w: operator is not currently eligible for model support", errSupportRefreshNotApplicable,
			)
		}
		newSupport.SupportActive = false
		newSupport.SuspendReason = types.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_JAIL
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
	capability types.ModelCapabilityState,
	currentEpoch, height uint64,
) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	model, err := k.GetModel(ctx, newSupport.ModelId)
	if err != nil {
		return err
	}
	oldStatus := modelSupportStatus(oldSupport, currentEpoch)
	if oldSupport != nil {
		if model.ActiveSupportStake < oldSupport.ActiveSupportStakeSnapshot {
			return fmt.Errorf("model support aggregate underflow")
		}
		model.ActiveSupportStake -= oldSupport.ActiveSupportStakeSnapshot
		if oldSupport.ActiveSupportStakeSnapshot > 0 {
			if model.ActiveSupporterCount == 0 {
				return fmt.Errorf("model active supporter count underflow")
			}
			model.ActiveSupporterCount--
		}
		if oldSupport.SupportFreshUntilEpoch > 0 {
			oldExpiry := types.NewModelSupportExpiryIndexKey(oldSupport.SupportFreshUntilEpoch, oldSupport.OperatorAddress, oldSupport.ModelId)
			if err := k.ModelSupportExpiryIndex.Remove(ctx, oldExpiry); err != nil && !errors.Is(err, collections.ErrNotFound) {
				return err
			}
		}
	}
	if math.MaxUint64-model.ActiveSupportStake < newSupport.ActiveSupportStakeSnapshot {
		return fmt.Errorf("model support aggregate overflow")
	}
	model.ActiveSupportStake += newSupport.ActiveSupportStakeSnapshot
	if newSupport.ActiveSupportStakeSnapshot > 0 {
		if model.ActiveSupporterCount == math.MaxUint32 {
			return fmt.Errorf("model active supporter count overflow")
		}
		model.ActiveSupporterCount++
	}
	if err := k.deriveModelStatus(ctx, &model, height); err != nil {
		return err
	}
	if err := capability.Validate(); err != nil {
		return err
	}
	if err := newSupport.Validate(); err != nil {
		return err
	}
	key := types.NewModelSupportKey(newSupport.OperatorAddress, newSupport.ModelId)
	if err := k.ModelCapability.Set(ctx, types.NewModelCapabilityKey(newSupport.OperatorAddress, newSupport.ModelId), capability); err != nil {
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
		Operator: newSupport.OperatorAddress, ModelId: newSupport.ModelId, SuspendReason: newSupport.SuspendReason,
		SupportVersion: newSupport.SupportVersion, OldStatus: oldStatus, NewStatus: newStatus,
		ExpiryEpoch: newSupport.SupportFreshUntilEpoch,
	})
	return nil
}

func (k Keeper) reweightModelSupport(
	ctx context.Context,
	oldSupport types.ModelSupportState,
	currentEpoch, height uint64,
	belowMinReason types.ModelSupportSuspendReason,
) (types.ModelSupportState, bool, error) {
	model, err := k.GetModel(ctx, oldSupport.ModelId)
	if err != nil {
		return types.ModelSupportState{}, false, err
	}
	capability, err := k.GetModelCapabilityState(ctx, oldSupport.OperatorAddress, oldSupport.ModelId)
	if err != nil {
		return types.ModelSupportState{}, false, err
	}
	newSupport := oldSupport
	newSupport.SupportActive = false
	newSupport.ActiveSupportStakeSnapshot = 0
	weight, eligible, err := k.deriveSupportWeight(ctx, model, capability, newSupport, currentEpoch)
	if err != nil {
		return types.ModelSupportState{}, false, err
	}
	if eligible {
		if newSupport.ActivationKind != types.ModelSupportActivationNone {
			newSupport.SupportActive = true
			newSupport.ActiveSupportStakeSnapshot = weight
			newSupport.SuspendReason = types.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_NONE
		}
	} else if oldSupport.DeclaredSupport && oldSupport.ActivationKind != types.ModelSupportActivationNone {
		bond, err := k.GetServiceBondState(ctx, oldSupport.OperatorAddress)
		if err != nil {
			return types.ModelSupportState{}, false, err
		}
		if bond.JailCount != 0 || bond.Status == types.ServiceBondStatusJailed {
			newSupport.SuspendReason = types.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_JAIL
		} else {
			newSupport.SuspendReason = belowMinReason
		}
	}
	if newSupport.SupportActive == oldSupport.SupportActive &&
		newSupport.ActiveSupportStakeSnapshot == oldSupport.ActiveSupportStakeSnapshot &&
		newSupport.SuspendReason == oldSupport.SuspendReason {
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
	supports, err := k.collectDeclaredSupports(ctx, operatorAddress)
	if err != nil {
		return err
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	currentEpoch := epochForHeight(height, params.Epoch.EpochLengthBlocks)
	for _, support := range supports {
		if _, _, err := k.reweightModelSupport(ctx, support, currentEpoch, height, types.ModelSupportSuspendReason_MODEL_SUPPORT_SUSPEND_REASON_BOND_BELOW_MIN); err != nil {
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
		ctx, collections.NewPrefixedPairRange[string, shared.Hash32Key](operatorAddress),
	)
	if err != nil {
		return err
	}
	defer iter.Close()
	anyActive := false
	var visited uint32
	for ; iter.Valid(); iter.Next() {
		if visited == params.Support.MaxSupportedModelsPerOperator {
			return fmt.Errorf("operator support models exceed the configured bound")
		}
		visited++
		key, err := iter.Key()
		if err != nil {
			return err
		}
		support, err := k.ModelSupport.Get(ctx, types.NewModelSupportKey(key.K1(), key.K2()))
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
	return k.WriteServiceBondValue(ctx, types.NewServiceBondKey(operatorAddress), bond)
}

// deriveModelStatus recomputes ModelState.status from active_profile_count and
// persists the row, so the governance unfreeze entry (setModelStatusWithSource)
// can re-run it on its own: a model-level unfreeze resets status_source to
// AUTO_PROFILE and therefore owes the same recomputation, with no profile to
// drive it from.
//
// The write is unconditional because the caller may have adjusted
// active_profile_count without changing the status; the event is not, because the
// convention here is to emit only when the status actually moved.
func (k Keeper) deriveModelStatus(ctx context.Context, model *types.ModelState, height uint64) error {
	if model == nil {
		return fmt.Errorf("model is required")
	}
	oldModelStatus := model.Status
	if model.StatusSource == types.ModelStatusSourceAutoSupport {
		params, err := k.Params.Get(ctx)
		if err != nil {
			return err
		}
		active, err := modelSupportThresholdMet(*model, params.Support)
		if err != nil {
			return err
		}
		if active {
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

func modelSupportThresholdMet(model types.ModelState, params types.SupportParamsV1) (bool, error) {
	if model.ActiveSupporterCount < params.ActiveSupporterMinCount {
		return false, nil
	}
	hi, threshold := bits.Mul64(model.SupportMinStake, uint64(params.ActiveSupportStakeMultiple))
	if hi != 0 {
		return false, fmt.Errorf("model active support threshold overflows")
	}
	return model.ActiveSupportStake >= threshold, nil
}

// deriveSupportWeight loads the authoritative rows and delegates the complete
// eligibility/weight predicate to the same pure function used by Genesis.
func (k Keeper) deriveSupportWeight(ctx context.Context, model types.ModelState, capability types.ModelCapabilityState, support types.ModelSupportState, currentEpoch uint64) (uint64, bool, error) {
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
		Node: node, Bond: bond, Model: model,
		Capability: capability, Support: support,
	}, currentEpoch, params.Support)
}

func (k Keeper) requireSupportScope(ctx context.Context, operatorAddress string, modelID []byte, currentEpoch uint64) (types.ModelState, error) {
	node, err := k.GetCortexNodeState(ctx, operatorAddress)
	if err != nil {
		return types.ModelState{}, err
	}
	if node.ServiceKeyStatus != types.ServiceKeyStatus_SERVICE_KEY_STATUS_ACTIVE {
		return types.ModelState{}, fmt.Errorf("current Cortex service key is not active")
	}
	if tombstoned, err := k.IsTombstoned(ctx, operatorAddress); err != nil {
		return types.ModelState{}, err
	} else if tombstoned {
		return types.ModelState{}, fmt.Errorf("provider is tombstoned")
	}
	model, exists, err := k.loadModel(ctx, modelID)
	if err != nil {
		return types.ModelState{}, err
	}
	if !exists || !isParentModelOpenForProfile(model.Status) {
		return types.ModelState{}, fmt.Errorf("model is not open for support")
	}
	bond, err := k.GetServiceBondState(ctx, operatorAddress)
	if err != nil {
		return types.ModelState{}, err
	}
	// JAILED remains eligible for declaration and refresh, but both callers pass
	// through deriveSupportWeight, which keeps support_active and both snapshots
	// at zero while jail_count is non-zero. This preserves the declaration,
	// freshness and activation proof needed to accept reduced-weight recovery
	// duties without granting active support before the jail clears.
	//
	// Tombstone is unaffected — IsTombstoned above already rejected it, and
	// the parameter tablekeeps "after a tombstone the corresponding identity is
	// permanently refused re-entry" as the [hard boundary].
	if !types.IsCandidateEligibleBondStatus(bond.Status) {
		return types.ModelState{}, fmt.Errorf("service bond status %s is not eligible", bond.Status)
	}
	if effectiveCandidateBond(bond, currentEpoch) < model.SupportMinStake {
		return types.ModelState{}, fmt.Errorf("effective active bond is below model support_min_stake")
	}
	return model, nil
}

func (k Keeper) countOperatorModelSupports(ctx context.Context, operatorAddress string, stopAfter uint64) (uint64, error) {
	iter, err := k.ModelSupportByOperatorIndex.Iterate(ctx, collections.NewPrefixedPairRange[string, shared.Hash32Key](operatorAddress))
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

func (k Keeper) GetModelCapabilityState(ctx context.Context, operatorAddress string, modelID []byte) (types.ModelCapabilityState, error) {
	state, exists, err := k.loadModelCapability(ctx, operatorAddress, modelID)
	if err != nil {
		return types.ModelCapabilityState{}, err
	}
	if !exists {
		return types.ModelCapabilityState{}, fmt.Errorf("model capability not found")
	}
	return state, nil
}

func (k Keeper) GetModelSupportState(ctx context.Context, operatorAddress string, modelID []byte) (types.ModelSupportState, error) {
	state, exists, err := k.loadModelSupport(ctx, operatorAddress, modelID)
	if err != nil {
		return types.ModelSupportState{}, err
	}
	if !exists {
		return types.ModelSupportState{}, fmt.Errorf("model support not found")
	}
	return state, nil
}

func (k Keeper) loadModelCapability(ctx context.Context, operatorAddress string, modelID []byte) (types.ModelCapabilityState, bool, error) {
	state, err := k.ModelCapability.Get(ctx, types.NewModelCapabilityKey(strings.TrimSpace(operatorAddress), modelID))
	if errors.Is(err, collections.ErrNotFound) {
		return types.ModelCapabilityState{}, false, nil
	}
	return state, err == nil, err
}

func (k Keeper) loadModelSupport(ctx context.Context, operatorAddress string, modelID []byte) (types.ModelSupportState, bool, error) {
	state, err := k.ModelSupport.Get(ctx, types.NewModelSupportKey(strings.TrimSpace(operatorAddress), modelID))
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
