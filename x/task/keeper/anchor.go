package keeper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// Anchor commitment audit, first slice of ADR-0040 (proposed). The Worker's
// InferReceipt carries a third evidence commitment (WORKER_ANCHOR_OPENING);
// when the round-1 Verifier assignment is finalized, the chain samples the
// positions to open from that assignment's selection beacon, the Worker
// records the opening hash, and a selected Verifier records the per-position
// verdicts. Sampling policy, escrow and slashing are not in this slice.
const (
	// anchorSamplePositionsV0 is the number of positions sampled per task.
	anchorSamplePositionsV0 = 2
	// anchorSampleDomainV0 separates the position draw from every other use of
	// the selection beacon.
	anchorSampleDomainV0 = "TRUEOPEN_ANCHOR_SAMPLE_V0"
	// maxAnchorOpeningURIBytes bounds the fetch hint recorded on chain.
	maxAnchorOpeningURIBytes = 512
	// maxAnchorVerdictReasonBytes bounds the informational diagnostic.
	maxAnchorVerdictReasonBytes = 256
	// tokenIDsRawOverheadBytes is the two u32 counts of the A-level artifact
	// pair (input_token_ids_raw and generated_token_ids_raw).
	tokenIDsRawOverheadBytes = 8
)

func anchorCommitmentOf(receipt types.InferReceiptState) (types.EvidenceCommitmentV1, bool) {
	for _, item := range receipt.RequiredEvidenceCommitments {
		if item.EvidenceKind == shared.EvidenceKind_EVIDENCE_KIND_WORKER_ANCHOR_OPENING {
			return item, true
		}
	}
	return types.EvidenceCommitmentV1{}, false
}

// anchorRowsOf derives the committed row count (prompt + generated − 1) from
// the A-level token commitment size: the artifact pair encodes a u32 count and
// one u32 per token id for each sequence, so (size − 8) / 4 = L + G.
func anchorRowsOf(receipt types.InferReceiptState) (uint64, error) {
	for _, item := range receipt.RequiredEvidenceCommitments {
		if item.EvidenceKind != shared.EvidenceKind_EVIDENCE_KIND_WORKER_TOKEN_OPENING {
			continue
		}
		if item.EncodedSizeBytes < tokenIDsRawOverheadBytes+8 || (item.EncodedSizeBytes-tokenIDsRawOverheadBytes)%4 != 0 {
			return 0, fmt.Errorf("worker token evidence size %d is not a token id pair", item.EncodedSizeBytes)
		}
		total := (item.EncodedSizeBytes - tokenIDsRawOverheadBytes) / 4
		if total <= receipt.GeneratedTokenCount || total < 2 {
			return 0, fmt.Errorf("worker token evidence encodes %d ids, fewer than the %d generated", total, receipt.GeneratedTokenCount)
		}
		return total - 1, nil
	}
	return 0, errors.New("receipt has no worker token evidence commitment")
}

// anchorSamplePositions draws count distinct positions in [0, rows) from the
// beacon; the draw index keeps advancing past duplicates so the result is a
// deterministic function of (task, beacon, rows).
func anchorSamplePositions(taskID, beacon []byte, rows uint64, count int) []uint64 {
	seen := map[uint64]struct{}{}
	out := make([]uint64, 0, count)
	for i := uint32(0); len(out) < count && i < 64; i++ {
		h := sha256.New()
		h.Write([]byte(anchorSampleDomainV0))
		h.Write(taskID)
		h.Write(beacon)
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
	for i := 1; i < len(out); i++ { // ascending for the on-chain record
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// sampleAnchorAudit writes the AnchorAuditState of a task whose receipt
// carries an anchor commitment, once its round-1 Verifier assignment is
// finalized. Tasks without the commitment, and later rounds, are ignored.
func (k Keeper) sampleAnchorAudit(ctx context.Context, assignment types.VerifierAssignmentState) error {
	if assignment.VerifyRound != 1 || len(assignment.TaskId) != types.Hash32Len {
		return nil
	}
	taskKey := types.NewTaskKey(assignment.TaskId)
	if has, err := k.AnchorAudit.Has(ctx, taskKey); err != nil || has {
		return err
	}
	receipt, err := k.ReadInferReceipt(ctx, taskKey)
	if err != nil {
		return nil // no accepted receipt: nothing to audit
	}
	if _, ok := anchorCommitmentOf(receipt); !ok {
		return nil
	}
	rows, err := anchorRowsOf(receipt)
	if err != nil {
		return fmt.Errorf("anchor audit %x: %w", assignment.TaskId, err)
	}
	state := types.AnchorAuditState{
		TaskId:       append([]byte(nil), assignment.TaskId...),
		Sampled:      true,
		Positions:    anchorSamplePositions(assignment.TaskId, assignment.SelectionRandomnessBeacon, rows, anchorSamplePositionsV0),
		SampleBeacon: append([]byte(nil), assignment.SelectionRandomnessBeacon...),
		SampleHeight: assignment.SelectionRandomnessHeight,
	}
	return k.AnchorAudit.Set(ctx, taskKey, state)
}

func positionsEqual(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (m msgServer) SubmitAnchorOpening(ctx context.Context, req *types.MsgSubmitAnchorOpening) (*types.MsgSubmitAnchorOpeningResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "anchor opening request is required")
	}
	if len(req.TaskId) != types.Hash32Len || len(req.OpeningHash) != types.Hash32Len || bytes.Equal(req.OpeningHash, make([]byte, types.Hash32Len)) ||
		req.OpeningSizeBytes == 0 || len(req.Positions) == 0 || strings.TrimSpace(req.OpeningUri) == "" || len(req.OpeningUri) > maxAnchorOpeningURIBytes {
		return nil, status.Error(codes.InvalidArgument, "anchor opening envelope is invalid")
	}
	for i := 1; i < len(req.Positions); i++ {
		if req.Positions[i] <= req.Positions[i-1] {
			return nil, status.Error(codes.InvalidArgument, "anchor opening positions must be strictly ascending")
		}
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
	if !positionsEqual(state.Positions, req.Positions) {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "anchor opening positions do not match the sampled positions")
	}
	receipt, err := m.k.ReadInferReceipt(ctx, taskKey)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "accepted infer receipt is unavailable")
	}
	if err := m.k.requireInferReceiptSubmitter(ctx, taskKey, req.TaskId, receipt.WinnerWorker, req.SubmitterAddress); err != nil {
		return nil, err
	}
	if len(state.OpeningHash) == types.Hash32Len {
		if bytes.Equal(state.OpeningHash, req.OpeningHash) && state.OpeningSizeBytes == req.OpeningSizeBytes {
			return &types.MsgSubmitAnchorOpeningResponse{Status: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP}, nil
		}
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "anchor opening already recorded with a different hash")
	}
	state.OpeningHash = append([]byte(nil), req.OpeningHash...)
	state.OpeningSizeBytes = req.OpeningSizeBytes
	state.OpeningUri = req.OpeningUri
	state.OpeningHeight = height
	if err := m.k.AnchorAudit.Set(ctx, taskKey, state); err != nil {
		return nil, err
	}
	return &types.MsgSubmitAnchorOpeningResponse{Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED}, nil
}

