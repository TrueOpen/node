package keeper

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"sort"

	sdk "github.com/cosmos/cosmos-sdk/types"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

// Verifier-panel selection.
//
// The members of a panel round are chosen exactly as the Verifiers of a
// verification round are: the chain freezes the operators eligible at the
// opening height, ranks them into a candidate window with the window beacon,
// accepts hand-raises that a selected Task Builder relays for window members,
// and draws the panel from the hand-raisers with the selection beacon, weighted
// by stake, performance and jail factor. The commitments are the same ones
// (window source, window hash, proposal digest, legal set, selected set) and a
// hand-raise is the same signed VerifierHandraiseV1, with verify_round set to
// the panel round number.
//
// Only the storage differs. An audited task has finished its verification
// rounds, so a panel round keeps its window, hand-raises and proposals in one
// working row of its own and never touches the task's verification status or
// the round-keyed Verifier stores. Panel rounds are numbered after the
// verification rounds: an audit's first panel round is round 2.

const (
	// anchorAuditModePanel is the audit mode in which a Verifier panel decides.
	anchorAuditModePanel = types.AnchorAuditModePanel
	// anchorPanelFirstRound is the number of an audit's first panel round.
	// Round 1 is the task's own verification round.
	anchorPanelFirstRound = types.ChallengeVerifyRoundV1
	// anchorPanelMaxRounds bounds the panel rounds of one audit: the first
	// round, one automatic re-assignment and one re-audit.
	anchorPanelMaxRounds = uint32(3)
)

// isAnchorPanelRound reports a verify-round number a panel round may carry.
func isAnchorPanelRound(round uint32) bool {
	return round >= anchorPanelFirstRound && round < anchorPanelFirstRound+anchorPanelMaxRounds
}

// isAnchorPanelAudit reports an audit decided by a Verifier panel.
func isAnchorPanelAudit(state types.AnchorAuditState) bool {
	return state.AuditMode == anchorAuditModePanel
}

// anchorPanelSelection is the decoded working row of a panel round in
// selection.
type anchorPanelSelection struct {
	TaskID     []byte
	PanelRound uint32
	Window     types.VerifierCandidateWindowState
	Segments   []types.VerifierCandidateEligibilitySegmentState
	Members    []types.VerifierCandidateWindowMemberState
	Union      *types.TaskStageHandraiseUnionState
	Facts      []types.TaskCandidateFactState
	Proposals  []types.BuilderStageProposalState
}

func (s anchorPanelSelection) toStore() (internaltypes.AnchorPanelSelectionStoreState, error) {
	out := internaltypes.AnchorPanelSelectionStoreState{TaskId: append([]byte(nil), s.TaskID...), PanelRound: s.PanelRound}
	var err error
	if out.Window, err = s.Window.Marshal(); err != nil {
		return out, err
	}
	for i := range s.Segments {
		raw, err := s.Segments[i].Marshal()
		if err != nil {
			return out, err
		}
		out.EligibilitySegments = append(out.EligibilitySegments, raw)
	}
	for i := range s.Members {
		raw, err := s.Members[i].Marshal()
		if err != nil {
			return out, err
		}
		out.Members = append(out.Members, raw)
	}
	if s.Union != nil {
		if out.Union, err = s.Union.Marshal(); err != nil {
			return out, err
		}
	}
	for i := range s.Facts {
		raw, err := s.Facts[i].Marshal()
		if err != nil {
			return out, err
		}
		out.Facts = append(out.Facts, raw)
	}
	for i := range s.Proposals {
		raw, err := s.Proposals[i].Marshal()
		if err != nil {
			return out, err
		}
		out.Proposals = append(out.Proposals, raw)
	}
	return out, nil
}

