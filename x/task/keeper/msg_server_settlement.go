package keeper

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// SettleTask is the single funded terminal transition of a task. It follows the
// fixed handler order of keeper_api_contract.md §10.10a: an existing settlement is a
// NOOP replay before anything is rebuilt, the facts and the plan are derived by
// the two pure builders, and only then does ApplySettlementPlan move money — all
// inside one cache context, so any failure leaves zero writes.
func (m msgServer) SettleTask(ctx context.Context, req *types.MsgSettleTask) (*types.MsgSettleTaskResponse, error) {
	if req == nil || len(req.TaskId) != types.Hash32Len {
		return nil, status.Error(codes.InvalidArgument, "task_id must be Hash32")
	}
	if _, _, err := m.k.canonicalAddress("submitter_address", req.SubmitterAddress); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	taskKey := types.NewTaskKey(req.TaskId)

	// Step 1: an already settled task returns its original receipt. The plan is
	// never rebuilt at the current height, so a rebroadcast cannot re-derive a
	// different settlement or move funds twice.
	if replay, found, err := m.k.settleTaskReplay(ctx, taskKey); err != nil {
		return nil, err
	} else if found {
		return replay, nil
	}

	height, err := currentBlockHeight(ctx)
	if err != nil {
		return nil, err
	}
	inputs, err := m.k.loadSettlementInputs(ctx, taskKey, height)
	if err != nil {
		return nil, err
	}
	// authorizeSettlementSubmitter enforces §10.10a's SETTLE grace window and
	// reports whether the submitter was the duty Builder inside it. The boolean is
	// discarded: its only consumer was the Builder contribution credit, and Phase 0
	// does not create BuilderContributionState at all (keeper_api_contract.md:3273/:3635,
	// keeper_data_structure_contract.md:1564). The authorization it just performed
	// is the live half.
	if _, err := m.k.authorizeSettlementSubmitter(ctx, inputs, req.SubmitterAddress, height); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidSignature, err.Error())
	}
	return m.k.executeLoadedTaskSettlement(ctx, inputs, height)
}

// settleTaskFromDeadline is the signer-free internal trigger shared by EndBlock
// and MsgSweepDeadline. The caller owns the deadline index transaction; this
// method uses the same pure builders and funded apply path as MsgSettleTask.
func (k Keeper) settleTaskFromDeadline(
	ctx context.Context,
	taskKey types.TaskKey,
	settlementDeadline uint64,
	currentHeight uint64,
) (bool, error) {
	if currentHeight < settlementDeadline {
		return false, errorsmod.Wrap(types.ErrInvalidTaskStatus, "settlement deadline is not due")
	}
	if _, found, err := k.settleTaskReplay(ctx, taskKey); err != nil {
		return false, err
	} else if found {
		return false, nil
	}
	inputs, err := k.loadSettlementInputs(ctx, taskKey, currentHeight)
	if err != nil {
		return false, err
	}
	if _, err := k.executeLoadedTaskSettlement(ctx, inputs, currentHeight); err != nil {
		return false, err
	}
	return true, nil
}

func (k Keeper) executeLoadedTaskSettlement(
	ctx context.Context,
	inputs settlementInputs,
	height uint64,
) (*types.MsgSettleTaskResponse, error) {
	// Fault application returns Hub-owned jail/status fields that are committed
	// by fault_summary_hash, so the entire prepare/build/apply sequence shares one
	// cache transaction.
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	expectedID, err := types.TaskSettlementID(
		sdkCtx.ChainID(), inputs.Core.TaskId, inputs.Core.AcceptedTaskHash,
		inputs.EffectiveRound, inputs.CutoffHeight, height,
	)
	if err != nil {
		return nil, err
	}
	inputs, err = k.prepareSettlementFaults(cache, inputs, expectedID[:], height)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidSettlement, err.Error())
	}

	// Steps 5-6: both builders are pure; neither writes state nor moves funds.
	facts, settlementID, err := k.BuildSettlementFacts(cache, inputs, height)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidTaskStatus, err.Error())
	}
	if !bytes.Equal(settlementID, expectedID[:]) {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "settlement identifier derivations disagree")
	}
	taskKey := types.NewTaskKey(inputs.Core.TaskId)
	reimbursements, err := k.collectTaskGasReimbursements(cache, taskKey)
	if err != nil {
		return nil, err
	}
	plan, bill, err := k.BuildSettlementPlan(cache, inputs, facts, settlementID, reimbursements)
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}

	settlement, err := k.applySettlementPlan(cache, inputs, facts, plan, bill, settlementID, height)
	if err != nil {
		return nil, err
	}
	response := settlementResponse(settlement, plan, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED)
	write()
	return response, nil
}

