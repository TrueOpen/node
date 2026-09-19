package keeper

import (
	"bytes"
	"context"
	"errors"
	"math"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

var freezeFailureZeroRoot = make([]byte, 32)

type freezeSignalBuildResult struct {
	BuildStatus types.FreezeSignalBuildStatus
	RiskWindow  uint64
	SignalID    []byte
	Mutation    shared.MutationStatusV1
}

func freezeRiskWindow(windowID, windowBlocks uint64) (uint64, uint64, error) {
	if windowBlocks == 0 || windowID == math.MaxUint64 {
		return 0, 0, errorsmod.Wrap(types.ErrInvariantBroken, "freeze risk window is invalid")
	}
	endFactor := windowID + 1
	if endFactor > math.MaxUint64/windowBlocks {
		return 0, 0, errorsmod.Wrap(types.ErrInvariantBroken, "freeze risk window height overflow")
	}
	end := endFactor * windowBlocks
	startBase := windowID * windowBlocks
	if startBase == math.MaxUint64 {
		return 0, 0, errorsmod.Wrap(types.ErrInvariantBroken, "freeze risk window start overflow")
	}
	return startBase + 1, end, nil
}

func latestClosedFreezeRiskWindow(currentHeight, windowBlocks uint64) (uint64, bool) {
	if windowBlocks == 0 || currentHeight <= windowBlocks {
		return 0, false
	}
	return (currentHeight-1)/windowBlocks - 1, true
}

func (k Keeper) validateFreezeValidatorHistoryCoverage(ctx context.Context, params types.HubParamsV2) error {
	if k.validatorSnapshots == nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, "validator snapshot provider is unavailable")
	}
	historicalEntries, err := k.validatorSnapshots.HistoricalEntries(ctx)
	if err != nil {
		return errorsmod.Wrapf(types.ErrInvariantBroken, "staking historical_entries is unavailable: %v", err)
	}
	return types.ValidateFreezeValidatorHistoryCoverage(historicalEntries, params.Freeze.FreezeSignalVoteWindowBlocks)
}

func nextFreezeRiskWindow(profile types.ProfileState) (uint64, error) {
	if profile.XLastFreezeRiskWindowEvaluated == nil {
		return 0, nil
	}
	last := profile.GetLastFreezeRiskWindowEvaluated()
	if last == math.MaxUint64 {
		return 0, errorsmod.Wrap(types.ErrInvariantBroken, "profile freeze risk waterline overflow")
	}
	return last + 1, nil
}