func anchorPanelSelectionFromStore(stored internaltypes.AnchorPanelSelectionStoreState) (anchorPanelSelection, error) {
	out := anchorPanelSelection{TaskID: append([]byte(nil), stored.TaskId...), PanelRound: stored.PanelRound}
	if err := out.Window.Unmarshal(stored.Window); err != nil {
		return out, err
	}
	out.Segments = make([]types.VerifierCandidateEligibilitySegmentState, len(stored.EligibilitySegments))
	for i, raw := range stored.EligibilitySegments {
		if err := out.Segments[i].Unmarshal(raw); err != nil {
			return out, err
		}
	}
	out.Members = make([]types.VerifierCandidateWindowMemberState, len(stored.Members))
	for i, raw := range stored.Members {
		if err := out.Members[i].Unmarshal(raw); err != nil {
			return out, err
		}
	}
	if len(stored.Union) != 0 {
		out.Union = &types.TaskStageHandraiseUnionState{}
		if err := out.Union.Unmarshal(stored.Union); err != nil {
			return out, err
		}
	}
	out.Facts = make([]types.TaskCandidateFactState, len(stored.Facts))
	for i, raw := range stored.Facts {
		if err := out.Facts[i].Unmarshal(raw); err != nil {
			return out, err
		}
	}
	out.Proposals = make([]types.BuilderStageProposalState, len(stored.Proposals))
	for i, raw := range stored.Proposals {
		if err := out.Proposals[i].Unmarshal(raw); err != nil {
			return out, err
		}
	}
	if !bytes.Equal(out.Window.TaskId, out.TaskID) || out.Window.VerifyRound != out.PanelRound || !isAnchorPanelRound(out.PanelRound) {
		return out, fmt.Errorf("panel selection row does not match its window")
	}
	return out, nil
}

func (k Keeper) loadAnchorPanelSelection(ctx context.Context, taskKey types.TaskKey) (anchorPanelSelection, bool, error) {
	stored, err := k.AnchorPanelSelection.Get(ctx, taskKey)
	if err != nil {
		if errIsNotFound(err) {
			return anchorPanelSelection{}, false, nil
		}
		return anchorPanelSelection{}, false, err
	}
	selection, err := anchorPanelSelectionFromStore(stored)
	if err != nil {
		return anchorPanelSelection{}, false, err
	}
	return selection, true, nil
}

func (k Keeper) saveAnchorPanelSelection(ctx context.Context, taskKey types.TaskKey, selection anchorPanelSelection) error {
	stored, err := selection.toStore()
	if err != nil {
		return err
	}
	return k.AnchorPanelSelection.Set(ctx, taskKey, stored)
}

// dropAnchorPanelSelection removes a round's working row and releases what the
// row held: the pending stage duty of every accepted hand-raiser and the two
// beacon references. It is a no-op when the row is already gone.
func (k Keeper) dropAnchorPanelSelection(ctx context.Context, taskKey types.TaskKey) error {
	selection, found, err := k.loadAnchorPanelSelection(ctx, taskKey)
	if err != nil {
		// An unreadable row cannot be released field by field; remove it so it
		// cannot block the audit.
		return k.AnchorPanelSelection.Remove(ctx, taskKey)
	}
	if !found {
		return nil
	}
	if err := k.releaseCandidateStageDuties(ctx, selection.Facts); err != nil {
		return err
	}
	consumer := hex.EncodeToString(taskKey)
	if err := k.hubKeeper.ReleaseBeaconConsumerRef(ctx, selection.Window.WindowRandomnessHeight,
		hubtypes.BeaconConsumerKindVerifierWindow, consumer); err != nil {
		return err
	}
	if err := k.hubKeeper.ReleaseBeaconConsumerRef(ctx, selection.Window.SelectionRandomnessHeight,
		hubtypes.BeaconConsumerKindVerifierSelection, consumer); err != nil {
		return err
	}
	return k.AnchorPanelSelection.Remove(ctx, taskKey)
}

// anchorPanelEligibilityFacts are the live Hub facts that admit an operator to
// a panel window and weight its hand-raise.
type anchorPanelEligibilityFacts struct {
	Capability         hubtypes.ModelCapabilitySnapshot
	Support            hubtypes.ModelSupportSnapshot
	Bond               hubtypes.ServiceBondSnapshot
	MinStake           uint64
	PerformanceScore   uint32
	PerformanceVersion uint32
	JailFactor         uint32
}

