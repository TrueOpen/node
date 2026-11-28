package keeper

import (
	"context"

	"cosmossdk.io/collections"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	"github.com/TrueOpen/node/x/task/types"
)

// Worker timeout and late submission of a Verifier-panel audit.
//
// With anchor_late_window_blocks set, a timeout does not end the audit. It is
// recorded, and the Worker has the late window to submit what was missing (or
// to submit its proof again when a panel could not obtain the material). The
// late proof opens one more panel round, which decides like any other.
//
// What a timeout costs depends on whose omission it is:
//
//   - opening deadline missed, proof deadline missed, or the threshold of a
//     panel cast UNAVAILABLE: the Worker's. It is paused for dispatch until
//     its late submission, the timeout is counted in its rolling ledger, and
//     without a late submission by the late deadline the audit ends as LATE
//     EXPIRED (not paid, the timeout slash, no jail).
//   - two panel rounds without a conclusion: nobody's that the chain can name.
//     The Worker is not paused and nothing is counted. It may submit its proof
//     again to have another panel drawn; if it does not, or that panel reaches
//     no conclusion either, the audit ends without a verdict (paid, nobody
//     slashed). An absent vote never costs the Worker anything.
//
// A task has one late attempt and one late deadline, fixed at its first
// timeout.

const (
	lateStateNone      = types.AnchorLateStateV1_ANCHOR_LATE_STATE_V1_UNSPECIFIED
	lateStateAwaiting  = types.AnchorLateStateV1_ANCHOR_LATE_STATE_V1_AWAITING_LATE
	lateStateSubmitted = types.AnchorLateStateV1_ANCHOR_LATE_STATE_V1_LATE_SUBMITTED
	lateStateExpired   = types.AnchorLateStateV1_ANCHOR_LATE_STATE_V1_LATE_EXPIRED

	// anchorLatePauseScanLimit bounds the audits read to decide whether a
	// Worker still has a late submission outstanding. A Worker with more
	// outstanding than this stays paused until the count falls under it.
	anchorLatePauseScanLimit = 64
)

// anchorTimeoutIsWorkers reports a timeout reason that names an omission of
// the Worker itself.
func anchorTimeoutIsWorkers(reason types.AnchorTimeoutReasonV1) bool {
	switch reason {
	case types.AnchorTimeoutReasonV1_ANCHOR_TIMEOUT_REASON_V1_OPENING_DEADLINE,
		types.AnchorTimeoutReasonV1_ANCHOR_TIMEOUT_REASON_V1_PROOF_DEADLINE,
		types.AnchorTimeoutReasonV1_ANCHOR_TIMEOUT_REASON_V1_PANEL_UNAVAILABLE:
		return true
	}
	return false
}

// anchorLateAttemptUsed reports whether the audit's one late panel round was
// already opened.
func anchorLateAttemptUsed(state types.AnchorAuditState) bool {
	for _, round := range state.PanelRounds {
		if round.Attempt != 0 {
			return true
		}
	}
	return false
}

// anchorLateSubmissionOpen reports whether the Worker may still record a late
// opening or proof for the audit at height.
func anchorLateSubmissionOpen(state types.AnchorAuditState, height uint64) bool {
	return isAnchorPanelAudit(state) && state.ResultHeight == 0 && state.LateState == lateStateAwaiting &&
		height <= state.LateDeadlineHeight && !anchorLateAttemptUsed(state)
}

// anchorLateDueVerdict is the verdict an audit waiting for a late submission
// has reached at height without it being recorded yet: at the late deadline
// the Worker's own omission ends as LATE EXPIRED, anything else without a
// verdict. Settlement and the sweep both read it.
func anchorLateDueVerdict(state types.AnchorAuditState, height uint64) (types.AnchorVerdictV1, types.AnchorResultSourceV1, string, bool) {
	if state.ResultHeight != 0 || state.LateState != lateStateAwaiting || height < state.LateDeadlineHeight {
		return 0, 0, "", false
	}
	if _, open := anchorPanelOpenRound(state); open {
		return 0, 0, "", false
	}
	if anchorTimeoutIsWorkers(state.TimeoutReason) {
		return types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_LATE_EXPIRED,
			"the Worker made no retrievable late submission before the late deadline", true
	}
	return types.AnchorVerdictV1_ANCHOR_VERDICT_V1_NO_VERDICT, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_VERIFIER_PANEL,
		"the Verifier panels reached no conclusion", true
}

