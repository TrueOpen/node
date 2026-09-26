package keeper

import (
	"bytes"
	"context"
	"errors"
	"math"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

func (m *msgServer) UpdateTaskParams(ctx context.Context, req *types.MsgUpdateTaskParams) (*types.MsgUpdateTaskParamsResponse, error) {
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrInvalidUserAddress, "nil request")
	}
	authority, err := m.k.addressCodec.StringToBytes(req.Authority)
	if err != nil || !bytes.Equal(authority, m.k.authority) {
		return nil, errorsmod.Wrap(types.ErrInvalidUserAddress, "authority does not match task authority")
	}
	if err := req.Params.Validate(); err != nil {
		return nil, err
	}
	current, err := m.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	meta, err := m.k.GetTaskParamsMeta(ctx)
	if err != nil {
		return nil, err
	}
	if req.ExpectedVersion != meta.ParamsVersion {
		return nil, errorsmod.Wrapf(
			types.ErrTaskParamsVersionMismatch,
			"expected_version %d does not match current params_version %d",
			req.ExpectedVersion,
			meta.ParamsVersion,
		)
	}
	if field, changed := types.GenesisOnlyTaskParamsChanged(current, req.Params); changed {
		return nil, errorsmod.Wrap(types.ErrTaskParamsGenesisOnly, field)
	}
	if group, changed := types.RuntimeImmutableTaskParamsChanged(current, req.Params); changed {
		return nil, errorsmod.Wrap(types.ErrTaskParamsRuntimeImmutable, group)
	}
	hubParams := m.k.hubKeeper.GetHubParams(sdk.UnwrapSDKContext(ctx))
	windows, err := m.k.GetTaskSafetyWindows(ctx)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	if err := hubtypes.ValidateServiceUnbondingCoverage(
		hubParams.ServiceUnbondingPeriodBlocks,
		hubParams.UnbondingSlashSafetyMarginBlocks,
		hubtypes.ProfileChallengeOpenWindowMaxBlocks,
		windows.ChallengeResolveWindowBlocks,
		windows.EvidenceResponseWindowBlocks,
	); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	if err := types.ValidateVerifyOpenClock(
		req.Params.Deadlines,
		hubParams.DeltaWBlocks,
		hubParams.OpenVerifyBuilderProposalWindowBlocks,
	); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	if req.Params.Proposals.MaxCandidateUnionMembersPerStage > hubParams.CandidateSlotHardCapacity {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "candidate union cap exceeds Hub candidate slot capacity")
	}
	if err := types.ValidateSessionCloseHorizon(req.Params, hubtypes.ProfileChallengeOpenWindowMaxBlocks); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	newVersion, overflow := checkedAddUint64(meta.ParamsVersion, 1)
	if overflow {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "params_version overflow")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	paramsHash, err := types.TaskParamsHashV1(sdkCtx.ChainID(), newVersion, req.Params)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	nextMeta := types.TaskParamsMetaState{
		ParamsVersion: newVersion,
		ParamsHash:    paramsHash,
		UpdatedHeight: uint64(sdkCtx.BlockHeight()),
	}
	if err := nextMeta.Validate(); err != nil {
		return nil, err
	}
	cacheCtx, commit := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	if err := m.k.Params.Set(cache, req.Params); err != nil {
		return nil, err
	}
	if err := m.k.ParamsMeta.Set(cache, nextMeta); err != nil {
		return nil, err
	}
	if err := emitTypedEvent(cache, &types.EventTaskParamsUpdated{
		OldVersion: meta.ParamsVersion,
		NewVersion: newVersion,
		ParamsHash: append([]byte(nil), paramsHash...),
	}); err != nil {
		return nil, err
	}
	commit()
	return &types.MsgUpdateTaskParamsResponse{
		NewVersion: newVersion,
		ParamsHash: append([]byte(nil), paramsHash...),
		Status:     shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED,
	}, nil
}

func (k Keeper) GetTaskParamsMeta(ctx context.Context) (types.TaskParamsMetaState, error) {
	meta, err := k.ParamsMeta.Get(ctx)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.TaskParamsMetaState{}, nil
		}
		return types.TaskParamsMetaState{}, err
	}
	return meta, nil
}

func (m *msgServer) CreateSession(ctx context.Context, req *types.MsgCreateSession) (*types.MsgCreateSessionResponse, error) {
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrInvalidUserAddress, "nil request")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	response, err := m.createSession(sdk.WrapSDKContext(cacheCtx), req)
	if err != nil {
		return nil, err
	}
	commit()
	return response, nil
}

