package keeper_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"cosmossdk.io/collections"
	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// The task_id component of all four keyspaces is raw Hash32; operator_address
// stays text and BuilderSet version is uint64. encodeCustodyKey and signCompare are shared with
// custody_key_codec_test.go.
func TestTaskLiabilityCollectionsUseRawHash32TaskIDs(t *testing.T) {
	f := initFixture(t)
	taskID := bytes.Repeat([]byte{0x61}, shared.Hash32KeySize)
	const operator = "trueopen1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq3w3xs0"
	duty := shared.DutyWorker

	// Non-terminal Hash32 is exactly 32 bytes with no length prefix and no
	// terminator, which is where the 33 bytes per row come from.
	reservation := encodeCustodyKey(t, f.keeper.TaskLiabilityReservation.KeyCodec(),
		types.NewTaskLiabilityReservationKey(taskID, duty, operator))
	require.Equal(t, taskID, reservation[:shared.Hash32KeySize])
	require.Len(t, reservation, shared.Hash32KeySize+4+len(operator))

	byTask := encodeCustodyKey(t, f.keeper.TaskLiabilityByTaskIndex.KeyCodec(),
		types.NewTaskLiabilityByTaskKey(taskID, duty, operator))
	require.Equal(t, taskID, byTask[:shared.Hash32KeySize])

	// operator_address leads here, so the Hash32 sits in the middle.
	byOperator := encodeCustodyKey(t, f.keeper.ActiveLiabilityByOperatorIndex.KeyCodec(),
		types.NewActiveLiabilityByOperatorKey(operator, taskID, duty))
	require.Equal(t, taskID, byOperator[len(operator)+1:len(operator)+1+shared.Hash32KeySize])

	builderRef := encodeCustodyKey(t, f.keeper.BuilderSetTaskRef.KeyCodec(),
		types.NewBuilderSetTaskRefKey(taskID, 7))
	require.Equal(t, taskID, builderRef[:shared.Hash32KeySize])
	require.Len(t, builderRef, shared.Hash32KeySize+8)

	// The old key spelled task_id as 64 lower-hex chars, so it is now too wide to
	// encode at all.
	_, err := f.keeper.TaskLiabilityReservation.KeyCodec().Encode(make([]byte, 256),
		types.NewTaskLiabilityReservationKey([]byte(hex.EncodeToString(taskID)), duty, operator))
	require.Error(t, err, "old lowercase-hex task_id must not remain writable")
	_, err = f.keeper.BuilderSetTaskRef.KeyCodec().Encode(make([]byte, 256),
		types.NewBuilderSetTaskRefKey([]byte(hex.EncodeToString(taskID)), 7))
	require.Error(t, err, "old lowercase-hex task_id must not remain writable")
}

// ReleaseTaskLiabilities, DeleteOneClosedTaskLiability, ensureTaskLiabilityCapacity
// and operatorHasActiveLiability all walk a prefix range, so the retype must not
// reorder rows inside a prefix. Lower hex and raw memcmp agree, and this pins it
// on the real composed codecs -- including ActiveLiabilityByOperatorIndex, where
// the Hash32 is the middle component and a text operator precedes it.
func TestTaskLiabilityPrefixOrderSurvivesTheRawRetype(t *testing.T) {
	f := initFixture(t)
	left := append(bytes.Repeat([]byte{0x00}, 31), 0xff)
	right := append(bytes.Repeat([]byte{0x00}, 30), 0x01, 0x00)
	const operator = "trueopen1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq3w3xs0"
	duty := shared.DutyVerifier

	oldByTask := collections.TripleKeyCodec(collections.StringKey, collections.Int32Key, collections.StringKey)
	newByTask := f.keeper.TaskLiabilityByTaskIndex.KeyCodec()
	require.Equal(t,
		signCompare(bytes.Compare(
			encodeCustodyKey(t, oldByTask, collections.Join3(hex.EncodeToString(left), int32(duty), operator)),
			encodeCustodyKey(t, oldByTask, collections.Join3(hex.EncodeToString(right), int32(duty), operator)),
		)),
		signCompare(bytes.Compare(
			encodeCustodyKey(t, newByTask, types.NewTaskLiabilityByTaskKey(left, duty, operator)),
			encodeCustodyKey(t, newByTask, types.NewTaskLiabilityByTaskKey(right, duty, operator)),
		)),
	)

	oldByOperator := collections.TripleKeyCodec(collections.StringKey, collections.StringKey, collections.Int32Key)
	newByOperator := f.keeper.ActiveLiabilityByOperatorIndex.KeyCodec()
	require.Equal(t,
		signCompare(bytes.Compare(
			encodeCustodyKey(t, oldByOperator, collections.Join3(operator, hex.EncodeToString(left), int32(duty))),
			encodeCustodyKey(t, oldByOperator, collections.Join3(operator, hex.EncodeToString(right), int32(duty))),
		)),
		signCompare(bytes.Compare(
			encodeCustodyKey(t, newByOperator, types.NewActiveLiabilityByOperatorKey(operator, left, duty)),
			encodeCustodyKey(t, newByOperator, types.NewActiveLiabilityByOperatorKey(operator, right, duty)),
		)),
	)
}

// EnsureTaskLiabilityIndexInvariant is the only thing standing between a
// one-sided index and an unreleasable (or invisible) liability. Its primary
// key/value identity check and both index directions had no coverage before this
// batch.
func TestTaskLiabilityIndexInvariantRejectsMismatchAndOneSidedIndexes(t *testing.T) {
	operator := hubAddress(t, 231)
	taskID := hubHashBytes("liability-index-task")
	reserved := func(id []byte) types.TaskLiabilityReservationState {
		return types.TaskLiabilityReservationState{
			TaskId: id, OperatorAddress: operator, Duty: shared.DutyWorker,
			BondVersion: 1, CapabilityVersion: 1, ReservedAmount: 1,
			Status:                  types.TaskLiabilityStatusReserved,
			CandidatePoolSnapshotId: hubHashBytes("liability-index-snapshot"), SlotVersion: 1,
		}
	}

	t.Run("key does not match value", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
		require.NoError(t, f.keeper.TaskLiabilityReservation.Set(f.ctx,
			types.NewTaskLiabilityReservationKey(taskID, shared.DutyWorker, operator),
			reserved(hubHashBytes("liability-index-other")),
		))
		require.ErrorContains(t, f.keeper.EnsureTaskLiabilityIndexInvariant(f.ctx), "does not match its store key")
	})

	t.Run("reserved row without indexes", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
		require.NoError(t, f.keeper.TaskLiabilityReservation.Set(f.ctx,
			types.NewTaskLiabilityReservationKey(taskID, shared.DutyWorker, operator), reserved(taskID),
		))
		require.ErrorContains(t, f.keeper.EnsureTaskLiabilityIndexInvariant(f.ctx), "disagrees with its indexes")
	})

	t.Run("index without primary row", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
		require.NoError(t, f.keeper.ActiveLiabilityByOperatorIndex.Set(f.ctx,
			types.NewActiveLiabilityByOperatorKey(operator, taskID, shared.DutyWorker)))
		require.ErrorContains(t, f.keeper.EnsureTaskLiabilityIndexInvariant(f.ctx), "has no reserved primary row")
	})

	t.Run("by-task index without primary row", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
		require.NoError(t, f.keeper.TaskLiabilityByTaskIndex.Set(f.ctx,
			types.NewTaskLiabilityByTaskKey(taskID, shared.DutyWorker, operator)))
		require.ErrorContains(t, f.keeper.EnsureTaskLiabilityIndexInvariant(f.ctx), "has no reserved primary row")
	})
}