// countAnchorWorkerTimeout records a timeout of the Worker's own in its
// rolling ledger: one entry per settlement period, however many tasks timed
// out in it, and entries older than the rolling window dropped. It reports
// the periods with a timeout inside the window, this one included, and
// whether this timeout is the one that carries the period's repeat fault: the
// count has passed the free count and the period was not charged before.
// Without a settlement period or a rolling window nothing is counted.
func (k Keeper) countAnchorWorkerTimeout(ctx context.Context, worker string, height uint64, params types.TaskParamsV1) (uint32, bool, error) {
	v := params.Verification
	if v.BatchSettlementPeriodBlocks == 0 || v.AnchorTimeoutRollingWindowBlocks == 0 {
		return 0, false, nil
	}
	raw, err := k.sessionAddressToStore("worker_operator_address", worker)
	if err != nil {
		return 0, false, nil
	}
	ledger, err := k.WorkerTimeoutLedger.Get(ctx, raw)
	if err != nil && !errIsNotFound(err) {
		return 0, false, err
	}
	period := height / v.BatchSettlementPeriodBlocks
	kept := types.WorkerTimeoutLedgerState{OperatorAddress: worker}
	listed := false
	for index, entry := range ledger.Periods {
		if index >= len(ledger.Heights) || height-ledger.Heights[index] >= v.AnchorTimeoutRollingWindowBlocks {
			continue
		}
		kept.Periods = append(kept.Periods, entry)
		kept.Heights = append(kept.Heights, ledger.Heights[index])
		listed = listed || entry == period
	}
	if !listed {
		kept.Periods = append(kept.Periods, period)
		kept.Heights = append(kept.Heights, height)
	}
	charged := false
	for _, entry := range ledger.SlashedPeriods {
		for _, live := range kept.Periods {
			if entry == live {
				kept.SlashedPeriods = append(kept.SlashedPeriods, entry)
				charged = charged || entry == period
			}
		}
	}
	count := uint32(len(kept.Periods))
	repeat := !charged && count > v.AnchorTimeoutFreeCount
	if repeat {
		kept.SlashedPeriods = append(kept.SlashedPeriods, period)
	}
	return count, repeat, k.WorkerTimeoutLedger.Set(ctx, raw, kept)
}

// refreshAnchorLatePause sets the Worker's late-submission pause to what its
// audits say: paused while any of them waits for a late submission after an
// omission of the Worker's own. The audit being changed by the caller is
// passed with its new answer, since its row is not stored yet.
func (k Keeper) refreshAnchorLatePause(ctx context.Context, worker string, taskKey types.TaskKey, taskID []byte, selfWaiting bool) error {
	raw, err := k.sessionAddressToStore("worker_operator_address", worker)
	if err != nil {
		return nil
	}
	waiting := selfWaiting
	if !waiting {
		iter, err := k.WorkerLateIndex.Iterate(ctx, collections.NewPrefixedPairRange[types.AddrKey, types.Hash32Key](raw))
		if err != nil {
			return err
		}
		visited := 0
		for ; iter.Valid() && !waiting; iter.Next() {
			key, err := iter.Key()
			if err != nil {
				iter.Close()
				return err
			}
			if visited++; visited > anchorLatePauseScanLimit {
				waiting = true
				break
			}
			other := key.K2()
			if string(other) == string(taskKey) {
				continue
			}
			if audit, err := k.AnchorAudit.Get(ctx, other); err == nil &&
				audit.ResultHeight == 0 && audit.LateState == lateStateAwaiting && anchorTimeoutIsWorkers(audit.TimeoutReason) {
				waiting = true
			}
		}
		if err := iter.Close(); err != nil {
			return err
		}
	}
	reasons, changed, err := k.hubKeeper.SetWorkerDispatchPause(ctx, worker, hubtypes.WorkerPauseReasonLateSubmission, waiting)
	if err != nil || !changed {
		return err
	}
	if waiting {
		return emitTypedEvent(ctx, &types.EventWorkerDispatchPaused{
			OperatorAddress: worker, PauseReasons: reasons, TaskId: append([]byte(nil), taskID...),
		})
	}
	return emitTypedEvent(ctx, &types.EventWorkerDispatchResumed{OperatorAddress: worker, PauseReasons: reasons})
}

