package keeper

import (
	"context"
	"fmt"

	"github.com/TrueOpen/node/x/task/types"
)

func (k Keeper) extendValueEvidenceWindow(ctx context.Context, taskID types.TaskKey, revealDeadline uint64) error {
	params, err := k.ParamsForTask(ctx, taskID)
	if err != nil {
		return err
	}
	closeHeight, overflow := checkedHeightAdd(revealDeadline, params.Evidence.VerifierValueEvidenceWindowBlocks)
	if overflow {
		return fmt.Errorf("value evidence deadline overflows")
	}
	summary, err := k.TaskRoundSummary.Get(ctx, taskID)
	if errIsNotFound(err) {
		summary = types.TaskRoundSummaryState{TaskId: taskID, Round1FactsHashOrZero32: make([]byte, 32), Round2FactsHashOrZero32: make([]byte, 32), Round2EffectRootOrZero32: make([]byte, 32)}
	} else if err != nil {
		return err
	}
	if summary.XRoundsClosedHeight != nil {
		return fmt.Errorf("cannot extend a finalized task evidence window")
	}
	if closeHeight <= summary.GetVerifierValueEvidenceCloseHeight() {
		return nil
	}
	if old := summary.GetVerifierValueEvidenceCloseHeight(); old != 0 {
		if err = k.ValueEvidenceWindowCloseIndex.Remove(ctx, types.NewDeadlineIndexKey(old, taskID)); err != nil {
			return err
		}
	}
	summary.XVerifierValueEvidenceCloseHeight = &types.TaskRoundSummaryState_VerifierValueEvidenceCloseHeight{VerifierValueEvidenceCloseHeight: closeHeight}
	summary.XVerifierValueEvidenceWindowClosedHeight = nil
	if err = k.TaskRoundSummary.Set(ctx, taskID, summary); err != nil {
		return err
	}
	return addDeadlineIndex(ctx, k.ValueEvidenceWindowCloseIndex, taskID, closeHeight)
}

func (k Keeper) closeValueEvidenceWindow(ctx context.Context, taskID types.TaskKey, deadline, height uint64) error {
	summary, err := k.TaskRoundSummary.Get(ctx, taskID)
	if err != nil {
		return err
	}
	if deadline != summary.GetVerifierValueEvidenceCloseHeight() || deadline == 0 || height < deadline {
		return fmt.Errorf("value evidence close index disagrees with its summary")
	}
	if summary.XVerifierValueEvidenceWindowClosedHeight == nil {
		summary.XVerifierValueEvidenceWindowClosedHeight = &types.TaskRoundSummaryState_VerifierValueEvidenceWindowClosedHeight{VerifierValueEvidenceWindowClosedHeight: height}
		if err = k.TaskRoundSummary.Set(ctx, taskID, summary); err != nil {
			return err
		}
	}
	if err = k.ValueEvidenceWindowCloseIndex.Remove(ctx, types.NewDeadlineIndexKey(deadline, taskID)); err != nil {
		return err
	}
	if summary.MaxClosedRound == types.ChallengeVerifyRoundV1 && summary.OpenRoundCount == 0 {
		round, err := k.ReadVerificationRound(ctx, types.NewVerifyRoundKey(taskID, types.ChallengeVerifyRoundV1))
		if err != nil {
			return err
		}
		if round.XRoundEffectPlanRoot == nil {
			_, result, err := k.rebuildClosedRound(ctx, taskID, round.VerifyRound, round.GetClosedHeight(), round)
			if err != nil {
				return err
			}
			return k.freezeRoundEconomicEffects(ctx, taskID, round, result, round.GetRoundOutcome(), height)
		}
	}
	if summary.OpenRoundCount != 0 {
		return nil
	}
	return k.finalizeTaskRounds(ctx, taskID)
}

func (k Keeper) processValueEvidenceWindowCloses(ctx context.Context, height, maxItems uint64) (uint64, error) {
	return k.sweepExpiredTaskDeadlines(ctx, k.ValueEvidenceWindowCloseIndex, height, maxItems, func(ctx context.Context, taskID types.TaskKey, deadline uint64) (deadlineSweepOutcome, uint64, error) {
		summary, err := k.TaskRoundSummary.Get(ctx, taskID)
		if errIsNotFound(err) {
			return deadlineSweepStale, 0, nil
		}
		if err != nil {
			return deadlineSweepPending, 0, err
		}
		if err = k.closeValueEvidenceWindow(ctx, taskID, deadline, height); err != nil {
			return deadlineSweepPending, uint64(summary.Size()), err
		}
		return deadlineSweepAdvanced, uint64(summary.Size()), nil
	})
}
