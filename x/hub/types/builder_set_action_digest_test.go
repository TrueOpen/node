package types

import (
	"bytes"
	"testing"

	"github.com/cosmos/cosmos-sdk/types/bech32"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// digestMemberAddress builds a member whose raw bytes are a fill pattern, so the
// §9.6c ascending order is the fill order and the test can state it directly.
func digestMemberAddress(fill byte) string {
	address, err := bech32.ConvertAndEncode("trueopen", bytes.Repeat([]byte{fill}, 20))
	if err != nil {
		panic(err)
	}
	return address
}

func replaceBuilderSetActionForTest() ReplaceBuilderSetV1 {
	return ReplaceBuilderSetV1{
		ProposalId:             7,
		ExpectedCurrentVersion: 3,
		ExpectedCurrentSetHash: bytes.Repeat([]byte{0x11}, shared.Hash32KeySize),
		NextBuilderSetId:       "builder-set-4",
		Members: []string{
			digestMemberAddress(0x21), digestMemberAddress(0x22), digestMemberAddress(0x23),
		},
		EffectiveHeight: 900,
	}
}

// TestReplaceBuilderSetActionDigestSeparatesAdjacentUint64Fields is the reason
// this preimage is worth a test at all: proposal_id, expected_current_version and
// effective_height are three Uint64BE fields in one frame, so a transposition
// among them produces a perfectly well-formed digest. The pending row stores the
// digest for the whole lead window, which means a swap would not surface as a
// decode failure — it would surface as an exact replay being rejected as a
// conflict, or worse, as two different replacements sharing one digest.
func TestReplaceBuilderSetActionDigestSeparatesAdjacentUint64Fields(t *testing.T) {
	base := replaceBuilderSetActionForTest()
	baseDigest, err := ReplaceBuilderSetActionDigest("trueopen-digest-test", base)
	require.NoError(t, err)

	for name, mutate := range map[string]func(*ReplaceBuilderSetV1){
		"proposal_id":              func(a *ReplaceBuilderSetV1) { a.ProposalId = 900 },
		"expected_current_version": func(a *ReplaceBuilderSetV1) { a.ExpectedCurrentVersion = 7 },
		"effective_height":         func(a *ReplaceBuilderSetV1) { a.EffectiveHeight = 3 },
		"expected_current_set_hash": func(a *ReplaceBuilderSetV1) {
			a.ExpectedCurrentSetHash = bytes.Repeat([]byte{0x12}, shared.Hash32KeySize)
		},
		"next_builder_set_id": func(a *ReplaceBuilderSetV1) { a.NextBuilderSetId = "builder-set-5" },
		"member_dropped":      func(a *ReplaceBuilderSetV1) { a.Members = a.Members[:2] },
	} {
		t.Run(name, func(t *testing.T) {
			mutated := replaceBuilderSetActionForTest()
			mutate(&mutated)
			digest, err := ReplaceBuilderSetActionDigest("trueopen-digest-test", mutated)
			require.NoError(t, err)
			require.NotEqual(t, baseDigest, digest)
		})
	}

	// A digest minted on one chain must not be accepted as another chain's.
	other, err := ReplaceBuilderSetActionDigest("trueopen-digest-other", base)
	require.NoError(t, err)
	require.NotEqual(t, baseDigest, other)
}

// TestReplaceBuilderSetMemberBytesRejectsUnsortedMembers pins the §9.6c ordering
// rule at the digest boundary rather than only in the Keeper: the same member set
// submitted in two orders must not be able to mint two digests, because the
// snapshot the activation installs is sorted either way.
func TestReplaceBuilderSetMemberBytesRejectsUnsortedMembers(t *testing.T) {
	sorted := replaceBuilderSetActionForTest().Members
	decoded, err := ReplaceBuilderSetMemberBytes(sorted)
	require.NoError(t, err)
	require.Len(t, decoded, len(sorted))
	for index := 1; index < len(decoded); index++ {
		require.Negative(t, bytes.Compare(decoded[index-1], decoded[index]))
	}

	swapped := []string{sorted[1], sorted[0], sorted[2]}
	_, err = ReplaceBuilderSetMemberBytes(swapped)
	require.ErrorContains(t, err, "strictly ascending")

	duplicated := []string{sorted[0], sorted[0], sorted[2]}
	_, err = ReplaceBuilderSetMemberBytes(duplicated)
	require.ErrorContains(t, err, "strictly ascending")

	_, err = ReplaceBuilderSetMemberBytes(nil)
	require.ErrorContains(t, err, "must not be empty")
}
