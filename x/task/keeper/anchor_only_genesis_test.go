package keeper_test

import (
	"testing"

	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
	tasktypes "github.com/TrueOpen/node/x/task/types"
)

// anchorOnlyPendingGenesisV1 is a genesis holding one anchor-only task between
// its receipt and its sampling height: a READY window with no members and the
// receipt that committed the anchor.
func anchorOnlyPendingGenesisV1(t *testing.T, chainID string) *tasktypes.GenesisState {
	t.Helper()
	genesis := verifierSourceGenesisV1(t, chainID)
	genesis.TaskCores[0].VerificationStatus = tasktypes.VerificationStatus_VERIFICATION_STATUS_VERIFIER_SELECTION_PENDING
	commitments := []tasktypes.EvidenceCommitmentV1{
		{EvidenceKind: shared.EvidenceKind_EVIDENCE_KIND_WORKER_VALUE_OPENING, EvidenceHashOrRoot: repeatByte(0x41), EncodedSizeBytes: 100},
		{EvidenceKind: shared.EvidenceKind_EVIDENCE_KIND_WORKER_TOKEN_OPENING, EvidenceHashOrRoot: repeatByte(0x42), EncodedSizeBytes: 8 + 4*6},
		{EvidenceKind: shared.EvidenceKind_EVIDENCE_KIND_WORKER_ANCHOR_OPENING, EvidenceHashOrRoot: repeatByte(0x43), EncodedSizeBytes: 64},
	}
	evidenceHash, err := tasktypes.EvidenceCommitmentsHash(commitments)
	require.NoError(t, err)
	genesis.InferReceipts[0].RequiredEvidenceCommitments = commitments
	genesis.InferReceipts[0].EvidenceCommitmentCount = uint32(len(commitments))
	genesis.InferReceipts[0].EvidenceCommitmentsHash = evidenceHash[:]
	window := &genesis.VerifierCandidateWindows[0]
	window.EligibleCount, window.EligibilitySegmentCount, window.WindowSize = 0, 0, 0
	window.VerifierWindowSourceHash = make([]byte, tasktypes.Hash32Len)
	window.Status = tasktypes.VerifierCandidateWindowStatusV1_VERIFIER_CANDIDATE_WINDOW_STATUS_V1_READY
	window.GeneratedHeight = window.EligibilityFrozenHeight
	genesis.VerifierCandidateEligibilitySegments = nil
	return genesis
}

// An export taken while an anchor-only task waits for its sampling height
// imports back with the sampling still scheduled, and never as a Verifier
// window to build.
func TestAnchorOnlyPendingTaskSurvivesGenesisRoundTrip(t *testing.T) {
	f := initFixture(t)
	input := anchorOnlyPendingGenesisV1(t, genesisChainID(f))
	require.NoError(t, input.Validate())
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *input))
	window := input.VerifierCandidateWindows[0]
	taskKey := tasktypes.NewTaskKey(window.TaskId)

	scheduled, err := f.keeper.VerifierSelectionRandomnessIndex.Has(f.ctx,
		tasktypes.NewVerifyRoundIndexKey(window.SelectionRandomnessHeight, taskKey, window.VerifyRound))
	require.NoError(t, err)
	require.True(t, scheduled, "sampling must still be scheduled at the selection randomness height")
	fallback, err := f.keeper.VerifyOpenDeadlineIndex.Has(f.ctx, tasktypes.NewDeadlineIndexKey(window.AssignmentDeadlineHeight, taskKey))
	require.NoError(t, err)
	require.True(t, fallback)
	build, err := f.keeper.VerifierWindowBuildIndex.Has(f.ctx,
		tasktypes.NewVerifyRoundIndexKey(window.WindowRandomnessHeight, taskKey, window.VerifyRound))
	require.NoError(t, err)
	require.False(t, build, "an anchor-only window is never rebuilt as a Verifier window")
	closeIndexed, err := f.keeper.VerifierHandraiseCloseIndex.Has(f.ctx,
		tasktypes.NewVerifyRoundIndexKey(window.HandraiseCloseHeight, taskKey, window.VerifyRound))
	require.NoError(t, err)
	require.False(t, closeIndexed)

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(input, exported))
	restarted := initFixture(t)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	again, err := restarted.keeper.ExportGenesis(restarted.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(exported, again))
}

