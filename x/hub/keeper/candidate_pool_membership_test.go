package keeper

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TrueOpen/node/x/hub/types"
)

func TestCandidateBondChangeRequiresMembershipSyncOnlyAtPredicateFlip(t *testing.T) {
	partialSlash := types.ServiceBondState{
		Status: types.ServiceBondStatusActive, ActiveBond: 1, EffectiveActiveBond: 1,
	}
	require.False(t, candidateBondChangeRequiresMembershipSync(partialSlash, 0))

	fullSlash := partialSlash
	fullSlash.ActiveBond = 0
	fullSlash.EffectiveActiveBond = 0
	require.True(t, candidateBondChangeRequiresMembershipSync(fullSlash, 0))

	fullExit := partialSlash
	fullExit.Status = types.ServiceBondStatusUnbonding
	require.True(t, candidateBondChangeRequiresMembershipSync(fullExit, 0))
}
