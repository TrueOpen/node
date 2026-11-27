package keeper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// Two-step sampling of the anchor audit.
//
// In one-step sampling the first beacon after the receipt decides both whether
// a task is audited and which rows the Worker must open. A Profile whose anchor
// evidence requirement carries commitment schema version 2 is audited in two
// steps instead:
//
//	first beacon         coin only: selected or not (same formula, same rate)
//	list message         the selected Worker records the hash of its per-row
//	                     commitment list, in block H_L, by list_deadline_height
//	position beacons     the Hub beacons of the m heights after H_L, each a
//	                     verified proposer-VRF value, are aggregated
//	draw                 EndBlock of the last of those heights draws the
//	                     positions; opening and proof deadlines count from here
//
// After the draw the audit is exactly a one-step audit: opening, proof, vote,
// deadline outcomes and settlement are the existing code.
//
// What may penalise the Worker is one on-chain fact only: no list message was
// accepted by list_deadline_height. That ends the audit like a missing opening
// (REJECT, source TIMEOUT, the timeout fault). Everything the chain itself owes
// the audit - the beacons, the EndBlock visit, the draw - can only delay it or
// end it NO_VERDICT, which pays the Worker and slashes nothing.
const (
	// anchorSamplingModeOneStep and anchorSamplingModeTwoStep are the values of
	// AnchorAuditState.sampling_mode.
	anchorSamplingModeOneStep = uint32(0)
	anchorSamplingModeTwoStep = uint32(1)
	// anchorPositionBeaconDomainV1 frames the aggregated beacon of the position draw.
	anchorPositionBeaconDomainV1 = "TRUEOPEN_ANCHOR_POSITION_BEACON_V1"
	// anchorSampleDomainV1 separates the two-step position draw from the
	// one-step draw (anchorSampleDomainV0) and from every other use of a beacon.
	anchorSampleDomainV1 = "TRUEOPEN_ANCHOR_SAMPLE_V1"
	// anchorListEntryBytes is the size of one entry of the commitment list.
	anchorListEntryBytes = uint64(32)

	anchorListMissingReason     = "worker did not post the commitment list before the list deadline"
	anchorPositionsMissingReson = "the chain did not draw positions for the recorded commitment list"
	// anchorSamplingSweepConservativeBytesV1 is the byte charge of one visited
	// row of the sampling queue: the audit row, the receipt the row count is
	// read from, and the beacons of one window.
	anchorSamplingSweepConservativeBytesV1 = uint64(16 << 10)
)

// isAnchorTwoStep reports whether an audit is selected and sampled in two steps.
// An unselected row never enters the two-step flow, whatever its mode says.
func isAnchorTwoStep(state types.AnchorAuditState) bool {
	return state.SamplingMode == anchorSamplingModeTwoStep && state.Selected
}

func anchorListRecorded(state types.AnchorAuditState) bool {
	return len(state.CommitmentListHash) == types.Hash32Len
}

// anchorAwaitsList reports whether an open two-step audit still waits for the
// Worker's list; anchorAwaitsPositions whether it waits for the chain's draw.
func anchorAwaitsList(state types.AnchorAuditState) bool {
	return isAnchorTwoStep(state) && state.ResultHeight == 0 && !anchorListRecorded(state)
}

func anchorAwaitsPositions(state types.AnchorAuditState) bool {
	return isAnchorTwoStep(state) && state.ResultHeight == 0 && anchorListRecorded(state) && len(state.Positions) == 0
}

// anchorListDeadlinePassed reports whether the Worker's omission is
// established at height: no list was accepted and none can be any more.
func anchorListDeadlinePassed(state types.AnchorAuditState, height uint64) bool {
	return anchorAwaitsList(state) && height > state.ListDeadlineHeight
}

// anchorSamplingModeOf reads the sampling mode of a task from its locked
// Profile: the commitment schema version of the anchor evidence requirement.
// ok is false when the Profile cannot be read or carries no anchor requirement;
// the caller then writes no selected audit rather than guess a mode.
func (k Keeper) anchorSamplingModeOf(ctx context.Context, taskKey types.TaskKey) (uint32, bool) {
	core, err := k.TaskCore.Get(ctx, taskKey)
	if err != nil {
		return 0, false
	}
	profile, found := k.hubKeeper.GetProfileState(sdk.UnwrapSDKContext(ctx), core.ModelId, core.ProfileVersion)
	if !found {
		return 0, false
	}
	twoStep, has := shared.AnchorTwoStepSamplingRequired(profile.ExecutionSnapshot.VerificationProfile.EvidenceSchema)
	if !has {
		return 0, false
	}
	if twoStep {
		return anchorSamplingModeTwoStep, true
	}
	return anchorSamplingModeOneStep, true
}

