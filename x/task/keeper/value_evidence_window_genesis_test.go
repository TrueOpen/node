package keeper_test

import (
	"testing"

	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"

	tasktypes "github.com/TrueOpen/node/x/task/types"
)

const (
	earlyCloseRevealDeadline = uint64(35)
	earlyCloseHeight         = uint64(32)
)

// earlyClosedRoundGenesisV1 is a genesis holding one Task whose round 1 entered
// revealing with a reveal deadline of 35 and closed at 32, before it, with its
// value-evidence window still open and cut at the close, the shape the chain
// writes now. Tests move the window close to the other shapes.
func earlyClosedRoundGenesisV1(t *testing.T, chainID string) *tasktypes.GenesisState {
	t.Helper()
	genesis := verifierReadyGenesisV1(t, chainID)
	core := &genesis.TaskCores[0]
	assignment := &genesis.VerifierAssignments[0]
	assignment.RevealDeadlineHeight = earlyCloseRevealDeadline
	zero := make([]byte, tasktypes.Hash32Len)
	if len(genesis.TaskAssignments[0].ProfileExecutionSnapshotHash) != tasktypes.Hash32Len {
		genesis.TaskAssignments[0].ProfileExecutionSnapshotHash = repeatByte(0x52)
	}
	roundID, err := tasktypes.VerifyRoundID(chainID, core.AcceptedTaskHash, tasktypes.VerifyRoundV1)
	require.NoError(t, err)
	round := tasktypes.VerificationRoundState{
		TaskId: core.TaskId, TaskHash: core.AcceptedTaskHash, VerifyRound: tasktypes.VerifyRoundV1, RoundId: roundID[:],
		RoundOpenHeight: assignment.OpenVerifyHeight, RoundCloseDeadlineHeight: assignment.VerifyDeadlineHeight,
		InferReceiptRef:              genesis.InferReceipts[0].InferReceiptHash,
		ProfileExecutionSnapshotHash: genesis.TaskAssignments[0].ProfileExecutionSnapshotHash,
		GenerationParamsDigest:       genesis.TaskAssignments[0].GenerationParamsDigest,
		GeneratedTokenCount:          genesis.InferReceipts[0].GeneratedTokenCount,
		XClosedHeight:                &tasktypes.VerificationRoundState_ClosedHeight{ClosedHeight: earlyCloseHeight},
		XResultReceiptRefsHash:       &tasktypes.VerificationRoundState_ResultReceiptRefsHash{ResultReceiptRefsHash: zero},
		XConsensusClusterHash:        &tasktypes.VerificationRoundState_ConsensusClusterHash{ConsensusClusterHash: zero},
		XVerdict:                     &tasktypes.VerificationRoundState_Verdict{Verdict: tasktypes.TaskVerdict_TASK_VERDICT_NO_CONSENSUS},
		XFailureClass:                &tasktypes.VerificationRoundState_FailureClass{FailureClass: tasktypes.TaskFailureClass_TASK_FAILURE_CLASS_OBJECTIVE_FAULT},
	}
	factsHash, err := tasktypes.VerifyRoundFactsHash(chainID, round)
	require.NoError(t, err)
	round.XRoundFactsHash = &tasktypes.VerificationRoundState_RoundFactsHash{RoundFactsHash: factsHash[:]}
	genesis.VerificationRounds = []tasktypes.VerificationRoundState{round}
	genesis.TaskRoundSummaries = []tasktypes.TaskRoundSummaryState{{
		TaskId: core.TaskId, MaxClosedRound: tasktypes.VerifyRoundV1,
		Round1FactsHashOrZero32: factsHash[:], Round2FactsHashOrZero32: zero, Round2EffectRootOrZero32: zero,
		XVerifierValueEvidenceCloseHeight: &tasktypes.TaskRoundSummaryState_VerifierValueEvidenceCloseHeight{
			VerifierValueEvidenceCloseHeight: earlyCloseHeight + genesis.Params.Evidence.VerifierValueEvidenceWindowBlocks,
		},
	}}
	core.TaskPhase = tasktypes.TaskPhase_TASK_PHASE_REVEALING
	core.VerificationStatus = tasktypes.VerificationStatus_VERIFICATION_STATUS_NO_CONSENSUS
	return genesis
}

// A Task whose round closed before its reveal deadline carries a window cut at
// that close. Genesis import checks the window against the rounds, so a restart
// from an export must accept the cut window, rebuild its close index at the cut
// height, and export it unchanged.
func TestValueEvidenceCutWindowSurvivesGenesisRoundTrip(t *testing.T) {
	f := initFixture(t)
	input := earlyClosedRoundGenesisV1(t, genesisChainID(f))
	window := input.Params.Evidence.VerifierValueEvidenceWindowBlocks
	cut := earlyCloseHeight + window
	require.NoError(t, input.Validate())
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *input))

	taskKey := tasktypes.NewTaskKey(input.TaskCores[0].TaskId)
	indexed, err := f.keeper.ValueEvidenceWindowCloseIndex.Has(f.ctx, tasktypes.NewDeadlineIndexKey(cut, taskKey))
	require.NoError(t, err)
	require.True(t, indexed, "the close index must be rebuilt at the cut height")
	stale, err := f.keeper.ValueEvidenceWindowCloseIndex.Has(f.ctx, tasktypes.NewDeadlineIndexKey(earlyCloseRevealDeadline+window, taskKey))
	require.NoError(t, err)
	require.False(t, stale)

	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(input, exported))
	restarted := initFixture(t)
	require.NoError(t, restarted.keeper.InitGenesis(restarted.ctx, *exported))
	again, err := restarted.keeper.ExportGenesis(restarted.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(exported, again))
}

// A Task whose round closed early before the window could be cut keeps a window
// that ran to the reveal deadline. A chain upgraded with such Tasks in flight
// must still restart from an export.
func TestValueEvidenceLegacyWindowSurvivesGenesisRoundTrip(t *testing.T) {
	f := initFixture(t)
	input := earlyClosedRoundGenesisV1(t, genesisChainID(f))
	setValueWindowClose(input, earlyCloseRevealDeadline+input.Params.Evidence.VerifierValueEvidenceWindowBlocks)
	require.NoError(t, f.keeper.InitGenesis(f.ctx, *input))
	exported, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.True(t, proto.Equal(input, exported))
}

// A window that is neither the reveal deadline nor the early close plus the
// window length is corruption, and import must refuse it rather than start a
// chain whose Task would settle at an unexplained height.
func TestValueEvidenceWindowOffBothShapesIsRejectedAtGenesis(t *testing.T) {
	f := initFixture(t)
	window := earlyClosedRoundGenesisV1(t, genesisChainID(f)).Params.Evidence.VerifierValueEvidenceWindowBlocks
	for _, valueClose := range []uint64{
		earlyCloseHeight + window + 1,
		earlyCloseHeight + window - 1,
		earlyCloseRevealDeadline + window + 1,
	} {
		fresh := initFixture(t)
		input := earlyClosedRoundGenesisV1(t, genesisChainID(fresh))
		setValueWindowClose(input, valueClose)
		require.ErrorContains(t, fresh.keeper.InitGenesis(fresh.ctx, *input), "value window differs", "window close %d", valueClose)
	}
}

func setValueWindowClose(genesis *tasktypes.GenesisState, height uint64) {
	genesis.TaskRoundSummaries[0].XVerifierValueEvidenceCloseHeight = &tasktypes.TaskRoundSummaryState_VerifierValueEvidenceCloseHeight{
		VerifierValueEvidenceCloseHeight: height,
	}
}
