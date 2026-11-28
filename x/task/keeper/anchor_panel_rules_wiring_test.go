package keeper

import (
	"bytes"
	"context"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	internaltypes "github.com/TrueOpen/node/x/task/internal/types"
	"github.com/TrueOpen/node/x/task/types"
)

type panelBankTransfer struct {
	from, to string
	amount   int64
}

// panelRecordingBank records the transfers a re-audit makes.
type panelRecordingBank struct {
	types.BankKeeper
	log *[]panelBankTransfer
}

func (b panelRecordingBank) SendCoinsFromAccountToModule(_ context.Context, from sdk.AccAddress, module string, coins sdk.Coins) error {
	*b.log = append(*b.log, panelBankTransfer{from: from.String(), to: module, amount: coins[0].Amount.Int64()})
	return nil
}

func (b panelRecordingBank) SendCoinsFromModuleToAccount(_ context.Context, module string, to sdk.AccAddress, coins sdk.Coins) error {
	*b.log = append(*b.log, panelBankTransfer{from: module, to: to.String(), amount: coins[0].Amount.Int64()})
	return nil
}

// The re-audit fee is taken from the opener when it asks and returned only
// when the re-audit overturns the published verdict.
func TestReauditFeeIsEscrowedAndReturnedOnlyOnOverturn(t *testing.T) {
	for _, overturn := range []bool{true, false} {
		f := newAnchorPanelFixture(t, 4_000, 9)
		log := &[]panelBankTransfer{}
		f.keeper.bankKeeper = panelRecordingBank{BankKeeper: f.keeper.bankKeeper, log: log}
		f.server = msgServer{k: f.keeper}
		audit := f.publish(t, voteReject)
		opener := sdk.AccAddress(bytes.Repeat([]byte{0xd1}, 20)).String()
		_, err := f.server.RequestAnchorReaudit(f.at(int64(audit.ProvisionalHeight+1)), f.reauditMsg(opener, 100))
		require.NoError(t, err)
		require.Equal(t, []panelBankTransfer{{from: opener, to: shared.TaskChallengeEffectModuleName, amount: 2}}, *log)
		reaudit := f.assign(t)
		verdict := voteReject
		if overturn {
			verdict = votePass
		}
		f.castVotes(t, reaudit, reaudit.AssignmentHeight+1, verdict, verdict)
		if overturn {
			require.Equal(t, panelBankTransfer{from: shared.TaskChallengeEffectModuleName, to: opener, amount: 2}, (*log)[1])
			require.Len(t, *log, 2)
		} else {
			require.Len(t, *log, 1)
		}
	}
}

