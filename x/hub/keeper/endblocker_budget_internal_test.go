package keeper

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
)

func TestMaxEndblockRowBytesTracksSegmentWidthNotSlotCapacity(t *testing.T) {
	params := types.DefaultHubParams()
	base := maxEndblockRowBytes(params)

	widerCapacity := params
	widerCapacity.CandidatePool.CandidateSlotHardCapacity *= 2
	require.Equal(t, base, maxEndblockRowBytes(widerCapacity))

	widerSegment := params
	widerSegment.CandidatePool.CandidateBitmapSegmentBytes += 32
	require.Equal(t, base+32, maxEndblockRowBytes(widerSegment))

	widerBucket := params
	widerBucket.Bucket.MaxParameterBucketUpdateBytes += 64
	require.Equal(t, base+128, maxEndblockRowBytes(widerBucket))
}
