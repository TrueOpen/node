package keeper

import (
	"bytes"
	"context"
	"strings"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func (m msgServer) SetModelStatus(ctx context.Context, req *types.MsgSetModelStatus) (*types.MsgSetModelStatusResponse, error) {
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrInvalidSigner, "nil request")
	}
	if err := m.k.assertAuthority(req.Authority); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.ModelId) == "" || strings.TrimSpace(req.ModelId) != req.ModelId || !types.IsValidModelStatus(req.NewStatus) {
		return nil, errorsmod.Wrap(types.ErrInvalidModel, "canonical model_id and new_status are required")
	}
	if err := types.ValidateModelID(req.ModelId); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidModel, err.Error())
	}
	if req.NewStatus == types.ModelStatusActive {
		return nil, errorsmod.Wrap(types.ErrInvalidModel, "ACTIVE status is derived from active profile support and cannot be set by governance")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	old, err := m.k.GetModel(cache, req.ModelId)
	if err != nil {
		return nil, err
	}
	if err := validateGovernanceStatusTransition(old.Status, req.NewStatus, req.ReasonCode, isValidModelStatusTransition); err != nil {
		return nil, err
	}
	state, err := m.k.SetModelStatus(cache, req.ModelId, req.NewStatus, req.ReasonCode, sdkContextHeight(cacheCtx))
	if err != nil {
		return nil, err
	}
	response := &types.MsgSetModelStatusResponse{
		OldStatus: old.Status, NewStatus: state.Status, LastStatusChangeHeight: state.UpdatedHeight,
		Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED,
	}
	commit()
	return response, nil
}

func (m msgServer) SetProfileStatus(ctx context.Context, req *types.MsgSetProfileStatus) (*types.MsgSetProfileStatusResponse, error) {
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrInvalidSigner, "nil request")
	}
	if err := m.k.assertAuthority(req.Authority); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.ModelId) == "" || strings.TrimSpace(req.ModelId) != req.ModelId || req.ProfileVersion == 0 || !types.IsValidModelStatus(req.NewStatus) {
		return nil, errorsmod.Wrap(types.ErrInvalidModel, "canonical model_id, profile_version, and new_status are required")
	}
	if err := types.ValidateModelID(req.ModelId); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidModel, err.Error())
	}
	if req.NewStatus == types.ModelStatusActive {
		return nil, errorsmod.Wrap(types.ErrInvalidModel, "ACTIVE status is derived from P30 support thresholds and cannot be set by governance")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	old, err := m.k.GetProfile(cache, req.ModelId, req.ProfileVersion)
	if err != nil {
		return nil, err
	}
	if err := validateGovernanceStatusTransition(old.Status, req.NewStatus, req.ReasonCode, isValidProfileStatusTransition); err != nil {
		return nil, err
	}
	state, err := m.k.SetProfileStatus(cache, req.ModelId, req.ProfileVersion, req.NewStatus, sdkContextHeight(cacheCtx))
	if err != nil {
		return nil, err
	}
	response := &types.MsgSetProfileStatusResponse{
		OldStatus: old.Status, NewStatus: state.Status, LastStatusChangeHeight: state.UpdatedHeight,
		Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED,
	}
	commit()
	return response, nil
}

func validateGovernanceStatusTransition(
	oldStatus, newStatus types.ModelProfileStatus,
	reason types.GovernanceReason,
	transitionAllowed func(types.ModelProfileStatus, types.ModelProfileStatus) bool,
) error {
	if newStatus == types.ModelStatusEmergencyFrozen {
		return errorsmod.Wrap(types.ErrInvalidModel, "EMERGENCY_FROZEN is reserved for the validator emergency quorum")
	}
	if !transitionAllowed(oldStatus, newStatus) {
		return errorsmod.Wrapf(types.ErrInvalidModel, "governance transition %s -> %s is not allowed", oldStatus, newStatus)
	}
	validReason := false
	switch newStatus {
	case types.ModelStatusFrozen:
		validReason = reason == types.GovernanceReason_GOVERNANCE_REASON_FREEZE
	case types.ModelStatusDelisted:
		validReason = reason == types.GovernanceReason_GOVERNANCE_REASON_DELIST
	case types.ModelStatusRegistered:
		validReason = (oldStatus == types.ModelStatusFrozen && reason == types.GovernanceReason_GOVERNANCE_REASON_UNFREEZE) ||
			(oldStatus == types.ModelStatusEmergencyFrozen && reason == types.GovernanceReason_GOVERNANCE_REASON_EMERGENCY_RECOVERY)
	}
	if !validReason {
		return errorsmod.Wrapf(types.ErrInvalidModel, "governance transition %s -> %s does not match reason %s", oldStatus, newStatus, reason)
	}
	return nil
}