// loadAnchorPanelEligibilityFacts is the panel counterpart of
// loadVerifierEligibilityFacts. An operator is eligible for a panel of a task
// when, at this height:
//
//   - its service key is active;
//   - the task's model and Profile version are open and not frozen;
//   - it declared audit capability for the model, that capability is past its
//     waiting epoch, and its support declaration is fresh;
//   - its bond is live or jailed-but-recovering, is not being withdrawn in
//     full and covers the minimum stake the task froze;
//   - it is not tombstoned and its jail factor is not zero;
//   - it is not a block-producer identity, where that rule is in force.
//
// Verification capability and a task liability are not required: a panel
// member checks a proof and serves no model.
func (k Keeper) loadAnchorPanelEligibilityFacts(
	ctx context.Context,
	core types.TaskCoreState,
	assignment types.TaskAssignmentState,
	operatorBytes []byte,
	operator string,
	hubParams hubtypes.HubParamsSnapshot,
) (anchorPanelEligibilityFacts, error) {
	var zero anchorPanelEligibilityFacts
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if sdkCtx.BlockHeight() < 0 || hubParams.EpochLengthBlocks == 0 {
		return zero, fmt.Errorf("authoritative Hub epoch length is unavailable")
	}
	minStake, err := shared.ParseAmount(assignment.MinStakeSnapshot)
	if err != nil || minStake == 0 {
		return zero, fmt.Errorf("task minimum stake snapshot is unavailable")
	}
	address := sdk.AccAddress(operatorBytes)
	node, ok := k.hubKeeper.GetCortexNode(sdkCtx, address)
	if !ok || node.OperatorAddress != operator || node.ServiceKeyStatus != hubtypes.ServiceKeyStatusActive {
		return zero, fmt.Errorf("panel member service binding is unavailable")
	}
	serviceKey, err := k.hubKeeper.GetCurrentServiceKey(ctx, shared.ParticipantTypeCortexNode, operator)
	if err != nil {
		return zero, err
	}
	modelStatus := k.hubKeeper.GetModelStatus(sdkCtx, core.ModelId)
	profile, ok := k.hubKeeper.GetProfileState(sdkCtx, core.ModelId, core.ProfileVersion)
	if !ok || (profile.Status != hubtypes.ModelStatusRegistered && profile.Status != hubtypes.ModelStatusActive) ||
		!isParentModelOpenForProfile(modelStatus) ||
		k.hubKeeper.IsProfileFrozen(sdkCtx, core.ModelId, core.ProfileVersion) {
		return zero, fmt.Errorf("profile is not available")
	}
	currentEpoch := uint64(sdkCtx.BlockHeight()) / hubParams.EpochLengthBlocks
	capability, ok := k.hubKeeper.GetModelCapability(sdkCtx, address, core.ModelId)
	if !ok || !capability.AuditCapability || capability.CapabilityVersion == 0 ||
		currentEpoch < capability.AuditEffectiveFromEpoch {
		return zero, fmt.Errorf("audit capability is unavailable or not effective yet")
	}
	support, ok := k.hubKeeper.GetModelSupport(sdkCtx, address, core.ModelId)
	if !ok || !support.DeclaredSupport || support.SupportVersion == 0 ||
		support.SupportFreshUntilEpoch == 0 || currentEpoch >= support.SupportFreshUntilEpoch {
		return zero, fmt.Errorf("audit support is unavailable or stale")
	}
	orderValue, err := shared.ParseAmount(core.OrderValue)
	if err != nil || orderValue == 0 {
		return zero, fmt.Errorf("task order value is invalid")
	}
	bond, ok := k.hubKeeper.GetServiceBond(sdkCtx, address, orderValue)
	if !ok || !hubtypes.IsCandidateEligibleBondStatus(bond.Status) || bond.BondVersion == 0 ||
		bond.PendingUnbonding >= bond.EffectiveActiveBond || bond.EffectiveActiveBond < minStake {
		return zero, fmt.Errorf("panel member bond does not cover the task minimum stake")
	}
	if k.hubKeeper.GetNodeJailStatus(sdkCtx, address, hubtypes.ServiceBondRoleVerifier) == hubtypes.JailStatusTombstoned ||
		k.hubKeeper.GetNodeTombstone(sdkCtx, address) {
		return zero, fmt.Errorf("panel member is hard-invalidated")
	}
	scoring, err := k.hubKeeper.GetRoleScoringSnapshot(sdkCtx, address, hubtypes.ServiceBondRoleVerifier)
	if err != nil {
		return zero, err
	}
	if scoring.PerformanceScorePpm == 0 || scoring.PerformanceScorePpm > uint64(types.PartsPerMillion) ||
		scoring.PerformanceVersion == 0 || scoring.PerformanceVersion > uint64(^uint32(0)) {
		return zero, fmt.Errorf("panel member scoring snapshot is outside the registered range")
	}
	jailFactor := shared.CandidateJailFactorPPM(scoring.JailCount)
	if jailFactor == 0 {
		return zero, fmt.Errorf("panel member is hard-invalidated")
	}
	if hubParams.MinValidatorSeparation {
		var service sdk.AccAddress
		if serviceKey.ServiceAddress != "" {
			raw, err := k.addressCodec.StringToBytes(serviceKey.ServiceAddress)
			if err != nil {
				return zero, fmt.Errorf("panel member service address is invalid")
			}
			service = raw
		}
		pubkey, err := hex.DecodeString(serviceKey.ServicePubkey)
		if err != nil {
			return zero, fmt.Errorf("panel member service key is invalid")
		}
		overlaps, err := k.IsConsensusIdentity(ctx, address, service, pubkey)
		if err != nil || overlaps {
			// An identity that cannot be cleared is left out, like any other
			// operator whose facts cannot be read.
			return zero, fmt.Errorf("panel member is not separate from block production")
		}
	}
	return anchorPanelEligibilityFacts{
		Capability: capability, Support: support, Bond: bond, MinStake: minStake,
		PerformanceScore: uint32(scoring.PerformanceScorePpm), PerformanceVersion: uint32(scoring.PerformanceVersion),
		JailFactor: jailFactor,
	}, nil
}