func (k Keeper) setWorkerLateIndex(ctx context.Context, worker string, taskKey types.TaskKey, present bool) error {
	raw, err := k.sessionAddressToStore("worker_operator_address", worker)
	if err != nil {
		return nil
	}
	key := types.NewVerifierActiveJobKey(raw, taskKey)
	if present {
		return k.WorkerLateIndex.Set(ctx, key)
	}
	return k.WorkerLateIndex.Remove(ctx, key)
}

// retireAnchorLateWork removes a final audit from the Worker's late list and
// lifts the pause it was holding. It is a no-op for an audit that never timed
// out.
func (k Keeper) retireAnchorLateWork(ctx context.Context, taskKey types.TaskKey, state types.AnchorAuditState) error {
	if state.LateState == lateStateNone || state.WinnerWorker == "" {
		return nil
	}
	if err := k.setWorkerLateIndex(ctx, state.WinnerWorker, taskKey, false); err != nil {
		return err
	}
	return k.refreshAnchorLatePause(ctx, state.WinnerWorker, taskKey, state.TaskId, false)
}

// recordAnchorWorkerTimeout takes the timeout path for the audit at height. It
// reports false, having changed nothing, when the task's frozen parameters set
// no late window: the caller then applies the outcome of an audit without late
// submission. The caller stores the audit row.
func (k Keeper) recordAnchorWorkerTimeout(
	ctx context.Context,
	taskKey types.TaskKey,
	audit *types.AnchorAuditState,
	reason types.AnchorTimeoutReasonV1,
	height uint64,
) (bool, error) {
	params, err := k.ParamsForTask(ctx, taskKey)
	if err != nil || params.Verification.AnchorLateWindowBlocks == 0 {
		return false, nil
	}
	first := audit.LateState == lateStateNone
	deadline := audit.LateDeadlineHeight
	if first {
		var overflow bool
		if deadline, overflow = checkedHeightAdd(height, params.Verification.AnchorLateWindowBlocks); overflow {
			return false, nil
		}
		audit.TimeoutRecordedHeight = height
	}
	audit.TimeoutReason = reason
	workers := anchorTimeoutIsWorkers(reason)
	event := &types.EventAnchorWorkerTimeout{
		TaskId: append([]byte(nil), audit.TaskId...), WorkerOperatorAddress: audit.WinnerWorker, Reason: reason,
	}
	if workers {
		count, repeat, err := k.countAnchorWorkerTimeout(ctx, audit.WinnerWorker, height, params)
		if err != nil {
			return false, err
		}
		event.PeriodsInWindow, event.Slashed = count, repeat
		if repeat {
			// The timeout that takes the Worker past its free count is not
			// open to late submission: it ends here as a missed deadline, and
			// the task's settlement applies the timeout fault once.
			if err := emitTypedEvent(ctx, event); err != nil {
				return false, err
			}
			return true, k.finalizeAnchorPanelAudit(ctx, taskKey, audit, types.AnchorVerdictV1_ANCHOR_VERDICT_V1_REJECT,
				types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_TIMEOUT,
				"repeated Worker timeouts inside the rolling window", height)
		}
	}
	if !first && !workers {
		// The late panel reached no conclusion either. Nothing more can be
		// asked of the Worker.
		return true, k.finalizeAnchorPanelAudit(ctx, taskKey, audit, types.AnchorVerdictV1_ANCHOR_VERDICT_V1_NO_VERDICT,
			types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_VERIFIER_PANEL, "the Verifier panels reached no conclusion", height)
	}
	audit.LateState = lateStateAwaiting
	audit.LateDeadlineHeight = deadline
	event.LateDeadlineHeight = deadline
	if err := emitTypedEvent(ctx, event); err != nil {
		return false, err
	}
	if err := k.setWorkerLateIndex(ctx, audit.WinnerWorker, taskKey, true); err != nil {
		return false, err
	}
	if workers {
		if err := k.refreshAnchorLatePause(ctx, audit.WinnerWorker, taskKey, audit.TaskId, true); err != nil {
			return false, err
		}
	}
	return true, k.setAnchorPanelDue(ctx, taskKey, deadline)
}

