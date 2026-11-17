package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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