// settleTaskReplay returns the original receipt for an already settled task.
// §10.10a keeps the terminal summary as a second source so a compacted task
// still replays instead of being rebuilt.
func (k Keeper) settleTaskReplay(ctx context.Context, taskKey types.TaskKey) (*types.MsgSettleTaskResponse, bool, error) {
	settlement, err := k.TaskSettlement.Get(ctx, taskKey)
	if err == nil {
		plan, err := k.reconstructSettlementPlan(ctx, settlement)
		if err != nil {
			return nil, false, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
		}
		return settlementResponse(settlement, plan, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP), true, nil
	}
	if !errors.Is(err, collections.ErrNotFound) {
		return nil, false, err
	}
	terminal, err := k.TaskTerminalSummary.Get(ctx, taskKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if !bytes.Equal(terminal.TaskId, taskKey) || len(terminal.GetSettlementId()) != types.Hash32Len ||
		len(terminal.SettlementFactsHash) != types.Hash32Len || len(terminal.SettlementPlanHash) != types.Hash32Len ||
		terminal.FinalityStatus != shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL {
		return nil, false, errorsmod.Wrap(types.ErrInvariantBroken, "terminal settlement receipt is invalid")
	}
	return &types.MsgSettleTaskResponse{
		TaskId: append([]byte(nil), terminal.TaskId...), SettlementId: append([]byte(nil), terminal.GetSettlementId()...),
		SettlementFactsHash: append([]byte(nil), terminal.SettlementFactsHash...),
		SettlementPlanHash:  append([]byte(nil), terminal.SettlementPlanHash...),
		// The compacted summary keeps no refund amount; the refund was already
		// pushed by the original settlement, so the replay reports zero.
		Verdict: terminal.Verdict, RefundAmount: shared.NewAmount(0),
		SettlementHeight: terminal.SettlementHeight, TaskFinalityHeight: terminal.TaskFinalityHeight,
		Status: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP,
	}, true, nil
}

// loadSettlementInputs performs §10.10a step 2: it loads the authoritative rows
// and refuses anything that is not a closed, challenge-free, still-reserved task.
func (k Keeper) loadSettlementInputs(ctx context.Context, taskKey types.TaskKey, height uint64) (settlementInputs, error) {
	core, err := k.TaskCore.Get(ctx, taskKey)
	if err != nil {
		return settlementInputs{}, errorsmod.Wrap(types.ErrInvalidTaskStatus, "task is unavailable")
	}
	// §10.10a step 2 lists "verify phase=SETTLING" and "no conflicting terminal
	// state exists" as two separate things, and they must be reported separately
	// here: the former means "you came too early, retry once the challenge window
	// closes", the latter means "this is already final, stop retrying". Merging them
	// into one makes the Builder read "too early" as "already settled", whose only
	// workable response is to keep blindly retrying -- that is exactly where the 2-3
	// failing MsgSettleTask with code=1104 per block in production came from.
	if core.TaskPhase != types.TaskPhase_TASK_PHASE_SETTLING {
		return settlementInputs{}, errorsmod.Wrapf(types.ErrInvalidTaskStatus,
			"task phase is %s, settlement opens only after every round is closed and the challenge window has passed",
			core.TaskPhase)
	}
	if core.SettlementStatus != types.SettlementStatus_SETTLEMENT_STATUS_NONE ||
		core.FinalityStatus != shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_PENDING ||
		core.XTaskFinalityHeight != nil {
		return settlementInputs{}, errorsmod.Wrap(types.ErrInvalidTaskStatus, "task already has a terminal settlement")
	}
	summary, err := k.TaskRoundSummary.Get(ctx, taskKey)
	if err != nil {
		return settlementInputs{}, errorsmod.Wrap(types.ErrInvalidTaskStatus, "task round summary is unavailable")
	}
	if summary.OpenRoundCount != 0 || summary.XRoundsClosedHeight == nil ||
		(summary.XChallengeOpenHeight == nil) != (summary.XChallengeCloseHeight == nil) {
		return settlementInputs{}, errorsmod.Wrap(types.ErrInvalidTaskStatus, "task still has an open or unclosed round")
	}
	if summary.XChallengeCloseHeight != nil && height <= summary.GetChallengeCloseHeight() {
		// Carry the close height: from this message alone the submitter can work out
		// the height at which to retry next, instead of spinning on a timer.
		return settlementInputs{}, errorsmod.Wrapf(types.ErrInvalidTaskStatus,
			"round 1 challenge window is still open until height %d", summary.GetChallengeCloseHeight())
	}
	if summary.XSettlementFactsCutoffHeight == nil {
		return settlementInputs{}, errorsmod.Wrap(types.ErrInvalidTaskStatus, "settlement facts cutoff height is not frozen")
	}
	assignment, err := k.TaskAssignment.Get(ctx, taskKey)
	if err != nil {
		return settlementInputs{}, errorsmod.Wrap(types.ErrInvalidTaskStatus, "task assignment is unavailable")
	}
	selection, err := k.loadActiveTaskBuilderSelection(ctx, taskKey, core.TaskId)
	if err != nil {
		return settlementInputs{}, errorsmod.Wrap(types.ErrInvariantBroken, "active Task Builder selection is unavailable")
	}
	budget, err := k.TaskBudget.Get(ctx, taskKey)
	if err != nil || budget.BudgetStatus != types.TaskBudgetStatus_TASK_BUDGET_STATUS_RESERVED {
		return settlementInputs{}, errorsmod.Wrap(types.ErrInvalidTaskStatus, "task budget is not reserved")
	}
	infer, err := k.InferReceipt.Get(ctx, taskKey)
	if err != nil || len(infer.InferReceiptHash) != types.Hash32Len {
		return settlementInputs{}, errorsmod.Wrap(types.ErrInvalidTaskStatus, "infer receipt is unavailable")
	}

	effectiveRound := summary.EffectiveVerifyRound
	if !isPhase0VerifyRound(effectiveRound) {
		return settlementInputs{}, errorsmod.Wrap(types.ErrInvalidTaskStatus, "effective verify round is not frozen")
	}
	round1, err := k.VerificationRound.Get(ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1))
	if err != nil || round1.XClosedHeight == nil {
		return settlementInputs{}, errorsmod.Wrap(types.ErrInvalidTaskStatus, "round 1 is not closed")
	}
	effectiveRoundState := round1
	if effectiveRound != types.VerifyRoundV1 {
		effectiveRoundState, err = k.VerificationRound.Get(ctx, types.NewVerifyRoundKey(taskKey, effectiveRound))
		if err != nil || effectiveRoundState.XClosedHeight == nil {
			return settlementInputs{}, errorsmod.Wrap(types.ErrInvalidTaskStatus, "effective round is not closed")
		}
	}
	disqualified, err := k.loadAppliedRoundDisqualifications(ctx, taskKey, summary)
	if err != nil {
		return settlementInputs{}, err
	}
	round1Assignment, err := k.VerifierAssignment.Get(ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1))
	if err != nil {
		return settlementInputs{}, errorsmod.Wrap(types.ErrInvalidTaskStatus, "round 1 verifier assignment is unavailable")
	}
	if round1Assignment.SelectedVerifierCount == 0 ||
		round1Assignment.SelectedVerifierCount != uint32(len(round1Assignment.SelectedVerifiers)) {
		return settlementInputs{}, errorsmod.Wrap(types.ErrInvariantBroken, "round 1 verifier assignment is non-canonical")
	}
	cutoff := summary.GetSettlementFactsCutoffHeight()
	// The cluster is rebuilt from receipts rather than read back from the closed
	// round, so the payout vector and the committed cluster hash always agree.
	_, effectiveResult, err := k.rebuildClosedRound(ctx, taskKey, effectiveRound, cutoff, effectiveRoundState)
	if err != nil {
		return settlementInputs{}, err
	}
	round1Cluster := effectiveResult.Members
	if effectiveRound != types.VerifyRoundV1 {
		_, round1Result, err := k.rebuildClosedRound(ctx, taskKey, types.VerifyRoundV1, cutoff, round1)
		if err != nil {
			return settlementInputs{}, err
		}
		round1Cluster = round1Result.Members
	}

	dutyBuilder, err := k.settlementDutyBuilder(ctx, selection, round1Assignment, height)
	if err != nil {
		return settlementInputs{}, err
	}
	return settlementInputs{
		Core: core, Assignment: assignment, BuilderSelection: selection, Round1Assignment: round1Assignment,
		Budget: budget, Infer: infer, Summary: summary,
		Round1: round1, Round1Cluster: round1Cluster,
		EffectiveRound: effectiveRound, EffectiveResult: effectiveResult,
		DutyBuilder: dutyBuilder, CutoffHeight: cutoff,
		round1SelectedVerifierCount: round1Assignment.SelectedVerifierCount,
		DisqualifiedOperators:       disqualified,
	}, nil
}

