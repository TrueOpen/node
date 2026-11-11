package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGenesisAcceptsPreAnchorParams pins audit F-P1-5 (2026-11-09): a genesis
// written before the anchor parameters validates through the values InitGenesis
// will fill, so a new binary can cold-start from it. The windows it fills are
// the recommended ones, not the historical defaults.
func TestGenesisAcceptsPreAnchorParams(t *testing.T) {
	gs := DefaultGenesis()
	v := &gs.Params.Verification
	v.AnchorOpenWindowBlocks, v.AnchorProofWindowBlocks, v.AnchorVerdictWindowBlocks = 0, 0, 0
	v.AnchorPositionsPerTask, v.AnchorMinBondMultiple, v.AnchorContentVerdictWindowBlocks, v.AnchorContentRejectSlashBps = 0, 0, 0, 0
	require.False(t, gs.Params.HasAnchorParams())
	require.Error(t, gs.Params.Validate(), "the raw body still fails: anchor windows must be positive")
	require.NoError(t, gs.Validate(), "genesis validation fills the anchor parameters first")
	filled := gs.Params.WithAnchorDefaults()
	require.True(t, filled.HasAnchorParams())
	require.NoError(t, filled.Validate())
	require.Equal(t, RecommendedAnchorOpenWindowBlocks, filled.Verification.AnchorOpenWindowBlocks)
	require.Equal(t, RecommendedAnchorProofWindowBlocks, filled.Verification.AnchorProofWindowBlocks)
	require.Equal(t, RecommendedAnchorVerdictWindowBlocks, filled.Verification.AnchorVerdictWindowBlocks)
	require.Equal(t, DefaultTaskParams().Verification.AnchorContentRejectSlashBps, filled.Verification.AnchorContentRejectSlashBps)
}

// TestRepairAnchorParams pins the offline preview an operator runs before the
// anchor upgrade height (audit F-P1-4, 2026-11-09).
func TestRepairAnchorParams(t *testing.T) {
	p := DefaultTaskParams()
	p.Verification.AnchorOpenWindowBlocks, p.Verification.AnchorPositionsPerTask = 0, 0
	p.Cleanup.TaskTerminalSummaryRetentionBlocks = 100
	repaired, notes := RepairAnchorParams(p)
	require.Len(t, notes, 1)
	require.Contains(t, notes[0], "task_terminal_summary_retention_blocks")
	require.Equal(t, DefaultTaskTerminalSummaryRetentionBlocks, repaired.Cleanup.TaskTerminalSummaryRetentionBlocks)
	require.True(t, repaired.HasAnchorParams())
	require.NoError(t, repaired.Validate())
	clean, none := RepairAnchorParams(DefaultTaskParams())
	require.Empty(t, none)
	require.NoError(t, clean.Validate())
	// An audit floor of zero is no longer valid; the repair raises it and says so.
	zero := DefaultTaskParams()
	zero.Verification.AnchorAuditMinRateBps = 0
	require.Error(t, zero.Validate())
	raised, floorNotes := RepairAnchorParams(zero)
	require.Len(t, floorNotes, 1)
	require.Contains(t, floorNotes[0], "anchor_audit_min_rate_bps")
	require.Equal(t, DefaultAnchorAuditMinRateBPS, raised.Verification.AnchorAuditMinRateBps)
	require.Equal(t, zero.Verification.AnchorProofWindowBlocks, raised.Verification.AnchorProofWindowBlocks, "chosen windows are kept")
	require.NoError(t, raised.Validate())
}
