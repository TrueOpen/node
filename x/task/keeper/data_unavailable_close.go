package keeper

import (
	"context"
	"fmt"

	"github.com/cosmos/gogoproto/proto"

	"github.com/TrueOpen/node/x/task/types"
)

// persistDataUnavailableAggregates freezes one aggregate row per selected Task
// Builder at commit-deadline close. A later accepted commit invalidates its
// verifier's report by slot without rewriting the retained report history.
func (k Keeper) persistDataUnavailableAggregates(
	ctx context.Context,
	core types.TaskCoreState,
	assignment types.VerifierAssignmentState,
	closeHeight uint64,
) (bool, error) {
	taskKey := types.NewTaskKey(assignment.TaskId)
	selection, err := k.loadActiveTaskBuilderSelection(ctx, taskKey, assignment.TaskId)
	if err != nil {
		return false, fmt.Errorf("active Task Builder selection unavailable at commit deadline")
	}
	faultEligible := make([]bool, len(selection.SelectedTaskBuilders))
	if assignment.VerifyRound == types.VerifyRoundV1 {
		faultEligible, err = k.dataReadyAttesterSet(ctx, taskKey, assignment.TaskId, selection)
		if err != nil {
			return false, err
		}
	}
	input := DataUnavailableAggregateInput{
		ChainID: sdkChainID(ctx), TaskID: append([]byte(nil), assignment.TaskId...), VerifyRound: assignment.VerifyRound,
		CommitDeadlineHeight: assignment.CommitDeadlineHeight, CloseHeight: closeHeight,
		BuilderOperators: append([]string(nil), selection.SelectedTaskBuilders...),
		VerifierSlots:    make([]DataUnavailableVerifierSlot, len(assignment.SelectedVerifiers)),
	}
	for index, selected := range assignment.SelectedVerifiers {
		input.SelectedVerifierOperators = append(input.SelectedVerifierOperators, selected.OperatorAddress)
		report, found, err := k.getDataUnavailableReportIfExists(
			ctx, types.NewVerifyActorKey(taskKey, assignment.VerifyRound, selected.OperatorAddress),
		)
		if err != nil {
			return false, err
		}
		if found {
			slots, err := DecodeDataUnavailableBuilderBitmap(report.UnavailableTaskBuilderBitmap, selection.SelectedTaskBuilderCount)
			if err != nil {
				return false, fmt.Errorf("selected verifier slot %d report bitmap: %w", index, err)
			}
			input.VerifierSlots[index].Report = &DataUnavailableStableReport{
				TaskID: report.TaskId, VerifyRound: report.VerifyRound, VerifierOperatorAddress: report.VerifierOperatorAddress,
				UnavailableBuilderSlots: slots, ReportHeight: report.ReportHeight, ReportDigest: report.ReportDigest,
			}
		}
		commitKey, err := types.DeriveCommitKey(sdkChainID(ctx), assignment.TaskId, assignment.VerifyRound, selected.OperatorAddress)
		if err != nil {
			return false, err
		}
		_, hasCommit, err := k.getCommitStateIfExists(ctx, types.NewCommitKey(commitKey[:]))
		if err != nil {
			return false, err
		}
		input.VerifierSlots[index].HasAcceptedCommit = hasCommit
	}
	facts, err := BuildDataUnavailableAggregateFacts(input)
	if err != nil {
		return false, err
	}
	confirmed := false
	for builderIndex, fact := range facts {
		state := types.BuilderDataUnavailableAggregateState{
			TaskId: append([]byte(nil), assignment.TaskId...), VerifyRound: assignment.VerifyRound,
			BuilderOperatorAddress: fact.BuilderOperatorAddress, ValidReportCount: fact.ValidReportCount,
			RequiredReportCount: fact.RequiredReportCount,
			Status:              types.BuilderDataUnavailableAggregateStatusV1_BUILDER_DATA_UNAVAILABLE_AGGREGATE_STATUS_V1_COLLECTING,
		}
		state.ReportDigestsByVerifierSlot, err = encodeDataUnavailableDigestSlots(fact.ReportDigestsByVerifierSlot)
		if err != nil {
			return false, err
		}
		if fact.ThresholdReached {
			state.AggregateHash = append([]byte(nil), fact.AggregateHash...)
			if faultEligible[builderIndex] {
				confirmed = true
				state.Status = types.BuilderDataUnavailableAggregateStatusV1_BUILDER_DATA_UNAVAILABLE_AGGREGATE_STATUS_V1_CONFIRMED
			}
			// The round closes at its frozen deadline, not at whichever block the
			// sweep happened to reach it in. Every other field here is already a
			// function of frozen state, and this one has to be too: a thin round
			// parks in COMMITTING, and genesis re-arms CommitDeadlineIndex for that
			// status, so this close runs again after an export/import at a later
			// height. Stamping closeHeight there would rebuild a row the replay
			// comparison below no longer recognises, turning a benign re-run into
			// "conflicting ... replay" inside EndBlock. closeHeight stays the
			// liveness input it is: proof the deadline was actually reached.
			state.ThresholdReachedHeight = assignment.CommitDeadlineHeight
		}
		key := types.NewVerifyActorKey(taskKey, assignment.VerifyRound, fact.BuilderOperatorAddress)
		existing, err := k.BuilderDataUnavailableAggregate.Get(ctx, key)
		if err == nil {
			if !proto.Equal(&existing, &state) {
				return false, fmt.Errorf("conflicting Builder data unavailable aggregate replay")
			}
			continue
		}
		if !errIsNotFound(err) {
			return false, err
		}
		if err := k.BuilderDataUnavailableAggregate.Set(ctx, key, state); err != nil {
			return false, err
		}
		if fact.ThresholdReached && faultEligible[builderIndex] {
			if err := emitTypedEvent(ctx, &types.EventBuilderDataUnavailable{
				SessionId: core.SessionId, TaskId: assignment.TaskId, VerifyRound: assignment.VerifyRound,
				Builder: fact.BuilderOperatorAddress, ValidReportCount: fact.ValidReportCount,
				Threshold: fact.RequiredReportCount, AggregateHash: fact.AggregateHash,
			}); err != nil {
				return false, err
			}
		}
	}
	return confirmed, nil
}
