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

func (m msgServer) SubmitFreezeSignal(ctx context.Context, req *types.MsgSubmitFreezeSignal) (*types.MsgSubmitFreezeSignalResponse, error) {
	if req == nil {
		return nil, errorsmod.Wrap(types.ErrInvalidModel, "nil request")
	}
	if _, _, err := m.k.requireCanonicalAddress("submitter_address", req.SubmitterAddress); err != nil {
		return nil, err
	}
	if req.ProfileVersion == 0 || types.ValidateModelID(req.ModelId) != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidModel, "canonical model_id and positive profile_version are required")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if sdkCtx.BlockHeight() <= 0 {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "freeze signal requires a positive block height")
	}
	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	result, err := m.k.EnqueueNextFreezeRiskWindow(cache, req.ModelId, req.ProfileVersion, uint64(cacheCtx.BlockHeight()))
	if err != nil {
		return nil, err
	}
	if result.BuildStatus == types.FreezeSignalBuildStatus_FREEZE_SIGNAL_BUILD_STATUS_BUILDING {
		params, err := m.k.Params.Get(cache)
		if err != nil {
			return nil, err
		}
		used, advanced, err := m.k.advanceFreezeSignalBuild(
			cache, types.NewFreezeSignalBuildCursorKey(req.ModelId, req.ProfileVersion),
			uint64(cacheCtx.BlockHeight()), params.Freeze.MaxFreezeSignalBuildItemsPerTx,
		)
		if err != nil {
			return nil, err
		}
		if used == 0 {
			return nil, errorsmod.Wrap(types.ErrInvariantBroken, "freeze signal trigger made no progress")
		}
		result = advanced
	}
	write()
	response := &types.MsgSubmitFreezeSignalResponse{
		BuildStatus: result.BuildStatus, RiskWindowId: result.RiskWindow,
		Status: result.Mutation,
	}
	if len(result.SignalID) != 0 {
		response.XFreezeSignalId = &types.MsgSubmitFreezeSignalResponse_FreezeSignalId{FreezeSignalId: append([]byte(nil), result.SignalID...)}
	}
	return response, nil
}

