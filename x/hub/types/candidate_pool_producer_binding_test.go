package types_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/internal/testutil/domainfixture"
	"github.com/TrueOpen/node/x/hub/types"
)

type candidatePoolProducerSource struct {
	ChainID         string                        `json:"chain_id"`
	Epoch           uint64                        `json:"epoch"`
	SlotCapacity    uint32                        `json:"slot_capacity"`
	EffectiveHeight uint64                        `json:"effective_height"`
	ExpiresHeight   uint64                        `json:"expires_height"`
	SegmentIndex    uint32                        `json:"segment_index"`
	BitmapHex       string                        `json:"bitmap_hex"`
	Members         []candidatePoolProducerMember `json:"members"`
}

type candidatePoolProducerMember struct {
	Slot             uint32 `json:"slot"`
	SlotVersion      uint64 `json:"slot_version"`
	OperatorBytesHex string `json:"operator_bytes_hex"`
	AllocatedEpoch   uint64 `json:"allocated_epoch"`
}

func TestCandidatePoolFixtureBindsProductionProducers(t *testing.T) {
	const path = "testdata/candidate_pool_v1.json"
	fixture := domainfixture.Load(t, path)
	vectors := domainfixture.ByName(t, path)
	var source candidatePoolProducerSource
	require.NoError(t, json.Unmarshal(fixture.Source, &source))
	require.Len(t, source.Members, 2)

	bitmap := domainfixture.DecodeHex(t, "candidate bitmap", source.BitmapHex)
	members := make([]types.CandidatePoolMemberRef, len(source.Members))
	bindings := make([][]byte, len(source.Members))
	for index, member := range source.Members {
		operator := domainfixture.DecodeHex(t, "candidate operator", member.OperatorBytesHex)
		binding, err := types.CandidateSlotBindingHash(
			member.Slot, member.SlotVersion, operator, member.AllocatedEpoch,
		)
		require.NoError(t, err)
		bindings[index] = binding
		members[index] = types.CandidatePoolMemberRef{
			Slot: member.Slot, SlotVersion: member.SlotVersion,
			OperatorBytes: operator, BindingHash: binding,
		}
	}

	segment, err := types.CandidatePoolSegmentHash(
		source.Epoch, source.SegmentIndex, bitmap, uint32(len(members)), members,
	)
	require.NoError(t, err)
	segments := []types.CandidatePoolSegmentRef{{SegmentIndex: source.SegmentIndex, Bitmap: bitmap}}
	activeBitmap, err := types.CandidateActiveBitmapHash(source.SlotCapacity, uint32(len(segments)), segments)
	require.NoError(t, err)
	memberSet, err := types.CandidateMemberSetHash(uint32(len(members)), members)
	require.NoError(t, err)
	pool, err := types.CandidatePoolHash(
		source.ChainID, source.Epoch, source.SlotCapacity, source.EffectiveHeight,
		source.ExpiresHeight, uint32(len(members)), activeBitmap, memberSet,
	)
	require.NoError(t, err)
	snapshot, err := types.CandidatePoolSnapshotID(source.ChainID, source.Epoch, pool)
	require.NoError(t, err)
	membership, err := types.CandidateMembershipBinding(
		source.ChainID, snapshot, pool, members[0].Slot, members[0].SlotVersion, members[0].OperatorBytes,
	)
	require.NoError(t, err)

	got := map[string][]byte{
		"candidate_slot_binding_v1":            bindings[0],
		"candidate_pool_segment_v1":            segment,
		"candidate_active_bitmap_v1":           activeBitmap,
		"candidate_member_set_v1":              memberSet,
		"global_candidate_pool_v1":             pool,
		"global_candidate_pool_snapshot_id_v1": snapshot,
		"candidate_membership_v1":              membership,
	}
	bound := make(map[string]struct{}, len(got))
	for name, digest := range got {
		vector, ok := vectors[name]
		require.True(t, ok, "producer result %s has no vector", name)
		domainfixture.RequireDigest(t, vector, digest)
		bound[name] = struct{}{}
	}
	domainfixture.RequireBoundSet(t, vectors, bound, nil)
}
