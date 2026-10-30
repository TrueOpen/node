package keeper

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	"github.com/TrueOpen/node/x/task/types"
	"github.com/TrueOpen/wire/dispatch"
	"github.com/cosmos/gogoproto/proto"
)

func dispatchRound(record types.DispatchRecordState, number uint32) (*types.DispatchRoundV1, error) {
	for _, round := range record.Rounds {
		if round != nil && round.DispatchRound == number {
			return round, nil
		}
	}
	return nil, fmt.Errorf("dispatch round is absent")
}

func (k Keeper) committedDispatchRoster(ctx context.Context, record types.DispatchRecordState, number uint32, roster *types.DispatchRosterV1, params types.DispatchParamsV1) ([]dispatch.RosterMember, error) {
	round, err := dispatchRound(record, number)
	if err != nil {
		return nil, err
	}
	if roster == nil || uint64(roster.Size()) > params.MaxDispatchRosterBytes || !bytes.Equal(roster.TaskId, record.TaskId) || roster.ReferenceHeight != record.ReferenceHeight || !bytes.Equal(roster.CandidatePoolSnapshotId, record.CandidatePoolSnapshotId) {
		return nil, fmt.Errorf("roster does not match the retained round scope")
	}
	members := make([]dispatch.RosterMember, len(roster.Members))
	for i, member := range roster.Members {
		if member == nil {
			return nil, fmt.Errorf("roster contains a nil member")
		}
		members[i] = dispatch.RosterMember{Slot: member.Slot, SlotVersion: member.SlotVersion, OperatorAddress: member.OperatorAddress, CandidateWeight: member.CandidateWeight}
	}
	var taskID, snapshotID [32]byte
	copy(taskID[:], record.TaskId)
	copy(snapshotID[:], record.CandidatePoolSnapshotId)
	hash, err := dispatch.RosterDigest(sdkContextFrom(ctx).ChainID(), taskID, record.ReferenceHeight, snapshotID, members)
	if err != nil || !bytes.Equal(hash[:], round.RosterHash) {
		return nil, fmt.Errorf("roster commitment mismatch: %w", err)
	}
	return members, nil
}

func (k Keeper) frozenDispatchPool(ctx context.Context, record types.DispatchRecordState, core types.TaskCoreState) ([]dispatch.RosterMember, error) {
	snapshot, found := k.hubKeeper.GetCandidatePoolSnapshot(sdkContextFrom(ctx), record.CandidatePoolSnapshotId)
	if !found || snapshot.PublishedHeight == 0 || snapshot.Status == hubtypes.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_PRUNED || snapshot.Epoch != record.VrfEpoch {
		return nil, fmt.Errorf("activated dispatch snapshot is unavailable")
	}
	capacity, segmentBytes, segmentCount, found := k.hubKeeper.GetCandidatePoolLayout(ctx, record.CandidatePoolSnapshotId)
	if !found || capacity == 0 || capacity != snapshot.SlotCapacity || capacity > k.hubKeeper.GetHubParams(sdkContextFrom(ctx)).CandidateSlotHardCapacity {
		return nil, fmt.Errorf("dispatch pool layout is invalid")
	}
	expected, err := bitmapSegmentCount(capacity, segmentBytes)
	if err != nil || expected != segmentCount {
		return nil, fmt.Errorf("dispatch pool segment layout is invalid")
	}
	segments := make([]taskBitmapSegment, segmentCount)
	for i := uint32(0); i < segmentCount; i++ {
		bitmap, err := k.hubKeeper.CandidatePoolSegmentBitmap(ctx, record.CandidatePoolSnapshotId, i)
		if err != nil {
			return nil, err
		}
		if uint32(len(bitmap)) != segmentBytes {
			return nil, fmt.Errorf("dispatch pool bitmap width is invalid")
		}
		segments[i] = taskBitmapSegment{Index: i, Bitmap: bitmap}
	}
	if err := validateBitmapTrailingBits(capacity, segmentBytes, segments); err != nil {
		return nil, err
	}
	var pool []dispatch.RosterMember
	var active uint32
	for slot := uint32(0); slot < capacity; slot++ {
		segment, index, mask, err := bitmapLocation(slot, capacity, segmentBytes)
		if err != nil {
			return nil, err
		}
		if segments[segment].Bitmap[index]&mask == 0 {
			continue
		}
		active++
		member, binding, err := k.hubKeeper.ResolveCandidatePoolMember(ctx, record.CandidatePoolSnapshotId, slot)
		if err != nil {
			return nil, err
		}
		if member.Slot != slot || binding.Slot != slot || member.SlotVersion != binding.SlotVersion || member.OperatorAddress != binding.OperatorAddress || !bytes.Equal(member.BindingHash, binding.BindingHash) || member.WeightInputs == nil {
			return nil, fmt.Errorf("dispatch snapshot member/binding is invalid")
		}
		var profile *hubtypes.CandidateProfileWeightInputV1
		for _, input := range member.WeightInputs.Profiles {
			if input != nil && input.ProfileVersion == core.ProfileVersion && bytes.Equal(input.ModelId, core.ModelId) {
				profile = input
				break
			}
		}
		weight, err := hubtypes.FrozenWorkerCandidateWeight(snapshot, member.WeightInputs, profile)
		if err != nil {
			return nil, err
		}
		if weight != 0 {
			pool = append(pool, dispatch.RosterMember{Slot: slot, SlotVersion: member.SlotVersion, OperatorAddress: member.OperatorAddress, CandidateWeight: uint64(weight)})
		}
	}
	if active != snapshot.ActiveCount {
		return nil, fmt.Errorf("dispatch pool member count does not match its bitmap")
	}
	return pool, nil
}

