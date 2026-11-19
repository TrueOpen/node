package types

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The Default* anchor windows are historical values: they are read into params
// bodies recorded before the windows existed and are part of the frozen
// DefaultTaskParams body, so they must never move.
func TestAnchorDefaultWindowsAreFrozen(t *testing.T) {
	require.Equal(t, uint64(900), DefaultAnchorOpenWindowBlocks)
	require.Equal(t, uint64(6_600), DefaultAnchorProofWindowBlocks)
	require.Equal(t, uint64(900), DefaultAnchorVerdictWindowBlocks)
	v := DefaultTaskParams().Verification
	require.Equal(t, DefaultAnchorOpenWindowBlocks, v.AnchorOpenWindowBlocks)
	require.Equal(t, DefaultAnchorProofWindowBlocks, v.AnchorProofWindowBlocks)
	require.Equal(t, DefaultAnchorVerdictWindowBlocks, v.AnchorVerdictWindowBlocks)

	// A body recorded before the windows existed is still read with them.
	old := DefaultTaskParams()
	old.Verification.AnchorOpenWindowBlocks, old.Verification.AnchorProofWindowBlocks, old.Verification.AnchorVerdictWindowBlocks = 0, 0, 0
	old.Verification.AnchorAuditMinRateBps = 0
	require.NoError(t, ValidateHistoricalTaskParams(old))
	_, err := TaskParamsHashV1(taskParamsGoldenChainID, 1, old)
	require.NoError(t, err, "the commitment of a recorded pre-anchor body stays computable")
}

// The recommended windows are wall-clock times; in blocks they depend on the
// block time. The constants are stated for the reference block time.
func TestRecommendedAnchorWindowBlocksFollowBlockTime(t *testing.T) {
	require.Equal(t, 500*time.Millisecond, AnchorWindowBlockTime)
	open, proof, verdict := RecommendedAnchorWindowBlocks(AnchorWindowBlockTime)
	require.Equal(t, uint64(1_800), open)
	require.Equal(t, uint64(20_000), proof)
	require.Equal(t, uint64(7_200), verdict)
	require.Equal(t, RecommendedAnchorOpenWindowBlocks, open)
	require.Equal(t, RecommendedAnchorProofWindowBlocks, proof)
	require.Equal(t, RecommendedAnchorVerdictWindowBlocks, verdict)

	open, proof, verdict = RecommendedAnchorWindowBlocks(6 * time.Second)
	require.Equal(t, []uint64{150, 1_667, 600}, []uint64{open, proof, verdict}, "rounded up: never shorter than the time it stands for")
	open, proof, verdict = RecommendedAnchorWindowBlocks(time.Second)
	require.Equal(t, []uint64{900, 10_000, 3_600}, []uint64{open, proof, verdict})
	open, proof, verdict = RecommendedAnchorWindowBlocks(0)
	require.Equal(t, []uint64{1_800, 20_000, 7_200}, []uint64{open, proof, verdict}, "no block time reads as the reference")
	open, proof, verdict = RecommendedAnchorWindowBlocks(time.Nanosecond)
	require.Equal(t, []uint64{AnchorWindowBlocksMaximum, AnchorWindowBlocksMaximum, AnchorWindowBlocksMaximum}, []uint64{open, proof, verdict}, "capped at the validation bound")
}