// anchorPanelSelectionClock is the frozen schedule of one panel round's
// selection. It reuses the challenge-round selection windows of the task's
// frozen parameters: the same three steps separate the same events.
type anchorPanelSelectionClock struct {
	WindowRandomnessHeight    uint64
	HandraiseCloseHeight      uint64
	SelectionRandomnessHeight uint64
	AssignmentDeadlineHeight  uint64
}

func freezeAnchorPanelSelectionClock(openHeight uint64, params types.ChallengeParamsV1) (anchorPanelSelectionClock, error) {
	if params.CandidateWindowDelayBlocks == 0 || params.HandraiseWindowBlocks == 0 || params.AssignmentDelayBlocks == 0 {
		return anchorPanelSelectionClock{}, fmt.Errorf("panel selection clock contains a zero window")
	}
	window, o1 := checkedHeightAdd(openHeight, params.CandidateWindowDelayBlocks)
	handraiseClose, o2 := checkedHeightAdd(window, params.HandraiseWindowBlocks)
	selection, o3 := checkedHeightAdd(handraiseClose, params.AssignmentDelayBlocks)
	// The draw waits this long for its beacon before the round is given up.
	deadline, o4 := checkedHeightAdd(selection, params.AssignmentDelayBlocks)
	if o1 || o2 || o3 || o4 {
		return anchorPanelSelectionClock{}, fmt.Errorf("panel selection clock overflows")
	}
	return anchorPanelSelectionClock{
		WindowRandomnessHeight: window, HandraiseCloseHeight: handraiseClose,
		SelectionRandomnessHeight: selection, AssignmentDeadlineHeight: deadline,
	}, nil
}

// anchorPanelSelectionSpanBlocks is the longest a panel round takes from its
// opening to its vote deadline under the frozen parameters.
func anchorPanelSelectionSpanBlocks(params types.TaskParamsV1) (uint64, bool) {
	clock, err := freezeAnchorPanelSelectionClock(0, params.Challenge)
	if err != nil {
		return 0, false
	}
	span, overflow := checkedHeightAdd(clock.AssignmentDeadlineHeight, params.Verification.AnchorPanelVoteWindowBlocks)
	if overflow {
		return 0, false
	}
	// One block for the sweep that closes the round after its vote deadline.
	span, overflow = checkedHeightAdd(span, 1)
	return span, !overflow
}

