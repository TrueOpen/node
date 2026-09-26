package keeper_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
)

// vrfPubkey builds a well-formed 32-byte VRF point. The bytes are never verified
// by ActivateDueVrfKeys — only their width is — so a fill pattern is enough to
// tell "the pending key was promoted" from "the active key stayed".
func vrfPubkey(fill byte) []byte {
	return bytes.Repeat([]byte{fill}, types.VrfPubkeyLen)
}

// TestActivateDueVrfKeysRetiresTheIteratedRow covers the three index-removal
// paths of §9.3a step 6. The sweep runs on every epoch boundary, so a row it
// fails to retire is re-visited for the life of the chain; each case below is
// written against a row whose activation epoch is strictly older than the epoch
// being swept, which is exactly the shape a removal keyed by (epoch, operator)
// instead of by the iterated key would miss.
func TestActivateDueVrfKeysRetiresTheIteratedRow(t *testing.T) {
	stale := hubAddress(t, 0x51)
	disagreeing := hubAddress(t, 0x52)
	rotating := hubAddress(t, 0x53)

	// A row scheduled for epoch 5 that is only swept at epoch 9 — a rotation that
	// came due while the chain was down.
	const scheduledAt = uint64(5)
	const sweptAt = uint64(9)

	t.Run("stale row with no pending key", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.VrfKeyActivationIndex.Set(f.ctx, types.NewVrfKeyActivationKey(scheduledAt, stale)))
		// No VrfKey row at all: the index outlived the state it pointed at.

		require.NoError(t, f.keeper.ActivateDueVrfKeys(f.ctx, sweptAt))

		has, err := f.keeper.VrfKeyActivationIndex.Has(f.ctx, types.NewVrfKeyActivationKey(scheduledAt, stale))
		require.NoError(t, err)
		require.False(t, has, "a stale activation row must be retired under the key it was iterated with")
	})

	t.Run("state disagrees with the row", func(t *testing.T) {
		f := initFixture(t)
		// The index says epoch 5, the state says the pending key is not authoritative
		// until epoch 20. Honouring the row would activate a key early, so the sweep
		// drops the row; it must drop the row it actually read.
		require.NoError(t, f.keeper.StoreVrfKey(f.ctx, types.VrfKeyState{
			OperatorAddress:       disagreeing,
			ActiveVrfPubkey:       vrfPubkey(0x01),
			ActiveFromEpoch:       1,
			XPendingVrfPubkey:     &types.VrfKeyState_PendingVrfPubkey{PendingVrfPubkey: vrfPubkey(0x02)},
			XPendingFromEpoch:     &types.VrfKeyState_PendingFromEpoch{PendingFromEpoch: 20},
			VrfAuthorizationNonce: 7,
		}))
		require.NoError(t, f.keeper.VrfKeyActivationIndex.Set(f.ctx, types.NewVrfKeyActivationKey(scheduledAt, disagreeing)))

		require.NoError(t, f.keeper.ActivateDueVrfKeys(f.ctx, sweptAt))

		has, err := f.keeper.VrfKeyActivationIndex.Has(f.ctx, types.NewVrfKeyActivationKey(scheduledAt, disagreeing))
		require.NoError(t, err)
		require.False(t, has, "a row the state contradicts must still be retired")

		state, err := f.keeper.GetVrfKey(f.ctx, disagreeing)
		require.NoError(t, err)
		require.Equal(t, vrfPubkey(0x01), state.ActiveVrfPubkey, "the early key must not have been promoted")
		require.NotNil(t, state.XPendingVrfPubkey, "the pending rotation must survive for its own epoch")
	})

	t.Run("overdue rotation is promoted and the row retired", func(t *testing.T) {
		f := initFixture(t)
		require.NoError(t, f.keeper.Params.Set(f.ctx, types.DefaultHubParams()))
		require.NoError(t, f.keeper.StoreVrfKey(f.ctx, types.VrfKeyState{
			OperatorAddress:       rotating,
			ActiveVrfPubkey:       vrfPubkey(0x01),
			ActiveFromEpoch:       1,
			XPendingVrfPubkey:     &types.VrfKeyState_PendingVrfPubkey{PendingVrfPubkey: vrfPubkey(0x02)},
			XPendingFromEpoch:     &types.VrfKeyState_PendingFromEpoch{PendingFromEpoch: scheduledAt},
			VrfAuthorizationNonce: 7,
		}))
		require.NoError(t, f.keeper.VrfKeyActivationIndex.Set(f.ctx, types.NewVrfKeyActivationKey(scheduledAt, rotating)))

		require.NoError(t, f.keeper.ActivateDueVrfKeys(f.ctx, sweptAt))

		state, err := f.keeper.GetVrfKey(f.ctx, rotating)
		require.NoError(t, err)
		require.Equal(t, vrfPubkey(0x02), state.ActiveVrfPubkey)
		require.Equal(t, scheduledAt, state.ActiveFromEpoch, "the key becomes authoritative from its own epoch, not the sweep's")
		require.Nil(t, state.XPendingVrfPubkey)
		require.Nil(t, state.XPendingFromEpoch)

		// The superseded key is kept for retrospective beacon verification.
		history, err := f.keeper.GetVrfKeyHistory(f.ctx, types.NewVrfKeyHistoryKey(rotating, 1))
		require.NoError(t, err)
		require.Equal(t, vrfPubkey(0x01), history.VrfPubkey)
		require.Equal(t, scheduledAt, history.RetiredAtEpoch)
		storedHistory, err := f.keeper.VrfKeyHistory.Get(f.ctx, types.NewVrfKeyHistoryKey(rotating, 1))
		require.NoError(t, err)
		encodedHistory, err := storedHistory.Marshal()
		require.NoError(t, err)
		require.False(t, bytes.Contains(encodedHistory, []byte(rotating)))
		require.True(t, bytes.Contains(encodedHistory, hubAddressBytes(t, rotating)))

		has, err := f.keeper.VrfKeyActivationIndex.Has(f.ctx, types.NewVrfKeyActivationKey(scheduledAt, rotating))
		require.NoError(t, err)
		require.False(t, has, "a completed rotation must retire the row it was iterated with")

		// Re-running the sweep is a no-op, which is what proves the row is gone
		// rather than merely shadowed.
		require.NoError(t, f.keeper.ActivateDueVrfKeys(f.ctx, sweptAt+1))
		state, err = f.keeper.GetVrfKey(f.ctx, rotating)
		require.NoError(t, err)
		require.Equal(t, vrfPubkey(0x02), state.ActiveVrfPubkey)
	})
}