func (m msgServer) StakeService(ctx context.Context, req *types.MsgStakeService) (response *types.MsgStakeServiceResponse, err error) {
	defer func() { err = classifyPublicServiceError(err, types.ErrInvalidServiceBond) }()
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrInvalidServiceBond, "nil request")
	}
	operatorBytes, operator, err := m.k.requireCanonicalAddress("operator_address", req.OperatorAddress)
	if err != nil {
		return nil, err
	}
	var amount uint64
	switch action := req.Action.Action.(type) {
	case *types.ServiceStakeActionV1_Register:
		if action.Register == nil {
			return nil, errorsmod.Wrap(types.ErrInvalidServiceBond, "register action is required")
		}
		amount, err = shared.ParseAmount(action.Register.Amount)
	case *types.ServiceStakeActionV1_TopUp:
		if action.TopUp == nil {
			return nil, errorsmod.Wrap(types.ErrInvalidServiceBond, "top_up action is required")
		}
		amount, err = shared.ParseAmount(action.TopUp.Amount)
	default:
		return nil, errorsmod.Wrap(types.ErrInvalidServiceBond, "exactly one stake action is required")
	}
	if err != nil || amount == 0 {
		return nil, errorsmod.Wrap(types.ErrInvalidServiceBond, "valid positive amount is required")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	height := sdkContextHeight(cacheCtx)
	epochLength, err := m.k.epochLengthBlocks(cache)
	if err != nil {
		return nil, err
	}
	var result StakeServiceResult
	switch action := req.Action.Action.(type) {
	case *types.ServiceStakeActionV1_Register:
		result, err = m.k.prepareRegisterService(
			cache, sdkCtx.ChainID(), operator, action.Register.ServicePubkey, action.Register.ServiceKeyProof,
			amount, height, epochForHeight(height, epochLength),
		)
	case *types.ServiceStakeActionV1_TopUp:
		result, err = m.k.prepareTopUpService(cache, operator, amount, height, epochForHeight(height, epochLength))
	}
	if err != nil {
		return nil, err
	}
	coins, err := m.k.hubCoins(cache, amount)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidServiceBond, err.Error())
	}
	if err := m.k.bankKeeper.SendCoinsFromAccountToModule(cache, sdk.AccAddress(operatorBytes), types.ServiceBondModuleName, coins); err != nil {
		return nil, errorsmod.Wrap(types.ErrInsufficientServiceBond, err.Error())
	}
	if err := m.k.persistStakeService(cache, result); err != nil {
		return nil, err
	}
	if err := m.k.syncCandidateSlotMembershipForEpoch(cache, operator, height, result.Bond.EffectiveBondEpoch); err != nil {
		return nil, err
	}
	emitServiceStakeChangedEventWithAmount(cache, result.Bond, amount)
	commit()
	return &types.MsgStakeServiceResponse{
		OperatorAddress: result.Node.OperatorAddress, ActiveBond: shared.NewAmount(result.Bond.ActiveBond),
		BondVersion: result.Bond.BondVersion, EffectiveEpoch: result.Bond.EffectiveBondEpoch,
		Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED,
	}, nil
}

func (m msgServer) DeclareModelSupport(ctx context.Context, req *types.MsgDeclareModelSupport) (*types.MsgDeclareModelSupportResponse, error) {
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrInvalidModelCapability, "nil request")
	}
	_, operator, err := m.k.requireCanonicalAddress("operator_address", req.OperatorAddress)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.ModelId) == "" || strings.TrimSpace(req.ModelId) != req.ModelId ||
		req.ProfileVersion == 0 || (!req.InferenceCapability && !req.VerificationCapability) {
		return nil, errorsmod.Wrap(types.ErrInvalidModelCapability, "canonical model_id, positive profile_version, and at least one capability are required")
	}
	if err := types.ValidateModelID(req.ModelId); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidModelCapability, err.Error())
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	height := sdkContextHeight(sdkCtx)
	cacheCtx, commit := sdkCtx.CacheContext()
	result, err := m.k.DeclareModelSupport(
		sdk.WrapSDKContext(cacheCtx), operator, req.ModelId, req.ProfileVersion,
		req.InferenceCapability, req.VerificationCapability,
		height,
	)
	if err != nil {
		return nil, err
	}
	commit()
	return &types.MsgDeclareModelSupportResponse{
		SupportVersion: result.Support.SupportVersion, SupportActive: result.Support.SupportActive,
		FreshUntilEpoch: result.Support.SupportFreshUntilEpoch, Status: result.Status,
	}, nil
}

