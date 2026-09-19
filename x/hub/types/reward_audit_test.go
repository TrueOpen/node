package types_test

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	hubtypes "github.com/TrueOpen/node/x/hub/types"
	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestRewardEpochAuditHashGoldenAndMutationSeparation(t *testing.T) {
	primary := shared.CanonicalFrameBytes(shared.Uint64BE(7), []byte{0x01, 0x02})
	state := shared.CanonicalFrameBytes([]byte("ELIGIBLE"), []byte("42"))
	leaf, err := hubtypes.RewardEpochAuditLeafHash(
		"trueopen-local", 7, hubtypes.RewardBucket_REWARD_BUCKET_P2,
		hubtypes.RewardEpochPrunePhaseV1_REWARD_EPOCH_PRUNE_PHASE_V1_COMPETITION,
		primary, state,
	)
	require.NoError(t, err)
	fold, err := hubtypes.RewardEpochAuditFoldHash(
		"trueopen-local", 7, hubtypes.RewardBucket_REWARD_BUCKET_P2,
		hubtypes.RewardEpochAuditInitialRoot(), leaf,
	)
	require.NoError(t, err)
	root, err := hubtypes.RewardEpochAuditRootHash(
		"trueopen-local", 7, hubtypes.RewardBucket_REWARD_BUCKET_P2, fold,
		hubtypes.RewardEpochAuditCounts{EligibleTasks: 1},
	)
	require.NoError(t, err)
	require.Equal(t, "0a637a1cddbb6f9bf507c174a1bfa07c07c52f3573d70c59b8cc0eb58ed5c021", hex.EncodeToString(root))

	mutatedLeaf, err := hubtypes.RewardEpochAuditLeafHash(
		"trueopen-local", 7, hubtypes.RewardBucket_REWARD_BUCKET_P2,
		hubtypes.RewardEpochPrunePhaseV1_REWARD_EPOCH_PRUNE_PHASE_V1_COMPETITION,
		primary, shared.CanonicalFrameBytes([]byte("ELIGIBLE"), []byte("43")),
	)
	require.NoError(t, err)
	require.NotEqual(t, leaf, mutatedLeaf)
}

func TestRewardEpochAuditRejectsInvalidInputsAndCountOverflow(t *testing.T) {
	_, err := hubtypes.RewardEpochAuditFoldHash("trueopen-local", 1, hubtypes.RewardBucket_REWARD_BUCKET_P0, nil, make([]byte, 32))
	require.Error(t, err)
	_, err = hubtypes.RewardEpochAuditRootHash(
		"trueopen-local", 1, hubtypes.RewardBucket_REWARD_BUCKET_P0, make([]byte, 32),
		hubtypes.RewardEpochAuditCounts{EligibleTasks: ^uint64(0), Marks: 1},
	)
	require.ErrorContains(t, err, "overflows")
}

func TestRewardEpochAuditTypedLeafMatchesLegacyBytes(t *testing.T) {
	primary := shared.FlatCanonicalFrameV1([]byte("primary"))
	child := shared.FlatCanonicalFrameV1([]byte("child"))
	state := shared.NewCanonicalFrameBuilderV1().Raw([]byte("state")).Nested(child).Build()
	primaryBytes, err := primary.Bytes()
	require.NoError(t, err)
	stateBytes, err := state.Bytes()
	require.NoError(t, err)

	legacy, err := hubtypes.RewardEpochAuditLeafHash(
		"trueopen-test-1", 7, hubtypes.RewardBucket_REWARD_BUCKET_P1,
		hubtypes.RewardEpochPrunePhaseV1_REWARD_EPOCH_PRUNE_PHASE_V1_COMPETITION,
		primaryBytes, stateBytes,
	)
	require.NoError(t, err)
	typed, err := hubtypes.RewardEpochAuditLeafHashFramesV1(
		"trueopen-test-1", 7, hubtypes.RewardBucket_REWARD_BUCKET_P1,
		hubtypes.RewardEpochPrunePhaseV1_REWARD_EPOCH_PRUNE_PHASE_V1_COMPETITION,
		primary, state,
	)
	require.NoError(t, err)
	require.Equal(t, legacy, typed)
}