// anchorTwoStepSelection fills the fields a selected two-step audit carries
// from the first beacon on: the list deadline and the verdict deadline. The
// list window counts from height, the block that writes the row, which is the
// first block in which the Worker can see that it was selected (normally the
// sampling height itself; later only when EndBlock ran late). The verdict
// deadline is the upper bound over every later step and is never moved, so the
// settlement index row placed on it stays where it is.
func anchorTwoStepSelection(state *types.AnchorAuditState, v types.VerificationParamsV1, height uint64) bool {
	listWindow, beaconBlocks := v.AnchorTwoStepWindows()
	listDeadline, o1 := checkedHeightAdd(height, listWindow)
	drawBound, o2 := checkedHeightAdd(listDeadline, beaconBlocks)
	openBound, o3 := checkedHeightAdd(drawBound, v.AnchorOpenWindowBlocks)
	proofBound, o4 := checkedHeightAdd(openBound, v.AnchorProofWindowBlocks)
	verdictDeadline, o5 := checkedHeightAdd(proofBound, v.AnchorVerdictWindowBlocks)
	if o1 || o2 || o3 || o4 || o5 {
		return false
	}
	state.ListDeadlineHeight = listDeadline
	state.OpeningDeadlineHeight, state.ProofDeadlineHeight = 0, 0
	state.VerdictDeadlineHeight = verdictDeadline
	state.Positions = nil
	return true
}

// anchorPositionBeacon aggregates the contributing block beacons of one window.
func anchorPositionBeacon(chainID string, taskID []byte, firstHeight uint64, beacons [][]byte) []byte {
	h := sha256.New()
	h.Write([]byte(anchorPositionBeaconDomainV1))
	h.Write([]byte(chainID))
	h.Write(taskID)
	var first [8]byte
	binary.BigEndian.PutUint64(first[:], firstHeight)
	h.Write(first[:])
	var count [4]byte
	binary.BigEndian.PutUint32(count[:], uint32(len(beacons)))
	h.Write(count[:])
	for _, beacon := range beacons {
		h.Write(beacon)
	}
	return h.Sum(nil)
}

