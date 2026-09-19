package keeper

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/task/types"
)

func TestSessionIDUsesCanonicalAddressAndUint64Bytes(t *testing.T) {
	sessionID, err := DeriveSessionID([]byte{1, 2, 3}, 7)
	require.NoError(t, err)
	require.Equal(t, "eac72b2adddabad1edb1a85c61a3e0c30636775b07fc650491a6842f02c33b25", hex.EncodeToString(sessionID))

	textOwner, err := DeriveSessionID([]byte("010203"), 7)
	require.NoError(t, err)
	require.NotEqual(t, sessionID, textOwner)
	nextNonce, err := DeriveSessionID([]byte{1, 2, 3}, 8)
	require.NoError(t, err)
	require.NotEqual(t, sessionID, nextNonce)
}

func TestSessionSequenceRootGoldenVector(t *testing.T) {
	taskID := bytes.Repeat([]byte{0xaa}, types.Hash32Len)
	root, err := sessionSequenceRoot(make([]byte, types.Hash32Len), types.OrderSequenceState{
		OrderSequence:  7,
		Status:         types.OrderSequenceStatus_ORDER_SEQUENCE_STATUS_REFUNDED,
		TaskId:         taskID,
		ConsumedHeight: 11,
	})
	require.NoError(t, err)
	require.Equal(t, "16ff05ac6233a5dabf79e75dd3e30c9164c25fd015ac5313c9fb70ad5d54161a", hex.EncodeToString(root))

	_, err = sessionSequenceRoot(make([]byte, types.Hash32Len-1), types.OrderSequenceState{})
	require.ErrorIs(t, err, types.ErrInvariantBroken)
}