func dispatchRosterMemberEqual(a, b dispatch.RosterMember) bool {
	return a.Slot == b.Slot && a.SlotVersion == b.SlotVersion && a.OperatorAddress == b.OperatorAddress && a.CandidateWeight == b.CandidateWeight
}

func dispatchPoolMember(pool []dispatch.RosterMember, slot uint32) (dispatch.RosterMember, bool) {
	i := sort.Search(len(pool), func(i int) bool { return pool[i].Slot >= slot })
	if i == len(pool) || pool[i].Slot != slot {
		return dispatch.RosterMember{}, false
	}
	return pool[i], true
}

func dispatchRosterMember(roster []dispatch.RosterMember, operator string) (dispatch.RosterMember, bool) {
	for _, member := range roster {
		if member.OperatorAddress == operator {
			return member, true
		}
	}
	return dispatch.RosterMember{}, false
}

// dispatchExpectedOrder draws once over the complete profile pool. Rosters only
// filter it; sticky eligibility is evaluated against the first round exclusively.
func dispatchExpectedOrder(chain string, record types.DispatchRecordState, first, pool []dispatch.RosterMember, params types.TaskParamsV1, sequence uint64) ([]uint32, bool, error) {
	weighted := make([]dispatch.WeightedMember, len(pool))
	for i, member := range pool {
		weighted[i] = dispatch.WeightedMember{Slot: member.Slot, Weight: member.CandidateWeight}
	}
	var randomness [32]byte
	copy(randomness[:], record.VrfOutput)
	order, err := dispatch.DrawOrder(chain, randomness, weighted, params.Weights.MaxWeightedDrawAttemptsPerSelection)
	if err != nil {
		return nil, false, err
	}
	sticky := record.StickySnapshot
	if sticky == nil {
		return nil, false, fmt.Errorf("retained dispatch has no sticky snapshot")
	}
	winner := sticky.GetLastRecordWinner()
	member, inRoster := dispatchRosterMember(first, winner)
	_, inPool := dispatchPoolMember(pool, member.Slot)
	apply := winner != "" && inRoster && inPool && dispatchStickyWithinWindow(record.EarliestSubmitHeight, sticky.AnchorHeight, params.Dispatch.StickyWindowBlocks) && sticky.RunCount < params.Dispatch.StickyMaxOrders && sequence > sticky.AnchorSequence
	if apply {
		index := -1
		for i, slot := range order {
			if slot == member.Slot {
				index = i
				break
			}
		}
		if index < 0 {
			return nil, false, fmt.Errorf("sticky operator is absent from the eligible pool")
		}
		copy(order[1:index+1], order[:index])
		order[0] = member.Slot
	}
	return order, apply, nil
}

func (k Keeper) verifyOmissionRoot(ctx context.Context, record types.DispatchRecordState, round *types.DispatchRoundV1, evidence, bound *types.SignedHandraiseRootV1) error {
	if evidence == nil || evidence.BuilderAddress != record.BuilderAddress {
		return fmt.Errorf("omission root has another Builder")
	}
	digest, err := k.verifyDispatchMaterial(ctx, "task.v1.SignedHandraiseRootV1", evidence, &record.EarliestSubmitHeight)
	if err != nil {
		return err
	}
	if bound != nil {
		if bound.BuilderAddress != record.BuilderAddress {
			return fmt.Errorf("bound root has another Builder")
		}
		hash, err := k.verifyDispatchMaterial(ctx, "task.v1.SignedHandraiseRootV1", bound, &record.EarliestSubmitHeight)
		if err != nil {
			return err
		}
		if !bytes.Equal(hash[:], round.HandraiseRootDigest) {
			return fmt.Errorf("bound root preimage differs from the retained commitment")
		}
	}
	if bytes.Equal(digest[:], round.HandraiseRootDigest) {
		return nil
	}
	if bound == nil || evidence.RootHeight > record.EarliestSubmitHeight || evidence.RootHeight < bound.RootHeight || evidence.RootHeight == bound.RootHeight && evidence.RootVersion <= bound.RootVersion {
		return fmt.Errorf("evidence root is not a provably newer pre-dispatch root")
	}
	return nil
}