func anchorTaskVerdict(verdicts []types.AnchorPositionVerdictV1) types.AnchorVerdictV1 {
	out := types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS
	for _, v := range verdicts {
		switch v.Verdict {
		case types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT:
			return types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT
		case types.AnchorVerdictV1_ANCHOR_VERDICT_V1_NO_VERDICT:
			out = types.AnchorVerdictV1_ANCHOR_VERDICT_V1_NO_VERDICT
		}
	}
	return out
}

func (m msgServer) SubmitAnchorAuditResult(ctx context.Context, req *types.MsgSubmitAnchorAuditResult) (*types.MsgSubmitAnchorAuditResultResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "anchor audit result request is required")
	}
	if len(req.TaskId) != types.Hash32Len || len(req.OpeningHash) != types.Hash32Len || len(req.Verdicts) == 0 ||
		(len(req.ProofHash) != 0 && len(req.ProofHash) != types.Hash32Len) {
		return nil, status.Error(codes.InvalidArgument, "anchor audit result envelope is invalid")
	}
	positions := make([]uint64, len(req.Verdicts))
	for i, v := range req.Verdicts {
		if v.Verdict == types.AnchorVerdictV1_ANCHOR_VERDICT_V1_UNSPECIFIED || len(v.Reason) > maxAnchorVerdictReasonBytes {
			return nil, status.Error(codes.InvalidArgument, "anchor audit verdict is invalid")
		}
		positions[i] = v.Position
	}
	_, submitter, err := m.k.canonicalAddress("submitter_address", req.SubmitterAddress)
	if err != nil {
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
			return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "task has no anchor audit sample")
		}
		return nil, err
	}
	if len(state.OpeningHash) != types.Hash32Len {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "anchor opening has not been recorded")
	}
	if !bytes.Equal(state.OpeningHash, req.OpeningHash) {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "audit result names a different opening than the chain recorded")
	}
	if !positionsEqual(state.Positions, positions) {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "audit verdicts do not cover exactly the sampled positions")
	}
	assignment, err := m.k.ReadVerifierAssignment(ctx, types.NewVerifyRoundKey(req.TaskId, 1))
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "round 1 verifier assignment is unavailable")
	}
	var verifier string
	for _, selected := range assignment.SelectedVerifiers {
		if m.k.RequireCurrentServiceSubmitter(ctx, shared.ParticipantTypeCortexNode, selected.OperatorAddress, submitter) == nil {
			verifier = selected.OperatorAddress
			break
		}
	}
	if verifier == "" {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "submitter is not a selected round 1 Verifier current service")
	}
	if state.ResultHeight != 0 {
		return &types.MsgSubmitAnchorAuditResultResponse{TaskVerdict: state.TaskVerdict, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP}, nil
	}
	state.ResultVerifierOperatorAddress = verifier
	state.Verdicts = make([]types.AnchorPositionVerdictV1, len(req.Verdicts))
	for i, v := range req.Verdicts {
		state.Verdicts[i] = types.AnchorPositionVerdictV1{Position: v.Position, Verdict: v.Verdict, Reason: v.Reason}
	}
	state.ProofHash = append([]byte(nil), req.ProofHash...)
	state.ResultHeight = height
	state.TaskVerdict = anchorTaskVerdict(state.Verdicts)
	if err := m.k.AnchorAudit.Set(ctx, taskKey, state); err != nil {
		return nil, err
	}
	return &types.MsgSubmitAnchorAuditResultResponse{TaskVerdict: state.TaskVerdict, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED}, nil
}

func (q queryServer) AnchorAudit(ctx context.Context, req *types.QueryAnchorAuditRequest) (*types.QueryAnchorAuditResponse, error) {
	if req == nil || len(req.TaskId) != types.Hash32Len {
		return nil, status.Error(codes.InvalidArgument, "task_id must be 32 bytes")
	}
	state, err := q.k.AnchorAudit.Get(ctx, types.NewTaskKey(req.TaskId))
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "no anchor audit for task")
		}
		return nil, err
	}
	return &types.QueryAnchorAuditResponse{Audit: state}, nil
}
