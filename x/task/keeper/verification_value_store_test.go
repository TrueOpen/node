package keeper

import (
	"testing"

	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/task/types"
)

func TestAcceptedValueCommitmentsSurvivePrivateStorage(t *testing.T) {
	f, _, _ := valueEvidenceFixture(t, false)
	key, err := types.DeriveCommitKey(sdkChainID(f.ctx), f.taskID, 1, f.operators[0])
	require.NoError(t, err)
	commit := types.CommitState{CommitKey: key[:], TaskId: f.taskID, VerifyRound: 1, VerifierOperatorAddress: f.operators[0], CommitHash: bytes32(0x91), CommitSigningDigest: bytes32(0x92), SignatureDigest: bytes32(0x93), CommitHeight: 8, Status: types.CommitStatusV1_COMMIT_STATUS_V1_ACCEPTED, XVerifierValueRoot: &types.CommitState_VerifierValueRoot{VerifierValueRoot: bytes32(0x94)}}
	require.NoError(t, f.keeper.WriteCommit(f.ctx, key[:], commit))
	stored, err := f.keeper.CommitState.Get(f.ctx, key[:])
	require.NoError(t, err)
	require.Len(t, stored.VerifierOperatorAddress, 20)
	read, err := f.keeper.ReadCommit(f.ctx, key[:])
	require.NoError(t, err)
	require.True(t, proto.Equal(&commit, &read))
	commit.XVerifierValueRoot = nil
	require.NoError(t, f.keeper.WriteCommit(f.ctx, key[:], commit))
	read, err = f.keeper.ReadCommit(f.ctx, key[:])
	require.NoError(t, err)
	require.Nil(t, read.XVerifierValueRoot)
	commit.XVerifierValueRoot = &types.CommitState_VerifierValueRoot{VerifierValueRoot: []byte{1}}
	require.Error(t, f.keeper.WriteCommit(f.ctx, key[:], commit))
	row, err := f.keeper.ReadResultReceipt(f.ctx, key[:])
	require.NoError(t, err)
	row.MetricLeafCount = 2
	row.VerifierValueRoot = bytes32(0x95)
	row.VerifierEvidenceKeyCommitment = bytes32(0x96)
	require.NoError(t, f.keeper.WriteResultReceipt(f.ctx, key[:], row))
	result, err := f.keeper.ReadResultReceipt(f.ctx, key[:])
	require.NoError(t, err)
	require.True(t, proto.Equal(&row, &result), "accepted metric dimensions and both commitments must survive storage")
}