func (k Keeper) scheduleNextFreezeRiskWindow(ctx context.Context, profile types.ProfileState) error {
	if profile.Status == types.ModelStatusDelisted {
		return nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	next, err := nextFreezeRiskWindow(profile)
	if err != nil {
		return err
	}
	_, end, err := freezeRiskWindow(next, params.Freeze.FreezeRiskWindowBlocks)
	if err != nil || end == math.MaxUint64 {
		return errorsmod.Wrap(types.ErrInvariantBroken, "next freeze schedule height overflow")
	}
	return k.FreezeRiskWindowScheduleIndex.Set(ctx, types.NewFreezeRiskWindowScheduleKey(end+1, profile.ModelId, profile.ProfileVersion))
}

func (k Keeper) EnqueueNextFreezeRiskWindow(ctx context.Context, modelID string, profileVersion uint32, currentHeight uint64) (freezeSignalBuildResult, error) {
	profile, err := k.GetProfile(ctx, modelID, profileVersion)
	if err != nil {
		return freezeSignalBuildResult{}, err
	}
	if profile.Status == types.ModelStatusDelisted {
		return freezeSignalBuildResult{}, errorsmod.Wrap(types.ErrInvalidModel, "delisted profile has no freeze risk feed")
	}
	cursorKey := types.NewFreezeSignalBuildCursorKey(modelID, profileVersion)
	if cursor, err := k.FreezeSignalBuildCursor.Get(ctx, cursorKey); err == nil {
		return freezeSignalBuildResult{BuildStatus: types.FreezeSignalBuildStatus_FREEZE_SIGNAL_BUILD_STATUS_BUILDING, RiskWindow: cursor.RiskWindowId, Mutation: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP}, nil
	} else if !errors.Is(err, collections.ErrNotFound) {
		return freezeSignalBuildResult{}, err
	}
	if signal, found, err := k.openFreezeSignalForProfile(ctx, modelID, profileVersion); err != nil {
		return freezeSignalBuildResult{}, err
	} else if found {
		return freezeSignalBuildResult{BuildStatus: types.FreezeSignalBuildStatus_FREEZE_SIGNAL_BUILD_STATUS_OPEN, RiskWindow: signal.RiskWindowId, SignalID: append([]byte(nil), signal.FreezeSignalId...), Mutation: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP}, nil
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return freezeSignalBuildResult{}, err
	}
	windowID, err := nextFreezeRiskWindow(profile)
	if err != nil {
		return freezeSignalBuildResult{}, err
	}
	latest, closed := latestClosedFreezeRiskWindow(currentHeight, params.Freeze.FreezeRiskWindowBlocks)
	if !closed || windowID > latest {
		return freezeSignalBuildResult{}, errorsmod.Wrap(types.ErrInvalidModel, "no complete freeze risk window is ready")
	}
	start, end, err := freezeRiskWindow(windowID, params.Freeze.FreezeRiskWindowBlocks)
	if err != nil {
		return freezeSignalBuildResult{}, err
	}
	windowKey := types.NewFreezeSignalByWindowKey(modelID, profileVersion, windowID)
	if binding, err := k.FreezeSignalByWindow.Get(ctx, windowKey); err == nil {
		result := freezeSignalBuildResult{RiskWindow: windowID, Mutation: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP}
		switch binding.Phase {
		case types.FreezeSignalWindowPhase_FREEZE_SIGNAL_WINDOW_PHASE_BUILDING:
			result.BuildStatus = types.FreezeSignalBuildStatus_FREEZE_SIGNAL_BUILD_STATUS_BUILDING
		case types.FreezeSignalWindowPhase_FREEZE_SIGNAL_WINDOW_PHASE_OPEN:
			result.BuildStatus = types.FreezeSignalBuildStatus_FREEZE_SIGNAL_BUILD_STATUS_OPEN
			result.SignalID = append([]byte(nil), binding.GetFreezeSignalId()...)
		case types.FreezeSignalWindowPhase_FREEZE_SIGNAL_WINDOW_PHASE_CLOSED:
			result.BuildStatus = types.FreezeSignalBuildStatus_FREEZE_SIGNAL_BUILD_STATUS_BELOW_THRESHOLD_NOOP
		default:
			return freezeSignalBuildResult{}, errorsmod.Wrap(types.ErrInvariantBroken, "freeze window binding has invalid phase")
		}
		return result, nil
	} else if !errors.Is(err, collections.ErrNotFound) {
		return freezeSignalBuildResult{}, err
	}
	cursor := types.FreezeSignalBuildCursorState{
		ModelId: modelID, ProfileVersion: profileVersion, RiskWindowId: windowID,
		RiskWindowStartHeight: start, RiskWindowEndHeight: end,
		RollingFailureRefsHash: append([]byte(nil), freezeFailureZeroRoot...),
	}
	if err := k.FreezeSignalBuildCursor.Set(ctx, cursorKey, cursor); err != nil {
		return freezeSignalBuildResult{}, err
	}
	binding := types.FreezeSignalByWindowIndex{
		ModelId: modelID, ProfileVersion: profileVersion, RiskWindowId: windowID,
		Phase: types.FreezeSignalWindowPhase_FREEZE_SIGNAL_WINDOW_PHASE_BUILDING,
	}
	if err := k.FreezeSignalByWindow.Set(ctx, windowKey, binding); err != nil {
		return freezeSignalBuildResult{}, err
	}
	return freezeSignalBuildResult{BuildStatus: types.FreezeSignalBuildStatus_FREEZE_SIGNAL_BUILD_STATUS_BUILDING, RiskWindow: windowID, Mutation: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED}, nil
}

func (k Keeper) ProcessFreezeRiskWindowSchedules(ctx context.Context, currentHeight, limit uint64) (uint64, error) {
	if limit == 0 {
		return 0, nil
	}
	visited := uint64(0)
	for visited < limit {
		iter, err := k.FreezeRiskWindowScheduleIndex.Iterate(ctx, nil)
		if err != nil {
			return visited, err
		}
		if !iter.Valid() {
			iter.Close()
			break
		}
		key, err := iter.Key()
		iter.Close()
		if err != nil {
			return visited, err
		}
		if key.K1() > currentHeight {
			break
		}
		sdkCtx := sdk.UnwrapSDKContext(ctx)
		cacheCtx, write := sdkCtx.CacheContext()
		cache := sdk.WrapSDKContext(cacheCtx)
		profile, err := k.GetProfile(cache, key.K2(), key.K3())
		if err != nil {
			return visited, errorsmod.Wrap(types.ErrInvariantBroken, "freeze schedule references missing profile")
		}
		if err := k.FreezeRiskWindowScheduleIndex.Remove(cache, key); err != nil {
			return visited, err
		}
		if profile.Status != types.ModelStatusDelisted {
			if _, err := k.EnqueueNextFreezeRiskWindow(cache, profile.ModelId, profile.ProfileVersion, currentHeight); err != nil {
				return visited, err
			}
		}
		write()
		visited++
	}
	return visited, nil
}

func (k Keeper) ProcessFreezeSignalBuilds(ctx context.Context, currentHeight, limit uint64) (uint64, error) {
	if limit == 0 {
		return 0, nil
	}
	visited := uint64(0)
	for visited < limit {
		iter, err := k.FreezeSignalBuildCursor.Iterate(ctx, nil)
		if err != nil {
			return visited, err
		}
		if !iter.Valid() {
			iter.Close()
			break
		}
		key, err := iter.Key()
		iter.Close()
		if err != nil {
			return visited, err
		}
		sdkCtx := sdk.UnwrapSDKContext(ctx)
		cacheCtx, write := sdkCtx.CacheContext()
		used, _, err := k.advanceFreezeSignalBuild(sdk.WrapSDKContext(cacheCtx), key, currentHeight, uint32(min(limit-visited, uint64(math.MaxUint32))))
		if err != nil {
			return visited, err
		}
		if used == 0 {
			return visited, errorsmod.Wrap(types.ErrInvariantBroken, "freeze build cursor made no bounded progress")
		}
		write()
		visited += uint64(used)
	}
	return visited, nil
}

func (k Keeper) advanceFreezeSignalBuild(ctx context.Context, key types.FreezeSignalBuildCursorKey, currentHeight uint64, limit uint32) (uint32, freezeSignalBuildResult, error) {
	if k.freezeTaskValidator == nil || k.validatorSnapshots == nil {
		return 0, freezeSignalBuildResult{}, errorsmod.Wrap(types.ErrInvariantBroken, "freeze runtime dependencies are unavailable")
	}
	cursor, err := k.FreezeSignalBuildCursor.Get(ctx, key)
	if err != nil {
		return 0, freezeSignalBuildResult{}, err
	}
	page, err := k.freezeTaskValidator.ScanFreezeSignalFailures(ctx, types.FreezeSignalFailureScanRequest{
		ModelID: cursor.ModelId, ProfileVersion: cursor.ProfileVersion,
		RiskWindowStartHeight: cursor.RiskWindowStartHeight, RiskWindowEndHeight: cursor.RiskWindowEndHeight,
		LastIndexKey: cursor.LastFailureIndexKey, Limit: limit,
	})
	if err != nil {
		return 0, freezeSignalBuildResult{}, err
	}
	if page.Visited > limit || page.Visited == 0 && !page.Done {
		return 0, freezeSignalBuildResult{}, errorsmod.Wrap(types.ErrInvariantBroken, "task failure scan exceeded its budget or stalled")
	}
	for _, failure := range page.Failures {
		if len(failure.TaskID) != 32 || len(failure.EvidenceDigest) != 32 || len(failure.SettlementID) != 0 && len(failure.SettlementID) != 32 ||
			failure.FinalityHeight < cursor.RiskWindowStartHeight || failure.FinalityHeight > cursor.RiskWindowEndHeight {
			return 0, freezeSignalBuildResult{}, errorsmod.Wrap(types.ErrInvariantBroken, "task failure scan returned a malformed row")
		}
		if failure.FailureClass == types.FreezeFailureClassInsufficientVerifier {
			if failure.IncludedInRoot || cursor.ExcludedInsufficientVerifierCount == math.MaxUint32 {
				return 0, freezeSignalBuildResult{}, errorsmod.Wrap(types.ErrInvariantBroken, "invalid insufficient-verifier freeze row")
			}
			cursor.ExcludedInsufficientVerifierCount++
			continue
		}
		if !failure.IncludedInRoot {
			return 0, freezeSignalBuildResult{}, errorsmod.Wrap(types.ErrInvariantBroken, "eligible freeze failure was not marked for the root")
		}
		if cursor.IncludedFailureTaskRefCount == math.MaxUint32 {
			return 0, freezeSignalBuildResult{}, errorsmod.Wrap(types.ErrInvariantBroken, "freeze failure count overflow")
		}
		cursor.IncludedFailureTaskRefCount++
		switch failure.FailureClass {
		case types.FreezeFailureClassMetricThresholdBreach:
			cursor.IncludedFailureClassCounts.MetricThresholdBreach++
		case types.FreezeFailureClassObjectiveFault:
			cursor.IncludedFailureClassCounts.ObjectiveFault++
		case types.FreezeFailureClassSchemaFault:
			cursor.IncludedFailureClassCounts.SchemaFault++
		case types.FreezeFailureClassWorkerEvidenceFault:
			cursor.IncludedFailureClassCounts.WorkerEvidenceFault++
		default:
			return 0, freezeSignalBuildResult{}, errorsmod.Wrap(types.ErrInvariantBroken, "task failure scan returned an unsupported class")
		}
		rollingHash, err := freezeFailureRefsRoot(
			cursor.RollingFailureRefsHash,
			failure.TaskID, failure.SettlementID, failure.FinalityHeight,
			failure.FailureClass, failure.EvidenceDigest,
		)
		if err != nil {
			return 0, freezeSignalBuildResult{}, err
		}
		cursor.RollingFailureRefsHash = rollingHash
	}
	if math.MaxUint64-cursor.VisitedCount < uint64(page.Visited) {
		return 0, freezeSignalBuildResult{}, errorsmod.Wrap(types.ErrInvariantBroken, "freeze build visited count overflow")
	}
	cursor.VisitedCount += uint64(page.Visited)
	cursor.LastFailureIndexKey = append([]byte(nil), page.LastIndexKey...)
	if !page.Done {
		if err := k.FreezeSignalBuildCursor.Set(ctx, key, cursor); err != nil {
			return 0, freezeSignalBuildResult{}, err
		}
		return page.Visited, freezeSignalBuildResult{BuildStatus: types.FreezeSignalBuildStatus_FREEZE_SIGNAL_BUILD_STATUS_BUILDING, RiskWindow: cursor.RiskWindowId, Mutation: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED}, nil
	}
	result, err := k.finishFreezeSignalBuild(ctx, key, cursor, currentHeight)
	if err != nil {
		return 0, freezeSignalBuildResult{}, err
	}
	// Completing an empty window still consumes one bounded item.
	used := page.Visited
	if used == 0 {
		used = 1
	}
	return used, result, nil
}

func (k Keeper) finishFreezeSignalBuild(ctx context.Context, key types.FreezeSignalBuildCursorKey, cursor types.FreezeSignalBuildCursorState, currentHeight uint64) (freezeSignalBuildResult, error) {
	profile, err := k.GetProfile(ctx, cursor.ModelId, cursor.ProfileVersion)
	if err != nil {
		return freezeSignalBuildResult{}, err
	}
	next, err := nextFreezeRiskWindow(profile)
	if err != nil || next != cursor.RiskWindowId {
		return freezeSignalBuildResult{}, errorsmod.Wrap(types.ErrInvariantBroken, "freeze build cursor does not match profile waterline")
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return freezeSignalBuildResult{}, err
	}
	windowKey := types.NewFreezeSignalByWindowKey(cursor.ModelId, cursor.ProfileVersion, cursor.RiskWindowId)
	if cursor.IncludedFailureTaskRefCount < params.Freeze.MinFreezeSignalFailureCount {
		profile.XLastFreezeRiskWindowEvaluated = &types.ProfileState_LastFreezeRiskWindowEvaluated{LastFreezeRiskWindowEvaluated: cursor.RiskWindowId}
		profile.UpdatedHeight = currentHeight
		if err := k.setProfileState(ctx, profile); err != nil {
			return freezeSignalBuildResult{}, err
		}
		if err := k.FreezeSignalBuildCursor.Remove(ctx, key); err != nil {
			return freezeSignalBuildResult{}, err
		}
		if err := k.FreezeSignalByWindow.Remove(ctx, windowKey); err != nil {
			return freezeSignalBuildResult{}, err
		}
		if err := k.scheduleNextFreezeRiskWindow(ctx, profile); err != nil {
			return freezeSignalBuildResult{}, err
		}
		return freezeSignalBuildResult{BuildStatus: types.FreezeSignalBuildStatus_FREEZE_SIGNAL_BUILD_STATUS_BELOW_THRESHOLD_NOOP, RiskWindow: cursor.RiskWindowId, Mutation: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP}, nil
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if err := k.validateFreezeValidatorHistoryCoverage(ctx, params); err != nil {
		return k.rescheduleFreezeSignalBuild(ctx, key, cursor, currentHeight)
	}
	snapshot, err := k.validatorSnapshots.CaptureValidatorSetSnapshot(sdkCtx, currentHeight)
	if err != nil {
		return k.rescheduleFreezeSignalBuild(ctx, key, cursor, currentHeight)
	}
	if len(snapshot.ValidatorSetHash) != 32 || snapshot.TotalVotingPower == 0 || snapshot.Height != currentHeight {
		return freezeSignalBuildResult{}, errorsmod.Wrap(types.ErrInvariantBroken, "validator snapshot is malformed")
	}
	profile.XLastFreezeRiskWindowEvaluated = &types.ProfileState_LastFreezeRiskWindowEvaluated{LastFreezeRiskWindowEvaluated: cursor.RiskWindowId}
	profile.UpdatedHeight = currentHeight
	if err := k.setProfileState(ctx, profile); err != nil {
		return freezeSignalBuildResult{}, err
	}
	signalID, err := freezeSignalID(
		sdkCtx.ChainID(), cursor.ModelId,
		cursor.ProfileVersion, cursor.RiskWindowId,
		cursor.RiskWindowStartHeight, cursor.RiskWindowEndHeight,
		cursor.IncludedFailureTaskRefCount, cursor.RollingFailureRefsHash,
	)
	if err != nil {
		return freezeSignalBuildResult{}, err
	}
	voteDeadline, overflow := checkedAddHubUint64(currentHeight, params.Freeze.FreezeSignalVoteWindowBlocks)
	if overflow {
		return freezeSignalBuildResult{}, errorsmod.Wrap(types.ErrInvariantBroken, "freeze vote deadline overflow")
	}
	signal := types.FreezeSignalState{
		FreezeSignalId: signalID, ModelId: cursor.ModelId, ProfileVersion: cursor.ProfileVersion,
		RiskWindowId: cursor.RiskWindowId, RiskWindowStartHeight: cursor.RiskWindowStartHeight, RiskWindowEndHeight: cursor.RiskWindowEndHeight,
		IncludedFailureTaskRefCount: cursor.IncludedFailureTaskRefCount, IncludedFailureTaskRefsHash: append([]byte(nil), cursor.RollingFailureRefsHash...),
		IncludedFailureClassCounts: cursor.IncludedFailureClassCounts, ExcludedInsufficientVerifierCount: cursor.ExcludedInsufficientVerifierCount,
		TotalVotingPowerSnapshot: snapshot.TotalVotingPower, SignalStatus: types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_OPEN,
		ValidatorSnapshotHeight: snapshot.Height, ValidatorSetHash: append([]byte(nil), snapshot.ValidatorSetHash...),
		CreatedHeight: currentHeight, VoteDeadlineHeight: voteDeadline,
	}
	if err := k.FreezeSignalState.Set(ctx, signalID, signal); err != nil {
		return freezeSignalBuildResult{}, err
	}
	if err := k.FreezeSignalByProfileIndex.Set(ctx, types.NewFreezeSignalByProfileKey(signal.ModelId, signal.ProfileVersion, signal.SignalStatus, signal.RiskWindowEndHeight, signalID)); err != nil {
		return freezeSignalBuildResult{}, err
	}
	if err := k.FreezeSignalDeadlineIndex.Set(ctx, types.NewFreezeSignalDeadlineKey(voteDeadline, signalID)); err != nil {
		return freezeSignalBuildResult{}, err
	}
	binding := types.FreezeSignalByWindowIndex{
		ModelId: signal.ModelId, ProfileVersion: signal.ProfileVersion, RiskWindowId: signal.RiskWindowId,
		Phase:           types.FreezeSignalWindowPhase_FREEZE_SIGNAL_WINDOW_PHASE_OPEN,
		XFreezeSignalId: &types.FreezeSignalByWindowIndex_FreezeSignalId{FreezeSignalId: append([]byte(nil), signalID...)},
	}
	if err := k.FreezeSignalByWindow.Set(ctx, windowKey, binding); err != nil {
		return freezeSignalBuildResult{}, err
	}
	if err := k.FreezeSignalBuildCursor.Remove(ctx, key); err != nil {
		return freezeSignalBuildResult{}, err
	}
	mustEmitHubEvent(ctx, &types.EventFreezeSignalSubmitted{
		FreezeSignalId: signalID, ModelId: signal.ModelId, ProfileVersion: signal.ProfileVersion,
		RiskWindowId: signal.RiskWindowId, RiskWindowStartHeight: signal.RiskWindowStartHeight,
		RiskWindowEndHeight: signal.RiskWindowEndHeight, FailureRefsHash: signal.IncludedFailureTaskRefsHash,
		FailureRefCount: signal.IncludedFailureTaskRefCount, VoteDeadlineHeight: signal.VoteDeadlineHeight,
	})
	return freezeSignalBuildResult{BuildStatus: types.FreezeSignalBuildStatus_FREEZE_SIGNAL_BUILD_STATUS_OPEN, RiskWindow: cursor.RiskWindowId, SignalID: signalID, Mutation: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED}, nil
}

// freezeFailureRefsRoot folds one accepted Task failure into the rolling
// TRUEOPEN_FREEZE_FAILURE_REFS_V1 root. The previous root is the first framed field, so
// the result is order-dependent by construction and the scan order in
// advanceFreezeSignalBuild is part of the commitment.
//
// It is a free function because everything it needs is on the failure row and the
// cursor's current root: pulling the preimage out of the scan loop is what lets a
// golden vector state one fold with its own values. Four of its six fields are
// 32-byte opaque strings - previous_root, task_id, settlement_id_or_empty and
// evidence_digest - and while the builder call sat inline no test could distinguish
// any permutation of them.
func freezeFailureRefsRoot(
	previousRoot, taskID, settlementIDOrEmpty []byte, taskFinalityHeight uint64,
	failureClass types.FreezeFailureClass, evidenceDigest []byte,
) ([]byte, error) {
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainFreezeFailureRefsV1)).Raw(
		previousRoot,
		taskID, settlementIDOrEmpty, shared.Uint64BE(taskFinalityHeight),
		shared.EnumBE(uint32(failureClass)), evidenceDigest,
	).Sum()
}

