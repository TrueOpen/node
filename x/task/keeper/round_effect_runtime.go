package keeper

import (
	"bytes"
	"context"
	"sort"

	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

type roundEffectSubject struct {
	operator string
	duty     shared.Duty
	reserved uint64
}

func (k Keeper) freezeRoundEconomicEffects(
	ctx context.Context,
	taskKey types.TaskKey,
	round2 types.VerificationRoundState,
	round2Result roundCloseResult,
	outcome shared.RoundOutcomeV1,
	height uint64,
) error {
	if round2.VerifyRound != types.ChallengeVerifyRoundV1 || round2.XClosedHeight == nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, "round 2 is not terminal")
	}
	key := types.NewVerifyRoundKey(taskKey, types.ChallengeVerifyRoundV1)
	funding, err := k.ReadRoundFunding(ctx, key)
	if err != nil || funding.XClosedHeight != nil || funding.XRoundOutcome != nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, "challenge funding is not open")
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	zero := shared.NewAmount(0)
	effects := make([]shared.RoundEconomicEffectV1, 0, len(round2Result.Samples)+len(round2Result.Members)*2+5)
	appendEffect := func(effect shared.RoundEconomicEffectV1) {
		effect.EffectIndex = uint32(len(effects))
		effect.AppliedAmount = zero
		effect.UnfilledAmount = zero
		effect.Status = shared.RoundEconomicEffectStatusV1_ROUND_ECONOMIC_EFFECT_STATUS_V1_PENDING
		effects = append(effects, effect)
	}

	// Every timely, valid result earns the frozen slot fee. Consensus-cluster
	// membership affects the verdict, not whether an otherwise valid duty was
	// performed.
	samples := append([]types.VerifierSampleV1(nil), round2Result.Samples...)
	sort.Slice(samples, func(i, j int) bool { return samples[i].SelectedVerifierIndex < samples[j].SelectedVerifierIndex })
	for _, sample := range samples {
		appendEffect(shared.RoundEconomicEffectV1{
			EffectKind:          shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_VERIFIER_FEE,
			XSourceAddress:      &shared.RoundEconomicEffectV1_SourceAddress{SourceAddress: funding.OpenerAddress},
			XDestinationAddress: &shared.RoundEconomicEffectV1_DestinationAddress{DestinationAddress: sample.VerifierOperatorAddress},
			RequestedAmount:     funding.ChallengeSlotFee,
		})
	}

	var subjects []roundEffectSubject
	if outcome == shared.RoundOutcomeV1_ROUND_OUTCOME_V1_DIVERGED {
		subjects, err = k.disprovedRoundOneSubjects(ctx, taskKey, round2Result.Verdict)
		if err != nil {
			return err
		}
		// §10.14a orders these by operator address bytes, and the resulting
		// effect_index sequence is committed by TRUEOPEN_ROUND_EFFECT_PLAN_V1. A
		// comparator that swallowed a decode error would compare two empty slices,
		// make every comparison false, and leave sort.Slice free to pick any
		// permutation — a different one per node, and therefore a different plan
		// root. The keys are decoded once, up front, and a failure or a collision
		// stops the round instead of being ordered arbitrarily.
		keys := make([][]byte, len(subjects))
		for i, subject := range subjects {
			operator, err := types.CanonicalOperatorAddressBytes("effect source", subject.operator)
			if err != nil {
				return errorsmod.Wrap(types.ErrInvariantBroken, err.Error())
			}
			keys[i] = operator
		}
		order := make([]int, len(subjects))
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(a, b int) bool {
			return bytes.Compare(keys[order[a]], keys[order[b]]) < 0
		})
		sorted := make([]roundEffectSubject, 0, len(subjects))
		for i, index := range order {
			if i > 0 && bytes.Equal(keys[order[i-1]], keys[index]) {
				return errorsmod.Wrap(types.ErrInvariantBroken,
					"two disproved round-1 subjects decode to the same operator address")
			}
			sorted = append(sorted, subjects[index])
		}
		subjects = sorted
		for _, subject := range subjects {
			appendEffect(shared.RoundEconomicEffectV1{
				EffectKind:      shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_EARNING_DISQUALIFICATION,
				XSourceAddress:  &shared.RoundEconomicEffectV1_SourceAddress{SourceAddress: subject.operator},
				RequestedAmount: zero,
			})
		}
		for _, subject := range subjects {
			requested, ok := mulDivFloorRound(subject.reserved, uint64(funding.ChallengeFaultSlashBpsSnapshot), uint64(types.BasisPointsMaximum))
			if !ok {
				return errorsmod.Wrap(types.ErrInvariantBroken, "challenge slash amount overflows")
			}
			appendEffect(shared.RoundEconomicEffectV1{
				EffectKind:      shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_SERVICE_SLASH,
				XSourceAddress:  &shared.RoundEconomicEffectV1_SourceAddress{SourceAddress: subject.operator},
				RequestedAmount: shared.NewAmount(requested),
			})
		}
	}

	verifierPaid, overflow := checkedMulUint64(mustAmountValue(funding.ChallengeSlotFee), uint64(len(samples)))
	if overflow {
		return errorsmod.Wrap(types.ErrInvariantBroken, "challenge verifier payment overflows")
	}
	if outcome == shared.RoundOutcomeV1_ROUND_OUTCOME_V1_DIVERGED {
		appendEffect(shared.RoundEconomicEffectV1{
			EffectKind:          shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_OPENER_COST_RECOVERY,
			XDestinationAddress: &shared.RoundEconomicEffectV1_DestinationAddress{DestinationAddress: funding.OpenerAddress},
			RequestedAmount:     shared.NewAmount(verifierPaid),
		})
	}

	bond := mustAmountValue(funding.ChallengeOpenBond)
	bondSlash := uint64(0)
	switch outcome {
	case shared.RoundOutcomeV1_ROUND_OUTCOME_V1_DIVERGED:
		appendEffect(shared.RoundEconomicEffectV1{
			EffectKind:          shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_BOND_REFUND,
			XDestinationAddress: &shared.RoundEconomicEffectV1_DestinationAddress{DestinationAddress: funding.OpenerAddress},
			RequestedAmount:     shared.NewAmount(bond),
		})
	case shared.RoundOutcomeV1_ROUND_OUTCOME_V1_CONCURRED:
		bondSlash = bond
		appendEffect(shared.RoundEconomicEffectV1{EffectKind: shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_BOND_SLASH, RequestedAmount: shared.NewAmount(bond)})
	case shared.RoundOutcomeV1_ROUND_OUTCOME_V1_UNRESOLVED,
		shared.RoundOutcomeV1_ROUND_OUTCOME_V1_UNAVAILABLE,
		shared.RoundOutcomeV1_ROUND_OUTCOME_V1_EXPIRED:
		bondSlash, _ = mulDivFloorRound(bond, uint64(funding.ChallengeInconclusiveBondSlashBpsSnapshot), uint64(types.BasisPointsMaximum))
		bondRefund := bond - bondSlash
		if bondRefund != 0 {
			appendEffect(shared.RoundEconomicEffectV1{
				EffectKind:          shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_BOND_REFUND,
				XDestinationAddress: &shared.RoundEconomicEffectV1_DestinationAddress{DestinationAddress: funding.OpenerAddress},
				RequestedAmount:     shared.NewAmount(bondRefund),
			})
		}
		if bondSlash != 0 {
			appendEffect(shared.RoundEconomicEffectV1{EffectKind: shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_BOND_SLASH, RequestedAmount: shared.NewAmount(bondSlash)})
		}
	default:
		return errorsmod.Wrap(types.ErrInvariantBroken, "challenge outcome is invalid")
	}

	maxResidual := bondSlash
	for _, effect := range effects {
		if effect.EffectKind != shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_SERVICE_SLASH {
			continue
		}
		value := mustAmountValue(effect.RequestedAmount)
		var overflow bool
		maxResidual, overflow = checkedHeightAdd(maxResidual, value)
		if overflow {
			return errorsmod.Wrap(types.ErrInvariantBroken, "challenge residual cap overflows")
		}
	}
	if maxResidual != 0 {
		appendEffect(shared.RoundEconomicEffectV1{
			EffectKind:      shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_TREASURY_RESIDUAL,
			RequestedAmount: shared.NewAmount(maxResidual),
		})
	}
	// V1 can pay at most challenge_count slots, disqualify/slash the one
	// Worker plus three round-1 Verifiers, then append recovery, up to two bond
	// actions and residual.
	if len(effects) == 0 || uint64(len(effects)) > uint64(params.Challenge.ChallengeVerifierCount)+12 {
		return errorsmod.Wrap(types.ErrInvariantBroken, "round effect plan exceeds its derived bound")
	}
	planRoot, err := types.RoundEffectPlanRoot(sdk.UnwrapSDKContext(ctx).ChainID(), taskKey, types.ChallengeVerifyRoundV1, effects)
	if err != nil {
		return err
	}
	for index, effect := range effects {
		if err := k.RoundEconomicEffect.Set(ctx, types.NewRoundEconomicEffectKey(taskKey, types.ChallengeVerifyRoundV1, uint32(index)),
			types.RoundEconomicEffectState{TaskId: append([]byte(nil), taskKey...), VerifyRound: types.ChallengeVerifyRoundV1, Effect: effect}); err != nil {
			return err
		}
	}
	round2.RoundEffectCount = uint32(len(effects))
	round2.XRoundEffectPlanRoot = &types.VerificationRoundState_RoundEffectPlanRoot{RoundEffectPlanRoot: planRoot[:]}
	if err := k.WriteVerificationRound(ctx, key, round2); err != nil {
		return err
	}
	cursor := types.RoundEconomicEffectApplyCursorState{
		TaskId: append([]byte(nil), taskKey...), VerifyRound: types.ChallengeVerifyRoundV1,
		NextApplyHeight: height, EffectPoolBalance: zero,
	}
	if err := k.RoundEconomicEffectApplyCursor.Set(ctx, key, cursor); err != nil {
		return err
	}
	return k.RoundEconomicEffectApplyIndex.Set(ctx, types.NewVerifyRoundIndexKey(height, taskKey, types.ChallengeVerifyRoundV1))
}

