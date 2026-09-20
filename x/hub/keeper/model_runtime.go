package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (k Keeper) RegisterModelProfileState(
	ctx context.Context,
	proposer string,
	projection shared.ModelProfileProjection,
	registrationDigest []byte,
	minStake, registrationFee, height uint64,
) (types.ModelState, types.ProfileState, types.RegistrationReceipt, bool, error) {
	if proposer == "" || proposer != strings.TrimSpace(proposer) {
		return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, fmt.Errorf("proposer address must be canonical")
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, err
	}
	parsedMinStake, err := modelRegistrationCoinAmount("min_stake", projection.MinStake, params.Phase0.BusinessDenom)
	if err != nil || parsedMinStake != minStake {
		return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, fmt.Errorf("min_stake does not match its canonical Coin")
	}
	parsedFee, err := modelRegistrationCoinAmount("registration_fee", projection.RegistrationFee, params.Phase0.BusinessDenom)
	if err != nil || parsedFee != registrationFee {
		return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, fmt.Errorf("registration_fee does not match its canonical Coin")
	}
	digestKey := shared.Hash32Key(registrationDigest)
	if receipt, err := k.RegistrationReceipt.Get(ctx, digestKey); err == nil {
		if err := receipt.Validate(); err != nil {
			return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, fmt.Errorf("invalid registration receipt: %w", err)
		}
		profile, profileErr := k.GetProfile(ctx, receipt.ModelId, receipt.ProfileVersion)
		if profileErr != nil || !bytes.Equal(profile.RegistrationDigest, registrationDigest) ||
			receipt.ModelId != projection.ModelId || receipt.ProfileVersion != projection.ProfileVersion ||
			profile.ProposerAddress != proposer || profile.RegistrationFeePaid != registrationFee {
			return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, fmt.Errorf("registration receipt invariant mismatch")
		}
		storedProjection := profileProjectionFromState(profile)
		storedProjection.MinStake = sdk.NewCoin(params.Phase0.BusinessDenom, sdkmath.NewIntFromUint64(profile.MinStake))
		storedProjection.RegistrationFee = sdk.NewCoin(params.Phase0.BusinessDenom, sdkmath.NewIntFromUint64(profile.RegistrationFeePaid))
		left, leftErr := types.CanonicalModelProfileProjection(projection)
		right, rightErr := types.CanonicalModelProfileProjection(storedProjection)
		if leftErr != nil || rightErr != nil || !bytes.Equal(left, right) {
			return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, fmt.Errorf("registration receipt projection mismatch")
		}
		model, err := k.GetModel(ctx, receipt.ModelId)
		return model, profile, receipt, true, err
	} else if !errors.Is(err, collections.ErrNotFound) {
		return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, err
	}

	model, exists, err := k.loadModel(ctx, projection.ModelId)
	if err != nil {
		return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, err
	}
	expectedFee := params.Treasury.ModelProfileRegistrationFee
	if exists {
		expectedFee = params.Treasury.ProfileVersionUpdateFee
	}
	expectedFeeAmount, err := shared.ParseAmount(expectedFee)
	if err != nil || registrationFee != expectedFeeAmount {
		return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, fmt.Errorf("registration_fee does not match the effective Hub parameter")
	}
	if !exists {
		if projection.ProfileVersion != 1 || projection.PreviousProfileVersion != 0 {
			return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, fmt.Errorf("new model must register profile_version 1 with previous_profile_version 0")
		}
		model = types.ModelState{
			ModelId: projection.ModelId, ProposerAddress: proposer,
			Status: types.ModelStatusRegistered, StatusSource: types.ModelStatusSourceAutoProfile,
			LatestProfileVersion: projection.ProfileVersion, RegistrationFeePaid: registrationFee,
			CreatedHeight: height, UpdatedHeight: height,
		}
	} else {
		if model.ProposerAddress != proposer {
			return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, fmt.Errorf("model registrant does not match proposer")
		}
		if projection.ProfileVersion != model.LatestProfileVersion+1 || projection.PreviousProfileVersion != model.LatestProfileVersion {
			return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, fmt.Errorf("profile version must immediately follow latest profile version")
		}
		if projection.ProfileVersion > types.MaxProfilesPerModel {
			return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, fmt.Errorf("model profiles must not exceed %d", types.MaxProfilesPerModel)
		}
		if _, found, err := k.loadProfile(ctx, projection.ModelId, projection.ProfileVersion); err != nil {
			return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, err
		} else if found {
			return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, fmt.Errorf("profile %s/%d already exists", projection.ModelId, projection.ProfileVersion)
		}
		model.LatestProfileVersion = projection.ProfileVersion
		model.RegistrationFeePaid, err = checkedAdd(model.RegistrationFeePaid, registrationFee)
		if err != nil {
			return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, fmt.Errorf("model registration fee overflow: %w", err)
		}
		model.UpdatedHeight = height
	}

	bondFloor, err := k.serviceBondMinInitial(ctx)
	if err != nil {
		return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, err
	}
	profile := profileStateFromProjection(proposer, projection, registrationDigest, minStake, registrationFee, height)
	if latest, closed := latestClosedFreezeRiskWindow(height, params.Freeze.FreezeRiskWindowBlocks); closed {
		profile.XLastFreezeRiskWindowEvaluated = &types.ProfileState_LastFreezeRiskWindowEvaluated{LastFreezeRiskWindowEvaluated: latest}
	}
	if err := types.ValidatePricingProfile(profile.PricingProfile, profile.RefPrice, params.Price); err != nil {
		return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, err
	}
	if err := validateProfileRegistrationState(profile, bondFloor); err != nil {
		return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, err
	}
	if err := model.Validate(); err != nil {
		return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, err
	}
	receipt := types.RegistrationReceipt{
		ModelId: projection.ModelId, ProfileVersion: projection.ProfileVersion,
	}
	if err := receipt.Validate(); err != nil {
		return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, err
	}
	if err := k.setModelState(ctx, model); err != nil {
		return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, err
	}
	if err := k.setProfileState(ctx, profile); err != nil {
		return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, err
	}
	if err := k.scheduleNextFreezeRiskWindow(ctx, profile); err != nil {
		return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, err
	}
	if err := k.RegistrationReceipt.Set(ctx, digestKey, receipt); err != nil {
		return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, err
	}
	return model, profile, receipt, false, nil
}

