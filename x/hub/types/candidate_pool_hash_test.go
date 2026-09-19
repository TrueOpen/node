package types_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestCandidatePoolHashFunctionsUseFrozenFrames(t *testing.T) {
	opA := bytes.Repeat([]byte{0x11}, 20)
	opB := bytes.Repeat([]byte{0x22}, 20)
	bindingA, err := types.CandidateSlotBindingHash(1, 2, opA, 3)
	require.NoError(t, err)
	require.Equal(t, shared.CanonicalHashBytes(shared.MustDomain(shared.DomainCandidateSlotBindingV1),
		shared.Uint32BE(1), shared.Uint64BE(2), opA, shared.Uint64BE(3)), bindingA)
	bindingB, err := types.CandidateSlotBindingHash(3, 1, opB, 3)
	require.NoError(t, err)

	members := []types.CandidatePoolMemberRef{
		{Slot: 1, SlotVersion: 2, OperatorBytes: opA, BindingHash: bindingA},
		{Slot: 3, SlotVersion: 1, OperatorBytes: opB, BindingHash: bindingB},
	}
	bitmap := []byte{0x0a, 0x00}
	segmentHash, err := types.CandidatePoolSegmentHash(9, 0, bitmap, 2, members)
	require.NoError(t, err)
	segmentMembers, err := shared.RepeatedFrameV1(
		shared.CanonicalFrameBytes(shared.Uint32BE(1), shared.Uint64BE(2), bindingA),
		shared.CanonicalFrameBytes(shared.Uint32BE(3), shared.Uint64BE(1), bindingB),
	)
	require.NoError(t, err)
	require.Equal(t, shared.CanonicalHashBytes(shared.MustDomain(shared.DomainCandidatePoolSegmentV1),
		shared.Uint64BE(9), shared.Uint32BE(0), bitmap, shared.Uint32BE(2),
		segmentMembers), segmentHash)

	segments := []types.CandidatePoolSegmentRef{{SegmentIndex: 0, Bitmap: bitmap}}
	bitmapHash, err := types.CandidateActiveBitmapHash(15, 1, segments)
	require.NoError(t, err)
	bitmapSegments, err := shared.RepeatedFrameV1(
		shared.CanonicalFrameBytes(shared.Uint32BE(0), bitmap),
	)
	require.NoError(t, err)
	require.Equal(t, shared.CanonicalHashBytes(shared.MustDomain(shared.DomainCandidateActiveBitmapV1),
		shared.Uint32BE(15), shared.Uint32BE(1), bitmapSegments), bitmapHash)
	memberHash, err := types.CandidateMemberSetHash(2, members)
	require.NoError(t, err)
	memberSet, err := shared.RepeatedFrameV1(
		shared.CanonicalFrameBytes(shared.Uint32BE(1), shared.Uint64BE(2), opA, bindingA),
		shared.CanonicalFrameBytes(shared.Uint32BE(3), shared.Uint64BE(1), opB, bindingB),
	)
	require.NoError(t, err)
	require.Equal(t, shared.CanonicalHashBytes(shared.MustDomain(shared.DomainCandidateMemberSetV1),
		shared.Uint32BE(2), memberSet), memberHash)
	poolHash, err := types.CandidatePoolHash("trueopen-test", 9, 15, 91, 100, 2, bitmapHash, memberHash)
	require.NoError(t, err)
	require.Equal(t, shared.CanonicalHashBytes(shared.MustDomain(shared.DomainGlobalCandidatePoolV1),
		[]byte("trueopen-test"), shared.Uint64BE(9), shared.Uint32BE(15), shared.Uint64BE(91),
		shared.Uint64BE(100), shared.Uint32BE(2), bitmapHash, memberHash), poolHash)
	snapshotID, err := types.CandidatePoolSnapshotID("trueopen-test", 9, poolHash)
	require.NoError(t, err)
	require.Equal(t, shared.CanonicalHashBytes(shared.MustDomain(shared.DomainGlobalCandidatePoolSnapshotIDV1),
		[]byte("trueopen-test"), shared.Uint64BE(9), poolHash), snapshotID)
	membership, err := types.CandidateMembershipBinding("trueopen-test", snapshotID, poolHash, 1, 2, opA)
	require.NoError(t, err)
	require.Equal(t, shared.CanonicalHashBytes(shared.MustDomain(shared.DomainCandidateMembershipV1),
		[]byte("trueopen-test"), snapshotID, poolHash, shared.Uint32BE(1), shared.Uint64BE(2), opA), membership)
}

