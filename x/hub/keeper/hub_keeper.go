package keeper

import (
	"strings"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (k Keeper) GetNodeJailStatus(ctx sdk.Context, addr sdk.AccAddress, duty string) types.JailStatus {
	if _, err := parseTaskLiabilityDuty(normalizeServiceRole(duty)); err != nil {
		return ""
	}
	bond, exists, err := k.loadServiceBond(ctx, addr.String())
	if err != nil || !exists {
		return ""
	}
	if bond.Status == types.ServiceBondStatusTombstoned {
		return types.JailStatusTombstoned
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return ""
	}
	if bond.JailCount >= params.Service.TombstoneJailCountThreshold {
		return types.JailStatusTombstoned
	}
	if bond.JailCount != 0 || bond.Status == types.ServiceBondStatusJailed {
		return types.JailStatusJailed
	}
	return ""
}

func (k Keeper) GetNodeTombstone(ctx sdk.Context, addr sdk.AccAddress) bool {
	tombstoned, err := k.IsTombstoned(ctx, addr.String())
	return err == nil && tombstoned
}

func (k Keeper) GetCortexNode(ctx sdk.Context, addr sdk.AccAddress) (types.CortexNodeSnapshot, bool) {
	state, err := k.ReadCortexNodeStore(ctx, addr.String())
	if err != nil {
		return types.CortexNodeSnapshot{}, false
	}
	return types.CortexNodeSnapshot{
		OperatorAddress: state.OperatorAddress, ServiceAuthorizationNonce: state.ServiceAuthorizationNonce,
		ServiceKeyStatus: state.ServiceKeyStatus, PendingStageDutyCount: state.PendingStageDutyCount,
	}, true
}

func (k Keeper) GetProfileCapability(ctx sdk.Context, provider sdk.AccAddress, modelID string, profileVersion uint32) (types.ProfileCapabilitySnapshot, bool) {
	if err := types.ValidateModelID(modelID); err != nil {
		return types.ProfileCapabilitySnapshot{}, false
	}
	state, err := k.ProfileCapability.Get(ctx, types.NewProfileCapabilityKey(provider.String(), modelID, profileVersion))
	if err != nil {
		return types.ProfileCapabilitySnapshot{}, false
	}
	return profileCapabilitySnapshot(state), true
}

func (k Keeper) GetModelSupport(ctx sdk.Context, provider sdk.AccAddress, modelID string, profileVersion uint32) (types.ModelSupportSnapshot, bool) {
	if err := types.ValidateModelID(modelID); err != nil {
		return types.ModelSupportSnapshot{}, false
	}
	state, err := k.ModelSupport.Get(ctx, types.NewModelSupportKey(provider.String(), modelID, profileVersion))
	if err != nil {
		return types.ModelSupportSnapshot{}, false
	}
	snapshot := modelSupportSnapshot(state)
	currentEpoch := uint64(0)
	if ctx.BlockHeight() > 0 {
		// Freshness is an epoch comparison, so a defaulted epoch length here would
		// answer "still fresh" for support that expired under the configured one.
		// The reader already has a fail-closed channel; use it.
		epochLength, err := k.epochLengthBlocks(ctx)
		if err != nil {
			return types.ModelSupportSnapshot{}, false
		}
		currentEpoch = epochForHeight(uint64(ctx.BlockHeight()), epochLength)
	}
	if snapshot.DeclaredSupport && (snapshot.SupportFreshUntilEpoch == 0 || currentEpoch >= snapshot.SupportFreshUntilEpoch) {
		snapshot.DeclaredSupport = false
		snapshot.SupportActive = false
	}
	return snapshot, true
}

func (k Keeper) GetModelStatus(ctx sdk.Context, modelID string) types.ModelStatus {
	if err := types.ValidateModelID(modelID); err != nil {
		return types.ModelStatusUnspecified
	}
	state, err := k.Model.Get(ctx, modelID)
	if err != nil {
		return types.ModelStatusUnspecified
	}
	return state.Status
}

func (k Keeper) GetProfileState(ctx sdk.Context, modelID string, profileVersion uint32) (types.ProfileStateSnapshot, bool) {
	if err := types.ValidateModelID(modelID); err != nil {
		return types.ProfileStateSnapshot{}, false
	}
	state, err := k.Profile.Get(ctx, types.NewProfileStateKey(modelID, profileVersion))
	if err != nil {
		return types.ProfileStateSnapshot{}, false
	}
	execution := shared.ProfileExecutionSnapshot{
		ManifestHash: append([]byte(nil), state.ManifestHash...), TokenizerHash: append([]byte(nil), state.TokenizerHash...),
		RuntimeClass: state.RuntimeClass, RequiredTopK: state.RequiredTopK, GenerationType: state.GenerationType,
		VerificationProfile: state.VerificationProfile, VerificationThresholds: state.VerificationThresholds,
		BatchVerification: state.BatchVerification, SchemaHash: append([]byte(nil), state.SchemaHash...),
	}
	executionHash, err := types.ProfileExecutionSnapshotHash(execution)
	if err != nil {
		return types.ProfileStateSnapshot{}, false
	}
	return types.ProfileStateSnapshot{
		ModelID: state.ModelId, ProfileVersion: state.ProfileVersion, Status: state.Status,
		TaskTypes: append([]shared.TaskType(nil), state.TaskTypes...), ResourceTier: state.ResourceTier,
		MinStake: state.MinStake, ChallengeOpenWindowBlocks: state.ChallengeOpenWindowBlocks,
		ActiveSupporterCount: state.ActiveSupporterCount, ActiveSupportStake: state.ActiveSupportStake,
		StatusSource: state.StatusSource, ExecutionSnapshot: execution, ExecutionSnapshotHash: executionHash,
		PricingProfile: state.PricingProfile, RefPrice: state.RefPrice,
	}, true
}

func (k Keeper) IsProfileFrozen(ctx sdk.Context, modelID string, profileVersion uint32) bool {
	profile, exists := k.GetProfileState(ctx, modelID, profileVersion)
	if !exists {
		return false
	}
	return profile.Status == types.ModelStatusFrozen || profile.Status == types.ModelStatusEmergencyFrozen || profile.Status == types.ModelStatusDelisted
}

func (k Keeper) GetHubParams(ctx sdk.Context) types.HubParamsSnapshot {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return types.HubParamsSnapshot{}
	}
	return types.HubParamsSnapshot{
		EpochLengthBlocks: normalizedEpochLengthBlocks(params), DeltaWBlocks: params.Epoch.DeltaWBlocks,
		ServiceUnbondingPeriodBlocks:          params.Service.ServiceUnbondingPeriodBlocks,
		UnbondingSlashSafetyMarginBlocks:      params.Service.UnbondingSlashSafetyMarginBlocks,
		ObjectiveForgerySlashBps:              params.Service.ObjectiveForgerySlashBps,
		CandidateSlotHardCapacity:             params.CandidatePool.CandidateSlotHardCapacity,
		BuildersPerTask:                       params.Builder.BuildersPerTask,
		AssignmentBuilderProposalWindowBlocks: params.Builder.AssignmentBuilderProposalWindowBlocks,
		OpenVerifyBuilderProposalWindowBlocks: params.Builder.OpenVerifyBuilderProposalWindowBlocks,
		SettlementBuilderGraceBlocks:          params.Builder.SettlementBuilderGraceBlocks,
		FreezeFailureIndexRetentionBlocks:     params.Freeze.FreezeFailureIndexRetentionBlocks,
		MaxQueryPageTokenBytes:                params.QueryEvent.MaxQueryPageTokenBytes, MaxQueryPageLimit: params.QueryEvent.MaxQueryPageLimit,
		MaxQueryResponseBytes:        params.QueryEvent.MaxQueryResponseBytes,
		MaxEndblockVisitedItemsTotal: params.QueryEvent.MaxEndblockVisitedItemsTotal,
		BusinessDenom:                params.Phase0.BusinessDenom,
		EVMChainID:                   params.Phase0.EvmChainId,
	}
}

