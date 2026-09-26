package keeper

import (
	"bytes"
	"context"
	"fmt"
	"sort"

	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// verifierWindowClock is produced once, when the InferReceipt is accepted. No
// later runner accepts current parameters as an input.
type verifierWindowClock struct {
	WindowRandomnessHeight     uint64
	BuilderProposalCloseHeight uint64
	HandraiseCloseHeight       uint64
	SelectionRandomnessHeight  uint64
	AssignmentDeadlineHeight   uint64
}

type verifierWindowClockParams struct {
	DeltaWBlocks                uint64
	BuilderProposalWindowBlocks uint64
	SelfRescueMarginBlocks      uint64
	VerifyOpenDeadlineBlocks    uint64
}

func freezeVerifierWindowClock(receiptHeight uint64, params verifierWindowClockParams) (verifierWindowClock, error) {
	window, overflow := checkedHeightAdd(receiptHeight, params.DeltaWBlocks)
	if overflow {
		return verifierWindowClock{}, fmt.Errorf("verifier window height overflow")
	}
	builderClose, overflow := checkedHeightAdd(window, params.BuilderProposalWindowBlocks)
	if overflow {
		return verifierWindowClock{}, fmt.Errorf("builder proposal close height overflow")
	}
	handraiseClose, overflow := checkedHeightAdd(builderClose, params.SelfRescueMarginBlocks)
	if overflow {
		return verifierWindowClock{}, fmt.Errorf("handraise close height overflow")
	}
	selection, overflow := checkedHeightAdd(handraiseClose, params.DeltaWBlocks)
	if overflow {
		return verifierWindowClock{}, fmt.Errorf("selection randomness height overflow")
	}
	assignmentDue, overflow := checkedHeightAdd(receiptHeight, params.VerifyOpenDeadlineBlocks)
	if overflow {
		return verifierWindowClock{}, fmt.Errorf("assignment deadline height overflow")
	}
	if !(receiptHeight < window && window < builderClose && builderClose < handraiseClose && handraiseClose < selection && selection < assignmentDue) {
		return verifierWindowClock{}, fmt.Errorf("verifier window clock is not strictly increasing")
	}
	return verifierWindowClock{
		WindowRandomnessHeight: window, BuilderProposalCloseHeight: builderClose,
		HandraiseCloseHeight: handraiseClose, SelectionRandomnessHeight: selection,
		AssignmentDeadlineHeight: assignmentDue,
	}, nil
}

func verifierWindowSize(eligibleCount, ratioPpm, minimum, maximum uint32) (uint32, error) {
	if ratioPpm > 1_000_000 || minimum > maximum {
		return 0, fmt.Errorf("verifier window sizing parameters are invalid")
	}
	if eligibleCount == 0 {
		return 0, nil
	}
	size := uint32(uint64(eligibleCount) * uint64(ratioPpm) / 1_000_000)
	if size < minimum {
		size = minimum
	}
	if size > maximum {
		size = maximum
	}
	if size > eligibleCount {
		size = eligibleCount
	}
	return size, nil
}

type verifierWindowCandidate struct {
	Slot            uint32
	SlotVersion     uint64
	OperatorAddress string
}

type verifierEligibilityFacts struct {
	Profile            hubtypes.ProfileStateSnapshot
	Capability         hubtypes.ProfileCapabilitySnapshot
	Support            hubtypes.ModelSupportSnapshot
	Bond               hubtypes.ServiceBondSnapshot
	PerformanceScore   uint32
	PerformanceVersion uint32
	JailFactor         uint32
}

// loadVerifierEligibilityFacts revalidates the Hub-owned hard eligibility
// facts used both when the receipt source is frozen and when a handraise is
// accepted. REGISTERED profiles permit declared, fresh cold-start support;
// ACTIVE profiles additionally require the support activation bit.
func (k Keeper) loadVerifierEligibilityFacts(
	ctx context.Context,
	core types.TaskCoreState,
	operatorBytes []byte,
	operator string,
	hubParams hubtypes.HubParamsSnapshot,
) (verifierEligibilityFacts, error) {
	var zero verifierEligibilityFacts
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if sdkCtx.BlockHeight() < 0 || hubParams.EpochLengthBlocks == 0 {
		return zero, fmt.Errorf("authoritative Hub epoch length is unavailable")
	}
	address := sdk.AccAddress(operatorBytes)
	node, ok := k.hubKeeper.GetCortexNode(sdkCtx, address)
	if !ok || node.OperatorAddress != operator || node.ServiceKeyStatus != hubtypes.ServiceKeyStatusActive {
		return zero, fmt.Errorf("verifier service binding is unavailable")
	}
	profile, ok := k.hubKeeper.GetProfileState(sdkCtx, core.ModelId, core.ProfileVersion)
	if !ok || (profile.Status != hubtypes.ModelStatusRegistered && profile.Status != hubtypes.ModelStatusActive) ||
		!isParentModelOpenForProfile(k.hubKeeper.GetModelStatus(sdkCtx, core.ModelId)) ||
		k.hubKeeper.IsProfileFrozen(sdkCtx, core.ModelId, core.ProfileVersion) {
		return zero, fmt.Errorf("profile is not available")
	}
	capability, ok := k.hubKeeper.GetProfileCapability(sdkCtx, address, core.ModelId, core.ProfileVersion)
	if !ok || !capability.VerificationCapability || capability.CapabilityVersion == 0 {
		return zero, fmt.Errorf("verifier capability is unavailable")
	}
	support, ok := k.hubKeeper.GetModelSupport(sdkCtx, address, core.ModelId, core.ProfileVersion)
	currentEpoch := uint64(sdkCtx.BlockHeight()) / hubParams.EpochLengthBlocks
	if !ok || !support.DeclaredSupport || support.SupportVersion == 0 ||
		support.SupportFreshUntilEpoch == 0 || currentEpoch >= support.SupportFreshUntilEpoch {
		return zero, fmt.Errorf("verifier profile support is unavailable or stale")
	}
	orderValue, err := shared.ParseAmount(core.OrderValue)
	if err != nil || orderValue == 0 {
		return zero, fmt.Errorf("task order value is invalid")
	}
	bond, ok := k.hubKeeper.GetServiceBond(sdkCtx, address, orderValue)
	if !ok || !hubtypes.IsCandidateEligibleBondStatus(bond.Status) ||
		bond.PendingUnbonding >= bond.EffectiveActiveBond || bond.RequiredTaskLiability == 0 ||
		bond.EffectiveActiveBond < profile.MinStake || bond.AvailableBond < bond.RequiredTaskLiability {
		return zero, fmt.Errorf("verifier bond cannot cover stake and liability")
	}
	// Only tombstone hard-invalidates. GetNodeJailStatus already reports
	// TOMBSTONED once jail_count reaches the threshold, so this keeps the whole
	// ejection rule while leaving jail_count 1/2 to the graduated factor below.
	if k.hubKeeper.GetNodeJailStatus(sdkCtx, address, hubtypes.ServiceBondRoleVerifier) == hubtypes.JailStatusTombstoned ||
		k.hubKeeper.GetNodeTombstone(sdkCtx, address) {
		return zero, fmt.Errorf("verifier is hard-invalidated")
	}
	scoring, err := k.hubKeeper.GetRoleScoringSnapshot(sdkCtx, address, hubtypes.ServiceBondRoleVerifier)
	if err != nil {
		return zero, err
	}
	if scoring.PerformanceScorePpm == 0 || scoring.PerformanceScorePpm > uint64(types.PartsPerMillion) ||
		scoring.PerformanceVersion == 0 || scoring.PerformanceVersion > uint64(^uint32(0)) {
		return zero, fmt.Errorf("verifier scoring snapshot is outside the registered range")
	}
	jailFactor := candidateJailFactorPpm(scoring.JailCount)
	if jailFactor == 0 {
		return zero, fmt.Errorf("verifier is hard-invalidated")
	}
	return verifierEligibilityFacts{
		Profile: profile, Capability: capability, Support: support, Bond: bond,
		PerformanceScore: uint32(scoring.PerformanceScorePpm), PerformanceVersion: uint32(scoring.PerformanceVersion),
		JailFactor: jailFactor,
	}, nil
}

// deriveVerifierEligibilityCandidates scans the Task-locked CandidatePool body
// once at receipt acceptance. The bitmap is only a stable identity source;
// profile, duty, service, bond and hard-invalidation eligibility are read from
// their authoritative Hub rows at the receipt height.
func (k Keeper) deriveVerifierEligibilityCandidates(
	ctx context.Context,
	core types.TaskCoreState,
	assignment types.TaskAssignmentState,
	hubParams hubtypes.HubParamsSnapshot,
) (uint32, uint32, []verifierWindowCandidate, error) {
	if len(core.TaskId) != types.Hash32Len || len(assignment.CandidatePoolSnapshotId) != types.Hash32Len ||
		len(assignment.CandidatePoolHash) != types.Hash32Len || assignment.CandidatePoolRefReleased {
		return 0, 0, nil, fmt.Errorf("Task-locked candidate pool commitment is unavailable")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	snapshot, found := k.hubKeeper.GetCandidatePoolSnapshot(sdkCtx, assignment.CandidatePoolSnapshotId)
	if !found || !bytes.Equal(snapshot.SnapshotId, assignment.CandidatePoolSnapshotId) ||
		!bytes.Equal(snapshot.PoolHash, assignment.CandidatePoolHash) || snapshot.SchemaVersion != 1 ||
		(snapshot.Status != hubtypes.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_ACTIVE &&
			snapshot.Status != hubtypes.CandidatePoolSnapshotStatus_CANDIDATE_POOL_SNAPSHOT_STATUS_EXPIRED) {
		return 0, 0, nil, fmt.Errorf("Task assignment candidate pool commitment does not match the authoritative snapshot")
	}
	if retained, err := k.hubKeeper.HasCandidatePoolTaskRef(ctx, core.TaskId, assignment.CandidatePoolSnapshotId); err != nil {
		return 0, 0, nil, err
	} else if !retained {
		return 0, 0, nil, fmt.Errorf("Task-locked candidate pool has no retention reference")
	}
	slotCapacity, segmentBytes, segmentCount, ok := k.hubKeeper.GetCandidatePoolLayout(ctx, assignment.CandidatePoolSnapshotId)
	if !ok || slotCapacity == 0 || slotCapacity != snapshot.SlotCapacity || segmentBytes == 0 || segmentCount == 0 ||
		hubParams.CandidateSlotHardCapacity == 0 || slotCapacity > hubParams.CandidateSlotHardCapacity {
		return 0, 0, nil, fmt.Errorf("Task-locked candidate pool layout exceeds the registered hard capacity")
	}
	expectedSegments, err := bitmapSegmentCount(slotCapacity, segmentBytes)
	if err != nil || expectedSegments != segmentCount {
		return 0, 0, nil, fmt.Errorf("Task-locked candidate pool layout is inconsistent")
	}

	poolSegments := make([]taskBitmapSegment, segmentCount)
	for segmentIndex := uint32(0); segmentIndex < segmentCount; segmentIndex++ {
		bitmap, err := k.hubKeeper.CandidatePoolSegmentBitmap(ctx, assignment.CandidatePoolSnapshotId, segmentIndex)
		if err != nil {
			return 0, 0, nil, fmt.Errorf("candidate pool segment %d: %w", segmentIndex, err)
		}
		if uint32(len(bitmap)) != segmentBytes {
			return 0, 0, nil, fmt.Errorf("candidate pool segment %d has invalid width", segmentIndex)
		}
		poolSegments[segmentIndex] = taskBitmapSegment{Index: segmentIndex, Bitmap: bitmap}
	}
	if err := validateBitmapTrailingBits(slotCapacity, segmentBytes, poolSegments); err != nil {
		return 0, 0, nil, err
	}

	eligible := make([]verifierWindowCandidate, 0)
	var activeSlots uint32
	for slot := uint32(0); slot < slotCapacity; slot++ {
		segmentIndex, byteIndex, mask, err := bitmapLocation(slot, slotCapacity, segmentBytes)
		if err != nil {
			return 0, 0, nil, err
		}
		if poolSegments[segmentIndex].Bitmap[byteIndex]&mask == 0 {
			continue
		}
		activeSlots++
		member, binding, err := k.hubKeeper.ResolveCandidatePoolMember(ctx, assignment.CandidatePoolSnapshotId, slot)
		if err != nil {
			return 0, 0, nil, fmt.Errorf("active candidate pool slot %d: %w", slot, err)
		}
		operatorBytes, operator, err := k.canonicalAddress("verifier_operator_address", member.OperatorAddress)
		if err != nil || member.Slot != slot || member.SlotVersion == 0 || binding.Slot != slot ||
			binding.SlotVersion != member.SlotVersion || binding.OperatorAddress != operator ||
			!bytes.Equal(member.BindingHash, binding.BindingHash) {
			return 0, 0, nil, fmt.Errorf("active candidate pool slot %d has an invalid immutable binding", slot)
		}
		if operator == assignment.WinnerWorker {
			continue
		}
		if _, err := k.loadVerifierEligibilityFacts(ctx, core, operatorBytes, operator, hubParams); err != nil {
			continue
		}
		eligible = append(eligible, verifierWindowCandidate{
			Slot: slot, SlotVersion: member.SlotVersion, OperatorAddress: operator,
		})
	}
	if activeSlots != snapshot.ActiveCount {
		return 0, 0, nil, fmt.Errorf("Task-locked candidate pool active count does not match its bitmap")
	}
	return slotCapacity, segmentBytes, eligible, nil
}

func (k Keeper) loadVerifierEligibilitySource(
	ctx context.Context,
	header types.VerifierCandidateWindowState,
	slotCapacity, segmentBytes uint32,
) ([]types.VerifierCandidateEligibilitySegmentState, []verifierWindowCandidate, uint64, error) {
	segmentCount, err := bitmapSegmentCount(slotCapacity, segmentBytes)
	if err != nil || segmentCount != header.EligibilitySegmentCount {
		return nil, nil, 0, fmt.Errorf("verifier eligibility layout does not match its header")
	}
	segments := make([]types.VerifierCandidateEligibilitySegmentState, segmentCount)
	candidates := make([]verifierWindowCandidate, 0, header.EligibleCount)
	var rowBytes uint64
	for segmentIndex := uint32(0); segmentIndex < segmentCount; segmentIndex++ {
		segment := types.VerifierCandidateEligibilitySegmentState{
			TaskId: append([]byte(nil), header.TaskId...), VerifyRound: header.VerifyRound,
			SegmentIndex: segmentIndex, Bitmap: make([]byte, segmentBytes),
		}
		stored, err := k.VerifierCandidateEligibilitySegment.Get(ctx,
			types.NewVerifierEligibilitySegmentKey(types.NewTaskKey(header.TaskId), header.VerifyRound, segmentIndex))
		if err == nil {
			segment = stored
			rowBytes += uint64(stored.Size())
		} else if !errIsNotFound(err) {
			return nil, nil, rowBytes, err
		}
		if !bytes.Equal(segment.TaskId, header.TaskId) || segment.VerifyRound != header.VerifyRound ||
			segment.SegmentIndex != segmentIndex || uint32(len(segment.Bitmap)) != segmentBytes {
			return nil, nil, rowBytes, fmt.Errorf("verifier eligibility segment %d is non-canonical", segmentIndex)
		}
		segments[segmentIndex] = segment
	}
	if sourceHash, err := verifierWindowSourceHash(sdk.UnwrapSDKContext(ctx).ChainID(), header, slotCapacity, segmentBytes, segments); err != nil {
		return nil, nil, rowBytes, err
	} else if !bytes.Equal(sourceHash[:], header.VerifierWindowSourceHash) {
		return nil, nil, rowBytes, fmt.Errorf("verifier eligibility source commitment mismatch")
	}
	for slot := uint32(0); slot < slotCapacity; slot++ {
		segmentIndex, byteIndex, mask, err := bitmapLocation(slot, slotCapacity, segmentBytes)
		if err != nil {
			return nil, nil, rowBytes, err
		}
		if segments[segmentIndex].Bitmap[byteIndex]&mask == 0 {
			continue
		}
		member, binding, err := k.hubKeeper.ResolveCandidatePoolMember(ctx, header.CandidatePoolSnapshotId, slot)
		if err != nil || member.Slot != slot || member.SlotVersion == 0 || binding.Slot != slot ||
			binding.SlotVersion != member.SlotVersion || binding.OperatorAddress != member.OperatorAddress ||
			!bytes.Equal(member.BindingHash, binding.BindingHash) {
			return nil, nil, rowBytes, fmt.Errorf("frozen verifier slot %d no longer resolves to its immutable binding", slot)
		}
		candidates = append(candidates, verifierWindowCandidate{
			Slot: slot, SlotVersion: member.SlotVersion, OperatorAddress: member.OperatorAddress,
		})
	}
	if uint32(len(candidates)) != header.EligibleCount {
		return nil, nil, rowBytes, fmt.Errorf("resolved verifier eligibility count does not match its header")
	}
	return segments, candidates, rowBytes, nil
}

func (k Keeper) materializeVerifierWindowAtHeight(
	ctx context.Context,
	taskID types.TaskKey,
	verifyRound uint32,
	currentHeight uint64,
) (bool, uint64, error) {
	if !isPhase0VerifyRound(verifyRound) {
		return false, 0, fmt.Errorf("unsupported verifier round")
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	windowKey := types.NewVerifyRoundKey(taskID, verifyRound)
	header, err := k.VerifierCandidateWindow.Get(cache, windowKey)
	if err != nil {
		return false, 0, err
	}
	rowBytes := uint64(header.Size())
	if header.Status != types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_SOURCE_FROZEN ||
		header.WindowRandomnessHeight == 0 || currentHeight < header.WindowRandomnessHeight {
		return false, rowBytes, fmt.Errorf("verifier window is not a due frozen source")
	}
	if currentHeight > header.AssignmentDeadlineHeight || header.WindowSize == 0 {
		return false, rowBytes, nil
	}
	// currentHeight is a real block height and the guard above already rejects a
	// frozen height above it, so this check is implied. State it anyway: the
	// implication is a two-step inference, and the next reader should not have to
	// redo it before trusting the int64 conversion on the following line.
	if !shared.IsBlockHeightV1(header.WindowRandomnessHeight) {
		return false, rowBytes, fmt.Errorf("verifier window randomness height leaves the block height domain")
	}
	beacon, found := k.hubKeeper.GetBeaconForDomain(
		cacheCtx, shared.DomainVerifierWindowRankV1, int64(header.WindowRandomnessHeight),
	)
	if !found {
		return false, rowBytes, nil
	}
	if beacon.Height != int64(header.WindowRandomnessHeight) || len(beacon.Randomness) != types.Hash32Len {
		return false, rowBytes, fmt.Errorf("verifier window beacon does not match its frozen height")
	}
	slotCapacity, segmentBytes, segmentCount, ok := k.hubKeeper.GetCandidatePoolLayout(cache, header.CandidatePoolSnapshotId)
	if !ok || segmentCount != header.EligibilitySegmentCount {
		return false, rowBytes, fmt.Errorf("verifier window candidate pool layout is unavailable")
	}
	segments, candidates, sourceBytes, err := k.loadVerifierEligibilitySource(cache, header, slotCapacity, segmentBytes)
	rowBytes += sourceBytes
	if err != nil {
		return false, rowBytes, err
	}
	ready, members, err := materializeVerifierCandidateWindow(
		cacheCtx.ChainID(), header, slotCapacity, segmentBytes, segments, candidates,
		beacon.Randomness, currentHeight,
	)
	if err != nil {
		return false, rowBytes, err
	}
	taskKey := types.NewTaskKey(taskID)
	core, err := k.TaskCore.Get(cache, taskKey)
	if err != nil || !bytes.Equal(core.TaskId, header.TaskId) ||
		core.ReceiptStatus != types.ReceiptStatus_RECEIPT_STATUS_RECEIPT_ACCEPTED {
		return false, rowBytes, fmt.Errorf("verifier window Task core scope is inconsistent")
	}
	if err := k.validateActiveVerifierStage(cache, taskKey, core, verifyRound,
		types.VerificationStatus_VERIFICATION_STATUS_VERIFIER_WINDOW_PENDING); err != nil {
		return false, rowBytes, fmt.Errorf("verifier window Task core scope is inconsistent: %w", err)
	}
	rowBytes += uint64(core.Size())
	closeKey := types.NewVerifyRoundIndexKey(header.HandraiseCloseHeight, taskID, verifyRound)
	if exists, err := k.VerifierHandraiseCloseIndex.Has(cache, closeKey); err != nil || !exists {
		if err != nil {
			return false, rowBytes, err
		}
		return false, rowBytes, fmt.Errorf("verifier handraise close index is unavailable")
	}
	deadlineKey := types.NewDeadlineIndexKey(header.AssignmentDeadlineHeight, taskID)
	if exists, err := k.VerifyOpenDeadlineIndex.Has(cache, deadlineKey); err != nil || !exists {
		if err != nil {
			return false, rowBytes, err
		}
		return false, rowBytes, fmt.Errorf("verify-open deadline index is unavailable")
	}
	for _, member := range members {
		memberKey := types.NewVerifierWindowMemberKey(taskKey, verifyRound, member.RankIndex)
		if exists, err := k.VerifierCandidateWindowMember.Has(cache, memberKey); err != nil || exists {
			if err != nil {
				return false, rowBytes, err
			}
			return false, rowBytes, fmt.Errorf("SOURCE_FROZEN verifier window already has a member row")
		}
		if err := k.WriteVerifierWindowMember(cache, memberKey, member); err != nil {
			return false, rowBytes, err
		}
		rowBytes += uint64(member.Size())
	}
	core.VerificationStatus = types.VerificationStatus_VERIFICATION_STATUS_VERIFY_COLLECTION_OPEN
	core.UpdatedHeight = currentHeight
	if err := k.VerifierCandidateWindow.Set(cache, windowKey, ready); err != nil {
		return false, rowBytes, err
	}
	if err := k.TaskCore.Set(cache, taskKey, core); err != nil {
		return false, rowBytes, err
	}
	if err := k.VerifierWindowBuildIndex.Remove(cache,
		types.NewVerifyRoundIndexKey(header.WindowRandomnessHeight, taskID, verifyRound)); err != nil {
		return false, rowBytes, err
	}
	write()
	return true, rowBytes, nil
}

func freezeVerifierEligibilitySource(
	chainID string,
	header types.VerifierCandidateWindowState,
	slotCapacity, segmentBytes uint32,
	eligible []verifierWindowCandidate,
) (types.VerifierCandidateWindowState, []types.VerifierCandidateEligibilitySegmentState, error) {
	if chainID == "" || len(header.TaskId) != types.Hash32Len || len(header.InferReceiptHash) != types.Hash32Len ||
		len(header.CandidatePoolSnapshotId) != types.Hash32Len || len(header.CandidatePoolHash) != types.Hash32Len ||
		header.SchemaVersion != 1 || !isPhase0VerifyRound(header.VerifyRound) || header.EligibilityFrozenHeight == 0 {
		return types.VerifierCandidateWindowState{}, nil, fmt.Errorf("verifier eligibility scope is incomplete")
	}
	segmentCount, err := bitmapSegmentCount(slotCapacity, segmentBytes)
	if err != nil {
		return types.VerifierCandidateWindowState{}, nil, err
	}
	segments := make([]types.VerifierCandidateEligibilitySegmentState, segmentCount)
	for index := range segments {
		segments[index] = types.VerifierCandidateEligibilitySegmentState{
			TaskId: append([]byte(nil), header.TaskId...), VerifyRound: header.VerifyRound,
			SegmentIndex: uint32(index), Bitmap: make([]byte, segmentBytes),
		}
	}
	ordered := append([]verifierWindowCandidate(nil), eligible...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Slot < ordered[j].Slot })
	seenOperators := make(map[string]struct{}, len(ordered))
	for index, candidate := range ordered {
		if candidate.SlotVersion == 0 {
			return types.VerifierCandidateWindowState{}, nil, fmt.Errorf("eligible slot %d has zero slot_version", candidate.Slot)
		}
		_, operator, err := canonicalWindowOperator(candidate.OperatorAddress)
		if err != nil {
			return types.VerifierCandidateWindowState{}, nil, err
		}
		if index != 0 && candidate.Slot == ordered[index-1].Slot {
			return types.VerifierCandidateWindowState{}, nil, fmt.Errorf("duplicate eligible slot %d", candidate.Slot)
		}
		if _, exists := seenOperators[operator]; exists {
			return types.VerifierCandidateWindowState{}, nil, fmt.Errorf("duplicate eligible operator %s", operator)
		}
		seenOperators[operator] = struct{}{}
		segmentIndex, byteIndex, mask, err := bitmapLocation(candidate.Slot, slotCapacity, segmentBytes)
		if err != nil {
			return types.VerifierCandidateWindowState{}, nil, err
		}
		segments[segmentIndex].Bitmap[byteIndex] |= mask
	}
	header.EligibleCount = uint32(len(ordered))
	header.EligibilitySegmentCount = segmentCount
	if header.WindowSize > header.EligibleCount {
		return types.VerifierCandidateWindowState{}, nil, fmt.Errorf("window_size exceeds eligible_count")
	}
	sourceHash, err := verifierWindowSourceHash(chainID, header, slotCapacity, segmentBytes, segments)
	if err != nil {
		return types.VerifierCandidateWindowState{}, nil, err
	}
	header.VerifierWindowSourceHash = sourceHash[:]
	header.Status = types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_SOURCE_FROZEN
	header.XWindowRandomnessBeacon = nil
	header.XVerifierWindowHash = nil
	header.GeneratedHeight = 0
	return header, segments, nil
}

func verifierWindowSourceHash(
	chainID string,
	header types.VerifierCandidateWindowState,
	slotCapacity, segmentBytes uint32,
	segments []types.VerifierCandidateEligibilitySegmentState,
) ([32]byte, error) {
	var zero [32]byte
	if chainID == "" || len(header.TaskId) != types.Hash32Len || len(header.InferReceiptHash) != types.Hash32Len ||
		len(header.CandidatePoolSnapshotId) != types.Hash32Len || len(header.CandidatePoolHash) != types.Hash32Len {
		return zero, fmt.Errorf("verifier window source scope is incomplete")
	}
	segmentCount, err := bitmapSegmentCount(slotCapacity, segmentBytes)
	if err != nil {
		return zero, err
	}
	if header.EligibilitySegmentCount != segmentCount || uint32(len(segments)) != segmentCount {
		return zero, fmt.Errorf("eligibility segment count does not match pool layout")
	}
	ordered := append([]types.VerifierCandidateEligibilitySegmentState(nil), segments...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].SegmentIndex < ordered[j].SegmentIndex })
	bitmapSegments := make([]taskBitmapSegment, len(ordered))
	elements := make([]shared.CanonicalFrameV1, 0, len(ordered))
	var population uint32
	builder := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainVerifierWindowSourceV1)).Raw(
		[]byte(chainID), header.TaskId, shared.Uint32BE(header.VerifyRound), header.InferReceiptHash,
		header.CandidatePoolSnapshotId, header.CandidatePoolHash, shared.Uint64BE(header.EligibilityFrozenHeight),
		shared.Uint32BE(header.EligibleCount), shared.Uint32BE(header.EligibilitySegmentCount),
	)
	for index, segment := range ordered {
		if !bytes.Equal(segment.TaskId, header.TaskId) || segment.VerifyRound != header.VerifyRound ||
			segment.SegmentIndex != uint32(index) || len(segment.Bitmap) != int(segmentBytes) {
			return zero, fmt.Errorf("eligibility segment %d is non-canonical", index)
		}
		bitmapSegments[index] = taskBitmapSegment{Index: segment.SegmentIndex, Bitmap: segment.Bitmap}
		for _, value := range segment.Bitmap {
			population += uint32(bitsSet8(value))
		}
		elements = append(elements, shared.FlatCanonicalFrameV1(shared.Uint32BE(segment.SegmentIndex), segment.Bitmap))
	}
	if err := validateBitmapTrailingBits(slotCapacity, segmentBytes, bitmapSegments); err != nil {
		return zero, err
	}
	if population != header.EligibleCount {
		return zero, fmt.Errorf("eligible_count %d does not match bitmap population %d", header.EligibleCount, population)
	}
	digest, err := builder.Nested(shared.CanonicalRepeatedFramesV1(elements)).Sum()
	if err != nil {
		return zero, err
	}
	return [32]byte(digest), nil
}