// anchorOnlyHeldGenesisV1 is the genesis of a sampled, selected anchor-only
// task whose round 1 is closed and whose settlement is held for the anchor
// verdict. It returns the genesis and the audit's verdict deadline.
func anchorOnlyHeldGenesisV1(t *testing.T, chainID string) (*tasktypes.GenesisState, uint64) {
	t.Helper()
	input := anchorOnlyPendingGenesisV1(t, chainID)
	core := &input.TaskCores[0]
	window := input.VerifierCandidateWindows[0]
	sampledAt := window.SelectionRandomnessHeight
	zero := make([]byte, tasktypes.Hash32Len)
	input.VerifierAssignments = []tasktypes.VerifierAssignmentState{{
		TaskId: core.TaskId, VerifyRound: tasktypes.VerifyRoundV1, OpenVerifyHeight: sampledAt,
		VerifierCandidateWindowHash: zero, VerifierLegalSetHash: zero, SelectedVerifiersHash: zero,
		SelectionRandomnessHeight: sampledAt, SelectionRandomnessBeacon: repeatByte(0x51),
		CommitDeadlineHeight: sampledAt + 1, VerifyDeadlineHeight: sampledAt + 2,
	}}
	if len(input.TaskAssignments[0].ProfileExecutionSnapshotHash) != tasktypes.Hash32Len {
		input.TaskAssignments[0].ProfileExecutionSnapshotHash = repeatByte(0x52)
	}
	roundID, err := tasktypes.VerifyRoundID(chainID, core.AcceptedTaskHash, tasktypes.VerifyRoundV1)
	require.NoError(t, err)
	round := tasktypes.VerificationRoundState{
		TaskId: core.TaskId, TaskHash: core.AcceptedTaskHash, VerifyRound: tasktypes.VerifyRoundV1, RoundId: roundID[:],
		RoundOpenHeight: sampledAt, RoundCloseDeadlineHeight: sampledAt + 2, InferReceiptRef: window.InferReceiptHash,
		ProfileExecutionSnapshotHash: input.TaskAssignments[0].ProfileExecutionSnapshotHash,
		GenerationParamsDigest:       input.TaskAssignments[0].GenerationParamsDigest,
		GeneratedTokenCount:          input.InferReceipts[0].GeneratedTokenCount,
		XClosedHeight:                &tasktypes.VerificationRoundState_ClosedHeight{ClosedHeight: sampledAt},
		XResultReceiptRefsHash:       &tasktypes.VerificationRoundState_ResultReceiptRefsHash{ResultReceiptRefsHash: zero},
		XConsensusClusterHash:        &tasktypes.VerificationRoundState_ConsensusClusterHash{ConsensusClusterHash: zero},
		XVerdict:                     &tasktypes.VerificationRoundState_Verdict{Verdict: tasktypes.TaskVerdict_TASK_VERDICT_PASS},
		XFailureClass:                &tasktypes.VerificationRoundState_FailureClass{FailureClass: tasktypes.TaskFailureClass_TASK_FAILURE_CLASS_NONE},
	}
	factsHash, err := tasktypes.VerifyRoundFactsHash(chainID, round)
	require.NoError(t, err)
	round.XRoundFactsHash = &tasktypes.VerificationRoundState_RoundFactsHash{RoundFactsHash: factsHash[:]}
	input.VerificationRounds = []tasktypes.VerificationRoundState{round}
	summary := tasktypes.TaskRoundSummaryState{
		TaskId: core.TaskId, MaxClosedRound: tasktypes.VerifyRoundV1, EffectiveVerifyRound: tasktypes.VerifyRoundV1,
		Round1FactsHashOrZero32: factsHash[:], Round2FactsHashOrZero32: zero, Round2EffectRootOrZero32: zero,
		XRoundsClosedHeight:          &tasktypes.TaskRoundSummaryState_RoundsClosedHeight{RoundsClosedHeight: sampledAt},
		XSettlementFactsCutoffHeight: &tasktypes.TaskRoundSummaryState_SettlementFactsCutoffHeight{SettlementFactsCutoffHeight: sampledAt},
	}
	summaryHash, err := tasktypes.TaskRoundSummaryHash(chainID, summary)
	require.NoError(t, err)
	summary.XTaskRoundSummaryHash = &tasktypes.TaskRoundSummaryState_TaskRoundSummaryHash{TaskRoundSummaryHash: summaryHash[:]}
	input.TaskRoundSummaries = []tasktypes.TaskRoundSummaryState{summary}
	verdictDeadline := sampledAt + 30_000
	input.AnchorAudits = []tasktypes.AnchorAuditState{{
		TaskId: core.TaskId, Sampled: true, Selected: true, AuditRateBps: 10_000, AuditBasis: "test",
		SampleBeacon: repeatByte(0x51), SampleHeight: sampledAt, WinnerWorker: genesisWorker, Positions: []uint64{1, 3},
		OpeningDeadlineHeight: sampledAt + 1_800, ProofDeadlineHeight: sampledAt + 21_800, VerdictDeadlineHeight: verdictDeadline,
	}}
	core.TaskPhase = tasktypes.TaskPhase_TASK_PHASE_SETTLING
	core.VerificationStatus = tasktypes.VerificationStatus_VERIFICATION_STATUS_VERIFICATION_PASSED
	core.EffectiveVerifyRound = tasktypes.VerifyRoundV1
	return input, verdictDeadline
}

