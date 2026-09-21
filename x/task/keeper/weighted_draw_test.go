package keeper

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/task/types"
)

func TestWeightedDrawIsIndependentOfInputOrder(t *testing.T) {
	taskID := bytes.Repeat([]byte{1}, 32)
	legalSet := bytes.Repeat([]byte{2}, 32)
	beacon := bytes.Repeat([]byte{3}, 32)
	candidates := []types.TaskCandidateFactState{
		{Slot: 9, SlotVersion: 1, OperatorAddress: "trueopen1c5l3xut9207qyhajegcep54t0k50n4zhawaz04", CandidateWeight: 1},
		{Slot: 2, SlotVersion: 1, OperatorAddress: "trueopen1dyyce5xm8rdlgghm3v2f8asnl8g3xtl4hwnlcx", CandidateWeight: 3},
	}
	forward, err := drawWeightedCandidate("trueopen-test", workerAssignmentPurposeV1, taskID, legalSet, 11, beacon, 0, 64, candidates)
	require.NoError(t, err)
	reversed, err := drawWeightedCandidate("trueopen-test", workerAssignmentPurposeV1, taskID, legalSet, 11, beacon, 0, 64, []types.TaskCandidateFactState{candidates[1], candidates[0]})
	require.NoError(t, err)
	require.Equal(t, forward.Fact.Slot, reversed.Fact.Slot)
	require.Equal(t, forward.AcceptedCounter, reversed.AcceptedCounter)
}

func TestWeightedDrawRejectsZeroWeightAndAttemptLimit(t *testing.T) {
	scope := bytes.Repeat([]byte{1}, 32)
	_, err := drawWeightedCandidate("trueopen-test", workerAssignmentPurposeV1, scope, scope, 1, scope, 0, 4,
		[]types.TaskCandidateFactState{{Slot: 1}})
	require.ErrorContains(t, err, "zero weight")
	_, err = drawWeightedCandidate("trueopen-test", workerAssignmentPurposeV1, scope, scope, 1, scope, 0, 0,
		[]types.TaskCandidateFactState{{Slot: 1, CandidateWeight: 1}})
	require.ErrorContains(t, err, "positive attempt limit")
}