func (k Keeper) disprovedRoundOneSubjects(
	ctx context.Context,
	taskKey types.TaskKey,
	round2Verdict types.TaskVerdict,
) ([]roundEffectSubject, error) {
	round1, err := k.ReadVerificationRound(ctx, types.NewVerifyRoundKey(taskKey, types.VerifyRoundV1))
	if err != nil || round1.XClosedHeight == nil {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "round 1 is unavailable")
	}
	_, result, err := k.rebuildClosedRound(ctx, taskKey, types.VerifyRoundV1, round1.GetClosedHeight(), round1)
	if err != nil {
		return nil, err
	}
	assignment, err := k.ReadTaskAssignment(ctx, taskKey)
	if err != nil {
		return nil, err
	}
	operators := make([]struct {
		operator string
		duty     shared.Duty
	}, 0, len(result.Members)+1)
	if round1.GetVerdict() == types.TaskVerdict_TASK_VERDICT_PASS && round2Verdict == types.TaskVerdict_TASK_VERDICT_FAIL {
		operators = append(operators, struct {
			operator string
			duty     shared.Duty
		}{assignment.WinnerWorker, shared.DutyWorker})
	}
	for _, member := range result.Members {
		operators = append(operators, struct {
			operator string
			duty     shared.Duty
		}{member.VerifierOperatorAddress, shared.DutyVerifier})
	}
	seen := make(map[string]struct{}, len(operators))
	subjects := make([]roundEffectSubject, 0, len(operators))
	for _, item := range operators {
		if _, duplicate := seen[item.operator]; duplicate {
			return nil, errorsmod.Wrap(types.ErrInvariantBroken, "one operator holds two disproved duties")
		}
		seen[item.operator] = struct{}{}
		snapshot, err := k.hubKeeper.GetTaskLiabilityReservation(ctx, taskKey, item.duty, item.operator)
		if err != nil || !snapshot.Reserved || snapshot.ReservedAmount == 0 {
			return nil, errorsmod.Wrap(types.ErrInvariantBroken, "disproved operator liability is unavailable")
		}
		subjects = append(subjects, roundEffectSubject{operator: item.operator, duty: item.duty, reserved: snapshot.ReservedAmount})
	}
	return subjects, nil
}