// freezeAnchorPanelSelection builds the working row of a new panel round at
// height: the operators eligible now, less the task's Worker and the members
// of earlier panel rounds, frozen into a candidate window. It reports false,
// with no error, when a panel cannot be formed from what the chain holds (too
// few eligible operators, a released candidate pool, an unusable clock); the
// round then ends without a conclusion. Only store failures are errors.
func (k Keeper) freezeAnchorPanelSelection(
	ctx context.Context,
	taskKey types.TaskKey,
	audit types.AnchorAuditState,
	panelRound uint32,
	params types.TaskParamsV1,
	height uint64,
) (anchorPanelSelection, bool, error) {
	var none anchorPanelSelection
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	panelSize := params.Verification.AnchorPanelSize
	if !isAnchorPanelRound(panelRound) || !validAnchorPanelShape(panelSize, params.Verification.AnchorPanelThreshold) {
		return none, false, nil
	}
	core, err := k.TaskCore.Get(ctx, taskKey)
	if err != nil {
		if errIsNotFound(err) {
			return none, false, nil
		}
		return none, false, err
	}
	assignment, err := k.ReadTaskAssignment(ctx, taskKey)
	if err != nil || len(assignment.CandidatePoolSnapshotId) != types.Hash32Len ||
		len(assignment.CandidatePoolHash) != types.Hash32Len || assignment.CandidatePoolRefReleased {
		return none, false, nil
	}
	receipt, err := k.ReadInferReceipt(ctx, taskKey)
	if err != nil || len(receipt.InferReceiptHash) != types.Hash32Len {
		return none, false, nil
	}
	clock, err := freezeAnchorPanelSelectionClock(height, params.Challenge)
	if err != nil {
		return none, false, nil
	}
	hubParams := k.hubKeeper.GetHubParams(sdkCtx)
	prior := anchorPanelPriorMembers(audit.PanelRounds)
	slotCapacity, segmentBytes, candidates, err := k.deriveEligibilityCandidates(ctx, core, assignment, hubParams,
		func(operatorBytes []byte, operator string) bool {
			if _, served := prior[operator]; served {
				return false
			}
			_, err := k.loadAnchorPanelEligibilityFacts(ctx, core, assignment, operatorBytes, operator, hubParams)
			return err == nil
		})
	if err != nil || uint32(len(candidates)) < panelSize {
		return none, false, nil
	}
	windowSize, err := verifierWindowSize(uint32(len(candidates)), params.Weights.VerifierCandidateRatioPpm,
		params.Weights.VerifierCandidateWindowMin, params.Weights.VerifierCandidateWindowMax)
	if err != nil {
		return none, false, nil
	}
	if windowSize < panelSize {
		windowSize = panelSize
	}
	header := types.VerifierCandidateWindowState{
		SchemaVersion: 1, TaskId: append([]byte(nil), core.TaskId...), VerifyRound: panelRound,
		InferReceiptHash:        append([]byte(nil), receipt.InferReceiptHash...),
		CandidatePoolSnapshotId: append([]byte(nil), assignment.CandidatePoolSnapshotId...),
		CandidatePoolHash:       append([]byte(nil), assignment.CandidatePoolHash...), EligibilityFrozenHeight: height,
		WindowRandomnessHeight: clock.WindowRandomnessHeight, BuilderProposalCloseHeight: clock.HandraiseCloseHeight,
		HandraiseCloseHeight: clock.HandraiseCloseHeight, SelectionRandomnessHeight: clock.SelectionRandomnessHeight,
		AssignmentDeadlineHeight: clock.AssignmentDeadlineHeight, WindowSize: windowSize,
	}
	header, segments, err := freezeVerifierEligibilitySource(sdkCtx.ChainID(), header, slotCapacity, segmentBytes, candidates)
	if err != nil {
		return none, false, nil
	}
	kept := segments[:0]
	for _, segment := range segments {
		if !bitmapIsZero(segment.Bitmap) {
			kept = append(kept, segment)
		}
	}
	return anchorPanelSelection{
		TaskID: append([]byte(nil), core.TaskId...), PanelRound: panelRound, Window: header, Segments: kept,
	}, true, nil
}

// anchorPanelFullSegments expands the stored non-empty eligibility segments to
// the full layout the window commitments are computed over.
func anchorPanelFullSegments(selection anchorPanelSelection, segmentBytes uint32) []types.VerifierCandidateEligibilitySegmentState {
	full := make([]types.VerifierCandidateEligibilitySegmentState, selection.Window.EligibilitySegmentCount)
	for index := range full {
		full[index] = types.VerifierCandidateEligibilitySegmentState{
			TaskId: append([]byte(nil), selection.TaskID...), VerifyRound: selection.PanelRound,
			SegmentIndex: uint32(index), Bitmap: make([]byte, segmentBytes),
		}
	}
	for _, segment := range selection.Segments {
		if segment.SegmentIndex < uint32(len(full)) && uint32(len(segment.Bitmap)) == segmentBytes {
			full[segment.SegmentIndex] = segment
		}
	}
	return full
}

