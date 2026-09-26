package keeper

import (
	"bytes"
	"context"
	"errors"

	"cosmossdk.io/collections"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	shared "github.com/TrueOpen/node/x/shared/types"
	"github.com/TrueOpen/node/x/task/types"
)

// Verification read-side query surface. Missing rows return NotFound, while malformed
// authoritative rows return Internal instead of a plausible empty projection.

// InferReceipt returns the authoritative
// InferReceiptState; after cleanup a short commitment list means the body was
// pruned while hash/count stay auditable.
func (q *queryServer) InferReceipt(ctx context.Context, req *types.QueryInferReceiptRequest) (*types.QueryInferReceiptResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	taskKey, err := requireQueryHash32("task_id", req.TaskId)
	if err != nil {
		return nil, err
	}
	receipt, err := q.k.ReadInferReceipt(ctx, taskKey)
	if err != nil {
		return nil, queryVerificationStoreError(err, "infer receipt")
	}
	if !bytes.Equal(receipt.TaskId, req.TaskId) {
		return nil, status.Error(codes.Internal, "infer receipt primary key does not match task_id")
	}
	return &types.QueryInferReceiptResponse{Receipt: receipt}, nil
}

// VerifierCandidateWindow: SOURCE_FROZEN must be
// FailedPrecondition, READY must return exactly window_size members in rank
// order, PRUNED must return the header with empty members.
func (q *queryServer) VerifierCandidateWindow(ctx context.Context, req *types.QueryVerifierCandidateWindowRequest) (*types.QueryVerifierCandidateWindowResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	taskKey, err := requireQueryHash32("task_id", req.TaskId)
	if err != nil {
		return nil, err
	}
	if !isPhase0VerifyRound(req.VerifyRound) {
		return nil, status.Error(codes.InvalidArgument, "verify_round must be 1 or 2")
	}
	window, err := q.k.VerifierCandidateWindow.Get(ctx, types.NewVerifyRoundKey(taskKey, req.VerifyRound))
	if err != nil {
		return nil, queryVerificationStoreError(err, "verifier candidate window")
	}
	if !bytes.Equal(window.TaskId, req.TaskId) || window.VerifyRound != req.VerifyRound {
		return nil, status.Error(codes.Internal, "verifier candidate window primary key does not match its scope")
	}
	response := &types.QueryVerifierCandidateWindowResponse{Window: window, Members: []types.VerifierCandidateWindowMemberState{}}
	caps, err := q.queryCaps(ctx)
	if err != nil {
		return nil, err
	}
	responseByteCap := caps.responseSize
	switch window.Status {
	case types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_SOURCE_FROZEN:
		return nil, status.Error(codes.FailedPrecondition, "verifier candidate window is not materialized")
	case types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY:
		if window.WindowSize == 0 || len(window.GetVerifierWindowHash()) != types.Hash32Len {
			return nil, status.Error(codes.Internal, "READY verifier candidate window has an incomplete header")
		}
		responseBytes := window.Size()
		for rank := uint32(0); rank < window.WindowSize; rank++ {
			member, err := q.k.ReadVerifierWindowMember(ctx, types.NewVerifierWindowMemberKey(taskKey, req.VerifyRound, rank))
			if err != nil {
				return nil, status.Errorf(codes.Internal, "READY verifier candidate window has no dense member at rank %d", rank)
			}
			if !bytes.Equal(member.TaskId, req.TaskId) || member.VerifyRound != req.VerifyRound || member.RankIndex != rank {
				return nil, status.Errorf(codes.Internal, "verifier window member at rank %d has mismatched scope", rank)
			}
			responseBytes += member.Size()
			if uint64(responseBytes) > responseByteCap {
				return nil, status.Error(codes.ResourceExhausted, "verifier candidate window exceeds the response byte cap")
			}
			response.Members = append(response.Members, member)
		}
	case types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_PRUNED:
		if window.SchemaVersion != 1 ||
			len(window.InferReceiptHash) != types.Hash32Len ||
			len(window.CandidatePoolSnapshotId) != types.Hash32Len ||
			len(window.CandidatePoolHash) != types.Hash32Len ||
			len(window.VerifierWindowSourceHash) != types.Hash32Len ||
			window.EligibilitySegmentCount == 0 || window.WindowSize > window.EligibleCount {
			return nil, status.Error(codes.Internal, "PRUNED verifier candidate window has an invalid retained source header")
		}
		if !(window.EligibilityFrozenHeight < window.WindowRandomnessHeight &&
			window.WindowRandomnessHeight < window.BuilderProposalCloseHeight &&
			window.BuilderProposalCloseHeight < window.HandraiseCloseHeight &&
			window.HandraiseCloseHeight < window.SelectionRandomnessHeight &&
			window.SelectionRandomnessHeight < window.AssignmentDeadlineHeight) {
			return nil, status.Error(codes.Internal, "PRUNED verifier candidate window has a non-canonical retained clock")
		}
		retainedReady := len(window.GetVerifierWindowHash()) == types.Hash32Len && len(window.GetWindowRandomnessBeacon()) == types.Hash32Len && window.GeneratedHeight >= window.WindowRandomnessHeight
		neverGenerated := window.XVerifierWindowHash == nil && window.XWindowRandomnessBeacon == nil && window.GeneratedHeight == 0
		if !retainedReady && !neverGenerated {
			return nil, status.Error(codes.Internal, "PRUNED verifier candidate window has a partial materialization header")
		}
	default:
		return nil, status.Error(codes.Internal, "verifier candidate window has no valid status")
	}
	return response, nil
}