// TestActivateDueVrfKeysLeavesFutureRowsAlone guards the ascending-epoch break:
// the index is keyed (activation_epoch, operator), so the first row past the
// epoch ends the scan and everything after it has to stay scheduled.
func TestActivateDueVrfKeysLeavesFutureRowsAlone(t *testing.T) {
	f := initFixture(t)
	operator := hubAddress(t, 0x54)

	require.NoError(t, f.keeper.StoreVrfKey(f.ctx, types.VrfKeyState{
		OperatorAddress:       operator,
		ActiveVrfPubkey:       vrfPubkey(0x01),
		ActiveFromEpoch:       1,
		XPendingVrfPubkey:     &types.VrfKeyState_PendingVrfPubkey{PendingVrfPubkey: vrfPubkey(0x02)},
		XPendingFromEpoch:     &types.VrfKeyState_PendingFromEpoch{PendingFromEpoch: 12},
		VrfAuthorizationNonce: 3,
	}))
	require.NoError(t, f.keeper.VrfKeyActivationIndex.Set(f.ctx, types.NewVrfKeyActivationKey(12, operator)))

	require.NoError(t, f.keeper.ActivateDueVrfKeys(f.ctx, 11))

	has, err := f.keeper.VrfKeyActivationIndex.Has(f.ctx, types.NewVrfKeyActivationKey(12, operator))
	require.NoError(t, err)
	require.True(t, has, "a rotation scheduled for a later epoch must stay scheduled")

	state, err := f.keeper.GetVrfKey(f.ctx, operator)
	require.NoError(t, err)
	require.Equal(t, vrfPubkey(0x01), state.ActiveVrfPubkey)
}
