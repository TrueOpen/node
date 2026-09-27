package types_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func TestTaskAssignmentStorePreservesMinStakeSnapshot(t *testing.T) {
	public := tasktypes.TaskAssignmentState{
		TaskId:           bytes.Repeat([]byte{0x41}, tasktypes.Hash32Len),
		MinStakeSnapshot: shared.NewAmount(1_000_000),
	}
	encoded, err := public.Marshal()
	require.NoError(t, err)
	var stored internaltypes.TaskAssignmentStoreState
	require.NoError(t, stored.Unmarshal(encoded))
	require.NotEmpty(t, stored.MinStakeSnapshot)
	reencoded, err := stored.Marshal()
	require.NoError(t, err)
	var projected tasktypes.TaskAssignmentState
	require.NoError(t, projected.Unmarshal(reencoded))
	require.Equal(t, public.TaskId, projected.TaskId)
	require.Equal(t, public.MinStakeSnapshot, projected.MinStakeSnapshot)
}

func TestTaskTerminalSummaryStorePreservesRawModelID(t *testing.T) {
	modelID := bytes.Repeat([]byte{0xff}, tasktypes.Hash32Len)
	public := tasktypes.TaskTerminalSummaryState{
		TaskId:  bytes.Repeat([]byte{0x42}, tasktypes.Hash32Len),
		ModelId: modelID,
	}
	encoded, err := public.Marshal()
	require.NoError(t, err)
	var stored internaltypes.TaskTerminalSummaryStoreState
	require.NoError(t, stored.Unmarshal(encoded))
	require.Equal(t, modelID, stored.ModelId)
	reencoded, err := stored.Marshal()
	require.NoError(t, err)
	var projected tasktypes.TaskTerminalSummaryState
	require.NoError(t, projected.Unmarshal(reencoded))
	require.Equal(t, modelID, projected.ModelId)
}
