package keeper

import (
	"bytes"
	"context"
	"strconv"

	"cosmossdk.io/collections"
	errorsmod "cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// SubmitAnchorPanelVote records one panel member's vote on the round it was
// drawn for. The member's current service signs the transaction. A vote is
// PASS or REJECT over exactly the sampled positions, or UNAVAILABLE when the
// member could not obtain the recorded material; it cannot be changed. The
// round closes as soon as the threshold of equal votes is reached or every
// member has voted.
func (m msgServer) SubmitAnchorPanelVote(ctx context.Context, req *types.MsgSubmitAnchorPanelVote) (*types.MsgSubmitAnchorPanelVoteResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "anchor panel vote request is required")
	}
	if len(req.TaskId) != types.Hash32Len || len(req.OpeningHash) != types.Hash32Len || len(req.ProofHash) != types.Hash32Len {
		return nil, status.Error(codes.InvalidArgument, "anchor panel vote envelope is invalid")
	}
	_, operator, err := m.k.canonicalAddress("verifier_operator_address", req.VerifierOperatorAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if _, _, err := m.k.canonicalAddress("submitter_address", req.SubmitterAddress); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return nil, err
	}
	taskKey := types.NewTaskKey(req.TaskId)
	audit, err := m.k.AnchorAudit.Get(ctx, taskKey)
	if err != nil {
		if errIsNotFound(err) {
			return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "task has no anchor audit sample")
		}
		return nil, err
	}
	if !isAnchorPanelAudit(audit) {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "this audit is not decided by a Verifier panel")
	}
	// A vote names its round, so a replay is recognised on a closed round too.
	var round *types.AnchorPanelRoundState
	for index := range audit.PanelRounds {
		if audit.PanelRounds[index].PanelRound == req.PanelRound {
			round = &audit.PanelRounds[index]
		}
	}
	if round == nil {
		return nil, errorsmod.Wrapf(types.ErrInvalidAssignment, "audit has no panel round %d", req.PanelRound)
	}
	vote := *req
	vote.VerifierOperatorAddress = operator
	if round.ClosedHeight != 0 {
		if existing, voted := anchorPanelVoteOf(*round, operator); voted && existing.Verdict == req.Verdict &&
			anchorPositionVerdictsEqual(existing.Verdicts, req.Verdicts) {
			return &types.MsgSubmitAnchorPanelVoteResponse{
				RoundResult: round.Result, RoundClosed: true, Status: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP,
			}, nil
		}
		return nil, errorsmod.Wrapf(types.ErrInvalidAssignment, "panel round %d is closed", req.PanelRound)
	}
	if audit.ResultHeight != 0 {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "anchor audit verdict is already final")
	}
	replay, err := validateAnchorPanelVote(audit, *round, &vote, height)
	if err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, err.Error())
	}
	if err := m.k.RequireCurrentServiceSubmitter(ctx, shared.ParticipantTypeCortexNode, operator, req.SubmitterAddress); err != nil {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "submitter is not the panel member's current service")
	}
	if replay {
		return &types.MsgSubmitAnchorPanelVoteResponse{Status: shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP}, nil
	}
	params, err := m.k.ParamsForTask(ctx, taskKey)
	if err != nil {
		return nil, err
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	round.Votes = append(round.Votes, types.AnchorPanelVoteState{
		VerifierOperatorAddress: operator, Verdict: req.Verdict, Height: height,
		Verdicts: append([]types.AnchorPositionVerdictV1(nil), req.Verdicts...),
	})
	if err := emitTypedEvent(cache, &types.EventAnchorPanelVote{
		TaskId: append([]byte(nil), req.TaskId...), PanelRound: round.PanelRound,
		VerifierOperatorAddress: operator, Verdict: req.Verdict,
	}); err != nil {
		return nil, err
	}
	response := &types.MsgSubmitAnchorPanelVoteResponse{Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED}
	if result := anchorPanelRoundResult(*round, params.Verification.AnchorPanelThreshold, height); result != types.AnchorVerdictV1_ANCHOR_VERDICT_V1_UNSPECIFIED {
		if err := m.k.closeAnchorPanelRound(cache, taskKey, &audit, result, height); err != nil {
			return nil, err
		}
		response.RoundResult, response.RoundClosed = result, true
	}
	if err := m.k.AnchorAudit.Set(cache, taskKey, audit); err != nil {
		return nil, err
	}
	write()
	return response, nil
}