// applyRoundEconomicEffects advances at most maxItems from one round cursor.
// Every call is expected to run in the caller's cache transaction.
func (k Keeper) applyRoundEconomicEffects(
	ctx context.Context,
	taskKey types.TaskKey,
	verifyRound uint32,
	currentHeight uint64,
	maxItems uint32,
) (uint32, bool, error) {
	if verifyRound != types.ChallengeVerifyRoundV1 || maxItems == 0 {
		return 0, false, errorsmod.Wrap(types.ErrInvalidTaskStatus, "invalid round effect apply scope")
	}
	key := types.NewVerifyRoundKey(taskKey, verifyRound)
	cursor, err := k.RoundEconomicEffectApplyCursor.Get(ctx, key)
	if err != nil {
		return 0, false, err
	}
	round, err := k.ReadVerificationRound(ctx, key)
	if err != nil || round.XClosedHeight == nil || round.RoundEffectCount == 0 || len(round.GetRoundEffectPlanRoot()) != types.Hash32Len {
		return 0, false, errorsmod.Wrap(types.ErrInvariantBroken, "round effect plan is unavailable")
	}
	funding, err := k.ReadRoundFunding(ctx, key)
	if err != nil {
		return 0, false, err
	}
	visited := uint32(0)
	for visited < maxItems && cursor.NextEffectIndex < round.RoundEffectCount {
		effectKey := types.NewRoundEconomicEffectKey(taskKey, verifyRound, cursor.NextEffectIndex)
		state, err := k.RoundEconomicEffect.Get(ctx, effectKey)
		if err != nil || state.Effect.EffectIndex != cursor.NextEffectIndex {
			return visited, false, errorsmod.Wrap(types.ErrInvariantBroken, "round effect sequence has a gap")
		}
		visited++
		cursor.VisitedCount++
		if state.Effect.Status == shared.RoundEconomicEffectStatusV1_ROUND_ECONOMIC_EFFECT_STATUS_V1_PENDING {
			if err := k.applyOneRoundEconomicEffect(ctx, taskKey, round, &funding, &cursor, &state.Effect, currentHeight); err != nil {
				return visited, false, err
			}
			if err := k.RoundEconomicEffect.Set(ctx, effectKey, state); err != nil {
				return visited, false, err
			}
			cursor.AppliedCount++
		} else if state.Effect.Status != shared.RoundEconomicEffectStatusV1_ROUND_ECONOMIC_EFFECT_STATUS_V1_APPLIED {
			return visited, false, errorsmod.Wrap(types.ErrInvariantBroken, "round effect status is invalid")
		}
		cursor.NextEffectIndex++
	}
	if err := k.WriteRoundFunding(ctx, key, funding); err != nil {
		return visited, false, err
	}
	oldIndex := types.NewVerifyRoundIndexKey(cursor.NextApplyHeight, taskKey, verifyRound)
	if err := k.RoundEconomicEffectApplyIndex.Remove(ctx, oldIndex); err != nil {
		return visited, false, err
	}
	if cursor.NextEffectIndex < round.RoundEffectCount {
		nextHeight, overflow := checkedHeightAdd(currentHeight, 1)
		if overflow {
			return visited, false, errorsmod.Wrap(types.ErrInvariantBroken, "round effect cursor height overflows")
		}
		cursor.NextApplyHeight = nextHeight
		if err := k.RoundEconomicEffectApplyCursor.Set(ctx, key, cursor); err != nil {
			return visited, false, err
		}
		if err := k.RoundEconomicEffectApplyIndex.Set(ctx, types.NewVerifyRoundIndexKey(nextHeight, taskKey, verifyRound)); err != nil {
			return visited, false, err
		}
		return visited, false, nil
	}
	if err := k.finishRoundEconomicEffects(ctx, taskKey, round, funding, cursor, currentHeight); err != nil {
		return visited, false, err
	}
	return visited, true, nil
}