func (k Keeper) omissionVictim(ctx context.Context, record types.DispatchRecordState, root *types.SignedHandraiseRootV1, proof *types.HandraiseInclusionProofV1, core types.TaskCoreState) (*types.StandingHandraiseV1, error) {
	if proof == nil || proof.Leaf == nil || proof.Leaf.Handraise == nil {
		return nil, fmt.Errorf("handraise inclusion proof is required")
	}
	leaf, handraise := proof.Leaf, proof.Leaf.Handraise
	if !leaf.Admitted || handraise.FreeSlotCount == 0 || handraise.ExpiryHeight < record.RecordedHeight || handraise.BuilderAddress != record.BuilderAddress || handraise.ChainId != sdkContextFrom(ctx).ChainID() || handraise.Member == nil || !bytes.Equal(handraise.Member.CandidatePoolSnapshotId, record.CandidatePoolSnapshotId) {
		return nil, fmt.Errorf("handraise admission leaf is not eligible for this dispatch")
	}
	profile := false
	for _, p := range handraise.Profiles {
		if p != nil && p.ProfileVersion == core.ProfileVersion && bytes.Equal(p.ModelId, core.ModelId) {
			profile = true
			break
		}
	}
	if !profile {
		return nil, fmt.Errorf("handraise omits the Task profile")
	}
	// The signed Builder root authenticates the admitted leaf. Rechecking its
	// standing signature against a later Cortex key would erase honest omission
	// proofs after that Cortex rotates; this fault is against the root's Builder.
	raw, err := proto.Marshal(handraise)
	if err != nil {
		return nil, err
	}
	digest, err := dispatch.SigningDigest("task.v1.StandingHandraiseV1", raw)
	if err != nil {
		return nil, err
	}
	leafDigest, err := dispatch.HandraiseLeafDigest(digest, leaf.Admitted, uint32(leaf.AdmitRejectReason), leaf.BuilderMeasuredRttMs)
	if err != nil {
		return nil, err
	}
	var rootHash [32]byte
	if len(root.Root) != 32 {
		return nil, fmt.Errorf("handraise root width is invalid")
	}
	copy(rootHash[:], root.Root)
	siblings := make([][32]byte, len(proof.Siblings))
	for i, hash := range proof.Siblings {
		if len(hash) != 32 {
			return nil, fmt.Errorf("handraise sibling width is invalid")
		}
		copy(siblings[i][:], hash)
	}
	if !dispatch.VerifyHandraiseProof(rootHash, leafDigest, proof.LeafIndex, root.LeafCount, siblings) {
		return nil, fmt.Errorf("handraise inclusion path is invalid")
	}
	return handraise, nil
}

func verifyRosterAbsence(roster []dispatch.RosterMember, victim dispatch.RosterMember, proof *types.DispatchRosterNonInclusionProofV1) error {
	if proof == nil {
		return fmt.Errorf("roster non-inclusion proof is required")
	}
	i := sort.Search(len(roster), func(i int) bool { return roster[i].Slot >= victim.Slot })
	if i < len(roster) && roster[i].Slot == victim.Slot {
		return fmt.Errorf("victim is already in the committed roster")
	}
	equal := func(got *types.DispatchRosterMemberV1, want dispatch.RosterMember) bool {
		return got != nil && dispatchRosterMemberEqual(dispatch.RosterMember{Slot: got.Slot, SlotVersion: got.SlotVersion, OperatorAddress: got.OperatorAddress, CandidateWeight: got.CandidateWeight}, want)
	}
	if i == 0 && proof.GetPredecessor() != nil || i > 0 && !equal(proof.GetPredecessor(), roster[i-1]) || i == len(roster) && proof.GetSuccessor() != nil || i < len(roster) && !equal(proof.GetSuccessor(), roster[i]) {
		return fmt.Errorf("roster adjacency proof does not match committed neighbors")
	}
	return nil
}