// materializeAnchorPanelWindow ranks the frozen eligible operators with the
// window beacon and writes the window members into the working row. It reports
// false while the beacon is not available.
func (k Keeper) materializeAnchorPanelWindow(ctx context.Context, selection *anchorPanelSelection, height uint64) (bool, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	header := selection.Window
	if header.Status != types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_SOURCE_FROZEN ||
		!shared.IsBlockHeightV1(header.WindowRandomnessHeight) || height < header.WindowRandomnessHeight {
		return false, fmt.Errorf("panel window is not a due frozen source")
	}
	beacon, found := k.hubKeeper.GetBeaconForDomain(sdkCtx, shared.DomainVerifierWindowRankV1, int64(header.WindowRandomnessHeight))
	if !found {
		return false, nil
	}
	if beacon.Height != int64(header.WindowRandomnessHeight) || len(beacon.Randomness) != types.Hash32Len {
		return false, fmt.Errorf("panel window beacon does not match its frozen height")
	}
	slotCapacity, segmentBytes, segmentCount, ok := k.hubKeeper.GetCandidatePoolLayout(ctx, header.CandidatePoolSnapshotId)
	if !ok || segmentCount != header.EligibilitySegmentCount {
		return false, fmt.Errorf("panel window candidate pool layout is unavailable")
	}
	segments := anchorPanelFullSegments(*selection, segmentBytes)
	candidates := make([]verifierWindowCandidate, 0, header.EligibleCount)
	for slot := uint32(0); slot < slotCapacity; slot++ {
		segmentIndex, byteIndex, mask, err := bitmapLocation(slot, slotCapacity, segmentBytes)
		if err != nil {
			return false, err
		}
		if segments[segmentIndex].Bitmap[byteIndex]&mask == 0 {
			continue
		}
		member, binding, err := k.hubKeeper.ResolveCandidatePoolMember(ctx, header.CandidatePoolSnapshotId, slot)
		if err != nil || member.Slot != slot || member.SlotVersion == 0 || binding.Slot != slot ||
			binding.SlotVersion != member.SlotVersion || binding.OperatorAddress != member.OperatorAddress ||
			!bytes.Equal(member.BindingHash, binding.BindingHash) {
			return false, fmt.Errorf("frozen panel slot %d no longer resolves to its immutable binding", slot)
		}
		candidates = append(candidates, verifierWindowCandidate{Slot: slot, SlotVersion: member.SlotVersion, OperatorAddress: member.OperatorAddress})
	}
	ready, members, err := materializeVerifierCandidateWindow(
		sdkCtx.ChainID(), header, slotCapacity, segmentBytes, segments, candidates, beacon.Randomness, height,
	)
	if err != nil {
		return false, err
	}
	selection.Window = ready
	selection.Members = members
	return true, nil
}

// anchorPanelUnionSegments rebuilds the hand-raise union bitmap from the
// accepted facts. The facts are the stored truth; the bitmap is their
// commitment form.
func anchorPanelUnionSegments(facts []types.TaskCandidateFactState, slotCapacity, segmentBytes, segmentCount uint32) ([]taskBitmapSegment, error) {
	segments := make([]taskBitmapSegment, segmentCount)
	for index := range segments {
		segments[index] = taskBitmapSegment{Index: uint32(index), Bitmap: make([]byte, segmentBytes)}
	}
	for _, fact := range facts {
		segmentIndex, byteIndex, mask, err := bitmapLocation(fact.Slot, slotCapacity, segmentBytes)
		if err != nil {
			return nil, err
		}
		segments[segmentIndex].Bitmap[byteIndex] |= mask
	}
	return segments, nil
}