// VerifierAssignment returns the single V1 verification round. The round is a
// protocol constant rather than a caller selector or a scanned current-round
// guess.
func (q *queryServer) VerifierAssignment(ctx context.Context, req *types.QueryVerifierAssignmentRequest) (*types.QueryVerifierAssignmentResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	taskKey, err := requireQueryHash32("task_id", req.TaskId)
	if err != nil {
		return nil, err
	}
	if !isPhase0VerifyRound(req.VerifyRound) {
		return nil, status.Error(codes.InvalidArgument, "verify_round must be 1 or 2")
	}
	assignment, err := q.loadVerifierAssignment(ctx, taskKey, req.VerifyRound)
	if err != nil {
		return nil, err
	}
	return &types.QueryVerifierAssignmentResponse{Assignment: assignment}, nil
}

// VerifyCommit is keyed by the canonical commit_key.
func (q *queryServer) VerifyCommit(ctx context.Context, req *types.QueryVerifyCommitRequest) (*types.QueryVerifyCommitResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if _, err := requireQueryHash32("task_id", req.TaskId); err != nil {
		return nil, err
	}
	if !isPhase0VerifyRound(req.VerifyRound) {
		return nil, status.Error(codes.InvalidArgument, "verify_round must be 1 or 2")
	}
	verifier, err := q.requireCanonicalAddress("verifier_operator_address", req.VerifierOperatorAddress)
	if err != nil {
		return nil, err
	}
	commitKey, rawCommitKey, err := queryCommitKey(ctx, req.TaskId, req.VerifyRound, verifier)
	if err != nil {
		return nil, err
	}
	commit, err := q.k.ReadCommit(ctx, commitKey)
	if err != nil {
		return nil, queryVerificationStoreError(err, "verify commit")
	}
	if !bytes.Equal(commit.TaskId, req.TaskId) || commit.VerifyRound != req.VerifyRound || commit.VerifierOperatorAddress != verifier || !bytes.Equal(commit.CommitKey, rawCommitKey[:]) {
		return nil, status.Error(codes.Internal, "verify commit primary key does not match its scope")
	}
	return &types.QueryVerifyCommitResponse{Commit: commit}, nil
}

// ResultReceipt performs the authoritative (task, round, verifier)
// lookup; never substituted by a settlement aggregate.
func (q *queryServer) ResultReceipt(ctx context.Context, req *types.QueryResultReceiptRequest) (*types.QueryResultReceiptResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if _, err := requireQueryHash32("task_id", req.TaskId); err != nil {
		return nil, err
	}
	if !isPhase0VerifyRound(req.VerifyRound) {
		return nil, status.Error(codes.InvalidArgument, "verify_round must be 1 or 2")
	}
	verifier, err := q.requireCanonicalAddress("verifier_operator_address", req.VerifierOperatorAddress)
	if err != nil {
		return nil, err
	}
	commitKey, rawCommitKey, err := queryCommitKey(ctx, req.TaskId, req.VerifyRound, verifier)
	if err != nil {
		return nil, err
	}
	receipt, err := q.k.ReadResultReceipt(ctx, commitKey)
	if err != nil {
		return nil, queryVerificationStoreError(err, "result receipt")
	}
	if !bytes.Equal(receipt.CommitKey, rawCommitKey[:]) || !bytes.Equal(receipt.TaskId, req.TaskId) ||
		receipt.VerifyRound != req.VerifyRound || receipt.VerifierOperatorAddress != verifier {
		return nil, status.Error(codes.Internal, "result receipt primary key does not match commit_key")
	}
	return &types.QueryResultReceiptResponse{Receipt: receipt}, nil
}

