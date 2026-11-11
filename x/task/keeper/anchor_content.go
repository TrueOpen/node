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

// ADR-0040 v7: the content-integrity verdict (B1 gradient certification).
//
// The compute verdict (anchor_vote.go) answers "did the Worker run the
// registered weights at the sampled positions"; it is decided first and the
// task settles on it exactly as in v6. The content verdict answers "is the
// committed trajectory the registered model's own decoding" (direction
// statistic z over gradients certified with beacon rows Y_x = x*W). It is a
// second, separate track:
//
//   - its own deadline, content_verdict_deadline_height = verdict deadline +
//     anchor_content_verdict_window_blocks, fixed at sampling (window 0 = no
//     content track);
//   - its own votes (MsgSubmitAnchorContentVote) and tally, final at >= 2/3 of
//     the last total voting power, like the compute track;
//   - its own slash, anchor_content_reject_slash_bps of the bond D fixed at
//     sampling, with the Worker jailed (hub fault anchor_content_reject).
//
// Settlement policy. A task settles on its compute verdict and is never held
// for the content verdict: the content track opens only once the task has
// settled, and a later REJECT_CONTENT does not claw the payment back. The bond
// D is what covers it: D >= anchor_min_bond_multiple * R (default 40 R, the
// content deterrent) or the task is audited at 100% (anchor_rate.go). The
// Worker is slashed at most once per task by the anchor audit: when the compute
// track already produced a fault (REJECT, or TIMEOUT because the Worker never
// opened), the content track closes without a vote (source COMPUTE_REJECTED). A
// Worker that opened its rows on the content track and then never proved is the
// exception (decision of 2026-11-09): its single fault is anchor_content_reject
// (slash and jail), applied at settlement, and the content track records
// REJECT_CONTENT (source TIMEOUT) — "open, then withhold the proof" must not be
// cheaper than being rejected.
//
// Deadline. A content verdict that is not final by its deadline is closed when
// evidence cleanup reaches the task (cleanup is held until the deadline):
// REJECT_CONTENT when the Worker's recorded proof is not bound to a sealed B1
// epoch (the Worker withheld the material it owed, which must not be cheaper
// than being rejected), NO_VERDICT otherwise (the validators were silent).
//
// B1 beacon x. g and v_int must be fixed before x exists, or a Worker could
// solve the three integer identities <g, x_j> = <v_int, Y_x_j> for a forged g.
// "Max opening height + 1" alone is not enough: with an unfrozen opening set a
// Worker could see x and then add another opening to the same epoch with g
// chosen knowing x. MsgSealAnchorEpoch therefore freezes the opening set (every
// member already opened, so its opening hash, which covers the g and v_int rows,
// is on chain; no task can join a sealed epoch; a sealed task takes no opening
// at or after x) and records x_beacon_height = freeze height + 1, which is above
// every member opening. x comes from the hub beacon at that height, written in
// that block's PreBlocker. Proofs that carry Y_x rows must name that height and
// are accepted only once the beacon exists; the proof records it.

const (
	// anchorXBeaconDomainV1 names the B1 consumer of the hub beacon.
	anchorXBeaconDomainV1 = "TRUEOPEN_ANCHOR_B1_X_BEACON_V1"
	// anchorContentRejectDomainV1 frames the evidence of a content slash.
	anchorContentRejectDomainV1 = "TRUEOPEN_ANCHOR_CONTENT_REJECT_EVIDENCE_V1"
	// maxAnchorEpochTasks bounds the members of one sealed epoch.
	maxAnchorEpochTasks = 256
	// maxAnchorContentPositionVerdicts bounds the per-position diagnostics of a vote.
	maxAnchorContentPositionVerdicts = int(types.AnchorPositionsPerTaskMaximum)
)