func TestCandidatePoolHashRejectsNonCanonicalCollections(t *testing.T) {
	op := bytes.Repeat([]byte{0x33}, 20)
	binding, err := types.CandidateSlotBindingHash(1, 1, op, 0)
	require.NoError(t, err)
	members := []types.CandidatePoolMemberRef{
		{Slot: 2, SlotVersion: 1, OperatorBytes: op, BindingHash: binding},
		{Slot: 1, SlotVersion: 1, OperatorBytes: op, BindingHash: binding},
	}
	_, err = types.CandidateMemberSetHash(2, members)
	require.ErrorContains(t, err, "strictly ascending")

	_, err = types.CandidatePoolSegmentHash(1, 0, []byte{0x02}, 1, []types.CandidatePoolMemberRef{
		{Slot: 2, SlotVersion: 1, OperatorBytes: op, BindingHash: binding},
	})
	require.ErrorContains(t, err, "not active")

	_, err = types.CandidateActiveBitmapHash(9, 2, []types.CandidatePoolSegmentRef{
		{SegmentIndex: 0, Bitmap: []byte{0}},
		{SegmentIndex: 1, Bitmap: []byte{0x02}},
	})
	require.ErrorContains(t, err, "trailing bit")

	_, err = types.CandidateActiveBitmapHash(9, 2, []types.CandidatePoolSegmentRef{
		{SegmentIndex: 1, Bitmap: []byte{0}},
		{SegmentIndex: 0, Bitmap: []byte{0}},
	})
	require.ErrorContains(t, err, "contiguous and ascending")
}

func TestCandidateMemberSetHashDefinesEmptySet(t *testing.T) {
	got, err := types.CandidateMemberSetHash(0, nil)
	require.NoError(t, err)
	want := shared.CanonicalHashBytes(
		shared.MustDomain(shared.DomainCandidateMemberSetV1),
		shared.Uint32BE(0), shared.CanonicalFrameBytes(shared.Uint32BE(0)),
	)
	require.Equal(t, want, got)
}

func TestCandidatePoolCapacityBoundaries(t *testing.T) {
	bitmapHash := bytes.Repeat([]byte{0x44}, 32)
	memberHash := bytes.Repeat([]byte{0x55}, 32)

	_, err := types.CandidatePoolHash("trueopen-test", 1, 2, 10, 20, 1, bitmapHash, memberHash)
	require.NoError(t, err, "capacity max-1 must be accepted")
	_, err = types.CandidatePoolHash("trueopen-test", 1, 2, 10, 20, 2, bitmapHash, memberHash)
	require.NoError(t, err, "capacity max must be accepted")
	_, err = types.CandidatePoolHash("trueopen-test", 1, 2, 10, 20, 3, bitmapHash, memberHash)
	require.ErrorContains(t, err, "active_count")
}

func TestCandidateActiveBitmapCapacityCrossesSegmentBoundary(t *testing.T) {
	for _, test := range []struct {
		name         string
		capacity     uint32
		segmentCount uint32
		segments     []types.CandidatePoolSegmentRef
	}{
		{name: "max-1", capacity: 7, segmentCount: 1, segments: []types.CandidatePoolSegmentRef{{SegmentIndex: 0, Bitmap: []byte{0x7f}}}},
		{name: "max", capacity: 8, segmentCount: 1, segments: []types.CandidatePoolSegmentRef{{SegmentIndex: 0, Bitmap: []byte{0xff}}}},
		{name: "max+1", capacity: 9, segmentCount: 2, segments: []types.CandidatePoolSegmentRef{{SegmentIndex: 0, Bitmap: []byte{0xff}}, {SegmentIndex: 1, Bitmap: []byte{0x01}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := types.CandidateActiveBitmapHash(test.capacity, test.segmentCount, test.segments)
			require.NoError(t, err)
		})
	}
}