func (k Keeper) processRoundEconomicEffectApplyIndexWithBudget(
	ctx context.Context,
	currentHeight, maxRounds, maxBytes uint64,
) (deadlineSweepUsage, error) {
	return k.sweepExpiredVerifyRoundIndexWithBudget(
		ctx, k.RoundEconomicEffectApplyIndex, currentHeight, maxRounds, maxBytes,
		func(ctx context.Context, taskID types.TaskKey, verifyRound uint32, deadline uint64) (deadlineSweepOutcome, uint64, error) {
			cursor, err := k.RoundEconomicEffectApplyCursor.Get(ctx, types.NewVerifyRoundKey(taskID, verifyRound))
			if err != nil {
				if errIsNotFound(err) {
					return deadlineSweepStale, 0, nil
				}
				return deadlineSweepPending, 0, err
			}
			rowBytes := uint64(cursor.Size())
			if cursor.NextApplyHeight != deadline {
				return deadlineSweepPending, rowBytes, errorsmod.Wrap(types.ErrInvariantBroken, "round effect index disagrees with cursor")
			}
			params, err := k.Params.Get(ctx)
			if err != nil {
				return deadlineSweepPending, rowBytes, err
			}
			visited, _, err := k.applyRoundEconomicEffects(ctx, taskID, verifyRound, currentHeight, params.Challenge.MaxRoundEffectItemsPerBlock)
			if err != nil {
				return deadlineSweepPending, rowBytes, err
			}
			rowBytes += uint64(visited) * taskDeadlineConservativeBytesV1
			return deadlineSweepAdvanced, rowBytes, nil
		},
	)
}

