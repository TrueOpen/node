package types

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMMRRootV1PublishedPrimitiveVectors(t *testing.T) {
	domain := "TRUEOPEN_TEST_MMR_V1"
	leaves := [][]byte{[]byte("a"), []byte("bb"), []byte("ccc"), []byte("dddd"), []byte("eeeee"), []byte("ffffff"), []byte("ggggggg")}
	expected := map[int]string{
		0: "3c16dfdfb27ee4ced6c1b13b9a50ed851bb5920197cbc4860e2cebfef120e1cb",
		1: "dbc7d3e5c3605543519d154eb0a305ee58b8139f73430235efea8aa599b6093d",
		2: "9862fe9d7ba78edad8ca4cb4b03a30a2be04c5d6ac8db82f4e4253f2286d302d",
		3: "1ae71241b47a2e87318b726529eacb9d18e3f44d6f4f1426acc3a3e010a4c199",
		7: "fa774b62e96d1dc3b5348e049842a9cadc2145baa9ac68d5a91eb9f36150b381",
	}

	accumulator, err := NewMMRAccumulatorV1(domain)
	require.NoError(t, err)
	root, err := accumulator.Root()
	require.NoError(t, err)
	require.Equal(t, expected[0], hex.EncodeToString(root))
	for index, leaf := range leaves {
		require.NoError(t, accumulator.Append(leaf))
		root, err = accumulator.Root()
		require.NoError(t, err)
		if want, ok := expected[index+1]; ok {
			require.Equal(t, want, hex.EncodeToString(root))
		}
	}
	require.Equal(t, uint64(len(leaves)), accumulator.LeafCount())

	oneShot, err := MMRRootV1(domain, leaves)
	require.NoError(t, err)
	require.Equal(t, expected[7], hex.EncodeToString(oneShot))
}

func TestMMRRootV1PublishedOutputVectors(t *testing.T) {
	const domain = "TRUEOPEN_OUTPUT_MMR_V1"
	chunks := [][]byte{[]byte("Hello"), []byte(", "), []byte("world"), []byte("!")}
	wantPrefixes := []string{
		"43563e7c76e0b5a63993b14b15d8d3db5be6a111e81e89532b426748997596ff",
		"6dfe72f925fdb0a408ad83b1339199d7a5cac40a0d79a0515cf167a858e5fa77",
		"6df2d843848de023d294eb25f4eb7b0b763bd28de0d6b363a5d79e3468d27c5d",
		"17da96c6c109eb9889d40d667f726a0fdbb8a0c85173d2274aef93e93ebb0d45",
	}
	for count, want := range wantPrefixes {
		root, err := MMRRootV1(domain, chunks[:count+1])
		require.NoError(t, err)
		require.Equal(t, want, hex.EncodeToString(root))
	}

	emptyOutput, err := MMRRootV1(domain, [][]byte{{}})
	require.NoError(t, err)
	require.Equal(t, "df63f8049ceef9870c9238e256a1af5bd0011692f442cc6141b3b3a69517a9a6", hex.EncodeToString(emptyOutput))
	emptyTree, err := MMREmptyV1(domain)
	require.NoError(t, err)
	require.NotEqual(t, emptyTree, emptyOutput)
}

func TestMMRRootV1RejectsMalformedInputs(t *testing.T) {
	_, err := MMRRootV1("", nil)
	require.ErrorContains(t, err, "domain")
	_, err = MMRNodeV1("TRUEOPEN_TEST_MMR_V1", make([]byte, 31), make([]byte, 32))
	require.ErrorContains(t, err, "both be 32 bytes")

	first, err := MMRLeafV1("TRUEOPEN_TEST_MMR_V1", 0, []byte("same"))
	require.NoError(t, err)
	second, err := MMRLeafV1("TRUEOPEN_TEST_MMR_V1", 1, []byte("same"))
	require.NoError(t, err)
	require.NotEqual(t, first, second, "the leaf index is part of the preimage")
}