func (k Keeper) GetServiceBond(ctx sdk.Context, cortexNode sdk.AccAddress, orderValue uint64) (types.ServiceBondSnapshot, bool) {
	if orderValue == 0 {
		return types.ServiceBondSnapshot{}, false
	}
	state, err := k.ReadServiceBondValue(ctx, types.NewServiceBondKey(cortexNode.String()))
	if err != nil {
		return types.ServiceBondSnapshot{}, false
	}
	currentEpoch := uint64(0)
	if ctx.BlockHeight() > 0 {
		epochLength, err := k.epochLengthBlocks(ctx)
		if err != nil {
			return types.ServiceBondSnapshot{}, false
		}
		currentEpoch = epochForHeight(uint64(ctx.BlockHeight()), epochLength)
	}
	effectiveBond := types.EffectiveActiveBond(state, currentEpoch)
	availableBond, err := types.AvailableBond(state, currentEpoch)
	if err != nil {
		return types.ServiceBondSnapshot{}, false
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return types.ServiceBondSnapshot{}, false
	}
	slashBps, minLiability, orderCoverageBps, err := taskLiabilityEconomics(params.Service)
	if err != nil {
		return types.ServiceBondSnapshot{}, false
	}
	requiredLiability, err := types.RequiredTaskLiability(
		effectiveBond, orderValue, orderCoverageBps, slashBps, minLiability,
	)
	if err != nil {
		return types.ServiceBondSnapshot{}, false
	}
	return types.ServiceBondSnapshot{
		OperatorAddress: state.OperatorAddress, Status: state.Status,
		Amount:     sdk.NewCoin(params.Phase0.BusinessDenom, sdkmath.NewIntFromUint64(state.ActiveBond)),
		ActiveBond: state.ActiveBond, EffectiveActiveBond: effectiveBond, AvailableBond: availableBond,
		RequiredTaskLiability: requiredLiability,
		ReservedLiability:     state.ReservedLiability, BondVersion: state.BondVersion,
		EffectiveEpoch: state.EffectiveBondEpoch, PendingUnbonding: state.PendingUnbondingTotal,
	}, true
}