// RequestAnchorReaudit opens the one re-audit an audit may have. Any account
// may ask for it while the provisional verdict is published; it pays the fee
// of one panel round. A new panel of other members decides: an opposite
// decision replaces the published verdict and returns the fee, anything else
// leaves the published verdict standing.
func (m msgServer) RequestAnchorReaudit(ctx context.Context, req *types.MsgRequestAnchorReaudit) (*types.MsgRequestAnchorReauditResponse, error) {
	if req == nil || len(req.TaskId) != types.Hash32Len {
		return nil, status.Error(codes.InvalidArgument, "anchor re-audit request needs a 32-byte task_id")
	}
	openerBytes, opener, err := m.k.canonicalAddress("opener_address", req.OpenerAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	maxFee, err := shared.ParseAmount(req.MaxFee)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "max_fee is not an amount")
	}
	height, err := currentBlockHeight(ctx)
	if err != nil {
		return nil, err
	}
	taskKey := types.NewTaskKey(req.TaskId)
	audit, err := m.k.AnchorAudit.Get(ctx, taskKey)
	if err != nil {
		if errIsNotFound(err) {
			return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "task has no anchor audit sample")
		}
		return nil, err
	}
	if !isAnchorPanelAudit(audit) {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "this audit is not decided by a Verifier panel")
	}
	if audit.ReauditRequested {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "a re-audit was already requested for this audit")
	}
	if _, open := anchorPanelOpenRound(audit); open || audit.ResultHeight != 0 || !isAnchorDecision(audit.ProvisionalVerdict) ||
		height < audit.ProvisionalHeight || height >= audit.PublicationEndHeight {
		return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "audit has no published provisional verdict open to re-audit")
	}
	fee := m.k.anchorPanelFee(ctx, taskKey)
	if maxFee < fee {
		return nil, errorsmod.Wrapf(types.ErrInvalidAssignment, "max_fee is below the re-audit fee %d", fee)
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	cache := sdk.WrapSDKContext(cacheCtx)
	if fee > 0 {
		denom := m.k.hubKeeper.GetHubParams(sdkCtx).BusinessDenom
		if denom == "" {
			return nil, errorsmod.Wrap(types.ErrInvariantBroken, "business denomination is unavailable")
		}
		coins := sdk.NewCoins(sdk.NewCoin(denom, sdkmath.NewIntFromUint64(fee)))
		if err := m.k.bankKeeper.SendCoinsFromAccountToModule(cache, sdk.AccAddress(openerBytes), shared.TaskChallengeEffectModuleName, coins); err != nil {
			return nil, errorsmod.Wrap(types.ErrInvalidAssignment, "opener cannot pay the re-audit fee")
		}
	}
	audit.ReauditRequested = true
	audit.ReauditOpener = opener
	audit.ReauditFee = strconv.FormatUint(fee, 10)
	panelRound := anchorPanelFirstRound + uint32(len(audit.PanelRounds))
	if err := emitTypedEvent(cache, &types.EventAnchorReauditRequested{
		TaskId: append([]byte(nil), req.TaskId...), OpenerAddress: opener, PanelRound: panelRound, Fee: shared.NewAmount(fee),
	}); err != nil {
		return nil, err
	}
	if err := m.k.openAnchorPanelRound(cache, taskKey, &audit, types.AnchorPanelRoundKindV1_ANCHOR_PANEL_ROUND_KIND_V1_REAUDIT, height); err != nil {
		return nil, err
	}
	if err := m.k.AnchorAudit.Set(cache, taskKey, audit); err != nil {
		return nil, err
	}
	write()
	return &types.MsgRequestAnchorReauditResponse{
		PanelRound: panelRound, Fee: shared.NewAmount(fee), Status: shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED,
	}, nil
}

