package keeper

import (
	"bytes"
	"context"
	"errors"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// UpdateHubParams implements the keeper_api_contract.md §18.0 ordering exactly:
// assertAuthority -> Params.Validate -> expected_version match ->
// per-field genesis-only rejection -> cross-field clamp -> one atomic write of
// params and its meta row -> event 110.
//
// The previous implementation never read req.ExpectedVersion, kept no params
// version state, answered with an all-zero response and emitted no event. It also
// compared whole submessages (`!current.Support.Equal(next.Support)`), which
// rejected purely operational changes such as
// max_model_support_prune_items_per_block, and gated the genesis-only fields on
// "are there rows yet", which §18.0 does not allow.
func (m msgServer) UpdateHubParams(ctx context.Context, req *types.MsgUpdateHubParams) (*types.MsgUpdateHubParamsResponse, error) {
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrInvalidSigner, "nil request")
	}
	authority, err := m.k.addressCodec.StringToBytes(req.Authority)
	if err != nil || !bytes.Equal(authority, m.k.authority) {
		return nil, errorsmod.Wrap(types.ErrInvalidSigner, "authority does not match hub authority")
	}
	if err := req.Params.Validate(); err != nil {
		return nil, err
	}
	current, err := m.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	meta, err := m.k.GetHubParamsMeta(ctx)
	if err != nil {
		return nil, err
	}
	if req.ExpectedVersion != meta.ParamsVersion {
		return nil, errorsmod.Wrapf(
			types.ErrHubParamsVersionMismatch,
			"expected_version %d does not match current params_version %d",
			req.ExpectedVersion, meta.ParamsVersion,
		)
	}
	if field, changed := types.GenesisOnlyHubParamsChanged(current, req.Params); changed {
		if field.RequiresSupportReindex {
			return nil, errorsmod.Wrap(types.ErrActiveSupportReindexRequired, field.Name)
		}
		return nil, errorsmod.Wrap(types.ErrHubParamsGenesisOnly, field.Name)
	}
	if err := m.validateCrossModuleSafetyWindows(ctx, req.Params); err != nil {
		return nil, err
	}
	newVersion, err := checkedAdd(meta.ParamsVersion, 1)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "params_version overflow")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	paramsHash, err := types.HubParamsHash(sdkCtx.ChainID(), newVersion, req.Params)
	if err != nil {
		return nil, err
	}
	nextMeta := types.HubParamsMetaState{
		ParamsVersion: newVersion,
		ParamsHash:    paramsHash,
		UpdatedHeight: sdkContextHeight(sdkCtx),
	}
	if err := nextMeta.Validate(); err != nil {
		return nil, err
	}
	// One atomic pair: a params row without its meta row would make every later
	// expected_version check unverifiable.
	cacheCtx, commit := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	if err := m.k.Params.Set(cache, req.Params); err != nil {
		return nil, err
	}
	if err := m.k.ParamsMeta.Set(cache, nextMeta); err != nil {
		return nil, err
	}
	commit()

	// CONTRACT-GAP: keeper_api_contract.md §5.11's ProtocolEventPrimaryLocatorV1 oneof
	// has no params/global branch, so code 110 has no legal primary locator to
	// carry. The typed payload below is emitted without one; the document side
	// must add the branch.
	mustEmitHubEvent(ctx, &types.EventHubParamsUpdated{
		OldVersion: meta.ParamsVersion,
		NewVersion: newVersion,
		ParamsHash: append([]byte(nil), paramsHash...),
	})
	return &types.MsgUpdateHubParamsResponse{
		NewVersion: newVersion,
		ParamsHash: append([]byte(nil), paramsHash...),
		Status:     shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED,
	}, nil
}

// GetHubParamsMeta returns the params version bookkeeping row. A fresh store that
// predates the first update has no row; version 0 is then the only value an
// expected_version can legally match.
func (k Keeper) GetHubParamsMeta(ctx context.Context) (types.HubParamsMetaState, error) {
	meta, err := k.ParamsMeta.Get(ctx)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.HubParamsMetaState{}, nil
		}
		return types.HubParamsMetaState{}, err
	}
	return meta, nil
}

func (m msgServer) validateCrossModuleSafetyWindows(ctx context.Context, params types.HubParamsV2) error {
	if m.taskSafetyWindows == nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, "task safety window dependency is unavailable")
	}
	windows, err := m.taskSafetyWindows.GetTaskSafetyWindows(ctx)
	if err != nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	if err := types.ValidateServiceUnbondingCoverage(
		params.Service.ServiceUnbondingPeriodBlocks,
		params.Service.UnbondingSlashSafetyMarginBlocks,
		types.ProfileChallengeOpenWindowMaxBlocks,
		windows.ChallengeResolveWindowBlocks,
		windows.EvidenceResponseWindowBlocks,
	); err != nil {
		return err
	}
	return m.k.validateFreezeValidatorHistoryCoverage(ctx, params)
}

