package keeper

import (
	"context"
	"fmt"

	"github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

// loadCandidatePoolSnapshot takes the raw store key. The lower-hex it used to
// take was decoded on the very next line only to compare it back against
// snapshot_id; with a raw key that decode is gone and the key/value identity
// check below compares the same bytes the caller looked up with.
func (k Keeper) loadCandidatePoolSnapshot(ctx context.Context, snapshotID shared.Hash32Key) (types.CandidatePoolSnapshotState, error) {
	rawID, err := candidateHashKey(snapshotID)
	if err != nil {
		return types.CandidatePoolSnapshotState{}, err
	}
	snapshot, err := k.CandidatePoolSnapshot.Get(ctx, rawID)
	if err != nil {
		return types.CandidatePoolSnapshotState{}, err
	}
	if snapshot.SchemaVersion != 1 || !equalCandidateBytes(snapshot.SnapshotId, rawID) || len(snapshot.PoolHash) != 32 || len(snapshot.ActiveBitmapHash) != 32 || len(snapshot.MemberSetHash) != 32 || snapshot.SlotCapacity == 0 || snapshot.ActiveCount > snapshot.SlotCapacity || snapshot.EffectiveHeight >= snapshot.ExpiresHeight {
		return types.CandidatePoolSnapshotState{}, fmt.Errorf("candidate pool snapshot header is invalid")
	}
	switch snapshot.Status {
	case candidateSnapshotReady, candidateSnapshotActive, candidateSnapshotExpired:
		if snapshot.PrunedHeight != 0 {
			return types.CandidatePoolSnapshotState{}, fmt.Errorf("unpruned candidate snapshot has pruned_height")
		}
	case candidateSnapshotPruned:
		if snapshot.PrunedHeight == 0 || snapshot.TaskRefCount != 0 {
			return types.CandidatePoolSnapshotState{}, fmt.Errorf("pruned candidate snapshot is invalid")
		}
	default:
		return types.CandidatePoolSnapshotState{}, fmt.Errorf("candidate pool snapshot status is invalid")
	}
	return snapshot, nil
}

func (k Keeper) loadCandidatePoolMember(ctx context.Context, snapshot types.CandidatePoolSnapshotState, slot uint32) (types.CandidatePoolMemberState, error) {
	member, err := k.CandidatePoolMember.Get(ctx, types.NewCandidatePoolMemberKey(snapshot.Epoch, slot))
	if err != nil {
		return types.CandidatePoolMemberState{}, err
	}
	if member.Epoch != snapshot.Epoch || member.Slot != slot || member.SlotVersion == 0 || len(member.BindingHash) != 32 {
		return types.CandidatePoolMemberState{}, fmt.Errorf("candidate pool member state is invalid")
	}
	binding, err := k.CandidateSlotBinding.Get(ctx, types.NewCandidateSlotBindingKey(slot, member.SlotVersion))
	if err != nil {
		return types.CandidatePoolMemberState{}, err
	}
	operatorBytes, canonical, err := k.requireCanonicalAddress("operator_address", member.OperatorAddress)
	if err != nil || canonical != binding.OperatorAddress || binding.Slot != slot || binding.SlotVersion != member.SlotVersion || !equalCandidateBytes(binding.BindingHash, member.BindingHash) {
		return types.CandidatePoolMemberState{}, fmt.Errorf("candidate member/binding join is invalid")
	}
	want, err := types.CandidateSlotBindingHash(slot, member.SlotVersion, operatorBytes, binding.AllocatedEpoch)
	if err != nil || !equalCandidateBytes(want, binding.BindingHash) {
		return types.CandidatePoolMemberState{}, fmt.Errorf("candidate binding hash is invalid")
	}
	return member, nil
}

func (k Keeper) verifyCandidatePoolBody(ctx context.Context, snapshot types.CandidatePoolSnapshotState) error {
	if snapshot.Status == candidateSnapshotPruned {
		return fmt.Errorf("candidate pool body has been pruned")
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}
	members, err := k.candidateMembersForEpoch(ctx, snapshot.Epoch)
	if err != nil {
		return err
	}
	segments, err := k.candidateSegmentsForHash(ctx, snapshot.Epoch, snapshot.SlotCapacity, params.CandidatePool.CandidateBitmapSegmentBytes)
	if err != nil {
		return err
	}
	for _, segment := range segments {
		if candidateBitmapEmpty(segment.Bitmap) {
			continue
		}
		segmentMembers := candidateMemberRefsWithinSegment(members, segment.SegmentIndex, params.CandidatePool.CandidateBitmapSegmentBytes)
		wantSegmentHash, err := types.CandidatePoolSegmentHash(
			snapshot.Epoch, segment.SegmentIndex, segment.Bitmap, uint32(len(segmentMembers)), segmentMembers,
		)
		if err != nil {
			return err
		}
		stored, err := k.CandidatePoolActiveSegment.Get(ctx, types.NewCandidatePoolSegmentKey(snapshot.Epoch, segment.SegmentIndex))
		if err != nil || !equalCandidateBytes(stored.SegmentHash, wantSegmentHash) {
			return fmt.Errorf("candidate segment %d commitment mismatch", segment.SegmentIndex)
		}
	}
	bitmapHash, err := types.CandidateActiveBitmapHash(snapshot.SlotCapacity, uint32(len(segments)), segments)
	if err != nil {
		return err
	}
	memberHash, err := types.CandidateMemberSetHash(uint32(len(members)), members)
	if err != nil {
		return err
	}
	poolHash, err := types.CandidatePoolHash(candidatePoolChainID(ctx), snapshot.Epoch, snapshot.SlotCapacity, snapshot.EffectiveHeight, snapshot.ExpiresHeight, snapshot.ActiveCount, bitmapHash, memberHash)
	if err != nil {
		return err
	}
	snapshotID, err := types.CandidatePoolSnapshotID(candidatePoolChainID(ctx), snapshot.Epoch, poolHash)
	if err != nil {
		return err
	}
	if uint32(len(members)) != snapshot.ActiveCount || !equalCandidateBytes(bitmapHash, snapshot.ActiveBitmapHash) || !equalCandidateBytes(memberHash, snapshot.MemberSetHash) || !equalCandidateBytes(poolHash, snapshot.PoolHash) || !equalCandidateBytes(snapshotID, snapshot.SnapshotId) {
		return fmt.Errorf("candidate pool body commitment mismatch")
	}
	return nil
}