// deriveAnchorXBeacon returns the chain beacon randomness at xHeight: the hub
// beacon that also drives verifier selection (proposer VRF, or the block-hash
// placeholder before VRF is required). It is written in the PreBlocker of block
// xHeight, so no transaction that froze the epoch could know it. False while it
// does not exist. Off chain, x is derived from (chain_id, epoch_manifest_hash,
// this randomness, module key, row index).
func (k Keeper) deriveAnchorXBeacon(ctx context.Context, xHeight uint64) ([]byte, bool) {
	if xHeight == 0 || !shared.IsBlockHeightV1(xHeight) {
		return nil, false
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	beacon, found := k.hubKeeper.GetBeaconForDomain(sdkCtx, anchorXBeaconDomainV1, int64(xHeight))
	if !found || beacon.Height != int64(xHeight) || len(beacon.Randomness) != types.Hash32Len {
		return nil, false
	}
	return append([]byte(nil), beacon.Randomness...), true
}

// boundXHeight is the x height the recorded proof declared (zero: compute-only proof).
func boundXHeight(state types.AnchorAuditState) uint64 {
	if state.WorkerProofXBound {
		return state.XBeaconHeight
	}
	return 0
}

// bindAnchorProofToX enforces the B1 ordering on a new proof and records x on the audit.
func (k Keeper) bindAnchorProofToX(ctx context.Context, state *types.AnchorAuditState, req *types.MsgSubmitAnchorProof, height uint64) error {
	if state.XBeaconHeight == 0 {
		if req.XBeaconHeight != 0 {
			return errorsmod.Wrap(types.ErrInvalidAssignment, "anchor proof names an x beacon height but the task was never sealed into a B1 epoch")
		}
		return nil // compute-only proof of an unsealed task (v6 behavior)
	}
	if req.XBeaconHeight != state.XBeaconHeight {
		return errorsmod.Wrapf(types.ErrInvalidAssignment, "anchor proof of a sealed task must carry the Y_x rows of x beacon height %d", state.XBeaconHeight)
	}
	if !bytes.Equal(req.EpochManifestHash, state.EpochManifestHash) {
		return errorsmod.Wrap(types.ErrInvalidAssignment, "anchor proof names a different epoch manifest than the sealed epoch")
	}
	if height < state.XBeaconHeight {
		return errorsmod.Wrapf(types.ErrInvalidAssignment, "x beacon height %d has not been reached", state.XBeaconHeight)
	}
	x, ok := k.deriveAnchorXBeacon(ctx, state.XBeaconHeight)
	if !ok {
		return errorsmod.Wrapf(types.ErrInvalidAssignment, "beacon at x beacon height %d is unavailable", state.XBeaconHeight)
	}
	state.XBeacon = x
	state.WorkerProofXBound = true
	epochKey := types.Hash32Key(state.EpochManifestHash)
	epoch, err := k.AnchorEpoch.Get(ctx, epochKey)
	if err != nil {
		return errorsmod.Wrap(types.ErrInvariantBroken, "sealed task names an epoch the chain does not hold")
	}
	if len(epoch.XBeacon) == 0 {
		epoch.XBeacon = append([]byte(nil), x...)
		if err := k.AnchorEpoch.Set(ctx, epochKey, epoch); err != nil {
			return err
		}
	}
	return nil
}

// SealAnchorEpoch closes a Worker's B1 proving epoch (ADR-0040 v7).
func (m msgServer) SealAnchorEpoch(ctx context.Context, req *types.MsgSealAnchorEpoch) (*types.MsgSealAnchorEpochResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "anchor epoch seal request is required")
	}
	if len(req.EpochManifestHash) != types.Hash32Len || bytes.Equal(req.EpochManifestHash, make([]byte, types.Hash32Len)) ||
		len(req.TaskIds) == 0 || len(req.TaskIds) > maxAnchorEpochTasks {
		return nil, status.Error(codes.InvalidArgument, "anchor epoch seal envelope is invalid")
	}
	for i, id := range req.TaskIds {
		if len(id) != types.Hash32Len {
			return nil, status.Error(codes.InvalidArgument, "anchor epoch task_ids must be 32 bytes")
		}
		if i > 0 && bytes.Compare(req.TaskIds[i-1], id) >= 0 {
			return nil, status.Error(codes.InvalidArgument, "anchor epoch task_ids must be strictly ascending")
		}
	}
	if _, _, err := m.k.canonicalAddress("submitter_address", req.SubmitterAddress); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return nil, err
	}
	epochKey := types.Hash32Key(req.EpochManifestHash)
	if existing, err := m.k.AnchorEpoch.Get(ctx, epochKey); err == nil {
		if sameTaskIDs(existing.TaskIds, req.TaskIds) {
			if err := m.k.RequireCurrentServiceSubmitter(ctx, shared.ParticipantTypeCortexNode, existing.Worker, req.SubmitterAddress); err != nil {
				return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "submitter is not the winner Worker's current service")
			}
			return &types.MsgSealAnchorEpochResponse{XBeaconHeight: existing.XBeaconHeight, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP}, nil
		}
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "anchor epoch already sealed with different members")
	} else if !errors.Is(err, collections.ErrNotFound) {
		return nil, err
	}
	states := make([]types.AnchorAuditState, len(req.TaskIds))
	worker := ""
	maxOpening := uint64(0)
	for i, id := range req.TaskIds {
		taskKey := types.NewTaskKey(id)
		state, err := m.k.AnchorAudit.Get(ctx, taskKey)
		if err != nil {
			if errors.Is(err, collections.ErrNotFound) {
				return nil, errorsmod.Wrapf(types.ErrInvalidAssignment, "epoch member %x has no anchor audit", id)
			}
			return nil, err
		}
		switch {
		case !state.Sampled || !anchorSelected(state):
			return nil, errorsmod.Wrapf(types.ErrInvalidAssignment, "epoch member %x was not selected for anchor audit", id)
		case state.ResultHeight != 0:
			return nil, errorsmod.Wrapf(types.ErrInvalidAssignment, "epoch member %x already has a final compute verdict", id)
		case len(state.OpeningHash) != types.Hash32Len:
			return nil, errorsmod.Wrapf(types.ErrInvalidAssignment, "epoch member %x has not been opened", id)
		case len(state.WorkerProofHash) == types.Hash32Len:
			return nil, errorsmod.Wrapf(types.ErrInvalidAssignment, "epoch member %x was proved before its epoch was sealed", id)
		case state.EpochSealHeight != 0:
			return nil, errorsmod.Wrapf(types.ErrInvalidAssignment, "epoch member %x is already sealed into another epoch", id)
		}
		winner, err := m.k.anchorWinner(ctx, taskKey, state)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			worker = winner
		} else if winner != worker {
			return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "epoch members belong to different Workers")
		}
		if state.OpeningHeight > maxOpening {
			maxOpening = state.OpeningHeight
		}
		states[i] = state
	}
	if err := m.k.RequireCurrentServiceSubmitter(ctx, shared.ParticipantTypeCortexNode, worker, req.SubmitterAddress); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "submitter is not the winner Worker's current service")
	}
	// Every member opened at or before this block, so freeze + 1 > max opening height.
	xHeight, overflow := checkedHeightAdd(height, 1)
	if overflow {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "x beacon height overflows")
	}
	for i, state := range states {
		if state.ProofDeadlineHeight != 0 && xHeight > state.ProofDeadlineHeight {
			return nil, errorsmod.Wrapf(types.ErrInvalidAssignment, "epoch member %x: x beacon height %d is past its proof deadline %d",
				req.TaskIds[i], xHeight, state.ProofDeadlineHeight)
		}
	}
	for i := range states {
		states[i].EpochSealHeight = height
		states[i].XBeaconHeight = xHeight
		states[i].EpochManifestHash = append([]byte(nil), req.EpochManifestHash...)
		if err := m.k.AnchorAudit.Set(ctx, types.NewTaskKey(req.TaskIds[i]), states[i]); err != nil {
			return nil, err
		}
	}
	ids := make([][]byte, len(req.TaskIds))
	for i, id := range req.TaskIds {
		ids[i] = append([]byte(nil), id...)
	}
	if err := m.k.AnchorEpoch.Set(ctx, epochKey, types.AnchorEpochState{
		EpochManifestHash: append([]byte(nil), req.EpochManifestHash...), Worker: worker, TaskIds: ids,
		MaxOpeningHeight: maxOpening, SealHeight: height, XBeaconHeight: xHeight,
	}); err != nil {
		return nil, err
	}
	return &types.MsgSealAnchorEpochResponse{XBeaconHeight: xHeight, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED}, nil
}

