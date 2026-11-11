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

// anchorCurrentPower is the bonded power a validator holds now: zero once it is
// unbonded or unknown. Tallies weigh votes with it, so a vote never outlives its
// validator's stake and a stake moved to another validator votes only there.
func (k Keeper) anchorCurrentPower(ctx context.Context, valAddr []byte) uint64 {
	validator, err := k.stakingKeeper.GetValidator(ctx, sdk.ValAddress(valAddr))
	if err != nil || !validator.IsBonded() {
		return 0
	}
	power, err := k.stakingKeeper.GetLastValidatorPower(ctx, sdk.ValAddress(valAddr))
	if err != nil || power <= 0 {
		return 0
	}
	return uint64(power)
}

// anchorCurrentTotalPower is the bonded set's current total power.
func (k Keeper) anchorCurrentTotalPower(ctx context.Context) (uint64, error) {
	total, err := k.stakingKeeper.GetLastTotalPower(ctx)
	if err != nil || !total.IsPositive() || !total.IsUint64() {
		return 0, errorsmod.Wrap(types.ErrInvariantBroken, "last total voting power is unavailable")
	}
	return total.Uint64(), nil
}

// anchorRecountComputeTally rebuilds the compute-track tally from the task's
// stored votes at the validators' current power (see anchorCurrentPower). Vote
// counts are the number of stored votes; numerator and denominator are always
// the same bonded set.
func (k Keeper) anchorRecountComputeTally(ctx context.Context, taskKey types.TaskKey) (types.AnchorVoteTallyV1, error) {
	iter, err := k.AnchorValidatorVote.Iterate(ctx, collections.NewPrefixedPairRange[types.TaskKey, []byte](taskKey))
	if err != nil {
		return types.AnchorVoteTallyV1{}, err
	}
	defer iter.Close()
	kvs, err := iter.KeyValues()
	if err != nil {
		return types.AnchorVoteTallyV1{}, err
	}
	var tally types.AnchorVoteTallyV1
	for _, kv := range kvs {
		power := k.anchorCurrentPower(ctx, kv.Key.K2())
		tally.VoteCount++
		switch kv.Value.Verdict {
		case types.AnchorVerdictV1_ANCHOR_VERDICT_V1_PASS:
			tally.PassPower += power
		case types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT:
			tally.RejectPower += power
		default:
			tally.NoVerdictPower += power
		}
	}
	tally.TotalPower, err = k.anchorCurrentTotalPower(ctx)
	return tally, err
}