// DataUnavailableReports returns rows in fixed selected-verifier order.
func (q *queryServer) DataUnavailableReports(ctx context.Context, req *types.QueryDataUnavailableReportsRequest) (*types.QueryDataUnavailableReportsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if _, err := requireQueryHash32("task_id", req.TaskId); err != nil {
		return nil, err
	}
	if !isPhase0VerifyRound(req.VerifyRound) {
		return nil, status.Error(codes.InvalidArgument, "verify_round must be 1 or 2")
	}
	caps, err := q.queryCaps(ctx)
	if err != nil {
		return nil, err
	}
	limit, encodedToken, err := resolveQueryPage(req.Page, caps)
	if err != nil {
		return nil, err
	}
	taskKey := types.NewTaskKey(req.TaskId)
	assignment, err := q.loadVerifierAssignment(ctx, taskKey, req.VerifyRound)
	if err != nil {
		return nil, err
	}
	chainID := sdkContextFrom(ctx).ChainID()
	queryHeight, lastOperator, rpcDigest, selectorDigest, err := q.decodeDataUnavailableReportsPageToken(
		ctx, encodedToken, taskKey, req.VerifyRound,
	)
	if err != nil {
		return nil, err
	}
	start := 0
	seenOperators := make(map[string]struct{}, len(assignment.SelectedVerifiers))
	for i, selected := range assignment.SelectedVerifiers {
		canonical, err := q.requireCanonicalAddress("selected verifier operator_address", selected.OperatorAddress)
		if err != nil || canonical != selected.OperatorAddress || selected.SlotVersion == 0 {
			return nil, status.Errorf(codes.Internal, "verifier assignment selected slot %d is invalid", i)
		}
		if _, duplicate := seenOperators[canonical]; duplicate {
			return nil, status.Errorf(codes.Internal, "verifier assignment selected slot %d duplicates an operator", i)
		}
		seenOperators[canonical] = struct{}{}
		if lastOperator == canonical {
			start = i + 1
		}
	}
	if lastOperator != "" {
		if _, found := seenOperators[lastOperator]; !found {
			return nil, status.Error(codes.InvalidArgument, "page_token primary key is not in the selected verifier vector")
		}
	}

	reports := make([]types.DataUnavailableReportState, 0, len(assignment.SelectedVerifiers)-start)
	primaryKeys := make([][]byte, 0, len(assignment.SelectedVerifiers)-start)
	for i := start; i < len(assignment.SelectedVerifiers); i++ {
		selected := assignment.SelectedVerifiers[i]
		report, err := q.k.ReadDataUnavailableReport(ctx, types.NewVerifyActorKey(taskKey, req.VerifyRound, selected.OperatorAddress))
		if err != nil {
			if errors.Is(err, collections.ErrNotFound) {
				continue
			}
			return nil, status.Error(codes.Internal, err.Error())
		}
		if !bytes.Equal(report.TaskId, req.TaskId) || report.VerifyRound != req.VerifyRound || report.VerifierOperatorAddress != selected.OperatorAddress {
			return nil, status.Error(codes.Internal, "data unavailable report primary key does not match its scope")
		}
		if report.ReportHeight < assignment.OpenVerifyHeight || report.ReportHeight > assignment.CommitDeadlineHeight {
			return nil, status.Error(codes.Internal, "data unavailable report is outside the frozen commit deadline")
		}
		expectedDigest, err := DataUnavailableReportDigest(chainID, DataUnavailableReportAttempt{
			TaskID: req.TaskId, VerifyRound: req.VerifyRound,
			VerifierOperatorAddress:           report.VerifierOperatorAddress,
			UnavailableTaskBuilderBitmap:      report.UnavailableTaskBuilderBitmap,
			ServiceAuthorizationNonceSnapshot: report.ServiceAuthorizationNonceSnapshot,
		})
		if err != nil || !bytes.Equal(expectedDigest, report.ReportDigest) {
			return nil, status.Error(codes.Internal, "data unavailable report digest does not match its stored facts")
		}
		primaryKey, err := q.encodeDataUnavailableReportPrimaryKey(taskKey, req.VerifyRound, selected.OperatorAddress)
		if err != nil {
			return nil, status.Error(codes.Internal, "could not encode data unavailable report primary key")
		}
		reports = append(reports, report)
		primaryKeys = append(primaryKeys, primaryKey)
	}

	response := &types.QueryDataUnavailableReportsResponse{Reports: []types.DataUnavailableReportState{}}
	consumed := 0
	for consumed < len(reports) && uint32(len(response.Reports)) < limit {
		candidate := append(response.Reports, reports[consumed])
		probe := &types.QueryDataUnavailableReportsResponse{Reports: candidate}
		if uint64(probe.Size()) > caps.responseSize {
			if len(response.Reports) == 0 {
				return nil, status.Error(codes.Internal, "single data unavailable report exceeds the response byte cap")
			}
			break
		}
		response.Reports = candidate
		consumed++
	}
	if consumed < len(reports) {
		if len(response.Reports) == 0 {
			return nil, status.Error(codes.Internal, "pagination made no forward progress")
		}
		next, err := encodeDataUnavailableReportsPageToken(rpcDigest, selectorDigest, primaryKeys[consumed-1], queryHeight)
		if err != nil {
			return nil, status.Error(codes.Internal, "could not encode data unavailable reports page token")
		}
		response.Page = shared.QueryPageResponseV1{NextPageToken: next}
		if uint64(response.Size()) > caps.responseSize {
			return nil, status.Error(codes.Internal, "data unavailable reports page token exceeds the response byte cap")
		}
	}
	return response, nil
}

