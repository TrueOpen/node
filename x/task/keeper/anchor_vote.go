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
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// ADR-0040 v4: the anchor verdict is decided by the consensus validators, not by
// the round-1 Verifiers. Each bonded validator checks the Worker's proof package
// off-chain (CPU, no weights) and votes; one side holding at least two thirds of
// the last total voting power makes the verdict final. Settlement of a PASS round
// waits for that verdict until verdict_deadline_height; a REJECT verdict (or a
// Worker that never opened / never proved by the deadline) fails the task and
// slashes the Worker's task reservation.

// anchorVerdictDeadline is the height by which the audit verdict must be final.
// Zero means the audit predates v4: it is never held on and never slashed.
func anchorVerdictDeadline(state types.AnchorAuditState) uint64 {
	return state.VerdictDeadlineHeight
}

// anchorQuorum reports whether power is at least two thirds of total.
func anchorQuorum(power, total uint64) bool {
	if total == 0 {
		return false
	}
	return sdkmath.NewIntFromUint64(power).MulRaw(3).GTE(sdkmath.NewIntFromUint64(total).MulRaw(2))
}

// anchorTimeoutVerdict is the verdict the deadline imposes on an audit that has
// no final vote: the Worker's own omissions are REJECT, a silent validator set
// is NO_VERDICT (the Worker is paid, nothing is slashed).
func anchorTimeoutVerdict(state types.AnchorAuditState) (types.AnchorVerdictV1, string) {
	switch {
	case len(state.OpeningHash) != types.Hash32Len:
		return types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT, "worker did not open the sampled rows before the verdict deadline"
	case len(state.WorkerProofHash) != types.Hash32Len:
		return types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT, "worker did not prove its opening before the verdict deadline"
	default:
		return types.AnchorVerdictV1_ANCHOR_VERDICT_V1_NO_VERDICT, "validators did not reach two thirds of the voting power before the verdict deadline"
	}
}

func emitAnchorVerdictEvent(ctx context.Context, state types.AnchorAuditState) error {
	return emitTypedEvent(ctx, &types.EventAnchorVerdictFinalized{
		TaskId: append([]byte(nil), state.TaskId...), Verdict: state.TaskVerdict, Source: state.ResultSource,
		PassPower: state.Tally.PassPower, RejectPower: state.Tally.RejectPower, TotalPower: state.Tally.TotalPower,
		Reason: state.ResultReason,
	})
}

// anchorSettlementView returns the audit as settlement must read it at height:
// nil when the task has no (relevant) audit, the recorded final verdict, or the
// verdict the passed deadline imposes (ResultHeight still zero: the caller
// persists it). While a PASS round's verdict is still due it returns the
// ErrInvalidTaskStatus "pending until" error that holds settlement.
func (k Keeper) anchorSettlementView(ctx context.Context, taskKey types.TaskKey, roundVerdict types.TaskVerdict, height uint64) (*types.AnchorAuditState, error) {
	state, err := k.AnchorAudit.Get(ctx, taskKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if !state.Sampled {
		return nil, nil
	}
	if state.ResultHeight != 0 {
		return &state, nil
	}
	deadline := anchorVerdictDeadline(state)
	if deadline == 0 {
		return nil, nil // pre-v4 audit: advisory only
	}
	if height < deadline {
		if roundVerdict != types.TaskVerdict_TASK_VERDICT_PASS {
			return nil, nil // the Worker is not paid either way: nothing to hold on
		}
		return nil, errorsmod.Wrapf(types.ErrInvalidTaskStatus, "anchor audit verdict is pending until height %d", deadline)
	}
	state.TaskVerdict, state.ResultReason = anchorTimeoutVerdict(state)
	state.ResultSource = types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_TIMEOUT
	return &state, nil
}

// anchorSettlementHoldHeight is the height a PASS round must wait for before it
// settles (zero: no hold). Used to place the settlement deadline and to report
// the next deadline to Builders. The effective round is read only when an audit
// is actually pending, so tasks without one never touch round state here.
func (k Keeper) anchorSettlementHoldHeight(ctx context.Context, taskKey types.TaskKey, effectiveRound uint32) (uint64, error) {
	state, err := k.AnchorAudit.Get(ctx, taskKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	if !state.Sampled || state.ResultHeight != 0 || anchorVerdictDeadline(state) == 0 {
		return 0, nil
	}
	round, err := k.ReadVerificationRound(ctx, types.NewVerifyRoundKey(taskKey, effectiveRound))
	if err != nil {
		return 0, err
	}
	if round.GetVerdict() != types.TaskVerdict_TASK_VERDICT_PASS {
		return 0, nil
	}
	return anchorVerdictDeadline(state), nil
}

// anchorRejectEvidenceDigest frames the facts a REJECT slash is applied on.
func anchorRejectEvidenceDigest(chainID string, state types.AnchorAuditState) []byte {
	h := sha256.New()
	h.Write([]byte("TRUEOPEN_ANCHOR_REJECT_EVIDENCE_V1"))
	h.Write([]byte(chainID))
	h.Write(state.TaskId)
	h.Write(state.OpeningHash)
	h.Write(state.WorkerProofHash)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], state.ResultHeight)
	h.Write(buf[:])
	h.Write([]byte{byte(state.TaskVerdict), byte(state.ResultSource)})
	return h.Sum(nil)
}