// anchorRecountContentTally is anchorRecountComputeTally for the content track.
func (k Keeper) anchorRecountContentTally(ctx context.Context, taskKey types.TaskKey) (types.AnchorVoteTallyV1, error) {
	iter, err := k.AnchorContentVote.Iterate(ctx, collections.NewPrefixedPairRange[types.TaskKey, []byte](taskKey))
	if err != nil {
		return types.AnchorVoteTallyV1{}, err
	}
	defer iter.Close()
	kvs, err := iter.KeyValues()
	if err != nil {
		return types.AnchorVoteTallyV1{}, err
	}
	var tally types.AnchorVoteTallyV1
	for _, kv := range kvs {
		power := k.anchorCurrentPower(ctx, kv.Key.K2())
		tally.VoteCount++
		switch kv.Value.Verdict {
		case types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_PASS:
			tally.PassPower += power
		case types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_REJECT_CONTENT:
			tally.RejectPower += power
		default:
			tally.NoVerdictPower += power
		}
	}
	tally.TotalPower, err = k.anchorCurrentTotalPower(ctx)
	return tally, err
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
		return types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT, "worker did not open the sampled rows before the opening deadline"
	case len(state.WorkerProofHash) != types.Hash32Len:
		return types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT, "worker did not prove its opening before the proof deadline"
	default:
		return types.AnchorVerdictV1_ANCHOR_VERDICT_V1_NO_VERDICT, "validators did not reach two thirds of the voting power within the voting window"
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
	if !state.Sampled || !anchorSelected(state) {
		return nil, nil // no audit, or the coin skipped this task: settle on the round verdict
	}
	deadline := anchorVerdictDeadline(state)
	if deadline == 0 {
		// Audit recorded before validator votes existed (pre-v4): any result on it
		// is a single Verifier's advisory opinion and never binds settlement.
		return nil, nil
	}
	if state.ResultHeight != 0 {
		return &state, nil
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
	if !state.Sampled || !anchorSelected(state) || state.ResultHeight != 0 || anchorVerdictDeadline(state) == 0 {
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
	voter, err := m.k.anchorVoter(ctx, req.ValidatorOperatorAddress, req.SubmitterAddress)
	if err != nil {
		return nil, err
	}
	valAddr, power := voter.valAddr, voter.power // power is recorded on the vote for the record; tallies re-weigh at current power
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
	if !anchorSelected(state) {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "task was not selected for anchor audit")
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
		Verdict: req.Verdict, ProofHash: append([]byte(nil), req.ProofHash...), Height: height, Power: power,
		Verdicts: make([]types.AnchorPositionVerdictV1, len(req.Verdicts)),
	}
	for i, v := range req.Verdicts {
		vote.Verdicts[i] = types.AnchorPositionVerdictV1{Position: v.Position, Verdict: v.Verdict, Reason: v.Reason}
	}
	if err := m.k.AnchorValidatorVote.Set(ctx, voteKey, vote); err != nil {
		return nil, err
	}
	// The tally weighs every stored vote at its validator's CURRENT power against
	// the current total (anchorRecountComputeTally), never the power at voting
	// time: accumulating vote-time power against a total overwritten per vote mixed
	// snapshots, and one stake re-delegated to a new validator between two votes
	// was counted twice (a 1/3 stake reached the 2/3 quorum; audit C-1, 2026-11-09).
	tally, err := m.k.anchorRecountComputeTally(ctx, taskKey)
	if err != nil {
		return nil, err
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

// anchorVoterFacts is a verified anchor voter: the validator's operator bytes,
// its last voting power and the last total power.
type anchorVoterFacts struct {
	valAddr []byte
	power   uint64
	total   uint64
}

// anchorVoter checks that submitter may vote for the validator (its operator
// account or its registered vote address, ADR-0040 v6) and that the validator is
// bonded with voting power. Shared by the compute and content tracks.
func (k Keeper) anchorVoter(ctx context.Context, validatorOperatorAddress, submitterAddress string) (anchorVoterFacts, error) {
	if k.stakingKeeper == nil {
		return anchorVoterFacts{}, status.Error(codes.FailedPrecondition, "anchor validator votes require the staking keeper")
	}
	signer, _, err := k.canonicalAddress("submitter_address", submitterAddress)
	if err != nil {
		return anchorVoterFacts{}, status.Error(codes.InvalidArgument, err.Error())
	}
	valAddr, err := k.stakingKeeper.ValidatorAddressCodec().StringToBytes(validatorOperatorAddress)
	if err != nil || len(valAddr) == 0 {
		return anchorVoterFacts{}, status.Error(codes.InvalidArgument, "validator_operator_address is invalid")
	}
	if !bytes.Equal(signer, valAddr) {
		// ADR-0040 v6: a registered vote address may sign for the validator, so
		// the operator key can stay offline.
		delegation, err := k.AnchorVoteKey.Get(ctx, valAddr)
		if err != nil || delegation.VoteAddress != submitterAddress {
			return anchorVoterFacts{}, errorsmod.Wrap(types.ErrInvalidSignature, "submitter is neither the validator operator account nor its registered vote address")
		}
	}
	validator, err := k.stakingKeeper.GetValidator(ctx, sdk.ValAddress(valAddr))
	if err != nil || !validator.IsBonded() {
		return anchorVoterFacts{}, errorsmod.Wrap(types.ErrInvalidAssignment, "validator is not bonded")
	}
	power, err := k.stakingKeeper.GetLastValidatorPower(ctx, sdk.ValAddress(valAddr))
	if err != nil || power <= 0 {
		return anchorVoterFacts{}, errorsmod.Wrap(types.ErrInvalidAssignment, "validator has no voting power")
	}
	total, err := k.stakingKeeper.GetLastTotalPower(ctx)
	if err != nil || !total.IsPositive() || !total.IsUint64() {
		return anchorVoterFacts{}, errorsmod.Wrap(types.ErrInvariantBroken, "last total voting power is unavailable")
	}
	return anchorVoterFacts{valAddr: valAddr, power: uint64(power), total: total.Uint64()}, nil
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

// RegisterAnchorVoteKey records (or, with an empty vote_address, clears) the hot
// vote address of a validator. Signed by the operator account (ADR-0040 v6).
func (m msgServer) RegisterAnchorVoteKey(ctx context.Context, req *types.MsgRegisterAnchorVoteKey) (*types.MsgRegisterAnchorVoteKeyResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "anchor vote key request is required")
	}
	if m.k.stakingKeeper == nil {
		return nil, status.Error(codes.FailedPrecondition, "anchor vote keys require the staking keeper")
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
		return nil, errorsmod.Wrap(types.ErrInvalidSignature, "only the validator operator account may register its vote key")
	}
	if _, err := m.k.stakingKeeper.GetValidator(ctx, sdk.ValAddress(valAddr)); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "validator does not exist")
	}
	if req.VoteAddress == "" {
		if err := m.k.AnchorVoteKey.Remove(ctx, valAddr); err != nil && !errors.Is(err, collections.ErrNotFound) {
			return nil, err
		}
		return &types.MsgRegisterAnchorVoteKeyResponse{Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED}, nil
	}
	if _, canonical, err := m.k.canonicalAddress("vote_address", req.VoteAddress); err != nil || canonical != req.VoteAddress {
		return nil, status.Error(codes.InvalidArgument, "vote_address must be a canonical account address")
	}
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return nil, err
	}
	if existing, err := m.k.AnchorVoteKey.Get(ctx, valAddr); err == nil && existing.VoteAddress == req.VoteAddress {
		return &types.MsgRegisterAnchorVoteKeyResponse{Status: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP}, nil
	}
	if err := m.k.AnchorVoteKey.Set(ctx, valAddr, types.AnchorVoteKeyState{
		ValidatorOperatorAddress: req.ValidatorOperatorAddress, VoteAddress: req.VoteAddress, Height: height,
	}); err != nil {
		return nil, err
	}
	return &types.MsgRegisterAnchorVoteKeyResponse{Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED}, nil
}