// BuilderDataUnavailable: PRUNED empties the digest
// vector but keeps count/hash.
func (q *queryServer) BuilderDataUnavailable(ctx context.Context, req *types.QueryBuilderDataUnavailableRequest) (*types.QueryBuilderDataUnavailableResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if _, err := requireQueryHash32("task_id", req.TaskId); err != nil {
		return nil, err
	}
	if !isPhase0VerifyRound(req.VerifyRound) {
		return nil, status.Error(codes.InvalidArgument, "verify_round must be 1 or 2")
	}
	builder, err := q.requireCanonicalAddress("builder_operator_address", req.BuilderOperatorAddress)
	if err != nil {
		return nil, err
	}
	aggregate, err := q.k.ReadDataUnavailableAggregate(ctx, types.NewVerifyActorKey(types.NewTaskKey(req.TaskId), req.VerifyRound, builder))
	if err != nil {
		return nil, queryVerificationStoreError(err, "builder data unavailable aggregate")
	}
	if !bytes.Equal(aggregate.TaskId, req.TaskId) || aggregate.VerifyRound != req.VerifyRound || aggregate.BuilderOperatorAddress != builder {
		return nil, status.Error(codes.Internal, "builder data unavailable aggregate primary key does not match its scope")
	}
	if aggregate.Status == types.BuilderDataUnavailableAggregateStatusV1_BUILDER_DATA_UNAVAILABLE_AGGREGATE_STATUS_V1_PRUNED && len(aggregate.ReportDigestsByVerifierSlot) != 0 {
		return nil, status.Error(codes.Internal, "PRUNED builder data unavailable aggregate still has digest bodies")
	}
	return &types.QueryBuilderDataUnavailableResponse{Aggregate: aggregate}, nil
}