// AnchorPanelAssignments pages over the open panel rounds an operator is a
// member of, with everything it needs to vote.
func (q queryServer) AnchorPanelAssignments(ctx context.Context, req *types.QueryAnchorPanelAssignmentsRequest) (*types.QueryAnchorPanelAssignmentsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "anchor panel assignments request is required")
	}
	if len(req.StartAfter) != 0 && len(req.StartAfter) != types.Hash32Len {
		return nil, status.Error(codes.InvalidArgument, "start_after must be 32 bytes")
	}
	raw, err := q.k.sessionAddressToStore("verifier_operator_address", req.VerifierOperatorAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	_, operator, err := q.k.canonicalAddress("verifier_operator_address", req.VerifierOperatorAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	limit := anchorPageLimit(req.Limit)
	rng := collections.NewPrefixedPairRange[types.AddrKey, types.Hash32Key](raw)
	if len(req.StartAfter) == types.Hash32Len {
		rng = rng.StartExclusive(types.NewTaskKey(req.StartAfter))
	}
	iter, err := q.k.AnchorPanelMemberIndex.Iterate(ctx, rng)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	out := &types.QueryAnchorPanelAssignmentsResponse{}
	var last []byte
	for ; iter.Valid(); iter.Next() {
		if len(out.Assignments) >= limit {
			out.Next = append([]byte(nil), last...)
			break
		}
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		taskKey := key.K2()
		last = taskKey
		audit, err := q.k.AnchorAudit.Get(ctx, taskKey)
		if err != nil {
			continue
		}
		index, open := anchorPanelOpenRound(audit)
		if !open || !anchorPanelMember(audit.PanelRounds[index], operator) {
			continue
		}
		round := audit.PanelRounds[index]
		assignment := types.AnchorPanelAssignmentV1{
			TaskId: append([]byte(nil), audit.TaskId...), PanelRound: round.PanelRound, Kind: round.Kind,
			AssignmentHeight: round.AssignmentHeight, VoteDeadlineHeight: round.VoteDeadlineHeight, Audit: audit,
		}
		if core, err := q.k.TaskCore.Get(ctx, taskKey); err == nil && bytes.Equal(core.TaskId, audit.TaskId) {
			assignment.ModelId = append([]byte(nil), core.ModelId...)
			assignment.ProfileVersion = core.ProfileVersion
		}
		out.Assignments = append(out.Assignments, assignment)
	}
	return out, nil
}

// AnchorAuditsUnderReview pages over the audits whose provisional verdict is
// published and not final yet.
func (q queryServer) AnchorAuditsUnderReview(ctx context.Context, req *types.QueryAnchorAuditsUnderReviewRequest) (*types.QueryAnchorAuditsUnderReviewResponse, error) {
	if req == nil {
		req = &types.QueryAnchorAuditsUnderReviewRequest{}
	}
	if len(req.StartAfter) != 0 && len(req.StartAfter) != types.Hash32Len {
		return nil, status.Error(codes.InvalidArgument, "start_after must be 32 bytes")
	}
	limit := anchorPageLimit(req.Limit)
	var rng collections.Ranger[types.TaskKey]
	if len(req.StartAfter) == types.Hash32Len {
		rng = new(collections.Range[types.TaskKey]).StartExclusive(types.NewTaskKey(req.StartAfter))
	}
	iter, err := q.k.AnchorUnderReviewIndex.Iterate(ctx, rng)
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	out := &types.QueryAnchorAuditsUnderReviewResponse{}
	var last []byte
	for ; iter.Valid(); iter.Next() {
		if len(out.Audits) >= limit {
			out.Next = append([]byte(nil), last...)
			break
		}
		taskKey, err := iter.Key()
		if err != nil {
			return nil, err
		}
		last = taskKey
		audit, err := q.k.AnchorAudit.Get(ctx, taskKey)
		if err != nil || audit.ResultHeight != 0 {
			continue
		}
		out.Audits = append(out.Audits, audit)
	}
	return out, nil
}

// anchorPanelCandidateWindow answers the candidate-window query for a panel
// round that is choosing its members: the frozen window header and, once the
// window beacon has ranked it, its members in rank order. It reports
// found=false when the task has no working row for that round (the round was
// drawn or closed, or the task has no panel audit), so the caller falls back to
// the verification rounds.
func (q *queryServer) anchorPanelCandidateWindow(ctx context.Context, taskKey types.TaskKey, panelRound uint32) (*types.QueryVerifierCandidateWindowResponse, bool, error) {
	selection, found, err := q.k.loadAnchorPanelSelection(ctx, taskKey)
	if err != nil {
		return nil, true, status.Error(codes.Internal, "panel selection row is unreadable")
	}
	if !found || selection.PanelRound != panelRound {
		return nil, false, nil
	}
	if selection.Window.Status != types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY {
		return nil, true, status.Error(codes.FailedPrecondition, "verifier candidate window is not materialized")
	}
	caps, err := q.queryCaps(ctx)
	if err != nil {
		return nil, true, err
	}
	response := &types.QueryVerifierCandidateWindowResponse{Window: selection.Window, Members: selection.Members}
	if response.Members == nil {
		response.Members = []types.VerifierCandidateWindowMemberState{}
	}
	if uint64(response.Size()) > caps.responseSize {
		return nil, true, status.Error(codes.ResourceExhausted, "verifier candidate window exceeds the response byte cap")
	}
	return response, true, nil
}

func anchorPageLimit(requested uint32) int {
	limit := int(requested)
	if limit <= 0 {
		limit = anchorPendingDefaultLimit
	}
	if limit > anchorPendingMaxLimit {
		limit = anchorPendingMaxLimit
	}
	return limit
}
