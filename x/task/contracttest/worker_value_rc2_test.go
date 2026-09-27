package contracttest

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	tasktypes "github.com/TrueOpen/node/x/task/types"
)

func TestWorkerValueCommitmentMatchesWireRC2(t *testing.T) {
	root, err := hex.DecodeString("26436a419e76afb3d88ef3435346b34399abae22f62346c19bf63e9be0737281")
	require.NoError(t, err)
	commitment, size, err := tasktypes.WorkerValueCommitment(tasktypes.WorkerValueCommitmentV3{
		SchemaVersion:                3,
		ChainId:                      "trueopen-golden-1",
		TaskId:                       bytes.Repeat([]byte{0x11}, 32),
		AcceptedTaskHash:             bytes.Repeat([]byte{0x22}, 32),
		WorkerOperatorAddress:        "trueopen1rfjz7r3u8t65teavh5utquj3kwvsj983p3jclz",
		EvidenceSchemaHash:           bytes.Repeat([]byte{0x44}, 32),
		WorkerValueRoot:              root,
		WorkerValuesEncodedSizeBytes: 817,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(817), size)
	require.Equal(t, "17b4b66711beba88ab8ea07f28d21576daa206c78583ec4f3023aaaa1fd7f44d", hex.EncodeToString(commitment[:]))
}