func sameTaskIDs(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

// anchorComputeRejected reports whether the compute track condemns the Worker:
// a final REJECT, or (not yet recorded) the REJECT its deadline imposes because
// the Worker never opened or never proved.
func anchorComputeRejected(state types.AnchorAuditState) bool {
	if state.ResultHeight != 0 {
		return state.TaskVerdict == types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT
	}
	verdict, _ := anchorTimeoutVerdict(state)
	return verdict == types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT
}

// anchorContentOpen reports whether the audit has a content track still awaiting its verdict.
func anchorContentOpen(state types.AnchorAuditState) bool {
	return state.Sampled && anchorSelected(state) && state.ContentVerdictDeadlineHeight != 0 && state.ContentResultHeight == 0
}

// anchorOpenedNeverProved reports a content-track audit whose Worker opened the
// sampled rows and then never recorded a proof. Decision of 2026-11-09: that is a
// content fault (anchor_content_reject: slash and jail), not the smaller
// anchor_timeout — otherwise "open, then withhold the proof" would be the cheapest
// exit for a Worker whose content would fail the check. Never opening at all stays
// anchor_timeout: the Worker never entered the content track.
func anchorOpenedNeverProved(state types.AnchorAuditState) bool {
	return state.ContentVerdictDeadlineHeight != 0 && len(state.OpeningHash) == types.Hash32Len && len(state.WorkerProofHash) != types.Hash32Len
}

// anchorWorkerFaultForVerdict is the Hub fault type and slash bps that settlement
// applies for a REJECT compute verdict: the validators rejected the proof →
// anchor_reject; the deadline rejected because the Worker never opened →
// anchor_timeout; the Worker opened on the content track and never proved →
// anchor_content_reject (see anchorOpenedNeverProved).
func anchorWorkerFaultForVerdict(state types.AnchorAuditState, v types.VerificationParamsV1) (string, uint32) {
	if state.ResultSource != types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_TIMEOUT {
		return hubtypes.FaultTypeAnchorReject, v.AnchorRejectSlashBps
	}
	if anchorOpenedNeverProved(state) {
		return hubtypes.FaultTypeAnchorContentReject, v.AnchorContentRejectSlashBps
	}
	return hubtypes.FaultTypeAnchorTimeout, v.AnchorTimeoutSlashBps
}

// anchorContentDeadlineVerdict is the content verdict imposed when the content
// deadline passes without a final vote.
func anchorContentDeadlineVerdict(state types.AnchorAuditState) (types.AnchorContentVerdictV1, types.AnchorResultSourceV1, string) {
	switch {
	case anchorOpenedNeverProved(state):
		// Settlement normally applied this task's single fault already (as
		// anchor_content_reject, see anchorWorkerFaultForVerdict) and
		// finalizeAnchorContent then records the verdict without a second slash;
		// if settlement never reached the audit, the content slash is applied here.
		return types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_REJECT_CONTENT, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_TIMEOUT,
			"worker opened the sampled rows and never proved them: a content fault (anchor_content_reject)"
	case anchorComputeRejected(state):
		return types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_NO_VERDICT, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_COMPUTE_REJECTED,
			"compute verdict rejected the Worker; the content track closes without a second slash"
	case !state.WorkerProofXBound:
		return types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_REJECT_CONTENT, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_TIMEOUT,
			"worker never recorded a proof bound to a sealed B1 epoch (no Y_x rows)"
	default:
		return types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_NO_VERDICT, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_TIMEOUT,
			"validators did not reach two thirds of the voting power within the content window"
	}
}

// anchorContentRejectEvidenceDigest frames the facts a content slash is applied on.
func anchorContentRejectEvidenceDigest(chainID string, state types.AnchorAuditState) []byte {
	h := sha256.New()
	h.Write([]byte(anchorContentRejectDomainV1))
	h.Write([]byte(chainID))
	h.Write(state.TaskId)
	h.Write(state.OpeningHash)
	h.Write(state.WorkerProofHash)
	h.Write(state.XBeacon)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], state.ContentResultHeight)
	h.Write(buf[:])
	h.Write([]byte{byte(state.ContentVerdict), byte(state.ContentResultSource)})
	return h.Sum(nil)
}