func materializeVerifierCandidateWindow(
	chainID string,
	header types.VerifierCandidateWindowState,
	slotCapacity, segmentBytes uint32,
	segments []types.VerifierCandidateEligibilitySegmentState,
	candidates []verifierWindowCandidate,
	randomness []byte,
	generatedHeight uint64,
) (types.VerifierCandidateWindowState, []types.VerifierCandidateWindowMemberState, error) {
	if header.Status != types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_SOURCE_FROZEN ||
		len(randomness) != types.Hash32Len || header.WindowRandomnessHeight == 0 || generatedHeight < header.WindowRandomnessHeight {
		return types.VerifierCandidateWindowState{}, nil, fmt.Errorf("verifier window is not ready to materialize")
	}
	sourceHash, err := verifierWindowSourceHash(chainID, header, slotCapacity, segmentBytes, segments)
	if err != nil {
		return types.VerifierCandidateWindowState{}, nil, err
	}
	if !bytes.Equal(sourceHash[:], header.VerifierWindowSourceHash) {
		return types.VerifierCandidateWindowState{}, nil, fmt.Errorf("verifier window source commitment mismatch")
	}
	if uint32(len(candidates)) != header.EligibleCount || header.WindowSize > header.EligibleCount {
		return types.VerifierCandidateWindowState{}, nil, fmt.Errorf("eligible candidate count does not match frozen source")
	}
	orderedSegments := append([]types.VerifierCandidateEligibilitySegmentState(nil), segments...)
	sort.Slice(orderedSegments, func(i, j int) bool { return orderedSegments[i].SegmentIndex < orderedSegments[j].SegmentIndex })

	type rankedCandidate struct {
		candidate verifierWindowCandidate
		rank      [32]byte
	}
	ranked := make([]rankedCandidate, 0, len(candidates))
	seenSlots := make(map[uint32]struct{}, len(candidates))
	seenOperators := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		segmentIndex, byteIndex, mask, err := bitmapLocation(candidate.Slot, slotCapacity, segmentBytes)
		if err != nil {
			return types.VerifierCandidateWindowState{}, nil, err
		}
		if orderedSegments[segmentIndex].Bitmap[byteIndex]&mask == 0 {
			return types.VerifierCandidateWindowState{}, nil, fmt.Errorf("candidate slot %d is outside frozen eligibility", candidate.Slot)
		}
		if candidate.SlotVersion == 0 {
			return types.VerifierCandidateWindowState{}, nil, fmt.Errorf("candidate slot_version is zero")
		}
		_, operator, err := canonicalWindowOperator(candidate.OperatorAddress)
		if err != nil {
			return types.VerifierCandidateWindowState{}, nil, err
		}
		if _, exists := seenSlots[candidate.Slot]; exists {
			return types.VerifierCandidateWindowState{}, nil, fmt.Errorf("duplicate candidate slot %d", candidate.Slot)
		}
		if _, exists := seenOperators[operator]; exists {
			return types.VerifierCandidateWindowState{}, nil, fmt.Errorf("duplicate candidate operator %s", operator)
		}
		seenSlots[candidate.Slot] = struct{}{}
		seenOperators[operator] = struct{}{}
		candidate.OperatorAddress = operator
		rank, err := verifierWindowRank(chainID, header, randomness, candidate)
		if err != nil {
			return types.VerifierCandidateWindowState{}, nil, err
		}
		ranked = append(ranked, rankedCandidate{candidate: candidate, rank: rank})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if comparison := bytes.Compare(ranked[i].rank[:], ranked[j].rank[:]); comparison != 0 {
			return comparison < 0
		}
		return ranked[i].candidate.Slot < ranked[j].candidate.Slot
	})
	members := make([]types.VerifierCandidateWindowMemberState, header.WindowSize)
	for index := range members {
		candidate := ranked[index]
		members[index] = types.VerifierCandidateWindowMemberState{
			SchemaVersion: 1, TaskId: append([]byte(nil), header.TaskId...), VerifyRound: header.VerifyRound,
			RankIndex: uint32(index), Slot: candidate.candidate.Slot, SlotVersion: candidate.candidate.SlotVersion,
			OperatorAddress: candidate.candidate.OperatorAddress, VerifierWindowRank: candidate.rank[:],
		}
	}
	windowHash, err := verifierWindowHash(chainID, header, randomness, members)
	if err != nil {
		return types.VerifierCandidateWindowState{}, nil, err
	}
	header.XWindowRandomnessBeacon = &types.VerifierCandidateWindowState_WindowRandomnessBeacon{WindowRandomnessBeacon: append([]byte(nil), randomness...)}
	header.XVerifierWindowHash = &types.VerifierCandidateWindowState_VerifierWindowHash{VerifierWindowHash: windowHash[:]}
	header.Status = types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY
	header.GeneratedHeight = generatedHeight
	return header, members, nil
}

