package keeper

import (
	"context"
	"fmt"
	"strconv"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// Verifier-panel audit: the life of an audit decided by panels.
//
//	proof recorded ─► round 2 (first panel) ─► PASS / REJECT ─► provisional verdict
//	                    │                                         │ publication window
//	                    │ no conclusion                           ├─ nobody asks ─► final
//	                    ▼                                         └─ re-audit (round 4, other members)
//	                  round 3 (one automatic re-assignment)             ├─ opposite decision ─► that one is final
//	                    ├─ PASS / REJECT ─► provisional verdict         └─ anything else ─► provisional stands
//	                    └─ no conclusion ─► ends without a verdict
//
// Two members saying they could not obtain the material ends the audit as
// "material unavailable": the Worker's duty to keep what it recorded
// retrievable, settled like a missed deadline.
//
// The audit row records a result (result_height, task_verdict) only when the
// verdict is final, so settlement reads it exactly as it reads any audit and
// holds a PASS round until then. An absent vote is that member's own miss and
// is never read as a statement about the Worker.

// anchorPanelOpenRound returns the index of the audit's open panel round.
func anchorPanelOpenRound(state types.AnchorAuditState) (int, bool) {
	last := len(state.PanelRounds) - 1
	if last < 0 || state.PanelRounds[last].ClosedHeight != 0 {
		return 0, false
	}
	return last, true
}

func isAnchorDecision(v types.AnchorVerdictV1) bool {
	return v == types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS || v == types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT
}

// anchorPanelRetryPending reports an audit whose first panel round ended with
// no conclusion and whose automatic re-assignment has not been opened yet.
func anchorPanelRetryPending(state types.AnchorAuditState) bool {
	if state.ResultHeight != 0 || len(state.PanelRounds) != 1 || isAnchorDecision(state.ProvisionalVerdict) {
		return false
	}
	round := state.PanelRounds[0]
	if round.ClosedHeight == 0 {
		return false
	}
	step, _ := anchorPanelStepAfter(round.Kind, round.Result)
	return step == anchorPanelStepRetry
}

// anchorPanelDueVerdict is the final verdict an audit has reached at height
// without it being recorded yet: a provisional verdict whose publication
// window has ended with no re-audit running. Settlement and the sweep both
// read it, so a late sweep never delays or changes an outcome.
func anchorPanelDueVerdict(state types.AnchorAuditState, height uint64) (types.AnchorVerdictV1, types.AnchorResultSourceV1, bool) {
	if state.ResultHeight != 0 || !isAnchorDecision(state.ProvisionalVerdict) {
		return 0, 0, false
	}
	if _, open := anchorPanelOpenRound(state); open {
		return 0, 0, false
	}
	if state.ReauditRequested && len(state.PanelRounds) > 0 &&
		state.PanelRounds[len(state.PanelRounds)-1].Kind == types.AnchorPanelRoundKindV1_ANCHOR_PANEL_ROUND_KIND_V1_REAUDIT {
		// A closed re-audit records its outcome when it closes.
		return 0, 0, false
	}
	if height < state.PublicationEndHeight {
		return 0, 0, false
	}
	return state.ProvisionalVerdict, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_VERIFIER_PANEL, true
}

// anchorPanelVerdictWindowBlocks is how long after the proof deadline a panel
// audit may take under the frozen parameters: the first round, its
// re-assignment, the publication window and a re-audit, plus the ordinary
// verdict window as slack for deferred sweeps.
func anchorPanelVerdictWindowBlocks(params types.TaskParamsV1) (uint64, bool) {
	span, ok := anchorPanelSelectionSpanBlocks(params)
	if !ok {
		return 0, false
	}
	total := params.Verification.AnchorVerdictWindowBlocks
	for _, add := range []uint64{span, span, span, params.Verification.AnchorPublicationWindowBlocks} {
		next, overflow := checkedHeightAdd(total, add)
		if overflow {
			return 0, false
		}
		total = next
	}
	return total, true
}

// anchorPanelFee is the fee of one panel round of a task: the share of the
// price a verification round takes. It is recorded on each round; it is paid
// out with the batch settlement, not here.
func (k Keeper) anchorPanelFee(ctx context.Context, taskKey types.TaskKey) uint64 {
	budget, err := k.TaskBudget.Get(ctx, taskKey)
	if err != nil {
		return 0
	}
	receipt, err := k.ReadInferReceipt(ctx, taskKey)
	if err != nil {
		return 0
	}
	priceBid, err := shared.ParseAmount(budget.PriceBid)
	if err != nil {
		return 0
	}
	workerGross, ok := mulDivFloorRound(receipt.GeneratedTokenCount, priceBid, 1_000_000)
	if !ok {
		return 0
	}
	fee, ok := mulDivFloorRound(workerGross, uint64(budget.VerifyRatioBpsSnapshot), uint64(types.BasisPointsMaximum))
	if !ok {
		return 0
	}
	return fee
}

func (k Keeper) setAnchorPanelDue(ctx context.Context, taskKey types.TaskKey, height uint64) error {
	if err := k.clearAnchorPanelDue(ctx, taskKey); err != nil {
		return err
	}
	if height == 0 {
		return nil
	}
	if err := k.AnchorPanelDueIndex.Set(ctx, types.NewDeadlineIndexKey(height, taskKey)); err != nil {
		return err
	}
	return k.AnchorPanelDueHeight.Set(ctx, taskKey, height)
}

func (k Keeper) clearAnchorPanelDue(ctx context.Context, taskKey types.TaskKey) error {
	height, err := k.AnchorPanelDueHeight.Get(ctx, taskKey)
	if err != nil {
		if errIsNotFound(err) {
			return nil
		}
		return err
	}
	if err := removeDeadlineIndex(ctx, k.AnchorPanelDueIndex, taskKey, height); err != nil {
		return err
	}
	return k.AnchorPanelDueHeight.Remove(ctx, taskKey)
}

func (k Keeper) setAnchorPanelMemberIndex(ctx context.Context, taskKey types.TaskKey, members []string, present bool) error {
	for _, member := range members {
		raw, err := k.sessionAddressToStore("panel member index", member)
		if err != nil {
			return err
		}
		key := types.NewVerifierActiveJobKey(raw, taskKey)
		if present {
			err = k.AnchorPanelMemberIndex.Set(ctx, key)
		} else {
			err = k.AnchorPanelMemberIndex.Remove(ctx, key)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// retireAnchorPanelWork removes every working row and index of a task's panel
// audit. It runs when the verdict becomes final and again at cleanup.
func (k Keeper) retireAnchorPanelWork(ctx context.Context, taskKey types.TaskKey, state types.AnchorAuditState) error {
	if err := k.dropAnchorPanelSelection(ctx, taskKey); err != nil {
		return err
	}
	if err := k.clearAnchorPanelDue(ctx, taskKey); err != nil {
		return err
	}
	for _, round := range state.PanelRounds {
		if err := k.setAnchorPanelMemberIndex(ctx, taskKey, round.Members, false); err != nil {
			return err
		}
	}
	return k.AnchorUnderReviewIndex.Remove(ctx, taskKey)
}

// openAnchorPanelRound appends a new panel round to the audit and freezes its
// candidate window. When no panel can be formed the round is closed at once
// with no conclusion and the audit moves on from there. The caller stores the
// audit row.
func (k Keeper) openAnchorPanelRound(
	ctx context.Context,
	taskKey types.TaskKey,
	audit *types.AnchorAuditState,
	kind types.AnchorPanelRoundKindV1,
	height uint64,
) error {
	panelRound := anchorPanelFirstRound + uint32(len(audit.PanelRounds))
	round := types.AnchorPanelRoundState{
		PanelRound: panelRound, Kind: kind, OpenHeight: height,
		PanelFee: strconv.FormatUint(k.anchorPanelFee(ctx, taskKey), 10),
	}
	audit.PanelRound = panelRound
	audit.PanelRounds = append(audit.PanelRounds, round)
	if err := emitTypedEvent(ctx, &types.EventAnchorPanelRoundOpened{
		TaskId: append([]byte(nil), audit.TaskId...), PanelRound: panelRound, Kind: kind, OpenHeight: height,
	}); err != nil {
		return err
	}
	if err := k.dropAnchorPanelSelection(ctx, taskKey); err != nil {
		return err
	}
	params, err := k.ParamsForTask(ctx, taskKey)
	if err != nil {
		return k.closeAnchorPanelRound(ctx, taskKey, audit, types.AnchorVerdictV1_ANCHOR_VERDICT_V1_NO_VERDICT, height)
	}
	selection, formed, err := k.freezeAnchorPanelSelection(ctx, taskKey, *audit, panelRound, params, height)
	if err != nil {
		return err
	}
	if !formed {
		return k.closeAnchorPanelRound(ctx, taskKey, audit, types.AnchorVerdictV1_ANCHOR_VERDICT_V1_NO_VERDICT, height)
	}
	consumer := fmt.Sprintf("%x", []byte(taskKey))
	if err := k.hubKeeper.AcquireBeaconConsumerRef(ctx, selection.Window.WindowRandomnessHeight,
		hubtypes.BeaconConsumerKindVerifierWindow, consumer); err != nil {
		return err
	}
	if err := k.hubKeeper.AcquireBeaconConsumerRef(ctx, selection.Window.SelectionRandomnessHeight,
		hubtypes.BeaconConsumerKindVerifierSelection, consumer); err != nil {
		return err
	}
	if err := k.saveAnchorPanelSelection(ctx, taskKey, selection); err != nil {
		return err
	}
	return k.setAnchorPanelDue(ctx, taskKey, selection.Window.WindowRandomnessHeight)
}

// closeAnchorPanelRound closes the audit's open round with result and takes
// the step that follows from it. The caller stores the audit row.
func (k Keeper) closeAnchorPanelRound(
	ctx context.Context,
	taskKey types.TaskKey,
	audit *types.AnchorAuditState,
	result types.AnchorVerdictV1,
	height uint64,
) error {
	index, open := anchorPanelOpenRound(*audit)
	if !open {
		return nil
	}
	round := &audit.PanelRounds[index]
	round.Result = result
	round.ClosedHeight = height
	if err := k.dropAnchorPanelSelection(ctx, taskKey); err != nil {
		return err
	}
	if err := k.setAnchorPanelMemberIndex(ctx, taskKey, round.Members, false); err != nil {
		return err
	}
	if err := emitTypedEvent(ctx, &types.EventAnchorPanelRoundClosed{
		TaskId: append([]byte(nil), audit.TaskId...), PanelRound: round.PanelRound, Result: result,
		VoteCount: uint32(len(round.Votes)),
	}); err != nil {
		return err
	}
	step, reason := anchorPanelStepAfter(round.Kind, result)
	switch step {
	case anchorPanelStepProvisional:
		params, err := k.ParamsForTask(ctx, taskKey)
		publication := uint64(0)
		if err == nil {
			publication = params.Verification.AnchorPublicationWindowBlocks
		}
		end, overflow := checkedHeightAdd(height, publication)
		if overflow {
			end = height
		}
		audit.ProvisionalVerdict = result
		audit.ProvisionalHeight = height
		audit.PublicationEndHeight = end
		if err := k.AnchorUnderReviewIndex.Set(ctx, taskKey); err != nil {
			return err
		}
		if err := emitTypedEvent(ctx, &types.EventAnchorProvisionalVerdict{
			TaskId: append([]byte(nil), audit.TaskId...), Verdict: result, PublicationEndHeight: end,
		}); err != nil {
			return err
		}
		return k.setAnchorPanelDue(ctx, taskKey, end)
	case anchorPanelStepRetry:
		// The re-assignment needs a candidate-pool scan. The sweep opens it, at
		// most one scan per block.
		return k.setAnchorPanelDue(ctx, taskKey, height)
	case anchorPanelStepWorkerTimeout:
		audit.TimeoutReason = reason
		if reason == types.AnchorTimeoutReasonV1_ANCHOR_TIMEOUT_REASON_V1_PANEL_UNAVAILABLE {
			return k.finalizeAnchorPanelAudit(ctx, taskKey, audit, types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT,
				types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_MATERIAL_UNAVAILABLE,
				"panel members could not obtain the recorded material", height)
		}
		return k.finalizeAnchorPanelAudit(ctx, taskKey, audit, types.AnchorVerdictV1_ANCHOR_VERDICT_V1_NO_VERDICT,
			types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_VERIFIER_PANEL,
			"two panel rounds ended without a conclusion", height)
	case anchorPanelStepReauditDone:
		final, overturned, source := anchorReauditOutcome(audit.ProvisionalVerdict, result)
		if overturned || len(round.Members) == 0 {
			// The opener was right, or no panel could be formed for the re-audit:
			// either way its fee goes back.
			if err := k.refundAnchorReauditFee(ctx, *audit); err != nil {
				return err
			}
			audit.ReauditFee = "0"
		}
		reasonText := "re-audit confirmed the published verdict"
		if overturned {
			reasonText = "re-audit reached the opposite decision"
		} else if !isAnchorDecision(result) {
			reasonText = "re-audit ended without a decision; the published verdict stands"
		}
		return k.finalizeAnchorPanelAudit(ctx, taskKey, audit, final, source, reasonText, height)
	}
	return nil
}

// finalizeAnchorPanelAudit records the final verdict. From here settlement
// reads the audit like any other. The caller stores the audit row.
func (k Keeper) finalizeAnchorPanelAudit(
	ctx context.Context,
	taskKey types.TaskKey,
	audit *types.AnchorAuditState,
	verdict types.AnchorVerdictV1,
	source types.AnchorResultSourceV1,
	reason string,
	height uint64,
) error {
	audit.TaskVerdict = verdict
	audit.ResultSource = source
	audit.ResultReason = truncateAnchorReason(reason)
	audit.ResultHeight = height
	audit.FinalHeight = height
	if err := k.retireAnchorPanelWork(ctx, taskKey, *audit); err != nil {
		return err
	}
	return emitAnchorVerdictEvent(ctx, *audit)
}

func (k Keeper) refundAnchorReauditFee(ctx context.Context, audit types.AnchorAuditState) error {
	if audit.ReauditFee == "" || audit.ReauditOpener == "" {
		return nil
	}
	fee, err := strconv.ParseUint(audit.ReauditFee, 10, 64)
	if err != nil || fee == 0 {
		return nil
	}
	opener, _, err := k.canonicalAddress("reaudit_opener", audit.ReauditOpener)
	if err != nil {
		return nil
	}
	denom := k.hubKeeper.GetHubParams(sdk.UnwrapSDKContext(ctx)).BusinessDenom
	if denom == "" {
		return fmt.Errorf("business denomination is unavailable")
	}
	coins := sdk.NewCoins(sdk.NewCoin(denom, sdkmath.NewIntFromUint64(fee)))
	return k.bankKeeper.SendCoinsFromModuleToAccount(ctx, shared.TaskChallengeEffectModuleName, sdk.AccAddress(opener), coins)
}

// takeAnchorPanelScan claims this block's candidate-pool scan for the sweep.
func (k Keeper) takeAnchorPanelScan(ctx context.Context, height uint64) (bool, error) {
	last, err := k.AnchorPanelLastScanHeight.Get(ctx)
	if err != nil && !errIsNotFound(err) {
		return false, err
	}
	if err == nil && last >= height {
		return false, nil
	}
	return true, k.AnchorPanelLastScanHeight.Set(ctx, height)
}

// advanceAnchorPanel moves one task's panel audit as far as it can go at
// height and schedules the next height it has to be looked at. inSweep allows
// the step that scans the candidate pool. It never fails for a reason that
// lies in the task's own rows: such a round is closed with no conclusion.
func (k Keeper) advanceAnchorPanel(ctx context.Context, taskKey types.TaskKey, height uint64, inSweep bool) error {
	audit, err := k.AnchorAudit.Get(ctx, taskKey)
	if err != nil {
		if errIsNotFound(err) {
			return k.clearAnchorPanelDue(ctx, taskKey)
		}
		return err
	}
	if !isAnchorPanelAudit(audit) || audit.ResultHeight != 0 {
		return k.retireAnchorPanelWork(ctx, taskKey, audit)
	}
	noConclusion := types.AnchorVerdictV1_ANCHOR_VERDICT_V1_NO_VERDICT
	index, open := anchorPanelOpenRound(audit)
	switch {
	case open && len(audit.PanelRounds[index].Members) == 0:
		round := &audit.PanelRounds[index]
		params, paramsErr := k.ParamsForTask(ctx, taskKey)
		selection, found, loadErr := k.loadAnchorPanelSelection(ctx, taskKey)
		if paramsErr != nil || loadErr != nil || !found || selection.PanelRound != round.PanelRound {
			err = k.closeAnchorPanelRound(ctx, taskKey, &audit, noConclusion, height)
			break
		}
		window := selection.Window
		if window.Status == types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_SOURCE_FROZEN {
			if height < window.WindowRandomnessHeight {
				err = k.setAnchorPanelDue(ctx, taskKey, window.WindowRandomnessHeight)
				break
			}
			ready, materializeErr := k.materializeAnchorPanelWindow(ctx, &selection, height)
			switch {
			case materializeErr != nil || (!ready && height >= window.AssignmentDeadlineHeight):
				err = k.closeAnchorPanelRound(ctx, taskKey, &audit, noConclusion, height)
			case !ready:
				err = k.setAnchorPanelDue(ctx, taskKey, height+1)
			default:
				if err = k.saveAnchorPanelSelection(ctx, taskKey, selection); err == nil {
					err = k.setAnchorPanelDue(ctx, taskKey, selection.Window.SelectionRandomnessHeight)
				}
			}
			break
		}
		if height < window.SelectionRandomnessHeight {
			err = k.setAnchorPanelDue(ctx, taskKey, window.SelectionRandomnessHeight)
			break
		}
		members, selectedHash, beaconFound, drawn := k.drawAnchorPanel(ctx, selection, params, height)
		switch {
		case !beaconFound && height < window.AssignmentDeadlineHeight:
			err = k.setAnchorPanelDue(ctx, taskKey, height+1)
		case !beaconFound || !drawn:
			err = k.closeAnchorPanelRound(ctx, taskKey, &audit, noConclusion, height)
		default:
			deadline, overflow := checkedHeightAdd(height, params.Verification.AnchorPanelVoteWindowBlocks)
			if overflow {
				err = k.closeAnchorPanelRound(ctx, taskKey, &audit, noConclusion, height)
				break
			}
			round.Members = members
			round.AssignmentHeight = height
			round.VoteDeadlineHeight = deadline
			round.SelectedVerifiersHash = selectedHash
			if err = k.dropAnchorPanelSelection(ctx, taskKey); err != nil {
				break
			}
			if err = k.setAnchorPanelMemberIndex(ctx, taskKey, members, true); err != nil {
				break
			}
			if err = emitTypedEvent(ctx, &types.EventAnchorPanelAssigned{
				TaskId: append([]byte(nil), audit.TaskId...), PanelRound: round.PanelRound,
				Members: append([]string(nil), members...), VoteDeadlineHeight: deadline,
			}); err != nil {
				break
			}
			err = k.setAnchorPanelDue(ctx, taskKey, deadline+1)
		}
	case open:
		round := audit.PanelRounds[index]
		threshold := uint32(0)
		if params, paramsErr := k.ParamsForTask(ctx, taskKey); paramsErr == nil {
			threshold = params.Verification.AnchorPanelThreshold
		}
		if result := anchorPanelRoundResult(round, threshold, height); result != types.AnchorVerdictV1_ANCHOR_VERDICT_V1_UNSPECIFIED {
			err = k.closeAnchorPanelRound(ctx, taskKey, &audit, result, height)
		} else {
			err = k.setAnchorPanelDue(ctx, taskKey, round.VoteDeadlineHeight+1)
		}
	case anchorPanelRetryPending(audit):
		if !inSweep {
			err = k.setAnchorPanelDue(ctx, taskKey, height)
			break
		}
		granted, scanErr := k.takeAnchorPanelScan(ctx, height)
		if scanErr != nil {
			return scanErr
		}
		if !granted {
			err = k.setAnchorPanelDue(ctx, taskKey, height+1)
			break
		}
		err = k.openAnchorPanelRound(ctx, taskKey, &audit, types.AnchorPanelRoundKindV1_ANCHOR_PANEL_ROUND_KIND_V1_RETRY, height)
	default:
		if verdict, source, due := anchorPanelDueVerdict(audit, height); due {
			err = k.finalizeAnchorPanelAudit(ctx, taskKey, &audit, verdict, source, "publication window ended without a re-audit", height)
		} else if isAnchorDecision(audit.ProvisionalVerdict) {
			err = k.setAnchorPanelDue(ctx, taskKey, audit.PublicationEndHeight)
		} else {
			err = k.clearAnchorPanelDue(ctx, taskKey)
		}
	}
	if err != nil {
		return err
	}
	return k.AnchorAudit.Set(ctx, taskKey, audit)
}

// anchorPanelSweepConservativeBytesV1 is charged per visited panel audit: the
// audit row, its working selection row and the index rows it rewrites.
const anchorPanelSweepConservativeBytesV1 = uint64(64 << 10)

// processAnchorPanelDue is the EndBlock queue of panel audits. Each visited
// task is advanced inside its own cache; a task whose step fails is taken off
// the queue and left to its verdict deadline, where it ends without a verdict.
// One failing task therefore never stops the block or the tasks behind it.
func (k Keeper) processAnchorPanelDue(ctx context.Context, currentHeight, limit uint64) (uint64, error) {
	var visited uint64
	for visited < limit {
		head, found, err := firstDueTaskDeadline(ctx, k.AnchorPanelDueIndex, currentHeight)
		if err != nil || !found {
			return visited, err
		}
		taskKey := types.TaskKey(head.primaryID)
		visited++
		sdkCtx := sdk.UnwrapSDKContext(ctx)
		cacheCtx, write := sdkCtx.CacheContext()
		if err := k.advanceAnchorPanel(sdk.WrapSDKContext(cacheCtx), taskKey, currentHeight, true); err != nil {
			sdkCtx.Logger().Error("panel audit step failed; the task is left to its verdict deadline",
				"task", fmt.Sprintf("%x", []byte(taskKey)), "err", err)
			if err := removeDeadlineIndex(ctx, k.AnchorPanelDueIndex, taskKey, head.deadline); err != nil {
				return visited, err
			}
			if err := k.AnchorPanelDueHeight.Remove(ctx, taskKey); err != nil && !errIsNotFound(err) {
				return visited, err
			}
			continue
		}
		write()
	}
	return visited, nil
}

// anchorPanelHasWork reports whether any working row or index of a task's
// panel audit is still stored.
func (k Keeper) anchorPanelHasWork(ctx context.Context, taskKey types.TaskKey, state types.AnchorAuditState) (bool, error) {
	if has, err := k.AnchorPanelSelection.Has(ctx, taskKey); err != nil || has {
		return has, err
	}
	if has, err := k.AnchorPanelDueHeight.Has(ctx, taskKey); err != nil || has {
		return has, err
	}
	if has, err := k.AnchorUnderReviewIndex.Has(ctx, taskKey); err != nil || has {
		return has, err
	}
	for _, round := range state.PanelRounds {
		for _, member := range round.Members {
			raw, err := k.sessionAddressToStore("panel member index", member)
			if err != nil {
				continue
			}
			if has, err := k.AnchorPanelMemberIndex.Has(ctx, types.NewVerifierActiveJobKey(raw, taskKey)); err != nil || has {
				return has, err
			}
		}
	}
	return false, nil
}

// rebuildAnchorPanelIndexes restores the derived rows of an imported panel
// audit that is not final: the open assignments of its members, its place in
// the under-review list, and a visit by the sweep in the first block. The
// working selection row of a round that was still choosing its members is not
// part of the exported state; such a round is closed by that first visit with
// no conclusion and the audit goes on from there.
func (k Keeper) rebuildAnchorPanelIndexes(ctx context.Context, taskKey types.TaskKey, state types.AnchorAuditState) error {
	if !isAnchorPanelAudit(state) || state.ResultHeight != 0 || len(state.PanelRounds) == 0 {
		return nil
	}
	if index, open := anchorPanelOpenRound(state); open {
		if err := k.setAnchorPanelMemberIndex(ctx, taskKey, state.PanelRounds[index].Members, true); err != nil {
			return err
		}
	}
	if isAnchorDecision(state.ProvisionalVerdict) {
		if err := k.AnchorUnderReviewIndex.Set(ctx, taskKey); err != nil {
			return err
		}
	}
	height := uint64(1)
	if current := sdk.UnwrapSDKContext(ctx).BlockHeight(); current > 1 {
		height = uint64(current)
	}
	return k.setAnchorPanelDue(ctx, taskKey, height)
}
