package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestGenesisAcceptsPreAnchorParams pins audit F-P1-5 (2026-11-09): a genesis
// written before the anchor parameters validates through the defaults InitGenesis
// will fill, so a new binary can cold-start from it.
func TestGenesisAcceptsPreAnchorParams(t *testing.T) {
	gs := DefaultGenesis()
	v := &gs.Params.Verification
	v.AnchorOpenWindowBlocks, v.AnchorProofWindowBlocks, v.AnchorVerdictWindowBlocks = 0, 0, 0
	require.False(t, gs.Params.HasAnchorParams())
	require.Error(t, gs.Params.Validate(), "the raw body still fails: anchor windows must be positive")
	require.NoError(t, gs.Validate(), "genesis validation fills the anchor defaults first")
	filled := gs.Params.WithAnchorDefaults()
	require.True(t, filled.HasAnchorParams())
	require.NoError(t, filled.Validate())
	d := DefaultTaskParams().Verification
	require.Equal(t, d.AnchorProofWindowBlocks, filled.Verification.AnchorProofWindowBlocks)
	require.Equal(t, d.AnchorVerdictWindowBlocks, filled.Verification.AnchorVerdictWindowBlocks)
}

// TestRepairAnchorParams pins the offline preview an operator runs before the
// anchor upgrade height (audit F-P1-4, 2026-11-09).
func TestRepairAnchorParams(t *testing.T) {
	p := DefaultTaskParams()
	p.Verification.AnchorOpenWindowBlocks = 0
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
}