// applyAnchorContentFault slashes D by anchor_content_reject_slash_bps and jails the
// Worker. It never fails the caller (a vote transaction or an EndBlock cleanup step):
// a hub refusal is recorded in the returned note and nothing is slashed.
func (k Keeper) applyAnchorContentFault(ctx context.Context, taskKey types.TaskKey, state types.AnchorAuditState, height uint64) ([]byte, string) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	fail := func(err error) ([]byte, string) {
		sdkCtx.Logger().Error("anchor content slash not applied", "task", fmt.Sprintf("%x", state.TaskId), "err", err)
		return nil, truncateAnchorReason("; slash not applied: " + err.Error())
	}
	core, err := k.TaskCore.Get(cache, taskKey)
	if err != nil {
		return fail(fmt.Errorf("task core unavailable: %w", err))
	}
	params, err := k.ParamsForTask(cache, taskKey)
	if err != nil {
		return fail(err)
	}
	winner, err := k.anchorWinner(cache, taskKey, state)
	if err != nil {
		return fail(err)
	}
	bps := params.Verification.AnchorContentRejectSlashBps
	applied, err := k.hubKeeper.ApplyTaskRoleFault(cache, hubtypes.TaskRoleFaultFact{
		SessionID: append([]byte(nil), core.SessionId...), TaskID: append([]byte(nil), state.TaskId...),
		OperatorAddress: winner, Duty: shared.DutyWorker, FaultType: hubtypes.FaultTypeAnchorContentReject,
		ClassificationSource: shared.FailureClassificationSource_FAILURE_CLASSIFICATION_SOURCE_SETTLEMENT,
		EvidenceDigest:       anchorContentRejectEvidenceDigest(sdkCtx.ChainID(), state),
		SlashBps:             bps, Height: height,
		RequestedAmount: anchorSlashAmount(state.SlashableBond, bps),
	})
	if err != nil {
		return fail(err)
	}
	write()
	return append([]byte(nil), applied.FaultId...), ""
}