func (k Keeper) GetRoleScoringSnapshot(ctx sdk.Context, roleAddress sdk.AccAddress, role string) (types.RoleScoringSnapshot, error) {
	jail, err := k.GetJail(ctx, roleAddress.String(), role)
	if err != nil {
		return types.RoleScoringSnapshot{}, err
	}
	return types.RoleScoringSnapshot{
		PerformanceScorePpm: types.PerformanceScoreDefaultPpm,
		PerformanceVersion:  types.PerformanceScoreMethodVersionV1,
		JailCount:           jail.JailCount,
	}, nil
}

func (k Keeper) GetBeaconValue(ctx sdk.Context, height int64) (types.BeaconSnapshot, bool) {
	if height < 0 {
		return types.BeaconSnapshot{}, false
	}
	state, err := k.getBeaconStoreAtHeight(ctx, uint64(height))
	if err != nil || !state.Verified {
		return types.BeaconSnapshot{}, false
	}
	if state.SourceTag == types.BeaconSourceProposerVRFV1 && len(state.ProofDigest) != shared.Hash32KeySize {
		return types.BeaconSnapshot{}, false
	}
	if state.SourceTag == types.BeaconSourcePlaceholderBlockHashV1 && len(state.ProofDigest) != 0 {
		return types.BeaconSnapshot{}, false
	}
	return types.BeaconSnapshot{Height: int64(state.Height), Randomness: append([]byte(nil), state.Randomness...), SourceTag: state.SourceTag, ProofDigest: append([]byte(nil), state.ProofDigest...)}, true
}

func (k Keeper) GetBeaconForDomain(ctx sdk.Context, domain string, height int64) (types.BeaconSnapshot, bool) {
	if strings.TrimSpace(domain) == "" {
		return types.BeaconSnapshot{}, false
	}
	if state, ok := k.GetBeaconValue(ctx, height); ok {
		return state, true
	}
	if height < 0 {
		return types.BeaconSnapshot{}, false
	}
	// GetBeaconValue is deliberately verified-only. Domain consumers may use
	// the stored block-hash placeholder only while the committed activation
	// policy still permits it; once VRF is required there is no fallback.
	params, err := k.Params.Get(ctx)
	if err != nil {
		return types.BeaconSnapshot{}, false
	}
	if params.Beacon.VrfRequiredFromHeight != 0 && uint64(height) >= params.Beacon.VrfRequiredFromHeight {
		return types.BeaconSnapshot{}, false
	}
	state, err := k.getBeaconStoreAtHeight(ctx, uint64(height))
	if err != nil || state.SourceTag != types.BeaconSourcePlaceholderBlockHashV1 || state.Verified || len(state.ProofDigest) != 0 {
		return types.BeaconSnapshot{}, false
	}
	return types.BeaconSnapshot{
		Height: int64(state.Height), Randomness: append([]byte(nil), state.Randomness...), SourceTag: state.SourceTag,
	}, true
}

func profileCapabilitySnapshot(state types.ProfileCapabilityState) types.ProfileCapabilitySnapshot {
	return types.ProfileCapabilitySnapshot{
		OperatorAddress: state.OperatorAddress, ModelID: state.ModelId, ProfileVersion: state.ProfileVersion,
		InferenceCapability: state.InferenceCapability, VerificationCapability: state.VerificationCapability,
		CapabilityVersion: state.CapabilityVersion,
	}
}

func modelSupportSnapshot(state types.ModelSupportState) types.ModelSupportSnapshot {
	return types.ModelSupportSnapshot{
		OperatorAddress: state.OperatorAddress, ModelID: state.ModelId, ProfileVersion: state.ProfileVersion,
		DeclaredSupport: state.DeclaredSupport, SupportActive: state.SupportActive, ActivationKind: state.ActivationKind,
		FirstActivationDuty: state.FirstActivationDuty, FirstSupportTaskID: append([]byte(nil), state.FirstSupportTaskId...),
		FirstSupportOrderValue: state.FirstSupportOrderValue, P30CutoffEpoch: state.GetP30CutoffEpoch(),
		P30Bootstrap:           state.GetP30Bootstrap(),
		SupportFreshUntilEpoch: state.SupportFreshUntilEpoch, ActiveSupportStakeSnapshot: state.ActiveSupportStakeSnapshot,
		EligibleSupportStakeSnapshot: state.EligibleSupportStakeSnapshot, SupportVersion: state.SupportVersion,
	}
}