// freezeSignalID is the TRUEOPEN_FREEZE_SIGNAL_V1 identity of one completed build.
//
// Its three consecutive Uint64BE fields - risk_window_id, risk_window_start_height
// and risk_window_end_height - are the reason this is a named function: they are
// indistinguishable to any binding that classifies a field by its encoder, so the
// only thing that can tell them apart is calling this with known values and checking
// the digest.
func freezeSignalID(
	chainID, modelID string, profileVersion uint32,
	riskWindowID, riskWindowStartHeight, riskWindowEndHeight uint64,
	includedFailureTaskRefCount uint32, includedFailureTaskRefsHash []byte,
) ([]byte, error) {
	return shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainFreezeSignalV1)).Raw(
		[]byte(chainID), []byte(modelID),
		shared.Uint32BE(profileVersion), shared.Uint64BE(riskWindowID),
		shared.Uint64BE(riskWindowStartHeight), shared.Uint64BE(riskWindowEndHeight),
		shared.Uint32BE(includedFailureTaskRefCount), includedFailureTaskRefsHash,
	).Sum()
}

func (k Keeper) rescheduleFreezeSignalBuild(
	ctx context.Context,
	key types.FreezeSignalBuildCursorKey,
	cursor types.FreezeSignalBuildCursorState,
	currentHeight uint64,
) (freezeSignalBuildResult, error) {
	retryHeight, overflow := checkedAddHubUint64(currentHeight, 1)
	if overflow {
		return freezeSignalBuildResult{}, errorsmod.Wrap(types.ErrInvariantBroken, "freeze signal retry height overflow")
	}
	windowKey := types.NewFreezeSignalByWindowKey(cursor.ModelId, cursor.ProfileVersion, cursor.RiskWindowId)
	if err := k.FreezeSignalBuildCursor.Remove(ctx, key); err != nil {
		return freezeSignalBuildResult{}, err
	}
	if err := k.FreezeSignalByWindow.Remove(ctx, windowKey); err != nil {
		return freezeSignalBuildResult{}, err
	}
	if err := k.FreezeRiskWindowScheduleIndex.Set(ctx, types.NewFreezeRiskWindowScheduleKey(retryHeight, cursor.ModelId, cursor.ProfileVersion)); err != nil {
		return freezeSignalBuildResult{}, err
	}
	return freezeSignalBuildResult{
		BuildStatus: types.FreezeSignalBuildStatus_FREEZE_SIGNAL_BUILD_STATUS_BUILDING,
		RiskWindow:  cursor.RiskWindowId,
		Mutation:    shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED,
	}, nil
}