// finalizeAnchorContent records a final content verdict, applies the content slash
// on REJECT_CONTENT (unless the compute track already slashed this task), persists
// the audit and emits the event.
func (k Keeper) finalizeAnchorContent(ctx context.Context, taskKey types.TaskKey, state *types.AnchorAuditState,
	verdict types.AnchorContentVerdictV1, source types.AnchorResultSourceV1, reason string, height uint64, strict bool,
) error {
	state.ContentVerdict = verdict
	state.ContentResultSource = source
	state.ContentResultHeight = height
	state.ContentResultReason = truncateAnchorReason(reason)
	if verdict == types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_REJECT_CONTENT && len(state.WorkerFaultId) != types.Hash32Len {
		faultID, note := k.applyAnchorContentFault(ctx, taskKey, *state, height)
		if strict && len(faultID) != types.Hash32Len {
			// On the vote path the transaction fails instead of recording a verdict
			// whose slash silently did not happen; the validator retries and the
			// content deadline still closes the track (audit C-2, 2026-11-09).
			return errorsmod.Wrap(types.ErrInvariantBroken, "anchor content slash could not be applied"+note)
		}
		state.ContentWorkerFaultId = faultID
		if note != "" {
			state.ContentResultReason = truncateAnchorReason(state.ContentResultReason + note)
		}
	}
	if err := k.AnchorAudit.Set(ctx, taskKey, *state); err != nil {
		return err
	}
	return emitTypedEvent(ctx, &types.EventAnchorContentVerdictFinalized{
		TaskId: append([]byte(nil), state.TaskId...), Verdict: state.ContentVerdict, Source: state.ContentResultSource,
		PassPower: state.ContentTally.PassPower, RejectPower: state.ContentTally.RejectPower, TotalPower: state.ContentTally.TotalPower,
		Reason: state.ContentResultReason,
	})
}

// closeAnchorContentAtCleanup closes an open content track when evidence cleanup
// reaches the audit (cleanup is held until the content deadline, see
// anchorContentCleanupHold). Reports whether it changed the audit.
func (k Keeper) closeAnchorContentAtCleanup(ctx context.Context, taskKey types.TaskKey, state *types.AnchorAuditState) (bool, error) {
	if !anchorContentOpen(*state) {
		return false, nil
	}
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return false, err
	}
	verdict, source, reason := anchorContentDeadlineVerdict(*state)
	if height < state.ContentVerdictDeadlineHeight && source != types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_COMPUTE_REJECTED {
		// Only reachable for state written before cleanup was held for the content
		// track: never condemn the Worker before its window has run out.
		verdict, source = types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_NO_VERDICT, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_TIMEOUT
		reason = "evidence cleanup reached the audit before its content deadline"
	}
	return true, k.finalizeAnchorContent(ctx, taskKey, state, verdict, source, reason, height, false)
}