// TaskFailureClass: freeze_signal_eligible must be
// recomputable from the class, and the row survives Task compaction until
// TaskFailureClassWindowPruneIndex expires.
func (q *queryServer) TaskFailureClass(ctx context.Context, req *types.QueryTaskFailureClassRequest) (*types.QueryTaskFailureClassResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	taskKey, err := requireQueryHash32("task_id", req.TaskId)
	if err != nil {
		return nil, err
	}
	if !isPhase0VerifyRound(req.VerifyRound) {
		return nil, status.Error(codes.InvalidArgument, "verify_round must be 1 or 2")
	}
	failure, err := q.k.TaskFailureClass.Get(ctx, types.NewVerifyRoundKey(taskKey, req.VerifyRound))
	if err != nil {
		return nil, queryVerificationStoreError(err, "task failure class")
	}
	if !bytes.Equal(failure.TaskId, req.TaskId) || failure.VerifyRound != req.VerifyRound {
		return nil, status.Error(codes.Internal, "task failure class primary key does not match task_id")
	}
	freezeEligible, known := types.FreezeSignalEligibilityForTaskFailureClass(failure.FailureClass)
	if !known {
		return nil, status.Error(codes.Internal, "task failure class has an unknown failure_class")
	}
	if failure.FreezeSignalEligible != freezeEligible {
		return nil, status.Error(codes.Internal, "task failure class freeze_signal_eligible does not match failure_class")
	}
	return &types.QueryTaskFailureClassResponse{Failure: failure}, nil
}

// The separate taskID argument is gone with X-16: the store key and the proto
// task_id are the same 32 bytes now, so the store lookups and the scope checks
// below cannot be pointed at two different tasks by a caller that renders one of
// them differently.
func (q *queryServer) loadVerifierAssignment(ctx context.Context, taskKey types.TaskKey, verifyRound uint32) (types.VerifierAssignmentState, error) {
	assignment, err := q.k.ReadVerifierAssignment(ctx, types.NewVerifyRoundKey(taskKey, verifyRound))
	if err != nil {
		return types.VerifierAssignmentState{}, queryVerificationStoreError(err, "verifier assignment")
	}
	if err := q.validateVerifierAssignmentQueryRow(assignment, sdkContextFrom(ctx).ChainID(), taskKey, verifyRound); err != nil {
		return types.VerifierAssignmentState{}, err
	}
	core, err := q.k.TaskCore.Get(ctx, taskKey)
	if err != nil || !bytes.Equal(core.TaskId, taskKey) {
		return types.VerifierAssignmentState{}, status.Error(codes.Internal, "verifier assignment is orphaned from task_core")
	}
	window, err := q.k.VerifierCandidateWindow.Get(ctx, types.NewVerifyRoundKey(taskKey, verifyRound))
	if err != nil || !bytes.Equal(window.TaskId, taskKey) || window.VerifyRound != verifyRound {
		return types.VerifierAssignmentState{}, status.Error(codes.Internal, "verifier assignment is orphaned from its candidate window")
	}
	if !bytes.Equal(window.GetVerifierWindowHash(), assignment.VerifierCandidateWindowHash) {
		return types.VerifierAssignmentState{}, status.Error(codes.Internal, "verifier assignment candidate window hash does not match its source")
	}
	if window.Status != types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY &&
		window.Status != types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_PRUNED {
		return types.VerifierAssignmentState{}, status.Error(codes.Internal, "verifier assignment candidate window is not materialized")
	}
	if assignment.SelectionRandomnessHeight != window.SelectionRandomnessHeight ||
		assignment.SelectionRandomnessHeight <= window.WindowRandomnessHeight ||
		assignment.OpenVerifyHeight > window.AssignmentDeadlineHeight {
		return types.VerifierAssignmentState{}, status.Error(codes.Internal, "verifier assignment clock does not match its candidate window")
	}
	return assignment, nil
}

func (q *queryServer) validateVerifierAssignmentQueryRow(assignment types.VerifierAssignmentState, chainID string, taskID []byte, round uint32) error {
	if !bytes.Equal(assignment.TaskId, taskID) || assignment.VerifyRound != round {
		return status.Error(codes.Internal, "verifier assignment primary key does not match its scope")
	}
	if assignment.SelectedVerifierCount == 0 || uint32(len(assignment.SelectedVerifiers)) != assignment.SelectedVerifierCount {
		return status.Error(codes.Internal, "verifier assignment has an invalid selected verifier count")
	}
	if len(assignment.VerifierCandidateWindowHash) != types.Hash32Len || len(assignment.VerifierLegalSetHash) != types.Hash32Len ||
		len(assignment.SelectedVerifiersHash) != types.Hash32Len || len(assignment.SelectionRandomnessBeacon) != types.Hash32Len ||
		assignment.SelectionRandomnessHeight == 0 || assignment.OpenVerifyHeight < assignment.SelectionRandomnessHeight ||
		assignment.CommitDeadlineHeight <= assignment.OpenVerifyHeight || assignment.VerifyDeadlineHeight <= assignment.CommitDeadlineHeight {
		return status.Error(codes.Internal, "verifier assignment has an incomplete retained header")
	}
	if assignment.RevealDeadlineHeight != 0 &&
		assignment.RevealDeadlineHeight >= assignment.VerifyDeadlineHeight {
		return status.Error(codes.Internal, "verifier assignment has a non-canonical reveal deadline")
	}
	selectedHash, err := SelectedVerifierRefsHash(
		chainID, taskID, round, assignment.VerifierLegalSetHash,
		assignment.SelectionRandomnessHeight, assignment.SelectionRandomnessBeacon,
		assignment.SelectedVerifiers,
	)
	if err != nil || !bytes.Equal(selectedHash, assignment.SelectedVerifiersHash) {
		return status.Error(codes.Internal, "verifier assignment selected_verifiers_hash does not match its ordered refs")
	}
	return nil
}