func (k Keeper) applyOneRoundEconomicEffect(
	ctx context.Context,
	taskKey types.TaskKey,
	round types.VerificationRoundState,
	funding *types.RoundFundingState,
	cursor *types.RoundEconomicEffectApplyCursorState,
	effect *shared.RoundEconomicEffectV1,
	height uint64,
) error {
	requested, err := shared.ParseAmount(effect.RequestedAmount)
	if err != nil {
		return err
	}
	setApplied := func(applied, unfilled uint64) {
		effect.AppliedAmount = shared.NewAmount(applied)
		effect.UnfilledAmount = shared.NewAmount(unfilled)
		effect.Status = shared.RoundEconomicEffectStatusV1_ROUND_ECONOMIC_EFFECT_STATUS_V1_APPLIED
	}
	switch effect.EffectKind {
	case shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_VERIFIER_FEE:
		if err := k.sendChallengeFundsToAccount(ctx, effect.GetDestinationAddress(), requested); err != nil {
			return err
		}
		remaining := mustAmountValue(funding.VerifierBudgetRemaining)
		paid := mustAmountValue(funding.VerifierPaid)
		if requested > remaining {
			return errorsmod.Wrap(types.ErrInvariantBroken, "verifier fee exceeds remaining challenge budget")
		}
		paid, overflow := checkedHeightAdd(paid, requested)
		if overflow {
			return errorsmod.Wrap(types.ErrInvariantBroken, "verifier paid amount overflows")
		}
		funding.VerifierBudgetRemaining = shared.NewAmount(remaining - requested)
		funding.VerifierPaid = shared.NewAmount(paid)
		setApplied(requested, 0)
	case shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_EARNING_DISQUALIFICATION:
		if effect.GetXSourceAddress() == nil || requested != 0 {
			return errorsmod.Wrap(types.ErrInvariantBroken, "earning disqualification is malformed")
		}
		setApplied(0, 0)
	case shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_SERVICE_SLASH,
		shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_OPENER_COST_RECOVERY,
		shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_TREASURY_RESIDUAL:
		duty := shared.Duty_DUTY_UNSPECIFIED
		if effect.EffectKind == shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_SERVICE_SLASH {
			duty, err = k.roundEffectSourceDuty(ctx, taskKey, effect.GetSourceAddress())
			if err != nil {
				return err
			}
		}
		core, err := k.TaskCore.Get(ctx, taskKey)
		if err != nil {
			return err
		}
		var evidenceDigest []byte
		if effect.EffectKind == shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_SERVICE_SLASH {
			classification, err := k.TaskFailureClass.Get(ctx, types.NewVerifyRoundKey(taskKey, round.VerifyRound))
			if err != nil || len(classification.EvidenceDigest) != types.Hash32Len {
				return errorsmod.Wrap(types.ErrInvariantBroken, "round divergence classification is unavailable")
			}
			evidenceDigest = classification.EvidenceDigest
		}
		receipt, err := k.hubKeeper.ApplyRoundEconomicEffect(ctx, hubtypes.RoundEconomicEffectApplyRequest{
			SessionID: core.SessionId, TaskID: taskKey, RoundID: round.RoundId, VerifyRound: round.VerifyRound, Duty: duty,
			Effect: *effect, EffectPoolBalance: cursor.EffectPoolBalance,
			EvidenceDigest: evidenceDigest, Height: height,
		})
		if err != nil {
			return err
		}
		if receipt.EffectIndex != effect.EffectIndex || receipt.EffectKind != effect.EffectKind {
			return errorsmod.Wrap(types.ErrInvariantBroken, "Hub round effect receipt scope mismatch")
		}
		effect.AppliedAmount, effect.UnfilledAmount = receipt.AppliedAmount, receipt.UnfilledAmount
		effect.Status = shared.RoundEconomicEffectStatusV1_ROUND_ECONOMIC_EFFECT_STATUS_V1_APPLIED
		cursor.EffectPoolBalance = receipt.EffectPoolBalance
		if effect.EffectKind == shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_OPENER_COST_RECOVERY {
			funding.OpenerRecovery = receipt.AppliedAmount
		}
		if effect.EffectKind == shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_TREASURY_RESIDUAL {
			funding.TreasuryResidual = receipt.AppliedAmount
		}
	case shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_BOND_REFUND:
		if requested > mustAmountValue(funding.BondLockedAmount) {
			return errorsmod.Wrap(types.ErrInvariantBroken, "bond refund exceeds locked amount")
		}
		if err := k.sendChallengeFundsToAccount(ctx, effect.GetDestinationAddress(), requested); err != nil {
			return err
		}
		funding.BondLockedAmount = shared.NewAmount(mustAmountValue(funding.BondLockedAmount) - requested)
		funding.BondRefund = shared.NewAmount(mustAmountValue(funding.BondRefund) + requested)
		setApplied(requested, 0)
	case shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_BOND_SLASH:
		if requested > mustAmountValue(funding.BondLockedAmount) {
			return errorsmod.Wrap(types.ErrInvariantBroken, "bond slash exceeds locked amount")
		}
		pool := mustAmountValue(cursor.EffectPoolBalance)
		pool, overflow := checkedHeightAdd(pool, requested)
		if overflow {
			return errorsmod.Wrap(types.ErrInvariantBroken, "effect pool balance overflows")
		}
		cursor.EffectPoolBalance = shared.NewAmount(pool)
		funding.BondLockedAmount = shared.NewAmount(mustAmountValue(funding.BondLockedAmount) - requested)
		funding.BondSlash = shared.NewAmount(mustAmountValue(funding.BondSlash) + requested)
		setApplied(requested, 0)
	default:
		return errorsmod.Wrap(types.ErrInvariantBroken, "unknown round effect kind")
	}
	return nil
}

