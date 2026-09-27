package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"cosmossdk.io/collections"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

var _ hubtypes.RoleFaultConsumerGate = Keeper{}

// RoleFaultConsumersClosed is the Task-owned retention gate used before Hub
// prunes a RoleFault. It accepts only a final Task whose round/effect consumers
// are closed and whose retained classification contains the exact evidence
// digest carried by the Hub fault.
func (k Keeper) RoleFaultConsumersClosed(ctx context.Context, taskID, evidenceDigest []byte) (bool, error) {
	taskKey, err := taskStoreKey(taskID)
	if err != nil {
		return false, err
	}
	if len(evidenceDigest) != types.Hash32Len {
		return false, fmt.Errorf("role fault evidence_digest must be Hash32")
	}

	final, err := k.taskAuthorityIsFinal(ctx, taskKey)
	if err != nil || !final {
		return false, err
	}
	matched, err := k.hasFinalFailureEvidence(ctx, taskKey, evidenceDigest)
	if err != nil || !matched {
		return false, err
	}

	if summary, err := k.TaskRoundSummary.Get(ctx, taskKey); err == nil {
		if summary.OpenRoundCount != 0 || summary.XRoundsClosedHeight == nil ||
			summary.XSettlementFactsCutoffHeight == nil {
			return false, nil
		}
	} else if !errors.Is(err, collections.ErrNotFound) {
		return false, err
	}
	if has, err := k.RoundEconomicEffectApplyCursor.Has(ctx, types.NewVerifyRoundKey(taskKey, types.ChallengeVerifyRoundV1)); err != nil {
		return false, err
	} else if has {
		return false, nil
	}
	iter, err := k.RoundEconomicEffect.Iterate(ctx,
		collections.NewPrefixedTripleRange[types.Hash32Key, uint32, uint32](taskKey))
	if err != nil {
		return false, err
	}
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		state, err := iter.Value()
		if err != nil {
			return false, err
		}
		if state.Effect.Status != shared.RoundEconomicEffectStatusV1_ROUND_ECONOMIC_EFFECT_STATUS_V1_APPLIED {
			return false, nil
		}
	}
	return true, nil
}

func (k Keeper) taskAuthorityIsFinal(ctx context.Context, taskKey types.TaskKey) (bool, error) {
	core, err := k.TaskCore.Get(ctx, taskKey)
	if err == nil {
		if core.FinalityStatus != shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL ||
			core.XTaskFinalityHeight == nil {
			return false, nil
		}
		settlement, err := k.ReadTaskSettlement(ctx, taskKey)
		if errors.Is(err, collections.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !bytes.Equal(settlement.TaskId, core.TaskId) ||
			settlement.TaskFinalityHeight != core.GetTaskFinalityHeight() {
			return false, fmt.Errorf("final task settlement authority is inconsistent")
		}
		return true, nil
	}
	if !errors.Is(err, collections.ErrNotFound) {
		return false, err
	}
	terminal, err := k.ReadTaskTerminalSummary(ctx, taskKey)
	if errors.Is(err, collections.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if terminal.FinalityStatus != shared.TaskFinalityStatusV1_TASK_FINALITY_STATUS_V1_FINAL ||
		terminal.TaskFinalityHeight == 0 {
		return false, fmt.Errorf("terminal task summary is not final")
	}
	return true, nil
}

func (k Keeper) hasFinalFailureEvidence(ctx context.Context, taskKey types.TaskKey, digest []byte) (bool, error) {
	var matched bool
	for _, round := range []uint32{types.VerifyRoundV1, types.ChallengeVerifyRoundV1} {
		state, err := k.TaskFailureClass.Get(ctx, types.NewVerifyRoundKey(taskKey, round))
		if errors.Is(err, collections.ErrNotFound) {
			continue
		}
		if err != nil {
			return false, err
		}
		if !bytes.Equal(state.TaskId, taskKey) || state.VerifyRound != round {
			return false, fmt.Errorf("task failure classification key mismatch")
		}
		if bytes.Equal(state.EvidenceDigest, digest) {
			if state.SupersededByVerifyRound != 0 || state.XTaskFinalityHeight == nil {
				return false, nil
			}
			if matched {
				return false, fmt.Errorf("role fault evidence matches multiple task classifications")
			}
			matched = true
		}
	}
	return matched, nil
}