const (
	anchorPendingDefaultLimit = 100
	anchorPendingMaxLimit     = 500
	// anchorPendingScanCap bounds the rows one query may visit.
	anchorPendingScanCap = 20_000
)

// AnchorAuditsPending pages over sampled, selected audits without a final verdict.
func (q queryServer) AnchorAuditsPending(ctx context.Context, req *types.QueryAnchorAuditsPendingRequest) (*types.QueryAnchorAuditsPendingResponse, error) {
	if req == nil {
		req = &types.QueryAnchorAuditsPendingRequest{}
	}
	if len(req.StartAfter) != 0 && len(req.StartAfter) != types.Hash32Len {
		return nil, status.Error(codes.InvalidArgument, "start_after must be 32 bytes")
	}
	limit := int(req.Limit)
	if limit <= 0 {
		limit = anchorPendingDefaultLimit
	}
	if limit > anchorPendingMaxLimit {
		limit = anchorPendingMaxLimit
	}
	var rng collections.Ranger[types.TaskKey] // a typed nil *Range would not read as "no range"
	if len(req.StartAfter) == types.Hash32Len {
		rng = new(collections.Range[types.TaskKey]).StartExclusive(types.NewTaskKey(req.StartAfter))
	}
	iter, err := q.k.AnchorAudit.Iterate(ctx, rng)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	out := &types.QueryAnchorAuditsPendingResponse{}
	visited := 0
	var last []byte
	for ; iter.Valid(); iter.Next() {
		visited++
		kv, err := iter.KeyValue()
		if err != nil {
			return nil, err
		}
		last = kv.Key
		state := kv.Value
		match := state.Sampled && anchorSelected(state) && state.ResultHeight == 0 && anchorVerdictDeadline(state) != 0
		if req.Content {
			// ADR-0040 v7: settled audits whose content verdict is still open.
			match = anchorContentOpen(state) && len(state.WorkerProofHash) == types.Hash32Len && !anchorComputeRejected(state)
			if match {
				settled, err := q.k.taskAuthorityIsFinal(ctx, types.NewTaskKey(kv.Key))
				if err != nil {
					return nil, err
				}
				match = settled
			}
		}
		if match {
			out.Audits = append(out.Audits, state)
			if len(out.Audits) >= limit {
				break
			}
		}
		if visited >= anchorPendingScanCap {
			break
		}
	}
	if iter.Valid() && last != nil {
		out.Next = append([]byte(nil), last...)
	}
	return out, nil
}