// anchorSamplePositionsV1 draws count distinct positions in [0, rows) for a
// two-step audit. The first beacon, the recorded list hash and the aggregated
// beacon all enter every draw, so no position is a function of values that
// existed before the list was recorded.
func anchorSamplePositionsV1(chainID string, taskID, sampleBeacon, listHash, positionBeacon []byte, rows uint64, count int) []uint64 {
	seen := map[uint64]struct{}{}
	out := make([]uint64, 0, count)
	for i := uint32(0); len(out) < count && i < 64; i++ {
		h := sha256.New()
		h.Write([]byte(anchorSampleDomainV1))
		h.Write([]byte(chainID))
		h.Write(taskID)
		h.Write(sampleBeacon)
		h.Write(listHash)
		h.Write(positionBeacon)
		var idx [4]byte
		binary.BigEndian.PutUint32(idx[:], i)
		h.Write(idx[:])
		p := binary.BigEndian.Uint64(h.Sum(nil)[:8]) % rows
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// setAnchorSamplingDue places (or moves) the one row the EndBlock queue holds
// for a two-step audit; clearAnchorSamplingDue removes it.
func (k Keeper) setAnchorSamplingDue(ctx context.Context, taskKey types.TaskKey, height uint64) error {
	if err := k.clearAnchorSamplingDue(ctx, taskKey); err != nil {
		return err
	}
	if height == 0 {
		return nil
	}
	if err := k.AnchorSamplingDueIndex.Set(ctx, types.NewDeadlineIndexKey(height, taskKey)); err != nil {
		return err
	}
	return k.AnchorSamplingDueHeight.Set(ctx, taskKey, height)
}

func (k Keeper) clearAnchorSamplingDue(ctx context.Context, taskKey types.TaskKey) error {
	height, err := k.AnchorSamplingDueHeight.Get(ctx, taskKey)
	if err != nil {
		if errIsNotFound(err) {
			return nil
		}
		return err
	}
	if err := removeDeadlineIndex(ctx, k.AnchorSamplingDueIndex, taskKey, height); err != nil {
		return err
	}
	return k.AnchorSamplingDueHeight.Remove(ctx, taskKey)
}

// anchorSamplingDueHeightOf is the height at which the queue must next visit
// an open two-step audit: the block after the list deadline while the list is
// missing, the last contributing height once it is recorded. Zero means the
// audit needs no visit.
func anchorSamplingDueHeightOf(state types.AnchorAuditState) uint64 {
	switch {
	case anchorAwaitsList(state):
		due, overflow := checkedHeightAdd(state.ListDeadlineHeight, 1)
		if overflow {
			return 0
		}
		return due
	case anchorAwaitsPositions(state):
		return state.PositionBeaconLastHeight
	default:
		return 0
	}
}

// SubmitAnchorCommitmentList records the hash of the winner Worker's per-row
// commitment list for a task that two-step sampling selected. The first
// accepted message is final: the same hash and size again is a NOOP at any
// later height, any other content is refused for the rest of the audit. The
// position draw is bound to the block that includes this message: the
// contributing beacons are those of the heights strictly after it.
func (m msgServer) SubmitAnchorCommitmentList(ctx context.Context, req *types.MsgSubmitAnchorCommitmentList) (*types.MsgSubmitAnchorCommitmentListResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "anchor commitment list request is required")
	}
	if len(req.TaskId) != types.Hash32Len || len(req.ListHash) != types.Hash32Len || bytes.Equal(req.ListHash, make([]byte, types.Hash32Len)) ||
		req.ListSizeBytes == 0 {
		return nil, status.Error(codes.InvalidArgument, "anchor commitment list envelope is invalid")
	}
	if req.ListSizeBytes > maxAnchorOpeningSizeBytes || req.ListSizeBytes%anchorListEntryBytes != 0 {
		return nil, status.Errorf(codes.InvalidArgument, "anchor commitment list size must be a multiple of %d bytes", anchorListEntryBytes)
	}
	if !validAnchorMaterialURI(req.ListUri) {
		return nil, status.Error(codes.InvalidArgument, "anchor commitment list location must be an http or https URL")
	}
	if _, _, err := m.k.canonicalAddress("submitter_address", req.SubmitterAddress); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return nil, err
	}
	taskKey := types.NewTaskKey(req.TaskId)
	state, err := m.k.AnchorAudit.Get(ctx, taskKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "task has no anchor audit sample yet")
		}
		return nil, err
	}
	if !anchorSelected(state) {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "task was not selected for anchor audit")
	}
	if !isAnchorTwoStep(state) {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "task is audited with one-step sampling and takes no commitment list")
	}
	winner, err := m.k.anchorWinner(ctx, taskKey, state)
	if err != nil {
		return nil, err
	}
	// Signer rule of the opening: only the winner Worker's current service may
	// record its list; a Builder cannot write it on the Worker's behalf.
	if err := m.k.RequireCurrentServiceSubmitter(ctx, shared.ParticipantTypeCortexNode, winner, req.SubmitterAddress); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "submitter is not the winner Worker's current service")
	}
	if anchorListRecorded(state) {
		// Decided before the deadline and before the final-verdict check, so a
		// repeated broadcast answers the same at every later stage. The location
		// of the repetition is not compared and not stored.
		if bytes.Equal(state.CommitmentListHash, req.ListHash) && state.CommitmentListSizeBytes == req.ListSizeBytes {
			return &types.MsgSubmitAnchorCommitmentListResponse{
				PositionBeaconFirstHeight: state.PositionBeaconFirstHeight, PositionBeaconLastHeight: state.PositionBeaconLastHeight,
				Status: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP,
			}, nil
		}
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "anchor commitment list already recorded with a different hash; the first recorded list is final")
	}
	if state.ResultHeight != 0 {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "anchor audit verdict is already final")
	}
	if height > state.ListDeadlineHeight {
		return nil, errorsmod.Wrapf(types.ErrInvalidAssignment, "anchor commitment list deadline %d has passed", state.ListDeadlineHeight)
	}
	if !anchorLocationsUnderPrefix(state, req.ListUri) {
		return nil, errorsmod.Wrapf(types.ErrInvalidAssignment, "anchor commitment list location must lie under the Worker's published data-availability endpoint %s", state.DaUriPrefix)
	}
	receipt, err := m.k.ReadInferReceipt(ctx, taskKey)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "accepted infer receipt is unavailable")
	}
	rows, err := anchorRowsOf(receipt)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, err.Error())
	}
	// The row count is the chain's own; the message carries none.
	if req.ListSizeBytes/anchorListEntryBytes != rows {
		return nil, errorsmod.Wrapf(types.ErrInvalidAssignment, "anchor commitment list size must be %d bytes per row for the task's %d rows", anchorListEntryBytes, rows)
	}
	params, err := m.k.ParamsForTask(ctx, taskKey)
	if err != nil {
		return nil, err
	}
	_, beaconBlocks := params.Verification.AnchorTwoStepWindows()
	first, o1 := checkedHeightAdd(height, 1)
	last, o2 := checkedHeightAdd(height, beaconBlocks)
	if o1 || o2 {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "anchor position beacon height overflows")
	}
	state.CommitmentListHash = append([]byte(nil), req.ListHash...)
	state.CommitmentListSizeBytes = req.ListSizeBytes
	state.CommitmentListUri = req.ListUri
	state.CommitmentListHeight = height
	state.PositionBeaconFirstHeight, state.PositionBeaconLastHeight = first, last
	if err := m.k.AnchorAudit.Set(ctx, taskKey, state); err != nil {
		return nil, err
	}
	if err := m.k.setAnchorSamplingDue(ctx, taskKey, last); err != nil {
		return nil, err
	}
	if err := emitAnchorListRecordedEvent(ctx, state); err != nil {
		return nil, err
	}
	return &types.MsgSubmitAnchorCommitmentListResponse{
		PositionBeaconFirstHeight: first, PositionBeaconLastHeight: last,
		Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED,
	}, nil
}