func (k Keeper) openFreezeSignalForProfile(ctx context.Context, modelID string, profileVersion uint32) (types.FreezeSignalState, bool, error) {
	rangeValue := collections.NewSuperPrefixedQuadRange3[string, uint32, int32, types.FreezeSignalWindowOrderKey](modelID, profileVersion, int32(types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_OPEN))
	iter, err := k.FreezeSignalByProfileIndex.Iterate(ctx, rangeValue)
	if err != nil {
		return types.FreezeSignalState{}, false, err
	}
	defer iter.Close()
	if !iter.Valid() {
		return types.FreezeSignalState{}, false, nil
	}
	key, err := iter.Key()
	if err != nil {
		return types.FreezeSignalState{}, false, err
	}
	signalID := key.K4().K2()
	signal, err := k.FreezeSignalState.Get(ctx, signalID)
	if err != nil {
		return types.FreezeSignalState{}, false, errorsmod.Wrap(types.ErrInvariantBroken, "open freeze index references missing signal")
	}
	if signal.SignalStatus != types.FreezeSignalStatus_FREEZE_SIGNAL_STATUS_OPEN || !bytes.Equal(signal.FreezeSignalId, signalID) {
		return types.FreezeSignalState{}, false, errorsmod.Wrap(types.ErrInvariantBroken, "open freeze index disagrees with signal")
	}
	iter.Next()
	if iter.Valid() {
		return types.FreezeSignalState{}, false, errorsmod.Wrap(types.ErrInvariantBroken, "profile has multiple open freeze signals")
	}
	return signal, true, nil
}

func (k Keeper) IsFreezeFailureWindowProtected(ctx context.Context, modelID string, profileVersion uint32, finalityHeight uint64) (bool, error) {
	if cursor, err := k.FreezeSignalBuildCursor.Get(ctx, types.NewFreezeSignalBuildCursorKey(modelID, profileVersion)); err == nil {
		return finalityHeight >= cursor.RiskWindowStartHeight && finalityHeight <= cursor.RiskWindowEndHeight, nil
	} else if !errors.Is(err, collections.ErrNotFound) {
		return false, err
	}
	signal, found, err := k.openFreezeSignalForProfile(ctx, modelID, profileVersion)
	if err != nil || !found {
		return false, err
	}
	return finalityHeight >= signal.RiskWindowStartHeight && finalityHeight <= signal.RiskWindowEndHeight, nil
}

func (k Keeper) deactivateModelSupports(ctx context.Context, modelID, reason string, height uint64) error {
	return k.EnqueueModelSupportDeactivation(ctx, modelID, reason, height)
}

func (k Keeper) deactivateProfileSupports(ctx context.Context, modelID string, profileVersion uint32, reason string, height uint64) error {
	return k.EnqueueSupportDeactivation(ctx, modelID, profileVersion, reason, height)
}