func (m msgServer) EmergencyFreezeVote(ctx context.Context, req *types.MsgEmergencyFreezeVote) (*types.MsgEmergencyFreezeVoteResponse, error) {
	if req == nil || len(req.FreezeSignalId) != 32 {
		return nil, errorsmod.Wrap(types.ErrInvalidFreezeVote, "freeze_signal_id must be Hash32")
	}
	if req.Vote != types.EmergencyFreezeVote_EMERGENCY_FREEZE_VOTE_ACCEPT && req.Vote != types.EmergencyFreezeVote_EMERGENCY_FREEZE_VOTE_REJECT {
		return nil, errorsmod.Wrap(types.ErrInvalidFreezeVote, "vote must be ACCEPT or REJECT")
	}
	if _, _, err := m.k.requireCanonicalAddress("validator_address", req.ValidatorAddress); err != nil {
		return nil, err
	}
	if m.validatorSnapshots == nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "validator snapshot provider is unavailable")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if sdkCtx.BlockHeight() <= 0 {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "freeze vote requires a positive block height")
	}
	height := uint64(sdkCtx.BlockHeight())
	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	signal, err := m.k.FreezeSignalState.Get(cache, req.FreezeSignalId)
	if errors.Is(err, collections.ErrNotFound) {
		return nil, errorsmod.Wrap(types.ErrInvalidFreezeVote, "freeze signal does not exist")
	}
	if err != nil {
		return nil, err
	}
	snapshot := types.ValidatorSnapshot{
		Height: signal.ValidatorSnapshotHeight, ValidatorSetHash: append([]byte(nil), signal.ValidatorSetHash...),
		TotalVotingPower: signal.TotalVotingPowerSnapshot,
	}
	member, found, err := m.validatorSnapshots.GetValidatorSnapshotMemberBySigner(cacheCtx, snapshot, req.ValidatorAddress)
	if err != nil {
		return nil, err
	}
	if !found || len(member.ConsensusAddress) == 0 || member.VotingPower == 0 || member.SignerAddress != req.ValidatorAddress {
		return nil, errorsmod.Wrap(types.ErrInvalidSigner, "validator signer is not a member of the frozen snapshot")
	}
	voteKey := types.NewEmergencyFreezeVoteKey(req.FreezeSignalId, member.ConsensusAddress)
	if existing, err := m.k.EmergencyFreezeVoteState.Get(cache, voteKey); err == nil {
		if existing.Vote != req.Vote || existing.VotingPowerSnapshot != member.VotingPower ||
			!bytes.Equal(existing.FreezeSignalId, req.FreezeSignalId) || !bytes.Equal(existing.ValidatorConsensusAddress, member.ConsensusAddress) {
			return nil, errorsmod.Wrap(types.ErrInvalidFreezeVote, "validator freeze vote conflicts with its committed ballot")
		}
		return &types.MsgEmergencyFreezeVoteResponse{
			AcceptedVotingPower: signal.AcceptedVotingPower, RejectedVotingPower: signal.RejectedVotingPower,
			SignalStatus: signal.SignalStatus, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP,
		}, nil
	} else if !errors.Is(err, collections.ErrNotFound) {
		return nil, err
	}
	if signal.SignalStatus != types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_OPEN || height > signal.VoteDeadlineHeight {
		return nil, errorsmod.Wrap(types.ErrInvalidFreezeVote, "freeze signal is not open for voting")
	}
	if req.Vote == types.EmergencyFreezeVote_EMERGENCY_FREEZE_VOTE_ACCEPT {
		signal.AcceptedVotingPower, err = addHubUint64(signal.AcceptedVotingPower, member.VotingPower)
	} else {
		signal.RejectedVotingPower, err = addHubUint64(signal.RejectedVotingPower, member.VotingPower)
	}
	if err != nil {
		return nil, err
	}
	totalCast, err := addHubUint64(signal.AcceptedVotingPower, signal.RejectedVotingPower)
	if err != nil || totalCast > signal.TotalVotingPowerSnapshot {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "freeze vote tally exceeds frozen voting power")
	}
	vote := types.EmergencyFreezeVoteState{
		FreezeSignalId: append([]byte(nil), req.FreezeSignalId...), ValidatorConsensusAddress: append([]byte(nil), member.ConsensusAddress...),
		VotingPowerSnapshot: member.VotingPower, Vote: req.Vote, AcceptedHeight: height,
	}
	if err := m.k.EmergencyFreezeVoteState.Set(cache, voteKey, vote); err != nil {
		return nil, err
	}
	if err := m.k.FreezeSignalState.Set(cache, signal.FreezeSignalId, signal); err != nil {
		return nil, err
	}
	mustEmitHubEvent(cache, &types.EventEmergencyFreezeVoteRecorded{
		FreezeSignalId: signal.FreezeSignalId, ValidatorConsensusAddress: member.ConsensusAddress, Vote: req.Vote,
		VotingPowerSnapshot: member.VotingPower, AcceptedVotingPower: signal.AcceptedVotingPower, RejectedVotingPower: signal.RejectedVotingPower,
	})
	if hasEmergencyFreezeQuorum(signal.AcceptedVotingPower, signal.TotalVotingPowerSnapshot) {
		if err := m.k.closeFreezeSignal(cache, signal, types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_ACCEPTED, height); err != nil {
			return nil, err
		}
		signal.SignalStatus = types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_ACCEPTED
		mustEmitHubEvent(cache, &types.EventEmergencyFreezeAccepted{
			FreezeSignalId: signal.FreezeSignalId, ModelId: signal.ModelId, ProfileVersion: signal.ProfileVersion,
			AcceptedVotingPower: signal.AcceptedVotingPower, TotalVotingPowerSnapshot: signal.TotalVotingPowerSnapshot,
			ValidatorSetHash: signal.ValidatorSetHash,
		})
		if err := m.k.setProfileEmergencyFrozen(cache, signal.ModelId, signal.ProfileVersion, height); err != nil {
			return nil, err
		}
	} else if signal.RejectedVotingPower > signal.TotalVotingPowerSnapshot/types.FreezeQuorumDenominator {
		if err := m.k.closeFreezeSignal(cache, signal, types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_REJECTED, height); err != nil {
			return nil, err
		}
		signal.SignalStatus = types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_REJECTED
		mustEmitHubEvent(cache, &types.EventEmergencyFreezeRejected{
			FreezeSignalId: signal.FreezeSignalId, ModelId: signal.ModelId, ProfileVersion: signal.ProfileVersion,
			RejectedVotingPower: signal.RejectedVotingPower, TotalVotingPowerSnapshot: signal.TotalVotingPowerSnapshot,
		})
	}
	write()
	return &types.MsgEmergencyFreezeVoteResponse{
		AcceptedVotingPower: signal.AcceptedVotingPower, RejectedVotingPower: signal.RejectedVotingPower,
		SignalStatus: signal.SignalStatus, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED,
	}, nil
}