func verifierWindowRank(chainID string, header types.VerifierCandidateWindowState, randomness []byte, candidate verifierWindowCandidate) ([32]byte, error) {
	var zero [32]byte
	if chainID == "" || len(header.TaskId) != types.Hash32Len || len(header.InferReceiptHash) != types.Hash32Len ||
		len(header.CandidatePoolSnapshotId) != types.Hash32Len || len(header.CandidatePoolHash) != types.Hash32Len ||
		len(header.VerifierWindowSourceHash) != types.Hash32Len || len(randomness) != types.Hash32Len || candidate.SlotVersion == 0 {
		return zero, fmt.Errorf("verifier rank scope is incomplete")
	}
	operator, _, err := canonicalWindowOperator(candidate.OperatorAddress)
	if err != nil {
		return zero, err
	}
	digest, err := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainVerifierWindowRankV1)).Raw(
		[]byte(chainID), header.TaskId, shared.Uint32BE(header.VerifyRound), header.InferReceiptHash,
		header.CandidatePoolSnapshotId, header.CandidatePoolHash, header.VerifierWindowSourceHash,
		shared.Uint64BE(header.WindowRandomnessHeight), randomness, shared.Uint32BE(candidate.Slot),
		shared.Uint64BE(candidate.SlotVersion), operator,
	).Sum()
	if err != nil {
		return zero, err
	}
	return [32]byte(digest), nil
}