func (k Keeper) loadAppliedRoundDisqualifications(
	ctx context.Context,
	taskKey types.TaskKey,
	summary types.TaskRoundSummaryState,
) ([]string, error) {
	if summary.MaxClosedRound < types.ChallengeVerifyRoundV1 {
		return nil, nil
	}
	round, err := k.VerificationRound.Get(ctx, types.NewVerifyRoundKey(taskKey, types.ChallengeVerifyRoundV1))
	if err != nil || round.XClosedHeight == nil || round.XRoundEffectPlanRoot == nil || round.XRoundEffectRoot == nil {
		return nil, errorsmod.Wrap(types.ErrInvalidTaskStatus, "challenge round effects are not closed")
	}
	if round.RoundEffectCount == 0 ||
		!bytes.Equal(summary.Round2EffectRootOrZero32, round.GetRoundEffectRoot()) {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "challenge round effect summary is inconsistent")
	}
	effects := make([]shared.RoundEconomicEffectV1, 0, round.RoundEffectCount)
	disqualified := make([]string, 0)
	for index := uint32(0); index < round.RoundEffectCount; index++ {
		state, err := k.RoundEconomicEffect.Get(ctx, types.NewRoundEconomicEffectKey(taskKey, types.ChallengeVerifyRoundV1, index))
		if err != nil || !bytes.Equal(state.TaskId, taskKey) || state.VerifyRound != types.ChallengeVerifyRoundV1 ||
			state.Effect.EffectIndex != index ||
			state.Effect.Status != shared.RoundEconomicEffectStatusV1_ROUND_ECONOMIC_EFFECT_STATUS_V1_APPLIED {
			return nil, errorsmod.Wrap(types.ErrInvariantBroken, "challenge round effect vector is incomplete")
		}
		effects = append(effects, state.Effect)
		if state.Effect.EffectKind == shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_EARNING_DISQUALIFICATION {
			if state.Effect.XSourceAddress == nil || state.Effect.XDestinationAddress != nil ||
				state.Effect.GetSourceAddress() == "" {
				return nil, errorsmod.Wrap(types.ErrInvariantBroken, "earning disqualification effect has an invalid actor")
			}
			disqualified = append(disqualified, state.Effect.GetSourceAddress())
		}
	}
	planRoot, err := types.RoundEffectPlanRoot(sdk.UnwrapSDKContext(ctx).ChainID(), taskKey, types.ChallengeVerifyRoundV1, effects)
	if err != nil || !bytes.Equal(planRoot[:], round.GetRoundEffectPlanRoot()) {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "challenge round effect plan root is invalid")
	}
	effectRoot, err := types.RoundEffectRoot(
		sdk.UnwrapSDKContext(ctx).ChainID(), taskKey, types.ChallengeVerifyRoundV1, round.GetRoundEffectPlanRoot(), effects,
	)
	if err != nil || !bytes.Equal(effectRoot[:], round.GetRoundEffectRoot()) {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "challenge round effect root is invalid")
	}
	return disqualified, nil
}