// A vote is taken only from a member of the round, signed by its service, on
// the opening and proof the chain recorded, while the round is open, and once.
func TestPanelVoteRules(t *testing.T) {
	f := newAnchorPanelFixture(t, 4_000, 6)
	round := f.assign(t)
	at := round.AssignmentHeight + 1
	member := round.Members[0]
	cast := func(height uint64, mutate func(*types.MsgSubmitAnchorPanelVote)) (*types.MsgSubmitAnchorPanelVoteResponse, error) {
		msg := f.voteMsg(round, member, votePass)
		mutate(msg)
		return f.server.SubmitAnchorPanelVote(f.at(int64(height)), msg)
	}
	outsider := ""
	for _, candidate := range f.candidates {
		if !anchorPanelMember(round, candidate) {
			outsider = candidate
		}
	}
	for name, test := range map[string]struct {
		mutate func(*types.MsgSubmitAnchorPanelVote)
		want   string
	}{
		"not a member": {func(m *types.MsgSubmitAnchorPanelVote) {
			m.VerifierOperatorAddress, m.SubmitterAddress = outsider, outsider
		}, "not a member"},
		"the Worker": {func(m *types.MsgSubmitAnchorPanelVote) {
			m.VerifierOperatorAddress, m.SubmitterAddress = f.worker, f.worker
		}, "not a member"},
		"not its service":     {func(m *types.MsgSubmitAnchorPanelVote) { m.SubmitterAddress = f.builders[0] }, "current service"},
		"another proof":       {func(m *types.MsgSubmitAnchorPanelVote) { m.ProofHash = bytes32(0xee) }, "different opening or proof"},
		"another opening":     {func(m *types.MsgSubmitAnchorPanelVote) { m.OpeningHash = bytes32(0xee) }, "different opening or proof"},
		"another round":       {func(m *types.MsgSubmitAnchorPanelVote) { m.PanelRound = 9 }, "no panel round"},
		"missing positions":   {func(m *types.MsgSubmitAnchorPanelVote) { m.Verdicts = m.Verdicts[:1] }, "exactly the"},
		"verdict mismatch":    {func(m *types.MsgSubmitAnchorPanelVote) { m.Verdict = voteReject }, "at least one rejected position"},
		"no-verdict vote":     {func(m *types.MsgSubmitAnchorPanelVote) { m.Verdict = noVerdict }, "PASS, REJECT or UNAVAILABLE"},
		"unavailable + rows":  {func(m *types.MsgSubmitAnchorPanelVote) { m.Verdict = voteUnavailable }, "carries no position verdicts"},
		"bad operator string": {func(m *types.MsgSubmitAnchorPanelVote) { m.VerifierOperatorAddress = "x" }, "verifier_operator_address"},
	} {
		_, err := cast(at, test.mutate)
		require.ErrorContains(t, err, test.want, name)
	}
	_, err := cast(round.AssignmentHeight-1, func(*types.MsgSubmitAnchorPanelVote) {})
	require.ErrorContains(t, err, "not assigned yet")
	_, err = cast(round.VoteDeadlineHeight+1, func(*types.MsgSubmitAnchorPanelVote) {})
	require.ErrorContains(t, err, "vote deadline")
	require.Empty(t, f.round(t).Votes, "a refused vote writes nothing")

	response, err := cast(round.VoteDeadlineHeight, func(*types.MsgSubmitAnchorPanelVote) {})
	require.NoError(t, err, "the deadline height itself is open")
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_APPLIED, response.Status)
	response, err = cast(round.VoteDeadlineHeight, func(*types.MsgSubmitAnchorPanelVote) {})
	require.NoError(t, err)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, response.Status, "the same vote again changes nothing")
	_, err = f.server.SubmitAnchorPanelVote(f.at(int64(round.VoteDeadlineHeight)), f.voteMsg(round, member, voteUnavailable))
	require.ErrorContains(t, err, "cannot be changed")
	require.Len(t, f.round(t).Votes, 1)

	// After the round closed, a member's own vote is still answered, a new one refused.
	f.castVotes(t, types.AnchorPanelRoundState{PanelRound: round.PanelRound, Members: round.Members[1:]}, round.VoteDeadlineHeight, votePass)
	require.NotZero(t, f.round(t).ClosedHeight)
	response, err = cast(round.VoteDeadlineHeight, func(*types.MsgSubmitAnchorPanelVote) {})
	require.NoError(t, err)
	require.True(t, response.RoundClosed)
	require.Equal(t, shared.MutationStatusV1_MUTATION_STATUS_V1_NOOP, response.Status)
	_, err = f.server.SubmitAnchorPanelVote(f.at(int64(round.VoteDeadlineHeight)), f.voteMsg(round, round.Members[2], votePass))
	require.ErrorContains(t, err, "is closed")
}

// A panel audit takes no validator vote, and an audit under the validator
// vote takes no panel vote.
func TestPanelAndValidatorVotesDoNotMix(t *testing.T) {
	f := newAnchorPanelFixture(t, 4_000, 6)
	_, err := f.vote(0, f.valAcct[0], votePass, f.audit.WorkerProofHeight+1)
	require.ErrorContains(t, err, "decided by a Verifier panel")

	legacy := newAnchorOnlyFixture(t, 4_000, true)
	legacy.submitReceipt(t)
	state := legacy.sampleRound(t)
	legacy.openAndProveSampled(t, state)
	_, err = legacy.server.SubmitAnchorPanelVote(legacy.at(int64(state.SampleHeight+3)), &types.MsgSubmitAnchorPanelVote{
		TaskId: legacy.taskID, PanelRound: 2, OpeningHash: bytes32(0x0c), ProofHash: bytes32(0x0d), Verdict: voteUnavailable,
		VerifierOperatorAddress: legacy.operators[1], SubmitterAddress: legacy.operators[1],
	})
	require.ErrorContains(t, err, "not decided by a Verifier panel")
	_, err = legacy.server.RequestAnchorReaudit(legacy.at(int64(state.SampleHeight+3)), &types.MsgRequestAnchorReaudit{
		TaskId: legacy.taskID, OpenerAddress: legacy.operators[1], MaxFee: shared.NewAmount(10),
	})
	require.ErrorContains(t, err, "not decided by a Verifier panel")
}