// anchorContentCleanupHold is the height evidence cleanup of a task must wait for
// while its content verdict is open (zero: no hold).
func (k Keeper) anchorContentCleanupHold(ctx context.Context, taskKey types.TaskKey, height uint64) (uint64, error) {
	state, err := k.AnchorAudit.Get(ctx, taskKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	if !anchorContentOpen(state) || anchorComputeRejected(state) || height >= state.ContentVerdictDeadlineHeight {
		return 0, nil
	}
	return state.ContentVerdictDeadlineHeight, nil
}

// SubmitAnchorContentVote records a bonded validator's content verdict (ADR-0040 v7).
func (m msgServer) SubmitAnchorContentVote(ctx context.Context, req *types.MsgSubmitAnchorContentVote) (*types.MsgSubmitAnchorContentVoteResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "anchor content vote request is required")
	}
	if len(req.TaskId) != types.Hash32Len || len(req.ProofHash) != types.Hash32Len ||
		(len(req.XBeacon) != 0 && len(req.XBeacon) != types.Hash32Len) || len(req.Verdicts) > maxAnchorContentPositionVerdicts {
		return nil, status.Error(codes.InvalidArgument, "anchor content vote envelope is invalid")
	}
	switch req.Verdict {
	case types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_PASS, types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_REJECT_CONTENT,
		types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_NO_VERDICT:
	default:
		return nil, status.Error(codes.InvalidArgument, "anchor content vote verdict is invalid")
	}
	for _, v := range req.Verdicts {
		if v.Verdict == types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_UNSPECIFIED || len(v.Reason) > maxAnchorVerdictReasonBytes {
			return nil, status.Error(codes.InvalidArgument, "anchor content vote position verdict is invalid")
		}
	}
	voter, err := m.k.anchorVoter(ctx, req.ValidatorOperatorAddress, req.SubmitterAddress)
	if err != nil {
		return nil, err
	}
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return nil, err
	}
	taskKey := types.NewTaskKey(req.TaskId)
	state, err := m.k.AnchorAudit.Get(ctx, taskKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "task has no anchor audit sample")
		}
		return nil, err
	}
	if !state.Sampled || !anchorSelected(state) {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "task was not selected for anchor audit")
	}
	if state.ContentVerdictDeadlineHeight == 0 {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "anchor audit has no content track")
	}
	if len(state.WorkerProofHash) != types.Hash32Len {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "worker has not recorded a proof yet")
	}
	if !bytes.Equal(state.WorkerProofHash, req.ProofHash) {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "vote names a proof other than the Worker's recorded proof")
	}
	if !bytes.Equal(state.XBeacon, req.XBeacon) {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "vote names an x beacon other than the one the chain recorded")
	}
	if state.ContentResultHeight != 0 {
		return &types.MsgSubmitAnchorContentVoteResponse{ContentVerdict: state.ContentVerdict, Finalized: true, Tally: state.ContentTally,
			Status: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP}, nil
	}
	if state.ResultHeight != 0 && state.TaskVerdict == types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "compute verdict rejected the Worker; the content track is closed")
	}
	if height >= state.ContentVerdictDeadlineHeight {
		return nil, errorsmod.Wrapf(types.ErrInvalidAssignment, "anchor content verdict deadline %d has passed", state.ContentVerdictDeadlineHeight)
	}
	// Settlement is never held for the content verdict: the track opens once the
	// task has settled on its compute verdict, so a content slash cannot race the
	// settlement of the Worker's task reservation.
	if settled, err := m.k.taskAuthorityIsFinal(ctx, taskKey); err != nil {
		return nil, err
	} else if !settled {
		return nil, errorsmod.Wrap(types.ErrInvalidTaskStatus, "anchor content votes open once the task has settled on its compute verdict")
	}
	if !state.WorkerProofXBound {
		// The content deadline decides this case deterministically (REJECT_CONTENT,
		// source TIMEOUT): a vote on a proof without Y_x rows would only let a
		// validator quorum overturn it (audit C-5, 2026-11-09).
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "anchor content votes need a proof bound to a sealed B1 epoch; an unbound proof is closed by the content deadline")
	}
	voteKey := collections.Join(taskKey, voter.valAddr)
	if existing, err := m.k.AnchorContentVote.Get(ctx, voteKey); err == nil {
		if existing.Verdict == req.Verdict && bytes.Equal(existing.ProofHash, req.ProofHash) {
			return &types.MsgSubmitAnchorContentVoteResponse{ContentVerdict: state.ContentVerdict, Tally: state.ContentTally,
				Status: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP}, nil
		}
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "validator already cast a content vote on this audit")
	} else if !errors.Is(err, collections.ErrNotFound) {
		return nil, err
	}
	vote := types.AnchorContentVoteState{
		TaskId: append([]byte(nil), req.TaskId...), ValidatorOperatorAddress: req.ValidatorOperatorAddress, VoterAddress: req.SubmitterAddress,
		Verdict: req.Verdict, ProofHash: append([]byte(nil), req.ProofHash...), XBeacon: append([]byte(nil), req.XBeacon...),
		Height: height, Power: voter.power, Verdicts: append([]types.AnchorContentPositionVerdictV1(nil), req.Verdicts...),
	}
	if err := m.k.AnchorContentVote.Set(ctx, voteKey, vote); err != nil {
		return nil, err
	}
	// Weighed at current power, same bonded set for numerator and denominator
	// (see anchorRecountComputeTally for why vote-time power must not be summed).
	tally, err := m.k.anchorRecountContentTally(ctx, taskKey)
	if err != nil {
		return nil, err
	}
	state.ContentTally = tally
	final := types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_UNSPECIFIED
	switch {
	case anchorQuorum(tally.PassPower, tally.TotalPower):
		final = types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_PASS
	case anchorQuorum(tally.RejectPower, tally.TotalPower):
		final = types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_REJECT_CONTENT
	case tally.PassPower+tally.RejectPower+tally.NoVerdictPower >= tally.TotalPower:
		final = types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_NO_VERDICT
	}
	if final == types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_UNSPECIFIED {
		if err := m.k.AnchorAudit.Set(ctx, taskKey, state); err != nil {
			return nil, err
		}
		return &types.MsgSubmitAnchorContentVoteResponse{Tally: state.ContentTally, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED}, nil
	}
	if req.Verdict == final && len(vote.Verdicts) > 0 {
		state.ContentVerdicts = vote.Verdicts
	}
	reason := fmt.Sprintf("content vote: pass %d / reject %d / no-verdict %d of %d power (%d votes)",
		tally.PassPower, tally.RejectPower, tally.NoVerdictPower, tally.TotalPower, tally.VoteCount)
	if err := m.k.finalizeAnchorContent(ctx, taskKey, &state, final, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_VALIDATOR_VOTE, reason, height, true); err != nil {
		return nil, err
	}
	return &types.MsgSubmitAnchorContentVoteResponse{ContentVerdict: state.ContentVerdict, Finalized: true, Tally: state.ContentTally,
		Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED}, nil
}