func (k Keeper) setProfileEmergencyFrozen(ctx context.Context, modelID string, profileVersion uint32, height uint64) error {
	key := types.NewProfileStateKey(modelID, profileVersion)
	profile, err := k.Profile.Get(ctx, key)
	if err != nil {
		return err
	}
	// An accepted freeze quorum is not an authority to bypass the lifecycle
	// matrix. isValidModelStatusTransition is the single source of truth for status
	// legality — setModelStatusWithSource and setProfileStatusWithSource both gate
	// on it — and force-writing EMERGENCY_FROZEN here gave the emergency path a
	// second authority that happened to be empty. The reachable rejection is
	// DELISTED, which the proto documents as "Terminal; never returns to a usable
	// status": a quorum landing on an already-delisted profile used to move it back
	// to EMERGENCY_FROZEN, from where governance reaches REGISTERED with
	// EMERGENCY_RECOVERY, undoing a terminal delisting through the freeze path. The
	// matrix also turns a second accepted signal on an already EMERGENCY_FROZEN
	// profile into a no-op instead of a re-stamp that emitted an old == new event.
	//
	// Refusing means "return nil", not "error". The signal has already been closed
	// as ACCEPTED by the caller and EventEmergencyFreezeAccepted already records
	// that the quorum passed, so the vote is not lost; an error would instead
	// discard the deciding validator's ballot with its Tx and leave the signal
	// impossible to close by vote at all, punishing an honest validator for a
	// governance decision it did not make. Consuming the work item while skipping
	// the terminal target is the established shape here: scheduleNextFreezeRiskWindow
	// (reached from closeFreezeSignal on this very path) and
	// ProcessFreezeRiskWindowSchedules both drop DELISTED profiles silently.
	if !isValidProfileStatusTransition(profile.Status, types.ModelStatusEmergencyFrozen) {
		return nil
	}
	if err := k.deactivateProfileSupports(ctx, modelID, profileVersion, types.ModelSupportDeactivateFrozen, height); err != nil {
		return err
	}
	oldStatus := profile.Status
	profile.Status = types.ModelStatusEmergencyFrozen
	profile.StatusSource = types.ProfileStatusSourceEmergency
	profile.UpdatedHeight = height
	if err := k.setProfileState(ctx, profile); err != nil {
		return err
	}
	if err := k.applyActiveProfileCountDelta(ctx, modelID, oldStatus, profile.Status, height); err != nil {
		return err
	}
	mustEmitHubEvent(ctx, &types.EventModelProfileStateChanged{
		ModelId: modelID, ProfileVersion: profileVersion, OldStatus: oldStatus,
		NewStatus: profile.Status, Source: profile.StatusSource,
	})
	return nil
}

func checkedAddHubUint64(left, right uint64) (uint64, bool) {
	return shared.CheckedAddUint64(left, right)
}

func addHubUint64(left, right uint64) (uint64, error) {
	value, overflow := checkedAddHubUint64(left, right)
	if overflow {
		return 0, errorsmod.Wrap(types.ErrInvariantBroken, "voting power overflow")
	}
	return value, nil
}

func hasEmergencyFreezeQuorum(accepted, total uint64) bool {
	if total == 0 {
		return false
	}
	whole, remainder := total/types.FreezeQuorumDenominator, total%types.FreezeQuorumDenominator
	threshold := whole*types.FreezeQuorumNumerator + (remainder*types.FreezeQuorumNumerator+types.FreezeQuorumDenominator-1)/types.FreezeQuorumDenominator
	return accepted >= threshold
}