// Under modes 0 and 1 nothing of the panel audit exists: the audit row, its
// deadlines, the proof transaction and the events are what they were.
func TestValidatorVoteModesAreUnchanged(t *testing.T) {
	for _, mode := range []uint32{0, 1} {
		f := newAnchorOnlyFixture(t, 4_000, true)
		f.params.Verification.AnchorAuditMode = mode
		// Panel parameters that are set but not switched on change nothing.
		f.params.Verification.AnchorPanelSize, f.params.Verification.AnchorPanelThreshold = 3, 2
		f.params.Verification.AnchorPanelVoteWindowBlocks, f.params.Verification.AnchorPublicationWindowBlocks = 20, 50
		require.NoError(t, f.keeper.Params.Set(f.ctx, f.params))
		f.submitReceipt(t)
		state := f.sampleRound(t)
		require.Equal(t, mode, state.AuditMode)
		require.Equal(t, state.ProofDeadlineHeight+f.params.Verification.AnchorVerdictWindowBlocks, state.VerdictDeadlineHeight)

		events := sdk.UnwrapSDKContext(f.ctx).EventManager()
		before := len(events.Events())
		f.openAndProveSampled(t, state)
		require.Len(t, events.Events(), before, "the opening and the proof emit no event in these modes")
		got, err := f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
		require.NoError(t, err)
		require.Empty(t, got.PanelRounds)
		require.Zero(t, got.PanelRound)
		has, err := f.keeper.anchorPanelHasWork(f.ctx, f.taskKey, got)
		require.NoError(t, err)
		require.False(t, has)
		visited, err := f.keeper.processAnchorPanelDue(f.at(int64(state.VerdictDeadlineHeight)), state.VerdictDeadlineHeight, 10)
		require.NoError(t, err)
		require.Zero(t, visited)

		voteAt := state.SampleHeight + 3
		_, err = f.vote(0, f.valAcct[0], votePass, voteAt)
		require.NoError(t, err)
		response, err := f.vote(1, f.valAcct[1], votePass, voteAt)
		require.NoError(t, err)
		require.True(t, response.Finalized)
		settlement, err := f.settleNow(t, voteAt+1)
		require.NoError(t, err)
		f.requirePaidInFull(t, settlement)
	}
}

// In panel mode a task the coin did not select has no panel and settles as
// before; and a hand-raise for a round after the second on a task without a
// panel round is refused as it always was.
func TestPanelModeLeavesUnselectedTasksAlone(t *testing.T) {
	f := newAnchorOnlyFixture(t, 1_000_000_000, true)
	f.params.Verification.AnchorAuditMode = types.AnchorAuditModePanel
	f.params.Verification.AnchorPanelSize, f.params.Verification.AnchorPanelThreshold = 3, 2
	f.params.Verification.AnchorPanelVoteWindowBlocks = 20
	f.zeroAuditRate(t)
	f.submitReceipt(t)
	state := f.sampleRound(t)
	require.False(t, state.Selected)
	settlement, err := f.settleNow(t, f.window.SelectionRandomnessHeight+2)
	require.NoError(t, err)
	f.requirePaidInFull(t, settlement)
	has, err := f.keeper.anchorPanelHasWork(f.ctx, f.taskKey, state)
	require.NoError(t, err)
	require.False(t, has)

	_, err = f.server.SubmitVerifierHandraises(f.at(int64(state.SampleHeight+1)), &types.MsgSubmitVerifierHandraises{
		TaskId: f.taskID, SubmitterAddress: f.builders[0], Handraises: []types.VerifierHandraiseV1{{
			SchemaVersion: types.VerifierHandraiseSchemaVersionV1, ChainId: sdkChainID(f.ctx), TaskId: f.taskID, VerifyRound: 3,
			InferReceiptHash: bytes32(1), OutputHash: bytes32(2), ModelId: bytes32(3), ProfileVersion: 1,
			Member: types.CandidateMemberRefV1{CandidatePoolSnapshotId: bytes32(4), Slot: 1, SlotVersion: 1, OperatorAddress: f.operators[1]},
			Duty:   shared.Duty_DUTY_VERIFIER, ExpiryHeight: 1_000, ServiceSignature: make([]byte, 64),
		}},
	})
	require.ErrorContains(t, err, "verifier handraise envelope is invalid")
}

