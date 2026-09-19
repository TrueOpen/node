package keeper

import (
	"bytes"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

type assignmentRandomnessHubStub struct {
	internalStubHubKeeper
	beacon hubtypes.BeaconSnapshot
	found  bool
	domain string
}

func (s *assignmentRandomnessHubStub) GetBeaconForDomain(_ sdk.Context, domain string, _ int64) (hubtypes.BeaconSnapshot, bool) {
	s.domain = domain
	return s.beacon, s.found
}

func TestAssignmentRandomnessQueryUsesTheDrawBeaconDomain(t *testing.T) {
	f := initInternalFixture(t)
	taskID := bytes.Repeat([]byte{0x31}, types.Hash32Len)
	stub := &assignmentRandomnessHubStub{
		beacon: hubtypes.BeaconSnapshot{
			Height:      25,
			Randomness:  bytes.Repeat([]byte{0x32}, types.Hash32Len),
			ProofDigest: bytes.Repeat([]byte{0x33}, types.Hash32Len),
		},
		found: true,
	}
	f.keeper.hubKeeper = stub
	require.NoError(t, f.keeper.TaskAssignment.Set(f.ctx, types.NewTaskKey(taskID), types.TaskAssignmentState{
		TaskId: taskID, AssignmentRandomnessHeight: 25,
		AssignmentCandidateSetHash: bytes.Repeat([]byte{0x34}, types.Hash32Len),
	}))

	response, err := (&queryServer{k: f.keeper}).AssignmentRandomness(f.ctx, &types.QueryAssignmentRandomnessRequest{TaskId: taskID})
	require.NoError(t, err)
	require.Equal(t, shared.DomainWeightedDrawV1, stub.domain)
	require.Equal(t, stub.beacon.Randomness, response.Randomness.BeaconRandomness)
	require.NotNil(t, response.Randomness.GetXBeaconProofDigest())
	require.Equal(t, stub.beacon.ProofDigest, response.Randomness.GetBeaconProofDigest())

	stub.found = false
	_, err = (&queryServer{k: f.keeper}).AssignmentRandomness(f.ctx, &types.QueryAssignmentRandomnessRequest{TaskId: taskID})
	require.Equal(t, codes.Internal, status.Code(err))
}

// The placeholder beacon source has no VRF proof, so the optional field has to
// come back absent rather than as a zero-length digest: an empty bytes value on
// the wire would be indistinguishable from a present-but-empty proof.
func TestAssignmentRandomnessQueryProjectsAPlaceholderBeaconWithoutAProofDigest(t *testing.T) {
	f := initInternalFixture(t)
	taskID := bytes.Repeat([]byte{0x35}, types.Hash32Len)
	stub := &assignmentRandomnessHubStub{
		beacon: hubtypes.BeaconSnapshot{
			Height:     25,
			Randomness: bytes.Repeat([]byte{0x36}, types.Hash32Len),
			SourceTag:  hubtypes.BeaconSourcePlaceholderBlockHashV1,
		},
		found: true,
	}
	f.keeper.hubKeeper = stub
	require.NoError(t, f.keeper.TaskAssignment.Set(f.ctx, types.NewTaskKey(taskID), types.TaskAssignmentState{
		TaskId: taskID, AssignmentRandomnessHeight: 25,
		AssignmentCandidateSetHash: bytes.Repeat([]byte{0x37}, types.Hash32Len),
	}))

	response, err := (&queryServer{k: f.keeper}).AssignmentRandomness(f.ctx, &types.QueryAssignmentRandomnessRequest{TaskId: taskID})
	require.NoError(t, err)
	require.Equal(t, stub.beacon.Randomness, response.Randomness.BeaconRandomness)
	require.Nil(t, response.Randomness.GetXBeaconProofDigest())
	require.Nil(t, response.Randomness.GetBeaconProofDigest())

	// A non-placeholder source that still has no digest is a broken snapshot, not
	// an absent optional.
	stub.beacon.SourceTag = hubtypes.BeaconSourceProposerVRFV1
	_, err = (&queryServer{k: f.keeper}).AssignmentRandomness(f.ctx, &types.QueryAssignmentRandomnessRequest{TaskId: taskID})
	require.Equal(t, codes.Internal, status.Code(err))
}
