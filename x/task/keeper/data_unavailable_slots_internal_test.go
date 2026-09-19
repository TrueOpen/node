package keeper

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/task/types"
)

func TestDataUnavailableDigestSlotsUsePresenceForAbsence(t *testing.T) {
	digests := [][]byte{bytes.Repeat([]byte{0x31}, types.Hash32Len), nil}
	slots, err := encodeDataUnavailableDigestSlots(digests)
	require.NoError(t, err)
	require.IsType(t, &types.BuilderDataUnavailableSlotV1_ReportDigest{}, slots[0].GetXReportDigest())
	require.Nil(t, slots[1].GetXReportDigest())

	decoded, err := decodeDataUnavailableDigestSlots(slots)
	require.NoError(t, err)
	require.Equal(t, digests, decoded)
}

func TestDataUnavailableDigestSlotsRejectPresentInvalidHash32(t *testing.T) {
	for _, digest := range [][]byte{{}, bytes.Repeat([]byte{0x31}, types.Hash32Len-1), make([]byte, types.Hash32Len)} {
		_, err := decodeDataUnavailableDigestSlots([]types.BuilderDataUnavailableSlotV1{{
			XReportDigest: &types.BuilderDataUnavailableSlotV1_ReportDigest{ReportDigest: digest},
		}})
		require.Error(t, err)
	}
}