func profileStateFromProjection(proposer string, p shared.ModelProfileProjection, digest []byte, minStake, fee, height uint64) types.ProfileState {
	return types.ProfileState{
		ModelId: p.ModelId, ProfileVersion: p.ProfileVersion,
		ManifestHash: append([]byte(nil), p.ManifestHash...), TokenizerHash: append([]byte(nil), p.TokenizerHash...),
		RuntimeClass: p.RuntimeClass, RequiredTopK: p.RequiredTopK, TaskTypes: append([]shared.TaskType(nil), p.TaskTypes...),
		GenerationType: p.GenerationType, ResourceTier: p.ResourceTier, MinStake: minStake,
		ChallengeOpenWindowBlocks: p.ChallengeOpenWindowBlocks, VerificationProfile: p.VerificationProfile,
		VerificationThresholds: p.VerificationThresholds, BatchVerification: p.BatchVerification,
		PricingProfile: p.PricingProfile, TimeoutBootstrapProfile: p.TimeoutBootstrapProfile,
		SchemaHash: append([]byte(nil), p.SchemaHash...), Status: types.ModelStatusRegistered,
		StatusSource: types.ProfileStatusSourceAutoSupport, RegistrationFeePaid: fee,
		PreviousProfileVersion: p.PreviousProfileVersion, ProposerAddress: proposer,
		RegistrationDigest: append([]byte(nil), digest...), CreatedHeight: height, UpdatedHeight: height,
		RefPrice: p.PricingProfile.InitialOutputPrice,
	}
}