// SubmitAnchorValidatorVote records a bonded validator's verdict on the Worker's
// recorded proof. The signer must be the validator operator's account; a
// validator votes once per audit; the verdict is final at two thirds of the last
// total voting power, or NO_VERDICT once every bonded validator has voted
// without a side reaching it.
func (m msgServer) SubmitAnchorValidatorVote(ctx context.Context, req *types.MsgSubmitAnchorValidatorVote) (*types.MsgSubmitAnchorValidatorVoteResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "anchor validator vote request is required")
	}
	if len(req.TaskId) != types.Hash32Len || len(req.OpeningHash) != types.Hash32Len || len(req.ProofHash) != types.Hash32Len {
		return nil, status.Error(codes.InvalidArgument, "anchor validator vote envelope is invalid")
	}
	switch req.Verdict {
	case types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS, types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT, types.AnchorVerdictV1_ANCHOR_VERDICT_V1_NO_VERDICT:
	default:
		return nil, status.Error(codes.InvalidArgument, "anchor validator vote verdict is invalid")
	}
	for _, v := range req.Verdicts {
		if v.Verdict == types.AnchorVerdictV1_ANCHOR_VERDICT_V1_UNSPECIFIED || len(v.Reason) > maxAnchorVerdictReasonBytes {
			return nil, status.Error(codes.InvalidArgument, "anchor validator vote position verdict is invalid")
		}
	}
	if m.k.stakingKeeper == nil {
		return nil, status.Error(codes.FailedPrecondition, "anchor validator votes require the staking keeper")
	}
	signer, _, err := m.k.canonicalAddress("submitter_address", req.SubmitterAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	valAddr, err := m.k.stakingKeeper.ValidatorAddressCodec().StringToBytes(req.ValidatorOperatorAddress)
	if err != nil || len(valAddr) == 0 {
		return nil, status.Error(codes.InvalidArgument, "validator_operator_address is invalid")
	}
	if !bytes.Equal(signer, valAddr) {
		return nil, errorsmod.Wrap(types.ErrInvalidSignature, "submitter is not the validator operator account")
	}
	validator, err := m.k.stakingKeeper.GetValidator(ctx, sdk.ValAddress(valAddr))
	if err != nil || !validator.IsBonded() {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "validator is not bonded")
	}
	power, err := m.k.stakingKeeper.GetLastValidatorPower(ctx, sdk.ValAddress(valAddr))
	if err != nil || power <= 0 {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "validator has no voting power")
	}
	total, err := m.k.stakingKeeper.GetLastTotalPower(ctx)
	if err != nil || !total.IsPositive() || !total.IsUint64() {
		return nil, errorsmod.Wrap(types.ErrInvariantBroken, "last total voting power is unavailable")
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
	if len(state.OpeningHash) != types.Hash32Len || !bytes.Equal(state.OpeningHash, req.OpeningHash) {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "vote names a different opening than the chain recorded")
	}
	if len(state.WorkerProofHash) != types.Hash32Len {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "worker has not recorded a proof yet")
	}
	if !bytes.Equal(state.WorkerProofHash, req.ProofHash) {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "vote names a proof other than the Worker's recorded proof")
	}
	if state.ResultHeight != 0 {
		return &types.MsgSubmitAnchorValidatorVoteResponse{TaskVerdict: state.TaskVerdict, Finalized: true, Tally: state.Tally, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP}, nil
	}
	if deadline := anchorVerdictDeadline(state); deadline != 0 && height >= deadline {
		return nil, errorsmod.Wrapf(types.ErrInvalidAssignment, "anchor verdict deadline %d has passed", deadline)
	}
	voteKey := collections.Join(taskKey, []byte(valAddr))
	if existing, err := m.k.AnchorValidatorVote.Get(ctx, voteKey); err == nil {
		if existing.Verdict == req.Verdict && bytes.Equal(existing.ProofHash, req.ProofHash) {
			return &types.MsgSubmitAnchorValidatorVoteResponse{TaskVerdict: state.TaskVerdict, Tally: state.Tally, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP}, nil
		}
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "validator already voted on this audit")
	} else if !errors.Is(err, collections.ErrNotFound) {
		return nil, err
	}
	vote := types.AnchorValidatorVoteState{
		TaskId: append([]byte(nil), req.TaskId...), ValidatorOperatorAddress: req.ValidatorOperatorAddress, VoterAddress: req.SubmitterAddress,
		Verdict: req.Verdict, ProofHash: append([]byte(nil), req.ProofHash...), Height: height, Power: uint64(power),
		Verdicts: make([]types.AnchorPositionVerdictV1, len(req.Verdicts)),
	}
	for i, v := range req.Verdicts {
		vote.Verdicts[i] = types.AnchorPositionVerdictV1{Position: v.Position, Verdict: v.Verdict, Reason: v.Reason}
	}
	if err := m.k.AnchorValidatorVote.Set(ctx, voteKey, vote); err != nil {
		return nil, err
	}
	tally := state.Tally
	tally.TotalPower = total.Uint64()
	tally.VoteCount++
	switch req.Verdict {
	case types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS:
		tally.PassPower += uint64(power)
	case types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT:
		tally.RejectPower += uint64(power)
	default:
		tally.NoVerdictPower += uint64(power)
	}
	state.Tally = tally
	final := types.AnchorVerdictV1_ANCHOR_VERDICT_V1_UNSPECIFIED
	switch {
	case anchorQuorum(tally.PassPower, tally.TotalPower):
		final = types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS
	case anchorQuorum(tally.RejectPower, tally.TotalPower):
		final = types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT
	case tally.PassPower+tally.RejectPower+tally.NoVerdictPower >= tally.TotalPower:
		final = types.AnchorVerdictV1_ANCHOR_VERDICT_V1_NO_VERDICT // every bonded validator voted; no side reached two thirds
	}
	if final != types.AnchorVerdictV1_ANCHOR_VERDICT_V1_UNSPECIFIED {
		state.ResultHeight = height
		state.TaskVerdict = final
		state.ResultSource = types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_VALIDATOR_VOTE
		state.ResultReason = fmt.Sprintf("validator vote: pass %d / reject %d / no-verdict %d of %d power (%d votes)",
			tally.PassPower, tally.RejectPower, tally.NoVerdictPower, tally.TotalPower, tally.VoteCount)
		state.ProofHash = append([]byte(nil), state.WorkerProofHash...)
		if req.Verdict == final && len(vote.Verdicts) > 0 {
			state.Verdicts = vote.Verdicts
		}
	}
	if err := m.k.AnchorAudit.Set(ctx, taskKey, state); err != nil {
		return nil, err
	}
	if final != types.AnchorVerdictV1_ANCHOR_VERDICT_V1_UNSPECIFIED {
		if err := emitAnchorVerdictEvent(ctx, state); err != nil {
			return nil, err
		}
	}
	return &types.MsgSubmitAnchorValidatorVoteResponse{
		TaskVerdict: state.TaskVerdict, Finalized: final != types.AnchorVerdictV1_ANCHOR_VERDICT_V1_UNSPECIFIED,
		Tally: state.Tally, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED,
	}, nil
}

func (q queryServer) AnchorVotes(ctx context.Context, req *types.QueryAnchorVotesRequest) (*types.QueryAnchorVotesResponse, error) {
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
	iter, err := q.k.AnchorValidatorVote.Iterate(ctx, collections.NewPrefixedPairRange[types.TaskKey, []byte](taskKey))
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	votes, err := iter.Values()
	if err != nil {
		return nil, err
	}
	return &types.QueryAnchorVotesResponse{Votes: votes, Tally: state.Tally}, nil
}