// A missing beacon delays the round and, past the round's own deadline, ends
// it without a conclusion. It is never the Worker's fault.
func TestMissingPanelBeaconNeverFaultsTheWorker(t *testing.T) {
	f := newAnchorPanelFixture(t, 4_000, 7)
	selection := f.selection(t)
	window := selection.Window
	f.hubState.noBeacon[int64(window.WindowRandomnessHeight)] = true
	require.Equal(t, uint64(1), f.sweep(t, window.WindowRandomnessHeight))
	require.Equal(t, window.WindowRandomnessHeight+1, f.dueHeight(t), "the round waits one block at a time")
	require.Zero(t, f.sweep(t, window.WindowRandomnessHeight), "a waiting round is not revisited in the same block")
	f.sweep(t, window.AssignmentDeadlineHeight)
	audit := f.reload(t)
	require.Equal(t, noVerdict, audit.PanelRounds[0].Result)
	require.Len(t, audit.PanelRounds, 2)
	require.Zero(t, audit.ResultHeight)
	require.Empty(t, *f.faults)
}

// The sweep never returns an error for one task's broken rows: the task is
// taken off the queue and ends at its verdict deadline without a verdict.
func TestPanelSweepSurvivesABrokenRow(t *testing.T) {
	f := newAnchorPanelFixture(t, 4_000, 7)
	due := f.dueHeight(t)
	// An unreadable working row.
	require.NoError(t, f.keeper.AnchorPanelSelection.Set(f.ctx, f.taskKey, internaltypes.AnchorPanelSelectionStoreState{
		TaskId: f.taskID, PanelRound: 2, Window: []byte{0xff, 0xff, 0xff},
	}))
	require.Equal(t, uint64(2), f.sweep(t, due), "the broken round is closed, then its re-assignment is opened")
	audit := f.reload(t)
	require.Equal(t, noVerdict, audit.PanelRounds[0].Result)
	require.Len(t, audit.PanelRounds, 2)

	// A due entry whose audit row is gone.
	require.NoError(t, f.keeper.AnchorAudit.Remove(f.ctx, f.taskKey))
	visited, err := f.keeper.processAnchorPanelDue(f.at(int64(f.dueHeight(t))), f.dueHeight(t), 10)
	require.NoError(t, err)
	require.Equal(t, uint64(1), visited)
	_, err = f.keeper.AnchorPanelDueHeight.Get(f.ctx, f.taskKey)
	require.Error(t, err, "the orphaned entry is removed")
}

// EndBlock runs at most one candidate-pool scan per block: a second
// re-assignment due in the same block waits for the next one.
func TestPanelSweepScansThePoolOncePerBlock(t *testing.T) {
	f := newAnchorPanelFixture(t, 4_000, 7)
	round := f.assign(t)
	height := round.VoteDeadlineHeight + 1
	granted, err := f.keeper.takeAnchorPanelScan(f.ctx, height)
	require.NoError(t, err)
	require.True(t, granted)
	granted, err = f.keeper.takeAnchorPanelScan(f.ctx, height)
	require.NoError(t, err)
	require.False(t, granted)

	// The block's scan is spent: the silent round is closed, its re-assignment waits.
	require.Equal(t, uint64(2), f.sweep(t, height))
	audit := f.reload(t)
	require.Len(t, audit.PanelRounds, 1)
	require.Equal(t, height+1, f.dueHeight(t))
	require.Equal(t, uint64(1), f.sweep(t, height+1))
	require.Len(t, f.reload(t).PanelRounds, 2)
}

// If the sweep never runs, settlement still reads the right outcome: a verdict
// whose publication window has ended is final, and an audit that never got a
// verdict ends without one at its frozen deadline. Neither costs the Worker.
func TestSettlementDoesNotDependOnTheSweep(t *testing.T) {
	published := newAnchorPanelFixture(t, 4_000, 6)
	audit := published.publish(t, votePass)
	_, err := published.settleNow(t, audit.PublicationEndHeight-1)
	require.ErrorContains(t, err, "pending until")
	settlement, err := published.settleNow(t, audit.PublicationEndHeight)
	require.NoError(t, err)
	published.requireAuditedPaid(t, settlement)
	audit = published.reload(t)
	require.Equal(t, votePass, audit.TaskVerdict)
	require.Equal(t, audit.PublicationEndHeight, audit.FinalHeight)
	published.requireNoPanelWork(t)

	stalled := newAnchorPanelFixture(t, 4_000, 6)
	audit = stalled.reload(t)
	_, err = stalled.settleNow(t, audit.VerdictDeadlineHeight-1)
	require.ErrorContains(t, err, "pending until")
	settlement, err = stalled.settleNow(t, audit.VerdictDeadlineHeight)
	require.NoError(t, err)
	stalled.requirePaidInFull(t, settlement)
	require.Empty(t, *stalled.faults)
	audit = stalled.reload(t)
	require.Equal(t, noVerdict, audit.TaskVerdict)
	require.Equal(t, types.AnchorResultSourceV1_ANCHOR_RESULT_SOURCE_V1_TIMEOUT, audit.ResultSource)
	stalled.requireNoPanelWork(t)
	// A sweep arriving afterwards finds nothing to do.
	require.Zero(t, stalled.sweep(t, audit.VerdictDeadlineHeight+1))
}