// AnchorVoteKey returns a validator's registered vote address.
func (q queryServer) AnchorVoteKey(ctx context.Context, req *types.QueryAnchorVoteKeyRequest) (*types.QueryAnchorVoteKeyResponse, error) {
	if req == nil || q.k.stakingKeeper == nil {
		return nil, status.Error(codes.InvalidArgument, "validator_operator_address is required")
	}
	valAddr, err := q.k.stakingKeeper.ValidatorAddressCodec().StringToBytes(req.ValidatorOperatorAddress)
	if err != nil || len(valAddr) == 0 {
		return nil, status.Error(codes.InvalidArgument, "validator_operator_address is invalid")
	}
	state, err := q.k.AnchorVoteKey.Get(ctx, valAddr)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "no anchor vote key for validator")
		}
		return nil, err
	}
	return &types.QueryAnchorVoteKeyResponse{VoteKey: state}, nil
}

// effectiveSettlementDeadline is the one settlement deadline the index holds
// for a task (ADR-0040 v6): the round deadline, moved past the anchor verdict
// deadline of a selected audit on a PASS round. It deliberately ignores whether
// the verdict is already final, so every recomputation (finalize, manual sweep,
// cleanup, genesis rebuild) names the same index row.
func (k Keeper) effectiveSettlementDeadline(ctx context.Context, taskKey types.TaskKey, summary types.TaskRoundSummaryState, params types.TaskParamsV1) (uint64, error) {
	deadline, err := settlementDeadlineHeight(summary.GetRoundsClosedHeight(), params)
	if err != nil {
		return 0, err
	}
	state, err := k.AnchorAudit.Get(ctx, taskKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return deadline, nil
		}
		return 0, err
	}
	verdictDeadline := anchorVerdictDeadline(state)
	if !state.Sampled || !anchorSelected(state) || verdictDeadline == 0 || verdictDeadline+1 <= deadline {
		return deadline, nil
	}
	round, err := k.ReadVerificationRound(ctx, types.NewVerifyRoundKey(taskKey, summary.EffectiveVerifyRound))
	if err != nil {
		return 0, err
	}
	if round.GetVerdict() != types.TaskVerdict_TASK_VERDICT_PASS {
		return deadline, nil
	}
	return verdictDeadline + 1, nil
}