func profileProjectionFromState(s types.ProfileState) shared.ModelProfileProjection {
	return shared.ModelProfileProjection{
		ModelId: s.ModelId, ProfileVersion: s.ProfileVersion, ManifestHash: s.ManifestHash,
		TokenizerHash: s.TokenizerHash, RuntimeClass: s.RuntimeClass, RequiredTopK: s.RequiredTopK,
		TaskTypes: s.TaskTypes, GenerationType: s.GenerationType, ResourceTier: s.ResourceTier,
		ChallengeOpenWindowBlocks: s.ChallengeOpenWindowBlocks, VerificationProfile: s.VerificationProfile,
		VerificationThresholds: s.VerificationThresholds, BatchVerification: s.BatchVerification,
		PricingProfile: s.PricingProfile, TimeoutBootstrapProfile: s.TimeoutBootstrapProfile,
		SchemaHash: s.SchemaHash, PreviousProfileVersion: s.PreviousProfileVersion,
	}
}

func (k Keeper) GetModel(ctx context.Context, modelID string) (types.ModelState, error) {
	state, exists, err := k.loadModel(ctx, modelID)
	if err != nil {
		return types.ModelState{}, err
	}
	if !exists {
		return types.ModelState{}, fmt.Errorf("model %s not found", modelID)
	}
	return state, nil
}

func (k Keeper) SetModelStatus(ctx context.Context, modelID string, newStatus types.ModelProfileStatus, reason types.GovernanceReason, height uint64) (types.ModelState, error) {
	return k.setModelStatusWithSource(ctx, modelID, newStatus, types.ModelStatusSourceGovernance, reason, height)
}

func (k Keeper) setModelStatusWithSource(ctx context.Context, modelID string, newStatus types.ModelProfileStatus, source types.ModelStatusSource, reason types.GovernanceReason, height uint64) (types.ModelState, error) {
	state, err := k.GetModel(ctx, modelID)
	if err != nil {
		return types.ModelState{}, err
	}
	if !isValidModelStatusTransition(state.Status, newStatus) {
		return types.ModelState{}, fmt.Errorf("cannot transition model %s from %s to %s", modelID, state.Status, newStatus)
	}
	oldStatus := state.Status
	state.Status, state.StatusSource, state.UpdatedHeight = newStatus, source, height
	if statusReturnsToAutoDerivation(newStatus) {
		state.StatusSource = types.ModelStatusSourceAutoProfile
	}
	if err := state.Validate(); err != nil {
		return types.ModelState{}, err
	}
	if statusDisablesSupport(newStatus) {
		if err := k.deactivateModelSupports(ctx, state.ModelId, types.ModelSupportDeactivateFrozen, height); err != nil {
			return types.ModelState{}, err
		}
	}
	if err := k.setModelState(ctx, state); err != nil {
		return types.ModelState{}, err
	}
	// The event keeps the trigger of *this* transition, which is what the enum
	// documents ("attributes one model status transition to its trigger"). The
	// persisted status_source above is the derivation lock, and the two only
	// coincide when the target is not REGISTERED; see statusReturnsToAutoDerivation.
	mustEmitHubEvent(ctx, &types.EventModelStateChanged{
		ModelId: modelID, OldStatus: oldStatus, NewStatus: newStatus, Source: source, Reason: reason,
	})
	if statusReturnsToAutoDerivation(newStatus) {
		if err := k.deriveModelStatus(ctx, &state, height); err != nil {
			return types.ModelState{}, err
		}
	}
	return state, nil
}

func (k Keeper) GetProfile(ctx context.Context, modelID string, profileVersion uint32) (types.ProfileState, error) {
	state, exists, err := k.loadProfile(ctx, modelID, profileVersion)
	if err != nil {
		return types.ProfileState{}, err
	}
	if !exists {
		return types.ProfileState{}, fmt.Errorf("profile %s/%d not found", modelID, profileVersion)
	}
	return state, nil
}