// The frozen verdict deadline of a panel audit covers the whole schedule: two
// rounds, the publication window and a re-audit.
func TestPanelVerdictDeadlineCoversTheSchedule(t *testing.T) {
	f := newAnchorPanelFixtureBeforeProof(t, 4_000, 6)
	span, ok := anchorPanelSelectionSpanBlocks(f.params)
	require.True(t, ok)
	require.Equal(t, uint64(1+1+1+1)+panelVoteWindow+1, span)
	want := f.audit.ProofDeadlineHeight + f.params.Verification.AnchorVerdictWindowBlocks + 3*span + panelPublication
	require.Equal(t, want, f.audit.VerdictDeadlineHeight)
}

// The Worker's own omissions stay what they are in every audit mode: no
// opening or no proof by the deadline is the timeout fault.
func TestPanelModeWorkerOmissionIsStillATimeout(t *testing.T) {
	const bond = uint64(4_000)
	f := newAnchorPanelFixtureBeforeProof(t, bond, 6)
	settlement, err := f.settleNow(t, f.audit.VerdictDeadlineHeight)
	require.NoError(t, err)
	f.requireWorkerFaulted(t, settlement, "anchor_timeout", f.params.Verification.AnchorTimeoutSlashBps, bond)
	require.Empty(t, f.reload(t).PanelRounds)
}

// An import restores the derived rows of an audit in flight. A round that was
// still choosing its members has no working row after the import and is
// closed by the first sweep with no conclusion.
func TestPanelIndexesAreRebuiltOnImport(t *testing.T) {
	assigned := newAnchorPanelFixture(t, 4_000, 7)
	round := assigned.assign(t)
	audit := assigned.reload(t)
	require.NoError(t, assigned.keeper.retireAnchorPanelWork(assigned.ctx, assigned.taskKey, audit))
	require.NoError(t, assigned.keeper.rebuildAnchorPanelIndexes(assigned.at(500), assigned.taskKey, audit))
	query := &queryServer{k: assigned.keeper}
	list, err := query.AnchorPanelAssignments(assigned.ctx, &types.QueryAnchorPanelAssignmentsRequest{VerifierOperatorAddress: round.Members[0]})
	require.NoError(t, err)
	require.Len(t, list.Assignments, 1)
	require.Equal(t, uint64(500), assigned.dueHeight(t))

	choosing := newAnchorPanelFixture(t, 4_000, 7)
	audit = choosing.reload(t)
	require.NoError(t, choosing.keeper.retireAnchorPanelWork(choosing.ctx, choosing.taskKey, audit))
	require.NoError(t, choosing.keeper.rebuildAnchorPanelIndexes(choosing.at(500), choosing.taskKey, audit))
	choosing.sweep(t, 500)
	audit = choosing.reload(t)
	require.Equal(t, noVerdict, audit.PanelRounds[0].Result)
	require.Len(t, audit.PanelRounds, 2)

	published := newAnchorPanelFixture(t, 4_000, 7)
	audit = published.publish(t, votePass)
	require.NoError(t, published.keeper.retireAnchorPanelWork(published.ctx, published.taskKey, audit))
	require.NoError(t, published.keeper.rebuildAnchorPanelIndexes(published.ctx, published.taskKey, audit))
	under, err := (&queryServer{k: published.keeper}).AnchorAuditsUnderReview(published.ctx, nil)
	require.NoError(t, err)
	require.Len(t, under.Audits, 1)

	// A final audit and a validator-vote audit get nothing.
	final := audit
	final.ResultHeight = 9
	require.NoError(t, published.keeper.retireAnchorPanelWork(published.ctx, published.taskKey, audit))
	require.NoError(t, published.keeper.rebuildAnchorPanelIndexes(published.ctx, published.taskKey, final))
	has, err := published.keeper.anchorPanelHasWork(published.ctx, published.taskKey, final)
	require.NoError(t, err)
	require.False(t, has)
}

