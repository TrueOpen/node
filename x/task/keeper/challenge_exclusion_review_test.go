package keeper

import (
	"bytes"
	"testing"

	"github.com/TrueOpen/node/x/task/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func TestChallengeSourceAlreadyExcludesAllPriorSelectedVerifiers(t *testing.T) {
	f := newVerificationFixture(t)
	var candidates []verifierWindowCandidate
	for i, previous := range f.assignment.SelectedVerifiers {
		candidates = append(candidates, verifierWindowCandidate{Slot: uint32(i), SlotVersion: 1, OperatorAddress: previous.OperatorAddress})
	}
	for i := 3; i < 6; i++ {
		candidates = append(candidates, verifierWindowCandidate{Slot: uint32(i), SlotVersion: 1, OperatorAddress: sdk.AccAddress(bytes.Repeat([]byte{byte(i + 0x91)}, 20)).String()})
	}
	filtered := excludePriorRoundVerifiers(candidates, f.assignment)
	require.Len(t, filtered, 3)
	window := types.VerifierCandidateWindowState{SchemaVersion: 1, TaskId: f.taskID, VerifyRound: 2, InferReceiptHash: bytes32(0x31), CandidatePoolSnapshotId: bytes32(0x32), CandidatePoolHash: bytes32(0x33), EligibilityFrozenHeight: 10, WindowSize: 3}
	frozen, segments, err := freezeVerifierEligibilitySource(sdkChainID(f.ctx), window, 8, 1, filtered)
	require.NoError(t, err)
	require.Equal(t, uint32(3), frozen.EligibleCount)
	for slot := uint32(0); slot < 6; slot++ {
		segment, offset, mask, err := bitmapLocation(slot, 8, 1)
		require.NoError(t, err)
		require.Equal(t, slot >= 3, segments[segment].Bitmap[offset]&mask != 0)
	}
	params := types.DefaultTaskParams()
	params.Challenge.MaxVerifyRound = 3
	require.Error(t, params.Validate(), "V1 has only round 1 and its disjoint round 2")
}