// freezeAnchorPanelCandidateFact turns one relayed hand-raise into the frozen
// fact the draw weighs. It mirrors freezeVerifierCandidateFact with the panel
// eligibility rule; a hand-raise that does not apply (wrong scope, expired,
// not a window member, ineligible now, bad signature) is reported with
// errCandidateNotApplicable so it is skipped without failing its proposal.
func (k Keeper) freezeAnchorPanelCandidateFact(
	ctx context.Context,
	core types.TaskCoreState,
	assignment types.TaskAssignmentState,
	receipt types.InferReceiptState,
	window types.VerifierCandidateWindowState,
	member types.VerifierCandidateWindowMemberState,
	handraise types.VerifierHandraiseV1,
	params types.TaskParamsV1,
	acceptedHeight uint64,
) (types.TaskCandidateFactState, error) {
	var none types.TaskCandidateFactState
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	if handraise.SchemaVersion != types.VerifierHandraiseSchemaVersionV1 || handraise.Duty != shared.Duty_DUTY_VERIFIER ||
		handraise.ChainId != sdkCtx.ChainID() || !bytes.Equal(handraise.TaskId, core.TaskId) ||
		handraise.VerifyRound != window.VerifyRound || !bytes.Equal(handraise.InferReceiptHash, receipt.InferReceiptHash) ||
		!bytes.Equal(handraise.OutputHash, receipt.OutputHash) || !bytes.Equal(handraise.ModelId, core.ModelId) ||
		handraise.ProfileVersion != core.ProfileVersion {
		return none, fmt.Errorf("panel handraise scope is invalid: %w", errCandidateNotApplicable)
	}
	if handraise.ExpiryHeight == 0 || acceptedHeight > handraise.ExpiryHeight || acceptedHeight > window.HandraiseCloseHeight {
		return none, fmt.Errorf("panel handraise expired: %w", errCandidateNotApplicable)
	}
	if !bytes.Equal(handraise.Member.CandidatePoolSnapshotId, window.CandidatePoolSnapshotId) ||
		handraise.Member.Slot != member.Slot || handraise.Member.SlotVersion != member.SlotVersion ||
		handraise.Member.OperatorAddress != member.OperatorAddress {
		return none, fmt.Errorf("panel handraise window member mismatch: %w", errCandidateNotApplicable)
	}
	operatorBytes, operator, err := k.canonicalAddress("verifier_operator_address", handraise.Member.OperatorAddress)
	if err != nil {
		return none, fmt.Errorf("%w: %w", errCandidateNotApplicable, err)
	}
	if operator == assignment.WinnerWorker {
		return none, fmt.Errorf("winner worker cannot audit its own task: %w", errCandidateNotApplicable)
	}
	poolMember, binding, err := k.hubKeeper.ResolveCandidatePoolMember(ctx, window.CandidatePoolSnapshotId, member.Slot)
	if err != nil {
		return none, fmt.Errorf("candidate pool member: %w: %w", err, errCandidateNotApplicable)
	}
	if poolMember.SlotVersion != member.SlotVersion || poolMember.OperatorAddress != operator ||
		binding.SlotVersion != member.SlotVersion || binding.OperatorAddress != operator || !bytes.Equal(poolMember.BindingHash, binding.BindingHash) {
		return none, fmt.Errorf("candidate pool member binding mismatch: %w", errCandidateNotApplicable)
	}
	node, ok := k.hubKeeper.GetCortexNode(sdkCtx, sdk.AccAddress(operatorBytes))
	if !ok || node.OperatorAddress != operator || node.ServiceKeyStatus != hubtypes.ServiceKeyStatusActive ||
		node.ServiceAuthorizationNonce != handraise.ServiceAuthorizationNonce {
		return none, fmt.Errorf("current service binding mismatch: %w", errCandidateNotApplicable)
	}
	eligibility, err := k.loadAnchorPanelEligibilityFacts(ctx, core, assignment, operatorBytes, operator, k.hubKeeper.GetHubParams(sdkCtx))
	if err != nil {
		return none, fmt.Errorf("%w: %w", errCandidateNotApplicable, err)
	}
	weight, err := shared.CandidateWeightPPM(
		eligibility.Bond.EffectiveActiveBond, eligibility.MinStake,
		eligibility.PerformanceScore, eligibility.JailFactor,
		params.Weights.VerifierStakeWeightPpm, params.Weights.VerifierPerformanceWeightPpm,
		params.Weights.CandidateWeightPpmMax,
	)
	if err != nil {
		return none, fmt.Errorf("%w: %w", errCandidateNotApplicable, err)
	}
	digest, err := types.VerifierHandraiseSigningDigest(handraise)
	if err != nil {
		return none, fmt.Errorf("%w: %w", errCandidateNotApplicable, err)
	}
	if err := k.hubKeeper.VerifyCurrentCortexServiceDigest(ctx, operator, handraise.ServiceSignature, digest[:], acceptedHeight); err != nil {
		return none, fmt.Errorf("%w: %w", errCandidateNotApplicable, err)
	}
	return types.TaskCandidateFactState{
		SchemaVersion: types.AssignmentCandidateSchemaVersionV1,
		TaskId:        append([]byte(nil), core.TaskId...), Stage: types.TaskCandidateStage_TASK_CANDIDATE_STAGE_OPEN_VERIFY,
		Slot: member.Slot, SlotVersion: member.SlotVersion, OperatorAddress: operator, Duty: shared.Duty_DUTY_VERIFIER,
		ActiveBondSnapshot: shared.NewAmount(eligibility.Bond.EffectiveActiveBond), AvailableBondSnapshot: shared.NewAmount(eligibility.Bond.AvailableBond),
		RequiredTaskLiabilitySnapshot: shared.NewAmount(0), MinStakeSnapshot: shared.NewAmount(eligibility.MinStake),
		PerformanceScoreSnapshotPpm: eligibility.PerformanceScore, PerformanceMethodVersion: eligibility.PerformanceVersion,
		CandidateJailFactorSnapshotPpm: eligibility.JailFactor, BondVersionSnapshot: eligibility.Bond.BondVersion,
		CapabilityVersionSnapshot: eligibility.Capability.CapabilityVersion,
		SupportVersionSnapshot:    eligibility.Support.SupportVersion, CandidateWeight: weight, HandraiseSigningDigest: digest[:],
	}, nil
}

