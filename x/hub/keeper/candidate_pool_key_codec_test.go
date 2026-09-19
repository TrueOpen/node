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

// The four candidate-pool keyspaces are keyed on the same raw Hash32 as the rest
// of stage 3. encodeCustodyKey and signCompare are shared with
// custody_key_codec_test.go.
func TestCandidatePoolCollectionsUseRawHash32Keys(t *testing.T) {
	f := initFixture(t)
	id := bytes.Repeat([]byte{0x53}, shared.Hash32KeySize)

	snapshotCodec := f.keeper.CandidatePoolSnapshot.KeyCodec()
	encoded := encodeCustodyKey(t, snapshotCodec, id)
	require.Len(t, encoded, shared.Hash32KeySize)
	require.Equal(t, id, encoded)
	_, err := snapshotCodec.Encode(make([]byte, 64), []byte(hex.EncodeToString(id)))
	require.Error(t, err, "old lowercase-hex StringKey must not remain writable")
	_, decoded, err := snapshotCodec.Decode(encoded)
	require.NoError(t, err)
	encoded[0] ^= 0xff
	require.Equal(t, byte(0x53), decoded[0], "decoded key must not alias iterator storage")

	// task_ref is the only pair with a non-terminal Hash32 on both sides.
	taskID := bytes.Repeat([]byte{0x54}, shared.Hash32KeySize)
	refEncoded := encodeCustodyKey(t, f.keeper.CandidatePoolTaskRef.KeyCodec(), types.NewCandidatePoolTaskRefKey(taskID, id))
	require.Len(t, refEncoded, 2*shared.Hash32KeySize)
	require.Equal(t, taskID, refEncoded[:shared.Hash32KeySize])
	require.Equal(t, id, refEncoded[shared.Hash32KeySize:])

	expiryEncoded := encodeCustodyKey(t, f.keeper.CandidatePoolExpiryIndex.KeyCodec(), types.NewCandidatePoolExpiryIndexKey(7, id))
	require.Len(t, expiryEncoded, 8+shared.Hash32KeySize)
	pruneEncoded := encodeCustodyKey(t, f.keeper.CandidatePoolPruneIndex.KeyCodec(),
		types.NewCandidatePoolPruneIndexKey(7, id, types.CandidatePoolPrunePhase_CANDIDATE_POOL_PRUNE_PHASE_BODY))
	require.Len(t, pruneEncoded, 8+shared.Hash32KeySize+4)
	require.Equal(t, id, pruneEncoded[8:8+shared.Hash32KeySize])
}

// Prune and expiry are scanned in key order and firstDue* takes whichever row
// sorts first, so a reordering would silently change which snapshot is pruned
// first when several fall due at the same height. Lower hex and raw memcmp order
// identically, which is what keeps that observable order fixed across the
// retype -- assert it on the real composed codecs, not just on the component.
func TestCandidatePoolIndexOrderSurvivesTheRawRetype(t *testing.T) {
	f := initFixture(t)
	left := append(bytes.Repeat([]byte{0x00}, 31), 0xff)
	right := append(bytes.Repeat([]byte{0x00}, 30), 0x01, 0x00)
	const height = uint64(9)

	oldExpiry := collections.PairKeyCodec(collections.Uint64Key, collections.StringKey)
	newExpiry := f.keeper.CandidatePoolExpiryIndex.KeyCodec()
	require.Equal(t,
		signCompare(bytes.Compare(
			encodeCustodyKey(t, oldExpiry, collections.Join(height, hex.EncodeToString(left))),
			encodeCustodyKey(t, oldExpiry, collections.Join(height, hex.EncodeToString(right))),
		)),
		signCompare(bytes.Compare(
			encodeCustodyKey(t, newExpiry, types.NewCandidatePoolExpiryIndexKey(height, left)),
			encodeCustodyKey(t, newExpiry, types.NewCandidatePoolExpiryIndexKey(height, right)),
		)),
	)

	// The prune snapshot_id is non-terminal, so this also pins that the phase
	// suffix still breaks ties the same way it did behind a hex component.
	oldPrune := collections.TripleKeyCodec(collections.Uint64Key, collections.StringKey, collections.Int32Key)
	newPrune := f.keeper.CandidatePoolPruneIndex.KeyCodec()
	body := types.CandidatePoolPrunePhase_CANDIDATE_POOL_PRUNE_PHASE_BODY
	require.Equal(t,
		signCompare(bytes.Compare(
			encodeCustodyKey(t, oldPrune, collections.Join3(height, hex.EncodeToString(left), int32(body))),
			encodeCustodyKey(t, oldPrune, collections.Join3(height, hex.EncodeToString(right), int32(body))),
		)),
		signCompare(bytes.Compare(
			encodeCustodyKey(t, newPrune, types.NewCandidatePoolPruneIndexKey(height, left, body)),
			encodeCustodyKey(t, newPrune, types.NewCandidatePoolPruneIndexKey(height, right, body)),
		)),
	)
}

// Both candidate-pool invariants read the store key directly now. Neither had
// any coverage before this batch, so a mismatch was only ever caught by reading
// the code.
func TestCandidatePoolInvariantsRejectKeyValueMismatch(t *testing.T) {
	t.Run("snapshot header", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
		key := hubHashBytes("candidate-key")
		require.NoError(t, f.keeper.CandidatePoolSnapshot.Set(f.ctx, key, types.CandidatePoolSnapshotState{
			SchemaVersion: 1, Epoch: 1, SnapshotId: hubHashBytes("candidate-value"),
			PoolHash: hubHashBytes("pool"), Status: types.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_READY,
			SlotCapacity: 8, ActiveBitmapHash: hubHashBytes("bitmap"), MemberSetHash: hubHashBytes("members"),
			EffectiveHeight: 1, ExpiresHeight: 2,
		}))
		require.ErrorContains(t, f.keeper.EnsureCandidatePoolBodyInvariant(f.ctx), "does not match its store key")
	})

	t.Run("current pointer", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.InitGenesis(f.ctx, *types.DefaultGenesis()))
		active := hubHashBytes("candidate-active")
		require.NoError(t, f.keeper.CandidatePoolSnapshot.Set(f.ctx, active, types.CandidatePoolSnapshotState{
			SchemaVersion: 1, Epoch: 1, SnapshotId: active,
			PoolHash: hubHashBytes("pool"), Status: types.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_ACTIVE,
			SlotCapacity: 8, ActiveBitmapHash: hubHashBytes("bitmap"), MemberSetHash: hubHashBytes("members"),
			EffectiveHeight: 1, ExpiresHeight: 2,
		}))
		require.NoError(t, f.keeper.CurrentCandidatePool.Set(f.ctx, types.CurrentCandidatePoolState{
			Epoch: 1, SnapshotId: hubHashBytes("candidate-other"), PoolHash: hubHashBytes("pool"),
		}))
		require.ErrorContains(t, f.keeper.EnsureCandidatePoolPointerInvariant(f.ctx), "does not resolve to the ACTIVE snapshot")
	})
}