// cleanupAnchorAudit retires a task's anchor rows at evidence cleanup
// (ADR-0040 v6), one row per call like every other cleanup step (the EndBlock
// fair-share budget allows a single visited row): first each vote, then the
// audit itself - removed, or, when it names the Worker role fault it produced,
// slimmed to the facts the cross-module fault check reads. It reports 1 while
// it changed a row and 0 once nothing is left to do.
func (k Keeper) cleanupAnchorAudit(ctx context.Context, taskKey types.TaskKey) (uint64, error) {
	state, err := k.AnchorAudit.Get(ctx, taskKey)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return 0, nil
		}
		return 0, err
	}
	// ADR-0040 v7: an open content track is closed first (cleanup is held until its deadline).
	if changed, err := k.closeAnchorContentAtCleanup(ctx, taskKey, &state); err != nil || changed {
		return boolCount(changed), err
	}
	if deleted, err := deleteFirstMapRow(ctx, k.AnchorValidatorVote,
		collections.NewPrefixedPairRange[types.TaskKey, []byte](taskKey)); err != nil || deleted {
		return boolCount(deleted), err
	}
	if deleted, err := deleteFirstMapRow(ctx, k.AnchorContentVote,
		collections.NewPrefixedPairRange[types.TaskKey, []byte](taskKey)); err != nil || deleted {
		return boolCount(deleted), err
	}
	if len(state.WorkerFaultId) != types.Hash32Len && len(state.ContentWorkerFaultId) != types.Hash32Len &&
		state.ContentVerdict != types.AnchorContentVerdictV1_ANCHOR_CONTENT_VERDICT_V1_REJECT_CONTENT {
		// A REJECT_CONTENT whose slash the Hub refused keeps its slim row (audit C-2):
		// the verdict must not vanish just because no fault id names it.
		if err := k.releaseAnchorEpochMember(ctx, state); err != nil {
			return 0, err
		}
		if err := k.AnchorAudit.Remove(ctx, taskKey); err != nil {
			return 0, err
		}
		return 1, nil
	}
	if state.AnchorCommitUri == "" && state.OpeningUri == "" && len(state.Positions) == 0 && state.AuditBasis == "" && len(state.Verdicts) == 0 &&
		len(state.ContentVerdicts) == 0 && len(state.OpeningHash) == 0 {
		return 0, nil // already slim
	}
	if err := k.releaseAnchorEpochMember(ctx, state); err != nil {
		return 0, err
	}
	slim := types.AnchorAuditState{
		TaskId: state.TaskId, Sampled: state.Sampled, Selected: state.Selected, AuditRateBps: state.AuditRateBps,
		WinnerWorker: state.WinnerWorker, VerdictDeadlineHeight: state.VerdictDeadlineHeight,
		ResultHeight: state.ResultHeight, TaskVerdict: state.TaskVerdict, ResultSource: state.ResultSource,
		WorkerFaultId: state.WorkerFaultId, SlashableBond: state.SlashableBond,
		// ADR-0040 v7: the content verdict and its fault reference survive like the compute ones.
		ContentVerdictDeadlineHeight: state.ContentVerdictDeadlineHeight, ContentVerdict: state.ContentVerdict,
		ContentResultSource: state.ContentResultSource, ContentResultHeight: state.ContentResultHeight,
		ContentWorkerFaultId: state.ContentWorkerFaultId,
	}
	if err := k.AnchorAudit.Set(ctx, taskKey, slim); err != nil {
		return 0, err
	}
	return 1, nil
}

// releaseAnchorEpochMember drops the audit's task from its sealed B1 epoch and
// removes the epoch once no member remains, so the epoch table shrinks with the
// audits it indexes (audit C-3, 2026-11-09). Called once, when the audit is
// removed or slimmed (the slim row no longer names the epoch).
func (k Keeper) releaseAnchorEpochMember(ctx context.Context, state types.AnchorAuditState) error {
	if len(state.EpochManifestHash) != types.Hash32Len {
		return nil
	}
	key := types.Hash32Key(state.EpochManifestHash)
	epoch, err := k.AnchorEpoch.Get(ctx, key)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return nil
		}
		return err
	}
	kept := make([][]byte, 0, len(epoch.TaskIds))
	for _, id := range epoch.TaskIds {
		if !bytes.Equal(id, state.TaskId) {
			kept = append(kept, id)
		}
	}
	if len(kept) == 0 {
		return k.AnchorEpoch.Remove(ctx, key)
	}
	epoch.TaskIds = kept
	return k.AnchorEpoch.Set(ctx, key, epoch)
}