func (m *msgServer) createSession(ctx context.Context, req *types.MsgCreateSession) (*types.MsgCreateSessionResponse, error) {
	ownerBytes, owner, err := m.k.canonicalAddress("signer_address", req.SignerAddress)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidUserAddress, err.Error())
	}
	nonceState, err := m.k.ReadSessionNonce(ctx, owner)
	if err != nil {
		if !errors.Is(err, collections.ErrNotFound) {
			return nil, err
		}
		nonceState = types.SessionNonceState{UserAddress: owner}
	}
	if nonceState.UserAddress != "" && nonceState.UserAddress != owner {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "session nonce key does not match state owner")
	}
	nonce := nonceState.NextSessionNonce
	if nonce == math.MaxUint64 {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "session nonce exhausted")
	}
	sessionID, err := DeriveSessionID(ownerBytes, nonce)
	if err != nil {
		return nil, err
	}
	sessionKey, err := sessionStoreKey(sessionID)
	if err != nil {
		return nil, err
	}
	if exists, err := m.k.Stream.Has(ctx, sessionKey); err != nil {
		return nil, err
	} else if exists {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "derived session_id already exists")
	}
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return nil, err
	}
	stream := types.StreamState{
		SessionId:        append([]byte(nil), sessionID...),
		OwnerUserAddress: owner,
		LastActiveHeight: height,
		Status:           types.SessionStatus_SESSION_STATUS_ACTIVE,
	}
	if err := m.k.setStreamState(ctx, stream); err != nil {
		return nil, err
	}
	if err := m.k.SessionByOwnerIndex.Set(ctx, types.NewSessionByOwnerKey(owner, sessionKey)); err != nil {
		return nil, err
	}
	nonceState.UserAddress = owner
	nonceState.NextSessionNonce = nonce + 1
	if err := m.k.WriteSessionNonce(ctx, owner, nonceState); err != nil {
		return nil, err
	}
	if err := emitTypedEvent(ctx, &types.EventSessionCreated{SessionId: sessionID, Owner: owner, Nonce: nonce}); err != nil {
		return nil, err
	}
	return &types.MsgCreateSessionResponse{
		SessionId:    sessionID,
		SessionNonce: nonce,
		Status:       shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED,
	}, nil
}

func (m *msgServer) CancelOrder(ctx context.Context, req *types.MsgCancelOrder) (*types.MsgCancelOrderResponse, error) {
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrInvalidOrderSequence, "nil request")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, commit := sdkCtx.CacheContext()
	response, err := m.cancelOrder(sdk.WrapSDKContext(cacheCtx), req)
	if err != nil {
		return nil, err
	}
	commit()
	return response, nil
}

func (m *msgServer) cancelOrder(ctx context.Context, req *types.MsgCancelOrder) (*types.MsgCancelOrderResponse, error) {
	sessionKey, err := sessionStoreKey(req.SessionId)
	if err != nil {
		return nil, err
	}
	_, owner, err := m.k.canonicalAddress("signer_address", req.SignerAddress)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidUserAddress, err.Error())
	}
	stream, err := m.k.ReadStream(ctx, sessionKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, errorsmod.Wrap(types.ErrInvalidSessionID, "session not found")
		}
		return nil, err
	}
	if stream.OwnerUserAddress != owner {
		return nil, errorsmod.Wrap(types.ErrInvalidUserAddress, "session owner mismatch")
	}
	if stream.Status == types.SessionStatus_SESSION_STATUS_CLOSED {
		return nil, errorsmod.Wrap(types.ErrInvalidSessionID, "closed session cannot cancel an order")
	}
	params, err := m.k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if stream.NextExpectedSequence >= uint64(params.Session.MaxOrderSequencesPerActiveSession) || req.OrderSequence != stream.NextExpectedSequence {
		return nil, errorsmod.Wrap(types.ErrInvalidOrderSequence, "order_sequence is not the next unused sequence")
	}
	key := types.NewOrderSequenceStateKey(sessionKey, req.OrderSequence)
	if existing, err := m.k.OrderSequence.Get(ctx, key); err == nil {
		return nil, errorsmod.Wrapf(types.ErrInvalidOrderSequence, "order sequence already %s", existing.Status)
	} else if !errors.Is(err, collections.ErrNotFound) {
		return nil, err
	}
	gasMeter := sdk.UnwrapSDKContext(ctx).GasMeter()
	if gasMeter.GasRemaining() < params.Session.CancelOrderMinGas {
		return nil, errorsmod.Wrapf(types.ErrInvalidOrderSequence,
			"cancel order requires at least %d remaining gas", params.Session.CancelOrderMinGas)
	}
	gasMeter.ConsumeGas(params.Session.CancelOrderMinGas, "cancel order guard")
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return nil, err
	}
	previous := stream
	if err := m.k.OrderSequence.Set(ctx, key, types.OrderSequenceState{
		SessionId:       append([]byte(nil), req.SessionId...),
		OrderSequence:   req.OrderSequence,
		Status:          types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_CANCELLED,
		CancelledHeight: height,
	}); err != nil {
		return nil, err
	}
	stream.NextExpectedSequence++
	stream.LastActiveHeight = height
	stream.Status = types.SessionStatus_SESSION_STATUS_ACTIVE
	if err := m.k.replaceStreamState(ctx, previous, stream); err != nil {
		return nil, err
	}
	if err := emitTypedEvent(ctx, &types.EventOrderCancelled{
		SessionId:     req.SessionId,
		OrderSequence: req.OrderSequence,
	}); err != nil {
		return nil, err
	}
	return &types.MsgCancelOrderResponse{
		CancelledSequence:    req.OrderSequence,
		NextExpectedSequence: stream.NextExpectedSequence,
		Status:               shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED,
	}, nil
}
