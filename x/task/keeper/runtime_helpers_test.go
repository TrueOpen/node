package keeper_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/keeper"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

// §16.3 QueryRoleActiveTasks needs both reverse indexes; the writer takes raw
// 32-byte task IDs and a canonical operator address.
func TestRoleActiveTaskIndexesAddAndRemove(t *testing.T) {
	f := initFixture(t)
	genesis := taskGenesisV1(t, genesisChainID(f))
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *genesis))

	taskID := genesis.TaskCores[0].TaskId
	taskKey := taskKeyOf(taskID)

	hasWorker, err := f.keeper.WorkerActiveTaskIndex.Has(f.ctx, tasktypes.NewWorkerActiveTaskKey(genesisWorker, taskKey))
	require.NoError(t, err)
	require.True(t, hasWorker, "genesis must rebuild WorkerActiveTaskIndex from the assignment")

	require.NoError(t, f.keeper.AddVerifierActiveJobIndex(f.ctx, "verifier-a", taskID))
	require.NoError(t, f.keeper.VerifierAssignment.Set(
		f.ctx,
		tasktypes.NewVerifyRoundKey(taskKey, tasktypes.VerifyRoundV1),
		tasktypes.VerifierAssignmentState{
			TaskId:            taskID,
			VerifyRound:       tasktypes.VerifyRoundV1,
			SelectedVerifiers: []tasktypes.SelectedVerifierV1{{OperatorAddress: "verifier-a"}},
		},
	))
	hasVerifier, err := f.keeper.VerifierActiveJobIndex.Has(f.ctx, tasktypes.NewVerifierActiveJobKey("verifier-a", taskKey))
	require.NoError(t, err)
	require.True(t, hasVerifier)

	require.NoError(t, f.keeper.RemoveRoleActiveTaskIndexes(f.ctx, taskID))
	hasWorker, err = f.keeper.WorkerActiveTaskIndex.Has(f.ctx, tasktypes.NewWorkerActiveTaskKey(genesisWorker, taskKey))
	require.NoError(t, err)
	require.False(t, hasWorker)
	hasVerifier, err = f.keeper.VerifierActiveJobIndex.Has(f.ctx, tasktypes.NewVerifierActiveJobKey("verifier-a", taskKey))
	require.NoError(t, err)
	require.False(t, hasVerifier)
}

func TestRoleActiveTaskIndexValidation(t *testing.T) {
	f := initFixture(t)
	require.ErrorIs(t, f.keeper.AddWorkerActiveTaskIndex(f.ctx, " ", repeatByte(0x04)), tasktypes.ErrInvalidAssignment)
	require.ErrorIs(t, f.keeper.AddVerifierActiveJobIndex(f.ctx, " ", repeatByte(0x04)), tasktypes.ErrInvalidOpenVerify)
	// A task_id that is not exactly 32 bytes is rejected before any write.
	require.Error(t, f.keeper.AddWorkerActiveTaskIndex(f.ctx, genesisWorker, []byte{0x01}))
}

// §5.13 / §1.4: session_id = H_FIELDS_V1("TRUEOPEN_SESSION_V1", user_address, nonce)
// over canonical address bytes and a u64 big-endian nonce, never display text.
func TestDeriveSessionIDUsesRegisteredPreimage(t *testing.T) {
	ownerBytes := bytes.Repeat([]byte{0x0a}, 20)
	want := shared.CanonicalHashBytes(
		shared.MustDomain(shared.DomainSessionV1),
		ownerBytes,
		shared.Uint64BE(7),
	)
	got, err := keeper.DeriveSessionID(ownerBytes, 7)
	require.NoError(t, err)
	require.Equal(t, want, got)
	// An external anchor. `want` above is the preimage written out a second time,
	// so a reordering applied to both DeriveSessionID and this test stays green;
	// the frozen hex below is a session_id that already exists on chain, and it
	// cannot be re-derived from the code under test.
	require.Equal(t, "f3e136453c4ff3532bf0429ec68016be579986071f7d67863bcbf10ccd34b0e3",
		hex.EncodeToString(got),
		"TRUEOPEN_SESSION_V1 is a frozen consensus preimage; moving this constant is a consensus change and must be re-checked against the API contract §5.13 and the §1.4 domain registry")
	nextNonce, err := keeper.DeriveSessionID(ownerBytes, 8)
	require.NoError(t, err)
	require.NotEqual(t, got, nextNonce)
}