func (k Keeper) finishRoundEconomicEffects(
	ctx context.Context,
	taskKey types.TaskKey,
	round types.VerificationRoundState,
	funding types.RoundFundingState,
	cursor types.RoundEconomicEffectApplyCursorState,
	height uint64,
) error {
	effects := make([]shared.RoundEconomicEffectV1, round.RoundEffectCount)
	for index := uint32(0); index < round.RoundEffectCount; index++ {
		row, err := k.RoundEconomicEffect.Get(ctx, types.NewRoundEconomicEffectKey(taskKey, round.VerifyRound, index))
		if err != nil || row.Effect.Status != shared.RoundEconomicEffectStatusV1_ROUND_ECONOMIC_EFFECT_STATUS_V1_APPLIED {
			return errorsmod.Wrap(types.ErrInvariantBroken, "round effect is not fully applied")
		}
		effects[index] = row.Effect
	}
	remaining := mustAmountValue(funding.VerifierBudgetRemaining)
	if remaining != 0 {
		if err := k.sendChallengeFundsToAccount(ctx, funding.OpenerAddress, remaining); err != nil {
			return err
		}
		funding.VerifierBudgetRemaining = shared.NewAmount(0)
	}
	if mustAmountValue(funding.BondLockedAmount) != 0 || mustAmountValue(cursor.EffectPoolBalance) != 0 {
		return errorsmod.Wrap(types.ErrInvariantBroken, "challenge funding was not fully resolved")
	}
	funding.XRoundOutcome = &types.RoundFundingState_RoundOutcome{RoundOutcome: round.GetRoundOutcome()}
	funding.XClosedHeight = &types.RoundFundingState_ClosedHeight{ClosedHeight: round.GetClosedHeight()}
	resolution, err := types.RoundFundingResolutionHash(sdk.UnwrapSDKContext(ctx).ChainID(), funding)
	if err != nil {
		return err
	}
	funding.XFundingResolutionHash = &types.RoundFundingState_FundingResolutionHash{FundingResolutionHash: resolution[:]}
	root, err := types.RoundEffectRoot(sdk.UnwrapSDKContext(ctx).ChainID(), taskKey, round.VerifyRound, round.GetRoundEffectPlanRoot(), effects)
	if err != nil {
		return err
	}
	round.XRoundEffectRoot = &types.VerificationRoundState_RoundEffectRoot{RoundEffectRoot: root[:]}
	round.XFundingResolutionHash = &types.VerificationRoundState_FundingResolutionHash{FundingResolutionHash: resolution[:]}
	key := types.NewVerifyRoundKey(taskKey, round.VerifyRound)
	if err := k.WriteRoundFunding(ctx, key, funding); err != nil {
		return err
	}
	if err := k.WriteVerificationRound(ctx, key, round); err != nil {
		return err
	}
	summary, err := k.TaskRoundSummary.Get(ctx, taskKey)
	if err != nil {
		return err
	}
	summary.Round2EffectRootOrZero32 = root[:]
	if err := k.TaskRoundSummary.Set(ctx, taskKey, summary); err != nil {
		return err
	}
	if err := k.RoundEconomicEffectApplyCursor.Remove(ctx, key); err != nil {
		return err
	}
	if err := emitTypedEvent(ctx, &types.EventVerificationRoundClosed{
		TaskId: taskKey, VerifyRound: round.VerifyRound,
		XRoundOutcome: &types.EventVerificationRoundClosed_RoundOutcome{RoundOutcome: round.GetRoundOutcome()},
		Verdict:       round.GetVerdict(), RoundFactsHash: round.GetRoundFactsHash(), RoundEffectRoot: root[:], ClosedHeight: round.GetClosedHeight(),
	}); err != nil {
		return err
	}
	return k.finalizeTaskRounds(ctx, taskKey)
}

func (k Keeper) roundEffectSourceDuty(ctx context.Context, taskKey types.TaskKey, operator string) (shared.Duty, error) {
	assignment, err := k.ReadTaskAssignment(ctx, taskKey)
	if err == nil && assignment.WinnerWorker == operator {
		return shared.DutyWorker, nil
	}
	return shared.DutyVerifier, nil
}

func (k Keeper) sendChallengeFundsToAccount(ctx context.Context, address string, amount uint64) error {
	if amount == 0 {
		return nil
	}
	raw, _, err := k.canonicalAddress("challenge beneficiary", address)
	if err != nil {
		return err
	}
	denom := k.hubKeeper.GetHubParams(sdk.UnwrapSDKContext(ctx)).BusinessDenom
	if denom == "" {
		return errorsmod.Wrap(types.ErrInvariantBroken, "business denomination is unavailable")
	}
	coins := sdk.NewCoins(sdk.NewCoin(denom, sdkmath.NewIntFromUint64(amount)))
	return k.bankKeeper.SendCoinsFromModuleToAccount(ctx, shared.TaskChallengeEffectModuleName, sdk.AccAddress(raw), coins)
}