func verifierWindowHash(
	chainID string,
	header types.VerifierCandidateWindowState,
	randomness []byte,
	members []types.VerifierCandidateWindowMemberState,
) ([32]byte, error) {
	var zero [32]byte
	if chainID == "" || len(header.TaskId) != types.Hash32Len || len(header.InferReceiptHash) != types.Hash32Len ||
		len(header.CandidatePoolSnapshotId) != types.Hash32Len || len(header.CandidatePoolHash) != types.Hash32Len ||
		len(header.VerifierWindowSourceHash) != types.Hash32Len || uint32(len(members)) != header.WindowSize ||
		len(randomness) != types.Hash32Len {
		return zero, fmt.Errorf("verifier window member count or randomness is invalid")
	}
	builder := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainVerifierWindowV1)).Raw(
		[]byte(chainID), header.TaskId, shared.Uint32BE(header.VerifyRound), header.InferReceiptHash,
		header.CandidatePoolSnapshotId, header.CandidatePoolHash, header.VerifierWindowSourceHash,
		shared.Uint32BE(header.EligibleCount), shared.Uint64BE(header.WindowRandomnessHeight), randomness,
		shared.Uint32BE(header.WindowSize),
	)
	elements := make([]shared.CanonicalFrameV1, 0, len(members))
	seenOperators := make(map[string]struct{}, len(members))
	for index, member := range members {
		if member.RankIndex != uint32(index) || member.SchemaVersion != 1 || !bytes.Equal(member.TaskId, header.TaskId) ||
			member.VerifyRound != header.VerifyRound || member.SlotVersion == 0 || len(member.VerifierWindowRank) != types.Hash32Len {
			return zero, fmt.Errorf("verifier window member %d is non-canonical", index)
		}
		operator, canonical, err := canonicalWindowOperator(member.OperatorAddress)
		if err != nil {
			return zero, err
		}
		if _, exists := seenOperators[canonical]; exists {
			return zero, fmt.Errorf("duplicate verifier window operator %s", canonical)
		}
		seenOperators[canonical] = struct{}{}
		elements = append(elements, shared.FlatCanonicalFrameV1(
			shared.Uint32BE(member.RankIndex), shared.Uint32BE(member.Slot), shared.Uint64BE(member.SlotVersion),
			operator, member.VerifierWindowRank,
		))
	}
	digest, err := builder.Nested(shared.CanonicalRepeatedFramesV1(elements)).Sum()
	if err != nil {
		return zero, err
	}
	return [32]byte(digest), nil
}