// rebuildClosedRound re-derives a closed round's cluster and checks it still
// reproduces the commitments that were frozen when the round closed. A drift here
// means state and its own commitment disagree, which must stop settlement rather
// than silently pay out against recomputed facts.
func (k Keeper) rebuildClosedRound(
	ctx context.Context,
	taskKey types.TaskKey,
	verifyRound uint32,
	cutoffHeight uint64,
	closed types.VerificationRoundState,
) (types.VerificationRoundState, roundCloseResult, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	core, err := k.TaskCore.Get(ctx, taskKey)
	if err != nil {
		return types.VerificationRoundState{}, roundCloseResult{}, errorsmod.Wrap(types.ErrInvalidTaskStatus, "task is unavailable")
	}
	assignment, err := k.VerifierAssignment.Get(ctx, types.NewVerifyRoundKey(taskKey, verifyRound))
	if err != nil {
		return types.VerificationRoundState{}, roundCloseResult{}, errorsmod.Wrap(types.ErrInvalidTaskStatus, "verifier assignment is unavailable")
	}
	samples, err := k.collectRoundSamples(ctx, sdkCtx, core, assignment, verifyRound, cutoffHeight)
	if err != nil {
		return types.VerificationRoundState{}, roundCloseResult{}, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	consensus, err := types.AggregateVerifierConsensus(assignment.SelectedVerifierCount, samples)
	if err != nil {
		return types.VerificationRoundState{}, roundCloseResult{}, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	result := roundCloseResult{
		Verdict: consensus.Verdict, FailureClass: consensus.FailureClass,
		GeneratedTokenCount:   consensus.GeneratedTokenCount,
		Members:               consensus.Members,
		ResultReceiptRefsHash: make([]byte, types.Hash32Len),
		ConsensusClusterHash:  make([]byte, types.Hash32Len),
	}
	if len(consensus.Members) > 0 {
		refs, err := types.ResultReceiptRefsHash(sdkCtx.ChainID(), core.TaskId, verifyRound, consensus.Members)
		if err != nil {
			return types.VerificationRoundState{}, roundCloseResult{}, err
		}
		cluster, err := types.ConsensusClusterHash(sdkCtx.ChainID(), core.TaskId, verifyRound, consensus.Members)
		if err != nil {
			return types.VerificationRoundState{}, roundCloseResult{}, err
		}
		result.ResultReceiptRefsHash, result.ConsensusClusterHash = refs[:], cluster[:]
	}
	if closed.GetVerdict() != result.Verdict || closed.GetFailureClass() != result.FailureClass ||
		closed.GeneratedTokenCount != result.GeneratedTokenCount ||
		!bytes.Equal(closed.GetResultReceiptRefsHash(), result.ResultReceiptRefsHash) ||
		!bytes.Equal(closed.GetConsensusClusterHash(), result.ConsensusClusterHash) {
		return types.VerificationRoundState{}, roundCloseResult{}, errorsmod.Wrap(
			types.ErrInvariantBroken, "closed round does not reproduce its frozen commitments")
	}
	result.RoundFactsHash = append([]byte(nil), closed.GetRoundFactsHash()...)
	return closed, result, nil
}

// settlementDutyBuilder resolves the SETTLE duty builder from the frozen rank.
// §10.10a keeps the *actual* submitter out of the plan hash and out of the store,
// so only this derived operator is ever committed.
func (k Keeper) settlementDutyBuilder(
	ctx context.Context,
	selection types.TaskBuilderSelectionState,
	assignment types.VerifierAssignmentState,
	height uint64,
) (string, error) {
	index, _, err := settlementBuilderSchedule(
		uint64(len(selection.SelectedTaskBuilders)),
		k.hubKeeper.GetHubParams(sdk.UnwrapSDKContext(ctx)).SettlementBuilderGraceBlocks,
		assignment.RevealDeadlineHeight,
		height,
	)
	if err != nil {
		return "", errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	return selection.SelectedTaskBuilders[index], nil
}

func (k Keeper) authorizeSettlementSubmitter(
	ctx context.Context,
	inputs settlementInputs,
	submitter string,
	height uint64,
) (bool, error) {
	grace := k.hubKeeper.GetHubParams(sdk.UnwrapSDKContext(ctx)).SettlementBuilderGraceBlocks
	index, permissionless, err := settlementBuilderSchedule(
		uint64(len(inputs.BuilderSelection.SelectedTaskBuilders)), grace,
		inputs.Round1Assignment.RevealDeadlineHeight, height,
	)
	if err != nil {
		return false, err
	}
	duty := inputs.BuilderSelection.SelectedTaskBuilders[index]
	if duty != inputs.DutyBuilder {
		return false, fmt.Errorf("settlement duty Builder does not match the frozen schedule")
	}
	if permissionless {
		return false, nil
	}
	if err := k.RequireCurrentServiceSubmitter(ctx, shared.ParticipantTypeBuilder, duty, submitter); err != nil {
		return false, err
	}
	return true, nil
}

func settlementBuilderSchedule(builderCount, grace, revealDeadline, height uint64) (uint64, bool, error) {
	if builderCount == 0 || grace == 0 || revealDeadline == 0 {
		return 0, false, fmt.Errorf("settlement Builder schedule is incomplete")
	}
	graceSpan, overflow := checkedMulUint64(builderCount, grace)
	if overflow {
		return 0, false, fmt.Errorf("settlement Builder grace span overflows")
	}
	permissionlessHeight, overflow := checkedHeightAdd(revealDeadline, graceSpan)
	if overflow {
		return 0, false, fmt.Errorf("permissionless settlement height overflows")
	}
	if height > permissionlessHeight {
		return builderCount - 1, true, nil
	}
	index := uint64(0)
	if height > revealDeadline {
		index = (height - revealDeadline - 1) / grace
	}
	if index >= builderCount {
		index = builderCount - 1
	}
	return index, false, nil
}

func (k Keeper) collectTaskGasReimbursements(ctx context.Context, taskKey types.TaskKey) ([]types.TaskGasReimbursementV1, error) {
	// Bounded by this task's own prefix: a full-collection scan would make one
	// settlement's cost grow with every other task's gas receipts.
	iter, err := k.TaskGasReimbursement.Iterate(ctx,
		collections.NewPrefixedTripleRange[types.Hash32Key, types.Hash32Key, uint32](taskKey))
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	items := make([]types.TaskGasReimbursementV1, 0)
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(key.K1(), taskKey) {
			return nil, errorsmod.Wrap(types.ErrInvariantBroken, "gas reimbursement prefix scan left the task")
		}
		item, err := iter.Value()
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	// The plan digest embeds this array inline, so its order has to be the frozen
	// (tx_hash, item_index) order rather than whatever the iterator produced.
	sort.SliceStable(items, func(a, b int) bool {
		if cmp := bytes.Compare(items[a].TxHash, items[b].TxHash); cmp != 0 {
			return cmp < 0
		}
		return items[a].ItemIndex < items[b].ItemIndex
	})
	return items, nil
}

func settlementResponse(
	settlement types.TaskSettlementState,
	plan types.SettlementPlanV1,
	mutation shared.MutationStatusV1,
) *types.MsgSettleTaskResponse {
	return &types.MsgSettleTaskResponse{
		TaskId: append([]byte(nil), settlement.TaskId...), SettlementId: append([]byte(nil), settlement.SettlementId...),
		SettlementFactsHash: append([]byte(nil), settlement.SettlementFactsHash...),
		SettlementPlanHash:  append([]byte(nil), settlement.SettlementPlanHash...),
		Verdict:             settlement.Verdict, RefundAmount: plan.RefundAmount,
		SettlementHeight: settlement.SettlementHeight, TaskFinalityHeight: settlement.TaskFinalityHeight,
		Status: mutation,
	}
}

// applySettlementPlan is the only writer of TaskSettlementState. It performs the
// §10.10a step 7 sequence in one cache context: settlement rows, budget
// finalization, earnings credit, treasury maintenance, user refund, terminal task
// state, liability release and the code 19 event.
func (k Keeper) applySettlementPlan(
	ctx context.Context,
	inputs settlementInputs,
	facts types.SettlementFactsV1,
	plan types.SettlementPlanV1,
	bill types.TaskSettlementBillV1,
	settlementID []byte,
	height uint64,
) (types.TaskSettlementState, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	taskKey := types.NewTaskKey(inputs.Core.TaskId)
	if err := k.applyDataUnavailableBuilderFaults(ctx, inputs.Core); err != nil {
		return types.TaskSettlementState{}, err
	}
	factsHash, err := types.SettlementFactsHash(sdkCtx.ChainID(), settlementID, facts)
	if err != nil {
		return types.TaskSettlementState{}, err
	}
	planHash, err := types.SettlementPlanHash(sdkCtx.ChainID(), plan)
	if err != nil {
		return types.TaskSettlementState{}, err
	}
	billHash, err := types.SettlementBillHash(bill)
	if err != nil {
		return types.TaskSettlementState{}, err
	}
	finalityHeight := inputs.Summary.GetRoundsClosedHeight()
	if err := k.persistSettlementFailureRows(ctx, inputs, finalityHeight); err != nil {
		return types.TaskSettlementState{}, err
	}

	settlement := types.TaskSettlementState{
		TaskId: append([]byte(nil), inputs.Core.TaskId...), SettlementId: append([]byte(nil), settlementID...),
		FeeRuleVersion: plan.FeeRuleVersion, EffectiveVerifyRound: plan.EffectiveVerifyRound,
		Verdict: facts.Verdict, FailureClass: facts.FailureClass,
		SettlementFactsCutoffHeight: facts.SettlementFactsCutoffHeight, SettlementHeight: facts.SettlementHeight,
		TaskFinalityHeight:  finalityHeight,
		GeneratedTokenCount: plan.GeneratedTokenCount, WorkUnit: plan.WorkUnit,
		InferReceiptRefOrZero32: append([]byte(nil), inputs.Infer.InferReceiptHash...),
		WorkerGross:             plan.WorkerGross, WorkerMaintenance: plan.WorkerMaintenance, WorkerNet: plan.WorkerNet,
		VerifierSlotGross: plan.VerifierSlotGross, VerifierPayoutCount: uint32(len(plan.VerifierPayouts)),
		GasReimbursementCount: uint32(len(plan.GasReimbursements)), GasReimbursementsHash: plan.GasReimbursementsHash,
		MaintenanceFee: plan.MaintenanceFee, RefundAmount: plan.RefundAmount,
		OriginalReservedAmount: plan.OriginalReservedAmount,
		SettlementFactsHash:    factsHash[:], TaskRoundSummaryHash: plan.TaskRoundSummaryHash,
		SettlementBillHashOrZero32: billHash[:], SettlementPlanHash: planHash[:],
		GasReimbursedTotal: inputs.Budget.GasReimbursedTotal,
	}
	if inputs.Assignment.WinnerWorker != "" {
		settlement.XWorkerOperatorAddress = &types.TaskSettlementState_WorkerOperatorAddress{
			WorkerOperatorAddress: inputs.Assignment.WinnerWorker,
		}
	}
	if err := k.TaskSettlement.Set(ctx, taskKey, settlement); err != nil {
		return types.TaskSettlementState{}, err
	}
	paidRolesHash, err := types.PaidRolesHash(sdkCtx.ChainID(), inputs.Core.TaskId, settlementID, facts.PaidRoles)
	if err != nil {
		return types.TaskSettlementState{}, err
	}
	if err := k.SettlementFactsRetained.Set(ctx, taskKey, types.SettlementFactsRetainedState{
		TaskId: append([]byte(nil), inputs.Core.TaskId...), TaskHash: append([]byte(nil), inputs.Core.AcceptedTaskHash...),
		SettlementId: append([]byte(nil), settlementID...), SettlementFactsHash: factsHash[:],
		SettlementFactsCutoffHeight: facts.SettlementFactsCutoffHeight, SettlementHeight: facts.SettlementHeight,
		SettlementDutyBuilderOperator: facts.SettlementDutyBuilderOperator,
		ConsensusClusterHash:          append([]byte(nil), facts.ConsensusClusterHash...),
		RecomputedVerdict:             facts.Verdict, FailureClass: facts.FailureClass,
		FaultSummaryHash: append([]byte(nil), facts.FaultSummaryHash...),
		PaidRolesHash:    paidRolesHash[:], PaidRoleCount: uint32(len(facts.PaidRoles)),
		InferReceiptRefOrZero32:      append([]byte(nil), facts.InferReceiptRef...),
		ResultReceiptRefsHash:        append([]byte(nil), facts.ResultReceiptRefsHash...),
		ChallengeCloseHeight:         facts.ChallengeCloseHeight,
		EffectiveVerifyRound:         plan.EffectiveVerifyRound,
		FeeRuleVersion:               plan.FeeRuleVersion,
		GeneratedTokenCount:          plan.GeneratedTokenCount,
		WorkUnit:                     plan.WorkUnit,
		SettlementBillHashOrZero32:   append([]byte(nil), billHash[:]...),
		TaskRoundSummaryHash:         append([]byte(nil), plan.TaskRoundSummaryHash...),
		EvidenceSchemaHash:           append([]byte(nil), inputs.Assignment.EvidenceSchemaHash...),
		JudgmentFunctionVersion:      inputs.Assignment.JudgmentFunctionVersion,
		CanonicalEncodingVersion:     inputs.Assignment.CanonicalEncodingVersion,
		ProfileExecutionSnapshotHash: append([]byte(nil), inputs.Assignment.ProfileExecutionSnapshotHash...),
		GenerationParamsDigest:       append([]byte(nil), inputs.Assignment.GenerationParamsDigest...),
		MetricAggregateProofVersion:  inputs.Assignment.MetricAggregateProofVersion,
	}); err != nil {
		return types.TaskSettlementState{}, err
	}
	for _, payout := range plan.VerifierPayouts {
		if err := k.VerifierPayout.Set(ctx, types.NewVerifierPayoutKey(taskKey, payout.SelectedVerifierIndex), types.VerifierPayoutState{
			TaskId: append([]byte(nil), inputs.Core.TaskId...), SettlementId: append([]byte(nil), settlementID...),
			OperatorAddress: payout.OperatorAddress, SelectedVerifierIndex: payout.SelectedVerifierIndex,
			Gross: payout.Gross, Maintenance: payout.Maintenance, Net: payout.Net,
		}); err != nil {
			return types.TaskSettlementState{}, err
		}
	}

	credits, err := settlementEarningsCredits(inputs, plan)
	if err != nil {
		return types.TaskSettlementState{}, err
	}
	if len(credits) > 0 {
		if err := k.hubKeeper.CreditTaskSettlementEarnings(ctx, inputs.Core.SessionId, inputs.Core.TaskId, credits, height); err != nil {
			return types.TaskSettlementState{}, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
		}
	}
	// No settle-stage Builder contribution is credited. keeper_api_contract.md:3273/:3635
	// and keeper_data_structure_contract.md:1564 all say Phase 0 does not create
	// BuilderContributionState:
	// it has no Builder reward or term consumer, and its four counter arrays have
	// no complete producer. The proposal receipt and its event are the audit fact.
	if maintenance, err := shared.ParseAmount(plan.MaintenanceFee); err != nil {
		return types.TaskSettlementState{}, err
	} else if maintenance != 0 {
		if err := k.hubKeeper.CreditTaskMaintenance(ctx, settlementID, plan.MaintenanceFee, height); err != nil {
			return types.TaskSettlementState{}, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
		}
	}
	if err := k.finalizeSettledBudget(ctx, inputs, plan); err != nil {
		return types.TaskSettlementState{}, err
	}
	if err := k.releaseTaskLiabilitiesForSettlement(ctx, inputs, height); err != nil {
		return types.TaskSettlementState{}, err
	}

	core := inputs.Core
	if facts.Verdict == types.TaskVerdict_TASK_VERDICT_PASS {
		core.TaskPhase = types.TaskPhase_TASK_PHASE_SETTLED
		core.VerificationStatus = types.VerificationStatus_VERIFICATION_STATUS_VERIFICATION_PASSED
	} else {
		core.TaskPhase = types.TaskPhase_TASK_PHASE_FAILED
		if facts.Verdict == types.TaskVerdict_TASK_VERDICT_NO_CONSENSUS {
			core.VerificationStatus = types.VerificationStatus_VERIFICATION_STATUS_NO_CONSENSUS
		} else {
			core.VerificationStatus = types.VerificationStatus_VERIFICATION_STATUS_VERIFY_FAILED
		}
	}
	// FINALIZED, not REFUNDED, even on a failing verdict. §16.4's enum tables pair
	// OrderSequenceStatus REFUNDED=4/SETTLED=5 with SettlementStatus
	// FINALIZED=2/REFUNDED=3, and this path writes ORDER_SEQUENCE_STATUS_SETTLED;
	// marking the settlement REFUNDED here would make the two disagree on the same
	// task. It is also not a pure refund: §10.10c pays the round-1 verifier cluster
	// off worker_gross even when worker_payable_gross is zero, so a FAIL verdict
	// still moves business funds. The one path that is definitionally a refund —
	// pre_verification_failure, which already declares ORDER_SEQUENCE_STATUS_REFUNDED
	// — is where SETTLEMENT_STATUS_REFUNDED is produced. Whether §10.7's thin round
	// should reach that shape through this path is the open contract question
	// recorded as N-26/N-37(b).
	core.SettlementStatus = types.SettlementStatus_SETTLEMENT_STATUS_FINALIZED
	core.FinalityStatus = shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL
	core.EffectiveVerifyRound = plan.EffectiveVerifyRound
	core.XTaskFinalityHeight = &types.TaskCoreState_TaskFinalityHeight{TaskFinalityHeight: finalityHeight}
	core.UpdatedHeight = height
	if err := k.TaskCore.Set(ctx, taskKey, core); err != nil {
		return types.TaskSettlementState{}, err
	}
	cleanupHeight, overflow := checkedHeightAdd(finalityHeight, core.EvidenceRetentionBlocksSnapshot)
	if overflow {
		return types.TaskSettlementState{}, errorsmod.Wrap(types.ErrInvariantBroken, "evidence cleanup height overflows")
	}
	if err := addDeadlineIndex(ctx, k.EvidenceCleanupIndex, taskKey, cleanupHeight); err != nil {
		return types.TaskSettlementState{}, err
	}
	workerGross, err := shared.ParseAmount(plan.WorkerGross)
	if err != nil {
		return types.TaskSettlementState{}, err
	}
	if facts.Verdict == types.TaskVerdict_TASK_VERDICT_PASS && workerGross != 0 {
		priceBid, err := shared.ParseAmount(inputs.Budget.PriceBid)
		if err != nil {
			return types.TaskSettlementState{}, err
		}
		if err := k.hubKeeper.RecordProfilePriceSample(ctx, shared.ProfilePriceSampleV1{
			TaskId: append([]byte(nil), inputs.Core.TaskId...), ModelId: inputs.Core.ModelId,
			ProfileVersion: inputs.Core.ProfileVersion, PriceBid: priceBid,
		}); err != nil {
			return types.TaskSettlementState{}, errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
		}
	}
	if err := k.recordTaskSupportCompletions(ctx, inputs, facts, finalityHeight); err != nil {
		return types.TaskSettlementState{}, err
	}
	if err := emitTypedEvent(ctx, &types.EventTaskSettled{
		SessionId: append([]byte(nil), inputs.Core.SessionId...), TaskId: append([]byte(nil), inputs.Core.TaskId...),
		SettlementId: append([]byte(nil), settlementID...), Verdict: facts.Verdict, FailureClass: facts.FailureClass,
		EffectiveVerifyRound: plan.EffectiveVerifyRound, SettlementFactsHash: factsHash[:],
		SettlementPlanHash: planHash[:], TaskRoundSummaryHash: plan.TaskRoundSummaryHash,
		SettlementFactsCutoffHeight: facts.SettlementFactsCutoffHeight,
		SettlementHeight:            facts.SettlementHeight, TaskFinalityHeight: finalityHeight,
	}); err != nil {
		return types.TaskSettlementState{}, err
	}
	return settlement, nil
}

// settlementEarningsCredits turns the plan into the credit vector Hub expects.
// net + gas reimbursement both land in claimable_task_fee; maintenance is
// Treasury's and is not a credit.
//
// Hub requires the vector to be strictly ascending in *decoded address bytes*,
// which is not the Bech32 string order: the Bech32 charset
// "qpzry9x8gf2tvdw0s3jn54khce6mua7l" is not in ASCII order, so sorting the
// encoded strings would hand Hub an out-of-order vector and fail the settlement.
func settlementEarningsCredits(inputs settlementInputs, plan types.SettlementPlanV1) ([]hubtypes.TaskEarningsCredit, error) {
	totals := make(map[string]uint64, len(plan.VerifierPayouts)+1+len(plan.GasReimbursements))
	add := func(address string, amount shared.Amount) error {
		value, err := shared.ParseAmount(amount)
		if err != nil {
			return err
		}
		if value == 0 || address == "" {
			return nil
		}
		sum, ok := checkedAddU64(totals[address], value)
		if !ok {
			return fmt.Errorf("settlement credit for %s overflows", address)
		}
		totals[address] = sum
		return nil
	}
	if err := add(inputs.Assignment.WinnerWorker, plan.WorkerNet); err != nil {
		return nil, err
	}
	for _, payout := range plan.VerifierPayouts {
		if err := add(payout.OperatorAddress, payout.Net); err != nil {
			return nil, err
		}
	}
	for _, item := range plan.GasReimbursements {
		if err := add(item.FeePayer, item.ReimbursedAmount); err != nil {
			return nil, err
		}
	}
	addresses := make([]string, 0, len(totals))
	decoded := make(map[string][]byte, len(totals))
	for address := range totals {
		raw, err := types.CanonicalOperatorAddressBytes("settlement_beneficiary", address)
		if err != nil {
			return nil, err
		}
		addresses = append(addresses, address)
		decoded[address] = raw
	}
	sort.Slice(addresses, func(a, b int) bool {
		return bytes.Compare(decoded[addresses[a]], decoded[addresses[b]]) < 0
	})
	credits := make([]hubtypes.TaskEarningsCredit, 0, len(addresses))
	for index, address := range addresses {
		if index != 0 && bytes.Equal(decoded[addresses[index-1]], decoded[address]) {
			return nil, fmt.Errorf("settlement credits collide on one beneficiary")
		}
		credits = append(credits, hubtypes.TaskEarningsCredit{
			Beneficiary: address, Amount: shared.NewAmount(totals[address]),
		})
	}
	return credits, nil
}

// finalizeSettledBudget drains the escrow exactly. BuildSettlementPlan already
// proved net + maintenance + gas + refund == the apply-start reserved amount, so
// this only executes that identity as three transfers:
//
//	net + gas   -> Hub Rewards, which is the account MsgClaimEarnings pays from
//	maintenance -> Treasury
//	refund      -> the ordering user
//
// The earnings ledger is only a claim, never a transfer, so omitting the Rewards
// leg would leave every payout unclaimable and strand the balance in escrow.
func (k Keeper) finalizeSettledBudget(ctx context.Context, inputs settlementInputs, plan types.SettlementPlanV1) error {
	maintenance, err := shared.ParseAmount(plan.MaintenanceFee)
	if err != nil {
		return err
	}
	refund, err := shared.ParseAmount(plan.RefundAmount)
	if err != nil {
		return err
	}
	claimable, err := settlementClaimableTotal(plan)
	if err != nil {
		return err
	}
	if err := k.sendFromTaskEscrow(ctx, hubtypes.RewardsModuleName, claimable); err != nil {
		return err
	}
	if err := k.sendFromTaskEscrow(ctx, hubtypes.TreasuryModuleName, maintenance); err != nil {
		return err
	}
	if refund > 0 {
		if err := k.releaseTaskBudgetRefund(ctx, inputs.Core.UserAddress, refund); err != nil {
			return err
		}
	}
	budget := inputs.Budget
	budget.ReservedAmount = shared.NewAmount(0)
	budget.TxFeeReserveRemaining = shared.NewAmount(0)
	budget.BudgetStatus = types.TaskBudgetStatus_TASK_BUDGET_STATUS_FINALIZED
	if err := k.TaskBudget.Set(ctx, types.NewTaskKey(inputs.Core.TaskId), budget); err != nil {
		return err
	}
	// A settled task is no longer in flight. The registered invariant ties
	// open_pending_count to the count of RESERVED budgets, so this has to run in
	// the same transition that leaves RESERVED.
	if err := k.closeStreamPendingTask(ctx, inputs.Core.SessionId); err != nil {
		return err
	}
	return k.finalizeOrderSequenceState(ctx, inputs.Core.TaskId, types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_SETTLED)
}

// settlementClaimableTotal is the part of the escrow that becomes a Hub earnings
// claim: every payee net plus every gas reimbursement. It must equal the sum of
// the credits settlementEarningsCredits hands to Hub.
func settlementClaimableTotal(plan types.SettlementPlanV1) (uint64, error) {
	total, err := shared.ParseAmount(plan.WorkerNet)
	if err != nil {
		return 0, err
	}
	for _, payout := range plan.VerifierPayouts {
		net, err := shared.ParseAmount(payout.Net)
		if err != nil {
			return 0, err
		}
		sum, ok := checkedAddU64(total, net)
		if !ok {
			return 0, fmt.Errorf("settlement claimable total overflows")
		}
		total = sum
	}
	for _, item := range plan.GasReimbursements {
		amount, err := shared.ParseAmount(item.ReimbursedAmount)
		if err != nil {
			return 0, err
		}
		sum, ok := checkedAddU64(total, amount)
		if !ok {
			return 0, fmt.Errorf("settlement claimable total overflows")
		}
		total = sum
	}
	return total, nil
}

func (k Keeper) sendFromTaskEscrow(ctx context.Context, module string, amount uint64) error {
	if amount == 0 {
		return nil
	}
	denom := k.hubKeeper.GetHubParams(sdk.UnwrapSDKContext(ctx)).BusinessDenom
	if denom == "" {
		return errorsmod.Wrap(types.ErrInvariantBroken, "business denom is unavailable")
	}
	coins := sdk.NewCoins(sdk.NewCoin(denom, sdkmath.NewIntFromUint64(amount)))
	if err := k.bankKeeper.SendCoinsFromModuleToModule(ctx, shared.TaskEscrowModuleName, module, coins); err != nil {
		return errorsmod.Wrap(types.ErrInsufficientEscrow, err.Error())
	}
	return nil
}

func (k Keeper) releaseTaskLiabilitiesForSettlement(ctx context.Context, inputs settlementInputs, height uint64) error {
	sessionID := hex.EncodeToString(inputs.Core.SessionId)
	taskID := hex.EncodeToString(inputs.Core.TaskId)
	if err := k.releaseTaskOnlineResponsibilities(ctx, sessionID, taskID, height); err != nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	if err := k.releaseTaskAdmissionRefs(ctx, inputs.Core, height); err != nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	if err := k.RemoveRoleActiveTaskIndexes(ctx, inputs.Core.TaskId); err != nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	return nil
}

// recordTaskSupportCompletions is condition 1 of keeper_api_contract.md §10.9's
// activation rule:
//
//	"Promotion of a support to active is triggered after task finality, not by a
//	 separate Msg ... 1. the task settled successfully, there was no fault, and the
//	 paid duties include the duty actually assigned to that Cortex Node."
//
// Conditions 2-8 (capability match, the WORKER P30 / VERIFIER evidence split,
// activation proof, jail interlock, stake snapshots and the Profile aggregate)
// all live in Hub's RecordTaskSupportCompletion, which had no caller at all. The
// consequence was the whole activation chain: activation_kind stayed NONE, so
// support_active stayed false, so active_supporter_count stayed 0, so no Profile
// could ever derive ACTIVE — and governance is explicitly forbidden from writing
// ACTIVE directly, so nothing else could unstick it.
//
// facts.PaidRoles is exactly the "paid duties" set: derivePaidRoles already
// drops a disqualified operator and only includes the Worker on a PASS verdict,
// which is the "no fault" half. A non-PASS settlement has no paid Worker and
// refunds the business budget, so nothing activates from it.
func (k Keeper) recordTaskSupportCompletions(
	ctx context.Context,
	inputs settlementInputs,
	facts types.SettlementFactsV1,
	finalityHeight uint64,
) error {
	if facts.Verdict != types.TaskVerdict_TASK_VERDICT_PASS || len(facts.PaidRoles) == 0 {
		return nil
	}
	orderValue, err := shared.ParseAmount(inputs.Core.OrderValue)
	if err != nil {
		return err
	}
	if orderValue == 0 {
		return errorsmod.Wrap(types.ErrInvariantBroken, "settled task has no frozen order_value")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	profile, found := k.hubKeeper.GetProfileState(sdkCtx, inputs.Core.ModelId, inputs.Core.ProfileVersion)
	if !found {
		return errorsmod.Wrap(types.ErrInvariantBroken, "settled task references an unknown profile")
	}
	// parameter_table.md freezes tier 1..5 -> P0..P4 as a table; RewardBucketForResourceTier
	// reproduces it literally rather than inferring it from coincident enum values.
	if profile.ResourceTier > math.MaxUint32 {
		return errorsmod.Wrap(types.ErrInvariantBroken, "profile resource_tier is out of range")
	}
	bucket, err := hubtypes.RewardBucketForResourceTier(uint32(profile.ResourceTier))
	if err != nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
	}
	for _, role := range facts.PaidRoles {
		if err := k.hubKeeper.RecordTaskSupportCompletion(ctx, hubtypes.TaskSupportCompletionFact{
			TaskID:          append([]byte(nil), inputs.Core.TaskId...),
			OperatorAddress: role.OperatorAddress,
			ModelID:         inputs.Core.ModelId,
			ProfileVersion:  inputs.Core.ProfileVersion,
			Duty:            role.Duty,
			RewardBucket:    bucket,
			OrderValue:      orderValue,
			Height:          finalityHeight,
		}); err != nil {
			return errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
		}
	}
	return nil
}
