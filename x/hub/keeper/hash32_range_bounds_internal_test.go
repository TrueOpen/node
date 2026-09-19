package keeper

import (
	"testing"

	"github.com/stretchr/testify/require"

	shared "github.com/TrueOpen/node/x/shared/types"
)

// Two range bounds used to be spelled as an empty string / empty slice, which the
// old variable-width codecs encoded to zero bytes. Hash32KeyCodec is fixed width
// and rejects a short key, so both are now written as the smallest 32-byte value.
//
// That substitution is only sound while the replacement really is the minimum: a
// bound one bit higher would silently skip the lowest row of every scan, and no
// existing test would notice because the skipped row simply would not appear.
// These live in the internal test package because the helpers are unexported and
// the property being pinned is theirs, not the collections'.
func TestRangeLowBoundsAreTheSmallestHash32(t *testing.T) {
	smallest := make([]byte, shared.Hash32KeySize)
	got := lowestFreezeSignalID()
	require.Len(t, got, shared.Hash32KeySize, "a short bound is rejected by the fixed-width codec")
	require.Equal(t, smallest, []byte(got), "bound must not exclude the lowest possible row")
}