func verifierCandidateWeightPpm(
	activeBond, minStake uint64,
	performance, jailFactor, stakeWeight, performanceWeight, maximum uint32,
) (uint32, error) {
	if activeBond == 0 || minStake == 0 || performance == 0 || performance > types.PartsPerMillion ||
		jailFactor == 0 || jailFactor > types.PartsPerMillion ||
		uint64(stakeWeight)+uint64(performanceWeight) != uint64(types.PartsPerMillion) || maximum == 0 {
		return 0, fmt.Errorf("verifier candidate weight inputs are invalid")
	}
	stakeScore := stakeScorePpm(activeBond, minStake)
	weighted := (stakeScore*uint64(stakeWeight) + uint64(performance)*uint64(performanceWeight)) / uint64(types.PartsPerMillion)
	weight := weighted * uint64(jailFactor) / uint64(types.PartsPerMillion)
	if weight == 0 || weight > uint64(maximum) {
		return 0, fmt.Errorf("verifier candidate weight is outside the registered range")
	}
	return uint32(weight), nil
}

func (k Keeper) freezeVerifierCandidateFact(
	ctx context.Context,
	core types.TaskCoreState,
	assignment types.TaskAssignmentState,
	receipt types.InferReceiptState,
	window types.VerifierCandidateWindowState,
	member types.VerifierCandidateWindowMemberState,
	handraise types.VerifierHandraiseV1,
	acceptedHeight uint64,
) (types.TaskCandidateFactState, error) {
	if handraise.SchemaVersion != types.VerifierHandraiseSchemaVersionV1 || handraise.Duty != shared.Duty_DUTY_VERIFIER ||
		handraise.ChainId != sdk.UnwrapSDKContext(ctx).ChainID() || !bytes.Equal(handraise.TaskId, core.TaskId) ||
		handraise.VerifyRound != window.VerifyRound || !bytes.Equal(handraise.InferReceiptHash, receipt.InferReceiptHash) ||
		!bytes.Equal(handraise.OutputHash, receipt.OutputHash) || handraise.ModelId != core.ModelId ||
		handraise.ProfileVersion != core.ProfileVersion {
		return types.TaskCandidateFactState{}, fmt.Errorf("verifier handraise scope is invalid")
	}
	if handraise.ExpiryHeight == 0 || acceptedHeight > handraise.ExpiryHeight || acceptedHeight > window.HandraiseCloseHeight {
		return types.TaskCandidateFactState{}, fmt.Errorf("verifier handraise expired")
	}
	if !bytes.Equal(handraise.Member.CandidatePoolSnapshotId, window.CandidatePoolSnapshotId) ||
		handraise.Member.Slot != member.Slot || handraise.Member.SlotVersion != member.SlotVersion ||
		handraise.Member.OperatorAddress != member.OperatorAddress {
		return types.TaskCandidateFactState{}, fmt.Errorf("verifier handraise window member mismatch")
	}
	operatorBytes, operator, err := k.canonicalAddress("verifier_operator_address", handraise.Member.OperatorAddress)
	if err != nil {
		return types.TaskCandidateFactState{}, err
	}
	if operator == assignment.WinnerWorker {
		return types.TaskCandidateFactState{}, fmt.Errorf("winner worker cannot verify the same task")
	}
	poolMember, binding, err := k.hubKeeper.ResolveCandidatePoolMember(ctx, window.CandidatePoolSnapshotId, member.Slot)
	if err != nil {
		return types.TaskCandidateFactState{}, fmt.Errorf("candidate pool member: %w", err)
	}
	if poolMember.SlotVersion != member.SlotVersion || poolMember.OperatorAddress != operator ||
		binding.SlotVersion != member.SlotVersion || binding.OperatorAddress != operator || !bytes.Equal(poolMember.BindingHash, binding.BindingHash) {
		return types.TaskCandidateFactState{}, fmt.Errorf("candidate pool member binding mismatch")
	}
	node, ok := k.hubKeeper.GetCortexNode(sdk.UnwrapSDKContext(ctx), sdk.AccAddress(operatorBytes))
	if !ok || node.OperatorAddress != operator || node.ServiceKeyStatus != hubtypes.ServiceKeyStatusActive ||
		node.ServiceAuthorizationNonce != handraise.ServiceAuthorizationNonce {
		return types.TaskCandidateFactState{}, fmt.Errorf("current service binding mismatch")
	}
	hubParams := k.hubKeeper.GetHubParams(sdk.UnwrapSDKContext(ctx))
	eligibility, err := k.loadVerifierEligibilityFacts(ctx, core, operatorBytes, operator, hubParams)
	if err != nil {
		return types.TaskCandidateFactState{}, err
	}
	if len(assignment.ProfileExecutionSnapshotHash) != types.Hash32Len ||
		len(eligibility.Profile.ExecutionSnapshotHash) != types.Hash32Len ||
		!bytes.Equal(assignment.ProfileExecutionSnapshotHash, eligibility.Profile.ExecutionSnapshotHash) {
		return types.TaskCandidateFactState{}, fmt.Errorf("locked Profile execution snapshot is unavailable")
	}
	params, err := k.Params.Get(ctx)
	if err != nil {
		return types.TaskCandidateFactState{}, err
	}
	weight, err := verifierCandidateWeightPpm(
		eligibility.Bond.EffectiveActiveBond, eligibility.Profile.MinStake,
		eligibility.PerformanceScore, eligibility.JailFactor,
		params.Weights.VerifierStakeWeightPpm, params.Weights.VerifierPerformanceWeightPpm,
		params.Weights.CandidateWeightPpmMax,
	)
	if err != nil {
		return types.TaskCandidateFactState{}, err
	}
	digest, err := types.VerifierHandraiseSigningDigest(handraise)
	if err != nil {
		return types.TaskCandidateFactState{}, err
	}
	if err := k.hubKeeper.VerifyCurrentCortexServiceDigest(ctx, operator, handraise.ServiceSignature, digest[:], acceptedHeight); err != nil {
		return types.TaskCandidateFactState{}, err
	}
	return types.TaskCandidateFactState{
		SchemaVersion: types.AssignmentCandidateSchemaVersionV1,
		TaskId:        append([]byte(nil), core.TaskId...), Stage: types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY,
		Slot: member.Slot, SlotVersion: member.SlotVersion, OperatorAddress: operator, Duty: shared.Duty_DUTY_VERIFIER,
		ActiveBondSnapshot: shared.NewAmount(eligibility.Bond.EffectiveActiveBond), AvailableBondSnapshot: shared.NewAmount(eligibility.Bond.AvailableBond),
		RequiredTaskLiabilitySnapshot: shared.NewAmount(eligibility.Bond.RequiredTaskLiability), MinStakeSnapshot: shared.NewAmount(eligibility.Profile.MinStake),
		PerformanceScoreSnapshotPpm: eligibility.PerformanceScore, PerformanceMethodVersion: eligibility.PerformanceVersion,
		CandidateJailFactorSnapshotPpm: eligibility.JailFactor, BondVersionSnapshot: eligibility.Bond.BondVersion,
		CapabilityVersionSnapshot: eligibility.Capability.CapabilityVersion,
		SupportVersionSnapshot:    eligibility.Support.SupportVersion, CandidateWeight: weight, HandraiseSigningDigest: digest[:],
	}, nil
}