func (k Keeper) SetProfileStatus(ctx context.Context, modelID string, profileVersion uint32, newStatus types.ModelProfileStatus, height uint64) (types.ProfileState, error) {
	return k.setProfileStatusWithSource(ctx, modelID, profileVersion, newStatus, types.ProfileStatusSourceGovernance, height)
}
func (k Keeper) setProfileStatusWithSource(ctx context.Context, modelID string, profileVersion uint32, newStatus types.ModelProfileStatus, source types.ProfileStatusSource, height uint64) (types.ProfileState, error) {
	state, err := k.GetProfile(ctx, modelID, profileVersion)
	if err != nil {
		return types.ProfileState{}, err
	}
	if !isValidProfileStatusTransition(state.Status, newStatus) {
		return types.ProfileState{}, fmt.Errorf("cannot transition profile %s/%d from %s to %s", modelID, profileVersion, state.Status, newStatus)
	}
	oldStatus := state.Status
	state.Status, state.StatusSource, state.UpdatedHeight = newStatus, source, height
	if statusReturnsToAutoDerivation(newStatus) {
		state.StatusSource = types.ProfileStatusSourceAutoSupport
	}
	// Only the structural check belongs on a lifecycle transition, exactly like
	// the model-side sibling setModelStatusWithSource above.
	//
	// This path used to call validateProfileRegistrationState, which adds the
	// registration *admission* clamp min_stake >= params.Service.ServiceBondMinInitial
	// on top of state.Validate(). ProfileState.MinStake is frozen at registration
	// time while the parameter is a live governance value, so once the floor was
	// raised past a profile's recorded min_stake every governance transition of
	// that profile (FROZEN / DELISTED / back to REGISTERED) failed the clamp
	// before the write and the profile became permanently ungovernable. A
	// parameter change must not reinterpret an already-admitted profile; the
	// clamp belongs solely to RegisterModelProfileState. The asymmetry made this
	// worse rather than safer: setProfileEmergencyFrozen and
	// deriveProfileAndModelStatus only ever validated structurally, so the floor
	// bricked the governance entry alone and left the validator freeze quorum as
	// the only way to move such a profile.
	if err := state.Validate(); err != nil {
		return types.ProfileState{}, err
	}
	if statusDisablesSupport(newStatus) {
		if err := k.deactivateProfileSupports(ctx, modelID, profileVersion, types.ModelSupportDeactivateFrozen, height); err != nil {
			return types.ProfileState{}, err
		}
	}
	if err := k.setProfileState(ctx, state); err != nil {
		return types.ProfileState{}, err
	}
	if err := k.applyActiveProfileCountDelta(ctx, modelID, oldStatus, newStatus, height); err != nil {
		return types.ProfileState{}, err
	}
	// Source is the trigger of this transition, not the lock that was just written;
	// see the sibling comment in setModelStatusWithSource.
	mustEmitHubEvent(ctx, &types.EventModelProfileStateChanged{
		ModelId: modelID, ProfileVersion: profileVersion,
		OldStatus: oldStatus, NewStatus: newStatus, Source: source,
	})
	// The unfreeze handed the row back to the support aggregates, so the aggregates
	// decide the status from here. Re-running the derivation inside the same Tx is
	// what keeps the API contract rule 5 ("the response returns the
	// persisted status") honest: an
	// unfreeze that lands on aggregates still above the activation thresholds
	// persists ACTIVE, and the response reports ACTIVE rather than a REGISTERED that
	// no longer exists in the store. REGISTERED -> ACTIVE is a legal matrix edge, so
	// the two-step FROZEN -> REGISTERED -> ACTIVE walk never invents the illegal
	// FROZEN -> ACTIVE edge that a single fused write would have produced.
	if statusReturnsToAutoDerivation(newStatus) {
		if err := k.deriveProfileAndModelStatus(ctx, &state, height); err != nil {
			return types.ProfileState{}, err
		}
	}
	return state, nil
}