// The two queries: a member sees its open assignment with what it needs to
// vote, and a published verdict is listed until it is final.
func TestPanelQueries(t *testing.T) {
	f := newAnchorPanelFixture(t, 4_000, 6)
	query := &queryServer{k: f.keeper}
	round := f.assign(t)
	for _, member := range round.Members {
		list, err := query.AnchorPanelAssignments(f.ctx, &types.QueryAnchorPanelAssignmentsRequest{VerifierOperatorAddress: member})
		require.NoError(t, err)
		require.Len(t, list.Assignments, 1)
		got := list.Assignments[0]
		require.Equal(t, f.taskID, got.TaskId)
		require.Equal(t, round.PanelRound, got.PanelRound)
		require.Equal(t, round.VoteDeadlineHeight, got.VoteDeadlineHeight)
		require.Equal(t, f.audit.Positions, got.Audit.Positions)
		require.Equal(t, f.audit.WorkerProofHash, got.Audit.WorkerProofHash)
		require.Len(t, got.ModelId, types.Hash32Len)
		require.Equal(t, uint32(1), got.ProfileVersion)
	}
	none, err := query.AnchorPanelAssignments(f.ctx, &types.QueryAnchorPanelAssignmentsRequest{VerifierOperatorAddress: f.worker})
	require.NoError(t, err)
	require.Empty(t, none.Assignments)
	_, err = query.AnchorPanelAssignments(f.ctx, &types.QueryAnchorPanelAssignmentsRequest{VerifierOperatorAddress: "x"})
	require.Error(t, err)
	under, err := query.AnchorAuditsUnderReview(f.ctx, &types.QueryAnchorAuditsUnderReviewRequest{})
	require.NoError(t, err)
	require.Empty(t, under.Audits)

	f.castVotes(t, round, round.AssignmentHeight+1, votePass, votePass)
	list, err := query.AnchorPanelAssignments(f.ctx, &types.QueryAnchorPanelAssignmentsRequest{VerifierOperatorAddress: round.Members[2]})
	require.NoError(t, err)
	require.Empty(t, list.Assignments, "a closed round is no assignment")
	under, err = query.AnchorAuditsUnderReview(f.ctx, &types.QueryAnchorAuditsUnderReviewRequest{})
	require.NoError(t, err)
	require.Len(t, under.Audits, 1)
	require.Equal(t, votePass, under.Audits[0].ProvisionalVerdict)
	audit := f.reload(t)
	f.sweep(t, audit.PublicationEndHeight)
	under, err = query.AnchorAuditsUnderReview(f.ctx, &types.QueryAnchorAuditsUnderReviewRequest{})
	require.NoError(t, err)
	require.Empty(t, under.Audits)
}

// Evidence cleanup leaves nothing of a panel audit behind.
func TestPanelAuditIsCleanedUp(t *testing.T) {
	f := newAnchorPanelFixture(t, 4_000, 6)
	audit := f.publish(t, votePass)
	_, err := f.settleNow(t, audit.PublicationEndHeight)
	require.NoError(t, err)
	// Working rows that outlived the verdict for any reason are retired by cleanup too.
	require.NoError(t, f.keeper.setAnchorPanelDue(f.ctx, f.taskKey, 7))
	core := mustCore(t, f.anchorOnlyFixture)
	cleanupAt := core.GetTaskFinalityHeight() + core.EvidenceRetentionBlocksSnapshot
	for i := 0; i < 200; i++ {
		processed, err := f.keeper.processEvidenceCleanupEndBlock(f.at(int64(cleanupAt)), cleanupAt, 16)
		require.NoError(t, err)
		if processed == 0 {
			break
		}
	}
	_, err = f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.Error(t, err)
	_, err = f.keeper.AnchorPanelDueHeight.Get(f.ctx, f.taskKey)
	require.Error(t, err)
	iter, err := f.keeper.AnchorPanelDueIndex.Iterate(f.ctx, nil)
	require.NoError(t, err)
	defer iter.Close()
	require.False(t, iter.Valid())
}

// The panel queue is part of the EndBlock schedule and its work is counted.
func TestPanelQueueRunsInEndBlock(t *testing.T) {
	f := newAnchorPanelFixture(t, 4_000, 6)
	due := f.dueHeight(t)
	result, err := f.keeper.RunEndBlockDeadlineSweeps(f.at(int64(due)), due, 10)
	require.NoError(t, err)
	require.Equal(t, uint64(1), result.AnchorPanel)
	require.Equal(t, types.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY, f.selection(t).Window.Status)
}