func verifierLegalSetHash(
	chainID string,
	header types.VerifierCandidateWindowState,
	union types.TaskStageHandraiseUnionState,
	members []types.VerifierCandidateWindowMemberState,
	facts []types.TaskCandidateFactState,
) ([32]byte, error) {
	var zero [32]byte
	if chainID == "" || header.Status != types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY ||
		len(header.GetVerifierWindowHash()) != types.Hash32Len || union.Stage != types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY ||
		union.Status != types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_FINALIZING ||
		!bytes.Equal(header.TaskId, union.TaskId) || !bytes.Equal(header.CandidatePoolSnapshotId, union.CandidatePoolSnapshotId) ||
		!bytes.Equal(header.CandidatePoolHash, union.CandidatePoolHash) || len(union.UnionBitmapHash) != types.Hash32Len ||
		union.WindowCloseHeight != header.HandraiseCloseHeight ||
		union.GetSelectionRandomnessHeight() != header.SelectionRandomnessHeight || uint32(len(facts)) != union.UnionCount {
		return zero, fmt.Errorf("verifier legal set scope is inconsistent")
	}
	recomputedWindowHash, err := verifierWindowHash(chainID, header, header.GetWindowRandomnessBeacon(), members)
	if err != nil {
		return zero, err
	}
	if !bytes.Equal(recomputedWindowHash[:], header.GetVerifierWindowHash()) {
		return zero, fmt.Errorf("verifier window commitment mismatch")
	}
	windowBySlot := make(map[uint32]types.VerifierCandidateWindowMemberState, len(members))
	for _, member := range members {
		windowBySlot[member.Slot] = member
	}
	ordered := append([]types.TaskCandidateFactState(nil), facts...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Slot < ordered[j].Slot })
	builder := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainVerifierLegalSetV1)).Raw(
		[]byte(chainID), header.TaskId, shared.Uint32BE(header.VerifyRound), header.CandidatePoolSnapshotId,
		header.CandidatePoolHash, header.GetVerifierWindowHash(), union.UnionBitmapHash, shared.Uint32BE(union.UnionCount),
	)
	elements := make([]shared.CanonicalFrameV1, 0, len(ordered))
	for index, fact := range ordered {
		if err := validateVerifierCandidateFact(header.TaskId, fact); err != nil {
			return zero, fmt.Errorf("verifier candidate fact %d: %w", index, err)
		}
		member, inWindow := windowBySlot[fact.Slot]
		if !inWindow || member.SlotVersion != fact.SlotVersion || member.OperatorAddress != fact.OperatorAddress {
			return zero, fmt.Errorf("verifier candidate fact %d is outside the frozen window", index)
		}
		if index != 0 && fact.Slot == ordered[index-1].Slot {
			return zero, fmt.Errorf("duplicate verifier candidate slot %d", fact.Slot)
		}
		operator, err := types.CanonicalOperatorAddressBytes("verifier_operator_address", fact.OperatorAddress)
		if err != nil {
			return zero, err
		}
		activeBond, err := shared.ParseAmount(fact.ActiveBondSnapshot)
		if err != nil {
			return zero, err
		}
		availableBond, err := shared.ParseAmount(fact.AvailableBondSnapshot)
		if err != nil {
			return zero, err
		}
		requiredLiability, err := shared.ParseAmount(fact.RequiredTaskLiabilitySnapshot)
		if err != nil {
			return zero, err
		}
		minStake, err := shared.ParseAmount(fact.MinStakeSnapshot)
		if err != nil {
			return zero, err
		}
		elements = append(elements, shared.FlatCanonicalFrameV1(
			shared.Uint32BE(fact.Slot), shared.Uint64BE(fact.SlotVersion), operator,
			shared.Uint32BE(fact.CandidateWeight), shared.Uint64BE(activeBond), shared.Uint64BE(availableBond),
			shared.Uint64BE(requiredLiability), shared.Uint64BE(minStake), shared.Uint32BE(fact.PerformanceScoreSnapshotPpm),
			shared.Uint32BE(fact.PerformanceMethodVersion), shared.Uint32BE(fact.CandidateJailFactorSnapshotPpm),
			shared.Uint64BE(fact.BondVersionSnapshot), shared.Uint64BE(fact.SupportVersionSnapshot),
		))
	}
	digest, err := builder.Nested(shared.CanonicalRepeatedFramesV1(elements)).Sum()
	if err != nil {
		return zero, err
	}
	return [32]byte(digest), nil
}