// applyActiveProfileCountDelta keeps ModelState.active_profile_count in step with
// a single profile status write in O(1).
//
// Neither setProfileStatusWithSource nor setProfileEmergencyFrozen used to touch
// the counter. Before P0-3 an ACTIVE -> FROZEN transition happened to be repaired
// indirectly: the synchronous supporter fan-out zeroed every support row, and
// deriveProfileAndModelStatus then saw StatusSource == AUTO_SUPPORT, dropped the
// profile back to REGISTERED and decremented there. With the fan-out now
// asynchronous, status and StatusSource land as GOVERNANCE/EMERGENCY first and
// the cursor drain never recomputes them, so the counter would drift up forever.
// Governance setting a profile straight to ACTIVE never incremented it either.
//
// Model-level freezes are unaffected: their profiles keep StatusSource ==
// AUTO_SUPPORT, so deriveProfileAndModelStatus still owns those transitions.
func (k Keeper) applyActiveProfileCountDelta(ctx context.Context, modelID string, oldStatus, newStatus types.ModelProfileStatus, height uint64) error {
	wasActive := oldStatus == types.ModelStatusActive
	isActive := newStatus == types.ModelStatusActive
	if wasActive == isActive {
		return nil
	}
	model, err := k.GetModel(ctx, modelID)
	if err != nil {
		return err
	}
	if isActive {
		model.ActiveProfileCount, err = checkedAddUint32(model.ActiveProfileCount, 1)
		if err != nil {
			return fmt.Errorf("model %s active_profile_count overflow: %w", modelID, err)
		}
	} else {
		if model.ActiveProfileCount == 0 {
			return fmt.Errorf("model %s active_profile_count underflow", modelID)
		}
		model.ActiveProfileCount--
	}
	model.UpdatedHeight = height
	return k.setModelState(ctx, model)
}

func (k Keeper) loadModel(ctx context.Context, modelID string) (types.ModelState, bool, error) {
	if err := types.ValidateModelID(modelID); err != nil {
		return types.ModelState{}, false, err
	}
	state, err := k.Model.Get(ctx, modelID)
	if errors.Is(err, collections.ErrNotFound) {
		return types.ModelState{}, false, nil
	}
	return state, err == nil, err
}
func (k Keeper) loadProfile(ctx context.Context, modelID string, profileVersion uint32) (types.ProfileState, bool, error) {
	if err := types.ValidateModelID(modelID); err != nil {
		return types.ProfileState{}, false, err
	}
	state, err := k.Profile.Get(ctx, types.NewProfileStateKey(modelID, profileVersion))
	if errors.Is(err, collections.ErrNotFound) {
		return types.ProfileState{}, false, nil
	}
	return state, err == nil, err
}

func isValidModelStatusTransition(from, to types.ModelProfileStatus) bool {
	return isValidProfileStatusTransition(from, to)
}

func isValidProfileStatusTransition(from, to types.ModelProfileStatus) bool {
	if from == to {
		return false
	}
	switch from {
	case types.ModelStatusRegistered:
		return to == types.ModelStatusActive || to == types.ModelStatusFrozen || to == types.ModelStatusEmergencyFrozen || to == types.ModelStatusDelisted
	case types.ModelStatusActive:
		return to == types.ModelStatusFrozen || to == types.ModelStatusEmergencyFrozen || to == types.ModelStatusDelisted
	case types.ModelStatusFrozen:
		return to == types.ModelStatusRegistered || to == types.ModelStatusEmergencyFrozen || to == types.ModelStatusDelisted
	case types.ModelStatusEmergencyFrozen:
		return to == types.ModelStatusRegistered || to == types.ModelStatusDelisted
	default:
		return false
	}
}
func statusDisablesSupport(status types.ModelProfileStatus) bool {
	return status == types.ModelStatusFrozen || status == types.ModelStatusEmergencyFrozen || status == types.ModelStatusDelisted
}

