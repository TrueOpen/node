package types_test

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/task/types"
)

func TestDeriveTaskIDFromRawSessionMatchesNormativeVector(t *testing.T) {
	sessionID, err := hex.DecodeString("abababababababababababababababababababababababababababababababab")
	require.NoError(t, err)
	taskID, err := types.DeriveTaskIDFromRawSession(sessionID, 42)
	require.NoError(t, err)
	require.Equal(t, "0891a5c5704d4dff5671daab9b353f31c86f81ac53cf1b3c4ef5171322c19f50", hex.EncodeToString(taskID[:]))

	next, err := types.DeriveTaskIDFromRawSession(sessionID, 43)
	require.NoError(t, err)
	require.NotEqual(t, taskID, next)

	for _, invalid := range [][]byte{nil, sessionID[:31], append(sessionID, 0)} {
		_, err = types.DeriveTaskIDFromRawSession(invalid, 42)
		require.ErrorContains(t, err, "session_id must be Hash32")
	}
}
