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
	proposerBytes, canonicalProposer, err := k.requireCanonicalAddress("proposer_address", proposer)
	if err != nil || canonicalProposer != proposer {
		return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, fmt.Errorf("proposer address must be canonical")
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, err
	}
	derivedModelID, err := types.DeriveModelIDV1(sdk.UnwrapSDKContext(ctx).ChainID(), projection.Source.Provider, projection.Source.RepoId, proposerBytes)
	if err != nil || !bytes.Equal(derivedModelID, projection.ModelId) {
		return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, fmt.Errorf("model_id does not match the derived repository identity")
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
			!bytes.Equal(receipt.ModelId, projection.ModelId) || receipt.ProfileVersion != projection.ProfileVersion ||
			profile.ProposerAddress != proposer || profile.RegistrationFeePaid != registrationFee {
			return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, fmt.Errorf("registration receipt invariant mismatch")
		}
		model, modelErr := k.GetModel(ctx, receipt.ModelId)
		if modelErr != nil {
			return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, modelErr
		}
		storedProjection := profileProjectionFromState(profile, model)
		storedProjection.MinStake = sdk.NewCoin(params.Phase0.BusinessDenom, sdkmath.NewIntFromUint64(profile.MinStake))
		storedProjection.RegistrationFee = sdk.NewCoin(params.Phase0.BusinessDenom, sdkmath.NewIntFromUint64(profile.RegistrationFeePaid))
		left, leftErr := types.CanonicalModelProfileProjection(projection)
		right, rightErr := types.CanonicalModelProfileProjection(storedProjection)
		if leftErr != nil || rightErr != nil || !bytes.Equal(left, right) {
			return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, fmt.Errorf("registration receipt projection mismatch")
		}
		return model, profile, receipt, true, nil
	} else if !errors.Is(err, collections.ErrNotFound) {
		return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, err
	}

	model, exists, err := k.loadModel(ctx, projection.ModelId)
	if err != nil {
		return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, err
	}
	var oldPendingHeight, recheckHeight uint64
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
			Status: types.ModelStatusRegistered, StatusSource: types.ModelStatusSourceAutoSupport,
			LatestProfileVersion: projection.ProfileVersion, RegistrationFeePaid: registrationFee,
			CreatedHeight: height, UpdatedHeight: height,
			SupportMinStake: minStake, Provider: projection.Source.Provider, RepoId: projection.Source.RepoId,
		}
	} else {
		oldPendingHeight = model.PendingEffectiveHeight
		if model.ProposerAddress != proposer || model.Provider != projection.Source.Provider || model.RepoId != projection.Source.RepoId {
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
		if minStake > model.SupportMinStake {
			if params.Service.MinStakeGracePeriodBlocks == 0 {
				return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, fmt.Errorf("min_stake_grace_period_blocks must be positive")
			}
			recheckHeight, err = checkedAdd(height, params.Service.MinStakeGracePeriodBlocks)
			if err != nil {
				return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, err
			}
			model.PendingSupportMinStake = minStake
			model.PendingEffectiveHeight = recheckHeight
		} else if minStake < model.SupportMinStake || oldPendingHeight != 0 {
			model.SupportMinStake = minStake
			model.PendingSupportMinStake = 0
			model.PendingEffectiveHeight = 0
			recheckHeight = height
		}
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
	if oldPendingHeight != 0 {
		if err := k.ModelSupportRecheckIndex.Remove(ctx, types.NewModelSupportRecheckIndexKey(oldPendingHeight, model.ModelId)); err != nil && !errors.Is(err, collections.ErrNotFound) {
			return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, err
		}
	}
	if recheckHeight != 0 {
		if err := k.ModelSupportRecheckIndex.Set(ctx, types.NewModelSupportRecheckIndexKey(recheckHeight, model.ModelId)); err != nil {
			return types.ModelState{}, types.ProfileState{}, types.RegistrationReceipt{}, false, err
		}
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
		StatusSource: types.ProfileStatusSourceGovernance, RegistrationFeePaid: fee,
		PreviousProfileVersion: p.PreviousProfileVersion, ProposerAddress: proposer,
		RegistrationDigest: append([]byte(nil), digest...), CreatedHeight: height, UpdatedHeight: height,
		RefPrice: p.PricingProfile.InitialOutputPrice,
		Source: shared.ProfileSourceRefV1{SourceUri: p.Source.SourceUri, Revision: p.Source.Revision,
			ResolverVersion: p.Source.ResolverVersion, RepoType: p.Source.RepoType},
		ToolCallParser: p.ToolCallParser, ReasoningParser: p.ReasoningParser,
	}
}

func profileProjectionFromState(s types.ProfileState, model types.ModelState) shared.ModelProfileProjection {
	return shared.ModelProfileProjection{
		ModelId: s.ModelId, ProfileVersion: s.ProfileVersion, ManifestHash: s.ManifestHash,
		TokenizerHash: s.TokenizerHash, RuntimeClass: s.RuntimeClass, RequiredTopK: s.RequiredTopK,
		TaskTypes: s.TaskTypes, GenerationType: s.GenerationType, ResourceTier: s.ResourceTier,
		ChallengeOpenWindowBlocks: s.ChallengeOpenWindowBlocks, VerificationProfile: s.VerificationProfile,
		VerificationThresholds: s.VerificationThresholds, BatchVerification: s.BatchVerification,
		PricingProfile: s.PricingProfile, TimeoutBootstrapProfile: s.TimeoutBootstrapProfile,
		SchemaHash: s.SchemaHash, PreviousProfileVersion: s.PreviousProfileVersion,
		Source: shared.SourceRefV1{Provider: model.Provider, RepoId: model.RepoId,
			SourceUri: s.Source.SourceUri, Revision: s.Source.Revision,
			ResolverVersion: s.Source.ResolverVersion, RepoType: s.Source.RepoType},
		ToolCallParser: s.ToolCallParser, ReasoningParser: s.ReasoningParser,
	}
}

func (k Keeper) GetModel(ctx context.Context, modelID []byte) (types.ModelState, error) {
	state, exists, err := k.loadModel(ctx, modelID)
	if err != nil {
		return types.ModelState{}, err
	}
	if !exists {
		return types.ModelState{}, fmt.Errorf("model %x not found", modelID)
	}
	return state, nil
}

func (k Keeper) SetModelStatus(ctx context.Context, modelID []byte, newStatus types.ModelProfileStatus, reason types.GovernanceReason, height uint64) (types.ModelState, error) {
	return k.setModelStatusWithSource(ctx, modelID, newStatus, types.ModelStatusSourceGovernance, reason, height)
}

func (k Keeper) setModelStatusWithSource(ctx context.Context, modelID []byte, newStatus types.ModelProfileStatus, source types.ModelStatusSource, reason types.GovernanceReason, height uint64) (types.ModelState, error) {
	if has, err := k.ModelSupportDeactivateCursor.Has(ctx, modelID); err != nil {
		return types.ModelState{}, err
	} else if has {
		return types.ModelState{}, fmt.Errorf("model support deactivation must finish before another model status change")
	}
	state, err := k.GetModel(ctx, modelID)
	if err != nil {
		return types.ModelState{}, err
	}
	if !isValidModelStatusTransition(state.Status, newStatus) {
		return types.ModelState{}, fmt.Errorf("cannot transition model %x from %s to %s", modelID, state.Status, newStatus)
	}
	oldStatus := state.Status
	state.Status, state.StatusSource, state.UpdatedHeight = newStatus, source, height
	if statusReturnsToAutoDerivation(newStatus) {
		state.StatusSource = types.ModelStatusSourceAutoSupport
	}
	if err := state.Validate(); err != nil {
		return types.ModelState{}, err
	}
	if statusDisablesSupport(newStatus) {
		// A threshold recheck in flight is meaningless once the model can no
		// longer support at all, and one model may only have one batch support
		// transition in flight at a time: clear it here rather than reject the
		// governance action, which must win over a not-yet-effective threshold
		// change.
		if state.PendingEffectiveHeight != 0 {
			if err := k.ModelSupportRecheckIndex.Remove(ctx, types.NewModelSupportRecheckIndexKey(state.PendingEffectiveHeight, state.ModelId)); err != nil && !errors.Is(err, collections.ErrNotFound) {
				return types.ModelState{}, err
			}
			state.PendingSupportMinStake, state.PendingEffectiveHeight = 0, 0
		}
		if err := k.ModelSupportRecheckCursor.Remove(ctx, state.ModelId); err != nil && !errors.Is(err, collections.ErrNotFound) {
			return types.ModelState{}, err
		}
		if err := k.EnqueueModelSupportDeactivation(ctx, state.ModelId); err != nil {
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

func (k Keeper) GetProfile(ctx context.Context, modelID []byte, profileVersion uint32) (types.ProfileState, error) {
	state, exists, err := k.loadProfile(ctx, modelID, profileVersion)
	if err != nil {
		return types.ProfileState{}, err
	}
	if !exists {
		return types.ProfileState{}, fmt.Errorf("profile %x/%d not found", modelID, profileVersion)
	}
	return state, nil
}

func (k Keeper) SetProfileStatus(ctx context.Context, modelID []byte, profileVersion uint32, newStatus types.ModelProfileStatus, height uint64) (types.ProfileState, error) {
	return k.setProfileStatusWithSource(ctx, modelID, profileVersion, newStatus, types.ProfileStatusSourceGovernance, height)
}
func (k Keeper) setProfileStatusWithSource(ctx context.Context, modelID []byte, profileVersion uint32, newStatus types.ModelProfileStatus, source types.ProfileStatusSource, height uint64) (types.ProfileState, error) {
	state, err := k.GetProfile(ctx, modelID, profileVersion)
	if err != nil {
		return types.ProfileState{}, err
	}
	if !isValidProfileStatusTransition(state.Status, newStatus) {
		return types.ProfileState{}, fmt.Errorf("cannot transition profile %x/%d from %s to %s", modelID, profileVersion, state.Status, newStatus)
	}
	oldStatus := state.Status
	state.Status, state.StatusSource, state.UpdatedHeight = newStatus, source, height
	if newStatus == types.ModelStatusRegistered {
		state.StatusSource = types.ProfileStatusSourceGovernance
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
	if err := k.setProfileState(ctx, state); err != nil {
		return types.ProfileState{}, err
	}
	// Source is the trigger of this transition, not the lock that was just written;
	// see the sibling comment in setModelStatusWithSource.
	mustEmitHubEvent(ctx, &types.EventModelProfileStateChanged{
		ModelId: modelID, ProfileVersion: profileVersion,
		OldStatus: oldStatus, NewStatus: newStatus, Source: source,
	})
	return state, nil
}

func (k Keeper) loadModel(ctx context.Context, modelID []byte) (types.ModelState, bool, error) {
	if len(modelID) != shared.Hash32KeySize {
		return types.ModelState{}, false, fmt.Errorf("model_id must be raw Hash32")
	}
	state, err := k.Model.Get(ctx, modelID)
	if errors.Is(err, collections.ErrNotFound) {
		return types.ModelState{}, false, nil
	}
	return state, err == nil, err
}
func (k Keeper) loadProfile(ctx context.Context, modelID []byte, profileVersion uint32) (types.ProfileState, bool, error) {
	if len(modelID) != shared.Hash32KeySize {
		return types.ProfileState{}, false, fmt.Errorf("model_id must be raw Hash32")
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