func validateVerifierCandidateFact(taskID []byte, fact types.TaskCandidateFactState) error {
	if fact.SchemaVersion != types.AssignmentCandidateSchemaVersionV1 || !bytes.Equal(taskID, fact.TaskId) ||
		fact.Stage != types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY || fact.Duty != shared.Duty_DUTY_VERIFIER ||
		fact.SlotVersion == 0 || fact.CandidateWeight == 0 || fact.CandidateWeight > types.MaxAssignmentCandidateWeight ||
		fact.PerformanceScoreSnapshotPpm == 0 || fact.PerformanceScoreSnapshotPpm > types.PartsPerMillion || fact.PerformanceMethodVersion == 0 ||
		fact.CandidateJailFactorSnapshotPpm == 0 || fact.CandidateJailFactorSnapshotPpm > 1_000_000 ||
		fact.BondVersionSnapshot == 0 || fact.CapabilityVersionSnapshot == 0 || fact.SupportVersionSnapshot == 0 ||
		len(fact.HandraiseSigningDigest) != types.Hash32Len {
		return fmt.Errorf("verifier candidate fact is non-canonical")
	}
	return nil
}

func selectedVerifiersHash(
	chainID string,
	taskID []byte,
	verifyRound uint32,
	legalSetHash []byte,
	randomnessHeight uint64,
	randomness []byte,
	selected []weightedDrawResult,
) ([32]byte, []types.SelectedVerifierV1, error) {
	var zero [32]byte
	if chainID == "" || len(taskID) != types.Hash32Len || !isPhase0VerifyRound(verifyRound) || len(legalSetHash) != types.Hash32Len ||
		len(randomness) != types.Hash32Len || len(selected) == 0 {
		return zero, nil, fmt.Errorf("selected verifier scope is incomplete")
	}
	refs := make([]types.SelectedVerifierV1, len(selected))
	for index, result := range selected {
		refs[index] = types.SelectedVerifierV1{
			OperatorAddress: result.Fact.OperatorAddress, Slot: result.Fact.Slot, SlotVersion: result.Fact.SlotVersion,
		}
	}
	digest, err := SelectedVerifierRefsHash(chainID, taskID, verifyRound, legalSetHash, randomnessHeight, randomness, refs)
	if err != nil {
		return zero, nil, err
	}
	return [32]byte(digest), refs, nil
}

