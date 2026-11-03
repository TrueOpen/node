package types

import (
	"bytes"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRoundAcceptorRequiresPositiveStoreClock(t *testing.T) {
	row := DispatchRoundAcceptorState{SessionId: bytes.Repeat([]byte{1}, 32), DispatchTicketHash: bytes.Repeat([]byte{2}, 32), BuilderAddress: "trueopen1rfjz7r3u8t65teavh5utquj3kwvsj983p3jclz", RegisteredHeight: 1}
	require.NoError(t, ValidateDispatchRoundAcceptor(row))
	for _, height := range []uint64{0, uint64(math.MaxInt64) + 1} {
		row.RegisteredHeight = height
		require.Error(t, ValidateDispatchRoundAcceptor(row))
	}
}
