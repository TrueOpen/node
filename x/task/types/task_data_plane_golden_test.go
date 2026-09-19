package types_test

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

type taskDataPlaneGolden struct {
	SchemaVersion string `json:"schema_version"`
	TaskID        struct {
		SessionIDRawHex string `json:"session_id_raw_hex"`
		OrderSequence   string `json:"order_sequence"`
		PreimageHex     string `json:"preimage_hex"`
		ExpectedHex     string `json:"expected_hex"`
	} `json:"task_id"`
}

func TestTaskDataPlaneV1MachineReadableGolden(t *testing.T) {
	raw, err := os.ReadFile("testdata/task_data_plane_v1_golden.json")
	require.NoError(t, err)
	var fixture taskDataPlaneGolden
	require.NoError(t, json.Unmarshal(raw, &fixture))
	require.Equal(t, "trueopen-task-data-plane-golden-v1", fixture.SchemaVersion)

	sequence := goldenUint64(t, fixture.TaskID.OrderSequence)
	sessionID, err := hex.DecodeString(fixture.TaskID.SessionIDRawHex)
	require.NoError(t, err)
	require.Len(t, sessionID, types.Hash32Len)
	preimage := shared.CanonicalFrameBytes(
		[]byte(shared.MustDomain(shared.DomainTaskIDV1)),
		sessionID,
		shared.Uint64BE(sequence),
	)
	require.Equal(t, fixture.TaskID.PreimageHex, hex.EncodeToString(preimage))
	require.Equal(t, fixture.TaskID.ExpectedHex, hex.EncodeToString(shared.CanonicalHashBytes(
		shared.MustDomain(shared.DomainTaskIDV1),
		sessionID,
		shared.Uint64BE(sequence),
	)))
	taskID, err := types.DeriveTaskIDFromRawSession(sessionID, sequence)
	require.NoError(t, err)
	require.Equal(t, fixture.TaskID.ExpectedHex, hex.EncodeToString(taskID[:]))
}

func goldenUint64(t *testing.T, value string) uint64 {
	t.Helper()
	parsed, err := strconv.ParseUint(value, 10, 64)
	require.NoError(t, err)
	return parsed
}
