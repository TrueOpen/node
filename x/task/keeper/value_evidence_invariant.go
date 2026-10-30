package keeper

import (
	"bytes"
	"context"
	"fmt"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// EnsureValueEvidenceInvariant checks receipt authority and the bidirectional
// task-window index. Import and crisis checks share this validation.
func (k Keeper) EnsureValueEvidenceInvariant(ctx context.Context) error {
	if err := k.ensureValueEvidenceWindows(ctx); err != nil {
		return err
	}
	return k.ensureValueEvidenceReceipts(ctx)
}

func (k Keeper) ensureValueEvidenceWindows(ctx context.Context) error {
	summaries, err := collectMapValues(ctx, k.TaskRoundSummary)
	if err != nil {
		return err
	}
	expected := make(map[string]bool)
	for _, summary := range summaries {
		var revealMax uint64
		for _, round := range []uint32{types.VerifyRoundV1, types.ChallengeVerifyRoundV1} {
			assignment, err := k.ReadVerifierAssignment(ctx, types.NewVerifyRoundKey(summary.TaskId, round))
			if errIsNotFound(err) {
				continue
			}
			if err != nil {
				return err
			}
			revealMax = max(revealMax, assignment.RevealDeadlineHeight)
		}
		if revealMax == 0 {
			if summary.XVerifierValueEvidenceCloseHeight != nil {
				return fmt.Errorf("value window exists without a revealed round")
			}
			continue
		}
		params, err := k.ParamsForTask(ctx, summary.TaskId)
		if err != nil {
			return err
		}
		closeHeight, overflow := checkedHeightAdd(revealMax, params.Evidence.VerifierValueEvidenceWindowBlocks)
		if overflow || summary.GetVerifierValueEvidenceCloseHeight() != closeHeight {
			return fmt.Errorf("value window differs from its frozen reveal deadline")
		}
		if summary.XVerifierValueEvidenceWindowClosedHeight != nil {
			if summary.GetVerifierValueEvidenceWindowClosedHeight() < closeHeight {
				return fmt.Errorf("value window was closed prematurely")
			}
		} else {
			key := types.NewDeadlineIndexKey(closeHeight, summary.TaskId)
			encoded, err := indexKeyBytes(k.ValueEvidenceWindowCloseIndex.KeyCodec(), key)
			if err != nil {
				return err
			}
			expected[encoded] = true
			if summary.XRoundsClosedHeight != nil {
				return fmt.Errorf("task finalized before value evidence closed")
			}
		}
	}
	return checkDerivedKeySet(ctx, k.ValueEvidenceWindowCloseIndex, expected)
}

func (k Keeper) ensureValueEvidenceReceipts(ctx context.Context) error {
	iter, err := k.ValueEvidenceReceipt.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer iter.Close()
	targetCounts := make(map[string]uint32)
	targetFaults := make(map[string]string)
	for ; iter.Valid(); iter.Next() {
		entry, err := iter.KeyValue()
		if err != nil {
			return err
		}
		row, err := k.ProjectValueEvidenceReceiptStore(entry.Value)
		if err != nil {
			return err
		}
		if !bytes.Equal(entry.Key.K1(), row.TaskId) || entry.Key.K2() != row.VerifyRound || !bytes.Equal(entry.Key.K3(), entry.Value.TargetOperatorAddress) || entry.Key.K4() != row.Position {
			return fmt.Errorf("value receipt key mismatch")
		}
		core, err := k.TaskCore.Get(ctx, row.TaskId)
		if err != nil {
			return err
		}
		infer, err := k.ReadInferReceipt(ctx, row.TaskId)
		if err != nil {
			return err
		}
		if row.Position >= infer.GeneratedTokenCount {
			return fmt.Errorf("value receipt position is outside accepted output")
		}
		summary, err := k.TaskRoundSummary.Get(ctx, row.TaskId)
		if err != nil {
			return err
		}
		if row.AcceptedHeight > summary.GetVerifierValueEvidenceCloseHeight() || row.AcceptedHeight < core.CreatedHeight {
			return fmt.Errorf("value receipt acceptance height is outside its task window")
		}
		assignment, err := k.ReadVerifierAssignment(ctx, types.NewVerifyRoundKey(row.TaskId, row.VerifyRound))
		if err != nil {
			return err
		}
		if assignment.RevealDeadlineHeight == 0 {
			return fmt.Errorf("value receipt round never entered revealing")
		}
		if row.FaultedDuty == shared.DutyWorker {
			if row.TargetOperatorAddress != infer.WinnerWorker {
				return fmt.Errorf("value receipt Worker differs from winner")
			}
		} else {
			key, err := types.DeriveCommitKey(sdkChainID(ctx), row.TaskId, row.VerifyRound, row.TargetOperatorAddress)
			if err != nil {
				return err
			}
			result, err := k.ReadResultReceipt(ctx, key[:])
			if err != nil {
				return err
			}
			if result.AcceptedHeight > row.AcceptedHeight {
				return fmt.Errorf("value receipt predates its accepted reveal")
			}
		}
		prefix := string(row.TaskId) + string(shared.Uint32BE(row.VerifyRound)) + row.TargetOperatorAddress
		targetCounts[prefix]++
		params, err := k.ParamsForTask(ctx, row.TaskId)
		if err != nil {
			return err
		}
		if targetCounts[prefix] > params.Evidence.MaxVerifierValueEvidencePositions {
			return fmt.Errorf("value receipt prefix exceeds cumulative cap")
		}
		if id, exists := targetFaults[prefix]; exists && id != string(row.FaultId) {
			return fmt.Errorf("value receipts disagree on their first fault")
		}
		targetFaults[prefix] = string(row.FaultId)
	}
	return nil
}
