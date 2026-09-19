package types_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

func TestBatchResultKindClosedSet(t *testing.T) {
	for _, kind := range []string{
		shared.BatchResultKindModelSupport,
		shared.BatchResultKindVerifyCommit,
		shared.BatchResultKindVerifyResult,
	} {
		require.True(t, shared.IsBatchResultKind(kind), kind)
	}
	for _, kind := range []string{"", "verify_commit", "VERIFY_RESULTS", "MODEL-SUPPORT"} {
		require.False(t, shared.IsBatchResultKind(kind), kind)
	}
}

func TestBatchResultDigestGoldenAndMutation(t *testing.T) {
	items := []shared.BatchResultDigestItemV1{
		{ObjectID: make([]byte, 32), Status: shared.BatchMutationAppliedV1},
		{ObjectID: make([]byte, 32), Status: shared.BatchMutationNoopV1},
	}
	items[0].ObjectID[0] = 1
	items[1].ObjectID[0] = 2
	digest, err := shared.BatchResultDigestV1("trueopen-test-1", shared.BatchResultKindVerifyCommit, items)
	require.NoError(t, err)
	require.Equal(t, "7df61533ca20ca3376123b416baea124deef5b4cd867e78d882f8fa8bb6d35ae", fmt.Sprintf("%x", digest))

	reordered := []shared.BatchResultDigestItemV1{items[1], items[0]}
	reorderedDigest, err := shared.BatchResultDigestV1("trueopen-test-1", shared.BatchResultKindVerifyCommit, reordered)
	require.NoError(t, err)
	require.NotEqual(t, digest, reorderedDigest)
	mutated := []shared.BatchResultDigestItemV1{
		{ObjectID: append([]byte(nil), items[0].ObjectID...), Status: items[0].Status},
		items[1],
	}
	mutated[0].ObjectID[31] = 1
	mutatedDigest, err := shared.BatchResultDigestV1("trueopen-test-1", shared.BatchResultKindVerifyCommit, mutated)
	require.NoError(t, err)
	require.NotEqual(t, digest, mutatedDigest)

	_, err = shared.BatchResultDigestV1("trueopen-test-1", "verify_commit", items)
	require.ErrorContains(t, err, "unknown batch result kind")
	items[1].Status = 0
	_, err = shared.BatchResultDigestV1("trueopen-test-1", shared.BatchResultKindVerifyCommit, items)
	require.ErrorContains(t, err, "not APPLIED or NOOP")
}
