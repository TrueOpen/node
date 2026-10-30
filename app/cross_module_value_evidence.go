package app

import (
	"bytes"
	"context"
	"fmt"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

// Every retained receipt must name the same objective fault. A later position
// reuses the first fault ID while retaining its own evidence digest, so at
// least one receipt (not necessarily the smallest position) must bind the
// fault's original digest and height.
func (app *App) ensureValueEvidenceFaultReferences(ctx context.Context) error {
	iter, err := app.TaskKeeper.ValueEvidenceReceipt.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer iter.Close()
	matched := map[string]bool{}
	for ; iter.Valid(); iter.Next() {
		stored, err := iter.Value()
		if err != nil {
			return err
		}
		row, err := app.TaskKeeper.ProjectValueEvidenceReceiptStore(stored)
		if err != nil {
			return err
		}
		fault, err := app.HubKeeper.ReadRoleFaultValue(ctx, row.FaultId)
		if err != nil {
			return fmt.Errorf("value receipt has no Hub fault: %w", err)
		}
		if !bytes.Equal(fault.TaskId, row.TaskId) || fault.OperatorAddress != row.TargetOperatorAddress || fault.Duty != row.FaultedDuty || !hubtypes.IsMergedValueEvidenceFault(fault) || fault.RecordedHeight > row.AcceptedHeight {
			return fmt.Errorf("value receipt disagrees with Hub fault attribution")
		}
		id := string(row.FaultId)
		cleaning, err := app.TaskKeeper.TaskCleanupCursor.Has(ctx, row.TaskId)
		if err != nil {
			return err
		}
		matched[id] = matched[id] || cleaning || !hubtypes.IsValueEvidenceFault(fault) || bytes.Equal(fault.EvidenceDigest, row.EvidenceDigest) && fault.RecordedHeight == row.AcceptedHeight
	}
	for _, found := range matched {
		if !found {
			return fmt.Errorf("value fault has no matching first receipt")
		}
	}
	faults, err := app.HubKeeper.RoleFault.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer faults.Close()
	for ; faults.Valid(); faults.Next() {
		stored, err := faults.Value()
		if err != nil {
			return err
		}
		fault, err := app.HubKeeper.ProjectRoleFaultStore(stored)
		if err != nil {
			return err
		}
		if !hubtypes.IsValueEvidenceFault(fault) || matched[string(fault.FaultId)] {
			continue
		}
		live, err := app.TaskKeeper.TaskCore.Has(ctx, fault.TaskId)
		if err != nil {
			return err
		}
		cleaning, err := app.TaskKeeper.TaskCleanupCursor.Has(ctx, fault.TaskId)
		if err != nil {
			return err
		}
		if live && !cleaning {
			return fmt.Errorf("live value fault has no matching receipt")
		}
	}
	return nil
}

// Challenge slash custody and the Task effect cursor are independently stored.
// Check both directions while details are live. Cleanup removes Hub receipts
// before the Task effects, so the cursor explicitly authorizes that interval.
func (app *App) ensureRoundSlashReferences(ctx context.Context) error {
	effects, err := app.TaskKeeper.RoundEconomicEffect.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer effects.Close()
	expected := map[string]shared.RoundEconomicEffectV1{}
	for ; effects.Valid(); effects.Next() {
		state, err := effects.Value()
		if err != nil {
			return err
		}
		effect := state.Effect
		if effect.EffectKind != shared.RoundEconomicEffectKindV1_ROUND_ECONOMIC_EFFECT_KIND_V1_SERVICE_SLASH || effect.Status != shared.RoundEconomicEffectStatusV1_ROUND_ECONOMIC_EFFECT_STATUS_V1_APPLIED {
			continue
		}
		requested, err := shared.ParseAmount(effect.RequestedAmount)
		if err != nil {
			return err
		}
		if requested == 0 {
			continue
		}
		cleaning, err := app.TaskKeeper.TaskCleanupCursor.Has(ctx, state.TaskId)
		if err != nil {
			return err
		}
		if cleaning {
			continue
		}
		round, err := app.TaskKeeper.ReadVerificationRound(ctx, tasktypes.NewVerifyRoundKey(state.TaskId, state.VerifyRound))
		if err != nil {
			return err
		}
		key := hubtypes.NewSlashSummaryKey(hubtypes.SlashSourceKind_SLASH_SOURCE_KIND_CHALLENGE_EFFECT, round.RoundId, effect.EffectIndex)
		row, err := app.HubKeeper.ReadSlashSummaryValue(ctx, key)
		if err != nil {
			return fmt.Errorf("applied round effect has no Hub slash summary: %w", err)
		}
		if !bytes.Equal(row.GetTaskId(), state.TaskId) || row.OperatorAddress != effect.GetSourceAddress() || row.RequestedAmount != effect.RequestedAmount || row.AppliedAmount != effect.AppliedAmount || row.UnfilledAmount != effect.UnfilledAmount || row.Destination != hubtypes.SlashDestination_SLASH_DESTINATION_CHALLENGE_EFFECT_POOL {
			return fmt.Errorf("round effect disagrees with Hub slash custody")
		}
		expected[string(round.RoundId)+string(shared.Uint32BE(effect.EffectIndex))] = effect
	}
	summaries, err := app.HubKeeper.SlashSummary.Iterate(ctx, nil)
	if err != nil {
		return err
	}
	defer summaries.Close()
	for ; summaries.Valid(); summaries.Next() {
		stored, err := summaries.Value()
		if err != nil {
			return err
		}
		row, err := app.HubKeeper.ProjectSlashSummaryStore(stored)
		if err != nil {
			return err
		}
		if row.SourceKind != hubtypes.SlashSourceKind_SLASH_SOURCE_KIND_CHALLENGE_EFFECT {
			continue
		}
		cleaning, err := app.TaskKeeper.TaskCleanupCursor.Has(ctx, row.GetTaskId())
		if err != nil {
			return err
		}
		if cleaning {
			continue
		}
		if _, exists := expected[string(row.SourceId)+string(shared.Uint32BE(row.EffectIndex))]; !exists {
			return fmt.Errorf("Hub challenge slash has no applied Task effect")
		}
	}
	return nil
}