func emitAnchorListRecordedEvent(ctx context.Context, state types.AnchorAuditState) error {
	return emitTypedEvent(ctx, &types.EventAnchorCommitmentListRecorded{
		TaskId: append([]byte(nil), state.TaskId...), WorkerOperatorAddress: state.WinnerWorker,
		ListHash: append([]byte(nil), state.CommitmentListHash...), ListSizeBytes: state.CommitmentListSizeBytes,
		Height:                    state.CommitmentListHeight,
		PositionBeaconFirstHeight: state.PositionBeaconFirstHeight, PositionBeaconLastHeight: state.PositionBeaconLastHeight,
	})
}

// anchorContributingBeacons reads the block beacons of one window. It reports
// false when any of them is missing, is not the value of its own height, or is
// not a verified proposer-VRF value: a block-hash placeholder is chosen by the
// proposer of its block and is never accepted here.
func (k Keeper) anchorContributingBeacons(ctx context.Context, first, last uint64) ([][]byte, bool) {
	if first == 0 || last < first || last-first >= uint64(types.AnchorPositionBeaconBlocksMaximum) {
		return nil, false
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	out := make([][]byte, 0, last-first+1)
	for h := first; h <= last; h++ {
		if !shared.IsBlockHeightV1(h) {
			return nil, false
		}
		beacon, found := k.hubKeeper.GetBeaconForDomain(sdkCtx, shared.DomainWeightedDrawV1, int64(h))
		if !found || beacon.Height != int64(h) || len(beacon.Randomness) != types.Hash32Len ||
			beacon.SourceTag != hubtypes.BeaconSourceProposerVRFV1 || len(beacon.ProofDigest) != types.Hash32Len {
			return nil, false
		}
		out = append(out, beacon.Randomness)
	}
	return out, true
}

// finalizeAnchorSamplingOutcome ends a two-step audit that never reached the
// draw, in the EndBlock queue: REJECT for the Worker's missing list, NO_VERDICT
// when the chain could not draw. Settlement applies the consequence.
func (k Keeper) finalizeAnchorSamplingOutcome(ctx context.Context, taskKey types.TaskKey, state types.AnchorAuditState, height uint64, verdict types.AnchorVerdictV1, reason string) error {
	state.ResultHeight = height
	state.TaskVerdict = verdict
	state.ResultSource = types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_TIMEOUT
	state.ResultReason = truncateAnchorReason(reason)
	if err := k.AnchorAudit.Set(ctx, taskKey, state); err != nil {
		return err
	}
	if err := k.clearAnchorSamplingDue(ctx, taskKey); err != nil {
		return err
	}
	return emitAnchorVerdictEvent(ctx, state)
}

// advanceAnchorSampling is one visit of the EndBlock queue to a two-step
// audit. It never moves an audit toward a Worker fault except on the one fact
// the Worker controls (no list by the deadline).
func (k Keeper) advanceAnchorSampling(ctx context.Context, taskKey types.TaskKey, height uint64) error {
	state, err := k.AnchorAudit.Get(ctx, taskKey)
	if err != nil {
		if errIsNotFound(err) {
			return k.clearAnchorSamplingDue(ctx, taskKey)
		}
		return err
	}
	switch {
	case anchorAwaitsList(state):
		if height <= state.ListDeadlineHeight {
			// Visited early (an imported or re-keyed row): wait for the deadline.
			return k.setAnchorSamplingDue(ctx, taskKey, anchorSamplingDueHeightOf(state))
		}
		return k.finalizeAnchorSamplingOutcome(ctx, taskKey, state, height,
			types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT, anchorListMissingReason)
	case anchorAwaitsPositions(state):
		return k.drawAnchorPositions(ctx, taskKey, state, height)
	default:
		// Final, drawn, or not a two-step audit: nothing left for this queue.
		return k.clearAnchorSamplingDue(ctx, taskKey)
	}
}

// drawAnchorPositions draws the positions of a two-step audit whose list is
// recorded, once the last contributing height is reached.
//
// The Worker's opening and proof windows and the validators' voting window all
// count from the block of the draw and must end by the verdict deadline fixed
// at selection. A draw that could no longer give all three their full length
// is not made: the audit ends NO_VERDICT, never with shortened windows.
//
// A window with a missing or non-VRF beacon draws nothing. It is replaced by a
// fresh window of the same length after the current block, as often as the
// time bound above allows; the heights are never slid to "the next good block",
// which would hand the choice of contributing blocks to whoever can spoil one.
func (k Keeper) drawAnchorPositions(ctx context.Context, taskKey types.TaskKey, state types.AnchorAuditState, height uint64) error {
	if height < state.PositionBeaconLastHeight {
		return k.setAnchorSamplingDue(ctx, taskKey, state.PositionBeaconLastHeight)
	}
	noDraw := func(reason string) error {
		return k.finalizeAnchorSamplingOutcome(ctx, taskKey, state, height,
			types.AnchorVerdictV1_ANCHOR_VERDICT_V1_NO_VERDICT, anchorPositionsMissingReson+": "+reason)
	}
	params, err := k.ParamsForTask(ctx, taskKey)
	if err != nil {
		return noDraw("the task's parameters are unavailable")
	}
	v := params.Verification
	remaining, o1 := checkedHeightAdd(v.AnchorOpenWindowBlocks, v.AnchorProofWindowBlocks)
	remaining, o2 := checkedHeightAdd(remaining, v.AnchorVerdictWindowBlocks)
	fits := func(drawHeight uint64) bool {
		end, overflow := checkedHeightAdd(drawHeight, remaining)
		return !o1 && !o2 && !overflow && end <= state.VerdictDeadlineHeight
	}
	if !fits(height) {
		return noDraw("the draw would leave less than the full opening, proof and verdict windows")
	}
	beacons, ok := k.anchorContributingBeacons(ctx, state.PositionBeaconFirstHeight, state.PositionBeaconLastHeight)
	if !ok {
		_, beaconBlocks := v.AnchorTwoStepWindows()
		first, o3 := checkedHeightAdd(height, 1)
		last, o4 := checkedHeightAdd(height, beaconBlocks)
		if o3 || o4 || !fits(last) {
			return noDraw("a contributing beacon is missing or is not a proposer-VRF value and no further window fits")
		}
		sdk.UnwrapSDKContext(ctx).Logger().Error("anchor position draw restarted: a contributing beacon is missing or is not a proposer-VRF value",
			"task", fmt.Sprintf("%x", []byte(taskKey)), "failed_first", state.PositionBeaconFirstHeight, "failed_last", state.PositionBeaconLastHeight,
			"first", first, "last", last)
		state.PositionBeaconFirstHeight, state.PositionBeaconLastHeight = first, last
		if err := k.AnchorAudit.Set(ctx, taskKey, state); err != nil {
			return err
		}
		if err := k.setAnchorSamplingDue(ctx, taskKey, last); err != nil {
			return err
		}
		return emitAnchorListRecordedEvent(ctx, state)
	}
	receipt, err := k.ReadInferReceipt(ctx, taskKey)
	if err != nil {
		return noDraw("the accepted infer receipt is unavailable")
	}
	rows, err := anchorRowsOf(receipt)
	if err != nil || rows == 0 {
		return noDraw("the receipt's row count cannot be derived")
	}
	chainID := sdk.UnwrapSDKContext(ctx).ChainID()
	state.PositionBeacon = anchorPositionBeacon(chainID, state.TaskId, state.PositionBeaconFirstHeight, beacons)
	state.Positions = anchorSamplePositionsV1(chainID, state.TaskId, state.SampleBeacon, state.CommitmentListHash, state.PositionBeacon, rows, anchorSamplePositionsV0)
	if len(state.Positions) == 0 {
		return noDraw("the draw produced no position")
	}
	state.PositionsHeight = height
	// fits(height) bounds both sums by the verdict deadline.
	state.OpeningDeadlineHeight = height + v.AnchorOpenWindowBlocks
	state.ProofDeadlineHeight = state.OpeningDeadlineHeight + v.AnchorProofWindowBlocks
	if err := k.AnchorAudit.Set(ctx, taskKey, state); err != nil {
		return err
	}
	if err := k.clearAnchorSamplingDue(ctx, taskKey); err != nil {
		return err
	}
	return emitTypedEvent(ctx, &types.EventAnchorPositionsDrawn{
		TaskId: append([]byte(nil), state.TaskId...), Positions: append([]uint64(nil), state.Positions...),
		PositionBeaconLastHeight: state.PositionBeaconLastHeight,
		OpeningDeadlineHeight:    state.OpeningDeadlineHeight, ProofDeadlineHeight: state.ProofDeadlineHeight,
	})
}

// processAnchorSamplingDue is the EndBlock queue of two-step audits. Each
// visited row is one unit of the shared budget. A row whose step fails is
// logged and taken out of the queue; the audit is then left to its verdict
// deadline, where an audit without positions ends NO_VERDICT and a missing
// list ends as the Worker's omission through the settlement view. It never
// returns an error for one bad row, so one audit cannot stop the block.
func (k Keeper) processAnchorSamplingDue(ctx context.Context, currentHeight, limit uint64) (uint64, error) {
	var visited uint64
	for visited < limit {
		head, found, err := firstDueTaskDeadline(ctx, k.AnchorSamplingDueIndex, currentHeight)
		if err != nil || !found {
			return visited, err
		}
		taskKey := types.TaskKey(head.primaryID)
		visited++
		sdkCtx := sdk.UnwrapSDKContext(ctx)
		cacheCtx, write := sdkCtx.CacheContext()
		err = k.advanceAnchorSampling(sdk.WrapSDKContext(cacheCtx), taskKey, currentHeight)
		if err == nil {
			// A step must take the row out of the due range or move it; a row
			// left in place would be revisited for free in every later block.
			if still, herr := k.AnchorSamplingDueIndex.Has(sdk.WrapSDKContext(cacheCtx), types.NewDeadlineIndexKey(head.deadline, taskKey)); herr != nil || still {
				err = fmt.Errorf("the step left its queue row in place")
			}
		}
		if err != nil {
			sdkCtx.Logger().Error("anchor sampling step failed; the audit is left to its verdict deadline",
				"task", fmt.Sprintf("%x", []byte(taskKey)), "err", err)
			if err := removeDeadlineIndex(ctx, k.AnchorSamplingDueIndex, taskKey, head.deadline); err != nil {
				return visited, err
			}
			if err := k.AnchorSamplingDueHeight.Remove(ctx, taskKey); err != nil && !errIsNotFound(err) {
				return visited, err
			}
			continue
		}
		write()
	}
	return visited, nil
}

// rebuildAnchorSamplingDue restores the queue row of an imported two-step
// audit that is still waiting for its list or for its draw.
func (k Keeper) rebuildAnchorSamplingDue(ctx context.Context, taskKey types.TaskKey, state types.AnchorAuditState) error {
	if due := anchorSamplingDueHeightOf(state); due != 0 {
		return k.setAnchorSamplingDue(ctx, taskKey, due)
	}
	return nil
}

var _ hubtypes.AnchorTwoStepParamsGate = Keeper{}

// AnchorTwoStepSamplingConfigured reports whether the current parameters carry
// a list window and a beacon count. The Hub reads it before it admits a Profile
// version that asks for two-step sampling.
func (k Keeper) AnchorTwoStepSamplingConfigured(ctx context.Context) (bool, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return false, err
	}
	return params.Verification.AnchorTwoStepConfigured(), nil
}