// advanceAnchorWorkerDeadlines watches the opening and proof deadlines of a
// panel audit that has no round yet. A missed one is the Worker's timeout. It
// leaves an audit without a late window alone: that one is settled at its
// verdict deadline as before. The caller stores the audit row.
func (k Keeper) advanceAnchorWorkerDeadlines(ctx context.Context, taskKey types.TaskKey, audit *types.AnchorAuditState, height uint64) error {
	reason, deadline := types.AnchorTimeoutReasonV1_ANCHOR_TIMEOUT_REASON_V1_OPENING_DEADLINE, audit.OpeningDeadlineHeight
	switch {
	case audit.LateState != lateStateNone || len(audit.Positions) == 0 || len(audit.WorkerProofHash) == types.Hash32Len:
		return k.clearAnchorPanelDue(ctx, taskKey)
	case len(audit.OpeningHash) == types.Hash32Len:
		reason, deadline = types.AnchorTimeoutReasonV1_ANCHOR_TIMEOUT_REASON_V1_PROOF_DEADLINE, audit.ProofDeadlineHeight
	}
	if deadline == 0 {
		return k.clearAnchorPanelDue(ctx, taskKey)
	}
	if height <= deadline {
		return k.setAnchorPanelDue(ctx, taskKey, deadline+1)
	}
	handled, err := k.recordAnchorWorkerTimeout(ctx, taskKey, audit, reason, height)
	if err != nil || handled {
		return err
	}
	return k.clearAnchorPanelDue(ctx, taskKey)
}

// expireAnchorLate ends an audit whose late deadline has passed. The caller
// stores the audit row.
func (k Keeper) expireAnchorLate(ctx context.Context, taskKey types.TaskKey, audit *types.AnchorAuditState, height uint64) error {
	verdict, source, reason, due := anchorLateDueVerdict(*audit, height)
	if !due {
		return k.setAnchorPanelDue(ctx, taskKey, audit.LateDeadlineHeight)
	}
	if err := markAnchorLateExpired(ctx, audit, source, height); err != nil {
		return err
	}
	return k.finalizeAnchorPanelAudit(ctx, taskKey, audit, verdict, source, reason, height)
}

// markAnchorLateExpired records the LATE EXPIRED state on an audit that ends
// with that source.
func markAnchorLateExpired(ctx context.Context, audit *types.AnchorAuditState, source types.AnchorResultSourceV1, height uint64) error {
	if source != types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_LATE_EXPIRED {
		return nil
	}
	audit.LateState = lateStateExpired
	return emitTypedEvent(ctx, &types.EventAnchorLateExpired{
		TaskId: append([]byte(nil), audit.TaskId...), WorkerOperatorAddress: audit.WinnerWorker, Height: height,
	})
}

// acceptAnchorLateProof moves an audit whose late proof was just recorded to
// its late panel round: the Worker's pause ends unless another audit of its
// still waits. The caller stores the audit row.
func (k Keeper) acceptAnchorLateProof(ctx context.Context, taskKey types.TaskKey, audit *types.AnchorAuditState, height uint64) error {
	audit.LateState = lateStateSubmitted
	if err := emitTypedEvent(ctx, &types.EventAnchorLateSubmitted{
		TaskId: append([]byte(nil), audit.TaskId...), WorkerOperatorAddress: audit.WinnerWorker, Height: height,
	}); err != nil {
		return err
	}
	return k.refreshAnchorLatePause(ctx, audit.WinnerWorker, taskKey, audit.TaskId, false)
}

// WorkerLateSubmissions lists a Worker's audits that timed out and are not
// final: those waiting for its late submission and those whose late panel is
// still at work.
func (q queryServer) WorkerLateSubmissions(ctx context.Context, req *types.QueryWorkerLateSubmissionsRequest) (*types.QueryWorkerLateSubmissionsResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "worker late submissions request is required")
	}
	raw, err := q.k.sessionAddressToStore("worker_operator_address", req.WorkerOperatorAddress)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	iter, err := q.k.WorkerLateIndex.Iterate(ctx, collections.NewPrefixedPairRange[types.AddrKey, types.Hash32Key](raw))
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	out := &types.QueryWorkerLateSubmissionsResponse{}
	for ; iter.Valid() && len(out.Submissions) < anchorPendingMaxLimit; iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, err
		}
		audit, err := q.k.AnchorAudit.Get(ctx, key.K2())
		if err != nil || audit.ResultHeight != 0 {
			continue
		}
		out.Submissions = append(out.Submissions, types.WorkerLateSubmissionV1{
			TaskId: append([]byte(nil), audit.TaskId...), TimeoutReason: audit.TimeoutReason,
			TimeoutRecordedHeight: audit.TimeoutRecordedHeight, LateDeadlineHeight: audit.LateDeadlineHeight, LateState: audit.LateState,
		})
	}
	return out, nil
}
