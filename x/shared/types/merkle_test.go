package types

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMerkleRootV1EmptyAndOrderedTree(t *testing.T) {
	domain := "TRUEOPEN_TEST_TREE_V1"
	leaves := [][]byte{hashByte(1), hashByte(2), hashByte(3)}

	got, err := MerkleRootV1(domain, leaves)
	require.NoError(t, err)

	domainFrame := testMerkleDomainFrame(domain)
	l0 := testMerkleHash([]byte(merkleLeafPrefix), domainFrame, leaves[0])
	l1 := testMerkleHash([]byte(merkleLeafPrefix), domainFrame, leaves[1])
	l2 := testMerkleHash([]byte(merkleLeafPrefix), domainFrame, leaves[2])
	want := testMerkleHash([]byte(merkleNodePrefix), domainFrame,
		testMerkleHash([]byte(merkleNodePrefix), domainFrame, l0, l1), l2)
	require.Equal(t, want, got)

	empty, err := MerkleRootV1(domain, nil)
	require.NoError(t, err)
	require.Equal(t, testMerkleHash([]byte(merkleEmptyPrefix), domainFrame), empty)
	require.NotEqual(t, make([]byte, sha256.Size), empty)
}

func TestMerkleRootV1PreservesLeafOrder(t *testing.T) {
	forward, err := MerkleRootV1("TRUEOPEN_TEST_TREE_V1", [][]byte{hashByte(1), hashByte(2)})
	require.NoError(t, err)
	reverse, err := MerkleRootV1("TRUEOPEN_TEST_TREE_V1", [][]byte{hashByte(2), hashByte(1)})
	require.NoError(t, err)
	require.False(t, bytes.Equal(forward, reverse))

	single, err := MerkleRootV1("TRUEOPEN_TEST_TREE_V1", [][]byte{hashByte(1)})
	require.NoError(t, err)
	duplicate, err := MerkleRootV1("TRUEOPEN_TEST_TREE_V1", [][]byte{hashByte(1), hashByte(1)})
	require.NoError(t, err)
	require.NotEqual(t, single, duplicate, "MERKLE_ROOT_V1 preserves duplicate leaves instead of deduplicating")
}

func TestMerkleRootV1RejectsInvalidInput(t *testing.T) {
	_, err := MerkleRootV1("", nil)
	require.ErrorContains(t, err, "domain")
	_, err = MerkleRootV1("TRUEOPEN_TEST_TREE_V1", [][]byte{{1}})
	require.ErrorContains(t, err, "must be 32 bytes")
}

// TestMerkleRootV1EnforcesSection6Limits covers the three bounds MERKLE_ROOT_V1
// gained: the §6 domain cap, the §6 repeated-element cap on the leaf vector, and
// the UTF-8 rule PayloadFrameV1 already had. All three are rejection-only -- the
// one production caller passes a DomainRegistryV1 ASCII literal and a leaf vector
// far below the cap -- so the positive cases below are what proves that.
func TestMerkleRootV1EnforcesSection6Limits(t *testing.T) {
	leaves := [][]byte{hashByte(1), hashByte(2)}

	atCap := "TRUEOPEN_" + strings.Repeat("D", MaxCanonicalDomainBytesV1-len("TRUEOPEN_")-len("_V1")) + "_V1"
	require.Len(t, atCap, MaxCanonicalDomainBytesV1)
	root, err := MerkleRootV1(atCap, leaves)
	require.NoError(t, err)
	require.Len(t, root, sha256.Size)

	_, err = MerkleRootV1(atCap+"X", leaves)
	require.ErrorContains(t, err, "merkle domain exceeds 128 bytes")

	// Invalid UTF-8 was accepted here while PayloadFrameV1 rejected it.
	_, err = MerkleRootV1(string([]byte{0xff}), leaves)
	require.ErrorContains(t, err, "UTF-8")

	// The leaf-count cap is checked before the per-level slices are reserved, so
	// the nil leaves below are never dereferenced.
	overCap := make([][]byte, MaxCanonicalRepeatedElementsV1+1)
	_, err = MerkleRootV1("TRUEOPEN_TEST_TREE_V1", overCap)
	require.ErrorContains(t, err, "merkle leaf count 65535 exceeds limit 65534")

	// One below the cap is still a well-formed tree, so the bound rejects and
	// nothing else.
	atLeafCap := make([][]byte, MaxCanonicalRepeatedElementsV1)
	for i := range atLeafCap {
		atLeafCap[i] = hashByte(byte(i))
	}
	root, err = MerkleRootV1("TRUEOPEN_TEST_TREE_V1", atLeafCap)
	require.NoError(t, err)
	require.Len(t, root, sha256.Size)
}

func hashByte(value byte) []byte {
	return bytes.Repeat([]byte{value}, sha256.Size)
}

func testMerkleDomainFrame(domain string) []byte {
	framed := make([]byte, 4+len(domain))
	binary.BigEndian.PutUint32(framed[:4], uint32(len(domain)))
	copy(framed[4:], domain)
	return framed
}

func testMerkleHash(parts ...[]byte) []byte {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write(part)
	}
	return h.Sum(nil)
}