// ClaimEarnings is §14's pull path.
func (m *msgServer) ClaimEarnings(ctx context.Context, req *types.MsgClaimEarnings) (*types.MsgClaimEarningsResponse, error) {
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrInvalidEarnings, "nil request")
	}
	_, signer, err := m.k.requireCanonicalAddress("signer_address", req.SignerAddress)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidUserAddress, err.Error())
	}
	result, err := m.k.ClaimEarnings(ctx, signer, req.ClaimClass)
	if err != nil {
		return nil, err
	}
	remaining, err := shared.ParseAmount(result.State.ClaimableAmount)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidEarnings, err.Error())
	}
	if result.MaturedItems > uint64(^uint32(0)) {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "matured item count exceeds uint32")
	}
	mutationStatus := shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP
	if result.Mutated {
		mutationStatus = shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED
	}
	return &types.MsgClaimEarningsResponse{
		Beneficiary:        result.Address,
		ClaimedAmount:      shared.NewAmount(result.Amount),
		RemainingClaimable: shared.NewAmount(remaining),
		MaturedItems:       uint32(result.MaturedItems),
		Status:             mutationStatus,
	}, nil
}

// RunRewardEpoch exposes the same bounded cursor used by EndBlock. The runner
// emits only the business events produced by individual reward operations.
func (m *msgServer) RunRewardEpoch(ctx context.Context, req *types.MsgRunRewardEpoch) (*types.MsgRunRewardEpochResponse, error) {
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrInvalidEarnings, "nil request")
	}
	if _, _, err := m.k.requireCanonicalAddress("submitter_address", req.SubmitterAddress); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidUserAddress, err.Error())
	}
	if !validRewardBucket(req.RewardBucket) {
		return nil, errorsmod.Wrap(types.ErrInvalidEarnings, "reward_bucket is invalid")
	}
	if req.MaxItems == 0 {
		return nil, errorsmod.Wrap(types.ErrInvalidEarnings, "max_items must be non-zero")
	}
	params, err := m.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if req.MaxItems > params.Reward.MaxRewardEpochItemsPerBlock {
		return nil, errorsmod.Wrapf(types.ErrInvalidEarnings, "max_items %d exceeds configured cap %d", req.MaxItems, params.Reward.MaxRewardEpochItemsPerBlock)
	}
	result, err := m.k.RunRewardEpoch(ctx, req.Epoch, req.RewardBucket, req.MaxItems)
	if err != nil {
		return nil, err
	}
	if result.Visited > uint64(^uint32(0)) || result.Advanced > uint64(^uint32(0)) {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "reward epoch counters exceed uint32")
	}
	mutationStatus := shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP
	if result.Mutated {
		mutationStatus = shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED
	}
	return &types.MsgRunRewardEpochResponse{
		Phase:    result.Phase,
		Visited:  uint32(result.Visited),
		Advanced: uint32(result.Advanced),
		Status:   mutationStatus,
	}, nil
}

// Per-class claim messages are deliberately absent from the frozen V1 wire:
//
//   - ClaimServiceReward / ClaimBuilderReward / ClaimInfrastructureReward:
//     MsgClaimServiceReward, MsgClaimBuilderReward and
//     MsgClaimInfrastructureReward are all gone. They were thin wrappers that
//     called ClaimEarnings with a fixed claim_class, so MsgClaimEarnings with
//     CLAIM_CLASS_V1_SERVICE / _BUILDER covers two of them exactly. The
//     infrastructure one has no successor at all: CLAIM_CLASS_V1 has no
//     INFRASTRUCTURE member and EarningsState has no infrastructure sub-ledger.
//   - RunBuilderRewardEpoch / RunTreasuryEpoch are replaced by the single
//     MsgRunRewardEpoch entry in the frozen tx.proto.
//
// The merged runner must assert:
//   - the epoch has ended (current_height >= next_epoch_start_height) before any
//     reward is closed — both deleted handlers checked this and it is the only
//     thing stopping an epoch from being closed twice with different data;
//   - the run is idempotent per (epoch, reward_bucket): RewardEpochCursorState's
//     phase plus the RewardAccrual double-spend ledger are the guards;
//   - Σ(builder payouts) <= the Builder 5% emission share with a deterministic
//     remainder rule, and every payout is booked through accrueReward.