// AnchorEpoch returns one sealed B1 epoch and, once the chain reached its x beacon
// height, the derived x.
func (q queryServer) AnchorEpoch(ctx context.Context, req *types.QueryAnchorEpochRequest) (*types.QueryAnchorEpochResponse, error) {
	if req == nil || len(req.EpochManifestHash) != types.Hash32Len {
		return nil, status.Error(codes.InvalidArgument, "epoch_manifest_hash must be 32 bytes")
	}
	epoch, err := q.k.AnchorEpoch.Get(ctx, types.Hash32Key(req.EpochManifestHash))
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "no sealed anchor epoch")
		}
		return nil, err
	}
	x := append([]byte(nil), epoch.XBeacon...)
	if len(x) == 0 {
		if derived, ok := q.k.deriveAnchorXBeacon(ctx, epoch.XBeaconHeight); ok {
			x = derived
		}
	}
	return &types.QueryAnchorEpochResponse{Epoch: epoch, XBeacon: x}, nil
}

// AnchorContentVotes returns the content votes and tally of one audit.
func (q queryServer) AnchorContentVotes(ctx context.Context, req *types.QueryAnchorContentVotesRequest) (*types.QueryAnchorContentVotesResponse, error) {
	if req == nil || len(req.TaskId) != types.Hash32Len {
		return nil, status.Error(codes.InvalidArgument, "task_id must be 32 bytes")
	}
	taskKey := types.NewTaskKey(req.TaskId)
	state, err := q.k.AnchorAudit.Get(ctx, taskKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "no anchor audit for task")
		}
		return nil, err
	}
	iter, err := q.k.AnchorContentVote.Iterate(ctx, collections.NewPrefixedPairRange[types.TaskKey, []byte](taskKey))
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	votes, err := iter.Values()
	if err != nil {
		return nil, err
	}
	return &types.QueryAnchorContentVotesResponse{Votes: votes, Tally: state.ContentTally}, nil
}