// dataUnavailableReportsPageDigests names this RPC's two ordered selector fields
// - task_id then verify_round, per §16.2's request field order - and hands them
// to the one shared producer.
//
// It cannot use decodeStringPairQueryPageToken above: this query's primary key is
// the (task_id, verify_round, verifier_operator_address) triple rather than a
// (string, Hash32) pair. Only the primary key differs, though, so the digest pair
// must still come from the same place.
func dataUnavailableReportsPageDigests(chainID string, taskID []byte, verifyRound uint32) ([]byte, []byte, error) {
	return shared.QueryPageDigestsV1(
		chainID, shared.QueryRPCTaskDataUnavailableReportsV1,
		taskID, shared.Uint32BE(verifyRound),
	)
}

func (q *queryServer) decodeDataUnavailableReportsPageToken(
	ctx context.Context,
	encoded []byte,
	taskKey types.TaskKey,
	verifyRound uint32,
) (uint64, string, []byte, []byte, error) {
	sdkCtx := sdkContextFrom(ctx)
	if sdkCtx.BlockHeight() < 0 {
		return 0, "", nil, nil, status.Error(codes.Internal, "query block height is negative")
	}
	queryHeight := uint64(sdkCtx.BlockHeight())
	// The selector digest already hashed the raw task_id, so the store key and
	// the digest preimage are now literally the same bytes and the separate
	// taskID argument is gone.
	rpcDigest, selectorDigest, err := dataUnavailableReportsPageDigests(sdkCtx.ChainID(), taskKey, verifyRound)
	if err != nil {
		return 0, "", nil, nil, status.Error(codes.Internal, err.Error())
	}
	if len(encoded) == 0 {
		return queryHeight, "", rpcDigest, selectorDigest, nil
	}
	lastPrimaryKey, err := shared.DecodePageTokenV1(encoded, rpcDigest, selectorDigest, queryHeight)
	if err != nil {
		return 0, "", nil, nil, status.Error(codes.InvalidArgument, err.Error())
	}
	codec := q.k.DataUnavailableReport.KeyCodec()
	read, key, err := codec.Decode(lastPrimaryKey)
	if err != nil || read != len(lastPrimaryKey) || !bytes.Equal(key.K1(), taskKey) || key.K2() != verifyRound {
		return 0, "", nil, nil, status.Error(codes.InvalidArgument, "page_token has a non-canonical primary key")
	}
	reencoded, err := q.encodeDataUnavailableReportPrimaryKey(key.K1(), key.K2(), key.K3())
	if err != nil || !bytes.Equal(reencoded, lastPrimaryKey) {
		return 0, "", nil, nil, status.Error(codes.InvalidArgument, "page_token primary key is not canonically encoded")
	}
	operator, err := q.requireCanonicalAddress("page_token verifier_operator_address", key.K3())
	if err != nil || operator != key.K3() {
		return 0, "", nil, nil, status.Error(codes.InvalidArgument, "page_token has a non-canonical verifier operator")
	}
	return queryHeight, operator, rpcDigest, selectorDigest, nil
}

func (q *queryServer) encodeDataUnavailableReportPrimaryKey(taskKey types.TaskKey, verifyRound uint32, operator string) ([]byte, error) {
	key := types.NewVerifyActorKey(taskKey, verifyRound, operator)
	codec := q.k.DataUnavailableReport.KeyCodec()
	encoded := make([]byte, codec.Size(key))
	written, err := codec.Encode(encoded, key)
	if err != nil {
		return nil, err
	}
	return encoded[:written], nil
}