func TestAnchorPanelParamsValidation(t *testing.T) {
	base := types.DefaultTaskParams()
	require.Zero(t, base.Verification.AnchorAuditMode)
	require.NoError(t, base.Validate(), "the defaults carry no panel")
	mutate := func(change func(*types.VerificationParamsV1)) error {
		params := types.DefaultTaskParams()
		params.Verification.AnchorAuditMode = types.AnchorAuditModePanel
		params.Verification.AnchorPanelSize, params.Verification.AnchorPanelThreshold = 3, 2
		params.Verification.AnchorPanelVoteWindowBlocks = 20
		change(&params.Verification)
		return params.Validate()
	}
	require.NoError(t, mutate(func(*types.VerificationParamsV1) {}))
	require.NoError(t, mutate(func(v *types.VerificationParamsV1) { v.AnchorPanelSize, v.AnchorPanelThreshold = 1, 1 }))
	require.NoError(t, mutate(func(v *types.VerificationParamsV1) { v.AnchorPanelThreshold = 3 }))
	for name, change := range map[string]func(*types.VerificationParamsV1){
		"unknown mode":       func(v *types.VerificationParamsV1) { v.AnchorAuditMode = 3 },
		"no panel":           func(v *types.VerificationParamsV1) { v.AnchorPanelSize = 0 },
		"oversized panel":    func(v *types.VerificationParamsV1) { v.AnchorPanelSize, v.AnchorPanelThreshold = 16, 9 },
		"half is not enough": func(v *types.VerificationParamsV1) { v.AnchorPanelSize, v.AnchorPanelThreshold = 4, 2 },
		"threshold too low":  func(v *types.VerificationParamsV1) { v.AnchorPanelThreshold = 1 },
		"threshold too high": func(v *types.VerificationParamsV1) { v.AnchorPanelThreshold = 4 },
		"no vote window":     func(v *types.VerificationParamsV1) { v.AnchorPanelVoteWindowBlocks = 0 },
		"huge publication": func(v *types.VerificationParamsV1) {
			v.AnchorPublicationWindowBlocks = types.AnchorWindowBlocksMaximum + 1
		},
		"bps over 100%": func(v *types.VerificationParamsV1) { v.AnchorVerifierWrongVoteSlashBps = 10_001 },
	} {
		require.Error(t, mutate(change), name)
	}
	// Outside panel mode the panel's own shape is not judged.
	off := types.DefaultTaskParams()
	off.Verification.AnchorAuditMode = 1
	off.Verification.AnchorPanelSize, off.Verification.AnchorPanelThreshold = 4, 1
	require.NoError(t, off.Validate())
}

// The candidate window of a panel round is read through the same query as a
// verification round's, with verify_round set to the panel round number: not
// materialized before the window beacon, the ranked eligible members after it,
// and gone once the panel is drawn.
func TestPanelCandidateWindowIsQueryable(t *testing.T) {
	f := newAnchorPanelFixture(t, 4_000, 6)
	query := &queryServer{k: f.keeper}
	request := &types.QueryVerifierCandidateWindowRequest{TaskId: f.taskID, VerifyRound: f.audit.PanelRound}
	_, err := query.VerifierCandidateWindow(f.ctx, request)
	require.ErrorContains(t, err, "not materialized")

	selection := f.selection(t)
	f.sweep(t, selection.Window.WindowRandomnessHeight)
	selection = f.selection(t)
	response, err := query.VerifierCandidateWindow(f.ctx, request)
	require.NoError(t, err)
	require.Equal(t, selection.Window, response.Window)
	require.Equal(t, f.audit.PanelRound, response.Window.VerifyRound)
	require.Len(t, response.Members, int(selection.Window.WindowSize))
	for rank, member := range response.Members {
		require.Equal(t, uint32(rank), member.RankIndex, "members are listed in rank order")
		require.Contains(t, f.candidates, member.OperatorAddress)
		require.NotEqual(t, f.worker, member.OperatorAddress)
	}
	// A round number the audit has not opened falls through to the
	// verification rounds, which know no round after the second.
	_, err = query.VerifierCandidateWindow(f.ctx, &types.QueryVerifierCandidateWindowRequest{TaskId: f.taskID, VerifyRound: f.audit.PanelRound + 1})
	require.ErrorContains(t, err, "verify_round must be 1 or 2")
	// Round 1 of the task is still its own zero-Verifier round.
	first, err := query.VerifierCandidateWindow(f.ctx, &types.QueryVerifierCandidateWindowRequest{TaskId: f.taskID, VerifyRound: types.VerifyRoundV1})
	require.NoError(t, err)
	require.True(t, isAnchorOnlyWindow(first.Window))

	_, err = f.server.SubmitVerifierHandraises(f.at(int64(selection.Window.HandraiseCloseHeight)), &types.MsgSubmitVerifierHandraises{
		TaskId: f.taskID, Handraises: f.handraises(t, selection), SubmitterAddress: f.builders[0],
	})
	require.NoError(t, err)
	f.sweep(t, selection.Window.SelectionRandomnessHeight)
	require.Len(t, f.round(t).Members, 3)
	_, err = query.VerifierCandidateWindow(f.ctx, request)
	require.Error(t, err, "a drawn round has no window left; its members are in the audit row")
}