// statusReturnsToAutoDerivation reports whether landing on `status` hands the row
// back to the automatic status derivation, i.e. whether status_source must be
// reset to AUTO_PROFILE / AUTO_SUPPORT. Model and profile share the predicate for
// the same reason isParentModelOpenForProfile and isProfileOpenForOrders do: the
// two sides must not drift apart.
//
// REGISTERED is the only such status. It is the base status that
// deriveProfileAndModelStatus itself writes whenever the aggregates fall back
// below the activation thresholds — the absence of a verdict, never a verdict.
// The three statuses statusDisablesSupport covers are verdicts no support
// statistic can produce, and those stay locked: the API contract:2563
// "the governance freeze/delist statuses may only be rewritten by the governance
// path", the data-structure contract:698 "governance or EmergencyFreeze
// may still change a model/profile status to FROZEN / EMERGENCY_FROZEN / DELISTED"
// — REGISTERED is deliberately absent from that list.
//
// Without the reset the lock was permanent and ACTIVE became unreachable for the
// rest of the chain's life. The derivation is gated on the AUTO sources
// (the data-structure contract:696 "the automatic aggregation may rewrite
// the model status only when status_source=AUTO_PROFILE"), and it is the *only*
// writer of ACTIVE, because both governance entries refuse that target outright
// (msg_server_registry.go SetModelStatus / SetProfileStatus,
// the API contract:2225 "ACTIVE is derived only from valid support,
// the P30 activation facts and the thresholds"). So a GOVERNANCE/EMERGENCY-stamped
// REGISTERED row had no path to ACTIVE at all: not a Msg, not the derivation, not
// Genesis. That contradicts the data-structure contract:698 "once the
// profile-local supporter count and support stake ratio thresholds are reached,
// ProfileState.status enters ACTIVE automatically from REGISTERED ... no extra
// 'apply for ACTIVE' transaction is needed" and the API contract
// §9.6a:1499 "a governance unfreeze of FROZEN returns to REGISTERED, not directly
// to ACTIVE" — "not directly" presupposes that it does get there indirectly.
func statusReturnsToAutoDerivation(status types.ModelProfileStatus) bool {
	return status == types.ModelStatusRegistered
}

// isParentModelOpenForProfile and isProfileOpenForOrders are named views of one
// predicate, types.IsModelProfileStatusOpen, so the model and profile sides can
// never drift apart.
func isParentModelOpenForProfile(status types.ModelProfileStatus) bool {
	return types.IsModelProfileStatusOpen(status)
}
func isProfileOpenForOrders(status types.ModelProfileStatus) bool {
	return types.IsModelProfileStatusOpen(status)
}

// validateProfileRegistrationState enforces the single minimum-deposit value.
//
// Ruling 16: the floor is params.Service.ServiceBondMinInitial (§18.0 field 12).
// The repo previously carried two: the unregistered keeper constant
// MinServiceBond = 500_000 used here, and ServiceBondMinInitial = 1_000_000 used
// by prepareRegisterService. Behaviour change: with default params the profile
// min_stake floor rises from 500_000 to 1_000_000.
func validateProfileRegistrationState(state types.ProfileState, serviceBondMinInitial uint64) error {
	if err := state.Validate(); err != nil {
		return err
	}
	if state.MinStake < serviceBondMinInitial {
		return fmt.Errorf("profile min_stake %d below service bond floor %d", state.MinStake, serviceBondMinInitial)
	}
	return nil
}

// serviceBondMinInitial resolves the single registered minimum-deposit parameter.
func (k Keeper) serviceBondMinInitial(ctx context.Context) (uint64, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return 0, err
	}
	return types.AmountToUint64(params.Service.ServiceBondMinInitial, false)
}

func validateProfileStateWithParams(state types.ProfileState, params types.HubParamsV2) error {
	if err := state.Validate(); err != nil {
		return err
	}
	return types.ValidatePricingProfile(state.PricingProfile, state.RefPrice, params.Price)
}

func (k Keeper) validateStoredProfile(ctx context.Context, state types.ProfileState) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	return validateProfileStateWithParams(state, params)
}