// SelectedVerifierRefsHash recomputes the selected_verifiers_hash retained by
// VerifierAssignmentState from its frozen ordered references.
func SelectedVerifierRefsHash(
	chainID string,
	taskID []byte,
	verifyRound uint32,
	legalSetHash []byte,
	randomnessHeight uint64,
	randomness []byte,
	selected []types.SelectedVerifierV1,
) ([]byte, error) {
	if chainID == "" || len(taskID) != types.Hash32Len || !isPhase0VerifyRound(verifyRound) || len(legalSetHash) != types.Hash32Len ||
		randomnessHeight == 0 || len(randomness) != types.Hash32Len || len(selected) == 0 {
		return nil, fmt.Errorf("selected verifier scope is incomplete")
	}
	builder := shared.NewCanonicalHashBuilderV1(shared.MustDomain(shared.DomainSelectedVerifiersV1)).Raw(
		[]byte(chainID), taskID, shared.Uint32BE(verifyRound), legalSetHash,
		shared.Uint64BE(randomnessHeight), randomness, shared.Uint32BE(uint32(len(selected))),
	)
	elements := make([]shared.CanonicalFrameV1, 0, len(selected))
	seenSlots := make(map[uint32]struct{}, len(selected))
	seenOperators := make(map[string]struct{}, len(selected))
	for index, ref := range selected {
		if ref.SlotVersion == 0 {
			return nil, fmt.Errorf("selected verifier slot %d has no slot_version", index)
		}
		if _, exists := seenSlots[ref.Slot]; exists {
			return nil, fmt.Errorf("selected verifier slot %d repeats", ref.Slot)
		}
		seenSlots[ref.Slot] = struct{}{}
		operator, err := types.CanonicalOperatorAddressBytes("selected_verifier", ref.OperatorAddress)
		if err != nil {
			return nil, err
		}
		if _, exists := seenOperators[string(operator)]; exists {
			return nil, fmt.Errorf("selected verifier operator at index %d repeats", index)
		}
		seenOperators[string(operator)] = struct{}{}
		elements = append(elements, shared.FlatCanonicalFrameV1(
			shared.Uint32BE(uint32(index)), shared.Uint32BE(ref.Slot),
			shared.Uint64BE(ref.SlotVersion), operator,
		))
	}
	return builder.Nested(shared.CanonicalRepeatedFramesV1(elements)).Sum()
}

func buildVerifierAssignment(
	chainID string,
	header types.VerifierCandidateWindowState,
	union types.TaskStageHandraiseUnionState,
	members []types.VerifierCandidateWindowMemberState,
	facts []types.TaskCandidateFactState,
	randomness []byte,
	selectedCount uint32,
	maxAttempts uint64,
	openHeight, commitDeadlineHeight, verifyDeadlineHeight uint64,
) (types.VerifierAssignmentState, error) {
	if header.SelectionRandomnessHeight <= header.WindowRandomnessHeight || len(randomness) != types.Hash32Len ||
		openHeight > header.AssignmentDeadlineHeight || !(openHeight < commitDeadlineHeight && commitDeadlineHeight < verifyDeadlineHeight) {
		return types.VerifierAssignmentState{}, fmt.Errorf("verifier assignment clock is invalid")
	}
	legalSetHash, err := verifierLegalSetHash(chainID, header, union, members, facts)
	if err != nil {
		return types.VerifierAssignmentState{}, err
	}
	selected, err := drawWeightedCandidatesWithoutReplacement(
		chainID, header.TaskId, legalSetHash[:], header.SelectionRandomnessHeight,
		randomness, selectedCount, maxAttempts, facts,
	)
	if err != nil {
		return types.VerifierAssignmentState{}, err
	}
	selectedHash, refs, err := selectedVerifiersHash(
		chainID, header.TaskId, header.VerifyRound, legalSetHash[:],
		header.SelectionRandomnessHeight, randomness, selected,
	)
	if err != nil {
		return types.VerifierAssignmentState{}, err
	}
	return types.VerifierAssignmentState{
		TaskId: append([]byte(nil), header.TaskId...), VerifyRound: header.VerifyRound, OpenVerifyHeight: openHeight,
		VerifierCandidateWindowHash: append([]byte(nil), header.GetVerifierWindowHash()...), VerifierLegalSetHash: legalSetHash[:],
		SelectedVerifiersHash: selectedHash[:], SelectionRandomnessHeight: header.SelectionRandomnessHeight,
		SelectionRandomnessBeacon: append([]byte(nil), randomness...), SelectedVerifiers: refs,
		SelectedVerifierCount: uint32(len(refs)), VerifierHandraiseCount: union.UnionCount,
		CommitDeadlineHeight: commitDeadlineHeight, VerifyDeadlineHeight: verifyDeadlineHeight,
	}, nil
}

func canonicalWindowOperator(value string) ([]byte, string, error) {
	operator, err := types.CanonicalOperatorAddressBytes("operator_address", value)
	if err != nil {
		return nil, "", err
	}
	// CanonicalOperatorAddressBytes validates the display form; retaining it is
	// safe because the frozen State stores that exact canonical string.
	return operator, value, nil
}
