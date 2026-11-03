package types_test

import (
	"testing"

	"github.com/TrueOpen/node/x/hub/types"
	"github.com/stretchr/testify/require"
)

func TestCandidateBuildLeadCoversCapacityAndPublication(t *testing.T) {
	for _, lead := range []uint64{4, 5} {
		params := types.DefaultHubParams()
		params.CandidatePool.CandidateSlotHardCapacity = 4096
		params.CandidatePool.MaxCandidatePoolBuildMembersPerBlock = 1024
		params.CandidatePool.CandidatePoolBuildLeadBlocks = lead
		if lead < 5 {
			require.Error(t, params.Validate())
		} else {
			require.NoError(t, params.Validate())
		}
	}
	params := types.DefaultHubParams()
	params.CandidatePool.MaxCandidatePoolBuildMembersPerBlock = 1
	params.CandidatePool.CandidatePoolBuildLeadBlocks = 10
	require.Error(t, params.Validate(), "governance cannot lower throughput below the build clock")
}