func encodeDataUnavailableReportsPageToken(rpcDigest, selectorDigest, lastPrimaryKey []byte, queryHeight uint64) ([]byte, error) {
	return shared.EncodePageTokenV1(rpcDigest, selectorDigest, lastPrimaryKey, queryHeight)
}

func queryCommitKey(ctx context.Context, taskID []byte, verifyRound uint32, verifier string) (types.CommitKey, [32]byte, error) {
	key, err := types.DeriveCommitKey(sdkContextFrom(ctx).ChainID(), taskID, verifyRound, verifier)
	if err != nil {
		return nil, [32]byte{}, status.Error(codes.Internal, err.Error())
	}
	// The derived digest is the store key itself now, so the [32]byte is only
	// returned for the callers that compare it against a proto `bytes` field.
	return types.NewCommitKey(key[:]), key, nil
}

func queryVerificationStoreError(err error, object string) error {
	if errors.Is(err, collections.ErrNotFound) {
		return status.Errorf(codes.NotFound, "%s not found", object)
	}
	return status.Error(codes.Internal, err.Error())
}

// EvidenceCleanup is the registered bounded cleanup projection.
func (q *queryServer) EvidenceCleanup(ctx context.Context, req *types.QueryEvidenceCleanupRequest) (*types.QueryEvidenceCleanupResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	taskKey, err := requireQueryHash32("task_id", req.TaskId)
	if err != nil {
		return nil, err
	}
	cursor, cursorErr := q.k.TaskCleanupCursor.Get(ctx, taskKey)
	summary, summaryErr := q.k.ReadTaskTerminalSummary(ctx, taskKey)
	cursorFound := cursorErr == nil
	summaryFound := summaryErr == nil
	if cursorErr != nil && !errors.Is(cursorErr, collections.ErrNotFound) {
		return nil, status.Error(codes.Internal, cursorErr.Error())
	}
	if summaryErr != nil && !errors.Is(summaryErr, collections.ErrNotFound) {
		return nil, status.Error(codes.Internal, summaryErr.Error())
	}
	if cursorFound && summaryFound {
		return nil, status.Error(codes.Internal, "task has both cleanup cursor and terminal summary")
	}
	if cursorFound {
		if !bytes.Equal(cursor.TaskId, req.TaskId) || cursor.Phase == types.TaskCleanupPhase_TASK_CLEANUP_PHASE_UNSPECIFIED {
			return nil, status.Error(codes.Internal, "task cleanup cursor is non-canonical")
		}
		phase := cursor.Phase
		return &types.QueryEvidenceCleanupResponse{Cleanup: types.TaskCleanupProgressViewV1{
			TaskId: append([]byte(nil), req.TaskId...), Status: types.TaskCleanupStatus_TASK_CLEANUP_STATUS_RUNNING,
			XPhase:       &types.TaskCleanupProgressViewV1_Phase{Phase: phase},
			VisitedCount: cursor.VisitedCount, DeletedCount: cursor.DeletedCount,
		}}, nil
	}
	if summaryFound {
		if !bytes.Equal(summary.TaskId, req.TaskId) || len(summary.SummaryHash) != types.Hash32Len {
			return nil, status.Error(codes.Internal, "task terminal summary is non-canonical")
		}
		return &types.QueryEvidenceCleanupResponse{Cleanup: types.TaskCleanupProgressViewV1{
			TaskId: append([]byte(nil), req.TaskId...), Status: types.TaskCleanupStatus_TASK_CLEANUP_STATUS_COMPACTED,
		}}, nil
	}
	core, err := q.k.TaskCore.Get(ctx, taskKey)
	if err != nil {
		return nil, queryVerificationStoreError(err, "task cleanup progress")
	}
	if !bytes.Equal(core.TaskId, req.TaskId) {
		return nil, status.Error(codes.Internal, "task cleanup progress primary key does not match task_id")
	}
	return &types.QueryEvidenceCleanupResponse{Cleanup: types.TaskCleanupProgressViewV1{
		TaskId: append([]byte(nil), req.TaskId...),
		Status: types.TaskCleanupStatus_TASK_CLEANUP_STATUS_NOT_SCHEDULED,
	}}, nil
}