// The content-track recommendations are wall-clock times too and convert the
// same way; the content track itself ships off.
func TestRecommendedAnchorContentWindowsFollowBlockTime(t *testing.T) {
	require.Equal(t, uint64(7_200), RecommendedAnchorContentVerdictWindowBlocks, "one hour at the 0.5 s reference")
	require.Equal(t, uint64(4_800), RecommendedAnchorContentOpenWindowBlocks, "40 minutes at the 0.5 s reference")
	require.Equal(t, uint64(3_600), AnchorContentOpenWindowBlocksMinimum, "30 minutes at the 0.5 s reference")
	require.Equal(t, RecommendedAnchorContentVerdictWindowBlocks, AnchorWindowBlocks(RecommendedAnchorContentVerdictWindowSeconds, AnchorWindowBlockTime))
	require.Equal(t, RecommendedAnchorContentOpenWindowBlocks, AnchorWindowBlocks(RecommendedAnchorContentOpenWindowSeconds, AnchorWindowBlockTime))
	require.Equal(t, uint64(600), AnchorWindowBlocks(RecommendedAnchorContentVerdictWindowSeconds, 6*time.Second))
	require.Zero(t, DefaultAnchorContentVerdictWindowBlocks)
	require.Zero(t, DefaultGenesis().Params.Verification.AnchorContentVerdictWindowBlocks)

	// A body without the content-track fields gets the defaults and keeps its windows.
	p := DefaultTaskParams()
	v := &p.Verification
	v.AnchorOpenWindowBlocks, v.AnchorProofWindowBlocks, v.AnchorVerdictWindowBlocks = 100, 200, 300
	v.AnchorPositionsPerTask, v.AnchorMinBondMultiple, v.AnchorContentVerdictWindowBlocks, v.AnchorContentRejectSlashBps = 0, 0, 0, 0
	filled := p.WithAnchorDefaults().Verification
	require.Equal(t, []uint64{100, 200, 300}, []uint64{filled.AnchorOpenWindowBlocks, filled.AnchorProofWindowBlocks, filled.AnchorVerdictWindowBlocks})
	require.Equal(t, DefaultAnchorPositionsPerTask, filled.AnchorPositionsPerTask)
	require.Zero(t, filled.AnchorContentVerdictWindowBlocks)
}

// A new chain starts on the recommended windows; the frozen default body is not
// what DefaultGenesis hands out.
func TestDefaultGenesisCarriesRecommendedAnchorWindows(t *testing.T) {
	gs := DefaultGenesis()
	require.NoError(t, gs.Validate())
	v := gs.Params.Verification
	require.Equal(t, RecommendedAnchorOpenWindowBlocks, v.AnchorOpenWindowBlocks)
	require.Equal(t, RecommendedAnchorProofWindowBlocks, v.AnchorProofWindowBlocks)
	require.Equal(t, RecommendedAnchorVerdictWindowBlocks, v.AnchorVerdictWindowBlocks)
	want := DefaultTaskParams()
	want.Verification.AnchorOpenWindowBlocks = RecommendedAnchorOpenWindowBlocks
	want.Verification.AnchorProofWindowBlocks, want.Verification.AnchorVerdictWindowBlocks = RecommendedAnchorProofWindowBlocks, RecommendedAnchorVerdictWindowBlocks
	require.Equal(t, want, gs.Params, "nothing but the anchor windows differs from the default body")
}

// Windows a chain already carries are governance's choice and are never
// replaced by the fill.
func TestWithAnchorDefaultsKeepsChosenWindows(t *testing.T) {
	chosen := DefaultTaskParams()
	v := &chosen.Verification
	v.AnchorOpenWindowBlocks, v.AnchorProofWindowBlocks, v.AnchorVerdictWindowBlocks = 100, 200, 300
	v.AnchorAuditMinRateBps, v.AnchorAuditRiskMarginBps = 7, 123
	v.AnchorRejectSlashBps, v.AnchorTimeoutSlashBps = 11, 13
	require.Equal(t, chosen, chosen.WithAnchorDefaults())
	repaired, notes := RepairAnchorParams(chosen)
	require.Empty(t, notes)
	require.Equal(t, chosen, repaired)

	// The historical defaults are a valid choice too and are left alone.
	historical := DefaultTaskParams()
	require.Equal(t, historical, historical.WithAnchorDefaults())
}

// The audit floor must be positive in a body that is validated as new (genesis,
// governance update); recorded bodies with a zero floor still hash.
func TestAnchorAuditFloorMustBePositive(t *testing.T) {
	p := DefaultTaskParams()
	p.Verification.AnchorAuditMinRateBps = 0
	require.ErrorContains(t, p.Validate(), "anchor_audit_min_rate_bps must be greater than 0")
	genesis := DefaultGenesis()
	genesis.Params.Verification.AnchorAuditMinRateBps = 0
	require.ErrorContains(t, genesis.Validate(), "anchor_audit_min_rate_bps")
	p.Verification.AnchorAuditMinRateBps = 1
	require.NoError(t, p.Validate())
	p.Verification.AnchorAuditRiskMarginBps = 0
	require.NoError(t, p.Validate(), "a zero margin is the floor rate for every task and stays valid")

	recorded := DefaultTaskParams()
	recorded.Verification.AnchorAuditMinRateBps = 0
	require.NoError(t, ValidateHistoricalTaskParams(recorded))
	_, err := TaskParamsHashV1(taskParamsGoldenChainID, 3, recorded)
	require.NoError(t, err)
}