func (m msgServer) BatchConfirmModelSupport(ctx context.Context, req *types.MsgBatchConfirmModelSupport) (*types.MsgBatchConfirmModelSupportResponse, error) {
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrInvalidSupportBatch, "nil request")
	}
	if _, _, err := m.k.requireCanonicalAddress("submitter_address", req.SubmitterAddress); err != nil {
		return nil, err
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	height := sdkContextHeight(sdkCtx)
	epochLength, err := m.k.epochLengthBlocks(ctx)
	if err != nil {
		return nil, err
	}
	currentEpoch := epochForHeight(height, epochLength)
	if req.EpochIndex != currentEpoch {
		return nil, errorsmod.Wrapf(types.ErrInvalidSupportBatch, "epoch_index %d does not match current epoch %d", req.EpochIndex, currentEpoch)
	}
	cacheCtx, commit := sdkCtx.CacheContext()
	result, err := m.k.processModelSupportBatch(sdk.WrapSDKContext(cacheCtx), sdkCtx.ChainID(), req.EpochIndex, req.Confirmations, height, uint64(req.Size()))
	if err != nil {
		return nil, err
	}
	commit()
	return &types.MsgBatchConfirmModelSupportResponse{
		AcceptedConfirmations: result.AcceptedConfirmations, RefreshedProfileCount: result.RefreshedProfiles,
		BatchDigest: result.BatchDigest, Status: result.Status,
	}, nil
}

func (m msgServer) BeginServiceUnstake(ctx context.Context, req *types.MsgBeginServiceUnstake) (response *types.MsgBeginServiceUnstakeResponse, err error) {
	defer func() { err = classifyPublicServiceError(err, types.ErrInvalidServiceBond) }()
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrInvalidServiceBond, "nil request")
	}
	_, operator, err := m.k.requireCanonicalAddress("operator_address", req.OperatorAddress)
	if err != nil {
		return nil, err
	}
	amount, err := shared.ParseAmount(req.Amount)
	if err != nil || amount == 0 {
		return nil, errorsmod.Wrap(types.ErrInvalidServiceBond, "valid positive amount is required")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	bond, unbonding, err := m.k.beginServiceUnstake(sdk.WrapSDKContext(cacheCtx), sdkCtx.ChainID(), operator, amount, sdkContextHeight(cacheCtx))
	if err != nil {
		return nil, err
	}
	commit()
	return &types.MsgBeginServiceUnstakeResponse{
		UnbondingId: append([]byte(nil), unbonding.UnbondingId...), MatureHeight: unbonding.MatureHeight,
		RemainingActiveBond: shared.NewAmount(bond.ActiveBond), Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED,
	}, nil
}

func (m msgServer) WithdrawServiceUnbonded(ctx context.Context, req *types.MsgWithdrawServiceUnbonded) (response *types.MsgWithdrawServiceUnbondedResponse, err error) {
	defer func() { err = classifyPublicServiceError(err, types.ErrInvalidServiceBond) }()
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrInvalidServiceBond, "nil request")
	}
	operatorBytes, operator, err := m.k.requireCanonicalAddress("operator_address", req.OperatorAddress)
	if err != nil {
		return nil, err
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	var result WithdrawServiceUnbondedResult
	switch locator := req.Locator.Locator.(type) {
	case *types.UnbondingLocatorV1_ById:
		if locator.ById == nil {
			return nil, errorsmod.Wrap(types.ErrInvalidServiceBond, "by_id locator is required")
		}
		result, err = m.k.withdrawServiceUnbondedByID(cache, sdkCtx.ChainID(), operator, locator.ById.Id, sdkContextHeight(cacheCtx))
	case *types.UnbondingLocatorV1_Batch:
		if locator.Batch == nil {
			return nil, errorsmod.Wrap(types.ErrInvalidServiceBond, "batch locator is required")
		}
		result, err = m.k.withdrawServiceUnbondedBatch(cache, sdkCtx.ChainID(), operator, locator.Batch.MaxItems, sdkContextHeight(cacheCtx))
	default:
		return nil, errorsmod.Wrap(types.ErrInvalidServiceBond, "exactly one unbonding locator is required")
	}
	if err != nil {
		return nil, err
	}
	if result.Status == shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED && result.WithdrawnAmount > 0 {
		coins, err := m.k.hubCoins(cache, result.WithdrawnAmount)
		if err != nil {
			return nil, errorsmod.Wrap(types.ErrInvalidServiceBond, err.Error())
		}
		if err := m.k.bankKeeper.SendCoinsFromModuleToAccount(cache, types.ServiceBondModuleName, sdk.AccAddress(operatorBytes), coins); err != nil {
			return nil, err
		}
	}
	commit()
	return &types.MsgWithdrawServiceUnbondedResponse{
		WithdrawnAmount: shared.NewAmount(result.WithdrawnAmount), WithdrawnItems: result.WithdrawnItems, Status: result.Status,
	}, nil
}

func (k Keeper) assertAuthority(authority string) error {
	bytesValue, err := k.addressCodec.StringToBytes(strings.TrimSpace(authority))
	if err != nil || !bytes.Equal(bytesValue, k.authority) {
		return errorsmod.Wrap(types.ErrInvalidSigner, "authority does not match hub authority")
	}
	return nil
}