// drawAnchorPanel draws the panel of a round from its hand-raisers with the
// selection beacon. It reports found=false while the beacon is not available
// and drawn=false when a full panel cannot be drawn (fewer hand-raisers than
// seats, or a draw the bounded attempts could not complete).
func (k Keeper) drawAnchorPanel(
	ctx context.Context,
	selection anchorPanelSelection,
	params types.TaskParamsV1,
	height uint64,
) (members []string, selectedHash []byte, found, drawn bool) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	window := selection.Window
	if window.Status != types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY ||
		!shared.IsBlockHeightV1(window.SelectionRandomnessHeight) || height < window.SelectionRandomnessHeight {
		return nil, nil, true, false
	}
	beacon, ok := k.hubKeeper.GetBeaconForDomain(sdkCtx, shared.DomainWeightedDrawV1, int64(window.SelectionRandomnessHeight))
	if !ok {
		return nil, nil, false, false
	}
	panelSize := params.Verification.AnchorPanelSize
	if beacon.Height != int64(window.SelectionRandomnessHeight) || len(beacon.Randomness) != types.Hash32Len ||
		selection.Union == nil || uint32(len(selection.Facts)) < panelSize || height > window.AssignmentDeadlineHeight ||
		params.Weights.MaxWeightedDrawAttemptsPerSelection == 0 {
		return nil, nil, true, false
	}
	union := *selection.Union
	union.Status = types.TaskCandidateStageStatusV1_TASK_CANDIDATE_STAGE_STATUS_V1_FINALIZING
	// The assignment clock only has to be strictly increasing for the shared
	// builder; a panel round keeps its own vote deadline.
	commit, o1 := checkedHeightAdd(height, 1)
	final, o2 := checkedHeightAdd(height, 2)
	if o1 || o2 {
		return nil, nil, true, false
	}
	assignment, err := buildVerifierAssignment(
		sdkCtx.ChainID(), window, union, selection.Members, selection.Facts, beacon.Randomness,
		panelSize, uint64(params.Weights.MaxWeightedDrawAttemptsPerSelection), height, commit, final,
	)
	if err != nil || uint32(len(assignment.SelectedVerifiers)) != panelSize {
		return nil, nil, true, false
	}
	members = make([]string, 0, len(assignment.SelectedVerifiers))
	for _, selected := range assignment.SelectedVerifiers {
		members = append(members, selected.OperatorAddress)
	}
	return members, assignment.SelectedVerifiersHash, true, true
}

// sortedAnchorPanelFacts returns facts in ascending slot order.
func sortedAnchorPanelFacts(facts []types.TaskCandidateFactState) []types.TaskCandidateFactState {
	out := append([]types.TaskCandidateFactState(nil), facts...)
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return out
}