// An export taken while a sampled anchor-only task is held for its verdict
// imports back with the zero-Verifier round and the settlement hold in place.
func TestAnchorOnlyHeldTaskSurvivesGenesisRoundTrip(t *testing.T) {
	f := initFixture(t)
	input, verdictDeadline := anchorOnlyHeldGenesisV1(t, genesisChainID(f))
	core := &input.TaskCores[0]
	window := input.VerifierCandidateWindows[0]
	sampledAt := window.SelectionRandomnessHeight
	require.NoError(t, input.Validate())
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *input))

	taskKey := tasktypes.NewTaskKey(core.TaskId)
	held, err := f.keeper.SettlementDeadlineIndex.Has(f.ctx, tasktypes.NewDeadlineIndexKey(verdictDeadline+1, taskKey))
	require.NoError(t, err)
	require.True(t, held, "the settlement hold must be rebuilt past the verdict deadline")
	for name, has := range map[string]func() (bool, error){
		"selection": func() (bool, error) {
			return f.keeper.VerifierSelectionRandomnessIndex.Has(f.ctx, tasktypes.NewVerifyRoundIndexKey(sampledAt, taskKey, tasktypes.VerifyRoundV1))
		},
		"verify-open": func() (bool, error) {
			return f.keeper.VerifyOpenDeadlineIndex.Has(f.ctx, tasktypes.NewDeadlineIndexKey(window.AssignmentDeadlineHeight, taskKey))
		},
		"commit": func() (bool, error) {
			return f.keeper.CommitDeadlineIndex.Has(f.ctx, tasktypes.NewDeadlineIndexKey(sampledAt+1, taskKey))
		},
		"verify": func() (bool, error) {
			return f.keeper.VerifyDeadlineIndex.Has(f.ctx, tasktypes.NewDeadlineIndexKey(sampledAt+2, taskKey))
		},
	} {
		found, err := has()
		require.NoError(t, err)
		require.False(t, found, "a closed anchor-only round must not rebuild a %s deadline", name)
	}
	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(input, exported))
}

// A two-step audit survives a full export and import in each waiting stage:
// the new fields travel inside anchor_audits, the settlement hold is rebuilt on
// the verdict deadline although the audit has no positions yet, and the
// sampling queue row is rebuilt from the row.
func TestTwoStepAuditSurvivesGenesisRoundTrip(t *testing.T) {
	for name, stage := range map[string]func(audit *tasktypes.AnchorAuditState, sampledAt uint64) uint64{
		"awaiting the list": func(audit *tasktypes.AnchorAuditState, sampledAt uint64) uint64 {
			return audit.ListDeadlineHeight + 1
		},
		"awaiting the draw": func(audit *tasktypes.AnchorAuditState, sampledAt uint64) uint64 {
			audit.CommitmentListHash, audit.CommitmentListSizeBytes = repeatByte(0x1a), 32*5
			audit.CommitmentListUri, audit.CommitmentListHeight = "https://da.example/list", sampledAt+3
			audit.PositionBeaconFirstHeight, audit.PositionBeaconLastHeight = sampledAt+4, sampledAt+11
			return audit.PositionBeaconLastHeight
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := initFixture(t)
			input, verdictDeadline := anchorOnlyHeldGenesisV1(t, genesisChainID(f))
			sampledAt := input.VerifierCandidateWindows[0].SelectionRandomnessHeight
			audit := &input.AnchorAudits[0]
			audit.SamplingMode, audit.Positions = 1, nil
			audit.OpeningDeadlineHeight, audit.ProofDeadlineHeight = 0, 0
			audit.ListDeadlineHeight = sampledAt + 1_800
			wantDue := stage(audit, sampledAt)
			require.NoError(t, input.Validate())
			require.NoError(t, f.keeper.InitGenesis(f.ctx, *input))

			taskKey := tasktypes.NewTaskKey(input.TaskCores[0].TaskId)
			held, err := f.keeper.SettlementDeadlineIndex.Has(f.ctx, tasktypes.NewDeadlineIndexKey(verdictDeadline+1, taskKey))
			require.NoError(t, err)
			require.True(t, held, "a two-step audit without positions still holds settlement after an import")
			queued, err := f.keeper.AnchorSamplingDueIndex.Has(f.ctx, tasktypes.NewDeadlineIndexKey(wantDue, taskKey))
			require.NoError(t, err)
			require.True(t, queued, "the sampling queue row is rebuilt")
			due, err := f.keeper.AnchorSamplingDueHeight.Get(f.ctx, taskKey)
			require.NoError(t, err)
			require.Equal(t, wantDue, due)

			exported, err := f.keeper.ExportGenesis(f.ctx)
			require.NoError(t, err)
			require.True(t, proto.Equal(input, exported), "the queue is derived state and is not exported")
			restarted := initFixture(t)
			require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
			again, err := restarted.keeper.ExportGenesis(restarted.ctx)
			require.NoError(t, err)
			require.True(t, proto.Equal(exported, again))
		})
	}
}