// A task in flight when a network switches to panel audit keeps the mode it
// was accepted under. An audit sampled before the switch stays on the
// validator vote: its proof opens no panel round, panel messages are refused
// for it, validator votes decide it and the Worker is paid as before.
func TestAuditInFlightAtTheSwitchStaysOnTheValidatorVote(t *testing.T) {
	f := newAnchorOnlyFixture(t, 4_000, true)
	f.submitReceipt(t)
	state := f.sampleRound(t)
	require.True(t, state.Selected)
	require.Zero(t, state.AuditMode)

	switched := f.params
	switched.Verification.AnchorAuditMode = types.AnchorAuditModePanel
	switched.Verification.AnchorPanelSize, switched.Verification.AnchorPanelThreshold = 3, 2
	switched.Verification.AnchorPanelVoteWindowBlocks, switched.Verification.AnchorPublicationWindowBlocks = panelVoteWindow, panelPublication
	require.NoError(t, switched.Validate())
	require.NoError(t, f.keeper.Params.Set(f.ctx, switched))

	f.openAndProveSampled(t, state)
	got, err := f.keeper.AnchorAudit.Get(f.ctx, f.taskKey)
	require.NoError(t, err)
	require.Zero(t, got.AuditMode)
	require.Empty(t, got.PanelRounds, "an audit sampled before the switch opens no panel round")
	require.Equal(t, state.VerdictDeadlineHeight, got.VerdictDeadlineHeight, "its deadlines are the ones frozen at sampling")
	has, err := f.keeper.anchorPanelHasWork(f.ctx, f.taskKey, got)
	require.NoError(t, err)
	require.False(t, has)
	voteAt := state.SampleHeight + 3
	_, err = f.server.SubmitAnchorPanelVote(f.at(int64(voteAt)), &types.MsgSubmitAnchorPanelVote{
		TaskId: f.taskID, PanelRound: 2, OpeningHash: got.OpeningHash, ProofHash: got.WorkerProofHash, Verdict: voteUnavailable,
		VerifierOperatorAddress: f.operators[1], SubmitterAddress: f.operators[1],
	})
	require.ErrorContains(t, err, "not decided by a Verifier panel")
	visited, err := f.keeper.processAnchorPanelDue(f.at(int64(state.VerdictDeadlineHeight)), state.VerdictDeadlineHeight, 10)
	require.NoError(t, err)
	require.Zero(t, visited)

	_, err = f.vote(0, f.valAcct[0], votePass, voteAt)
	require.NoError(t, err)
	response, err := f.vote(1, f.valAcct[1], votePass, voteAt)
	require.NoError(t, err)
	require.True(t, response.Finalized)
	settlement, err := f.settleNow(t, voteAt+1)
	require.NoError(t, err)
	f.requirePaidInFull(t, settlement)
	require.Empty(t, *f.faults)
}

// The same holds when nobody votes on the in-flight audit after the switch:
// it ends at its own verdict deadline without a verdict, the Worker is paid
// and nobody is slashed.
func TestAuditInFlightAtTheSwitchIsNotFaultedBySilence(t *testing.T) {
	f := newAnchorOnlyFixture(t, 4_000, true)
	f.submitReceipt(t)
	state := f.sampleRound(t)
	switched := f.params
	switched.Verification.AnchorAuditMode = types.AnchorAuditModePanel
	switched.Verification.AnchorPanelSize, switched.Verification.AnchorPanelThreshold = 3, 2
	switched.Verification.AnchorPanelVoteWindowBlocks, switched.Verification.AnchorPublicationWindowBlocks = panelVoteWindow, panelPublication
	require.NoError(t, f.keeper.Params.Set(f.ctx, switched))
	f.openAndProveSampled(t, state)
	settlement, err := f.settleNow(t, state.VerdictDeadlineHeight)
	require.NoError(t, err)
	f.requirePaidInFull(t, settlement)
	require.Empty(t, *f.faults)
}
